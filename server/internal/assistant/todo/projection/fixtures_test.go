package projection

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/jsonsubset"
	"github.com/nwverse/persona/server/internal/proto"
)

// The shared To-dos view cases (spec/fixtures/todo-view and todo-counts, #10226): the projection answers every one
// exactly, compared as JSON values, and what it writes holds to schema/wire/todo-view.schema.json.

const fixtures = "../../../../../spec/fixtures/"

// viewInput is a case's input: one to-do as the list carries it, its runs' sessions, and what the row does not carry.
type viewInput struct {
	Now          time.Time         `json:"now"`
	Todo         json.RawMessage   `json:"todo"`
	Sessions     []proto.Session   `json:"sessions"`
	RestoredAt   *time.Time        `json:"restored_at"`
	SomedayUntil *time.Time        `json:"someday_until"`
	Loc          string            `json:"loc"`
	Steps        []json.RawMessage `json:"steps"`
	Superseded   bool              `json:"superseded"`
}

// countsInput is a list case's input: the list as GET /api/v1/todos carries it, and the marks by to-do id.
type countsInput struct {
	Now          time.Time            `json:"now"`
	Todos        []json.RawMessage    `json:"todos"`
	Sessions     []proto.Session      `json:"sessions"`
	RestoredAt   map[string]time.Time `json:"restored_at"`
	SomedayUntil map[string]time.Time `json:"someday_until"`
	Loc          string               `json:"loc"`
	Projects     map[string]string    `json:"projects"`
}

type fixtureCase[I any] struct {
	Name     string `json:"name"`
	Input    I      `json:"input"`
	Expected struct {
		View   json.RawMessage     `json:"view"`
		Counts json.RawMessage     `json:"counts"`
		Places json.RawMessage     `json:"places"`
		Order  map[string][]string `json:"order"`
	} `json:"expected"`
}

func readCases[I any](t *testing.T, file string) []fixtureCase[I] {
	t.Helper()
	data, err := os.ReadFile(fixtures + file)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Version int              `json:"version"`
		Cases   []fixtureCase[I] `json:"cases"`
	}
	if err := json.Unmarshal(data, &doc); err != nil || doc.Version != 1 || len(doc.Cases) == 0 {
		t.Fatalf("%s: %v (version %d, %d cases)", file, err, doc.Version, len(doc.Cases))
	}
	return doc.Cases
}

// location is the case's zone; UTC when it names none.
func location(t *testing.T, name string) *time.Location {
	t.Helper()
	if name == "" {
		return nil
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

// decodeFacts reads a listed row into Facts, refusing nothing: a row carries many fields the rules do not read.
func decodeFacts(t *testing.T, raw json.RawMessage, sessions []proto.Session, loc *time.Location) Facts {
	t.Helper()
	var f Facts
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	f.Sessions, f.Loc = sessions, loc
	return f
}

// viewList is a view case's to-do, with its marks, then its steps: the list it is read in.
func viewList(t *testing.T, in viewInput) []Facts {
	t.Helper()
	loc := location(t, in.Loc)
	f := decodeFacts(t, in.Todo, in.Sessions, loc)
	f.RestoredAt, f.SomedayUntil, f.Superseded = in.RestoredAt, in.SomedayUntil, in.Superseded
	list := []Facts{f}
	for _, raw := range in.Steps {
		list = append(list, decodeFacts(t, raw, in.Sessions, loc))
	}
	return list
}

// listFacts are a list case's facts, its marks put on each to-do they name.
func listFacts(t *testing.T, in countsInput) []Facts {
	t.Helper()
	loc := location(t, in.Loc)
	var list []Facts
	for _, raw := range in.Todos {
		f := decodeFacts(t, raw, in.Sessions, loc)
		if at, ok := in.RestoredAt[f.ID]; ok {
			f.RestoredAt = &at
		}
		if at, ok := in.SomedayUntil[f.ID]; ok {
			f.SomedayUntil = &at
		}
		list = append(list, f)
	}
	return list
}

// sameJSON fails unless got, written as JSON, is the same JSON value as want, and holds to the schema's def.
func sameJSON(t *testing.T, def string, got any, want json.RawMessage) {
	t.Helper()
	out, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var g, w any
	if err := json.Unmarshal(out, &g); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(want, &w); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Errorf("%s:\n got  %s\n want %s", def, out, compact(t, want))
	}
	if p := holdsTo(t, def, out); p != "" {
		t.Errorf("%s does not hold to the schema: %s", def, p)
	}
}

func compact(t *testing.T, raw json.RawMessage) []byte {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	out, _ := json.Marshal(v)
	return out
}

// viewSchema is schema/wire/todo-view.schema.json, compiled once.
var viewSchema = sync.OnceValues(func() (map[string]any, error) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "..", "schema", "wire", "todo-view.schema.json"))
	if err != nil {
		return nil, err
	}
	return jsonsubset.Compile(data)
})

