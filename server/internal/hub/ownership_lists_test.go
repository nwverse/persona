package hub

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
)

// legacyWriteMerged and legacyStampManager are writeMerged and stampManager as they were before the scan (#2807),
// with one change: a 2xx body of null is no answer (it panicked, assigning to a nil map). The tests hold the merge to
// them byte for byte.
func legacyWriteMerged(w http.ResponseWriter, key string, answers []listAnswer) {
	var base *listAnswer
	var items []json.RawMessage
	var top map[string]json.RawMessage
	for i := range answers {
		a := &answers[i]
		if !a.ok() {
			continue
		}
		var t map[string]json.RawMessage
		if json.Unmarshal(a.body, &t) != nil || t == nil {
			continue
		}
		raw, has := t[key]
		var list []json.RawMessage
		if has && json.Unmarshal(raw, &list) != nil {
			continue
		}
		for _, it := range list {
			items = append(items, legacyStampManager(it, a.id))
		}
		if base == nil {
			base, top = a, t
		}
	}
	if base == nil {
		a := answers[0]
		copyHeader(w.Header(), a.header)
		w.WriteHeader(a.status)
		_, _ = w.Write(a.body)
		return
	}
	if items == nil {
		items = []json.RawMessage{}
	}
	list, _ := json.Marshal(items)
	top[key] = list
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(top)
	copyHeader(w.Header(), base.header)
	w.Header().Del("Content-Length")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(base.status)
	_, _ = w.Write(buf.Bytes())
}

func legacyStampManager(item json.RawMessage, id string) json.RawMessage {
	t := bytes.TrimSpace(item)
	if len(t) < 2 || t[0] != '{' {
		return item
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(t, &fields) != nil {
		return item
	}
	idJSON, _ := json.Marshal(id)
	if _, has := fields[ManagerQuery]; has {
		fields[ManagerQuery] = idJSON
		out, _ := json.Marshal(fields)
		return out
	}
	var b bytes.Buffer
	b.WriteString(`{"manager":`)
	b.Write(idJSON)
	if len(fields) > 0 {
		b.WriteByte(',')
		b.Write(bytes.TrimSpace(t[1:]))
	} else {
		b.WriteByte('}')
	}
	return b.Bytes()
}

// mergeCase is one merge: the managers' answers, and for the inbox the language its lists are localized in.
type mergeCase struct {
	name    string
	key     string
	answers []listAnswer
}

// legacyInboxMerge is the old inbox pipeline: relayHTTP localized each answer whole (i18n.InboxJSON), then the merge.
func legacyInboxMerge(l i18n.Lang, key string, answers []listAnswer) *httptest.ResponseRecorder {
	local := cloneAnswers(answers)
	for i := range local {
		if loc := i18n.InboxJSON(l, local[i].body); !bytes.Equal(loc, local[i].body) {
			local[i].body = loc
			local[i].header.Del("Content-Length")
		}
	}
	rec := httptest.NewRecorder()
	legacyWriteMerged(rec, key, local)
	return rec
}

// inboxMerge is the new one: a 2xx answer reaches the merge as its manager gave it, any other localized by the relay.
func inboxMerge(forms *inboxForms, l i18n.Lang, route, key string, answers []listAnswer) *httptest.ResponseRecorder {
	local := cloneAnswers(answers)
	for i := range local {
		if local[i].ok() {
			local[i].localize = &inboxLocalize{forms: forms, slot: inboxSlot{route: route, manager: local[i].id, lang: l}}
		} else if loc := i18n.InboxJSON(l, local[i].body); !bytes.Equal(loc, local[i].body) {
			local[i].body = loc
			local[i].header.Del("Content-Length")
		}
	}
	rec := httptest.NewRecorder()
	writeMerged(rec, key, local)
	return rec
}

func cloneAnswers(answers []listAnswer) []listAnswer {
	out := slices.Clone(answers)
	for i := range out {
		out[i].header = out[i].header.Clone()
		if out[i].header == nil {
			out[i].header = http.Header{}
		}
		out[i].body = slices.Clone(out[i].body)
	}
	return out
}

func sameResponse(t *testing.T, what string, got, want *httptest.ResponseRecorder) {
	t.Helper()
	if got.Code != want.Code || !maps.EqualFunc(got.Header(), want.Header(), slices.Equal) {
		t.Fatalf("%s: status %d %v, want %d %v", what, got.Code, got.Header(), want.Code, want.Header())
	}
	g, w := got.Body.Bytes(), want.Body.Bytes()
	if bytes.Equal(g, w) {
		return
	}
	d := 0
	for d < len(g) && d < len(w) && g[d] == w[d] {
		d++
	}
	t.Fatalf("%s: bytes differ at %d (%d vs %d B)\n got %q\nwant %q", what, d, len(g), len(w),
		g[max(0, d-60):min(len(g), d+60)], w[max(0, d-60):min(len(w), d+60)])
}

// inboxBody is an inbox answer of n items made from the mock hub's (scripts/mock/inbox_events.json, their Chinese
// chain text kept), encoded as the manager encodes it (apierr.WriteJSON: HTML escaped).
func inboxBody(tb testing.TB, n int) []byte {
	tb.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "scripts", "mock", "inbox_events.json"))
	if err != nil {
		tb.Fatal(err)
	}
	var fixture struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil || len(fixture.Items) == 0 {
		tb.Fatalf("inbox_events.json: %v", err)
	}
	at := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	items := make([]map[string]any, n)
	for i := range items {
		dec := json.NewDecoder(bytes.NewReader(fixture.Items[i%len(fixture.Items)]))
		dec.UseNumber()
		if err := dec.Decode(&items[i]); err != nil {
			tb.Fatal(err)
		}
		items[i]["id"] = fmt.Sprintf("%s:seed%05d", items[i]["source"], i)
		items[i]["updated_at"] = at.Add(-time.Duration(i) * time.Minute).Format(time.RFC3339Nano)
	}
	var b bytes.Buffer
	_ = json.NewEncoder(&b).Encode(map[string]any{"as_of": at.Format(time.RFC3339Nano), "items": items,
		"triage": map[string]any{"waiting": 3, "reason": "", "sources": []any{}}})
	return b.Bytes()
}

