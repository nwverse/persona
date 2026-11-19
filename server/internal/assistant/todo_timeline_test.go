package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistant/todo/timeline"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

// A to-do's timeline on the wire (todo_timeline.go, #10465): GET /api/v1/todos/{id}/timeline pages through its rounds,
// and GET /api/v1/todos/{id} carries the ones the paper shows, how many there are, its results and its recap, all
// read from the stores the manager keeps (the feed's items about it, its decisions, its runs and their sessions).

// todoHistory is a to-do a run works on (server/s1, working) with a history: three instructions of the owner's, a turn
// of the run after the first (a receipt from before #10465, naming its computer only, with a PR), a rework long ago
// (a Background round, on its day in the owner's zone, Asia/Tokyo) and, with a database, a decision of the owner's.
// It answers the to-do's id and its rounds' ids, oldest first.
func todoHistory(t *testing.T, e *todoEnv, database *db.DB) (string, []string) {
	t.Helper()
	ctx := context.Background()
	setSettings(t, e.s, func(v *Settings) { v.Timezone = "Asia/Tokyo" })
	began := time.Now().UTC().Add(-time.Minute)
	e.sessions("server", proto.Session{SessionID: "s1", Title: "Hedge trimming", Status: proto.StatusWorking,
		State: proto.StateWorking, StateSince: began, UpdatedAt: began, Activity: "measuring the hedge"})
	id := e.create("Prune the hedge").ID
	e.must("PUT", "/api/v1/todos/"+id+"/agents/server/s1", "")
	rounds := []string{"start"}
	if database != nil {
		var d struct{ Decision db.TodoDecisionRow }
		_ = json.Unmarshal(e.must("POST", "/api/v1/todos/"+id+"/decisions", `{"question":"Which side first?","choice":"The street side"}`).Body.Bytes(), &d)
		rounds = append(rounds, "r:dec:"+d.Decision.ID)
	}
	t0 := time.Now().UTC().Add(time.Minute)
	add := func(kind string, body any, at time.Time) string {
		t.Helper()
		it, err := e.s.appendFeedAt(ctx, "conv1", kind, id, body, at)
		if err != nil {
			t.Fatal(err)
		}
		return it.ID
	}
	add(feedReview, reviewFeedBody{Title: "Prune the hedge", Verdict: reviewRework, Round: 1, Note: "the corner is uneven"},
		time.Date(2026, 1, 15, 20, 0, 0, 0, time.UTC)) // 2026-01-16 in Tokyo
	ask := map[string]string{"machine_id": "server", "session_id": "s1"}
	for i, text := range []string{"trim the street side first", "then the corner", "and sweep up after"} {
		body := map[string]string{"text": text}
		for k, v := range ask {
			body[k] = v
		}
		rounds = append(rounds, "r:feed:"+add(feedRouted, body, t0.Add(time.Duration(2*i)*time.Minute)))
		if i == 0 {
			add(feedReceipt, receiptBody{Title: "Prune the hedge", State: "done", Machine: "server", Summary: "Trimmed the street side.",
				PR: "https://github.com/o/garden/pull/7"}, t0.Add(time.Minute))
		}
	}
	rounds = slices.Insert(rounds, 1, "bg:2026-01-16") // after start, before every round that began later
	return id, rounds
}

func roundIDs(rounds []assistantwire.TodoRound) []string {
	out := make([]string, len(rounds))
	for i, r := range rounds {
		out[i] = r.ID
	}
	return out
}

