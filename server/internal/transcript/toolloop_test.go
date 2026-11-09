package transcript

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

func loopClaude(d *LoopDetector, n int, tool string, input any, result string, at time.Time) {
	call, _ := json.Marshal(map[string]any{"type": "assistant", "timestamp": at, "message": map[string]any{"content": []any{map[string]any{"type": "tool_use", "id": fmt.Sprint(n), "name": tool, "input": input}}}})
	out, _ := json.Marshal(map[string]any{"type": "user", "timestamp": at, "message": map[string]any{"content": []any{map[string]any{"type": "tool_result", "tool_use_id": fmt.Sprint(n), "content": result}}}})
	d.Line(call)
	d.Line(out)
}

func TestToolLoopPatterns(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		tools   []string
		results []string
		step    time.Duration
		count   int
	}{
		{"same below threshold", []string{"Read", "Read", "Read"}, nil, time.Second, 0},
		{"same", []string{"Read", "Read", "Read", "Read"}, nil, time.Second, 4},
		{"two-cycle", []string{"Read", "Edit", "Read", "Edit", "Read", "Edit"}, nil, time.Second, 6},
		{"three-cycle", []string{"Read", "Edit", "Grep", "Read", "Edit", "Grep", "Read", "Edit", "Grep"}, nil, time.Second, 9},
		{"different results", []string{"Read", "Read", "Read", "Read"}, []string{"a", "b", "a", "b"}, time.Second, 0},
		{"slow polling", []string{"Read", "Read", "Read", "Read"}, nil, 30 * time.Second, 0},
		{"wait tool", []string{"Monitor", "Monitor", "Monitor", "Monitor"}, nil, time.Second, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var d LoopDetector
			for i, tool := range tc.tools {
				result := "ok"
				if tc.results != nil {
					result = tc.results[i]
				}
				loopClaude(&d, i, tool, map[string]any{"file_path": "file.go"}, result, start.Add(time.Duration(i)*tc.step))
			}
			got := d.Candidate()
			if tc.count == 0 {
				if got != nil {
					t.Fatalf("unexpected loop: %+v", got)
				}
				return
			}
			if got == nil || got.Count != tc.count || !got.Since.Equal(start) {
				t.Fatalf("candidate = %+v, want count %d since %v", got, tc.count, start)
			}
		})
	}
}

func TestToolLoopCeilingAndReset(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var d LoopDetector
	for i := 0; i < 120; i++ {
		loopClaude(&d, i, "Read", map[string]any{"file_path": "a"}, "same", start.Add(time.Duration(i)*time.Second))
	}
	if got := d.Candidate(); got == nil || got.Count != 120 || !got.Since.Equal(start) {
		t.Fatalf("candidate = %+v", got)
	}
	copy := d.Clone()
	copy.Line([]byte(`{"type":"user","message":{"content":"new prompt"}}`))
	if copy.Candidate() != nil || d.Candidate() == nil {
		t.Fatal("new prompt must reset only its detector")
	}
	d.Line([]byte(`{"type":"system","subtype":"turn_duration"}`))
	if d.Candidate() != nil {
		t.Fatal("turn end must reset")
	}
}

func TestToolLoopPollingAndPrivateData(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, command := range []string{"gh pr checks 42", "gh run view 1", "tail -n 10 server.log", "sleep 1", "while true; do check; done"} {
		var d LoopDetector
		for i := 0; i < 5; i++ {
			loopClaude(&d, i, "Bash", map[string]any{"command": command}, "same", start.Add(time.Duration(i)*time.Second))
		}
		if got := d.Candidate(); got != nil {
			t.Fatalf("polling %q: %+v", command, got)
		}
	}
	var d LoopDetector
	for i := 0; i < 4; i++ {
		loopClaude(&d, i, "Read", map[string]any{"file_path": "private-input"}, "private-output", start.Add(time.Duration(i)*time.Second))
	}
	b, _ := json.Marshal(d.Candidate())
	if string(b) != `{"tool":"Read","count":4,"since":"2026-01-01T00:00:00Z"}` {
		t.Fatalf("summary = %s", b)
	}
}

func TestToolLoopChangedInputAndQueuedPrompt(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var d LoopDetector
	for i := 0; i < 4; i++ {
		loopClaude(&d, i, "Read", map[string]any{"path": "one"}, "ok", start.Add(time.Duration(i)*time.Second))
	}
	if d.Candidate() == nil {
		t.Fatal("candidate missing")
	}
	line := `{"type":"assistant","timestamp":"2026-01-01T00:00:04Z","message":{"content":[{"type":"tool_use","id":"next","name":"Read","input":{"path":"two"}}]}}`
	d.Line([]byte(line))
	if d.Candidate() != nil {
		t.Fatal("a different call must clear the previous candidate immediately")
	}
	d.Line([]byte(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"next","content":"ok"}]}}`))
	for i := 5; i < 9; i++ {
		loopClaude(&d, i, "Read", map[string]any{"path": "one"}, "ok", start.Add(time.Duration(i)*time.Second))
	}
	if d.Candidate() == nil {
		t.Fatal("new candidate missing")
	}
	d.Line([]byte(`{"type":"attachment","attachment":{"type":"queued_command","commandMode":"prompt","origin":{"kind":"human"}}}`))
	if d.Candidate() != nil || len(d.pending) != 0 {
		t.Fatal("queued user message must reset detection")
	}
	if loopHash([]byte(`{"number":9007199254740992}`)) == loopHash([]byte(`{"number":9007199254740993}`)) {
		t.Fatal("large JSON numbers lost precision")
	}
}

func TestToolLoopCodex(t *testing.T) {
	var d LoopDetector
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 4; i++ {
		at := start.Add(time.Duration(i) * time.Second)
		call, _ := json.Marshal(map[string]any{"type": "response_item", "timestamp": at, "payload": map[string]any{"type": "function_call", "name": "exec_command", "call_id": fmt.Sprint(i), "arguments": `{"cmd":"go test"}`}})
		out, _ := json.Marshal(map[string]any{"type": "response_item", "timestamp": at, "payload": map[string]any{"type": "function_call_output", "call_id": fmt.Sprint(i), "output": []any{map[string]any{"type": "input_text", "text": "same"}}}})
		d.Line(call)
		d.Line(out)
	}
	if got := d.Candidate(); got == nil || got.Count != 4 {
		t.Fatalf("candidate = %+v", got)
	}
	d.Line([]byte(`{"type":"event_msg","payload":{"type":"task_started"}}`))
	if d.Candidate() != nil {
		t.Fatal("new turn must reset")
	}
}
