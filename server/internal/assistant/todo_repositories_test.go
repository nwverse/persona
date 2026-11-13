package assistant

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/assistant/repository"
	"github.com/nwverse/persona/server/internal/db"
)

func TestTodoRepositoryHTTPJourneyAndLegacyCompatibility(t *testing.T) {
	dsn := newDatabase(t)
	database, err := db.Open(t.Context(), dsn, db.ManagerSet)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	s, _ := repositoryService(t, database)
	w := placeDo(s, "", "POST", repoBase, `{"url":"https://github.com/example/web"}`)
	var saved struct{ Repository RepositoryBinding }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &saved) != nil {
		t.Fatal(w.Code, w.Body)
	}
	repoID := saved.Repository.ID
	payload := fmt.Sprintf(`{"title":"Linked work","repository_bindings":{"catalog_revision":1,"repositories":[{"repo_id":%q,"links":[{"kind":"pull_request","number":"7"},{"kind":"pull_request","number":"8"}]}]}}`, repoID)
	w = placeDo(s, "", "POST", "/api/v1/todos", payload)
	var created struct{ Todo Todo }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &created) != nil || created.Todo.ID == "" {
		t.Fatal(w.Code, w.Body)
	}
	id := created.Todo.ID
	path := "/api/v1/todos/" + id + "/repositories"
	read := func(service *Service) repository.TaskSet {
		t.Helper()
		w := placeDo(service, "", "GET", path, "")
		var set repository.TaskSet
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &set) != nil {
			t.Fatal(w.Code, w.Body)
		}
		return set
	}
	original := read(s)
	if original.Manager != s.managerIdentity() || len(original.Repositories) != 1 || len(original.Repositories[0].Links) != 2 || original.Repositories[0].RequestedRef.Kind != "default" {
		t.Fatal(original)
	}
	if w := placeDo(s, "", "PATCH", "/api/v1/todos/"+id, `{"notes":"Old client update"}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	restarted, _ := repositoryService(t, database)
	if got := read(restarted); len(got.Repositories[0].Links) != 2 || got.Revision != original.Revision {
		t.Fatal("restart/old client lost links", got)
	}
	// Existing task metadata, legacy PR progress and workspace admission remain untouched.
	s.todos.mu.Lock()
	s.todos.todos[id].Stage = "review"
	s.todos.todos[id].PRURL = "https://github.com/example/web/pull/99"
	s.todos.mu.Unlock()
	w = placeDo(s, "", "PUT", path+"/"+repoID+"/links", `{"expected_revision":1,"links":[{"kind":"issue","number":"7"}]}`)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w := placeDo(s, "", "PUT", path, `{"expected_revision":2,"expected_catalog_revision":1,"repositories":[]}`); w.Code != 200 {
		t.Fatal("task associations permanently frozen", w.Code, w.Body)
	}
	todo, _ := s.todoNow(id)
	if todo.Stage != "review" || todo.PRURL != "https://github.com/example/web/pull/99" || len(read(s).Repositories) != 0 {
		t.Fatal("association edit changed task progress", todo)
	}
	// Invalid create rolls back the Todo instead of returning a partial success.
	w = placeDo(s, "", "POST", "/api/v1/todos", strings.Replace(payload, repoID, "foreign_repo", 1))
	if w.Code != 404 {
		t.Fatal(w.Code, w.Body)
	}
	rows, err := database.ListTodos(t.Context())
	if err != nil || len(rows) != 1 {
		t.Fatal("partial create", len(rows), err)
	}
	for _, bad := range []string{`null`, `{"expected_revision":3,"expected_catalog_revision":1,"repositories":null}`, `{"expected_revision":3,"expected_catalog_revision":1,"repositories":[],"manager":"other"}`, `{"expected_revision":3,"expected_catalog_revision":1,"repositories":[]} {}`} {
		if w := placeDo(s, "", "PUT", path, bad); w.Code != 400 {
			t.Fatal(bad, w.Code, w.Body)
		}
	}
	if w := placeDo(s, "read", "PUT", path, `{"expected_revision":3,"expected_catalog_revision":1,"repositories":[]}`); w.Code != 403 {
		t.Fatal("read-only write", w.Code, w.Body)
	}
	if w := placeDo(s, "", "PUT", path, `{"expected_revision":1,"expected_catalog_revision":1,"repositories":[]}`); w.Code != 409 || !strings.Contains(w.Body.String(), "task_repositories_revision_conflict") {
		t.Fatal(w.Code, w.Body)
	}
	// Zero-repository legacy creation still needs neither a catalog nor a new store capability.
	legacy, _ := repositoryService(t, ruleStore{&memStore{}})
	if w := placeDo(legacy, "", "POST", "/api/v1/todos", `{"title":"Ordinary work"}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w := placeDo(legacy, "", "POST", "/api/v1/todos", `{"title":"Requires atomic storage","repository_bindings":{"catalog_revision":0,"repositories":[]}}`); w.Code != 503 {
		t.Fatal(w.Code, w.Body)
	}
}

