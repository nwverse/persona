package assistant

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/prompts"
)

// supervisorRef is the direction conversation that supervises the decisionAgentReplyService
// fixture's to-do (newSupervisedService: settings.Main = "conv1" on the assistant machine).
func supervisorRef() SessionRef {
	return SessionRef{MachineID: "server", SessionID: "conv1"}
}

// An exact option label typed in the supervisor / direction conversation settles that
// conversation's own pending decision card, the same way an Agent-chat exact choice does.
func TestSupervisorConversationExactChoiceSettles(t *testing.T) {
	s, f, todo := decisionAgentReplyService(t)
	a := askOf(s, todo.ID)
	// Use the non-recommended option: a recommended label is recorded with an empty Chosen by
	// the existing "OK takes the recommendation" rule, so the second option asserts Chosen too.
	want := a.Options[1].Label
	s.userSpokeText(context.Background(), supervisorRef(), want)
	got := askOf(s, todo.ID)
	if got == nil || got.AcceptedAt.IsZero() || got.Chosen != want || got.ByDefault {
		t.Fatalf("the supervisor reply did not settle the exact option: %+v", got)
	}
	if todoNow(s, todo.ID).Supervision.State == reviewNeedsUser {
		t.Fatal("the decision stayed in needs_user")
	}
	if strings.Contains(strings.Join(pendingKinds(s, todo.ID), ","), sigDecisionReply) ||
		signalled(s, f, todo.ID, sigDecisionReply, "task_decided") {
		t.Fatal("an exact supervisor choice must not emit a hand-back signal")
	}
}

// A lone option letter in the supervisor conversation must not settle the card: that
// conversation is the owner's main chat, where a one-letter message usually answers something
// unrelated. It is treated as free text: the default pauses and the card is handed back.
func TestSupervisorConversationLoneLetterDoesNotSettle(t *testing.T) {
	s, f, todo := decisionAgentReplyService(t)
	s.userSpokeText(context.Background(), supervisorRef(), "B")
	got := askOf(s, todo.ID)
	if got == nil || got.Chosen != "" || !got.AcceptedAt.IsZero() {
		t.Fatalf("a lone letter settled the supervisor card: %+v", got)
	}
	if got.AgentReply == nil || got.DueAt.IsZero() {
		t.Fatalf("a lone letter was ignored instead of handed back: %+v", got)
	}
	if todoNow(s, todo.ID).Supervision.State != reviewNeedsUser {
		t.Fatal("the decision left needs_user on a lone letter")
	}
	waitFor(t, "the lone-letter hand-off", func() bool {
		return signalled(s, f, todo.ID, sigDecisionReply, "task_decided")
	})
	// The full label still settles afterwards.
	s.userSpokeText(context.Background(), supervisorRef(), askOf(s, todo.ID).Options[1].Label)
	if want := askOf(s, todo.ID).Options[1].Label; askOf(s, todo.ID).Chosen != want {
		t.Fatalf("the full label after the lone letter did not settle %q", want)
	}
}

// Free text in the supervisor conversation pauses the default and hands the decision back to
// that conversation's supervisor via decision_reply, without guessing an option.
func TestSupervisorConversationFreeTextHandsBack(t *testing.T) {
	s, f, todo := decisionAgentReplyService(t)
	s.userSpokeText(context.Background(), supervisorRef(), "let me think about the rollout risk first")
	got := askOf(s, todo.ID)
	if got == nil || got.AgentReply == nil || got.DueAt.IsZero() || got.Chosen != "" || !got.AcceptedAt.IsZero() {
		t.Fatalf("free text was guessed or settled: %+v", got)
	}
	if todoNow(s, todo.ID).Supervision.State != reviewNeedsUser {
		t.Fatal("free text must keep the decision in needs_user")
	}
	waitFor(t, "the supervisor hand-off", func() bool {
		return signalled(s, f, todo.ID, sigDecisionReply, "task_decided")
	})
}

