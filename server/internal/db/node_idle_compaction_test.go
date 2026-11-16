package db

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

func TestNodeIdleCompactionStateRoundTrip(t *testing.T) {
	d, ctx := open(t), context.Background()
	for _, tc := range []struct {
		name string
		raw  string
	}{
		{"older_node", ""},
		{"attempted", `{"policy":{"enabled":true,"idle_minutes":30,"context_tokens":300000},"since":"2026-01-02T03:04:05Z","tokens":450000,"attempted":true,"future":{"keep":true}}`},
		{"rearmed", `{"policy":{"enabled":false,"idle_minutes":45,"context_tokens":600000},"since":"2026-01-02T04:05:06Z","tokens":90000,"attempted":false}`},
		{"cleared", ""},
		{"null", "null"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wire := map[string]any{"launcher": "claude", "cwd": "/example/main"}
			if tc.raw != "" {
				wire["idle_compaction"] = json.RawMessage(tc.raw)
			}
			raw, err := json.Marshal(wire)
			if err != nil {
				t.Fatal(err)
			}
			var state NodeSession
			if err := json.Unmarshal(raw, &state); err != nil {
				t.Fatal(err)
			}
			if err := d.PutNodeSession(ctx, "example-machine", "example-main", state); err != nil {
				t.Fatal(err)
			}
			var model json.RawMessage
			if err := d.pool.QueryRow(ctx, `SELECT value FROM settings WHERE key = 'node_model.example-machine.example-main'`).Scan(&model); err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(model, &fields); err != nil {
				t.Fatal(err)
			}
			var want, stored any
			if tc.raw != "" {
				if err := json.Unmarshal([]byte(tc.raw), &want); err != nil {
					t.Fatal(err)
				}
			}
			if len(fields["idle_compaction"]) > 0 {
				if err := json.Unmarshal(fields["idle_compaction"], &stored); err != nil {
					t.Fatal(err)
				}
			}
			if !reflect.DeepEqual(stored, want) {
				t.Fatalf("stored idle compaction = %s, want %s", fields["idle_compaction"], tc.raw)
			}
			sessions, err := d.NodeSessions(ctx, "example-machine")
			if err != nil {
				t.Fatal(err)
			}
			raw, err = json.Marshal(sessions["example-main"])
			if err != nil {
				t.Fatal(err)
			}
			fields = nil
			if err := json.Unmarshal(raw, &fields); err != nil {
				t.Fatal(err)
			}
			var loaded any
			if len(fields["idle_compaction"]) > 0 {
				if err := json.Unmarshal(fields["idle_compaction"], &loaded); err != nil {
					t.Fatal(err)
				}
			}
			if !reflect.DeepEqual(loaded, want) {
				t.Fatalf("loaded idle compaction = %s, want %s", fields["idle_compaction"], tc.raw)
			}
			if want == nil && len(fields["idle_compaction"]) > 0 {
				t.Fatal("absent idle compaction must remain omitted on state.load")
			}
		})
	}
}
