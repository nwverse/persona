package assistant

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/kb"
)

// sortEnv is todoEnv with a knowledge base of three fictional projects: Garden app, Q4 launch (alias "launch plan")
// and Docs site, and Old blog, which is done and so never suggested.
func sortEnv(t *testing.T, database *db.DB) *todoEnv {
	t.Helper()
	f := newFakeHub().withMachine()
	var s *Service
	if database == nil {
		s = newKBService(t, f)
	} else {
		s = newKBServiceOn(t, f, database)
	}
	e := kbEng(t, s)
	for _, p := range []kb.Update{
		{Name: "Garden app", Kind: "long"},
		{Name: "Q4 launch", Kind: "short", Aliases: []string{"launch plan"}},
		{Name: "Docs site", Kind: "long"},
		{Name: "Old blog", Kind: "short", Status: "done"},
	} {
		if _, err := e.Apply(kb.KindProject, p.Name, p); err != nil {
			t.Fatal(err)
		}
	}
	return &todoEnv{t: t, s: s, f: f}
}

func sortStores(t *testing.T, body func(t *testing.T, e *todoEnv)) {
	t.Run("memory", func(t *testing.T) { body(t, sortEnv(t, nil)) })
	t.Run("store", func(t *testing.T) { body(t, sortEnv(t, todoTestDB(t))) })
}

func (e *todoEnv) addTodo(body string) string {
	e.t.Helper()
	var res struct{ Todo Todo }
	_ = json.Unmarshal(e.must("POST", "/api/v1/todos", body).Body.Bytes(), &res)
	if res.Todo.ID == "" {
		e.t.Fatalf("not created: %s", body)
	}
	return res.Todo.ID
}

func (e *todoEnv) suggest() sortResult {
	e.t.Helper()
	var res sortResult
	if err := json.Unmarshal(e.must("POST", "/api/v1/todo-sort/suggest", "").Body.Bytes(), &res); err != nil {
		e.t.Fatal(err)
	}
	return res
}

func sortItemOf(res sortResult, id string) sortItem {
	for _, it := range res.Items {
		if it.Todo == id {
			return it
		}
	}
	return sortItem{}
}

// The suggestions: a title naming a project is the rule's; the model places the rest, confirms a rule or marks a
// one-off Temporary; unknown projects from the model and done projects are dropped; filed and Temporary to-dos are
// not offered; the sure ones come first with their tick, the unplaced last.
func TestTodoSortSuggest(t *testing.T) {
	sortStores(t, func(t *testing.T, e *todoEnv) {
		garden := e.addTodo(`{"title":"Water the Garden app seedlings"}`)
		milk := e.addTodo(`{"title":"Buy milk"}`)
		notes := e.addTodo(`{"title":"Write release notes","notes":"for the launch"}`)
		mystery := e.addTodo(`{"title":"Think about it"}`)
		blog := e.addTodo(`{"title":"Old blog cleanup"}`)
		e.addTodo(`{"title":"Already filed","project":"docs-site"}`)
		temp := e.addTodo(`{"title":"Call the plumber"}`)
		e.must("PATCH", "/api/v1/todos/"+temp, `{"temporary":true}`)
		e.f.mu.Lock()
		e.f.sortReply = `{"items":[` +
			`{"todo":"` + milk + `","project":"","temporary":true,"confidence":0.9,"reason":"A one-off errand"},` +
			`{"todo":"` + notes + `","project":"q4-launch","confidence":0.7,"reason":"Notes mention the launch"},` +
			`{"todo":"` + garden + `","project":"garden-app","confidence":0.95,"reason":"About the app's plants"},` +
			`{"todo":"` + mystery + `","project":"no-such-project","confidence":0.9,"reason":"made up"},` +
			`{"todo":"` + blog + `","project":"old-blog","confidence":0.9,"reason":"done project"}]}`
		e.f.mu.Unlock()

		res := e.suggest()
		if res.Model != "used" || res.Unsorted != 5 || res.Sorted != 1 || len(res.Items) != 5 {
			t.Fatalf("result: %+v", res)
		}
		if g := sortItemOf(res, garden); g.Project != "garden-app" || g.ProjectName != "Garden app" || g.Confidence != 0.95 || !g.Preselect ||
			g.By != "title" || g.Reason != "About the app's plants" {
			t.Fatalf("garden: %+v", g)
		}
		if m := sortItemOf(res, milk); !m.Temporary || m.Project != "" || !m.Preselect || m.By != "model" {
			t.Fatalf("milk: %+v", m)
		}
		if n := sortItemOf(res, notes); n.Project != "q4-launch" || n.Confidence != 0.7 || !n.Preselect {
			t.Fatalf("notes: %+v", n)
		}
		for _, id := range []string{mystery, blog} {
			if x := sortItemOf(res, id); x.Project != "" || x.Temporary || x.Preselect || x.Confidence != 0 {
				t.Fatalf("an invented or done project was kept: %+v", x)
			}
		}
		if res.Items[0].Todo != garden || res.Items[len(res.Items)-1].Project != "" {
			t.Fatalf("order: %+v", res.Items)
		}
		for _, p := range res.Projects {
			if p.Slug == "old-blog" {
				t.Fatalf("a done project is a target: %+v", res.Projects)
			}
		}
		e.f.mu.Lock()
		asked := e.f.sortAsked
		e.f.mu.Unlock()
		if len(asked) != 1 || !strings.Contains(asked[0].User, "Write release notes") || !strings.Contains(asked[0].User, "for the launch") ||
			strings.Contains(asked[0].User, "Already filed") || strings.Contains(asked[0].User, "Call the plumber") || strings.Contains(asked[0].User, "old-blog") {
			t.Fatalf("asked: %+v", asked)
		}
	})
}