func listOf(id string, status int, body string) listAnswer {
	return listAnswer{id: id, status: status, header: http.Header{"Content-Type": {"application/json; charset=utf-8"}, "X-Kept": {id}}, body: []byte(body)}
}

// mergeCases are the shapes the merge meets: every route's, and each edge of the scan.
func mergeCases(tb testing.TB) []mergeCase {
	zh := func(key string) string { return i18n.T(i18n.ZhHans, key) }
	q := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	mixed := `{"as_of":"t","items":[` +
		`{"id":"lark:1","source":"lark","kind":"dm","title":"User","chain":[{"kind":"received","text":` + q(i18n.T(i18n.ZhHans, "assistant-inbox_chain.received_from_other", i18n.Args{"count": 3, "sender": "Ann"})) + `}]},` +
		`{"id":"lark:2","source":"lark","kind":"dm","title":"no such inbox item","n":9007199254740993},` +
		`{"id":"mail:3","source":"mail","kind":"mail","triaged_by":"rules","reason":` + q(zh("assistant-inbox.by_rules")) + `},` +
		`{"id":"mail:4","source":"mail","kind":"mail","agent":{"error":` + q(zh("inbox-errors.no_item")) + `},"handling":{"error":"the inbox item could not be saved"}},` +
		`{"id":"mail:5","source":"mail","kind":"mail","chain":[{"kind":"failed","text":"x","detail":"no such inbox item"}]},` +
		`{"error":"no such inbox item","items":[{"id":"n:1","source":"s","kind":"k","agent":{"error":"no such inbox item"}}]},` +
		`{"title":"plain <b>"}` +
		`],"triage":{"waiting":1,"reason":` + q(zh("assistant-inbox_triage.busy")) + `,"sources":[{"id":"mail","reason":` + q(zh("assistant-inbox_triage.slow")) + `,"read_reason":` + q(zh("inbox-errors.no_item")) + `}]}}`
	body400 := string(inboxBody(tb, 400))
	one := func(name, body string) mergeCase {
		return mergeCase{name, "items", []listAnswer{listOf("assistant", 200, body)}}
	}
	return []mergeCase{
		one("inbox 400", body400),
		{"todos", "todos", []listAnswer{listOf("assistant", 200, `{"todos":[{"id":"t1","title":"a","done":false},{"id":"t2","title":"b","due":null}],"counts":{"open":2}}`+"\n")}},
		{"extensions", "extensions", []listAnswer{listOf("assistant", 200, `{"extensions":[{"id":"x","name":"X","settings":{"a":[1,2]}}],"market":{"ok":true}}`+"\n")}},
		one("mixed changed and unchanged", mixed),
		one("manager already set", `{"items":[{"manager":"other","id":"a"},{"id":"b","manager":""},{"m\u0061nager":1},{"id":"c","Manager":"x"}]}`),
		one("pretty", "{ \"z\": [1, 2],\n \"items\" : [ {\"a\": \"x\"} , {} , { } , 3, \"s\", [ 1 ], {\"b\" :1}, {\"c\":1 }, {\"d\":[ ]}],\n \"as_of\": \"t \" }\n"),
		one("html and separators", "{\"items\":[{\"t\":\"<b>&amp;\"},{\"t\":\"a&b\"},\"x>y\",{\"t\":\"a\u2028b\u2029c\"},\"<i>\",{\"<k>\":1},{\"k\u2028\":1}],\"note\":\"a<b\",\"amp\":\"&\",\"sep\":\"x\u2028\",\"esc\":\"\\u003c\\n\"}"),
		one("empty objects", `{"items":[{},{ },{"a":{}}]}`),
		one("not objects", `{"items":[1,-0,1.50,1e3,"s",true,null,[1,{"a":2}],[ ]]}`),
		one("no key", `{"b":1,"a":{"x":"y"}}`),
		one("key not an array", `{"items":{"a":1}}`),
		one("null key", `{"items":null,"a":1}`),
		one("empty list", `{"items":[],"a":1}`),
		one("escaped top key", `{"items":[{"id":"a"}],"\u0061s_of":"t"}`),
		one("escaped list key", `{"\u0069tems":[{"id":"a"}],"items":[{"id":"b"}]}`),
		one("non-ASCII top key", "{\"items\":[{\"id\":\"a\"}],\"\u00e9\":1,\"z\":2}"),
		one("invalid UTF-8 top key", "{\"items\":[],\"\xff\":1,\"a\":2}"),
		one("repeated top key", `{"items":[{"id":"a"}],"a":1,"a":2}`),
		one("repeated list key", `{"items":[{"id":"a"}],"items":[{"id":"b"}]}`),
		one("repeated item key", `{"items":[{"id":"a","id":"b"}]}`),
		one("top is an item", `{"id":"lark:1","source":"lark","kind":"dm","agent":{"error":"no such inbox item"},"items":[{"id":"n","source":"s","kind":"k","agent":{"error":"no such inbox item"}}]}`),
		one("invalid UTF-8", "{\"items\":[{\"t\":\"a\xffb\"},\"\xfe\"],\"x\":\"\xff\"}"),
		one("null body", `null`),
		one("array body", `[{"id":"a","source":"s","kind":"k","agent":{"error":"no such inbox item"}}]`),
		one("not JSON", `{"items":[}`),
		one("trailing data", `{"items":[]} {}`),
		{"first manager failed", "items", []listAnswer{listOf("assistant", 503, `{"error":"no such inbox item","code":"not_found"}`), listOf("m-b", 200, mixed)}},
		{"nobody answered", "items", []listAnswer{listOf("assistant", 404, `{"error":"no such inbox item","code":"not_found"}`), listOf("m-b", 200, `[]`)}},
		{"two managers, one changes", "items", []listAnswer{listOf("assistant", 200, mixed), listOf("m-b<&>", 200, `{"items":[{"id":"x","source":"s","kind":"k","title":"same"}],"as_of":"u"}`)}},
		{"null, then one needing decoding", "items", []listAnswer{listOf("assistant", 200, `null`), listOf("m-b", 200, `{"items":[{"id":"x"}],"a":1,"a":2}`)}},
		{"two managers, second needs decoding", "items", []listAnswer{listOf("assistant", 200, mixed), listOf("m-b", 200, `{"items":[{"id":"x"}],"a":1,"a":2}`)}},
	}
}

