package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// storedSupervisePending reads the durable outbox without depending on its in-memory representation.
func storedSupervisePending(t *testing.T, s *Service) map[string]superviseSignal {
	t.Helper()
	var state struct {
		Pending map[string]superviseSignal `json:"supervise_pending"`
	}
	if _, err := s.store().Setting(context.Background(), relayKey, &state); err != nil {
		t.Fatal(err)
	}
	return state.Pending
}

// simulateSupervisionRestart clears memory only after digest and restoration can no longer use the old state.
// Replacing the whole book would replace its mutexes while timers still reference them.
func simulateSupervisionRestart(t *testing.T, s *Service) {
	t.Helper()
	b := &s.supervise
	b.pendingLoadMu.Lock()
	b.dedupMu.Lock()
	b.mu.Lock()
	if b.timer != nil {
		b.timer.Stop()
	}
	b.timer, b.timerAt = nil, time.Time{}
	b.pending, b.pendingLoaded, b.sent, b.dedup = nil, false, nil, nil
	b.queued, b.last, b.asks, b.hold, b.visible = nil, time.Time{}, nil, nil, nil
	b.legacyBackground, b.failSeen = nil, nil
	b.mu.Unlock()
	b.dedupMu.Unlock()
	b.pendingLoadMu.Unlock()
	s.relays.mu.Lock()
	s.relays.loaded = false
	s.relays.mu.Unlock()
}

// markLeaderTurnSignalled puts a terminated turn and its untold signal on the real turn ledger.
func markLeaderTurnSignalled(t *testing.T, s *Service, g superviseSignal) {
	t.Helper()
	last := proto.Session{MachineID: "box", SessionID: "s1", Status: proto.StatusIdle, UpdatedAt: time.Now().UTC()}
	if _, again := s.takeTurn(context.Background(), Todo{ID: g.Todo}, last, nil, turnClose{Key: "turn-" + g.Key}, true, time.Now().Add(-time.Hour), turnDone); again {
		t.Fatal("the terminated test turn was already taken")
	}
	if s.turnSignalled(g) {
		t.Fatal("the test turn signal was told before delivery")
	}
}

// requireLeaderTurnToldAfterRestart drops the in-memory ledger and requires the persisted told receipt to deduplicate.
func requireLeaderTurnToldAfterRestart(t *testing.T, s *Service, g superviseSignal) {
	t.Helper()
	waitFor(t, "the leader delivery's told receipt", func() bool {
		e, ok := s.turnOf(context.Background(), g.Todo)
		return ok && e.Signal != nil && e.Signal.Told != nil
	})
	b := &s.tasks.turns
	b.mu.Lock()
	b.loaded, b.doc = false, taskTurnsDoc{}
	b.mu.Unlock()
	if _, ok := s.turnOf(context.Background(), g.Todo); !ok { // the sweep loads the ledger before asking turnSignalled
		t.Fatal("the persisted turn is missing after restart")
	}
	if !s.turnSignalled(g) {
		e, ok := s.turnOf(context.Background(), g.Todo)
		t.Fatalf("the %s success path did not record the turn as told after restart: ok %v, turn %+v, signal %+v, original %+v", g.Key, ok, e, e.Signal, g)
	}
}

// A needs-user state produces no new watch signal. Its gathered notification must therefore survive a restart before
// the first digest, reach main, and leave the durable outbox only after real delivery or queue admission (#5732).
func TestNeedsUserPendingSurvivesRestartBeforeDigest(t *testing.T) {
	st := planUpdateStore{&planMemStore{memStore: &memStore{}, plans: map[string]db.TaskPlanRow{}}}
	s, f, todo := newSupervisedServiceOn(t, st)
	s.superviseTimes = superviseTimings{gather: time.Hour, every: time.Hour, urgent: time.Hour}
	ctx := context.Background()
	if _, _, err := s.updateSupervision(ctx, todo.ID, func(v *Supervision) bool {
		v.State = reviewNeedsUser
		return true
	}); err != nil {
		t.Fatal(err)
	}
	g := superviseSignal{Todo: todo.ID, Kind: sigIdle, Key: "owner-wait-1", Detail: "要用户决定：选择发布方式", At: time.Now()}
	s.signal(g)
	if got := storedSupervisePending(t, s)[g.id()]; got.Key != g.Key {
		t.Fatalf("the gathered needs-user signal was not persisted: %+v", got)
	}

	// A process restart loses the in-memory book and relay's loaded mark, not the shared store.
	simulateSupervisionRestart(t, s)
	s.superviseTimes = superviseTimings{gather: 1, every: 1, urgent: 1} // due immediately for the post-restart digest
	s.sendDigest(ctx)
	if got := strings.Join(f.noticesTo("conv1"), "\n"); !strings.Contains(got, todo.ID) || !strings.Contains(got, "选择发布方式") {
		t.Fatalf("the restored needs-user signal was not delivered to main:\n%s", got)
	}
	if got := storedSupervisePending(t, s); len(got) != 0 {
		t.Fatalf("the delivered signal stayed in the durable outbox: %+v", got)
	}
}

