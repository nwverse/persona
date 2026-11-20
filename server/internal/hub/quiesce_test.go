package hub

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/extwire"
	"github.com/nwverse/persona/server/internal/proto"
)

func TestWriteQuiescerWaitsForInFlightWrites(t *testing.T) {
	var q writeQuiescer
	if !q.begin() {
		t.Fatal("a write was refused before any pause")
	}
	paused := make(chan error, 1)
	go func() { paused <- q.pause(context.Background()) }()
	select {
	case err := <-paused:
		t.Fatalf("pause returned with a write in flight: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	if q.begin() {
		t.Fatal("a write was admitted while paused")
	}
	q.end()
	select {
	case err := <-paused:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("pause did not return after the write finished")
	}
	q.resume()
	if !q.begin() {
		t.Fatal("writes did not resume")
	}
	q.end()
}

func TestWriteQuiescerPauseCanBeCancelled(t *testing.T) {
	var q writeQuiescer
	if !q.begin() {
		t.Fatal("a write was refused before any pause")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := q.pause(ctx); err == nil {
		t.Fatal("pause ignored its cancelled context")
	}
	q.end()
	q.resume()
}

func TestAssistantGatePausesAndResumes(t *testing.T) {
	var g assistantGate
	ctx := context.Background()
	if err := g.pause(ctx); err != nil {
		t.Fatal(err)
	}
	resumed := make(chan bool, 1)
	go func() { resumed <- g.waitResumed(ctx) }()
	select {
	case <-resumed:
		t.Fatal("the supervisor resumed while paused")
	case <-time.After(30 * time.Millisecond):
	}
	if g.register(make(chan struct{}), func() {}) {
		t.Fatal("a child was registered while paused")
	}
	g.start()
	select {
	case ok := <-resumed:
		if !ok {
			t.Fatal("the supervisor did not resume")
		}
	case <-time.After(time.Second):
		t.Fatal("resume did not wake the supervisor")
	}

	// A running child is stopped and pause waits for its exit.
	exit := make(chan struct{})
	cancelled := make(chan struct{})
	if !g.register(exit, func() { close(cancelled) }) {
		t.Fatal("a child was not registered")
	}
	done := make(chan error, 1)
	go func() { done <- g.pause(ctx) }()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("pause did not cancel the child")
	}
	select {
	case err := <-done:
		t.Fatalf("pause returned before the child exited: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	if paused := g.unregister(); !paused {
		t.Fatal("the supervisor lost the pause")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	g.start()
}

// A report that arrived while a snapshot paused writes is written by the
// resume itself: the pending workers are only woken once writes are admitted.
func TestDevelopmentResumeWritesPendingExtReport(t *testing.T) {
	h := newTestHub(t)
	store := newMeshStore()
	h.store = store
	ctx := context.Background()
	if err := h.quiesce.pause(ctx); err != nil {
		t.Fatal(err)
	}
	defer h.quiesce.resume()
	want := extwire.Report{Features: []string{"fixture-report"}}
	h.queueExtReportSave(want)
	waitFor(t, "the paused report to stay pending", func() bool {
		h.ext.saveMu.Lock()
		defer h.ext.saveMu.Unlock()
		return h.ext.savePending != nil && !h.ext.saveRunning
	})
	h.resumeDevelopmentWriters()
	waitFor(t, "the pending report to be written after resume", func() bool {
		var got extwire.Report
		ok, err := store.Setting(ctx, extReportKey, &got)
		return err == nil && ok && slices.Equal(got.Features, want.Features)
	})
}

// A device session that ends while a snapshot paused writes keeps its end for
// the resume, and one that starts then records no row.
func TestDevelopmentPauseDefersDeviceSessionWrites(t *testing.T) {
	h := newTestHub(t)
	store := &deviceSessionWrites{meshStore: newMeshStore()}
	h.store = store
	ctx := context.Background()
	if err := h.quiesce.pause(ctx); err != nil {
		t.Fatal(err)
	}
	defer h.quiesce.resume()
	h.finishDeviceSession(7, time.Now(), "closed", 30)
	if store.finished != 0 {
		t.Fatal("a device session was finished while writes were paused")
	}
	// A connection that arrives now is served without a session row.
	r := httptest.NewRequest(http.MethodGet, "/api/v1/ws", nil)
	r = r.WithContext(context.WithValue(r.Context(), principalKey{}, principal{ID: "dev_fixture", Role: roleFull}))
	if c := h.deviceSessionConnected(r, &client{}); c != nil {
		t.Fatal("a device session was inserted while writes were paused")
	}
	if store.inserted != 0 {
		t.Fatal("a device session row was written while writes were paused")
	}
	h.resumeDevelopmentWriters()
	waitFor(t, "the deferred session end to be written", func() bool {
		store.mu.Lock()
		defer store.mu.Unlock()
		return store.finished == 1
	})
	if store.finishID != 7 || store.finishReason != "closed" {
		t.Fatalf("finish = %d %q", store.finishID, store.finishReason)
	}
}

// deviceSessionWrites counts the device-session writes that reach the store.
type deviceSessionWrites struct {
	*meshStore
	inserted, finished int
	finishID           int64
	finishReason       string
}

func (s *deviceSessionWrites) InsertDeviceSession(context.Context, db.DeviceSession) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inserted++
	return int64(s.inserted), nil
}

func (s *deviceSessionWrites) FinishDeviceSession(_ context.Context, id int64, _ time.Time, reason string, _ int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finished++
	s.finishID, s.finishReason = id, reason
	return nil
}

// Every route outside the apps' API that writes the hub's database is refused
// while a snapshot pauses writes: signing in (password and browser), trading a
// manager's install code, and the admin pages.
func TestDevelopmentPauseRefusesOuterWriteRoutes(t *testing.T) {
	h := newTestHub(t)
	h.store = newMeshStore()
	mux := http.NewServeMux()
	h.passwordLoginRoutes(mux)
	h.browserLoginRoutes(mux)
	h.managerJoinRoutes(mux)
	setAdmin(h, AdminConfig{Access: accessAny})
	h.adminRoutes(mux, http.NotFoundHandler(), http.NotFoundHandler())
	ctx := context.Background()
	if err := h.quiesce.pause(ctx); err != nil {
		t.Fatal(err)
	}
	defer h.quiesce.resume()
	for _, path := range []string{
		"/api/v1/auth/password/finish",
		"/api/v1/auth/browser-login/approve",
		"/api/v1/bus/join",
		"/api/v1/admin/network/access",
	} {
		t.Run(path, func(t *testing.T) {
			method := http.MethodPost
			if strings.HasPrefix(path, "/api/v1/admin/") {
				method = http.MethodPut
			}
			r := httptest.NewRequest(method, path, strings.NewReader("{}"))
			r.Header.Set("Content-Type", "application/json")
			r.TLS = &tls.ConnectionState{} // not the plaintext refusal
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			if w.Code != http.StatusServiceUnavailable {
				t.Fatalf("%s -> %d: %s", path, w.Code, w.Body.String())
			}
		})
	}
}

// Finding #1: authentication still runs before quiesceGate (strangers get 401,
// not 503), authenticated requests skip TouchDeviceToken while paused, and an
// already-launched TouchDeviceToken finishes before pause returns.
func TestDevelopmentPauseSkipsAndWaitsForDeviceTokenTouch(t *testing.T) {
	h := newTestHub(t)
	tok := "jd_fixture_token_touch_test"
	store := &tokenTouchStore{
		meshStore: newMeshStore(),
		token:     &db.DeviceToken{ID: "dev_touch", Name: "Phone", Kind: "phone", Role: roleFull},
		hash:      hashToken(tok),
	}
	h.store = store
	handler := h.auth(h.quiesceGate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))

	// Part A: while paused, strangers still get 401 (gate is after auth),
	// authenticated GET succeeds without calling TouchDeviceToken, and after
	// resume the next GET does touch the token.
	ctx := context.Background()
	if err := h.quiesce.pause(ctx); err != nil {
		t.Fatal(err)
	}
	// Stranger POST -> 401, not 503.
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/example", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("stranger during pause -> %d, want 401", w.Code)
	}
	// Authenticated GET -> 200, no TouchDeviceToken write.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/example", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("authenticated GET during pause -> %d, want 200", w.Code)
	}
	time.Sleep(25 * time.Millisecond)
	if n := store.touches.Load(); n != 0 {
		t.Fatalf("TouchDeviceToken ran %d times while paused", n)
	}
	// Authenticated POST -> 503, still no TouchDeviceToken write.
	reqPost := httptest.NewRequest(http.MethodPost, "/api/v1/example", nil)
	reqPost.Header.Set("Authorization", "Bearer "+tok)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, reqPost)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("authenticated POST during pause -> %d, want 503", w.Code)
	}
	time.Sleep(25 * time.Millisecond)
	if n := store.touches.Load(); n != 0 {
		t.Fatalf("TouchDeviceToken ran %d times after refused POST while paused", n)
	}

	// Part B: after resume, an authenticated GET launches TouchDeviceToken, and
	// a concurrent pause waits until that in-flight touch completes.
	h.quiesce.resume()
	started := make(chan struct{})
	release := make(chan struct{})
	store.onTouch = func() {
		close(started)
		<-release
	}
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/example", nil)
	req2.Header.Set("Authorization", "Bearer "+tok)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req2)
	if w.Code != http.StatusOK {
		t.Fatalf("GET after resume -> %d", w.Code)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("TouchDeviceToken did not run after resume")
	}
	paused := make(chan error, 1)
	go func() { paused <- h.quiesce.pause(ctx) }()
	select {
	case err := <-paused:
		t.Fatalf("pause returned while TouchDeviceToken was still in flight: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-paused:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("pause did not return after TouchDeviceToken completed")
	}
	h.quiesce.resume()
}

