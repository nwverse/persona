// Package transcript turns Claude Code session JSONL files into normalized proto.Items.
package transcript

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/nwverse/persona/server/internal/compat"
	"hash/crc32"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// Limits keep huge tool outputs and images from flooding the phone. Parsing keeps up to maxTextLen of any text, what
// "View full text" shows; pages and pushes carry a light copy (light): a tool's output cut to pageOutputLen, other text to
// pageTextLen, images without their data.
const (
	maxTextLen    = 1 << 20
	pageTextLen   = 24 * 1024
	pageOutputLen = 4 * 1024
	maxInputLen   = 24 * 1024 // tool inputs (a Write's content…) are not fetched whole: clipped for good
	maxImageLen   = 1536 * 1024
)

type entry struct {
	Type             string          `json:"type"`
	Subtype          string          `json:"subtype"`
	UUID             string          `json:"uuid"`
	ParentUUID       string          `json:"parentUuid"`
	DurationMS       json.RawMessage `json:"durationMs"`
	Timestamp        time.Time       `json:"timestamp"`
	IsSidechain      bool            `json:"isSidechain"`
	IsMeta           bool            `json:"isMeta"`
	IsCompactSummary bool            `json:"isCompactSummary"`
	CompactMetadata  *struct {
		Trigger   string `json:"trigger"`
		PreTokens int    `json:"preTokens"`
	} `json:"compactMetadata"`
	Content json.RawMessage `json:"content"`
	Message *struct {
		Role    string          `json:"role"`
		Model   string          `json:"model"`
		ID      string          `json:"id"`
		Content json.RawMessage `json:"content"`
		Usage   *struct {
			InputTokens              int `json:"input_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
			OutputTokens             int `json:"output_tokens"`
		} `json:"usage"`
	} `json:"message"`
	AITitle        string `json:"aiTitle"`
	CustomTitle    string `json:"customTitle"`
	GitBranch      string `json:"gitBranch"`
	Version        string `json:"version"`
	Entrypoint     string `json:"entrypoint"` // the client Claude Code runs in: "cli", "claude-desktop", "sdk-cli"…
	PermissionMode string `json:"permissionMode"`
	Effort         string `json:"effort"` // assistant lines: the effort level the reply was generated with
	Operation      string `json:"operation"`
	Attachment     *struct {
		Type             string          `json:"type"`
		Prompt           json.RawMessage `json:"prompt"`
		CommandMode      string          `json:"commandMode"`
		ReminderType     string          `json:"reminderType"` // ultra_effort_enter: full or sparse
		claudeGoalStatus                 // type goal_status (goal.go)
		Origin           *struct {
			Kind string `json:"kind"`
			Name string `json:"name"` // kind "peer": the Claude session that sent it
			Body string `json:"body"`
		} `json:"origin"`
	} `json:"attachment"`
	// Origin: where a user line came from; a subagent handing back its report carries the report here (Claude Code
	// 2.1.318+), on a hidden line its task notification then only points at (handbackOf). Raw: a shape not known yet
	// must not lose the line.
	Origin        json.RawMessage `json:"origin"`
	Cwd           string          `json:"cwd"`
	ToolUseResult json.RawMessage `json:"toolUseResult"`
	// system/api_error: {message, formatted} (raw: other lines may carry a plain string here), and which retry this is.
	Error        json.RawMessage `json:"error"`
	RetryAttempt int             `json:"retryAttempt"`
	MaxRetries   int             `json:"maxRetries"`
	// system/scheduled_task_fire: the /loop prompt that is about to run.
	Prompt string `json:"prompt"`
}

type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
	Source    *struct {
		Type      string `json:"type"`
		MediaType string `json:"media_type"`
		Data      string `json:"data"`
	} `json:"source"`
}

// Meta is session-level information collected while parsing.
type Meta struct {
	// Developer info for the chat header (all from fields Claude Code writes on each line).
	GitBranch string
	Version   string
	// Surface is the client the agent runs in: SurfaceDesktop (Claude's or Codex's desktop app), SurfaceCLI (its command
	// line, in a terminal), "" when the transcript does not say (an SDK, an older version). Claude Code writes its
	// entrypoint on every line, a Codex rollout its originator on the first (surface.go).
	Surface string
	// VersionAt is when the line that said Version was written (zero when it had no timestamp).
	VersionAt      time.Time
	PermissionMode string
	// ModeAt / Effort / EffortAt: when the permission mode was last seen, and the effort level of the latest reply.
	ModeAt   time.Time
	Effort   string
	EffortAt time.Time
	// Ultracode is independent of effort: nil means the transcript has no dated evidence. Since, when set by a
	// caller, ignores observations from before the current Claude process (resume resets the session-only switch).
	Ultracode        *bool
	UltracodeAt      time.Time
	UltracodeSince   time.Time
	ultracodeCommand *bool // a command's confirmed value, consumed by its next delayed attachment
	// CodexSandbox: Codex rollouts' sandbox of the latest turn (read-only, workspace-write…); PermissionMode is its approval policy.
	CodexSandbox  string
	ContextTokens int // input + cache read + cache creation of the latest reply: what the model last saw
	// ContextWindow is the model's context window when the transcript says it (a Codex rollout's token_count carries
	// model_context_window); 0 for Claude Code, whose transcripts do not (node/contextwindow.go knows by model id).
	ContextWindow int
	OutputTokens  int // total generated, counted once per message
	Compactions   int
	Turns         int // user prompts
	StartedAt     time.Time
	lastMessageID string
	// Queue holds input typed while Claude works and not yet taken (queue-operation lines:
	// enqueue / dequeue / remove / popAll — popAll is "up arrow pulls them back into the input").
	Queue []string
	// Background holds background shells / agents that have not reported back (see trackBackground).
	Background []proto.BackgroundTask
	bgPending  map[string]proto.BackgroundTask // tool_use_id → started, waiting for the tool result with its id
	// Workflows are the workflow runs the session launched (the Workflow tool), oldest first, at most workflowsKept:
	// what the transcript says (launched, the notification that it ended); the node adds the progress from disk.
	Workflows []proto.Workflow
	// WorkflowScripts: a workflow run's script file (run id → path), where its name and phases are.
	WorkflowScripts map[string]string
	// Loop is the /loop this session runs, if any (see trackBackground).
	Loop        *proto.LoopInfo
	loopPending map[string]proto.LoopInfo
	// Goal is the goal the agent works toward (Claude Code's /goal, a Codex thread goal), nil when none is set (goal.go).
	Goal *proto.GoalInfo
	// Plan is the agent's latest task list, counted (TodoWrite, TaskCreate / TaskUpdate; plan.go), nil when it keeps none.
	Plan        *proto.PlanInfo
	planItems   []planItem
	planUses    map[string]proto.Item // tool_use_id → a plan tool's call, waiting for its result
	planPending [][]byte              // planUses' ids (PlanPending)
	// LastActivity is the time of the last real exchange (user input, assistant output, tool calls). Claude Code
	// also appends recaps, titles and mode lines to idle sessions, so the file mtime is not "last used".
	LastActivity time.Time
	FirstUser    string // first thing the user typed; titles untitled (headless) sessions
	Title        string
	customTitle  bool // Title came from /rename; later ai-titles do not replace it
	// Sidechain keeps isSidechain lines: a subagent's own transcript is all sidechain.
	Sidechain bool
	// Codex: the file is a Codex rollout (codex.go), not a Claude transcript.
	Codex bool
	// TurnOpen: the latest turn has started and not finished: a rollout's task_started without task_complete; a
	// Claude prompt (or a background task waking it) without its turn_duration line or an interrupt after it.
	TurnOpen bool
	// TurnClosed: the lines read so far end with a turn finishing (Codex: task_complete / turn_aborted; Claude:
	// turn_duration / an interrupt), no turn started after it. Unlike !TurnOpen it is false when the lines say nothing
	// (a tail read from mid-turn).
	TurnClosed bool
	// TurnError / TurnErrorKind: the latest turn failed on the model service, as the agent's own record says it
	// (Codex: task_complete's error, #2185); empty when it ended well, or when the record does not say (Claude Code
	// writes its error as the answer: the hub reads that off LastText). Kind is proto.TurnErrorTransient / User, or
	// "" when the record gives no kind to go by.
	TurnError, TurnErrorKind string
	// codexSeen: newer rollouts record an item both as event_msg/item_completed and in history_mutation.
	codexSeen   map[string]bool
	codexTurnID string
	codexUserID string
	// LastTurnWake: the latest turn was started by a background task reporting back, not by a message (no
	// "finished" push for it).
	LastTurnWake bool
	// LastTool: the latest tool call no assistant text or new prompt came after (activity.go), nil when the agent's
	// last word is newer.
	LastTool *ToolCall
	// tree is where the line just parsed sits in the transcript's tree (branch.go); the File reads and clears it.
	tree treeLine
	// localInput: the line being parsed is input the terminal handles itself (a slash command, "! cmd"), no message.
	localInput bool
	// localTurn: the open turn was started by a local command (localInput), with no reply from the agent since; its
	// local output ends it, putting back the turn state from before it (beforeLocal) (#4661).
	localTurn   bool
	beforeLocal [2]bool // TurnOpen, TurnClosed
	// bashInput is the last "! cmd" typed in Claude Code, for the Bash call its output line turns into.
	bashInput string
	// notified holds the background-task notifications already shown: one is often recorded twice (as a user line and
	// as a queued_command attachment). The same task reporting again with other content is a new event and shows.
	notified map[string]bool
	// taskCalls: a background task's id → the call that started it, from its notifications that name both, for a
	// later one that names only the task (noteCall). It grows like notified.
	taskCalls map[string]string
	// handbacks: a subagent's id → the report it handed back (SubagentHandback), until its task notification takes it
	// as its detail. handbackCalls: those calls in the subagent's own transcript, whose results are not shown.
	handbacks     map[string]string
	handbackCalls map[string]bool
	Cwd           string
	Model         string
	LastText      string
	// LastTextAt is when the agent last wrote text, botmux's sentinel included (it ends a turn that answered in the
	// Feishu topic): zero when it never did.
	LastTextAt time.Time
	// LastPromptAt is when the latest message to the agent came in (typed, sent or delivered by a host), zero when
	// none did or it had no time.
	LastPromptAt time.Time
	Updated      time.Time
}

// botmuxSentinel is the line a botmux session's agent ends a turn with when its answer went to the Feishu topic
// through `botmux send`: a word for botmux, not the answer.
const botmuxSentinel = "BOTMUX_NOTHING_TO_SEND"

// Some Anthropic-compatible proxies have a "thinking as text" mode: the thinking block comes back as a text block whose
// lines are each wrapped in ANSI dim italic, opening with "💭 Thinking" and closing with "💭 Thought for Ns (N tokens)",
// so a terminal shows the reasoning inline. That is thinking, not the agent's words.
const (
	thinkingTextOpen = "\x1b[2;3m💭 Thinking\x1b[0m"
	thinkingTextMark = "💭 Thought for "
)

// thinkingAsText is the reasoning in a proxy's thinking-as-text block, without its styling and marker lines; ok is
// false when text is not such a block.
func thinkingAsText(text string) (thinking string, ok bool) {
	rest, ok := strings.CutPrefix(strings.TrimLeft(text, " \t\r\n"), thinkingTextOpen)
	if !ok {
		return "", false
	}
	lines := strings.Split(ansiRe.ReplaceAllString(rest, ""), "\n")
	lines = slices.DeleteFunc(lines, func(l string) bool { return strings.HasPrefix(strings.TrimSpace(l), thinkingTextMark) })
	return strings.TrimSpace(strings.Join(lines, "\n")), true
}

// withoutBotmuxSentinel is text without its botmux sentinel lines, "" when that is all it says (LastText then stays the
// turn's earlier words).
func withoutBotmuxSentinel(text string) string {
	if !strings.Contains(text, botmuxSentinel) {
		return text
	}
	lines := strings.Split(text, "\n")
	lines = slices.DeleteFunc(lines, func(l string) bool { return strings.TrimSpace(l) == botmuxSentinel })
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// pastedRe matches the wrapper Claude Code puts around long pasted input (also what herdr's bracketed paste from
// the phone, e.g. a WeChat voice-input transcript, turns into): <pasted_content id="ae9b">…</pasted_content id="ae9b">.
var pastedRe = lazyre.New(`<[\\/]*pasted_content(?:\s+id="[^"]*")?\s*>`) // also <\pasted…> / <\/pasted…> from nested pastes

// ContextRe matches the live snapshot Persona starts each message to its assistant with (node/assistant_context.go):
// context for the model, not something the user wrote. It is <persona-context>; history and older nodes have
// <persona-context> (#3182).
var ContextRe = lazyre.New(`(?s)<` + compat.ContextPattern() + `>.*?</` + compat.ContextPattern() + `>\s*`)

var tagRe = lazyre.New(`(?s)<(command-name|command-message|command-args|local-command-stdout|local-command-stderr|task-notification|system-reminder)>(.*?)</(command-name|command-message|command-args|local-command-stdout|local-command-stderr|task-notification|system-reminder)>`)

// APIRetrying ends an API error notice Claude Code is still retrying: the turn goes on.
const APIRetrying = "（正在重试）"

// InterruptedNotice is the notice a turn stopped by the user becomes (Claude Code and Codex); APIErrorPrefix starts
// the notice of a turn the model service failed. The channels read them as why a turn ended without words
// (channel.Stopped).
const (
	InterruptedNotice = "已中断"
	APIErrorPrefix    = "API 错误："
	// CompactedNotice marks where the context was compacted (Claude Code's compact_boundary, Codex's
	// ContextCompaction); CompactSummaryNotice stands for the summary a compacted conversation goes on from.
	CompactedNotice      = "上下文已压缩"
	CompactSummaryNotice = "已从压缩的上下文继续"
)

// CompactNotice is the notice a /compact without instructions becomes (no user line): a /compact sent from the phone
// is tied to it (node/echo.go).
const CompactNotice = "压缩上下文"

// ParseLine converts one JSONL line (1-based seq) into items, updating meta.
func ParseLine(seq int, line []byte, meta *Meta) []proto.Item {
	var items []proto.Item
	if meta.Codex || isCodexLine(line) {
		meta.Codex = true
		items = parseCodexLine(seq, line, meta)
	} else {
		items = parseLine(seq, line, meta)
	}
	if len(items) > 0 && items[0].Role == "user" && items[0].Kind == proto.KindText {
		meta.Turns++
	}
	if len(meta.FirstUser) > pageTextLen { // it titles the session in every list
		meta.FirstUser = clipTo(meta.FirstUser, pageTextLen)
	}
	defer func() { meta.localInput = false }()
	for _, it := range items {
		if it.Role != "system" && !it.TS.IsZero() && it.TS.After(meta.LastActivity) {
			meta.LastActivity = it.TS
		}
		meta.trackActivity(it)
		meta.trackPlan(it)
		switch {
		case it.Wake:
			meta.LastTurnWake = true
		case it.Role == "user" && it.Kind == proto.KindText && !it.Queued:
			meta.LastTurnWake = false
			// A message to the agent: not a local command, a `! cmd` or another agent's report (localInput).
			if it.From == "" && !meta.localInput && it.TS.After(meta.LastPromptAt) {
				meta.LastPromptAt = it.TS
			}
		}
		if !meta.Codex && (it.Wake || it.Role == "user" && it.Kind == proto.KindText && !it.Queued) {
			if local := meta.localInput && !it.Wake; local && !meta.localTurn {
				meta.localTurn, meta.beforeLocal = true, [2]bool{meta.TurnOpen, meta.TurnClosed}
			} else if !local {
				meta.localTurn = false
			}
			meta.TurnOpen, meta.TurnClosed = true, false // a Claude turn starts; parseLine closes it
		}
	}
	return items
}

func parseLine(seq int, line []byte, meta *Meta) []proto.Item {
	var e entry
	if err := json.Unmarshal(line, &e); err != nil {
		return nil
	}
	meta.tree = treeLine{uuid: e.UUID, parent: e.ParentUUID, sidechain: e.IsSidechain, assistant: e.Type == "assistant",
		system: e.Type == "system", turnEnd: e.Type == "system" && e.Subtype == "turn_duration"}
	if e.Cwd != "" {
		meta.Cwd = e.Cwd
	}
	if !e.Timestamp.IsZero() {
		meta.Updated = e.Timestamp
	}
	if e.GitBranch != "" {
		meta.GitBranch = e.GitBranch
	}
	if e.Version != "" {
		meta.Version, meta.VersionAt = e.Version, e.Timestamp
	}
	if e.Entrypoint != "" {
		meta.Surface = ClaudeSurface(e.Entrypoint)
	}
	if meta.StartedAt.IsZero() && !e.Timestamp.IsZero() {
		meta.StartedAt = e.Timestamp
	}
	// The mode is written on every prompt (user lines) and as its own line when it changes; the effort on replies.
	if e.PermissionMode != "" && !e.IsSidechain && (e.Type == "permission-mode" || e.Type == "user") {
		meta.PermissionMode, meta.ModeAt = e.PermissionMode, meta.Updated
	}
	if e.Type == "assistant" && !e.IsSidechain {
		meta.localTurn = false // the agent answers: a skill or a custom command expanded into a prompt is a turn
	}
	if e.Effort != "" && e.Type == "assistant" && !e.IsSidechain {
		meta.Effort, meta.EffortAt = e.Effort, meta.Updated
	}
	switch {
	case e.Type == "system" && e.Subtype == "compact_boundary":
		meta.Compactions++
	case e.Type == "assistant" && e.Message != nil && e.Message.Usage != nil && !e.IsSidechain:
		u := e.Message.Usage
		// A gateway's sum of a whole turn is not a context, nor the zeros it reports on the turn's other replies
		// (usage.go): the last real one stands.
		if tokens := u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens; tokens > 0 && PlausibleContext(tokens, 0) {
			meta.ContextTokens = tokens
		}
		if e.Message.ID != meta.lastMessageID { // every content block repeats the message's usage
			meta.OutputTokens += u.OutputTokens
			meta.lastMessageID = e.Message.ID
		}
	}
	if e.Type == "queue-operation" {
		content := rawString(e.Content)
		if strings.Contains(content, "<task-notification>") {
			// A background task reporting back is queued like input, but it is not something the user typed.
			meta.taskFinished(content, e.Timestamp)
			return nil
		}
		meta.applyQueue(e.Operation, content)
		return nil
	}
	if e.Type == "custom-title" && e.CustomTitle != "" { // /rename: the user's name beats Claude's generated one
		meta.Title, meta.customTitle = e.CustomTitle, true
		return nil
	}
	if e.Type == "ai-title" && e.AITitle != "" {
		if !meta.customTitle {
			meta.Title = e.AITitle
		}
		return nil
	}
	if e.IsSidechain && !meta.Sidechain {
		return nil
	}
	mk := func(idx int, role, kind string) proto.Item {
		id := e.UUID
		if id == "" {
			id = fmt.Sprintf("line%d", seq)
		}
		return proto.Item{ID: fmt.Sprintf("%s#%d", id, idx), Seq: seq, TS: e.Timestamp, Role: role, Kind: kind}
	}
	switch e.Type {
	case "attachment":
		// Input typed while Claude was working is queued, then absorbed into the running turn; it is stored as a
		// queued_command attachment instead of a user message, so without this it never shows up in the chat.
		a := e.Attachment
		if a != nil && !e.IsSidechain && (a.Type == "ultra_effort_enter" || a.Type == "ultra_effort_exit") {
			return meta.claudeUltracodeAttachment(a.Type, a.ReminderType, e.Timestamp, func() proto.Item { return mk(0, "system", proto.KindNotice) })
		}
		if a != nil && a.Type == "goal_status" && !e.IsSidechain {
			return meta.claudeGoal(a.claudeGoalStatus, meta.Updated, func() proto.Item { return mk(0, "system", proto.KindNotice) })
		}
		if a == nil || a.Type != "queued_command" {
			return nil
		}
		notification := a.CommandMode == "task-notification"
		// A subagent handing its report back while the session works (Claude Code 2.1.319): a queued command with a
		// peer origin that says handback, written by Claude Code itself. Like the hidden user line of one that came
		// while idle, it is no message: its report goes to the subagent's notification, which follows (reportFor).
		if a.Origin != nil && a.Origin.Kind == "peer" {
			if agent, report, ok := handbackOf(attachmentOrigin(line)); ok {
				meta.handedBack(agent, report)
				return nil
			}
		}
		if a.Origin != nil && a.Origin.Kind == "peer" { // another Claude session's message, absorbed mid-turn
			var prompt string
			_ = json.Unmarshal(a.Prompt, &prompt)
			it, ok := peerItem(mk, prompt, a.Origin.Name, a.Origin.Body)
			if !ok {
				return nil
			}
			it.Queued = true
			return []proto.Item{it}
		}
		if !notification && ((a.CommandMode != "" && a.CommandMode != "prompt") || (a.Origin != nil && a.Origin.Kind != "human")) {
			return nil
		}
		var text string
		var images []proto.Item // a message with pictures, sent while the agent worked: its prompt is blocks
		if json.Unmarshal(a.Prompt, &text) != nil {
			var blocks []block
			if json.Unmarshal(a.Prompt, &blocks) == nil {
				var parts []string
				for i, b := range blocks {
					switch b.Type {
					case "text":
						parts = append(parts, b.Text)
					case "image":
						if img := imageOf(b); img != nil {
							it := mk(i, "user", proto.KindImage)
							it.Image, it.Queued = img, true
							images = append(images, it)
						}
					}
				}
				text = strings.Join(parts, "\n")
			}
		}
		if strings.Contains(text, "<task-notification>") {
			meta.taskFinished(text, e.Timestamp)
			return userText(mk, text, meta) // a "background task finished" notice
		}
		if notification {
			return nil // other harness notifications are not user input
		}
		items := userText(mk, text, meta)
		for i := range items {
			items[i].Queued = true
		}
		if meta.FirstUser == "" && len(items) > 0 && items[0].Kind == proto.KindText {
			meta.FirstUser = items[0].Text
		}
		return append(items, images...) // the pictures dropped here once: the phone showed the message without them
	case "system":
		var text string
		switch e.Subtype {
		case "turn_duration":
			meta.TurnOpen, meta.TurnClosed = false, true
			ms, ok := recordedMilliseconds(e.DurationMS)
			if !ok || strings.TrimSpace(e.ParentUUID) == "" {
				return nil
			}
			it := mk(0, "system", "turn_duration")
			it.TurnDuration = &proto.TurnDuration{DurationMS: ms, Source: "claude_turn_duration", ParentUUID: e.ParentUUID}
			return []proto.Item{it}
		case "compact_boundary":
			text = CompactedNotice
		case "api_error":
			// Claude Code writes one line per retry (retryAttempt 1..maxRetries): show the first and, if it never
			// got through, the last.
			if e.RetryAttempt > 1 && (e.MaxRetries == 0 || e.RetryAttempt < e.MaxRetries) {
				return nil
			}
			text = APIErrorPrefix + apiErrorText(e)
			switch {
			case e.MaxRetries > 0 && e.RetryAttempt >= e.MaxRetries:
				text += fmt.Sprintf("（重试 %d 次后放弃）", e.RetryAttempt)
			case e.RetryAttempt == 1:
				text += APIRetrying
			}
		case "local_command":
			// Newer Claude Code writes slash commands and their output as system lines (older ones as user lines):
			// "<command-name>/model</command-name>…" is what the user typed, "<local-command-stdout>…" the answer.
			return userText(mk, rawString(e.Content), meta)
		case "scheduled_task_fire":
			// A /loop or cron job firing: the turn that follows was started by the schedule, not by a message.
			text = "/loop 到点，开始新一轮"
			if p := snippetLine(e.Prompt, 80); p != "" {
				text += "：" + p
			}
			it := mk(0, "system", proto.KindNotice)
			it.Text, it.Wake = text, true
			return []proto.Item{it}
		case "away_summary":
			recap := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(rawString(e.Content)), "(disable recaps in /config)"))
			if recap == "" {
				return nil
			}
			it := mk(0, "system", proto.KindRecap)
			it.Text = clip(recap)
			return []proto.Item{it}
		case "informational":
			text = rawString(e.Content)
		default:
			return nil
		}
		if strings.TrimSpace(text) == "" {
			return nil
		}
		it := mk(0, "system", proto.KindNotice)
		it.Text = clip(text)
		if e.Subtype == "compact_boundary" {
			it.Compact = &proto.Compact{}
			if m := e.CompactMetadata; m != nil {
				it.Compact.Trigger, it.Compact.PreTokens = m.Trigger, m.PreTokens
			}
		}
		return []proto.Item{it}
	case "user":
		if e.Message == nil {
			return nil
		}
		if agent, report, ok := handbackOf(e.Origin); ok {
			meta.handedBack(agent, report)
		}
		if e.IsCompactSummary {
			it := mk(0, "system", proto.KindNotice)
			it.Text = CompactSummaryNotice
			return []proto.Item{it}
		}
		if e.IsMeta {
			if it, ok := peerMessage(mk, e.Origin, e.Message.Content); ok {
				return []proto.Item{it}
			}
		}
		var s string
		if json.Unmarshal(e.Message.Content, &s) == nil {
			if strings.Contains(s, "<task-notification>") {
				meta.taskFinished(s, e.Timestamp)
			}
			if e.IsMeta {
				return nil
			}
			items := userText(mk, s, meta)
			if strings.Contains(s, "<task-notification>") {
				markWake(items)
			}
			if meta.FirstUser == "" && len(items) > 0 && items[0].Kind == proto.KindText {
				meta.FirstUser = items[0].Text
			}
			return items
		}
		var blocks []block
		if json.Unmarshal(e.Message.Content, &blocks) != nil {
			return nil
		}
		var out []proto.Item
		for i, b := range blocks {
			switch b.Type {
			case "text":
				if strings.Contains(b.Text, "<task-notification>") {
					meta.taskFinished(b.Text, e.Timestamp)
				}
				if e.IsMeta {
					continue
				}
				items := userText(func(_ int, role, kind string) proto.Item { return mk(i, role, kind) }, b.Text, meta)
				if strings.Contains(b.Text, "<task-notification>") {
					markWake(items)
				}
				if meta.FirstUser == "" && len(items) > 0 && items[0].Kind == proto.KindText {
					meta.FirstUser = items[0].Text
				}
				out = append(out, items...)
			case "image":
				if e.IsMeta {
					continue
				}
				if img := imageOf(b); img != nil {
					it := mk(i, "user", proto.KindImage)
					it.Image = img
					out = append(out, it)
				}
			case "tool_result":
				if meta.handbackCalls[b.ToolUseID] { // "Report delivered to your caller.": its report is the words above
					delete(meta.handbackCalls, b.ToolUseID)
					continue
				}
				text, imgs := toolResultContent(b.Content)
				meta.backgroundStarted(b.ToolUseID, text, b.IsError, e.Timestamp)
				it := mk(i, "user", proto.KindToolResult)
				it.ToolUseID = b.ToolUseID
				it.IsError = b.IsError
				it.Text = clip(text)
				out = append(out, it)
				for j, img := range imgs {
					im := mk(i*100+j+1, "user", proto.KindImage)
					im.ToolUseID = b.ToolUseID
					im.Image = img
					out = append(out, im)
				}
			}
		}
		return out
	case "assistant":
		if e.Message == nil {
			return nil
		}
		if e.Message.Model != "" && e.Message.Model != "<synthetic>" {
			meta.Model = e.Message.Model
		}
		var blocks []block
		if json.Unmarshal(e.Message.Content, &blocks) != nil {
			return nil
		}
		var out []proto.Item
		for i, b := range blocks {
			switch b.Type {
			case "text":
				if strings.TrimSpace(b.Text) == "" {
					continue
				}
				if t, ok := thinkingAsText(b.Text); ok {
					if t != "" {
						it := mk(i, "assistant", proto.KindThinking)
						it.Text = clip(t)
						out = append(out, it)
					}
					continue
				}
				it := mk(i, "assistant", proto.KindText)
				it.Text = clip(b.Text)
				meta.said(withoutBotmuxSentinel(b.Text), e.Timestamp)
				out = append(out, it)
			case "thinking":
				if strings.TrimSpace(b.Thinking) == "" {
					continue
				}
				it := mk(i, "assistant", proto.KindThinking)
				it.Text = clip(b.Thinking)
				out = append(out, it)
			case "tool_use", "server_tool_use":
				if b.Name == handbackTool {
					// a subagent handing its report back to the session that launched it: its last words, not a step
					if report := meta.handbackCall(b.ID, b.Input); report != "" {
						it := mk(i, "assistant", proto.KindText)
						it.Text = clip(report)
						meta.said(report, e.Timestamp)
						out = append(out, it)
					}
					continue
				}
				it := mk(i, "assistant", proto.KindToolUse)
				it.ToolUseID = b.ID
				it.ToolName = b.Name
				it.ToolInput = clipJSON(b.Input)
				out = append(out, it)
				meta.trackBackground(b.ID, b.Name, b.Input, e.Timestamp)
			}
		}
		return out
	}
	return nil
}

// Background tasks: Bash with run_in_background and subagents launched async answer with a task id right away
// ("Command running in background with ID: b85polnoj. Output is being written to: …" / "Async agent launched …
// agentId: …") and report back later with a <task-notification> carrying the same id. TaskStop / KillShell end them too.
// A workflow answers "Workflow launched in background. Task ID: w5k2nd1ap\nSummary: …\nTranscript dir: …\nScript file:
// …\nRun ID: wf_…"; a Monitor "Monitor started (task bqke1prjm, expires in 30m unless …)".
var (
	// agentId needs its colon: newer results first warn about "the agentId below", whose next word is not the id.
	bgIDRe     = lazyre.New(`(?:running in background with ID:?|agentId:|in background\. Task ID:?|Monitor started \(task:?)\s*([A-Za-z0-9_-]+)`)
	wfRunRe    = lazyre.New(`(?m)^Run ID:\s*(wf_[A-Za-z0-9_-]+)`)
	wfSumRe    = lazyre.New(`(?m)^Summary:\s*(.+)$`)
	wfScriptRe = lazyre.New(`(?m)^Script file:\s*(\S+\.js)\s*$`)
	wfDirRe    = lazyre.New(`(?m)^Transcript dir:\s*\S*/(wf_[A-Za-z0-9_-]+)\s*$`)
	wfNameRe   = lazyre.New("\\bname:\\s*['\"`]([^'\"`\\n]+)['\"`]")
	expiresRe  = lazyre.New(`expires in (\d+)(ms|s|m|h)\b`)
	wakeInRe   = lazyre.New(`Next wakeup scheduled for [^(]*\(in (\d+)s\)`)
	bgMovedRe  = lazyre.New(`moved to the background \(ID: ([A-Za-z0-9_-]+)\)`) // a foreground command that timed out
	bgOutputRe = lazyre.New(`Output is being written to:\s*(\S+?)\.?(?:\s|$)`)
)

func (m *Meta) trackBackground(toolUseID, name string, raw json.RawMessage, ts time.Time) {
	var in struct {
		Command     string `json:"command"`
		Description string `json:"description"`
		Background  bool   `json:"run_in_background"`
		TaskID      string `json:"task_id"`
		ShellID     string `json:"shell_id"`
		// ScheduleWakeup / CronCreate (the /loop skill)
		Delay     float64 `json:"delaySeconds"`
		Prompt    string  `json:"prompt"`
		Reason    string  `json:"reason"`
		Stop      bool    `json:"stop"`
		Cron      string  `json:"cron"`
		Recurring *bool   `json:"recurring"`
		// Workflow
		WorkflowName string `json:"name"`
		Script       string `json:"script"`
		ScriptPath   string `json:"scriptPath"`
	}
	_ = json.Unmarshal(raw, &in)
	switch name {
	case "ScheduleWakeup":
		if in.Stop {
			m.Loop = nil
			return
		}
		if m.loopPending == nil {
			m.loopPending = map[string]proto.LoopInfo{}
		}
		m.loopPending[toolUseID] = proto.LoopInfo{Kind: "wakeup", Prompt: in.Prompt, Reason: in.Reason,
			IntervalSeconds: int(in.Delay), NextAt: ts.Add(time.Duration(in.Delay) * time.Second)}
	case "CronCreate":
		if (in.Recurring != nil && !*in.Recurring) || onceCron(in.Cron) {
			return // a one-off reminder, not a loop
		}
		m.Loop = &proto.LoopInfo{Kind: "cron", Prompt: in.Prompt, Cron: in.Cron}
	case "CronDelete":
		if m.Loop != nil && m.Loop.Kind == "cron" {
			m.Loop = nil
		}
	case "Bash":
		if !in.Background {
			return
		}
		m.pend(toolUseID, proto.BackgroundTask{Kind: "shell", Command: in.Command, Description: in.Description, StartedAt: ts})
	case "Agent", "Task":
		// Whether it went to the background shows in the result ("Async agent launched").
		m.pend(toolUseID, proto.BackgroundTask{Kind: "agent", Description: in.Description, StartedAt: ts})
	case "Monitor":
		m.pend(toolUseID, proto.BackgroundTask{Kind: "monitor", Command: in.Command, Description: in.Description, StartedAt: ts})
	case "Workflow":
		name := in.WorkflowName
		if mm := wfNameRe.FindStringSubmatch(in.Script); name == "" && mm != nil {
			name = mm[1]
		}
		if name == "" {
			name = scriptName(in.ScriptPath)
		}
		m.pend(toolUseID, proto.BackgroundTask{Kind: "workflow", Description: name, StartedAt: ts})
	case "TaskStop", "KillShell", "KillBash":
		m.removeBackground(in.TaskID + in.ShellID)
		m.workflowEnded(in.TaskID+in.ShellID, proto.WorkflowKilled, ts)
	}
}

// onceCron reports a cron expression pinned to one date ("33 17 21 10 *": day of month and month both fixed).
func onceCron(expr string) bool {
	f := strings.Fields(expr)
	if len(f) < 5 {
		return false
	}
	_, errDay := strconv.Atoi(f[2])
	_, errMonth := strconv.Atoi(f[3])
	return errDay == nil && errMonth == nil
}

func (m *Meta) pend(toolUseID string, t proto.BackgroundTask) {
	if m.bgPending == nil {
		m.bgPending = map[string]proto.BackgroundTask{}
	}
	m.bgPending[toolUseID] = t
}

func (m *Meta) backgroundStarted(toolUseID, result string, isError bool, ts time.Time) {
	if l, ok := m.loopPending[toolUseID]; ok {
		delete(m.loopPending, toolUseID)
		if !isError {
			if mm := wakeInRe.FindStringSubmatch(result); mm != nil { // "Next wakeup scheduled for 11:57:00 (in 1859s)"
				if n, err := strconv.Atoi(mm[1]); err == nil {
					l.NextAt = ts.Add(time.Duration(n) * time.Second)
				}
			}
			if m.Loop != nil && m.Loop.Kind == "wakeup" {
				l.Ticks = m.Loop.Ticks
			}
			l.Ticks++
			m.Loop = &l
		}
		return
	}
	t, ok := m.bgPending[toolUseID]
	if !ok {
		if mv := bgMovedRe.FindStringSubmatch(result); mv != nil && !isError {
			m.removeBackground(mv[1])
			m.Background = append(m.Background, proto.BackgroundTask{ID: mv[1], Kind: "shell", Description: "超时转到后台的命令",
				OutputFile: outputFile(result), StartedAt: ts})
		}
		return
	}
	delete(m.bgPending, toolUseID)
	match := bgIDRe.FindStringSubmatch(result)
	if isError || match == nil {
		return // ran in the foreground after all, or failed to start
	}
	t.ID = match[1]
	t.OutputFile = outputFile(result)
	if t.StartedAt.IsZero() {
		t.StartedAt = ts
	}
	switch t.Kind {
	case "monitor":
		if e := expiresRe.FindStringSubmatch(result); e != nil {
			n, _ := strconv.Atoi(e[1])
			unit := map[string]time.Duration{"ms": time.Millisecond, "s": time.Second, "m": time.Minute, "h": time.Hour}[e[2]]
			at := ts.Add(time.Duration(n) * unit)
			t.ExpiresAt = &at
		}
	case "workflow":
		m.workflowStarted(&t, toolUseID, result)
	}
	m.removeBackground(t.ID)
	m.Background = append(m.Background, t)
}

// workflowsKept: how many workflow runs a session's Meta remembers.
const workflowsKept = 8

func (m *Meta) workflowStarted(t *proto.BackgroundTask, toolUseID, result string) {
	w := proto.Workflow{TaskID: t.ID, Name: t.Description, Status: proto.WorkflowRunning, Running: true, StartedAt: t.StartedAt,
		ToolUseID: toolUseID}
	if r := wfRunRe.FindStringSubmatch(result); r != nil {
		w.RunID = r[1]
	} else if r := wfDirRe.FindStringSubmatch(result); r != nil {
		w.RunID = r[1]
	}
	if r := wfSumRe.FindStringSubmatch(result); r != nil {
		w.Summary = strings.TrimSpace(r[1])
	}
	if r := wfScriptRe.FindStringSubmatch(result); r != nil && w.RunID != "" {
		if m.WorkflowScripts == nil {
			m.WorkflowScripts = map[string]string{}
		}
		m.WorkflowScripts[w.RunID] = r[1]
		if w.Name == "" {
			w.Name = scriptName(r[1])
		}
	}
	t.RunID = w.RunID
	if t.Description == "" {
		t.Description = cmpOr(w.Name, w.Summary)
	}
	for i, o := range m.Workflows {
		if o.TaskID == w.TaskID {
			m.Workflows = append(m.Workflows[:i:i], m.Workflows[i+1:]...)
			break
		}
	}
	m.Workflows = append(m.Workflows, w)
	if n := len(m.Workflows) - workflowsKept; n > 0 {
		for _, o := range m.Workflows[:n] {
			delete(m.WorkflowScripts, o.RunID)
		}
		m.Workflows = append([]proto.Workflow(nil), m.Workflows[n:]...)
	}
}

// workflowEnded records that the workflow with task id ended with status (its notification, a TaskStop).
func (m *Meta) workflowEnded(id, status string, ts time.Time) {
	for i := range m.Workflows {
		w := &m.Workflows[i]
		if w.TaskID != id || !w.Running {
			continue
		}
		switch status {
		case proto.WorkflowFailed, proto.WorkflowKilled:
		case "stopped":
			status = proto.WorkflowKilled
		default:
			status = proto.WorkflowCompleted
		}
		w.Status, w.Running, w.EndedAt = status, false, &ts
	}
}

// scriptName is a workflow's name from its script file: "<name>-wf_<run>.js" (a saved one: "<name>.js").
func scriptName(path string) string {
	base := strings.TrimSuffix(path[strings.LastIndexAny(path, "/\\")+1:], ".js")
	if i := strings.Index(base, "-wf_"); i > 0 {
		base = base[:i]
	}
	return base
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func outputFile(result string) string {
	if o := bgOutputRe.FindStringSubmatch(result); o != nil {
		return o[1]
	}
	return ""
}

// said notes the agent writing at ts: LastText (when t has words) and what its branch said.
func (m *Meta) said(t string, ts time.Time) {
	if t != "" {
		m.LastText = t
		m.tree.said = t
	}
	m.LastTextAt = ts
	m.tree.saidAt, m.tree.spoke = ts, true
}

func (m *Meta) taskFinished(notification string, ts time.Time) {
	if id := firstTag(notification, "task-id"); id != "" {
		m.removeBackground(id)
		m.workflowEnded(id, firstTag(notification, "status"), ts)
	}
}

func (m *Meta) removeBackground(id string) {
	if id == "" {
		return
	}
	for i, t := range m.Background {
		if t.ID == id {
			m.Background = append(m.Background[:i:i], m.Background[i+1:]...)
			return
		}
	}
}

func (m *Meta) applyQueue(op, content string) {
	// Queued text keeps the <pasted_content> wrapper of long / voice input; show what the user wrote.
	content = strings.TrimSpace(pastedRe.ReplaceAllString(content, ""))
	if from, body, ok := CrossSession(content); ok {
		content = "【来自 " + from + "】" + body
	}
	removeOne := func() bool {
		for i, q := range m.Queue {
			if q == content {
				m.Queue = append(m.Queue[:i:i], m.Queue[i+1:]...)
				return true
			}
		}
		return false
	}
	switch op {
	case "enqueue":
		if content != "" {
			m.Queue = append(m.Queue, content)
		}
	case "dequeue":
		if len(m.Queue) > 0 {
			m.Queue = m.Queue[1:]
		}
	case "remove", "popAll":
		if content == "" || !removeOne() {
			if op == "popAll" {
				m.Queue = nil
			} else if len(m.Queue) > 0 {
				m.Queue = m.Queue[1:]
			}
		}
	}
}

// A message another Claude session sent this one ("Another Claude session sent a message:" + the tagged body + an
// English note on how to treat it).
// caveatRe drops Claude Code's note that the lines around it came from a local command (context for the model).
var caveatRe = lazyre.New(`(?s)<local-command-caveat>.*?</local-command-caveat>`)

var teammateRe = lazyre.New(`(?s)<teammate-message([^>]*)>\s*(.*?)\s*</teammate-message>`)
var teammateIDRe = lazyre.New(`teammate_id="([^"]*)"`)

var crossRe = lazyre.New(`(?s)<cross-session-message([^>]*)>\s*(.*?)\s*</cross-session-message>`)
var crossNameRe = lazyre.New(`from-name="([^"]*)"`)

// peerMessage is another Claude session's message that came while this one was idle: Claude Code (2.1.299 on) writes
// it as a hidden (isMeta) user line with the sender in its origin, and it starts a turn, so it reads as the message it
// is, the sender's name on it, and opens the turn like a typed one (ParseLine). One absorbed mid-turn is a
// queued_command attachment instead (parseLine). A subagent handing back its report has a peer origin too: its report
// comes with its task notification (handbackOf). ok false for anything else.
func peerMessage(mk func(int, string, string) proto.Item, origin, content json.RawMessage) (proto.Item, bool) {
	var o struct {
		Kind     string `json:"kind"`
		Name     string `json:"name"`
		Body     string `json:"body"`
		Handback bool   `json:"handback"`
	}
	if len(origin) == 0 || json.Unmarshal(origin, &o) != nil || o.Kind != "peer" || o.Handback {
		return proto.Item{}, false
	}
	var text string
	if json.Unmarshal(content, &text) != nil {
		var blocks []block
		_ = json.Unmarshal(content, &blocks)
		var parts []string
		for _, b := range blocks {
			if b.Type == "text" {
				parts = append(parts, b.Text)
			}
		}
		text = strings.Join(parts, "\n")
	}
	return peerItem(mk, text, o.Name, o.Body)
}

// attachmentOrigin is an attachment line's origin as Claude Code wrote it (only queued commands with a peer origin
// read it, so not every line is decoded twice).
func attachmentOrigin(line []byte) json.RawMessage {
	var l struct {
		Attachment struct {
			Origin json.RawMessage `json:"origin"`
		} `json:"attachment"`
	}
	if json.Unmarshal(line, &l) != nil {
		return nil
	}
	return l.Attachment.Origin
}

// FromSession is who a message another Claude session sent is from when it does not say (the apps and the hub put it
// in the reader's language: hub/transcript_text.go, assistant/fleet_text.go).
const FromSession = "另一个会话"

// peerItem is a message another Claude session sent, as the chat shows it: the body the origin carries (the tagged one
// in text when it does not), the sender's name on it. ok false when there is no body.
func peerItem(mk func(int, string, string) proto.Item, text, name, body string) (proto.Item, bool) {
	from, tagged, ok := CrossSession(text)
	if b := strings.TrimSpace(body); b != "" || !ok {
		tagged = b
	}
	if name != "" {
		from = name
	} else if !ok {
		from = FromSession
	}
	// The user's own message from the phone, through the session's peer inbox (node/claudepeer.go): its words, without
	// what Persona told the model about where they came from.
	if from == PersonaPeer {
		if words, ok := prompts.PeerMessageBody(tagged); ok {
			tagged = strings.TrimSpace(words)
		}
	}
	if tagged == "" {
		return proto.Item{}, false
	}
	it := mk(0, "user", proto.KindText)
	it.Text, it.From = clip(tagged), from
	return it, true
}

// PersonaPeer is the sender name node signs the messages it sends through a claude's peer inbox with (the user's
// own words from the phone, node/claudepeer.go peerSender).
const PersonaPeer = "Persona"

// CrossSession returns the sender and body of a cross-session message, ok false for anything else.
func CrossSession(s string) (from, body string, ok bool) {
	m := crossRe.FindStringSubmatch(s)
	if m == nil {
		return "", "", false
	}
	from = FromSession
	if n := crossNameRe.FindStringSubmatch(m[1]); n != nil && n[1] != "" {
		from = n[1]
	}
	return from, strings.TrimSpace(m[2]), true
}

// markWake flags a background task's notice that arrives as a user message: the agent was idle and the notice
// started a new turn (ones absorbed mid-turn come as queued_command attachments instead).
func markWake(items []proto.Item) {
	for i := range items {
		if items[i].Kind == proto.KindNotice {
			items[i].Wake = true
		}
	}
}

func userText(mk func(int, string, string) proto.Item, s string, meta *Meta) []proto.Item {
	trimmed := legacyLarkAgent(strings.TrimSpace(caveatRe.ReplaceAllString(pastedRe.ReplaceAllString(ContextRe.ReplaceAllString(s, ""), ""), "")))
	if trimmed == "" {
		return nil
	}
	if from, body, ok := CrossSession(trimmed); ok {
		it := mk(0, "user", proto.KindText)
		it.Text, it.From = clip(body), from
		return []proto.Item{it}
	}
	if m := teammateRe.FindStringSubmatch(trimmed); m != nil { // agent teams: a teammate reporting to the lead
		from := "队友"
		if n := teammateIDRe.FindStringSubmatch(m[1]); n != nil && n[1] != "" {
			from = n[1]
		}
		it := mk(0, "user", proto.KindText)
		it.Text, it.From = clip(teammateNote(strings.TrimSpace(m[2]))), from
		return []proto.Item{it}
	}
	// "! cmd" typed in Claude Code runs the command directly: the input reads as the user's own line, its output
	// as a Bash call under it (the input line comes first; its command is kept for the output's call).
	if strings.HasPrefix(trimmed, "<bash-input>") {
		cmd := strings.TrimSpace(firstTag(trimmed, "bash-input"))
		meta.bashInput, meta.localInput = cmd, true
		it := mk(0, "user", proto.KindText)
		it.Text = clip("! " + cmd)
		return []proto.Item{it}
	}
	if strings.HasPrefix(trimmed, "<bash-stdout>") || strings.HasPrefix(trimmed, "<bash-stderr>") {
		out := strings.TrimSpace(strings.TrimSpace(firstTag(trimmed, "bash-stdout")) + "\n" + strings.TrimSpace(firstTag(trimmed, "bash-stderr")))
		input, _ := json.Marshal(map[string]string{"command": meta.bashInput})
		meta.bashInput = ""
		meta.endLocalTurn()
		use := mk(0, "assistant", proto.KindToolUse)
		id := strings.TrimSuffix(use.ID, "#0") + "-bash"
		use.ToolUseID, use.ToolName, use.ToolInput = id, "Bash", input
		res := mk(1, "user", proto.KindToolResult)
		res.ToolUseID, res.Text, res.IsError = id, clip(out), strings.TrimSpace(firstTag(trimmed, "bash-stderr")) != ""
		return []proto.Item{use, res}
	}
	if strings.HasPrefix(trimmed, "<command-name>") || strings.HasPrefix(trimmed, "<command-message>") {
		name := firstTag(trimmed, "command-name")
		args := firstTag(trimmed, "command-args")
		if name == "/effort" && isUltracodeCommand(args) && !meta.tree.sidechain {
			// The command's confirmed output becomes one notice; the command itself is not a user prompt.
			meta.localInput = true
			return nil
		}
		if name == "/compact" && strings.TrimSpace(args) == "" {
			// Persona compacts an idle assistant conversation itself (node/assistant.go): a notice, not a message the
			// user sent, and not activity that would make the conversation unread.
			it := mk(0, "system", proto.KindNotice)
			it.Text = CompactNotice
			return []proto.Item{it}
		}
		meta.localInput = true
		it := mk(0, "user", proto.KindText)
		it.Text = strings.TrimSpace(name + " " + args)
		return []proto.Item{it}
	}
	if strings.HasPrefix(trimmed, "<local-command-stdout>") || strings.HasPrefix(trimmed, "<local-command-stderr>") {
		meta.endLocalTurn()
		it := mk(0, "system", proto.KindNotice)
		it.Text = clip(strings.TrimSpace(ansiRe.ReplaceAllString(tagRe.ReplaceAllString(trimmed, "$2"), "")))
		if it.Text == "" {
			return nil
		}
		meta.claudeGoalOutput(it.Text, meta.Updated)
		if strings.HasPrefix(trimmed, "<local-command-stdout>") && !meta.tree.sidechain {
			if items, ok := meta.claudeUltracodeOutput(it.Text, it.TS, it); ok {
				return items
			}
		}
		return []proto.Item{it}
	}
	if strings.HasPrefix(trimmed, "<task-notification>") {
		if meta.notified[trimmed] {
			return nil
		}
		if meta.notified == nil {
			meta.notified = map[string]bool{}
		}
		meta.notified[trimmed] = true
		it := mk(0, "system", proto.KindNotice)
		it.Task, it.Text = taskNote(trimmed)
		meta.noteCall(it.Task)
		meta.reportFor(it.Task)
		return []proto.Item{it}
	}
	if trimmed == "[Request interrupted by user]" || trimmed == "[Request interrupted by user for tool use]" {
		meta.TurnOpen, meta.TurnClosed = false, true
		it := mk(0, "system", proto.KindNotice)
		it.Text = InterruptedNotice
		return []proto.Item{it}
	}
	text := strings.TrimSpace(tagRe.ReplaceAllStringFunc(trimmed, func(m string) string {
		if strings.HasPrefix(m, "<system-reminder>") {
			return ""
		}
		return m
	}))
	if text == "" {
		return nil
	}
	it := mk(0, "user", proto.KindText)
	it.Text = clip(text)
	return []proto.Item{it}
}

// endLocalTurn: a local command's output came, so the turn it started (no agent reply in between) is over and the
// turn state is what it was before the command: Claude Code writes no turn_duration for it (#4661).
func (m *Meta) endLocalTurn() {
	if m.localTurn && m.TurnOpen {
		m.TurnOpen, m.TurnClosed = m.beforeLocal[0], m.beforeLocal[1]
	}
	m.localTurn = false
}

// ansiRe matches terminal escape sequences (colors, bold) that local command output keeps.
var ansiRe = lazyre.New(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

// apiErrorText is what an api_error line says went wrong: error.formatted / error.message, or older lines' content.
func apiErrorText(e entry) string {
	var s string
	if json.Unmarshal(e.Error, &s) == nil && s != "" {
		return s
	}
	var obj struct {
		Message   string `json:"message"`
		Formatted string `json:"formatted"`
	}
	if json.Unmarshal(e.Error, &obj) == nil {
		if obj.Formatted != "" {
			return obj.Formatted
		}
		if obj.Message != "" {
			return obj.Message
		}
	}
	if s := strings.TrimSpace(rawString(e.Content)); s != "" {
		return s
	}
	return "请求失败"
}

// snippetLine is the first line of s, at most n runes.
func snippetLine(s string, n int) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	r := []rune(strings.TrimSpace(s))
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return string(r)
}

func firstTag(s, tag string) string {
	open, close := "<"+tag+">", "</"+tag+">"
	i := strings.Index(s, open)
	if i < 0 {
		return ""
	}
	rest := s[i+len(open):]
	j := strings.Index(rest, close)
	if j < 0 {
		return ""
	}
	return strings.TrimSpace(rest[:j])
}

func toolResultContent(raw json.RawMessage) (string, []*proto.Image) {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s, nil
	}
	var blocks []block
	if json.Unmarshal(raw, &blocks) != nil {
		return "", nil
	}
	var parts []string
	var imgs []*proto.Image
	for _, b := range blocks {
		switch b.Type {
		case "text":
			parts = append(parts, b.Text)
		case "image":
			if img := imageOf(b); img != nil {
				imgs = append(imgs, img)
			}
		case "tool_reference":
			parts = append(parts, "已加载工具 "+b.Name)
		}
	}
	return strings.Join(parts, "\n"), imgs
}

func imageOf(b block) *proto.Image {
	if b.Source == nil || b.Source.Type != "base64" || len(b.Source.Data) > maxImageLen {
		return nil
	}
	return &proto.Image{Mime: b.Source.MediaType, Data: b.Source.Data}
}

func rawString(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return string(raw)
}

func clip(s string) string { return clipTo(s, maxTextLen) }

// clipTo cuts s to at most n bytes on a UTF-8 boundary and notes the original size.
func clipTo(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return s[:cut] + fmt.Sprintf("\n…（已截断，共 %d 字节）", len(s))
}

// light is the copy of an item that pages and pushes carry; cut says something was left out (fetch it with Full).
// Long text ends in a line that says it was cut, so apps that do not know FullSize still show it truncated, not whole.
// Only a tool's output has a "View full text" button in the app, so only its note says to tap for the whole.
func light(it proto.Item, keepImage bool) (_ proto.Item, cut bool) {
	limit, note := pageTextLen, "\n…（已截断，共 %d 字节）"
	if it.Kind == proto.KindToolResult {
		limit, note = pageOutputLen, "\n…（已截断，点开看全文，共 %d 字节）"
	}
	if len(it.Text) > limit && it.Role == "user" && it.Kind == proto.KindText {
		if t, ok := lightContexts(it.Text, limit); ok { // what the user wrote stays whole, the blocks' bodies are cut
			it.FullSize, it.Text = len(it.Text), t
			cut = true
		}
	}
	if len(it.Text) > limit {
		end := limit
		for end > 0 && (it.Text[end]&0xC0) == 0x80 {
			end--
		}
		if nl := strings.LastIndexByte(it.Text[:end], '\n'); nl > end-512 { // end on a whole line when one is near
			end = nl
		}
		it.FullSize = len(it.Text)
		it.Text = it.Text[:end] + fmt.Sprintf(note, len(it.Text))
		cut = true
	}
	if it.Image != nil && it.Image.Data != "" && !keepImage {
		it.Image = it.Image.Placeholder()
		cut = true
	}
	return it, cut
}

// clipJSON shortens very long string fields (e.g. Write content) inside tool inputs.
func clipJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) <= maxInputLen {
		return raw
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return raw
	}
	for k, v := range m {
		if s, ok := v.(string); ok && len(s) > maxInputLen/2 {
			m[k] = clipTo(s, maxInputLen/2)
		}
	}
	b, _ := json.Marshal(m)
	return b
}

