package assistant

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestSettingsToolHasOneSchemaAndLegacyCallsStillRun(t *testing.T) {
	ctx := context.Background()
	s := newTestService(t, newFakeHub().withMachine())
	counts := map[string]int{}
	for _, tool := range s.Tools(ctx) {
		counts[tool.Name]++
	}
	if counts["persona_settings"] != 1 || counts["jarvis_settings"] != 0 {
		t.Errorf("settings schemas: persona_settings=%d jarvis_settings=%d, want one canonical schema only", counts["persona_settings"], counts["jarvis_settings"])
	}
	r := s.Tool(ctx, ToolCall{Name: "jarvis_settings", Arguments: json.RawMessage(`{}`), MachineID: "server", SessionID: "c1"})
	if r.IsError || len(r.Content) == 0 || !strings.Contains(r.Content[0].Text, "assistant.launcher") {
		t.Fatalf("the legacy settings call no longer runs: %+v", r)
	}
}
