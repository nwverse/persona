package assistant

import (
	"context"
	"encoding/json"
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

// #7539: once a task is accepted (or cancelled), a later turn of its agent that nobody sent for the task — a wakeup
// it set itself, a turn already running at the acceptance — neither opens it again nor tells its supervisor anything.
// Only what its conversation sends the agent (agent_send, the owner's message on its card), the owner's Reopen or
// todo_reopen opens it again. A task the judge closed on its own still opens again on its agent's next turn (#5892).

// supervisionSeen is what the supervisor was told, or is about to be told, of one task: the kinds of its signals
// waiting, the key last told for each kind, and how many supervision messages named it.
type supervisionSeen struct {
	pending []string
	told    map[string]string
	notices int
}

func supervisionOf(s *Service, f *fakeHub, id string) supervisionSeen {
	seen := supervisionSeen{pending: pendingKinds(s, id), told: map[string]string{}}
	b := &s.supervise
	b.mu.Lock()
	for k, v := range b.sent {
		if strings.HasPrefix(k, id+"|") {
			seen.told[k] = v
		}
	}
	b.mu.Unlock()
	for _, n := range f.notices() {
		if strings.Contains(n, id) {
			seen.notices++
		}
	}
	return seen
}

// nothingToldAfter waits out the supervision messages' timings and fails when anything about task id was raised or
// told since before.
func nothingToldAfter(t *testing.T, s *Service, f *fakeHub, id string, before supervisionSeen) {
	t.Helper()
	time.Sleep(4 * shortSuperviseTimings.every)
	after := supervisionOf(s, f, id)
	if len(after.pending) != 0 || !maps.Equal(after.told, before.told) || after.notices != before.notices {
		t.Fatalf("the supervisor heard of a closed task: waiting %v, told %v (before %v), %d messages (before %d):\n%s",
			after.pending, after.told, before.told, after.notices, before.notices, strings.Join(f.notices(), "\n---\n"))
	}
}

func judgeCalls(f *fakeHub) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.judged
}

func receiptCount(t *testing.T, s *Service) int {
	t.Helper()
	items, _, err := s.feed(context.Background(), "conv1", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, it := range items {
		if it.Kind == feedReceipt {
			n++
		}
	}
	return n
}

// reviewAndWait runs task_review on id from its supervisor conv1, and waits out what the review itself tells.
func reviewAndWait(t *testing.T, s *Service, id, verdict string) Todo {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"todo_id": id, "verdict": verdict, "note": "Checked the PR and the screenshots"})
	if _, err := s.runTool(context.Background(), "server", "conv1", "task_review", raw); err != nil {
		t.Fatalf("%s: %v", verdict, err)
	}
	time.Sleep(4 * shortSuperviseTimings.every)
	got := todoNow(s, id)
	if got.DoneAt == nil {
		t.Fatalf("%s did not close it: %+v", verdict, got)
	}
	return got
}

// closedUnchanged fails when closed task was's state, stage, PR or verdict moved, or it was opened again.
func closedUnchanged(t *testing.T, s *Service, was Todo) {
	t.Helper()
	got := todoNow(s, was.ID)
	if got.DoneAt == nil || !got.DoneAt.Equal(*was.DoneAt) || got.DoneBy != was.DoneBy || got.DoneReason != was.DoneReason ||
		got.ReopenedAt != nil || got.Stage != was.Stage || got.PRURL != was.PRURL || got.Supervision == nil ||
		got.Supervision.State != was.Supervision.State || len(got.Supervision.Reviews) != len(was.Supervision.Reviews) {
		t.Fatalf("the closed task changed:\n got %+v (supervision %+v)\nwant %+v (supervision %+v)", got, got.Supervision, was, was.Supervision)
	}
}

// doneAndReviewed is a plan-started supervised task whose agent finished, told as waiting for review.
func doneAndReviewed(t *testing.T) (*Service, *fakeHub, Todo) {
	t.Helper()
	s, f, todo := newSupervisedService(t)
	agentTurn(s, f, proto.StatusWorking, "", 0)
	agentTurn(s, f, proto.StatusDone, "Fixed it: PR https://github.com/o/r/pull/77 is merged", 0)
	waitFor(t, "the review message", func() bool { return supervisionOf(s, f, todo.ID).notices == 1 })
	return s, f, todo
}

