package node

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestPreparedAssistantClaudeAcknowledgesStartupWithoutAUserTurn(t *testing.T) {
	f := cliTest(t)
	// This CLI acknowledges initialization as the real stream-json control protocol does.
	script := strings.Replace(fakeCLI, `*'"subtype":"set_permission_mode"'*|`, `*'"subtype":"initialize"'*|*'"subtype":"set_permission_mode"'*|`, 1)
	if err := os.WriteFile(filepath.Join(os.Getenv("HOME"), "bin", "claude"), []byte("#!/bin/sh\n"+script), 0o700); err != nil {
		t.Fatal(err)
	}
	raw, err := nodeCall(t, f.n, "assistant.start_prepared", map[string]any{"launcher": "claude", "cwd": filepath.Join(f.n.home, ".persona", "assistant"), "permission_mode": "plan"})
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		SessionID string `json:"session_id"`
	}
	_ = json.Unmarshal(raw, &out)
	stdin := f.file("stdin.jsonl")
	if out.SessionID == "" || !strings.Contains(stdin, `"subtype":"initialize"`) || strings.Contains(stdin, `"type":"user"`) {
		t.Fatalf("startup sent a turn: %s %s", raw, stdin)
	}
	f.n.mu.Lock()
	retained := f.n.procs[out.SessionID].model.PrepareOnly
	f.n.mu.Unlock()
	if retained {
		t.Fatal("one-shot preparation leaked into recovery metadata")
	}
	f.n.stopAssistantProcess(out.SessionID) // the node may restart before the owner's first message
	if _, err := nodeCall(t, f.n, "session.send", map[string]any{"session_id": out.SessionID, "text": "Continue"}); err != nil {
		t.Fatal(err)
	}
	cliWait(t, "first real message", func() bool { return strings.Contains(f.file("stdin.jsonl"), "Continue") })
}

func TestPreparedAssistantClaudeFailureStopsTheCandidate(t *testing.T) {
	f := cliTest(t) // the fixture refuses initialize
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := f.n.assistantSend(ctx, "", t.TempDir(), "claude", "", nil, "", "", modelExecution{PrepareOnly: true})
	if err == nil || strings.Contains(f.file("stdin.jsonl"), `"type":"user"`) {
		t.Fatalf("unconfirmed startup: %v", err)
	}
	f.n.mu.Lock()
	left := len(f.n.procs)
	f.n.mu.Unlock()
	if left != 0 {
		t.Fatal("failed candidate was kept")
	}
}

func TestPreparedAssistantRuntimeStartsWithoutSendingAndKeepsFirstMessageInstructions(t *testing.T) {
	n, hub := runtimeNode(t)
	assistantHub(hub)
	host := dialRuntime(t, n, assistantRuntimeHello())
	host.answer = func(method string, _ json.RawMessage) (any, error) {
		if method == "session.start" {
			return map[string]any{"session": map[string]any{"session_id": "session-a1", "status": "idle"}}, nil
		}
		return map[string]any{"ok": true}, nil
	}
	raw, err := nodeCall(t, n, "assistant.start_prepared", map[string]any{"launcher": "dsh", "cwd": filepath.Join(n.home, ".persona", "assistant")})
	if err != nil {
		t.Fatal(err)
	}
	if len(host.got("session.start")) != 1 || len(host.got("session.send")) != 0 {
		t.Fatal("preparing a runtime sent a model turn")
	}
	var out struct {
		SessionID string `json:"session_id"`
	}
	_ = json.Unmarshal(raw, &out)
	if st, _ := n.state.get(out.SessionID); !st.AssistantPrepared {
		t.Fatal("first-message instructions were not retained")
	}
	if _, err := nodeCall(t, n, "session.send", map[string]any{"session_id": out.SessionID, "text": "Continue"}); err != nil {
		t.Fatal(err)
	}
	sends := host.got("session.send")
	if len(sends) != 1 || !strings.Contains(string(sends[0]), "MEMORY.md") || !strings.Contains(string(sends[0]), "Continue") {
		t.Fatalf("first message: %s", sends)
	}
	if st, _ := n.state.get(out.SessionID); st.AssistantPrepared {
		t.Fatal("first-message instructions stayed pending after send")
	}
}

func TestPreparedAssistantRefusesExistingOrNonemptyRequests(t *testing.T) {
	n, _ := runtimeNode(t)
	for _, p := range []map[string]any{{"session_id": "existing"}, {"text": "Do work"}, {"new_session_id": sidA}} {
		if _, err := nodeCall(t, n, "assistant.start_prepared", p); err == nil {
			t.Fatalf("accepted %v", p)
		}
	}
}

func TestPreparedAssistantHandoffReadsBoundedRuntimeHistoryWithoutSending(t *testing.T) {
	n, _ := runtimeNode(t)
	host := dialRuntime(t, n, assistantRuntimeHello())
	const id = "session-a1"
	n.state.put(id, sessionState{Launcher: "dsh", Cwd: filepath.Join(n.home, ".persona", "assistant")})
	var items []proto.Item
	for i := range 40 {
		items = append(items, proto.Item{Seq: i + 1, Role: "assistant", Kind: proto.KindText, Text: strings.Repeat("x", 2000)})
	}
	items = append(items, proto.Item{Seq: 41, Role: "user", Kind: proto.KindText, Text: "Keep the acceptance criteria"})
	host.answer = func(method string, _ json.RawMessage) (any, error) { return map[string]any{"items": items}, nil }
	raw, err := nodeCall(t, n, "assistant.handoff", map[string]any{"session_id": id})
	if err != nil {
		t.Fatal(err)
	}
	var h Handoff
	if err := json.Unmarshal(raw, &h); err != nil {
		t.Fatal(err)
	}
	if len(h.Recent) == 0 || len(h.Recent) > handoffTurns || h.Recent[len(h.Recent)-1].Text != "Keep the acceptance criteria" {
		t.Fatalf("runtime context: %+v", h)
	}
	total := 0
	for _, turn := range h.Recent {
		total += len([]rune(turn.Text))
	}
	if total > handoffRecentMax || len(host.got("session.send")) != 0 {
		t.Fatal("runtime handoff was unbounded or sent a model turn")
	}
}

func TestPreparedAssistantCodexStartsAThreadWithoutATurn(t *testing.T) {
	const thread = "019a1930-7e92-7073-9bb8-d2ad3126f329"
	fakeCodexHome(t, thread)
	record := filepath.Join(t.TempDir(), "requests.jsonl")
	t.Setenv("PERSONA_FAKE_CODEX_REQUESTS", record)
	n := codexTestNode(t)
	withFakeHub(t, n)
	raw, err := nodeCall(t, n, "assistant.start_prepared", map[string]any{"launcher": "codex", "cwd": t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { n.stopAssistantProcess(thread) })
	b, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), thread) || !strings.Contains(string(b), `"method":"thread/start"`) || strings.Contains(string(b), `"method":"turn/start"`) {
		t.Fatalf("Codex preparation: %s %s", raw, b)
	}
}
