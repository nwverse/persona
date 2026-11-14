package assistant

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

// A task an ordinary conversation hands out with task_plan keeps that conversation as its origin, but the main
// conversation supervises and reviews it (#5732).
func TestSideConversationTaskIsSupervisedByMain(t *testing.T) {
	s, f := newQuietService(t)
	ctx := context.Background()
	f.judge = `{"verdict":"done","reason":"PR 已合并"}`
	f.known["side1"] = true
	f.setSessions("server", proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle},
		proto.Session{SessionID: "side1", MachineID: "server", Assistant: true, Status: proto.StatusIdle})
	raw, _ := json.Marshal(map[string]any{"tasks": []map[string]any{{
		"title": "修登录崩溃", "prompt": "iPad 横屏时登录页崩溃，修好并补测试", "machine": "box", "cwd": "/code/app", "readonly": true,
		"why": "这个仓库最近在 box 上", "done_when": []string{"PR 合并到 main"}, "expect_minutes": 30,
	}}})
	res, err := s.toolTaskPlan(ctx, "side1", raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res, "The main conversation supervises them") {
		t.Fatalf("the planning conversation is not told who supervises: %s", res)
	}
	waitFor(t, "the link", func() bool { return len(onlyTodo(s).Agents) == 1 })
	todo := onlyTodo(s)
	if v := todo.Supervision; v == nil || v.Supervisor != "conv1" || todo.Origin != "side1" || len(v.DoneWhen) != 1 {
		t.Fatalf("ledger = %+v (origin %q)", todo.Supervision, todo.Origin)
	}

	// The supervision message goes only to the main conversation.
	agentTurn(s, f, proto.StatusWorking, "", 0)
	agentTurn(s, f, proto.StatusDone, "做完了，PR https://github.com/o/r/pull/7 已合并", 0)
	waitFor(t, "the main conversation's supervision message", func() bool {
		for _, n := range f.noticesTo("conv1") {
			if strings.Contains(n, "📋 督办") {
				return true
			}
		}
		return false
	})
	for _, n := range f.noticesTo("side1") {
		if strings.Contains(n, "📋 督办") {
			t.Fatalf("the side conversation got the supervision message:\n%s", n)
		}
	}

	// The origin cannot accept and close its own task; the main reviewer can, and the origin hears the outcome.
	if _, err := runJSON(t, s, "side1", "task_review", map[string]any{"todo_id": todo.ID, "verdict": "accepted"}); err == nil {
		t.Fatal("the side conversation accepted a main-supervised task")
	}
	if _, err := runJSON(t, s, "conv1", "task_review", map[string]any{"todo_id": todo.ID, "verdict": "accepted", "note": "PR 已合并",
		"checks": []map[string]any{{"criterion": "PR 合并到 main", "met": true, "evidence": "pull/7"}}}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the task closed", func() bool {
		t, ok := s.todoNow(todo.ID)
		return ok && t.DoneAt != nil
	})
	waitFor(t, "the origin's end notice", func() bool { return len(endNotices(f, "side1")) == 1 })
}

// Every conversation's plan defaults to the main conversation's supervision. Only a task leader's worker keeps its
// Agent supervisor; without a main conversation there is no conversation supervisor to assign (#5732).
func TestPlanSupervisorDefaults(t *testing.T) {
	s, f := newQuietService(t)
	ctx := context.Background()
	setSettings(t, s, func(v *Settings) { v.Leaders = []string{"lead1"} })
	f.setSessions("server", proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle},
		proto.Session{SessionID: "side1", MachineID: "server", Assistant: true, Status: proto.StatusIdle})
	for _, c := range []struct {
		conv, parent, want string
		ok                 bool
	}{
		{"side1", "", "conv1", true},
		{"gone", "", "conv1", true},
		{"conv1", "", "conv1", true},
		{"side1", "box/s1", "", false},
		{"", "", "", false},
		{"lead1", "", "conv1", true},
	} {
		if got, ok := s.defaultPlanSupervisor(ctx, c.conv, c.parent); got != c.want && ok || ok != c.ok {
			t.Errorf("defaultPlanSupervisor(%q, %q) = %q %v, want %q %v", c.conv, c.parent, got, ok, c.want, c.ok)
		}
	}
	setSettings(t, s, func(v *Settings) { v.Main = "" })
	if got, ok := s.defaultPlanSupervisor(ctx, "side1", ""); ok || got != "" {
		t.Fatalf("no main conversation: %q %v, want no supervisor", got, ok)
	}
}

