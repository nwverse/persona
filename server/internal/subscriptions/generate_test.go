package subscriptions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/rpc"
)

// Drafting a recipe with a model (generate.go, #2040), against a fake model that answers in turn.

// fakeModel answers each call with the next of answers (a JSON string or an error) and keeps what it was asked,
// with how many calls the connector had had by then.
type fakeModel struct {
	mu      sync.Mutex
	answers []any
	calls   []ModelCall
	conn    *fakeConn
	before  []int // the connector's calls when each model call was made
}

func (m *fakeModel) JSON(_ context.Context, c ModelCall) (json.RawMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c.Turns = append([]Turn(nil), c.Turns...)
	m.calls = append(m.calls, c)
	if m.conn != nil {
		m.before = append(m.before, m.conn.callCount())
	}
	if len(m.answers) == 0 {
		return nil, errors.New("the fake model has no more answers")
	}
	a := m.answers[0]
	m.answers = m.answers[1:]
	if err, ok := a.(error); ok {
		return nil, err
	}
	return json.RawMessage(a.(string)), nil
}

func (m *fakeModel) asked() []ModelCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]ModelCall(nil), m.calls...)
}

const (
	goodPick = `{"tool":"list_notices","args":{"since":"{{since}}","limit":25}}`
	goodMap  = `{"mode":"items","items_path":"$.items","fields":{"id":"$.id","title":"$.title","sender":"$.from.name","time":"$.at","body":"$.text"},"kind":"sub_notice","group_by":"source","note":"New notices from the office, each titled with its subject."}`
)

// withModel gives the env's service a fake model answering answers, the note in language.
func (e *env) withModel(language string, answers ...any) *fakeModel {
	m := &fakeModel{answers: answers, conn: e.conn}
	e.svc = New(Options{Store: e.store, Connectors: e.conns, Clock: e.clock, Location: func() *time.Location { return time.UTC },
		Model: m, Language: func(context.Context) string { return language }, Features: Features{Generate: true},
		Audit: func(_ context.Context, id, action string, by Actor, _ map[string]any) {
			e.mu.Lock()
			e.audit = append(e.audit, id+" "+action+" "+by.ID)
			e.mu.Unlock()
		}})
	return m
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "prompts", name+".golden")
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v (UPDATE_GOLDEN=1 writes it)", name, err)
	}
	if got != string(want) {
		t.Errorf("%s differs from %s\n--- got ---\n%s\n--- want ---\n%s", name, path, got, want)
	}
}

func errOf(t *testing.T, err error) *Error {
	t.Helper()
	var se *Error
	if !errors.As(err, &se) {
		t.Fatalf("not a service error: %v", err)
	}
	return se
}

