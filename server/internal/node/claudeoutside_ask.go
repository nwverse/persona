package node

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/screen"
	"github.com/nwverse/persona/server/internal/transcript"
)

// Question cards for a Claude session outside Persona's terminals (docs/integrations/claude-integration.md "Question
// cards outside Persona's terminals"). Such a claude runs no hook of Persona's, so its AskUserQuestion made no card: the
// apps only showed the row's line "it is asking you a question: reply to answer", and the questions and their options
// were nowhere to be seen. While one waits on a question it asked (its status file says waiting and its transcript's
// latest call is an AskUserQuestion nothing answered yet) and its peer inbox is one the node can use, the node reads
// the call's questions out of the transcript and lists them as a card along with the Codex cards
// (codex.interactions), so the hub keeps it as it keeps those. The phone's answer comes back as codex.answer and goes
// to the session through its inbox as the user's answers (prompts agents/answer), at priority now: that ends the
// question on the computer and the model takes the message as its answer (sendOutside, #5961). A claude in a tmux
// pane is left out: its dialog is read off its screen.

// outsideAskPrefix starts the id of such a card; codex.answer tells it from a Codex card by it.
const outsideAskPrefix = "clq_"

// outsideAskCards are the question cards of the claudes outside Persona's panes that wait on a question they asked.
func (n *Node) outsideAskCards() []proto.Interaction {
	var out []proto.Interaction
	seen := map[string]bool{}
	for _, c := range n.liveClaudes() {
		// The status file first: every other check reads a file or the process, and almost no claude is waiting.
		if c.Status != "waiting" || seen[c.SessionID] || n.outsideSource(c) == "" || outsidePane(c) != "" {
			continue
		}
		if p, has := peerOf(c); !has || !peerReachable(p) {
			continue // nothing could answer it from the phone: the row says to answer on the computer
		}
		seen[c.SessionID] = true
		if ix, ok := n.outsideAskCard(c); ok {
			out = append(out, ix)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

// outsideAskCacheEntry caches readPendingOutsideAsk for one transcript file stat so repeated waiting polls avoid disk
// I/O while any write (size or mtime change) re-reads immediately without metaMinInterval staleness.
type outsideAskCacheEntry struct {
	path  string
	size  int64
	mtime time.Time
	item  *proto.Item
}

// pendingOutsideAsk returns the current turn's unanswered AskUserQuestion call that outside claude c is waiting on, or
// nil when c is not waiting or its current turn's latest tool call is not an open AskUserQuestion. It performs a
// bounded read-only suffix scan directly against the transcript file — never acquiring subscriber push locks or
// triggering watch pushes — so card creation, Capabilities.Approve, AttentionReason, and priority-now replies share
// one source of truth without blocking on slow links or quickMeta's 3s cache / 384KB tail / KindText clearing.
func (n *Node) pendingOutsideAsk(c claudeProc) *proto.Item {
	if c.Status != "waiting" {
		return nil
	}
	id := c.SessionID
	p := n.transcriptPath(id)
	if p == "" {
		return nil
	}
	st, err := os.Stat(p)
	if err != nil {
		return nil
	}
	n.cx.mu.Lock()
	if e, ok := n.cx.outsideAskCache[id]; ok && e.path == p && e.size == st.Size() && e.mtime.Equal(st.ModTime()) {
		item := copyItemPtr(e.item)
		n.cx.mu.Unlock()
		return item
	}
	n.cx.mu.Unlock()

	item := readPendingOutsideAsk(p, st.Size())
	n.cx.mu.Lock()
	if n.cx.outsideAskCache == nil {
		n.cx.outsideAskCache = map[string]outsideAskCacheEntry{}
	} else if len(n.cx.outsideAskCache) >= 64 {
		clear(n.cx.outsideAskCache)
	}
	n.cx.outsideAskCache[id] = outsideAskCacheEntry{path: p, size: st.Size(), mtime: st.ModTime(), item: copyItemPtr(item)}
	n.cx.mu.Unlock()
	return item
}

func copyItemPtr(it *proto.Item) *proto.Item {
	if it == nil {
		return nil
	}
	cp := *it
	return &cp
}

// readPendingOutsideAsk scans expanding bounded suffixes of path (from quickTail up to lookBackReach) read-only and
// returns the current turn's latest tool call when it is an unanswered AskUserQuestion, else nil. Any non-queued user
// message or turn close ends the previous turn and resets any earlier unanswered AskUserQuestion, whereas trailing
// assistant text or queued user messages in the same turn keep it open.
func readPendingOutsideAsk(path string, size int64) *proto.Item {
	if size <= 0 {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	for chunk := int64(quickTail); ; chunk = min(chunk*2, lookBackReach) {
		start := max(size-chunk, 0)
		buf := make([]byte, size-start)
		if nr, err := f.ReadAt(buf, start); err != nil && nr < len(buf) {
			return nil
		}
		lines := bytes.Split(buf, []byte("\n"))
		if start > 0 && len(lines) > 0 {
			lines = lines[1:] // partial first line; included whole in the next larger window
		}
		var tm transcript.Meta
		answered := map[string]bool{}
		var last *proto.Item
		sawBoundary := false
		for _, line := range lines {
			if len(line) == 0 {
				continue
			}
			prevClosed := tm.TurnClosed
			items := transcript.ParseLine(0, line, &tm)
			if tm.TurnClosed && !prevClosed {
				last = nil
				sawBoundary = true
			}
			for i := range items {
				it := items[i]
				switch {
				case it.Kind == proto.KindToolResult && it.ToolUseID != "":
					answered[it.ToolUseID] = true
				case it.Wake || (it.Role == "user" && it.Kind != proto.KindToolResult && !it.Queued):
					last = nil
					sawBoundary = true
				case it.Role == "assistant" && it.Kind == proto.KindToolUse:
					cp := it
					last = &cp
					sawBoundary = true
				}
			}
		}
		if sawBoundary || start == 0 || chunk == lookBackReach {
			if last == nil || last.ToolName != askTool || last.ToolUseID == "" || answered[last.ToolUseID] {
				return nil
			}
			return last
		}
	}
}

// outsideAskCard is the card of the question claude c waits on, ok false when its current turn's latest call is not an
// AskUserQuestion still open (a permission prompt, a plan, a dialog of its own), when an active AskUserQuestion hook
// already covers the same tool call, or when the call cannot be one card.
func (n *Node) outsideAskCard(c claudeProc) (proto.Interaction, bool) {
	id := c.SessionID
	call := n.pendingOutsideAsk(c)
	if call == nil || n.activeAsks.coversAsk(id, call.ToolUseID) {
		return proto.Interaction{}, false
	}
	var ix proto.Interaction
	if !questionInteraction(hookInput{ToolInput: call.ToolInput}, &ix) || proto.HasDuplicateQuestionText(ix.Questions) {
		return proto.Interaction{}, false // answers go by question text: alike-worded ones are answered with a reply
	}
	for i, q := range ix.Questions {
		if q.Options == nil {
			ix.Questions[i].Options = []proto.QuestionOption{}
		}
	}
	ix.ID = outsideAskPrefix + shortHash(id+"\x00"+call.ToolUseID)
	ix.SessionID, ix.Agent, ix.ToolName, ix.ToolUseID = id, "claude", askTool, call.ToolUseID
	// No Skip: the inbox carries messages only, and any message to the session is taken as its answer.
	ix.CanCancel = false
	ix.Cwd = c.Cwd
	ix.CreatedAt = call.TS.UTC()
	if call.TS.IsZero() {
		ix.CreatedAt = time.Now().UTC()
	}
	ix.ExpiresAt = time.Now().Add(24 * time.Hour).UTC()
	if p := n.transcriptPath(id); p != "" {
		if st, err := os.Stat(p); err == nil {
			m := n.meta(p, st)
			ix.SessionTitle = m.title
			if ix.Cwd == "" {
				ix.Cwd = m.cwd
			}
		}
	}
	if ix.SessionTitle == "" && c.NameSource != "derived" {
		ix.SessionTitle = c.Name
	}
	if ix.SessionTitle == "" && ix.Cwd != "" {
		ix.SessionTitle = filepath.Base(ix.Cwd)
	}
	return ix, true
}

// outsideAnswer sends the phone's answer to a card of outsideAskCards through the session's inbox. A card handed back
// to the computer is not listed again while the claude asks it.
func (n *Node) outsideAnswer(ctx context.Context, session, id string, r proto.Resolution) error {
	if r.Cancel {
		return fmt.Errorf("question cancellation is unavailable for a Claude session outside Persona's terminals")
	}
	if r.Dismiss {
		n.cx.dismiss(session, id)
		return nil
	}
	var card *proto.Interaction
	if c, ok := n.outsideClaude(session); ok && c.Status == "waiting" {
		if ix, ok := n.outsideAskCard(c); ok && ix.ID == id {
			card = &ix
		}
	}
	if card == nil {
		return refusal(screen.PromptChanged, outsideAskGone, nil)
	}
	text := outsideAnswerTextIn(*card, r, agentPromptLanguage(ctx))
	if text == "" {
		return fmt.Errorf("没有回答任何一道题")
	}
	handled, err := n.sendOutsidePriority(ctx, session, text, nil, peerNow)
	if !handled {
		return refusal(screen.PromptChanged, outsideAskGone, nil)
	}
	if err == nil {
		n.cx.hold(session, id) // its transcript may show the question closed a moment after the inbox took the answer
	}
	return err
}

// outsideAskGone refuses an answer to a question the session no longer waits on.
const outsideAskGone = "Claude 已经不在等这个回答了（可能在电脑上答过了，或者这一轮被中断了），这次什么都没发。"

// outsideAnswerText is the user's answers as the message the session takes for them (agents/answer), "" when the
// resolution answers none of the card's questions.
func outsideAnswerText(ix proto.Interaction, r proto.Resolution) string {
	return outsideAnswerTextIn(ix, r, "")
}

func outsideAnswerTextIn(ix proto.Interaction, r proto.Resolution, language string) string {
	a := prompts.AgentAnswer{Language: language, ByAssistant: r.AnsweredBy == proto.AnsweredByAssistant}
	if a.ByAssistant {
		a.Reason = oneLineText(r.Reason)
	}
	for _, qa := range proto.QuestionAnswers(ix, r) {
		if answer := strings.TrimSpace(qa.Answer); answer != "" {
			a.Items = append(a.Items, prompts.AgentAnswerItem{Question: oneLineText(qa.Question), Answer: answer})
		}
	}
	if len(a.Items) == 0 {
		return ""
	}
	return prompts.RenderAgentAnswer(a)
}

// oneLineText folds a question's line breaks: each answer is one line of the list the session reads.
func oneLineText(s string) string { return strings.Join(strings.Fields(s), " ") }
