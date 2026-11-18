package devices

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/nodeproto"
	"github.com/nwverse/persona/server/internal/proto"
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
