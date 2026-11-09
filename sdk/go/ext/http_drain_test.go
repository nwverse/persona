package ext

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// sockDir is a temporary directory with a short path: t.TempDir() can be too long for a unix socket (104 bytes on
// macOS).
func sockDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "ext")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// unixClient is an HTTP client that reaches the server on the unix socket at sock.
func unixClient(sock string) *http.Client {
	return &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sock)
		},
	}}
}

// waitHealthy polls GET /health until the server on sock answers.
func waitHealthy(t *testing.T, c *http.Client) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		resp, err := c.Get("http://ext/health")
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("server not answering /health: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestServeHTTPWaitsForInFlightRequests(t *testing.T) {
	sock := filepath.Join(sockDir(t), "h.sock")
	started, release := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	defer unblock() // a failing test does not leave the handler blocked
	var finished atomic.Bool
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		_, _ = w.Write([]byte("done"))
		finished.Store(true)
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	returned := make(chan error, 1)
	go func() { returned <- ServeHTTP(ctx, sock, h) }()
	c := unixClient(sock)
	waitHealthy(t, c)

	type result struct {
		status int
		body   string
		err    error
	}
	got := make(chan result, 1)
	go func() {
		resp, err := c.Get("http://ext/slow")
		if err != nil {
			got <- result{err: err}
			return
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		got <- result{resp.StatusCode, string(b), err}
	}()
	<-started
	cancel()

	select {
	case err := <-returned:
		t.Fatalf("ServeHTTP returned (%v) while a request was still in flight", err)
	case <-time.After(300 * time.Millisecond):
	}
	unblock()
	select {
	case err := <-returned:
		if err != nil {
			t.Fatalf("ServeHTTP: %v", err)
		}
		if !finished.Load() {
			t.Fatal("ServeHTTP returned before the handler finished")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("ServeHTTP did not return after the request finished")
	}
	r := <-got
	if r.err != nil || r.status != http.StatusOK || r.body != "done" {
		t.Fatalf("response = %d %q, %v; want 200 \"done\"", r.status, r.body, r.err)
	}
}

func TestServeHTTPCutsRequestsAfterTheDrain(t *testing.T) {
	defer func(d time.Duration) { httpDrain = d }(httpDrain)
	httpDrain = 100 * time.Millisecond
	sock := filepath.Join(sockDir(t), "h.sock")
	started, cut := make(chan struct{}), make(chan struct{})
	h := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done() // never answers on its own
		close(cut)
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	returned := make(chan error, 1)
	go func() { returned <- ServeHTTP(ctx, sock, h) }()
	c := unixClient(sock)
	waitHealthy(t, c)
	go func() {
		if resp, err := c.Get("http://ext/stuck"); err == nil {
			_ = resp.Body.Close()
		}
	}()
	<-started
	cancel()
	select {
	case err := <-returned:
		if err == nil || !strings.Contains(err.Error(), "cut") {
			t.Fatalf("ServeHTTP = %v; want the error that it cut a request", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("ServeHTTP did not return after the drain")
	}
	select {
	case <-cut:
	case <-time.After(10 * time.Second):
		t.Fatal("the request still running after the drain was not cut")
	}
}

func TestServeHTTPReturnsListenError(t *testing.T) {
	sock := filepath.Join(sockDir(t), "missing", "h.sock")
	if err := ServeHTTP(context.Background(), sock, http.NotFoundHandler()); err == nil {
		t.Fatal("ServeHTTP on a socket in a missing directory: no error")
	}
}

// mainHelperEnv makes the test binary run Main with an HTTP program instead of the tests (see startMain).
const mainHelperEnv = "PERSONA_SDK_TEST_MAIN_HTTP"

// TestMainHelper is the program startMain runs: Main with a handler that sends its headers at once and its body
// after 300 ms, so the parent can stop the program while the response is in flight.
func TestMainHelper(t *testing.T) {
	if os.Getenv(mainHelperEnv) != "1" {
		t.Skip("run by TestMainDrainsHTTP")
	}
	Main(Program{ID: "drain", Version: "1.0.0", HTTP: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		time.Sleep(300 * time.Millisecond)
		_, _ = w.Write([]byte("done"))
	})})
}

// startMain runs TestMainHelper in a child process serving on a socket in a temporary directory; its stderr goes to
// the returned file.
func startMain(t *testing.T, stdin *os.File) (*exec.Cmd, *http.Client, *os.File) {
	t.Helper()
	dir := sockDir(t)
	sock := filepath.Join(dir, "h.sock")
	stderr, err := os.Create(filepath.Join(dir, "stderr"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stderr.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestMainHelper$")
	// The race detector sleeps a second before a program exits, which would let the response finish without the fix.
	cmd.Env = append(os.Environ(), mainHelperEnv+"=1", "PERSONA_EXTENSION_SOCKET="+sock,
		"GORACE="+strings.TrimSpace(os.Getenv("GORACE")+" atexit_sleep_ms=0"))
	if stdin != nil {
		cmd.Stdin = stdin
	}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	c := unixClient(sock)
	waitHealthy(t, c)
	return cmd, c, stderr
}

// TestMainDrainsHTTP stops a program the way the hub does while a response is in flight, and the program finishes the
// response before it exits: SIGTERM to a program that does not speak the protocol (rpc: none, nothing on stdin), and
// stdin closed on one that does.
func TestMainDrainsHTTP(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SIGTERM and unix sockets")
	}
	t.Run("sigterm, rpc none", func(t *testing.T) {
		cmd, c, stderr := startMain(t, nil)
		drains(t, cmd, c, stderr, func() error { return cmd.Process.Signal(syscall.SIGTERM) })
	})
	t.Run("stdin closed", func(t *testing.T) {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		defer w.Close()
		if _, err := w.Write([]byte(`{"id":"1","method":"initialize","params":{"protocol":1}}` + "\n")); err != nil {
			t.Fatal(err)
		}
		cmd, c, stderr := startMain(t, r)
		_ = r.Close() // the child has its own copy
		drains(t, cmd, c, stderr, w.Close)
	})
}

// drains starts a slow request, stops the program with stop once the response headers arrived, and checks that the
// body still arrives in full and the program exits cleanly.
func drains(t *testing.T, cmd *exec.Cmd, c *http.Client, stderr *os.File, stop func() error) {
	t.Helper()
	resp, err := c.Get("http://ext/slow")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil || string(body) != "done" {
		t.Fatalf("body = %q, %v; want \"done\" (stderr: %s)", body, err, logOf(stderr))
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("program exit: %v (stderr: %s)", err, logOf(stderr))
	}
}

// logOf is what the child wrote to f so far.
func logOf(f *os.File) string {
	b, _ := os.ReadFile(f.Name())
	return string(b)
}
