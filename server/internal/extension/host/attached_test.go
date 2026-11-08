package host

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/extension"
	"github.com/nwverse/persona/server/internal/extension/serve"
)

// far is a plugin running on its own on a unix socket: it serves demo() on each connection, or closes it at once
// (mute), and counts the connections it took.
type far struct {
	ln      net.Listener
	accepts atomic.Int32
	mu      sync.Mutex
	conns   []net.Conn
}

func serveFar(t *testing.T, sock string, mute bool) *far {
	t.Helper()
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	f := &far{ln: ln}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			f.accepts.Add(1)
			if mute {
				c.Close()
				continue
			}
			f.mu.Lock()
			f.conns = append(f.conns, c)
			f.mu.Unlock()
			go func() { defer c.Close(); _ = serve.Serve(context.Background(), c, c, demo()) }()
		}
	}()
	t.Cleanup(f.stop)
	return f
}

// stop is the program ending: its socket and every connection close.
func (f *far) stop() {
	f.ln.Close()
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.conns {
		c.Close()
	}
	f.conns = nil
}

func farDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "hst") // a unix socket path stays short
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func attachedSpec(t *testing.T, sock string, o Observer) Spec {
	s := spec(t, "")
	s.Command, s.RPCSocket, s.Attached, s.Startup, s.Observe = "", sock, true, true, o
	return s
}

func timings(t *testing.T, retry, grace time.Duration) {
	r, g := RemoteRetry, OfflineGrace
	RemoteRetry, OfflineGrace = retry, grace
	t.Cleanup(func() { RemoteRetry, OfflineGrace = r, g })
}

// hubLines are the host's own lines about the plugin.
func hubLines(o *observed) []string {
	_, _, logs := o.snapshot()
	var out []string
	for _, l := range logs {
		if strings.HasPrefix(l, LogHub+": ") {
			out = append(out, l)
		}
	}
	return out
}

// An attached plugin that cannot be reached is tried every RemoteRetry, not with the doubling backoff, and stays
// starting within the grace; trying again with the same result is not logged or told as another exit.
func TestAttachedTriedEveryRemoteRetry(t *testing.T) {
	timings(t, 50*time.Millisecond, time.Hour)
	sock := filepath.Join(farDir(t), "far.sock")
	f := serveFar(t, sock, true) // takes each connection and closes it: initialize never answers
	o := &observed{}
	p := Start(context.Background(), attachedSpec(t, sock, o))
	defer p.Stop()
	time.Sleep(1500 * time.Millisecond)
	// every 50ms: about 30 tries; the backoff (50ms doubling) would have made 5
	if n := f.accepts.Load(); n < 10 {
		t.Fatalf("%d tries in 1.5s", n)
	}
	if st, why := p.Status(); st != StateStarting || why != "" {
		t.Fatalf("state %s (%s), want starting within the grace", st, why)
	}
	l := p.Link()
	if l.Online || l.Why == "" || time.Since(l.Since) < time.Second {
		t.Fatalf("link %+v", l)
	}
	_, runs, _ := o.snapshot()
	if len(runs) != 1 || !strings.HasPrefix(runs[0], RunExit+" ") {
		t.Fatalf("runs %q: want the one exit, no start", runs)
	}
	if lines := hubLines(o); len(lines) != 1 || !strings.Contains(lines[0], "again in 50ms") {
		t.Fatalf("the host's lines %q: want one", lines)
	}
}

// Offline past the grace it is an error, and stays one (no flip back to starting at each try); once it answers it is
// running and online; losing it again is starting, then an error.
func TestAttachedGraceThenErrorThenBack(t *testing.T) {
	timings(t, 20*time.Millisecond, 300*time.Millisecond)
	sock := filepath.Join(farDir(t), "far.sock")
	o := &observed{}
	began := time.Now()
	p := Start(context.Background(), attachedSpec(t, sock, o))
	defer p.Stop()
	waitState(t, p, StateError)
	if took := time.Since(began); took < OfflineGrace {
		t.Fatalf("an error after %s, before the grace", took)
	}
	if _, why := p.Status(); !strings.Contains(why, sock) {
		t.Fatal(why)
	}
	ch := p.Changes()
	time.Sleep(200 * time.Millisecond) // ten more tries
	select {
	case <-ch:
		st, _ := p.Status()
		t.Fatalf("a try that changes nothing changed the state (now %s)", st)
	default:
	}

	f := serveFar(t, sock, false)
	waitState(t, p, StateRunning)
	l := p.Link()
	if !l.Online || l.Why != "" || l.Said != "1.0.0" || time.Since(l.Since) > time.Second {
		t.Fatalf("link online %+v", l)
	}
	var n int
	if err := p.Call(context.Background(), "demo.pid", struct{}{}, &n); err != nil || n != os.Getpid() {
		t.Fatal(n, err)
	}
	if lines := hubLines(o); !strings.Contains(strings.Join(lines, "|"), "connected after") {
		t.Fatalf("the host's lines %q", lines)
	}

	f.stop()
	waitState(t, p, StateStarting)
	if l := p.Link(); l.Online || l.Why == "" {
		t.Fatalf("link offline %+v", l)
	}
	waitState(t, p, StateError)
	_, runs, _ := o.snapshot()
	var starts, exits int
	for _, r := range runs {
		switch {
		case r == RunStart:
			starts++
		case strings.HasPrefix(r, RunExit+" "):
			exits++
		}
	}
	// out at first, one start when it came online, its loss, and the reason it cannot be reached again
	if starts != 1 || exits < 2 || exits > 3 {
		t.Fatalf("runs %q", runs)
	}
}