// The case of #7539: accepted, then its agent's own wakeup starts a turn that only confirms nothing is left and ends.
func TestAcceptedPlanTaskStaysClosedOnALateDoneTurn(t *testing.T) {
	s, f, todo := doneAndReviewed(t)
	accepted := reviewAndWait(t, s, todo.ID, reviewAccepted)
	if accepted.DoneBy != db.DoneByReview || accepted.PlanID == "" {
		t.Fatalf("not a plan task accepted by its review: %+v", accepted)
	}
	before, judged, receipted := supervisionOf(s, f, todo.ID), judgeCalls(f), receiptCount(t, s)

	time.Sleep(5 * time.Millisecond)
	agentTurn(s, f, proto.StatusWorking, "", 0) // nothing was sent for the task
	closedUnchanged(t, s, accepted)
	agentTurn(s, f, proto.StatusDone, "Checked again: nothing is left. PR https://github.com/o/r/pull/78", 0)
	closedUnchanged(t, s, accepted)
	if n := judgeCalls(f); n != judged {
		t.Fatalf("the late turn was judged: %d judgments, %d before", n, judged)
	}
	if n := receiptCount(t, s); n != receipted {
		t.Fatalf("the late turn got a receipt: %d, %d before", n, receipted)
	}
	nothingToldAfter(t, s, f, todo.ID, before)
}

// A turn already running when the task was accepted ends after it: the same.
func TestAcceptedTaskStaysClosedWhenARunningTurnEndsAfterIt(t *testing.T) {
	s, f, todo := doneAndReviewed(t)
	agentTurn(s, f, proto.StatusWorking, "", 0) // it goes on for a moment, still the task's
	accepted := reviewAndWait(t, s, todo.ID, reviewAccepted)
	before, judged, receipted := supervisionOf(s, f, todo.ID), judgeCalls(f), receiptCount(t, s)

	agentTurn(s, f, proto.StatusDone, "Also tidied the notes: PR https://github.com/o/r/pull/78 is merged", 0)
	closedUnchanged(t, s, accepted)
	if n := judgeCalls(f); n != judged {
		t.Fatalf("the turn was judged: %d judgments, %d before", n, judged)
	}
	if n := receiptCount(t, s); n != receipted {
		t.Fatalf("the turn got a receipt: %d, %d before", n, receipted)
	}
	nothingToldAfter(t, s, f, todo.ID, before)
}

// Accepted while its agent's turn end is being read: the receipt still says how the turn ended (as for a task closed
// by hand meanwhile), and that is all: no stage or PR from that reply, no judgment, no signal.
func TestAcceptedWhileItsTurnEndIsRead(t *testing.T) {
	s, f, todo := doneAndReviewed(t)
	agentTurn(s, f, proto.StatusWorking, "", 0)
	var accepted Todo
	var before supervisionSeen
	var judged, receipted int
	f.hookReads(receiptHooks{before: func(read int) {
		if read == 1 && accepted.ID == "" { // the receipt's read of the turn: the review comes in now
			accepted = reviewAndWait(t, s, todo.ID, reviewAccepted)
			before, judged, receipted = supervisionOf(s, f, todo.ID), judgeCalls(f), receiptCount(t, s)
		}
	}})
	agentTurn(s, f, proto.StatusDone, "Also tidied the notes: PR https://github.com/o/r/pull/78 is merged", 0)
	f.hookReads(receiptHooks{})
	if accepted.ID == "" {
		t.Fatal("the turn's end was never read")
	}
	closedUnchanged(t, s, accepted)
	if n := receiptCount(t, s); n != receipted+1 {
		t.Fatalf("%d receipts, %d before: the turn's end has none", n, receipted)
	}
	if n := judgeCalls(f); n != judged {
		t.Fatalf("the turn was judged: %d judgments, %d before", n, judged)
	}
	nothingToldAfter(t, s, f, todo.ID, before)
}

