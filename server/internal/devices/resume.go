package devices

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nwverse/persona/server/internal/nodeproto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// Resuming a node connection (#10663, docs/protocol/protocol.md "Resuming"): a resident node that announces
// node_resume_v1 keeps the connection when its transport ends, and so does the manager (rpc.Peer.EnableResume). The
// transport going (a relay ended, a link dropped, pings unanswered over a half-open one) then leaves the peer waiting:
// its calls pending, its watches on, the machine shown as it was. resumeConn opens a new transport on the same path
// and takes the connection back over it, the node writing again what the manager did not read and the other way
// round; nothing is watched again. Refused (the node restarted), or not done within resumeWindow, the peer closes and
// the run loop connects anew as it always did.

// resumeWindow is how long a dropped connection waits to be resumed (the node keeps it as long).
var resumeWindow = time.Duration(nodeproto.ResumeWindowSeconds) * time.Second

// resumeTryWait bounds one try: opening a transport and the rpc.resume exchange on it.
var resumeTryWait = 10 * time.Second

// resumeCounts is a computer's dropped connections that could resume, and how they ended (logged with the lanes').
type resumeCounts struct {
	drops, resumed, fellBack int
	last, most               time.Duration // to resume: the latest, the longest
}

func (r resumeCounts) String() string {
	return fmt.Sprintf("drops=%d resumed=%d fell_back=%d recover=%s/%s", r.drops, r.resumed, r.fellBack,
		r.last.Round(time.Millisecond), r.most.Round(time.Millisecond))
}

// cut closes the transport in use; the connection, kept for a resume, waits for a new one (resumeConn).
func (c *nodeConn) cut() { c.pipe().close() }

// openPipe opens a new transport to the node on path p, by via; it lasts as long as ctx.
func (m *Machine) openPipe(ctx context.Context, p *nodePath, via string) (*nodePipe, error) {
	if via == viaHub {
		return m.d.openThroughHub(ctx, p.hub, p.mid)
	}
	l, err := p.src.open(ctx, via)
	if err != nil && via == "tailcat" {
		p.src.tailcatReset()
	}
	return l, err
}

// resumeConn takes c back after its transport was lost with err (rpc.Peer.OnLost): the old transport is closed, and
// new ones are tried on its path until one resumes it, the node refuses, or the window ends.
func (m *Machine) resumeConn(c *nodeConn, err error) {
	lostAt := time.Now()
	m.mu.Lock()
	m.resumes.drops++
	m.mu.Unlock()
	c.cut() // a write still stuck on it returns
	if c.restarting.Load() {
		// The resident node said it restarts: the next process has none of this connection.
		c.peer.Close(fmt.Errorf("the node restarts: %w", err))
		m.fellBack(lostAt, "the node restarts")
		return
	}
	m.d.logf("[%s] connection over %s lost (%v): resuming within %s", m.cfg.ID, c.via, oneLineErr(err), resumeWindow)
	deadline := lostAt.Add(resumeWindow)
	wait := 200 * time.Millisecond
	for try := 1; time.Until(deadline) > 0; try++ {
		rerr := m.tryResume(c)
		if rerr == nil {
			took := time.Since(lostAt)
			m.mu.Lock()
			m.resumes.resumed++
			m.resumes.last, m.resumes.most = took, max(m.resumes.most, took)
			counts := m.resumes
			m.mu.Unlock()
			m.d.logf("[%s] connection over %s resumed after %s (try %d): %s %s", m.cfg.ID, c.via, took.Round(time.Millisecond),
				try, counts, c.peer.LaneStats())
			return
		}
		if errors.Is(rerr, rpc.ErrResumeRefused) {
			c.peer.Close(fmt.Errorf("resume refused (%v) after: %w", rerr, err))
			m.fellBack(lostAt, oneLineErr(rerr))
			return
		}
		select {
		case <-time.After(min(wait, time.Until(deadline))):
		case <-c.peer.Done():
		case <-c.ctx.Done():
			return
		}
		if c.peer.Err() != nil {
			break
		}
		wait = min(wait*2, 3*time.Second)
	}
	select { // the peer closes itself as its window ends; the run loop connects anew
	case <-c.peer.Done():
		m.fellBack(lostAt, "not resumed in time")
	case <-c.ctx.Done():
	}
}

// tryResume opens one transport on c's path and resumes c over it.
func (m *Machine) tryResume(c *nodeConn) error {
	tctx, cancel := context.WithTimeout(c.ctx, resumeTryWait)
	defer cancel()
	l, err := m.openPipe(c.ctx, c.path, c.via)
	if err != nil {
		return err
	}
	if err := c.peer.Resume(tctx, l.r, l.w); err != nil {
		l.close()
		return err
	}
	c.pipeNow.Store(l)
	return nil
}

// fellBack counts and logs a drop that was not resumed: the run loop connects anew.
func (m *Machine) fellBack(lostAt time.Time, why string) {
	m.mu.Lock()
	m.resumes.fellBack++
	counts := m.resumes
	m.mu.Unlock()
	m.d.logf("[%s] connection not resumed after %s (%s): connecting anew; %s", m.cfg.ID,
		time.Since(lostAt).Round(time.Millisecond), why, counts)
}

// resumeCounts is the counts for a line about this computer's connections: none until one could resume.
func (m *Machine) resumeCounts() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.resumes == (resumeCounts{}) {
		return ""
	}
	return " " + m.resumes.String()
}

// resumeOn is the "connected" line's word for whether the connection can resume (lanes=on only).
func resumeOn(p *rpc.Peer) string {
	if !p.Framed() {
		return ""
	}
	return " resume=" + onOff(p.Resumable())
}
