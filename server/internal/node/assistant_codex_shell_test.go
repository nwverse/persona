package node

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCodexShellZsh(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh is not installed")
	}
	for _, tt := range []struct {
		name, rc, launcher string
		flags              []string
		mark               string
	}{
		{name: "plain", launcher: "codex"},
		{name: "command", launcher: "command codex -m test-model", flags: []string{"-m", "test-model"}},
		{name: "codex alias", rc: "alias codex='command codex --no-alt-screen'\n", launcher: "codex", flags: []string{"--no-alt-screen"}},
		{name: "launcher alias", rc: "alias cx='command codex -m test-model'\n", launcher: "cx", flags: []string{"-m", "test-model"}},
		{name: "launcher function", rc: "cx() { export LAUNCH_MARK=from-function; command codex -m 'model with spaces'; }\n", launcher: "cx", flags: []string{"-m", "model with spaces"}, mark: "from-function"},
		{name: "codex function", rc: "codex() { export LAUNCH_MARK=from-codex-function; command codex -m 'model with spaces'; }\n", launcher: "codex", flags: []string{"-m", "model with spaces"}, mark: "from-codex-function"},
		{name: "codex function inline environment", rc: "codex() { LAUNCH_MARK=inline-proxy command codex -m 'model with spaces' \"$@\"; }\n", launcher: "codex", flags: []string{"-m", "model with spaces"}, mark: "inline-proxy"},
		{name: "function with codex alias", rc: "alias codex='command codex --no-alt-screen'\ncx() { export LAUNCH_MARK=from-function; command codex -m test-model; }\n", launcher: "cx", flags: []string{"-m", "test-model"}, mark: "from-function"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			bin := filepath.Join(home, "bin")
			if err := os.Mkdir(bin, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(home, ".zshrc"), []byte(tt.rc), 0o600); err != nil {
				t.Fatal(err)
			}
			// Print NUL-delimited values so spaces and empty arguments remain observable.
			fake := "#!/bin/sh\nprintf '%s\\0' \"${LAUNCH_MARK-}\" \"$@\"\n"
			if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(fake), 0o700); err != nil {
				t.Fatal(err)
			}
			// Only a guard against a hang: on macOS the first run of the fresh fake codex waits for the system's checks,
			// up to 10 s while go test ./... links its test binaries (#2307).
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			args := []string{"-c", "model_provider=\"test provider\"", ""}
			cmd := exec.CommandContext(ctx, zsh, append([]string{"-ic", codexShell(tt.launcher), "persona"}, args...)...)
			cmd.Env = []string{"HOME=" + home, "ZDOTDIR=" + home, "PATH=" + bin + ":/usr/bin:/bin"}
			out, err := cmd.Output()
			if err != nil {
				if exit, ok := err.(*exec.ExitError); ok {
					t.Fatalf("launcher failed: %v: %s", err, exit.Stderr)
				}
				t.Fatal(err)
			}
			want := append([]string{tt.mark}, tt.flags...)
			want = append(want, "app-server")
			want = append(want, args...)
			got := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("arguments = %q, want %q", got, want)
			}
		})
	}
}