// A late acknowledgment for an older stretch cannot delete the newer one gathered under the same signal id.
func TestPendingAckKeepsNewerStretch(t *testing.T) {
	st := planUpdateStore{&planMemStore{memStore: &memStore{}, plans: map[string]db.TaskPlanRow{}}}
	s, _, todo := newSupervisedServiceOn(t, st)
	s.superviseTimes = superviseTimings{gather: time.Hour, every: time.Hour, urgent: time.Hour}
	old := superviseSignal{Todo: todo.ID, Kind: sigIdle, Key: "old", At: time.Now()}
	newer := superviseSignal{Todo: todo.ID, Kind: sigIdle, Key: "new", At: time.Now()}
	s.signal(old)
	s.signal(newer)
	s.ackSupervisePending([]superviseSignal{old})
	if got := storedSupervisePending(t, s)[newer.id()]; got.Key != newer.Key {
		t.Fatalf("the old acknowledgment removed the newer stretch: %+v", got)
	}
}

// A task leader's direct worker delivery uses the same durable acknowledgment as a conversation digest.
func TestTaskLeaderDeliveryAcknowledgesPending(t *testing.T) {
	st := planUpdateStore{&planMemStore{memStore: &memStore{}, plans: map[string]db.TaskPlanRow{}}}
	s, f, todo := newSupervisedServiceOn(t, st)
	s.superviseTimes = superviseTimings{gather: time.Hour, every: time.Hour, urgent: time.Hour}
	g := superviseSignal{Todo: todo.ID, Kind: sigStalled, Key: "worker-stall", At: time.Now()}
	s.signal(g)
	f.setSessions("box", proto.Session{MachineID: "box", SessionID: "s1", Status: proto.StatusWorking, State: proto.StateWorking})
	lead := TodoAgent{MachineID: "box", SessionID: "s1"}
	if !s.deliverToLeaderAgent(context.Background(), lead, []superviseSignal{g}, testWeb, time.Now(), time.Time{}) {
		t.Fatal("the online task leader did not accept its worker signal")
	}
	if got := storedSupervisePending(t, s); len(got) != 0 {
		t.Fatalf("the worker signal stayed pending after delivery: %+v", got)
	}
}

// Starting an offline leader's resume is not delivery: the outbox clears only after the blocked real send completes.
func TestOfflineTaskLeaderAcknowledgesOnlyAfterSend(t *testing.T) {
	st := planUpdateStore{&planMemStore{memStore: &memStore{}, plans: map[string]db.TaskPlanRow{}}}
	s, f, todo := newSupervisedServiceOn(t, st)
	s.superviseTimes = superviseTimings{gather: time.Hour, every: time.Hour, urgent: time.Hour}
	if _, _, err := s.updateSupervision(context.Background(), todo.ID, func(v *Supervision) bool { v.State = reviewPending; return true }); err != nil {
		t.Fatal(err)
	}
	g := superviseSignal{Todo: todo.ID, Kind: sigReview, Key: "offline-worker-stall", At: time.Now()}
	s.signal(g)
	markLeaderTurnSignalled(t, s, g)
	f.setSessions("box", proto.Session{MachineID: "box", SessionID: "s1", Status: proto.StatusOffline})
	started, release := make(chan struct{}), make(chan struct{})
	f.answerNode(DeviceSessionSend, func(_ string, _ json.RawMessage) (any, error) {
		close(started)
		<-release
		return map[string]any{"ok": true}, nil
	})
	lead := TodoAgent{MachineID: "box", SessionID: "s1"}
	if !s.deliverToLeaderAgent(context.Background(), lead, []superviseSignal{g}, testWeb, time.Now(), time.Time{}) {
		t.Fatal("the offline leader's resume was not started")
	}
	<-started
	if got := storedSupervisePending(t, s)[g.id()]; got.Key != g.Key {
		t.Fatal("the offline leader was acknowledged before its real send")
	}
	close(release)
	waitFor(t, "the offline leader's real-send acknowledgment", func() bool { return len(storedSupervisePending(t, s)) == 0 })
	requireLeaderTurnToldAfterRestart(t, s, g)
}