// Open tasks written before universal main supervision are rehomed without losing their criteria. Explicit leader
// assignments and task-leader workers keep their specialized supervisor (#5732).
func TestOpenLegacySideTaskRehomesToMain(t *testing.T) {
	s, _, todo := newSupervisedService(t)
	ctx := context.Background()
	setSettings(t, s, func(v *Settings) { v.Leaders = []string{"lead1"} })
	if _, _, err := s.updateSupervision(ctx, todo.ID, func(v *Supervision) bool {
		v.Supervisor = "side1"
		return true
	}); err != nil {
		t.Fatal(err)
	}
	putTodo(s, Todo{ID: "explicit-leader", Origin: "lead1", Supervision: &Supervision{Supervisor: "lead1"}})
	putTodo(s, Todo{ID: "leader-worker", Origin: "conv1", Supervision: &Supervision{Supervisor: "box/leader-agent"}})
	// A pre-#5732 task_plan(supervisor=leader) is not the manual-Agent exception: its saved plan says so.
	if err := s.savePlan(ctx, &TaskPlan{ID: "old-leader-plan", Tasks: []PlanTask{{TodoID: "legacy-leader-plan", Supervisor: "lead1"}}}); err != nil {
		t.Fatal(err)
	}
	putTodo(s, Todo{ID: "legacy-leader-plan", Origin: "conv1", PlanID: "old-leader-plan", Supervision: &Supervision{Supervisor: "lead1"}})
	// A leader's old default plan has no explicit supervisor field; its saved conversation identifies the same topology.
	if err := s.savePlan(ctx, &TaskPlan{ID: "old-default-leader-plan", Conversation: "lead1", Tasks: []PlanTask{{TodoID: "legacy-default-leader-plan"}}}); err != nil {
		t.Fatal(err)
	}
	putTodo(s, Todo{ID: "legacy-default-leader-plan", Origin: "lead1", PlanID: "old-default-leader-plan", Supervision: &Supervision{Supervisor: "lead1"}})
	cfg := s.settings(ctx)
	if got := s.digestTo(ctx, cfg, superviseSignal{Todo: "legacy-leader-plan", Kind: sigReview}); got != "conv1" {
		t.Fatalf("legacy leader-plan still routes to %q", got)
	}
	if got := s.digestTo(ctx, cfg, superviseSignal{Todo: "explicit-leader", Kind: sigReview}); got != "lead1" {
		t.Fatalf("the explicit manual leader route changed to %q", got)
	}
	if got := s.digestTo(ctx, cfg, superviseSignal{Todo: "legacy-default-leader-plan", Kind: sigReview}); got != "conv1" {
		t.Fatalf("legacy default leader-plan still routes to %q", got)
	}
	s.rehomeSupervisedTodos(ctx)
	if got := todoNow(s, todo.ID).Supervision; got.Supervisor != "conv1" || len(got.DoneWhen) != 2 || got.ExpectMinutes != 30 {
		t.Fatalf("rehomed ledger = %+v", got)
	}
	if got := todoNow(s, "explicit-leader").Supervision.Supervisor; got != "lead1" {
		t.Fatalf("explicit leader changed to %q", got)
	}
	if got := todoNow(s, "leader-worker").Supervision.Supervisor; got != "box/leader-agent" {
		t.Fatalf("task-leader worker changed to %q", got)
	}
	if got := todoNow(s, "legacy-leader-plan").Supervision.Supervisor; got != "conv1" {
		t.Fatalf("legacy leader-plan did not rehome to main: %q", got)
	}
	if got := todoNow(s, "legacy-default-leader-plan").Supervision.Supervisor; got != "conv1" {
		t.Fatalf("legacy default leader-plan did not rehome to main: %q", got)
	}
}

