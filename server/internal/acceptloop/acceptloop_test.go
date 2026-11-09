package acceptloop

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// scriptedListener returns its scripted Accept results in order, then repeats the last one.
type scriptedListener struct {
	mu     sync.Mutex
	script []acceptResult
	calls  atomic.Int64
}

type acceptResult struct {
	c   net.Conn
	err error
}

func (l *scriptedListener) Accept() (net.Conn, error) {
	l.calls.Add(1)
	l.mu.Lock()
	defer l.mu.Unlock()
	r := l.script[0]
	if len(l.script) > 1 {
		l.script = l.script[1:]
	}
	return r.c, r.err
}

func (l *scriptedListener) Close() error   { return nil }
func (l *scriptedListener) Addr() net.Addr { return &net.UnixAddr{Name: "scripted.sock", Net: "unix"} }

func shortBackoff(t *testing.T, lo, hi time.Duration) {
	oldMin, oldMax := backoffMin, backoffMax
	backoffMin, backoffMax = lo, hi
	t.Cleanup(func() { backoffMin, backoffMax = oldMin, oldMax })
}

func goRun(ctx context.Context, ln net.Listener, logf func(string, ...any), handle func(net.Conn)) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		Run(ctx, ln, logf, handle)
	}()
	return done
}

func waitClosed(t *testing.T, done <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal(what)
	}
}

// Running out of file descriptors for a moment does not stop the socket: the connection after it is served.
func TestRunSurvivesTransientErrors(t *testing.T) {
	shortBackoff(t, time.Millisecond, 4*time.Millisecond)
	server, client := net.Pipe()
	defer client.Close()
	ln := &scriptedListener{script: []acceptResult{{err: syscall.EMFILE}, {err: syscall.ENFILE}, {c: server}, {err: net.ErrClosed}}}
	var logs atomic.Int64
	got := make(chan net.Conn, 1)
	done := goRun(context.Background(), ln, func(string, ...any) { logs.Add(1) }, func(c net.Conn) { got <- c })
	select {
	case c := <-got:
		if c != server {
			t.Fatalf("handled %v, want the accepted connection", c)
		}
		c.Close()
	case <-time.After(5 * time.Second):
		t.Fatal("the connection after two failed accepts was never handled")
	}
	waitClosed(t, done, "Run did not end on a closed listener")
	if n := logs.Load(); n != 1 {
		t.Fatalf("%d log lines for one run of failures, want 1", n)
	}
}

// While Accept keeps failing the loop waits longer each time instead of spinning, and ends with ctx.
func TestRunBacksOffWhileFailing(t *testing.T) {
	shortBackoff(t, 5*time.Millisecond, time.Second)
	ln := &scriptedListener{script: []acceptResult{{err: syscall.EMFILE}}}
	ctx, cancel := context.WithCancel(context.Background())
	done := goRun(ctx, ln, func(string, ...any) {}, func(net.Conn) { t.Error("handled a connection") })
	time.Sleep(60 * time.Millisecond)
	cancel()
	waitClosed(t, done, "Run did not end with ctx")
	// Waits of 5, 10, 20 ms fit in 60 ms: about 4 calls. A loop that retries at once makes hundreds of thousands.
	if n := ln.calls.Load(); n > 10 {
		t.Fatalf("Accept called %d times in 60 ms while failing", n)
	}
}

// A served connection resets the wait: a failure after it waits the minimum again, and is logged again.
func TestRunResetsBackoffAfterSuccess(t *testing.T) {
	shortBackoff(t, time.Millisecond, time.Hour)
	a, b := net.Pipe()
	defer b.Close()
	var script []acceptResult
	for range 8 { // waits of 1 … 128 ms; without the reset the next failure would wait 256 ms
		script = append(script, acceptResult{err: syscall.ECONNABORTED})
	}
	script = append(script, acceptResult{c: a}, acceptResult{err: syscall.EMFILE}, acceptResult{c: a}, acceptResult{err: net.ErrClosed})
	ln := &scriptedListener{script: script}
	var handled []time.Time // appended on the loop's goroutine only, read after it ended
	var logs atomic.Int64
	done := goRun(context.Background(), ln, func(string, ...any) { logs.Add(1) }, func(net.Conn) { handled = append(handled, time.Now()) })
	waitClosed(t, done, "Run did not get through the script")
	if len(handled) != 2 {
		t.Fatalf("handled %d connections, want 2", len(handled))
	}
	if gap := handled[1].Sub(handled[0]); gap > 150*time.Millisecond {
		t.Fatalf("one failure after a served connection waited %s: the wait was not reset", gap)
	}
	if n := logs.Load(); n != 2 {
		t.Fatalf("%d log lines for two runs of failures, want 2", n)
	}
}

// Closing a real listener ends Run at once: its error wraps net.ErrClosed, which is not retried or logged.
func TestRunEndsWhenARealListenerCloses(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() }) // a failed check below must not leave Run blocked in Accept
	served := make(chan struct{}, 1)
	var logs atomic.Int64
	done := goRun(context.Background(), ln, func(string, ...any) { logs.Add(1) }, func(c net.Conn) {
		c.Close()
		served <- struct{}{}
	})
	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	select {
	case <-served:
	case <-time.After(5 * time.Second):
		t.Fatal("a connection to the listener was never handled")
	}
	ln.Close()
	waitClosed(t, done, "Run did not end when its listener closed")
	if n := logs.Load(); n != 0 {
		t.Fatalf("%d log lines for a closed listener, want 0", n)
	}
}
