package rpc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/netshape"
)

// resumeLink is a manager peer h and a node peer n, framed and resumable over a shaped link that a test cuts (both
// ends closed, what was on its way lost) and resumes over a new one.
type resumeLink struct {
	t            *testing.T
	link         netshape.Config
	h, n         *Peer
	hLost, nLost chan error
	mu           sync.Mutex
	a, b         net.Conn
	served       chan error
}

// newResumeLink makes the pair; setup, when set, configures both peers (handlers) before they serve. hResume and
// nResume enable resuming on each side with window (0: not).
func newResumeLink(t *testing.T, link netshape.Config, window time.Duration, hResume, nResume bool, setup func(h, n *Peer)) *resumeLink {
	t.Helper()
	l := &resumeLink{t: t, link: link, hLost: make(chan error, 8), nLost: make(chan error, 8), served: make(chan error, 2)}
	l.a, l.b = netshape.Pipe(link, link)
	l.h, l.n = New("h", l.a), New("n", l.b)
	for _, side := range []struct {
		p      *Peer
		resume bool
		lost   chan error
	}{{l.h, hResume, l.hLost}, {l.n, nResume, l.nLost}} {
		side.p.EnableLanes([]string{"ping"}, []string{"bulk", "bulk.event"})
		if side.resume {
			side.p.EnableResume(window)
		}
		lost := side.lost
		side.p.OnLost = func(err error) { lost <- err }
		side.p.OnRequest = func(context.Context, string, json.RawMessage) (any, error) { return map[string]any{}, nil }
	}
	if setup != nil {
		setup(l.h, l.n)
	}
	go func() { l.served <- l.h.Serve(context.Background(), l.a) }()
	go func() { l.served <- l.n.Serve(context.Background(), l.b) }()
	t.Cleanup(func() {
		l.h.Close(ErrClosed)
		l.n.Close(ErrClosed)
		l.mu.Lock()
		defer l.mu.Unlock()
		l.a.Close()
		l.b.Close()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := l.h.UpgradeLanes(ctx); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return l.n.Framed() })
	return l
}

// cut closes both ends of the transport: what was on its way either way is lost.
func (l *resumeLink) cut() {
	l.mu.Lock()
	a, b := l.a, l.b
	l.mu.Unlock()
	a.Close()
	b.Close()
}

// waitLost waits until both sides were told the transport was lost.
func (l *resumeLink) waitLost() {
	l.t.Helper()
	for _, ch := range []chan error{l.hLost, l.nLost} {
		select {
		case <-ch:
		case <-time.After(5 * time.Second):
			l.t.Fatal("a side was not told its transport was lost")
		}
	}
}

// resume connects the two again over a new link: h asks with its token, n answers as a resident node does, from the
// first line of the new transport.
func (l *resumeLink) resume() error {
	a, b := netshape.Pipe(l.link, l.link)
	accepted := make(chan error, 1)
	go func() {
		br := bufio.NewReader(b)
		line, err := br.ReadBytes('\n')
		if err != nil {
			accepted <- err
			return
		}
		req, ok := ParseResume(line)
		if !ok {
			accepted <- fmt.Errorf("not an rpc.resume: %q", line)
			return
		}
		accepted <- l.n.AcceptResume(req, io.MultiReader(bytes.NewReader(bufferedOf(br)), b), b)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := l.h.Resume(ctx, a, a)
	if aerr := <-accepted; err == nil {
		err = aerr
	}
	if err != nil {
		a.Close()
		b.Close()
		return err
	}
	l.mu.Lock()
	l.a, l.b = a, b
	l.mu.Unlock()
	return nil
}

// A cut in the middle of traffic both ways: calls in flight answer once the connection is resumed, each once, and the
// node's events arrive each once and in order, the ones lost on the way with the old transport written again.
func TestResumeKeepsCallsAndEventsExactlyOnce(t *testing.T) {
	const events = 1500
	release := make(chan struct{})
	var mu sync.Mutex
	var got []int
	var dup, gap int
	l := newResumeLink(t, netshape.Config{Rate: 20_000_000, Delay: 10 * time.Millisecond, Queue: 256 << 10}, 10*time.Second, true, true, func(h, n *Peer) {
		n.OnRequest = func(ctx context.Context, method string, params json.RawMessage) (any, error) {
			if method == "slow" {
				<-release
			}
			return json.RawMessage(params), nil
		}
		h.OnEvent = func(event string, params json.RawMessage) {
			var e struct {
				I int `json:"i"`
			}
			_ = json.Unmarshal(params, &e)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case len(got) > 0 && e.I <= got[len(got)-1]:
				dup++
			case e.I != len(got)+1:
				gap++
			}
			got = append(got, e.I)
		}
	})
	sent := make(chan error, 1)
	go func() { // events of every size, some longer than a frame
		for i := 1; i <= events; i++ {
			pad := strings.Repeat("x", (i%5)*9000)
			if err := l.n.Notify("bulk.event", map[string]any{"i": i, "pad": pad}); err != nil {
				sent <- err
				return
			}
		}
		sent <- nil
	}()
	slow := make(chan error, 8)
	for i := range 8 {
		go func() {
			var out map[string]int
			err := l.h.Call(context.Background(), "slow", map[string]int{"k": i}, &out)
			if err == nil && out["k"] != i {
				err = fmt.Errorf("answer %v to call %d", out, i)
			}
			slow <- err
		}()
	}
	eventually(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(got) > events/4 })
	l.cut()
	l.waitLost()
	if !l.h.Suspended() || !l.n.Suspended() {
		t.Fatal("a side not waiting to be resumed after the cut")
	}
	if err := l.resume(); err != nil {
		t.Fatalf("resume: %v", err)
	}
	close(release)
	for range 8 {
		select {
		case err := <-slow:
			if err != nil {
				t.Fatalf("a call in flight across the cut: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("a call in flight never answered")
		}
	}
	if err := <-sent; err != nil {
		t.Fatalf("events: %v", err)
	}
	eventually(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(got) >= events })
	mu.Lock()
	defer mu.Unlock()
	if len(got) != events || dup > 0 || gap > 0 {
		t.Fatalf("%d events for %d sent, %d twice, %d out of order", len(got), events, dup, gap)
	}
	if l.h.LaneStats().Resent == 0 && l.n.LaneStats().Resent == 0 {
		t.Fatal("nothing written again: the cut lost nothing on its way")
	}
}

