package assistant

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/db"
)

type userCheckPassStore struct {
	*db.DB
	closeCalls       atomic.Int32
	ledgerWrites     atomic.Int32
	acceptanceWrites atomic.Int32
	stageWrites      atomic.Int32
	failLedger       atomic.Bool
}

func (d *userCheckPassStore) ReviewDoneTodo(ctx context.Context, id, reason string, accepted bool, at time.Time) (*db.TodoRow, bool, error) {
	d.closeCalls.Add(1)
	return d.DB.ReviewDoneTodo(ctx, id, reason, accepted, at)
}

func (d *userCheckPassStore) PutTodoSupervision(ctx context.Context, row db.TodoSupervisionRow) error {
	if row.State == reviewAccepted {
		d.ledgerWrites.Add(1)
		if d.failLedger.Load() {
			return errors.New("supervision store unavailable")
		}
	}
	return d.DB.PutTodoSupervision(ctx, row)
}

func (d *userCheckPassStore) PutSetting(ctx context.Context, key string, value any) error {
	switch key {
	case acceptanceKey:
		d.acceptanceWrites.Add(1)
	case reportStagesKey:
		d.stageWrites.Add(1)
	}
	return d.DB.PutSetting(ctx, key, value)
}

func TestUserCheckRecoversEveryPersistedPassStep(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name             string
		done, ledgerDone bool
		verified         bool
	}{
		{name: "before completion"},
		{name: "after completion", done: true},
		{name: "after ledger update", done: true, ledgerDone: true},
		{name: "verified with interrupted ledger write", done: true, verified: true},
		{name: "already finished", done: true, ledgerDone: true, verified: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &userCheckPassStore{DB: todoTestDB(t)}
			s, f, todo := acceptedForUserCheckOn(t, store)
			s.accept.mu.Lock()
			s.accept.asks[todo.ID].AcceptedAt = time.Now().UTC()
			err := s.saveAcceptErrLocked(ctx)
			s.accept.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			acceptedAt := askOf(s, todo.ID).AcceptedAt
			if tc.done {
				if _, err := s.reviewDoneTodo(ctx, todo.ID, "Owner passed", true); err != nil {
					t.Fatal(err)
				}
			}
			if tc.ledgerDone {
				if _, _, err := s.updateSupervision(ctx, todo.ID, func(v *Supervision) bool {
					v.State = reviewAccepted
					return true
				}); err != nil {
					t.Fatal(err)
				}
			}
			if tc.verified {
				s.setTaskStageKind(ctx, todo.ID, "verify", "", "", true)
			}
			doneAt := todoNow(s, todo.ID).DoneAt
			updated := updatedCards(f)
			store.closeCalls.Store(0)
			store.ledgerWrites.Store(0)
			store.acceptanceWrites.Store(0)
			store.stageWrites.Store(0)

			restarted := newServiceOn(t, f, store)
			restarted.verifySupervisedAsks(ctx)
			restarted.verifySupervisedAsks(ctx)
			if got := todoNow(restarted, todo.ID); got.DoneAt == nil || got.Supervision.State != reviewAccepted || !got.stageVerified {
				t.Fatalf("persisted Pass not fully recovered: done=%v ledger=%q stage=%q verified=%v", got.DoneAt, got.Supervision.State, got.Stage, got.stageVerified)
			}
			row, err := store.GetTodo(ctx, todo.ID)
			if err != nil || row.DoneAt == nil || row.DoneBy != db.DoneByReview {
				t.Fatalf("completion not durable: %+v, %v", row, err)
			}
			if doneAt != nil && !row.DoneAt.Equal(*doneAt) {
				t.Fatalf("recovery repeated completion: %v -> %v", doneAt, row.DoneAt)
			}
			if got := askOf(restarted, todo.ID); got == nil || !got.AcceptedAt.Equal(acceptedAt) {
				t.Fatalf("recovery replaced the original owner Pass: %+v", got)
			}
			wantClose, wantLedger, wantVerify := int32(0), int32(0), int32(0)
			if !tc.done {
				wantClose = 1
			}
			if !tc.ledgerDone {
				wantLedger = 1
			}
			if !tc.verified {
				wantVerify = 1
			}
			if got := store.closeCalls.Load(); got != wantClose {
				t.Fatalf("completion calls=%d, want %d", got, wantClose)
			}
			if got := store.ledgerWrites.Load(); got != wantLedger {
				t.Fatalf("ledger writes=%d, want %d", got, wantLedger)
			}
			if got := store.acceptanceWrites.Load(); got != 0 {
				t.Fatalf("rewrote the already-durable owner Pass %d times", got)
			}
			if got := store.stageWrites.Load(); got != wantVerify {
				t.Fatalf("stage writes=%d, want %d", got, wantVerify)
			}
			if got := len(updated()); got != int(wantVerify) {
				t.Fatalf("card updates=%d, want %d", got, wantVerify)
			}
			if result, err := restarted.acceptTry(ctx, todo.ID, ""); err != nil || result.taken {
				t.Fatalf("completed Pass taken twice: %+v, %v", result, err)
			}
			again := newServiceOn(t, f, store)
			again.verifySupervisedAsks(ctx)
			if got := todoNow(again, todo.ID); got.Supervision.State != reviewAccepted || got.Stage != "verify" || !got.stageVerified {
				t.Fatalf("recovered verification was not durable: %+v", got)
			}
			if store.closeCalls.Load() != wantClose || store.ledgerWrites.Load() != wantLedger ||
				store.acceptanceWrites.Load() != 0 || store.stageWrites.Load() != wantVerify || len(updated()) != int(wantVerify) {
				t.Fatal("another restart repeated completed Pass work")
			}
		})
	}
}

