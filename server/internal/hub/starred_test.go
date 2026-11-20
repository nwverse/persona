package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"

	"github.com/nwverse/persona/server/internal/extwire"
)

const starredTestPath = "/api/v1/assistant/starred"

func TestStarredRouteIsolation(t *testing.T) {
	h := newTestHub(t)
	api := h.routes()
	tokens := extTokensOf(t, h)
	token := tokens.issue("starred-check", "Starred check", []string{extwire.ScopeAgentsRead, extwire.ScopeAgentsControl})
	for _, route := range []struct{ method, suffix string }{
		{"GET", ""}, {"POST", ""}, {"DELETE", "/example"},
		{"GET", "/collections"}, {"POST", "/collections"},
		{"PATCH", "/collections/sc-example"}, {"DELETE", "/collections/sc-example"},
		{"PATCH", "/collections/assignments"},
	} {
		path := starredTestPath + route.suffix
		if allowed(principal{Role: roleAnswer}, route.method, path) || allowed(principal{Role: roleLink}, route.method, path) {
			t.Fatalf("%s %s widened a restricted device's access", route.method, path)
		}
		if status, _ := call(t, api, route.method, path, token, "{}"); status != http.StatusForbidden {
			t.Fatalf("%s %s should refuse an extension token, got %d", route.method, path, status)
		}
	}
	tokens.mu.Lock()
	defer tokens.mu.Unlock()
	if len(tokens.served) != 0 {
		t.Fatalf("restricted requests reached the manager: %v", tokens.served)
	}
}

func TestStarredCollectionsFollowSelectedManager(t *testing.T) {
	h, srv, _ := newBusHub(t)
	connectFakeManager(t, h, srv, assistantwire.AssistantID)
	const otherID = "m-0a0b0c"
	other := connectFakeManager(t, h, srv, otherID)
	const base = starredTestPath + "/collections"
	for _, tc := range []struct{ method, path string }{
		{"GET", base}, {"POST", base},
		{"PATCH", base + "/sc-example"}, {"PATCH", base + "/assignments"},
		{"DELETE", base + "/sc-example?revision=3"},
	} {
		status, out := ownershipGet(t, h, srv, tc.method, tc.path, map[string]string{ManagerHeader: otherID})
		if status != http.StatusOK || out["who"] != otherID {
			t.Fatalf("%s %s reached the wrong manager: %d %v", tc.method, tc.path, status, out)
		}
		want := tc.method + " " + tc.path
		select {
		case got := <-other.got:
			// The relay separates the URL query from the path.
			if tc.method == "DELETE" {
				want = "DELETE " + base + "/sc-example"
			}
			if got != want {
				t.Fatalf("relayed %q, want %q", got, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("manager did not receive %q", want)
		}
	}

	const legacyID = "m-0a0b0d"
	legacyHTTP := make(chan assistantwire.HTTPRequest, 2)
	legacy := dialRawBusWith(t, srv, busToken(t, h, legacyID, legacyID),
		func(_ context.Context, method string, raw json.RawMessage) (any, error) {
			if method == assistantwire.MethodHTTP {
				var request assistantwire.HTTPRequest
				if err := json.Unmarshal(raw, &request); err != nil {
					return nil, err
				}
				legacyHTTP <- request
				return assistantwire.HTTPResponse{Status: http.StatusNotFound, Body: []byte("404 page not found")}, nil
			}
			return nil, nil
		})
	if err := legacy.call("manager.hello", map[string]any{"id": legacyID}, nil); err != nil {
		t.Fatal(err)
	}
	if status, _ := ownershipGet(t, h, srv, http.MethodGet, base, map[string]string{ManagerHeader: legacyID}); status != http.StatusNotFound {
		t.Fatalf("legacy manager's 404 was not preserved: %d", status)
	}
	const query = "revision=12&manager=" + legacyID
	if status, _ := ownershipGet(t, h, srv, http.MethodDelete, base+"/sc-example?"+query, nil); status != http.StatusNotFound {
		t.Fatalf("collection mutation did not reach the manager selected by query: %d", status)
	}
	for _, want := range []assistantwire.HTTPRequest{
		{Method: http.MethodGet, Path: base},
		{Method: http.MethodDelete, Path: base + "/sc-example", Query: query},
	} {
		select {
		case req := <-legacyHTTP:
			if req.Method != want.Method || req.Path != want.Path || req.Query != want.Query {
				t.Fatalf("relayed %s %s?%s, want %s %s?%s", req.Method, req.Path, req.Query, want.Method, want.Path, want.Query)
			}
		case <-time.After(time.Second):
			t.Fatalf("the selected legacy manager did not receive %s %s", want.Method, want.Path)
		}
	}
}

func TestStarredBroadcastKeepsManager(t *testing.T) {
	h := newTestHub(t)
	app := wsApp(t, h)
	err := h.relayBroadcastFrom("m-example", json.RawMessage(`{"type":"assistant_starred","manager":"forged"}`))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case raw := <-app.out:
		var event map[string]json.RawMessage
		if err := json.Unmarshal(raw, &event); err != nil {
			t.Fatal(err)
		}
		if string(event["type"]) != `"assistant_starred"` || string(event["manager"]) != `"m-example"` {
			t.Fatalf("wrong event type or provenance: %s", raw)
		}
		if event["star"] != nil || event["text"] != nil {
			t.Fatal("snapshot left the manager on the event")
		}
	case <-time.After(time.Second):
		t.Fatal("the star's notification was not relayed")
	}
}
