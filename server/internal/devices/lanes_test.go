package devices

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/nodeproto"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// lanesMachine connects a machine to a fake node: announcing lanes or not, able to frame or not. It returns the
// machine and the lines its devices logged.
func lanesMachine(t *testing.T, announce, able bool) (*Machine, func() string) {
	t.Helper()
	d := newTestDevices(t)
	var mu sync.Mutex
	var log strings.Builder
	d.logf = func(f string, a ...any) {
		mu.Lock()
		fmt.Fprintf(&log, f+"\n", a...)
		mu.Unlock()
	}
	m := newMachine(d, MachineConfig{ID: "lanes", Name: "Laptop", Kind: "ssh"})
	m.dialNode = func(context.Context) (net.Conn, error) {
		managerSide, nodeSide := net.Pipe()
		p := rpc.New("n", nodeSide)
		if able {
			p.EnableLanes(nodeproto.ControlLane, nodeproto.BulkLane)
		}
		p.OnRequest = func(ctx context.Context, method string, params json.RawMessage) (any, error) {
			switch method {
			case "node.info":
				info := proto.NodeInfo{Hostname: "laptop", OS: "linux", Arch: "amd64", Home: "/home/me", Version: "test",
					Protocol: nodeproto.Version, ProtocolMin: nodeproto.MinManagerVersion, Capabilities: []string{nodeproto.Sessions}}
				if announce {
					info.Capabilities = append(info.Capabilities, nodeproto.NodeLanes)
				}
				return info, nil
			case "sessions.list":
				return map[string]any{"sessions": []proto.Session{}}, nil
			case "file.chunk":
				return map[string]any{"data": make([]byte, 300_000)}, nil
			case "rpc.lanes":
				return nil, errors.New("unknown method rpc.lanes") // a node that announced it but cannot
			}
			return map[string]any{"ok": true}, nil
		}
		go func() { _ = p.Serve(context.Background(), nodeSide); nodeSide.Close() }()
		t.Cleanup(func() { managerSide.Close(); nodeSide.Close() })
		return managerSide, nil
	}
	d.addEntry(m)
	runMachine(t, m)
	eventually(t, 5*time.Second, "connected", m.reachable)
	return m, func() string {
		mu.Lock()
		defer mu.Unlock()
		return log.String()
	}
}

// The manager asks for lanes when node.info announces them, and only then; a node that announces them and refuses
// keeps lines. Calls work every way.
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
			m, logged := lanesMachine(t, c.announce, c.able)
			m.mu.Lock()
			peer := m.peer
			m.mu.Unlock()
			if peer.Framed() != c.framed {
				t.Fatalf("framed=%t", peer.Framed())
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			var chunk struct {
				Data []byte `json:"data"`
			}
			if err := m.call(ctx, "file.chunk", map[string]any{}, &chunk); err != nil || len(chunk.Data) != 300_000 {
				t.Fatalf("file.chunk: %d bytes, %v", len(chunk.Data), err)
			}
			if err := m.call(ctx, "session.info", map[string]any{}, nil); err != nil {
				t.Fatal(err)
			}
			want := "lanes=off"
			if c.framed {
				want = "lanes=on"
			}
			eventually(t, 2*time.Second, "the connected line", func() bool { return strings.Contains(logged(), want) })
			if got := strings.Contains(logged(), "staying with lines"); got != c.refused {
				t.Fatalf("refusal logged=%t:\n%s", got, logged())
			}
		})
	}
}
