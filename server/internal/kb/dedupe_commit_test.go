package kb

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDedupeCommitKeepsACompletedUnmerge(t *testing.T) {
	for _, same := range []bool{false, true} {
		t.Run(map[bool]string{false: "different", true: "same"}[same], func(t *testing.T) {
			e := testEngine(t, nil)
			mergeFixture(t, e)
			if _, err := e.Unmerge("drop"); err != nil {
				t.Fatal(err)
			}
			key := pairKey("keep", "drop")
			before := e.Store.dedupeVerdicts()[key]
			// This answer was computed before the user's split and is now stale, including a "different" answer:
			// replacing Split with Same=false would allow another automatic judgment after dedupeRecheck.
			merged, err := e.commitDedupe(key, dedupeVerdict{Same: same, At: time.Now().UTC()}, "keep", "drop")
			if err != nil || merged {
				t.Fatalf("stale answer committed: merged=%v err=%v", merged, err)
			}
			if got := e.Store.dedupeVerdicts()[key]; got != before {
				t.Fatalf("split changed: got %+v want %+v", got, before)
			}
			for _, slug := range []string{"keep", "drop"} {
				if p, ok := e.Store.Project(slug); !ok || !p.Live() {
					t.Fatalf("project %s was merged again: %+v", slug, p)
				}
			}
		})
	}
}

func TestDedupeCommitWaitsForProjectMutation(t *testing.T) {
	e := testEngine(t, nil)
	for _, slug := range []string{"keep", "drop"} {
		if err := e.Store.PutProject(&Project{Slug: slug, Name: "Example " + slug, Status: "active"}); err != nil {
			t.Fatal(err)
		}
	}
	e.Store.applyMu.Lock()
	locked := true
	defer func() {
		if locked {
			e.Store.applyMu.Unlock()
		}
	}()
	type result struct {
		merged bool
		err    error
	}
	started, done := make(chan struct{}), make(chan result, 1)
	go func() {
		close(started)
		merged, err := e.commitDedupe(pairKey("keep", "drop"), dedupeVerdict{Same: true}, "keep", "drop")
		done <- result{merged, err}
	}()
	<-started
	select {
	case got := <-done:
		t.Fatalf("commit did not wait for the project mutation lock: %+v", got)
	case <-time.After(100 * time.Millisecond):
	}
	// A manual edit completes before the queued automatic result can acquire the mutation lock.
	if err := e.mergeProjects("keep", "drop", "manual", true); err != nil {
		t.Fatal(err)
	}
	drop, _ := e.Store.Project("drop")
	if err := e.unmergeProject(drop); err != nil {
		t.Fatal(err)
	}
	e.Store.applyMu.Unlock()
	locked = false
	select {
	case got := <-done:
		if got.err != nil || got.merged {
			t.Fatalf("queued result reversed the completed unmerge: %+v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("queued result did not finish")
	}
	for _, slug := range []string{"keep", "drop"} {
		if p, ok := e.Store.Project(slug); !ok || !p.Live() {
			t.Fatalf("project %s was merged again: %+v", slug, p)
		}
	}
	if !e.Store.dedupeVerdicts()[pairKey("keep", "drop")].Split {
		t.Fatal("queued result overwrote the user's split")
	}
}

func TestDedupeCommitStopsWhenVerdictCannotBeStored(t *testing.T) {
	e := testEngine(t, nil)
	for _, slug := range []string{"keep", "drop"} {
		if err := e.Store.PutProject(&Project{Slug: slug, Name: "Example " + slug, Status: "active"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(e.Store.dedupePath(), 0700); err != nil {
		t.Fatal(err)
	}
	merged, err := e.commitDedupe(pairKey("keep", "drop"), dedupeVerdict{Same: true}, "keep", "drop")
	if err == nil || merged {
		t.Fatalf("unrecorded verdict merged projects: merged=%v err=%v", merged, err)
	}
	for _, slug := range []string{"keep", "drop"} {
		if p, ok := e.Store.Project(slug); !ok || !p.Live() {
			t.Fatalf("project %s changed after write failure: %+v", slug, p)
		}
	}
}

func TestDedupeVerdictRefusesReadOnlyStore(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "absent")
	store, err := OpenReadOnly(dir)
	if err != nil {
		t.Fatal(err)
	}
	verdict := dedupeVerdict{Same: true}
	if err := store.putDedupeVerdict("keep|drop", verdict); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("store write: %v", err)
	}
	if merged, err := (&Engine{Store: store}).commitDedupe("keep|drop", verdict, "keep", "drop"); merged || !errors.Is(err, ErrReadOnly) {
		t.Fatalf("engine write: merged=%v err=%v", merged, err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only verdict created archive: %v", err)
	}
}
