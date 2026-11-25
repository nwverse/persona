package externalsourcestore_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	w "github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
)

func TestExternalInboxReceiptsCommitAndDeduplicateAcrossWriters(t *testing.T) {
	d := open(t)
	ctx := t.Context()
	now := time.Now()
	ev := w.ExternalInboxEvent{Key: "event_key", EventID: "comment:12", Ref: ref{Provider: "github"},
		Summary: "Synthetic review"}
	row := db.InboxRow{ID: "invalid", UpdatedAt: now, Data: []byte(`{"source":"\u0000"}`)}
	if err := d.SaveExternalInboxItems(ctx, []db.InboxRow{row}, []w.ExternalInboxEvent{ev}, now); err == nil {
		t.Fatal("invalid Inbox row committed its receipt")
	}
	if got, err := d.ReadyExternalInboxEvents(ctx, now, 5); err != nil || len(got) != 0 {
		t.Fatalf("partial commit: %+v, %v", got, err)
	}
	next := open(t)
	stores := []interface {
		SaveExternalInboxItems(context.Context, []db.InboxRow, []w.ExternalInboxEvent, time.Time) error
	}{d, next}
	var wg sync.WaitGroup
	errs := make(chan error, len(stores))
	for i, store := range stores {
		wg.Go(func() {
			key := fmt.Sprintf("inbox_%d", i)
			copy := ev
			copy.InboxID = w.ExternalInboxItemID(key)
			row := db.InboxRow{ID: key, UpdatedAt: now, Data: []byte(`{"source":"github"}`)}
			errs <- store.SaveExternalInboxItems(ctx, []db.InboxRow{row}, []w.ExternalInboxEvent{copy}, now)
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got, err := next.ReadyExternalInboxEvents(ctx, now, 5); err != nil || len(got) != 1 || got[0].EventID != ev.EventID {
		t.Fatalf("event receipt: %+v, %v", got, err)
	}
	plan := &w.ExternalInboxPlan{Owner: &w.ExternalInboxDelivery{
		Target: w.ExternalInboxTarget{ID: "todo_a", Kind: w.ExternalTargetTodo},
	}}
	if err := next.SaveExternalInboxProgress(ctx, ev.Key, time.Time{}, plan, true); err != nil {
		t.Fatal(err)
	}
	if err := d.SaveExternalInboxItems(ctx, nil, []w.ExternalInboxEvent{ev}, now); err != nil {
		t.Fatal(err)
	}
	if got, err := d.ReadyExternalInboxEvents(ctx, now, 5); err != nil || len(got) != 0 {
		t.Fatalf("a replay re-opened a completed receipt: %+v, %v", got, err)
	}
	if owners, err := d.RecentExternalInboxOwners(ctx, ev.Ref, "other_event"); err != nil || len(owners) != 1 || owners[0].Target != plan.Owner.Target {
		t.Fatalf("a new event cannot find the old owner: %+v, %v", owners, err)
	}
}

func TestExternalInboxSweepExcludesAnOverlappingManager(t *testing.T) {
	first, second := open(t), open(t)
	acquired, release := make(chan struct{}), make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- first.WithExternalInboxSweep(t.Context(), func() error {
			close(acquired)
			<-release
			return nil
		})
	}()
	select {
	case <-acquired:
	case err := <-firstDone:
		t.Fatalf("first manager did not acquire its lock: %v", err)
	case <-t.Context().Done():
		t.Fatal("first manager did not run")
	}
	err := second.WithExternalInboxSweep(t.Context(), func() error {
		return fmt.Errorf("a concurrent worker entered the same manager's sweep")
	})
	close(release)
	if firstErr := <-firstDone; err != nil || firstErr != nil {
		t.Fatalf("sweeps: first %v, second %v", firstErr, err)
	}
}
