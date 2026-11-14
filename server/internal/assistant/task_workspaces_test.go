package assistant

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/taskworkspace"
	"github.com/nwverse/persona/server/internal/workspacenaming"
)

func TestBackendWorkspaceManagerRecoveryAndLegacyCompatibility(t *testing.T) {
	database, err := db.Open(t.Context(), newDatabase(t), db.ManagerSet)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	f := newFakeHub()
	f.sessions["box"] = nil
	var physical *taskworkspace.Record
	created, starts := 0, 0
	lose := true
	f.nodes = map[string]nodeAnswer{
		"workspace.naming.resolve": func(_ string, raw json.RawMessage) (any, error) {
			var src workspacenaming.Source
			json.Unmarshal(raw, &src)
			return workspacenaming.Resolve(src, workspacenaming.NodeContext{Home: "/home/example"}), nil
		},
		"task.workspace.capabilities": func(string, json.RawMessage) (any, error) {
			return map[string]string{"capability": taskworkspace.Capability}, nil
		},
		"task.workspace.prepare": func(_ string, raw json.RawMessage) (any, error) {
			var req taskworkspace.Request
			json.Unmarshal(raw, &req)
			if physical == nil || physical.OperationID != req.OperationID {
				created++
				r := taskworkspace.Record{SchemaVersion: 1, WorkspaceID: req.WorkspaceID, OperationID: req.OperationID, NodeOperationID: req.OperationID, Plan: req.Plan, InputDigest: taskworkspace.InputKey(req.Plan), State: "ready", Uses: []string{}}
				for i, p := range req.Plan.Repositories {
					r.Repositories = append(r.Repositories, taskworkspace.Instance{RepoID: p.RepoID, Input: req.Plan.Source.Repositories.Repositories[i], Path: p.AbsolutePath, RelativePath: p.RelativePath, TaskBranch: p.TaskBranch, CommonDir: "/home/example/owned.git", ResolvedCommit: strings.Repeat("a", 40), State: "ready"})
				}
				physical = &r
			}
			if lose {
				lose = false
				return nil, errors.New("response lost")
			}
			return *physical, nil
		},
		"task.workspace.inspect": func(string, json.RawMessage) (any, error) {
			if physical == nil {
				return taskworkspace.Record{Error: taskworkspace.Failure("not_found", "inspect", "", "", "")}, nil
			}
			return *physical, nil
		},
		"task.workspace.cancel": func(string, json.RawMessage) (any, error) {
			if physical == nil {
				return taskworkspace.Record{Error: taskworkspace.Failure("not_found", "cancel", "", "", "")}, nil
			}
			r := *physical
			r.State = "cancelled"
			return r, nil
		},
		"task.workspace.cleanup": func(string, json.RawMessage) (any, error) {
			r := *physical
			r.State = "removed"
			r.Cleanup = &taskworkspace.CleanupResult{Removed: true, RetainedPaths: []string{"/home/example/owned.git"}}
			return r, nil
		},
		"task.workspace.acquire": func(string, json.RawMessage) (any, error) { return *physical, nil },
		"task.workspace.release": func(string, json.RawMessage) (any, error) { return *physical, nil },
		DeviceSessionStart: func(string, json.RawMessage) (any, error) {
			starts++
			return nil, errors.New("not part of preparation")
		},
	}
	s, _ := repositoryService(t, database)
	setHub(s, f)
	w := placeDo(s, "", "POST", repoBase, `{"url":"https://github.com/example/web"}`)
	var catalog struct{ Repository RepositoryBinding }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &catalog) != nil {
		t.Fatal(w.Code, w.Body)
	}
	payload := fmt.Sprintf(`{"title":"Example","repository_bindings":{"catalog_revision":1,"repositories":[{"repo_id":%q}]}}`, catalog.Repository.ID)
	w = placeDo(s, "", "POST", "/api/v1/todos", payload)
	var createdTodo struct{ Todo Todo }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &createdTodo) != nil {
		t.Fatal(w.Code, w.Body)
	}
	id := createdTodo.Todo.ID
	base := "/api/v1/todos/" + id + "/backend-workspace"
	input := fmt.Sprintf(`{"operation_id":"op_example","machine_id":"box","layout":"root","selected_repo_ids":[%q]}`, catalog.Repository.ID)
	if w = placeDo(s, "read", "POST", base+"/prepare", input); w.Code != 403 {
		t.Fatal("readonly created resources", w.Code, w.Body)
	}
	never := namingRequest{OperationID: "op_never_sent", MachineID: "box", Layout: "root", SelectedRepoIDs: []string{catalog.Repository.ID}}
	if err = s.ensureBackendWorkspacePlan(t.Context(), id, never); err != nil {
		t.Fatal(err)
	}
	if _, err = database.BeginTaskWorkspace(t.Context(), id, never.OperationID, s.managerIdentity()); err != nil {
		t.Fatal(err)
	}
	if canceled, err := s.CancelBackendWorkspace(t.Context(), id, never.OperationID); err != nil || canceled.State != "cancelled" {
		t.Fatal(canceled, err)
	}
	w = placeDo(s, "", "POST", base+"/prepare", input)
	var first taskworkspace.Record
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &first) != nil || first.State != "preparing" || first.Error == nil || first.Error.Code != "operation_unknown" {
		t.Fatal(w.Code, w.Body)
	}
	restarted := newServiceOn(t, f, database)
	view, err := restarted.InspectBackendWorkspace(t.Context(), id, "op_example")
	if err != nil || view.Observation == nil || view.Observation.State != "ready" {
		t.Fatal(view, err)
	}
	w = placeDo(restarted, "", "POST", base+"/prepare", `{"operation_id":"op_example"}`)
	var ready taskworkspace.Record
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &ready) != nil || ready.State != "ready" || created != 1 || starts != 0 {
		t.Fatal(w.Code, w.Body, created, starts)
	}
	// Metadata edits do not invalidate the physical workspace or change task stage.
	todo, _ := restarted.todoNow(id)
	stage := todo.Stage
	if w = placeDo(restarted, "", "PATCH", "/api/v1/todos/"+id, `{"notes":"Old client edit"}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w = placeDo(restarted, "", "PUT", "/api/v1/todos/"+id+"/repositories", `{"expected_revision":1,"expected_catalog_revision":1,"repositories":[]}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if got, _ := restarted.todoNow(id); got.Stage != stage {
		t.Fatal("preparation changed task stage", got)
	}
	use, err := restarted.AcquireBackendWorkspace(t.Context(), id, "op_example", "session_example")
	if err != nil || use.State != "ready" {
		t.Fatal(use, err)
	}
	if _, err = restarted.CleanupBackendWorkspace(t.Context(), id, "op_example", false, false); err == nil {
		t.Fatal("cleanup ignored use")
	}
	if _, err = restarted.ReleaseBackendWorkspace(t.Context(), id, "op_example", "session_example"); err != nil {
		t.Fatal(err)
	}
	removed, err := restarted.CleanupBackendWorkspace(t.Context(), id, "op_example", false, false)
	if err != nil || removed.State != "removed" || !removed.Cleanup.Removed {
		t.Fatal(removed, err)
	}
	late, err := restarted.PrepareBackendWorkspace(t.Context(), id, "op_example")
	if err != nil || late.State != "removed" || created != 1 {
		t.Fatal("removed operation revived", late, err)
	}
	// The assistant has the same recovery actions without requiring UI steps.
	output, err := restarted.toolTodoWorkspace(t.Context(), "box", "self", json.RawMessage(fmt.Sprintf(`{"id":%q,"action":"inspect"}`, id)))
	if err != nil || !strings.Contains(output, "workspace") {
		t.Fatal(output, err)
	}
}
