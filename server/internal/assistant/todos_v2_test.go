package assistant

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
)

func TestTodosV2SettingsLifecycle(t *testing.T) {
	s, _, _ := briefService(t, "Prepare the release checklist")
	if out, bad := tool(t, s, "todo_create", map[string]any{"title": "Release checklist"}); bad {
		t.Fatal(out)
	}
	item, ok := todoByTitle(s, "Release checklist")
	if !ok {
		t.Fatal("missing checklist")
	}

	put := func(manager, role, body string, status int) string {
		t.Helper()
		w, _ := permissionSettingsRequest(t, s, "PUT", "/api/v1/assistant", manager, role, body)
		if w.Code != status {
			t.Fatalf("PUT %s (manager=%q role=%q): got %d, want %d: %s", body, manager, role, w.Code, status, w.Body.String())
		}
		return w.Body.String()
	}

	// Default is false.
	w, state := permissionSettingsRequest(t, s, "GET", "/api/v1/assistant", s.managerIdentity(), "full", "")
	if w.Code != http.StatusOK || state.TodosV2 {
		t.Fatalf("GET default: code=%d todos_v2=%t", w.Code, state.TodosV2)
	}

	// Cross-manager and read-only role are rejected with 403; missing manager target returns 400.
	put("another", "full", `{"todos_v2":true}`, http.StatusForbidden)
	put("", "full", `{"todos_v2":true}`, http.StatusBadRequest)
	put(s.managerIdentity(), "read", `{"todos_v2":true}`, http.StatusForbidden)

	// Enable with matching optimistic lock.
	put(s.managerIdentity(), "full", `{"todos_v2":true,"expected_todos_v2":false}`, http.StatusOK)
	w, state = permissionSettingsRequest(t, s, "GET", "/api/v1/assistant", s.managerIdentity(), "full", "")
	if w.Code != http.StatusOK || !state.TodosV2 {
		t.Fatalf("GET after enable: code=%d todos_v2=%t", w.Code, state.TodosV2)
	}

	// Stale expected_todos_v2 returns 409 configuration_conflict without changing state.
	conflictBody := put(s.managerIdentity(), "full", `{"todos_v2":false,"expected_todos_v2":false}`, http.StatusConflict)
	var errResp struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal([]byte(conflictBody), &errResp); err != nil || errResp.Code != "configuration_conflict" {
		t.Fatalf("unexpected conflict response: %s", conflictBody)
	}

	// Lossless rollback to false with matching optimistic lock.
	put(s.managerIdentity(), "full", `{"todos_v2":false,"expected_todos_v2":true}`, http.StatusOK)
	w, state = permissionSettingsRequest(t, s, "GET", "/api/v1/assistant", s.managerIdentity(), "full", "")
	if w.Code != http.StatusOK || state.TodosV2 {
		t.Fatalf("GET after rollback: code=%d todos_v2=%t", w.Code, state.TodosV2)
	}

	// Toggling performs zero destructive migration on existing to-dos.
	after, ok := todoByTitle(s, "Release checklist")
	if !ok || !reflect.DeepEqual(after, item) {
		t.Fatalf("setting change mutated saved to-do: got %+v, want %+v", after, item)
	}
}

func TestTodosV2PersistsForSelectedManager(t *testing.T) {
	st := &memStore{}
	f := newFakeHub().withMachine()
	s := newServiceOn(t, f, st)
	s.id = "example-manager"

	w, _ := permissionSettingsRequest(t, s, "PUT", "/api/v1/assistant", s.id, "full", `{"todos_v2":true,"expected_todos_v2":false}`)
	if w.Code != http.StatusOK {
		t.Fatal(w.Body.String())
	}

	reloaded := newServiceOn(t, f, st)
	reloaded.id = s.id
	w, view := permissionSettingsRequest(t, reloaded, "GET", "/api/v1/assistant", s.id, "full", "")
	if w.Code != http.StatusOK || view.Manager != s.id || !view.TodosV2 {
		t.Fatalf("lost manager preference on reload: code=%d manager=%q todos_v2=%t", w.Code, view.Manager, view.TodosV2)
	}
}