type tokenTouchStore struct {
	*meshStore
	token   *db.DeviceToken
	hash    string
	touches atomic.Int32
	onTouch func()
}

func (s *tokenTouchStore) DeviceTokenByHash(_ context.Context, hash string) (*db.DeviceToken, error) {
	if hash == s.hash {
		cp := *s.token
		return &cp, nil
	}
	return nil, nil
}

func (s *tokenTouchStore) TouchDeviceToken(_ context.Context, _ string) error {
	s.touches.Add(1)
	if s.onTouch != nil {
		s.onTouch()
	}
	return nil
}

// Finding #2: timed and background Store writers defer due work while paused
// and wake on resume without performing any Store write during the pause.
func TestDevelopmentPauseDefersTimedBackgroundWriters(t *testing.T) {
	t.Run("lock tests", func(t *testing.T) {
		h := newTestHub(t)
		due := time.Now().Add(-time.Second)
		store := &bgWriteStore{meshStore: newMeshStore(), lockTestDue: &due}
		h.store = store
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		if err := h.quiesce.pause(ctx); err != nil {
			t.Fatal(err)
		}
		stop, err := h.startLockTests(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer stop()
		time.Sleep(40 * time.Millisecond)
		if n := store.lockTestAttempts.Load(); n != 0 {
			t.Fatalf("AttemptLockTest ran %d times while paused", n)
		}
		h.resumeDevelopmentWriters()
		waitFor(t, "due lock test dispatched after resume", func() bool {
			return store.lockTestAttempts.Load() >= 1
		})
	})

	t.Run("machine entries", func(t *testing.T) {
		prev := machineEntriesDelay
		machineEntriesDelay = 10 * time.Millisecond
		defer func() { machineEntriesDelay = prev }()

		h := newTestHub(t)
		store := &bgWriteStore{meshStore: newMeshStore()}
		h.store = store
		h.devices.mu.Lock()
		h.devices.keptRead = true
		h.devices.reportedRead = true
		h.devices.reported = reportedMachines{"assistant": {"m1": {Cfg: json.RawMessage(`{"id":"m1"}`)}}}
		h.devices.mu.Unlock()
		ctx := context.Background()
		if err := h.quiesce.pause(ctx); err != nil {
			t.Fatal(err)
		}
		h.machineEntriesChanged()
		time.Sleep(40 * time.Millisecond)
		if store.settingWrites(machineEntriesKey) != 0 {
			t.Fatal("machine entries wrote to store while paused")
		}
		h.resumeDevelopmentWriters()
		waitFor(t, "deferred machine entries written after resume", func() bool {
			return store.settingWrites(machineEntriesKey) >= 1
		})
	})

	t.Run("live activity claim hold and save", func(t *testing.T) {
		h := newTestHub(t)
		store := &bgWriteStore{meshStore: newMeshStore()}
		h.store = store
		la := h.live
		ctx := context.Background()
		if err := h.quiesce.pause(ctx); err != nil {
			t.Fatal(err)
		}
		now := time.Now()
		if _, claimed := la.claimStart(db.LiveActivity{DeviceID: "dev1", PushToStartToken: "tok"}, now); claimed {
			t.Fatal("claimStart succeeded while paused")
		}
		la.holdStart(context.Background(), "dev1", now, fleetRefresh)
		la.update("dev1", func(v *db.LiveActivity) { v.PushToken = "" })
		if store.laClaims.Load() != 0 || store.laHolds.Load() != 0 || store.laSaves.Load() != 0 {
			t.Fatalf("live activity wrote while paused: claims=%d holds=%d saves=%d",
				store.laClaims.Load(), store.laHolds.Load(), store.laSaves.Load())
		}
		h.resumeDevelopmentWriters()
		waitFor(t, "deferred live activity hold and save flushed after resume", func() bool {
			return store.laHolds.Load() >= 1 && store.laSaves.Load() >= 1
		})
		la.mu.Lock()
		repair := la.repair
		la.mu.Unlock()
		select {
		case <-la.signal:
		default:
			if !repair {
				t.Fatal("live activity loop was not woken after deferred claimStart")
			}
		}
	})

	t.Run("client ops maintenance", func(t *testing.T) {
		prev := clientOpsCleanupEvery
		clientOpsCleanupEvery = 10 * time.Millisecond
		defer func() { clientOpsCleanupEvery = prev }()

		h := newTestHub(t)
		store := &bgWriteStore{meshStore: newMeshStore()}
		h.store = store
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		if err := h.quiesce.pause(ctx); err != nil {
			t.Fatal(err)
		}
		if err := h.startClientOps(ctx); err != nil {
			t.Fatal(err)
		}
		time.Sleep(40 * time.Millisecond)
		if n := store.clientOpCleanups.Load(); n != 0 {
			t.Fatalf("CleanupClientOps ran %d times while paused", n)
		}
		h.resumeDevelopmentWriters()
		waitFor(t, "deferred client ops cleanup ran after resume", func() bool {
			return store.clientOpCleanups.Load() >= 1
		})
	})

	t.Run("push maintenance and held pushes", func(t *testing.T) {
		prev := pushDeviceMaintenance
		pushDeviceMaintenance = 10 * time.Millisecond
		defer func() { pushDeviceMaintenance = prev }()

		h := newTestHub(t)
		store := &bgWriteStore{meshStore: newMeshStore()}
		h.store = store
		h.push = newPusher(t.TempDir(), store, func(string, ...any) {})
		h.push.quiesce = &h.quiesce
		h.push.web = newWebPusher(store, func(string, ...any) {})
		h.push.web.quiesce = &h.quiesce
		// Reset counters after startup reconciliation in newPusher.
		store.pushReconciles.Store(0)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		if err := h.quiesce.pause(ctx); err != nil {
			t.Fatal(err)
		}
		go h.push.maintenanceLoop(ctx)
		h.push.recordIntercept(heldPushRecord{At: time.Now(), Title: "held"})
		// GET /api/v1/push/web must not persist a new VAPID key while paused.
		if _, err := h.push.web.key(context.Background()); err == nil {
			t.Fatal("webpush key creation succeeded while paused")
		}
		time.Sleep(40 * time.Millisecond)
		if n := store.pushReconciles.Load(); n != 0 {
			t.Fatalf("ReconcilePushDevices ran %d times while paused", n)
		}
		if store.settingWrites(heldPushesSettingKey) != 0 {
			t.Fatal("saveIntercepts wrote while paused")
		}
		if store.settingWrites(webPushKeysSetting) != 0 {
			t.Fatal("webpush key wrote while paused")
		}
		h.resumeDevelopmentWriters()
		waitFor(t, "deferred push maintenance and held pushes ran after resume", func() bool {
			return store.pushReconciles.Load() >= 1 && store.settingWrites(heldPushesSettingKey) >= 1
		})
	})

	t.Run("persona tiers", func(t *testing.T) {
		h := newTestHub(t)
		store := &bgWriteStore{meshStore: newMeshStore()}
		h.store = store
		ctx := context.Background()
		if err := h.quiesce.pause(ctx); err != nil {
			t.Fatal(err)
		}
		h.setPersonaTier(ctx, "pkg", &personaTiers{Default: "base"}, "gold", "alice")
		if store.settingWrites(personaTiersKey) != 0 {
			t.Fatal("persona tiers wrote while paused")
		}
		h.resumeDevelopmentWriters()
		waitFor(t, "deferred persona tiers written after resume", func() bool {
			return store.settingWrites(personaTiersKey) >= 1
		})
	})
}

type bgWriteStore struct {
	*meshStore
	lockTestDue      *time.Time
	lockTestAttempts atomic.Int32
	laClaims         atomic.Int32
	laHolds          atomic.Int32
	laSaves          atomic.Int32
	clientOpCleanups atomic.Int32
	pushReconciles   atomic.Int32
	settingMu        sync.Mutex
	settingCounts    map[string]int
}

func (s *bgWriteStore) settingWrites(key string) int {
	s.settingMu.Lock()
	defer s.settingMu.Unlock()
	return s.settingCounts[key]
}

func (s *bgWriteStore) PutSetting(ctx context.Context, key string, v any) error {
	s.settingMu.Lock()
	if s.settingCounts == nil {
		s.settingCounts = map[string]int{}
	}
	s.settingCounts[key]++
	s.settingMu.Unlock()
	return s.meshStore.PutSetting(ctx, key, v)
}

func (s *bgWriteStore) NextLockTestDue(context.Context) (*time.Time, error) {
	if s.lockTestAttempts.Load() > 0 {
		return nil, nil
	}
	return s.lockTestDue, nil
}

func (s *bgWriteStore) DueLockTests(context.Context, time.Time) ([]db.LockTest, error) {
	if s.lockTestAttempts.Load() > 0 {
		return nil, nil
	}
	return []db.LockTest{{TestID: "lt_1", DeviceID: "dev1", DueAt: *s.lockTestDue}}, nil
}

func (s *bgWriteStore) AttemptLockTest(context.Context, string, int64, time.Time) (bool, error) {
	s.lockTestAttempts.Add(1)
	return true, nil
}

func (s *bgWriteStore) ClaimLiveActivityStart(context.Context, string, string, time.Time, time.Duration, time.Duration) (time.Time, bool, error) {
	s.laClaims.Add(1)
	return time.Time{}, true, nil
}

func (s *bgWriteStore) HoldLiveActivityStart(context.Context, string, time.Time, time.Duration) (bool, error) {
	s.laHolds.Add(1)
	return true, nil
}

func (s *bgWriteStore) SaveLiveActivity(context.Context, db.LiveActivity) error {
	s.laSaves.Add(1)
	return nil
}

func (s *bgWriteStore) CleanupClientOps(context.Context, time.Time) error {
	s.clientOpCleanups.Add(1)
	return nil
}

func (s *bgWriteStore) ReconcilePushDevices(context.Context, time.Time, time.Duration) ([]db.PushDeviceRetirement, error) {
	s.pushReconciles.Add(1)
	return nil, nil
}

// Finding #4: both failure branches of pauseDevelopmentWriters wake all
// deferred workers (ext reports, hello writers, device session finishes, and
// assistantGate) instead of only calling quiesce.resume().
func TestPauseDevelopmentWritersFailureWakesDeferredWorkers(t *testing.T) {
	for _, branch := range []string{"quiesce-wait-failure", "assistant-stop-failure"} {
		t.Run(branch, func(t *testing.T) {
			h := newTestHub(t)
			store := &deviceSessionWrites{meshStore: newMeshStore()}
			h.store = store
			h.startHelloWriters()
			defer h.stopHelloWriters()

			pauseCtx, pauseCancel := context.WithCancel(context.Background())
			defer pauseCancel()
			var done chan error

			if branch == "quiesce-wait-failure" {
				// Hold an in-flight write so quiesce.pause sets paused=true and blocks.
				if !h.quiesce.begin() {
					t.Fatal("initial begin failed")
				}
				done = make(chan error, 1)
				go func() { done <- h.pauseDevelopmentWriters(pauseCtx) }()
				waitFor(t, "quiesce paused", func() bool {
					h.quiesce.mu.Lock()
					defer h.quiesce.mu.Unlock()
					return h.quiesce.paused
				})
			} else {
				// Register a stubborn child that never exits when cancelled.
				childCancelled := make(chan struct{})
				if !h.assistantProc.register(make(chan struct{}), func() { close(childCancelled) }) {
					t.Fatal("failed to register assistant child")
				}
				done = make(chan error, 1)
				go func() { done <- h.pauseDevelopmentWriters(pauseCtx) }()
				select {
				case <-childCancelled:
				case <-time.After(time.Second):
					t.Fatal("pauseDevelopmentWriters did not cancel assistant child")
				}
			}

			// Queue deferred writes across ext report, hello writer, and device session while paused.
			wantExt := extwire.Report{Features: []string{"after-failed-pause"}}
			h.queueExtReportSave(wantExt)
			h.updateClientApp("dev_hello", func(a assistantwire.ClientApp, _ bool) assistantwire.ClientApp {
				a.Platform = "ios"
				a.SeenAt = time.Now()
				return a
			})
			h.finishDeviceSession(42, time.Now(), "closed", 30)

			waitFor(t, "ext report deferred while paused", func() bool {
				h.ext.saveMu.Lock()
				defer h.ext.saveMu.Unlock()
				return h.ext.savePending != nil && !h.ext.saveRunning
			})

			// Abort the pause.
			pauseCancel()
			if branch == "quiesce-wait-failure" {
				h.quiesce.end()
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("expected pauseDevelopmentWriters to fail")
				}
			case <-time.After(time.Second):
				t.Fatal("pauseDevelopmentWriters did not return after cancel")
			}

			// Assistant gate must be open again on both branches.
			resumed := make(chan bool, 1)
			go func() { resumed <- h.assistantProc.waitResumed(context.Background()) }()
			select {
			case ok := <-resumed:
				if !ok {
					t.Fatal("assistantProc did not resume")
				}
			case <-time.After(time.Second):
				t.Fatal("assistantProc stayed paused after failed pauseDevelopmentWriters")
			}

			// Every deferred worker must be woken and persist its pending write.
			ctx := context.Background()
			waitFor(t, "deferred ext report written", func() bool {
				var got extwire.Report
				ok, err := store.Setting(ctx, extReportKey, &got)
				return err == nil && ok && slices.Equal(got.Features, wantExt.Features)
			})
			waitFor(t, "deferred hello client app written", func() bool {
				var got map[string]assistantwire.ClientApp
				ok, err := store.Setting(ctx, clientAppsSetting, &got)
				return err == nil && ok && got["dev_hello"].Platform == "ios"
			})
			waitFor(t, "deferred device session finish written", func() bool {
				store.mu.Lock()
				defer store.mu.Unlock()
				return store.finished == 1 && store.finishID == 42
			})
		})
	}
}

