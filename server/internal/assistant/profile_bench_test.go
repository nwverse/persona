package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

// BenchmarkAssistantViewLargeInventory measures the complete response path:
// a small Persona list on a machine with thousands of coding conversations.
func BenchmarkAssistantViewLargeInventory(b *testing.B) {
	f := newFakeHub()
	f.hello.Assistant.MachineID = "box"
	f.hello.Assistant.Machines = []string{"box"}
	f.devices = []Device{{ID: "box", Home: "/home/u"}}
	d := newFleetDevs()
	d.add("box", "Computer")
	st := newFleetMem()
	s := New(Options{Hub: f, Devices: d, Store: func() Store { return st }})
	list := make([]proto.Session, 8200)
	for i := range list {
		list[i] = proto.Session{SessionID: fmt.Sprintf("session-%05d", i), Cwd: "/code/example", Agent: "claude",
			Status: proto.StatusDone, Title: "Example task", LastText: strings.Repeat("Example result. ", 32)}
		if i < 14 {
			list[i].Cwd = "/home/u/.persona/assistant"
		}
	}
	d.setSessions("box", list)
	ctx := context.Background()
	if got := s.view(ctx, false); len(got.Conversations) != 14 {
		b.Fatalf("conversations = %d, want 14", len(got.Conversations))
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := json.Marshal(s.view(ctx, false)); err != nil {
			b.Fatal(err)
		}
	}
}
