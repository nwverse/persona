package assistant

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

func TestTodoResponsibleAPIAndPersistence(t *testing.T) {
	todoStores(t, func(t *testing.T, e *todoEnv, database *db.DB) {
		// Create with a session responsible party (kind lowercased, fields trimmed).
		w := e.must("POST", "/api/v1/todos", `{"title":"Monitor rollout","responsible":{"kind":" Session ","name":" deploy-watch ","machine_id":" host-1 ","session_id":" sess-9 "}}`)
		var created struct{ Todo Todo }
		if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
			t.Fatalf("decode create: %v (%s)", err, w.Body)
		}
		wantSession := TodoResponsible{
			Kind:      db.ResponsibleSession,
			Name:      "deploy-watch",
			MachineID: "host-1",
			SessionID: "sess-9",
		}
		if created.Todo.Responsible == nil || *created.Todo.Responsible != wantSession {
			t.Fatalf("created responsible = %+v, want %+v", created.Todo.Responsible, wantSession)
		}

		// GET single and LIST both include responsible.
		w = e.must("GET", "/api/v1/todos/"+created.Todo.ID, "")
		var fetched struct{ Todo Todo }
		if err := json.Unmarshal(w.Body.Bytes(), &fetched); err != nil || fetched.Todo.Responsible == nil || *fetched.Todo.Responsible != wantSession {
			t.Fatalf("GET single = %s (%v)", w.Body, err)
		}
		if got := e.get(created.Todo.ID); got == nil || got.Responsible == nil || *got.Responsible != wantSession {
			t.Fatalf("GET list = %+v", got)
		}

		// MCP todo_get and todo_list expose responsible.
		getOut, err := e.s.toolTodoGet(context.Background(), "", "", mustMarshalJSON(t, map[string]any{"id": created.Todo.ID}))
		if err != nil {
			t.Fatalf("toolTodoGet: %v", err)
		}
		if !strings.Contains(getOut, `responsible: session "deploy-watch" (host-1/sess-9)`) {
			t.Fatalf("toolTodoGet missing responsible: %s", getOut)
		}
		listOut, err := e.s.toolTodoList(context.Background(), mustMarshalJSON(t, map[string]any{}))
		if err != nil {
			t.Fatalf("toolTodoList: %v", err)
		}
		if !strings.Contains(listOut, "responsible session: deploy-watch") {
			t.Fatalf("toolTodoList missing responsible: %s", listOut)
		}

		// PATCH omitting responsible preserves it.
		e.must("PATCH", "/api/v1/todos/"+created.Todo.ID, `{"notes":"watching metrics"}`)
		if got := e.get(created.Todo.ID); got == nil || got.Responsible == nil || *got.Responsible != wantSession {
			t.Fatalf("after notes-only PATCH = %+v", got)
		}

		// PATCH updates responsible to a person (without machine/session hints).
		w = e.must("PATCH", "/api/v1/todos/"+created.Todo.ID, `{"responsible":{"kind":"person","name":"  Jordan  "}}`)
		var updated struct{ Todo Todo }
		if err := json.Unmarshal(w.Body.Bytes(), &updated); err != nil {
			t.Fatalf("decode patch person: %v", err)
		}
		wantPerson := TodoResponsible{Kind: db.ResponsiblePerson, Name: "Jordan"}
		if updated.Todo.Responsible == nil || *updated.Todo.Responsible != wantPerson {
			t.Fatalf("updated responsible = %+v, want %+v", updated.Todo.Responsible, wantPerson)
		}

		// Reloading from Postgres preserves responsible.
		if database != nil {
			reloaded := New(Options{Hub: e.f, Store: func() Store { return database }, Logf: testLogf(t)})
			got, ok, err := reloaded.todo(context.Background(), created.Todo.ID)
			if err != nil || !ok || got.Responsible == nil || *got.Responsible != wantPerson {
				t.Fatalf("reloaded responsible = %+v (ok=%v err=%v), want %+v", got.Responsible, ok, err, wantPerson)
			}
		}

		// Validation errors on POST and PATCH return 400 and leave stored state unchanged.
		badPayloads := []string{
			`{"responsible":{"kind":"agent","name":"bot"}}`,
			`{"responsible":{"kind":"session","name":""}}`,
			`{"responsible":{"kind":"person","name":"   "}}`,
			`{"responsible":"ops"}`,
			`{"responsible":123}`,
			`{"responsible":{}}`,
			`{"responsible":{"kind":"session","name":"` + strings.Repeat("a", responsibleNameMax+1) + `"}}`,
			`{"responsible":{"kind":"session","name":"ops","machine_id":"` + strings.Repeat("m", responsibleHintMax+1) + `"}}`,
			`{"responsible":{"kind":"session","name":"ops","session_id":"` + strings.Repeat("s", responsibleHintMax+1) + `"}}`,
		}
		for _, body := range badPayloads {
			if w := e.do("PATCH", "/api/v1/todos/"+created.Todo.ID, body); w.Code != http.StatusBadRequest {
				t.Fatalf("PATCH %s = %d %s, want 400", body, w.Code, w.Body)
			}
		}
		if got := e.get(created.Todo.ID); got == nil || got.Responsible == nil || *got.Responsible != wantPerson {
			t.Fatalf("invalid PATCH mutated responsible: %+v", got)
		}
		if w := e.do("POST", "/api/v1/todos", `{"title":"Bad","responsible":{"kind":"bogus","name":"x"}}`); w.Code != http.StatusBadRequest {
			t.Fatalf("POST invalid responsible = %d %s, want 400", w.Code, w.Body)
		}

		// Read-only role cannot PATCH responsible.
		if w := e.doAs("readonly", "PATCH", "/api/v1/todos/"+created.Todo.ID, `{"responsible":null}`); w.Code != http.StatusForbidden {
			t.Fatalf("read-only PATCH = %d %s, want 403", w.Code, w.Body)
		}

		// PATCH responsible: null clears it cleanly.
		w = e.must("PATCH", "/api/v1/todos/"+created.Todo.ID, `{"responsible":null}`)
		var cleared struct{ Todo Todo }
		if err := json.Unmarshal(w.Body.Bytes(), &cleared); err != nil {
			t.Fatalf("decode clear: %v", err)
		}
		if cleared.Todo.Responsible != nil {
			t.Fatalf("cleared responsible = %+v, want nil", cleared.Todo.Responsible)
		}
		if strings.Contains(w.Body.String(), `"responsible"`) {
			t.Fatalf("cleared JSON should omit responsible: %s", w.Body)
		}
		if got := e.get(created.Todo.ID); got == nil || got.Responsible != nil {
			t.Fatalf("GET list after clear = %+v", got)
		}
		if database != nil {
			reloaded := New(Options{Hub: e.f, Store: func() Store { return database }, Logf: testLogf(t)})
			got, ok, err := reloaded.todo(context.Background(), created.Todo.ID)
			if err != nil || !ok || got.Responsible != nil {
				t.Fatalf("reloaded after clear = %+v (ok=%v err=%v), want nil", got.Responsible, ok, err)
			}
		}
	})
}

