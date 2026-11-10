package subscriptionsui

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/subscriptions"
)

// draftEnv is env with drafting on: a manager's drafts whose finishing is signalled on done.
func draftEnv(lang i18n.Lang) (Env, chan struct{}) {
	done := make(chan struct{}, 4)
	e := env(lang, true)
	e.Drafts = NewDrafts(func() { done <- struct{}{} })
	return e, done
}

func wait(t *testing.T, done chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the draft never finished")
	}
}

var calendarDraft = subscriptions.Recipe{Mode: "items", Tool: "list_events", Args: json.RawMessage(`{"calendar":"work","updated_min":"{{since}}"}`),
	ItemsPath: "$.items", Kind: "sub_event", GroupBy: "source",
	Fields: &wire.ConnectorSubscriptionFields{Title: "$.summary", Time: "$.start.dateTime", ID: "$.id"}}

// Without the model stage, or without a model, the press is not offered (or says why); with one it asks first, naming the
// model's connection.
func TestDraftIsOfferedWithAModel(t *testing.T) {
	f := newFake()
	e, _ := draftEnv(i18n.ZhHans)
	if item(draw(t, f, e, state{P: pageSource, C: "apps", S: "gmail"}), "g.draft") != nil {
		t.Error("offered before #2040 is in")
	}
	f.generate = true
	r := item(draw(t, f, e, state{P: pageSource, C: "apps", S: "gmail"}), "g.draft")
	if r == nil || r.Action != nil || r.Subtitle == "" {
		t.Errorf("no model: the row says so and does nothing: %+v", r)
	}
	f.model = subscriptions.ModelInfo{OK: true, Provider: "Example Cloud", Model: "large"}
	r = item(draw(t, f, e, state{P: pageSource, C: "apps", S: "gmail"}), "g.draft")
	if r == nil || r.Action == nil || r.Action.ID != "draft" || !strings.Contains(r.Action.Confirm, "Example Cloud · large") {
		t.Fatalf("the press asks first, naming the model: %+v", r)
	}
	if item(draw(t, f, e, state{P: pageSource, C: "apps", S: "calendar"}), "g.draft") != nil {
		t.Error("offered for a source whose extension brings its own recipe")
	}
}

// A draft for a source runs in the background (the page shows it and draws again); done, it offers its form, which starts
// from the draft; saved unchanged it is the model's, changed it is the user's.
func TestDraftASource(t *testing.T) {
	f := newFake()
	f.generate, f.model = true, subscriptions.ModelInfo{OK: true, Provider: "Example Cloud"}
	f.release = make(chan struct{})
	f.gen = func(subscriptions.GenerateRequest) (subscriptions.Generated, error) {
		return subscriptions.Generated{Recipe: calendarDraft, Note: "Lists the work calendar's changed events.", DryRun: f.dry}, nil
	}
	f.dry = subscriptions.DryRun{OK: true, ItemsPath: "$.items", Count: 1, Shape: &subscriptions.Shape{Type: "json"},
		Items: []subscriptions.Preview{{UID: "e1", New: true, Title: "Lab meeting"}}, Paths: []subscriptions.PathValue{{Path: "$.summary", Example: "Lab meeting"}}}
	e, done := draftEnv(i18n.En)
	st := state{P: pageSource, C: "apps", S: "gmail"}
	src := f.cats[0].Sources[2]
	src.Tools = append(src.Tools, subscriptions.CatalogTool{Name: "list_events", Subscribable: true, InputSchema: f.cats[0].Sources[1].Tools[0].InputSchema})
	f.cats[0].Sources[2] = src
	res := handle(t, f, e, st, "draft", nil)
	if r := item(*res.View, "g.running"); r == nil || res.View.RefreshAfter < 30 {
		t.Fatalf("running: %+v, refresh %d", r, res.View.RefreshAfter)
	}
	if again := handle(t, f, e, state{P: pageSource, C: "apps", S: "calendar"}, "draft", nil); again.Toast == "" {
		t.Error("a second draft on the same connector should wait")
	}
	close(f.release)
	wait(t, done)
	if len(f.genAsks) != 1 || f.genAsks[0].Connector != "apps" || f.genAsks[0].Source != "gmail" || f.genAsks[0].ID != "" {
		t.Fatalf("asked: %+v", f.genAsks)
	}
	v := draw(t, f, e, st)
	if r := item(v, "g.note"); item(v, "g.done") == nil || r == nil || r.Text != "Lists the work calendar's changed events." {
		t.Errorf("done, with the model's note: %+v", r)
	}
	review := item(v, "g.review")
	if review == nil || review.Action == nil || review.Action.Push == nil {
		t.Fatalf("no way to the draft's form: %+v", review)
	}
	setup := parseState(review.Action.Push.State)
	if !setup.G || setup.T != "list_events" {
		t.Fatalf("setup state: %+v", setup)
	}
	v = draw(t, f, e, setup)
	if c := item(v, "a.calendar"); c == nil || c.Value != "work" {
		t.Errorf("the form starts from the draft's arguments: %+v", c)
	}
	if c := item(v, "m.title"); c == nil || c.Value != "$.summary" {
		t.Errorf("and its fields: %+v", c)
	}
	if item(v, "note") == nil || section(v, "dry") == nil {
		t.Error("the note and the draft's dry run are missing")
	}
	form := map[string]string{"arg.calendar": "work", "arg.updated_min": "{{since}}", "f.title": "$.summary", "f.time": "$.start.dateTime",
		"f.id": "$.id", "kind": "sub_event", "group_by": "source", "mode": "items"}
	setup.I = "$.items"
	handle(t, f, e, setup, "save", form)
	if len(f.created) != 1 || f.created[0].RecipeOrigin != subscriptions.OriginModel {
		t.Fatalf("saved unchanged, the model's: %+v", f.created)
	}
	if _, kept := e.Drafts.get(sourceKey("apps", "gmail")); kept {
		t.Error("a saved draft is kept")
	}
	// Changed before saving: the user's.
	f.release = make(chan struct{})
	f.created, f.subs = nil, newFake().subs
	handle(t, f, e, st, "draft", nil)
	close(f.release)
	wait(t, done)
	form["f.title"] = "$.description"
	handle(t, f, e, setup, "save", form)
	if len(f.created) != 1 || f.created[0].RecipeOrigin != "" {
		t.Fatalf("saved changed, the user's: %+v", f.created)
	}
}

