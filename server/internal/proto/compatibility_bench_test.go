package proto

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// sessionListJSON is a busy machine's list as a node or a manager sends it: 150 sessions with 600 bytes of text each.
func sessionListJSON(tb testing.TB) []byte {
	list := make([]Session, 150)
	for i := range list {
		at := time.Unix(1700000000+int64(i), 0)
		list[i] = Session{MachineID: "box", SessionID: fmt.Sprintf("s%03d", i), Title: "Run the tests", Cwd: "/Users/me/projects/app",
			Status: StatusIdle, State: StateIdle, Agent: "claude", Model: "claude-test", SpaceKind: "herdr", SpaceID: "w1",
			UpdatedAt: at, ActivityAt: &at, LastText: strings.Repeat("x", 600), StartedByPersona: i%2 == 0}
	}
	raw, err := json.Marshal(map[string]any{"sessions": list})
	if err != nil {
		tb.Fatal(err)
	}
	return raw
}

// BenchmarkUnmarshalSessionList decodes the list, as the hub and the manager do on every list a node or a manager
// sends (the node's sessions.changed, the manager's fleet.sessions).
func BenchmarkUnmarshalSessionList(b *testing.B) {
	raw := sessionListJSON(b)
	b.SetBytes(int64(len(raw)))
	b.ReportAllocs()
	for b.Loop() {
		var p struct {
			Sessions []Session `json:"sessions"`
		}
		if err := json.Unmarshal(raw, &p); err != nil || len(p.Sessions) != 150 {
			b.Fatal(len(p.Sessions), err)
		}
	}
}

// Each session is decoded straight from the list's decoder (UnmarshalJSONFrom), not read out and decoded again into
// a copy: 355 allocations for the list measured, 661 before.
func TestSessionListDecodeAllocations(t *testing.T) {
	raw := sessionListJSON(t)
	var p struct {
		Sessions []Session `json:"sessions"`
	}
	p.Sessions = make([]Session, 0, 150)
	n := testing.AllocsPerRun(20, func() {
		p.Sessions = p.Sessions[:0]
		if err := json.Unmarshal(raw, &p); err != nil || len(p.Sessions) != 150 {
			t.Fatal(len(p.Sessions), err)
		}
	})
	if n > 366 {
		t.Fatalf("decoding 150 sessions allocates %v times", n)
	}
}
