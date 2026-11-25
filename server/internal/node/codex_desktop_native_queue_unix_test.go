//go:build !windows

package node

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

func TestCodexDesktopUserChatWithoutLegacySidebarEntry(t *testing.T) {
	home, _ := startFakeCodexBus(t, "")
	for _, tc := range []struct {
		name, origin, source, threadSource string
		want                               bool
	}{
		{"desktop user", "Codex Desktop", "vscode", "user", true},
		{"desktop subagent", "Codex Desktop", "vscode", "subagent", false},
		{"background job", "Codex Desktop", "exec", "user", false},
		{"CLI session", "codex-tui", "cli", "user", false},
		{"unclassified external process", "Codex Desktop", "vscode", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			meta := map[string]any{"type": "session_meta", "payload": map[string]any{
				"originator": tc.origin, "source": tc.source, "thread_source": tc.threadSource,
			}}
			line, _ := json.Marshal(meta)
			path, _ := appRollout(t, string(line))
			if got := codexDesktopChat(codexSource{home: home, thread: newUUID(), path: path}); got != tc.want {
				t.Fatalf("sendable=%t want %t", got, tc.want)
			}
		})
	}
}

func fakeNativeQueueCLI(t *testing.T, home, pending, reply string, dropAfterAdd bool) string {
	t.Helper()
	cli := filepath.Join(home, "codex")
	if reply == "" && !dropAfterAdd {
		reply = `{"id":3,"result":{"queuedSubmission":{"id":"ack","clientUserMessageId":"mobile-send-42"}}}`
	}
	addAction := `printf '%s\n' '` + reply + `'`
	if dropAfterAdd {
		addAction = `exit 0`
	}
	script := `#!/bin/sh
while IFS= read -r line; do
  printf '%s\n' "$line" >> "$CODEX_HOME/requests"
  case "$line" in
    *'"method":"initialize"'*) printf '%s\n' '{"id":1,"result":{}}';;
    *'"method":"thread/queue/list"'*) printf '%s\n' '{"id":2,"result":{"data":[{"clientUserMessageId":"` + pending + `"}]}}';;
    *'"method":"thread/queue/add"'*) ` + addAction + `;;
  esac
done
`
	if err := os.WriteFile(cli, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return cli
}

func TestCodexDesktopSendWithMissingFollowerBus(t *testing.T) {
	const thread = "018f0000-0000-4000-8000-000000000001"
	const clientID = "mobile-send-42"
	for _, tc := range []struct {
		name, pending, reply string
		stale, openFails     bool
		dropAfterAdd         bool
		wantAdd, wantQueued  bool
		wantUnknown          bool
	}{
		{name: "missing", wantAdd: true, wantQueued: true},
		{name: "stale and busy", stale: true, wantAdd: true, wantQueued: true},
		{name: "already persisted", pending: clientID, wantQueued: true},
		{name: "open failed", openFails: true},
		{name: "server refused", reply: `{"id":3,"error":{"code":-32602,"message":"thread archived"}}`, wantAdd: true},
		{name: "missing receipt", reply: `{"id":3,"result":{}}`, wantAdd: true, wantUnknown: true},
		{name: "transport closed after add", dropAfterAdd: true, wantAdd: true, wantUnknown: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, listener := listenFakeCodexBus(t)
			if tc.stale {
				listener.(*net.UnixListener).SetUnlinkOnClose(false)
			}
			listener.Close()
			if codexDesktopAvailable(home) {
				t.Fatal("a dead socket alone advertised desktop delivery")
			}
			cli := fakeNativeQueueCLI(t, home, tc.pending, tc.reply, tc.dropAfterAdd)
			prevCLI, prevOpen := codexDesktopQueueCLI, openCodexQueueThread
			t.Cleanup(func() { codexDesktopQueueCLI, openCodexQueueThread = prevCLI, prevOpen })
			codexDesktopQueueCLI = func(h string) string {
				if h == home {
					return cli
				}
				return ""
			}
			openCodexQueueThread = func(_ context.Context, bin string, dst codexSource) error {
				if bin != cli || dst.thread != thread || dst.home != home {
					t.Fatalf("wrong desktop destination %q %+v", bin, dst)
				}
				if tc.openFails {
					return errors.New("desktop launch failed")
				}
				return nil
			}
			if !codexDesktopAvailable(home) {
				t.Fatal("native queue not advertised")
			}
			if codexDesktopBusAvailable(home) {
				t.Fatal("native queue falsely advertised legacy follower bus")
			}
			ctx, queued := withSendQueued(context.WithValue(t.Context(), sendTraceKey{}, clientID))
			src := codexSource{home: home, thread: thread}
			if tc.stale {
				src.path, _ = appRollout(t, `{"type":"event_msg","payload":{"type":"task_started","turn_id":"busy"}}`)
			}
			err := (&Node{}).sendCodexDesktop(ctx, src, "hello\n世界", nil)
			if *queued != tc.wantQueued || (err == nil) != tc.wantQueued || maybeDelivered(err) != tc.wantUnknown {
				t.Fatalf("queued=%t err=%v", *queued, err)
			}
			wire, err := os.ReadFile(filepath.Join(home, "requests"))
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			for _, line := range strings.Split(strings.TrimSpace(string(wire)), "\n") {
				var msg struct {
					Method string
					Params struct {
						ThreadID, ClientUserMessageID string
						Input                         []struct{ Text string }
					}
				}
				if err := json.Unmarshal([]byte(line), &msg); err != nil {
					t.Fatal(err)
				}
				switch msg.Method {
				case "thread/queue/add":
					count++
					p := msg.Params
					if p.ThreadID != thread || p.ClientUserMessageID != clientID || len(p.Input) != 1 || p.Input[0].Text != "hello\n世界" {
						t.Fatalf("changed queued input: %s", line)
					}
				case "thread/resume", "turn/start":
					t.Fatalf("queue client took ownership: %s", line)
				}
			}
			if (count == 1) != tc.wantAdd || count > 1 {
				t.Fatalf("queue writes=%d, wantAdd=%t", count, tc.wantAdd)
			}
		})
	}
}