// Finding #5 (extensions_link.go): deterministic interleavings between
// saveExtReports observing pause / popping savePending and resumeDevelopmentWriters.
func TestExtReportSaveResumeInterleaving(t *testing.T) {
	t.Run("resume between pop and beginOrDefer", func(t *testing.T) {
		h := newTestHub(t)
		store := newMeshStore()
		h.store = store
		ctx := context.Background()
		if err := h.quiesce.pause(ctx); err != nil {
			t.Fatal(err)
		}
		reached := make(chan struct{})
		release := make(chan struct{})
		var once sync.Once
		h.ext.saveBeforeBegin = func() {
			once.Do(func() {
				close(reached)
				<-release
			})
		}
		want := extwire.Report{Features: []string{"race-before-begin"}}
		h.queueExtReportSave(want)
		select {
		case <-reached:
		case <-time.After(time.Second):
			t.Fatal("saveExtReports did not reach saveBeforeBegin")
		}
		// Resume runs while savePending is nil and saveRunning is true.
		h.resumeDevelopmentWriters()
		close(release)
		waitFor(t, "report persisted without another report arriving", func() bool {
			var got extwire.Report
			ok, err := store.Setting(ctx, extReportKey, &got)
			return err == nil && ok && slices.Equal(got.Features, want.Features)
		})
	})

	t.Run("resume racing with paused beginOrDefer handoff", func(t *testing.T) {
		h := newTestHub(t)
		store := newMeshStore()
		h.store = store
		ctx := context.Background()
		if err := h.quiesce.pause(ctx); err != nil {
			t.Fatal(err)
		}
		inDefer := make(chan struct{})
		release := make(chan struct{})
		var once sync.Once
		h.quiesce.testOnPauseDefer = func() {
			once.Do(func() {
				close(inDefer)
				<-release
			})
		}
		want := extwire.Report{Features: []string{"race-in-defer"}}
		h.queueExtReportSave(want)
		select {
		case <-inDefer:
		case <-time.After(time.Second):
			t.Fatal("saveExtReports did not enter paused beginOrDefer")
		}
		// Start resume while saveExtReports has observed paused==true and has
		// not yet restored savePending / cleared saveRunning.
		resumeDone := make(chan struct{})
		go func() {
			h.resumeDevelopmentWriters()
			close(resumeDone)
		}()
		time.Sleep(20 * time.Millisecond)
		close(release)
		select {
		case <-resumeDone:
		case <-time.After(time.Second):
			t.Fatal("resumeDevelopmentWriters did not finish")
		}
		waitFor(t, "report persisted after synchronized resume", func() bool {
			var got extwire.Report
			ok, err := store.Setting(ctx, extReportKey, &got)
			return err == nil && ok && slices.Equal(got.Features, want.Features)
		})
	})
}