// A judgment that lands after the acceptance is of work from before it: dropped, whether the to-do is closed already
// or task_review has written its ledger and not closed the to-do yet. Work after an explicit reopen is judged as ever.
func TestLateJudgmentOfAnAcceptedTaskIsDropped(t *testing.T) {
	s, f, todo := newSupervisedService(t)
	ctx := context.Background()
	work := time.Now()
	agentTurn(s, f, proto.StatusWorking, "", 0)
	judgedCopy := todoNow(s, todo.ID) // the task as the judge was handed it
	time.Sleep(5 * time.Millisecond)
	if _, _, err := s.updateSupervision(ctx, todo.ID, func(v *Supervision) bool { // task_review, before it closes the to-do
		v.State = reviewAccepted
		v.Reviews = append(v.Reviews, TaskReview{At: time.Now().UTC(), Verdict: reviewAccepted})
		return true
	}); err != nil {
		t.Fatal(err)
	}
	verdicts := []taskDoneCheck{{Verdict: verdictDone, Reason: "merged"}, {Verdict: verdictNotDone, Reason: "one case left"},
		{Verdict: verdictUnsure, Reason: "cannot tell"}}
	for _, c := range verdicts {
		s.superviseJudged(ctx, judgedCopy, work, c, nil)
		if v := todoNow(s, todo.ID).Supervision; v.State != reviewAccepted || len(pendingKinds(s, todo.ID)) != 0 {
			t.Fatalf("judged %s while accepted: ledger %s, waiting %v", c.Verdict, v.State, pendingKinds(s, todo.ID))
		}
	}
	if ok, err := s.reviewDoneTodo(ctx, todo.ID, "Checked", true); err != nil || !ok {
		t.Fatalf("close: %v %v", ok, err)
	}
	for _, c := range verdicts {
		s.superviseJudged(ctx, judgedCopy, work, c, nil)
		if v := todoNow(s, todo.ID).Supervision; v.State != reviewAccepted || len(pendingKinds(s, todo.ID)) != 0 {
			t.Fatalf("judged %s once closed: ledger %s, waiting %v", c.Verdict, v.State, pendingKinds(s, todo.ID))
		}
	}

	if err := s.reopenTodo(ctx, todo.ID); err != nil {
		t.Fatal(err)
	}
	s.superviseJudged(ctx, todoNow(s, todo.ID), time.Now(), verdicts[0], nil)
	if v := todoNow(s, todo.ID).Supervision; v.State != reviewPending || !strings.Contains(strings.Join(pendingKinds(s, todo.ID), ","), sigReview) {
		t.Fatalf("work after the reopen judged done: ledger %s, waiting %v", v.State, pendingKinds(s, todo.ID))
	}
}

// What the task's conversation sends its agent after the acceptance opens a plan-started task again, as it does one
// with no plan (#6726): a turn nobody sent and another conversation's agent_send leave it closed, its own agent_send
// opens it.
func TestAcceptedPlanTaskReopensWhenItsConversationSendsMore(t *testing.T) {
	s, f := newQuietService(t)
	ctx := context.Background()
	const sid = "s1abcdef"
	turn := func(state string) {
		status := proto.StatusIdle
		if state == proto.StateWorking {
			status = proto.StatusWorking
		}
		f.setSessions("box", proto.Session{SessionID: sid, MachineID: "box", Cwd: "/code/app", Agent: "claude", Status: status,
			State: state, UpdatedAt: time.Now()})
		if state == proto.StateWorking {
			s.taskWorking(ctx, "box", sid)
		}
	}
	done := time.Now()
	putTodo(s, Todo{ID: "todo_a", Title: "Write the plan", Origin: "conv1", PlanID: "plan_1", CreatedAt: done.Add(-time.Hour),
		DoneAt: &done, DoneBy: db.DoneByReview, Supervision: &Supervision{Supervisor: "conv1", State: reviewAccepted},
		Agents: []TodoAgent{{MachineID: "box", SessionID: sid}}})
	s.todos.mu.Lock()
	delete(s.todos.activeBySession, [2]string{"box", sid}) // closed: as setTodoRow does
	s.todos.mu.Unlock()
	turn(proto.StateIdle)
	time.Sleep(5 * time.Millisecond)
	open := func() bool { return todoNow(s, "todo_a").DoneAt == nil }
	send := func(from, text string) {
		t.Helper()
		if _, err := runJSON(t, s, from, "agent_send", map[string]any{"machine": "box", "session_id": sid, "text": text}); err != nil {
			t.Fatalf("agent_send from %s: %v", from, err)
		}
	}

	turn(proto.StateWorking) // its own wakeup, or the owner typing in its session
	if open() {
		t.Fatal("a turn nobody sent for the task reopened it")
	}
	turn(proto.StateIdle)
	send("conv7", "Also look at the other dashboard")
	turn(proto.StateWorking)
	if open() {
		t.Fatal("another conversation's agent_send reopened it")
	}
	turn(proto.StateIdle)
	send("conv1", "Add a table to section three")
	turn(proto.StateWorking)
	if got := todoNow(s, "todo_a"); got.DoneAt != nil || got.ReopenedAt == nil {
		t.Fatalf("its own conversation's agent_send did not reopen it: %+v", got)
	}
}

