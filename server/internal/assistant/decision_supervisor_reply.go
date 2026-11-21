package assistant

import (
	"context"
	"time"

	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// Side-conversation markers, kept local so this file does not depend on task_completion.go's
// package vars (a parallel change extracts a shared prompts classifier). They match the two
// envelopes RenderSideConversation emits (prompts/assistant/side_conversation.md).
var (
	supervisorSideConversationContext = lazyre.New(`<persona-context\b[^>]*\bkind\s*=\s*["']side-conversation["'][^>]*>`)
	supervisorSideConversationMarker  = lazyre.New(`<persona-side-conversation\s*/>`)
)

// supervisorDecisionSpoke handles an owner message written in a supervisor / direction
// conversation itself. The Agent-chat path (agentDecisionSpoke) anchors on the to-do's worker
// session via activeTodoOf, which a supervisor conversation never is; this is the reverse
// lookup: the open needs_user to-dos supervised by the conversation the owner wrote in.
//
// An exact option label settles the single to-do it uniquely matches. A lone option letter
// ("A", "b") never settles here: unlike an Agent session, a supervisor / direction conversation
// is usually the owner's main chat and carries unrelated replies (its own lettered or yes/no
// questions), so a one-letter message cannot be tied to the card and is treated as free text
// instead. Free text never guesses an option: each affected to-do pauses its default and hands
// back to this conversation's supervisor through the existing decision_reply mechanism
// (#11852), with the reply sourced from the supervisor conversation. Provenance-marked relay /
// side-conversation input is not the owner answering and changes nothing. When one message
// could settle several to-dos at once, none is settled silently: every candidate is handed back
// for an explicit choice.
func (s *Service) supervisorDecisionSpoke(ctx context.Context, r SessionRef, at time.Time, text string) {
	if !supervisorOwnerText(text) {
		return
	}
	if err := s.ensureTodosLoaded(ctx); err != nil {
		s.logf("decision: supervisor reply: load tasks: %v", err)
		return
	}
	candidates := s.supervisedDecisionCandidates(r.SessionID)
	if len(candidates) == 0 {
		return
	}
	matched := make([]Todo, 0, len(candidates))
	for _, t := range candidates {
		a, ok := s.askCopy(t.ID)
		if !ok || supervisorOptionLabel(text, a.Options) == "" {
			continue
		}
		matched = append(matched, t)
	}
	switch {
	case len(matched) == 1 && len(candidates) == 1:
		s.settleSupervisorChoice(ctx, matched[0], at, r, text)
	case len(matched) == 1:
		// One exact match among several open questions is still ambiguous: the same words may be
		// meant for another card, so do not settle; hand every candidate back to the supervisor.
		s.handBackSupervisorReplies(ctx, r, at, candidates, text)
	default:
		// No unique exact answer (zero or several matches): never guess, hand them all back.
		s.handBackSupervisorReplies(ctx, r, at, candidates, text)
	}
}

// supervisorOwnerText is the provenance gate for a message observed on a supervisor
// conversation: a relay, a message-source envelope or a side-conversation wrapper is another
// conversation's input, not the owner answering a card. Empty text (a hook without payload) has
// no owner words to settle with, so it is ignored here rather than treated as an answer.
func supervisorOwnerText(text string) bool {
	if text == "" {
		return false
	}
	if supervisorSideConversationContext.MatchString(text) || supervisorSideConversationMarker.MatchString(text) {
		return false
	}
	if src := prompts.ItemSource(proto.Item{Role: "user", Kind: proto.KindText, Text: text}); src != nil && src.Kind != "" {
		return false
	}
	return prompts.ParseRelaySource(text) == nil
}

// supervisedDecisionCandidates returns the open to-dos that conversation supervisorID
// supervises and whose decide ask is still waiting, as the caller's snapshot reads them. A
// supervisor conversation is never an active worker session, so activeTodoOf cannot find these;
// the Supervision.Supervisor relationship is the reverse index.
func (s *Service) supervisedDecisionCandidates(supervisorID string) []Todo {
	s.todos.mu.RLock()
	todos := make([]Todo, 0, len(s.todos.todos))
	for _, t := range s.todos.todos {
		if t.DoneAt != nil || t.Supervision == nil || t.Supervision.Supervisor != supervisorID ||
			t.Supervision.State != reviewNeedsUser {
			continue
		}
		todos = append(todos, *t)
	}
	s.todos.mu.RUnlock()
	out := todos[:0]
	for _, t := range todos {
		a, ok := s.askCopy(t.ID)
		if !ok || !decisionOpen(&a, t) || !a.RecheckAt.IsZero() || ownerDecisionReturned(t.Supervision) {
			continue
		}
		out = append(out, t)
	}
	return out
}

// supervisorOptionLabel matches a full option label only, never a lone letter. The Agent-chat
// path accepts letters because that session is scoped to one task; the supervisor conversation
// carries every conversation the owner has, so an isolated "A" there usually answers something
// else and must not settle a card irreversibly.
func supervisorOptionLabel(text string, options []DecisionOption) string {
	return matchDecisionOption(text, options, false)
}

// settleSupervisorChoice records a unique exact option label typed in the supervisor
// conversation, sourcing the answer from that conversation rather than the worker Agent.
func (s *Service) settleSupervisorChoice(ctx context.Context, t Todo, at time.Time, r SessionRef, text string) {
	id := t.ID
	s.reviewDismissals.actions.acquire(id)
	defer s.reviewDismissals.actions.release(id)
	cur, ok := s.todoNow(id)
	if !ok || cur.DoneAt != nil || cur.Supervision == nil || cur.Supervision.State != reviewNeedsUser {
		return
	}
	a, ok := s.askCopy(id)
	if !ok || !decisionOpen(&a, cur) || !a.RecheckAt.IsZero() || ownerDecisionReturned(cur.Supervision) ||
		!at.After(a.AskedAt) {
		return
	}
	label := supervisorOptionLabel(text, a.Options)
	if label == "" {
		return
	}
	taken, err := s.decisionToldLocked(ctx, id, label, r.MachineID, r.SessionID, text)
	if err != nil {
		s.logf("task %s: decision: record supervisor reply: %v", id, err)
		return
	}
	if taken {
		// decisionToldLocked's endAsk already asks a change and redraws the card.
		s.askChanged(ctx)
	}
}

// handBackSupervisorReplies pauses each candidate's default and emits a decision_reply signal
// that routes back to this conversation's supervisor digest. No option is inferred: the
// supervisor reads the owner's words and confirms with task_decided or clarifies.
func (s *Service) handBackSupervisorReplies(ctx context.Context, r SessionRef, at time.Time, todos []Todo, text string) {
	for _, t := range todos {
		s.handBackOneSupervisorReply(ctx, r, at, t.ID, text)
	}
}

func (s *Service) handBackOneSupervisorReply(ctx context.Context, r SessionRef, at time.Time, id, text string) {
	s.reviewDismissals.actions.acquire(id)
	defer s.reviewDismissals.actions.release(id)
	cur, ok := s.todoNow(id)
	if !ok || cur.DoneAt != nil || cur.Supervision == nil || cur.Supervision.State != reviewNeedsUser {
		return
	}
	b := &s.accept
	b.mu.Lock()
	if !s.loadAcceptLocked(ctx) {
		b.mu.Unlock()
		return
	}
	a := b.asks[id]
	if a == nil || !decideAsk(a) || a.AskedAt.IsZero() || !a.AcceptedAt.IsZero() ||
		!at.After(a.AskedAt) || acceptAskStale(a, cur) || acceptAskEnded(a, cur) || !a.RecheckAt.IsZero() ||
		ownerDecisionReturned(cur.Supervision) {
		b.mu.Unlock()
		return
	}
	if a.AgentReply != nil { // a prior reply already paused this round; keep the first hand-off
		b.mu.Unlock()
		return
	}
	a.AgentReply = &decisionAgentReply{MachineID: r.MachineID, SessionID: r.SessionID, At: at.UTC(), Text: clip(text, 2000)}
	a.DueAt = at.UTC()
	// Even a failed write must not let this process take the default; the in-memory pause stands and the sweep retries.
	if err := s.saveAcceptErrLocked(ctx); err != nil {
		s.logf("task %s: decision: persist supervisor reply: %v", id, err)
	}
	cp := *a
	b.mu.Unlock()
	s.askChanged(ctx)
	s.signalAgentDecisionReply(id, cp)
	go func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		s.updateAskCards(ctx, id, cp)
	}()
}
