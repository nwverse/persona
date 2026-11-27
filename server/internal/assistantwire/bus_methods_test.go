package assistantwire_test

import (
	"encoding/json"
	"testing"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/buswire"
)

func TestExtensionBusMethodsWire(t *testing.T) {
	checkMethodWire(t, assistantwire.ExtensionIdentityMethod, "extensions.identity", `{}`, `{"supported":true}`)
	checkMethodWire(t, assistantwire.ModelsExtensionCompleteMethod, "models.extension_complete",
		`{"storage_id":"old-id","extension":"example","params":{"messages":[{"role":"user","content":"example"}]}}`,
		`{"text":"ok","model":"example","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1,"requests":1}}`)
	checkMethodWire(t, assistantwire.ModelsPurposeCompleteMethod, "models.purpose_complete",
		`{"purpose":"subscription.recipe","params":{"messages":[{"role":"user","content":"example"}]}}`,
		`{"text":"{}","json":{},"model":"example","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1,"requests":1}}`)
	checkMethodWire(t, assistantwire.ExtensionTrustForgetMethod, "assistant.extension_trust_forget", `{"extension":"example"}`, `{}`)
	checkMethodWire(t, assistantwire.AssistantConversationsMethod, "assistant.conversations",
		`{"since":"2026-01-02T00:00:00Z","include_archived":true,"limit":1}`, `{"items":[],"total":2,"next_offset":1}`)
	checkMethodWire(t, assistantwire.AssistantMessagesMethod, "assistant.messages",
		`{"id":"example","after_seq":3}`, `{"messages":[],"next_seq":4}`)
	checkMethodWire(t, assistantwire.AssistantMemoryMethod, "assistant.memory", `{"include_content":true}`,
		`{"entries":[{"name":"example.md","size":3,"modified":"2026-01-02T00:00:00Z","content":"abc"}]}`)
}

func TestModelEntriesAndApprovalBusMethodsWire(t *testing.T) {
	checkMethodWire(t, assistantwire.ModelsUsableMethod, "models.usable", `{"purpose":"inbox.triage"}`,
		`{"usable":true,"provider":"Example API","model":"example"}`)
	checkJSONRoundTrip[assistantwire.ModelsUsableResult](t, `{"usable":false}`)
	checkMethodWire(t, assistantwire.ModelsLaunchersMethod, "models.launchers", `{}`,
		`{"launchers":[{"name":"example","base_url":"https://api.example.com/v1"}]}`)
	checkMethodWire(t, assistantwire.AssistantEntriesListMethod, "assistant.entries.list", `{"after":"example","by":"user"}`,
		`{"entries":[],"after":"example","has_more":false}`)
	checkMethodWire(t, assistantwire.FleetApprovalApplyMethod, "fleet.approval_apply",
		`{"interaction_id":"example","resolution":{"allow":true}}`, `{"text":"applied"}`)
	checkJSONRoundTrip[assistantwire.FleetApprovalApplyResult](t, `{"text":"","stale":true}`)
	checkMethodWire(t, assistantwire.FleetApprovalDiscardMethod, "fleet.approval_discard",
		`{"interaction_id":"example","status":"denied"}`, `{"ok":true}`)
}

func checkMethodWire[P, R any](t *testing.T, m buswire.Method[P, R], name, request, reply string) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		if m.Name != name {
			t.Fatalf("method = %q, want %q", m.Name, name)
		}
		checkJSONRoundTrip[P](t, request)
		checkJSONRoundTrip[R](t, reply)
	})
}

func checkJSONRoundTrip[T any](t *testing.T, want string) {
	t.Helper()
	var value T
	if err := json.Unmarshal([]byte(want), &value); err != nil {
		t.Fatal(err)
	}
	if raw, err := json.Marshal(value); err != nil || string(raw) != want {
		t.Errorf("%T wire = %s, %v, want %s", value, raw, err, want)
	}
}