// The merge writes what the old one wrote, byte for byte, header and status too; for the inbox, in every language,
// what localizing each answer whole before merging it wrote.
func TestWriteMergedMatchesDecoding(t *testing.T) {
	for _, c := range mergeCases(t) {
		want := httptest.NewRecorder()
		legacyWriteMerged(want, c.key, cloneAnswers(c.answers))
		got := httptest.NewRecorder()
		writeMerged(got, c.key, cloneAnswers(c.answers))
		sameResponse(t, c.name, got, want)
		if c.key != "items" {
			continue
		}
		forms := &inboxForms{}
		for _, l := range []i18n.Lang{i18n.En, i18n.ZhHans, i18n.Ja, i18n.En} { // en again: from the kept forms
			for _, route := range []string{"/api/v1/inbox", "/api/v1/inbox/done"} {
				sameResponse(t, fmt.Sprintf("%s, %s %s", c.name, l, route), inboxMerge(forms, l, route, c.key, c.answers), legacyInboxMerge(l, c.key, c.answers))
			}
		}
	}
}

// The todos' owners are read from the merge's scan where it says what decoding reads, else by decoding: the same ids
// noted for every shape, case-folded or repeated keys and escaped ids included.
func TestTodoOwnersMatchDecoding(t *testing.T) {
	for _, body := range []string{
		`{"todos":[{"id":"t1","title":"a"},{"id":"t2"}]}`,
		`{"todos":[],"counts":{}}`,
		`{"todos":null}`,
		`{"counts":1}`,
		`null`,
		`[]`,
		`{"todos":[1]}`,
		`{"todos":[null,{"id":"t3"}]}`,
		`{"todos":[{"title":"no id"},{"id":""},{"id":null}]}`,
		`{"todos":[{"id":5}]}`,
		`{"todos":[1,{"id":"t19"}]}`,
		`{"todos":[{"id":5},{"id":"t20"}]}`,
		`{"todos":[{"ID":"t4"}]}`,
		`{"todos":[{"i_d":"x","I-D":"y","id":"t21"}]}`,
		`{"todos":[{"Id":"t5","id":"t6"}]}`,
		`{"todos":[{"id":"t7","id":"t8"}]}`,
		`{"todos":[{"\u0069d":"t9"}]}`,
		`{"todos":[{"id":"t\u0031"}]}`,
		`{"todos":[{"id":"t<&>"}]}`,
		`{"todos":[{"id":"t\"q"}]}`,
		`{"todos":[{"id":"\u00e9"}]}`,
		`{"todos":[{"id":"t10"}],"Todos":[{"id":"t11"}]}`,
		`{"Todos":[{"id":"t12"}]}`,
		`{"todos":[{"id":"t13"}],"todos":[{"id":"t14"}]}`,
		`{"todos":{"id":"t15"}}`,
		`{ "todos" : [ { "id" : "t16" } ] }`,
		`{"todos":[{"id":"t17","x":{"id":"nested"}}]}`,
		`{"todos":[{"id":"t18"}]`,
	} {
		var want []string
		for _, id := range decodedTodoIDs([]byte(body)) {
			if id != "" {
				want = append(want, id)
			}
		}
		h := &Hub{}
		a := listOf("m-a", 200, body)
		h.noteTodoOwners(&a)
		got := slices.Sorted(maps.Keys(h.todoOwners))
		slices.Sort(want)
		if !slices.Equal(got, slices.Compact(want)) {
			t.Errorf("%s: noted %q, decoding reads %q", body, got, want)
		}
		for id, m := range h.todoOwners {
			if m != "m-a" {
				t.Errorf("%s: %s noted for %s", body, id, m)
			}
		}
		merged, plain := httptest.NewRecorder(), httptest.NewRecorder()
		writeMerged(merged, "todos", []listAnswer{a})
		legacyWriteMerged(plain, "todos", []listAnswer{listOf("m-a", 200, body)})
		sameResponse(t, body, merged, plain)
	}
}

