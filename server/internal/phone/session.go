package phone

// One resident session per phone: a supervisor goroutine finds the phone (manual address, Bonjour, the last address
// that worked), starts the bridge's serve on it, and when the link drops starts again after a backoff. It belongs to
// the Manager, which lives as long as the node process: a client closing or the hub restarting doesn't touch it, and
// an idle connected phone stays connected. Calls go one at a time, as the bridge answers them; an input operation
// whose answer never came is reported as uncertain and never sent again (docs/extensions/phone-pairing.md).

import (
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Session states (Device.State).
const (
	StateIdle       = "idle"       // no session: never used since the node started, or parked after a long time unreachable
	StateConnecting = "connecting" // finding the phone and bringing the bridge's session up
	StateConnected  = "connected"
	StateBackoff    = "backoff" // the last attempt failed (LastError); the next one comes after a delay, or sooner when a call waits
)

// readOnlyOps are the bridge's operations that change nothing on the phone (docs/extensions/phone-bridge.md): the
// only ones sent again after a dropped link.
var readOnlyOps = map[string]bool{"device_info": true, "screenshot": true, "ax_tree": true, "list_apps": true}

// ReadOnly says whether op changes nothing on the phone, so it may be retried.
func ReadOnly(op string) bool { return readOnlyOps[op] }

// Timing; variables so tests can shorten them.
var (
	backoffMin    = time.Second
	backoffMax    = 2 * time.Minute
	stableAfter   = 30 * time.Second // a link that lasted this long resets the backoff
	parkAfter     = 10 * time.Minute // unreachable and no call for this long: stop trying until the next call
	minGap        = time.Second      // between attempts even when a call asks for one now
	browseWait    = 3 * time.Second
	answerMargin  = 300 * time.Millisecond // timeout_ms leaves this much of the caller's deadline for the answer to come back
	readOnlyRetry = 1                      // times a read-only call is sent again after its link dropped
)

// conn is one live link and what a call needs of it.
type conn struct {
	link    Link
	answers chan []byte   // lines with an id
	down    chan struct{} // closed when the link has ended
}

type session struct {
	m    *Manager
	udid string
	wake chan struct{}
	turn chan struct{} // one call at a time (a semaphore, so waiting for it can be cancelled)

	mu         sync.Mutex
	running    bool
	state      string
	lastErr    error
	errAt      time.Time // when lastErr was set
	ep         Endpoint
	info       json.RawMessage // device_info from the ready event
	cur        *conn
	up         chan struct{} // closed while cur != nil
	lastDemand time.Time     // last time a call wanted the link
	since      time.Time     // when the state last changed
}

func newSession(m *Manager, udid string) *session {
	return &session{m: m, udid: udid, wake: make(chan struct{}, 1), turn: make(chan struct{}, 1), state: StateIdle, up: make(chan struct{}), since: m.now()}
}

// ensureRunning starts the supervisor if it isn't running, and notes that a call wants the link.
func (s *session) ensureRunning() {
	s.mu.Lock()
	s.lastDemand = s.m.now()
	start := !s.running && s.m.ctx.Err() == nil
	if start {
		s.running = true
	}
	s.mu.Unlock()
	if start && !s.m.spawn(s.run) {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
	}
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *session) setState(state string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != state {
		s.since = s.m.now()
	}
	s.state = state
	if err != nil || state == StateConnected {
		s.lastErr, s.errAt = err, s.m.now()
	}
}

// run is the supervisor: connect, serve until the link drops, back off, again.
func (s *session) run() {
	defer func() {
		s.mu.Lock()
		s.running = false
		s.state, s.since = StateIdle, s.m.now() // no link outlives the supervisor
		s.mu.Unlock()
	}()
	var backoff time.Duration
	for {
		if s.m.ctx.Err() != nil {
			return
		}
		rec, err := s.m.store.Load(s.udid)
		if errors.Is(err, ErrNotPaired) {
			s.setState(StateIdle, &Error{Code: CodeNotPaired, Message: err.Error()})
			return
		}
		var c *conn
		var ep Endpoint
		started := s.m.now()
		if err == nil {
			s.setState(StateConnecting, nil)
			c, ep, err = s.connect(rec)
		}
		if err == nil {
			s.serve(c, ep, rec)
			if s.m.now().Sub(started) >= stableAfter {
				backoff = 0
			}
			err = c.link.Err()
		}
		if s.m.ctx.Err() != nil {
			return
		}
		backoff = nextBackoff(backoff)
		s.setState(StateBackoff, err)
		s.m.logf("phone %s: %v; again in %s", short(s.udid), err, backoff.Round(time.Second))
		if !s.wait(backoff) {
			return
		}
	}
}

// wait sleeps d, cut short (but not below minGap) by a call wanting the link. False: stop (the manager closed, or
// nobody has wanted the link for parkAfter).
func (s *session) wait(d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	for {
		select {
		case <-s.m.ctx.Done():
			return false
		case <-t.C:
		case <-s.wake:
			if d > minGap {
				t.Reset(minGap)
				d = minGap
				continue
			}
			continue
		}
		s.mu.Lock()
		parked := s.m.now().Sub(s.lastDemand) >= parkAfter
		s.mu.Unlock()
		return !parked
	}
}

func nextBackoff(d time.Duration) time.Duration {
	if d < backoffMin {
		d = backoffMin
	} else {
		d *= 2
	}
	d = min(d, backoffMax)
	// ±20%, so several phones don't retry in step
	return time.Duration(float64(d) * (0.8 + 0.4*rand.Float64()))
}

// connect tries where the phone may be, in order: the manual address, Bonjour, the last address that worked.
func (s *session) connect(rec Record) (*conn, Endpoint, error) {
	path, err := s.m.store.RecordPath(rec.UDID)
	if err != nil {
		return nil, Endpoint{}, err
	}
	dial, err := s.m.dialer()
	if err != nil {
		return nil, Endpoint{}, err
	}
	var tried []string
	var lastErr error
	try := func(ep Endpoint) *conn {
		key := ep.String()
		for _, t := range tried {
			if t == key {
				return nil
			}
		}
		tried = append(tried, key)
		ctx, cancel := context.WithTimeout(s.m.ctx, readyTimeout+5*time.Second)
		defer cancel()
		link, info, err := dial(ctx, rec, path, ep)
		if err != nil {
			lastErr = err
			return nil
		}
		s.mu.Lock()
		s.info = info
		s.mu.Unlock()
		return startConn(link)
	}
	if rec.Address != "" {
		if ep, err := ParseAddress(rec.Address); err == nil {
			if c := try(ep); c != nil {
				return c, ep, nil
			}
		} else {
			lastErr = err
		}
	}
	if rec.Identifier != "" {
		ctx, cancel := context.WithTimeout(s.m.ctx, browseWait+time.Second)
		services, err := s.m.browse(ctx, browseWait, func(found []Service) bool { return FindByIdentifier(found, rec.Identifier) != nil })
		cancel()
		if svc := FindByIdentifier(services, rec.Identifier); svc != nil {
			for _, a := range svc.Addrs {
				ep := Endpoint{Host: a.String(), Port: svc.Port, Source: "bonjour"}
				if c := try(ep); c != nil {
					return c, ep, nil
				}
			}
		} else if err != nil && lastErr == nil {
			lastErr = err
		}
	}
	if rec.LastHost != "" {
		ep := Endpoint{Host: rec.LastHost, Port: rec.LastPort, Source: "last"}
		if c := try(ep); c != nil {
			return c, ep, nil
		}
	}
	if lastErr == nil {
		lastErr = &Error{Code: CodeUnreachable, Message: "the phone wasn't found on the network (it advertises itself over Bonjour on the same Wi-Fi; elsewhere set its address with personad phone address)"}
	}
	return nil, Endpoint{}, lastErr
}

// startConn wraps a ready link and passes its answers on until it ends.
func startConn(link Link) *conn {
	c := &conn{link: link, answers: make(chan []byte, 4), down: make(chan struct{})}
	go func() {
		defer close(c.down)
		for line := range link.Lines() {
			var head struct {
				ID *string `json:"id"`
			}
			if json.Unmarshal(line, &head) != nil || head.ID == nil {
				continue // an event; the link records a disconnect's reason itself
			}
			select {
			case c.answers <- line:
			default: // nobody is waiting (the call timed out); the next call skips it by its id anyway
			}
		}
	}()
	return c
}

// serve publishes c as the session's link until it ends, remembering where the phone was found.
func (s *session) serve(c *conn, ep Endpoint, rec Record) {
	if ep.Source == "bonjour" && (ep.Host != rec.LastHost || ep.Port != rec.LastPort) {
		s.m.remember(s.udid, ep)
	}
	s.mu.Lock()
	s.cur, s.ep = c, ep
	close(s.up)
	s.mu.Unlock()
	s.setState(StateConnected, nil)
	s.m.logf("phone %s: connected at %s (%s)", short(s.udid), ep, ep.Source)
	select {
	case <-c.down:
	case <-s.m.ctx.Done():
		c.link.Close()
		<-c.down
	}
	s.mu.Lock()
	s.cur, s.up = nil, make(chan struct{})
	s.mu.Unlock()
}

// await is the live link, waiting for one (and asking the supervisor for it) until ctx ends. A failure the user has
// to fix on the phone or this computer ends the wait at once instead.
func (s *session) await(ctx context.Context) (*conn, error) {
	start := s.m.now()
	for {
		s.ensureRunning()
		s.mu.Lock()
		c, up, state, lastErr, errAt := s.cur, s.up, s.state, s.lastErr, s.errAt
		s.mu.Unlock()
		if c != nil {
			return c, nil
		}
		if state == StateIdle && Code(lastErr) == CodeNotPaired {
			return nil, unsent(lastErr)
		}
		if lastErr != nil && !errAt.Before(start) && needsUser[Code(lastErr)] {
			return nil, unsent(lastErr)
		}
		select {
		case <-up:
		case <-ctx.Done():
			msg := "the phone couldn't be reached in time"
			s.mu.Lock()
			if s.lastErr != nil {
				msg += ": " + s.lastErr.Error()
			}
			s.mu.Unlock()
			return nil, &Error{Code: CodeUnreachable, Message: msg}
		case <-time.After(100 * time.Millisecond): // to look at the state again
		}
	}
}

// needsUser are the failures to connect that waiting won't fix: someone has to act on the phone or this computer.
// Retrying goes on in the background all the same.
var needsUser = map[string]bool{
	CodeNotPaired: true, CodeBridgeMissing: true, "pairing_missing": true, "pairing_rejected": true,
	"developer_mode_off": true, "ddi_not_mounted": true, "unsupported": true, "wrong_device": true,
}

// call sends one operation and waits for its answer. See Manager.Call.
func (s *session) call(ctx context.Context, op string, args json.RawMessage) (json.RawMessage, error) {
	select {
	case s.turn <- struct{}{}:
	case <-ctx.Done():
		return nil, &Error{Code: CodeUnreachable, Message: "another call to this phone didn't finish in time"}
	}
	defer func() { <-s.turn }()

	readOnly := ReadOnly(op)
	retries := 0
	for {
		c, err := s.await(ctx)
		if err != nil {
			return nil, err
		}
		id := newID()
		req := map[string]any{"v": 1, "id": id, "op": op}
		if len(args) > 0 {
			req["args"] = args
		}
		if dl, ok := ctx.Deadline(); ok {
			req["timeout_ms"] = max(int64(time.Until(dl)-answerMargin)/int64(time.Millisecond), 100)
		}
		line, err := json.Marshal(req)
		if err != nil {
			return nil, err
		}
		if err := c.link.Send(line); err != nil {
			// No whole line reached the bridge, so nothing reached the phone: safe to send on the next link, once
			// this one is gone.
			c.link.Close()
			select {
			case <-c.down:
			case <-ctx.Done():
				return nil, &Error{Code: CodeUnreachable, Message: "the link to the phone broke before " + op + " was sent", beforeSend: true}
			}
			continue
		}
		answer, err := waitAnswer(ctx, c, id)
		if err == nil {
			return answer, nil
		}
		lost := errors.Is(err, errLinkLost) || Code(err) == bridgeDisconnected
		switch {
		case readOnly && lost && retries < readOnlyRetry:
			retries++
			continue
		case readOnly && errors.Is(err, errLinkLost):
			return nil, &Error{Code: bridgeDisconnected, Message: "the link to the phone dropped"}
		case readOnly && ctx.Err() != nil && Code(err) == "":
			return nil, &Error{Code: bridgeTimeout, Message: op + " didn't answer in time"}
		case readOnly:
			return nil, err
		case lost || Code(err) == bridgeTimeout || ctx.Err() != nil && Code(err) == "":
			s.m.logf("phone %s: %s sent, outcome unknown: %v", short(s.udid), op, err)
			return nil, &Error{Code: CodeUncertain, Message: op + " was sent to the phone but its result never came back (" + err.Error() + "); it may or may not have happened, and it won't be sent again: look at the screen before trying again"}
		default:
			return nil, err
		}
	}
}

var errLinkLost = errors.New("the link to the phone dropped")

// waitAnswer waits for the answer with id: its result, its *Error, errLinkLost, or ctx's error.
func waitAnswer(ctx context.Context, c *conn, id string) (json.RawMessage, error) {
	for {
		select {
		case line := <-c.answers:
			if result, ok, err := matchAnswer(line, id); ok {
				return result, err
			}
		case <-c.down:
			// The link may have answered just before it ended: those answers are already queued.
			for {
				select {
				case line := <-c.answers:
					if result, ok, err := matchAnswer(line, id); ok {
						return result, err
					}
				default:
					return nil, errLinkLost
				}
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// matchAnswer reads line as the answer to id; ok false when it answers something else (a late answer to an earlier
// call that timed out).
func matchAnswer(line []byte, id string) (result json.RawMessage, ok bool, err error) {
	var a struct {
		ID     string          `json:"id"`
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
		Error  *Error          `json:"error"`
	}
	if json.Unmarshal(line, &a) != nil || a.ID != id {
		return nil, false, nil
	}
	if a.OK {
		if len(a.Result) == 0 {
			return json.RawMessage("{}"), true, nil
		}
		return a.Result, true, nil
	}
	if a.Error == nil || a.Error.Code == "" {
		return nil, true, &Error{Code: "internal", Message: "the bridge failed without saying why"}
	}
	return nil, true, a.Error
}

// status is the session's part of Device.
func (s *session) status(d *Device) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d.State = s.state
	d.Since = s.since
	if s.state == StateConnected {
		d.Address = s.ep.String()
		d.AddressSource = s.ep.Source
	}
	if s.lastErr != nil {
		d.LastError = s.lastErr.Error()
		d.LastErrorCode = Code(s.lastErr)
	}
	if len(s.info) > 0 {
		d.Info = s.info
	}
}

// stop ends the live link, if any; the supervisor connects again only when a call asks (or after its backoff).
func (s *session) stop() {
	s.mu.Lock()
	c := s.cur
	s.mu.Unlock()
	if c != nil {
		c.link.Close()
	}
}

func newID() string {
	var b [8]byte
	_, _ = cryptoRand(b[:])
	return hex.EncodeToString(b[:])
}

var cryptoRand = crand.Read

// ParseAddress reads a manual address: "host", "host:port", "[v6]:port" or a bare IPv6 address.
func ParseAddress(s string) (Endpoint, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Endpoint{}, errors.New("empty address")
	}
	if host, port, err := net.SplitHostPort(s); err == nil {
		p, err := strconv.Atoi(port)
		if err != nil || p <= 0 || p > 65535 || host == "" || strings.HasPrefix(host, "-") || strings.ContainsAny(host, " /") {
			return Endpoint{}, fmt.Errorf("invalid address %q", s)
		}
		return Endpoint{Host: host, Port: p, Source: "manual"}, nil
	}
	host := strings.TrimSuffix(strings.TrimPrefix(s, "["), "]")
	if strings.ContainsAny(host, " /[]") || strings.HasPrefix(host, "-") {
		return Endpoint{}, fmt.Errorf("invalid address %q", s)
	}
	return Endpoint{Host: host, Source: "manual"}, nil
}

func short(udid string) string {
	if len(udid) > 8 {
		return udid[:8]
	}
	return udid
}
