package saved

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
)

type todoViewTestStore struct {
	mu        sync.Mutex
	raw       []byte
	casWrites int
	down      bool
}

func (st *todoViewTestStore) Setting(_ context.Context, key string, out any) (bool, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if key != TodoViewsKey || st.raw == nil {
		return false, nil
	}
	return true, json.Unmarshal(st.raw, out)
}

func (st *todoViewTestStore) CompareSetting(_ context.Context, key string, revision int64, value any) (bool, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if key != TodoViewsKey || st.down {
		return false, errors.New("saved view store unavailable")
	}
	var doc todoViewsDocument
	if st.raw != nil {
		if err := json.Unmarshal(st.raw, &doc); err != nil {
			return false, err
		}
	}
	if doc.Revision != revision {
		return false, nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return false, err
	}
	st.raw = raw
	st.casWrites++
	return true, nil
}

type todoViewTestEvents struct {
	mu     sync.Mutex
	values []map[string]any
}

func todoViewFixture(store any) (*http.ServeMux, *todoViewTestEvents) {
	mux, events := http.NewServeMux(), &todoViewTestEvents{}
	Register(Host{
		Store:  func() any { return store },
		Handle: func(pattern string, fn http.HandlerFunc) { mux.HandleFunc(pattern, fn) },
		Event: func(_ context.Context, e map[string]any) {
			events.mu.Lock()
			defer events.mu.Unlock()
			events.values = append(events.values, e)
		},
		Full:    func(r *http.Request) bool { return r.Header.Get("Role") == "full" },
		BodyErr: func(w http.ResponseWriter, _, _ error) { w.WriteHeader(http.StatusRequestEntityTooLarge) },
	})
	return mux, events
}

func todoViewRequest(mux *http.ServeMux, method, role, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, assistantwire.SavedTodoViewsPath, strings.NewReader(body))
	req.Header.Set("Role", role)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