// When only the macOS native thread queue is available (no legacy follower bus), App chats
// advertise Send without claiming Interrupt or Model/Effort switching, and those bus-only
// controls still fail closed with codex_read_only.
func TestCodexDesktopNativeQueueCapabilitiesAndControls(t *testing.T) {
	home := unavailableCodexBus(t, true)
	cli := fakeNativeQueueCLI(t, home, "", `{"id":3,"result":{"queuedSubmission":{"id":"ack","clientUserMessageId":"mobile-send-42"}}}`, false)
	prevCLI, prevOpen, prevAppCLI, prevSeed := codexDesktopQueueCLI, openCodexQueueThread, codexAppCLI, seedCodexAppChat
	t.Cleanup(func() {
		codexDesktopQueueCLI, openCodexQueueThread, codexAppCLI, seedCodexAppChat = prevCLI, prevOpen, prevAppCLI, prevSeed
	})
	n := remoteTestNode(t)
	n.home = filepath.Dir(home)
	n.configureCodex(proto.CodexConfig{})
	canonHome := n.codexHome()
	realHome := realPath(home)
	codexDesktopQueueCLI = func(h string) string {
		if h == home || h == canonHome || h == realHome {
			return cli
		}
		return ""
	}
	codexAppCLI = func() (string, error) { return cli, nil }
	openCodexQueueThread = func(_ context.Context, _ string, _ codexSource) error { return nil }

	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".codex-global-state.json"), []byte(`{"projectless-thread-ids":["`+appChat+`"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	path := writeAppRollout(t, home, appChat, cwd, time.Now())
	appendLines(t, path, event("task_started"))
	id := n.registerCodex(home, appChat, path)
	src, _ := n.codexSource(id)
	withCodexAppModels(t, src.home, map[string]codexModelEfforts{"gpt-x": {levels: []string{"low", "high"}, def: "low"}})

	row := appChatRow(t, n)
	if row.AgentName != "Codex App" || row.SpaceKind != "codex_app" || row.Status != proto.StatusWorking {
		t.Fatalf("App identity or status lost: %+v", row)
	}
	caps := row.Capabilities
	if caps == nil || !caps.Read || !caps.Send || caps.Interrupt || caps.Model != nil || caps.Takeover != nil || len(row.Efforts) != 0 || row.ReadOnlyReason != "" {
		t.Fatalf("queue-only row advertised wrong capabilities: %+v, efforts=%v, reason=%q", caps, row.Efforts, row.ReadOnlyReason)
	}

	// Bus-only controls still refuse cleanly with delivered: none.
	wantReason := i18n.T(i18n.Default, codexDesktopUnavailableKey)
	for name, run := range map[string]func() error{
		"interrupt": func() error { return n.interruptCodex(context.Background(), id) },
		"settings":  func() error { return modesCall(t, n, id, map[string]any{"effort": "high"}) },
	} {
		err := run()
		if code, data := refusalOf(t, err); code != "codex_read_only" || data["delivered"] != deliveredNone || err.Error() != wantReason {
			t.Fatalf("%s: %v, data %v", name, err, data)
		}
	}

	// The codex-app launcher is offered with locked model selection; custom model/effort refuses before seeding.
	if _, added := n.withCodexAppLauncher(nil); !added {
		t.Fatal("codex-app launcher not offered with native queue")
	}
	if models, err := n.launcherModels(context.Background(), codexAppLauncher, "", true); err != nil || models.Locked != codexAppModelLocked {
		t.Fatalf("launcher models = %+v, %v; want Locked=%q", models, err, codexAppModelLocked)
	}
	seeded := 0
	seedCodexAppChat = func(_ context.Context, _, _, _, _ string) (string, error) {
		seeded++
		return appChat, nil
	}
	if _, err := n.newCodexAppChat(context.Background(), runtimeStartParams{Cwd: cwd, Launcher: codexAppLauncher, Prompt: "hi", Model: "gpt-x"}, ""); refusalCode(err) != "codex_read_only" || seeded != 0 {
		t.Fatalf("custom model start = %v, seeded=%d; want refusal before seeding", err, seeded)
	}
	ctx := context.WithValue(context.Background(), sendTraceKey{}, "mobile-send-42")
	if s, err := n.newCodexAppChat(ctx, runtimeStartParams{Cwd: cwd, Launcher: codexAppLauncher, Prompt: "hi"}, ""); err != nil || s == nil || seeded != 1 {
		t.Fatalf("default-model new chat via native queue = (%+v, %v), seeded=%d", s, err, seeded)
	}
}

func TestStartCodexDesktopTurnFallsBackToNativeQueueWhenBusDisappears(t *testing.T) {
	const thread = "018f0000-0000-4000-8000-000000000002"
	const clientID = "mobile-send-42"
	home := unavailableCodexBus(t, true)
	cli := fakeNativeQueueCLI(t, home, "", `{"id":3,"result":{"queuedSubmission":{"id":"ack","clientUserMessageId":"`+clientID+`"}}}`, false)
	prevCLI, prevOpen := codexDesktopQueueCLI, openCodexQueueThread
	t.Cleanup(func() { codexDesktopQueueCLI, openCodexQueueThread = prevCLI, prevOpen })
	codexDesktopQueueCLI = func(h string) string {
		if h == home {
			return cli
		}
		return ""
	}
	openCodexQueueThread = func(_ context.Context, _ string, _ codexSource) error { return nil }

	ctx, queued := withSendQueued(context.WithValue(context.Background(), sendTraceKey{}, clientID))
	input := codexDesktopInput([]codexDesktopMessage{{text: "held follow-up"}})
	if err := (&Node{}).startCodexDesktopTurn(ctx, codexSource{home: home, thread: thread}, input); err != nil || !*queued {
		t.Fatalf("fallback startCodexDesktopTurn: err=%v queued=%v", err, *queued)
	}
	wire, err := os.ReadFile(filepath.Join(home, "requests"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(wire), `"method":"thread/queue/add"`) || !strings.Contains(string(wire), `"held follow-up"`) {
		t.Fatalf("held turn did not fall back to thread/queue/add:\n%s", wire)
	}
}
