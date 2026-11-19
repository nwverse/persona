package devices

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/nodeproto"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// The manager asks for lanes when node.info announces them, and only then; a node that announces them and refuses
// keeps lines. Calls work every way, a bulk one included.
func TestMachineNegotiatesLanes(t *testing.T) {
	for _, c := range []struct {
		name            string
		announce, able  bool
		framed, refused bool
	}{
		{"new node", true, true, true, false},
		{"node without the capability", false, true, false, false},
		{"node that announces and refuses", true, false, false, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := newTestDevices(t)
			logs := &benchLog{}
			d.logf = func(f string, a ...any) { logs.add(fmt.Sprintf(f, a...)) }
			info := proto.NodeInfo{Hostname: "box", OS: "linux", Arch: "amd64", Home: "/home/me", Version: "test",
				Protocol: nodeproto.Version, ProtocolMin: nodeproto.MinManagerVersion, Capabilities: nodeproto.Required}
			if c.announce {
				info.Capabilities = append(info.Capabilities, nodeproto.NodeLanes)
			}
			n := &testNode{info: &info, lanes: c.able, unknown: map[string]bool{"rpc.lanes": !c.able}}
			m := connectedMachine(t, d, "lanes", n)
			m.mu.Lock()
			peer := m.peer
			m.mu.Unlock()
			if peer.Framed() != c.framed {
				t.Fatalf("framed=%t", peer.Framed())
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			chunk := strings.Repeat("d", 300_000) // several frames on the bulk lane
			if err := m.call(ctx, "file.upload.chunk", map[string]any{"upload_id": "u1", "offset": 0, "data": chunk}, nil); err != nil {
				t.Fatal(err)
			}
			if got := n.seen("file.upload.chunk"); len(got) != 1 || !strings.Contains(got[0], chunk) {
				t.Fatalf("the node read %d upload chunks", len(got))
			}
			want := "lanes=" + onOff(c.framed)
			eventually(t, 2*time.Second, "the connected line", func() bool { return logs.count(want) > 0 })
			if got := logs.count("staying with lines") > 0; got != c.refused {
				t.Fatalf("refusal logged=%t: %q", got, logs.since(0))
			}
		})
	}
}

// A forwarded port's bytes waiting for credit do not hold the hub's other events to the node (terminal input) behind
// them: they queue apart.
func TestNotesPortBytesDoNotHoldTerminalInput(t *testing.T) {
	d := newTestDevices(t)
	m := newMachine(d, MachineConfig{ID: "box", Name: "box", Kind: "ssh"})
	nodeIn, managerOut := io.Pipe()
	managerIn, nodeOut := io.Pipe()
	seen := &seenBytes{} // what reaches a node that reads everything and never acknowledges
	go func() { _, _ = io.Copy(seen, nodeIn) }()
	peer := rpc.New("h", managerOut)
	peer.EnableLanes(nodeproto.ControlLane, nodeproto.BulkLane)
	go func() { _ = peer.Serve(context.Background(), managerIn) }()
	t.Cleanup(func() { managerOut.Close(); nodeOut.Close(); peer.Close(nil) })
	upgraded := make(chan error, 1)
	go func() { upgraded <- peer.UpgradeLanes(context.Background()) }()
	eventually(t, 5*time.Second, "rpc.lanes asked", func() bool { return strings.Contains(seen.String(), "rpc.lanes") })
	if _, err := nodeOut.Write([]byte(`{"id":"h1","result":{"version":1}}` + "\n")); err != nil {
		t.Fatal(err)
	}
	if err := <-upgraded; err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.peer = peer
	m.mu.Unlock()
	chunk, _ := json.Marshal(map[string]any{"id": "p1", "data": strings.Repeat("A", 48<<10)})
	for range 4 { // more than a window: the later ones wait for credit that never comes
		m.queueNote("stream.data", chunk)
	}
	eventually(t, 5*time.Second, "port bytes waiting for credit", func() bool { return peer.LaneStats().InFlight >= 32<<10 })
	m.queueNote("terminal.input", json.RawMessage(`{"id":"t1","data":"bHM="}`))
	eventually(t, 5*time.Second, "terminal input on the wire", func() bool { return strings.Contains(seen.String(), "terminal.input") })
}

// seenBytes is what a node read, for a test to look into while it goes on.
type seenBytes struct {
	mu sync.Mutex
	b  []byte
}

func (s *seenBytes) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.b = append(s.b, p...)
	return len(p), nil
}

func (s *seenBytes) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return string(s.b)
}

// A forwarded port's close reaches the node after all its bytes, framed or not: the close used to queue apart from the
// bytes, and framed it went ahead of bytes waiting for credit, so the node dropped the stream's last bytes.
func TestPortStreamClosesAfterItsBytes(t *testing.T) {
	for _, framed := range []bool{false, true} {
		t.Run(fmt.Sprintf("framed=%t", framed), func(t *testing.T) {
			d := newTestDevices(t)
			info := proto.NodeInfo{Hostname: "box", OS: "linux", Arch: "amd64", Home: "/home/me", Version: "test",
				Protocol: nodeproto.Version, ProtocolMin: nodeproto.MinManagerVersion, Capabilities: nodeproto.Required}
			if framed {
				info.Capabilities = append(info.Capabilities, nodeproto.NodeLanes)
			}
			n := &testNode{info: &info, lanes: framed}
			m := connectedMachine(t, d, "box", n)
			const chunks = 8
			for i := range chunks { // more than a window's worth: framed, the later ones wait for credit
				raw, _ := json.Marshal(map[string]any{"id": "p1", "i": i, "data": strings.Repeat("A", 24<<10)})
				m.queueNote("stream.data", raw)
			}
			m.queueNote("stream.close", json.RawMessage(`{"id":"p1"}`))
			var stream []string
			eventually(t, 10*time.Second, "the stream's events at the node", func() bool {
				stream = stream[:0]
				for _, e := range n.eventsSeen() {
					if event, _, _ := strings.Cut(e, " "); strings.HasPrefix(event, "stream.") {
						stream = append(stream, event)
					}
				}
				return len(stream) == chunks+1
			})
			if stream[chunks] != "stream.close" {
				t.Fatalf("the close came before the stream's last bytes: %v", stream)
			}
		})
	}
}