// A source with no recipe of its own: the model picks from the tools alone, maps from a sample, and the draft that
// passes the checks is answered without anything written; creating with it records the model.
func TestGenerateDraftsARecipeForANewSource(t *testing.T) {
	e := newEnv(t)
	m := e.withModel("English", goodPick, goodMap)
	out, err := e.svc.Generate(e.ctx, GenerateRequest{Connector: "example", Source: "notices"}, Actor{ID: "device:phone"})
	if err != nil {
		t.Fatal(err)
	}
	if !out.DryRun.OK || out.Subscription != nil || out.Recipe.Tool != "list_notices" || out.Recipe.ItemsPath != "$.items" ||
		out.Recipe.Fields.Title != "$.title" || out.Note != "New notices from the office, each titled with its subject." || len(out.DryRun.Items) != 3 {
		t.Fatalf("draft %+v", out)
	}
	if all, _ := e.store.ListSubscriptions(e.ctx); len(all) != 0 || len(e.audit) != 0 {
		t.Fatalf("a draft for a new source wrote %d subscriptions, audit %v", len(all), e.audit)
	}
	calls := m.asked()
	if len(calls) != 2 || m.before[0] != 0 || m.before[1] != 1 {
		t.Fatalf("model calls %d, connector calls before each %v: the pick sees no result, the map one sample", len(calls), m.before)
	}
	pick, mapping := calls[0], calls[1]
	if len(pick.Turns) != 1 || pick.Turns[0].Role != "user" || strings.Contains(pick.Turns[0].Content, "Notice 1") {
		t.Fatalf("pick turns %+v", pick.Turns)
	}
	if s := string(pick.Schema); !strings.Contains(s, `"enum":["list_notices","balance"]`) || strings.Contains(s, "send_message") {
		t.Fatalf("pick schema offers %s: only the subscribable tools", s)
	}
	if !strings.Contains(mapping.Turns[0].Content, `"title":"Notice 1"`) || pick.Label != "subscription pick" || mapping.Label != "subscription map" {
		t.Fatalf("map input %q", mapping.Turns[0].Content)
	}
	golden(t, "pick.system", pick.System)
	golden(t, "pick.user", pick.Turns[0].Content)
	golden(t, "pick.schema", string(pick.Schema))
	golden(t, "map.system", mapping.System)
	golden(t, "map.user", mapping.Turns[0].Content)
	golden(t, "map.schema", string(mapping.Schema))

	sub, err := e.svc.Create(e.ctx, CreateRequest{Connector: "example", Source: "notices", Recipe: &out.Recipe, RecipeOrigin: OriginModel}, Actor{ID: "device:phone"})
	if err != nil || sub.RecipeOrigin != OriginModel || !sub.RecipeAt.Equal(e.clock.Now()) || sub.State != StateOff {
		t.Fatalf("created from the draft: %+v %v", sub, err)
	}
	if _, err := e.svc.Create(e.ctx, CreateRequest{Connector: "example", Source: "notices", RecipeOrigin: OriginModel}, Actor{}); codeOf(err) != CodeBadRequest {
		t.Fatalf("recipe_origin without a recipe: %v", err)
	}
}

// A draft that fails a check is asked for once more with the problem; one that fails again is reported with the dry
// run and the draft, and nothing is saved.
func TestGenerateRetriesAFailingDraftOnceThenReportsWhy(t *testing.T) {
	noTitle := strings.Replace(goodMap, `"title":"$.title"`, `"title":"$.subject"`, 1)
	t.Run("fixed", func(t *testing.T) {
		e := newEnv(t)
		m := e.withModel("English", goodPick, noTitle, goodMap)
		out, err := e.svc.Generate(e.ctx, GenerateRequest{Connector: "example", Source: "notices"}, Actor{})
		if err != nil || out.Recipe.Fields.Title != "$.title" {
			t.Fatalf("%+v %v", out, err)
		}
		retry := m.asked()[2]
		if len(retry.Turns) != 3 || retry.Turns[1].Role != "assistant" || retry.Turns[1].Content != noTitle || retry.Turns[2].Role != "user" {
			t.Fatalf("retry turns %+v", retry.Turns)
		}
		golden(t, "retry.user", retry.Turns[2].Content)
	})
	t.Run("still failing", func(t *testing.T) {
		e := newEnv(t)
		sub := e.create(itemsRecipe())
		m := e.withModel("English", goodPick, noTitle, noTitle, goodMap)
		_, err := e.svc.Generate(e.ctx, GenerateRequest{ID: sub.ID}, Actor{})
		se := errOf(t, err)
		if se.Code != CodeNoTitle || se.Status != http.StatusUnprocessableEntity || se.DryRun == nil || se.DryRun.OK ||
			se.Draft == nil || se.Draft.Fields.Title != "$.subject" {
			t.Fatalf("%+v", se)
		}
		if n := len(m.asked()); n != 3 {
			t.Fatalf("model calls %d, want pick, map and one more map", n)
		}
		if got := e.get(sub.ID); got.Revision != sub.Revision || got.RecipeOrigin != OriginUser {
			t.Fatalf("a failing draft was saved: %+v", got)
		}
	})
	t.Run("a pick that does not fit", func(t *testing.T) {
		e := newEnv(t)
		m := e.withModel("English", `{"tool":"list_notices","args":{"limit":25}}`, goodPick, goodMap)
		if _, err := e.svc.Generate(e.ctx, GenerateRequest{Connector: "example", Source: "notices"}, Actor{}); err != nil {
			t.Fatal(err)
		}
		calls := m.asked()
		if len(calls) != 3 || m.before[1] != 0 || !strings.Contains(calls[1].Turns[2].Content, "(bad_args)") {
			t.Fatalf("calls %d, connector calls %v", len(calls), m.before)
		}
		// Both tries spent on the pick: the draft as far as it got.
		e = newEnv(t)
		e.withModel("English", `{"tool":"list_notices","args":{}}`, `{"tool":"list_notices","args":{"limit":"many"}}`)
		_, err := e.svc.Generate(e.ctx, GenerateRequest{Connector: "example", Source: "notices"}, Actor{})
		if se := errOf(t, err); se.Code != CodeBadArgs || se.Draft == nil || se.Draft.Tool != "list_notices" || e.conn.callCount() != 0 {
			t.Fatalf("%+v, %d connector calls", se, e.conn.callCount())
		}
	})
	t.Run("the connector's own failure is not retried", func(t *testing.T) {
		e := newEnv(t)
		m := e.withModel("English", goodPick, goodMap)
		e.conn.setAnswer(func(connector.CatalogQuery) (string, error) { return "", &connectorDown })
		_, err := e.svc.Generate(e.ctx, GenerateRequest{Connector: "example", Source: "notices"}, Actor{})
		if se := errOf(t, err); se.Code != CodeNotConnected || len(m.asked()) != 1 {
			t.Fatalf("%+v after %d model calls", se, len(m.asked()))
		}
	})
}

