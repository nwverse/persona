package iphonectl

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// Ops a request can ask for.
const (
	OpLook   = "look"
	OpDo     = "do"
	OpStatus = "status"
)

// Request is one call from the hub: a look, do or status on a phone, for one conversation (Session).
type Request struct {
	Device  string          `json:"device"`
	Session string          `json:"session"`
	Op      string          `json:"op"`
	Args    json.RawMessage `json:"args,omitempty"`
	// Approved resumes the do that stopped for the owner's approval: the approval id that do answered with, which
	// the hub sends only after the owner approved. The model never sees the id.
	Approved string `json:"approved,omitempty"`
	// Confirm is the owner's setting to be asked before any input, on top of the steps the rules always ask about.
	Confirm bool `json:"confirm,omitempty"`
}

// LookArgs are a look's arguments.
type LookArgs struct {
	Screenshot *bool `json:"screenshot,omitempty"` // default true
}

// DoArgs are a do's arguments.
type DoArgs struct {
	Steps json.RawMessage `json:"steps"`
	// Numbering is the numbering the steps' element numbers come from (what the look said); 0 takes the latest.
	Numbering  int  `json:"numbering,omitempty"`
	Screenshot bool `json:"screenshot,omitempty"` // with the read after the steps
}

// Response is the answer: the result for the model (Text), the same as data (Output), a screenshot, and, when a
// step waits for the owner, what to ask them and the id to resume with. Approval and ApprovalID are for the hub
// only: neither is in Text or Output.
type Response struct {
	Output        json.RawMessage `json:"output"`
	Text          string          `json:"text"`
	Image         *Image          `json:"image,omitempty"`
	NeedsApproval bool            `json:"needs_approval,omitempty"`
	Approval      *Approval       `json:"approval_summary,omitempty"`
	ApprovalID    string          `json:"approval_id,omitempty"`
}

// Options shape a controller.
type Options struct {
	Limits Limits
	Rules  Rules
}

// Controller operates the phones a node reaches, keeping per conversation the latest numbered read and the step
// waiting for approval. Its state is in memory: a restarted node starts with no numbers and nothing pending, and
// says so instead of guessing.
type Controller struct {
	limits Limits
	rules  Rules

	mu      sync.Mutex
	devices map[string]*phone
}

// NewController is a controller; one per node.
func NewController(o Options) *Controller {
	return &Controller{limits: o.Limits.withDefaults(), rules: o.Rules, devices: map[string]*phone{}}
}

// phone is one device: calls on it run one at a time, whichever conversation they come from.
type phone struct {
	calls chan struct{} // holds a token while a look or do runs

	mu       sync.Mutex
	sessions map[string]*session
	// out are the inputs sent and not answered yet, by when they were sent: one still out past its limit holds back
	// the next inputs, until it answers or Limits.Stuck has passed.
	out  map[uint64]time.Time
	next uint64
}

// sending notes an input as out and returns the function that notes its answer.
func (p *phone) sending() func() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.next++
	id := p.next
	p.out[id] = time.Now()
	return func() {
		p.mu.Lock()
		delete(p.out, id)
		p.mu.Unlock()
	}
}

// stuck is how long the oldest input still out has been, 0 when none is or it is past stuck.
func (p *phone) stuck(stuck time.Duration) time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	var oldest time.Duration
	for _, at := range p.out {
		if age := time.Since(at); age < stuck && age > oldest {
			oldest = age
		}
	}
	return oldest
}

// session is one conversation on a phone.
type session struct {
	screen  *Screen
	read    time.Time
	pending *pending
	last    string
	used    time.Time
}

// pending is a do stopped at a step that waits for the owner.
type pending struct {
	id         string
	steps      []Step // from the waiting step on
	offset     int    // the waiting step's index in the do the model asked for
	view       int    // the numbering the steps' numbers come from
	print      string // the waiting step on the screen it was checked on
	confirm    bool   // the owner's confirm setting the do stopped under
	confirmed  bool   // the owner had already confirmed the do (Confirm)
	shot       sig    // without an element tree: the screen the owner saw when asked
	screenshot bool
	expires    time.Time
}