// A second cut while the first resume is writing again what was lost: the next resume takes it from where that one got.
func TestResumeAgainDuringAResume(t *testing.T) {
	const events = 2000
	var mu sync.Mutex
	var got []int
	l := newResumeLink(t, netshape.Config{Rate: 4_000_000, Delay: 20 * time.Millisecond, Queue: 256 << 10}, 10*time.Second, true, true, func(h, n *Peer) {
		h.OnEvent = func(event string, params json.RawMessage) {
			var e struct {
				I int `json:"i"`
			}
			_ = json.Unmarshal(params, &e)
			mu.Lock()
			got = append(got, e.I)
			mu.Unlock()
		}
	})
	go func() {
		for i := 1; i <= events; i++ {
			if l.n.Notify("bulk.event", map[string]any{"i": i, "pad": strings.Repeat("y", 4000)}) != nil {
				return
			}
		}
	}()
	eventually(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(got) > 200 })
	l.cut()
	l.waitLost()
	if err := l.resume(); err != nil {
		t.Fatalf("first resume: %v", err)
	}
	l.cut() // at once: what the first resume wrote again is on its way, partly
	l.waitLost()
	if err := l.resume(); err != nil {
		t.Fatalf("second resume: %v", err)
	}
	eventually(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(got) >= events })
	mu.Lock()
	defer mu.Unlock()
	for i, v := range got {
		if v != i+1 {
			t.Fatalf("event %d is %d (%d events)", i+1, v, len(got))
		}
	}
	if len(got) != events {
		t.Fatalf("%d events for %d", len(got), events)
	}
}