// holdsTo says what in raw does not hold to the schema's definition def, "" when it all does.
func holdsTo(t *testing.T, def string, raw []byte) string {
	t.Helper()
	root, err := viewSchema()
	if err != nil {
		t.Fatal(err)
	}
	v, err := jsonsubset.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	return jsonsubset.ValidateAt(root, root["$defs"].(map[string]any)[def].(map[string]any), v, "$", 0)
}

func TestProjectionFollowsTheViewCases(t *testing.T) {
	for _, c := range readCases[viewInput](t, "todo-view/todo_view.json") {
		t.Run(c.Name, func(t *testing.T) {
			list := viewList(t, c.Input)
			v := View(Relate(list)[0], c.Input.Now)
			sameJSON(t, "TodoSummary", v, c.Expected.View)
			// Read with its steps as a list, it ranks 0 in its section and reads the same.
			if all := Views(list, c.Input.Now); !reflect.DeepEqual(all[0], v) {
				t.Errorf("in its list: %+v, alone %+v", all[0], v)
			}
		})
	}
}

func TestProjectionFollowsTheCountsCases(t *testing.T) {
	for _, c := range readCases[countsInput](t, "todo-counts/todo_counts.json") {
		t.Run(c.Name, func(t *testing.T) {
			list := listFacts(t, c.Input)
			views := Views(list, c.Input.Now)
			for i, v := range views {
				out, _ := json.Marshal(v)
				if p := holdsTo(t, "TodoSummary", out); p != "" {
					t.Errorf("%s's view: %s", list[i].ID, p)
				}
			}
			sameJSON(t, "TodoCounts", Counts(list, views), c.Expected.Counts)
			sameJSON(t, "TodoPlaces", Places(list, views, c.Input.Projects), c.Expected.Places)
			if c.Expected.Order == nil {
				return
			}
			got := map[string][]string{}
			for i, v := range views {
				got[v.Section] = append(got[v.Section], list[i].ID)
			}
			for s, ids := range got {
				slices.SortFunc(ids, func(a, b string) int {
					return views[slices.IndexFunc(list, func(f Facts) bool { return f.ID == a })].Rank -
						views[slices.IndexFunc(list, func(f Facts) bool { return f.ID == b })].Rank
				})
				got[s] = ids
			}
			if !reflect.DeepEqual(got, c.Expected.Order) {
				t.Errorf("order by rank:\n got  %v\n want %v", got, c.Expected.Order)
			}
		})
	}
}

// Every reason a case expects is a TodoReason, and every line key one the README lists.
func TestProjectionCasesUseKnownNames(t *testing.T) {
	keys := []string{lineFailedStart, lineAgentAsks, lineInterrupted, lineDecision, lineDecisionDefault, lineCannot,
		lineAwaitsReply, lineWorking, linePersonaWorking, lineStarting, lineBackground, lineParked, lineFinished,
		lineAccepted, lineDeployed, linePROpen, lineUsageLimit, lineFinishedSeen, lineCompleted, lineCanceled,
		lineReported, lineSuperseded, lineSomeday}
	readme, err := os.ReadFile(fixtures + "todo-view/README.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range keys {
		if !slices.Contains(splitCells(string(readme)), "`"+k+"`") {
			t.Errorf("line key %s is not in spec/fixtures/todo-view/README.md's table", k)
		}
	}
	for _, c := range readCases[viewInput](t, "todo-view/todo_view.json") {
		var v assistantwire.TodoSummary
		if err := json.Unmarshal(c.Expected.View, &v); err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(assistantwire.TodoReasons, v.Reason) {
			t.Errorf("%s: reason %s", c.Name, v.Reason)
		}
		if v.Line != nil && !slices.Contains(keys, v.Line.Key) {
			t.Errorf("%s: line key %s", c.Name, v.Line.Key)
		}
	}
}

// splitCells are the first cells of the README's table rows.
func splitCells(md string) []string {
	var out []string
	for line := range strings.Lines(md) {
		if cell, ok := strings.CutPrefix(line, "|"); ok {
			if i := strings.IndexByte(cell, '|'); i >= 0 {
				out = append(out, strings.TrimSpace(cell[:i]))
			}
		}
	}
	return out
}
