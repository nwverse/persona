package assistant

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/identity"
	"github.com/nwverse/persona/server/internal/proto"
)

// The conversations' spaces as the manager notes them (conversation_space.go, #10941): the rule that keeps the health
// space's words from an extension tells a conversation its computer no longer lists (quiet for a week, or past its 100
// newest) and one it has not reported since a restart by what was noted, so the owner's ordinary tasks stay shown, a
// health conversation's stay hidden, and one never noted stays hidden (fail closed). The names and words are made up.

// quietSessions are the assistant's computer's conversations: the main one conv1, an ordinary chat conv2, a direction
// conv_lead that supervises tasks, a health conversation h0, and a run s1 outside the space.
func quietSessions() (main, chat, lead, health, run proto.Session) {
	main, chat = scopeSession("conv1", "Persona", ""), scopeSession("conv2", "Moving house", "")
	lead, health = scopeSession("conv_lead", "Home", ""), scopeSession("h0", "Sleep", identity.HealthDir("/home/owner"))
	main.Assistant, chat.Assistant, lead.Assistant, health.Assistant = true, true, true, true
	return main, chat, lead, health, scopeSession("s1", "Paperwork", "/home/owner/forms")
}

// quietTasks are a task conv2 handed out, one of the main conversation's, one conv_lead supervises, one h0 handed out and
// one from a conversation no computer ever listed, made while their conversations are listed, and a to-do made by hand.
type quietTasks struct{ chat, main, led, health, ghost, own Todo }

