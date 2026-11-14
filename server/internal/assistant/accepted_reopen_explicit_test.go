package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

// #7539, the explicit reopens of an accepted task: whoever may ask it for more (its conversation, its supervisor, the
// background supervisor acting for the main conversation) opens it again with what it sends the task's agent, and that
// holds across a manager restart between the send and the turn it queued. Everyone else's sends, a bare /exit or /quit,
// a card message for another task, and a send that definitely failed (even when a turn of the agent started while it
// was out) leave it closed.

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

// sendVia sends agent longAgent text from conversation from: with agent_send, agent_interrupt's then, or as the
// owner's message on task id's card ("card").
func sendVia(t *testing.T, s *Service, via, from, id, text string) error {
	t.Helper()
	if via == "card" {
		_, err := s.routeToTask(context.Background(), from, id, text)
		return err
	}
	return sendTo(t, s, via, from, text)
}

// acceptedPlanTask is plannedTask (st its database, nil: none) with its agent's work done and accepted by conv1, the
// clock past the acceptance.
func acceptedPlanTask(t *testing.T, st Store) (*Service, *fakeHub, Todo) {
	t.Helper()
	s, f, todo := plannedTask(t, st)
	longTurn(s, f, proto.StatusWorking, "")
	longTurn(s, f, proto.StatusDone, "Fixed it: PR https://github.com/o/r/pull/77 is merged")
	waitFor(t, "the review message", func() bool { return supervisionOf(s, f, todo.ID).notices == 1 })
	accepted := reviewAndWait(t, s, todo.ID, reviewAccepted)
	waitFor(t, "the clock to pass the acceptance", func() bool { return time.Now().After(*accepted.DoneAt) })
	return s, f, accepted
}

// acceptedByBackground is plannedTask with the background supervisor on (as by default), its agent's work done and
// accepted by the background supervisor bg for the main conversation (the ledger still names conv1), the clock past
// the acceptance.
func acceptedByBackground(t *testing.T) (s *Service, f *fakeHub, accepted Todo, bg string) {
	t.Helper()
	s, f, todo := plannedTask(t, nil)
	s.noBackground = false // on, as by default (newBackgroundService)
	f.mu.Lock()
	f.made = 100
	f.mu.Unlock()
	longTurn(s, f, proto.StatusWorking, "")
	longTurn(s, f, proto.StatusDone, "Fixed it: PR https://github.com/o/r/pull/77 is merged")
	bg = backgroundID(t, s)
	listBackground(t, s, f, bg)
	waitFor(t, "the background supervisor's review message", func() bool { return len(f.noticesTo(bg)) == 1 })
	if _, err := runJSON(t, s, bg, "task_review", map[string]any{"todo_id": todo.ID, "verdict": reviewAccepted,
		"note": "Checked the PR and the screenshots"}); err != nil {
		t.Fatalf("the background supervisor's accept: %v", err)
	}
	settleService(t, s)
	accepted = todoNow(s, todo.ID)
	if accepted.DoneAt == nil || accepted.DoneBy != db.DoneByReview || accepted.Supervision.Supervisor != "conv1" {
		t.Fatalf("not accepted for the main conversation: %+v %+v", accepted, accepted.Supervision)
	}
	waitFor(t, "the clock to pass the acceptance", func() bool { return time.Now().After(*accepted.DoneAt) })
	return s, f, accepted, bg
}

// sendHookHub is a fakeHub that runs onSend, once, when the manager calls session.send, before the node answers (what
// happens on the agent's computer while the send is out), and afterList, once, after box's session list was read.
type sendHookHub struct {
	*fakeHub
	mu                sync.Mutex
	onSend, afterList func()
}

func (h *sendHookHub) take(hook *func()) func() {
	h.mu.Lock()
	defer h.mu.Unlock()
	fn := *hook
	*hook = nil
	return fn
}

func (h *sendHookHub) DeviceCall(ctx context.Context, p DeviceCall) (json.RawMessage, error) {
	if p.Method == DeviceSessionSend {
		if fn := h.take(&h.onSend); fn != nil {
			fn()
		}
	}
	out, err := h.fakeHub.DeviceCall(ctx, p)
	if p.Method == DeviceSessionsList && p.Device == "box" {
		if fn := h.take(&h.afterList); fn != nil {
			fn()
		}
	}
	return out, err
}