// Finding #5 (devicesessions.go): deterministic interleavings between
// finishDeviceSession and resumeDevelopmentWriters, preserving FIFO order.
func TestDeviceSessionFinishResumeInterleavingAndOrder(t *testing.T) {
	for _, point := range []string{"after-append", "after-refuse"} {
		t.Run(point, func(t *testing.T) {
			h := newTestHub(t)
			store := &orderedDeviceSessionWrites{meshStore: newMeshStore()}
			h.store = store
			ctx := context.Background()
			if err := h.quiesce.pause(ctx); err != nil {
				t.Fatal(err)
			}
			reached := make(chan struct{})
			release := make(chan struct{})
			var once sync.Once
			hook := func() {
				once.Do(func() {
					close(reached)
					<-release
				})
			}
			if point == "after-append" {
				h.testAfterFinishAppend = hook
			} else {
				h.testAfterFinishRefuse = hook
			}

			finish1Done := make(chan struct{})
			go func() {
				h.finishDeviceSession(7, time.Now(), "first", 30)
				close(finish1Done)
			}()
			select {
			case <-reached:
			case <-time.After(time.Second):
				t.Fatalf("finishDeviceSession did not reach %s", point)
			}

			// Resume races right at the interleaving point, and a second session
			// ends right after resume starts.
			resumeDone := make(chan struct{})
			go func() {
				h.resumeDevelopmentWriters()
				close(resumeDone)
			}()
			time.Sleep(20 * time.Millisecond)
			close(release)
			select {
			case <-finish1Done:
			case <-time.After(time.Second):
				t.Fatal("finishDeviceSession(7) did not return")
			}
			select {
			case <-resumeDone:
			case <-time.After(time.Second):
				t.Fatal("resumeDevelopmentWriters did not return")
			}

			h.finishDeviceSession(8, time.Now(), "second", 30)

			store.mu.Lock()
			got := slices.Clone(store.ids)
			store.mu.Unlock()
			if !slices.Equal(got, []int64{7, 8}) {
				t.Fatalf("finished session ids = %v, want [7 8]", got)
			}
		})
	}
}

