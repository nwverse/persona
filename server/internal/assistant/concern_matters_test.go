package assistant

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// Matters (concern_matters.go): every message the owner writes in the main conversation is filed under exactly one
// concern, a task belongs to the concern its part_of chain (or its brief's message) names, and only a concern closed
// by concern_report waits for the owner's Got it. conv1 is the main conversation; texts and ids are placeholders.

// listedMatter is a to-do as GET /api/v1/todos lists it, with what a matter adds.
type listedMatter struct {
	ID          string       `json:"id"`
	Title       string       `json:"title"`
	Kind        string       `json:"kind"`
	DoneAt      *time.Time   `json:"done_at"`
	ConfirmedAt *time.Time   `json:"confirmed_at"`
	HeaderCard  string       `json:"header_card"`
	Concern     *ConcernView `json:"concern"`
	Matter      string       `json:"matter"`
	PartOf      string       `json:"part_of"`
}

func (e *todoEnv) matters() map[string]listedMatter {
	e.t.Helper()
	var res struct {
		Todos []listedMatter `json:"todos"`
	}
	if err := json.Unmarshal(e.must("GET", "/api/v1/todos", "").Body.Bytes(), &res); err != nil {
		e.t.Fatal(err)
	}
	out := map[string]listedMatter{}
	for _, t := range res.Todos {
		out[t.ID] = t
	}
	return out
}

func ownerSaid(id, text string, at time.Time) proto.Item {
	return proto.Item{ID: id, TS: at, Role: "user", Kind: proto.KindText, Text: text}
}

func wordTexts(v *ConcernView) []string {
	if v == nil {
		return nil
	}
	out := make([]string, 0, len(v.Words))
	for _, w := range v.Words {
		out = append(out, w.Text)
	}
	return out
}

func concernNamed(t *testing.T, m map[string]listedMatter, title string) listedMatter {
	t.Helper()
	for _, c := range m {
		if c.Kind == "concern" && c.Title == title {
			return c
		}
	}
	t.Fatalf("no concern %q in %+v", title, m)
	return listedMatter{}
}

