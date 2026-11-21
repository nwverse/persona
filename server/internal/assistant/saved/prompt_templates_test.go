package saved

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type promptTemplateTestStore struct {
	values       map[string]json.RawMessage
	mu           sync.Mutex
	batches      int
	failBatch    bool
	readsStarted chan struct{}
	resumeReads  chan struct{}
}

func (st *promptTemplateTestStore) Setting(_ context.Context, key string, out any) (bool, error) {
	st.mu.Lock()
	raw, ok := st.values[key]
	var started, resume chan struct{}
	if key == PromptsKey && st.readsStarted != nil {
		started, resume = st.readsStarted, st.resumeReads
		st.readsStarted, st.resumeReads = nil, nil
	}
	st.mu.Unlock()
	if started != nil {
		close(started)
		<-resume
	}
	if !ok {
		return false, nil
	}
	return true, json.Unmarshal(raw, out)
}

func (st *promptTemplateTestStore) PutSetting(ctx context.Context, key string, next any) error {
	return st.put(ctx, map[string]any{key: next})
}

func (st *promptTemplateTestStore) PutSettings(ctx context.Context, next map[string]any) error {
	st.mu.Lock()
	fail := st.failBatch
	st.mu.Unlock()
	if fail {
		return errors.New("the two prompt settings could not be committed")
	}
	return st.put(ctx, next)
}