var connectorDown = rpc.Error{Code: wire.ConnectorErrorCodeNotSignedIn, Message: "signed out"}

// The sample the model maps from is cut: at most 3 entries a list, 120 characters a string, 8 KB in all.
func TestGenerateSampleStaysWithinLimits(t *testing.T) {
	e := newEnv(t)
	long := strings.Repeat("長", 2000)
	var items []map[string]any
	for i := range 30 {
		extra := map[string]any{}
		for k := range 60 {
			extra[fmt.Sprintf("k%03d", k)] = long
		}
		items = append(items, map[string]any{"id": fmt.Sprint("n", i), "title": long, "at": "2026-11-10T08:00:00Z", "extra": extra,
			"deep": map[string]any{"a": map[string]any{"b": map[string]any{"c": map[string]any{"d": []any{long, long, long, long}}}}}})
	}
	big, _ := json.Marshal(map[string]any{"items": items})
	e.conn.setAnswer(func(connector.CatalogQuery) (string, error) { return string(big), nil })
	m := e.withModel("English", goodPick, goodMap)
	if _, err := e.svc.Generate(e.ctx, GenerateRequest{Connector: "example", Source: "notices"}, Actor{}); err != nil {
		t.Fatal(err)
	}
	in := m.asked()[1].Turns[0].Content
	_, sample, _ := strings.Cut(in, "Its answer, cut:\n\n")
	if len(sample) > sampleBytes || sample == "" {
		t.Fatalf("sample of %d bytes", len(sample))
	}
	checkCut(t, sample)

	// Each step of cutting, down to the last resort.
	for _, raw := range []string{string(big), `[` + strings.Repeat(`{"`+strings.Repeat("k", 300)+`":1},`, 200) + `{}]`,
		strings.Repeat(`{"a":`, 60) + `1` + strings.Repeat(`}`, 60), `{"text":"` + long + `"}`} {
		s := sampleOf(readResult(raw))
		if len(s) > sampleBytes {
			t.Fatalf("sample of %d bytes from %.40s", len(s), raw)
		}
		if json.Valid([]byte(s)) {
			checkCut(t, s)
		}
	}
	if s := sampleOf(readResult("Balance: " + long)); utf8.RuneCountInString(s) != sampleString {
		t.Fatalf("a text answer's start is %d characters", utf8.RuneCountInString(s))
	}
}

