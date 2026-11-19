package node

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/nodeproto"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// A resident node keeps its connection when the transport under it ends: the manager comes back on a new one with its
// token and takes it back, the same Node serving it (#10663). A token the node does not have is refused on the spot,
// and the connection served is left as it was.
func TestAttachResumesAConnection(t *testing.T) {
	home := shortHome(t)
	t.Setenv("PERSONA_HOME", filepath.Join(home, ".persona"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var made atomic.Int32
	extra := make(chan net.Conn)
	done := make(chan error, 1)
	go func() {
		done <- Attach(ctx, func() (*Node, error) {
			made.Add(1)
			n, err := New("test")
			if err == nil {
				n.logger.SetOutput(io.Discard)
				t.Cleanup(n.starts.close)
			}
			return n, err
		}, extra, nil)
	}()
	m1, n1 := net.Pipe()
	extra <- n1
	hub := rpc.New("h", m1)
	hub.EnableLanes(nodeproto.ControlLane, nodeproto.BulkLane)
	hub.EnableResume(5 * time.Second)
	lost := make(chan error, 4)
	hub.OnLost = func(err error) { lost <- err }
	hub.OnRequest = func(context.Context, string, json.RawMessage) (any, error) { return map[string]any{}, nil }
	go func() { _ = hub.Serve(ctx, m1) }()
	cctx, ccancel := context.WithTimeout(ctx, 20*time.Second)
	defer ccancel()
	var info proto.NodeInfo
	if err := hub.Call(cctx, "node.info", map[string]any{}, &info); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(info.Capabilities, nodeproto.NodeResume) {
		t.Fatalf("a resident node does not announce %s", nodeproto.NodeResume)
	}
	if err := hub.UpgradeLanes(cctx); err != nil {
		t.Fatal(err)
	}
	if !hub.Resumable() {
		t.Fatal("not resumable after rpc.lanes with a resident node")
	}
	m1.Close() // the transport goes: a relay ended, a link dropped
	select {
	case <-lost:
	case <-time.After(5 * time.Second):
		t.Fatal("the manager's peer was not told its transport was lost")
	}
	m2, n2 := net.Pipe()
	extra <- n2
	if err := hub.Resume(cctx, m2, m2); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if err := hub.Call(cctx, "ping", map[string]any{}, nil); err != nil {
		t.Fatalf("after resuming: %v", err)
	}
	if made.Load() != 1 {
		t.Fatalf("%d Nodes made: a resume made a new one", made.Load())
	}

	// A token this node does not have: refused at once, the connection served left alone.
	m3, n3 := net.Pipe()
	extra <- n3
	go func() {
		_, _ = m3.Write([]byte(`{"id":"x1","method":"rpc.resume","params":{"token":"not-this-one","read":0}}` + "\n"))
	}()
	line, err := bufio.NewReader(m3).ReadString('\n')
	if err != nil || !strings.Contains(line, rpc.CodeResumeRefused) {
		t.Fatalf("a made-up token: %q, %v", line, err)
	}
	if err := hub.Call(cctx, "ping", map[string]any{}, nil); err != nil {
		t.Fatalf("after a refused resume: %v", err)
	}
	if made.Load() != 1 {
		t.Fatalf("%d Nodes made: a refused resume made one", made.Load())
	}
	cancel()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("Attach did not end")
	}
}

// A transport that ends before it said a whole first line (a manager's attempt to resume cut on its way) is let go:
// it does not replace the connection kept (#10829's review).
func TestAttachIgnoresATransportThatSaidNothing(t *testing.T) {
	home := shortHome(t)
	t.Setenv("PERSONA_HOME", filepath.Join(home, ".persona"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var made atomic.Int32
	extra := make(chan net.Conn)
	done := make(chan error, 1)
	go func() {
		done <- Attach(ctx, func() (*Node, error) {
			made.Add(1)
			n, err := New("test")
			if err == nil {
				n.logger.SetOutput(io.Discard)
				t.Cleanup(n.starts.close)
			}
			return n, err
		}, extra, nil)
	}()
	m1, n1 := net.Pipe()
	extra <- n1
	hub := rpc.New("h", m1)
	hub.EnableLanes(nodeproto.ControlLane, nodeproto.BulkLane)
	hub.OnRequest = func(context.Context, string, json.RawMessage) (any, error) { return map[string]any{}, nil }
	go func() { _ = hub.Serve(ctx, m1) }()
	cctx, ccancel := context.WithTimeout(ctx, 20*time.Second)
	defer ccancel()
	if err := hub.Call(cctx, "ping", map[string]any{}, nil); err != nil {
		t.Fatal(err)
	}
	for _, partial := range []string{"", `{"id":"h7","method":"rpc.res`} {
		m2, n2 := net.Pipe()
		extra <- n2
		go func() {
			_, _ = m2.Write([]byte(partial))
			m2.Close()
		}()
	}
	if err := hub.Call(cctx, "ping", map[string]any{}, nil); err != nil {
		t.Fatalf("the connection served after two that said nothing: %v", err)
	}
	if made.Load() != 1 {
		t.Fatalf("%d Nodes made", made.Load())
	}
	cancel()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("Attach did not end")
	}
}