// An unreachable leader's signal is delivered as a decision above it; the original worker signal must not replay too.
func TestUnreachableLeaderEscalationReplacesPending(t *testing.T) {
	st := planUpdateStore{&planMemStore{memStore: &memStore{}, plans: map[string]db.TaskPlanRow{}}}
	s, f, todo := newSupervisedServiceOn(t, st)
	s.superviseTimes = superviseTimings{gather: 1, every: 1, urgent: 1}
	ctx := context.Background()
	if _, _, err := s.updateSupervision(ctx, todo.ID, func(v *Supervision) bool {
		v.Supervisor = "box/s1"
		return true
	}); err != nil {
		t.Fatal(err)
	}
	released := time.Now()
	putTodo(s, Todo{ID: "leader-task", Origin: "conv1", Supervision: &Supervision{Supervisor: "conv1"},
		Agents: []TodoAgent{{MachineID: "box", SessionID: "s1", Source: sourceLeader, ReleasedAt: &released}}})
	f.setSessions("box")
	g := superviseSignal{Todo: todo.ID, Kind: sigStalled, Key: "unreachable-leader", At: time.Now()}
	s.signal(g)
	s.sendDigest(ctx)
	if got := strings.Join(f.noticesTo("conv1"), "\n"); !strings.Contains(got, todo.ID) || !strings.Contains(got, "cannot be reached") {
		t.Fatalf("the leader's decision did not reach main:\n%s", got)
	}
	if got := storedSupervisePending(t, s); len(got) != 0 {
		t.Fatalf("the escalated worker signal remained for a duplicate restart delivery: %+v", got)
	}
}

// A retry after the first resume failure is a real delivery too, and acknowledges the worker's durable signal.
func TestLeaderRetrySuccessAcknowledgesPending(t *testing.T) {
	st := planUpdateStore{&planMemStore{memStore: &memStore{}, plans: map[string]db.TaskPlanRow{}}}
	s, f, todo := newSupervisedServiceOn(t, st)
	s.superviseTimes = superviseTimings{gather: time.Hour, every: time.Hour, urgent: time.Hour}
	g := superviseSignal{Todo: todo.ID, Kind: sigReview, Key: "retry-leader", At: time.Now()}
	s.signal(g)
	markLeaderTurnSignalled(t, s, g)
	setSettings(t, s, func(v *Settings) { v.Main = "" }) // nobody above: only the leader retry can deliver it
	calls := 0
	f.answerNode(DeviceSessionSend, func(_ string, _ json.RawMessage) (any, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("resume failed")
		}
		return map[string]any{"ok": true}, nil
	})
	s.leaderResume = time.Millisecond
	lead := TodoAgent{MachineID: "box", SessionID: "s1"}
	s.escalateLeaderMessage(lead, []superviseSignal{g}, "worker supervision")
	waitFor(t, "the successful leader retry's acknowledgment", func() bool { return len(storedSupervisePending(t, s)) == 0 })
	requireLeaderTurnToldAfterRestart(t, s, g)
}