// A legacy side's queued review was already acknowledged from the durable outbox. Rehoming its task must replay that
// exact signal for main, not leave an unusable review prompt behind in the old conversation (#5732).
func TestLegacySideQueuedReviewRehomesToMain(t *testing.T) {
	st := planUpdateStore{&planMemStore{memStore: &memStore{}, plans: map[string]db.TaskPlanRow{}}}
	s, f, todo := newSupervisedServiceOn(t, st)
	s.superviseTimes = superviseTimings{gather: 1, every: 1, urgent: time.Millisecond}
	ctx := context.Background()
	if _, _, err := s.updateSupervision(ctx, todo.ID, func(v *Supervision) bool {
		v.Supervisor, v.State = "side1", reviewPending
		return true
	}); err != nil {
		t.Fatal(err)
	}
	g := superviseSignal{Todo: todo.ID, Kind: sigReview, Key: "legacy-side-review", Detail: "legacy-review-marker-5732", At: time.Now()}
	s.signal(g)
	reserveUndelivered(t, s, g) // the old digest reserved dedup and took the signal out of memory before queue admission
	if !s.queueDigest("side1", relayMsg{From: superviseTool, Tool: superviseTool, Text: "queued legacy review for " + todo.ID, At: g.At},
		[]superviseSignal{g}, g.At, time.Time{}) {
		t.Fatal("the legacy side did not accept its queued review")
	}
	if got := storedSupervisePending(t, s); len(got) != 0 {
		t.Fatalf("queue admission did not acknowledge the old side's outbox signal: %+v", got)
	}

	s.rehomeSupervisedTodos(ctx)
	s.sendDigest(ctx)
	waitFor(t, "the rehomed review in the main conversation", func() bool {
		got := strings.Join(f.noticesTo("conv1"), "\n")
		return strings.Contains(got, todo.ID) && strings.Contains(got, "legacy-review-marker-5732")
	})
	if got := s.queuedFor("side1"); got != 0 {
		t.Fatalf("the old side still holds %d queued message(s)", got)
	}
	if got := strings.Join(f.noticesTo("side1"), "\n"); strings.Contains(got, "legacy-review-marker-5732") {
		t.Fatalf("the old side received the rehomed review:\n%s", got)
	}
	if _, err := runJSON(t, s, "conv1", "task_review", map[string]any{"todo_id": todo.ID, "verdict": "accepted", "note": "验收通过"}); err != nil {
		t.Fatalf("main could not accept the rehomed review: %v", err)
	}
	if got := todoNow(s, todo.ID); got.DoneAt == nil {
		t.Fatal("main's acceptance did not close the rehomed task")
	}
}

// A queued notice explicitly addressed to the configured legacy background is intentional compatibility delivery,
// not an orphaned side signal. Crash recovery must leave it there even though the ledger already names main (#5732).
func TestLegacyBackgroundQueuedReviewStaysWithBackground(t *testing.T) {
	s, f, todo := newSupervisedService(t)
	setSuperviseTimingsForTest(s, superviseTimings{gather: time.Hour, every: time.Hour, urgent: time.Hour})
	setSettings(t, s, func(v *Settings) { v.BackgroundSupervisor, v.BackgroundSupervisorMachineID = "bg1", "server" })
	f.setSessions("server",
		proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle},
		proto.Session{SessionID: "bg1", MachineID: "server", Assistant: true, Status: proto.StatusIdle})
	if _, _, err := s.updateSupervision(context.Background(), todo.ID, func(v *Supervision) bool {
		v.State = reviewPending
		return true
	}); err != nil {
		t.Fatal(err)
	}
	g := superviseSignal{Todo: todo.ID, Kind: sigReview, Key: "intentional-background-review", Detail: "legacy-background-marker", At: time.Now()}
	s.signal(g)
	reserveUndelivered(t, s, g)
	if !s.queueDigest("bg1", relayMsg{From: superviseTool, Tool: superviseTool, Text: "queued background review for " + todo.ID, At: g.At},
		[]superviseSignal{g}, g.At, time.Time{}) {
		t.Fatal("the configured legacy background did not accept its intentional queue entry")
	}

	s.rehomeSupervisedTodos(context.Background())
	if got := s.queuedFor("bg1"); got != 1 {
		t.Fatalf("crash recovery removed the intentional legacy-background entry: queue=%d", got)
	}
	if got := s.queuedFor("conv1"); got != 0 {
		t.Fatalf("the intentional background entry was duplicated to main: queue=%d", got)
	}
}

