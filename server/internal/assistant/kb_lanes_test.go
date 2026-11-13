package assistant

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/ai"
	"github.com/nwverse/persona/server/internal/kb"
)

// kbRoundNow does what a round of the loop starts, every lane once, one after another (the loop runs each in its own
// goroutine).
func kbRoundNow(ctx context.Context, s *Service, e *kb.Engine) {
	lanes := s.kbLaneSet(&kbRounds{})
	if !s.kbPrepare(ctx, e) {
		return
	}
	for _, l := range lanes.lanes {
		s.kbRunLane(ctx, e, l)
	}
}

// waitEnded takes the next lane end from k, failing the test after a few seconds.
func waitEnded(t *testing.T, k *kbLanes) *kbLane {
	t.Helper()
	select {
	case l := <-k.done:
		k.ended(l)
		return l
	case <-time.After(5 * time.Second):
		t.Fatal("no lane ended")
		return nil
	}
}

// A slow lane does not hold back another (#4247): while one run of the slow lane goes on, the quick lane runs again at
// every round; the slow one is not started a second time, and runs again once it ends because it became due meanwhile.
func TestKBLaneSlowDoesNotHoldBackAnother(t *testing.T) {
	release, started := make(chan struct{}), make(chan struct{}, 2)
	var slowRuns, quickRuns atomic.Int32
	slow := &kbLane{name: "slow", run: func(context.Context, *kb.Engine) { slowRuns.Add(1); started <- struct{}{}; <-release }}
	quick := &kbLane{name: "quick", run: func(context.Context, *kb.Engine) { quickRuns.Add(1) }}
	k := newKBLanes(slow, quick)
	run := func(l *kbLane) { l.run(context.Background(), nil) }
	ready := func() bool { return true }

	k.startDue(ready, run)
	<-started
	if l := waitEnded(t, k); l != quick {
		t.Fatalf("%s ended first", l.name)
	}
	for round := 2; round <= 3; round++ {
		k.tick()
		k.startDue(ready, run)
		if l := waitEnded(t, k); l != quick || quickRuns.Load() != int32(round) {
			t.Fatalf("round %d: %s ended, quick ran %d time(s)", round, l.name, quickRuns.Load())
		}
	}
	if slowRuns.Load() != 1 || !slow.running || !slow.due {
		t.Fatalf("slow: %d run(s), running %v, due %v; want 1, true, true", slowRuns.Load(), slow.running, slow.due)
	}
	if !k.busy() {
		t.Fatal("not busy while the slow lane runs")
	}
	close(release)
	if l := waitEnded(t, k); l != slow {
		t.Fatalf("%s ended", l.name)
	}
	k.startDue(ready, run) // it became due while it ran: it starts again at once
	if l := waitEnded(t, k); l != slow || slowRuns.Load() != 2 {
		t.Fatalf("%s ended, slow ran %d time(s)", l.name, slowRuns.Load())
	}
	if k.busy() {
		t.Fatal("busy with every lane ended")
	}
}

// Nothing due: prepare is not called. prepare false: no lane starts and they stay due for the next round.
func TestKBLanePrepare(t *testing.T) {
	var runs, prepared atomic.Int32
	l := &kbLane{name: "one", run: func(context.Context, *kb.Engine) { runs.Add(1) }}
	k := newKBLanes(l)
	run := func(l *kbLane) { l.run(context.Background(), nil) }
	k.startDue(func() bool { prepared.Add(1); return false }, run)
	if prepared.Load() != 1 || runs.Load() != 0 || !l.due || l.running {
		t.Fatalf("prepared %d, ran %d, due %v, running %v", prepared.Load(), runs.Load(), l.due, l.running)
	}
	k.startDue(func() bool { prepared.Add(1); return true }, run)
	waitEnded(t, k)
	k.startDue(func() bool { prepared.Add(1); return true }, run) // not due
	if prepared.Load() != 2 || runs.Load() != 1 {
		t.Fatalf("prepared %d, ran %d; want 2, 1", prepared.Load(), runs.Load())
	}
}