// With no model, the rules alone stand (and say so): a title, or an alias in it.
func TestTodoSortSuggestWithoutModel(t *testing.T) {
	e := sortEnv(t, nil)
	byName := e.addTodo(`{"title":"Fix the Docs site search"}`)
	byAlias := e.addTodo(`{"title":"Draft the launch plan"}`)
	none := e.addTodo(`{"title":"Something else"}`)
	res := e.suggest() // sortReply "": the model call fails
	if res.Model != "failed" && res.Model != "unavailable" {
		t.Fatalf("model: %q", res.Model)
	}
	if it := sortItemOf(res, byName); it.Project != "docs-site" || it.By != "title" || it.Confidence != sureByTitle || !it.Preselect ||
		!strings.Contains(it.Reason, "Docs site") {
		t.Fatalf("by name: %+v", it)
	}
	if it := sortItemOf(res, byAlias); it.Project != "q4-launch" {
		t.Fatalf("by alias: %+v", it)
	}
	if it := sortItemOf(res, none); it.Project != "" || it.Preselect {
		t.Fatalf("none: %+v", it)
	}
}

// An Agent working in a folder named for a project (or in a project's own folders) places its to-do there, and a
// sure rule is not moved by the model's other guess.
func TestTodoSortRulesFolders(t *testing.T) {
	e := sortEnv(t, nil)
	eng := kbEng(t, e.s)
	byslug := map[string]*kb.Project{}
	for _, p := range sortCandidates(eng) {
		byslug[p.Slug] = p
	}
	todo := Todo{ID: "todo_x", Title: "Fix the build", Agents: []TodoAgent{{MachineID: "server", Cwd: "/Users/someone/src/docs-site"}}}
	var it sortItem
	e.s.sortRules(context.Background(), eng, byslug, todo, &it, func(key string, a i18n.Args) string {
		return i18n.T(i18n.En, "assistant-todos.sort_reason_"+key, a)
	})
	if it.Project != "docs-site" || it.By != "dirs" || it.Confidence != sureByDirs || it.Reason != "Its Agent works in ~/src/docs-site, a folder of Docs site" {
		t.Fatalf("dirs: %+v", it)
	}
	items := []sortItem{it}
	mergeSortReply(items, sortReply{Items: []struct {
		Todo       string  `json:"todo"`
		Project    string  `json:"project"`
		Temporary  bool    `json:"temporary"`
		Confidence float64 `json:"confidence"`
		Reason     string  `json:"reason"`
	}{{Todo: "todo_x", Project: "garden-app", Confidence: 0.9, Reason: "other"}}}, byslug)
	if items[0].Project != "docs-site" {
		t.Fatalf("a sure rule was moved: %+v", items[0])
	}
}