// File incrementally parses a transcript file. Safe for concurrent use.
type File struct {
	Path string

	mu      sync.Mutex
	offset  int64
	lines   int
	partial []byte
	items   []proto.Item
	meta    Meta
	// History identity (Span.History): the file's inode, a hash of its first line, and how many times it was found
	// rewritten while this process read it.
	ino       uint64
	firstHash uint32
	gen       int
	// tree: the transcript's branches (branch.go): lines on an abandoned one are not kept as items.
	tree *branchTree
	// cut: where the lines whose stored items were cut down (light) sit in the file, by seq; Full reads them again.
	// Images used to stay in memory for every session ever opened.
	cut map[int]lineRef
	// A big file opened cold is parsed from a window at its end (window.go): lines 1..base are not parsed yet, win
	// says where they are, and meta covers only the parsed lines (partialMeta) until Meta works it out for the file.
	base        int
	win         *window
	partialMeta bool
}

type lineRef struct {
	off int64
	n   int
}

// Span says where a batch of items sits in the transcript, so a client can tell whether it continues what it has.
// History names this version of the file: it changes when the file is replaced or rewritten (seqs start over), and
// stays the same across node restarts. From is the line the batch continues from (it holds lines From+1..To), To the
// last line it accounts for — lines without items (titles, modes, attachments) count too, so To is often past the last
// item's seq.
type Span struct {
	History  string
	From, To int
}