// idle is how long an unused session is kept.
const idle = 6 * time.Hour

func (c *Controller) phone(id string) *phone {
	c.mu.Lock()
	defer c.mu.Unlock()
	p := c.devices[id]
	if p == nil {
		p = &phone{calls: make(chan struct{}, 1), sessions: map[string]*session{}, out: map[uint64]time.Time{}}
		c.devices[id] = p
	}
	return p
}

// session is the conversation's state, which the caller changes only while holding the phone's call token (or,
// for status, reads under p.mu).
func (p *phone) session(id string) *session {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	for k, s := range p.sessions {
		if now.Sub(s.used) > idle {
			delete(p.sessions, k)
		}
	}
	s := p.sessions[id]
	if s == nil {
		s = &session{}
		p.sessions[id] = s
	}
	s.used = now
	return s
}

func (p *phone) acquire(ctx context.Context, wait time.Duration) bool {
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case p.calls <- struct{}{}:
		return true
	case <-t.C:
	case <-ctx.Done():
	}
	return false
}

func (p *phone) release() { <-p.calls }

func (p *phone) set(fn func()) {
	p.mu.Lock()
	defer p.mu.Unlock()
	fn()
}

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

const busyNote = "another call on this phone is still running"

// Handle answers one request, reaching the phone through b (the bridge for req.Device). It always answers, within
// Limits.Call (plus Limits.Busy when the phone is busy) or the context's deadline, whichever comes first.
func (c *Controller) Handle(ctx context.Context, b Bridge, req Request) Response {
	if req.Device == "" {
		return errResponse("no device")
	}
	switch req.Op {
	case OpLook:
		var args LookArgs
		if err := decodeArgs(req.Args, &args); err != nil {
			return errResponse("bad arguments: " + err.Error())
		}
		shot := args.Screenshot == nil || *args.Screenshot
		r := c.look(ctx, b, req.Device, req.Session, shot)
		return response(r, r.Text(), r.image)
	case OpDo:
		if req.Approved != "" {
			r := c.resume(ctx, b, req)
			return r.response()
		}
		var args DoArgs
		if err := decodeArgs(req.Args, &args); err != nil {
			return errResponse("bad arguments: " + err.Error())
		}
		steps, err := ParseSteps(args.Steps)
		if err != nil {
			return errResponse("nothing was sent: " + err.Error())
		}
		r := c.do(ctx, b, req.Device, req.Session, run{steps: steps, view: args.Numbering, screenshot: args.Screenshot,
			confirm: req.Confirm})
		return r.response()
	case OpStatus:
		r := c.status(ctx, b, req.Device, req.Session)
		return response(r, r.Text(), nil)
	}
	return errResponse(fmt.Sprintf("unknown op %q (%s, %s, %s)", req.Op, OpLook, OpDo, OpStatus))
}

func decodeArgs(raw json.RawMessage, v any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return json.Unmarshal(raw, v)
}

func errResponse(msg string) Response {
	out, _ := json.Marshal(map[string]string{"error": msg})
	return Response{Output: out, Text: msg + "."}
}

func response(v any, text string, img *Image) Response {
	out, err := json.Marshal(v)
	if err != nil {
		return errResponse("encoding the result: " + err.Error())
	}
	return Response{Output: out, Text: text, Image: img}
}

// read runs a read-only bridge call within limit (and deadline), once more if the link dropped.
func read[T any](ctx context.Context, deadline time.Time, limit time.Duration, fn func(context.Context) (T, error)) (T, error) {
	var v T
	var err error
	for try := 0; try < 2; try++ {
		l := min(limit, time.Until(deadline))
		var timedOut bool
		if v, err, timedOut = boundedValue(ctx, l, fn); timedOut {
			return v, fmt.Errorf("no answer from the phone within %s", round(max(l, 0)))
		}
		if code(err) != CodeDisconnected {
			break
		}
	}
	return v, err
}