// Temporary and a project exclude each other: marking one clears the other, either way.
func TestTodoTemporaryMark(t *testing.T) {
	projectStores(t, func(t *testing.T, e *todoEnv) {
		id := e.addTodo(`{"title":"Pick up the parcel","project":"社群运营"}`)
		e.must("PATCH", "/api/v1/todos/"+id, `{"temporary":true}`)
		if got, _ := e.todo(id); !got.Temporary || got.Project != "" {
			t.Fatalf("marked: %+v", got)
		}
		e.must("PATCH", "/api/v1/todos/"+id, `{"title":"Pick up the parcel today"}`)
		if got, _ := e.todo(id); !got.Temporary {
			t.Fatalf("another edit cleared it: %+v", got)
		}
		e.must("PATCH", "/api/v1/todos/"+id, `{"project":"诊断能力"}`)
		if got, _ := e.todo(id); got.Temporary || got.Project != "诊断能力" {
			t.Fatalf("filed: %+v", got)
		}
		var raw map[string]any
		other := e.addTodo(`{"title":"Plain"}`)
		var res struct{ Todos []map[string]any }
		_ = json.Unmarshal(e.must("GET", "/api/v1/todos", "").Body.Bytes(), &res)
		for _, td := range res.Todos {
			if td["id"] == other {
				raw = td
			}
		}
		if _, ok := raw["temporary"]; ok || raw == nil {
			t.Fatalf("a plain to-do carries temporary: %v", raw)
		}
	})
}

// Apply writes the picks as one batch; undo puts back what is still as the batch left it and names what changed
// since; a batch is undone once; a refused item writes nothing.
func TestTodoSortApplyUndo(t *testing.T) {
	sortStores(t, func(t *testing.T, e *todoEnv) {
		a := e.addTodo(`{"title":"Plant tomatoes"}`)
		b := e.addTodo(`{"title":"Buy milk"}`)
		c := e.addTodo(`{"title":"Proofread guide"}`)
		d := e.addTodo(`{"title":"Filed already","project":"docs-site"}`)

		if w := e.do("POST", "/api/v1/todo-sort/apply", `{"items":[{"id":"`+a+`","project":"Garden app"},{"id":"`+b+`","project":"nowhere"}]}`); w.Code != http.StatusBadRequest ||
			!strings.Contains(w.Body.String(), "item 2") {
			t.Fatalf("unknown project: %d %s", w.Code, w.Body)
		}
		if got, _ := e.todo(a); got.Project != "" {
			t.Fatalf("a refused batch moved one: %+v", got)
		}

		var applied sortApplied
		_ = json.Unmarshal(e.must("POST", "/api/v1/todo-sort/apply", `{"items":[{"id":"`+a+`","project":"Garden app"},{"id":"`+b+`","temporary":true},`+
			`{"id":"`+c+`","project":"docs-site"},{"id":"`+d+`","project":"docs-site"}]}`).Body.Bytes(), &applied)
		if applied.Batch == "" || applied.Moved != 3 || applied.Unchanged != 1 {
			t.Fatalf("applied: %+v", applied)
		}
		if got, _ := e.todo(a); got.Project != "garden-app" {
			t.Fatalf("a: %+v", got)
		}
		if got, _ := e.todo(b); !got.Temporary {
			t.Fatalf("b: %+v", got)
		}
		e.must("PATCH", "/api/v1/todos/"+c, `{"project":"q4-launch"}`) // the owner moved it since

		var list struct{ Batches []sortBatchView }
		_ = json.Unmarshal(e.must("GET", "/api/v1/todo-sort/batches", "").Body.Bytes(), &list)
		if len(list.Batches) != 1 || len(list.Batches[0].Items) != 3 || list.Batches[0].Titles[a] != "Plant tomatoes" || list.Batches[0].Kind != sortKindSort {
			t.Fatalf("batches: %+v", list)
		}

		var undone sortUndone
		_ = json.Unmarshal(e.must("POST", "/api/v1/todo-sort/batches/"+applied.Batch+"/undo", "").Body.Bytes(), &undone)
		if undone.Undone != 2 || undone.Skipped != 1 {
			t.Fatalf("undone: %+v", undone)
		}
		for _, o := range undone.Outcomes {
			if (o.Todo == c) != (o.Outcome == undoChanged) {
				t.Fatalf("outcomes: %+v", undone.Outcomes)
			}
		}
		if got, _ := e.todo(a); got.Project != "" || got.Temporary {
			t.Fatalf("a not back: %+v", got)
		}
		if got, _ := e.todo(b); got.Temporary {
			t.Fatalf("b not back: %+v", got)
		}
		if got, _ := e.todo(c); got.Project != "q4-launch" {
			t.Fatalf("the owner's later move was undone: %+v", got)
		}
		if w := e.do("POST", "/api/v1/todo-sort/batches/"+applied.Batch+"/undo", ""); w.Code != http.StatusConflict {
			t.Fatalf("twice: %d %s", w.Code, w.Body)
		}
		if w := e.do("POST", "/api/v1/todo-sort/batches/tsb_nope/undo", ""); w.Code != http.StatusNotFound {
			t.Fatalf("unknown: %d %s", w.Code, w.Body)
		}
		if w := e.do("POST", "/api/v1/todo-sort/apply", `{"items":[]}`); w.Code != http.StatusBadRequest {
			t.Fatalf("empty: %d", w.Code)
		}
	})
}

