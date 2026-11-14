package devices

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/rpc"
)

// A connection whose node stops answering (a half-open link) is closed after pingMissLimit unanswered pings in a
// row, so the machine reconnects instead of holding every call until its own wait runs out (#7510).
func TestMeasureNodesClosesAConnectionThatStopsAnswering(t *testing.T) {
	oldWait := pingWait
	pingWait = 50 * time.Millisecond
	t.Cleanup(func() { pingWait = oldWait })
	d := newTestDevices(t)
	m := newMachine(d, MachineConfig{ID: "m1", Kind: "ssh", SSHHost: "m1"})
	d.addEntry(m)
	peer := rpc.New("h", io.Discard) // writes go nowhere: no answer ever comes
	m.mu.Lock()
	m.peer, m.online, m.active, m.transport = peer, true, &nodePath{key: "m1"}, "ssh"
	m.mu.Unlock()
	for i := 1; i < pingMissLimit; i++ {
		d.measureNodes(context.Background())
		select {
		case <-peer.Done():
			t.Fatalf("closed after %d unanswered ping(s), want %d", i, pingMissLimit)
		default:
		}
	}
	d.measureNodes(context.Background())
	select {
	case <-peer.Done():
	default:
		t.Fatalf("still open after %d unanswered pings", pingMissLimit)
	}
	if err := peer.Err(); err == nil || err.Error() != "no answer to 2 pings in a row (a half-open link?)" {
		t.Fatalf("closed with %v", err)
	}
}

// Only pings in a row on the same connection count: an answer (an error the node answered with too) clears the
// count, a connection already gone counts neither way, and a new connection starts over.
func TestPingMissesCountInARowOnOneConnection(t *testing.T) {
	d := newTestDevices(t)
	m := newMachine(d, MachineConfig{ID: "m1", Kind: "ssh", SSHHost: "m1"})
	peer := rpc.New("h", io.Discard)
	m.mu.Lock()
	m.peer = peer
	m.mu.Unlock()
	open := func() bool {
		select {
		case <-peer.Done():
			return false
		default:
			return true
		}
	}
	m.pingAnswered(peer, context.DeadlineExceeded)
	m.pingAnswered(peer, nil)
	m.pingAnswered(peer, context.DeadlineExceeded)
	m.pingAnswered(peer, &rpc.Error{Code: "busy", Message: "busy"})
	m.pingAnswered(peer, context.DeadlineExceeded)
	m.pingAnswered(peer, rpc.ErrDropped) // gone: neither a miss nor an answer
	if !open() {
		t.Fatal("closed although no two misses were in a row")
	}
	other := rpc.New("h", io.Discard)
	m.pingAnswered(other, context.DeadlineExceeded) // another connection's miss starts its own count
	m.pingAnswered(peer, context.DeadlineExceeded)
	if !open() {
		t.Fatal("misses on two connections were added up")
	}
	m.pingAnswered(peer, &rpc.Error{Message: "context deadline exceeded"}) // a relay's words for its wait running out
	if open() {
		t.Fatal("two misses in a row did not close the connection")
	}

	// A connection the machine no longer uses is not closed (it is the run loop's to end).
	m.mu.Lock()
	m.peer = rpc.New("h", io.Discard)
	m.mu.Unlock()
	m.pingAnswered(other, context.DeadlineExceeded)
	select {
	case <-other.Done():
		t.Fatal("closed a connection the machine no longer uses")
	default:
	}
}
