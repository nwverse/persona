package rpc

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

// Resuming a framed connection (#10663, docs/protocol/protocol.md "Resuming"). Two peers that both enabled it agree on
// it when they switch to frames (rpc.lanes): the answering side hands the asking side a token. From then on each side
// acknowledges every frame it reads and keeps what it wrote until the other side acknowledges it (laneWriter.kept,
// bounded), and the transport ending does not end the peer: it is lost, the peer waits for resumeWindow with its calls
// pending and its handlers running, and a new transport carrying the token (Resume on the asking side, AcceptResume on
// the answering side) takes over where the old one stopped. Each side tells the other the last frame it read, and
// writes again what came after it; a frame read twice is dropped. Past the window, or when the other side cannot take
// the connection back, the peer closes as it always did.

const (
	resumeMethod = "rpc.resume"
	// CodeResumeRefused is the code of the answer to an rpc.resume that cannot take a connection back: no such token
	// (another process, a token already used), or frames it would need are no longer kept.
	CodeResumeRefused = "resume_refused"
	// resumeLineMax bounds the line a new transport starts with, rpc.resume or its answer.
	resumeLineMax = 64 << 10
)

// resumeKeepMax and resumeKeepAge bound what a writer keeps unacknowledged for a resume: past either the connection
// cannot be resumed (a gap) until the other side acknowledges everything written before it; a resume then falls back
// to a full reconnect. A healthy connection keeps what is on its way, a round trip's worth. Vars so tests lower them.
var (
	resumeKeepMax = 16 << 20
	resumeKeepAge = time.Minute
	// resumeStale: a transport on which nothing at all was read for this long while frames written over it wait to be
	// acknowledged is taken as dead (an address changed, a link gone without a word) and lost, to be resumed over a new
	// one. Acknowledgements come for every frame read, so a working transport answers within a round trip; resumeCheck
	// is how often that is looked at.
	resumeStale = 15 * time.Second
	resumeCheck = time.Second
)

// ErrResumeRefused is what Resume returns when the other side would not take the connection back: the caller lets the
// peer go and connects anew.
var ErrResumeRefused = errors.New("rpc: resume refused")

// errResumed is what the frame reader reads when the transport under it was replaced: the part of a frame it held is
// dropped, the frame comes again from the next one.
var errResumed = errors.New("rpc: transport replaced")

// resumeState is a peer's resuming (mu): enabled with a window, on once negotiated, the token, the transport's
// generation, and whether it is lost.
type resumeState struct {
	window time.Duration
	on     bool
	token  string
	gen    uint64
	lost   bool
	lostAt time.Time
	timer  *time.Timer
	// accepting: an AcceptResume is writing its answer.
	accepting bool
	sr        *swapReader
	fr        *frameReader
}

type resumeRequest struct {
	Token string `json:"token"`
	Read  uint64 `json:"read"`
}

type resumeAnswer struct {
	Read  uint64 `json:"read"`
	Token string `json:"token"`
}

type resumeGrant struct {
	Token string `json:"token"`
}

// EnableResume lets this peer resume its connection after the transport under it ends (resume.go): once it is framed
// with a peer that enabled it too, a lost transport leaves the peer waiting up to window for a new one, its calls and
// handlers going on. The asking side (UpgradeLanes) asks for it; the answering side grants it. Call it before Serve and
// EnableLanes' negotiation. Without it, or with a peer that does not know it, nothing changes.
func (p *Peer) EnableResume(window time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.resume = &resumeState{window: window}
}

// Resumable: the connection was agreed resumable and what this side kept would resume it now.
func (p *Peer) Resumable() bool {
	p.mu.Lock()
	rs, lw := p.resume, p.lw
	on := rs != nil && rs.on
	p.mu.Unlock()
	return on && lw != nil && lw.resumable()
}

// Suspended: the transport was lost and the peer waits to be resumed.
func (p *Peer) Suspended() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.resume != nil && p.resume.lost
}