func NewFile(path string) *File { return &File{Path: path} }

// Where is the path the file is read from now.
func (f *File) Where() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Path
}

// Relocate points the file at where its transcript is now: Codex moves a rollout between sessions/ and
// archived_sessions/ when a thread is archived or restored. What was read stays; reading goes on from there (a moved
// file keeps its inode, so Refresh does not start over).
func (f *File) Relocate(path string) {
	f.mu.Lock()
	f.Path = path
	f.mu.Unlock()
}

// NewSubagentFile reads a subagent's transcript (<session>/subagents/agent-<id>.jsonl, every line a sidechain).
func NewSubagentFile(path string) *File { return &File{Path: path, meta: Meta{Sidechain: true}} }

func (f *File) history() string {
	if f.lines == 0 {
		return "" // nothing read yet: no identity to compare
	}
	return fmt.Sprintf("%x.%08x.%d", f.ino, f.firstHash, f.gen) + f.branchToken()
}

// Refresh reads newly appended complete lines and returns the new items.
func (f *File) Refresh() ([]proto.Item, error) {
	fresh, _, err := f.RefreshSpan()
	return fresh, err
}

// RefreshSpan is Refresh that also says which lines it read (From == To: none).
func (f *File) RefreshSpan() ([]proto.Item, Span, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fresh, span, err := f.refresh()
	if err != nil || f.tree == nil || len(f.tree.revoked) == 0 {
		return fresh, span, err
	}
	// Claude went on from a turn the node took for rewound (RewoundAt): it was not. Its items are gone from those kept,
	// so the file is read again from the start without it, under a new token (the clients start over, as for a file
	// rewritten).
	manual := map[string]bool{}
	for u := range f.tree.manual {
		manual[u] = true
	}
	for _, u := range f.tree.revoked {
		delete(manual, u)
	}
	f.offset, f.lines, f.partial, f.items, f.cut, f.meta = 0, 0, nil, nil, nil, Meta{Sidechain: f.meta.Sidechain}
	f.base, f.win, f.partialMeta, f.firstHash = 0, nil, false, 0
	f.tree = &branchTree{manual: manual, marked: f.tree.marked}
	f.gen++
	return f.refresh()
}