// A newer durable stretch can share its id with an older signal still queued for the legacy side. Restart loading must
// restore that outbox before replaying the queue, or the stale replay overwrites the only copy of the newer review.
func TestRehomeRestoreKeepsNewerDurableReview(t *testing.T) {
	st := planUpdateStore{&planMemStore{memStore: &memStore{}, plans: map[string]db.TaskPlanRow{}}}
	s, f, todo := newSupervisedServiceOn(t, st)
	s.superviseTimes = superviseTimings{gather: time.Hour, every: time.Hour, urgent: time.Hour}
	ctx := context.Background()
	if _, _, err := s.updateSupervision(ctx, todo.ID, func(v *Supervision) bool {
		v.Supervisor, v.State = "side1", reviewPending
		return true
	}); err != nil {
		t.Fatal(err)
	}
	old := superviseSignal{Todo: todo.ID, Kind: sigReview, Key: "old-queued-review", Detail: "old-queued-marker-5732", At: time.Now()}
	s.signal(old)
	reserveUndelivered(t, s, old)
	if !s.queueDigest("side1", relayMsg{From: superviseTool, Tool: superviseTool, Text: "queued old review", At: old.At},
		[]superviseSignal{old}, old.At, time.Time{}) {
		t.Fatal("the legacy side did not accept its queued review")
	}
	newer := superviseSignal{Todo: todo.ID, Kind: sigReview, Key: "newer-durable-review", Detail: "newer-durable-marker-5732", At: old.At.Add(time.Second)}
	s.signal(newer)
	if got := storedSupervisePending(t, s)[newer.id()]; got.Key != newer.Key {
		t.Fatalf("the newer stretch was not durable before restart: %+v", got)
	}

	simulateSupervisionRestart(t, s)
	reloadTodos(t, s) // the real first-load path rehomes legacy supervision and restores the durable outbox
	if got := storedSupervisePending(t, s)[newer.id()]; got.Key != newer.Key || got.Detail != newer.Detail {
		t.Fatalf("the queued replay replaced the newer durable stretch: %+v", got)
	}
	s.superviseTimes = superviseTimings{gather: 1, every: 1, urgent: time.Hour}
	s.sendDigest(ctx)
	waitFor(t, "the newer durable review after rehome", func() bool {
		return strings.Contains(strings.Join(f.noticesTo("conv1"), "\n"), newer.Detail)
	})
	if got := strings.Join(f.noticesTo("conv1"), "\n"); strings.Contains(got, old.Detail) {
		t.Fatalf("main received the stale queued review instead of only the newer stretch:\n%s", got)
	}
}

// The other task in a mixed digest is regathered too. Its older queued stretch must not replace a newer durable one.
func TestDropMixedDigestKeepsNewerDurableSignal(t *testing.T) {
	st := planUpdateStore{&planMemStore{memStore: &memStore{}, plans: map[string]db.TaskPlanRow{}}}
	s, _, todo := newSupervisedServiceOn(t, st)
	s.superviseTimes = superviseTimings{gather: time.Hour, every: time.Hour, urgent: time.Hour}
	other := Todo{ID: "other-task", Supervision: &Supervision{Supervisor: "side1", State: reviewPending}}
	putTodo(s, other)
	at := time.Now()
	target := superviseSignal{Todo: todo.ID, Kind: sigReview, Key: "target-old", Detail: "target", At: at}
	oldOther := superviseSignal{Todo: other.ID, Kind: sigReview, Key: "other-old", Detail: "other old", At: at}
	s.signal(target)
	s.signal(oldOther)
	if !s.queueDigest("side1", relayMsg{From: superviseTool, Tool: superviseTool, Text: "mixed", At: at},
		[]superviseSignal{target, oldOther}, at, time.Time{}) {
		t.Fatal("the mixed digest did not enter the queue")
	}
	newOther := superviseSignal{Todo: other.ID, Kind: sigReview, Key: "other-new", Detail: "other new", At: at.Add(time.Second)}
	s.signal(newOther)
	if got := storedSupervisePending(t, s)[newOther.id()]; got.Key != newOther.Key {
		t.Fatalf("the newer mixed-digest stretch was not durable: %+v", got)
	}

	simulateSupervisionRestart(t, s)
	n, taken := s.dropQueuedSupervisionTasks("side1", map[string]bool{todo.ID: true}, []Todo{todo, other})
	if n != 1 || len(taken) != 1 || taken[0].Key != target.Key {
		t.Fatalf("drop = %d, taken %+v", n, taken)
	}
	if got := storedSupervisePending(t, s)[newOther.id()]; got.Key != newOther.Key || got.Detail != newOther.Detail {
		t.Fatalf("regathering the mixed digest replaced the newer durable stretch: %+v", got)
	}
}

