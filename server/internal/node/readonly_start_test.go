package node

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestReadOnlyTerminalStartRefusesUnsupportedLaunchers(t *testing.T) {
	withCodexLaunchers(t, map[string][]string{"code-alias": {"codex", "--full-auto"}})
	n, hh, oh := startNode(t)
	for _, tc := range []struct{ launcher, agent string }{
		{"codex", ""}, {"code-alias", ""}, {"wrapper", "codex"}, {traexAgent, ""},
	} {
		t.Run(tc.launcher, func(t *testing.T) {
			_, err := n.start(context.Background(), t.TempDir(), tc.launcher, "Inspect the project", "", "", "plan", "", "", "", tc.agent)
			if code, data := refusalOf(t, err); code != "readonly_unsupported" || data["delivered"] != deliveredNone {
				t.Fatalf("start: %v (%v)", err, data)
			}
		})
	}
	if hh.asked()+oh.asked() != 0 {
		t.Fatalf("a refused start reached an engine: herdr %d, Orca %d", hh.asked(), oh.asked())
	}
}

func TestReadOnlyCodexResumeIsRefusedBeforeOpeningATab(t *testing.T) {
	n, host, cwd := codexHistoryNode(t)
	_, err := n.start(context.Background(), cwd, "", "Inspect the project", startThread, "", "plan", "", "", "", "")
	if code, data := refusalOf(t, err); code != "readonly_unsupported" || data["delivered"] != deliveredNone {
		t.Fatalf("resume: %v (%v)", err, data)
	}
	if got := host.trace("tab.create", "pane.send_input"); len(got) != 0 {
		t.Fatalf("refused resume reached the terminal: %v", got)
	}
}

func TestReadOnlyRuntimeStartRefusesEveryProvider(t *testing.T) {
	n, _ := runtimeNode(t)
	if err := n.useEngine(context.Background(), proto.EnginePaseo); err != nil {
		t.Fatal(err)
	}
	host := dialRuntime(t, n, paseoEngineHello())
	host.answer = func(method string, params json.RawMessage) (any, error) {
		return map[string]any{"session": map[string]any{"session_id": "paseo-readonly", "status": "idle", "cwd": "/w/app"}}, nil
	}
	for _, launcher := range []string{"paseo-claude", "paseo-codex", "claude", ""} {
		t.Run(launcher, func(t *testing.T) {
			_, err := nodeCall(t, n, "session.start", map[string]any{
				"cwd": "/w/app", "launcher": launcher, "permission_mode": "plan", "prompt": "Inspect the project",
			})
			if rpcCode(err) != "readonly_unsupported" {
				t.Fatalf("start: %v", err)
			}
		})
	}
	// Paseo's takeover path does not apply modes, even for a named Claude provider.
	if _, err := nodeCall(t, n, "session.start", map[string]any{
		"cwd": "/w/app", "launcher": "paseo-claude", "permission_mode": "plan", "resume_session_id": sdkSession,
	}); rpcCode(err) != "readonly_unsupported" {
		t.Fatalf("takeover: %v", err)
	}
	if got := host.got("session.start"); len(got) != 0 {
		t.Fatalf("refused starts reached the runtime: %v", got)
	}
	for _, tc := range []struct{ launcher, mode string }{
		{"paseo-claude", "default"}, {"paseo-codex", "acceptEdits"}, {"claude", ""},
	} {
		if _, err := nodeCall(t, n, "session.start", map[string]any{
			"cwd": "/w/app", "launcher": tc.launcher, "permission_mode": tc.mode, "prompt": "Inspect the project",
		}); err != nil {
			t.Fatalf("%s / %s: %v", tc.launcher, tc.mode, err)
		}
	}
	got := host.got("session.start")
	if len(got) != 3 {
		t.Fatalf("allowed starts: %v", got)
	}
	var start map[string]any
	if err := json.Unmarshal(got[0], &start); err != nil || start["permission_mode"] != "default" {
		t.Fatalf("Claude mode: %s (%v)", got[0], err)
	}
}

func TestReadOnlyRuntimeStartRefusesUnverifiedRuntimes(t *testing.T) {
	for _, agent := range []string{"dsh", "pi", "agy", "claude"} {
		t.Run(agent, func(t *testing.T) {
			n, _ := runtimeNode(t)
			hello := dshHello()
			hello["launchers"] = []map[string]any{{"name": "sample", "agent": agent}}
			host := dialRuntime(t, n, hello)
			_, err := nodeCall(t, n, "session.start", map[string]any{
				"cwd": "/w/app", "launcher": "sample", "permission_mode": "plan", "prompt": "Inspect the project",
			})
			if rpcCode(err) != "readonly_unsupported" || len(host.got("session.start")) != 0 {
				t.Fatalf("start: %v, requests %v", err, host.got("session.start"))
			}
		})
	}
}