// TestTodosV2AudienceLosslessRollback verifies the full additive audience lifecycle (#14619):
// todos_v2 off (baseline) -> enable todos_v2 -> write human/autonomous via HTTP API & MCP tools
// (todo_create, todo_update, task_plan) -> disable todos_v2 -> assert zero data loss, untouched legacy
// wire fields & identical projection/counts -> re-enable todos_v2 -> assert audience values survive intact.
func TestTodosV2AudienceLosslessRollback(t *testing.T) {
	s, _, _ := briefService(t, "Prepare the architecture memo and run CI cleanup")

	req := func(s *Service, method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set(RoleHeader, "full")
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}

	setTodosV2 := func(want bool, expected bool) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"todos_v2": want, "expected_todos_v2": expected})
		w, _ := permissionSettingsRequest(t, s, "PUT", "/api/v1/assistant", s.managerIdentity(), "full", string(body))
		if w.Code != http.StatusOK {
			t.Fatalf("PUT todos_v2=%t: code=%d body=%s", want, w.Code, w.Body.String())
		}
	}
	getTodos := func() (map[string]map[string]any, map[string]any) {
		t.Helper()
		w := req(s, "GET", "/api/v1/todos", "")
		if w.Code != http.StatusOK {
			t.Fatalf("GET /api/v1/todos: code=%d body=%s", w.Code, w.Body.String())
		}
		var payload struct {
			Todos  []map[string]any `json:"todos"`
			Counts map[string]any   `json:"counts"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
			t.Fatalf("decode GET /api/v1/todos: %v", err)
		}
		byID := make(map[string]map[string]any, len(payload.Todos))
		for _, item := range payload.Todos {
			id, _ := item["id"].(string)
			byID[id] = item
		}
		return byID, payload.Counts
	}
	getTodoItem := func(id string) map[string]any {
		t.Helper()
		w := req(s, "GET", "/api/v1/todos/"+id, "")
		if w.Code != http.StatusOK {
			t.Fatalf("GET /api/v1/todos/%s: code=%d body=%s", id, w.Code, w.Body.String())
		}
		var payload struct {
			Todo map[string]any `json:"todo"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
			t.Fatalf("decode GET /api/v1/todos/%s: %v", id, err)
		}
		return payload.Todo
	}

	// 1. Baseline with todos_v2=false: legacy to-do omits "audience" on the wire.
	w := req(s, "POST", "/api/v1/todos", `{"title":"Legacy baseline item","notes":"created before todos_v2"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("POST legacy todo: code=%d body=%s", w.Code, w.Body.String())
	}
	var legacyResp struct {
		Todo Todo `json:"todo"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &legacyResp); err != nil {
		t.Fatal(err)
	}
	legacyID := legacyResp.Todo.ID
	beforeRows, _ := getTodos()
	if _, hasKey := beforeRows[legacyID]["audience"]; hasKey {
		t.Fatalf("legacy todo should omit empty audience key on wire, got: %v", beforeRows[legacyID])
	}

	// 2. Enable todos_v2.
	setTodosV2(true, false)

	// 3a. HTTP POST with audience="human" and invalid audience rejection.
	w = req(s, "POST", "/api/v1/todos", `{"title":"Bad audience","audience":"robot"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("POST invalid audience: got %d, want 400: %s", w.Code, w.Body.String())
	}
	w = req(s, "POST", "/api/v1/todos", `{"title":"HTTP human deliverable","audience":"human"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("POST human todo: code=%d body=%s", w.Code, w.Body.String())
	}
	var httpHumanResp struct {
		Todo Todo `json:"todo"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &httpHumanResp); err != nil {
		t.Fatal(err)
	}
	httpHumanID := httpHumanResp.Todo.ID
	if httpHumanResp.Todo.Audience != db.AudienceHuman {
		t.Fatalf("POST human audience = %q, want %q", httpHumanResp.Todo.Audience, db.AudienceHuman)
	}

	// 3b. HTTP PATCH updates audience and rejects invalid value.
	w = req(s, "PATCH", "/api/v1/todos/"+httpHumanID, `{"audience":"invalid"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("PATCH invalid audience: got %d, want 400: %s", w.Code, w.Body.String())
	}
	w = req(s, "PATCH", "/api/v1/todos/"+httpHumanID, `{"audience":"autonomous"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH autonomous: code=%d body=%s", w.Code, w.Body.String())
	}
	w = req(s, "PATCH", "/api/v1/todos/"+httpHumanID, `{"audience":"human"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH back to human: code=%d body=%s", w.Code, w.Body.String())
	}

	// 3c. MCP todo_create & todo_update (including clearing with "" and rejecting invalid values).
	if out, bad := tool(t, s, "todo_create", map[string]any{"title": "Invalid MCP audience", "audience": "unknown"}); !bad {
		t.Fatalf("todo_create invalid audience should fail, got: %s", out)
	}
	if out, bad := tool(t, s, "todo_create", map[string]any{"title": "MCP autonomous task", "audience": "autonomous"}); bad {
		t.Fatalf("todo_create autonomous: %s", out)
	}
	mcpAuto, ok := todoByTitle(s, "MCP autonomous task")
	if !ok || mcpAuto.Audience != db.AudienceAutonomous {
		t.Fatalf("todo_create autonomous saved = %+v (ok=%t)", mcpAuto, ok)
	}
	if out, bad := tool(t, s, "todo_create", map[string]any{"title": "MCP cleared later", "audience": "human"}); bad {
		t.Fatalf("todo_create clear target: %s", out)
	}
	mcpCleared, ok := todoByTitle(s, "MCP cleared later")
	if !ok || mcpCleared.Audience != db.AudienceHuman {
		t.Fatalf("todo_create clear target saved = %+v (ok=%t)", mcpCleared, ok)
	}
	if out, bad := tool(t, s, "todo_update", map[string]any{"id": mcpCleared.ID, "audience": "invalid"}); !bad {
		t.Fatalf("todo_update invalid audience should fail, got: %s", out)
	}
	if out, bad := tool(t, s, "todo_update", map[string]any{"id": mcpCleared.ID, "audience": ""}); bad {
		t.Fatalf("todo_update clear audience: %s", out)
	}
	mcpClearedAfter, _ := s.todoNow(mcpCleared.ID)
	if mcpClearedAfter.Audience != "" {
		t.Fatalf("todo_update clear audience left %q, want empty", mcpClearedAfter.Audience)
	}

	// 3d. MCP task_plan with audience (valid & invalid).
	if out, bad := tool(t, s, "task_plan", map[string]any{
		"tasks": []map[string]any{{
			"title": "Bad plan audience", "prompt": "inspect", "machine": "box", "cwd": "/tmp",
			"readonly": true, "role": "worker", "goal": "check", "audience": "bogus",
		}},
	}); !bad || !strings.Contains(out, "audience") {
		t.Fatalf("task_plan invalid audience should fail, bad=%t out=%s", bad, out)
	}
	if out, bad := tool(t, s, "task_plan", map[string]any{
		"tasks": []map[string]any{{
			"title": "Audit release pipeline status", "prompt": "inspect release pipeline status and report findings", "machine": "box", "cwd": "/tmp",
			"readonly": true, "role": "worker", "goal": "verify release pipeline state", "audience": "autonomous",
		}},
	}); bad {
		t.Fatalf("task_plan autonomous failed: %s", out)
	}
	var plannedTodo Todo
	var foundPlanned bool
	s.todos.mu.RLock()
	for _, td := range s.todos.todos {
		if td.PlanID != "" {
			plannedTodo = copyTodoLocked(td)
			foundPlanned = true
			break
		}
	}
	s.todos.mu.RUnlock()
	if !foundPlanned || plannedTodo.Audience != db.AudienceAutonomous {
		t.Fatalf("task_plan autonomous todo = %+v (found=%t)", plannedTodo, foundPlanned)
	}

	// Snapshot list, views, and counts while todos_v2=true.
	onRows, onCounts := getTodos()
	if len(onRows) != 5 {
		t.Fatalf("expected 5 todos while todos_v2=true, got %d", len(onRows))
	}

	// 4. Disable todos_v2 (rollback).
	setTodosV2(false, true)

	// 5. Assert zero data loss, identical projection/counts, untouched legacy wire fields, and clean single-item read.
	offRows, offCounts := getTodos()
	if len(offRows) != len(onRows) {
		t.Fatalf("rollback changed todo count: off=%d on=%d", len(offRows), len(onRows))
	}
	if !reflect.DeepEqual(offCounts, onCounts) {
		t.Fatalf("rollback changed counts: off=%v on=%v", offCounts, onCounts)
	}
	for id, onRow := range onRows {
		offRow, exists := offRows[id]
		if !exists {
			t.Fatalf("todo %s lost after rollback", id)
		}
		if !reflect.DeepEqual(offRow["view"], onRow["view"]) {
			t.Fatalf("todo %s view changed on rollback: off=%v on=%v", id, offRow["view"], onRow["view"])
		}
		for _, legacyKey := range []string{"id", "title", "notes", "created_at", "updated_at", "spaces", "agents", "quiet", "view"} {
			if _, ok := offRow[legacyKey]; !ok {
				t.Errorf("todo %s missing legacy wire key %q after rollback", id, legacyKey)
			}
		}
	}
	if _, hasKey := offRows[legacyID]["audience"]; hasKey {
		t.Fatalf("legacy todo unexpectedly gained audience key after rollback: %v", offRows[legacyID])
	}
	if _, hasKey := offRows[mcpCleared.ID]["audience"]; hasKey {
		t.Fatalf("cleared todo unexpectedly kept audience key after rollback: %v", offRows[mcpCleared.ID])
	}
	singleHuman := getTodoItem(httpHumanID)
	if singleHuman["audience"] != db.AudienceHuman {
		t.Fatalf("GET /api/v1/todos/%s audience = %v, want %q", httpHumanID, singleHuman["audience"], db.AudienceHuman)
	}

	// 6. Re-enable todos_v2 and confirm all stored audience values remain intact.
	setTodosV2(true, false)
	reEnabledRows, reEnabledCounts := getTodos()
	if !reflect.DeepEqual(reEnabledCounts, onCounts) {
		t.Fatalf("counts changed after re-enable: got=%v want=%v", reEnabledCounts, onCounts)
	}
	wantAudience := map[string]string{
		legacyID:        "",
		mcpCleared.ID:   "",
		httpHumanID:     db.AudienceHuman,
		mcpAuto.ID:      db.AudienceAutonomous,
		plannedTodo.ID:  db.AudienceAutonomous,
	}
	for id, want := range wantAudience {
		td, ok := s.todoNow(id)
		if !ok {
			t.Fatalf("todo %s missing after re-enable", id)
		}
		if td.Audience != want {
			t.Fatalf("todo %s audience after re-enable = %q, want %q", id, td.Audience, want)
		}
		gotWire, _ := reEnabledRows[id]["audience"].(string)
		if gotWire != want {
			t.Fatalf("todo %s wire audience after re-enable = %q, want %q", id, gotWire, want)
		}
	}
	// Ensure context-based read also sees todos_v2 enabled cleanly.
	if !s.settings(context.Background()).todosV2On() {
		t.Fatalf("expected todosV2On to be true after re-enable")
	}
}

func TestTodosV2CriticalPathRankingAndRollback(t *testing.T) {
	s, _, _ := briefService(t, "Critical path ranking regression")
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	older, err := s.createPieceTodo(ctx, "conv1", "Isolated older ask", "", db.TodoEdit{})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := s.createPieceTodo(ctx, "conv1", "Release rollout", "", db.TodoEdit{})
	if err != nil {
		t.Fatal(err)
	}
	partOf := parent.ID
	blocker, err := s.createPieceTodo(ctx, "conv1", "Critical path blocker", "", db.TodoEdit{PartOf: &partOf})
	if err != nil {
		t.Fatal(err)
	}
	afterBlocker := []string{blocker.ID}
	mid, err := s.createPieceTodo(ctx, "conv1", "Downstream step 1", "", db.TodoEdit{PartOf: &partOf, After: &afterBlocker})
	if err != nil {
		t.Fatal(err)
	}
	afterMid := []string{mid.ID}
	if _, err := s.createPieceTodo(ctx, "conv1", "Downstream step 2", "", db.TodoEdit{PartOf: &partOf, After: &afterMid}); err != nil {
		t.Fatal(err)
	}

	// Mark both older (-3h) and blocker (-1h) as needing the owner via needs_user review.
	s.todos.mu.Lock()
	olderAt := now.Add(-3 * time.Hour)
	s.todos.todos[older.ID].Supervision = &Supervision{
		State:     reviewNeedsUser,
		UpdatedAt: olderAt,
		Reviews:   []TaskReview{{At: olderAt, Verdict: reviewNeedsUser, Reason: "Approve isolated task?"}},
	}
	blockerAt := now.Add(-1 * time.Hour)
	s.todos.todos[blocker.ID].Supervision = &Supervision{
		State:     reviewNeedsUser,
		UpdatedAt: blockerAt,
		Reviews:   []TaskReview{{At: blockerAt, Verdict: reviewNeedsUser, Reason: "Unblock downstream chain?"}},
	}
	s.todos.mu.Unlock()

	setTodosV2 := func(want, expected bool) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"todos_v2": want, "expected_todos_v2": expected})
		w, _ := permissionSettingsRequest(t, s, "PUT", "/api/v1/assistant", s.managerIdentity(), "full", string(body))
		if w.Code != http.StatusOK {
			t.Fatalf("PUT todos_v2=%t: code=%d body=%s", want, w.Code, w.Body.String())
		}
	}
	getViews := func() map[string]assistantwire.TodoSummary {
		t.Helper()
		r := httptest.NewRequest("GET", "/api/v1/todos", nil)
		r.Header.Set(RoleHeader, "full")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("GET /api/v1/todos: code=%d body=%s", w.Code, w.Body.String())
		}
		var resp struct {
			Todos []struct {
				ID   string                     `json:"id"`
				View *assistantwire.TodoSummary `json:"view"`
			} `json:"todos"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		out := make(map[string]assistantwire.TodoSummary, len(resp.Todos))
		for _, td := range resp.Todos {
			if td.View != nil {
				out[td.ID] = *td.View
			}
		}
		return out
	}

	// Default (todos_v2=false): older ask ranks ahead of newer blocker.
	legacyViews := getViews()
	if legacyViews[older.ID].Section != assistantwire.TodoListSectionNeedsYou ||
		legacyViews[blocker.ID].Section != assistantwire.TodoListSectionNeedsYou {
		t.Fatalf("expected both in needs_you: older=%+v blocker=%+v", legacyViews[older.ID], legacyViews[blocker.ID])
	}
	if !(legacyViews[older.ID].Rank < legacyViews[blocker.ID].Rank) {
		t.Fatalf("legacy order mismatch: older.rank=%d blocker.rank=%d",
			legacyViews[older.ID].Rank, legacyViews[blocker.ID].Rank)
	}

	// Enable todos_v2: blocker (2 downstream open tasks) ranks ahead of older (0 downstream).
	setTodosV2(true, false)
	v2Views := getViews()
	if !(v2Views[blocker.ID].Rank < v2Views[older.ID].Rank) {
		t.Fatalf("todos_v2 critical-path order mismatch: blocker.rank=%d older.rank=%d",
			v2Views[blocker.ID].Rank, v2Views[older.ID].Rank)
	}

	// Disable todos_v2: legacy order and views are restored instantaneously.
	setTodosV2(false, true)
	rollbackViews := getViews()
	if !reflect.DeepEqual(rollbackViews, legacyViews) {
		t.Fatalf("rollback views mismatch:\ngot:  %+v\nwant: %+v", rollbackViews, legacyViews)
	}
}