func TestUserCheckRetriesFailedPassLedgerBeforeVerification(t *testing.T) {
	ctx := context.Background()
	store := &userCheckPassStore{DB: todoTestDB(t)}
	s, f, todo := acceptedForUserCheckOn(t, store)
	updated := updatedCards(f)
	store.failLedger.Store(true)
	if result, err := s.acceptTry(ctx, todo.ID, ""); result.taken || !errors.Is(err, errAcceptStore) {
		t.Fatalf("failed ledger update reported a completed Pass: %+v, %v", result, err)
	}
	done := todoNow(s, todo.ID)
	if done.DoneAt == nil || done.Supervision.State != reviewUserCheck || done.stageVerified {
		t.Fatalf("failed ledger update skipped ahead to verified: %+v", done)
	}
	a := askOf(s, todo.ID)
	if a == nil || a.AcceptedAt.IsZero() || len(updated()) != 0 {
		t.Fatalf("durable Pass lost or prematurely redrawn: ask=%+v, cards=%d", a, len(updated()))
	}
	store.closeCalls.Store(0)
	store.acceptanceWrites.Store(0)
	store.stageWrites.Store(0)
	restarted := newServiceOn(t, f, store)
	restarted.verifySupervisedAsks(ctx)
	if got := todoNow(restarted, todo.ID); got.Supervision.State != reviewUserCheck || got.stageVerified {
		t.Fatalf("failed restart skipped the pending ledger write: %+v", got)
	}
	store.failLedger.Store(false)
	restarted.verifySupervisedAsks(ctx)
	got := todoNow(restarted, todo.ID)
	if got.Supervision.State != reviewAccepted || !got.stageVerified || !got.DoneAt.Equal(*done.DoneAt) {
		t.Fatalf("ledger recovery did not finish the original Pass: %+v", got)
	}
	if store.closeCalls.Load() != 0 || store.acceptanceWrites.Load() != 0 || store.stageWrites.Load() != 1 || len(updated()) != 1 {
		t.Fatalf("recovery repeated durable steps: closes=%d asks=%d stages=%d cards=%d",
			store.closeCalls.Load(), store.acceptanceWrites.Load(), store.stageWrites.Load(), len(updated()))
	}
}
