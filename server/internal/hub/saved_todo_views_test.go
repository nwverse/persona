package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/extwire"
)

func TestSavedTodoViewsHubOwnerScope(t *testing.T) {
	h := newTestHub(t)
	api := h.routes()
	tokens := extTokensOf(t, h)
	token := tokens.issue("view-check", "Saved views check", []string{
		extwire.ScopeTodosRead, extwire.ScopeTodosWrite, extwire.ScopeAgentsRead, extwire.ScopeAgentsControl,
	})
	path := assistantwire.SavedTodoViewsPath
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		if allowed(principal{Role: roleAnswer}, method, path) || allowed(principal{Role: roleLink}, method, path) {
			t.Fatalf("%s widened a restricted device's access to saved views", method)
		}
		if code, _ := call(t, api, method, path, token, "{}"); code != http.StatusForbidden {
			t.Fatalf("%s accepted a scoped extension token: %d", method, code)
		}
	}
	tokens.mu.Lock()
	defer tokens.mu.Unlock()
	if len(tokens.served) != 0 {
		t.Fatalf("excluded credentials reached the manager: %v", tokens.served)
	}
}

func TestSavedTodoViewsHubRoutesSelectedAndLegacyManagers(t *testing.T) {
	h, srv, _ := newBusHub(t)
	const legacyID = assistantwire.AssistantID
	oldRoutes := http.NewServeMux()
	oldRoutes.HandleFunc("GET /api/v1/assistant/saved-prompts", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	legacy := dialRawBusWith(t, srv, busToken(t, h, legacyID, legacyID),
		func(_ context.Context, method string, raw json.RawMessage) (any, error) {
			if method != assistantwire.MethodHTTP {
				return nil, nil
			}
			var p assistantwire.HTTPRequest
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, err
			}
			w := httptest.NewRecorder()
			oldRoutes.ServeHTTP(w, httptest.NewRequest(p.Method, p.Path, bytes.NewReader(p.Body)))
			return assistantwire.HTTPResponse{Status: w.Code, Header: w.Header(), Body: w.Body.Bytes()}, nil
		})
	if err := legacy.call("manager.hello", map[string]any{"id": legacyID}, nil); err != nil {
		t.Fatal(err)
	}

	const named = "m-0a0b0c"
	connectFakeManager(t, h, srv, named)
	path := assistantwire.SavedTodoViewsPath
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		if code, _ := ownershipGet(t, h, srv, method, path, nil); code != http.StatusNotFound {
			t.Fatalf("%s to a manager before saved views returned %d", method, code)
		}
		code, out := ownershipGet(t, h, srv, method, path, map[string]string{ManagerHeader: named})
		if code != http.StatusOK || out["who"] != named {
			t.Fatalf("%s did not follow the selected manager: %d %v", method, code, out)
		}
	}
}

func TestSavedTodoViewsInvalidationCarriesManagerAndRevision(t *testing.T) {
	h, a, b := pathsHub(t, quiet, quiet)
	app := wsApp(t, h)
	got := broadcastsTo(t, app, b, `{"type":"saved_todo_views","revision":"9007199254740993","manager":"forged"}`)
	got = append(got, broadcastsTo(t, app, a, `{"type":"saved_todo_views","revision":"7"}`)...)
	owners := map[string]string{}
	for _, message := range got {
		var typ, revision, manager string
		_ = json.Unmarshal(message["type"], &typ)
		if typ != "saved_todo_views" {
			continue
		}
		_ = json.Unmarshal(message["revision"], &revision)
		_ = json.Unmarshal(message["manager"], &manager)
		owners[revision] = manager
	}
	if len(owners) != 2 || owners["9007199254740993"] != "m-0b0b0b" || owners["7"] != "" {
		t.Fatalf("incorrect event scope or revision precision: %v", owners)
	}
}
