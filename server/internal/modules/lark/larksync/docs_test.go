package larksync

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/larksyncwire"
)

// docxBlocks is a small document: a heading, a paragraph with a bold run and a link, a bullet, two numbered items, a
// to-do, code, a quote, a divider and a 2×2 table.
func docxBlocks(id string) []any {
	text := func(s string) map[string]any {
		return map[string]any{"elements": []any{map[string]any{"text_run": map[string]any{"content": s}}}}
	}
	return []any{
		map[string]any{"block_id": id, "block_type": 1, "children": []string{"h", "p", "b", "o1", "o2", "td", "c", "q", "dv", "tb"}, "page": text("Title")},
		map[string]any{"block_id": "h", "parent_id": id, "block_type": 4, "heading2": text("Goals")},
		map[string]any{"block_id": "p", "parent_id": id, "block_type": 2, "text": map[string]any{"elements": []any{
			map[string]any{"text_run": map[string]any{"content": "Ship "}},
			map[string]any{"text_run": map[string]any{"content": "now", "text_element_style": map[string]any{"bold": true}}},
			map[string]any{"text_run": map[string]any{"content": " spec", "text_element_style": map[string]any{"link": map[string]any{"url": "https%3A%2F%2Fexample.com%2Fspec"}}}},
		}}},
		map[string]any{"block_id": "b", "parent_id": id, "block_type": 12, "bullet": text("one")},
		map[string]any{"block_id": "o1", "parent_id": id, "block_type": 13, "ordered": text("first")},
		map[string]any{"block_id": "o2", "parent_id": id, "block_type": 13, "ordered": text("second")},
		map[string]any{"block_id": "td", "parent_id": id, "block_type": 17, "todo": map[string]any{"elements": text("done it")["elements"], "style": map[string]any{"done": true}}},
		map[string]any{"block_id": "c", "parent_id": id, "block_type": 14, "code": text("go test ./...")},
		map[string]any{"block_id": "q", "parent_id": id, "block_type": 15, "quote": text("wise words")},
		map[string]any{"block_id": "dv", "parent_id": id, "block_type": 22, "divider": map[string]any{}},
		map[string]any{"block_id": "tb", "parent_id": id, "block_type": 31, "children": []string{"c1", "c2", "c3", "c4"},
			"table": map[string]any{"cells": []string{"c1", "c2", "c3", "c4"}, "property": map[string]any{"column_size": 2, "row_size": 2}}},
		map[string]any{"block_id": "c1", "parent_id": "tb", "block_type": 32, "children": []string{"t1"}},
		map[string]any{"block_id": "c2", "parent_id": "tb", "block_type": 32, "children": []string{"t2"}},
		map[string]any{"block_id": "c3", "parent_id": "tb", "block_type": 32, "children": []string{"t3"}},
		map[string]any{"block_id": "c4", "parent_id": "tb", "block_type": 32, "children": []string{"t4"}},
		map[string]any{"block_id": "t1", "parent_id": "c1", "block_type": 2, "text": text("Name")},
		map[string]any{"block_id": "t2", "parent_id": "c2", "block_type": 2, "text": text("Owner")},
		map[string]any{"block_id": "t3", "parent_id": "c3", "block_type": 2, "text": text("Sync")},
		map[string]any{"block_id": "t4", "parent_id": "c4", "block_type": 2, "text": text("Team | A")},
	}
}

const wantDocxMarkdown = "## Goals\n\n" +
	"Ship **now** [spec](https://example.com/spec)\n\n" +
	"- one\n1. first\n2. second\n- [x] done it\n\n" +
	"```\ngo test ./...\n```\n\n" +
	"> wise words\n\n" +
	"---\n\n" +
	"| Name | Owner |\n| --- | --- |\n| Sync | Team \\| A |"

func TestRenderDocx(t *testing.T) {
	raw, _ := json.Marshal(docxBlocks("doxA"))
	var blocks []docxBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		t.Fatal(err)
	}
	if got := renderDocx("doxA", blocks); got != wantDocxMarkdown {
		t.Fatalf("markdown:\n%s\nwant:\n%s", got, wantDocxMarkdown)
	}
}