func TestConcernFileFilesEachOwnerMessage(t *testing.T) {
	e := newConcernEnv(t)
	t0 := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)

	// Only the main conversation files messages.
	if _, err := runJSON(t, e.s, "conv_other", "concern_file", map[string]any{"title": "x"}); err == nil {
		t.Fatal("a conversation other than the main one filed a message")
	}
	if _, err := e.tool("concern_file", map[string]any{}); err == nil {
		t.Fatal("filed with neither id nor title")
	}

	// A new matter: the owner's newest message is filed under it; Persona's own messages never are.
	e.f.setItems(
		ownerSaid("m1", "the test box keeps running out of memory", t0),
		proto.Item{ID: "a1", TS: t0.Add(time.Second), Role: "assistant", Kind: proto.KindText, Text: "on it"},
		proto.Item{ID: "n1", TS: t0.Add(2 * time.Second), Role: "user", Kind: proto.KindText,
			Text: "injected notice", Injected: &proto.Injected{Kind: proto.InjectedSystem}},
		proto.Item{ID: "r1", TS: t0.Add(3 * time.Second), Role: "user", Kind: proto.KindText, Text: "relayed", From: "peer-agent"},
		proto.Item{ID: "x1", TS: t0.Add(4 * time.Second), Role: "user", Kind: proto.KindText, Text: "tool result",
			Task: &proto.TaskNote{TaskID: "bg"}},
	)
	out, err := e.tool("concern_file", map[string]any{"title": "test box memory", "now": "checking what holds it"})
	if err != nil {
		t.Fatal(err)
	}
	m := e.matters()
	c := concernNamed(t, m, "test box memory")
	if got := wordTexts(c.Concern); len(got) != 1 || got[0] != "the test box keeps running out of memory" ||
		c.Concern.Words[0].MessageID != "m1" || c.Concern.Words[0].Conversation != "conv1" ||
		!c.Concern.Words[0].At.Equal(t0) {
		t.Fatalf("words: %+v (%s)", c.Concern, out)
	}
	if c.Concern.Now != "checking what holds it" {
		t.Fatalf("now: %q", c.Concern.Now)
	}

	// A follow-up goes under the existing matter by id; the one filed already is not filed twice.
	e.f.setItems(
		ownerSaid("m1", "the test box keeps running out of memory", t0),
		ownerSaid("m2", "and look at the router while you are at it", t0.Add(time.Minute)),
	)
	if _, err := e.tool("concern_file", map[string]any{"id": c.ID}); err != nil {
		t.Fatal(err)
	}
	if got := wordTexts(e.matters()[c.ID].Concern); strings.Join(got, "|") !=
		"the test box keeps running out of memory|and look at the router while you are at it" {
		t.Fatalf("follow-up: %v", got)
	}
	if out, _ := e.tool("concern_file", map[string]any{"id": c.ID}); !strings.Contains(out, "all filed already") {
		t.Fatalf("filed again: %s", out)
	}

	// Filed wrongly: quote moves it to another matter (a new one here).
	if _, err := e.tool("concern_file", map[string]any{"title": "router work", "quote": "look at the router"}); err != nil {
		t.Fatal(err)
	}
	m = e.matters()
	other := concernNamed(t, m, "router work")
	if got := wordTexts(other.Concern); len(got) != 1 || got[0] != "and look at the router while you are at it" {
		t.Fatalf("moved: %v", got)
	}
	if got := wordTexts(m[c.ID].Concern); len(got) != 1 {
		t.Fatalf("left behind: %v", got)
	}

	// A decision for the owner: the question and its answers; a report takes them away.
	if _, err := e.tool("concern_file", map[string]any{"id": other.ID, "message": "none", "ask": "stop the old router process?",
		"options": []string{"stop it", "leave it", "", "a", "b", "c"}}); err != nil {
		t.Fatal(err)
	}
	v := e.matters()[other.ID].Concern
	if v.Ask != "stop the old router process?" || strings.Join(v.Options, "|") != "stop it|leave it|a|b" {
		t.Fatalf("ask: %+v", v)
	}
	if _, err := e.tool("concern_report", map[string]any{"id": other.ID, "report": "leaving it for now."}); err != nil {
		t.Fatal(err)
	}
	if v := e.matters()[other.ID].Concern; v.Ask != "" || len(v.Options) != 0 {
		t.Fatalf("ask kept after the report: %+v", v)
	}

	// The owner speaks of a matter that was done: it opens again and needs a new report.
	e.f.setItems(ownerSaid("m3", "actually stop that router process", t0.Add(2*time.Minute)))
	if _, err := e.tool("concern_file", map[string]any{"id": other.ID}); err != nil {
		t.Fatal(err)
	}
	if got := e.matters()[other.ID]; got.DoneAt != nil {
		t.Fatalf("a done matter spoken of again stays done: %+v", got)
	}
}

func TestConcernFileRefusesATask(t *testing.T) {
	e := newConcernEnv(t)
	t0 := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	e.f.setItems(ownerSaid("m1", "can the 1317 build run", t0))
	if _, err := e.tool("concern_file", map[string]any{"title": "1317 build"}); err != nil {
		t.Fatal(err)
	}
	c := concernNamed(t, e.matters(), "1317 build")
	task := e.create("upgrade 1317")
	e.must("PATCH", "/api/v1/todos/"+task.ID, `{"part_of":"`+c.ID+`"}`)
	if _, err := e.tool("concern_file", map[string]any{"id": task.ID, "message": "none"}); err == nil {
		t.Fatal("a task id was accepted as a concern")
	}
	// The words route refuses a task id the same way (404, no such concern).
	if w := e.do("PUT", "/api/v1/todos/"+task.ID+"/words",
		`{"conversation":"conv1","message_id":"m1"}`); w.Code != http.StatusNotFound {
		t.Fatalf("words under a task: %d %s", w.Code, w.Body)
	}
}

func TestConcernCreateFilesTheMessageBeingHandled(t *testing.T) {
	e := newConcernEnv(t)
	e.f.setItems(ownerSaid("m9", "can the summary table be trusted", time.Now().UTC()))
	if _, err := e.tool("todo_create", map[string]any{"title": "summary table trust", "kind": "concern"}); err != nil {
		t.Fatal(err)
	}
	c := concernNamed(t, e.matters(), "summary table trust")
	if got := wordTexts(c.Concern); len(got) != 1 || got[0] != "can the summary table be trusted" {
		t.Fatalf("words: %v", got)
	}
}