// A crash after the supervisor write but before queue reconciliation must still be recoverable on the next load.
func TestRehomeAfterSupervisorWriteRecoversQueuedReview(t *testing.T) {
	st := planUpdateStore{&planMemStore{memStore: &memStore{}, plans: map[string]db.TaskPlanRow{}}}
	s, f, todo := newSupervisedServiceOn(t, st)
	s.superviseTimes = superviseTimings{gather: time.Hour, every: time.Hour, urgent: time.Hour}
	ctx := context.Background()
	if _, _, err := s.updateSupervision(ctx, todo.ID, func(v *Supervision) bool {
		v.Supervisor, v.State = "side1", reviewPending
		return true
	}); err != nil {
		t.Fatal(err)
	}
	g := superviseSignal{Todo: todo.ID, Kind: sigReview, Key: "crash-after-supervisor", Detail: "crash-after-supervisor-marker", At: time.Now()}
	s.signal(g)
	reserveUndelivered(t, s, g)
	if !s.queueDigest("side1", relayMsg{From: superviseTool, Tool: superviseTool, Text: "queued", At: g.At},
		[]superviseSignal{g}, g.At, time.Time{}) {
		t.Fatal("the review did not enter the old side's queue")
	}
	if _, _, err := s.updateSupervision(ctx, todo.ID, func(v *Supervision) bool { v.Supervisor = "conv1"; return true }); err != nil {
		t.Fatal(err)
	}
	simulateSupervisionRestart(t, s) // the process stopped before rehomeQueuedSupervision ran

	s.rehomeSupervisedTodos(ctx)
	s.superviseTimes = superviseTimings{gather: 1, every: 1, urgent: time.Hour}
	s.sendDigest(ctx)
	waitFor(t, "the queued review recovered after the supervisor-write crash", func() bool {
		return strings.Contains(strings.Join(f.noticesTo("conv1"), "\n"), g.Detail)
	})
}

// relayWriteWatch records each durable relay state so a test can stop at any write and check that the review survives.
type relayWriteWatch struct {
	Store
	writes []relayState
}

func (s *relayWriteWatch) PutSetting(ctx context.Context, key string, v any) error {
	if key == relayKey {
		raw, err := json.Marshal(v)
		if err != nil {
			return err
		}
		var state relayState
		if err := json.Unmarshal(raw, &state); err != nil {
			return err
		}
		s.writes = append(s.writes, state)
	}
	return s.Store.PutSetting(ctx, key, v)
}

func relayHasSignal(state relayState, g superviseSignal) bool {
	for _, queue := range state.Queue {
		for _, msg := range queue {
			if msg.Signals == nil {
				continue
			}
			for _, queued := range *msg.Signals {
				if queued.id() == g.id() && queued.Key == g.Key {
					return true
				}
			}
		}
	}
	return false
}

// Queue removal/detach and the replay outbox entry must be one durable write: a stop between them loses the only copy.
func TestRehomeQueueAndReplayShareOneDurableWrite(t *testing.T) {
	st := &relayWriteWatch{Store: planUpdateStore{&planMemStore{memStore: &memStore{}, plans: map[string]db.TaskPlanRow{}}}}
	s, _, todo := newSupervisedServiceOn(t, st)
	s.superviseTimes = superviseTimings{gather: time.Hour, every: time.Hour, urgent: time.Hour}
	ctx := context.Background()
	if _, _, err := s.updateSupervision(ctx, todo.ID, func(v *Supervision) bool {
		v.Supervisor, v.State = "side1", reviewPending
		return true
	}); err != nil {
		t.Fatal(err)
	}
	g := superviseSignal{Todo: todo.ID, Kind: sigReview, Key: "atomic-rehome", Detail: "atomic rehome", At: time.Now()}
	s.signal(g)
	reserveUndelivered(t, s, g)
	if !s.queueDigest("side1", relayMsg{From: superviseTool, Tool: superviseTool, Text: "queued", At: g.At},
		[]superviseSignal{g}, g.At, time.Time{}) {
		t.Fatal("the review did not enter the old side's queue")
	}
	st.writes = nil

	s.rehomeSupervisedTodos(ctx)
	if len(st.writes) == 0 {
		t.Fatal("rehome wrote no relay state")
	}
	for i, state := range st.writes {
		_, pending := state.SupervisePending[g.id()]
		if !relayHasSignal(state, g) && !pending {
			t.Fatalf("relay write %d has neither the old queued signal nor its durable replay: stopping here loses it", i)
		}
	}
}

