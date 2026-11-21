package assistant

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

// A review answering a supervision message about earlier work closes nothing its agent is working on again (SV-CE2).
// The judge finds the work done and the task waits for review. A message saying so is composed and sits in the queue
// while its supervisor is busy. Then the agent starts a new round: the owner asked it for more, or its rework came.
// supervisedWorking takes the ledger off pending_review and drops the signal not sent yet, but a message already
// written still reaches the supervisor. Its task_review accepted must be refused: the work it accepts is not the work
// going on. Accepting it would close the to-do under the agent, and a closed task's turns do not open it again
// (closedTurnReopens).
func TestAcceptFromAStaleReviewMessageIsRefusedWhileTheAgentWorksAgain(t *testing.T) {
	s, f, todo := newSupervisedService(t)
	ctx := context.Background()
	agentTurn(s, f, proto.StatusWorking, "", 0)
	agentTurn(s, f, proto.StatusDone, "修好了，PR https://github.com/o/r/pull/901 已合并", 0)
	waitFor(t, "the review message", func() bool { return len(f.notices()) == 1 })
	if v := todoNow(s, todo.ID).Supervision; v.State != reviewPending {
		t.Fatalf("not waiting for review: %+v", v)
	}
	// A new round begins before the supervisor reads that message.
	agentTurn(s, f, proto.StatusWorking, "", 0)
	if v := todoNow(s, todo.ID).Supervision; v.State == reviewPending {
		t.Fatalf("still waiting for review while its agent works again: %+v", v)
	}
	raw, _ := json.Marshal(map[string]any{"todo_id": todo.ID, "verdict": "accepted", "note": "PR 已合并，截图齐了",
		"checks": []ReviewCheck{{Criterion: "PR 合并到 master", Met: true, Evidence: "pull/901 merged"},
			{Criterion: "浅色 / 深色截图贴进 PR", Met: true, Evidence: "PR 里有两张"}}})
	res, err := s.runTool(ctx, "server", "conv1", "task_review", raw)
	now := todoNow(s, todo.ID)
	if err == nil || now.DoneAt != nil || now.Supervision.State == reviewAccepted {
		t.Fatalf("accepted from the message about the earlier round while the agent works again: err=%v res=%q done_at=%v state=%q",
			err, res, now.DoneAt, now.Supervision.State)
	}
	if !strings.Contains(err.Error(), "working again") {
		t.Fatalf("refusal does not say why: %v", err)
	}
	// The new round ends and is judged done: the task waits for review again, and that review accepts it.
	agentTurn(s, f, proto.StatusDone, "changelog 也更新了", 0)
	waitFor(t, "the review again", func() bool { return todoNow(s, todo.ID).Supervision.State == reviewPending })
	if _, err := s.runTool(ctx, "server", "conv1", "task_review", raw); err != nil {
		t.Fatalf("accepted after the new round was judged done: %v", err)
	}
	if now := todoNow(s, todo.ID); now.DoneAt == nil || now.Supervision.State != reviewAccepted {
		t.Fatalf("not closed by its review: done_at=%v state=%q", now.DoneAt, now.Supervision.State)
	}
}

// The same message held twice (the main conversation took over, #4262): one review sends the work back for rework and
// its agent starts on it; an acceptance answering that same message is refused while the rework is not judged done.
func TestAcceptAfterAReworkStartedFromTheSameMessageIsRefused(t *testing.T) {
	s, f, todo := newSupervisedService(t)
	ctx := context.Background()
	agentTurn(s, f, proto.StatusWorking, "", 0)
	agentTurn(s, f, proto.StatusDone, "修好了，PR https://github.com/o/r/pull/901 已合并", 0)
	waitFor(t, "the review message", func() bool { return len(f.notices()) == 1 })
	review := func(verdict string, checks ...ReviewCheck) error {
		raw, _ := json.Marshal(map[string]any{"todo_id": todo.ID, "verdict": verdict, "note": "截图只有浅色，补深色", "checks": checks})
		_, err := s.runTool(ctx, "server", "conv1", "task_review", raw)
		return err
	}
	merged := ReviewCheck{Criterion: "PR 合并到 master", Met: true, Evidence: "pull/901 merged"}
	if err := review("rework", merged, ReviewCheck{Criterion: "浅色 / 深色截图贴进 PR", Met: false, Evidence: "只有浅色"}); err != nil {
		t.Fatalf("rework: %v", err)
	}
	agentTurn(s, f, proto.StatusWorking, "", 0) // the rework reached its agent
	err := review("accepted", merged, ReviewCheck{Criterion: "浅色 / 深色截图贴进 PR", Met: true, Evidence: "PR 里有两张"})
	if now := todoNow(s, todo.ID); err == nil || now.DoneAt != nil {
		t.Fatalf("accepted from the message the rework answered while the rework runs: err=%v done_at=%v state=%q", err, now.DoneAt,
			now.Supervision.State)
	}
}
