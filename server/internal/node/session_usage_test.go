package node

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

// usage.sessions (#12936): a session's transcript and its subagents, each request once by message.id, in four kinds.
func TestSessionUsageSumsTranscriptAndSubagents(t *testing.T) {
	n, home := testNode(t)
	const id = "aaaaaaaa-0000-0000-0000-000000012936"
	proj := filepath.Join(home, ".claude", "projects", "-work")
	dir := filepath.Join(proj, id, "subagents")
	if err := os.MkdirAll(filepath.Join(dir, "workflows", "run1"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(proj, id+".jsonl")
	writeUsageLines(t, path, usagePrompt,
		// m1 over two content blocks, its output growing: counted once at the most
		usageReply("m1", "claude-opus-5-5", `{"input_tokens":10,"cache_read_input_tokens":1000,"cache_creation_input_tokens":100,"output_tokens":5}`),
		usageReply("m1", "claude-opus-5-5", `{"input_tokens":10,"cache_read_input_tokens":1000,"cache_creation_input_tokens":100,"output_tokens":50}`),
		usageReply("m2", "claude-opus-5-5", `{"input_tokens":1,"cache_read_input_tokens":2000,"output_tokens":20}`),
		// a repeat of m1 further on (a resumed copy): not counted again
		usageReply("m1", "claude-opus-5-5", `{"input_tokens":10,"cache_read_input_tokens":1000,"cache_creation_input_tokens":100,"output_tokens":50}`),
		usageReply("x", "<synthetic>", `{"input_tokens":0,"output_tokens":0}`),
	)
	writeUsageLines(t, filepath.Join(dir, "agent-s1.jsonl"), usagePrompt,
		usageReply("s1r1", "claude-sonnet-5-5", `{"input_tokens":3,"cache_read_input_tokens":300,"cache_creation_input_tokens":30,"output_tokens":7}`),
		// a copy of the session's m2 in the subagent's file: the session already counted it
		usageReply("m2", "claude-opus-5-5", `{"input_tokens":1,"cache_read_input_tokens":2000,"output_tokens":20}`),
	)
	writeUsageLines(t, filepath.Join(dir, "agent-s1.meta.json"), `{"agentType":"Explore","description":"Find the usage code"}`)
	writeUsageLines(t, filepath.Join(dir, "workflows", "run1", "agent-w1.jsonl"), usagePrompt,
		usageReply("w1r1", "claude-opus-5-5", `{"input_tokens":2,"output_tokens":4}`))

	got, err := n.sessionUsage(t.Context(), proto.SessionUsageParams{SessionIDs: []string{id, "bbbbbbbb-0000-0000-0000-000000000001"}})
	if err != nil {
		t.Fatal(err)
	}
	wantFound := []proto.SessionUsageFound{{SessionID: id, Found: true, Agent: "claude"}, {SessionID: "bbbbbbbb-0000-0000-0000-000000000001"}}
	if !reflect.DeepEqual(got.Sessions, wantFound) {
		t.Fatalf("sessions: %+v", got.Sessions)
	}
	want := []proto.SessionUsageRow{
		{SessionID: id, Agent: "claude", Model: "claude-opus-5-5", Day: "2026-11-12", Input: 11, CacheWrite: 100, CacheRead: 3000, Output: 70, Requests: 2},
		{SessionID: id, Subagent: "s1", SubagentType: "Explore", SubagentTitle: "Find the usage code", Agent: "claude", Model: "claude-sonnet-5-5",
			Day: "2026-11-12", Input: 3, CacheWrite: 30, CacheRead: 300, Output: 7, Requests: 1},
		{SessionID: id, Subagent: "run1/w1", Agent: "claude", Model: "claude-opus-5-5", Day: "2026-11-12", Input: 2, Output: 4, Requests: 1},
	}
	if !reflect.DeepEqual(got.Rows, want) {
		t.Fatalf("rows:\n got %+v\nwant %+v", got.Rows, want)
	}

	// The file grows: the next call reads only the new line and adds it.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(usageReply("m3", "claude-opus-5-5", `{"input_tokens":5,"output_tokens":5}`) + "\n")
	f.Close()
	before := n.sessionUsageCache.files[path].off
	got, _ = n.sessionUsage(t.Context(), proto.SessionUsageParams{SessionIDs: []string{id}})
	if got.Rows[0].Input != 16 || got.Rows[0].Requests != 3 {
		t.Fatalf("after growth: %+v", got.Rows[0])
	}
	if n.sessionUsageCache.files[path].off <= before {
		t.Fatal("the cache did not keep its offset")
	}
}

// A Codex rollout is read with the Codex parser: each grown token_count is a request.
func TestSessionUsageCodexRollout(t *testing.T) {
	n, home := testNode(t)
	const id = "019a0000-0000-7000-8000-000000012936"
	dir := filepath.Join(home, ".codex", "sessions", "2026", "11", "12")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeUsageLines(t, filepath.Join(dir, "rollout-2026-11-12T08-00-00-"+id+".jsonl"),
		`{"timestamp":"2026-11-12T08:00:00Z","type":"session_meta","payload":{"id":"`+id+`","cwd":"/work"}}`,
		`{"timestamp":"2026-11-12T08:00:00Z","type":"turn_context","payload":{"model":"gpt-5.5"}}`,
		`{"timestamp":"2026-11-12T08:00:01Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":1000,"cached_input_tokens":800,"output_tokens":50,"total_tokens":1050},"total_token_usage":{"total_tokens":1050}}}}`,
		`{"timestamp":"2026-11-12T08:00:02Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":1000,"cached_input_tokens":800,"output_tokens":50,"total_tokens":1050},"total_token_usage":{"total_tokens":1050}}}}`,
	)
	got, err := n.sessionUsage(t.Context(), proto.SessionUsageParams{SessionIDs: []string{id}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 1 {
		t.Fatalf("rows: %+v (sessions %+v)", got.Rows, got.Sessions)
	}
	r := got.Rows[0]
	if r.Agent != "codex" || r.Model != "gpt-5.5" || r.Input != 200 || r.CacheRead != 800 || r.Output != 50 || r.Requests != 1 {
		t.Fatalf("row: %+v", r)
	}
}