// A detached head suppresses another rehome replay, but selective /stop still needs its text's todo id once the head
// is no longer draining. Empty structured metadata must not make the old message impossible to remove.
func TestDetachedQueueHeadStillStopsByTodoID(t *testing.T) {
	s, _, todo := newSupervisedService(t)
	empty := []superviseSignal{}
	s.queueRelay("side1", relayMsg{From: superviseTool, Tool: superviseTool, Text: "detached review for " + todo.ID,
		Signals: &empty, At: time.Now()})
	n, taken := s.dropQueuedSupervisionTasks("side1", map[string]bool{todo.ID: true}, []Todo{todo})
	if n != 1 || len(taken) != 1 || taken[0].Todo != todo.ID || taken[0].Kind != sigUnjudged {
		t.Fatalf("detached /stop drop = %d, %+v", n, taken)
	}
	if got := s.queuedFor("side1"); got != 0 {
		t.Fatalf("the stopped task's detached text stayed queued: %d", got)
	}
}

// blockedAssistantDrain holds the old side's real drain without fakeHub's global lock, so rehome and main can proceed.
type blockedAssistantDrain struct {
	Hub
	started, release chan struct{}
	once             sync.Once
	mu               sync.Mutex
	texts            map[string][]string
}

func (h *blockedAssistantDrain) DeviceCall(ctx context.Context, p DeviceCall) (json.RawMessage, error) {
	if p.Method != DeviceAssistantSend {
		return h.Hub.DeviceCall(ctx, p)
	}
	var v struct {
		SessionID string `json:"session_id"`
		Text      string `json:"text"`
	}
	if err := json.Unmarshal(p.Params, &v); err != nil {
		return nil, err
	}
	h.mu.Lock()
	h.texts[v.SessionID] = append(h.texts[v.SessionID], v.Text)
	h.mu.Unlock()
	if v.SessionID == "side1" {
		h.once.Do(func() { close(h.started) })
		select {
		case <-h.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return json.Marshal(map[string]any{"ok": true})
}

func (h *blockedAssistantDrain) text(session string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return strings.Join(h.texts[session], "\n")
}

func (h *blockedAssistantDrain) count(session string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.texts[session])
}

// setSuperviseTimingsForTest waits out an in-flight digest and stops its timer before changing timings. Every
// production read is under at least one of these locks; the tests must not write the configuration concurrently.
func setSuperviseTimingsForTest(s *Service, timings superviseTimings) {
	b := &s.supervise
	b.dedupMu.Lock()
	defer b.dedupMu.Unlock()
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.timer != nil {
		b.timer.Stop()
	}
	b.timer, b.timerAt = nil, time.Time{}
	s.superviseTimes = timings
}

// A restart drops supervise.queued while the persisted relay queue survives. Even when the old side has already started
// draining its head, rehoming must replay that head's structured review for main rather than waiting for another restart.
func TestLegacySideDrainingReviewRehomesAfterRestart(t *testing.T) {
	st := planUpdateStore{&planMemStore{memStore: &memStore{}, plans: map[string]db.TaskPlanRow{}}}
	s, f, todo := newSupervisedServiceOn(t, st)
	s.superviseTimes = superviseTimings{gather: time.Hour, every: time.Hour, urgent: time.Hour}
	ctx := context.Background()
	if _, _, err := s.updateSupervision(ctx, todo.ID, func(v *Supervision) bool {
		v.Supervisor, v.State = "side1", reviewPending
		return true
	}); err != nil {
		t.Fatal(err)
	}
	g := superviseSignal{Todo: todo.ID, Kind: sigReview, Key: "legacy-draining-review", Detail: "legacy-draining-marker-5732", At: time.Now()}
	s.signal(g)
	reserveUndelivered(t, s, g)
	if !s.queueDigest("side1", relayMsg{From: superviseTool, Tool: superviseTool, Text: "queued legacy review for " + todo.ID, At: g.At},
		[]superviseSignal{g}, g.At, time.Time{}) {
		t.Fatal("the legacy side did not accept its queued review")
	}
	if got := storedSupervisePending(t, s); len(got) != 0 {
		t.Fatalf("queue admission did not acknowledge the old side's outbox signal: %+v", got)
	}
	simulateSupervisionRestart(t, s)

	h := &blockedAssistantDrain{Hub: f, started: make(chan struct{}), release: make(chan struct{}), texts: map[string][]string{}}
	setHub(s, h)
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(h.release) }) }
	defer unblock()
	main := proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle}
	side := proto.Session{SessionID: "side1", MachineID: "server", Assistant: true, Status: proto.StatusIdle}
	f.setSessions("server", main, side)
	s.drainRelays("server", []proto.Session{side})
	select {
	case <-h.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the old side did not start draining its queued review")
	}

	s.rehomeSupervisedTodos(ctx)
	// A second restart before main's send must not let the old draining head acknowledge the replayed outbox signal.
	simulateSupervisionRestart(t, s)
	s.restoreSupervisePending(ctx)
	if got := storedSupervisePending(t, s)[g.id()]; got.Key != g.Key {
		t.Fatalf("the old draining head acknowledged main's replay before delivery: %+v", got)
	}
	setSuperviseTimingsForTest(s, superviseTimings{gather: 1, every: 1, urgent: time.Hour})
	s.sendDigest(ctx)
	waitFor(t, "the draining side's review replayed to main", func() bool {
		got := h.text("conv1")
		return strings.Contains(got, todo.ID) && strings.Contains(got, "legacy-draining-marker-5732")
	})
	if got := strings.Count(h.text("conv1"), "legacy-draining-marker-5732"); got != 1 {
		t.Fatalf("main received the replayed marker %d times, want exactly one", got)
	}
	// The detached head stays behind a failed old-side send. Another restart must not reinterpret it as legacy text.
	before := h.count("conv1")
	setSuperviseTimingsForTest(s, superviseTimings{gather: time.Hour, every: time.Hour, urgent: time.Hour})
	simulateSupervisionRestart(t, s)
	s.rehomeSupervisedTodos(ctx)
	setSuperviseTimingsForTest(s, superviseTimings{gather: 1, every: 1, urgent: time.Hour})
	s.sendDigest(ctx)
	if got := h.count("conv1"); got != before {
		t.Fatalf("the detached head generated another main turn after restart: before %d, after %d", before, got)
	}
	if _, err := runJSON(t, s, "conv1", "task_review", map[string]any{"todo_id": todo.ID, "verdict": "accepted", "note": "验收通过"}); err != nil {
		t.Fatalf("main could not accept the review replayed during the old side's drain: %v", err)
	}
	if got := todoNow(s, todo.ID); got.DoneAt == nil {
		t.Fatal("main's acceptance did not close the rehomed task")
	}
}