func (st *promptTemplateTestStore) put(_ context.Context, next map[string]any) error {
	raws := make(map[string]json.RawMessage, len(next))
	for key, value := range next {
		raw, err := json.Marshal(value)
		if err != nil {
			return err
		}
		raws[key] = raw
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.values == nil {
		st.values = map[string]json.RawMessage{}
	}
	for key, raw := range raws {
		st.values[key] = raw
	}
	if len(next) == 2 {
		st.batches++
	}
	return nil
}

type ordinaryPromptTestStore struct{ promptStore }

func promptTemplateFixture(store any) (*promptBook, *http.ServeMux, *[]string) {
	mux := http.NewServeMux()
	var events []string
	books := &promptBook{h: Host{
		Store:  func() any { return store },
		Handle: func(pattern string, fn http.HandlerFunc) { mux.HandleFunc(pattern, fn) },
		Full:   func(r *http.Request) bool { return r.Header.Get("Role") == "full" },
		Event:  func(_ context.Context, e map[string]any) { events = append(events, e["type"].(string)) },
	}}
	books.routes()
	return books, mux, &events
}

type promptTemplateReply struct {
	TemplateVersion *int     `json:"template_version"`
	Prompt          Prompt   `json:"prompt"`
	Prompts         []Prompt `json:"prompts"`
}

func promptTemplateRequest(mux *http.ServeMux, method, path, role, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/api/v1/assistant/saved-prompts"+path, strings.NewReader(body))
	req.Header.Set("Role", role)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

func promptTemplateResult(t *testing.T, mux *http.ServeMux, method, path, body string, status int) promptTemplateReply {
	t.Helper()
	w := promptTemplateRequest(mux, method, path, "full", body)
	if w.Code != status {
		t.Fatalf("%s %s got %d, want %d: %s", method, path, w.Code, status, w.Body)
	}
	var out promptTemplateReply
	if status == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func promptTemplateBody(name, text string, choice string) string {
	return fmt.Sprintf(`{"name":%q,"text":%q%s}`, name, text, choice)
}

func requireTemplateVersion(t *testing.T, reply promptTemplateReply) {
	t.Helper()
	if reply.TemplateVersion == nil || *reply.TemplateVersion != 1 {
		t.Fatalf("response lacks template support: %+v", reply)
	}
}

func TestSavedPromptTemplatesOptInAndOldWrites(t *testing.T) {
	st := &promptTemplateTestStore{}
	_, mux, events := promptTemplateFixture(st)
	empty := promptTemplateResult(t, mux, "GET", "", "", 200)
	requireTemplateVersion(t, empty)
	if empty.Prompts == nil || len(empty.Prompts) != 0 {
		t.Fatalf("empty capable list: %+v", empty)
	}

	const source = "Review {{ repository }} and {{branch}}."
	plain := promptTemplateResult(t, mux, "POST", "", promptTemplateBody("plain-review", source, ""), 200)
	requireTemplateVersion(t, plain)
	if plain.Prompt.Template || plain.Prompt.Text != source {
		t.Fatalf("ordinary braces enabled template rendering: %+v", plain)
	}
	template := promptTemplateResult(t, mux, "POST", "", promptTemplateBody("review", source, `,"template":true`), 200)
	requireTemplateVersion(t, template)
	item := template.Prompt
	if !item.Template || item.ID == "" || item.Text != source {
		t.Fatalf("valid template was not enabled: %+v", item)
	}
	if strings.Contains(string(st.values[PromptsKey]), `"template"`) {
		t.Fatalf("template flag leaked into the legacy list: %s", st.values[PromptsKey])
	}
	var flags promptTemplateFlags
	if _, err := st.Setting(t.Context(), PromptTemplatesKey, &flags); err != nil || !flags.enabled(item) {
		t.Fatalf("metadata did not bind the source text: %v %v", flags, err)
	}

	path := "/" + item.ID
	changed := "Check {{repository}} for {{focus}}"
	legacyValid := promptTemplateResult(t, mux, "PUT", path, promptTemplateBody("renamed", changed, ""), 200)
	requireTemplateVersion(t, legacyValid)
	if !legacyValid.Prompt.Template || legacyValid.Prompt.ID != item.ID || !legacyValid.Prompt.CreatedAt.Equal(item.CreatedAt) {
		t.Fatalf("an old client's valid edit lost identity or opt-in: %+v", legacyValid)
	}
	order := fmt.Sprintf(`{"ids":[%q,%q]}`, item.ID, plain.Prompt.ID)
	reordered := promptTemplateResult(t, mux, "PUT", "/order", order, 200)
	if !reordered.Prompts[0].Template || reordered.Prompts[1].Template {
		t.Fatalf("moving rows changed opt-in: %+v", reordered)
	}
	legacyInvalid := promptTemplateResult(t, mux, "PUT", path, promptTemplateBody("renamed", "literal {{", ""), 200)
	requireTemplateVersion(t, legacyInvalid)
	if legacyInvalid.Prompt.Template || legacyInvalid.Prompt.Text != "literal {{" {
		t.Fatalf("old client could not replace with ordinary text: %+v", legacyInvalid)
	}
	on := promptTemplateResult(t, mux, "PUT", path, promptTemplateBody("renamed", changed, `,"template":true`), 200)
	off := promptTemplateResult(t, mux, "PUT", path, promptTemplateBody("renamed", changed, `,"template":false`), 200)
	requireTemplateVersion(t, off)
	if !on.Prompt.Template || off.Prompt.Template || off.Prompt.Text != changed || off.Prompt.ID != item.ID {
		t.Fatalf("disable did not remove the flag or preserve text and identity: %+v %+v", on, off)
	}
	plainAgain := promptTemplateResult(t, mux, "PUT", path, promptTemplateBody("renamed", changed, ""), 200)
	if plainAgain.Prompt.Template {
		t.Fatal("an omitted setting re-enabled an explicitly disabled template")
	}
	promptTemplateResult(t, mux, "DELETE", path, "", 200)
	if len(*events) != st.batches || len(*events) != 9 {
		t.Fatalf("every committed change should broadcast once: %v, %d batches", *events, st.batches)
	}
	for _, event := range *events {
		if event != "assistant_saved_prompts" {
			t.Fatalf("changed event contract: %q", event)
		}
	}
}

func TestSavedPromptTemplatesRollbackOnlyReenablesExactSource(t *testing.T) {
	st := &promptTemplateTestStore{}
	_, mux, _ := promptTemplateFixture(st)
	template := promptTemplateResult(t, mux, "POST", "", promptTemplateBody("review", "{{repo}}", `,"template":true`), 200).Prompt
	legacy := ordinaryPromptTestStore{st}
	_, oldMux, _ := promptTemplateFixture(legacy)
	old := promptTemplateResult(t, oldMux, "GET", "", "", 200)
	if old.TemplateVersion != nil || old.Prompts[0].Template || old.Prompts[0].Text != template.Text {
		t.Fatalf("old transport advertised template support or changed source: %+v", old)
	}
	promptTemplateResult(t, oldMux, "POST", "", promptTemplateBody("unsupported", "{{field}}", `,"template":true`), 503)
	path := "/" + template.ID
	replaced := promptTemplateResult(t, oldMux, "PUT", path, promptTemplateBody("review-from-old", "{{different}}", ""), 200)
	if replaced.TemplateVersion != nil || replaced.Prompt.Template {
		t.Fatal("an ordinary old-client edit used template metadata")
	}

	// Reopen the capable manager after an old writer changed the source. Its sidecar key
	// survived, but it does not opt in the replacement, even though its syntax is valid.
	_, reopened, _ := promptTemplateFixture(st)
	changed := promptTemplateResult(t, reopened, "GET", "", "", 200)
	requireTemplateVersion(t, changed)
	if changed.Prompts[0].Template || changed.Prompts[0].Text != "{{different}}" {
		t.Fatalf("downgrade enabled unapproved template text: %+v", changed)
	}
	if !strings.Contains(string(st.values[PromptTemplatesKey]), promptTextHash(template.Text)) {
		t.Fatal("the old manager removed the sidecar setting")
	}
	promptTemplateResult(t, reopened, "POST", "", promptTemplateBody("unrelated", "plain", ""), 200)
	promptTemplateResult(t, oldMux, "PUT", path, promptTemplateBody("review-restored", template.Text, ""), 200)
	restored := promptTemplateResult(t, reopened, "GET", "", "", 200)
	if !restored.Prompts[0].Template || restored.Prompts[0].Text != template.Text {
		t.Fatalf("restoring exact source lost its original metadata: %+v", restored)
	}
	promptTemplateResult(t, reopened, "DELETE", path, "", 200)
	if strings.Contains(string(st.values[PromptTemplatesKey]), template.ID) {
		t.Fatal("deleting a prompt left its template metadata")
	}
}

func TestSavedPromptTemplatesRejectInvalidAndDoNotPartiallyWrite(t *testing.T) {
	st := &promptTemplateTestStore{}
	_, mux, events := promptTemplateFixture(st)
	first := promptTemplateResult(t, mux, "POST", "", promptTemplateBody("review", "Check {{repo}}", `,"template":true`), 200).Prompt
	path := "/" + first.ID
	before := map[string]json.RawMessage{
		PromptsKey: st.values[PromptsKey], PromptTemplatesKey: st.values[PromptTemplatesKey],
	}
	for _, choice := range []string{`,"template":null`, `,"template":"true"`, `,"template":1`} {
		promptTemplateResult(t, mux, "PUT", path, promptTemplateBody(first.Name, first.Text, choice), 400)
	}
	promptTemplateResult(t, mux, "PUT", path, promptTemplateBody(first.Name, "invalid {{", `,"template":true`), 400)
	if denied := promptTemplateRequest(mux, "PUT", path, "read", promptTemplateBody(first.Name, first.Text, `,"template":false`)); denied.Code != 403 {
		t.Fatalf("read-only device could change the flag: %d %s", denied.Code, denied.Body)
	}
	st.failBatch = true
	promptTemplateResult(t, mux, "PUT", path, promptTemplateBody(first.Name, "plain", `,"template":false`), 500)
	if !reflect.DeepEqual(st.values, before) || len(*events) != 1 || st.batches != 1 {
		t.Fatalf("a rejected operation partly changed the prompts or metadata: %v, %v", st.values, *events)
	}
}

func TestSavedPromptTemplatesReadAndWriteUseTheSameBookLock(t *testing.T) {
	st := &promptTemplateTestStore{}
	book, mux, _ := promptTemplateFixture(st)
	input := promptTemplateResult(t, mux, "POST", "", promptTemplateBody("review", "{{repo}}", ""), 200).Prompt
	path := "/" + input.ID
	st.readsStarted, st.resumeReads = make(chan struct{}), make(chan struct{})
	started, resume := st.readsStarted, st.resumeReads
	reader := make(chan *httptest.ResponseRecorder, 1)
	go func() { reader <- promptTemplateRequest(mux, "GET", "", "full", "") }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		close(resume)
		t.Fatal("read did not reach the legacy rows")
	}
	if book.mu.TryLock() {
		book.mu.Unlock()
		close(resume)
		t.Fatal("GET read two settings without the book lock")
	}
	writer := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		writer <- promptTemplateRequest(mux, "PUT", path, "full", promptTemplateBody(input.Name, input.Text, `,"template":true`))
	}()
	close(resume)
	read, wrote := <-reader, <-writer
	if read.Code != 200 || wrote.Code != 200 || strings.Contains(read.Body.String(), `"template":true`) ||
		!strings.Contains(wrote.Body.String(), `"template":true`) {
		t.Fatalf("GET crossed the same-source flag commit: read %s, write %s", read.Body, wrote.Body)
	}
}

func TestSavedPromptTemplatesGrammar(t *testing.T) {
	for _, tc := range []struct {
		name   string
		source string
		valid  bool
	}{
		{"one field", "{{repo}}", true},
		{"case-sensitive letters and marks", "{{Équipe_٣}} / {{équipe_٣}} / {{a\u0301}}", true},
		{"ASCII padding and hyphen", "Review {{\t_repo-name  }}", true},
		{"escape braces and backslash", `\{{literal\}} \\ {{repo}} \ordinary`, true},
		{"escape followed by a declaration", `\\{{repo}}`, true},
		{"single braces are literal", "{a} {{repo}} {b}", true},
		{"nested opening braces", "{{{repo}}", false},
		{"single closing brace is literal", "{{repo}}}", true},
		{"max Unicode identifier", "{{" + strings.Repeat("界", 32) + "}}", true},
		{"identifier beyond maximum", "{{" + strings.Repeat("界", 33) + "}}", false},
		{"maximum slots", strings.Repeat("{{repo}}", 64), true},
		{"too many slots", strings.Repeat("{{repo}}", 65), false},
		{"maximum distinct fields", "{{a}}{{b}}{{c}}{{d}}{{e}}{{f}}{{g}}{{h}}", true},
		{"too many distinct fields", "{{a}}{{A}}{{b}}{{c}}{{d}}{{e}}{{f}}{{g}}{{h}}", false},
		{"escaped double braces only", `\{{repo\}}`, false},
		{"no fields", "plain text", false},
		{"empty identifier", "{{ \t }}", false},
		{"unauthorized leading class", "{{٣repo}}", false},
		{"nondecimal numeric class", "{{repo²}}", false},
		{"line break in name", "{{repo\n}}", false},
		{"non ASCII field padding", "{{\u00a0repo}}", false},
		{"missing close", "Review {{repo}", false},
		{"stray close", "{{repo}} }}", false},
		{"escape inside a name", `{{re\po}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := validPromptTemplate(tc.source); got != tc.valid {
				t.Fatalf("validPromptTemplate(%q) = %v, want %v", tc.source, got, tc.valid)
			}
		})
	}
}
