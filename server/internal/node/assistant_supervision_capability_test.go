package node

import (
	"context"
	"encoding/json"
	"testing"
)

func TestAssistantExecutionAdvertisesSupervisionInstructions(t *testing.T) {
	n := codexTestNode(t)
	out, err := n.handle(context.Background(), "assistant.execution", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	var capability struct {
		Instructions bool `json:"supervision_instructions"`
	}
	if err := json.Unmarshal(raw, &capability); err != nil || !capability.Instructions {
		t.Fatalf("node did not advertise role-scoped instructions: %s, %v", raw, err)
	}
}
