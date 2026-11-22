package assistant

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	rq "github.com/nwverse/persona/server/internal/resourcequeue"
)

func resourceFixture(t *testing.T) (*Service, *db.DB, rq.Definition) {
	t.Helper()
	database, err := db.Open(t.Context(), newDatabase(t), db.ManagerSet)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(database.Close)
	s := New(Options{Hub: newFakeHub(), Store: func() Store { return database }})
	d := rq.Definition{ID: "ci.example", Capacity: 3, Mirror: rq.Mirror{Machine: "computer-a", Target: "runner.example.com"}}
	if err := database.PutSetting(t.Context(), resourceDefinitionsKey, []rq.Definition{d}); err != nil {
		t.Fatal(err)
	}
	return s, database, d
}
func TestResourceConcurrentAdmissionAndRestart(t *testing.T) {
	s, st, d := resourceFixture(t)
	ctx := t.Context()
	var wg sync.WaitGroup
	errs := make(chan error, 50)
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _, err := s.resourceRows(ctx, d, func(rows *[]rq.Lease, seq int64) (rq.Reply, error) {
				return rq.Acquire(rows, rq.Request{Resource: d.ID, Project: "example", Commit: fmt.Sprintf("%040x", i+1), Holder: rq.Holder{Machine: "computer-a", Session: fmt.Sprint(i)}}, time.Now(), fmt.Sprint(i), seq)
			})
			if err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	restarted := New(Options{Hub: newFakeHub(), Store: func() Store { return st }})
	status, err := restarted.resourceStatus(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	granted, queued := 0, 0
	for _, l := range status[0].Leases {
		if l.State == "granted" {
			granted++
		}
		if l.State == "queued" {
			queued++
		}
	}
	if granted != 3 || queued != 47 {
		t.Fatalf("after restart granted=%d queued=%d", granted, queued)
	}
}
func TestResourceNewJoinedCachedAndMirrorUnavailable(t *testing.T) {
	s, st, d := resourceFixture(t)
	ctx := t.Context()
	in := rq.Request{Action: "acquire", Resource: d.ID, Project: "example", Commit: strings.Repeat("a", 40), Groups: []string{"web"}, Holder: rq.Holder{Machine: "computer-a", Session: "one"}}
	first, err := s.resourceRequest(ctx, in)
	if err != nil || first.Status != "granted" {
		t.Fatalf("first: %+v %v", first, err)
	}
	in.Holder.Session = "two"
	joined, err := s.resourceRequest(ctx, in)
	if err != nil || joined.Status != "joined" || joined.Lease.ID != first.Lease.ID {
		t.Fatalf("joined: %+v %v", joined, err)
	}
	_, err = s.resourceRequest(ctx, rq.Request{Action: "release", Resource: d.ID, ID: first.Lease.ID, Token: first.Lease.Token, Holder: first.Lease.Holder, Outcome: &rq.Outcome{Exit: 0, Run: "example-run"}})
	if err != nil {
		t.Fatal(err)
	}
	cached, err := s.resourceRequest(ctx, in)
	if err != nil || cached.Status != "cached" {
		t.Fatalf("cached: %+v %v", cached, err)
	}
	previous := rq.Snapshot{ObservedAt: time.Now().Add(-time.Minute), Queue: &rq.Queue{V: 1, Slots: 3, Running: []rq.Job{}, Waiting: []rq.Job{}}, Unknown: false}
	if err := st.PutSetting(ctx, resourceMirrorKey(d.ID), previous); err != nil {
		t.Fatal(err)
	}
	s.resourceRefresh(context.Background())
	snap := s.resourceMirror(ctx, d.ID)
	if !snap.Unknown || snap.Queue == nil || !snap.ObservedAt.Equal(previous.ObservedAt) {
		t.Fatalf("stale snapshot lost: %+v", snap)
	}
}