// The background supervisor accepts the main conversation's plan-started task (its ledger keeps the main conversation
// as the supervisor), then sends its agent more: the task opens again, and the work it asked for is judged and waits
// for its review again. Another conversation's send before it leaves the task closed.
func TestBackgroundSupervisorReopensATaskItAccepted(t *testing.T) {
	for _, tool := range []string{"agent_send", "agent_interrupt"} {
		t.Run(tool, func(t *testing.T) {
			s, f, todo, bg := acceptedByBackground(t)
			accepted := todo

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
// closed through the restart and the agent's next turn, also when a turn of the agent started (other work) while that
// send was out and ends after the restart.
func TestMoreForAnAcceptedTaskSurvivesARestart(t *testing.T) {
	for _, c := range []struct {
		name, via string
		fails     bool
		// meanwhile: a turn of the agent starts while the send is out
		meanwhile bool
	}{
		{"card", "card", false, false},
		{"agent_send", "agent_send", false, false},
		{"card, failed", "card", true, false},
		{"agent_send, failed", "agent_send", true, false},
		{"card, failed while a turn started", "card", true, true},
		{"agent_send, failed while a turn started", "agent_send", true, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			database := taskRecoveryDatabase(t)
			s, f, accepted := acceptedPlanTask(t, database)
			if c.fails {
				f.mu.Lock()
				f.failSend = errors.New("no such session")
				f.mu.Unlock()
			}
			if c.meanwhile {
				setHub(s, &sendHookHub{fakeHub: f, onSend: func() { longTurn(s, f, proto.StatusWorking, "") }})
			}
			if err := sendVia(t, s, c.via, "conv1", accepted.ID, "Please cover the landscape case too"); c.fails != (err != nil) {
				t.Fatalf("the send: %v", err)
			}
			if c.fails {
				closedUnchanged(t, s, accepted)
			}

			// The manager restarts before the agent takes up the text it queued.
			restarted, hub := newQuietServiceOn(t, database)
			hub.judge = `{"verdict":"done","reason":"merged"}`
			if c.meanwhile { // the turn that started while the send was out ends
				longTurn(restarted, hub, proto.StatusDone, "The other dashboard looks fine")
				closedUnchanged(t, restarted, accepted)
			}
			longTurn(restarted, hub, proto.StatusWorking, "")
			if c.fails {
				closedUnchanged(t, restarted, accepted)
				return
			}
			if got := todoNow(restarted, accepted.ID); got.DoneAt != nil || got.ReopenedAt == nil {
				t.Fatalf("after the restart, the turn its %s queued left the task closed: %+v", c.via, got)
			}
			longTurn(restarted, hub, proto.StatusDone, "Landscape covered: PR https://github.com/o/r/pull/78 is merged")
			waitFor(t, "the review again", func() bool { return todoNow(restarted, accepted.ID).Supervision.State == reviewPending })
			waitFor(t, "the review message after the restart", func() bool { return supervisionOf(restarted, hub, accepted.ID).notices == 1 })
		})
	}
}

// A turn of the agent starts while a send of more for its accepted task is out (its own wakeup, say, or the send's
// own turn coming in before the node's answer). The turn waits for that answer: when the send definitely failed, it
// was other work, and the task stays closed through it and its end, with nothing judged, receipted or told. When the
// node took the send, the task opens and the turn is the task's: its done end waits for review, also when that end
// came before the answer.
func TestATurnDuringASendWaitsForItsAnswer(t *testing.T) {
	for _, via := range []string{"agent_send", "agent_interrupt", "card"} {
		for _, outcome := range []string{"failed", "sent", "sent, ended first"} {
			t.Run(via+", "+outcome, func(t *testing.T) {
				s, f, accepted := acceptedPlanTask(t, nil)
				before, judged, receipted := supervisionOf(s, f, accepted.ID), judgeCalls(f), receiptCount(t, s)
				if outcome == "failed" {
					f.mu.Lock()
					f.failSend = errors.New("the input box holds a draft; nothing was typed")
					f.mu.Unlock()
				}
				setHub(s, &sendHookHub{fakeHub: f, onSend: func() {
					longTurn(s, f, proto.StatusWorking, "")
					if got := todoNow(s, accepted.ID); got.DoneAt == nil {
						t.Errorf("reopened by a turn before the send's answer: %+v", got)
					}
					if outcome == "sent, ended first" {
						longTurn(s, f, proto.StatusDone, "Landscape covered: PR https://github.com/o/r/pull/78 is merged")
					}
				}})
				err := sendVia(t, s, via, "conv1", accepted.ID, "Please cover the landscape case too")
				if outcome == "failed" {
					if err == nil {
						t.Fatal("the send did not fail")
					}
					closedUnchanged(t, s, accepted)
					longTurn(s, f, proto.StatusDone, "The other dashboard looks fine")
					closedUnchanged(t, s, accepted)
					if n := judgeCalls(f); n != judged {
						t.Fatalf("the turn was judged: %d judgments, %d before", n, judged)
					}
					if n := receiptCount(t, s); n != receipted {
						t.Fatalf("the turn got a receipt: %d, %d before", n, receipted)
					}
					nothingToldAfter(t, s, f, accepted.ID, before)
					return
				}
				if err != nil {
					t.Fatalf("the send: %v", err)
				}
				if got := todoNow(s, accepted.ID); got.DoneAt != nil || got.ReopenedAt == nil {
					t.Fatalf("the send was taken, and the task stayed closed: %+v", got)
				}
				if outcome == "sent" {
					longTurn(s, f, proto.StatusDone, "Landscape covered: PR https://github.com/o/r/pull/78 is merged")
				}
				waitFor(t, "the review again", func() bool { return todoNow(s, accepted.ID).Supervision.State == reviewPending })
				waitFor(t, "the next review message", func() bool { return supervisionOf(s, f, accepted.ID).notices > before.notices })
			})
		}
	}
}

// A bare /exit or /quit closes the agent (the node runs it as the agents' own quit command): it asks the accepted
// task for nothing, from its conversation as from the background supervisor, with agent_send, agent_interrupt's then
// or the task's card, and leaves the task closed, also for a turn of the agent after it.
func TestAQuitCommandLeavesAnAcceptedTaskClosed(t *testing.T) {
	for _, c := range []struct{ from, via, text string }{
		{"conv1", "agent_send", "/exit"},
		{"conv1", "agent_interrupt", "/quit"},
		{"conv1", "card", " /exit "},
		{"background", "agent_send", "/quit"},
		{"background", "agent_interrupt", "/exit"},
	} {
		t.Run(c.from+", "+c.via+" "+c.text, func(t *testing.T) {
			s, f, accepted, bg := acceptedByBackground(t)
			from := c.from
			if from == "background" {
				from = bg
			}
			if err := sendVia(t, s, c.via, from, accepted.ID, c.text); err != nil {
				t.Fatalf("the send: %v", err)
			}
			closedUnchanged(t, s, accepted)
			longTurn(s, f, proto.StatusWorking, "")
			closedUnchanged(t, s, accepted)
		})
	}
}

// A message on an accepted task's card reopens that task only. Its agent is handed to another task of the same
// conversation, accepted too, while the card's message looks its agents up: the message goes to the agent as before,
// and neither task opens again, not even on the agent's next turn.
func TestACardMessageReopensOnlyItsOwnTask(t *testing.T) {
	ctx := context.Background()
	s, f, accepted := acceptedPlanTask(t, nil)
	now := time.Now()
	putTodo(s, Todo{ID: "todo_b", Title: "Tidy the release notes", Origin: "conv1", CreatedAt: now, UpdatedAt: now,
		Supervision: &Supervision{Supervisor: "conv1", State: reviewAccepted, Reviews: []TaskReview{{At: now, Verdict: reviewAccepted}}}})
	var other Todo
	setHub(s, &sendHookHub{fakeHub: f, afterList: func() { // the agents of the card's task were just read
		if err := s.linkTodoAgent(ctx, "todo_b", "box", longAgent, "", "/code/app", "claude"); err != nil {
			t.Errorf("hand the agent to the other task: %v", err)
			return
		}
		done := time.Now()
		s.applyTodoRow(ctx, "todo_b", db.TodoRow{ID: "todo_b", Title: "Tidy the release notes", Origin: "conv1", CreatedAt: now,
			UpdatedAt: done, DoneAt: &done, DoneBy: db.DoneByReview}, nil)
		other = todoNow(s, "todo_b")
	}})
	if _, err := s.routeToTask(ctx, "conv1", accepted.ID, "Please cover the landscape case too"); err != nil {
		t.Fatalf("the card message: %v", err)
	}
	if other.ID == "" || other.DoneAt == nil || other.DoneBy != db.DoneByReview {
		t.Fatalf("the other task did not take the agent and close: %+v", other)
	}
	if t2, ok := s.closedTaskOf(ctx, "box", longAgent); !ok || t2.ID != other.ID {
		t.Fatalf("the agent is not the other task's: %+v %v", t2, ok)
	}
	closedUnchanged(t, s, other)
	if got := todoNow(s, accepted.ID); got.DoneAt == nil {
		t.Fatalf("the card's task opened again without its agent: %+v", got)
	}
	longTurn(s, f, proto.StatusWorking, "")
	closedUnchanged(t, s, other)
}
