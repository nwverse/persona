package assistant

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// The clients' SOP cases (spec/fixtures/triage, web/src/lib/todo-triage-fixture.test.ts reads the same files): the hub's
// copy places every to-do in the same section by the same rule, merges the same rows and counts the same.

const triageFixtures = "../../../spec/fixtures/triage/"

type triageFixtureCase struct {
	Name  string `json:"name"`
	Input struct {
		Now          time.Time       `json:"now"`
		Sessions     []proto.Session `json:"sessions"`
		Todo         json.RawMessage `json:"todo"`
		Todos        json.RawMessage `json:"todos"`
		RestoredAt   time.Time       `json:"restored_at"`
		IncludeQuiet bool            `json:"include_quiet"`
	} `json:"input"`
	Expected struct {
		Section  string                `json:"section"`
		Rule     string                `json:"rule"`
		Last     time.Time             `json:"last"`
		Sections map[string][][]string `json:"sections"`
		Open     int                   `json:"open"`
		Shelved  int                   `json:"shelved"`
		Finished int                   `json:"finished"`
		Keys     []string              `json:"keys"`
	} `json:"expected"`
}

func readTriageCases(t *testing.T, file string) []triageFixtureCase {
	t.Helper()
	b, err := os.ReadFile(triageFixtures + file)
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Version int                 `json:"version"`
		Cases   []triageFixtureCase `json:"cases"`
	}
	if err := json.Unmarshal(b, &f); err != nil || f.Version != 1 || len(f.Cases) == 0 {
		t.Fatalf("%s: %v (version %d, %d cases)", file, err, f.Version, len(f.Cases))
	}
	return f.Cases
}

func lookupOf(list []proto.Session) triageLookup {
	return func(machine, session string) (proto.Session, bool) {
		i := slices.IndexFunc(list, func(s proto.Session) bool { return s.MachineID == machine && s.SessionID == session })
		if i < 0 {
			return proto.Session{}, false
		}
		return list[i], true
	}
}

func TestTriageFollowsTheSharedCases(t *testing.T) {
	for _, c := range readTriageCases(t, "triage.json") {
		t.Run(c.Name, func(t *testing.T) {
			var td listedTodo
			if err := json.Unmarshal(c.Input.Todo, &td); err != nil {
				t.Fatal(err)
			}
			got := triageTodo(td, lookupOf(c.Input.Sessions), c.Input.Now, c.Input.RestoredAt, time.UTC)
			if got.Section != c.Expected.Section || got.Rule != c.Expected.Rule {
				t.Fatalf("got %s / %s, want %s / %s", got.Section, got.Rule, c.Expected.Section, c.Expected.Rule)
			}
			if !c.Expected.Last.IsZero() && !got.Last.Equal(c.Expected.Last) {
				t.Fatalf("last %v, want %v", got.Last, c.Expected.Last)
			}
		})
	}
}

func TestTriageTodosFollowsTheSharedCases(t *testing.T) {
	for _, c := range readTriageCases(t, "groups.json") {
		t.Run(c.Name, func(t *testing.T) {
			var list []listedTodo
			if err := json.Unmarshal(c.Input.Todos, &list); err != nil {
				t.Fatal(err)
			}
			r := triageTodos(list, lookupOf(c.Input.Sessions), c.Input.Now, time.UTC, c.Input.IncludeQuiet)
			for _, s := range triageSections {
				var got [][]string
				for _, g := range r.Sections[s] {
					var ids []string
					for _, m := range g.Members {
						ids = append(ids, m.ID)
					}
					got = append(got, ids)
				}
				want := c.Expected.Sections[s]
				if len(got) != len(want) || !slices.EqualFunc(got, want, slices.Equal) {
					t.Fatalf("section %s: got %v, want %v", s, got, want)
				}
			}
			if r.Open != c.Expected.Open || r.Shelved != c.Expected.Shelved || r.Finished != c.Expected.Finished {
				t.Fatalf("counts %d/%d/%d, want %d/%d/%d", r.Open, r.Shelved, r.Finished, c.Expected.Open, c.Expected.Shelved, c.Expected.Finished)
			}
		})
	}
}

func TestTriageRelatedKeysFollowTheSharedCases(t *testing.T) {
	for _, c := range readTriageCases(t, "related.json") {
		t.Run(c.Name, func(t *testing.T) {
			var td listedTodo
			if err := json.Unmarshal(c.Input.Todo, &td); err != nil {
				t.Fatal(err)
			}
			if got := triageRelatedKeys(td.Todo); !slices.Equal(got, c.Expected.Keys) {
				t.Fatalf("got %v, want %v", got, c.Expected.Keys)
			}
		})
	}
}

func TestTriageDueByUsesUTCWithoutAUserZone(t *testing.T) {
	td := Todo{ID: "due", DueDate: "2026-11-10"}
	got, ok := triageDueBy(td, nil)
	if !ok || got.Location() != time.UTC || got.Format(time.RFC3339) != "2026-11-10T23:59:59Z" {
		t.Fatalf("nil zone: got %v (%v), ok=%v", got, got.Location(), ok)
	}
	zone := time.FixedZone("user", 8*60*60)
	got, ok = triageDueBy(td, zone)
	if !ok || got.Location() != zone || got.Format(time.RFC3339) != "2026-11-10T23:59:59+08:00" {
		t.Fatalf("user zone: got %v (%v), ok=%v", got, got.Location(), ok)
	}
}

// Dismissed agents are not the apps' (formatTodo leaves them out): one still asking does not make the to-do wait.
func TestTriageLeavesDismissedAgentsOut(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	gone := now.Add(-time.Hour)
	td := listedTodo{Todo: Todo{ID: "t", CreatedAt: now.Add(-2 * time.Hour), UpdatedAt: now.Add(-time.Hour), Origin: "c",
		Agents: []TodoAgent{{MachineID: "m", SessionID: "ask", DismissedAt: &gone}}}}
	look := lookupOf([]proto.Session{{MachineID: "m", SessionID: "ask", State: proto.StateNeedsYou, UpdatedAt: now}})
	if got := triageTodo(td, look, now, time.Time{}, time.UTC); got.Rule != ruleRest {
		t.Fatalf("got %+v", got)
	}
}