type lanesModel struct{ err error }

func (m lanesModel) JSONMax(context.Context, string, string, int, any) error { return m.err }

// A lane run's model calls are counted through its context: answered, failed (an error, its own time running out) and
// put off; a call cancelled with its run counts as nothing, and calls outside a lane run are not counted.
func TestKBCountedModel(t *testing.T) {
	st := &kbLaneStats{}
	ctx := context.WithValue(context.Background(), kbLaneStatsKey{}, st)
	paused := &ai.PausedError{Pause: ai.Pause{Reason: ai.ReasonBusy, Until: time.Now()}}
	for _, err := range []error{nil, nil, errors.New("example"), paused, context.Canceled, context.DeadlineExceeded} {
		_ = kbCounted(lanesModel{err: err}).JSONMax(ctx, "", "", 1, nil)
	}
	_ = kbCounted(lanesModel{}).JSONMax(context.Background(), "", "", 1, nil)
	if st.calls.Load() != 4 || st.failed.Load() != 2 || st.putOff.Load() != 1 {
		t.Fatalf("calls %d, failed %d, put off %d; want 4, 2, 1", st.calls.Load(), st.failed.Load(), st.putOff.Load())
	}
	if kbCounted(nil) != nil {
		t.Fatal("a missing model became one")
	}
}

// The index lane (the search index, kb/search_index.go) builds the index on its first run and then follows the
// files at most every kbIndexEvery, since a run looks at every file and a live message kicks a round every few
// seconds; text deleted on purpose does not wait, so the index never keeps it past the next round.
func TestKBIndexLaneRunsWhenThereIsSomethingToDo(t *testing.T) {
	s := newKBService(t, newFakeHub())
	e := kbEng(t, s)
	ctx := context.Background()
	r := &kbIndexRuns{}
	if e.Store.SearchIndexReady() || !e.Store.SearchIndexWanted() {
		t.Fatal("a new archive says it has a search index")
	}
	if !s.kbIndexLane(ctx, e, r) || !e.Store.SearchIndexReady() || r.last.IsZero() {
		t.Fatal("the first run built no index")
	}
	if s.kbIndexLane(ctx, e, r) {
		t.Fatal("ran again at once with nothing asked of it")
	}
	e.Store.SearchIndexPurge()
	if !s.kbIndexLane(ctx, e, r) || e.Store.SearchIndexWanted() {
		t.Fatal("text was deleted on purpose and the index waited")
	}
	r.last = r.last.Add(-kbIndexEvery)
	if !s.kbIndexLane(ctx, e, r) {
		t.Fatalf("did not run %s after its last run", kbIndexEvery)
	}
	// The loop's lanes have it, and a round runs it.
	var lane *kbLane
	for _, l := range s.kbLaneSet(&kbRounds{}).lanes {
		if l.name == "index" {
			lane = l
		}
	}
	if lane == nil {
		t.Fatal("the knowledge base has no index lane")
	}
	if err := os.RemoveAll(filepath.Join(e.Store.Dir, kb.SearchIndexDir)); err != nil {
		t.Fatal(err)
	}
	s.kbRunLane(ctx, e, lane)
	if _, err := os.Stat(filepath.Join(e.Store.Dir, kb.SearchIndexDir, "manifest.json")); err != nil {
		t.Fatalf("the lane wrote no index: %v", err)
	}
}

