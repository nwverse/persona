package node

import (
	"context"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

// The Agent SDK runtime's bridge, and so the claude it starts, has tool search on as the CLI's does
// (TestAssistantShellLauncherCompaction): exactly true, never false (#5838, #7317).
func TestAssistantSDKToolSearchOn(t *testing.T) {
	f := sdkTest(t)
	id, err := f.n.assistantSend(context.Background(), "", t.TempDir(), "claude", "hello", nil, "", "", sdkExec(proto.AssistantRuntimeAgentSDK))
	if err != nil {
		t.Fatal(err)
	}
	f.start(t)
	if got := envValue(f.proc(id).cmd.Env, "ENABLE_TOOL_SEARCH"); got != "true" {
		t.Fatalf("the bridge has ENABLE_TOOL_SEARCH=%q, want \"true\"", got)
	}
}
