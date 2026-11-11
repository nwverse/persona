package node

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
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

		{"incident env read redirect filter", proto.ModeBypass, "NO_COLOR=1 NO_PROGRESS=1 lark-cli docs +fetch --doc-id example > /tmp/doc.json | jq '.data'", true, false, "allow", 0},
		{"incident without risk metadata", proto.ModeBypass, "NO_COLOR=1 NO_PROGRESS=1 lark-cli docs +fetch --doc-id example > /tmp/doc.json | jq '.data'", true, false, "allow", 0},
		{"CLI ordinary write", proto.ModeBypass, "lark-cli docs +create --title example", true, false, "allow", 0},
		{"CLI send", proto.ModeBypass, "lark-cli im +messages-send --as user --text hello", true, false, "deny", 1},
		{"CLI bot send", proto.ModeBypass, "lark-cli im +messages-send --as bot --text hello", true, false, "allow", 0},
		{"CLI bot delete", proto.ModeBypass, "lark-cli drive files delete --as bot --file-token example", true, false, "deny", 1},
		{"CLI native send", proto.ModeBypass, "lark-cli im messages create --text hello", true, false, "deny", 1},
		{"CLI reply", proto.ModeBypass, "lark-cli im +messages-reply --text hello", true, false, "deny", 1},
		{"CLI comment", proto.ModeBypass, "lark-cli drive files comments create --text hello", true, false, "deny", 1},
		{"CLI mail", proto.ModeBypass, "lark-cli mail user_mailboxes messages send --data '{}'", true, false, "deny", 1},
		{"CLI delete", proto.ModeBypass, "lark-cli drive files delete --file-token example", true, false, "deny", 1},
		{"API send", proto.ModeBypass, "lark-cli api POST /open-apis/im/v1/messages --data '{}'", true, false, "deny", 1},
		{"API delete", proto.ModeBypass, "lark-cli api DELETE /open-apis/drive/v1/files/example", true, false, "deny", 1},
		{"gate send", proto.ModeBypass, "ex POST /conversations/example/messages '{}'", true, false, "deny", 1},
		{"gate delete", proto.ModeBypass, "ex DELETE /files/example", true, false, "deny", 1},
		{"loop read", proto.ModeBypass, "for value in one; do NO_COLOR=1 lark-cli docs +fetch --doc-id example | head; done", true, false, "allow", 0},
		{"loop ordinary write", proto.ModeBypass, "for value in one; do lark-cli docs +create --title example; done", true, false, "allow", 0},
		{"loop send", proto.ModeBypass, "for value in one; do lark-cli im +messages-send --text hello; done", true, false, "deny", 1},
		{"substitution read", proto.ModeBypass, "echo $(lark-cli api GET /open-apis/docx/v1/documents/example)", true, false, "allow", 0},
		{"substitution send", proto.ModeBypass, "echo $(lark-cli im +messages-send --text hello)", true, false, "deny", 1},
		{"script read", proto.ModeBypass, "sh -c 'lark-cli docs +fetch --doc-id example | head'", true, false, "allow", 0},
		{"script send", proto.ModeBypass, "sh -c 'lark-cli im +messages-send --text hello'", true, false, "deny", 1},
		{"unknown script", proto.ModeBypass, `python3 -c 'run("lark-cli im +messages-send")'`, true, false, "deny", 1},
		{"mixed read and send", proto.ModeBypass, "lark-cli docs +fetch --doc-id example | jq '.data'; lark-cli im +messages-send --text hello", true, false, "deny", 1},
		{"mixed read and unknown gate", proto.ModeBypass, "ex GET /health; for value in one; do ex POST $route; done", true, false, "deny", 1},
		{"unknown CLI write", proto.ModeBypass, "for value in one; do lark-cli custom $action; done", true, false, "deny", 1},
		{"explicit deny loop", proto.ModeBypass, "for value in one; do ex DELETE /notes/one; done", true, false, "deny", 0},
		{"explicit CLI deny loop", proto.ModeBypass, "for value in one; do lark-cli im +messages-send --text hello; done", true, false, "deny", 0},
		{"proxy write loop", proto.ModeBypass, "for value in one; do ex POST /notes '{}'; done", true, false, "deny", 0},
		{"proxy write script", proto.ModeBypass, "sh -c 'ex POST /notes {}'", true, false, "deny", 0},
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
			if tc.name == "explicit CLI deny loop" {
				cfg.Rules = []proto.LarkRule{{Method: "CLI", Path: "/im/+messages-send", Action: "deny"}}
			}
			if strings.HasPrefix(tc.name, "proxy write") {
				cfg.Gates[0].Auth = &proto.GateAuth{Header: "Authorization", Value: "Bearer ${EXAMPLE_TOKEN}"}
			}
			if tc.name != "incident without risk metadata" {
				cfg.CLI = &proto.LarkCLI{Bin: "/nonexistent/lark-cli"}
			}
			n.larkRisks = map[string]string{"\x00docs +fetch": "read", "\x00docs +create": "write"}
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
			if tc.assistant && tc.mode == proto.ModeBypass && tc.cards > 0 {
				for _, card := range fh.interactions() {
					if len(card.AlwaysKeys) != 0 {
						t.Fatalf("hard boundary offers Always: %v", card.AlwaysKeys)
					}
				}
			}
			if tc.cards == 0 && decision == "allow" && strings.Contains(reason, "手机") {
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

func TestAssistantBypassResumePersistsDefault(t *testing.T) {
	fakeCodexHome(t, testThread)
	n := codexTestNode(t)
	withFakeHub(t, n)
	cwd := t.TempDir()
	n.state.put(testThread, sessionState{Launcher: "codex", Cwd: cwd})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	defer n.stopAtConnectionEnd([]string{testThread}, nil)
	if _, err := n.assistantSend(ctx, testThread, "", "", "next", nil, "", ""); err != nil {
		t.Fatal(err)
	}
	st, _ := n.state.get(testThread)
	if st.AppMode != proto.ModeBypass {
		t.Fatalf("resumed AppMode = %q, want bypassPermissions", st.AppMode)
	}
}

func TestAssistantBypassNewCodexThreadPolicy(t *testing.T) {
	for _, mode := range []string{"", "default", "plan"} {
		t.Run("mode="+mode, func(t *testing.T) {
			fakeCodexHome(t, testThread)
			record := filepath.Join(t.TempDir(), "thread.json")
			t.Setenv("PERSONA_FAKE_CODEX_START", record)
			n := codexTestNode(t)
			withFakeHub(t, n)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			defer n.stopAtConnectionEnd([]string{testThread}, nil)
			if _, err := n.assistantSend(ctx, "", t.TempDir(), "codex", "hello", nil, mode, ""); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(record)
			if err != nil {
				t.Fatal(err)
			}
			var request struct {
				Params struct{ ApprovalPolicy, Sandbox string }
			}
			if err := json.Unmarshal(raw, &request); err != nil {
				t.Fatal(err)
			}
			want := mode
			if want == "" {
				want = proto.ModeBypass
			}
			approval, sandbox := codexPolicy(want)
			if request.Params.ApprovalPolicy != approval || request.Params.Sandbox != sandbox {
				t.Fatalf("thread/start policy = %+v, want %s/%s", request.Params, approval, sandbox)
			}
		})
	}
}

func TestAssistantBypassGateReportsPermissionMode(t *testing.T) {
	api := newFakeAPI(t)
	n, hub, _ := ledgerNode(t, api.URL)
	n.state = newState()
	const sid = "11111111-1111-4111-8111-111111111111"
	n.mu.Lock()
	n.procs = map[string]*assistantProc{sid: {id: sid, agent: "claude", exited: make(chan struct{})}}
	n.modes = map[string]sessionModes{}
	n.mu.Unlock()
	n.noteModes(sid, proto.ModeBypass, "")
	base, release, err := n.gates().open("ex", sid)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	decision, reason, err := n.larkCheck(context.Background(), sid, t.TempDir(), `ex POST /notes/example '{"text":"example"}'`, proto.Interaction{SessionID: sid})
	if err != nil || decision != "allow" {
		t.Fatalf("gate = %s %s %v", decision, reason, err)
	}
	if code, _ := send(t, "POST", base+"/api/v1/notes/example", `{"text":"example"}`); code != http.StatusOK {
		t.Fatalf("proxy status = %d", code)
	}
	reports := hub.reportList(t, 1)
	if reports[0].SessionID != sid || reports[0].LetThrough != (proto.LarkLetThrough{Kind: proto.LarkThroughBypass}) {
		t.Fatalf("report = %+v", reports[0])
	}
	if code, _ := send(t, "POST", base+"/api/v1/notes/example", `{"text":"example"}`); code != http.StatusForbidden {
		t.Fatalf("reused grant status = %d", code)
	}
}
