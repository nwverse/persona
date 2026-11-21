package assistant

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestSampledJudgeLateOwnerActivityCommitsNothing(t *testing.T) {
	ctx := context.Background()
	st := &reviewActivityStore{DB: todoTestDB(t)}
	s, f, todo := newSupervisedServiceOn(t, st)
	s.superviseTimes = superviseTimings{gather: time.Hour, every: time.Hour, urgent: time.Hour}
	sampleAt(t, s, 0)
	agent := todo.Agents[0]
	items := turnPage("sampled-review", "The result is checked.", true)
	row := turnRow(proto.StatusDone, 3, "The result is checked.")
	row.MachineID, row.SessionID = agent.MachineID, agent.SessionID
	f.setSessions(agent.MachineID, row)
	s.setSessions(agent.MachineID, []proto.Session{row})
	end, _ := turnEndOf(items)
	work := time.Now().UTC()
	s.takeTurn(ctx, todo, row, items, end, true, work, turnDone)
	before := todoNow(s, todo.ID)
	beforeRows, err := st.ListTodoSupervision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	feedBefore := feedKinds(s, todo.Origin)
	var checks []autoCheckItem
	for _, criterion := range todo.Supervision.DoneWhen {
		checks = append(checks, autoCheckItem{
			ReviewCheck: ReviewCheck{Criterion: criterion, Met: true, Evidence: "Checked"},
			Checkable:   true,
		})
	}
	check := taskDoneCheck{Verdict: verdictDone, WouldClose: true, Reason: "Checked",
		AutoCheck: &taskAutoCheck{Delivery: "live", Checks: checks}}
	reachedGuard := false
	st.beforeCommit = func() {
		reachedGuard = true
		setTaskViewer(t, s, agent.MachineID, agent.SessionID, true)
	}
	s.applyTaskDone(ctx, todo, work, true, check, nil)
	if !reachedGuard {
		t.Fatal("the unsampled acceptance did not reach its final commit guard")
	}
	after := todoNow(s, todo.ID)
	if after.DoneAt != nil || !reflect.DeepEqual(after.Supervision, before.Supervision) {
		t.Fatalf("late activity left a judge-written ledger on an open task: before=%+v after=%+v",
			before.Supervision, after.Supervision)
	}
	afterRows, err := st.ListTodoSupervision(ctx)
	if err != nil || !reflect.DeepEqual(afterRows, beforeRows) {
		t.Fatalf("the refused judge review persisted ledger changes: %+v %v", afterRows, err)
	}
	if feedKinds(s, todo.Origin) != feedBefore || s.resultOfTodo(after.ID) != nil {
		t.Fatal("the refused judge review published review effects")
	}
	if turn, ok := s.turnOf(ctx, todo.ID); !ok || turn.Judged != nil {
		t.Fatalf("the refused judgment cannot be retried after inactivity: %+v", turn)
	}
	st.beforeCommit = func() {}
	setTaskViewer(t, s, agent.MachineID, agent.SessionID, false)
	old := time.Now().Add(-time.Hour)
	s.taskActivity.mu.Lock()
	s.taskActivity.times[runtimeListID(agent.MachineID, agent.SessionID)] = old
	s.taskActivity.mu.Unlock()
	s.fleet.mu.Lock()
	s.fleet.seen[[2]string{agent.MachineID, agent.SessionID}] = old
	s.fleet.mu.Unlock()
	s.applyTaskDone(ctx, todo, work, true, check, nil)
	if cur := todoNow(s, todo.ID); cur.DoneAt == nil || cur.Supervision.State != reviewAccepted ||
		len(cur.Supervision.Reviews) != 1 || cur.Supervision.Reviews[0].By != reviewerJudge {
		t.Fatalf("the judge could not retry after inactivity: %+v", cur)
	}
}