// A 2xx answer of null is no answer, as any other that is not an object; it made the merge panic.
func TestWriteMergedNullBody(t *testing.T) {
	rec := httptest.NewRecorder()
	writeMerged(rec, "items", []listAnswer{listOf("assistant", 200, `null`), listOf("m-b", 200, `{"items":[{"id":"b"}]}`)})
	if rec.Code != 200 || rec.Body.String() != `{"items":[{"manager":"m-b","id":"b"}]}`+"\n" || rec.Header().Get("X-Kept") != "m-b" {
		t.Fatalf("%d %v %s", rec.Code, rec.Header(), rec.Body)
	}
}

// Each localized item is kept for its slot by its bytes, only the latest answer's: a GET after one item changed
// localizes that one and writes what a cold merge writes; another language or route is a slot of its own.
func TestInboxFormsKeepTheLatestAnswer(t *testing.T) {
	body := inboxBody(t, 40)
	var v map[string]any
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		t.Fatal(err)
	}
	forms := &inboxForms{}
	merge := func(l i18n.Lang, route string, body []byte) {
		t.Helper()
		answers := []listAnswer{listOf("assistant", 200, string(body))}
		sameResponse(t, string(l)+" "+route, inboxMerge(forms, l, route, "items", answers), inboxMerge(&inboxForms{}, l, route, "items", answers))
		sameResponse(t, string(l)+" "+route+" whole", inboxMerge(forms, l, route, "items", answers), legacyInboxMerge(l, "items", answers))
	}
	slotOf := func(l i18n.Lang, route string) map[inboxKey]inboxForm {
		forms.mu.Lock()
		defer forms.mu.Unlock()
		return forms.slots[inboxSlot{route: route, manager: "assistant", lang: l}].forms
	}
	merge(i18n.En, "/api/v1/inbox", body)
	first := maps.Clone(slotOf(i18n.En, "/api/v1/inbox"))
	if len(first) != 40 {
		t.Fatalf("kept %d items, want 40", len(first))
	}

	items := v["items"].([]any)
	items[7].(map[string]any)["title"] = "changed <title>"
	v["items"] = items[:30]
	var b bytes.Buffer
	_ = json.NewEncoder(&b).Encode(v)
	merge(i18n.En, "/api/v1/inbox", b.Bytes())
	next := slotOf(i18n.En, "/api/v1/inbox")
	fresh := 0
	for k := range next {
		if _, ok := first[k]; !ok {
			fresh++
		}
	}
	if len(next) != 30 || fresh != 1 {
		t.Fatalf("kept %d items, %d new; want the 30 of the latest answer, 1 new", len(next), fresh)
	}

	merge(i18n.ZhHans, "/api/v1/inbox", b.Bytes())
	merge(i18n.En, "/api/v1/inbox/done", body)
	if len(slotOf(i18n.ZhHans, "/api/v1/inbox")) != 30 || len(slotOf(i18n.En, "/api/v1/inbox")) != 30 || len(slotOf(i18n.En, "/api/v1/inbox/done")) != 40 {
		t.Fatal("a language or a route did not keep its own items")
	}
	merge(i18n.En, "/api/v1/inbox", body) // back to the first answer
}

