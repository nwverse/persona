package devices

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/nodeproto"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// residentNode is a fake resident node: it keeps its connection when the transport ends and takes it back for an
// rpc.resume with its token, as node/attach_resume.go does; restarted, it has none to take back.
type residentNode struct {
	mu        sync.Mutex
	peer      *rpc.Peer
	conn      net.Conn
	transport int // transports dialed
	fresh     int // connections served from the start
	restarted bool
	release   chan struct{} // what a "slow" call waits for
	slowSeen  chan struct{} // closed when one came
	slowOnce  sync.Once
}

func (n *residentNode) dial(context.Context) (net.Conn, error) {
	managerSide, nodeSide := net.Pipe()
	n.mu.Lock()
	n.transport++
	n.mu.Unlock()
	go func() {
		br := bufio.NewReader(nodeSide)
		line, _ := br.ReadBytes('\n')
		buffered, _ := br.Peek(br.Buffered())
		rest := io.MultiReader(bytes.NewReader(bytes.Clone(buffered)), nodeSide)
		n.mu.Lock()
		p, restarted := n.peer, n.restarted
		n.mu.Unlock()
		if req, ok := rpc.ParseResume(line); ok {
			if p != nil && !restarted && p.AcceptResume(req, rest, nodeSide) == nil {
				n.mu.Lock()
				old := n.conn
				n.conn = nodeSide
				n.mu.Unlock()
				old.Close()
				return
			}
			_ = rpc.RefuseResume(nodeSide, req)
			nodeSide.Close()
			return
		}
		p = rpc.New("n", nodeSide)
		p.EnableLanes(nodeproto.ControlLane, nodeproto.BulkLane)
		p.EnableResume(resumeWindow)
		p.OnLost = func(error) { n.cut() }
		p.OnRequest = func(ctx context.Context, method string, params json.RawMessage) (any, error) {
			switch method {
			case "node.info":
				return proto.NodeInfo{Hostname: "box", OS: "linux", Arch: "amd64", Home: "/home/me", Version: "test",
					Protocol: nodeproto.Version, ProtocolMin: nodeproto.MinManagerVersion,
					Capabilities: append(append([]string{}, nodeproto.Required...), nodeproto.NodeLanes, nodeproto.NodeResume)}, nil
			case "sessions.list":
				return map[string]any{"sessions": []proto.Session{}}, nil
			case "slow":
				n.slowOnce.Do(func() { close(n.slowSeen) })
				select {
				case <-n.release:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			return map[string]any{"ok": true}, nil
		}
		n.mu.Lock()
		n.peer, n.conn, n.restarted = p, nodeSide, false
		n.fresh++
		n.mu.Unlock()
		go func() { _ = p.Serve(context.Background(), io.MultiReader(bytes.NewReader(line), rest)) }()
	}()
	return managerSide, nil
}

// cut closes the transport in use, as a relay that ended or a link that dropped.
func (n *residentNode) cut() {
	n.mu.Lock()
	c := n.conn
	n.mu.Unlock()
	if c != nil {
		c.Close()
	}
}

func (n *residentNode) counts() (transports, fresh int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.transport, n.fresh
}

// resumingMachine runs a machine on a resident node and waits until it is connected, resumable.
func resumingMachine(t *testing.T) (*Machine, *residentNode, *benchLog) {
	t.Helper()
	d := newTestDevices(t)
	logs := &benchLog{}
	quiet := d.logf
	d.logf = func(format string, args ...any) {
		logs.add(fmt.Sprintf(format, args...))
		quiet(format, args...)
	}
	n := &residentNode{release: make(chan struct{}), slowSeen: make(chan struct{})}
	m := newMachine(d, MachineConfig{ID: "box", Name: "box", Kind: "ssh"})
	m.dialNode = n.dial
	d.addEntry(m)
	runMachine(t, m)
	eventually(t, 10*time.Second, "box connected, resumable", func() bool { return logs.count("lanes=on resume=on") > 0 })
	return m, n, logs
}

// A dropped transport is taken back: the call in flight answers once the connection is resumed, nothing connects or
// is watched again, and the machine never shows offline (#10663).
func TestMachineResumesADroppedConnection(t *testing.T) {
	m, n, logs := resumingMachine(t)
	call := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		call <- m.call(ctx, "slow", map[string]any{}, nil)
	}()
	select {
	case <-n.slowSeen:
	case <-time.After(5 * time.Second):
		t.Fatal("the call did not reach the node")
	}
	n.cut()
	eventually(t, 10*time.Second, "resumed", func() bool { return logs.count("] connection over ") > 0 && logs.count("resumed after") > 0 })
	close(n.release)
	if err := <-call; err != nil {
		t.Fatalf("the call in flight: %v", err)
	}
	if transports, fresh := n.counts(); transports != 2 || fresh != 1 {
		t.Fatalf("%d transports, %d served from the start: want 2 and 1", transports, fresh)
	}
	if c := logs.count("] connected over "); c != 1 {
		t.Fatalf("connected %d times", c)
	}
	if logs.count("offline") > 0 || logs.count("disconnected") > 0 {
		t.Fatalf("shown offline: %q", logs.since(0))
	}
	if !m.reachable() {
		t.Fatal("not reachable after resuming")
	}
	if logs.count("resumed=1") == 0 {
		t.Fatalf("the counts: %q", logs.since(0))
	}
}

// A node that restarted has nothing to take back: the manager is refused, and connects anew at once.
func TestMachineFallsBackWhenTheNodeHasNoConnection(t *testing.T) {
	_, n, logs := resumingMachine(t)
	n.mu.Lock()
	n.restarted = true
	n.mu.Unlock()
	n.cut()
	eventually(t, 10*time.Second, "connected anew", func() bool { return logs.count("] connected over ") == 2 })
	if logs.count("fell_back=1") == 0 || logs.count("resume refused") == 0 && logs.count("not resumed") == 0 {
		t.Fatalf("the fall back is not logged: %q", logs.since(0))
	}
}

// Pings unanswered on a connection that can resume cut its transport, not the connection: a new transport takes it
// back.
func TestPingMissesCutAResumableConnection(t *testing.T) {
	old := pingActivityWindow
	pingActivityWindow = 0 // the handshake's replies do not excuse these misses
	t.Cleanup(func() { pingActivityWindow = old })
	m, n, logs := resumingMachine(t)
	m.mu.Lock()
	peer := m.peer
	m.mu.Unlock()
	for range pingMissLimit {
		m.pingAnswered(peer, context.DeadlineExceeded)
	}
	eventually(t, 10*time.Second, "resumed", func() bool { return logs.count("resumed after") > 0 })
	if logs.count("cutting the transport to resume") != 1 {
		t.Fatalf("not cut: %q", logs.since(0))
	}
	if peer.Err() != nil {
		t.Fatalf("the connection closed: %v", peer.Err())
	}
	if _, fresh := n.counts(); fresh != 1 {
		t.Fatalf("%d connections from the start", fresh)
	}
}
