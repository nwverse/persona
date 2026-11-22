package node

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/compat"
)

func TestAgentDirectoryGuardResolvesAliases(t *testing.T) {
	home := t.TempDir()
	state := filepath.Join(home, "custom-state")
	assistant := filepath.Join(state, "assistant")
	if err := os.MkdirAll(assistant, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(home, "project-alias")
	if err := os.Symlink(assistant, alias); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(assistant, "child")
	if err := os.MkdirAll(child, 0700); err != nil {
		t.Fatal(err)
	}
	childAlias := filepath.Join(home, "child-alias")
	if err := os.Symlink(child, childAlias); err != nil {
		t.Fatal(err)
	}
	n := &Node{home: home, dir: state}
	// Resolve symlinks before lexical cleaning: alias/.. names the target's parent.
	for _, cwd := range []string{assistant, alias, "~/.persona/assistant", "~/" + compat.StateDir + "/assistant", assistant + "/../assistant", childAlias + "/.."} {
		if err := n.guardAgentDirectory(context.Background(), cwd); rpcCode(err) != "agent_assistant_workspace" {
			t.Errorf("%s: expected workspace refusal, got %v", cwd, err)
		}
	}
	for _, cwd := range []string{home, filepath.Join(home, "project", "assistant"), assistant + "-project"} {
		if err := n.guardAgentDirectory(context.Background(), cwd); err != nil {
			t.Errorf("project %s: %v", cwd, err)
		}
	}
}

func TestSessionStartRefusesAssistantAliasBeforeLaunching(t *testing.T) {
	n, _, home := herdrNode(t)
	assistant := filepath.Join(n.dir, "assistant")
	if err := os.MkdirAll(assistant, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(home, "alias")
	if err := os.Symlink(assistant, alias); err != nil {
		t.Fatal(err)
	}
	_, err := request(t, n, "session.start", map[string]any{"cwd": alias, "prompt": "Inspect the source"})
	if rpcCode(err) != "agent_assistant_workspace" {
		t.Fatalf("got %v", err)
	}
}

func TestMacAgentInstructionsDefaultOn(t *testing.T) {
	rules := agentPlatformInstructions("darwin", "Keep the task scope.")
	for _, want := range []string{"Keep the task scope.", "do not compile or run tests", "dedicated CI runner", "rebuild.sh", "make release-artifacts", "exempt"} {
		if !strings.Contains(rules, want) {
			t.Errorf("Mac rules missing %q: %s", want, rules)
		}
	}
	if got := agentPlatformInstructions("linux", "Keep the task scope."); got != "Keep the task scope." {
		t.Errorf("Linux instructions = %q", got)
	}
}