// The merge allocates about its output: 530,152 bytes for these 400 items (477,131 B), where decoding the answer and
// encoding it again took 7.0 MB. An inbox GET whose items are kept adds the slot's map, 605,368 bytes in all, where
// localizing the whole answer before merging it took 13.2 MB in en. The bounds are those plus 3%. Run in a process of
// its own (isolatedSessionAllocation): TotalAlloc counts every goroutine.
func TestMergedListAllocation(t *testing.T) {
	if !isolatedSessionAllocation(t) {
		return
	}
	body := inboxBody(t, 400)
	answers := []listAnswer{listOf("assistant", 200, string(body))}
	forms := &inboxForms{}
	for _, c := range []struct {
		name  string
		limit int
		merge func()
	}{
		{"writeMerged", 546_000, func() { writeMerged(discard(), "items", answers) }},
		{"inbox en, kept", 623_500, func() {
			local := []listAnswer{answers[0]}
			local[0].localize = &inboxLocalize{forms: forms, slot: inboxSlot{route: "/api/v1/inbox", manager: "assistant", lang: i18n.En}}
			writeMerged(discard(), "items", local)
		}},
	} {
		c.merge()
		if got := allocated(c.merge); got > c.limit && !raceEnabled {
			t.Errorf("%s: %d bytes for 400 items (%d B), want at most %d", c.name, got, len(body), c.limit)
		} else {
			t.Logf("%s: %d bytes", c.name, got)
		}
	}
}

// discardWriter keeps nothing it is written but its header, one map as a response's: a recorder's growing body would
// be counted as the merge's.
type discardWriter struct{ header http.Header }

func discard() discardWriter                      { return discardWriter{header: http.Header{}} }
func (d discardWriter) Header() http.Header       { return d.header }
func (discardWriter) Write(b []byte) (int, error) { return len(b), nil }
func (discardWriter) WriteHeader(int)             {}

func TestAppendJSONID(t *testing.T) {
	for _, id := range []string{"", "assistant", "m-b", "m b~", "m<b", "a>b", "a&b", `a"b`, `a\b`, "a\nb", "a\x7fb", "a\x00b",
		"\u00e9", "\u2028", "\xff", strings.Repeat("m", 100)} {
		want, _ := json.Marshal(id)
		if got := appendJSONID([]byte("x"), id); string(got) != "x"+string(want) {
			t.Errorf("appendJSONID(%q) = %s, want x%s", id, got, want)
		}
	}
}

func FuzzWriteMerged(f *testing.F) {
	for _, c := range mergeCases(f) {
		if c.key == "items" && len(c.answers) == 1 && len(c.answers[0].body) < 4<<10 {
			f.Add(c.answers[0].body)
		}
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		answers := []listAnswer{{id: "assistant", status: 200, header: http.Header{}, body: body}}
		want := httptest.NewRecorder()
		legacyWriteMerged(want, "items", cloneAnswers(answers))
		got := httptest.NewRecorder()
		writeMerged(got, "items", cloneAnswers(answers))
		sameResponse(t, "merge", got, want)
		if strings.Contains(string(body), "\"") { // anything InboxJSON could visit
			sameResponse(t, "inbox", inboxMerge(&inboxForms{}, i18n.En, "/api/v1/inbox", "items", answers), legacyInboxMerge(i18n.En, "items", answers))
		}
	})
}
