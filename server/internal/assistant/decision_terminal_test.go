package assistant

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// Terminal recovery on a decision (#13087, decision_terminal.go).

const legacyTerminalOption = "Resume the Agent on its terminal page"

func manualResumeRefused(err error) bool {
	var m *i18n.Message
	return errors.As(err, &m) && m.Key == "assistant-reports.manual_terminal_resume"
}

func settingPuts(st *memStore) int {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.puts
}

// The terminal link comes from the hub's own refusal to resume an Agent of the task, never from a reason's wording: a
// reason that mentions a terminal or says not to resume one links nothing, and a refusal links only the Agent refused.
func TestDecisionLinksTerminalOnlyAfterRefusedResume(t *testing.T) {
	raise := func(t *testing.T, s *Service, f *fakeHub, todo Todo, reason string) (acceptAsk, todoAsk) {
		t.Helper()
		waitsForReview(t, s, f, todo.ID, "The owner can choose the next step.")
		args := rolloutDecision()
		args["reason"] = reason
		if _, err := reviewErr(s, todo.ID, args); err != nil {
			t.Fatal(err)
		}
		a, ok := s.askCopy(todo.ID)
		if !ok || !decideAsk(&a) {
			t.Fatalf("no decision: %+v", a)
		}
		return a, s.todoAsks(context.Background())[todo.ID]
	}
	for _, reason := range []string{
		"Do not resume it on the terminal; start a replacement instead.",
		"The terminal output shows the build, and resuming later is fine.",
	} {
		t.Run("mention "+reason, func(t *testing.T) {
			s, f, todo := newSupervisedService(t)
			a, row := raise(t, s, f, todo, reason)
			if a.ResumeTerminal != nil || row.Terminal != nil {
				t.Fatalf("a reason's wording linked a terminal: %+v / %+v", a.ResumeTerminal, row.Terminal)
			}
		})
	}
	t.Run("refused", func(t *testing.T) {
		s, f, todo := newSupervisedService(t)
		ag := todo.Agents[0]
		stopped := proto.Session{MachineID: ag.MachineID, SessionID: ag.SessionID,
			Capabilities: &proto.SessionCapabilities{Read: true, Terminal: true, StopCause: proto.StopUser}}
		if err := s.resumeAllowed(context.Background(), "conv1", Device{ID: ag.MachineID}, stopped, "its context", nil); err == nil {
			t.Fatal("the assistant may resume a user stop")
		}
		a, row := raise(t, s, f, todo, "Choose how this task goes on.")
		want := &SessionRef{MachineID: ag.MachineID, SessionID: ag.SessionID}
		if !reflect.DeepEqual(a.ResumeTerminal, want) || !reflect.DeepEqual(row.Terminal, want) || row.TerminalOption != "" {
			t.Fatalf("refused Agent not linked: %+v / %+v %q", a.ResumeTerminal, row.Terminal, row.TerminalOption)
		}
		if s.manualResumeTarget(todoNow(s, todo.ID)) != nil {
			t.Fatal("one refusal linked a second decision")
		}
	})
}

// A refusal links a decision only for an Agent still on the task that it reached after joining it, and only when it is
// the one such Agent.
func TestManualResumeTargetFollowsTheTasksAgents(t *testing.T) {
	s := &Service{}
	joined := time.Now().Add(-time.Hour)
	one := TodoAgent{MachineID: "box", SessionID: "s1", LinkedAt: joined}
	two := TodoAgent{MachineID: "box", SessionID: "s2", LinkedAt: joined}
	if s.manualResumeTarget(Todo{Agents: []TodoAgent{one}}) != nil {
		t.Fatal("linked without a refusal")
	}
	s.noteManualResume("box", "s1")
	late := one
	late.LinkedAt = time.Now().Add(time.Minute)
	if s.manualResumeTarget(Todo{Agents: []TodoAgent{late}}) != nil {
		t.Fatal("a refusal from before the Agent joined linked it")
	}
	released := one
	released.ReleasedAt = &joined
	if s.manualResumeTarget(Todo{Agents: []TodoAgent{released}}) != nil {
		t.Fatal("released Agent linked")
	}
	s.noteManualResume("box", "s2")
	if s.manualResumeTarget(Todo{Agents: []TodoAgent{one, two}}) != nil {
		t.Fatal("two refused Agents: the link would not say which one the decision is about")
	}
	if got := s.manualResumeTarget(Todo{Agents: []TodoAgent{one}}); got == nil || got.SessionID != "s1" {
		t.Fatalf("refused Agent: %+v", got)
	}
	s.manualResumes.refused[[2]string{"box", "s2"}] = time.Now().Add(-manualResumeKeep - time.Minute)
	if s.manualResumeTarget(Todo{Agents: []TodoAgent{two}}) != nil {
		t.Fatal("a refusal older than manualResumeKeep linked")
	}
}

