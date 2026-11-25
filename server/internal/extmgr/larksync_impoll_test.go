package extmgr

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/kbwire"
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

// In ModeIM, a partial run that still made forward progress (delivered messages or moved a cursor) keeps the
// 15-minute cadence (larkSyncIMEvery), while a partial run with zero progress backs off to 1 hour (larkSyncPartialNext).
func TestLarkSyncIMPollPartialWithProgressKeeps15MinuteCadence(t *testing.T) {
	w := newLarkWorld(t)
	ctx := context.Background()

	w.addSyncBot(t, "bsync", "cli_sync", larksyncwire.TierFull)
	fullKey, fullAcc := w.adopt(t, "bsync", "ou_full", "acc-full", "ref-full", 24*time.Hour)
	if _, err := w.h.larkSyncChange(ctx, func(st *larkSyncState) error {
		st.binding(fullKey).Tier = larksyncwire.TierFull
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var nextRes larksyncwire.Result
	w.script("bsync", fullAcc, func(_ context.Context, _ larksyncwire.Binding, _ func(context.Context) (string, error), _ map[string]string, _ larksyncwire.Index) (larksyncwire.Result, error) {
		mu.Lock()
		res := nextRes
		nextRes = larksyncwire.Result{}
		mu.Unlock()
		return res, nil
	})

	// Initial full run seeds cursor.
	mu.Lock()
	nextRes = larksyncwire.Result{Cursors: map[string]string{"im:oc_big": "1000"}}
	mu.Unlock()
	w.h.larkSyncTick(ctx)

	// 15m later: ModeIM partial run that advances cursor im:oc_big -> 2000 keeps 15m schedule.
	w.advance(larkSyncIMEvery)
	mu.Lock()
	nextRes = larksyncwire.Result{
		Partial: true,
		Cursors: map[string]string{"im:oc_big": "2000"},
	}
	mu.Unlock()
	w.h.larkSyncTick(ctx)
	if fb := w.binding(t, fullKey); !fb.NextIMRunAt.Equal(w.now().Add(larkSyncIMEvery)) {
		t.Fatalf("partial ModeIM with cursor progress: NextIMRunAt=%v, want +15m (%v)", fb.NextIMRunAt, w.now().Add(larkSyncIMEvery))
	}

	// 15m later: ModeIM partial run with NO new messages/docs and NO cursor movement (im:oc_big stays 2000) backs off 1h.
	w.advance(larkSyncIMEvery)
	mu.Lock()
	nextRes = larksyncwire.Result{
		Partial: true,
		Cursors: map[string]string{"im:oc_big": "2000"},
	}
	mu.Unlock()
	w.h.larkSyncTick(ctx)
	if fb := w.binding(t, fullKey); !fb.NextIMRunAt.Equal(w.now().Add(larkSyncPartialNext)) {
		t.Fatalf("partial ModeIM with zero progress: NextIMRunAt=%v, want +1h (%v)", fb.NextIMRunAt, w.now().Add(larkSyncPartialNext))
	}
}

// One-time sender-name backfill scans pre-#15486 outbox chat batches, skips bots and other bindings' messages, resolves
// unique user open_ids once, emits repair chat batches (including DM chat names), sets BackfillVersion, and is
// idempotent on subsequent runs (even if BackfillVersion is reset).
func TestLarkSyncBackfillSenderNamesIdempotentAndScoped(t *testing.T) {
	w := newLarkWorld(t)
	ctx := context.Background()

	w.addSyncBot(t, "bsyncA", "cli_a", larksyncwire.TierFull)
	w.addSyncBot(t, "bsyncB", "cli_b", larksyncwire.TierFull)
	keyA, accA := w.adopt(t, "bsyncA", "ou_owner_a", "acc-a", "ref-a", 24*time.Hour)
	keyB, _ := w.adopt(t, "bsyncB", "ou_owner_b", "acc-b", "ref-b", 24*time.Hour)
	if _, err := w.h.larkSyncChange(ctx, func(st *larkSyncState) error {
		st.binding(keyA).Tier = larksyncwire.TierFull
		st.binding(keyB).Tier = larksyncwire.TierFull
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Seed pre-#15486 outbox rows:
	// - oc_grp: one unnamed human (ou_alice), one bot (cli_bot), one already-named human (ou_named), one belonging to binding B (ou_other_tenant).
	// - oc_dm: an unnamed DM with one unnamed peer message (ou_bob).
	seedBatch := func(cb connector.ChatBatch) db.LarkSyncOutbox {
		raw, err := json.Marshal(cb)
		if err != nil {
			t.Fatal(err)
		}
		return db.LarkSyncOutbox{Kind: db.LarkSyncChat, CanonicalKey: "chat:" + cb.Chat.ID, Payload: raw}
	}
	at := w.now().Add(-time.Hour)
	if err := w.store.CommitLarkSyncRun(ctx, keyA, nil, []larksyncwire.IndexRecord{
		{Key: larksyncwire.KeyMessage + "om_a1", Kind: "message", Sources: []larksyncwire.SourceRef{{Binding: keyA, Entry: larksyncwire.EntryChat}}},
		{Key: larksyncwire.KeyMessage + "om_bot", Kind: "message", Sources: []larksyncwire.SourceRef{{Binding: keyA, Entry: larksyncwire.EntryChat}}},
		{Key: larksyncwire.KeyMessage + "om_named", Kind: "message", Sources: []larksyncwire.SourceRef{{Binding: keyA, Entry: larksyncwire.EntryChat}}},
		{Key: larksyncwire.KeyMessage + "om_b1", Kind: "message", Sources: []larksyncwire.SourceRef{{Binding: keyB, Entry: larksyncwire.EntryChat}}},
		{Key: larksyncwire.KeyMessage + "om_dm1", Kind: "message", Sources: []larksyncwire.SourceRef{{Binding: keyA, Entry: larksyncwire.EntryChat}}},
	}, []db.LarkSyncOutbox{
		seedBatch(connector.ChatBatch{
			Chat: kbwire.SourceChat{ID: "oc_grp", Name: "Example Group", Kind: "group"},
			Messages: []kbwire.SourceMessage{
				{ID: "om_a1", SenderID: "ou_alice", SenderName: "", Text: "hello group", At: at},
				{ID: "om_bot", SenderID: "cli_bot", SenderName: "", Text: "bot notice", At: at.Add(time.Second)},
				{ID: "om_named", SenderID: "ou_named", SenderName: "Already Named", Text: "named", At: at.Add(2 * time.Second)},
				{ID: "om_b1", SenderID: "ou_other_tenant", SenderName: "", Text: "other binding", At: at.Add(3 * time.Second)},
			},
		}),
		seedBatch(connector.ChatBatch{
			Chat: kbwire.SourceChat{ID: "oc_dm", Name: "", Kind: "dm"},
			Messages: []kbwire.SourceMessage{
				{ID: "om_dm1", SenderID: "ou_bob", SenderName: "", Text: "hi dm", At: at.Add(4 * time.Second)},
			},
		}),
	}); err != nil {
		t.Fatal(err)
	}

	var resolveCalls atomic.Int32
	var lastRequested []string
	w.mu.Lock()
	w.resolveNames = func(_ context.Context, _ larksyncwire.Binding, _ func(context.Context) (string, error), openIDs []string) (map[string]string, error) {
		resolveCalls.Add(1)
		lastRequested = slices.Clone(openIDs)
		return map[string]string{
			"ou_alice": "Example Alice",
			"ou_bob":   "Example Bob",
		}, nil
	}
	w.mu.Unlock()
	w.script("bsyncA", accA, func(_ context.Context, _ larksyncwire.Binding, _ func(context.Context) (string, error), _ map[string]string, _ larksyncwire.Index) (larksyncwire.Result, error) {
		return larksyncwire.Result{}, nil
	})

	// Run 1: triggers backfill for keyA only.
	if err := w.h.larkSyncRunMode(ctx, keyA, larksyncwire.ModeIM); err != nil {
		t.Fatal(err)
	}
	if resolveCalls.Load() != 1 {
		t.Fatalf("resolveCalls = %d, want 1", resolveCalls.Load())
	}
	if !slices.Equal(lastRequested, []string{"ou_alice", "ou_bob"}) {
		t.Fatalf("resolved openIDs = %v, want [ou_alice ou_bob] (skipping cli_bot, om_named, and binding B's ou_other_tenant)", lastRequested)
	}
	if got := w.binding(t, keyA).BackfillVersion; got != larkSyncBackfillVersion {
		t.Fatalf("BackfillVersion = %d, want %d", got, larkSyncBackfillVersion)
	}
	// Verify repair rows were appended to outbox with resolved names and DM chat name.
	rows, err := w.store.LarkSyncOutboxAfter(ctx, db.LarkSyncChat, 2, 10)
	if err != nil || len(rows) != 2 {
		t.Fatalf("repair outbox rows = %d, err = %v", len(rows), err)
	}
	repaired := map[string]connector.ChatBatch{}
	for _, r := range rows {
		var cb connector.ChatBatch
		if err := json.Unmarshal(r.Payload, &cb); err != nil {
			t.Fatal(err)
		}
		repaired[cb.Chat.ID] = cb
	}
	if grp := repaired["oc_grp"]; len(grp.Messages) != 1 || grp.Messages[0].ID != "om_a1" || grp.Messages[0].SenderName != "Example Alice" {
		t.Fatalf("repaired oc_grp = %+v", grp)
	}
	if dm := repaired["oc_dm"]; dm.Chat.Name != "Example Bob" || len(dm.Messages) != 1 || dm.Messages[0].SenderName != "Example Bob" {
		t.Fatalf("repaired oc_dm = %+v", dm)
	}

	// Run 2: BackfillVersion is set -> does not call ResolveUserNames or emit any rows.
	if err := w.h.larkSyncRunMode(ctx, keyA, larksyncwire.ModeIM); err != nil {
		t.Fatal(err)
	}
	if resolveCalls.Load() != 1 {
		t.Fatalf("run 2 called ResolveUserNames again (%d)", resolveCalls.Load())
	}

	// Run 3: even if BackfillVersion is manually reset to 0, outbox scan sees the newer named rows and is a no-op.
	if _, err := w.h.larkSyncChange(ctx, func(st *larkSyncState) error {
		st.binding(keyA).BackfillVersion = 0
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := w.h.larkSyncRunMode(ctx, keyA, larksyncwire.ModeIM); err != nil {
		t.Fatal(err)
	}
	if resolveCalls.Load() != 1 {
		t.Fatalf("reset BackfillVersion re-resolved already-repaired messages (%d calls)", resolveCalls.Load())
	}
	if rowsAfter, _ := w.store.LarkSyncOutboxAfter(ctx, db.LarkSyncChat, 4, 10); len(rowsAfter) != 0 {
		t.Fatalf("unexpected extra outbox rows after idempotent rerun: %d", len(rowsAfter))
	}
}
