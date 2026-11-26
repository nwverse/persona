package node

import (
	"context"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/moduleswitch"
	"github.com/nwverse/persona/server/internal/node/larkgateport"
	"github.com/nwverse/persona/server/internal/proto"
)

// needsLarkGate skips a test of the lark-cli gate or Feishu card context in a build without the lark module
// (persona_without_lark, persona_core_only): both come from internal/modules/lark/larkgate through larkgateport.
func needsLarkGate(t testing.TB) {
	t.Helper()
	if larkgateport.Get(moduleswitch.Set{}) == nil {
		t.Skip("the lark module is not in this build")
	}
}

// needsLarkGateForCommand skips only subtests whose command exercises lark-cli when the lark module is absent, so
// generic CommandGate and dynamic-command cases continue to run in persona_without_lark and persona_core_only builds.
func needsLarkGateForCommand(t testing.TB, command string) {
	t.Helper()
	if larkgateport.Get(moduleswitch.Set{}) != nil {
		return
	}
	for _, needle := range []string{"lark-cli", `lark\-cli`, "lark-c?i", "lark-{cli", "lark''-cli", `lark""-cli`, `lark-c\li`} {
		if strings.Contains(command, needle) {
			t.Skip("the lark module is not in this build")
		}
	}
}

// With the lark module switched off (or excluded from the build), generic CommandGate enforcement still works and
// Lark-specific CLI/card-context helpers degrade to safe no-ops without panicking.
func TestLarkGateDisabledDegradesCleanly(t *testing.T) {
	prev := hubModules.Load()
	off := moduleswitch.Off("lark")
	hubModules.Store(&off)
	t.Cleanup(func() { hubModules.Store(prev) })

	if larkGate() != nil {
		t.Fatal("larkGate() should be nil when module lark is switched off")
	}
	if isLarkCLI("lark-cli") || larkMentionsCLI("lark-cli im +chat-list") || larkNamesCLI("lark-cli") {
		t.Fatal("CLI matchers must return false when module lark is switched off")
	}
	if deny := larkCommandDeny("cat ~/.lark-cli/config.json"); deny != "" {
		t.Fatalf("larkCommandDeny with module off = %q, want empty", deny)
	}
	// Generic CommandGate (assistant.gate) continues to classify and decide calls normally.
	gates := olGate("")
	v := classifyLark(`ex GET /conversations/oc_1/messages`, gates, "", nil)
	if v.deny != "" || v.other || len(v.calls) != 1 || larkDecision(proto.AssistantLark{Gates: gates}, v.calls[0]) != "allow" {
		t.Fatalf("generic CommandGate read failed with lark off: %+v", v)
	}
	v = classifyLark(`ex POST /conversations/oc_1/messages '{"text":"hi"}'`, gates, "", nil)
	if v.deny != "" || v.other || len(v.calls) != 1 || larkDecision(proto.AssistantLark{Gates: gates}, v.calls[0]) != "confirm" {
		t.Fatalf("generic CommandGate write failed with lark off: %+v", v)
	}
	n := &Node{}
	if got := n.larkCardContexts(context.Background(), proto.AssistantLark{}, v.calls); len(got) != 1 || got[0].OK {
		t.Fatalf("larkCardContexts with lark off = %+v, want OK=false", got)
	}
	if _, ok := larkMessageSummary(v.calls[0], larkCardContext{OK: true, Chat: "Room"}, i18n.En); ok {
		t.Fatal("larkMessageSummary should fall back when module lark is switched off")
	}
}