type orderedDeviceSessionWrites struct {
	*meshStore
	ids []int64
}

func (s *orderedDeviceSessionWrites) FinishDeviceSession(_ context.Context, id int64, _ time.Time, _ string, _ int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ids = append(s.ids, id)
	return nil
}

type busSeenRecord struct {
	id    string
	where *db.BusIdentity
}

type busSeenRecordingStore struct {
	managerStore
	seenMu  sync.Mutex
	records []busSeenRecord
	latest  map[string]*db.BusIdentity
}

var _ managerStore = (*busSeenRecordingStore)(nil)

func (s *busSeenRecordingStore) BusIdentities(context.Context) ([]db.BusIdentity, error) {
	return nil, nil
}
func (s *busSeenRecordingStore) SeeBusIdentity(_ context.Context, id string, where *db.BusIdentity) error {
	var cp *db.BusIdentity
	if where != nil {
		v := *where
		cp = &v
	}
	s.seenMu.Lock()
	defer s.seenMu.Unlock()
	if s.latest == nil {
		s.latest = map[string]*db.BusIdentity{}
	}
	s.records = append(s.records, busSeenRecord{id: id, where: cp})
	s.latest[id] = cp
	return nil
}

// Bus roster updates (connect metadata and nil disconnects) deferred while paused
// are coalesced per identity and flushed on resume without regressing newer updates.
func TestDevelopmentPauseDefersBusSeenUpdates(t *testing.T) {
	h := newTestHub(t)
	store := &busSeenRecordingStore{}
	h.bus.idents = store
	ctx := context.Background()
	if err := h.quiesce.pause(ctx); err != nil {
		t.Fatal(err)
	}
	h.busSeen(ctx, "mgr-1", &db.BusIdentity{Host: "host-a", Version: "1.0"})
	h.busSeen(ctx, "mgr-1", &db.BusIdentity{Host: "host-b", Version: "2.0"})
	h.busSeen(ctx, "mgr-2", &db.BusIdentity{Host: "host-c", Version: "1.0"})
	h.busSeen(ctx, "mgr-2", nil) // disconnect

	store.seenMu.Lock()
	pausedCount := len(store.records)
	store.seenMu.Unlock()
	if pausedCount != 0 {
		t.Fatalf("SeeBusIdentity called %d times while paused", pausedCount)
	}

	h.resumeDevelopmentWriters()
	waitFor(t, "deferred busSeen updates flushed", func() bool {
		store.seenMu.Lock()
		defer store.seenMu.Unlock()
		_, ok1 := store.latest["mgr-1"]
		_, ok2 := store.latest["mgr-2"]
		return ok1 && ok2
	})
	store.seenMu.Lock()
	got1 := store.latest["mgr-1"]
	got2 := store.latest["mgr-2"]
	store.seenMu.Unlock()
	if got1 == nil || got1.Host != "host-b" || got1.Version != "2.0" {
		t.Fatalf("mgr-1 persisted where = %+v, want host-b 2.0", got1)
	}
	if got2 != nil {
		t.Fatalf("mgr-2 persisted where = %+v, want nil disconnect", got2)
	}

	// Ordering race: a newer post-resume disconnect is never overwritten by a stale deferred connect.
	if err := h.quiesce.pause(ctx); err != nil {
		t.Fatal(err)
	}
	h.busSeen(ctx, "mgr-3", &db.BusIdentity{Host: "stale-host"})
	h.bus.seenMu.Lock()
	entry := h.bus.seen["mgr-3"]
	h.bus.seenMu.Unlock()
	entry.mu.Lock()
	h.resumeDevelopmentWriters()
	newerDone := make(chan struct{})
	go func() {
		h.busSeen(ctx, "mgr-3", nil)
		close(newerDone)
	}()
	waitFor(t, "newer busSeen queued", func() bool {
		h.bus.seenMu.Lock()
		defer h.bus.seenMu.Unlock()
		return entry.gen >= 2
	})
	entry.mu.Unlock()
	select {
	case <-newerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("newer busSeen did not finish")
	}
	h.nodeWorkers.Wait()
	store.seenMu.Lock()
	got3, ok3 := store.latest["mgr-3"]
	store.seenMu.Unlock()
	if !ok3 || got3 != nil {
		t.Fatalf("mgr-3 persisted where = %+v (ok=%v), want nil", got3, ok3)
	}
}