// A provenance-marked message reaching the supervisor conversation is not the owner answering:
// it neither settles nor withdraws the pending card.
func TestSupervisorConversationProvenanceIgnored(t *testing.T) {
	s, f, todo := decisionAgentReplyService(t)
	before := askOf(s, todo.ID)
	s.userSpokeText(context.Background(), supervisorRef(),
		prompts.RenderSideConversation("an unrelated request from another conversation"))
	got := askOf(s, todo.ID)
	if got == nil || got.AgentReply != nil || !got.DueAt.Equal(before.DueAt) || got.Chosen != "" {
		t.Fatalf("a side conversation changed the supervisor's pending decision: %+v", got)
	}
	if signalled(s, f, todo.ID, sigDecisionReply, "task_decided") {
		t.Fatal("a side conversation emitted a decision_reply signal")
	}
	if todoNow(s, todo.ID).Supervision.State != reviewNeedsUser {
		t.Fatal("the pending decision left needs_user")
	}
	// A genuine owner message right after still settles the card.
	s.userSpokeText(context.Background(), supervisorRef(), askOf(s, todo.ID).Options[0].Label)
	if askOf(s, todo.ID).AcceptedAt.IsZero() {
		t.Fatal("the genuine owner reply after provenance input did not settle")
	}
}

// A text-less spoke (a hook with no payload) cannot answer a supervisor card: there are no
// owner words to match or hand back, so the decision is left untouched.
func TestSupervisorConversationEmptyTextIgnored(t *testing.T) {
	s, f, todo := decisionAgentReplyService(t)
	before := askOf(s, todo.ID)
	s.userSpokeText(context.Background(), supervisorRef(), "")
	got := askOf(s, todo.ID)
	if got == nil || got.AgentReply != nil || !got.DueAt.Equal(before.DueAt) {
		t.Fatalf("an empty spoke changed the supervisor decision: %+v", got)
	}
	if signalled(s, f, todo.ID, sigDecisionReply, "task_decided") {
		t.Fatal("an empty spoke emitted a decision_reply signal")
	}
	// Guard against a flaky timing assumption: give any async hand-off a moment, then recheck.
	time.Sleep(4 * shortSuperviseTimings.gather)
	if askOf(s, todo.ID).AgentReply != nil {
		t.Fatal("an empty spoke paused the decision asynchronously")
	}
}

// addSecondSupervisedTodo puts another needs_user to-do supervised by conv1 with its own decide
// ask next to the fixture's to-do, so one message faces two open cards at once.
func addSecondSupervisedTodo(t *testing.T, s *Service) Todo {
	t.Helper()
	now := time.Now().UTC()
	second := Todo{
		ID: "todo_supervisor_second", Title: "Choose the rollback plan", Origin: "conv1",
		Supervision: &Supervision{Supervisor: "conv1", State: reviewNeedsUser, Round: 1},
		Agents:      []TodoAgent{},
	}
	putTodo(s, second)
	s.accept.mu.Lock()
	defer s.accept.mu.Unlock()
	if !s.accept.loaded {
		t.Fatal("accept book not loaded")
	}
	s.accept.asks[second.ID] = &acceptAsk{
		Conclusion: true, Outcome: outcomeDecide, Reason: "rollback strategy",
		Recommend: "snapshot", AskedAt: now, DueAt: now.Add(time.Hour),
		Options: []DecisionOption{
			{Label: "snapshot", LeadsTo: "restore a snapshot"},
			{Label: "redeploy", LeadsTo: "ship the previous build"},
		},
	}
	return second
}

// When the same supervisor conversation holds several open cards, even a label that exactly
// matches one card is not applied silently: every open card is handed back for an explicit,
// unambiguous choice, and none is settled.
func TestSupervisorConversationMultipleCardsNotSilentlySettled(t *testing.T) {
	s, f, todo := decisionAgentReplyService(t)
	second := addSecondSupervisedTodo(t, s)
	first := askOf(s, todo.ID)
	// The message exactly names the first card's non-recommended option.
	msg := first.Options[1].Label
	s.userSpokeText(context.Background(), supervisorRef(), msg)
	for _, id := range []string{todo.ID, second.ID} {
		got := askOf(s, id)
		if got == nil || got.AgentReply == nil || !got.AcceptedAt.IsZero() || got.Chosen != "" {
			t.Fatalf("card %s was silently settled instead of handed back: %+v", id, got)
		}
		if todoNow(s, id).Supervision.State != reviewNeedsUser {
			t.Fatalf("card %s left needs_user", id)
		}
	}
	waitFor(t, "both hand-offs", func() bool {
		return signalled(s, f, todo.ID, sigDecisionReply, "task_decided") &&
			signalled(s, f, second.ID, sigDecisionReply, "task_decided")
	})
}