// freshTree is a new tree for the file read again from somewhere: the roots the node set stay (none after a file
// replaced, whose tree is nil), with the lines that were under them then (branchTree.marked).
func (f *File) freshTree() *branchTree {
	if f.tree == nil {
		return &branchTree{}
	}
	t := f.tree.kept()
	return &t
}

func (f *File) refresh() ([]proto.Item, Span, error) {
	fh, err := os.Open(f.Path)
	if err != nil {
		return nil, Span{}, err
	}
	defer fh.Close()
	st, err := fh.Stat()
	if err != nil {
		return nil, Span{}, err
	}
	ino := inode(st)
	if st.Size() < f.offset || (f.ino != 0 && ino != 0 && ino != f.ino) { // truncated or replaced: start over
		f.offset, f.lines, f.partial, f.items, f.cut, f.meta, f.tree = 0, 0, nil, nil, nil, Meta{Sidechain: f.meta.Sidechain}, nil
		f.base, f.win, f.partialMeta = 0, nil, false
		f.firstHash = 0
		f.gen++
	}
	f.ino = ino
	from := f.lines
	if st.Size() == f.offset {
		return nil, Span{f.history(), from, from}, nil
	}
	if f.offset == 0 && st.Size() > 2*windowBytes {
		// A big file read cold: its end only (the batch continues from the lines not parsed).
		fresh, ok, err := f.openWindow(fh, st.Size())
		if err != nil {
			return nil, Span{}, err
		}
		if ok {
			return fresh, Span{f.history(), f.base, f.lines}, nil
		}
	}
	if _, err := fh.Seek(f.offset, io.SeekStart); err != nil {
		return nil, Span{}, err
	}
	// Read straight after the partial line into a buffer sized from the stat (bytes.MinRead spare so the EOF read does
	// not grow it): one copy of the new bytes instead of io.ReadAll's doublings plus an append. A file still growing
	// is read to EOF all the same.
	buf := bytes.NewBuffer(make([]byte, 0, len(f.partial)+int(st.Size()-f.offset)+bytes.MinRead))
	buf.Write(f.partial)
	if _, err := buf.ReadFrom(fh); err != nil {
		return nil, Span{}, err
	}
	data := buf.Bytes()
	f.offset += int64(len(data) - len(f.partial))
	f.seeNotifications(data)
	pos := f.offset - int64(len(data)) // where data starts in the file
	var fresh []proto.Item
	for {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			break
		}
		line, at := data[:i], pos
		data, pos = data[i+1:], pos+int64(i)+1
		f.lines++
		if f.lines == 1 {
			f.firstHash = crc32.ChecksumIEEE(line)
		}
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		items := ParseLine(f.lines, line, &f.meta)
		if f.tree == nil {
			f.tree = &branchTree{}
		}
		if trackLine(f.tree, &f.meta, items, &f.items, &fresh) { // on a branch that was gone back over (branch.go)
			continue
		}
		// New items go out with their images (subscribers that did not ask for lazy images get them whole); the
		// stored copy leaves them out.
		for _, it := range items {
			sent, _ := light(it, true)
			kept, cut := light(it, false)
			fresh = append(fresh, sent)
			f.items = append(f.items, kept)
			if cut {
				if f.cut == nil {
					f.cut = map[int]lineRef{}
				}
				f.cut[f.lines] = lineRef{at, i}
			}
		}
	}
	f.partial = append([]byte(nil), data...)
	return fresh, Span{f.history(), from, f.lines}, nil
}

