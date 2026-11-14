package node

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

// The main conversation on a managed launcher is held to the window the manager sends with it (#7314) when the
// launcher declares a larger one: Claude's compact window and Codex's limit, with the idle /compact at 75% of it. A
// smaller declared window, another conversation, a launcher Persona does not manage, nothing declared and a window of
// 0 (an older manager, or the setting at 0) keep what the launcher declares, as before.
func TestAssistantMainCompactWindow(t *testing.T) {
	fakeClaudeHome(t, "exit 0\n")
	t.Setenv("CODEX_HOME", t.TempDir())
	rc := "function testbig { export CLAUDE_CODE_AUTO_COMPACT_WINDOW=1000000 CLAUDE_CODE_MAX_CONTEXT_TOKENS=1000000; command claude \"$@\"; }\n"
	if err := os.WriteFile(filepath.Join(os.Getenv("HOME"), ".zshrc"), []byte(rc), 0o600); err != nil {
		t.Fatal(err)
	}
	big := []proto.ProviderModel{{ID: "big", ContextWindow: 1_000_000}}
	n := codexTestNode(t)
	n.dir = t.TempDir()
	n.setManaged([]proto.ManagedLauncher{
		{Name: "gw1m", Model: "big", Models: big},
		{Name: "gw200", Model: "big", Models: big, CompactWindow: 200_000},
		{Name: "gwbare", Model: "m"},
		{Name: "codex1m", Agent: "codex", Model: "big", Models: big},
	})
	cwd := t.TempDir()
	for _, tt := range []struct {
		name, launcher  string
		main            bool
		limit           int
		env             string
		codex, idle, at int // at: the window the idle threshold follows once Claude Code reports 1M
	}{
		{"main, managed 1M", "gw1m", true, 400_000, "CLAUDE_CODE_AUTO_COMPACT_WINDOW=400000 CLAUDE_CODE_MAX_CONTEXT_TOKENS=1000000", 400_000, 300_000, 300_000},
		{"main, managed 1M, window 0", "gw1m", true, 0, "CLAUDE_CODE_AUTO_COMPACT_WINDOW=1000000 CLAUDE_CODE_MAX_CONTEXT_TOKENS=1000000", 1_000_000, 750_000, 750_000},
		{"another conversation", "gw1m", false, 400_000, "CLAUDE_CODE_AUTO_COMPACT_WINDOW=1000000 CLAUDE_CODE_MAX_CONTEXT_TOKENS=1000000", 1_000_000, 750_000, 750_000},
		{"declared window below it", "gw200", true, 400_000, "CLAUDE_CODE_AUTO_COMPACT_WINDOW=200000 CLAUDE_CODE_MAX_CONTEXT_TOKENS=1000000", 200_000, 150_000, 150_000},
		{"nothing declared", "gwbare", true, 400_000, "", 0, 150_000, 750_000},
		{"main, managed Codex with a 1M model", "codex1m", true, 400_000, "CLAUDE_CODE_AUTO_COMPACT_WINDOW=400000 CLAUDE_CODE_MAX_CONTEXT_TOKENS=1000000", 400_000, 300_000, 300_000},
		{"another Codex conversation", "codex1m", false, 400_000, "CLAUDE_CODE_MAX_CONTEXT_TOKENS=1000000", 0, 750_000, 750_000},
		{"shell launcher declaring 1M", "testbig", true, 400_000, "", 0, 750_000, 750_000},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := n.assistantCompactionPolicy(tt.launcher, cwd, "", modelExecution{IsMain: tt.main, MainCompactWindow: tt.limit})
			if got := strings.Join(c.env(), " "); got != tt.env || c.codexLimit() != tt.codex || c.idle() != tt.idle {
				t.Fatalf("policy %+v: env %q codex %d idle %d, want %q %d %d", c, got, c.codexLimit(), c.idle(), tt.env, tt.codex, tt.idle)
			}
			args := strings.Join(codexArgs("personad", "test", c.codexLimit()), " ")
			if tt.codex > 0 && !strings.Contains(args, "model_auto_compact_token_limit="+strconv.Itoa(tt.codex)) {
				t.Fatalf("codex args %q, want limit %d", args, tt.codex)
			}
			p := &assistantProc{compactIdleAt: c.idle(), declaredWindow: c.declared()}
			p.mu.Lock()
			p.followWindow(1_000_000) // what Claude Code reports for a 1M model at the end of a turn
			at := p.compactIdleAt
			p.mu.Unlock()
			if at != tt.at {
				t.Fatalf("idle threshold after the reported window: %d, want %d", at, tt.at)
			}
		})
	}
}