func TestTodoRepositoryToolsAndTransportCompatibility(t *testing.T) {
	dsn := newDatabase(t)
	database, err := db.Open(t.Context(), dsn, db.ManagerSet)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	s, _ := repositoryService(t, database)
	w := placeDo(s, "", "POST", repoBase, `{"url":"https://github.com/example/web","clone_url":"git@`+`github.com:example/web.git"}`)
	var saved struct{ Repository RepositoryBinding }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &saved) != nil {
		t.Fatal(w.Code, w.Body)
	}
	id := saved.Repository.ID
	if w := placeDo(s, "", "PUT", repoBase+"/"+id, `{"url":"https://github.com/example/web","name":"Renamed"}`); w.Code != 200 || !strings.Contains(w.Body.String(), "git@"+"github.com:example/web.git") {
		t.Fatal("old catalog client lost transport", w.Code, w.Body)
	}
	args := fmt.Sprintf(`{"title":"Tool work","repository_bindings":{"catalog_revision":2,"repositories":[{"repo_id":%q}]}}`, id)
	if _, err := s.toolTodoCreate(t.Context(), "machine", "conversation", json.RawMessage(args)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.toolTodoCreate(t.Context(), "machine", "conversation", json.RawMessage(args)); err == nil {
		t.Fatal("dedup silently discarded associations")
	}
	rows, err := database.ListTodos(t.Context())
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	result, err := s.toolTodoRepositories(t.Context(), "machine", "conversation", "todo_repositories_get", json.RawMessage(fmt.Sprintf(`{"id":%q}`, rows[0].ID)))
	if err != nil || !strings.Contains(result, "git@"+"github.com:example/web.git") {
		t.Fatal(result, err)
	}
	_, err = s.toolTodoRepositories(t.Context(), "machine", "conversation", "todo_repository_links_set", json.RawMessage(fmt.Sprintf(`{"id":%q,"repo_id":%q,"expected_revision":1,"links":[{"kind":"pull_request","number":"9007199254740993"}]}`, rows[0].ID, id)))
	if err != nil {
		t.Fatal(err)
	}
	if w := placeDo(s, "", "PUT", repoBase+"/"+id, `{"url":"https://github.com/example/other"}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	set, err := s.readTodoRepositories(t.Context(), rows[0].ID)
	if err != nil || set.Repositories[0].CatalogState != "identity_changed" || set.Repositories[0].SourceSnapshot.Identity.Path != "example/web" {
		t.Fatal(set, err)
	}
	if w := placeDo(s, "", "DELETE", repoBase+"/"+id, ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	set, err = s.readTodoRepositories(t.Context(), rows[0].ID)
	if err != nil || set.Repositories[0].CatalogState != "removed" || len(set.Repositories[0].Links) != 1 {
		t.Fatal(set, err)
	}
}