func TestMarkdownTablesAndBound(t *testing.T) {
	got := markdownTable([][]any{{"a", "b", ""}, {1.5, true, ""}, {"", "", ""}})
	if want := "| a | b |\n| --- | --- |\n| 1.5 | true |\n"; got != want {
		t.Errorf("table = %q, want %q", got, want)
	}
	rich := []any{map[string]any{"text": "Hel", "type": "text"}, map[string]any{"text": "lo", "type": "text"}}
	got = recordsTable([]map[string]any{{"Name": rich, "Tags": []any{"x", "y"}, "Owner": []any{map[string]any{"name": "Sam"}}}})
	if want := "| Name | Owner | Tags |\n| --- | --- | --- |\n| Hello | Sam | x, y |\n"; got != want {
		t.Errorf("records = %q, want %q", got, want)
	}
	long := strings.Repeat("é", 100)
	if b := bound(long, 51); len(b) > 51 || !strings.HasSuffix(b, truncatedMark) || !strings.HasPrefix(b, "é") {
		t.Errorf("bound = %q (%d bytes)", b, len(b))
	}
}

// docPlatform answers a Drive holding docx doxA, sheet shtS and bitable bscB, and a wiki space whose node wikN wraps
// doxA and whose node wikM wraps docx doxM. Newest edits first: "old" ends the root's listing, so an entry after it
// would never be read. rev is doxA's revision (changed by the test between runs).
type docPlatform struct {
	*fakeOpen
	rev, blocks atomic.Int64
}

func newDocPlatform(t *testing.T, clk *fakeClock) *docPlatform {
	p := &docPlatform{fakeOpen: newFakeOpen(t, clk)}
	p.rev.Store(1)
	edited := secs(testNow.Add(-10 * time.Minute))
	p.handle("/open-apis/drive/v1/files", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("order_by") != "EditedTime" || q.Get("direction") != "DESC" {
			t.Errorf("drive query %v", q)
		}
		switch q.Get("folder_token") {
		case "":
			writeData(w, map[string]any{"files": []any{
				map[string]any{"token": "doxA", "name": "Plan", "type": "docx", "url": "https://tenant.feishu.cn/docx/doxA", "modified_time": edited},
				map[string]any{"token": "fldF", "name": "Folder", "type": "folder", "modified_time": edited},
				map[string]any{"token": "never", "name": "Never read", "type": "docx", "modified_time": edited},
				map[string]any{"token": "old", "name": "Old", "type": "docx", "modified_time": secs(testNow.Add(-100 * 24 * time.Hour))},
			}})
		case "fldF":
			writeData(w, map[string]any{"files": []any{
				map[string]any{"token": "shtS", "name": "Numbers", "type": "sheet", "url": "https://tenant.feishu.cn/sheets/shtS", "modified_time": edited},
				map[string]any{"token": "cutB", "name": "Base", "type": "shortcut", "modified_time": edited,
					"shortcut_info": map[string]string{"target_type": "bitable", "target_token": "bscB"}},
			}})
		default:
			t.Errorf("folder %q", q.Get("folder_token"))
		}
	})
	p.handle("/open-apis/docx/v1/documents/doxA", func(w http.ResponseWriter, _ *http.Request) {
		writeData(w, map[string]any{"document": map[string]any{"revision_id": p.rev.Load(), "title": "Plan"}})
	})
	p.handle("/open-apis/docx/v1/documents/doxA/blocks", func(w http.ResponseWriter, _ *http.Request) {
		p.blocks.Add(1)
		blocks := docxBlocks("doxA")
		if p.rev.Load() > 1 {
			blocks = blocks[:2] // revision 2 is only its heading
		}
		writeData(w, map[string]any{"items": blocks})
	})
	p.data("/open-apis/sheets/v3/spreadsheets/shtS/sheets/query", map[string]any{"sheets": []any{map[string]any{"sheet_id": "s1", "title": "Q1", "resource_type": "sheet"}}})
	p.data("/open-apis/sheets/v2/spreadsheets/shtS/values/s1!A1:T200", map[string]any{"valueRange": map[string]any{"values": [][]any{{"Month", "Sales"}, {"Jan", 10}}}})
	p.data("/open-apis/bitable/v1/apps/bscB/tables", map[string]any{"items": []any{map[string]any{"table_id": "tbl1", "name": "Tasks"}}})
	p.data("/open-apis/bitable/v1/apps/bscB/tables/tbl1/records", map[string]any{"items": []any{map[string]any{"fields": map[string]any{"Task": "Write tests", "Done": true}}}})
	p.data("/open-apis/wiki/v2/spaces", map[string]any{"items": []any{map[string]any{"space_id": "sp1"}}})
	p.handle("/open-apis/wiki/v2/spaces/sp1/nodes", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("parent_node_token") {
		case "":
			writeData(w, map[string]any{"items": []any{
				map[string]any{"node_token": "wikN", "obj_token": "doxA", "obj_type": "docx", "title": "Plan", "has_child": true, "obj_edit_time": edited},
			}})
		case "wikN":
			writeData(w, map[string]any{"items": []any{
				map[string]any{"node_token": "wikM", "obj_token": "doxM", "obj_type": "docx", "title": "Child", "obj_edit_time": edited},
			}})
		default:
			t.Errorf("wiki parent %q", r.URL.Query().Get("parent_node_token"))
		}
	})
	p.data("/open-apis/docx/v1/documents/doxM", map[string]any{"document": map[string]any{"revision_id": 4, "title": "Child"}})
	p.data("/open-apis/docx/v1/documents/doxM/blocks", map[string]any{"items": []any{
		map[string]any{"block_id": "doxM", "block_type": 1, "children": []string{"x"}},
		map[string]any{"block_id": "x", "parent_id": "doxM", "block_type": 2, "text": map[string]any{"elements": []any{map[string]any{"text_run": map[string]any{"content": "child body"}}}}},
	}})
	// "never" cannot be read: no permission on that one document. It is kept as its title only.
	p.handle("/open-apis/docx/v1/documents/never", func(w http.ResponseWriter, _ *http.Request) {
		writeCode(w, http.StatusForbidden, 1770032, "forbidden")
	})
	p.handle("/open-apis/docx/v1/documents/never/blocks", func(w http.ResponseWriter, _ *http.Request) {
		writeCode(w, http.StatusForbidden, 1770032, "forbidden")
	})
	p.handle("/open-apis/docx/v1/documents/never/raw_content", func(w http.ResponseWriter, _ *http.Request) {
		writeCode(w, http.StatusForbidden, 1770032, "forbidden")
	})
	return p
}