// checkCut fails when a list in sample has more than sampleItems entries or a string more than sampleString
// characters.
func checkCut(t *testing.T, sample string) {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(sample), &v); err != nil {
		t.Fatalf("the sample is not JSON: %v", err)
	}
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case string:
			if utf8.RuneCountInString(x) > sampleString {
				t.Fatalf("a string of %d characters", utf8.RuneCountInString(x))
			}
		case []any:
			if len(x) > sampleItems {
				t.Fatalf("a list of %d entries", len(x))
			}
			for _, e := range x {
				walk(e)
			}
		case map[string]any:
			for k, e := range x {
				walk(k)
				walk(e)
			}
		}
	}
	walk(v)
}

// No model set up, or a failed call: codes of their own, so the pages keep the manual path; nothing is called or
// written before the model answers.
func TestGenerateWithoutAModel(t *testing.T) {
	e := newEnv(t)
	_, err := e.svc.Generate(e.ctx, GenerateRequest{Connector: "example", Source: "notices"}, Actor{})
	if se := errOf(t, err); se.Code != CodeNoModel || se.Status != http.StatusServiceUnavailable || e.conn.callCount() != 0 {
		t.Fatalf("no model at all: %+v", se)
	}
	e.withModel("English", ErrNoModel)
	_, err = e.svc.Generate(e.ctx, GenerateRequest{Connector: "example", Source: "notices"}, Actor{})
	if se := errOf(t, err); se.Code != CodeNoModel || e.conn.callCount() != 0 {
		t.Fatalf("nothing to call: %+v", se)
	}
	e.withModel("English", goodPick, errors.New("upstream said 500 with the prompt in it"))
	_, err = e.svc.Generate(e.ctx, GenerateRequest{Connector: "example", Source: "notices"}, Actor{})
	if se := errOf(t, err); se.Code != CodeModelFailed || se.Status != http.StatusBadGateway || strings.Contains(se.Message, "prompt") {
		t.Fatalf("a failed call: %+v", se)
	}
	e.withModel("English", `{"tool":"list_notices"`)
	if _, err = e.svc.Generate(e.ctx, GenerateRequest{Connector: "example", Source: "notices"}, Actor{}); codeOf(err) != CodeModelFailed {
		t.Fatalf("an answer that is not JSON: %v", err)
	}
	for _, c := range []struct {
		in   GenerateRequest
		code string
	}{
		{GenerateRequest{Connector: "example"}, CodeBadRequest},
		{GenerateRequest{Connector: "nowhere", Source: "notices"}, CodeNotFound},
		{GenerateRequest{Connector: "example", Source: "nowhere"}, CodeNotFound},
		{GenerateRequest{ID: "sub_missing"}, CodeNotFound},
	} {
		if _, err := e.svc.Generate(e.ctx, c.in, Actor{}); codeOf(err) != c.code {
			t.Errorf("%+v: %v", c.in, err)
		}
	}
	src := noticesSource()
	src.Tools = src.Tools[1:2] // send_message only
	e.conn.setSources([]connector.CatalogSource{src})
	e.clock.advance(2 * time.Minute)
	if _, err := e.svc.Generate(e.ctx, GenerateRequest{Connector: "example", Source: "notices"}, Actor{}); codeOf(err) != CodeNotReadOnly {
		t.Fatalf("a source with no read-only tool: %v", err)
	}
}

// The note for the user is asked for in the user's language.
func TestGenerateNoteIsInTheUsersLanguage(t *testing.T) {
	e := newEnv(t)
	note := "事務室からの新しいお知らせ。"
	m := e.withModel("日本語", goodPick, strings.Replace(goodMap, "New notices from the office, each titled with its subject.", note, 1))
	out, err := e.svc.Generate(e.ctx, GenerateRequest{Connector: "example", Source: "notices"}, Actor{})
	if err != nil || out.Note != note {
		t.Fatalf("%q %v", out.Note, err)
	}
	if sys := m.asked()[1].System; !strings.Contains(sys, "one or two sentences in 日本語 for the user") {
		t.Fatalf("the map prompt does not ask for the user's language:\n%s", sys)
	}
	if strings.Contains(m.asked()[0].System, "日本語") {
		t.Fatal("the pick asks for a language: it writes nothing for the user")
	}
}