// A draft refused at choosing its tool brings only the tool and its arguments: the form starts there with defaults, and
// draws no empty dry run.
func TestDraftRefusedHalfway(t *testing.T) {
	f := newFake()
	f.generate, f.model = true, subscriptions.ModelInfo{OK: true, Provider: "Example Cloud"}
	f.gen = func(subscriptions.GenerateRequest) (subscriptions.Generated, error) {
		return subscriptions.Generated{}, &subscriptions.Error{Status: http.StatusUnprocessableEntity, Code: subscriptions.CodeBadArgs,
			Draft: &subscriptions.Recipe{Tool: "list_events", Args: json.RawMessage(`{"calendar":"family"}`)}}
	}
	src := f.cats[0].Sources[2]
	src.Tools = append(src.Tools, subscriptions.CatalogTool{Name: "list_events", Subscribable: true, InputSchema: f.cats[0].Sources[1].Tools[0].InputSchema})
	f.cats[0].Sources[2] = src
	e, done := draftEnv(i18n.ZhHans)
	handle(t, f, e, state{P: pageSource, C: "apps", S: "gmail"}, "draft", nil)
	wait(t, done)
	v := draw(t, f, e, state{P: pageSource, C: "apps", S: "gmail"})
	if r := item(v, "g.failed"); r == nil || r.Subtitle == "" {
		t.Errorf("why it failed: %+v", r)
	}
	goOn := item(v, "g.continue")
	if goOn == nil || item(v, "g.draft") == nil {
		t.Fatal("no way on from the half draft, or to try again")
	}
	v = draw(t, f, e, parseState(goOn.Action.Push.State))
	if c := item(v, "a.calendar"); c == nil || c.Value != "family" {
		t.Errorf("the half draft's arguments: %+v", c)
	}
	if c := item(v, "k.kind"); c == nil || c.Value != "sub_update" {
		t.Errorf("defaults where it stopped: %+v", c)
	}
	if section(v, "dry") != nil {
		t.Error("an empty dry run is drawn")
	}
}

// Regenerating a subscription saves when it passes; the recipe page then shows what its check pulled.
func TestRegenerate(t *testing.T) {
	f := newFake()
	f.generate, f.model = true, subscriptions.ModelInfo{OK: true, Provider: "Example Cloud"}
	saved := f.subs["sub_slack"]
	f.gen = func(in subscriptions.GenerateRequest) (subscriptions.Generated, error) {
		s := saved
		s.RecipeOrigin, s.State = subscriptions.OriginModel, subscriptions.StateOK
		f.subs[in.ID] = s
		return subscriptions.Generated{Recipe: s.Recipe, Note: "Reads the channel again.", Subscription: &s,
			DryRun: subscriptions.DryRun{OK: true, Items: []subscriptions.Preview{{UID: "m1", Title: "Hello"}}}}, nil
	}
	f.release = make(chan struct{}) // held until the press has answered: the fake is not safe to share
	e, done := draftEnv(i18n.En)
	v := draw(t, f, e, state{P: pageRecipe, ID: "sub_slack"})
	press := item(v, "g.draft")
	if press == nil || press.Action == nil || press.Action.ID != "regenerate" {
		t.Fatalf("no regenerate: %+v", press)
	}
	handle(t, f, e, state{P: pageRecipe, ID: "sub_slack"}, "regenerate", nil)
	close(f.release)
	wait(t, done)
	if len(f.genAsks) != 1 || f.genAsks[0].ID != "sub_slack" || f.genAsks[0].Revision != saved.Revision {
		t.Fatalf("asked: %+v", f.genAsks)
	}
	v = draw(t, f, e, state{P: pageRecipe, ID: "sub_slack"})
	if item(v, "g.done") == nil || item(v, "dry.0") == nil {
		t.Error("the regenerated recipe's note and pulled items are missing")
	}
}

// The turn-on confirmations name the model's connection that triages.
func TestConfirmNamesTheTriageModel(t *testing.T) {
	f := newFake()
	s := f.subs["sub_outlook"]
	s.ToInbox = false
	f.subs["sub_outlook"] = s
	v := draw(t, f, env(i18n.En, true), state{P: pageSource, ID: "sub_outlook"})
	if c := item(v, "d.to_inbox").Control; !strings.Contains(c.Action.Confirm, "Example Cloud · small") {
		t.Errorf("confirmation: %q", c.Action.Confirm)
	}
}
