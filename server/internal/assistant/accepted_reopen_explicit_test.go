package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

// #7539, the explicit reopens of an accepted task: whoever may ask it for more (its conversation, its supervisor, the
// background supervisor acting for the main conversation) opens it again with what it sends the task's agent, and that
// holds across a manager restart between the send and the turn it queued. Everyone else's sends, and a send that
// definitely failed, leave it closed.

// longAgent is the session id of the agent a plannedTask runs on: long enough for the agent tools to find it.
const longAgent = "s100001"

// plannedTask is newSupervisedServiceOn with its agent named longAgent (on box), st as its database (nil: none).
func plannedTask(t *testing.T, st Store) (*Service, *fakeHub, Todo) {
	t.Helper()
	s, f := newQuietServiceOn(t, st)
	f.mu.Lock()
	f.judge = `{"verdict":"done","reason":"merged"}`
	f.agents = 100000
	f.mu.Unlock()
	raw, _ := json.Marshal(map[string]any{"tasks": []map[string]any{{
		"title": "Fix the login crash", "prompt": "The login page crashes on an iPad in landscape: fix it and add tests",
		"machine": "box", "cwd": "/code/app", "readonly": true, "why": "the repository is on box",
		"done_when": []string{"PR merged", "light and dark screenshots in the PR"}, "expect_minutes": 30,
	}}})
	if _, err := s.toolTaskPlan(context.Background(), "conv1", raw); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the link", func() bool { return len(onlyTodo(s).Agents) == 1 })
	todo := onlyTodo(s)
	if todo.Agents[0].SessionID != longAgent || todo.PlanID == "" || todo.Supervision == nil || todo.Supervision.Supervisor != "conv1" {
		t.Fatalf("not a plan task of conv1 on %s: %+v %+v", longAgent, todo, todo.Supervision)
	}
	return s, f, todo
}

// longTurn is agentTurn for the agent longAgent.
func longTurn(s *Service, f *fakeHub, status, text string) {
	ctx := context.Background()
	st := proto.StateUnread
	if status == proto.StatusWorking {
		st = proto.StateWorking
	}
	f.setSessions("box", proto.Session{SessionID: longAgent, MachineID: "box", Cwd: "/code/app", Agent: "claude", Status: status,
		State: st, LastText: text, UpdatedAt: time.Now()})
	if status == proto.StatusWorking {
		s.taskWorking(ctx, "box", longAgent)
	} else {
		s.taskTurnEnded(ctx, "box", longAgent)
	}
}

// sendTo sends agent longAgent on box text from conversation from, with tool (agent_send, or agent_interrupt with
// then).
func sendTo(t *testing.T, s *Service, tool, from, text string) error {
	t.Helper()
	args := map[string]any{"machine": "box", "session_id": longAgent, "text": text}
	if tool == "agent_interrupt" {
		args = map[string]any{"machine": "box", "session_id": longAgent, "then": text}
	}
	_, err := runJSON(t, s, from, tool, args)
	return err
}

