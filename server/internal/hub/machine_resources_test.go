package hub

import (
	"net/http"
	"testing"

	"github.com/nwverse/persona/server/internal/extension"
)

func TestMachineResourcesExtensionScope(t *testing.T) {
	h := newTestHub(t)
	connectAssistant(t, h)
	mux := h.routes()
	tokens := extTokensOf(t, h)
	granted := tokens.issue("machine-reader", "Machine reader", []string{extension.ScopeMachinesRead})
	agentsOnly := tokens.issue("agent-reader", "Agent reader", []string{extension.ScopeAgentsRead})
	const path = "/api/v1/assistant/machine-resources"
	if code, body := call(t, mux, http.MethodGet, path, agentsOnly, ""); code != http.StatusForbidden ||
		body["code"] != "permission_denied" || body["scope"] != extension.ScopeMachinesRead {
		t.Fatalf("agents.read must not open machine resources: %d %v", code, body)
	}
	if code, body := call(t, mux, http.MethodGet, path, granted, ""); code != http.StatusOK {
		t.Fatalf("machines.read must open machine resources: %d %v", code, body)
	} else if _, ok := body["machines"].([]any); !ok {
		t.Fatalf("machine resources response has no machines list: %v", body)
	}
}
