package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// #7539, the explicit reopens of an accepted task: whoever may ask it for more (its conversation, its supervisor, the
// background supervisor acting for the main conversation) opens it again with what it sends the task's agent, and that
// holds across a manager restart between the send and the turn it queued, also one before the send's answer came back.
// Everyone else's sends (the background supervisor's for a task a side conversation supervises included), a bare /exit
// or /quit, a card message for another task, a send that definitely failed or never went out (even when a turn of the
// agent started while it was out), one whose answer never came with no turn of the agent soon after, and a card
// message whose new agent did not start leave it closed.

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

// longRow lists the agent longAgent on box with status and its last words text, as its node does.
func longRow(f *fakeHub, status, text string) {
	st := proto.StateUnread
	if status == proto.StatusWorking {
		st = proto.StateWorking
	}
	f.setSessions("box", proto.Session{SessionID: longAgent, MachineID: "box", Cwd: "/code/app", Agent: "claude", Status: status,
		State: st, LastText: text, UpdatedAt: time.Now()})
}

// longTurn is agentTurn for the agent longAgent.
func longTurn(s *Service, f *fakeHub, status, text string) {
	ctx := context.Background()
	longRow(f, status, text)
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

// beforeSend has f run fn, once, when the manager next calls session.send, before the node answers.
func beforeSend(f *fakeHub, fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.beforeSend = fn
}

// failSends has every session.send f gets next fail with err (nil: answered).
func failSends(f *fakeHub, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failSend = err
}

// shortSends makes a live send wait at most wait, and a send of more for an accepted task count grace past its own
// wait, for this test.
func shortSends(t *testing.T, wait, grace time.Duration) {
	t.Helper()
	oldWait, oldGrace := liveSendWait, sendGrace
	liveSendWait, sendGrace = wait, grace
	t.Cleanup(func() { liveSendWait, sendGrace = oldWait, oldGrace })
}

// clockPast waits until the clock is past at.
func clockPast(t *testing.T, at time.Time) {
	t.Helper()
	waitFor(t, "the clock to pass "+at.Format(time.RFC3339Nano), func() bool { return time.Now().After(at) })
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
				beforeSend(f, func() { longTurn(s, f, proto.StatusWorking, "") })
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
// own turn coming in before the node's answer). The turn waits for that answer. When the send definitely failed (the
// node refused it, or it never went out: cancelled, or its wait ran out while the node refused it as restarting), it
// was other work, and the task stays closed through it and its end, with nothing judged, receipted or told. When the
// node took the send, or its answer never came, the task opens and the turn is the task's: its done end waits for
// review, also when that end came before the answer.
func TestATurnDuringASendWaitsForItsAnswer(t *testing.T) {
	outcomes := []struct {
		name string
		err  error // the send's (nil: taken)
		// failed: the send definitely asked for nothing; ended: the turn's end comes before the answer
		failed, ended bool
	}{
		{"failed", errors.New("the input box holds a draft; nothing was typed"), true, false},
		{"never went out, cancelled", rpc.NotSent(context.Canceled), true, false},
		{"never went out, its wait ran out", rpc.NotSent(context.DeadlineExceeded), true, false},
		{"no answer", context.DeadlineExceeded, false, false},
		{"no answer, ended first", context.DeadlineExceeded, false, true},
		{"sent", nil, false, false},
		{"sent, ended first", nil, false, true},
	}
	for _, via := range []string{"agent_send", "agent_interrupt", "card"} {
		for _, o := range outcomes {
			t.Run(via+", "+o.name, func(t *testing.T) {
				s, f, accepted := acceptedPlanTask(t, nil)
				before, judged, receipted := supervisionOf(s, f, accepted.ID), judgeCalls(f), receiptCount(t, s)
				failSends(f, o.err)
				beforeSend(f, func() {
					longTurn(s, f, proto.StatusWorking, "")
					if got := todoNow(s, accepted.ID); got.DoneAt == nil {
						t.Errorf("reopened by a turn before the send's answer: %+v", got)
					}
					if o.ended {
						longTurn(s, f, proto.StatusDone, "Landscape covered: PR https://github.com/o/r/pull/78 is merged")
					}
				})
				err := sendVia(t, s, via, "conv1", accepted.ID, "Please cover the landscape case too")
				if (err != nil) != (o.err != nil) {
					t.Fatalf("the send: %v, want %v", err, o.err)
				}
				if o.failed {
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
				if got := todoNow(s, accepted.ID); got.DoneAt != nil || got.ReopenedAt == nil {
					t.Fatalf("the send was taken or may have been, its turn started, and the task stayed closed: %+v", got)
				}
				if !o.ended {
					longTurn(s, f, proto.StatusDone, "Landscape covered: PR https://github.com/o/r/pull/78 is merged")
				}
				waitFor(t, "the review again", func() bool { return todoNow(s, accepted.ID).Supervision.State == reviewPending })
				waitFor(t, "the next review message", func() bool { return supervisionOf(s, f, accepted.ID).notices > before.notices })
			})
		}
	}
}

// A send of more whose answer never came (the wait ran out, the link dropped) may still reach the agent. The accepted
// task stays closed until a turn of the agent starts while the send still counts (its own wait and a grace after it):
// that turn opens the task and is the task's. When no turn starts by then, the text never arrived as far as the task
// goes: a later turn leaves it closed, and its supervisor hears nothing.
func TestAnUnknownSendReopensOnlyForATurnSoonAfter(t *testing.T) {
	for _, via := range []string{"agent_send", "card"} {
		for _, late := range []bool{false, true} {
			name := via + ", its turn starts soon after"
			if late {
				name = via + ", no turn before it stops counting"
			}
			t.Run(name, func(t *testing.T) {
				if late {
					shortSends(t, 200*time.Millisecond, 100*time.Millisecond)
				}
				s, f, accepted := acceptedPlanTask(t, nil)
				before := supervisionOf(s, f, accepted.ID)
				failSends(f, context.DeadlineExceeded)
				if err := sendVia(t, s, via, "conv1", accepted.ID, "Please cover the landscape case too"); err == nil {
					t.Fatal("the send's answer came")
				}
				returned := time.Now()
				closedUnchanged(t, s, accepted) // nothing arrived yet
				if late {
					clockPast(t, returned.Add(liveSendWait+sendGrace))
					longTurn(s, f, proto.StatusWorking, "")
					closedUnchanged(t, s, accepted)
					longTurn(s, f, proto.StatusDone, "The other dashboard looks fine")
					closedUnchanged(t, s, accepted)
					nothingToldAfter(t, s, f, accepted.ID, before)
					return
				}
				longTurn(s, f, proto.StatusWorking, "")
				if got := todoNow(s, accepted.ID); got.DoneAt != nil || got.ReopenedAt == nil {
					t.Fatalf("the turn soon after a send with no answer left the task closed: %+v", got)
				}
				longTurn(s, f, proto.StatusDone, "Landscape covered: PR https://github.com/o/r/pull/78 is merged")
				waitFor(t, "the review again", func() bool { return todoNow(s, accepted.ID).Supervision.State == reviewPending })
				waitFor(t, "the next review message", func() bool { return supervisionOf(s, f, accepted.ID).notices > before.notices })
			})
		}
	}
}

// A live agent's node never answers agent_send while its link stays up (a runtime's tool call has no deadline of its
// own). The send still ends with its own wait, as one whose answer never came, and a turn of the agent held for that
// answer meanwhile is the task's: the task opens, and the turn's done end waits for review, whether it came before the
// wait ran out or after.
func TestALiveSendNeverAnsweredEndsWithItsWait(t *testing.T) {
	for _, ended := range []bool{false, true} {
		name := "the turn ends after the wait"
		if ended {
			name = "the turn ends during the wait"
		}
		t.Run(name, func(t *testing.T) {
			shortSends(t, 300*time.Millisecond, time.Minute)
			s, f, accepted := acceptedPlanTask(t, nil)
			before := supervisionOf(s, f, accepted.ID)
			f.mu.Lock()
			f.sendHangs = true
			f.mu.Unlock()
			beforeSend(f, func() {
				longTurn(s, f, proto.StatusWorking, "")
				if ended {
					longTurn(s, f, proto.StatusDone, "Landscape covered: PR https://github.com/o/r/pull/78 is merged")
				}
			})
			done := make(chan error, 1)
			go func() { done <- sendTo(t, s, "agent_send", "conv1", "Please cover the landscape case too") }()
			select {
			case err := <-done:
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("the send no node answered: %v, want its wait run out", err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("the send no node answered never ended")
			}
			if got := todoNow(s, accepted.ID); got.DoneAt != nil || got.ReopenedAt == nil {
				t.Fatalf("the turn held for the send left the task closed: %+v", got)
			}
			if !ended {
				longTurn(s, f, proto.StatusDone, "Landscape covered: PR https://github.com/o/r/pull/78 is merged")
			}
			waitFor(t, "the review again", func() bool { return todoNow(s, accepted.ID).Supervision.State == reviewPending })
			waitFor(t, "the next review message", func() bool { return supervisionOf(s, f, accepted.ID).notices > before.notices })
		})
	}
}

// Two sends to the agent overlap: a turn of the agent starts while the first, of more for the accepted task, is out,
// and is held for its answer; a second send is made before the turn ends (another conversation's, or more for the task
// again); the turn ends before either answer. The second send does not hide the held turn: once the task opens, the
// turn and its end are the task's, and the task waits for review again. When the first send fails, the second one,
// for the task too, takes the turn up.
func TestOverlappingSendsKeepTheHeldTurn(t *testing.T) {
	failed := errors.New("the input box holds a draft; nothing was typed")
	for _, c := range []struct {
		name, second  string // the conversation the second send is from
		first, latter error  // how each went
	}{
		{"another conversation's send meanwhile, which fails", "conv7", nil, failed},
		{"more for the task again meanwhile", "conv1", nil, nil},
		{"the first fails, the second for the task is taken", "conv1", failed, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			s, f, accepted := acceptedPlanTask(t, nil)
			before := supervisionOf(s, f, accepted.ID)
			first := s.noteAgentSend(ctx, "conv1", "box", longAgent, "", "Please cover the landscape case too")
			longTurn(s, f, proto.StatusWorking, "") // held for the first send's answer
			closedUnchanged(t, s, accepted)
			second := s.noteAgentSend(ctx, c.second, "box", longAgent, "", "And the portrait case")
			longTurn(s, f, proto.StatusDone, "Landscape covered: PR https://github.com/o/r/pull/78 is merged") // put off
			closedUnchanged(t, s, accepted)
			first.sent(ctx, c.first)
			second.sent(ctx, c.latter)
			if got := todoNow(s, accepted.ID); got.DoneAt != nil || got.ReopenedAt == nil {
				t.Fatalf("a send for the task was taken, and the task stayed closed: %+v", got)
			}
			waitFor(t, "the review again", func() bool { return todoNow(s, accepted.ID).Supervision.State == reviewPending })
			waitFor(t, "the next review message", func() bool { return supervisionOf(s, f, accepted.ID).notices > before.notices })
		})
	}
}

// More for an accepted task reaches its live agent, and the manager restarts before the node's answer to the send came
// back (the node had queued the text). The restarted manager takes the kept send as one whose answer is unknown: the
// agent's turn that starts while it counts is the task's (open, followed, judged, waiting for review), and one that
// starts after it stopped counting leaves the task closed. A turn that started while the send was out, before the
// restart, is the task's too once the manager starts (recoverSendsOut, as Run does), also when it ended before.
func TestMoreForAnAcceptedTaskSurvivesARestartBeforeItsAnswer(t *testing.T) {
	for _, c := range []struct {
		name, via string
		// before: what the agent did while the send was out, before the restart: "" nothing, "started" a turn,
		// "ended" a turn that started and ended
		before string
		late   bool // the turn starts after the send stopped counting
	}{
		{"card", "card", "", false},
		{"agent_send", "agent_send", "", false},
		{"agent_interrupt", "agent_interrupt", "", false},
		{"agent_send, a turn started before the restart", "agent_send", "started", false},
		{"card, a turn started and ended before the restart", "card", "ended", false},
		{"agent_send, its turn starts after the send stopped counting", "agent_send", "", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			if c.late {
				shortSends(t, 200*time.Millisecond, 100*time.Millisecond)
			}
			ctx := context.Background()
			database := taskRecoveryDatabase(t)
			s, f, accepted := acceptedPlanTask(t, database)
			out, gone, answered := make(chan struct{}), make(chan struct{}), make(chan error, 1)
			beforeSend(f, func() {
				if c.before != "" {
					longTurn(s, f, proto.StatusWorking, "")
				}
				if c.before == "ended" {
					longTurn(s, f, proto.StatusDone, "Landscape covered: PR https://github.com/o/r/pull/78 is merged")
				}
				close(out)
				<-gone // the manager that sent it stopped: it never reads the answer
			})
			go func() { answered <- sendVia(t, s, c.via, "conv1", accepted.ID, "Please cover the landscape case too") }()
			defer func() { // the old manager's call ends, as one that failed, once this test is done with it
				failSends(f, errors.New("the manager that sent it stopped"))
				close(gone)
				<-answered
			}()
			<-out
			sent := time.Now()
			closedUnchanged(t, s, accepted)

			// The manager restarts while the send is out.
			restarted, hub := newQuietServiceOn(t, database)
			hub.judge = `{"verdict":"done","reason":"merged"}`
			switch c.before {
			case "started":
				longRow(hub, proto.StatusWorking, "")
			case "ended":
				longRow(hub, proto.StatusDone, "Landscape covered: PR https://github.com/o/r/pull/78 is merged")
			}
			restarted.recoverSendsOut(ctx) // as Run does when the manager starts
			if c.before == "" {
				closedUnchanged(t, restarted, accepted) // the restart alone opens nothing
				if c.late {
					clockPast(t, sent.Add(liveSendWait+sendGrace))
					longTurn(restarted, hub, proto.StatusWorking, "")
					closedUnchanged(t, restarted, accepted)
					return
				}
				longTurn(restarted, hub, proto.StatusWorking, "")
			}
			if got := todoNow(restarted, accepted.ID); got.DoneAt != nil || got.ReopenedAt == nil {
				t.Fatalf("after the restart, the turn its %s queued left the task closed: %+v", c.via, got)
			}
			if c.before != "ended" {
				longTurn(restarted, hub, proto.StatusDone, "Landscape covered: PR https://github.com/o/r/pull/78 is merged")
			}
			waitFor(t, "the review again", func() bool { return todoNow(restarted, accepted.ID).Supervision.State == reviewPending })
			waitFor(t, "the review message after the restart", func() bool { return supervisionOf(restarted, hub, accepted.ID).notices == 1 })
		})
	}
}

// The background supervisor stands in for the main conversation only. A task a side conversation supervises sends that
// conversation its review signals; once accepted, the background supervisor's agent_send or agent_interrupt to its
// agent, and the agent's turn after it, leave it closed.
func TestBackgroundSupervisorLeavesASideConversationsTaskClosed(t *testing.T) {
	for _, tool := range []string{"agent_send", "agent_interrupt"} {
		t.Run(tool, func(t *testing.T) {
			s, f, todo, bg := acceptedByBackground(t)
			side := todo
			ledger := *todo.Supervision
			side.Origin, ledger.Supervisor, side.Supervision = "side1", "side1", &ledger
			putTodo(s, side)
			if !s.backgroundSupervises(context.Background(), bg, side) {
				t.Fatal("the background supervisor may not review the side conversation's task")
			}
			if err := sendTo(t, s, tool, bg, "Add the landscape case to the tests"); err != nil {
				t.Fatalf("%s from the background supervisor: %v", tool, err)
			}
			closedUnchanged(t, s, side)
			longTurn(s, f, proto.StatusWorking, "")
			closedUnchanged(t, s, side)
		})
	}
}

// The owner's message on the card of an accepted task whose agents have all stopped starts a new agent for it. When
// that start definitely fails (the agent's computer is unreachable, its launcher is refused), nothing was asked of the
// task: it stays exactly as accepted, and its supervisor hears nothing of it.
func TestACardMessageWhoseNewAgentFailsLeavesTheTaskClosed(t *testing.T) {
	for _, why := range []string{"computer unreachable", "launcher refused"} {
		t.Run(why, func(t *testing.T) {
			ctx := context.Background()
			s, f, todo := doneAndReviewed(t)
			accepted := reviewAndWait(t, s, todo.ID, reviewAccepted)
			before := supervisionOf(s, f, todo.ID)
			f.setSessions("box", proto.Session{SessionID: "s1", MachineID: "box", Cwd: "/code/app", Agent: "claude",
				LastText: "Fixed it: PR https://github.com/o/r/pull/77 is merged", Status: proto.StatusOffline, State: proto.StateOffline,
				UpdatedAt: time.Now(), Capabilities: &proto.SessionCapabilities{Read: true, Terminal: true}, ReadOnlyReason: "stopped"})
			f.mu.Lock()
			if why == "computer unreachable" {
				f.devices = []Device{{ID: "box", Name: "box", Online: true, Reachable: false}}
			} else {
				f.failStart = errors.New("launcher not found")
			}
			f.mu.Unlock()
			if _, err := s.routeToTask(ctx, "conv1", todo.ID, "Please cover the landscape case too"); err == nil {
				t.Fatal("the new agent started")
			}
			closedUnchanged(t, s, accepted)
			nothingToldAfter(t, s, f, todo.ID, before)
		})
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
	f.mu.Lock()
	f.afterList = map[string]func(){"box": func() { // the agents of the card's task were just read
		if err := s.linkTodoAgent(ctx, "todo_b", "box", longAgent, "", "/code/app", "claude"); err != nil {
			t.Errorf("hand the agent to the other task: %v", err)
			return
		}
		done := time.Now()
		s.applyTodoRow(ctx, "todo_b", db.TodoRow{ID: "todo_b", Title: "Tidy the release notes", Origin: "conv1", CreatedAt: now,
			UpdatedAt: done, DoneAt: &done, DoneBy: db.DoneByReview}, nil)
		other = todoNow(s, "todo_b")
	}}
	f.mu.Unlock()
	if _, err := s.routeToTask(ctx, "conv1", accepted.ID, "Please cover the landscape case too"); err != nil {
		t.Fatalf("the card message: %v", err)
	}
	if other.ID == "" || other.DoneAt == nil || other.DoneBy != db.DoneByReview || !slices.ContainsFunc(other.Agents, func(a TodoAgent) bool {
		return a.SessionID == longAgent && a.ReleasedAt == nil
	}) {
		t.Fatalf("the other task did not take the agent and close: %+v", other)
	}
	closedUnchanged(t, s, other)
	if got := todoNow(s, accepted.ID); got.DoneAt == nil {
		t.Fatalf("the card's task opened again without its agent: %+v", got)
	}
	longTurn(s, f, proto.StatusWorking, "")
	closedUnchanged(t, s, other)
}