// A pre-structured queue head has no Signals after restart. Its text's todo id is the compatible fallback even while
// that head is draining, so the rehomed task still reaches main instead of being stranded in the old conversation.
func TestLegacySideDrainingTextNoticeRehomesAfterRestart(t *testing.T) {
	st := planUpdateStore{&planMemStore{memStore: &memStore{}, plans: map[string]db.TaskPlanRow{}}}
	s, f, todo := newSupervisedServiceOn(t, st)
	s.superviseTimes = superviseTimings{gather: time.Hour, every: time.Hour, urgent: time.Hour}
	ctx := context.Background()
	if _, _, err := s.updateSupervision(ctx, todo.ID, func(v *Supervision) bool {
		v.Supervisor, v.State = "side1", reviewPending
		return true
	}); err != nil {
		t.Fatal(err)
	}
	g := superviseSignal{Todo: todo.ID, Kind: sigReview, Key: "legacy-text-review", Detail: "legacy text", At: time.Now()}
	s.signal(g)
	reserveUndelivered(t, s, g)
	s.queueRelay("side1", relayMsg{From: superviseTool, Tool: superviseTool, Text: "pre-structured review for " + todo.ID, At: g.At})
	s.ackSupervisePending([]superviseSignal{g}) // queue admission had acknowledged the old outbox before the restart
	simulateSupervisionRestart(t, s)

	h := &blockedAssistantDrain{Hub: f, started: make(chan struct{}), release: make(chan struct{}), texts: map[string][]string{}}
	setHub(s, h)
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(h.release) }) }
	defer unblock()
	main := proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle}
	side := proto.Session{SessionID: "side1", MachineID: "server", Assistant: true, Status: proto.StatusIdle}
	f.setSessions("server", main, side)
	s.drainRelays("server", []proto.Session{side})
	select {
	case <-h.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the old side did not start draining its pre-structured notice")
	}

	s.rehomeSupervisedTodos(ctx)
	setSuperviseTimingsForTest(s, superviseTimings{gather: 1, every: 1, urgent: time.Hour})
	s.sendDigest(ctx)
	waitFor(t, "the pre-structured draining notice replayed to main", func() bool {
		return strings.Contains(h.text("conv1"), todo.ID)
	})
	before := h.count("conv1")
	setSuperviseTimingsForTest(s, superviseTimings{gather: time.Hour, every: time.Hour, urgent: time.Hour})
	simulateSupervisionRestart(t, s)
	s.rehomeSupervisedTodos(ctx)
	setSuperviseTimingsForTest(s, superviseTimings{gather: 1, every: 1, urgent: time.Hour})
	s.sendDigest(ctx)
	if got := h.count("conv1"); got != before {
		t.Fatalf("the text fallback repeated after its head was detached: before %d, after %d", before, got)
	}
	if _, err := runJSON(t, s, "conv1", "task_review", map[string]any{"todo_id": todo.ID, "verdict": "accepted", "note": "验收通过"}); err != nil {
		t.Fatalf("main could not accept the pre-structured notice's task: %v", err)
	}
}

