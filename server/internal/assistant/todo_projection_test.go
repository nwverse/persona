package assistant

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistant/todo/projection"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/kb"
	"github.com/nwverse/persona/server/internal/proto"
)

// The To-dos view's adapter (todo_projection.go, #10226): the facts it hands the projection are the list's row, so the
// shared cases (which decode rows of GET /api/v1/todos) say what the manager answers.

// projectionNotListedYet are the Facts fields GET /api/v1/todos does not carry yet: the restores PR (#10226) adds both
// to Todo. Once it has, this test reports them: copy them in projectionFacts (its HOOK line) and drop them here.
var projectionNotListedYet = map[string]bool{"RestoredAt": true, "SomedayUntil": true}

// Every field of a listed row, filled, reads the same through the adapter as through the row's JSON, and every JSON
// field of projection.Facts is one the row writes: a field added to either side without the other fails here.
func TestProjectionFactsMatchTheList(t *testing.T) {
	var row listedTodo
	n := 0
	fillForTest(reflect.ValueOf(&row).Elem(), 0, &n)
	raw, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	var decoded projection.Facts
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if got := projectionFacts(row, nil, nil); !reflect.DeepEqual(got, decoded) {
		gotJSON, _ := json.Marshal(got)
		wantJSON, _ := json.Marshal(decoded)
		t.Fatalf("the adapter and the row's JSON differ:\n adapter %s\n json    %s", gotJSON, wantJSON)
	}
	ft, fv := reflect.TypeOf(decoded), reflect.ValueOf(decoded)
	for i := range ft.NumField() {
		name, tag := ft.Field(i).Name, ft.Field(i).Tag.Get("json")
		if tag == "-" {
			continue
		}
		listed := !fv.Field(i).IsZero()
		switch {
		case projectionNotListedYet[name] && listed:
			t.Errorf("GET /api/v1/todos carries %s now: copy it in projectionFacts (its HOOK line) and drop it from "+
				"projectionNotListedYet", name)
		case !projectionNotListedYet[name] && !listed:
			t.Errorf("projection.Facts.%s (%q) is not a field of the listed row", name, strings.Split(tag, ",")[0])
		}
	}
}

// fillForTest gives every exported field v reaches a value that is not zero (one element for a slice or map), at most
// eight levels deep; n makes each time and number its own.
func fillForTest(v reflect.Value, depth int, n *int) {
	if depth > 8 {
		return
	}
	*n++
	switch {
	case v.Type() == reflect.TypeFor[time.Time]():
		v.Set(reflect.ValueOf(time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC).Add(time.Duration(*n) * time.Minute)))
		return
	case v.Type() == reflect.TypeFor[json.RawMessage]():
		v.SetBytes([]byte(`{}`))
		return
	}
	switch v.Kind() {
	case reflect.String:
		v.SetString("v" + strings.Repeat("x", *n%3+1))
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(int64(*n%100 + 1))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(uint64(*n%100 + 1))
	case reflect.Float32, reflect.Float64:
		v.SetFloat(float64(*n) + 0.5)
	case reflect.Pointer:
		p := reflect.New(v.Type().Elem())
		fillForTest(p.Elem(), depth+1, n)
		v.Set(p)
	case reflect.Slice:
		s := reflect.MakeSlice(v.Type(), 1, 1)
		fillForTest(s.Index(0), depth+1, n)
		v.Set(s)
	case reflect.Map:
		m := reflect.MakeMap(v.Type())
		k, e := reflect.New(v.Type().Key()).Elem(), reflect.New(v.Type().Elem()).Elem()
		fillForTest(k, depth+1, n)
		fillForTest(e, depth+1, n)
		m.SetMapIndex(k, e)
		v.Set(m)
	case reflect.Struct:
		for i := range v.NumField() {
			if v.Type().Field(i).IsExported() {
				fillForTest(v.Field(i), depth+1, n)
			}
		}
	}
}