// Past its deadline, an older reversible decision that recommends the owner's terminal Resume has no default the hub
// can take: it is marked handled and reminded once, not refused and reminded again on every sweep.
func TestLegacyTerminalResumeDecisionRemindedOnceAtDeadline(t *testing.T) {
	at := time.Now().UTC().Add(-time.Hour)
	e := dismissalEnv(t, &memStore{}, at, true)
	e.s.todos.todos["task"].Origin = "conv"
	a := e.s.accept.asks["task"]
	a.Recommend, a.Options[0].Label = legacyTerminalOption, legacyTerminalOption
	for range 3 {
		e.s.sweepTaskDeadlines(time.Now())
		if cur, _ := e.s.askCopy("task"); cur.DueAt.IsZero() || !cur.AcceptedAt.IsZero() || cur.ByDefault {
			t.Fatalf("deadline not handled as a reminder: %+v", cur)
		}
	}
	if got := len(e.f.pushed()); got != 1 {
		t.Fatalf("%d decision pushes for 3 sweeps, want 1", got)
	}
}

// The owner's press on an older decision's terminal Resume (its OK, or the option itself) is refused before any answer
// is prepared or saved: the decision, its Don't handle and the stored settings stay exactly as they were.
func TestLegacyTerminalResumePressRefusedBeforeAnyWrite(t *testing.T) {
	for _, recommended := range []bool{true, false} {
		t.Run(map[bool]string{true: "recommended", false: "option"}[recommended], func(t *testing.T) {
			at := time.Now().UTC().Add(-time.Hour)
			st := &memStore{}
			e := dismissalEnv(t, st, at, true)
			a := e.s.accept.asks["task"]
			a.Options[1].Label = legacyTerminalOption
			if recommended {
				a.Recommend = legacyTerminalOption
				a.Options[0], a.Options[1] = a.Options[1], a.Options[0]
			}
			dismissed := dismissReview(t, e, at)
			before, _ := e.s.askCopy("task")
			puts, calls := settingPuts(st), e.f.callCount()
			if recommended {
				if _, err := e.s.acceptTry(context.Background(), "task", ""); !manualResumeRefused(err) {
					t.Fatalf("OK on a terminal Resume: %v", err)
				}
			}
			if _, err := e.s.chooseOption(context.Background(), "task", before.AskedAt.Format(timeKeyLayout), legacyTerminalOption); !manualResumeRefused(err) {
				t.Fatalf("terminal Resume chosen: %v", err)
			}
			if got := settingPuts(st); got != puts {
				t.Fatalf("a refused press wrote %d settings", got-puts)
			}
			if after, _ := e.s.askCopy("task"); !reflect.DeepEqual(before, after) {
				t.Fatalf("a refused press changed the decision: %+v", after)
			}
			if e.f.sends != nil || e.f.callCount() != calls {
				t.Fatal("a refused press reached a device")
			}
			list := []listedTodo{{Todo: todoNow(e.s, "task")}}
			if err := e.s.listDecisionReviews(context.Background(), list); err != nil || list[0].DecisionReview == nil ||
				list[0].DecisionReview.DismissalID != dismissed.DismissalID {
				t.Fatalf("a refused press undid Don't handle: %+v %v", list[0].DecisionReview, err)
			}
		})
	}
}
