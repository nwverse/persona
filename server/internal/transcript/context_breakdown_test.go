package transcript

import (
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestBuildContextBreakdownExactAndEstimated(t *testing.T) {
	// Native exact breakdown wins over byte stats and fills missing free/auto_compact_threshold.
	native := &proto.ContextBreakdown{
		Source:         proto.ContextBreakdownExact,
		SystemAndRules: proto.Int(5000),
		ToolsAndMCP:    proto.Int(7000),
		Conversation:   proto.Int(8000),
		ToolOutputs:    proto.Int(10000),
	}
	got := BuildContextBreakdown(ContextStats{ConversationBytes: 4000, Native: native}, 30000, 200000, 167000, true)
	if got == nil || got.Source != proto.ContextBreakdownExact {
		t.Fatalf("expected exact breakdown, got %+v", got)
	}
	if *got.SystemAndRules != 5000 || *got.ToolsAndMCP != 7000 || *got.Conversation != 8000 || *got.ToolOutputs != 10000 {
		t.Fatalf("native categories altered: %+v", got)
	}
	if got.Free == nil || *got.Free != 170000 {
		t.Fatalf("free = %v, want 170000", got.Free)
	}
	if got.AutoCompactThreshold == nil || *got.AutoCompactThreshold != 167000 {
		t.Fatalf("auto_compact_threshold = %v, want 167000", got.AutoCompactThreshold)
	}

	// Full window estimated breakdown: attributes unrecorded prompt prefix to system_and_rules,
	// omits tools_and_mcp (not recorded in transcript), and conserves sum == contextTokens.
	est := BuildContextBreakdown(ContextStats{
		ConversationBytes: 4000, // 1000 tokens
		ToolOutputBytes:   8000, // 2000 tokens
	}, 25000, 200000, 180000, true)
	if est == nil || est.Source != proto.ContextBreakdownEstimated {
		t.Fatalf("expected estimated breakdown, got %+v", est)
	}
	if est.ToolsAndMCP != nil {
		t.Fatalf("tools_and_mcp must be omitted when unobserved, got %d", *est.ToolsAndMCP)
	}
	if est.SystemAndRules == nil || *est.SystemAndRules != 22000 {
		t.Fatalf("system_and_rules = %v, want 22000", est.SystemAndRules)
	}
	if est.Conversation == nil || *est.Conversation != 1000 || est.ToolOutputs == nil || *est.ToolOutputs != 2000 {
		t.Fatalf("conversation/tool_outputs = %v/%v, want 1000/2000", est.Conversation, est.ToolOutputs)
	}
	if est.Free == nil || *est.Free != 175000 || est.AutoCompactThreshold == nil || *est.AutoCompactThreshold != 180000 {
		t.Fatalf("free/threshold = %v/%v", est.Free, est.AutoCompactThreshold)
	}

	// Partial window (tail read only): proportionally apportions observed categories, omits
	// unobserved system_and_rules and tools_and_mcp without fabricating them.
	partial := BuildContextBreakdown(ContextStats{
		ConversationBytes: 4000, // 1:3 ratio with tool outputs
		ToolOutputBytes:   12000,
	}, 10000, 0, 0, false)
	if partial == nil || partial.Source != proto.ContextBreakdownEstimated {
		t.Fatalf("expected estimated partial breakdown, got %+v", partial)
	}
	if partial.SystemAndRules != nil || partial.ToolsAndMCP != nil || partial.Free != nil || partial.AutoCompactThreshold != nil {
		t.Fatalf("unobserved fields must be omitted on partial window without context window: %+v", partial)
	}
	if *partial.Conversation != 2500 || *partial.ToolOutputs != 7500 {
		t.Fatalf("partial split = %d/%d, want 2500/7500", *partial.Conversation, *partial.ToolOutputs)
	}

	// Over-estimated bytes in full window scale proportionally and conserve exact sum == contextTokens.
	scaled := BuildContextBreakdown(ContextStats{
		SystemBytes:       4000,
		ConversationBytes: 12000,
		ToolOutputBytes:   24000,
	}, 1001, 200000, 250000, true)
	if scaled == nil {
		t.Fatal("expected scaled breakdown")
	}
	sum := *scaled.SystemAndRules + *scaled.Conversation + *scaled.ToolOutputs
	if sum != 1001 {
		t.Fatalf("scaled category sum = %d, want 1001", sum)
	}
	if *scaled.AutoCompactThreshold != 200000 {
		t.Fatalf("auto_compact_threshold should clamp to contextWindow 200000, got %d", *scaled.AutoCompactThreshold)
	}

	// Omission when unknown: zero context tokens or empty stats must return nil (never fabricate).
	if BuildContextBreakdown(ContextStats{ConversationBytes: 400}, 0, 200000, 160000, true) != nil {
		t.Fatal("zero contextTokens must omit ContextBreakdown")
	}
	if BuildContextBreakdown(ContextStats{}, 25000, 200000, 160000, true) != nil {
		t.Fatal("empty stats without native breakdown must omit ContextBreakdown")
	}
}

func TestClaudeTranscriptContextBreakdownAndCompactionReset(t *testing.T) {
	lines := []string{
		`{"type":"user","uuid":"u1","timestamp":"2026-11-21T10:00:00Z","message":{"role":"user","content":"` + strings.Repeat("hello world ", 100) + `"}}`,
		`{"type":"assistant","uuid":"a1","timestamp":"2026-11-21T10:00:02Z","message":{"role":"assistant","id":"m1","model":"claude-sonnet-4-6","content":[{"type":"tool_use","id":"tu1","name":"Bash","input":{"command":"` + strings.Repeat("echo test && ", 60) + `"}}],"usage":{"input_tokens":1000,"cache_read_input_tokens":20000,"output_tokens":50}}}`,
		`{"type":"user","uuid":"u2","timestamp":"2026-11-21T10:00:03Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu1","content":"` + strings.Repeat("output line\n", 200) + `"}]}}`,
		`{"type":"assistant","uuid":"a2","timestamp":"2026-11-21T10:00:05Z","message":{"role":"assistant","id":"m2","model":"claude-sonnet-4-6","content":[{"type":"text","text":"All checks passed cleanly."}],"usage":{"input_tokens":1200,"cache_read_input_tokens":22800,"output_tokens":20}}}`,
	}
	var m Meta
	for i, line := range lines {
		ParseLine(i+1, []byte(line), &m)
	}
	if m.ContextTokens != 24000 {
		t.Fatalf("ContextTokens = %d, want 24000", m.ContextTokens)
	}
	bd := BuildContextBreakdown(m.ContextStats, m.ContextTokens, 200000, 0, true)
	if bd == nil || bd.Source != proto.ContextBreakdownEstimated {
		t.Fatalf("expected estimated breakdown, got %+v", bd)
	}
	if bd.SystemAndRules == nil || bd.Conversation == nil || bd.ToolOutputs == nil || bd.Free == nil {
		t.Fatalf("expected populated categories, got %+v", bd)
	}
	if *bd.Conversation <= 0 || *bd.ToolOutputs <= 0 || *bd.SystemAndRules <= 0 {
		t.Fatalf("expected positive categories, got sys=%d conv=%d tools=%d", *bd.SystemAndRules, *bd.Conversation, *bd.ToolOutputs)
	}
	if got := *bd.SystemAndRules + *bd.Conversation + *bd.ToolOutputs; got != 24000 {
		t.Fatalf("category sum = %d, want 24000", got)
	}
	if *bd.Free != 176000 {
		t.Fatalf("free = %d, want 176000", *bd.Free)
	}

	// Compaction resets stats and ContextTokens until the next reply arrives.
	ParseLine(5, []byte(`{"type":"system","subtype":"compact_boundary","uuid":"c1","timestamp":"2026-11-21T10:01:00Z","compactMetadata":{"trigger":"auto","preTokens":24000}}`), &m)
	if !m.ContextStats.CompactedInWindow || m.ContextStats.ConversationBytes != 0 || m.ContextStats.ToolOutputBytes != 0 {
		t.Fatalf("compaction must reset stats: %+v", m.ContextStats)
	}
	if BuildContextBreakdown(m.ContextStats, m.ContextTokens, 200000, 0, true) != nil {
		t.Fatal("right after compaction before next reply, breakdown must be omitted")
	}

	// Native exact breakdown on a line takes precedence as "exact".
	ParseLine(6, []byte(`{"type":"assistant","uuid":"a3","timestamp":"2026-11-21T10:02:00Z","context_breakdown":{"source":"exact","system_and_rules":4000,"tools_and_mcp":3000,"conversation":2000,"tool_outputs":500},"message":{"role":"assistant","id":"m3","model":"claude-sonnet-4-6","content":[{"type":"text","text":"Resumed."}],"usage":{"input_tokens":500,"cache_read_input_tokens":9000,"output_tokens":10}}}`), &m)
	exact := BuildContextBreakdown(m.ContextStats, m.ContextTokens, 200000, 0, true)
	if exact == nil || exact.Source != proto.ContextBreakdownExact || *exact.ToolsAndMCP != 3000 || *exact.Free != 190500 {
		t.Fatalf("expected native exact breakdown, got %+v", exact)
	}
}

func TestCodexRolloutContextBreakdownAndCompaction(t *testing.T) {
	lines := []string{
		`{"timestamp":"2026-11-21T10:00:00Z","type":"session_meta","payload":{"cwd":"/work","cli_version":"0.170","originator":"codex_cli_rs"}}`,
		`{"timestamp":"2026-11-21T10:00:01Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","id":"u1","content":[{"type":"text","text":"Inspect the build logs please."}]}}}`,
		`{"timestamp":"2026-11-21T10:00:02Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution","id":"c1","command":["go","test","./..."],"aggregated_output":"PASS ok all packages","status":"completed"}}}`,
		`{"timestamp":"2026-11-21T10:00:03Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"AgentMessage","id":"a1","content":[{"type":"text","text":"All packages passed."}]}}}`,
		`{"timestamp":"2026-11-21T10:00:04Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"output_tokens":42,"total_tokens":15000},"last_token_usage":{"input_tokens":14000,"output_tokens":42},"model_context_window":256000}}}`,
	}
	var m Meta
	m.Codex = true
	for i, line := range lines {
		ParseLine(i+1, []byte(line), &m)
	}
	bd := BuildContextBreakdown(m.ContextStats, m.ContextTokens, m.ContextWindow, 0, true)
	if bd == nil || bd.Source != proto.ContextBreakdownEstimated {
		t.Fatalf("expected Codex estimated breakdown, got %+v", bd)
	}
	if *bd.Conversation <= 0 || *bd.ToolOutputs <= 0 || *bd.Free != 242000 {
		t.Fatalf("unexpected Codex breakdown: %+v", bd)
	}

	ParseLine(6, []byte(`{"timestamp":"2026-11-21T10:01:00Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"ContextCompaction","id":"cmp1"}}}`), &m)
	if m.ContextTokens != 0 || !m.ContextStats.CompactedInWindow {
		t.Fatalf("Codex compaction did not reset context: tokens=%d stats=%+v", m.ContextTokens, m.ContextStats)
	}
}

func TestFoldWorkPreservesTTFTMS(t *testing.T) {
	now := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	items := []proto.Item{
		{ID: "u1#0", Seq: 1, TS: now, Role: "user", Kind: proto.KindText, Text: "check"},
		{ID: "t1#0", Seq: 2, TS: now, Role: "assistant", Kind: proto.KindThinking, Text: "thinking...", TTFTMS: proto.Int64(420)},
		{ID: "tu1#0", Seq: 3, TS: now, Role: "assistant", Kind: proto.KindToolUse, ToolUseID: "id1", ToolName: "Read", ToolInput: []byte(`{"file_path":"a.go"}`)},
		{ID: "tr1#0", Seq: 4, TS: now, Role: "user", Kind: proto.KindToolResult, ToolUseID: "id1", Text: "package main"},
		{ID: "a1#0", Seq: 5, TS: now, Role: "assistant", Kind: proto.KindText, Text: "done"},
	}
	folded := FoldWork(items)
	if len(folded) != 3 || folded[1].Kind != proto.KindFold {
		t.Fatalf("expected folded run, got %+v", folded)
	}
	if folded[1].TTFTMS == nil || *folded[1].TTFTMS != 420 {
		t.Fatalf("folded item lost TTFTMS: %v", folded[1].TTFTMS)
	}
	// Historical folded run without TTFT keeps nil TTFTMS.
	items[1].TTFTMS = nil
	foldedNoTTFT := FoldWork(items)
	if foldedNoTTFT[1].TTFTMS != nil {
		t.Fatalf("historical fold must not fabricate TTFTMS: %v", *foldedNoTTFT[1].TTFTMS)
	}
}