// After the tool changed, regenerating saves the model's recipe on the subscription: needs_recipe clears, and the
// switches, interval, label and rules stay.
func TestRegenerateAfterAToolChange(t *testing.T) {
	e := newEnv(t)
	sub := e.turnOn(e.create(itemsRecipe()))
	interval, rules, tier, label := 1800, "Only exams", "action", "Office notices"
	sub, err := e.svc.Update(e.ctx, sub.ID, Patch{Revision: sub.Revision, IntervalSeconds: &interval, TriageRules: &rules, FallbackTier: &tier, Label: &label}, Actor{})
	if err != nil {
		t.Fatal(err)
	}
	e.svc.RunDue(e.ctx)
	src := noticesSource()
	src.Tools[0].Name = "notices_feed"
	e.conn.setSources([]connector.CatalogSource{src})
	e.clock.advance(time.Hour)
	e.svc.RunDue(e.ctx)
	sub = e.get(sub.ID)
	if sub.State != StateNeedsRecipe {
		t.Fatalf("after the tool changed: %+v", sub)
	}
	stale := sub.Revision - 1
	m := e.withModel("English", strings.Replace(goodPick, "list_notices", "notices_feed", 1), goodMap)
	if _, err := e.svc.Generate(e.ctx, GenerateRequest{ID: sub.ID, Revision: stale}, Actor{}); codeOf(err) != CodeConflict || len(m.asked()) != 0 {
		t.Fatalf("at a revision the client did not see: %v", err)
	}
	e.audit = nil
	out, err := e.svc.Generate(e.ctx, GenerateRequest{ID: sub.ID, Revision: sub.Revision}, Actor{ID: "device:phone"})
	if err != nil {
		t.Fatal(err)
	}
	got := out.Subscription
	if got == nil || got.State != StateOK || got.Error != nil || got.Recipe.Tool != "notices_feed" || got.RecipeOrigin != OriginModel ||
		!got.RecipeAt.Equal(e.clock.Now()) || got.Revision != sub.Revision+1 || got.NextRunAt == nil {
		t.Fatalf("regenerated: %+v", got)
	}
	if !got.ToInbox || got.IntervalSeconds != interval || got.TriageRules != rules || got.FallbackTier != tier || got.Label != label {
		t.Fatalf("the user's settings changed: %+v", got)
	}
	if len(e.audit) != 1 || e.audit[0] != "example subscription_updated device:phone" {
		t.Fatalf("audit %v", e.audit)
	}
	calls := e.conn.callCount()
	e.svc.RunDue(e.ctx)
	if e.conn.callCount() != calls+1 || e.get(sub.ID).State != StateOK {
		t.Fatalf("the regenerated recipe does not run: %+v", e.get(sub.ID))
	}
}

// What drafting and the destinations run on, for the pages' consent: asked of the manager, never a model call.
func TestModelOf(t *testing.T) {
	e := newEnv(t)
	if got := e.svc.RecipeModel(e.ctx); got.OK {
		t.Fatalf("no model and no Models: %+v", got)
	}
	var asked []string
	models := func(_ context.Context, purpose string) ModelInfo {
		asked = append(asked, purpose)
		return ModelInfo{OK: true, Provider: "Example API", Model: "small-model"}
	}
	e.svc = New(Options{Store: e.store, Connectors: e.conns, Clock: e.clock, Models: models})
	if got := e.svc.RecipeModel(e.ctx); got.OK || len(asked) != 0 {
		t.Fatalf("drafting with no Model to call: %+v", got)
	}
	if got := e.svc.ModelOf(e.ctx, "inbox.triage"); !got.OK || got.Provider != "Example API" || asked[0] != "inbox.triage" {
		t.Fatalf("%+v %v", got, asked)
	}
	e.svc = New(Options{Store: e.store, Connectors: e.conns, Clock: e.clock, Models: models, Model: &fakeModel{}})
	if got := e.svc.RecipeModel(e.ctx); !got.OK || got.Model != "small-model" || asked[len(asked)-1] != "subscription.recipe" {
		t.Fatalf("%+v %v", got, asked)
	}
}