type hookedSettingsStore struct {
	*meshStore
	hookMu sync.Mutex
	onPut  func(key string, v any)
	onSave func(v db.LiveActivity)
	laLast db.LiveActivity
	laSeq  []string
}

func (s *hookedSettingsStore) PutSetting(ctx context.Context, key string, v any) error {
	s.hookMu.Lock()
	fn := s.onPut
	s.hookMu.Unlock()
	if fn != nil {
		fn(key, v)
	}
	return s.meshStore.PutSetting(ctx, key, v)
}

func (s *hookedSettingsStore) SaveLiveActivity(_ context.Context, v db.LiveActivity) error {
	s.hookMu.Lock()
	fn := s.onSave
	s.hookMu.Unlock()
	if fn != nil {
		fn(v)
	}
	s.hookMu.Lock()
	s.laLast = v
	s.laSeq = append(s.laSeq, v.PushToken)
	s.hookMu.Unlock()
	return nil
}

// Deferred Codex inventory writes cannot overwrite a newer post-resume report.
func TestResumedCodexInventoryCannotOverwriteNewerReport(t *testing.T) {
	t.Run("newer report arrives while resumed write is in flight", func(t *testing.T) {
		h := newTestHub(t)
		store := &hookedSettingsStore{meshStore: newMeshStore()}
		h.store = store
		m := newMachine(h, MachineConfig{ID: "box"})
		oldRaw, _ := json.Marshal(proto.CodexConfig{LegacyHome: "/old-home"})
		newRaw, _ := json.Marshal(proto.CodexConfig{LegacyHome: "/new-home"})

		ctx := context.Background()
		if err := h.quiesce.pause(ctx); err != nil {
			t.Fatal(err)
		}
		m.rememberCodexInventory(oldRaw)

		inOldPut := make(chan struct{})
		releaseOld := make(chan struct{})
		var once sync.Once
		store.onPut = func(key string, v any) {
			if cfg, ok := v.(proto.CodexConfig); ok && cfg.LegacyHome == "/old-home" {
				once.Do(func() {
					close(inOldPut)
					<-releaseOld
				})
			}
		}

		h.resumeDevelopmentWriters()
		select {
		case <-inOldPut:
		case <-time.After(2 * time.Second):
			t.Fatal("resumed Codex inventory write did not start")
		}
		newerDone := make(chan struct{})
		go func() {
			m.rememberCodexInventory(newRaw)
			close(newerDone)
		}()
		waitFor(t, "newer Codex inventory queued", func() bool {
			h.codexPendingMu.Lock()
			defer h.codexPendingMu.Unlock()
			return h.codexPending["box"].gen >= 2
		})
		close(releaseOld)
		select {
		case <-newerDone:
		case <-time.After(2 * time.Second):
			t.Fatal("newer Codex inventory did not finish")
		}
		h.nodeWorkers.Wait()
		var got proto.CodexConfig
		if ok, err := store.Setting(ctx, codexInventoryKey("box"), &got); err != nil || !ok || got.LegacyHome != "/new-home" {
			t.Fatalf("stored Codex inventory = %+v (ok=%v err=%v), want /new-home", got, ok, err)
		}
	})

	t.Run("newer report arrives before resumed worker acquires write lock", func(t *testing.T) {
		h := newTestHub(t)
		store := &hookedSettingsStore{meshStore: newMeshStore()}
		h.store = store
		m := newMachine(h, MachineConfig{ID: "box"})
		oldRaw, _ := json.Marshal(proto.CodexConfig{LegacyHome: "/old-home"})
		newRaw, _ := json.Marshal(proto.CodexConfig{LegacyHome: "/new-home"})

		ctx := context.Background()
		if err := h.quiesce.pause(ctx); err != nil {
			t.Fatal(err)
		}
		m.rememberCodexInventory(oldRaw)
		h.codexPendingMu.Lock()
		entry := h.codexPending["box"]
		h.codexPendingMu.Unlock()

		entry.mu.Lock()
		h.resumeDevelopmentWriters()
		newerDone := make(chan struct{})
		go func() {
			m.rememberCodexInventory(newRaw)
			close(newerDone)
		}()
		waitFor(t, "newer Codex inventory queued", func() bool {
			h.codexPendingMu.Lock()
			defer h.codexPendingMu.Unlock()
			return entry.gen >= 2
		})
		entry.mu.Unlock()
		select {
		case <-newerDone:
		case <-time.After(2 * time.Second):
			t.Fatal("newer Codex inventory did not finish")
		}
		h.nodeWorkers.Wait()
		var got proto.CodexConfig
		if ok, err := store.Setting(ctx, codexInventoryKey("box"), &got); err != nil || !ok || got.LegacyHome != "/new-home" {
			t.Fatalf("stored Codex inventory = %+v (ok=%v err=%v), want /new-home", got, ok, err)
		}
	})
}

