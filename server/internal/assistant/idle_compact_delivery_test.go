package assistant

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/proto"
)

func TestIdleCompactPolicyReachesMainDeliveryPaths(t *testing.T) {
	ctx := context.Background()
	f := newFakeHub().withMachine()
	s := newTestService(t, f)
	main, err := s.start(ctx, StartParams{Main: true, Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	want := assistantwire.IdleCompaction{Enabled: true, IdleMinutes: 30, ContextTokens: 300000}
	wantWindow := defaultMainCompactWindow
	check := func(params map[string]any) {
		t.Helper()
		raw, _ := json.Marshal(params["idle_compaction"])
		var got assistantwire.IdleCompaction
		if err := json.Unmarshal(raw, &got); err != nil || got != want {
			t.Fatalf("idle compaction policy = %s (%v), want %+v", raw, err, want)
		}
		raw, _ = json.Marshal(params["main_compact_window"])
		var window int
		if err := json.Unmarshal(raw, &window); err != nil || window != wantWindow {
			t.Fatalf("main compact window = %s (%v), want %d alongside idle policy", raw, err, wantWindow)
		}
	}
	check(f.lastSend(t))
	f.setSessions("server", proto.Session{SessionID: main.SessionID, Assistant: true})
	// Apps use sendModel/SendParams, rather than dispatching through Service.call.
	params, err := s.SendParams(ctx, SendParams{MachineID: "server", SessionID: main.SessionID, Params: map[string]any{"text": "next"}})
	if err != nil {
		t.Fatal(err)
	}
	check(params)
	// Supervision and conversation relays dispatch through Service.call.
	if err := s.deliverRelay(ctx, "server", main.SessionID, relayMsg{Tool: superviseTool, Text: "check tasks", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	check(f.lastSend(t))

	off, minutes, tokens := false, 45, 450000
	wantWindow = 250000
	setSettings(t, s, func(v *Settings) {
		v.IdleAutoCompact, v.IdleAutoCompactMinutes, v.IdleAutoCompactTokens = &off, &minutes, &tokens
		v.MainCompactWindow = &wantWindow
	})
	want = assistantwire.IdleCompaction{Enabled: false, IdleMinutes: minutes, ContextTokens: tokens}
	params, err = s.SendParams(ctx, SendParams{MachineID: "server", SessionID: main.SessionID, Params: map[string]any{"text": "next"}})
	if err != nil {
		t.Fatal(err)
	}
	check(params)
	if err := s.deliverRelay(ctx, "server", main.SessionID, relayMsg{Tool: "conversation_send", Text: "note", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	check(f.lastSend(t))
	if _, err := s.start(ctx, StartParams{Text: "side chat"}); err != nil {
		t.Fatal(err)
	}
	if _, exists := f.lastSend(t)["idle_compaction"]; exists {
		t.Fatal("side chat received the main chat's idle compaction policy")
	}
	if _, exists := f.lastSend(t)["main_compact_window"]; exists {
		t.Fatal("side chat received the main chat's compact window")
	}
}
