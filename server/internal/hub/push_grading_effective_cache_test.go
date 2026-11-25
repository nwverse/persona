package hub

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
)

type gradingCacheStore struct {
	settingsStore
	callsMu     sync.Mutex
	calls       int
	err         error
	block       chan struct{}
	started     chan struct{}
	startedOnce sync.Once
}

func newGradingCacheStore() *gradingCacheStore {
	return &gradingCacheStore{settingsStore: settingsStore{vals: map[string][]byte{}}}
}

func (s *gradingCacheStore) Settings(ctx context.Context, keys ...string) (map[string]json.RawMessage, error) {
	s.callsMu.Lock()
	s.calls++
	err := s.err
	s.callsMu.Unlock()
	if s.started != nil {
		s.startedOnce.Do(func() { close(s.started) })
	}
	if s.block != nil {
		select {
		case <-s.block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if err != nil {
		return nil, err
	}
	return s.settingsStore.Settings(ctx, keys...)
}

func (s *gradingCacheStore) stats() (int, error) {
	s.callsMu.Lock()
	defer s.callsMu.Unlock()
	return s.calls, s.err
}

func (s *gradingCacheStore) setErr(err error) {
	s.callsMu.Lock()
	defer s.callsMu.Unlock()
	s.err = err
}

func prepareGradingCacheStore(t *testing.T, store *gradingCacheStore) {
	t.Helper()
	ctx := context.Background()
	base := parseGradingTestConfig(t, `{"version":1,"enabled":true,"actions":{"P1":"push"}}`, false)
	if err := writePushGradingBase(ctx, store, base); err != nil {
		t.Fatal(err)
	}
	layer := pushGradingLayer{
		Source:    "extension:one",
		Manager:   "manager-a",
		UpdatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Config:    parseGradingTestConfig(t, `{"version":1,"actions":{"P2":"push"}}`, true),
	}
	if _, err := writePushGradingLayer(ctx, store, layer); err != nil {
		t.Fatal(err)
	}
}

func TestPushGradingEffectiveCachePerManager(t *testing.T) {
	ctx := context.Background()
	store := newGradingCacheStore()
	prepareGradingCacheStore(t, store)
	cache := newPushGradingEffectiveCache(time.Minute)

	for _, tc := range []struct {
		name     string
		manager  string
		wantP2   string
		wantP2By string
	}{
		{name: "owning manager", manager: "manager-a", wantP2: "push", wantP2By: "extension:one"},
		{name: "another manager", manager: "manager-b", wantP2: "silence", wantP2By: "default"},
		{name: "default manager", manager: "", wantP2: "silence", wantP2By: "default"},
		{name: "assistant manager", manager: assistantwire.AssistantID, wantP2: "silence", wantP2By: "default"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := cache.effective(ctx, store, tc.manager)
			if err != nil {
				t.Fatal(err)
			}
			if !got.Enabled || got.Actions.P1 != "push" || got.Actions.P2 != tc.wantP2 || got.By["actions.P2"] != tc.wantP2By {
				t.Fatalf("effective = %+v, want P2=%q by=%q", got, tc.wantP2, tc.wantP2By)
			}
		})
	}
	if calls, _ := store.stats(); calls != 1 {
		t.Fatalf("Settings calls = %d, want one cached load", calls)
	}
}

func TestPushGradingSnapshotManagerActivation(t *testing.T) {
	for _, tc := range []struct {
		name      string
		baseRaw   string
		ownerWant bool
		otherWant bool
	}{
		{name: "automatic", baseRaw: `{"version":1}`, ownerWant: true, otherWant: false},
		{name: "owner disabled", baseRaw: `{"version":1,"enabled":false}`, ownerWant: false, otherWant: false},
		{name: "owner enabled", baseRaw: `{"version":1,"enabled":true}`, ownerWant: true, otherWant: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := parseGradingTestConfig(t, tc.baseRaw, false)
			snapshot := pushGradingSnapshot{
				Base: &base,
				Layer: &pushGradingLayer{
					Source:  "extension:one",
					Manager: "manager-a",
					Config:  parseGradingTestConfig(t, `{"version":1}`, true),
				},
			}
			if got := snapshot.effectiveForManager(" manager-a "); got.Enabled != tc.ownerWant {
				t.Fatalf("owning manager enabled = %v, want %v", got.Enabled, tc.ownerWant)
			}
			if got := snapshot.effectiveForManager("manager-b"); got.Enabled != tc.otherWant {
				t.Fatalf("other manager enabled = %v, want %v", got.Enabled, tc.otherWant)
			}
		})
	}
}

