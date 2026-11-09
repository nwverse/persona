package assistant

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestReadinessMainLocalParityAndStateContinuity(t *testing.T) {
	ft := newFleetTest(t)
	ft.f.hello.Assistant.MachineID = "mac"
	ft.f.hello.Assistant.Machines = []string{"mac"}
	ft.f.devices = []Device{{ID: "mac", Home: "/home/u"}}
	rows := []proto.Session{
		{SessionID: "main", Cwd: "/home/u/.persona/assistant", Status: proto.StatusDone, LastText: "Example answer"},
		{SessionID: "coding", Cwd: "/code/example", Status: proto.StatusWorking},
		{SessionID: "pane", Cwd: "/home/u/.persona/assistant", PaneID: "p1", Status: proto.StatusDone},
	}
	ft.d.setSessions("mac", rows)
	ft.s.fleet.rep.sessions = map[string]SessionView{"main": {Title: "Persona", Origin: "app"}}
	ctx := context.Background()
	full, err := ft.s.sessions(ctx, "mac")
	if err != nil {
		t.Fatal(err)
	}
	key := [2]string{"mac", "coding"}
	mark := ft.s.fleet.stateSince[key]
	ft.s.fleet.hooked = map[[2]string]bool{key: true}
	for _, id := range []string{"main", "pane", "coding", "missing", ""} {
		got, ok := ft.s.readinessMain(ctx, "mac", id)
		var want proto.Session
		wantOK := false
		for _, row := range full {
			if row.SessionID == id {
				want, wantOK = row, true
			}
		}
		a, _ := json.Marshal(got)
		b, _ := json.Marshal(want)
		if ok != wantOK || string(a) != string(b) {
			t.Fatalf("%q: got %s/%v, want %s/%v", id, a, ok, b, wantOK)
		}
		if ft.s.fleet.stateSince[key] != mark || !ft.s.fleet.hooked[key] {
			t.Fatal("readiness removed another session's state or hook")
		}
	}
	if rows[0].Assistant || rows[0].Title != "" {
		t.Fatal("readiness changed the source inventory")
	}
}

func TestReadinessMainLegacyFallback(t *testing.T) {
	f := healthHub()
	s := newTestService(t, f)
	got, ok := s.readinessMain(context.Background(), "server", "main-0000-0000")
	if !ok || got.SessionID != "main-0000-0000" || got.Title != "Persona" {
		t.Fatalf("legacy main lookup: %#v/%v", got, ok)
	}
}