// Deferred assistant profile saves cannot overwrite a newer post-resume profile.
func TestResumedAssistantProfileCannotOverwriteNewerReport(t *testing.T) {
	t.Run("newer profile arrives while resumed write is in flight", func(t *testing.T) {
		h := newTestHub(t)
		store := &hookedSettingsStore{meshStore: newMeshStore()}
		h.store = store
		ctx := context.Background()
		if err := h.quiesce.pause(ctx); err != nil {
			t.Fatal(err)
		}
		h.keepAssistantProfile(ctx, &assistantwire.Profile{Timezone: "UTC", Main: "old-main"})

		inOldPut := make(chan struct{})
		releaseOld := make(chan struct{})
		var once sync.Once
		store.onPut = func(key string, v any) {
			if p, ok := v.(*assistantwire.Profile); ok && p.Main == "old-main" {
				once.Do(func() {
					close(inOldPut)
					<-releaseOld
				})
			}
		}

		h.resumeDevelopmentWriters()
		select {
		case <-inOldPut:
		case <-time.After(2 * time.Second):
			t.Fatal("resumed assistant profile write did not start")
		}
		newerDone := make(chan struct{})
		go func() {
			h.keepAssistantProfile(ctx, &assistantwire.Profile{Timezone: "Asia/Tokyo", Main: "new-main"})
			close(newerDone)
		}()
		waitFor(t, "newer assistant profile queued", func() bool {
			h.assistantProfileSave.mu.Lock()
			defer h.assistantProfileSave.mu.Unlock()
			return h.assistantProfileSave.gen >= 2
		})
		close(releaseOld)
		select {
		case <-newerDone:
		case <-time.After(2 * time.Second):
			t.Fatal("newer assistant profile did not finish")
		}
		h.nodeWorkers.Wait()
		var got assistantwire.Profile
		if ok, err := store.Setting(ctx, assistantProfileKey, &got); err != nil || !ok || got.Main != "new-main" || got.Timezone != "Asia/Tokyo" {
			t.Fatalf("stored assistant profile = %+v (ok=%v err=%v), want new-main/Asia/Tokyo", got, ok, err)
		}
	})

	t.Run("newer profile arrives before resumed worker acquires write lock", func(t *testing.T) {
		h := newTestHub(t)
		store := &hookedSettingsStore{meshStore: newMeshStore()}
		h.store = store
		ctx := context.Background()
		if err := h.quiesce.pause(ctx); err != nil {
			t.Fatal(err)
		}
		h.keepAssistantProfile(ctx, &assistantwire.Profile{Timezone: "UTC", Main: "old-main"})

		h.assistantProfileSave.writeMu.Lock()
		h.resumeDevelopmentWriters()
		newerDone := make(chan struct{})
		go func() {
			h.keepAssistantProfile(ctx, &assistantwire.Profile{Timezone: "Asia/Tokyo", Main: "new-main"})
			close(newerDone)
		}()
		waitFor(t, "newer assistant profile queued", func() bool {
			h.assistantProfileSave.mu.Lock()
			defer h.assistantProfileSave.mu.Unlock()
			return h.assistantProfileSave.gen >= 2
		})
		h.assistantProfileSave.writeMu.Unlock()
		select {
		case <-newerDone:
		case <-time.After(2 * time.Second):
			t.Fatal("newer assistant profile did not finish")
		}
		h.nodeWorkers.Wait()
		var got assistantwire.Profile
		if ok, err := store.Setting(ctx, assistantProfileKey, &got); err != nil || !ok || got.Main != "new-main" || got.Timezone != "Asia/Tokyo" {
			t.Fatalf("stored assistant profile = %+v (ok=%v err=%v), want new-main/Asia/Tokyo", got, ok, err)
		}
	})
}