// Origin alone is not acceptance authority: an old task without a ledger still needs main's explicit acceptance.
func TestSideOriginCannotAcceptUnsupervisedLegacyTask(t *testing.T) {
	s, _ := newQuietService(t)
	ctx := context.Background()
	todo, err := s.createTodo(ctx, "旧 side 任务", "做完", "side1", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runJSON(t, s, "side1", "task_review", map[string]any{"todo_id": todo.ID, "verdict": "accepted"}); err == nil {
		t.Fatal("the side origin accepted and closed a task without main review")
	}
	if todoNow(s, todo.ID).DoneAt != nil {
		t.Fatal("the unauthorized side review closed the task")
	}
	if _, err := runJSON(t, s, "conv1", "task_review", map[string]any{"todo_id": todo.ID, "verdict": "accepted"}); err != nil {
		t.Fatalf("main could not accept the legacy task: %v", err)
	}
}

// The explicit manual-Agent exception is usable end to end: its leader can accept the task it supervises.
func TestManualLeaderCanAcceptAssignedAgentTask(t *testing.T) {
	s, _ := newQuietService(t)
	ctx := context.Background()
	setSettings(t, s, func(v *Settings) { v.Leaders = []string{"lead1"} })
	todo, err := s.createTodo(ctx, "手动 Agent 任务", "做完", "lead1", "")
	if err != nil {
		t.Fatal(err)
	}
	s.superviseTodo(ctx, todo.ID, "lead1", []string{"完成"}, 0)
	if _, err := runJSON(t, s, "lead1", "task_review", map[string]any{"todo_id": todo.ID, "verdict": "accepted"}); err != nil {
		t.Fatalf("the explicit manual leader could not review its task: %v", err)
	}
	if todoNow(s, todo.ID).DoneAt == nil {
		t.Fatal("the manual leader's acceptance did not close its task")
	}
}

// endNotices are the task-end notices sent into conversation id.
func endNotices(f *fakeHub, id string) []string {
	var out []string
	for _, n := range f.noticesTo(id) {
		if strings.Contains(n, "这段对话派出去的任务") {
			out = append(out, n)
		}
	}
	return out
}
