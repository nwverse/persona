package assistant

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/prompts"
)

func watchTool(t *testing.T, s *Service, name, args string) string {
	t.Helper()
	out, err := s.runTool(context.Background(), "server", "run-session", name, json.RawMessage(args))
	if err != nil {
		t.Fatalf("%s %s: %v", name, args, err)
	}
	return out
}

func watchTasks(s *Service) []Task {
	return s.tasksView(context.Background(), func(t Task) bool { return t.WatchID != "" })
}

func onlyWatch(t *testing.T, s *Service) Watch {
	t.Helper()
	ws, err := s.watches(context.Background())
	if err != nil || len(ws) != 1 {
		t.Fatalf("want one card: %+v %v", ws, err)
	}
	return ws[0]
}

// Draft → preview → confirm makes one card and one scheduled task; deleting the card removes both.
func TestWatchDraftConfirmDelete(t *testing.T) {
	s := newTestService(t, newFakeHub())
	out := watchTool(t, s, "watch_draft", `{"question":"How many open orders are waiting in the Garden app shop?","title":"Open orders","schedule":"8:30","display":"trend","unit":"orders","alert":{"when":"above","value":20}}`)
	w := onlyWatch(t, s)
	if w.State != watchDraft || w.TaskID != "" || !strings.Contains(out, w.ID) || !strings.Contains(out, "08:30") || !strings.Contains(out, "above 20") {
		t.Fatalf("a draft with a preview and no task: %+v\n%s", w, out)
	}
	if len(watchTasks(s)) != 0 {
		t.Fatal("a draft has no scheduled task")
	}
	if _, err := s.runTool(context.Background(), "server", "run-session", "watch_report", json.RawMessage(`{"id":"`+w.ID+`","text":"3 open"}`)); err == nil {
		t.Fatal("a draft takes no readings")
	}
	watchTool(t, s, "watch_draft", `{"id":"`+w.ID+`","question":"How many open orders are waiting?"}`)
	if w = onlyWatch(t, s); w.Question != "How many open orders are waiting?" || w.Title != "Open orders" || w.Schedule.Time != "08:30" {
		t.Fatalf("revising keeps the rest: %+v", w)
	}

	out = watchTool(t, s, "watch_confirm", `{"id":"`+w.ID+`"}`)
	w = onlyWatch(t, s)
	tasks := watchTasks(s)
	if w.State != watchActive || len(tasks) != 1 || tasks[0].ID != w.TaskID || !strings.Contains(out, w.TaskID) {
		t.Fatalf("confirm makes one task: %+v %+v", w, tasks)
	}
	task := tasks[0]
	if task.WatchID != w.ID || task.Repeat != repeatDaily || task.Time != "08:30" || !task.Enabled || task.Push != pushAsked ||
		!strings.Contains(task.Prompt, "How many open orders are waiting?") || !strings.Contains(task.Prompt, "watch_report") {
		t.Fatalf("the task refreshes the card quietly: %+v", task)
	}
	watchTool(t, s, "watch_confirm", `{"id":"`+w.ID+`"}`)
	if len(watchTasks(s)) != 1 {
		t.Fatal("confirming twice makes no second task")
	}

	if out := watchTool(t, s, "watch_delete", `{"id":"`+w.ID+`"}`); !strings.Contains(out, "Deleted") {
		t.Fatal(out)
	}
	if ws, _ := s.watches(context.Background()); len(ws) != 0 || len(watchTasks(s)) != 0 {
		t.Fatalf("delete removes the card and its task: %+v %+v", ws, watchTasks(s))
	}
}

