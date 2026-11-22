package assistant

import (
	"context"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestAccountExhaustionDoesNotAutoContinueByDefault(t *testing.T) {
	for _, errText := range []string{
		"API Error: 503 no other eligible account",
		"API Error: 503 no eligible upstream account",
		"API Error: 503 upstream attempt budget is exhausted",
		"API Error: 503 no eligible upstream",
	} {
		t.Run(errText, func(t *testing.T) {
			s, f := transientService(t)
			f.setSessions("box", apiErrorTurn("s1", errText))
			triage(t, s, "s1")

			if e, ok := s.workEntry([2]string{"box", "s1"}); ok && (e.WakeAt != nil || e.Work.WakeAt != nil) {
				t.Fatalf("account exhaustion scheduled an automatic continuation: %+v", e)
			}
			s.workTick(context.Background(), time.Now().Add(time.Hour))
			if got := f.sentToAgents(); len(got) != 0 {
				t.Fatalf("account exhaustion automatically continued: %v", got)
			}
		})
	}
}

func TestAccountExhaustionRetryBudgetIsOptInAndBounded(t *testing.T) {
	s, f := transientService(t)
	f.hello.Agents.Triage.AccountExhaustionMaxRetries = 2
	const errText = "API Error: 503 no eligible upstream account"
	f.setSessions("box", apiErrorTurn("s1", errText))
	triage(t, s, "s1")

	for continuation := 1; continuation <= 2; continuation++ {
		e := entryOf(t, s, "s1")
		if e.WakeAt == nil || e.Work.On != proto.WaitTransient {
			t.Fatalf("opt-in continuation %d was not scheduled: %+v", continuation, e)
		}
		s.workTick(context.Background(), *e.WakeAt)
		if got := f.sentToAgents(); len(got) != continuation {
			t.Fatalf("after opt-in continuation %d: sent %v", continuation, got)
		}
		failAgain(t, s, f, continuation, errText)
	}

	if e := entryOf(t, s, "s1"); e.WakeAt != nil || e.Work.WakeAt != nil {
		t.Fatalf("account exhaustion exceeded its configured budget: %+v", e)
	}
	s.workTick(context.Background(), time.Now().Add(time.Hour))
	if got := f.sentToAgents(); len(got) != 2 {
		t.Fatalf("bounded opt-in policy sent another continuation: %v", got)
	}
}

func TestAccountExhaustionRetryBudgetCanBeDisabledWhilePending(t *testing.T) {
	s, f := transientService(t)
	f.hello.Agents.Triage.AccountExhaustionMaxRetries = 1
	f.setSessions("box", apiErrorTurn("s1", "API Error: 503 no other eligible account"))
	triage(t, s, "s1")
	e := entryOf(t, s, "s1")
	if e.WakeAt == nil {
		t.Fatalf("opt-in continuation was not scheduled: %+v", e)
	}

	f.hello.Agents.Triage.AccountExhaustionMaxRetries = 0
	s.workTick(context.Background(), *e.WakeAt)
	if got := f.sentToAgents(); len(got) != 0 {
		t.Fatalf("disabled account-exhaustion policy still sent: %v", got)
	}
	if current := entryOf(t, s, "s1"); current.WakeAt != nil || current.Work.WakeAt != nil {
		t.Fatalf("disabled account-exhaustion policy kept its wake: %+v", current)
	}
}

func TestAccountExhaustionCancelsAlreadyQueuedContinuation(t *testing.T) {
	s, f := transientService(t)
	c := apiErrorTurn("s1", "API Error: 503 Service Unavailable")
	f.setSessions("box", c)
	triage(t, s, "s1")
	e := entryOf(t, s, "s1")
	if e.WakeAt == nil || e.Work.On != proto.WaitTransient {
		t.Fatalf("ordinary transient control was not scheduled: %+v", e)
	}

	// A restored session can reveal the concrete cause after an older version
	// already queued the retry. The due send must classify the current error again.
	c.TurnError = "API Error: 503 no eligible upstream account"
	c.LastText = c.TurnError
	f.setSessions("box", c)
	s.workTick(context.Background(), *e.WakeAt)

	if got := f.sentToAgents(); len(got) != 0 {
		t.Fatalf("queued account-exhaustion continuation was sent: %v", got)
	}
	if current := entryOf(t, s, "s1"); current.WakeAt != nil || current.Work.WakeAt != nil {
		t.Fatalf("account-exhaustion wake survived the due recheck: %+v", current)
	}
}

// All Assistant conversations are exempt: main chat, leader and
// background-supervision conversations, side chats, scheduled-task and health
// chats, and meeting rooms. This fixture intentionally has none of the three
// protected roles, so it pins the wider exemption. Task-leader Agents are
// coding Agents and remain subject to the budget.
func TestAssistantAccountExhaustionStillAutoContinues(t *testing.T) {
	s, f := transientService(t)
	c := apiErrorTurn("s1", "API Error: 503 no eligible upstream account")
	c.Assistant = true
	f.setSessions("box", c)
	triage(t, s, "s1")

	e := entryOf(t, s, "s1")
	if e.WakeAt == nil || e.Work.On != proto.WaitTransient {
		t.Fatalf("assistant transient continuation was not scheduled: %+v", e)
	}
	s.workTick(context.Background(), *e.WakeAt)
	if got := f.sentToAgents(); len(got) != 1 || got[0] != "s1: 继续" {
		t.Fatalf("assistant account-exhaustion behavior changed: sent %v", got)
	}
}

func TestOrdinaryTransientErrorsStillAutoContinue(t *testing.T) {
	for _, errText := range []string{
		"API Error: 503 Service Unavailable",
		"API Error: 529 overloaded",
		"API Error: Connection lost mid-response",
		"API Error: 429 Too Many Requests",
	} {
		t.Run(errText, func(t *testing.T) {
			s, f := transientService(t)
			f.setSessions("box", apiErrorTurn("s1", errText))
			triage(t, s, "s1")
			e := entryOf(t, s, "s1")
			if e.WakeAt == nil || e.Work.On != proto.WaitTransient {
				t.Fatalf("ordinary transient error was not scheduled: %+v", e)
			}
			s.workTick(context.Background(), *e.WakeAt)
			if got := f.sentToAgents(); len(got) != 1 || got[0] != "s1: 继续" {
				t.Fatalf("ordinary transient error did not continue exactly once: %v", got)
			}
		})
	}
}
