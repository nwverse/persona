package hub

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestHubSessionContextBreakdownPreservedInPushAndSummary(t *testing.T) {
	now := time.Date(2026, 11, 21, 14, 0, 0, 0, time.UTC)
	bd := &proto.ContextBreakdown{
		Source:               proto.ContextBreakdownEstimated,
		SystemAndRules:       proto.Int(18000),
		Conversation:         proto.Int(4000),
		ToolOutputs:          proto.Int(3000),
		Free:                 proto.Int(175000),
		AutoCompactThreshold: proto.Int(167000),
	}
	s := proto.Session{
		MachineID:     "m1",
		SessionID:     "s1",
		Title:         "Session with breakdown",
		Cwd:           "/work",
		Status:        proto.StatusIdle,
		State:         proto.StateIdle,
		ContextTokens: 25000,
		ContextWindow: 200000,
		UpdatedAt:     now,
	}
	s.SetContextBreakdown(bd)

	// Full push encoding via encodeSessions -> appendSession.
	encoded := encodeSessions([]proto.Session{s})
	if len(encoded) != 1 {
		t.Fatalf("expected 1 encoded session, got %d", len(encoded))
	}
	for _, want := range []string{
		`"context_breakdown":{`,
		`"source":"estimated"`,
		`"system_and_rules":18000`,
		`"conversation":4000`,
		`"tool_outputs":3000`,
		`"free":175000`,
		`"auto_compact_threshold":167000`,
	} {
		if !strings.Contains(string(encoded[0].raw), want) {
			t.Fatalf("encoded push row missing %s: %s", want, encoded[0].raw)
		}
	}
	if strings.Contains(string(encoded[0].raw), `"tools_and_mcp"`) {
		t.Fatalf("unavailable tools_and_mcp must stay omitted: %s", encoded[0].raw)
	}

	// Summary row encoding via runtimeSummary must also retain context_breakdown.
	sumRaw, err := json.Marshal(runtimeSummary(s))
	if err != nil {
		t.Fatalf("marshal runtimeSummary: %v", err)
	}
	var decoded proto.Session
	if err := json.Unmarshal(sumRaw, &decoded); err != nil {
		t.Fatalf("unmarshal runtimeSummary: %v", err)
	}
	if !decoded.ContextBreakdown().Equal(bd) {
		t.Fatalf("runtimeSummary lost ContextBreakdown:\ngot  %+v\nwant %+v", decoded.ContextBreakdown(), bd)
	}
}

func TestHubTTFTFallbackAndNodePassthrough(t *testing.T) {
	var tr hubTTFTTracker
	t0 := time.Date(2026, 11, 21, 14, 0, 0, 0, time.UTC)

	// Historical replay without live send/delta stays omitted.
	hist := []proto.Item{{ID: "h1#0", Seq: 1, TS: t0, Role: "assistant", Kind: proto.KindText, Text: "historical"}}
	if tr.annotate("m1", "s1", hist, false) || hist[0].TTFTMS != nil {
		t.Fatalf("historical replay must omit ttft_ms: %v", hist[0].TTFTMS)
	}

	// Hub fallback path: send armed -> first delta -> session.items without node ttft_ms.
	tr.arm("m1", "s1", t0)
	ms, ok := tr.firstToken("m1", "s1", t0.Add(310*time.Millisecond))
	if !ok || ms != 310 {
		t.Fatalf("hub firstToken = %d (%v), want 310 (true)", ms, ok)
	}
	live := []proto.Item{
		{ID: "u1#0", Seq: 1, TS: t0, Role: "user", Kind: proto.KindText, Text: "hello"},
		{ID: "a1#0", Seq: 2, TS: t0.Add(320 * time.Millisecond), Role: "assistant", Kind: proto.KindText, Text: "world"},
	}
	if !tr.annotate("m1", "s1", live, true) || live[1].TTFTMS == nil || *live[1].TTFTMS != 310 {
		t.Fatalf("hub fallback failed to attach ttft_ms: %+v", live[1].TTFTMS)
	}

	// Node-provided TTFTMS takes precedence and is never overwritten by Hub fallback.
	tr.arm("m1", "s2", t0)
	tr.firstToken("m1", "s2", t0.Add(500*time.Millisecond))
	fromNode := []proto.Item{
		{ID: "a2#0", Seq: 1, TS: t0.Add(510 * time.Millisecond), Role: "assistant", Kind: proto.KindText, Text: "fast", TTFTMS: proto.Int64(190)},
		{ID: "a2#1", Seq: 2, TS: t0.Add(700 * time.Millisecond), Role: "assistant", Kind: proto.KindText, Text: "tail"},
	}
	tr.annotate("m1", "s2", fromNode, true)
	if fromNode[0].TTFTMS == nil || *fromNode[0].TTFTMS != 190 {
		t.Fatalf("node-provided ttft_ms overwritten: %v", fromNode[0].TTFTMS)
	}
	if fromNode[1].TTFTMS != nil {
		t.Fatalf("second item should stay nil after node ttft_ms consumed turn: %v", *fromNode[1].TTFTMS)
	}
}

func TestHubMachineEventsAttachTTFTToStreamedItemsAndOmitOnUnarmed(t *testing.T) {
	h := &Hub{
		clients: map[*client]struct{}{},
	}
	sub := &client{
		subs: map[[2]string]bool{{"m1", "s-live"}: true, {"m1", "s-hist"}: true},
		out:  make(chan []byte, 8),
	}
	addTestClient(h, sub)
	m := &Machine{
		hub:         h,
		cfg:         MachineConfig{ID: "m1"},
		lastSeq:     map[string]int{},
		lastHistory: map[string]string{},
	}

	// Armed turn: delta arrives -> items event pushes ttft_ms to subscribers.
	h.ttft.arm("m1", "s-live", time.Now().Add(-120*time.Millisecond))
	m.onEvent("session.delta", json.RawMessage(`{"session_id":"s-live","text":"Hi"}`))
	// Drain delta frame.
	<-sub.out

	liveItemTS := time.Now().UTC().Format(time.RFC3339Nano)
	m.onEvent("session.items", json.RawMessage(`{"session_id":"s-live","items":[{"id":"a1#0","seq":1,"ts":"`+liveItemTS+`","role":"assistant","kind":"text","text":"Hi there"}]}`))
	liveFrame := <-sub.out
	if !strings.Contains(string(liveFrame), `"ttft_ms":`) {
		t.Fatalf("expected ttft_ms on live streamed item push, got %s", liveFrame)
	}

	// Unarmed session (historical/background without live turn): ttft_ms must be omitted.
	m.onEvent("session.items", json.RawMessage(`{"session_id":"s-hist","items":[{"id":"h1#0","seq":1,"ts":"`+liveItemTS+`","role":"assistant","kind":"text","text":"Replayed"}]}`))
	histFrame := <-sub.out
	if strings.Contains(string(histFrame), `"ttft_ms"`) {
		t.Fatalf("unarmed session push must omit ttft_ms, got %s", histFrame)
	}
}
