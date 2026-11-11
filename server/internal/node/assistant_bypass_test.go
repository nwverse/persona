package node

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestAssistantUnselectedModeStartsBypass(t *testing.T) {
	f := sdkTest(t)
	id, err := f.n.assistantSend(context.Background(), "", t.TempDir(), "claude", "hello", nil, "", "", sdkExec(proto.AssistantRuntimeAgentSDK))
	if err != nil {
		t.Fatal(err)
	}
	f.idle(t, id)
	var start struct {
		Options struct{ PermissionMode string }
	}
	if err := json.Unmarshal([]byte(f.file("start.json")), &start); err != nil {
		t.Fatal(err)
	}
	if start.Options.PermissionMode != proto.ModeBypass {
		t.Fatalf("startup mode = %q, want bypassPermissions", start.Options.PermissionMode)
	}
	if err := f.n.setModes(context.Background(), id, "default", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := f.n.assistantSend(context.Background(), id, "", "", "next", nil, "", ""); err != nil {
		t.Fatal(err)
	}
	f.idle(t, id)
	if mode, _ := f.n.modesOf(id); mode != "default" {
		t.Fatalf("resumed mode = %q, lost explicit selection", mode)
	}
}

func TestAssistantBypassCommandGate(t *testing.T) {
	for _, tc := range []struct {
		name, mode, command string
		assistant, health   bool
		want                string
		cards               int
	}{
		{"read", proto.ModeBypass, "ex GET /health", true, false, "allow", 0},
		{"authorized write", proto.ModeBypass, "ex POST /notes '{}'", true, false, "allow", 0},
		{"compound command", proto.ModeBypass, "for value in one; do ex POST /notes '{}'; done", true, false, "allow", 0},
		{"explicit default", "default", "ex POST /notes '{}'", true, false, "deny", 1},
		{"ordinary agent", proto.ModeBypass, "ex POST /notes '{}'", false, false, "deny", 1},
		{"health", proto.ModeBypass, "ex POST /notes '{}'", true, true, "deny", 1},
		{"health execution directory", proto.ModeBypass, "ex POST /notes '{}'", true, false, "deny", 1},
		{"explicit deny", proto.ModeBypass, "ex DELETE /notes/one", true, false, "deny", 0},
		{"credential guard", proto.ModeBypass, "echo $EXAMPLE_TOKEN", true, false, "deny", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := codexTestNode(t)
			fh := withFakeHub(t, n)
			const sid = "11111111-1111-4111-8111-111111111111"
			cfg := olCfg(proto.LarkRule{Method: "DELETE", Path: "/notes/**", Action: "deny"})
			n.mu.Lock()
			n.larkCfg, n.larkCfgAt = cfg, time.Now().Add(time.Hour)
			if tc.assistant {
				n.procs[sid] = &assistantProc{id: sid, agent: "claude", exited: make(chan struct{})}
			}
			n.mu.Unlock()
			n.persistModes(sid, tc.mode, "")
			if tc.health {
				n.gates().markHealth(sid)
			}
			fh.mu.Lock()
			fh.answer = func(proto.Interaction) proto.Resolution {
				no := false
				return proto.Resolution{Allow: &no}
			}
			fh.mu.Unlock()
			cwd := t.TempDir()
			if tc.name == "health execution directory" {
				cwd = "/home/me/.persona/health"
			}
			decision, reason, err := n.larkCheck(context.Background(), sid, cwd, tc.command,
				proto.Interaction{SessionID: sid, Cwd: "/home/me/.persona/assistant", ExpiresAt: time.Now().Add(time.Minute)})
			if err != nil || decision != tc.want || len(fh.interactions()) != tc.cards {
				t.Fatalf("decision=%q reason=%q err=%v cards=%d, want %q/%d", decision, reason, err, len(fh.interactions()), tc.want, tc.cards)
			}
			if tc.cards == 0 && strings.Contains(reason, "手机") {
				t.Fatalf("claimed a phone answer without a card: %q", reason)
			}
		})
	}
}

func TestAssistantBypassPreservesRuntimeSelection(t *testing.T) {
	n, hub := runtimeNode(t)
	assistantHub(hub)
	host := dialRuntime(t, n, assistantRuntimeHello())
	sid := startedAssistant(t, n, host)
	for _, mode := range []string{"plan", ""} {
		if _, err := nodeCall(t, n, "assistant.send", map[string]any{"session_id": sid, "text": "next", "permission_mode": mode}); err != nil {
			t.Fatal(err)
		}
	}
	sends := host.got("session.send")
	var last struct{ Mode string }
	if err := json.Unmarshal(sends[len(sends)-1], &last); err != nil || last.Mode != "plan" {
		t.Fatalf("runtime selection overwritten: %s, %v", sends[len(sends)-1], err)
	}
	n.mu.Lock()
	delete(n.modes, sid)
	n.mu.Unlock()
	if _, err := nodeCall(t, n, "assistant.send", map[string]any{"session_id": sid, "text": "after restart"}); err != nil {
		t.Fatal(err)
	}
	sends = host.got("session.send")
	if err := json.Unmarshal(sends[len(sends)-1], &last); err != nil || last.Mode != "plan" {
		t.Fatalf("runtime selection lost after memory reset: %s, %v", sends[len(sends)-1], err)
	}
}