func docByKey(docs []larksyncwire.Doc, key string) (larksyncwire.Doc, bool) {
	for _, d := range docs {
		if d.Key == key {
			return d, true
		}
	}
	return larksyncwire.Doc{}, false
}

// Mock suite A, module part: one run reads every kind of document; the same docx reached through Drive and through
// a wiki node is one Doc with both sources, keyed by its obj_token and known by its node token too.
func TestDocumentsDriveWikiAndContent(t *testing.T) {
	clk := &fakeClock{t: testNow}
	p := newDocPlatform(t, clk)
	s := newTestSyncer(t, clk)
	b := binding(p.fakeOpen, "primary~a1", larksyncwire.TierDocs)
	idx := newMemIndex()

	res, err := s.Run(t.Context(), b, staticToken("tok"), nil, idx)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) != 0 || res.Partial {
		t.Fatalf("errors %+v partial %v", res.Errors, res.Partial)
	}
	a, ok := docByKey(res.Docs, larksyncwire.KeyDoc+"doxA")
	if !ok {
		t.Fatalf("docs = %+v", res.Docs)
	}
	if a.Kind != "docs" || a.ObjType != "docx" || a.Revision != 1 || a.Title != "Plan" || a.Body != wantDocxMarkdown || a.BodyHash != hashOf(wantDocxMarkdown) {
		t.Errorf("doxA = %+v", a)
	}
	if a.URL != "https://tenant.feishu.cn/docx/doxA" || !slices.Equal(a.Aliases, []string{"wikN"}) {
		t.Errorf("doxA url %q aliases %v, want its own link and the node token", a.URL, a.Aliases)
	}
	if got := entries(a.Sources); !slices.Equal(got, []string{b.Key + "/drive", b.Key + "/wiki"}) {
		t.Errorf("doxA sources = %v", got)
	}
	if p.blocks.Load() != 1 || p.count("/open-apis/docx/v1/documents/doxA") != 1 {
		t.Errorf("doxA read %d times, meta %d times, want once each", p.blocks.Load(), p.count("/open-apis/docx/v1/documents/doxA"))
	}
	m, ok := docByKey(res.Docs, larksyncwire.KeyDoc+"doxM")
	if !ok || m.URL != "https://tenant.feishu.cn/wiki/wikM" || !slices.Equal(m.Aliases, []string{"wikM"}) || m.Body != "child body" || m.Revision != 4 {
		t.Errorf("doxM = %+v", m)
	}
	sh, _ := docByKey(res.Docs, larksyncwire.KeyDoc+"shtS")
	if want := "## Q1\n\n| Month | Sales |\n| --- | --- |\n| Jan | 10 |\n"; sh.Body != want {
		t.Errorf("sheet body = %q, want %q", sh.Body, want)
	}
	bt, _ := docByKey(res.Docs, larksyncwire.KeyDoc+"bscB")
	if want := "## Tasks\n\n| Done | Task |\n| --- | --- |\n| true | Write tests |\n"; bt.Body != want || bt.ObjType != "bitable" {
		t.Errorf("bitable = %+v", bt)
	}
	nv, ok := docByKey(res.Docs, larksyncwire.KeyDoc+"never")
	if !ok || nv.Body != "" || nv.Revision != 0 || nv.Title != "Never read" {
		t.Errorf("unreadable doc = %+v, want its title at revision 0", nv)
	}
	if _, ok := docByKey(res.Docs, larksyncwire.KeyDoc+"old"); ok {
		t.Error("a document older than the backfill was read")
	}
	if res.Cursors["drive"] != secs(testNow.Add(-10*time.Minute)) || res.Cursors["wiki:sp1"] != secs(testNow.Add(-10*time.Minute)) {
		t.Errorf("cursors = %v", res.Cursors)
	}
	rec, ok := idx.get(larksyncwire.KeyDoc + "doxA")
	if !ok || rec.Revision != 1 || rec.BodyHash != a.BodyHash || rec.Kind != "doc" || len(rec.Sources) != 2 {
		t.Errorf("index record = %+v", rec)
	}
}