func TestTodoResponsibleChangedEvents(t *testing.T) {
	todoEventStores(t, func(t *testing.T, e *todoEventEnv, _ *db.DB) {
		// Create with responsible includes it in todo.changed.
		w := e.must("POST", "/api/v1/todos", `{"title":"External run","responsible":{"kind":"session","name":"nightly-bench","machine_id":"gpu-1","session_id":"tmux-7"}}`)
		var created struct{ Todo Todo }
		if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
			t.Fatal(err)
		}
		got := e.settle()
		if len(got) != 1 {
			t.Fatalf("created events = %+v", got)
		}
		ev := changedOf(t, got[0])
		want := assistantwire.TodoEventResponsible{Kind: "session", Name: "nightly-bench", MachineID: "gpu-1", SessionID: "tmux-7"}
		if ev.What != "created" || ev.Responsible == nil || *ev.Responsible != want {
			t.Fatalf("created event = %+v, want responsible %+v", ev, want)
		}

		// Updating responsible emits an updated event.
		e.must("PATCH", "/api/v1/todos/"+created.Todo.ID, `{"responsible":{"kind":"person","name":"Sam"}}`)
		got = e.settle()
		if len(got) != 1 {
			t.Fatalf("updated events = %+v", got)
		}
		ev = changedOf(t, got[0])
		wantPerson := assistantwire.TodoEventResponsible{Kind: "person", Name: "Sam"}
		if ev.What != "updated" || ev.Responsible == nil || *ev.Responsible != wantPerson {
			t.Fatalf("updated event = %+v, want responsible %+v", ev, wantPerson)
		}

		// Clearing responsible emits an updated event with responsible omitted.
		e.must("PATCH", "/api/v1/todos/"+created.Todo.ID, `{"responsible":null}`)
		got = e.settle()
		if len(got) != 1 {
			t.Fatalf("cleared events = %+v", got)
		}
		ev = changedOf(t, got[0])
		if ev.What != "updated" || ev.Responsible != nil || strings.Contains(string(got[0].raw), `"responsible"`) {
			t.Fatalf("cleared event = %s", got[0].raw)
		}
	})
}

