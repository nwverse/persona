package assistant

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

func BenchmarkReadinessMainLargeInventory(b *testing.B) {
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
	if _, ok := s.readinessMain(ctx, "box", "session-00000"); !ok {
		b.Fatal("main session not found")
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, ok := s.readinessMain(ctx, "box", "session-00000"); !ok {
			b.Fatal("main session not found")
		}
	}
}
