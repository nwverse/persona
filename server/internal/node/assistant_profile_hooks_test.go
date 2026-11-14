package node

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/runtimepkg"
)

// sdkProfileTest is sdkTest without its zsh-only fixture skip. The start under
// test uses the login shell selected on this machine (bash when zsh is absent).
func sdkProfileTest(t *testing.T) *sdkFixture {
	t.Helper()
	home := t.TempDir()
	bin := filepath.Join(home, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\n"+fakeCLI), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(home, ".zshrc"), filepath.Join(home, ".bashrc")} {
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"hasCompletedOnboarding":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("ZDOTDIR", home)
	shell := "/bin/sh"
	if runtime.GOOS == "windows" {
		shell = loginShellProgram() // Git Bash, not a Win32 path to MSYS's internal /bin/sh
		if shell == "" {
			t.Skip("Git Bash is required for the assistant launcher")
		}
	}
	t.Setenv("SHELL", shell)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	dir := t.TempDir()
	t.Setenv("SDK_FAKE_DIR", dir)
	load, find, claude := sdkLoadPackage, sdkFindJS, sdkClaude
	t.Cleanup(func() { sdkLoadPackage, sdkFindJS, sdkClaude = load, find, claude })
	files := []runtimepkg.File{{Path: "agent-sdk/package.json", Data: []byte(`{}`)}, {Path: "agent-sdk/dist/bridge.mjs", Data: []byte(fakeSDKBridge)}}
	sdkLoadPackage = func() (*runtimepkg.Package, error) {
		return &runtimepkg.Package{Name: sdkPackage, Hash: runtimepkg.Hash(files), Files: files}, nil
	}
	sdkFindJS = func(context.Context, string) (jsRuntime, error) { return jsRuntime{Path: shell}, nil }
	sdkClaude = func() (string, error) { return "/opt/test/bin/claude", nil }
	n := codexTestNode(t)
	n.dir = t.TempDir()
	buf := &lockedBuf{}
	n.logger = log.New(buf, "", 0)
	fh := withFakeHub(t, n)
	t.Cleanup(func() {
		n.mu.Lock()
		ids := make([]string, 0, len(n.procs))
		for id := range n.procs {
			ids = append(ids, id)
		}
		n.mu.Unlock()
		for _, id := range ids {
			n.stopAssistant(id)
		}
	})
	return &sdkFixture{n: n, fh: fh, dir: dir, log: buf}
}

// A new profile must have the existing opt-in hook before Claude's first
// startup. Other hooks and settings never leak into the private profile.
func TestAssistantNewProfileSeedsBypassHook(t *testing.T) {
	for _, runtime := range []string{"", proto.AssistantRuntimeAgentSDK} {
		t.Run(runtime, func(t *testing.T) {
			f := sdkProfileTest(t)
			n := f.n
			n.home = os.Getenv("HOME")
			source := filepath.Join(n.home, ".claude")
			if err := os.MkdirAll(source, 0o700); err != nil {
				t.Fatal(err)
			}
			command := "'/opt/extension/0.2.30/bin/agent-bypass-allow' hook --config-dir '" + source + "' --data '/tmp/extension data' --persona-extension=claude-bypass-allow"
			settings := map[string]any{
				"env":         map[string]any{"DO_NOT_COPY": "yes"},
				"permissions": map[string]any{"defaultMode": "plan"},
				"hooks": map[string]any{"PermissionRequest": []any{
					map[string]any{"matcher": "Bash", "hooks": []any{
						map[string]any{"type": "command", "command": command, "timeout": 10},
						map[string]any{"type": "command", "command": "unrelated-hook", "timeout": 2},
					}},
					map[string]any{"matcher": "Write", "hooks": []any{
						map[string]any{"type": "command", "command": "another-unrelated-hook", "timeout": 2},
					}},
				}},
			}
			body, _ := json.MarshalIndent(settings, "", "  ")
			sourcePath := filepath.Join(source, "settings.json")
			if err := os.WriteFile(sourcePath, append(body, '\n'), 0o600); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(sourcePath)
			id := "a5500000-0000-4000-8000-000000000558"
			p, err := n.spawnClaude(id, t.TempDir(), "claude", false, false, sdkExec(runtime))
			if err != nil {
				t.Fatal(err)
			}
			defer p.stop()

			profilePath := filepath.Join(n.dir, "profiles", "claude", "settings.json")
			raw, err := os.ReadFile(profilePath)
			if err != nil {
				t.Fatalf("new assistant profile has no startup hook: %v", err)
			}
			var got map[string]any
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || got["hooks"] == nil {
				t.Fatalf("profile copied unrelated settings: %s", raw)
			}
			groups := got["hooks"].(map[string]any)["PermissionRequest"].([]any)
			if len(groups) != 1 {
				t.Fatalf("profile copied unrelated groups: %s", raw)
			}
			hooks := groups[0].(map[string]any)["hooks"].([]any)
			if len(hooks) != 1 {
				t.Fatalf("mixed group was not reduced to the tagged entry: %s", raw)
			}
			entry := hooks[0].(map[string]any)
			if entry["command"] != command || entry["timeout"] != float64(10) {
				t.Fatalf("tagged entry changed: %+v", entry)
			}
			after, _ := os.ReadFile(sourcePath)
			if string(after) != string(before) {
				t.Fatal("source settings were modified")
			}
		})
	}
}