// ResumeRequest is an rpc.resume read as the first line of a new transport (ParseResume).
type ResumeRequest struct {
	ID    string
	Token string
	Read  uint64
}

// ParseResume reads line, the first a new transport brought, as an rpc.resume; ok false for anything else.
func ParseResume(line []byte) (ResumeRequest, bool) {
	var m message
	if json.Unmarshal(bytes.TrimSpace(line), &m) != nil || m.Method != resumeMethod || m.ID == "" {
		return ResumeRequest{}, false
	}
	var req resumeRequest
	if json.Unmarshal(m.Params, &req) != nil || req.Token == "" {
		return ResumeRequest{}, false
	}
	return ResumeRequest{ID: m.ID, Token: req.Token, Read: req.Read}, true
}

// newResumeToken is a token the other side presents to resume: random, used once.
func newResumeToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// grantResumeLocked: this side answers the other side's rpc.lanes, which asked for resuming (p.mu held). The token
// granted, or "" when this side did not enable it.
func (p *Peer) grantResumeLocked(asked bool) string {
	rs := p.resume
	if rs == nil || !asked {
		return ""
	}
	rs.on, rs.token = true, newResumeToken()
	return rs.token
}

// resumeGranted: the other side's answer to this side's rpc.lanes, which asked for resuming, granted it.
func (p *Peer) resumeGranted(token string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if rs := p.resume; rs != nil && token != "" {
		rs.on, rs.token = true, token
	}
}

// resumeTokenOf is the token granted in m, the answer to this side's rpc.lanes; "" when it grants none.
func resumeTokenOf(m message) string {
	var a lanesAnswer
	if json.Unmarshal(m.Result, &a) != nil || a.Resume == nil {
		return ""
	}
	return a.Resume.Token
}

// resumeOnLocked: frames written from now on are kept for a resume (p.mu held, the writer made).
func (p *Peer) resumeOnLocked() bool { return p.resume != nil && p.resume.on }

// lose is told by the reader or the writer that the transport of generation gen failed with err. true: the peer waits
// for a resume (or one already replaced that transport), and the caller waits for the next; false: the error ends the
// connection as it always did.
func (p *Peer) lose(err error, gen uint64) bool {
	p.mu.Lock()
	rs, lw := p.resume, p.lw
	select {
	case <-p.closed:
		p.mu.Unlock()
		return false
	default:
	}
	if rs == nil || !rs.on || lw == nil {
		p.mu.Unlock()
		return false
	}
	if gen != rs.gen || rs.lost {
		p.mu.Unlock()
		return true // a resume already took over, or this loss is known
	}
	if !lw.resumable() {
		p.mu.Unlock()
		return false
	}
	rs.lost, rs.lostAt = true, time.Now()
	lw.loseGen(gen)
	window := rs.window
	rs.timer = time.AfterFunc(window, func() {
		p.Close(fmt.Errorf("rpc: not resumed within %s: %w", window, err))
	})
	onLost := p.OnLost
	p.mu.Unlock()
	if onLost != nil {
		go onLost(err)
	}
	return true
}