func TestPushGradingEffectiveCacheTTLAndInvalidation(t *testing.T) {
	ctx := context.Background()
	store := newGradingCacheStore()
	prepareGradingCacheStore(t, store)
	now := time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC)
	cache := newPushGradingEffectiveCache(time.Minute)
	cache.now = func() time.Time { return now }

	if _, err := cache.effective(ctx, store, "manager-a"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(59 * time.Second)
	if _, err := cache.effective(ctx, store, "manager-a"); err != nil {
		t.Fatal(err)
	}
	if calls, _ := store.stats(); calls != 1 {
		t.Fatalf("fresh cache calls = %d, want 1", calls)
	}
	updatedBase := parseGradingTestConfig(t, `{"version":1,"enabled":true,"actions":{"P1":"push","P2":"push"}}`, false)
	if err := writePushGradingBase(ctx, store, updatedBase); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	got, err := cache.effective(ctx, store, "manager-b")
	if err != nil {
		t.Fatal(err)
	}
	if got.Actions.P2 != "push" || got.By["actions.P2"] != "base" {
		t.Fatalf("expired reload kept old configuration: %+v", got)
	}
	if calls, _ := store.stats(); calls != 2 {
		t.Fatalf("expired cache calls = %d, want 2", calls)
	}
	updatedBase = parseGradingTestConfig(t, `{"version":1,"enabled":true,"actions":{"P1":"push","P2":"hold"}}`, false)
	if err := writePushGradingBase(ctx, store, updatedBase); err != nil {
		t.Fatal(err)
	}
	cache.invalidate()
	got, err = cache.effective(ctx, store, "manager-b")
	if err != nil {
		t.Fatal(err)
	}
	if got.Actions.P2 != "hold" || got.By["actions.P2"] != "base" {
		t.Fatalf("invalidated reload kept old configuration: %+v", got)
	}
	if calls, _ := store.stats(); calls != 3 {
		t.Fatalf("invalidated cache calls = %d, want 3", calls)
	}
}

func TestPushGradingEffectiveCacheReloadFailure(t *testing.T) {
	ctx := context.Background()
	store := newGradingCacheStore()
	prepareGradingCacheStore(t, store)
	loadErr := errors.New("settings unavailable")
	now := time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC)
	cache := newPushGradingEffectiveCache(time.Minute)
	cache.now = func() time.Time { return now }

	store.setErr(loadErr)
	if _, err := cache.effective(ctx, store, "manager-a"); !errors.Is(err, loadErr) {
		t.Fatalf("first load error = %v, want %v", err, loadErr)
	}
	store.setErr(nil)
	if _, err := cache.effective(ctx, store, "manager-a"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	store.setErr(loadErr)
	got, err := cache.effective(ctx, store, "manager-a")
	if err != nil {
		t.Fatalf("stale fallback error = %v", err)
	}
	if got.Actions.P2 != "push" || got.By["actions.P2"] != "extension:one" {
		t.Fatalf("stale fallback changed configuration: %+v", got)
	}
	if calls, _ := store.stats(); calls != 3 {
		t.Fatalf("reload calls = %d, want 3", calls)
	}
	now = now.Add(30 * time.Second)
	if _, err := cache.effective(ctx, store, "manager-a"); err != nil {
		t.Fatal(err)
	}
	if calls, _ := store.stats(); calls != 3 {
		t.Fatalf("stale snapshot was retried before TTL: calls=%d", calls)
	}
	recoveredBase := parseGradingTestConfig(t, `{"version":1,"enabled":true,"actions":{"P1":"push","P2":"hold"}}`, false)
	if err := writePushGradingBase(ctx, store, recoveredBase); err != nil {
		t.Fatal(err)
	}
	store.setErr(nil)
	now = now.Add(30 * time.Second)
	got, err = cache.effective(ctx, store, "manager-b")
	if err != nil {
		t.Fatalf("recovered reload error = %v", err)
	}
	if got.Actions.P2 != "hold" || got.By["actions.P2"] != "base" {
		t.Fatalf("recovered reload kept stale configuration: %+v", got)
	}
	if calls, _ := store.stats(); calls != 4 {
		t.Fatalf("recovery calls = %d, want 4", calls)
	}
}

func TestPushGradingEffectiveCacheConcurrentLoad(t *testing.T) {
	ctx := context.Background()
	store := newGradingCacheStore()
	store.block = make(chan struct{})
	store.started = make(chan struct{})
	prepareGradingCacheStore(t, store)
	cache := newPushGradingEffectiveCache(time.Minute)

	const workers = 24
	start := make(chan struct{})
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			manager := "manager-a"
			if i%2 == 1 {
				manager = "manager-b"
			}
			got, err := cache.effective(ctx, store, manager)
			if err != nil {
				errs <- err
				return
			}
			want := "silence"
			if manager == "manager-a" {
				want = "push"
			}
			if got.Actions.P2 != want {
				errs <- errors.New("manager configuration crossed")
			}
		}(i)
	}
	close(start)
	select {
	case <-store.started:
	case <-time.After(5 * time.Second):
		close(store.block)
		t.Fatal("cache load did not start")
	}
	close(store.block)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if calls, _ := store.stats(); calls != 1 {
		t.Fatalf("concurrent cache calls = %d, want single-flight load", calls)
	}
}
