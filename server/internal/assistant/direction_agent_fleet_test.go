package assistant

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/devices"
	"github.com/nwverse/persona/server/internal/devicewire"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// The manager's real node connection masks its last list on disconnect. Exercise that path, then reconnect
// with an authoritative idle/list omission. No direction GET is needed to record the last working turn.
func TestDirectionAgentCountsAfterActualNodeDisconnect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	connections := make(chan net.Conn, 1)
	host := &readinessDevicesHost{reported: make(chan struct{})}
	var s *Service
	d := devices.New(devices.Options{Hub: host, DialNode: func(string) func(context.Context) (net.Conn, error) {
		return func(ctx context.Context) (net.Conn, error) {
			select {
			case c := <-connections:
				return c, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}, Detached: func(machine string) { s.NodeDetached(machine) }})
	f := newFakeHub()
	f.hello.Assistant.MachineID, f.hello.Assistant.Machines = "box", []string{"box"}
	st := &memStore{}
	s = New(Options{Devices: d, Hub: f, Store: func() Store { return st }, Logf: testLogf(t)})
	s.running.Store(&ctx)
	s.dtree.after = newFakeClock().After
	withSettings(t, f, s)
	t.Cleanup(func() { drainFleetBackground(t, s, cancel) })
	setSettings(t, s, func(v *Settings) {
		v.MachineID, v.Main, v.Leaders = "box", treeMain, []string{"conv_lead_a", "conv_lead_b"}
	})
	d.Configure(devicewire.Config{HubID: "test", Machines: []json.RawMessage{json.RawMessage(`{"id":"box","name":"Box","kind":"link"}`)}})
	done := make(chan struct{})
	go func() { defer close(done); _ = d.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("device loop did not stop")
		}
	})
	connect := func(rows []proto.Session) (*rpc.Peer, net.Conn) {
		manager, node := net.Pipe()
		p := rpc.New("fixture", node)
		p.OnRequest = func(_ context.Context, method string, _ json.RawMessage) (any, error) {
			switch method {
			case "node.info":
				return proto.NodeInfo{Hostname: "box", OS: "linux", Arch: "amd64", Home: "/home/fixture", Version: "test"}, nil
			case "sessions.list":
				return map[string]any{"sessions": rows}, nil
			}
			return map[string]any{}, nil
		}
		go func() { _ = p.Serve(ctx, node); _ = node.Close() }()
		connections <- manager
		t.Cleanup(func() { _ = node.Close() })
		return p, node
	}
	waitRow := func(id, status string) {
		t.Helper()
		waitFor(t, id+"/"+status, func() bool {
			row, ok := d.Session("box", id)
			return ok && row.Status == status
		})
	}
	a := proto.Session{SessionID: "conv_lead_a", Assistant: true, Cwd: "/home/fixture/.persona/assistant", Status: proto.StatusWorking}
	b := proto.Session{SessionID: "conv_lead_b", Assistant: true, Cwd: "/home/fixture/.persona/assistant", State: proto.StateWorking,
		BackgroundWorking: true, Subagents: []proto.Subagent{{ID: "working-child", Running: true}}}
	_, node := connect([]proto.Session{a, b})
	waitRow(a.SessionID, proto.StatusWorking)
	raw, ok := d.Session("box", b.SessionID)
	if !ok || raw.Status != "" || raw.State != proto.StateWorking || !raw.BackgroundWorking {
		t.Fatalf("old node's background-only parent was not cached: %+v", raw)
	}
	_ = node.Close()
	waitRow(a.SessionID, proto.StatusUnknown)
	checkDirectionAgentCounts(t, s, [2]int{1, 0}, [2]int{1, 1}) // B's child works, its own turn has ended
	a.Status, b.Status = proto.StatusIdle, proto.StatusBlocked
	b.State, b.BackgroundWorking, b.Subagents = "", false, nil
	peer, _ := connect([]proto.Session{a, b})
	waitRow(b.SessionID, proto.StatusBlocked)
	checkDirectionAgentCounts(t, s, [2]int{0, 1}, [2]int{1, 0}) // host still has an open turn paused for permission
	b.Status = proto.StatusWorking
	if err := peer.Notify("sessions.changed", map[string]any{"sessions": []proto.Session{a, b}}); err != nil {
		t.Fatal(err)
	}
	waitRow(b.SessionID, proto.StatusWorking)
	checkDirectionAgentCounts(t, s, [2]int{0, 1}, [2]int{1, 0})
	if err := peer.Notify("sessions.changed", map[string]any{"sessions": []proto.Session{a}}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "authoritative list without previously working B", func() bool { _, ok := d.Session("box", b.SessionID); return !ok })
	checkDirectionAgentCounts(t, s, [2]int{0, 1}, [2]int{0, 1})
}