// The adapter on the manager's own list: a plain to-do, one a run works on and one just closed, with the knowledge
// base's title on their project's place; and the same rows decoded from GET /api/v1/todos read the same views.
func TestProjectionOfTheManagersList(t *testing.T) {
	f := newFakeHub().withMachine()
	s := newKBService(t, f)
	if _, err := kbEng(t, s).Apply(kb.KindProject, "Garden", kb.Update{Name: "Garden", Kind: "long"}); err != nil {
		t.Fatal(err)
	}
	e := &todoEnv{t: t, s: s, f: f}
	began := time.Now().UTC().Add(-time.Minute)
	e.f.setSessions("server", proto.Session{MachineID: "server", SessionID: "s1", Title: "Hedge trimming",
		Status: proto.StatusWorking, State: proto.StateWorking, StateSince: began, UpdatedAt: began,
		Activity: "measuring the hedge"})
	var created struct{ Todo Todo }
	_ = json.Unmarshal(e.must("POST", "/api/v1/todos", `{"title":"Water the roses","project":"Garden"}`).Body.Bytes(), &created)
	plain := created.Todo
	if plain.Project == "" {
		t.Fatal("the to-do got no project")
	}
	worked, closed := e.create("Prune the hedge"), e.create("Order compost")
	e.must("PUT", "/api/v1/todos/"+worked.ID+"/agents/server/s1", "")
	e.must("PATCH", "/api/v1/todos/"+closed.ID, `{"done":true}`)

	body := e.must("GET", "/api/v1/todos", "").Body.Bytes()
	var listed struct{ Todos []listedTodo }
	if err := json.Unmarshal(body, &listed); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	got := e.s.todoProjectionOf(context.Background(), listed.Todos, now)

	want := map[string][3]string{
		plain.ID:  {assistantwire.TodoPhaseTodo, assistantwire.TodoListSectionTodo, assistantwire.TodoReasonRest},
		worked.ID: {assistantwire.TodoPhaseWorking, assistantwire.TodoListSectionWorking, assistantwire.TodoReasonAgentRunning},
		closed.ID: {assistantwire.TodoPhaseDone, assistantwire.TodoListSectionJustFinished, assistantwire.TodoReasonClosedUnseen},
	}
	for id, w := range want {
		v := got.Views[id]
		if [3]string{v.Phase, v.Section, v.Reason} != w {
			t.Errorf("%s: %s · %s · %s, want %v", id, v.Phase, v.Section, v.Reason, w)
		}
	}
	if l := got.Views[worked.ID].Line; l == nil || l.Key != "working" || l.Args["run"] != "Hedge trimming" ||
		l.Args["text"] != "measuring the hedge" {
		t.Errorf("the working run's line: %+v", l)
	}
	if c := got.Counts; c.Open != 2 || c.InProgress != 1 || c.NeedsYou != 0 || c.Sections["just_finished"] != 1 {
		t.Errorf("counts %+v", c)
	}
	places := got.Places.Places
	if len(places) != 4 || places[3].ID != "project:"+plain.Project || places[3].Title != "Garden" {
		t.Errorf("places %+v", places)
	}

	// The rows decode straight into projection.Facts, as the shared cases do.
	var rows struct{ Todos []projection.Facts }
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatal(err)
	}
	sessions, _ := e.s.sessions(context.Background(), "server")
	for i := range rows.Todos {
		if rows.Todos[i].ID == worked.ID {
			rows.Todos[i].Sessions = sessions
		}
		rows.Todos[i].Loc = e.s.location(context.Background())
	}
	views := projection.Views(rows.Todos, now)
	for i, r := range rows.Todos {
		if !reflect.DeepEqual(views[i], got.Views[r.ID]) {
			t.Errorf("%s from the JSON: %+v, from the adapter %+v", r.ID, views[i], got.Views[r.ID])
		}
	}
}
