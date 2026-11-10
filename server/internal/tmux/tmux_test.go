package tmux

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A node started inside a pane of another server (tmux -L work) carries that server in its own TMUX, and tmux without
// -S runs against the server TMUX names. A pane on the default server must still be reached on the default server, so
// the node's TMUX is not passed on; a pane on a named server is reached through -S.
func TestTmuxDefaultServerIgnoresTheNodesOwnTMUX(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake tmux is a shell script")
	}
	t.Setenv("TMUX", "/tmp/other/work,1,0")
	t.Setenv("TMUX_PANE", "%7")
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	bin := filepath.Join(dir, "tmux")
	script := `#!/bin/sh
echo "TMUX=$TMUX TMUX_PANE=$TMUX_PANE args=$*" >> ` + calls + `
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	if _, err := (Client{Bin: bin}).Capture(ctx, "%11", 0, false); err != nil {
		t.Fatal(err)
	}
	if _, err := (Client{Bin: bin, Socket: "/s"}).Capture(ctx, "%11", 0, false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 2 {
		t.Fatalf("calls = %q, want 2", b)
	}
	if want := "TMUX= TMUX_PANE= args=capture-pane -p -t %11"; lines[0] != want {
		t.Errorf("default server call = %q, want %q", lines[0], want)
	}
	if !strings.Contains(lines[1], "args=-S /s capture-pane -p -t %11") {
		t.Errorf("named server call = %q, want -S /s", lines[1])
	}
}