// The window is part of what a process runs: one started with another window is not reused. 0 leaves the
// fingerprint as it was before the window existed.
func TestAssistantMainCompactWindowFingerprint(t *testing.T) {
	n := assistantNode(t)
	n.setManaged([]proto.ManagedLauncher{{Name: "gw1m", Model: "big", Models: []proto.ProviderModel{{ID: "big", ContextWindow: 1_000_000}}}})
	capped := n.executionFingerprint("gw1m", modelExecution{IsMain: true, MainCompactWindow: 400_000})
	other := n.executionFingerprint("gw1m", modelExecution{IsMain: true, MainCompactWindow: 300_000})
	uncapped := n.executionFingerprint("gw1m", modelExecution{IsMain: true})
	if capped == uncapped || capped == other {
		t.Fatal("the fingerprint does not follow the window")
	}
	if raw, _ := json.Marshal(modelExecution{IsMain: true}); strings.Contains(string(raw), "MainCompactWindow") {
		t.Fatalf("0 changes the fingerprint of a process from before: %s", raw)
	}
}

// End to end on a managed launcher: the main conversation starts with the window and keeps it for the node's own
// messages (no options: the conversation's record), and a message with another window starts it again on that one.
func TestAssistantMainCompactWindowStart(t *testing.T) {
	f := sdkTest(t)
	f.n.setManaged([]proto.ManagedLauncher{{Name: "gw", Model: "big", Models: []proto.ProviderModel{{ID: "big", ContextWindow: 1_000_000}}}})
	execution := sdkExec(proto.AssistantRuntimeAgentSDK)
	execution.UseEffort, execution.IsMain, execution.MainCompactWindow = true, true, 400_000
	ctx := context.Background()
	id, err := f.n.assistantSend(ctx, "", t.TempDir(), "gw", "hello", nil, "", "", execution)
	if err != nil {
		t.Fatal(err)
	}
	env := f.start(t)["settings"].(map[string]any)["env"].(map[string]any)
	first := f.proc(id)
	if env["CLAUDE_CODE_AUTO_COMPACT_WINDOW"] != "400000" || env["CLAUDE_CODE_MAX_CONTEXT_TOKENS"] != "1000000" || first.compactIdleAt != 300_000 {
		t.Fatalf("main start: %v, idle %d", env, first.compactIdleAt)
	}
	if st, _ := f.n.state.get(id); !st.IsMain || st.MainCompactWindow != 400_000 {
		t.Fatalf("recorded %+v", st)
	}
	f.idle(t, id)
	if _, err := f.n.assistantSend(ctx, id, "", "", "again", nil, "", ""); err != nil {
		t.Fatal(err)
	}
	if f.proc(id) != first {
		t.Fatal("the node's own message restarted the main conversation without its window")
	}
	f.idle(t, id)
	execution.MainCompactWindow = 0
	if _, err := f.n.assistantSend(ctx, id, "", "gw", "off", nil, "", "", execution); err != nil {
		t.Fatal(err)
	}
	if p := f.proc(id); p == first || p.compactIdleAt != 750_000 {
		t.Fatalf("window 0: same process %v, idle %d", p == first, p.compactIdleAt)
	}
	if st, _ := f.n.state.get(id); st.MainCompactWindow != 0 {
		t.Fatalf("recorded window %d", st.MainCompactWindow)
	}
}