// The alert pushes only when the card's rule trips.
func TestWatchAlertRule(t *testing.T) {
	for _, tc := range []struct {
		name, alert string
		readings    []string
		pushes      []bool
	}{
		{"above", `{"when":"above","value":5}`, []string{`"number":3`, `"number":5`, `"number":6`}, []bool{false, false, true}},
		{"below", `{"when":"below","value":2}`, []string{`"number":3`, `"number":1`}, []bool{false, true}},
		{"bad", `{"when":"bad"}`, []string{`"status":"ok"`, `"status":"warn"`, `"status":"bad"`}, []bool{false, false, true}},
		{"change", `{"when":"change"}`, []string{`"number":4`, `"number":4`, `"number":7`}, []bool{false, false, true}},
		{"never", `null`, []string{`"number":99`, `"status":"bad"`}, []bool{false, false}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeHub()
			s := newTestService(t, f)
			watchTool(t, s, "watch_draft", `{"question":"Build queue length","display":"number","alert":`+tc.alert+`}`)
			w := onlyWatch(t, s)
			watchTool(t, s, "watch_confirm", `{"id":"`+w.ID+`"}`)
			for i, r := range tc.readings {
				before := len(f.pushed())
				out := watchTool(t, s, "watch_report", `{"id":"`+w.ID+`","text":"reading",`+r+`}`)
				pushed := len(f.pushed()) > before
				if pushed != tc.pushes[i] || strings.Contains(out, "tripped") != tc.pushes[i] {
					t.Fatalf("reading %d (%s): pushed %v, want %v (%s)", i, r, pushed, tc.pushes[i], out)
				}
				if pushed {
					p := f.pushed()[len(f.pushed())-1]
					if p.Title != w.Title || p.Body != "reading" || p.SessionID != "run-session" || !p.Asked {
						t.Fatalf("the push says the card and opens its run: %+v", p)
					}
				}
			}
			w = onlyWatch(t, s)
			if len(w.History) != len(tc.readings) || w.Latest == nil || w.Latest.Alert != tc.pushes[len(tc.pushes)-1] {
				t.Fatalf("history and the latest's alert: %+v", w)
			}
		})
	}
}

// Readings are bounded: one line of text, a valid status, a small table.
func TestWatchReadingChecks(t *testing.T) {
	s := newTestService(t, newFakeHub())
	watchTool(t, s, "watch_draft", `{"question":"Release checklist","display":"table"}`)
	w := onlyWatch(t, s)
	watchTool(t, s, "watch_confirm", `{"id":"`+w.ID+`"}`)
	for _, bad := range []string{
		`{"id":"` + w.ID + `","text":""}`,
		`{"id":"` + w.ID + `","text":"x","status":"red"}`,
		`{"id":"` + w.ID + `","text":"x","table":{"columns":[],"rows":[]}}`,
		`{"id":"` + w.ID + `","text":"x","table":{"columns":["a"],"rows":[["1","2"]]}}`,
		`{"id":"nope","text":"x"}`,
	} {
		if _, err := s.runTool(context.Background(), "server", "", "watch_report", json.RawMessage(bad)); err == nil {
			t.Errorf("refused: %s", bad)
		}
	}
	watchTool(t, s, "watch_report", `{"id":"`+w.ID+`","text":"2 of 3 done","table":{"columns":["Step","State"],"rows":[["Docs","done"],["Store page"]]}}`)
	if r := onlyWatch(t, s).Latest; r == nil || r.Table == nil || len(r.Table.Rows[1]) != 2 {
		t.Fatalf("a short row is padded to the header: %+v", r)
	}
}

