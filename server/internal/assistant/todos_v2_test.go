package assistant

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
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