// With no conversation above an unreachable leader, the current-process retry must keep both the durable signal and a
// real delivery attempt; removing it from pending and marking it sent would otherwise suppress delivery until restart.
func TestUnreachableLeaderWithoutUpperRetriesInCurrentProcess(t *testing.T) {
	st := planUpdateStore{&planMemStore{memStore: &memStore{}, plans: map[string]db.TaskPlanRow{}}}
	s, f, todo := newSupervisedServiceOn(t, st)
	s.superviseTimes = superviseTimings{gather: 1, every: 1, urgent: 5 * time.Millisecond}
	ctx := context.Background()
	if _, _, err := s.updateSupervision(ctx, todo.ID, func(v *Supervision) bool {
		v.Supervisor = "box/s1"
		return true
	}); err != nil {
		t.Fatal(err)
	}
	released := time.Now()
	leader := Todo{ID: "leader-task", Origin: "conv1", Supervision: &Supervision{},
		Agents: []TodoAgent{{MachineID: "box", SessionID: "s1", Source: sourceLeader, ReleasedAt: &released}}}
	putTodo(s, leader)
	f.setSessions("box") // the released leader has no reachable agent

	// sendDigest reads main at entry, then webBase before routing. Remove main between those reads so the real escalation
	// branch has nobody above it despite passing the entry guard. Restore main only after this attempt, before its retry.
	setMain := func(main string) {
		s.prefs.mu.Lock()
		v := *s.prefs.cached
		v.Main = main
		s.prefs.cached, s.prefs.at = &v, time.Now()
		s.prefs.gen++
		s.prefs.mu.Unlock()
	}
	f.answerApp(AppFeishu, FeishuStatusGet, func(json.RawMessage) (any, error) {
		setMain("")
		return FeishuStatus{}, nil
	})
	g := superviseSignal{Todo: todo.ID, Kind: sigStalled, Key: "retry-without-upper", At: time.Now()}
	if lead, handled, reachable := s.leaderRoute(g); !handled || reachable {
		t.Fatalf("test leader route = %+v, handled %v, reachable %v; want handled and unreachable", lead, handled, reachable)
	}
	s.signal(g)
	s.sendDigest(ctx)
	if got := s.settings(ctx).Main; got != "" {
		t.Fatalf("the test did not clear main during routing: %q", got)
	}
	if got := todoNow(s, todo.ID).Supervision.Supervisor; got != "box/s1" {
		t.Fatalf("the test worker was rehomed before escalation: supervisor %q", got)
	}
	kept := storedSupervisePending(t, s)[g.id()].Key == g.Key

	leader.Agents[0].ReleasedAt = nil // the same leader task has an active agent again; no hub restart
	putTodo(s, leader)
	setMain("conv1")
	f.setSessions("box", proto.Session{MachineID: "box", SessionID: "s1", Status: proto.StatusWorking, State: proto.StateWorking})
	delivered := false
	waitFor(t, "the current-process retry after the leader returns", func() bool {
		delivered = strings.Contains(strings.Join(deliveredTo(f, "s1"), "\n"), todo.ID)
		return delivered
	})
	waitFor(t, "the retried delivery's durable acknowledgment", func() bool { return len(storedSupervisePending(t, s)) == 0 })
	if !kept || !delivered {
		t.Fatalf("unreachable leader retry: durable signal kept %v, delivered %v, pending %+v", kept, delivered, storedSupervisePending(t, s))
	}
}

// Queue admission and outbox acknowledgment are one relay-state write. An older crash state with both is reconciled.
func TestQueuedSupervisionAndOutboxAreAtomic(t *testing.T) {
	st := planUpdateStore{&planMemStore{memStore: &memStore{}, plans: map[string]db.TaskPlanRow{}}}
	s, _, todo := newSupervisedServiceOn(t, st)
	s.superviseTimes = superviseTimings{gather: time.Hour, every: time.Hour, urgent: time.Hour}
	g := superviseSignal{Todo: todo.ID, Kind: sigStalled, Key: "queued-once", At: time.Now()}
	s.signal(g)
	signals := []superviseSignal{g}
	if s.queueRelay("conv1", relayMsg{Tool: superviseTool, Text: "queued", Signals: &signals, At: time.Now()}) != 1 {
		t.Fatal("the supervision message was not queued")
	}
	if got := storedSupervisePending(t, s); len(got) != 0 {
		t.Fatalf("queue admission left the same signal in the outbox: %+v", got)
	}
	// Reproduce the pre-fix crash window: both the durable queue and outbox contain the exact stretch.
	s.updateSupervisePending(func(pending map[string]superviseSignal) { pending[g.id()] = g })
	simulateSupervisionRestart(t, s)
	s.restoreSupervisePending(context.Background())
	if got := storedSupervisePending(t, s); len(got) != 0 {
		t.Fatalf("the queued signal was not reconciled from the outbox: %+v", got)
	}
	s.supervise.mu.Lock()
	_, restored := s.supervise.pending[g.id()]
	s.supervise.mu.Unlock()
	if restored {
		t.Fatal("the queued signal was restored for a second delivery")
	}
}