// The owner's message on the task's card opens it again too, and the work it starts is followed as ever: judged done,
// the task waits for review again.
func TestAcceptedPlanTaskReopensForTheOwnersCardMessage(t *testing.T) {
	s, f, todo := doneAndReviewed(t)
	reviewAndWait(t, s, todo.ID, reviewAccepted)
	if _, err := s.routeToTask(context.Background(), "conv1", todo.ID, "Please cover the landscape case too"); err != nil {
		t.Fatalf("the owner's message on the card: %v", err)
	}
	agentTurn(s, f, proto.StatusWorking, "", 0)
	if got := todoNow(s, todo.ID); got.DoneAt != nil || got.ReopenedAt == nil {
		t.Fatalf("the owner's card message did not reopen it: %+v", got)
	}
	agentTurn(s, f, proto.StatusDone, "Landscape covered: PR https://github.com/o/r/pull/78 is merged", 0)
	waitFor(t, "the review again", func() bool { return todoNow(s, todo.ID).Supervision.State == reviewPending })
	waitFor(t, "the second review message", func() bool { return supervisionOf(s, f, todo.ID).notices == 2 })
}

// A cancelled task stays cancelled when its agent ends one more turn "done" after it (#1851), with nothing told.
func TestCancelledTaskStaysClosedOnALateDoneTurn(t *testing.T) {
	s, f, todo := newSupervisedService(t)
	agentTurn(s, f, proto.StatusWorking, "", 0)
	cancelled := reviewAndWait(t, s, todo.ID, reviewCancelled)
	if !cancelled.Cancelled() {
		t.Fatalf("not cancelled: %+v", cancelled)
	}
	before, judged := supervisionOf(s, f, todo.ID), judgeCalls(f)

	agentTurn(s, f, proto.StatusDone, "Finished anyway: PR https://github.com/o/r/pull/78 is merged", 0)
	time.Sleep(5 * time.Millisecond)
	agentTurn(s, f, proto.StatusWorking, "", 0)
	agentTurn(s, f, proto.StatusDone, "Checked again: all done", 0)
	closedUnchanged(t, s, cancelled)
	if !todoNow(s, todo.ID).Cancelled() || judgeCalls(f) != judged {
		t.Fatalf("cancelled task judged or reopened: %+v, %d judgments (%d before)", todoNow(s, todo.ID), judgeCalls(f), judged)
	}
	nothingToldAfter(t, s, f, todo.ID, before)
}

// Unchanged: a task the hub's judge closed on its own (nobody accepted it) opens again on its agent's next turn, plan
// or not, with nothing sent for it (#5892).
func TestJudgeClosedTaskStillReopensOnTheNextTurn(t *testing.T) {
	s, f := newTaskService(t)
	f.judge = `{"verdict":"not_done","reason":"The overview is still missing"}`
	todo := startedTask(t, s, f)
	if todo.PlanID == "" || todo.Supervision != nil {
		t.Fatalf("not an unsupervised plan task: %+v", todo)
	}
	agentTurn(s, f, proto.StatusWorking, "", 0)
	agentTurn(s, f, proto.StatusDone, "The draft is written", 0)
	if ok, err := s.autoDoneTodo(context.Background(), todo.ID, "Done", time.Now().Add(-time.Minute)); err != nil || !ok {
		t.Fatalf("auto done: %v %v", ok, err)
	}
	time.Sleep(5 * time.Millisecond)
	agentTurn(s, f, proto.StatusWorking, "", 0)
	if got := todoNow(s, todo.ID); got.DoneAt != nil || got.ReopenedAt == nil {
		t.Fatalf("the judge's close did not give way to its agent's next turn: %+v", got)
	}
}
