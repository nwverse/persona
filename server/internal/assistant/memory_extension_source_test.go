package assistant

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/extmgr"
)

// fakeMemoryReader is the extensions' manager as the memory source reads it: out for every read, counted.
type fakeMemoryReader struct {
	mu    sync.Mutex
	gen   uint64
	reads int
	out   []extmgr.MemorySourceRead
}

func (f *fakeMemoryReader) MemorySourceReads(_ context.Context, b extmgr.MemoryReadBounds) []extmgr.MemorySourceRead {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	if b.PageLimit != memoryExtensionPageLimit || b.Pages != memoryExtensionPages || b.Entries != memoryExtensionMost ||
		b.CallTimeout != memoryExtensionCallTimeout || b.AnswerMost != memoryExtensionAnswerMost {
		return []extmgr.MemorySourceRead{{Err: errors.New("unexpected bounds")}}
	}
	return f.out
}

func (f *fakeMemoryReader) MemorySourcesGeneration() uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gen
}

// readsNow is how many reads f answered, once c's background read (if any) is done.
func (f *fakeMemoryReader) readsNow(t *testing.T, c *memoryExtensionCache) int {
	t.Helper()
	c.mu.Lock()
	wait := c.reading
	c.mu.Unlock()
	if wait != nil {
		select {
		case <-wait:
		case <-time.After(5 * time.Second):
			t.Fatal("the background read did not finish in 5s")
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reads
}

// qaMemoryPage is one tagged page of a fake memory extension's entries.
func qaMemoryPage(entries ...wire.MemoryEntry) []wire.MemoryEntriesResult {
	return []wire.MemoryEntriesResult{{Tagged: true, Entries: entries}}
}

// The sources' reads merged: a failed one gives nothing and makes the batch failed; an untagged one's entries are put
// in no track (not known, the strictest rule) while the tagged one's keep theirs; an id given before is dropped; the
// names are those of the extensions that gave entries.
func TestMemoryExtensionMerge(t *testing.T) {
	b, failed := memoryExtensionMerge([]extmgr.MemorySourceRead{
		{Extension: "qa-memory", Name: "QA Memory", Pages: qaMemoryPage(wire.MemoryEntry{ID: "qa-a", Text: "qa: a", Status: "active", OriginSpace: "qa-plain"})},
		{Extension: "qa-old", Name: "QA Old", Pages: []wire.MemoryEntriesResult{{Entries: []wire.MemoryEntry{
			{ID: "qa-a", Text: "qa: again", Status: "active"}, {ID: "qa-b", Text: "qa: b", Status: "active"}}}}},
		{Extension: "qa-broken", Name: "QA Broken", Err: errors.New("no answer in 5s")},
	})
	if !failed || !b.Tagged || len(b.Entries) != 2 || strings.Join(b.Names, ",") != "QA Memory,QA Old" {
		t.Fatalf("%+v failed %v", b, failed)
	}
	if b.Entries[0].ID != "qa-a" || b.Entries[0].OriginSpace != "qa-plain" || b.Entries[1].ID != "qa-b" || b.Entries[1].OriginSpace != entryTrackUnknown {
		t.Fatalf("%+v", b.Entries)
	}
	s, _ := memoryExtensionService(t, true)
	if u := s.memoryExtensionUses(context.Background(), true)(memoryExtensionBatch{Entries: b.Entries[1:]}.assistantEntries()[0]); u.Known || !u.sensitive() {
		t.Errorf("an untagged source's entry = %+v, want not known", u)
	}
	if b, failed := memoryExtensionMerge(nil); failed || len(b.Entries) != 0 {
		t.Errorf("no sources: %+v %v", b, failed)
	}
}

// The cache reads once per minute and generation, again sooner after a read in which a source failed, and its batch
// is what the sources answered.
func TestMemoryExtensionCache(t *testing.T) {
	ctx := context.Background()
	r := &fakeMemoryReader{out: []extmgr.MemorySourceRead{{Extension: "qa-memory", Name: "QA Memory",
		Pages: qaMemoryPage(wire.MemoryEntry{ID: "qa-a", Text: "qa: a", Status: "active", Standing: true})}}}
	c := &memoryExtensionCache{r: r}
	for range 3 {
		if b, err := c.MemoryExtensionEntries(ctx, true); err != nil || len(b.Entries) != 1 || !b.Entries[0].Standing {
			t.Fatalf("%+v %v", b, err)
		}
	}
	if n := r.readsNow(t, c); n != 1 {
		t.Fatalf("read %d times within a minute, want 1", n)
	}
	r.mu.Lock()
	r.gen++
	r.mu.Unlock()
	_, _ = c.MemoryExtensionEntries(ctx, true)
	if n := r.readsNow(t, c); n != 2 {
		t.Fatalf("an extension changed: read %d times, want 2", n)
	}
	// A minute later the kept batch is used at once and read again in the background.
	c.mu.Lock()
	c.at = time.Now().Add(-memoryExtensionFresh - time.Second)
	c.mu.Unlock()
	if b, err := c.MemoryExtensionEntries(ctx, true); err != nil || len(b.Entries) != 1 {
		t.Fatalf("a stale batch is used while read again: %+v %v", b, err)
	}
	if n := r.readsNow(t, c); n != 3 {
		t.Fatalf("a minute later: read %d times, want 3", n)
	}
	r.mu.Lock()
	r.out = []extmgr.MemorySourceRead{{Extension: "qa-memory", Err: errors.New("not_ready: loading")}}
	r.gen++
	r.mu.Unlock()
	if b, _ := c.MemoryExtensionEntries(ctx, true); len(b.Entries) != 0 {
		t.Fatalf("a failed source gives nothing: %+v", b)
	}
	if n := r.readsNow(t, c); n != 4 {
		t.Fatalf("a failed read: read %d times, want 4", n)
	}
	c.mu.Lock()
	c.at = time.Now().Add(-memoryExtensionRetry - time.Second)
	c.mu.Unlock()
	_, _ = c.MemoryExtensionEntries(ctx, true)
	if n := r.readsNow(t, c); n != 5 {
		t.Fatalf("after a failure: read %d times, want 5", n)
	}
	// The extensions' manager is a source as soon as the service is given it; a fake that reads none is not.
	s := newTestService(t, newFakeHub())
	s.setMemorySources(struct{}{})
	if s.memoryExtension != nil {
		t.Fatal("a manager without memory sources became one")
	}
	s.setMemorySources(r)
	if _, ok := s.memoryExtension.(*memoryExtensionCache); !ok {
		t.Fatalf("source %T", s.memoryExtension)
	}
}

// Only the extension's standing entries go to a standing channel; the others come in only by relevance or a recall.
func TestMemoryExtensionStandingOnlyStanding(t *testing.T) {
	ctx := context.Background()
	s, src := memoryExtensionService(t, true)
	named := extQAPlain
	named.Standing = false
	src.batch.Entries = []memoryExtensionEntry{named, extQAMain}
	a, err := s.standingMemory(ctx, "qa-main")
	if err != nil || entryIDs(a.Entries) != extID(extQAMain) {
		t.Errorf("standing = %s (%v), want only the standing entry", entryIDs(a.Entries), err)
	}
	if got := s.standingMemorySection(ctx, "qa-machine", "qa-main"); strings.Contains(got, named.Text) || !strings.Contains(got, extQAMain.Text) {
		t.Errorf("standing section = %q", got)
	}
	var ids []string
	for _, m := range s.recallExtensionMemory(ctx, "qa-main", "") {
		ids = append(ids, m.ID)
	}
	if got, want := strings.Join(ids, ","), extID(named)+","+extID(extQAMain); got != want {
		t.Errorf("recall = %s, want %s", got, want)
	}
}

// The main conversation's standing context: the main space's and general standing entries, never the finance one from
// qa-finance; nothing on a computer that is not the owner's, and nothing without a source or when it failed.
func TestStandingMemorySection(t *testing.T) {
	ctx := context.Background()
	s, src := memoryExtensionService(t, true)
	got := s.standingMemorySection(ctx, "qa-machine", "qa-main")
	if !strings.Contains(got, extQAPlain.Text) || !strings.Contains(got, extQAMain.Text) || strings.Contains(got, extQAFinance.Text) ||
		strings.Contains(got, extQAFinanceUntagged.Text) || strings.Contains(got, extQAUnknown.Text) {
		t.Errorf("main standing = %q", got)
	}
	if got := s.withStandingMemory(ctx, "qa-machine", "qa-main", "- 现在：qa"); !strings.HasPrefix(got, "- 现在：qa\n- ") || !strings.HasSuffix(got, "\n") {
		t.Errorf("after the snapshot = %q", got)
	}
	if got := s.withStandingMemory(ctx, "qa-other", "qa-main", "- 现在：qa"); got != "- 现在：qa" {
		t.Errorf("another computer = %q", got)
	}
	src.batch = memoryExtensionBatch{}
	if got := s.withStandingMemory(ctx, "qa-machine", "qa-main", "- 现在：qa"); got != "- 现在：qa" {
		t.Errorf("no entries = %q", got)
	}
	s.memoryExtension = &memoryExtensionCache{r: &fakeMemoryReader{out: []extmgr.MemorySourceRead{{Extension: "qa-memory", Err: errors.New("no answer in 5s")}}}}
	if got := s.standingMemorySection(ctx, "qa-machine", "qa-main"); got != "" {
		t.Errorf("a failed source = %q", got)
	}
	s.memoryExtension = nil
	if got := s.standingMemorySection(ctx, "qa-machine", "qa-main"); got != "" {
		t.Errorf("no source = %q", got)
	}
}

// The standing memory section in each language (#13586).
func TestGoldenStandingMemory(t *testing.T) {
	ctx := context.Background()
	for _, locale := range []string{"zh-Hans", "en", "ja"} {
		s, f, _, _ := memoryAssembleService(t)
		f.mu.Lock()
		f.hello.Locale = locale
		f.mu.Unlock()
		s.memoryEntries = &memEntrySource{}
		s.memoryExtension = &memExtensionSource{batch: memoryExtensionBatch{Entries: extQAEntries(), Tagged: true}}
		golden(t, "memory_standing."+locale, s.standingMemorySection(ctx, "qa-machine", "qa-main"))
	}
}

// A recall returns the extension's finance entry only once the conversation carries finance, the one from an unknown
// space never; a tag keeps the entries that name it.
func TestRecallExtensionMemory(t *testing.T) {
	ctx := context.Background()
	s, _ := memoryExtensionService(t, true)
	ids := func(tag string) string {
		var out []string
		for _, m := range s.recallExtensionMemory(ctx, "qa-main", tag) {
			out = append(out, m.ID)
		}
		return strings.Join(out, ",")
	}
	if got, want := ids(""), extID(extQAPlain)+","+extID(extQAMain); got != want {
		t.Errorf("without finance = %s, want %s", got, want)
	}
	if got := ids("envelope"); got != "" {
		t.Errorf("a finance tag without finance = %s", got)
	}
	if err := s.tagConversation(ctx, "qa-main", []string{categoryFinance}, infoTaggedByModel, ""); err != nil {
		t.Fatal(err)
	}
	if got := ids(""); !strings.Contains(got, extID(extQAFinance)) || strings.Contains(got, extID(extQAUnknown)) {
		t.Errorf("with finance = %s", got)
	}
	if got, want := ids("Envelope"), extID(extQAFinance); got != want {
		t.Errorf("tag = %s, want %s", got, want)
	}
}

// An Agent started outside a plan carries the general entries its work bears on, never a sensitive one, and only on
// the owner's computer.
func TestAgentStartMemory(t *testing.T) {
	ctx := context.Background()
	s, _ := memoryExtensionService(t, true)
	query := "qa: prepare the quarterly budget envelope totals and the weekly report bullet points"
	got := s.agentStartMemory(ctx, "qa-machine", query)
	if !strings.Contains(got, extQAMain.Text) || strings.Contains(got, extQAFinance.Text) || strings.Contains(got, extQAFinanceUntagged.Text) {
		t.Errorf("agent start = %q", got)
	}
	if got := s.agentStartMemory(ctx, "qa-other", query); got != "" {
		t.Errorf("another computer = %q", got)
	}
	if got := s.withProjectContextForTodo(ctx, "qa-machine", "", "", Todo{}, query); !strings.HasSuffix(got, "\n\n"+query) || !strings.Contains(got, extQAMain.Text) {
		t.Errorf("first message = %q", got)
	}
}

// The Inbox's helper is given the general entries the item bears on under the extension's name, never a sensitive one.
func TestInboxMemoryContext(t *testing.T) {
	ctx := context.Background()
	s, src := memoryExtensionService(t, true)
	src.batch.Names = []string{"QA Memory"}
	blocks := s.inboxMemoryContext(ctx, "qa: can you send the quarterly budget envelope totals, and the weekly report bullet points")
	if len(blocks) != 1 || blocks[0].Name != "QA Memory" || blocks[0].Text != "- "+extQAMain.Text {
		t.Errorf("Inbox = %+v", blocks)
	}
	src.batch = memoryExtensionBatch{}
	if blocks := s.inboxMemoryContext(ctx, "qa: the weekly report bullet points"); len(blocks) != 0 {
		t.Errorf("no entries = %+v", blocks)
	}
}

// The most important entries (the last) that fit the bound, in their order.
func TestMostImportantWithin(t *testing.T) {
	entries := memoryExtensionBatch{Entries: extQAEntries()}.assistantEntries()
	got := mostImportantWithin(entries, len(extQAMain.Text)+len(extQAUnknown.Text))
	if entryIDs(got) != extID(extQAMain)+","+extID(extQAUnknown) {
		t.Errorf("%s", entryIDs(got))
	}
	if got := mostImportantWithin(entries, 1); len(got) != 0 {
		t.Errorf("%s", entryIDs(got))
	}
}
