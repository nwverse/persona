package assistant

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/workspacenaming"
)

func TestWorkspaceNamingSettingsPersistAndOldClientsKeepWorking(t *testing.T) {
	database, err := db.Open(t.Context(), newDatabase(t), db.ManagerSet)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	f := newFakeHub()
	s := newServiceOn(t, f, database)
	put := func(body string, owner bool) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("PUT", "/api/v1/assistant", strings.NewReader(body))
		if owner {
			r.Header.Set(assistantwire.ManagerHeader, s.managerIdentity())
		}
		s.ServeHTTP(w, r)
		return w
	}
	patch := `{"workspace_rules":{"branch_template":"custom/{task-id}","base_directory":"{home}/projects"}}`
	if w := put(patch, false); w.Code != 400 {
		t.Fatal("unowned settings", w.Code, w.Body)
	}
	if w := put(patch, true); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w := put(`{"notes":"Old client edit","task_workspace_preference":"prefer-new"}`, false); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	restarted := newServiceOn(t, f, database)
	if got := restarted.settings(t.Context()); got.WorkspaceRules.BranchTemplate != "custom/{task-id}" || got.TaskWorkspacePreference != "prefer-new" {
		t.Fatal(got)
	}
	if w := put(`{"workspace_rules":{"branch_template":null}}`, true); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w := put(`{"workspace_rules":{"task_directory_template":"{branch}"}}`, true); w.Code != 400 {
		t.Fatal(w.Code, w.Body)
	}
}

func TestWorkspaceNamingHTTPOverridesAndStableOperationRetry(t *testing.T) {
	database, err := db.Open(t.Context(), newDatabase(t), db.ManagerSet)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	f := newFakeHub()
	f.sessions["box"] = nil
	calls := 0
	f.nodes = map[string]nodeAnswer{"workspace.naming.resolve": func(_ string, raw json.RawMessage) (any, error) {
		calls++
		var source workspacenaming.Source
		if err := json.Unmarshal(raw, &source); err != nil {
			return nil, err
		}
		return workspacenaming.Resolve(source, workspacenaming.NodeContext{Home: "/home/example"}), nil
	}}
	s := newServiceOn(t, f, database)
	todo, err := s.createPlainTodo(t.Context(), "Original title", "", db.TodoEdit{})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/todos/" + todo.ID
	if w := placeDo(s, "", "PUT", path+"/workspace-rules", `{"expected_revision":0,"workspace_rules":{"task_directory_template":"{yyyy}{mmdd}/{task-id}-{task-slug}"}}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	in := `{"machine_id":"box","layout":"container","selected_repo_ids":[],"operation_id":"example-operation"}`
	w := placeDo(s, "", "POST", path+"/workspace-naming/plans", in)
	var plan workspacenaming.Plan
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &plan) != nil || plan.Digest == "" {
		t.Fatal(w.Code, w.Body)
	}
	if w := placeDo(s, "", "PATCH", path, `{"title":"Changed title"}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w := placeDo(s, "", "PUT", path+"/workspace-rules", `{"expected_revision":1,"workspace_rules":null}`); w.Code != 200 {
		t.Fatal("Todo permanently frozen", w.Code, w.Body)
	}
	restarted := newServiceOn(t, f, database)
	w = placeDo(restarted, "", "POST", path+"/workspace-naming/plans", in)
	var retry workspacenaming.Plan
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &retry) != nil || retry.Digest != plan.Digest || calls != 1 {
		t.Fatal(w.Code, w.Body, calls)
	}
	if w := placeDo(s, "read", "PUT", path+"/workspace-rules", `{"expected_revision":2,"workspace_rules":null}`); w.Code != 403 {
		t.Fatal("readonly changed", w.Code)
	}
}