// One started by calls gets its grace from when it is needed, not from when the host began or it was last up.
func TestAttachedGraceCountsFromWhenNeeded(t *testing.T) {
	timings(t, 20*time.Millisecond, 300*time.Millisecond)
	sock := filepath.Join(farDir(t), "far.sock")
	s := attachedSpec(t, sock, nil)
	s.Startup, s.OnCall = false, true
	p := Start(context.Background(), s)
	defer p.Stop()
	time.Sleep(400 * time.Millisecond) // past the grace, but nothing needed it
	if st, _ := p.Status(); st != StateStopped {
		t.Fatal(st)
	}
	needed := time.Now()
	release := p.Hold()
	defer release()
	time.Sleep(100 * time.Millisecond) // a few tries
	if st, why := p.Status(); st != StateStarting {
		t.Fatalf("state %s (%s), want starting within the grace", st, why)
	}
	waitState(t, p, StateError)
	if took := time.Since(needed); took < OfflineGrace {
		t.Fatalf("an error %s after it was needed, before the grace", took)
	}
}

// One that answers as another version is refused: an error at once, whatever the grace, tried again with the backoff,
// and Link says what it said.
func TestAttachedOtherVersionIsRefused(t *testing.T) {
	timings(t, 10*time.Millisecond, time.Hour)
	sock := filepath.Join(farDir(t), "far.sock")
	f := serveFar(t, sock, false)
	o := &observed{}
	s := attachedSpec(t, sock, o)
	init := s.Init
	s.Init = func(ctx context.Context) (params extension.InitializeParams, err error) {
		params, err = init(ctx)
		params.Extension.Version = "2.0.0"
		return params, err
	}
	p := Start(context.Background(), s)
	defer p.Stop()
	waitState(t, p, StateError)
	if _, why := p.Status(); !strings.Contains(why, "2.0.0") {
		t.Fatal(why)
	}
	time.Sleep(time.Second)
	// the backoff from 50ms: 4 or 5 tries in a second; every RemoteRetry would be 100
	if n := f.accepts.Load(); n > 8 {
		t.Fatalf("%d tries in a second", n)
	}
	if l := p.Link(); l.Online || l.Said != "1.0.0" {
		t.Fatalf("link %+v", l)
	}
	if lines := hubLines(o); len(lines) != 1 {
		t.Fatalf("the host's lines %q: want one", lines)
	}
}

// An attached HTTP-only plugin: its /health is its link, with the same grace.
func TestAttachedHTTPOnly(t *testing.T) {
	timings(t, 20*time.Millisecond, 300*time.Millisecond)
	every := HealthEvery
	HealthEvery = 20 * time.Millisecond
	defer func() { HealthEvery = every }()
	sock := filepath.Join(farDir(t), "far.sock")
	s := spec(t, "")
	s.Command, s.RPC, s.HTTP, s.Socket, s.Attached, s.Startup = "", false, true, sock, true, true
	began := time.Now()
	p := Start(context.Background(), s)
	defer p.Stop()
	if st, _ := p.Status(); st != StateStarting {
		t.Fatal(st)
	}
	waitState(t, p, StateError)
	if took := time.Since(began); took < OfflineGrace {
		t.Fatalf("an error after %s, before the grace", took)
	}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})}
	go srv.Serve(ln)
	waitState(t, p, StateRunning)
	if l := p.Link(); !l.Online || l.Why != "" {
		t.Fatalf("link %+v", l)
	}
	srv.Close()
	waitState(t, p, StateStarting)
	if l := p.Link(); l.Online || !strings.Contains(l.Why, sock) {
		t.Fatalf("link %+v", l)
	}
	waitState(t, p, StateError)
}
