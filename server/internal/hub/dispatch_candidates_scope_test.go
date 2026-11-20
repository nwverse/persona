package hub

import (
	"net/http"
	"testing"

	"github.com/nwverse/persona/server/internal/extension"
	"github.com/nwverse/persona/server/internal/extwire"
)

func TestDispatchCandidatesScopeAuthorization(t *testing.T) {
	const path = "/api/v1/assistant/dispatch/candidates"
	const pattern = "GET " + path
	if scope := extwire.RouteScopes[pattern]; scope != extension.ScopeDispatchDefaults {
		t.Fatalf("%s scope = %q", pattern, scope)
	}
	for route, scope := range extwire.RouteScopes {
		if scope == extension.ScopeDispatchDefaults && route != pattern {
			t.Errorf("dispatch.defaults also grants %s", route)
		}
	}

	h := newTestHub(t)
	modelsNode(t, h, MachineConfig{ID: "box", Name: "box"}, func(string, map[string]any) (any, error) {
		return map[string]any{"launchers": []any{}}, nil
	})
	mux := h.routes()
	tokens := extTokensOf(t, h)
	candidates := tokens.issue("candidates", "Candidates", []string{extension.ScopeDispatchDefaults})
	for _, token := range []string{h.cfg.Token, candidates} {
		code, body := call(t, mux, http.MethodGet, path+"?machine=box", token, "")
		if _, ok := body["candidates"].([]any); code != http.StatusOK || !ok {
			t.Fatalf("read candidates: %d %v", code, body)
		}
	}

	none := tokens.issue("none", "None", nil)
	legacy := tokens.issue("legacy", "Legacy", extension.EveryScope())
	start := tokens.issue("start", "Start defaults", []string{extension.ScopeAssistantStartDefaults})
	for _, tc := range []struct{ name, token, method, path, scope string }{
		{"ungranted", none, "GET", path, extension.ScopeDispatchDefaults},
		{"legacy every-scope", legacy, "GET", path, extension.ScopeDispatchDefaults},
		{"start defaults only", start, "GET", path, extension.ScopeDispatchDefaults},
		{"worker presets", candidates, "GET", "/api/v1/assistant/worker-presets", extension.ScopeAssistantStartDefaults},
		{"read start defaults", candidates, "GET", "/api/v1/assistant/start-defaults", extension.ScopeAssistantStartDefaults},
		{"write start defaults", candidates, "PUT", "/api/v1/assistant/start-defaults", extension.ScopeAssistantStartDefaults},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, body := call(t, mux, tc.method, tc.path+"?machine=box", tc.token, "")
			if code != http.StatusForbidden || body["code"] != "permission_denied" || body["scope"] != tc.scope {
				t.Fatalf("%s %s: %d %v", tc.method, tc.path, code, body)
			}
		})
	}
}
