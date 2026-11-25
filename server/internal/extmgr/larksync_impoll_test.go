package extmgr

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/larksyncwire"
)

// Chat-enabled (full_with_im) bindings run a lightweight IM incremental sync every 15 minutes between daily full runs,
// reusing the same per-binding reader (and rate limiter), never overlapping a full run, respecting rate-limit backoff,
// and stopping when im_poll is turned off in Settings while the daily full run continues unchanged.
func TestLarkSyncIMPollScheduleToggleAndMutualExclusion(t *testing.T) {
	w := newLarkWorld(t)
	ctx := context.Background()

	w.addSyncBot(t, "bsync", "cli_sync", larksyncwire.TierFull)
	fullKey, fullAcc := w.adopt(t, "bsync", "ou_full", "acc-full", "ref-full", 24*time.Hour)
	docsKey, _ := w.adopt(t, larksyncwire.PrimaryBot, "ou_docs", "acc-docs", "ref-docs", 24*time.Hour)
	if _, err := w.h.larkSyncChange(ctx, func(st *larkSyncState) error {
		st.binding(fullKey).Tier = larksyncwire.TierFull
		st.binding(docsKey).Tier = larksyncwire.TierDocs
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var modes []string
	var nextRes larksyncwire.Result
	w.script("bsync", fullAcc, func(_ context.Context, b larksyncwire.Binding, _ func(context.Context) (string, error), _ map[string]string, _ larksyncwire.Index) (larksyncwire.Result, error) {
		mu.Lock()
		modes = append(modes, b.Mode)
		res := nextRes
		nextRes = larksyncwire.Result{}
		mu.Unlock()
		return res, nil
	})

	// Initial GET shows im_poll enabled by default.
	code, view := w.do(t, "GET", "/api/v1/bots/feishu/sync", "")
	if code != 200 || view["im_poll"] != true {
		t.Fatalf("initial view = %d %v, want im_poll=true", code, view)
	}

	// First tick runs the initial full sync for both bindings.
	w.h.larkSyncTick(ctx)
	fullNextDaily := w.binding(t, fullKey).NextRunAt
	if w.readers != 2 || !slices.Equal(modes, []string{larksyncwire.ModeFull}) {
		t.Fatalf("after first tick: readers=%d modes=%v", w.readers, modes)
	}

	// 10 minutes later: not yet due.
	w.advance(10 * time.Minute)
	w.h.larkSyncTick(ctx)
	if len(modes) != 1 {
		t.Fatalf("ran early at 10m: modes=%v", modes)
	}

	// 15 minutes after the full sync: only full_with_im runs ModeIM; docs_standard does not; same reader reused;
	// daily NextRunAt is untouched; unreadable Doc.Note reaches the outbox.
	w.advance(5 * time.Minute)
	mu.Lock()
	nextRes = larksyncwire.Result{
		Messages: []larksyncwire.Message{{ID: "om_inc", ChatID: "oc_1", ChatType: "p2p", Text: "hi", At: w.now()}},
		Docs: []larksyncwire.Doc{{
			Key:      larksyncwire.KeyDoc + "doxNoPerm",
			Kind:     "docs",
			ObjToken: "doxNoPerm",
			ObjType:  "docx",
			Title:    "https://my.feishu.cn/docx/doxNoPerm",
			URL:      "https://my.feishu.cn/docx/doxNoPerm",
			Note:     "unreadable: docx document (forbidden, code 1770032)",
			Revision: 0,
		}},
		Cursors: map[string]string{"im:oc_1": "1700000001000"},
	}
	mu.Unlock()
	docsBefore := countOf(w.ran, docsKey)
	w.h.larkSyncTick(ctx)
	if !slices.Equal(modes, []string{larksyncwire.ModeFull, larksyncwire.ModeIM}) {
		t.Fatalf("at 15m: modes=%v, want [full, im]", modes)
	}
	if countOf(w.ran, docsKey) != docsBefore {
		t.Fatalf("docs_standard binding ran during IM poll")
	}
	if w.readers != 2 {
		t.Fatalf("IM poll created a second reader (%d), must reuse the binding's reader/limiter", w.readers)
	}
	fb := w.binding(t, fullKey)
	if !fb.NextRunAt.Equal(fullNextDaily) {
		t.Fatalf("IM poll moved daily NextRunAt: got %v, want %v", fb.NextRunAt, fullNextDaily)
	}
	if !fb.NextIMRunAt.Equal(w.now().Add(larkSyncIMEvery)) {
		t.Fatalf("NextIMRunAt = %v, want %v", fb.NextIMRunAt, w.now().Add(larkSyncIMEvery))
	}
	items, err := larkSyncItems(larkSyncConnector{w.h}).Items(ctx)
	if err != nil || len(items) != 1 || items[0].Note != "unreadable: docx document (forbidden, code 1770032)" {
		t.Fatalf("outbox items = %+v, err = %v", items, err)
	}

	// Rate-limited IM poll backs off for larkSyncPartialNext (1 hour), not 15 minutes.
	w.advance(larkSyncIMEvery)
	mu.Lock()
	nextRes = larksyncwire.Result{
		Errors: []larksyncwire.ResourceError{{Resource: "im:oc_1", Kind: larksyncwire.ErrKindRateLimited, Detail: "429"}},
	}
	mu.Unlock()
	w.h.larkSyncTick(ctx)
	if fb := w.binding(t, fullKey); fb.Health != larksyncwire.HealthRateLimited || !fb.NextIMRunAt.Equal(w.now().Add(larkSyncPartialNext)) {
		t.Fatalf("rate-limited IM poll: health=%s nextIM=%v", fb.Health, fb.NextIMRunAt)
	}

	// Turning im_poll off in Settings stops 15-minute polls while leaving the daily full run intact.
	code, view = w.do(t, "PUT", "/api/v1/bots/feishu/sync", `{"im_poll":false}`)
	if code != 200 || view["im_poll"] != false {
		t.Fatalf("disable im_poll = %d %v", code, view)
	}
	w.advance(2 * time.Hour)
	beforeOff := len(modes)
	w.h.larkSyncTick(ctx)
	if len(modes) != beforeOff {
		t.Fatalf("IM poll ran while im_poll=false: modes=%v", modes)
	}

	// Reaching the daily slot still runs ModeFull even when im_poll is off.
	w.advance(w.binding(t, fullKey).NextRunAt.Sub(w.now()))
	w.h.larkSyncTick(ctx)
	if len(modes) != beforeOff+1 || modes[len(modes)-1] != larksyncwire.ModeFull {
		t.Fatalf("daily full run with im_poll=false: modes=%v", modes)
	}

	// Turning im_poll back on resumes 15-minute polls.
	code, view = w.do(t, "PUT", "/api/v1/bots/feishu/sync", `{"im_poll":true}`)
	if code != 200 || view["im_poll"] != true {
		t.Fatalf("re-enable im_poll = %d %v", code, view)
	}
	w.advance(larkSyncIMEvery)
	w.h.larkSyncTick(ctx)
	if modes[len(modes)-1] != larksyncwire.ModeIM {
		t.Fatalf("IM poll did not resume after re-enabling: modes=%v", modes)
	}

	// Mutual exclusion: while a full run holds rt.running, a concurrent IM poll is refused with errLarkSyncRunning.
	started := make(chan struct{})
	release := make(chan struct{})
	w.script("bsync", fullAcc, func(_ context.Context, _ larksyncwire.Binding, _ func(context.Context) (string, error), _ map[string]string, _ larksyncwire.Index) (larksyncwire.Result, error) {
		close(started)
		<-release
		return larksyncwire.Result{}, nil
	})
	done := make(chan error, 1)
	go func() { done <- w.h.larkSyncRunMode(ctx, fullKey, larksyncwire.ModeFull) }()
	<-started
	if err := w.h.larkSyncRunMode(ctx, fullKey, larksyncwire.ModeIM); !errors.Is(err, errLarkSyncRunning) {
		close(release)
		t.Fatalf("concurrent ModeIM during ModeFull returned %v, want errLarkSyncRunning", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("held ModeFull failed: %v", err)
	}
}
