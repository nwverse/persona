package node

import (
	"os/exec"
	"strings"
	"testing"
)

func TestOtherAgentStartCommand(t *testing.T) {
	const prompt = "a 'quoted' prompt; $(printf unexpected)"
	cmd := otherAgentCommand("wrapper", "00000000-0000-4000-8000-000000000001", prompt)
	// Execute only a fake shell function: a first prompt must remain a single literal argument.
	out, err := exec.Command("sh", "-c", `wrapper() { printf '%s\n' "$#" "$@"; }; `+cmd).Output()
	if err != nil || string(out) != "3\nresume\n00000000-0000-4000-8000-000000000001\n"+prompt+"\n" {
		t.Fatalf("command arguments = %q (%v)", out, err)
	}
	if strings.Contains(cmd, "--session-id") || strings.Contains(cmd, "--settings") || strings.Contains(cmd, "--mcp-config") {
		t.Fatal("a non-Claude start must not receive Claude flags")
	}
}

// Typed into a shell without start files, ^U would erase the line and a CR would then run what follows it.
func TestOtherAgentStartCommandHasNoControls(t *testing.T) {
	const prompt = "hi\x15touch pwned\r\x03\x1b[201~"
	cmd := otherAgentCommand("wrapper", "00000000-0000-4000-8000-000000000001", prompt)
	if strings.ContainsFunc(cmd, pasteControl) {
		t.Fatalf("command %q keeps a control character", cmd)
	}
	out, err := exec.Command("sh", "-c", `wrapper() { printf '%s\n' "$#" "$@"; }; `+cmd).Output()
	if want := "3\nresume\n00000000-0000-4000-8000-000000000001\nhi<U+0015>touch pwned\r<U+0003><ESC>[201~\n"; err != nil || string(out) != want {
		t.Fatalf("command arguments = %q (%v), want %q", out, err, want)
	}
}