// Resume takes a lost connection back over a new transport r / w: it asks the other side with the token it was given,
// both tell each other the last frame they read, and each writes again what the other has not. ErrResumeRefused: the
// other side will not (let the peer go and connect anew); another error leaves the peer lost, to try again within its
// window. The caller closes r / w if it returns an error, which ends a read still waiting on them.
func (p *Peer) Resume(ctx context.Context, r io.Reader, w io.Writer) error {
	p.mu.Lock()
	rs := p.resume
	if rs == nil || !rs.on || !rs.lost || rs.fr == nil {
		p.mu.Unlock()
		return errors.New("rpc: nothing to resume")
	}
	gen, token, read := rs.gen, rs.token, rs.fr.lastRead.Load()
	id := fmt.Sprintf("%s%d", p.prefix, p.nextID.Add(1))
	p.mu.Unlock()
	params, err := marshal(resumeRequest{Token: token, Read: read})
	if err != nil {
		return err
	}
	line, err := encodeLine(message{ID: id, Method: resumeMethod, Params: params})
	if err != nil {
		return err
	}
	type answered struct {
		m   message
		br  *bufio.Reader
		err error
	}
	done := make(chan answered, 1)
	go func() {
		if _, err := w.Write(line); err != nil {
			done <- answered{err: err}
			return
		}
		br := bufio.NewReaderSize(r, 4096)
		b, err := readLineMax(br, resumeLineMax)
		if err != nil {
			done <- answered{err: err}
			return
		}
		var m message
		if err := json.Unmarshal(b, &m); err != nil {
			done <- answered{err: fmt.Errorf("rpc: answer to %s: %w", resumeMethod, err)}
			return
		}
		done <- answered{m: m, br: br}
	}()
	var a answered
	select {
	case a = <-done:
	case <-ctx.Done():
		return ctx.Err()
	case <-p.closed:
		return ErrClosed
	}
	if a.err != nil {
		return a.err
	}
	if a.m.ID != id {
		return fmt.Errorf("rpc: answer to %s: id %q", resumeMethod, a.m.ID)
	}
	if a.m.Error != nil {
		return fmt.Errorf("%w: %s", ErrResumeRefused, a.m.Error.Message)
	}
	var ans resumeAnswer
	if err := json.Unmarshal(a.m.Result, &ans); err != nil || ans.Token == "" {
		return fmt.Errorf("rpc: answer to %s: %s", resumeMethod, a.m.Result)
	}
	rest := io.MultiReader(bytes.NewReader(bufferedOf(a.br)), r)
	p.mu.Lock()
	defer p.mu.Unlock()
	if rs.gen != gen || !rs.lost {
		return errors.New("rpc: the connection changed while it resumed")
	}
	select {
	case <-p.closed:
		return ErrClosed
	default:
	}
	if !p.lw.canResume(ans.Read) {
		// The other side took it back already; this side cannot: the connection ends here.
		return fmt.Errorf("%w: frames after %d are no longer kept", ErrResumeRefused, ans.Read)
	}
	rs.token = ans.Token
	p.installLocked(rest, w, ans.Read)
	return nil
}

