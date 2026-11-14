package assistant

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/db"
)

// drainFleetBackground stops new background scheduling, then lets active calls finish before a test closes its store.
// Production Run uses the same canceled runCtx to stop new naming passes and direction-tree timers.
func drainFleetBackground(t *testing.T, s *Service, cancel context.CancelFunc) {
	t.Helper()
	cancel()

	names := &s.agentNames
	names.mu.Lock()
	if names.timer != nil {
		names.timer.Stop()
		names.timer = nil
		names.timerSeq++
	}
	names.mu.Unlock()

	tree := &s.dtree
	tree.mu.Lock()
	if tree.timer != nil && tree.timer.Stop() {
		tree.timer = nil
		tree.armed = false
		tree.runs.Done()
	}
	tree.mu.Unlock()

	// A screen-upgrade start checks runCtx under fleet.mu; taking it here closes the Add/Wait teardown window.
	s.fleet.mu.Lock()
	s.fleet.mu.Unlock()
	// workEvent and recordSendAsync use work.mu for the same runCtx + Add scheduling gate.
	s.work.mu.Lock()
	s.work.mu.Unlock()

	waitFleetBackground(t, "agent naming", &names.runs)
	waitFleetBackground(t, "direction tree", &tree.runs)
	waitFleetBackground(t, "screen upgrades", &s.fleet.upgrades)
	waitFleetBackground(t, "hub transcription", &s.hubTr.wg)
	waitFleetBackground(t, "work events", &s.work.eventWrites)
}

func waitFleetBackground(t *testing.T, name string, wg *sync.WaitGroup) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		t.Fatalf("%s did not finish before the database fixture closed", name)
	}
}

// delayedBackgroundStore lets a background database call finish only after its test has returned. A fixture that drains
// the call before closing the database sees no error; without that drain, the closed-pool error logs through the ended T.
type delayedBackgroundStore struct {
	*db.DB
	release chan struct{}
}

func newDelayedBackgroundStore(t *testing.T) *delayedBackgroundStore {
	t.Helper()
	st := &delayedBackgroundStore{DB: taskRecoveryDatabase(t), release: make(chan struct{})}
	time.AfterFunc(100*time.Millisecond, func() { close(st.release) })
	return st
}

func (s *delayedBackgroundStore) Setting(ctx context.Context, key string, out any) (bool, error) {
	if key == settingsKey {
		select {
		case <-s.release:
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
	return s.DB.Setting(ctx, key, out)
}

func (s *delayedBackgroundStore) AddWorkEvent(ctx context.Context, e db.WorkEvent) error {
	<-s.release
	return s.DB.AddWorkEvent(ctx, e)
}

// A database-backed fleet fixture must drain the service work it started before the test's database closes. Each case
// returns immediately: cleanup, not a sleep or an assertion inside the case, is what has to finish the background work.
func TestFleetDatabaseFixtureDrainsBackgroundWork(t *testing.T) {
	t.Run("direction_tree", func(t *testing.T) {
		ft := newFleetTestOn(t, taskRecoveryDatabase(t))
		if err := ft.s.ensureTodosLoaded(context.Background()); err != nil {
			t.Fatal(err)
		}
		ft.s.directionTreeSoon()
	})

	t.Run("agent_naming", func(t *testing.T) {
		ft := newFleetTestOn(t, newDelayedBackgroundStore(t))
		ft.s.agentNamesChanged(DeviceSessions{})
	})

	t.Run("work_events", func(t *testing.T) {
		ft := newFleetTestOn(t, newDelayedBackgroundStore(t))
		ft.s.workEvent("mac", "s1", "turn_finished", nil)
	})

	t.Run("work_events_after_stop", func(t *testing.T) {
		ft := newFleetTestOn(t, newDelayedBackgroundStore(t))
		time.AfterFunc(50*time.Millisecond, func() { ft.s.workEvent("mac", "s1", "turn_finished", nil) })
	})
}