func todoViewReply(t *testing.T, mux *http.ServeMux, method, role, body string, status int) todoViewsResponse {
	t.Helper()
	w := todoViewRequest(mux, method, role, body)
	if w.Code != status {
		t.Fatalf("%s %s: got %d, want %d (%s)", method, body, w.Code, status, w.Body)
	}
	var out todoViewsResponse
	if status == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func todoViewInput(op, revision, id, name string, criteria todoViewCriteria) string {
	fields := map[string]any{"op": op, "revision": revision}
	if op != "add" {
		fields["id"] = id
	}
	if op != "delete" {
		fields["name"], fields["criteria"] = name, criteria
	}
	raw, _ := json.Marshal(fields)
	return string(raw)
}

func TestSavedTodoViewsLifecycleAndOwnerOnly(t *testing.T) {
	store := &todoViewTestStore{}
	mux, events := todoViewFixture(store)
	empty := todoViewReply(t, mux, "GET", "full", "", 200)
	if empty.SchemaVersion != 1 || empty.Revision != "0" || empty.Views == nil || len(empty.Views) != 0 || !empty.CanWrite || store.casWrites != 0 {
		t.Fatalf("empty read seeded storage or omitted the collection: %+v", empty)
	}
	criteria := todoViewCriteria{Text: "  > draft  ", DueWindow: "today", SectionSort: "due"}
	add := todoViewInput("add", "0", "", " \uFEFF Review queue \uFEFF ", criteria)
	for _, role := range []string{"answer", "link", "extension", "read"} {
		todoViewReply(t, mux, "GET", role, "", 403)
		todoViewReply(t, mux, "POST", role, add, 403)
	}
	first := todoViewReply(t, mux, "POST", "full", add, 200)
	item := first.Views[0]
	if first.Revision != "1" || !validTodoViewID(item.ID) || item.Name != "Review queue" ||
		item.Criteria.Text != "> draft" || item.Criteria.DueWindow != "today" || item.Criteria.SectionSort != "due" ||
		item.CreatedAt.IsZero() || !item.CreatedAt.Equal(item.UpdatedAt) {
		t.Fatalf("first saved view: %+v", first)
	}
	criteria = todoViewCriteria{DueWindow: "no_due", SectionSort: "priority"}
	todoViewReply(t, mux, "POST", "full", todoViewInput("add", "1", "", "review QUEUE", criteria), 409)
	todoViewReply(t, mux, "POST", "full", todoViewInput("delete", "0", item.ID, "", criteria), 409)
	update := todoViewInput("update", "1", item.ID, "Not scheduled", criteria)
	second := todoViewReply(t, mux, "POST", "full", update, 200)
	current := second.Views[0]
	if second.Revision != "2" || current.ID != item.ID || current.Criteria != criteria ||
		!current.CreatedAt.Equal(item.CreatedAt) || current.UpdatedAt.Before(item.UpdatedAt) {
		t.Fatalf("update changed identity, dates, or literal criteria: %+v", second)
	}
	if r := todoViewReply(t, mux, "POST", "full", todoViewInput("update", "2", item.ID, current.Name, criteria), 200); r.Revision != "2" {
		t.Fatal("unchanged update advanced the revision")
	}
	restarted, _ := todoViewFixture(store)
	if r := todoViewReply(t, restarted, "GET", "full", "", 200); r.Revision != "2" || r.Views[0] != current {
		t.Fatalf("restart changed the document: %+v", r)
	}
	deleted := todoViewReply(t, mux, "POST", "full", todoViewInput("delete", "2", item.ID, "", criteria), 200)
	if deleted.Revision != "3" || deleted.Views == nil || len(deleted.Views) != 0 {
		t.Fatalf("deleting the last view reset the document: %+v", deleted)
	}
	todoViewReply(t, mux, "POST", "full", todoViewInput("delete", "3", item.ID, "", criteria), 404)
	if store.casWrites != 3 || len(events.values) != 3 {
		t.Fatalf("got %d writes and %d events, want three each", store.casWrites, len(events.values))
	}
	for i, e := range events.values {
		if len(e) != 2 || e["type"] != "saved_todo_views" || e["revision"] != fmt.Sprint(i+1) {
			t.Fatalf("invalidation exposed view data or wrong revision: %v", e)
		}
	}
}

func TestSavedTodoViewStrictRequestValidation(t *testing.T) {
	store := &todoViewTestStore{}
	mux, events := todoViewFixture(store)
	const prefix = `{"revision":"0","op":"add","name":"New","criteria":`
	const criteria = `{"text":"","due_window":"all","section_sort":"default"}`
	valid := prefix + criteria + "}"
	for _, invalid := range []string{
		"null", "{}",
		strings.Replace(valid, `"0"`, "0", 1),
		strings.Replace(valid, `"0"`, `"00"`, 1),
		strings.Replace(valid, `"0"`, `"9223372036854775807"`, 1),
		strings.Replace(valid, `"name":"New"`, `"Name":"New"`, 1),
		strings.Replace(valid, `"name":"New"`, `"name":"New","name":"Other"`, 1),
		strings.Replace(valid, `"name":"New"`, `"name":"New","id":null`, 1),
		strings.Replace(valid, `"name":"New"`, `"name":"\uFEFF "`, 1),
		valid + "{}",
		prefix + "null}",
		prefix + `{"due_window":"all","section_sort":"default"}}`,
		prefix + `{"text":null,"due_window":"all","section_sort":"default"}}`,
		prefix + `{"text":"two\nlines","due_window":"all","section_sort":"default"}}`,
		prefix + `{"text":"","text":"other","due_window":"all","section_sort":"default"}}`,
		prefix + `{"text":"","due_window":"overdue","section_sort":"default"}}`,
		prefix + `{"text":"","due_window":"all","section_sort":"recent"}}`,
		prefix + `{"text":"","due_window":"all","section_sort":"default","grouping":"folder"}}`,
		todoViewInput("add", "0", "", strings.Repeat("界", 65), todoViewCriteria{DueWindow: "all", SectionSort: "default"}),
		todoViewInput("add", "0", "", "Big text", todoViewCriteria{Text: strings.Repeat("界", 342), DueWindow: "all", SectionSort: "default"}),
	} {
		todoViewReply(t, mux, "POST", "full", invalid, 400)
	}
	todoViewReply(t, mux, "POST", "full", strings.Repeat(" ", 16<<10+1), 413)
	if store.casWrites != 0 || len(events.values) != 0 {
		t.Fatalf("invalid request modified saved data: %s, %v", store.raw, events.values)
	}
}

func TestSavedTodoViewsConcurrentRevisionsAndStorageFailures(t *testing.T) {
	store := &todoViewTestStore{raw: []byte(`{"schema_version":1,"revision":9007199254740993,"views":[]}`)}
	a, aEvents := todoViewFixture(store)
	b, bEvents := todoViewFixture(store)
	const revision = "9007199254740993"
	if got := todoViewReply(t, a, "GET", "full", "", 200).Revision; got != revision {
		t.Fatalf("revision lost precision on the wire: %s", got)
	}
	c := todoViewCriteria{DueWindow: "all", SectionSort: "default"}
	start, statuses := make(chan struct{}), make(chan int, 2)
	for index, mux := range []*http.ServeMux{a, b} {
		go func() {
			<-start
			w := todoViewRequest(mux, "POST", "full", todoViewInput("add", revision, "", fmt.Sprint(index), c))
			statuses <- w.Code
		}()
	}
	close(start)
	first, second := <-statuses, <-statuses
	if first+second != 200+409 || store.casWrites != 1 || len(aEvents.values)+len(bEvents.values) != 1 {
		t.Fatalf("concurrent writes were not serialized: %d %d, %d writes, events %v %v", first, second, store.casWrites, aEvents.values, bEvents.values)
	}
	doc := todoViewReply(t, a, "GET", "full", "", 200)
	if doc.Revision != "9007199254740994" || len(doc.Views) != 1 {
		t.Fatalf("incorrect CAS winner: %+v", doc)
	}
	store.down = true
	todoViewReply(t, b, "POST", "full", todoViewInput("delete", doc.Revision, doc.Views[0].ID, "", c), 503)
	kept := todoViewReply(t, b, "GET", "full", "", 200)
	if kept.Revision != doc.Revision || len(kept.Views) != 1 || kept.Views[0] != doc.Views[0] || store.casWrites != 1 {
		t.Fatalf("failed write changed the saved data: %+v", kept)
	}
	store.down = false
	full := todoViewsDocument{SchemaVersion: todoViewVersion, Revision: 50}
	now := time.Now().UTC()
	for i := range todoViewsMax {
		full.Views = append(full.Views, savedTodoView{ID: fmt.Sprintf("view_%024x", i), Name: fmt.Sprint(i),
			Criteria: c, CreatedAt: now, UpdatedAt: now})
	}
	store.raw, _ = json.Marshal(full)
	todoViewReply(t, a, "POST", "full", todoViewInput("add", "50", "", "one more", c), 400)
	for _, raw := range []string{
		`{"schema_version":null,"revision":1,"views":[]}`,
		`{"schema_version":2,"revision":1,"views":[]}`,
		`{"schema_version":1,"revision":null,"views":[]}`,
		`{"schema_version":1,"revision":1,"views":null}`,
	} {
		store.raw = []byte(raw)
		todoViewReply(t, a, "GET", "full", "", 503)
	}
	noCAS, _ := todoViewFixture(nil)
	todoViewReply(t, noCAS, "GET", "full", "", 503)
	todoViewReply(t, noCAS, "POST", "full", todoViewInput("add", "0", "", "no durable store", c), 503)
}