// before returns the index of the first item with seq >= beforeSeq (0 = len, the end). Callers hold f.mu.
func (f *File) before(beforeSeq int) int {
	if beforeSeq <= 0 {
		return len(f.items)
	}
	end := 0
	for end < len(f.items) && f.items[end].Seq < beforeSeq {
		end++
	}
	return end
}

// pageSpan is the span of items[start:end] counted back from beforeSeq. Callers hold f.mu.
func (f *File) pageSpan(start, beforeSeq int) Span {
	sp := Span{History: f.history(), To: f.lines}
	if beforeSeq > 0 {
		sp.To = beforeSeq - 1
	}
	if (start > 0 || f.base > 0) && start < len(f.items) {
		sp.From = f.items[start].Seq - 1
	}
	return sp
}

// Page returns items with seq < beforeSeq (0 = from the end), newest `limit`, in ascending order.
func (f *File) Page(beforeSeq, limit int) ([]proto.Item, bool, Span) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensureItems(beforeSeq, limit)
	end := f.before(beforeSeq)
	start := max(end-limit, 0)
	// Never split a transcript line across pages.
	for start > 0 && f.items[start].Seq == f.items[start-1].Seq {
		start--
	}
	out := append([]proto.Item(nil), f.items[start:end]...)
	return out, start > 0 || f.base > 0, f.pageSpan(start, beforeSeq)
}