// AcceptResume takes this side's connection back over a new transport r / w, which brought req: when its token is this
// connection's, it answers with the last frame read and a new token, and frames go on, each side writing again what
// the other has not read. ErrResumeRefused: not this connection's (another process's, or a token already used), or
// what req needs is no longer kept: nothing was written, the caller serves r / w as a new connection. A connection
// whose transport was not seen to fail yet (half open) is lost at once: the caller closes its old transport.
func (p *Peer) AcceptResume(req ResumeRequest, r io.Reader, w io.Writer) error {
	p.mu.Lock()
	rs, lw := p.resume, p.lw
	ok := rs != nil && rs.on && !rs.accepting && rs.fr != nil && lw != nil &&
		subtle.ConstantTimeCompare([]byte(req.Token), []byte(rs.token)) == 1
	select {
	case <-p.closed:
		ok = false
	default:
	}
	if !ok || !lw.canResume(req.Read) {
		p.mu.Unlock()
		return ErrResumeRefused
	}
	if !rs.lost {
		rs.lost, rs.lostAt = true, time.Now()
		lw.loseGen(rs.gen)
	}
	rs.accepting = true // one at a time: the token stays this one's until the answer is out
	next := newResumeToken()
	result, err := marshal(resumeAnswer{Read: rs.fr.lastRead.Load(), Token: next})
	p.mu.Unlock()
	if err == nil {
		var line []byte
		if line, err = encodeLine(message{ID: req.ID, Result: result}); err == nil {
			_, err = w.Write(line) // nothing else writes on w before installLocked
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	rs.accepting = false
	if err != nil {
		return err // the new transport failed already: still lost, a next one may come with the same token
	}
	select {
	case <-p.closed:
		return ErrClosed
	default:
	}
	rs.token = next
	p.installLocked(r, w, req.Read)
	return nil
}

// RefuseResume answers req on w: this side has no connection to take back for it. The other side connects anew.
func RefuseResume(w io.Writer, req ResumeRequest) error {
	line, err := encodeLine(message{ID: req.ID, Error: &Error{Message: "rpc: resume refused", Code: CodeResumeRefused}})
	if err != nil {
		return err
	}
	_, err = w.Write(line)
	return err
}

// installLocked makes r / w the connection's transport (p.mu held): the next generation, the writer writing again what
// came after peerRead, the reader reading on from r.
func (p *Peer) installLocked(r io.Reader, w io.Writer, peerRead uint64) {
	rs := p.resume
	rs.gen++
	rs.lost = false
	if rs.timer != nil {
		rs.timer.Stop()
		rs.timer = nil
	}
	p.lw.resume(w, rs.gen, peerRead)
	rs.sr.install(r, rs.gen)
}

// bufferedOf is what br read past the line it returned.
func bufferedOf(br *bufio.Reader) []byte {
	b, _ := br.Peek(br.Buffered())
	return bytes.Clone(b)
}

// readLineMax reads one line of at most max bytes, its newline dropped.
func readLineMax(br *bufio.Reader, max int) ([]byte, error) {
	var line []byte
	for {
		b, err := br.ReadSlice('\n')
		line = append(line, b...)
		if len(line) > max {
			return nil, bufio.ErrTooLong
		}
		if err == nil {
			return bytes.TrimSuffix(line, []byte("\n")), nil
		}
		if err != bufio.ErrBufferFull {
			return nil, err
		}
	}
}

// swapReader is the transport a framed connection reads, which a resume replaces (install). A read that fails tells
// the peer (lose); while it waits for a resume the read waits too, and once the next transport is in it returns
// errResumed.
type swapReader struct {
	p      *Peer
	mu     sync.Mutex
	r      io.Reader
	gen    uint64
	next   chan struct{} // closed when the next generation is installed
	readAt atomic.Int64  // unix nanoseconds: the latest read that brought bytes (watchStale)
}

func newSwapReader(p *Peer, r io.Reader) *swapReader {
	return &swapReader{p: p, r: r, next: make(chan struct{})}
}

func (s *swapReader) Read(b []byte) (int, error) {
	s.mu.Lock()
	r, gen, next := s.r, s.gen, s.next
	s.mu.Unlock()
	n, err := r.Read(b)
	if n > 0 {
		s.readAt.Store(time.Now().UnixNano())
	}
	if n > 0 || err == nil {
		return n, nil // an error with data comes again on the next read
	}
	if !s.p.lose(err, gen) {
		return 0, err
	}
	select {
	case <-next:
		return 0, errResumed
	case <-s.p.closed:
		return 0, err
	}
}

func (s *swapReader) install(r io.Reader, gen uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.r, s.gen = r, gen
	s.readAt.Store(time.Now().UnixNano()) // a new transport: its silence counts from now
	close(s.next)
	s.next = make(chan struct{})
}

// errStale is why watchStale lost a transport.
var errStale = errors.New("rpc: nothing read while frames waited to be acknowledged")

// watchStale loses the transport of a resumable connection that went silent for stale, looking every every, until
// the peer closes.
func (p *Peer) watchStale(stale, every time.Duration) {
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-p.closed:
			return
		case <-tick.C:
		}
		p.mu.Lock()
		rs, lw := p.resume, p.lw
		if rs == nil || !rs.on || rs.lost || rs.sr == nil || lw == nil {
			p.mu.Unlock()
			continue
		}
		gen, read := rs.gen, time.Unix(0, rs.sr.readAt.Load())
		p.mu.Unlock()
		now := time.Now()
		if oldest := lw.oldestKept(); !oldest.IsZero() && now.Sub(oldest) > stale && now.Sub(read) > stale {
			p.lose(fmt.Errorf("%w for %s", errStale, now.Sub(read).Round(time.Second)), gen)
		}
	}
}