// Pause, edit, reorder and the project filter, from the app's routes.
func TestWatchRoutes(t *testing.T) {
	s := newTestService(t, newFakeHub())
	watchTool(t, s, "watch_draft", `{"question":"Docs site uptime","display":"status","project":"docs-site"}`)
	watchTool(t, s, "watch_draft", `{"question":"Garden app crash count","display":"number"}`)
	ws, _ := s.watches(context.Background())
	a, b := ws[0].ID, ws[1].ID
	if rec := placeDo(s, "", "POST", "/api/v1/assistant/watches/"+a+"/confirm", ""); rec.Code != 200 {
		t.Fatalf("confirm: %d %s", rec.Code, rec.Body)
	}
	if rec := placeDo(s, "", "POST", "/api/v1/assistant/watches/"+b+"/confirm", ""); rec.Code != 200 {
		t.Fatalf("confirm: %d %s", rec.Code, rec.Body)
	}
	if ws, _ = s.watches(context.Background()); ws[0].ID != b {
		t.Fatalf("a confirmed card goes first: %+v", ws)
	}
	if rec := placeDo(s, "read", "PUT", "/api/v1/assistant/watches/"+a, `{"paused":true}`); rec.Code != 403 {
		t.Fatalf("a read-only device cannot change cards: %d", rec.Code)
	}
	rec := placeDo(s, "", "PUT", "/api/v1/assistant/watches/"+a, `{"paused":true,"schedule":"2h","alert":{"when":"bad"}}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"paused":true`) {
		t.Fatalf("pause: %d %s", rec.Code, rec.Body)
	}
	for _, task := range watchTasks(s) {
		if task.WatchID == a && (task.Enabled || task.Repeat != repeatInterval || task.EveryMinutes != 120) {
			t.Fatalf("pausing and the new rule reach the task: %+v", task)
		}
	}
	if rec := placeDo(s, "", "PUT", "/api/v1/assistant/watches/"+a, `{"display":"pie"}`); rec.Code != 400 {
		t.Fatalf("an unknown display is refused: %d", rec.Code)
	}
	if rec := placeDo(s, "", "PUT", "/api/v1/assistant/watches/order", `{"ids":["`+a+`","`+b+`"]}`); rec.Code != 200 {
		t.Fatalf("order: %d %s", rec.Code, rec.Body)
	}
	rec = placeDo(s, "", "GET", "/api/v1/assistant/watches", "")
	var all struct{ Watches []WatchView }
	if json.Unmarshal(rec.Body.Bytes(), &all) != nil || len(all.Watches) != 2 || all.Watches[0].ID != a {
		t.Fatalf("listed in the new order: %s", rec.Body)
	}
	rec = placeDo(s, "", "GET", "/api/v1/assistant/watches?project=docs-site", "")
	var mine struct{ Watches []WatchView }
	if json.Unmarshal(rec.Body.Bytes(), &mine) != nil || len(mine.Watches) != 1 || mine.Watches[0].ID != a {
		t.Fatalf("a project's cards only: %s", rec.Body)
	}
	if rec := placeDo(s, "", "DELETE", "/api/v1/assistant/watches/"+a, ""); rec.Code != 200 {
		t.Fatalf("delete: %d", rec.Code)
	}
	if rec := placeDo(s, "", "DELETE", "/api/v1/assistant/watches/"+a, ""); rec.Code != 404 {
		t.Fatalf("deleting again: %d", rec.Code)
	}
	if len(watchTasks(s)) != 1 {
		t.Fatalf("one card, one task: %+v", watchTasks(s))
	}
}

func TestWatchSchedule(t *testing.T) {
	for in, want := range map[string]WatchSchedule{
		"":            {Repeat: repeatDaily, Time: "09:00"},
		"7:05":        {Repeat: repeatDaily, Time: "07:05"},
		"30m":         {Repeat: repeatInterval, EveryMinutes: 30},
		"1d":          {Repeat: repeatInterval, EveryMinutes: 1440},
		"0 9 * * 1-5": {Repeat: repeatCron, Cron: "0 9 * * 1-5"},
	} {
		got, err := parseWatchSchedule(in)
		if err != nil || got.Repeat != want.Repeat || got.Time != want.Time || got.EveryMinutes != want.EveryMinutes || got.Cron != want.Cron {
			t.Errorf("%q: %+v %v", in, got, err)
		}
	}
	for _, bad := range []string{"1m", "soon", "25:00"} {
		if _, err := parseWatchSchedule(bad); err == nil {
			t.Errorf("%q is refused", bad)
		}
	}
}

func TestGoldenWatchRun(t *testing.T) {
	for _, lang := range []string{"", "en", "ja"} {
		for _, display := range []string{watchNumber, watchStatus, watchTable} {
			name := "watch_run." + display
			if lang != "" {
				name += "." + lang
			}
			golden(t, name, prompts.RenderWatchRun(prompts.WatchRun{Lang: lang, ID: "w_1a2b", Title: "Open orders",
				Question: "How many orders are waiting in the Garden app shop?", Display: display, Unit: "orders"}))
		}
	}
}