// Mock suite A, module part: the revision state machine across runs and bindings. Revision 1 is new; revision 2 is
// newer and handed over again; a binding that still reads revision 1 (late) hands nothing over and only adds its
// source; the same revision through another binding only adds its source.
func TestRevisionsAcrossRunsAndBindings(t *testing.T) {
	clk := &fakeClock{t: testNow}
	p := newDocPlatform(t, clk)
	s := newTestSyncer(t, clk)
	bA := binding(p.fakeOpen, "primary~a1", larksyncwire.TierDocs)
	bB := binding(p.fakeOpen, "sync2~a2", larksyncwire.TierDocs)
	bB.AccountID = "feishu:tenant_y:ou_other"
	key := larksyncwire.KeyDoc + "doxA"
	idx := newMemIndex()

	res, err := s.Run(t.Context(), bA, staticToken("tok"), nil, idx)
	if _, ok := docByKey(res.Docs, key); err != nil || !ok {
		t.Fatalf("run 1: %v %+v", err, res.Docs)
	}
	cursors := res.Cursors

	p.rev.Store(2)
	res, err = s.Run(t.Context(), bA, staticToken("tok"), cursors, idx)
	d, ok := docByKey(res.Docs, key)
	if err != nil || !ok || d.Revision != 2 || d.Body != "## Goals" {
		t.Fatalf("run 2 (newer): %v %+v", err, d)
	}
	if rec, _ := idx.get(key); rec.Revision != 2 {
		t.Fatalf("index after run 2 = %+v", rec)
	}
	if _, again := docByKey(res.Docs, larksyncwire.KeyDoc+"doxM"); again {
		t.Error("an unchanged document was handed over again")
	}

	// Binding B still sees revision 1.
	p.rev.Store(1)
	blocks := p.blocks.Load()
	res, err = s.Run(t.Context(), bB, staticToken("tok"), nil, idx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := docByKey(res.Docs, key); ok {
		t.Fatal("a stale revision was handed over")
	}
	if p.blocks.Load() != blocks {
		t.Error("a stale revision's body was read")
	}
	rec, _ := idx.get(key)
	if rec.Revision != 2 || rec.BodyHash != hashOf("## Goals") {
		t.Errorf("a stale revision overwrote the index: %+v", rec)
	}
	if got := entries(rec.Sources); !slices.Equal(got, []string{bA.Key + "/drive", bA.Key + "/wiki", bB.Key + "/drive", bB.Key + "/wiki"}) {
		t.Errorf("sources = %v", got)
	}

	// The same revision through B again: nothing handed over, nothing new to write for doxA.
	p.rev.Store(2)
	res, err = s.Run(t.Context(), bB, staticToken("tok"), nil, idx)
	if _, ok := docByKey(res.Docs, key); err != nil || ok {
		t.Fatalf("same revision: %v %+v", err, res.Docs)
	}
	if got, _ := idx.get(key); len(got.Sources) != 4 || got.Revision != 2 {
		t.Errorf("record = %+v", got)
	}
}

// An index that stages (the manager's) gets the records through Stage, never Put: the manager writes them with the
// cursors and the handed-over content in one transaction.
func TestRunStagesIndexRecordsWhenTheIndexCan(t *testing.T) {
	clk := &fakeClock{t: testNow}
	p := newDocPlatform(t, clk)
	s := newTestSyncer(t, clk)
	idx := &stagingIndex{memIndex: newMemIndex(), t: t}
	res, err := s.Run(t.Context(), binding(p.fakeOpen, "primary~a1", larksyncwire.TierDocs), staticToken("tok"), nil, idx)
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.staged) != len(res.Docs) || len(idx.recs) != 0 {
		t.Fatalf("staged %d records for %d docs, index holds %d", len(idx.staged), len(res.Docs), len(idx.recs))
	}
}