// The assistant's todo_sort: suggest, apply (an undoable batch, also a move asked for in chat), and undo by batch or
// a list of batches.
func TestTodoSortTool(t *testing.T) {
	e := sortEnv(t, nil)
	a := e.addTodo(`{"title":"Water the Garden app seedlings"}`)
	b := e.addTodo(`{"title":"Pay the invoice"}`)

	out, err := e.tool("todo_sort", map[string]any{"action": "suggest"})
	if err != nil || !strings.Contains(out, a) || !strings.Contains(out, "Garden app (garden-app)") || !strings.Contains(out, "no suggestion") ||
		!strings.Contains(out, "todo_sort apply") {
		t.Fatalf("suggest: %v %s", err, out)
	}
	out, err = e.tool("todo_sort", map[string]any{"action": "apply", "items": []map[string]any{{"id": a, "project": "Docs site"}, {"id": b, "project": "docs-site"}}})
	if err != nil || !strings.Contains(out, "Moved 2") {
		t.Fatalf("apply: %v %s", err, out)
	}
	batch := out[strings.Index(out, "tsb_"):][:len("tsb_")+12]
	if got, _ := e.todo(b); got.Project != "docs-site" {
		t.Fatalf("moved: %+v", got)
	}
	if out, err = e.tool("todo_sort", map[string]any{"action": "undo"}); err != nil || !strings.Contains(out, batch) {
		t.Fatalf("list: %v %s", err, out)
	}
	if out, err = e.tool("todo_sort", map[string]any{"action": "undo", "batch": batch}); err != nil || !strings.Contains(out, "2 put back") {
		t.Fatalf("undo: %v %s", err, out)
	}
	if got, _ := e.todo(b); got.Project != "" {
		t.Fatalf("not back: %+v", got)
	}
	if out, err = e.tool("todo_sort", map[string]any{"action": "apply", "items": []map[string]any{{"id": b, "temporary": true}}}); err != nil || !strings.Contains(out, "Moved 1") {
		t.Fatalf("temporary: %v %s", err, out)
	}
	if _, err = e.tool("todo_sort", map[string]any{"action": "apply", "items": []map[string]any{{"id": "todo_none", "project": "Docs site"}}}); err == nil {
		t.Fatal("an unknown to-do moved")
	}
	if _, err = e.tool("todo_sort", map[string]any{"action": "move"}); err == nil {
		t.Fatal("an unknown action ran")
	}
}