// The background supervisor accepts the main conversation's plan-started task (its ledger keeps the main conversation
// as the supervisor), then sends its agent more: the task opens again, and the work it asked for is judged and waits
// for its review again. Another conversation's send before it leaves the task closed.
func TestBackgroundSupervisorReopensATaskItAccepted(t *testing.T) {
	for _, tool := range []string{"agent_send", "agent_interrupt"} {
		t.Run(tool, func(t *testing.T) {
			s, f, todo := plannedTask(t, nil)
			s.noBackground = false // on, as by default (newBackgroundService)
			f.mu.Lock()
			f.made = 100
			f.mu.Unlock()
			longTurn(s, f, proto.StatusWorking, "")
			longTurn(s, f, proto.StatusDone, "Fixed it: PR https://github.com/o/r/pull/77 is merged")
			bg := backgroundID(t, s)
			listBackground(t, s, f, bg)
			waitFor(t, "the background supervisor's review message", func() bool { return len(f.noticesTo(bg)) == 1 })
			if _, err := runJSON(t, s, bg, "task_review", map[string]any{"todo_id": todo.ID, "verdict": reviewAccepted,
				"note": "Checked the PR and the screenshots"}); err != nil {
				t.Fatalf("the background supervisor's accept: %v", err)
			}
			settleService(t, s)
			accepted := todoNow(s, todo.ID)
			if accepted.DoneAt == nil || accepted.DoneBy != db.DoneByReview || accepted.Supervision.Supervisor != "conv1" {
				t.Fatalf("not accepted for the main conversation: %+v %+v", accepted, accepted.Supervision)
			}
			waitFor(t, "the clock to pass the acceptance", func() bool { return time.Now().After(*accepted.DoneAt) })

			if err := sendTo(t, s, tool, "conv7", "Also look at the other dashboard"); err != nil {
				t.Fatalf("%s from another conversation: %v", tool, err)
			}
			longTurn(s, f, proto.StatusWorking, "")
			closedUnchanged(t, s, accepted)
			longTurn(s, f, proto.StatusDone, "The other dashboard looks fine")
			closedUnchanged(t, s, accepted)

			told := len(f.noticesTo(bg))
			if err := sendTo(t, s, tool, bg, "Add the landscape case to the tests"); err != nil {
				t.Fatalf("%s from the background supervisor: %v", tool, err)
			}
			longTurn(s, f, proto.StatusWorking, "")
			if got := todoNow(s, todo.ID); got.DoneAt != nil || got.ReopenedAt == nil {
				t.Fatalf("the background supervisor's %s did not reopen it: %+v", tool, got)
			}
			longTurn(s, f, proto.StatusDone, "Landscape covered: PR https://github.com/o/r/pull/78 is merged")
			waitFor(t, "the review again", func() bool { return todoNow(s, todo.ID).Supervision.State == reviewPending })
			waitFor(t, "the background supervisor's next review message", func() bool { return len(f.noticesTo(bg)) > told })
		})
	}
}

// More for an accepted task reaches its live agent (the owner's message on its card, or agent_send from its
// conversation), which only queues it; the manager restarts before the agent takes it up. The turn it starts then is
// the task's: open, followed, judged, waiting for review. A send that definitely failed asked nothing: the task stays
// closed through the restart and the agent's next turn.
func TestMoreForAnAcceptedTaskSurvivesARestart(t *testing.T) {
	for _, c := range []struct {
		name, via string
		fails     bool
	}{
		{"card", "card", false},
		{"agent_send", "agent_send", false},
		{"card, failed", "card", true},
		{"agent_send, failed", "agent_send", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			database := taskRecoveryDatabase(t)
			s, f, todo := plannedTask(t, database)
			longTurn(s, f, proto.StatusWorking, "")
			longTurn(s, f, proto.StatusDone, "Fixed it: PR https://github.com/o/r/pull/77 is merged")
			waitFor(t, "the review message", func() bool { return supervisionOf(s, f, todo.ID).notices == 1 })
			accepted := reviewAndWait(t, s, todo.ID, reviewAccepted)
			waitFor(t, "the clock to pass the acceptance", func() bool { return time.Now().After(*accepted.DoneAt) })

			if c.fails {
				f.mu.Lock()
				f.failSend = errors.New("no such session")
				f.mu.Unlock()
			}
			const more = "Please cover the landscape case too"
			var err error
			if c.via == "card" {
				_, err = s.routeToTask(ctx, "conv1", todo.ID, more)
			} else {
				err = sendTo(t, s, "agent_send", "conv1", more)
			}
			if c.fails != (err != nil) {
				t.Fatalf("the send: %v", err)
			}
			if c.fails {
				closedUnchanged(t, s, accepted)
			}

			// The manager restarts before the agent takes up the text it queued.
			restarted, hub := newQuietServiceOn(t, database)
			hub.judge = `{"verdict":"done","reason":"merged"}`
			longTurn(restarted, hub, proto.StatusWorking, "")
			if c.fails {
				closedUnchanged(t, restarted, accepted)
				return
			}
			if got := todoNow(restarted, todo.ID); got.DoneAt != nil || got.ReopenedAt == nil {
				t.Fatalf("after the restart, the turn its %s queued left the task closed: %+v", c.via, got)
			}
			longTurn(restarted, hub, proto.StatusDone, "Landscape covered: PR https://github.com/o/r/pull/78 is merged")
			waitFor(t, "the review again", func() bool { return todoNow(restarted, todo.ID).Supervision.State == reviewPending })
			waitFor(t, "the review message after the restart", func() bool { return supervisionOf(restarted, hub, todo.ID).notices == 1 })
		})
	}
}
