package assistant

import (
	"context"
	"encoding/json"
	"slices"
	"sync"

	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// Each round of the background supervisor starts clean (#7315, docs/assistant/unified-entry.md "Each round starts
// clean"). It was one conversation that every wake went on with: every round reread every earlier one, and the
// tools of every kind of work, though a round needs its own signals and what the tasks' ledgers keep. It
// is still one conversation, because the clients' row, its rights over the tasks (backgroundSupervises), the
// announcement's receipt and the persisted relay queue all key on its id; but each round of it resumes at the end of
// its first turn (the node restarts its idle process with --resume-session-at, node/assistant_round.go), so a round
// reads the start brief, the whole snapshot and its own supervision message; the handling rules are the static
// supervision instructions its process starts with (supervision_instructions.go), as every supervisor's. That message
// is rendered Fresh: a line saying what crosses rounds is on the tasks, the round marker and each entry's last review
// (prompts.Supervise.Fresh). The round's process lists only the tools supervision uses (roundToolNames,
// through mcp.tools' tool_set).
//
// assistant.background_supervisor_keeps_context (off by default) brings back the one long conversation from its next
// round: no round is asked for, the message is rendered as before (not Fresh), and a message that asks for no round needs every tool,
// so the node replaces a round's process, once it is idle, by a plain resume with every tool (node/assistant_round.go).
//
// The node says how each round went (superviseRoundAnswered). A legacy round it could not start from the first turn
// because that turn is no longer on the conversation's chain (no_anchor), and a restarted round whose transcript is
// over backgroundTranscriptLimit, make the legacy conversation unable to continue. At the next idle digest its unread
// work moves to main (backgroundOutgrown, ensureBackground), without archiving it or starting a replacement (#5732).

// superviseRoundParam marks an assistant.send to the background supervisor that is a round (node/assistant_round.go).
const superviseRoundParam = "supervise_round"

// toolSetSupervise is the round's tool set as its process asks for it (mcp.tools tool_set).
const toolSetSupervise = "supervise"

type toolSetKey struct{}

// withToolSet is ctx carrying the tool set an mcp.tools request asks for (its tool_set), for sessionMCPTools: ctx as
// it is when the request names none.
func withToolSet(ctx context.Context, params json.RawMessage) context.Context {
	var p struct {
		ToolSet string `json:"tool_set"`
	}
	if json.Unmarshal(params, &p) != nil || p.ToolSet == "" {
		return ctx
	}
	return context.WithValue(ctx, toolSetKey{}, p.ToolSet)
}

// toolSetOf is the tool set withToolSet put on ctx, "" for none.
func toolSetOf(ctx context.Context) string {
	set, _ := ctx.Value(toolSetKey{}).(string)
	return set
}

// roundToolNames are the tools a round lists, and why each is in: the ones its supervision work uses (follow-ups,
// computers and agents, an agent's cards, the knowledge base, the tasks' plans, reviews and scope, a conversation's
// words), and the ones its prompts point it to: ask_escalate for a card (the supervision instructions), task_decided for
// a decision_reply entry (its message asks it to confirm the owner's answer with it, and the tool lets the background
// supervisor record it), todo_get where its message says what crosses rounds is kept, and todo_list, agents_list,
// list_followups and kb_get where the snapshot leaves items out. lark_search, or larkcli_search from the lark-cli
// connector, is listed when the install gives it.
// roundTools keeps them in the order of the lists they come from, so the cached prefix stays the same from round to
// round. Every tool a round's own message names is here, and the descriptions of these tools and the background
// supervisor's context name the others only in passing (TestRoundListsTheToolsItsMessageNames, roundPassingMentions
// there); every other tool the supervision instructions name is in roundToolsWithheld with why, and the instructions'
// background line sends that work elsewhere too (TestRoundToolsAgreeWithSupervisionInstructions). Left out as well, and
// why: the phone's, mail, inbox, view, place and routine tools (the owner's own errands; an inbox signal leaves replies
// to the Inbox's own draft); the model, settings, secret, skill and concern tools (the owner's changes, made in the
// main conversation); the repository and other to-do writing tools (the background supervisor is not a direction and
// records no work of its own); and the other agent, conversation, leader and direction tools (a supervised replacement
// starts with task_plan on the same todo, and what needs another conversation is the main conversation's).
var roundToolNames = []string{"notify_user", "supervision_finish", "schedule_followup", "list_followups", "cancel_followup",
	"machines_list", "agents_list", "agent_read", "agent_send", "agent_interrupt", "agent_archive", "interaction_answer",
	"ask_escalate", "kb_search", "kb_get", "kb_update", "conversation_read", "task_plan", "task_review", "task_scope",
	"task_decided", "todo_get", "todo_list", "lark_search", "larkcli_search"}

// roundToolsWithheld are the tools the supervision instructions name that a round does not list, each with why the
// background supervisor has no use for it in a round. The instructions every supervisor reads also tell it so (their
// background line), so the supervision instructions never send a round to a tool it lacks.
var roundToolsWithheld = map[string]string{
	"ask_user":          "an owner decision from the background supervisor is a task_review needs_user conclusion card, never ask_user (the instructions' background line)",
	"conversation_send": "leader_stuck goes to the main conversation (digestTo)",
	"task_start_screen": "failed and started go to the main conversation, and only it remedies a start (remedyTool)",
	"task_start_keys":   "failed and started go to the main conversation, and only it remedies a start (remedyTool)",
	"task_retry":        "failed and started go to the main conversation, and only it retries a start (toolTaskRetry)",
	"agent_supervise":   "only the main conversation adopts an agent opened outside supervision (toolAgentSupervise)",
	"todo_create":       "a direction's own work: the background supervisor is not a direction and records no to-dos",
	"todo_complete":     "a direction's own work: the background supervisor is not a direction and records no to-dos",
	"leader_progress":   "a direction's own work: only a leader conversation reports progress (toolLeaderProgress)",
}

// roundTools are the tools of list a round lists, in list's order.
func roundTools(list []proto.Tool) []proto.Tool {
	out := make([]proto.Tool, 0, len(roundToolNames))
	for _, t := range list {
		if slices.Contains(roundToolNames, t.Name) {
			out = append(out, t)
		}
	}
	return out
}

// backgroundTranscriptLimit is how large a legacy background supervisor's transcript can be before its unread work
// moves to main rather than starting another clean round. The file only grows (a compaction appends to it too), and
// every round restarts the process, so Claude Code reads and parses all of it at each round's start, as the node does
// to find the anchor and the app's history after a node restart, though the round uses only the first turn. Measured
// with Claude Code 2.1.322 and a local mock of the model API on synthetic lines of about 4 KiB
// (node/assistant_round_live_test.go): a round asks the model 0.24 s after its process starts with a small file, 0.47 s
// at 32 MiB and about 0.8 s at 128 MiB, and the node's anchor scan takes 0.09 s and 0.35 s. Real transcripts that large
// were not measured and may parse slower. Only a round that restarted the process counts (superviseRoundAnswered).
const backgroundTranscriptLimit int64 = 32 << 20

// backgroundKeepsContext is assistant.background_supervisor_keeps_context in effect with settings cfg: the background
// supervisor is one long conversation, as before #7315 (off by default).
func backgroundKeepsContext(cfg Settings) bool {
	return cfg.BackgroundSupervisorKeepsContext != nil && *cfg.BackgroundSupervisorKeepsContext
}

// superviseRoundReply is the node's answer to a round's assistant.send (node/assistant_round.go): Context "fresh" (it
// started from the first turn) or "kept" (it went in as before) with Why, and the transcript's size. A node from before
// #7315 answers none of them, and so does any node to the same message asked again under its once_id.
type superviseRoundReply struct {
	SessionID       string `json:"session_id"`
	Context         string `json:"context"`
	Why             string `json:"why"`
	TranscriptBytes int64  `json:"transcript_bytes"`
}

// superviseRounds records what legacy background rounds answered, in memory: which conversation can no longer start
// another clean round and why, and whether an old node was logged. It never starts or names a replacement.
type superviseRounds struct {
	mu       sync.Mutex
	outgrown map[string]string // legacy background supervisor → why its unread work moves to main
	oldNode  bool
}

// superviseRoundAnswered notes how round res of legacy background supervisor to went (deliverRelay).
func (s *Service) superviseRoundAnswered(to string, res superviseRoundReply) {
	r := &s.background.rounds
	r.mu.Lock()
	defer r.mu.Unlock()
	if res.Context == "" {
		if !r.oldNode {
			r.oldNode = true
			s.logf("supervise: a background round's answer says nothing of its context (a node from before #7315 keeps the whole conversation, or the same message was asked again)")
		}
		return
	}
	if res.Context == "fresh" {
		s.logf("supervise: background round %s: fresh", short(to))
	} else {
		s.logf("supervise: background round %s: kept (%s)", short(to), res.Why)
	}
	why := ""
	switch {
	case res.Context == "kept" && res.Why == "no_anchor":
		why = "no_anchor"
	case res.TranscriptBytes > backgroundTranscriptLimit && (res.Context == "fresh" || res.Why == "start_failed"):
		// Only where rounds restart the process: a conversation that keeps its context for good is neither restarted
		// per round nor spared idle compaction, so its size alone is no reason to move the work.
		why = "oversized"
	}
	if why == "" {
		return
	}
	if r.outgrown == nil {
		r.outgrown = map[string]string{}
	}
	r.outgrown[to] = why
}

// backgroundOutgrown reports why legacy background supervisor id can no longer start another clean round. It reserves
// the conversation in one step with the idle check, so no message goes into it while its unread work moves to main
// (backgroundLost). A busy or draining conversation stays eligible for a later digest. background.mu is held.
func (s *Service) backgroundOutgrown(ctx context.Context, cfg Settings, id string) string {
	if backgroundKeepsContext(cfg) {
		return ""
	}
	r := &s.background.rounds
	r.mu.Lock()
	why := r.outgrown[id]
	r.mu.Unlock()
	if why == "" {
		return ""
	}
	rl := &s.relays
	rl.mu.Lock()
	reserved := s.relayLocked(ctx) && !rl.draining[id]
	if reserved {
		rl.replacing[id] = true
	}
	rl.mu.Unlock()
	if !reserved {
		return ""
	}
	if c, ok := s.session(ctx, cfg.MachineID, id); !ok || c.Status == proto.StatusWorking || c.Starting {
		s.releaseBackground(ctx, cfg.MachineID, id)
		return ""
	}
	return why
}

// releaseBackground lets a legacy background supervisor whose move to main was deferred take messages again, and has
// what waits for it go in now that may: a drain passed it by meanwhile.
func (s *Service) releaseBackground(ctx context.Context, machine, id string) {
	rl := &s.relays
	rl.mu.Lock()
	delete(rl.replacing, id)
	rl.mu.Unlock()
	if c, ok := s.session(ctx, machine, id); ok {
		s.drainRelays(machine, []proto.Session{c})
	}
}

// lastReviewEntry is a task's last review as a Fresh supervision message repeats it (#7315): its verdict, when (local)
// and its reason or note in a clipped line, so a round that does not see the earlier ones neither asks again what was
// decided nor sends back what was sent back already. nil when it was never reviewed. Only Fresh messages show it.
func (s *Service) lastReviewEntry(ctx context.Context, v *Supervision) *prompts.SuperviseLastReview {
	if v == nil || len(v.Reviews) == 0 {
		return nil
	}
	r := v.Reviews[len(v.Reviews)-1]
	note := r.Reason
	if note == "" {
		note = r.Note
	}
	return &prompts.SuperviseLastReview{Verdict: r.Verdict, At: r.At.In(s.location(ctx)).Format("01-02 15:04"), Note: clip(oneLine(note), lastReviewClip)}
}

// lastReviewClip is how many characters of a last review's reason or note a Fresh entry keeps.
const lastReviewClip = 200