// The sort's prompt in the owner's language (zh-Hans stays the default text), with the projects and the to-dos as the
// model gets them.
func TestGoldenTodoSortLanguages(t *testing.T) {
	for _, locale := range []string{"zh-Hans", "en", "ja"} {
		t.Run(locale, func(t *testing.T) {
			e := sortEnv(t, nil)
			e.f.mu.Lock()
			e.f.hello.Locale = locale
			e.f.hello.Owner = "Example user"
			e.f.sortReply = `{"items":[]}`
			e.f.mu.Unlock()
			id := e.addTodo(`{"title":"Write release notes","notes":"for the launch"}`)
			if res := e.suggest(); res.Model != "used" {
				t.Fatalf("model: %q", res.Model)
			}
			e.f.mu.Lock()
			asked := e.f.sortAsked
			e.f.mu.Unlock()
			golden(t, "todo_sort."+locale, strings.ReplaceAll(modelPrompt(asked[0]), id, "todo_example"))
		})
	}
}

// A project's name counts in a title only as a whole word; Chinese and Japanese need no spaces around it.
func TestTodoSortWholeWord(t *testing.T) {
	cases := []struct {
		text, word string
		want       bool
	}{
		{"fix ranking bug", "ranking", true},
		{"reranking", "ranking", false},
		{"ranking-v2", "ranking", true},
		{"rankings", "ranking", false},
		{"rankings and ranking", "ranking", true},
		{"build the happy hour list", "app", false},
		{"修复排序服务的缓存", "排序服务", true},      // i18n-ignore
		{"ranking: 修复", "ranking", true}, // i18n-ignore
	}
	for _, c := range cases {
		if got := wholeWord(c.text, c.word); got != c.want {
			t.Errorf("wholeWord(%q, %q) = %v", c.text, c.word, got)
		}
	}
	byslug := map[string]*kb.Project{"ui": {Slug: "ui", Name: "UI"}, "docs-site": {Slug: "docs-site", Name: "Docs site"}}
	if got := titleNames("Rebuild the build script", byslug, nil); got != "" {
		t.Fatalf("UI matched inside build: %q", got)
	}
	if got := titleNames("Polish the UI colors", byslug, nil); got != "ui" {
		t.Fatalf("UI as a word: %q", got)
	}
}

// A Windows Agent's work folder as it reports it (C:\Users\...) is in the project whose folder the knowledge base
// keeps in the hub's canonical form (c:/Users/...).
func TestTodoSortRulesWindowsFolder(t *testing.T) {
	e := sortEnv(t, nil)
	eng := kbEng(t, e.s)
	if _, err := eng.Apply(kb.KindProject, "Win app", kb.Update{Name: "Win app", Kind: "long",
		Dirs: []string{"laptop:" + workspaceDirectory(`C:\Users\me\winapp`)}}); err != nil {
		t.Fatal(err)
	}
	byslug := map[string]*kb.Project{}
	for _, p := range sortCandidates(eng) {
		byslug[p.Slug] = p
	}
	todo := Todo{ID: "todo_w", Title: "Fix the build", Agents: []TodoAgent{{MachineID: "laptop", Cwd: `C:\Users\me\winapp\src`}}}
	var it sortItem
	e.s.sortRules(context.Background(), eng, byslug, todo, &it, func(key string, a i18n.Args) string {
		return i18n.T(i18n.En, "assistant-todos.sort_reason_"+key, a)
	})
	if it.By != "dirs" || byslug[it.Project] == nil || byslug[it.Project].Name != "Win app" {
		t.Fatalf("a Windows Agent in its project's folder: %+v", it)
	}
}