func newQuietTasks(t *testing.T, e *todoEnv) quietTasks {
	t.Helper()
	ctx := context.Background()
	main, chat, lead, health, run := quietSessions()
	e.sessions("server", main, chat, lead, health, run)
	task := func(title, origin string) Todo {
		t.Helper()
		got, err := e.s.createTaskTodo(ctx, title, "", origin, "", "")
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	q := quietTasks{chat: task("Book the movers", "conv2"), main: task("Renew the lease", "conv1"),
		led: task("Paint the hall", "conv1"), health: task("Book the sleep clinic", "h0"), ghost: task("Find the deeds", "ghost")}
	e.s.superviseTodo(ctx, q.led.ID, "conv_lead", nil, 0)
	q.own = e.create("Water the plants")
	return q
}

// extensionDo is a request of an extension's token, the hub naming the extension.
func extensionDo(e *todoEnv, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set(RoleHeader, roleExtension)
	r.Header.Set(ExtensionHeader, "example-mover")
	w := httptest.NewRecorder()
	e.s.ServeHTTP(w, r)
	return w
}

// extensionSees says which of ids GET /api/v1/todos answers an extension's token.
func extensionSees(t *testing.T, e *todoEnv, ids ...string) map[string]bool {
	t.Helper()
	w := e.doAs(roleExtension, "GET", "/api/v1/todos", "")
	if w.Code != http.StatusOK {
		t.Fatalf("an extension's list: %d %s", w.Code, w.Body)
	}
	seen := map[string]bool{}
	for _, id := range ids {
		seen[id] = strings.Contains(w.Body.String(), id)
	}
	return seen
}

// wantShown fails unless an extension's list has exactly the to-dos of shown among those of all.
func wantShown(t *testing.T, e *todoEnv, when string, all []Todo, shown ...Todo) {
	t.Helper()
	ids := make([]string, len(all))
	for i, td := range all {
		ids[i] = td.ID
	}
	seen := extensionSees(t, e, ids...)
	for _, td := range all {
		want := false
		for _, s := range shown {
			want = want || s.ID == td.ID
		}
		if seen[td.ID] != want {
			t.Errorf("%s: an extension is shown %q: %v, want %v", when, td.Title, seen[td.ID], want)
		}
	}
}

// The regression check of #10941: a task from a chat its computer no longer lists, one a quiet direction supervises
// and, while the computer lists nothing (a restart, before the node is back), the main conversation's, all stay on an
// extension's list and answer its routes; the health conversation's stays hidden, and so does one from a conversation
// never noted. On the store the spaces outlive a restart of the manager.
func TestQuietConversationsTasksStayShown(t *testing.T) {
	todoStores(t, func(t *testing.T, e *todoEnv, database *db.DB) {
		q := newQuietTasks(t, e)
		all := []Todo{q.chat, q.main, q.led, q.health, q.ghost, q.own}
		wantShown(t, e, "all listed", all, q.chat, q.main, q.led, q.own)

		// A week later the computer lists only the main conversation and the run.
		main, _, _, _, run := quietSessions()
		e.sessions("server", main, run)
		wantShown(t, e, "the chats quiet", all, q.chat, q.main, q.led, q.own)
		writes := []struct{ method, path, body string }{
			{"GET", "/api/v1/todos/" + q.chat.ID, ""},
			{"GET", "/api/v1/todos/" + q.led.ID, ""},
			{"PATCH", "/api/v1/todos/" + q.chat.ID, `{"notes":"two vans"}`},
			{"GET", "/api/v1/assistant/conversations/conv2/feed", ""},
			{"GET", "/api/v1/assistant/conversations/conv_lead/feed", ""},
		}
		if database != nil { // decisions are kept in the database only
			writes = append(writes, struct{ method, path, body string }{"POST", "/api/v1/todos/" + q.chat.ID + "/decisions",
				`{"question":"Which day?","choice":"Saturday"}`})
		}
		for _, w := range writes {
			if got := extensionDo(e, w.method, w.path, w.body); got.Code != http.StatusOK {
				t.Errorf("an extension's %s %s once its chat is quiet: %d %s", w.method, w.path, got.Code, got.Body)
			}
		}
		for _, path := range []string{"/api/v1/todos/" + q.health.ID, "/api/v1/todos/" + q.ghost.ID,
			"/api/v1/assistant/conversations/h0/feed", "/api/v1/assistant/conversations/ghost/feed"} {
			if got := e.doAs(roleExtension, "GET", path, ""); got.Code != http.StatusNotFound {
				t.Errorf("an extension's GET %s: %d %s", path, got.Code, got.Body)
			}
		}

		// The computer lists nothing yet.
		e.sessions("server")
		wantShown(t, e, "nothing listed", all, q.chat, q.main, q.led, q.own)

		if database == nil {
			return
		}
		// The manager starts again on the same store; its computer has not reported.
		again := newTodoEnv(t, database)
		wantShown(t, again, "after a restart", all, q.chat, q.main, q.led, q.own)
	})
}

// No event is lost to the rule: todo.changed and todo.decision of a task whose chat its computer no longer lists, or
// of the main conversation's while it lists nothing, are told; one whose verdict cannot be told yet (its conversation
// never listed) waits, and is told once it can be; the health conversation's is never told.
func TestQuietConversationsEventsAreTold(t *testing.T) {
	todoEventStores(t, func(t *testing.T, e *todoEventEnv, database *db.DB) {
		q := newQuietTasks(t, e.todoEnv)
		// decide writes a decision on to-do td (in the database only: in memory there are none to tell).
		decide := func(td Todo, question, choice string) {
			t.Helper()
			if database != nil {
				e.must("POST", "/api/v1/todos/"+td.ID+"/decisions", `{"question":"`+question+`","choice":"`+choice+`"}`)
			}
		}
		told := func(when string, got []recordedKBEvent, want ...string) {
			t.Helper()
			var names []string
			for _, ev := range got {
				var p struct {
					ID string `json:"id"`
				}
				_ = json.Unmarshal(ev.raw, &p)
				names = append(names, ev.name+" "+p.ID)
			}
			slices.Sort(names)
			want = slices.Sorted(slices.Values(want))
			if !slices.Equal(names, want) {
				t.Errorf("%s: told %v, want %v", when, names, want)
			}
		}
		changed := func(td Todo) string { return assistantwire.EventTodoChanged + " " + td.ID }
		decided := func(td Todo) []string {
			if database == nil {
				return nil
			}
			return []string{assistantwire.EventTodoDecision + " " + td.ID}
		}
		told("created, all listed", e.settle(), changed(q.chat), changed(q.main), changed(q.led), changed(q.own))

		main, _, _, _, run := quietSessions()
		e.sessions("server", main, run)
		e.must("PATCH", "/api/v1/todos/"+q.chat.ID, `{"title":"Book the movers for Saturday"}`)
		e.must("PATCH", "/api/v1/todos/"+q.led.ID, `{"title":"Paint the hall white"}`)
		e.must("PATCH", "/api/v1/todos/"+q.health.ID, `{"title":"Book the sleep clinic soon"}`)
		told("retitled once the chats are quiet", e.settle(), changed(q.chat), changed(q.led))
		decide(q.chat, "Which day?", "Saturday")
		told("a decision once its chat is quiet", e.told(), decided(q.chat)...)

		e.sessions("server")
		e.must("PATCH", "/api/v1/todos/"+q.main.ID, `{"title":"Renew the lease this week"}`)
		told("the main conversation's while nothing is listed", e.settle(), changed(q.main))

		// One never listed: nothing is told of it while its verdict cannot be told, not even after a retry.
		e.must("PATCH", "/api/v1/todos/"+q.ghost.ID, `{"title":"Find the deeds today"}`)
		decide(q.ghost, "Which drawer?", "The top one")
		decide(q.health, "Which night?", "Friday")
		told("never listed", e.settle())
		e.clock.advance(todoRetryAfter)
		told("never listed, judged again", e.told())
		// Its conversation is listed now, outside the space: what waited is told, its creation as it is now and its
		// decision; the health conversation's still is not.
		e.sessions("server", scopeSession("ghost", "Papers", ""))
		e.clock.advance(todoRetryDelay(1))
		got := e.told()
		told("listed at last", got, append(decided(q.ghost), changed(q.ghost))...)
		for _, ev := range got {
			if ev.name != assistantwire.EventTodoChanged {
				continue
			}
			if c := changedOf(t, ev); c.What != assistantwire.TodoCreated || c.Title != "Find the deeds today" {
				t.Errorf("what waited: %+v", c)
			}
		}
		e.clock.advance(todoRetryMost)
		told("nothing more", e.told())
	})
}

// The owner's own records do not pay for the rule: a closed task's outcome goes into its project's timeline when the
// conversation that handed it out is no longer listed and was never noted, as long as nothing says it is the health
// space's (TestHiddenTaskOutcomeStaysOutOfProjects: one that is stays out).
func TestOutcomeLineOfAnUntoldTask(t *testing.T) {
	for _, store := range []string{"memory", "store"} {
		t.Run(store, func(t *testing.T) {
			var st Store
			if store == "store" {
				st = todoTestDB(t)
			}
			s, f, todo := outcomeEnv(t, st, outcomeReply)
			f.setSessions("server") // conv1, never noted, no longer listed
			if v := s.todoVerdicts(context.Background(), []string{todo.ID}, nil, nil)[todo.ID]; v != todoUntold {
				t.Fatalf("the task's verdict: %d, want untold", v)
			}
			f.outcome = `{"outcome":"修好登录页横屏崩溃，PR 已合并","links":["https://github.com/o/weather/pull/12"]}`
			s.recordTaskOutcome(todo, time.Now().UTC())
			if log := kbEng(t, s).Store.Log("天气-app", 10); len(log) != 1 || !strings.Contains(log[0], "修好登录页横屏崩溃") {
				t.Fatalf("the project's timeline: %q", log)
			}
		})
	}
}

// A conversation noted in the health space on any computer is told so wherever else it is noted or listed outside it,
// and so is one listed in the space after it was noted outside it: on the store, a note outside the space is read back
// rather than taken as it was written (convSpaceBook).
func TestConversationSpaceHealthNoteWins(t *testing.T) {
	todoStores(t, func(t *testing.T, e *todoEnv, database *db.DB) {
		ctx := context.Background()
		e.s.noteConversationSpaces(ctx, []db.ConversationSpace{{MachineID: "laptop", SessionID: "x", Space: SpaceHealth}})
		chat := scopeSession("x", "Sleep", "")
		chat.Assistant = true
		e.sessions("server", chat) // listed outside the space on the assistant's computer
		moved := scopeSession("y", "Moving house", "")
		moved.Assistant = true
		e.sessions("server", chat, moved)
		if got := e.s.conversationSpaces(ctx, []string{"y"})["y"]; !got.known || got.space != "" {
			t.Fatalf("y listed outside the space: %+v", got)
		}
		moved.Space = SpaceHealth
		e.sessions("server", chat, moved)
		e.sessions("server")
		got := e.s.conversationSpaces(ctx, []string{"x", "y"})
		for _, id := range []string{"x", "y"} {
			if !got[id].known || got[id].space != SpaceHealth {
				t.Errorf("%s: %+v, want the health space", id, got[id])
			}
		}
		if database == nil {
			return
		}
		// The manager starts again, and its computer lists x outside the space first.
		again := newTodoEnv(t, database)
		again.sessions("server", chat)
		got = again.s.conversationSpaces(ctx, []string{"x", "y"})
		for _, id := range []string{"x", "y"} {
			if !got[id].known || got[id].space != SpaceHealth {
				t.Errorf("after a restart, %s: %+v, want the health space", id, got[id])
			}
		}
	})
}

// The conversations open tasks came from and those that supervise them are listed however quiet (keptSessions), so
// one quiet since before the manager noted spaces is noted once its computer lists it again; a done task's are not, nor
// is the main conversation twice, and a task leader's agent is kept on its own computer.
func TestOpenTasksConversationsAreKept(t *testing.T) {
	e := newTodoEnv(t, nil)
	ctx := context.Background()
	setSettings(t, e.s, func(v *Settings) { v.Main = "conv1" })
	task := func(title, origin string) Todo {
		t.Helper()
		got, err := e.s.createTaskTodo(ctx, title, "", origin, "", "")
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	task("Book the movers", "conv2")
	led := task("Paint the hall", "conv1")
	e.s.superviseTodo(ctx, led.ID, "conv_lead", nil, 0)
	byLeader := task("Fix the gate", "conv3")
	e.s.superviseTodo(ctx, byLeader.ID, "laptop/lead1", nil, 0)
	done := task("Find the deeds", "conv_old")
	e.must("PATCH", "/api/v1/todos/"+done.ID, `{"done":true}`)
	p := e.s.profile(ctx, []TaskView{}, &TodoReport{})
	if p == nil {
		t.Fatal("no profile")
	}
	want := []SessionRef{{MachineID: "laptop", SessionID: "lead1"}, {MachineID: "server", SessionID: "conv1"},
		{MachineID: "server", SessionID: "conv2"}, {MachineID: "server", SessionID: "conv3"},
		{MachineID: "server", SessionID: "conv_lead"}}
	got := slices.SortedFunc(slices.Values(p.Keep), func(a, b SessionRef) int {
		return strings.Compare(a.MachineID+"/"+a.SessionID, b.MachineID+"/"+b.SessionID)
	})
	if !slices.Equal(got, want) {
		t.Fatalf("kept %v, want %v", got, want)
	}
}