// PageTurns returns the items before beforeSeq (0 = the end) going back far enough to hold `turns` whole exchanges:
// it starts at a user prompt, so a page never opens in the middle of a turn. maxItems caps one enormous turn.
func (f *File) PageTurns(beforeSeq, turns, maxItems int) ([]proto.Item, bool, Span) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensureTurns(beforeSeq, turns, maxItems)
	end := f.before(beforeSeq)
	start, seen := end, 0
	for start > 0 && seen < turns && end-start < maxItems {
		start--
		if it := f.items[start]; it.Role == "user" && it.Kind == proto.KindText && !it.Queued {
			seen++
		}
	}
	for start > 0 && f.items[start].Seq == f.items[start-1].Seq { // never split a transcript line across pages
		start--
	}
	out := append([]proto.Item(nil), f.items[start:end]...)
	return out, start > 0 || f.base > 0, f.pageSpan(start, beforeSeq)
}

// Full returns an item whole: all of its text and its image data (pages and pushes carry a light copy). The line it
// came from is read and parsed again; if that no longer gives the item (the file changed under it), it says so
// rather than pass the cut copy off as whole.
func (f *File) Full(id string) (proto.Item, error) {
	f.mu.Lock()
	var it proto.Item
	found := false
	for {
		for i := len(f.items) - 1; i >= 0; i-- {
			if f.items[i].ID == id {
				it, found = f.items[i], true
				break
			}
		}
		if found || f.loadOlder() == 0 { // an item in lines not parsed yet: parse back to it
			break
		}
	}
	ref, cut := f.cut[it.Seq]
	scratch := Meta{Sidechain: f.meta.Sidechain, Codex: f.meta.Codex}
	f.mu.Unlock()
	if !found {
		return it, fmt.Errorf("no item %s", id)
	}
	if !cut {
		return it, nil
	}
	full, ok := f.reparse(it.Seq, ref, &scratch)[id]
	if !ok {
		return it, fmt.Errorf("item %s: line %d of the transcript changed, reload the chat", id, it.Seq)
	}
	it.Text, it.FullSize, it.Image = full.Text, 0, full.Image
	return it, nil
}

