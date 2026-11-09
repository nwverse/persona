package assistant

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// BenchmarkSessionsChangedEvent: a node's sessions.changed for 300 sessions as the assistant takes it (the devices
// have the list already, as in production).
func BenchmarkSessionsChangedEvent(b *testing.B) {
	f := newFakeHub()
	d := newFleetDevs()
	d.add("mac", "Mac")
	s := New(Options{Hub: f, Logf: func(string, ...any) {}, Devices: d})
	list := make([]proto.Session, 300)
	for i := range list {
		list[i] = proto.Session{SessionID: fmt.Sprintf("s%03d", i), Cwd: "/code/app", Agent: "claude",
			Status: proto.StatusDone, PaneID: fmt.Sprintf("p%d", i), SpaceName: "app", Title: "a session",
			LastText: strings.Repeat("the last words of the turn ", 8)}
	}
	params, _ := json.Marshal(map[string]any{"sessions": list})
	d.setSessions("mac", list)
	b.ReportAllocs()
	for b.Loop() {
		s.NodeEvent("mac", "sessions.changed", params)
	}
}

// A sessions.changed for a machine the devices do not have is decoded from the event, as before; one they have is
// their list.
func TestSessionsChangedEventSource(t *testing.T) {
	ft := newFleetTestNoStore(t)
	handed := make(chan DeviceSessions, 2)
	ft.s.sessionsSeen = func(d DeviceSessions) { handed <- d }
	next := func() DeviceSessions {
		t.Helper()
		select {
		case d := <-handed:
			return d
		case <-time.After(5 * time.Second):
			t.Fatal("SessionsChanged was not handed the list")
			return DeviceSessions{}
		}
	}
	params, _ := json.Marshal(map[string]any{"sessions": []proto.Session{{SessionID: "x1"}, {SessionID: "x2"}}})
	ft.s.NodeEvent("elsewhere", "sessions.changed", params)
	if d := next(); d.MachineID != "elsewhere" || len(d.Sessions) != 2 || d.Sessions[1].SessionID != "x2" ||
		d.Sessions[0].MachineID != "elsewhere" || d.Sessions[1].MachineID != "elsewhere" {
		t.Fatalf("decoded: %+v", d)
	}
	if got := ft.s.fleetPrevious("elsewhere"); len(got) != 2 || !hasKeys(got, "x1", "x2") {
		t.Fatalf("decoded, kept: %+v", got)
	}
	ft.d.setSessions("mac", []proto.Session{{SessionID: "s1", Title: "kept"}})
	ft.s.NodeEvent("mac", "sessions.changed", params)
	if d := next(); len(d.Sessions) != 1 || d.Sessions[0].SessionID != "s1" || d.Sessions[0].Title != "kept" ||
		d.Sessions[0].MachineID != "mac" {
		t.Fatalf("the devices' list: %+v", d)
	}
	if got := ft.s.fleetPrevious("mac"); len(got) != 1 || !hasKeys(got, "s1") {
		t.Fatalf("the devices' list, kept: %+v", got)
	}
}

func hasKeys[V any](m map[string]V, keys ...string) bool {
	for _, k := range keys {
		if _, ok := m[k]; !ok {
			return false
		}
	}
	return true
}
