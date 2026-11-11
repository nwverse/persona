package kb

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/ai"
)

// The knowledge base's per-source steps (#4246): a failed call counts toward the run's max like a successful one, the
// failed source waits (retryWait) while the next goes on, and a paused connection stops the run without counting.

func paused() error {
	return &ai.PausedError{Pause: ai.Pause{Reason: ai.ReasonBusy, Until: time.Now().Add(time.Minute)}}
}

// Five failing projects, max 3: three calls, not five; the two left go next run, the three wait.
func TestRefreshProjectsCountsFailuresTowardMax(t *testing.T) {
	ctx := context.Background()
	m := &failingModel{err: errors.New("ai: no JSON object in reply")}
	e := testEngine(t, m)
	for _, slug := range []string{"a", "b", "c", "d", "e"} {
		dirtyProject(t, e, slug)
	}
	if n := e.RefreshProjects(ctx, 3); n != 0 || m.calls != 3 {
		t.Fatalf("refreshed %d with %d call(s), want 0 with 3", n, m.calls)
	}
	if n := e.RefreshProjects(ctx, 3); n != 0 || m.calls != 5 {
		t.Fatalf("second run: %d call(s) in all, want 5 (the two left)", m.calls)
	}
	if n := e.RefreshProjects(ctx, 3); m.calls != 5 || n != 0 {
		t.Fatalf("third run sent %d more", m.calls-5)
	}
}

func completeRecording(t *testing.T, e *Engine, id string, start time.Time) {
	t.Helper()
	if _, err := e.PutRecording(id, recUpload(1, start, true, "示例：周五灰度"), start.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
}

// A recording whose summary fails waits and the next one is still summarized; the wait over and the model back, it is
// summarized too. It used to stop the run at the first failure and send that recording again every round.
func TestSummarizeRecordingsBackOffPerRecording(t *testing.T) {
	ctx := context.Background()
	m := &failingModel{err: errors.New("ai: no JSON object in reply")}
	e := testEngine(t, m)
	var logs []string
	e.Logf = func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) }
	start := time.Now().Add(-2 * time.Hour)
	newer, older := "7c9e6679-7425-40de-944b-e07fc1f90ae7", "9b2f6c1e-3d4a-4f5b-8c7d-2e1f0a9b8c6d"
	completeRecording(t, e, older, start)
	completeRecording(t, e, newer, start.Add(time.Minute))

	if n := e.SummarizeRecordings(ctx, 3); n != 0 || m.calls != 2 {
		t.Fatalf("summarized %d with %d call(s), want 0 with 2 (both tried)", n, m.calls)
	}
	if len(e.recordingRetries) != 2 || len(logs) != 2 {
		t.Fatalf("retries %v, logs %q", e.recordingRetries, logs)
	}
	if n := e.SummarizeRecordings(ctx, 3); n != 0 || m.calls != 2 {
		t.Fatalf("a run before the next try sent %d more", m.calls-2)
	}

	m.err = nil
	r := e.recordingRetries[older]
	r.at = time.Now().Add(-time.Second) // only the older one's wait is over
	e.recordingRetries[older] = r
	if n := e.SummarizeRecordings(ctx, 3); n != 1 || m.calls != 3 {
		t.Fatalf("summarized %d with %d call(s) in all, want 1 with 3", n, m.calls)
	}
	if _, ok := e.recordingRetries[older]; ok {
		t.Error("the success did not clear its failures")
	}
	if rec, _ := e.Store.Recording(older); rec.SummarizedRev != 1 {
		t.Errorf("older: summarized revision %d", rec.SummarizedRev)
	}
	if rec, _ := e.Store.Recording(newer); rec.SummarizedRev != 0 {
		t.Error("newer was summarized before its next try")
	}
}

// A paused connection: nothing counts as failed, both stay due for the next run.
func TestSummarizeRecordingsPausedDoesNotCount(t *testing.T) {
	m := &failingModel{err: paused()}
	e := testEngine(t, m)
	start := time.Now().Add(-2 * time.Hour)
	completeRecording(t, e, "7c9e6679-7425-40de-944b-e07fc1f90ae7", start)
	completeRecording(t, e, "9b2f6c1e-3d4a-4f5b-8c7d-2e1f0a9b8c6d", start.Add(time.Minute))
	if n := e.SummarizeRecordings(context.Background(), 3); n != 0 || m.calls != 1 || len(e.recordingRetries) != 0 {
		t.Fatalf("summarized %d, %d call(s), retries %v; want 0, 1, none", n, m.calls, e.recordingRetries)
	}
}

func untaggedItems(t *testing.T, e *Engine, n int) {
	t.Helper()
	for i := range n {
		if err := e.Store.PutItem(&Item{Kind: KindDoc, ID: fmt.Sprintf("d%03d", i), Title: fmt.Sprintf("示例文档 %d", i)}); err != nil {
			t.Fatal(err)
		}
	}
}

func untagged(e *Engine) int {
	n := 0
	for _, it := range e.Store.Items(KindDoc) {
		if it.TaggedAt.IsZero() {
			n++
		}
	}
	return n
}

// At most max calls a run; the rest are left (more) for the next run, which goes on with them.
func TestTagItemsStopsAtMax(t *testing.T) {
	ctx := context.Background()
	m := &failingModel{}
	e := testEngine(t, m)
	untaggedItems(t, e, 100)
	more, err := e.TagItems(ctx, 2)
	if err != nil || !more || m.calls != 2 || untagged(e) != 100-2*tagItemsBatch {
		t.Fatalf("more %v, err %v, %d call(s), %d untagged; want true, nil, 2, %d", more, err, m.calls, untagged(e), 100-2*tagItemsBatch)
	}
	more, err = e.TagItems(ctx, 2)
	if err != nil || more || m.calls != 3 || untagged(e) != 0 {
		t.Fatalf("second run: more %v, err %v, %d call(s), %d untagged", more, err, m.calls, untagged(e))
	}
}