// The timeline route: every round oldest first, in pages back from the latest; the turn of an older receipt given to
// the run on its computer (the computers' names read for it), the working run live in the latest round, the rework on
// its day in the owner's zone. A before that names no round 409, an unknown to-do 404; at most 100 rounds a page.
func TestTodoTimelineRoute(t *testing.T) {
	todoStores(t, func(t *testing.T, e *todoEnv, database *db.DB) {
		id, want := todoHistory(t, e, database)
		read := func(query string) assistantwire.TodoTimeline {
			t.Helper()
			var tl assistantwire.TodoTimeline
			body := e.must("GET", "/api/v1/todos/"+id+"/timeline"+query, "").Body.Bytes()
			if err := json.Unmarshal(body, &tl); err != nil || tl.Rounds == nil {
				t.Fatalf("%s: %v %s", query, err, body)
			}
			return tl
		}
		all := read("")
		if got := roundIDs(all.Rounds); !slices.Equal(got, want) || all.More {
			t.Fatalf("rounds %v (more %v), want %v", got, all.More, want)
		}
		first := all.Rounds[len(want)-3] // the first instruction's
		if i := slices.IndexFunc(first.Entries, func(e assistantwire.TodoTimelineEntry) bool {
			return e.ID == "run:server/s1@"+first.ID && e.Turns == 1 && e.Text == "Trimmed the street side."
		}); i < 0 {
			t.Errorf("the older receipt is not the run's turn: %+v", first.Entries)
		}
		last := all.Rounds[len(want)-1]
		if i := slices.IndexFunc(last.Entries, func(e assistantwire.TodoTimelineEntry) bool {
			return e.ID == "run:server/s1@"+last.ID && e.Live && e.Text == "measuring the hedge"
		}); i < 0 {
			t.Errorf("no live line of the working run in the latest round: %+v", last.Entries)
		}

		if p := read("?limit=2"); !slices.Equal(roundIDs(p.Rounds), want[len(want)-2:]) || !p.More {
			t.Errorf("the latest two: %v (more %v)", roundIDs(p.Rounds), p.More)
		}
		before := "?before=" + strings.ReplaceAll(want[len(want)-2], ":", "%3A") + "&limit=1"
		if p := read(before); !slices.Equal(roundIDs(p.Rounds), want[len(want)-3:len(want)-2]) || !p.More {
			t.Errorf("one before %s: %v (more %v)", want[len(want)-2], roundIDs(p.Rounds), p.More)
		}
		if p := read("?before=start"); len(p.Rounds) != 0 || p.More {
			t.Errorf("before the first: %v (more %v)", roundIDs(p.Rounds), p.More)
		}
		for _, q := range []string{"?limit=0", "?limit=x", "?limit=-3"} {
			if p := read(q); len(p.Rounds) != len(want) {
				t.Errorf("%s: %d rounds, want every one", q, len(p.Rounds))
			}
		}
		w := e.do("GET", "/api/v1/todos/"+id+"/timeline?before=r%3Afeed%3Agone", "")
		var refused struct{ Code string }
		if _ = json.Unmarshal(w.Body.Bytes(), &refused); w.Code != http.StatusConflict || refused.Code != "round_not_found" {
			t.Errorf("a round that is not there: %d %s", w.Code, w.Body)
		}
		w = e.do("GET", "/api/v1/todos/todo_0000000000000000/timeline", "")
		if _ = json.Unmarshal(w.Body.Bytes(), &refused); w.Code != http.StatusNotFound || refused.Code != "todo_not_found" {
			t.Errorf("an unknown to-do: %d %s", w.Code, w.Body)
		}
	})
}

