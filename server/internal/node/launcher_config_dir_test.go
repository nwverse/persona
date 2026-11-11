package node

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

// #3345: systemd exports the assistant's headless-only profile to the node. Neither
// the availability check nor model listing may mistake it for the launcher's profile.
func TestLauncherChecksIgnoreServiceClaudeConfigDir(t *testing.T) {
	for _, target := range []string{"default", "machine", "shell-default", "shell", "managed-default", "managed-machine", "managed-own"} {
		t.Run(target, func(t *testing.T) {
			seen := filepath.Join(t.TempDir(), "env")
			fakeClaudeHome(t, `printf '%s\n' "$CLAUDE_CONFIG_DIR" "$ANTHROPIC_BASE_URL" > `+shellQuote(seen)+`
case "$*" in
  *--input-format*)
    read line
    printf '%s\n' '`+claudeInitAnswer+`'
    read line ;;
  *)
    printf '%s\n' '{"type":"result","subtype":"success","duration_ms":37,"result":"ok","modelUsage":{"target-model":{}}}' ;;
esac
`)
			home := launchKeyHome(t)
			n := &Node{home: home, dir: filepath.Join(home, ".persona"), logger: log.New(io.Discard, "", 0)}
			service := filepath.Join(n.dir, "profiles", "claude")
			writeFile(t, filepath.Join(service, ".claude.json"), `{"firstStartVersion":"2.1.318"}`)
			t.Setenv("CLAUDE_CONFIG_DIR", service)
			t.Setenv("CLAUDE_CODE_CUSTOM_OAUTH_URL", "")
			launcher, wantDir, wantURL := "claude", "", ""
			rc := filepath.Join(os.Getenv("ZDOTDIR"), ".zshrc")
			switch target {
			case "shell-default":
				wantDir = filepath.Join(home, "shell-profile")
				writeFile(t, rc, `export CLAUDE_CONFIG_DIR=`+shellQuote(wantDir))
			case "machine":
				d := filepath.Join(home, "machine-profile")
				n.claudeStartDir.Store(&d)
				wantDir = d
			case "shell":
				machine := filepath.Join(home, "machine-profile")
				n.claudeStartDir.Store(&machine)
				launcher, wantDir, wantURL = "target", filepath.Join(home, "launcher-profile"), "https://gateway.example.com"
				writeFile(t, rc,
					`target() { CLAUDE_CONFIG_DIR=`+shellQuote(wantDir)+` ANTHROPIC_BASE_URL=`+shellQuote(wantURL)+` claude "$@"; }`)
			case "managed-default", "managed-machine", "managed-own":
				launcher, wantURL = "gateway", "https://gateway.example.com"
				l := proto.ManagedLauncher{Name: launcher, Kind: "api", BaseURL: wantURL, NoProxy: "gateway.example.com"}
				if target != "managed-default" {
					machine := filepath.Join(home, "machine-profile")
					n.claudeStartDir.Store(&machine)
				}
				if target == "managed-own" {
					wantDir = filepath.Join(home, "launcher-profile")
					l.Env = "CLAUDE_CONFIG_DIR=" + wantDir
				}
				writeFile(t, filepath.Join(service, "settings.json"), `{"env":{"NO_PROXY":"service.example.com"}}`)
				settingsDir := wantDir
				if settingsDir == "" {
					settingsDir = filepath.Join(home, ".claude")
				}
				writeFile(t, filepath.Join(settingsDir, "settings.json"), `{"env":{"NO_PROXY":"selected.example.com"}}`)
				n.setManaged([]proto.ManagedLauncher{l})
				n.bin, _ = os.Executable()
				t.Setenv("NODE_TEST_LAUNCH", "1")
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				ln, err := n.listenOnce(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer ln.Close()
			}
			checkEnv := func() {
				t.Helper()
				b, err := os.ReadFile(seen)
				if err != nil || string(b) != wantDir+"\n"+wantURL+"\n" {
					t.Fatalf("launcher environment = %q (%v), want config %q, gateway %q", b, err, wantDir, wantURL)
				}
			}
			r := n.launcherCheck(context.Background(), launcher)
			if !r.OK || r.Answer != "ok" || r.Model != "target-model" || r.Millis != 37 {
				t.Fatalf("headless check must run despite missing onboarding: %+v", r)
			}
			checkEnv()
			if strings.HasPrefix(target, "managed-") {
				path := n.launcherProbeSettings(context.Background(), launcher, "", home)
				b, err := os.ReadFile(path)
				var settings struct {
					Env map[string]string `json:"env"`
				}
				if err != nil || json.Unmarshal(b, &settings) != nil {
					t.Fatalf("read probe settings: %v, %s", err, b)
				}
				if got := settings.Env["NO_PROXY"]; !strings.Contains(got, "selected.example.com") || strings.Contains(got, "service.example.com") {
					t.Fatalf("probe settings came from the wrong profile: NO_PROXY=%q", got)
				}
			}
			models, err := n.listAgentModels(context.Background(), launcher, "claude")
			if err != nil || len(models) == 0 {
				t.Fatalf("headless model list: %v, %v", models, err)
			}
			checkEnv()
			t.Logf("service profile has no onboarding; check OK, answer=%q, duration=%dms; %s config=%q; model list OK", r.Answer, r.Millis, target, wantDir)
		})
	}
}

// Part A must not migrate conversations or change terminal launch selection.
func TestLauncherProbesLeaveConversationDirectoriesUnchanged(t *testing.T) {
	for _, runtime := range []string{"", proto.AssistantRuntimeAgentSDK} {
		t.Run("runtime="+runtime, func(t *testing.T) {
			f := sdkTest(t)
			f.n.dir = filepath.Join(t.TempDir(), ".persona")
			service, machine := t.TempDir(), t.TempDir()
			writeFile(t, filepath.Join(service, ".claude.json"), `{}`)
			t.Setenv("CLAUDE_CONFIG_DIR", service)
			f.n.claudeStartDir.Store(&machine)
			n := f.n
			id := "a5500000-0000-4000-8000-000000000003"
			cwd := filepath.Join(n.dir, "assistant")
			n.state.put(id, sessionState{Launcher: "claude", Cwd: cwd, Runtime: runtime})
			n.cdirs = newClaudeDirs()
			n.cdirs.scan = func() map[int]procEnv { return nil }
			writeFile(t, filepath.Join(machine, "projects", "-assistant", id+".jsonl"),
				`{"type":"user","cwd":`+jsonString(t, cwd)+`,"message":{"role":"user","content":"earlier"}}`+"\n")
			before := n.launcherCommand("claude")
			// Probe an unrelated wrapper without starting a model request.
			_, _ = n.launcherProbe(context.Background(), "missing_probe_launcher")
			if got := n.launcherCommand("claude"); got != before {
				t.Fatalf("probe changed an Agent's command: %q -> %q", before, got)
			}
			id, err := n.assistantSend(context.Background(), id, cwd, "claude", "hello", nil, "", "", modelExecution{Runtime: runtime, Fingerprint: "compat-probe"})
			if err != nil {
				t.Fatal(err)
			}
			p := f.proc(id)
			if runtime == proto.AssistantRuntimeAgentSDK {
				if got := envValue(p.cmd.Environ(), "CLAUDE_CONFIG_DIR"); got != machine {
					t.Fatalf("SDK lost the existing machine profile: %q", got)
				}
			} else {
				if got := strings.TrimSuffix(f.file("cli-profile"), "\n"); got != machine {
					t.Fatalf("CLI lost the existing machine profile: %q", got)
				}
			}
			if os.Getenv("CLAUDE_CONFIG_DIR") != service {
				t.Fatal("probe or conversation launch changed the node's service environment")
			}
			f.n.stopAssistant(id)
		})
	}
}