// Minutes are found through links in messages (and Drive), read with their transcript, and keyed by their token.
func TestMinutesFromAMessageLink(t *testing.T) {
	clk := &fakeClock{t: testNow}
	f := newFakeOpen(t, clk)
	s := newTestSyncer(t, clk)
	b := binding(f, "primary~a1", larksyncwire.TierFull)
	f.data("/open-apis/im/v1/chats", map[string]any{"items": []any{map[string]any{"chat_id": "oc_a"}}})
	f.data("/open-apis/im/v1/messages", map[string]any{"items": []any{
		textMsg("om_1", "oc_a", "ou_peer", "notes: https://tenant.feishu.cn/minutes/obcnM1.", testNow.Add(-time.Hour)),
	}})
	emptySearch(f)
	f.data("/open-apis/drive/v1/files", map[string]any{"files": []any{
		map[string]any{"token": "obcnM2", "name": "Standup", "type": "minutes", "url": "https://tenant.feishu.cn/minutes/obcnM2", "modified_time": secs(testNow.Add(-time.Hour))},
	}})
	f.data("/open-apis/wiki/v2/spaces", map[string]any{"items": []any{}})
	created := testNow.Add(-2 * time.Hour)
	for _, tok := range []string{"obcnM1", "obcnM2"} {
		f.data("/open-apis/minutes/v1/minutes/"+tok, map[string]any{"minute": map[string]any{"title": "Meeting " + tok, "create_time": msgTime(created), "url": "https://tenant.feishu.cn/minutes/" + tok}})
		f.handle("/open-apis/minutes/v1/minutes/"+tok+"/transcript", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("need_speaker") != "true" || r.URL.Query().Get("file_format") != "txt" {
				t.Errorf("transcript query %v", r.URL.Query())
			}
			_, _ = w.Write([]byte("Speaker 1: hello\n"))
		})
	}

	res, err := s.Run(t.Context(), b, staticToken("tok"), nil, newMemIndex())
	if err != nil {
		t.Fatal(err)
	}
	m1, ok := docByKey(res.Docs, larksyncwire.KeyMinute+"obcnM1")
	if !ok || m1.Kind != "meetings" || m1.ObjType != "minutes" || m1.Body != "Speaker 1: hello" || m1.Revision != created.Unix() || m1.Title != "Meeting obcnM1" {
		t.Fatalf("minute 1 = %+v (docs %+v)", m1, res.Docs)
	}
	if got := entries(m1.Sources); !slices.Equal(got, []string{b.Key + "/message_link"}) {
		t.Errorf("minute 1 sources = %v", got)
	}
	m2, ok := docByKey(res.Docs, larksyncwire.KeyMinute+"obcnM2")
	if !ok || !slices.Equal(entries(m2.Sources), []string{b.Key + "/drive"}) {
		t.Errorf("minute 2 = %+v", m2)
	}
}