func TestMatterResolutionAndOwnerMoves(t *testing.T) {
	e := newConcernEnv(t)
	t0 := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	e.f.setItems(ownerSaid("m1", "can 1317 run", t0))
	if _, err := e.tool("concern_file", map[string]any{"title": "1317 runs"}); err != nil {
		t.Fatal(err)
	}
	e.f.setItems(ownerSaid("m2", "can the trace be viewed", t0.Add(time.Minute)))
	if _, err := e.tool("concern_file", map[string]any{"title": "trace viewing"}); err != nil {
		t.Fatal(err)
	}
	m := e.matters()
	run, trace := concernNamed(t, m, "1317 runs"), concernNamed(t, m, "trace viewing")

	// A task under a concern, and a request with pieces of its own may go under one too (two levels).
	task := e.create("upgrade 1317")
	req := e.create("trace sections")
	piece := e.create("trace second section")
	e.must("PATCH", "/api/v1/todos/"+task.ID, `{"part_of":"`+run.ID+`"}`)
	e.must("PATCH", "/api/v1/todos/"+piece.ID, `{"part_of":"`+req.ID+`"}`)
	e.must("PATCH", "/api/v1/todos/"+req.ID, `{"part_of":"`+trace.ID+`"}`)
	if w := e.do("PATCH", "/api/v1/todos/"+run.ID, `{"part_of":"`+trace.ID+`"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("a concern became a piece: %d %s", w.Code, w.Body)
	}
	m = e.matters()
	if m[task.ID].Matter != run.ID || m[req.ID].Matter != trace.ID || m[piece.ID].Matter != trace.ID {
		t.Fatalf("matters: task %q req %q piece %q", m[task.ID].Matter, m[req.ID].Matter, m[piece.ID].Matter)
	}
	if m[run.ID].Matter != "" {
		t.Fatalf("a concern has a matter: %q", m[run.ID].Matter)
	}
	// Below concern, request and piece there is no further level, and a concern without tasks is no piece either.
	deep := e.create("third level")
	if w := e.do("PATCH", "/api/v1/todos/"+deep.ID, `{"part_of":"`+piece.ID+`"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("a piece took a piece: %d %s", w.Code, w.Body)
	}
	e.f.setItems(ownerSaid("m3", "one more thing", t0.Add(90*time.Second)))
	if _, err := e.tool("concern_file", map[string]any{"title": "bare matter"}); err != nil {
		t.Fatal(err)
	}
	bare := concernNamed(t, e.matters(), "bare matter")
	if w := e.do("PATCH", "/api/v1/todos/"+bare.ID, `{"part_of":"`+run.ID+`"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("a concern without tasks became a piece: %d %s", w.Code, w.Body)
	}

	// A task whose brief started from an owner message filed under a concern joins that matter.
	e.f.setItems(ownerSaid("m4", "also check the logs", t0.Add(2*time.Minute)))
	briefTask := e.create("read the logs")
	e.s.todos.mu.Lock()
	e.s.todos.todos[briefTask.ID].Brief = &TaskBrief{briefRecord: briefRecord{
		Request: &BriefRequest{Text: "also check the logs", At: t0.Add(2 * time.Minute), Conversation: "conv1", MessageID: "m4"}}}
	e.s.todos.mu.Unlock()
	if _, err := e.tool("concern_file", map[string]any{"id": run.ID}); err != nil {
		t.Fatal(err)
	}
	if got := e.matters()[briefTask.ID].Matter; got != run.ID {
		t.Fatalf("the brief's filed message did not place the task: %q", got)
	}
	var one struct {
		Todo listedMatter `json:"todo"`
	}
	if err := json.Unmarshal(e.must("GET", "/api/v1/todos/"+briefTask.ID, "").Body.Bytes(), &one); err != nil {
		t.Fatal(err)
	}
	if one.Todo.Matter != run.ID {
		t.Fatalf("GET of the one to-do: matter %q, the list says %q", one.Todo.Matter, run.ID)
	}

	// The owner moves a message to the other matter; the word says they did.
	if w := e.do("PUT", "/api/v1/todos/"+trace.ID+"/words", `{"conversation":"conv1","message_id":"m1"}`); w.Code != http.StatusOK {
		t.Fatalf("move: %d %s", w.Code, w.Body)
	}
	m = e.matters()
	if got := wordTexts(m[trace.ID].Concern); strings.Join(got, "|") != "can 1317 run|can the trace be viewed" ||
		m[trace.ID].Concern.Words[0].By != "owner" {
		t.Fatalf("moved: %+v", m[trace.ID].Concern)
	}
	if got := wordTexts(m[run.ID].Concern); strings.Join(got, "|") != "also check the logs" {
		t.Fatalf("left: %v", got)
	}
	if w := e.do("PUT", "/api/v1/todos/"+run.ID+"/words", `{"conversation":"conv1","message_id":"never"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("an unknown message without its text: %d", w.Code)
	}
}

// The header cards follow the matter-level rule: only a reported, unconfirmed concern is "finished"; no task ever is,
// and a matter whose tasks all finished but which was not reported gets no card.
func TestMatterHeaderCards(t *testing.T) {
	e := newConcernEnv(t)
	t0 := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	e.f.setItems(ownerSaid("m1", "is the sandbox network behind the failing test", t0))
	if _, err := e.tool("concern_file", map[string]any{"title": "sandbox network failure"}); err != nil {
		t.Fatal(err)
	}
	c := concernNamed(t, e.matters(), "sandbox network failure")
	if got := e.matters()[c.ID].HeaderCard; got != "" {
		t.Fatalf("a new matter raises no card: %q", got)
	}

	// An open ask raises a decide card.
	if _, err := e.tool("concern_file", map[string]any{"id": c.ID, "message": "none", "ask": "retry on the shared runner?"}); err != nil {
		t.Fatal(err)
	}
	if got := e.matters()[c.ID].HeaderCard; got != headerNeedsUser {
		t.Fatalf("an ask raises needs_user: %q", got)
	}

	task := e.create("check the sandbox network")
	e.must("PATCH", "/api/v1/todos/"+task.ID, `{"part_of":"`+c.ID+`"}`)
	e.must("PATCH", "/api/v1/todos/"+task.ID, `{"done":true}`)
	m := e.matters()
	// The task is finished and accepted by the done judge: it never waits, inside the open matter.
	if got := m[task.ID].HeaderCard; got != "" {
		t.Fatalf("a finished task raised a card inside an open matter: %q", got)
	}
	// Every task is finished but the matter is not reported: no tell card.
	if got := m[c.ID].HeaderCard; got != headerNeedsUser {
		t.Fatalf("the open ask still decides, an all-done unreported matter is not finished: %q", got)
	}
	if _, err := e.tool("concern_file", map[string]any{"id": c.ID, "message": "none", "ask": ""}); err != nil {
		t.Fatal(err)
	}
	if got := e.matters()[c.ID].HeaderCard; got != "" {
		t.Fatalf("all tasks done, unreported matter got a card: %q", got)
	}

	// The report raises the tell card, the Got it press takes it away; its finished task stays card-free.
	if _, err := e.tool("concern_report", map[string]any{"id": c.ID, "report": "not the sandbox: a flaky test."}); err != nil {
		t.Fatal(err)
	}
	m = e.matters()
	if got := m[c.ID].HeaderCard; got != headerFinished {
		t.Fatalf("a reported matter waits for Got it: %q", got)
	}
	if got := m[task.ID].HeaderCard; got != "" {
		t.Fatalf("the matter's task must never be finished: %q", got)
	}
	e.must("PUT", "/api/v1/todos/"+c.ID+"/confirm", "")
	m = e.matters()
	if got := m[c.ID].HeaderCard; got != "" || m[c.ID].ConfirmedAt == nil {
		t.Fatalf("confirmed matter: card %q", got)
	}
	if m[task.ID].ConfirmedAt == nil {
		t.Fatal("the confirm did not reach the matter's finished task")
	}

	// The single-to-do GET carries the same fields.
	one := map[string]any{}
	if err := json.Unmarshal(e.must("GET", "/api/v1/todos/"+c.ID, "").Body.Bytes(), &one); err != nil {
		t.Fatal(err)
	}
	row, _ := one["todo"].(map[string]any)
	if row == nil || row["header_card"] != "" {
		t.Fatalf("single GET header_card: %v", row)
	}
}

func TestKnowingAMatterPutsItAwayWithItsTasks(t *testing.T) {
	e := newConcernEnv(t)
	e.f.setItems(ownerSaid("m1", "is the failing test the sandbox network", time.Now().UTC()))
	if _, err := e.tool("concern_file", map[string]any{"title": "sandbox network"}); err != nil {
		t.Fatal(err)
	}
	c := concernNamed(t, e.matters(), "sandbox network")
	task := e.create("check the sandbox network")
	e.must("PATCH", "/api/v1/todos/"+task.ID, `{"part_of":"`+c.ID+`"}`)
	e.must("PATCH", "/api/v1/todos/"+task.ID, `{"done":true}`)

	// An open matter is put away with the conclusion its card showed, never without one.
	if w := e.do("PUT", "/api/v1/todos/"+c.ID+"/confirm", `{}`); w.Code != http.StatusBadRequest {
		t.Fatalf("put away without a conclusion: %d %s", w.Code, w.Body)
	}
	e.must("PUT", "/api/v1/todos/"+c.ID+"/confirm", `{"report":"not the sandbox."}`)
	m := e.matters()
	if m[c.ID].DoneAt == nil || m[c.ID].ConfirmedAt == nil {
		t.Fatalf("matter: %+v", m[c.ID])
	}
	if m[task.ID].ConfirmedAt == nil {
		t.Fatalf("its finished task still waits: %+v", m[task.ID])
	}

	// Undo takes the confirm off the matter and its task stays a task (never a finished card).
	e.must("DELETE", "/api/v1/todos/"+c.ID+"/confirm", "")
	if got := e.matters()[c.ID]; got.ConfirmedAt != nil || got.HeaderCard != headerFinished {
		t.Fatalf("after Undo: %+v", got)
	}

	// Answered right in the chat: the owner has it, so it closes already acknowledged.
	e.f.setItems(ownerSaid("m2", "what time is it", time.Now().UTC()))
	if _, err := e.tool("concern_file", map[string]any{"title": "current time"}); err != nil {
		t.Fatal(err)
	}
	now := concernNamed(t, e.matters(), "current time")
	if _, err := e.tool("concern_report", map[string]any{"id": now.ID, "report": "half past nine.", "owner_saw": true}); err != nil {
		t.Fatal(err)
	}
	got := e.matters()[now.ID]
	if got.DoneAt == nil || got.ConfirmedAt == nil || got.HeaderCard != "" {
		t.Fatalf("answered in the chat: %+v", got)
	}

	// A newer report on the matter the owner put away waits for them again; with owner_saw it does not.
	if _, err := e.tool("concern_report", map[string]any{"id": now.ID, "report": "a quarter to ten now."}); err != nil {
		t.Fatal(err)
	}
	if got := e.matters()[now.ID]; got.ConfirmedAt != nil || got.HeaderCard != headerFinished {
		t.Fatalf("a newer report does not wait: %+v", got)
	}
	if _, err := e.tool("concern_report", map[string]any{"id": now.ID, "report": "ten to ten.", "owner_saw": true}); err != nil {
		t.Fatal(err)
	}
	if got := e.matters()[now.ID]; got.ConfirmedAt == nil || got.HeaderCard != "" {
		t.Fatalf("a newer report the owner saw waits: %+v", got)
	}
	// A device's report (POST …/decisions, source reported) on the put-away matter makes it wait again too.
	e.must("POST", "/api/v1/todos/"+now.ID+"/decisions", `{"source":"reported","choice":"five to ten."}`)
	if got := e.matters()[now.ID]; got.ConfirmedAt != nil || got.HeaderCard != headerFinished {
		t.Fatalf("a device's newer report does not wait: %+v", got)
	}

	// The owner files a message under the done matter: it is being worked on again, as concern_file would make it.
	e.must("PUT", "/api/v1/todos/"+now.ID+"/words", `{"conversation":"conv1","message_id":"m3","text":"and in Toronto?"}`)
	if got := e.matters()[now.ID]; got.DoneAt != nil {
		t.Fatalf("filed under a done matter, still done: %+v", got)
	}

	// Deleting a matter forgets its filed messages, so they can be filed again.
	if err := e.s.removeTodo(context.Background(), c.ID); err != nil {
		t.Fatal(err)
	}
	if got := e.s.filedUnder(context.Background(), wordKey{"conv1", "m1"}); got != "" {
		t.Fatalf("a deleted matter still holds its message: %q", got)
	}
}

// A matter's about is saved, kept, updated, cleared and clipped; the tool result keeps reminding the main
// conversation while a matter has none.
func TestConcernFileWritesAbout(t *testing.T) {
	e := newConcernEnv(t)
	t0 := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	e.f.setItems(ownerSaid("m1", "can the summary table be trusted", t0))

	about := "Every row of the summary table must match its run records. It came up because a few passing rows never ran. It counts as done once a sample of twenty all match."
	out, err := e.tool("concern_file", map[string]any{"title": "summary table trust", "about": about})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "about is saved") {
		t.Fatalf("no word of the about: %s", out)
	}
	c := concernNamed(t, e.matters(), "summary table trust")
	if c.Concern == nil || c.Concern.About != about {
		t.Fatalf("about: %+v", c.Concern)
	}

	// Filing another message without about keeps it; now alone keeps it too.
	e.f.setItems(ownerSaid("m2", "a few more rows do not line up", t0.Add(time.Minute)))
	if _, err := e.tool("concern_file", map[string]any{"id": c.ID, "now": "spot-checking the second batch"}); err != nil {
		t.Fatal(err)
	}
	c = e.matters()[c.ID]
	if c.Concern.About != about || c.Concern.Now != "spot-checking the second batch" {
		t.Fatalf("kept: %+v", c.Concern)
	}

	// It is updated, and survives the book being read again (the store keeps it).
	if _, err := e.tool("concern_file", map[string]any{"id": c.ID, "message": "none", "about": "  only the last week's rows.  "}); err != nil {
		t.Fatal(err)
	}
	e.s.concerns.mu.Lock()
	e.s.concerns.loaded = false
	e.s.concerns.mu.Unlock()
	c = e.matters()[c.ID]
	if c.Concern.About != "only the last week's rows." || c.Concern.Now != "spot-checking the second batch" {
		t.Fatalf("updated: %+v", c.Concern)
	}

	// A matter with none says so; "" clears it; a long one is cut.
	e.f.setItems(ownerSaid("m3", "can the trace be viewed", t0.Add(2*time.Minute)))
	if out, err = e.tool("concern_file", map[string]any{"title": "trace viewing"}); err != nil {
		t.Fatal(err)
	} else if !strings.Contains(out, "no about yet") {
		t.Fatalf("no reminder: %s", out)
	}
	if _, err := e.tool("concern_file", map[string]any{"id": c.ID, "message": "none", "about": ""}); err != nil {
		t.Fatal(err)
	}
	if got := e.matters()[c.ID].Concern.About; got != "" {
		t.Fatalf("cleared: %q", got)
	}
	if _, err := e.tool("concern_file", map[string]any{"id": c.ID, "message": "none", "about": strings.Repeat("x", concernAboutMax+50)}); err != nil {
		t.Fatal(err)
	}
	if got := len([]rune(e.matters()[c.ID].Concern.About)); got != concernAboutMax {
		t.Fatalf("kept %d runes", got)
	}
}

// A matter's directions are the leader conversations supervising its tasks, ended ones included, never the main
// conversation or the background supervisor.
func TestConcernDirectionsFromItsTasksSupervisors(t *testing.T) {
	e := newConcernEnv(t)
	ctx := context.Background()
	setSettings(t, e.s, func(v *Settings) {
		v.Main, v.Leaders, v.BackgroundSupervisor = "conv1", []string{"lead_cases", "lead_trace"}, "conv_bg"
	})
	now := time.Now().UTC()
	e.s.directions.mu.Lock()
	if !e.s.directionsLocked(ctx) {
		e.s.directions.mu.Unlock()
		t.Fatal("directions unreadable")
	}
	e.s.directions.m["lead_cases"] = &direction{By: directionByAssistant, At: now, Name: "Cases"}
	e.s.directions.m["lead_trace"] = &direction{By: directionByAssistant, At: now, Name: "Trace"}
	e.s.directions.m["lead_old"] = &direction{By: directionByAssistant, At: now, EndedAt: &now, Name: "Old"}
	e.s.directions.mu.Unlock()

	t0 := now.Add(-time.Hour).Truncate(time.Second)
	e.f.setItems(ownerSaid("m1", "can the summary table be trusted", t0))
	if _, err := e.tool("concern_file", map[string]any{"title": "summary table trust"}); err != nil {
		t.Fatal(err)
	}
	e.f.setItems(ownerSaid("m2", "check the logs too", t0.Add(time.Minute)))
	if _, err := e.tool("concern_file", map[string]any{"title": "logs"}); err != nil {
		t.Fatal(err)
	}
	m := e.matters()
	sheet, logs := concernNamed(t, m, "summary table trust"), concernNamed(t, m, "logs")

	supervised := func(title, partOf, supervisor string) {
		t.Helper()
		task := e.create(title)
		e.must("PATCH", "/api/v1/todos/"+task.ID, `{"part_of":"`+partOf+`"}`)
		e.s.todos.mu.Lock()
		e.s.todos.todos[task.ID].Supervision = &Supervision{Supervisor: supervisor}
		e.s.todos.mu.Unlock()
	}
	supervised("run the cases", sheet.ID, "lead_cases")
	supervised("run the cases again", sheet.ID, "lead_cases") // the same direction once
	supervised("on the trace", sheet.ID, "lead_trace")
	supervised("the old spot check", sheet.ID, "lead_old")
	supervised("the main chat's own", sheet.ID, "conv1")
	supervised("the background supervisor's", sheet.ID, "conv_bg")
	supervised("a logs task", logs.ID, "conv1")

	m = e.matters()
	got := m[sheet.ID].Concern.Directions
	want := []ConcernDirection{{ID: "lead_cases", Name: "Cases"}, {ID: "lead_trace", Name: "Trace"}, {ID: "lead_old", Name: "Old"}}
	if len(got) != len(want) {
		t.Fatalf("directions: %+v", got)
	}
	names := map[string]string{}
	for _, d := range got {
		names[d.ID] = d.Name
	}
	for _, w := range want {
		if names[w.ID] != w.Name {
			t.Fatalf("directions: %+v, want %+v", got, want)
		}
	}
	if d := m[logs.ID].Concern.Directions; len(d) != 0 {
		t.Fatalf("a matter only the main conversation follows has directions: %+v", d)
	}
}

// POST /todos/reorganize never makes a concern part of anything, and a to-do it moves deeper under a matter's request
// still belongs to that matter.
func TestReorganizeUnderAMatter(t *testing.T) {
	e := newConcernEnv(t)
	e.f.setItems(ownerSaid("m1", "plan the move", time.Now().UTC()))
	if _, err := e.tool("concern_file", map[string]any{"title": "the move"}); err != nil {
		t.Fatal(err)
	}
	c := concernNamed(t, e.matters(), "the move")
	req, piece, other := e.create("book the van"), e.create("compare quotes"), e.create("pack the books")
	e.must("PATCH", "/api/v1/todos/"+req.ID, `{"part_of":"`+c.ID+`"}`)
	e.must("PATCH", "/api/v1/todos/"+piece.ID, `{"part_of":"`+req.ID+`"}`)

	if w := e.do("POST", "/api/v1/todos/reorganize", `{"ids":["`+c.ID+`"],"part_of":"`+other.ID+`"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("a concern moved under a task: %d %s", w.Code, w.Body)
	}
	if got := e.matters()[c.ID]; got.PartOf != "" {
		t.Fatalf("a refused reorganize wrote: %q", got.PartOf)
	}
	e.must("POST", "/api/v1/todos/reorganize", `{"ids":["`+other.ID+`"],"part_of":"`+piece.ID+`"}`)
	if got := e.matters()[other.ID]; got.Matter != c.ID {
		t.Fatalf("three levels under the matter: matter %q, want %q", got.Matter, c.ID)
	}
}

// A request root that enforces accepted prerequisites may sit under a matter: it is still the request's root.
func TestEnforcedRequestUnderAMatter(t *testing.T) {
	e := newConcernEnv(t)
	e.f.setItems(ownerSaid("m1", "ship the release", time.Now().UTC()))
	if _, err := e.tool("concern_file", map[string]any{"title": "the release"}); err != nil {
		t.Fatal(err)
	}
	c := concernNamed(t, e.matters(), "the release")
	req := e.create("cut the release")
	e.must("PATCH", "/api/v1/todos/"+req.ID, `{"part_of":"`+c.ID+`"}`)
	e.must("PATCH", "/api/v1/todos/"+req.ID, `{"require_accepted_after":true}`)
	if w := e.do("PATCH", "/api/v1/todos/"+c.ID, `{"require_accepted_after":true}`); w.Code != http.StatusBadRequest {
		t.Fatalf("a concern enforcing prerequisites: %d %s", w.Code, w.Body)
	}
}