// A real send succeeded but its ACK was lost. On restart an offline dedup hit retries the real send instead of stranding it.
func TestOfflineLeaderDedupHitRetriesPending(t *testing.T) {
	st := planUpdateStore{&planMemStore{memStore: &memStore{}, plans: map[string]db.TaskPlanRow{}}}
	s, f, todo := newSupervisedServiceOn(t, st)
	s.superviseTimes = superviseTimings{gather: time.Hour, every: time.Hour, urgent: time.Hour}
	g := superviseSignal{Todo: todo.ID, Kind: sigStalled, Key: "dedup-after-restart", At: time.Now()}
	f.setSessions("box", proto.Session{MachineID: "box", SessionID: "s1", Status: proto.StatusWorking, State: proto.StateWorking})
	s.signal(g)
	lead := TodoAgent{MachineID: "box", SessionID: "s1"}
	if !s.deliverToLeaderAgent(context.Background(), lead, []superviseSignal{g}, testWeb, time.Now(), time.Time{}) {
		t.Fatal("the initial real send failed")
	}
	s.updateSupervisePending(func(pending map[string]superviseSignal) { pending[g.id()] = g }) // ACK lost after that send
	f.setSessions("box", proto.Session{MachineID: "box", SessionID: "s1", Status: proto.StatusOffline})
	simulateSupervisionRestart(t, s)
	s.restoreSupervisePending(context.Background())
	s.supervise.mu.Lock()
	s.supervise.pending = map[string]superviseSignal{} // sendDigest took the stretch out for this delivery attempt
	s.supervise.mu.Unlock()
	if !s.deliverToLeaderAgent(context.Background(), lead, []superviseSignal{g}, testWeb, time.Now(), time.Time{}) {
		t.Fatal("the deduplicated offline signal did not enter its real-send retry")
	}
	waitFor(t, "the offline dedup retry's real-send acknowledgment", func() bool { return len(storedSupervisePending(t, s)) == 0 })
}

