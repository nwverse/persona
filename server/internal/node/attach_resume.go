package node

import (
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/nodeproto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// A resident node keeps the connection it serves when the transport under it ends (#10663, docs/protocol/protocol.md
// "Resuming"): for resumeWindow its Node and its calls go on, and a manager that comes back with the connection's token
// (rpc.resume, the first line of the new transport) takes it back where it stopped. A connection that starts any other
// way replaces the one served, as before; one with a token this node does not have (it restarted, or the token was used)
// is refused, and the manager connects anew.

// resumeWindow is how long a resident node keeps a connection whose transport ended (nodeproto.ResumeWindowSeconds).
var resumeWindow = time.Duration(nodeproto.ResumeWindowSeconds) * time.Second

// firstLineWait bounds the wait for a new transport's first line: the manager writes it at once (rpc.resume, or the
// handshake's first call), and an old one too.
var firstLineWait = 10 * time.Second

// attached is the connection a resident node serves: its transport, which a resume replaces, and its peer once made.
type attached struct {
	mu   sync.Mutex
	conn net.Conn
	peer *rpc.Peer
}

func (a *attached) setPeer(p *rpc.Peer) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.peer = p
}

// closeConn closes the transport now in use: one whose peer lost it, so a write still stuck on it returns.
func (a *attached) closeConn() {
	a.mu.Lock()
	c := a.conn
	a.mu.Unlock()
	_ = c.Close()
}

// end lets the connection go: its transport, and its peer, which may be waiting for a resume.
func (a *attached) end(err error) {
	a.mu.Lock()
	c, p := a.conn, a.peer
	a.mu.Unlock()
	_ = c.Close()
	if p != nil {
		p.Close(err)
	}
}

// resume takes the connection back over c, which brought req (rest: what follows it). false: refused, nothing written.
func (a *attached) resume(req rpc.ResumeRequest, c net.Conn, rest io.Reader) bool {
	a.mu.Lock()
	p := a.peer
	a.mu.Unlock()
	if p == nil || p.AcceptResume(req, rest, c) != nil {
		return false
	}
	a.mu.Lock()
	old := a.conn
	a.conn = c
	a.mu.Unlock()
	_ = old.Close()
	return true
}

// connReader is what a Node reads: the attached transport's bytes, the first line read ahead of them included. Close
// ends the connection, its transport and its peer: a resident node reloading ends it that way (node.Serve), and a
// connection kept for a resume must not wait out its window there.
type connReader struct {
	io.Reader
	a *attached
}

func (r connReader) Close() error {
	r.a.end(errReloading)
	return nil
}

var errReloading = errors.New("the node is restarting")

// peekedConn is a new transport and what readFirstLine read of it.
type peekedConn struct {
	c     net.Conn
	first []byte
	rest  io.Reader
}

// readFirstLine reads c's first line (rpc.ReadFirstLine), waiting at most firstLineWait. line is what it read: the
// line, or what came before giving up; rest is what follows it.
func readFirstLine(c net.Conn) (line []byte, rest io.Reader) {
	_ = c.SetReadDeadline(time.Now().Add(firstLineWait))
	defer func() { _ = c.SetReadDeadline(time.Time{}) }()
	line, rest, _ = rpc.ReadFirstLine(c, firstLineMax)
	return line, rest
}

// firstLineMax bounds a new transport's first line: rpc.resume, or the manager's first call, both short.
const firstLineMax = 64 << 10

var errReplaced = errors.New("replaced by a new connection")

// attachedWriter writes on the transport the attached connection began on. Once framed, the peer's frame writer
// writes on the transport a resume gave it (rpc.Peer.AcceptResume): only the lines before the switch come here.
type attachedWriter struct{ a *attached }

func (w attachedWriter) Write(b []byte) (int, error) {
	w.a.mu.Lock()
	c := w.a.conn
	w.a.mu.Unlock()
	return c.Write(b)
}