// WithImages puts the image data back into items (for callers that did not ask for lazy images).
func (f *File) WithImages(items []proto.Item) []proto.Item {
	var out []proto.Item
	parsed := map[int]map[string]proto.Item{}
	for i, it := range items {
		if it.Image == nil || !it.Image.Lazy {
			continue
		}
		f.mu.Lock()
		ref, cut := f.cut[it.Seq]
		scratch := Meta{Sidechain: f.meta.Sidechain, Codex: f.meta.Codex}
		f.mu.Unlock()
		if !cut {
			continue
		}
		if parsed[it.Seq] == nil {
			parsed[it.Seq] = f.reparse(it.Seq, ref, &scratch)
		}
		if full, ok := parsed[it.Seq][it.ID]; ok && full.Image != nil {
			if out == nil {
				out = append([]proto.Item(nil), items...)
			}
			out[i].Image = full.Image
		}
	}
	if out == nil {
		return items
	}
	return out
}

// reparse reads line seq again and returns its items by id (none if the file changed under it).
func (f *File) reparse(seq int, ref lineRef, scratch *Meta) map[string]proto.Item {
	fh, err := os.Open(f.Path)
	if err != nil {
		return nil
	}
	defer fh.Close()
	line := make([]byte, ref.n)
	if _, err := fh.ReadAt(line, ref.off); err != nil {
		return nil
	}
	out := map[string]proto.Item{}
	for _, it := range ParseLine(seq, line, scratch) {
		out[it.ID] = it
	}
	return out
}

// After returns items with seq > afterSeq, oldest first: at least limit of them unless fewer exist, and never part of
// a line (a Codex tool call is its call and its result on one line; a client resuming from the last seq it got would
// never see the rest). more says items are left after the last one returned. The span runs from afterSeq to the last
// line read when nothing is left, else to the last item's line.
func (f *File) After(afterSeq, limit int) (items []proto.Item, more bool, span Span) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensureAfter(afterSeq)
	span = Span{History: f.history(), From: afterSeq, To: max(f.lines, afterSeq)}
	i := 0
	for i < len(f.items) && f.items[i].Seq <= afterSeq {
		i++
	}
	end := min(i+limit, len(f.items))
	for end > i && end < len(f.items) && f.items[end].Seq == f.items[end-1].Seq {
		end++
	}
	if end == i {
		return nil, false, span
	}
	more = end < len(f.items)
	if more {
		span.To = f.items[end-1].Seq
	}
	return append([]proto.Item(nil), f.items[i:end]...), more, span
}

// Meta is the session-level information of the whole file (for a file opened by its end, worked out on first call).
func (f *File) Meta() Meta { return f.fullMeta() }

func (f *File) LastSeq() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lines
}