// A page has the 20 latest rounds when it is not told how many, and 100 at most.
func TestTodoTimelinePageLimits(t *testing.T) {
	e := newTodoEnv(t, nil)
	id := e.create("Answer the letters").ID
	t0 := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	for i := range 130 {
		if _, err := e.s.appendFeedAt(context.Background(), "conv1", feedRouted, id, map[string]string{"text": fmt.Sprint("letter ", i)},
			t0.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	for query, n := range map[string]int{"": todoTimelinePage, "?limit=1000": todoTimelineMaxPage, "?limit=130": todoTimelineMaxPage, "?limit=7": 7} {
		var tl assistantwire.TodoTimeline
		_ = json.Unmarshal(e.must("GET", "/api/v1/todos/"+id+"/timeline"+query, "").Body.Bytes(), &tl)
		if len(tl.Rounds) != n || !tl.More || tl.Rounds[n-1].Header == nil || tl.Rounds[n-1].Header.Text != "letter 129" {
			t.Errorf("%q: %d rounds (more %v), want the latest %d", query, len(tl.Rounds), tl.More, n)
		}
	}
}

// The item route: the row as the list has it, with the rounds the paper shows (the latest two that are not Background
// ones), how many there are, the results and what changed since the owner looked, the same as the timeline route has
// them; the same state read twice is the same bytes.
func TestTodoItemCarriesItsRounds(t *testing.T) {
	todoStores(t, func(t *testing.T, e *todoEnv, database *db.DB) {
		id, want := todoHistory(t, e, database)
		e.must("PUT", "/api/v1/todos/"+id+"/seen", "") // before the instructions: the run's turn is news
		body := e.must("GET", "/api/v1/todos/"+id, "").Body.Bytes()
		var item struct {
			Todo json.RawMessage `json:"todo"`
			assistantwire.TodoItemDetail
		}
		if err := json.Unmarshal(body, &item); err != nil || item.RoundsTotal == nil {
			t.Fatalf("%v %s", err, body)
		}
		var row listedTodo
		if err := json.Unmarshal(item.Todo, &row); err != nil || row.ID != id || row.View == nil {
			t.Fatalf("the row: %v %s", err, item.Todo)
		}
		if got := roundIDs(item.Rounds); !slices.Equal(got, want[len(want)-2:]) || *item.RoundsTotal != len(want) {
			t.Fatalf("rounds %v of %d, want %v of %d", got, *item.RoundsTotal, want[len(want)-2:], len(want))
		}
		var tl assistantwire.TodoTimeline
		_ = json.Unmarshal(e.must("GET", "/api/v1/todos/"+id+"/timeline", "").Body.Bytes(), &tl)
		paper, _ := json.Marshal(timeline.Latest(tl.Rounds, timeline.PaperRounds))
		if got, _ := json.Marshal(item.Rounds); !bytes.Equal(got, paper) {
			t.Fatalf("the item's rounds:\n%s\nthe timeline's latest:\n%s", got, paper)
		}
		if len(item.Results) != 1 || item.Results[0].Kind != assistantwire.TodoResultKindChange ||
			item.Results[0].URL != "https://github.com/o/garden/pull/7" {
			t.Errorf("results: %+v", item.Results)
		}
		if r := item.Recap; r == nil || r.Round != want[len(want)-3] || r.Counts[assistantwire.TodoEntryTypeRun] != 1 {
			t.Errorf("recap: %+v", item.Recap)
		}
		if again := e.must("GET", "/api/v1/todos/"+id, "").Body.Bytes(); !bytes.Equal(again, body) {
			t.Fatalf("unchanged state read twice:\n%s\n%s", body, again)
		}
	})
}

// A to-do with no history yet answers rounds [] and rounds_total 0, which a client tells apart from an older manager
// that sends neither; no recap and no results.
func TestTodoItemWithNoHistory(t *testing.T) {
	e := newTodoEnv(t, nil)
	id := e.create("Water the roses").ID
	var item map[string]json.RawMessage
	body := e.must("GET", "/api/v1/todos/"+id, "").Body.Bytes()
	if err := json.Unmarshal(body, &item); err != nil {
		t.Fatal(err)
	}
	if string(item["rounds"]) != "[]" || string(item["rounds_total"]) != "0" || item["recap"] != nil || item["results"] != nil {
		t.Fatalf("%s", body)
	}
	var tl map[string]json.RawMessage
	_ = json.Unmarshal(e.must("GET", "/api/v1/todos/"+id+"/timeline", "").Body.Bytes(), &tl)
	if string(tl["rounds"]) != "[]" || tl["more"] != nil {
		t.Fatalf("its timeline: %v", tl)
	}
}

// When the to-do's history cannot be read (here a store without the feed), the item route still answers the row,
// without the history's members, as an older manager does; the timeline route answers 503.
func TestTodoTimelineWithoutItsStores(t *testing.T) {
	e := &todoEnv{t: t, f: newFakeHub().withMachine()}
	e.s = newTestService(t, e.f) // memStore: no assistant_feed
	id := e.create("Order compost").ID
	var item map[string]json.RawMessage
	body := e.must("GET", "/api/v1/todos/"+id, "").Body.Bytes()
	if err := json.Unmarshal(body, &item); err != nil || item["todo"] == nil {
		t.Fatalf("%v %s", err, body)
	}
	for _, k := range []string{"rounds", "rounds_total", "recap", "results"} {
		if item[k] != nil {
			t.Errorf("%s with no readable history: %s", k, body)
		}
	}
	if w := e.do("GET", "/api/v1/todos/"+id+"/timeline", ""); w.Code != http.StatusServiceUnavailable {
		t.Errorf("the timeline: %d %s", w.Code, w.Body)
	}
}
