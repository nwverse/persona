package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/ai"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// ownerDecision is a needs_user review a decision the owner can answer (#3804).
func ownerDecision(todoID string) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{"todo_id": todoID, "verdict": "needs_user", "why_owner": "only_owner_knows",
		"reason": "两版公告选哪一版", "recommend": "A 版", "cost_of_waiting": "公告晚一天发",
		"options": []map[string]string{{"label": "A 版", "leads_to": "今天发 A 版"}, {"label": "B 版", "leads_to": "今天发 B 版"}}})
	return raw
}

// stopOnOwner makes the task's agent stop with drafts for the owner to pick from, and the judge say so (needs_user).
func stopOnOwner(t *testing.T, s *Service, f *fakeHub, todoID string) {
	t.Helper()
	f.judge = `{"verdict":"needs_user","reason":"两版草稿等用户挑一版再发"}`
	agentTurn(s, f, proto.StatusWorking, "", 0)
	agentTurn(s, f, proto.StatusDone, "两版公告草稿都写好了，等你挑一版我再发。", 0)
	waitFor(t, "the owner wait on the ledger", func() bool { return todoNow(s, todoID).Supervision.Waits != nil })
}

// A needs_user judgment is itself the visible waiting state, not only a note for a later supervisor round. The push
// carries the reason and task link, and judging the same stretch again does not push it twice (#5732).
func TestNeedsUserJudgmentWaitsAndPushesOnce(t *testing.T) {
	s, f := newQuietService(t)
	withWebBot(f, testWeb)
	f.known["side1"] = true
	if _, err := s.toolTaskPlan(context.Background(), "side1", planArgs(true)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the side task's agent", func() bool { return len(onlyTodo(s).Agents) == 1 })
	todo := onlyTodo(s)
	if todo.Quiet || todo.Supervision == nil || todo.Supervision.Supervisor != "conv1" {
		t.Fatalf("side task = %+v", todo)
	}
	f.judge = `{"verdict":"needs_user","reason":"需要你本人操作"}`
	agentTurn(s, f, proto.StatusWorking, "", 0)
	agentTurn(s, f, proto.StatusDone, "Spec 已完成，下一步由你发起实现。", 0)
	waitFor(t, "the visible owner wait", func() bool {
		return todoNow(s, todo.ID).Supervision.State == reviewNeedsUser
	})
	cur := todoNow(s, todo.ID)
	if cur.DoneAt != nil || cur.Supervision.Waits == nil {
		t.Fatalf("waiting task = %+v", cur)
	}
	waitFor(t, "the needs-user push", func() bool { return len(f.pushed()) == 1 })
	pushes := f.pushed()
	if len(pushes) != 1 || pushes[0].Kind != NotifyPush || pushes[0].SessionID != "conv1" ||
		!strings.Contains(pushes[0].Body, "需要你本人操作") || !strings.Contains(pushes[0].Body, testWeb+"/todos?id="+todo.ID) {
		t.Fatalf("needs-user pushes = %+v", pushes)
	}
	s.superviseJudged(context.Background(), cur, cur.Supervision.Waits.Work, taskDoneCheck{Verdict: verdictNeedsUser, Reason: "需要你本人操作"}, nil)
	if got := len(f.pushed()); got != 1 {
		t.Fatalf("same wait pushed %d times", got)
	}
}

// Main-dispatched tasks are quiet by default, but an owner-only action still gets its one push. Quiet suppresses
// routine task notices, never the request the owner must answer (#5732).
func TestMainQuietNeedsUserJudgmentPushesOnce(t *testing.T) {
	s, f := newQuietService(t)
	withWebBot(f, testWeb)
	if _, err := s.toolTaskPlan(context.Background(), "conv1", planArgs(true)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the main task's agent", func() bool { return len(onlyTodo(s).Agents) == 1 })
	todo := onlyTodo(s)
	if !todo.Quiet || todo.Supervision == nil || todo.Supervision.Supervisor != "conv1" {
		t.Fatalf("main quiet task = %+v", todo)
	}
	f.judge = `{"verdict":"needs_user","reason":"需要你本人操作"}`
	agentTurn(s, f, proto.StatusWorking, "", 0)
	agentTurn(s, f, proto.StatusDone, "Spec 已完成，下一步由你发起实现。", 0)
	waitFor(t, "the main quiet task's visible owner wait", func() bool {
		return todoNow(s, todo.ID).Supervision.State == reviewNeedsUser
	})
	waitFor(t, "the main quiet task's owner-action push", func() bool { return len(f.pushed()) == 1 })
	cur := todoNow(s, todo.ID)
	s.superviseJudged(context.Background(), cur, cur.Supervision.Waits.Work, taskDoneCheck{Verdict: verdictNeedsUser, Reason: "需要你本人操作"}, nil)
	if got := len(f.pushed()); got != 1 {
		t.Fatalf("same main quiet wait pushed %d times", got)
	}
}

// Needs-user pushes follow the conversation supervision route, not a task leader's machine/session reference.
// Manual leaders and the legacy background escalate this owner decision to main as well (#5732).
func TestNeedsUserPushGoesToMainForEverySupervisedTopology(t *testing.T) {
	s, f := newQuietService(t)
	setSettings(t, s, func(v *Settings) {
		v.Leaders = []string{"lead1"}
		v.BackgroundSupervisor, v.BackgroundSupervisorMachineID = "bg1", "server"
	})
	for _, todo := range []Todo{
		{ID: "manual-leader", Title: "手动 leader", Origin: "lead1", Supervision: &Supervision{Supervisor: "lead1"}},
		{ID: "leader-worker", Title: "leader worker", Origin: "box/s1", Supervision: &Supervision{Supervisor: "box/s1"}},
		{ID: "legacy-background", Title: "旧后台", Origin: "conv1", Supervision: &Supervision{Supervisor: "bg1"}},
	} {
		s.notifyTaskNeedsUser(context.Background(), todo, time.Now().UTC(), "需要用户本人处理")
	}
	pushes := f.pushed()
	if len(pushes) != 3 {
		t.Fatalf("needs-user pushes = %+v", pushes)
	}
	for _, push := range pushes {
		if push.SessionID != "conv1" {
			t.Errorf("push for %s went to %q instead of main", push.ID, push.SessionID)
		}
	}
}

// With no main configured, a task leader's machine/session reference is not an addressable conversation. Once main is
// restored, the same owner wait can retry its still-undelivered push; a later duplicate judgment does not send it again.
func TestNeedsUserPushRecoversAfterMainIsConfigured(t *testing.T) {
	s, f := newQuietService(t)
	work := time.Now().UTC()
	todo := Todo{ID: "leader-worker-no-main", Title: "等待 owner", Origin: "box/s1",
		Supervision: &Supervision{Supervisor: "box/s1", State: reviewNeedsUser, Waits: &OwnerWait{Work: work, At: work, Reason: "需要用户本人处理", PushPending: true}}}
	putTodo(s, todo)
	setSettings(t, s, func(v *Settings) { v.Main = "" })
	s.notifyTaskNeedsUser(context.Background(), todo, work, "需要用户本人处理")
	if got := f.pushed(); len(got) != 0 {
		t.Errorf("a worker reference was used as a push conversation: %+v", got)
	}
	f.mu.Lock()
	f.pushes = nil // keep the route-restoration half independent after recording the bad-address assertion above
	f.mu.Unlock()

	before := todoNow(s, todo.ID).Supervision.UpdatedAt
	setSettings(t, s, func(v *Settings) { v.Main = "conv1" })
	s.noteOwnerWait(context.Background(), todoNow(s, todo.ID), work, "需要用户本人处理")
	waitFor(t, "the pending push after main is restored", func() bool { return len(f.pushed()) == 1 })
	pushes := f.pushed()
	if len(pushes) != 1 || pushes[0].SessionID != "conv1" {
		t.Fatalf("restored needs-user push = %+v", pushes)
	}
	waitFor(t, "the delivered receipt on the same owner wait", func() bool {
		return todoNow(s, todo.ID).Supervision.UpdatedAt.After(before)
	})
	s.noteOwnerWait(context.Background(), todoNow(s, todo.ID), work, "需要用户本人处理")
	if got := len(f.pushed()); got != 1 {
		t.Fatalf("the delivered owner wait pushed %d times", got)
	}
}

// Settings recovery itself retries an explicitly undelivered wait. A pre-NotifiedAt legacy wait is not replayed on
// upgrade, and a delivered pending wait is not repeated by the next settings sweep (#5732).
func TestNeedsUserPushRetriesWhenSettingsRecover(t *testing.T) {
	s, f := newQuietService(t)
	work := time.Now().UTC()
	pending := Todo{ID: "pending-route-recovery", Title: "等待路由恢复", Origin: "box/s1",
		Supervision: &Supervision{Supervisor: "box/s1", State: reviewNeedsUser,
			Waits: &OwnerWait{Work: work, At: work, Reason: "需要用户本人处理", PushPending: true}}}
	legacy := Todo{ID: "legacy-already-told", Title: "升级前已通知", Origin: "conv1",
		Supervision: &Supervision{Supervisor: "conv1", State: reviewNeedsUser, Waits: &OwnerWait{Work: work, At: work, Reason: "旧通知"}}}
	putTodo(s, pending)
	putTodo(s, legacy)
	setSettings(t, s, func(v *Settings) { v.Main = "" })
	s.settingsChanged(context.Background())
	if got := f.pushed(); len(got) != 0 {
		t.Fatalf("an unset main routed a push to a worker: %+v", got)
	}

	before := todoNow(s, pending.ID).Supervision.UpdatedAt
	setSettings(t, s, func(v *Settings) { v.Main = "conv1" }) // the real settingsChanged hook retries the pending push
	waitFor(t, "the pending push on settings recovery", func() bool { return len(f.pushed()) == 1 })
	pushes := f.pushed()
	if len(pushes) != 1 || pushes[0].SessionID != "conv1" || !strings.Contains(pushes[0].ID, pending.ID) {
		t.Fatalf("settings recovery pushes = %+v", pushes)
	}
	waitFor(t, "the delivery receipt", func() bool { return todoNow(s, pending.ID).Supervision.UpdatedAt.After(before) })
	s.settingsChanged(context.Background())
	if got := f.pushed(); len(got) != 1 {
		t.Fatalf("settings recovery repeated a delivered push: %+v", got)
	}
}

type stalledOwnerPushHub struct {
	*fakeHub
	entered chan string
	release chan struct{}
}

func (h *stalledOwnerPushHub) NotifyRequest(ctx context.Context, p NotifyRequest) error {
	h.entered <- p.Notification.ID
	<-h.release // like rpc.Peer.writeContext's ongoing Write, this cannot be interrupted by ctx
	return nil
}

// One hub writer that cannot be interrupted must not hold every later task's push behind global dedup state (#5732).
func TestOwnerPushStalledWriterDoesNotBlockOtherTasks(t *testing.T) {
	s, f := newQuietService(t)
	hub := &stalledOwnerPushHub{fakeHub: f, entered: make(chan string, 2), release: make(chan struct{})}
	setHub(s, hub)
	released := false
	defer func() {
		if !released {
			close(hub.release)
		}
	}()
	for _, id := range []string{"first-stalled", "second-waiting"} {
		putTodo(s, Todo{ID: id, Title: id, Origin: "conv1", Supervision: &Supervision{Supervisor: "conv1"}})
	}
	work := time.Now().UTC()
	s.noteOwnerWait(context.Background(), todoNow(s, "first-stalled"), work, "first")
	select {
	case id := <-hub.entered:
		if !strings.Contains(id, "first-stalled") {
			t.Fatalf("first stalled push = %q", id)
		}
	case <-time.After(time.Second):
		t.Fatal("the first push did not reach the stalled writer")
	}
	s.noteOwnerWait(context.Background(), todoNow(s, "second-waiting"), work.Add(time.Second), "second")
	select {
	case id := <-hub.entered:
		if !strings.Contains(id, "second-waiting") {
			t.Fatalf("second push = %q", id)
		}
	case <-time.After(time.Second):
		t.Fatal("the second task stayed blocked behind the uninterruptible writer")
	}
	close(hub.release)
	released = true
	waitFor(t, "both owner-push receipts after the writer resumes", func() bool {
		for _, id := range []string{"first-stalled", "second-waiting"} {
			w := todoNow(s, id).Supervision.Waits
			if w == nil || w.PushPending || w.NotifiedAt == nil {
				return false
			}
		}
		return true
	})
}

// A delivered push whose receipt waits behind another supervision write must not stop a different task's push (#5732).
func TestOwnerPushReceiptWriteDoesNotBlockOtherTasks(t *testing.T) {
	s, f := newQuietService(t)
	work := time.Now().UTC()
	for _, id := range []string{"first-receipt-stalled", "second-receipt-independent"} {
		putTodo(s, Todo{ID: id, Title: id, Origin: "conv1", Supervision: &Supervision{Supervisor: "conv1", State: reviewNeedsUser,
			Waits: &OwnerWait{Work: work, At: work, Reason: id, PushPending: true}}})
	}
	s.supervise.writeMu.Lock()
	locked := true
	defer func() {
		if locked {
			s.supervise.writeMu.Unlock()
		}
	}()
	s.startOwnerWaitDelivery(context.Background(), todoNow(s, "first-receipt-stalled"), work, "first")
	waitFor(t, "the first push before its stalled receipt", func() bool { return len(f.pushed()) == 1 })
	s.startOwnerWaitDelivery(context.Background(), todoNow(s, "second-receipt-independent"), work, "second")
	waitFor(t, "the second push while the first receipt is stalled", func() bool { return len(f.pushed()) == 2 })
	s.supervise.writeMu.Unlock()
	locked = false
	waitFor(t, "both receipts after the supervision write resumes", func() bool {
		for _, id := range []string{"first-receipt-stalled", "second-receipt-independent"} {
			w := todoNow(s, id).Supervision.Waits
			if w == nil || w.PushPending || w.NotifiedAt == nil {
				return false
			}
		}
		return true
	})
}

// The explicit owner-action safety net still becomes the visible wait and one push when the model itself failed.
func TestOwnerWaitSurvivesJudgeFailure(t *testing.T) {
	s, f := newQuietService(t)
	f.known["side1"] = true
	if _, err := s.toolTaskPlan(context.Background(), "side1", planArgs(true)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the side task's agent", func() bool { return len(onlyTodo(s).Agents) == 1 })
	todo := onlyTodo(s)
	work := time.Now().UTC()
	s.applyTaskDone(context.Background(), todo, work, true,
		taskDoneCheck{Verdict: verdictNeedsUser, Reason: "Should I proceed with the implementation?"}, errors.New("model unavailable"))
	waitFor(t, "the visible wait despite the judge failure", func() bool {
		return todoNow(s, todo.ID).Supervision.State == reviewNeedsUser
	})
	if cur := todoNow(s, todo.ID); cur.DoneAt != nil || cur.Supervision.Waits == nil {
		t.Fatalf("waiting task = %+v", cur)
	}
	waitFor(t, "the owner-action push", func() bool { return len(f.pushed()) == 1 })
}

// A missing judge model does not erase the deterministic owner-action verdict before it reaches the visible wait.
func TestOwnerWaitSurvivesAnUnconfiguredJudge(t *testing.T) {
	s, f, _ := modelService(t)
	setSettings(t, s, func(v *Settings) { v.Main = "conv1" })
	f.known["conv1"] = true
	f.setSessions("server", proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle})
	ctx := context.Background()
	todo, err := s.createTodo(ctx, "Prepare the release", "", "conv1", "")
	if err != nil {
		t.Fatal(err)
	}
	s.superviseTodo(ctx, todo.ID, "conv1", nil, 0)
	todo = todoNow(s, todo.ID)
	check, judgeErr := judgeTaskDoneWith(ctx, s.purposeModel("inbox.judge"), taskDoneInput{
		Replies: []prompts.TaskDoneReply{{Text: "Spec 已完成，下一步由你发起实现。"}},
	})
	if !ai.IsNotConfigured(judgeErr) || check.Verdict != verdictNeedsUser {
		t.Fatalf("the deterministic owner action was not kept before apply: %+v, %v", check, judgeErr)
	}
	work := time.Now().UTC()
	s.applyTaskDone(ctx, todo, work, true, check, judgeErr)
	cur := todoNow(s, todo.ID)
	if cur.DoneAt != nil || cur.Supervision.State != reviewNeedsUser || cur.Supervision.Waits == nil {
		t.Fatalf("an unconfigured judge erased the owner wait: %+v", cur)
	}
	waitFor(t, "the owner-action push without a judge", func() bool { return len(f.pushed()) == 1 })
}

// pick from) was accepted as done, and the decision was lost with it. The judge's needs_user is kept on the ledger, and
// accepting is refused while it holds; a decision for the owner is what the review has to be.
func TestAcceptedIsRefusedWhileTheWorkWaitsOnTheOwner(t *testing.T) {
	s, f, todo := newSupervisedService(t)
	ctx := context.Background()
	stopOnOwner(t, s, f, todo.ID)
	w := todoNow(s, todo.ID).Supervision.Waits
	if w.Reason != "两版草稿等用户挑一版再发" || w.At.IsZero() || w.Work.IsZero() {
		t.Fatalf("waits = %+v", w)
	}
	if health, _ := taskHealth(todoNow(s, todo.ID), nil, time.Now()); health != "等你处理" {
		t.Fatalf("snapshot health = %q", health)
	}

	raw, _ := json.Marshal(map[string]any{"todo_id": todo.ID, "verdict": "accepted", "note": "草稿写好了"})
	_, err := s.runTool(ctx, "server", "conv1", "task_review", raw)
	if err == nil || !strings.Contains(err.Error(), "decision only the owner can make") || !strings.Contains(err.Error(), "needs_user") ||
		!strings.Contains(err.Error(), "两版草稿等用户挑一版再发") {
		t.Fatalf("accepted while the work waits on the owner: %v", err)
	}
	now := todoNow(s, todo.ID)
	if now.DoneAt != nil || len(now.Supervision.Reviews) != 0 || now.Supervision.Waits == nil {
		t.Fatalf("a refused review changed the task: done %v, %+v", now.DoneAt, now.Supervision)
	}
	// user_check does not get round it either.
	raw, _ = json.Marshal(map[string]any{"todo_id": todo.ID, "verdict": "accepted", "user_check": true})
	if _, err := s.runTool(ctx, "server", "conv1", "task_review", raw); err == nil {
		t.Fatal("accepted with user_check while the work waits on the owner")
	}

	// The decision goes to the owner: allowed, and the wait is answered.
	if res, err := s.runTool(ctx, "server", "conv1", "task_review", ownerDecision(todo.ID)); err != nil || !strings.Contains(res, "waits for the user") {
		t.Fatalf("needs_user: %s %v", res, err)
	}
	if v := todoNow(s, todo.ID).Supervision; v.State != reviewNeedsUser || v.Waits != nil {
		t.Fatalf("after the decision went to the owner: %+v", v)
	}
}

// The owner's answer reaches the agent and it goes on: the wait is over, a later not-done verdict still reaches main,
// and once the judge finds the work done the task is accepted as usual.
func TestOwnerWaitEndsWhenTheAgentGoesOn(t *testing.T) {
	s, f, todo := newSupervisedService(t)
	ctx := context.Background()
	stopOnOwner(t, s, f, todo.ID)

	agentTurn(s, f, proto.StatusWorking, "", 0)
	waitFor(t, "the visible owner wait cleared by work", func() bool {
		v := todoNow(s, todo.ID).Supervision
		return v.Waits == nil && v.State != reviewNeedsUser
	})
	f.judge = `{"verdict":"not_done","reason":"还没发"}`
	agentTurn(s, f, proto.StatusDone, "我按选定的 A 版改了，但还没发", 0)
	waitFor(t, "the following not-done verdict reaches main", func() bool {
		for _, text := range f.noticesTo("conv1") {
			if strings.Contains(text, "还没发") {
				return true
			}
		}
		return false
	})

	f.judge = `{"verdict":"done","reason":"按用户选的 A 版发了"}`
	agentTurn(s, f, proto.StatusWorking, "", 0)
	agentTurn(s, f, proto.StatusDone, "按你选的 A 版发出去了，PR https://github.com/o/r/pull/7 已合并", 0)
	waitFor(t, "waits for review", func() bool { return todoNow(s, todo.ID).Supervision.State == reviewPending })

	raw, _ := json.Marshal(map[string]any{"todo_id": todo.ID, "verdict": "accepted", "note": "A 版已发"})
	if res, err := s.runTool(ctx, "server", "conv1", "task_review", raw); err != nil || !strings.Contains(res, "accepted and closed") {
		t.Fatalf("accepted after the agent went on: %s %v", res, err)
	}
}

// A task the owner no longer wants can still be cancelled while its work waits on them.
func TestCancelledWhileTheWorkWaitsOnTheOwner(t *testing.T) {
	s, f, todo := newSupervisedService(t)
	stopOnOwner(t, s, f, todo.ID)
	raw, _ := json.Marshal(map[string]any{"todo_id": todo.ID, "verdict": "cancelled", "note": "用户说不发公告了"})
	if _, err := s.runTool(context.Background(), "server", "conv1", "task_review", raw); err != nil {
		t.Fatalf("cancelled: %v", err)
	}
	if now := todoNow(s, todo.ID); now.DoneAt == nil || now.Supervision.Waits != nil {
		t.Fatalf("after cancelling: done %v, %+v", now.DoneAt, now.Supervision)
	}
}

// The wait is kept in the database with the ledger, so a manager restart does not let the task be accepted.
func TestOwnerWaitSurvivesTheLedgerRow(t *testing.T) {
	at := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	notified := at.Add(time.Minute)
	v := &Supervision{Supervisor: "conv1", DoneWhen: []string{}, Waits: &OwnerWait{Work: at.Add(-time.Hour), At: at, Reason: "等用户挑一版", NotifiedAt: &notified}}
	back := supervisionFromRow(v.row("todo_x"))
	if back.Waits == nil || !back.Waits.At.Equal(at) || !back.Waits.Work.Equal(at.Add(-time.Hour)) || back.Waits.Reason != "等用户挑一版" ||
		back.Waits.NotifiedAt == nil || !back.Waits.NotifiedAt.Equal(notified) {
		t.Fatalf("waits back from its row = %+v", back.Waits)
	}
	pending := supervisionFromRow((&Supervision{Waits: &OwnerWait{PushPending: true}}).row("todo_pending"))
	if pending.Waits == nil || !pending.Waits.PushPending {
		t.Fatalf("pending delivery did not round-trip: %+v", pending.Waits)
	}
	c := v.clone()
	c.Waits.Reason = "changed"
	if v.Waits.Reason != "等用户挑一版" {
		t.Fatal("clone shares the wait")
	}
	if none := (&Supervision{}).row("todo_y"); none.Waits != nil {
		t.Fatalf("no wait is written as %s", none.Waits)
	}
}
