package node

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

// #3345: systemd exports the assistant's headless-only profile to the node. Neither
// the availability check nor model listing may mistake it for the launcher's profile.
func TestLauncherChecksIgnoreServiceClaudeConfigDir(t *testing.T) {
	for _, target := range []string{"default", "machine", "shell", "managed-default", "managed-machine", "managed-own"} {
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
			writeFile(t, rc, `export CLAUDE_CONFIG_DIR=`+shellQuote(service))
			switch target {
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
				l := proto.ManagedLauncher{Name: launcher, Kind: "api", BaseURL: wantURL}
				if target != "managed-default" {
					machine := filepath.Join(home, "machine-profile")
					n.claudeStartDir.Store(&machine)
					wantDir = machine
				}
				if target == "managed-own" {
					wantDir = filepath.Join(home, "launcher-profile")
					l.Env = "CLAUDE_CONFIG_DIR=" + wantDir
				}
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
			models, err := n.listAgentModels(context.Background(), launcher, "claude")
			if err != nil || len(models) == 0 {
				t.Fatalf("headless model list: %v, %v", models, err)
			}
			checkEnv()
			t.Logf("service profile has no onboarding; check OK, answer=%q, duration=%dms; %s config=%q; model list OK", r.Answer, r.Millis, target, wantDir)
		})
	}
}

func TestAssistantConfigDirIgnoresServiceAndMachine(t *testing.T) {
	for _, runtime := range []string{"", proto.AssistantRuntimeAgentSDK} {
		t.Run("runtime="+runtime, func(t *testing.T) {
			f := sdkTest(t)
			f.n.dir = filepath.Join(t.TempDir(), ".persona")
			service, machine := t.TempDir(), t.TempDir()
			writeFile(t, filepath.Join(service, ".claude.json"), `{}`)
			t.Setenv("CLAUDE_CONFIG_DIR", service)
			f.n.claudeStartDir.Store(&machine)
			for _, explicit := range []bool{false, true} {
				params := map[string]any{"launcher": "claude", "cwd": filepath.Join(f.n.dir, "assistant"), "text": "hello", "runtime": runtime}
				want := filepath.Join(f.n.dir, "profiles", "claude")
				if explicit {
					want = t.TempDir()
					params["config_dir"] = want
				}
				raw, err := nodeCall(t, f.n, "assistant.send", params)
				if err != nil {
					t.Fatal(err)
				}
				var res struct {
					SessionID string `json:"session_id"`
				}
				if err := json.Unmarshal(raw, &res); err != nil {
					t.Fatal(err)
				}
				p := f.proc(res.SessionID)
				if got := envValue(p.cmd.Env, "CLAUDE_CONFIG_DIR"); got != want {
					t.Fatalf("assistant config=%q, want %q (service and machine must not leak)", got, want)
				}
				if strings.Contains(strings.Join(p.cmd.Args, " "), machine) {
					t.Fatal("assistant command overrides its profile with the machine's")
				}
				f.idle(t, res.SessionID)
				f.n.stopAssistant(res.SessionID)
				// session.send after the process exits must retain an explicit directory.
				if _, err := nodeCall(t, f.n, "session.send", map[string]any{"session_id": res.SessionID, "text": "again"}); err != nil {
					t.Fatal(err)
				}
				if got := envValue(f.proc(res.SessionID).cmd.Env, "CLAUDE_CONFIG_DIR"); got != want {
					t.Fatalf("resumed config=%q, want %q", got, want)
				}
				f.n.stopAssistant(res.SessionID)
			}
		})
	}
}

func TestAssistantProfileSelectionAndDiscovery(t *testing.T) {
	n := &Node{home: t.TempDir(), dir: t.TempDir(), state: newState()}
	service, machine, own, explicit := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", service)
	n.claudeStartDir.Store(&machine)
	n.setManaged([]proto.ManagedLauncher{{Name: "gateway", Env: "CLAUDE_CONFIG_DIR=" + own}})
	if got := n.assistantConfigDir("gateway", ""); got != own {
		t.Fatalf("managed profile = %q, want %q", got, own)
	}
	if got := n.assistantConfigDir("gateway", explicit); got != explicit {
		t.Fatalf("explicit profile = %q, want %q", got, explicit)
	}
	private := n.assistantConfigDir("claude", "")
	if err := os.MkdirAll(private, 0o700); err != nil {
		t.Fatal(err)
	}
	n.state.put("session", sessionState{ConfigDir: explicit})
	dirs := n.claudeConfigDirs()
	if !slices.Contains(dirs, private) || !slices.Contains(dirs, explicit) {
		t.Fatalf("private and saved explicit profiles must be discoverable before a process scan: %v", dirs)
	}
	_, err := n.spawnClaude("session", t.TempDir(), "claude", false, false, modelExecution{ConfigDir: "relative-profile"})
	if err == nil || !strings.Contains(err.Error(), "config_dir") {
		t.Fatalf("relative config_dir was not refused: %v", err)
	}
	n.sessionScope = &nodeSessionScope{claude: own}
	if got := n.assistantConfigDir("claude", explicit); got != own {
		t.Fatalf("explicit config_dir escaped session scope: %q", got)
	}
	if got := envValue(n.claudeProcessEnv([]string{"CLAUDE_CONFIG_DIR=" + service}, explicit), "CLAUDE_CONFIG_DIR"); got != own {
		t.Fatalf("launch environment escaped session scope: %q", got)
	}
	if got := n.launcherCommand("wrapper"); !strings.Contains(got, own) {
		t.Fatalf("wrapper launch discarded the session scope: %q", got)
	}
}