func TestDirectionTreeResponsibleStateAndCounts(t *testing.T) {
	e := treeEnv(t)
	now := time.Now()
	fresh := now.Add(-10 * time.Minute)
	stale := now.Add(-3 * time.Hour)

	e.f.setSessions("mac",
		proto.Session{MachineID: "mac", SessionID: "s-needs", State: proto.StateNeedsYou},
		proto.Session{MachineID: "mac", SessionID: "s-work", State: proto.StateWorking},
		proto.Session{MachineID: "mac", SessionID: "s-idle", State: proto.StateIdle, UpdatedAt: stale},
	)

	// 1. Plain task (no linked agent, no responsible) -> planned.
	plainID := e.task("Unassigned planned task", func(td *Todo) {
		td.Supervision = &Supervision{Supervisor: "conv_lead_a"}
		td.UpdatedAt = fresh
	})

	// 2. Responsible set, no linked agent, fresh updated_at -> working (not planned).
	freshRespID := e.task("External session fresh update", func(td *Todo) {
		td.Supervision = &Supervision{Supervisor: "conv_lead_a"}
		td.Responsible = &TodoResponsible{Kind: db.ResponsibleSession, Name: "nightly-runner", MachineID: "box-1", SessionID: "s-ext"}
		td.UpdatedAt = fresh
	})

	// 3. Responsible set, no linked agent, stale created/updated_at but fresh progress note -> working.
	progressRespID := e.task("External person fresh progress", func(td *Todo) {
		td.Supervision = &Supervision{
			Supervisor: "conv_lead_a",
			Progress:   &TaskProgress{Kind: upReportProgress, Text: "Waiting on benchmark run", At: fresh},
		}
		td.Responsible = &TodoResponsible{Kind: db.ResponsiblePerson, Name: "Alex"}
		td.CreatedAt = stale
		td.UpdatedAt = stale
	})

	// 4. Responsible set, no linked agent, stale created/updated_at and no fresh progress -> stuck (not planned).
	staleRespID := e.task("External session stale", func(td *Todo) {
		td.Supervision = &Supervision{Supervisor: "conv_lead_a"}
		td.Responsible = &TodoResponsible{Kind: db.ResponsibleSession, Name: "old-runner"}
		td.CreatedAt = stale
		td.UpdatedAt = stale
	})

	// 5. Responsible set, no linked agent, blocked progress note -> stuck.
	blockedRespID := e.task("External person blocked", func(td *Todo) {
		td.Supervision = &Supervision{
			Supervisor: "conv_lead_a",
			Progress:   &TaskProgress{Kind: upReportBlocked, Text: "Waiting for quota increase", At: fresh},
		}
		td.Responsible = &TodoResponsible{Kind: db.ResponsiblePerson, Name: "Casey"}
		td.UpdatedAt = fresh
	})

	// 6. Responsible set, no linked agent, pending review -> review.
	reviewRespID := e.task("External session pending review", func(td *Todo) {
		td.Supervision = &Supervision{Supervisor: "conv_lead_a", State: reviewPending}
		td.Responsible = &TodoResponsible{Kind: db.ResponsibleSession, Name: "ci-watch"}
		td.UpdatedAt = fresh
	})

	// 7. Responsible set, no linked agent, needs_user -> needs_you.
	needsRespID := e.task("External person needs owner", func(td *Todo) {
		td.Supervision = &Supervision{Supervisor: "conv_lead_a", State: reviewNeedsUser}
		td.Responsible = &TodoResponsible{Kind: db.ResponsiblePerson, Name: "Riley"}
		td.UpdatedAt = fresh
	})

	// 8. Linked Persona agent takes precedence over responsible:
	//    - linked needs_you agent wins over fresh responsible.
	linkedNeedsID := e.task("Linked agent needs you", func(td *Todo) {
		td.Supervision = &Supervision{Supervisor: "conv_lead_b"}
		td.Responsible = &TodoResponsible{Kind: db.ResponsiblePerson, Name: "Alex"}
		td.Agents = []TodoAgent{{MachineID: "mac", SessionID: "s-needs"}}
		td.UpdatedAt = fresh
	})
	//    - linked working agent wins even when responsible has blocked progress note.
	linkedWorkID := e.task("Linked agent working", func(td *Todo) {
		td.Supervision = &Supervision{
			Supervisor: "conv_lead_b",
			Progress:   &TaskProgress{Kind: upReportBlocked, Text: "old note", At: fresh},
		}
		td.Responsible = &TodoResponsible{Kind: db.ResponsibleSession, Name: "ext-1"}
		td.Agents = []TodoAgent{{MachineID: "mac", SessionID: "s-work"}}
		td.CreatedAt = stale
		td.UpdatedAt = stale
	})
	//    - linked stale idle agent is stuck (linked agent precedence).
	linkedStaleID := e.task("Linked idle agent stale", func(td *Todo) {
		td.Supervision = &Supervision{Supervisor: "conv_lead_b"}
		td.Responsible = &TodoResponsible{Kind: db.ResponsibleSession, Name: "ext-2"}
		td.Agents = []TodoAgent{{MachineID: "mac", SessionID: "s-idle"}}
		td.CreatedAt = stale
		td.UpdatedAt = stale
	})

	tree := e.s.directionTree(context.Background(), now)
	if len(tree.Directions) != 3 {
		t.Fatalf("directions = %+v", tree.Directions)
	}

	leadA := tree.Directions[0]
	stateA := map[string]treeTask{}
	for _, task := range leadA.Tasks {
		stateA[task.ID] = task
	}
	wantStatesA := map[string]string{
		plainID:        treePlanned,
		freshRespID:    treeWorking,
		progressRespID: treeWorking,
		staleRespID:    treeStuck,
		blockedRespID:  treeStuck,
		reviewRespID:   treeReview,
		needsRespID:    treeNeedsYou,
	}
	for id, want := range wantStatesA {
		if got := stateA[id].State; got != want {
			t.Errorf("leadA task %s state = %q, want %q", id, got, want)
		}
	}
	wantCountsA := treeCounts{
		NeedsYou: 1,
		Review:   1,
		Stuck:    2,
		Working:  2,
		Planned:  1,
	}
	if leadA.Counts != wantCountsA {
		t.Fatalf("leadA counts = %+v, want %+v", leadA.Counts, wantCountsA)
	}
	if stateA[freshRespID].Responsible == nil || stateA[freshRespID].Responsible.Name != "nightly-runner" || stateA[freshRespID].Responsible.MachineID != "box-1" {
		t.Fatalf("tree task responsible = %+v", stateA[freshRespID].Responsible)
	}
	if stateA[plainID].Responsible != nil {
		t.Fatalf("plain task responsible = %+v, want nil", stateA[plainID].Responsible)
	}

	leadB := tree.Directions[1]
	stateB := map[string]string{}
	for _, task := range leadB.Tasks {
		stateB[task.ID] = task.State
	}
	if stateB[linkedNeedsID] != treeNeedsYou || stateB[linkedWorkID] != treeWorking || stateB[linkedStaleID] != treeStuck {
		t.Fatalf("leadB linked-agent precedence states = %+v", stateB)
	}
	if leadB.Counts != (treeCounts{NeedsYou: 1, Working: 1, Stuck: 1}) || !leadB.Running {
		t.Fatalf("leadB counts/running = %+v running=%v", leadB.Counts, leadB.Running)
	}

	// Clearing responsible via PATCH returns the unlinked task to planned and updates direction tree counts.
	e.must("PATCH", "/api/v1/todos/"+freshRespID, `{"responsible":null}`)
	treeAfter := e.s.directionTree(context.Background(), now)
	leadAAfter := treeAfter.Directions[0]
	var clearedTask treeTask
	for _, task := range leadAAfter.Tasks {
		if task.ID == freshRespID {
			clearedTask = task
		}
	}
	if clearedTask.State != treePlanned || clearedTask.Responsible != nil {
		t.Fatalf("after clearing responsible: task = %+v, want planned & nil responsible", clearedTask)
	}
	wantCountsAfter := treeCounts{
		NeedsYou: 1,
		Review:   1,
		Stuck:    2,
		Working:  1,
		Planned:  2,
	}
	if leadAAfter.Counts != wantCountsAfter {
		t.Fatalf("leadA counts after clear = %+v, want %+v", leadAAfter.Counts, wantCountsAfter)
	}
}

func mustMarshalJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