// Not resumed within the window: the peer closes as a connection always did, its calls dropped.
func TestResumeWindowEnds(t *testing.T) {
	l := newResumeLink(t, netshape.Config{}, 200*time.Millisecond, true, true, func(h, n *Peer) {
		n.OnRequest = func(ctx context.Context, method string, params json.RawMessage) (any, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}
	})
	call := make(chan error, 1)
	go func() { call <- l.h.Call(context.Background(), "wait", nil, nil) }()
	eventually(t, func() bool { return len(l.h.pendingMethods()) == 1 })
	l.cut()
	l.waitLost()
	select {
	case err := <-call:
		if !errors.Is(err, ErrDropped) {
			t.Fatalf("the call: %v, want dropped", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the call still waits after the window")
	}
	if !strings.Contains(fmt.Sprint(l.h.Err()), "not resumed within") {
		t.Fatalf("closed with %v", l.h.Err())
	}
	if err := l.resume(); err == nil {
		t.Fatal("resumed after the window")
	}
}

// A peer that never enabled resuming answers rpc.lanes as before: nothing is kept, and a cut ends the connection.
func TestResumeNeedsBothSides(t *testing.T) {
	for _, c := range []struct{ h, n bool }{{true, false}, {false, true}} {
		l := newResumeLink(t, netshape.Config{}, time.Second, c.h, c.n, nil)
		if l.h.Resumable() || l.n.Resumable() {
			t.Fatalf("resumable with h %t, n %t", c.h, c.n)
		}
		l.cut()
		for range 2 {
			select {
			case <-l.served:
			case <-time.After(5 * time.Second):
				t.Fatalf("h %t, n %t: a side still serves after the cut", c.h, c.n)
			}
		}
		select {
		case err := <-l.hLost:
			t.Fatalf("h told lost (%v) on a connection that cannot resume", err)
		default:
		}
	}
}

// A token is the connection's, once: one from before the last resume, a made-up one, or one given to another
// process (a peer with nothing to resume) is refused, and nothing is written on the new transport.
func TestResumeTokenOnceAndOwn(t *testing.T) {
	l := newResumeLink(t, netshape.Config{}, 5*time.Second, true, true, nil)
	l.h.mu.Lock()
	first := l.h.resume.token
	l.h.mu.Unlock()
	l.cut()
	l.waitLost()
	if err := l.resume(); err != nil {
		t.Fatal(err)
	}
	other := New("n", io.Discard)
	other.EnableLanes(nil, nil)
	other.EnableResume(time.Second)
	for name, c := range map[string]struct {
		p     *Peer
		token string
	}{
		"used":    {l.n, first},
		"made up": {l.n, newResumeToken()},
		"other":   {other, first},
	} {
		var w bytes.Buffer
		if err := c.p.AcceptResume(ResumeRequest{ID: "h99", Token: c.token}, strings.NewReader(""), &w); !errors.Is(err, ErrResumeRefused) {
			t.Errorf("%s token: %v", name, err)
		}
		if w.Len() > 0 {
			t.Errorf("%s token: wrote %q", name, w.String())
		}
	}
	if l.n.Suspended() {
		t.Fatal("a refused token suspended the connection")
	}
	var out map[string]any
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := l.h.Call(ctx, "echo", nil, &out); err != nil {
		t.Fatalf("after the refusals: %v", err)
	}
}

// The node side may not have seen its transport fail when the manager comes back on a new one (a half-open link):
// the resume takes the connection over all the same, and the old transport is let go.
func TestResumeBeforeTheOtherSideNoticed(t *testing.T) {
	l := newResumeLink(t, netshape.Config{}, 5*time.Second, true, true, nil)
	l.mu.Lock()
	oldA, oldB := l.a, l.b
	l.mu.Unlock()
	oldA.Close() // the manager's end only: the node's still looks open
	select {
	case <-l.hLost:
	case <-time.After(5 * time.Second):
		t.Fatal("h not told lost")
	}
	if err := l.resume(); err != nil {
		t.Fatal(err)
	}
	oldB.Close() // as the resident node does once a resume took over
	var out map[string]any
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := l.h.Call(ctx, "echo", nil, &out); err != nil {
		t.Fatalf("after resuming: %v", err)
	}
	if l.n.Suspended() || l.h.Suspended() {
		t.Fatal("still suspended after resuming")
	}
}

// What a writer keeps for a resume is bounded: past resumeKeepMax nothing is kept through the frame that went over (a
// gap), and the connection is resumable again once the other side acknowledges past it.
func TestResumeKeepIsBounded(t *testing.T) {
	old := resumeKeepMax
	resumeKeepMax = 100 << 10
	t.Cleanup(func() { resumeKeepMax = old })
	lw := newLaneWriter(io.Discard, make(chan struct{}), func(error) {}, nil)
	lw.keep = true
	now := time.Now()
	frame := make([]byte, 30<<10)
	for seq := uint64(1); seq <= 3; seq++ {
		lw.seq = seq
		lw.keepLocked(seq, frame, now)
	}
	if !lw.resumable() || !lw.canResume(0) {
		t.Fatal("90 KiB kept: not resumable")
	}
	lw.seq = 4
	lw.keepLocked(4, frame, now) // 120 KiB: over
	if lw.keptBytes != 0 || lw.resumable() || lw.canResume(2) {
		t.Fatalf("over the bound: %d bytes kept, resumable %t", lw.keptBytes, lw.resumable())
	}
	lw.seq = 5
	lw.keepLocked(5, frame, now)
	if err := lw.acked(4, 0); err != nil {
		t.Fatal(err)
	}
	if !lw.resumable() || !lw.canResume(4) || lw.canResume(3) {
		t.Fatal("the gap acknowledged: not resumable from it")
	}
	lw.seq = 6
	lw.keepLocked(6, frame, now.Add(resumeKeepAge+time.Second)) // the oldest kept too old: the other side is not reading
	if lw.resumable() {
		t.Fatal("frames kept unacknowledged past resumeKeepAge: still resumable")
	}
}

// pendingMethods is what a test waits on: the calls waiting for an answer.
func (p *Peer) pendingMethods() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []string
	for _, m := range p.methods {
		out = append(out, m)
	}
	return out
}