// Deferred Live Activity saves cannot overwrite newer post-resume updateContext state.
func TestResumedLiveActivitySaveCannotOverwriteNewerUpdate(t *testing.T) {
	t.Run("newer update arrives while resumed save is in flight", func(t *testing.T) {
		h := newTestHub(t)
		store := &hookedSettingsStore{meshStore: newMeshStore()}
		h.store = store
		la := h.live
		ctx := context.Background()
		if err := h.quiesce.pause(ctx); err != nil {
			t.Fatal(err)
		}
		if err := la.updateContext(ctx, "dev1", func(v *db.LiveActivity) { v.PushToken = "old-tok" }); err != nil {
			t.Fatal(err)
		}

		inOldSave := make(chan struct{})
		releaseOld := make(chan struct{})
		var once sync.Once
		store.onSave = func(v db.LiveActivity) {
			if v.PushToken == "old-tok" {
				once.Do(func() {
					close(inOldSave)
					<-releaseOld
				})
			}
		}

		h.resumeDevelopmentWriters()
		select {
		case <-inOldSave:
		case <-time.After(2 * time.Second):
			t.Fatal("resumed Live Activity save did not start")
		}
		newerDone := make(chan error, 1)
		go func() {
			newerDone <- la.updateContext(ctx, "dev1", func(v *db.LiveActivity) { v.PushToken = "new-tok" })
		}()
		waitFor(t, "newer Live Activity update queued", func() bool {
			la.mu.Lock()
			defer la.mu.Unlock()
			return la.rowGen["dev1"] >= 2
		})
		close(releaseOld)
		select {
		case err := <-newerDone:
			if err != nil {
				t.Fatalf("newer updateContext error: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("newer updateContext did not finish")
		}
		h.nodeWorkers.Wait()
		store.hookMu.Lock()
		got := store.laLast.PushToken
		seq := slices.Clone(store.laSeq)
		store.hookMu.Unlock()
		if got != "new-tok" {
			t.Fatalf("final persisted Live Activity token = %q (seq=%v), want new-tok", got, seq)
		}
	})

	t.Run("newer update arrives before resumed worker acquires device lock", func(t *testing.T) {
		h := newTestHub(t)
		store := &hookedSettingsStore{meshStore: newMeshStore()}
		h.store = store
		la := h.live
		ctx := context.Background()
		if err := h.quiesce.pause(ctx); err != nil {
			t.Fatal(err)
		}
		if err := la.updateContext(ctx, "dev1", func(v *db.LiveActivity) { v.PushToken = "old-tok" }); err != nil {
			t.Fatal(err)
		}
		la.mu.Lock()
		devMu := la.deviceSaveMuLocked("dev1")
		la.mu.Unlock()

		devMu.Lock()
		h.resumeDevelopmentWriters()
		newerDone := make(chan error, 1)
		go func() {
			newerDone <- la.updateContext(ctx, "dev1", func(v *db.LiveActivity) { v.PushToken = "new-tok" })
		}()
		waitFor(t, "newer Live Activity update queued", func() bool {
			la.mu.Lock()
			defer la.mu.Unlock()
			return la.rowGen["dev1"] >= 2
		})
		devMu.Unlock()
		select {
		case err := <-newerDone:
			if err != nil {
				t.Fatalf("newer updateContext error: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("newer updateContext did not finish")
		}
		h.nodeWorkers.Wait()
		store.hookMu.Lock()
		got := store.laLast.PushToken
		seq := slices.Clone(store.laSeq)
		store.hookMu.Unlock()
		if got != "new-tok" {
			t.Fatalf("final persisted Live Activity token = %q (seq=%v), want new-tok", got, seq)
		}
	})
}

// Resumed persona tier snapshots cannot overwrite newer post-resume tier state.
func TestResumedPersonaTiersCannotOverwriteNewerState(t *testing.T) {
	t.Run("newer tier arrives while resumed write is in flight", func(t *testing.T) {
		h := newTestHub(t)
		store := &hookedSettingsStore{meshStore: newMeshStore()}
		h.store = store
		ctx := context.Background()
		if err := h.quiesce.pause(ctx); err != nil {
			t.Fatal(err)
		}
		tiers := &personaTiers{Default: "base"}
		h.setPersonaTier(ctx, "pkg", tiers, "egg", "alice")

		inOldPut := make(chan struct{})
		releaseOld := make(chan struct{})
		var once sync.Once
		store.onPut = func(key string, v any) {
			if m, ok := v.(map[string]tierSeen); ok && m["pkg"].Tier == "egg" {
				once.Do(func() {
					close(inOldPut)
					<-releaseOld
				})
			}
		}

		h.resumeDevelopmentWriters()
		select {
		case <-inOldPut:
		case <-time.After(2 * time.Second):
			t.Fatal("resumed persona tiers write did not start")
		}
		newerDone := make(chan struct{})
		go func() {
			h.setPersonaTier(ctx, "pkg", tiers, "cosmic", "alice")
			close(newerDone)
		}()
		waitFor(t, "newer persona tier queued", func() bool {
			h.tiers.mu.Lock()
			defer h.tiers.mu.Unlock()
			return h.tiers.saveGen >= 2
		})
		close(releaseOld)
		select {
		case <-newerDone:
		case <-time.After(2 * time.Second):
			t.Fatal("newer setPersonaTier did not finish")
		}
		h.nodeWorkers.Wait()
		var got map[string]tierSeen
		if ok, err := store.Setting(ctx, personaTiersKey, &got); err != nil || !ok || got["pkg"].Tier != "cosmic" {
			t.Fatalf("stored persona tiers = %+v (ok=%v err=%v), want cosmic", got, ok, err)
		}
	})

	t.Run("newer tier arrives before resumed worker acquires write lock", func(t *testing.T) {
		h := newTestHub(t)
		store := &hookedSettingsStore{meshStore: newMeshStore()}
		h.store = store
		ctx := context.Background()
		if err := h.quiesce.pause(ctx); err != nil {
			t.Fatal(err)
		}
		tiers := &personaTiers{Default: "base"}
		h.setPersonaTier(ctx, "pkg", tiers, "egg", "alice")

		h.tiers.writeMu.Lock()
		h.resumeDevelopmentWriters()
		newerDone := make(chan struct{})
		go func() {
			h.setPersonaTier(ctx, "pkg", tiers, "cosmic", "alice")
			close(newerDone)
		}()
		waitFor(t, "newer persona tier queued", func() bool {
			h.tiers.mu.Lock()
			defer h.tiers.mu.Unlock()
			return h.tiers.saveGen >= 2
		})
		h.tiers.writeMu.Unlock()
		select {
		case <-newerDone:
		case <-time.After(2 * time.Second):
			t.Fatal("newer setPersonaTier did not finish")
		}
		h.nodeWorkers.Wait()
		var got map[string]tierSeen
		if ok, err := store.Setting(ctx, personaTiersKey, &got); err != nil || !ok || got["pkg"].Tier != "cosmic" {
			t.Fatalf("stored persona tiers = %+v (ok=%v err=%v), want cosmic", got, ok, err)
		}
	})
}