// An index run that fails (here: the index's directory cannot be made, as with a full disk or a directory the hub
// may not write) is not tried again at every kick: the next waits, longer after every failure in a row up to
// kbIndexRetryMax, and the log has the failure once however often it comes back. Another failure is said; so is the
// run that works again, after which the lane is as it was.
func TestKBIndexLaneWaitsAfterARunThatFailed(t *testing.T) {
	s := newKBService(t, newFakeHub())
	e := kbEng(t, s)
	ctx := context.Background()
	var logged []string
	s.logf = func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) }
	index := filepath.Join(e.Store.Dir, kb.SearchIndexDir)
	// A file where the index's directory goes: nothing can be written in it, whoever runs the test.
	if err := os.WriteFile(index, []byte("not a directory\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := &kbIndexRuns{}
	if !s.kbIndexLane(ctx, e, r) {
		t.Fatal("the first run did not start")
	}
	if len(logged) != 1 || r.failed != 1 || r.wait != kbIndexRetry {
		t.Fatalf("after the first failed run: logged %q, %d failed, waiting %s; want one line, one failure, %s", logged, r.failed, r.wait, kbIndexRetry)
	}
	if !e.Store.SearchIndexWanted() {
		t.Fatal("the index is not wanted: the kicks below would not have started a run anyway")
	}
	// Kicks: an index is still wanted, and no run starts until the wait is over.
	for range 5 {
		if s.kbIndexLane(ctx, e, r) {
			t.Fatalf("ran again within %s of a run that failed", r.wait)
		}
	}
	// The wait over, time after time: it grows to kbIndexRetryMax, and the same failure is not said again.
	waits := []time.Duration{}
	for range 8 {
		r.last = r.last.Add(-r.wait)
		if !s.kbIndexLane(ctx, e, r) {
			t.Fatalf("did not run %s after the run that failed", r.wait)
		}
		waits = append(waits, r.wait)
		if s.kbIndexLane(ctx, e, r) {
			t.Fatalf("ran again within %s of a run that failed", r.wait)
		}
	}
	want := []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute, kbIndexRetryMax, kbIndexRetryMax, kbIndexRetryMax, kbIndexRetryMax}
	if !reflect.DeepEqual(waits, want) {
		t.Fatalf("the waits after each failure were %v, want %v", waits, want)
	}
	if len(logged) != 1 || r.failed != 9 {
		t.Fatalf("after %d failed runs the log has %d line(s): %q", r.failed, len(logged), logged)
	}
	// Another failure is said: the directory is there now, its manifest cannot be read.
	if err := os.Remove(index); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(index, "manifest.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	r.last = r.last.Add(-r.wait)
	if !s.kbIndexLane(ctx, e, r) || len(logged) != 2 || logged[1] == logged[0] || r.wait != kbIndexRetryMax {
		t.Fatalf("after another failure: logged %q, waiting %s", logged, r.wait)
	}
	for _, l := range logged {
		if !strings.HasPrefix(l, "kb: search index: ") {
			t.Errorf("the log line %q is not the index's", l)
		}
	}
	// It works again: said once, the index is built, and the lane is back to its ordinary pace.
	if err := os.RemoveAll(index); err != nil {
		t.Fatal(err)
	}
	if s.kbIndexLane(ctx, e, r) {
		t.Fatal("ran before its wait was over")
	}
	r.last = r.last.Add(-r.wait)
	if !s.kbIndexLane(ctx, e, r) || !e.Store.SearchIndexReady() {
		t.Fatal("the run after the directory was put right built no index")
	}
	if r.wait != 0 || r.failed != 0 || r.said != "" {
		t.Fatalf("after a run that worked: %+v", r)
	}
	if n := len(logged); n < 3 || !strings.Contains(logged[2], "updating again after 10 failed run(s)") {
		t.Fatalf("the log after the run that worked: %q", logged)
	}
	if s.kbIndexLane(ctx, e, r) {
		t.Fatal("ran again at once with nothing asked of it")
	}
	// A run stopped with the manager is not a failure: nothing waits because of it.
	e.Store.SearchIndexPurge()
	stopped, cancel := context.WithCancel(ctx)
	cancel()
	said := len(logged)
	if !s.kbIndexLane(stopped, e, r) || r.wait != 0 || r.failed != 0 || len(logged) != said {
		t.Fatalf("after a run stopped with the manager: %+v, logged %q", r, logged[said:])
	}
}
