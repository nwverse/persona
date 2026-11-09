package egress

import (
	"net"
	"sync"
	"time"
)

// The bounds of a runtimeListener: the connections one keeps open at once, and the
// ones every runtimeListener of a Proxy keeps open together (its runtimeSlots).
const (
	runtimeConnsPerListener = 16
	runtimeSlotsPerProxy    = 128
)

// runtimeListener bounds the sockets a guest holds open in the manager, at Accept:
// before http.Server starts a goroutine or reads headers on a source runtime's proxy
// socket, and before the relay dials the node for an agent runtime's runtime.sock. A
// guest must not fill the manager with idle sockets before RPC/egress request limits
// can inspect them. A connection over either bound is closed at once, and Close closes
// every connection it handed out. The other listeners are not bounded.
type runtimeListener struct {
	net.Listener
	slots       chan struct{}
	done        chan struct{}
	once        sync.Once
	mu          sync.Mutex
	connections map[*runtimeConn]struct{}
}

func (l *runtimeListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		accepted := false
		select {
		case l.slots <- struct{}{}:
			accepted = true
		default:
		}
		if accepted {
			l.mu.Lock()
			closed := false
			select {
			case <-l.done:
				closed = true
			default:
			}
			if !closed && len(l.connections) < runtimeConnsPerListener {
				wrapped := &runtimeConn{Conn: c, owner: l}
				l.connections[wrapped] = struct{}{}
				l.mu.Unlock()
				return wrapped, nil
			}
			l.mu.Unlock()
			<-l.slots
		}
		_ = c.Close()
		// Rejected connection floods cannot turn the manager's accept loop into
		// an unbounded CPU broker. Only this extension's listener is throttled.
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-timer.C:
		case <-l.done:
			timer.Stop()
			return nil, net.ErrClosed
		}
	}
}

func (l *runtimeListener) Close() error {
	l.once.Do(func() {
		close(l.done)
		_ = l.Listener.Close()
		l.mu.Lock()
		all := make([]*runtimeConn, 0, len(l.connections))
		for c := range l.connections {
			all = append(all, c)
		}
		l.mu.Unlock()
		for _, c := range all {
			_ = c.Close()
		}
	})
	return nil
}

type runtimeConn struct {
	net.Conn
	owner *runtimeListener
	once  sync.Once
}

func (c *runtimeConn) Close() error {
	var err error
	c.once.Do(func() {
		err = c.Conn.Close()
		c.owner.mu.Lock()
		delete(c.owner.connections, c)
		c.owner.mu.Unlock()
		<-c.owner.slots
	})
	return err
}