// reserveUndelivered simulates a crash after dedup was reserved but before any delivery or queue admission.
func reserveUndelivered(t *testing.T, s *Service, g superviseSignal) {
	t.Helper()
	entry, ok := s.digestEntry(context.Background(), g, testWeb)
	if !ok {
		t.Fatal("the test signal is not renderable")
	}
	s.supervise.dedupMu.Lock()
	defer s.supervise.dedupMu.Unlock()
	// sendDigest takes the stretch out of the in-memory gather before reserving it; only the durable outbox then
	// records that it has not been delivered or admitted to the relay queue.
	s.supervise.mu.Lock()
	delete(s.supervise.pending, g.id())
	s.supervise.sent[g.id()] = g.Key
	s.supervise.mu.Unlock()
	_, _, _, err := s.reserveSupervision(context.Background(), []prompts.SuperviseEntry{entry}, []superviseSignal{g}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
}

// A dedup reservation is not delivery evidence: a still-pending needs-user signal must be delivered after restart.
func TestConversationDedupReservationDoesNotErasePending(t *testing.T) {
	st := planUpdateStore{&planMemStore{memStore: &memStore{}, plans: map[string]db.TaskPlanRow{}}}
	s, f, todo := newSupervisedServiceOn(t, st)
	s.superviseTimes = superviseTimings{gather: time.Hour, every: time.Hour, urgent: time.Hour}
	g := superviseSignal{Todo: todo.ID, Kind: sigIdle, Key: "reserved-not-delivered", Detail: "要用户决定：选择发布方式", At: time.Now()}
	s.signal(g)
	reserveUndelivered(t, s, g)
	simulateSupervisionRestart(t, s)
	s.superviseTimes = superviseTimings{gather: 1, every: 1, urgent: 1}
	s.sendDigest(context.Background())
	if got := strings.Join(f.noticesTo("conv1"), "\n"); !strings.Contains(got, "选择发布方式") {
		t.Fatalf("a dedup reservation erased the undelivered signal:\n%s", got)
	}
	if got := storedSupervisePending(t, s); len(got) != 0 {
		t.Fatalf("the delivered signal stayed pending: %+v", got)
	}
}

// A newer stretch can replace the outbox after an older reservation but before delivery. Its different Key must not
// make that unproven reservation look like a successful send after a restart.
func TestNewerPendingAfterUndeliveredReservationIsNotDeduplicated(t *testing.T) {
	st := planUpdateStore{&planMemStore{memStore: &memStore{}, plans: map[string]db.TaskPlanRow{}}}
	s, f, todo := newSupervisedServiceOn(t, st)
	s.superviseTimes = superviseTimings{gather: time.Hour, every: time.Hour, urgent: time.Hour}
	first := superviseSignal{Todo: todo.ID, Kind: sigIdle, Key: "reserved-old", Detail: "要用户决定：选择发布方式", At: time.Now()}
	s.signal(first)
	reserveUndelivered(t, s, first)
	newer := first
	newer.Key = "pending-new"
	s.signal(newer)
	simulateSupervisionRestart(t, s)
	s.superviseTimes = superviseTimings{gather: 1, every: 1, urgent: 1}
	s.sendDigest(context.Background())
	if got := strings.Join(f.noticesTo("conv1"), "\n"); !strings.Contains(got, newer.Detail) {
		t.Fatalf("the older undelivered reservation hid its newer pending replacement:\n%s", got)
	}
	if got := storedSupervisePending(t, s); len(got) != 0 {
		t.Fatalf("the delivered replacement stayed pending: %+v", got)
	}
}

// A replacement gathered after the predecessor's real send but before its ACK is an ordinary duplicate. The ACK must
// remove that predecessor's retry evidence without acknowledging the newer stretch itself.
func TestDeliveredPredecessorDoesNotWakeReplacement(t *testing.T) {
	st := planUpdateStore{&planMemStore{memStore: &memStore{}, plans: map[string]db.TaskPlanRow{}}}
	s, f, todo := newSupervisedServiceOn(t, st)
	s.superviseTimes = superviseTimings{gather: time.Hour, every: time.Hour, urgent: time.Hour}
	first := superviseSignal{Todo: todo.ID, Kind: sigIdle, Key: "sent-old", Detail: "要用户决定：选择发布方式", At: time.Now()}
	s.signal(first)
	reserveUndelivered(t, s, first)
	newer := first
	newer.Key = "replacement-after-send"
	s.signal(newer)
	s.ackSupervisePending([]superviseSignal{first}) // the real send succeeded; only its ACK was delayed
	s.superviseTimes = superviseTimings{gather: 1, every: 1, urgent: 1}
	s.sendDigest(context.Background())
	if got := strings.Join(f.noticesTo("conv1"), "\n"); got != "" {
		t.Fatalf("the delivered predecessor's replacement woke main again:\n%s", got)
	}
	if got := storedSupervisePending(t, s); len(got) != 0 {
		t.Fatalf("the deduplicated replacement stayed pending: %+v", got)
	}
}

// One member's dedup hit cannot hide it in a mixed batch nor let another member's delivery acknowledge it.
func TestMixedDedupBatchDeliversEveryPending(t *testing.T) {
	st := planUpdateStore{&planMemStore{memStore: &memStore{}, plans: map[string]db.TaskPlanRow{}}}
	s, f, todo := newSupervisedServiceOn(t, st)
	s.superviseTimes = superviseTimings{gather: time.Hour, every: time.Hour, urgent: time.Hour}
	old := superviseSignal{Todo: todo.ID, Kind: sigIdle, Key: "mixed-old", Detail: "旧的用户决定", At: time.Now()}
	newer := superviseSignal{Todo: todo.ID, Kind: sigUnjudged, Key: "mixed-new", Detail: "新的未判断停止", At: time.Now()}
	s.signal(old)
	reserveUndelivered(t, s, old)
	s.signal(newer)
	// The first stretch was already extracted for its undelivered attempt; a restart restores both durable members.
	simulateSupervisionRestart(t, s)
	s.superviseTimes = superviseTimings{gather: 1, every: 1, urgent: 1}
	s.sendDigest(context.Background())
	got := strings.Join(f.noticesTo("conv1"), "\n")
	if !strings.Contains(got, old.Detail) || !strings.Contains(got, newer.Detail) {
		t.Fatalf("the mixed batch dropped a pending member:\n%s", got)
	}
	if got := storedSupervisePending(t, s); len(got) != 0 {
		t.Fatalf("the mixed batch left delivered members pending: %+v", got)
	}
}

// Task leaders use the same rule for mixed batches, including an older dedup-reserved member.
func TestTaskLeaderMixedDedupBatchDeliversEveryPending(t *testing.T) {
	st := planUpdateStore{&planMemStore{memStore: &memStore{}, plans: map[string]db.TaskPlanRow{}}}
	s, f, todo := newSupervisedServiceOn(t, st)
	s.superviseTimes = superviseTimings{gather: time.Hour, every: time.Hour, urgent: time.Hour}
	f.setSessions("box", proto.Session{MachineID: "box", SessionID: "s1", Status: proto.StatusWorking, State: proto.StateWorking})
	old := superviseSignal{Todo: todo.ID, Kind: sigIdle, Key: "leader-mixed-old", Detail: "旧的 worker 决定", At: time.Now()}
	newer := superviseSignal{Todo: todo.ID, Kind: sigUnjudged, Key: "leader-mixed-new", Detail: "新的 worker 停止", At: time.Now()}
	s.signal(old)
	reserveUndelivered(t, s, old)
	s.signal(newer)
	lead := TodoAgent{MachineID: "box", SessionID: "s1"}
	if !s.deliverToLeaderAgent(context.Background(), lead, []superviseSignal{old, newer}, testWeb, time.Now(), time.Time{}) {
		t.Fatal("the mixed leader batch was not delivered")
	}
	messages := deliveredTo(f, "s1")
	if len(messages) == 0 || !strings.Contains(messages[len(messages)-1], old.Detail) || !strings.Contains(messages[len(messages)-1], newer.Detail) {
		t.Fatalf("the leader's mixed batch dropped a member: %q", messages)
	}
	if got := storedSupervisePending(t, s); len(got) != 0 {
		t.Fatalf("the leader's mixed batch left delivered members pending: %+v", got)
	}
}

// /stop silences a reporting task's gathered notices even when the manager restarts before the stop (#3308, #5732).
func TestStopReportingDropsPersistedPending(t *testing.T) {
	st := planUpdateStore{&planMemStore{memStore: &memStore{}, plans: map[string]db.TaskPlanRow{}}}
	s, f := newQuietServiceOn(t, st)
	ctx := context.Background()
	f.known["lark1"] = true
	f.setSessions("server", proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle},
		proto.Session{SessionID: "lark1", MachineID: "server", Assistant: true, Status: proto.StatusWorking})
	s.markReporter(ctx, "server", "lark1")
	if _, err := s.toolTaskPlan(ctx, "lark1", planArgs(true)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the reporting task's agent", func() bool { return len(onlyTodo(s).Agents) == 1 })
	agentTurn(s, f, proto.StatusWorking, "", 0)
	s.superviseTimes = superviseTimings{gather: time.Hour, every: time.Hour, urgent: time.Hour}
	todo := onlyTodo(s)
	g := superviseSignal{Todo: todo.ID, Kind: sigUnjudged, Key: "before-stop", At: time.Now()}
	s.signal(g)
	if got := storedSupervisePending(t, s)[g.id()]; got.Key != g.Key {
		t.Fatal("the test signal was not persisted")
	}
	simulateSupervisionRestart(t, s)
	if w := serve(s, "POST", "/api/v1/assistant/conversations/lark1/stop", ""); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if got := storedSupervisePending(t, s); len(got) != 0 {
		t.Fatalf("the stopped task's persisted signal survived: %+v", got)
	}
}