// A failing batch waits (its items) and the next batch goes on; the failures count toward max. The model back and the
// wait over, they are tagged.
func TestTagItemsBackOffPerBatch(t *testing.T) {
	ctx := context.Background()
	m := &failingModel{err: errors.New("ai: no JSON object in reply")}
	e := testEngine(t, m)
	untaggedItems(t, e, 50) // two batches
	more, err := e.TagItems(ctx, 3)
	if err != nil || more || m.calls != 2 || len(e.itemRetries) != 50 {
		t.Fatalf("more %v, err %v, %d call(s), %d waiting; want false, nil, 2, 50", more, err, m.calls, len(e.itemRetries))
	}
	if _, err := e.TagItems(ctx, 3); err != nil || m.calls != 2 {
		t.Fatalf("a run before the next try sent %d more (%v)", m.calls-2, err)
	}
	m.err = nil
	for k, r := range e.itemRetries {
		r.at = time.Now().Add(-time.Second)
		e.itemRetries[k] = r
	}
	if _, err := e.TagItems(ctx, 3); err != nil || m.calls != 4 || untagged(e) != 0 || len(e.itemRetries) != 0 {
		t.Fatalf("after the wait: err %v, %d call(s), %d untagged, %d waiting", err, m.calls, untagged(e), len(e.itemRetries))
	}
}

// A paused connection returns at once: no item waits, all stay untagged and more is reported (the next round goes on).
func TestTagItemsPausedDoesNotCount(t *testing.T) {
	m := &failingModel{err: paused()}
	e := testEngine(t, m)
	untaggedItems(t, e, 50)
	more, err := e.TagItems(context.Background(), 3)
	if !ai.IsPaused(err) || !more || m.calls != 1 || len(e.itemRetries) != 0 || untagged(e) != 50 {
		t.Fatalf("more %v, err %v, %d call(s), %d waiting, %d untagged", more, err, m.calls, len(e.itemRetries), untagged(e))
	}
}

// notConfigured is the error of a request with no model set up for it.
func notConfigured(t *testing.T) error {
	t.Helper()
	err := ai.New(ai.Config{}).JSONMax(context.Background(), "", "", 1, &struct{}{})
	if !ai.IsNotConfigured(err) {
		t.Fatalf("not the not-configured error: %v", err)
	}
	return err
}

// No model set up is not a source's failure: nothing waits for it (it used to be retried next round, and still is), and
// item tagging reports no more (the next collection asks again).
func TestNotConfiguredDoesNotBackOff(t *testing.T) {
	ctx := context.Background()
	m := &failingModel{err: notConfigured(t)}
	e := testEngine(t, m)
	start := time.Now().Add(-2 * time.Hour)
	completeRecording(t, e, "7c9e6679-7425-40de-944b-e07fc1f90ae7", start)
	completeRecording(t, e, "9b2f6c1e-3d4a-4f5b-8c7d-2e1f0a9b8c6d", start.Add(time.Minute))
	if n := e.SummarizeRecordings(ctx, 3); n != 0 || m.calls != 1 || len(e.recordingRetries) != 0 {
		t.Fatalf("recordings: %d, %d call(s), %d waiting; want 0, 1, 0", n, m.calls, len(e.recordingRetries))
	}
	untaggedItems(t, e, 10)
	if more, err := e.TagItems(ctx, 3); !ai.IsNotConfigured(err) || more || len(e.itemRetries) != 0 {
		t.Fatalf("items: more %v, err %v, %d waiting", more, err, len(e.itemRetries))
	}
	dirtyProject(t, e, "a")
	if e.RefreshProjects(ctx, 3); len(e.projectRetries) != 0 {
		t.Fatalf("projects: %d waiting", len(e.projectRetries))
	}
}

// A recording that failed is tried again at once when a new revision comes (the wait was for the old one); an item
// whose title changed likewise.
func TestChangedSourceDoesNotWait(t *testing.T) {
	ctx := context.Background()
	m := &failingModel{err: errors.New("ai: no JSON object in reply")}
	e := testEngine(t, m)
	start := time.Now().Add(-2 * time.Hour)
	id := "7c9e6679-7425-40de-944b-e07fc1f90ae7"
	completeRecording(t, e, id, start)
	e.SummarizeRecordings(ctx, 3)
	if e.SummarizeRecordings(ctx, 3); m.calls != 1 {
		t.Fatalf("%d call(s) before the new revision, want 1", m.calls)
	}
	if _, err := e.PutRecording(id, recUpload(2, start, true, "示例：改成周四灰度"), start.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	m.err = nil
	if n := e.SummarizeRecordings(ctx, 3); n != 1 || m.calls != 2 {
		t.Fatalf("new revision: summarized %d with %d call(s) in all, want 1 with 2", n, m.calls)
	}

	m.err, m.calls = errors.New("ai: no JSON object in reply"), 0
	untaggedItems(t, e, 1)
	_, _ = e.TagItems(ctx, 3)
	if _, _ = e.TagItems(ctx, 3); m.calls != 1 {
		t.Fatalf("%d call(s) before the edit, want 1", m.calls)
	}
	it, _ := e.Store.Item(KindDoc, "d000")
	it.Title = "示例文档（改）"
	if err := e.Store.PutItem(it); err != nil {
		t.Fatal(err)
	}
	if _, _ = e.TagItems(ctx, 3); m.calls != 2 {
		t.Fatalf("%d call(s) after the edit, want 2", m.calls)
	}
}
