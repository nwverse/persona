package supervise

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// The test binary stands in for the hub and the manager (`hub run`, `assistant`) when SUPERVISE_TEST_DIR is set: it
// notes each start (its pid and PERSONA_ASSISTANT_DEPLOY) in <dir>/<role>.starts, exits with the code in
// <dir>/<role>.exit when that file shows up (taking it), stops on SIGTERM unless <dir>/<role>.stubborn is there.
func TestMain(m *testing.M) {
	dir := os.Getenv("SUPERVISE_TEST_DIR")
	if dir != "" && len(os.Args) > 1 && (os.Args[1] == "hub" || os.Args[1] == "assistant") {
		child(dir, os.Args[1])
	}
	os.Exit(m.Run())
}

func child(dir, role string) {
	f, err := os.OpenFile(filepath.Join(dir, role+".starts"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		os.Exit(9)
	}
	fmt.Fprintf(f, "%d %s\n", os.Getpid(), os.Getenv("PERSONA_ASSISTANT_DEPLOY"))
	f.Close()
	term := make(chan os.Signal, 1)
	signal.Notify(term, syscall.SIGTERM)
	for {
		select {
		case <-term:
			if _, err := os.Stat(filepath.Join(dir, role+".stubborn")); err != nil {
				os.Exit(0)
			}
		case <-time.After(5 * time.Millisecond):
			exitFile := filepath.Join(dir, role+".exit")
			if b, err := os.ReadFile(exitFile); err == nil {
				_ = os.Remove(exitFile)
				code, _ := strconv.Atoi(strings.TrimSpace(string(b)))
				os.Exit(code)
			}
		}
	}
}

// syncBuffer is the supervisor's log, written from several goroutines.
type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

type fixture struct {
	e        *env
	dir      string // the children's notes
	out      *syncBuffer
	mu       sync.Mutex
	execs    int
	binds    []string
	bindErrs []error // what the next binds answer (nil after)
}

func newFixture(t *testing.T, hubJSON string) *fixture {
	t.Helper()
	old := []time.Duration{backoffFirst, backoffMost, steadyAfter, stopWait, bindRetry, modeRecheck}
	t.Cleanup(func() {
		backoffFirst, backoffMost, steadyAfter, stopWait, bindRetry, modeRecheck = old[0], old[1], old[2], old[3], old[4], old[5]
	})
	backoffFirst, backoffMost, steadyAfter, stopWait, bindRetry, modeRecheck =
		10*time.Millisecond, 40*time.Millisecond, time.Hour, 2*time.Second, 20*time.Millisecond, 20*time.Millisecond
	t.Setenv("PERSONA_LISTEN", "")
	t.Setenv(SeparateEnv, "")
	home, dir := t.TempDir(), t.TempDir()
	if hubJSON != "" {
		if err := os.WriteFile(filepath.Join(home, "hub.json"), []byte(hubJSON), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	f := &fixture{dir: dir, out: &syncBuffer{}}
	f.e = &env{self: os.Args[0], home: home, out: f.out,
		environ: append(os.Environ(), "SUPERVISE_TEST_DIR="+dir),
		execHub: func(string, []string) error {
			f.mu.Lock()
			f.execs++
			f.mu.Unlock()
			return nil
		},
		bind: func(_ context.Context, hubURL string) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.binds = append(f.binds, hubURL)
			if len(f.bindErrs) > 0 {
				err := f.bindErrs[0]
				f.bindErrs = f.bindErrs[1:]
				return err
			}
			return nil
		}}
	return f
}

// starts are the starts of role: pid and PERSONA_ASSISTANT_DEPLOY each.
func (f *fixture) starts(role string) [][2]string {
	b, _ := os.ReadFile(filepath.Join(f.dir, role+".starts"))
	var out [][2]string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if l == "" {
			continue
		}
		pid, deploy, _ := strings.Cut(l, " ")
		out = append(out, [2]string{pid, deploy})
	}
	return out
}

// stop makes role exit with code.
func (f *fixture) stop(t *testing.T, role string, code int) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.dir, role+".exit"), []byte(strconv.Itoa(code)), 0o600); err != nil {
		t.Fatal(err)
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

// running runs fn (the supervisor) until stop is called, which cancels it and waits for it to return.
func running(t *testing.T, fn func(ctx context.Context) error) (stop func() error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- fn(ctx) }()
	return func() error {
		cancel()
		select {
		case err := <-done:
			return err
		case <-time.After(15 * time.Second):
			t.Fatal("the supervisor did not return")
			return nil
		}
	}
}

// Not a service: the supervisor becomes the hub (exec), as the container was before; it starts nothing itself.
func TestChildModeIsTheHubAsBefore(t *testing.T) {
	for _, hubJSON := range []string{"", `{"token":"t"}`, `{"assistant":{"mode":"child"}}`, `{"assistant":{"mode":"service","external":true}}`} {
		f := newFixture(t, hubJSON)
		if err := f.e.hubContainer(context.Background()); err != nil || f.execs != 1 || len(f.starts("hub")) != 0 || len(f.binds) != 0 {
			t.Fatalf("%s: err %v, execs %d, starts %v, binds %v", hubJSON, err, f.execs, f.starts("hub"), f.binds)
		}
	}
}

// Service mode: the hub and the built-in manager (deploy service, bound first at the hub's loopback address) side by
// side; whichever stops starts again, and the other is left running; a stop stops both.
func TestServiceModeRunsBothAndRestartsOnlyTheOneThatStopped(t *testing.T) {
	f := newFixture(t, `{"listen":":18765","assistant":{"mode":"service"}}`)
	stop := running(t, f.e.hubContainer)
	waitFor(t, "both started", func() bool { return len(f.starts("hub")) == 1 && len(f.starts("assistant")) == 1 })
	if m := f.starts("assistant")[0]; m[1] != "service" {
		t.Fatalf("manager deploy %q", m[1])
	}
	hubPID := f.starts("hub")[0][0]

	f.stop(t, "assistant", 1)
	waitFor(t, "the manager again", func() bool { return len(f.starts("assistant")) == 2 })
	if h := f.starts("hub"); len(h) != 1 || h[0][0] != hubPID {
		t.Fatalf("the hub was restarted too: %v", h)
	}
	mgrPID := f.starts("assistant")[1][0]
	f.stop(t, "hub", 0) // even a clean exit (an admin's restart): started again
	waitFor(t, "the hub again", func() bool { return len(f.starts("hub")) == 2 })
	if m := f.starts("assistant"); len(m) != 2 || m[1][0] != mgrPID {
		t.Fatalf("the manager was restarted too: %v", m)
	}
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	for _, pid := range []string{f.starts("hub")[1][0], mgrPID} {
		n, _ := strconv.Atoi(pid)
		if syscall.Kill(n, 0) == nil {
			t.Errorf("pid %d still runs after the stop", n)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.binds) != 2 || f.binds[0] != "http://127.0.0.1:18765" {
		t.Fatalf("binds %v (one before each start of the manager)", f.binds)
	}
	log := f.out.String()
	if !strings.Contains(log, "manager stopped (exit status 1): starting it again in 10ms; the other runs on") {
		t.Fatalf("log:\n%s", log)
	}
}

// A program that keeps stopping waits longer each time, up to backoffMost.
func TestBackoffGrows(t *testing.T) {
	f := newFixture(t, `{"assistant":{"mode":"service"}}`)
	stop := running(t, f.e.hubContainer)
	for i := 1; i <= 4; i++ {
		waitFor(t, "the manager", func() bool { return len(f.starts("assistant")) == i })
		f.stop(t, "assistant", 3)
	}
	waitFor(t, "the manager again", func() bool { return len(f.starts("assistant")) == 5 })
	_ = stop()
	log := f.out.String()
	for _, want := range []string{"again in 10ms", "again in 20ms", "again in 40ms;"} {
		if !strings.Contains(log, want) {
			t.Fatalf("no %q in:\n%s", want, log)
		}
	}
	if strings.Contains(log, "again in 80ms") {
		t.Fatalf("past backoffMost:\n%s", log)
	}
}

// A binding that fails is said and tried again; the manager starts once it works, the hub runs all along.
func TestBindingRetried(t *testing.T) {
	f := newFixture(t, `{"assistant":{"mode":"service"}}`)
	f.bindErrs = []error{errors.New("the hub did not answer"), errors.New("still not")}
	stop := running(t, f.e.hubContainer)
	waitFor(t, "the manager", func() bool { return len(f.starts("assistant")) == 1 })
	_ = stop()
	if h := f.starts("hub"); len(h) != 1 {
		t.Fatalf("hub starts %v", h)
	}
	if log := f.out.String(); strings.Count(log, "manager: the hub did not answer; trying again") != 1 || !strings.Contains(log, "still not") {
		t.Fatalf("log:\n%s", log)
	}
}

// The built-in manager in a container of its own: the hub's container runs the hub alone.
func TestSeparateManager(t *testing.T) {
	f := newFixture(t, `{"assistant":{"mode":"service"}}`)
	t.Setenv(SeparateEnv, "separate")
	stop := running(t, f.e.hubContainer)
	waitFor(t, "the hub", func() bool { return len(f.starts("hub")) == 1 })
	time.Sleep(100 * time.Millisecond)
	_ = stop()
	if len(f.starts("assistant")) != 0 || len(f.binds) != 0 {
		t.Fatalf("a manager here: %v %v", f.starts("assistant"), f.binds)
	}
}

// --manager-only: waits while the hub starts its own child, then runs the built-in manager bound to --hub.
func TestManagerOnly(t *testing.T) {
	f := newFixture(t, `{"assistant":{"mode":"child"}}`)
	stop := running(t, func(ctx context.Context) error { return f.e.managerOnly(ctx, "http://hub:18660") })
	time.Sleep(100 * time.Millisecond)
	if len(f.starts("assistant")) != 0 {
		t.Fatal("started in child mode")
	}
	if err := os.WriteFile(filepath.Join(f.e.home, "hub.json"), []byte(`{"assistant":{"mode":"service"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the manager", func() bool { return len(f.starts("assistant")) == 1 })
	_ = stop()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.binds) != 1 || f.binds[0] != "http://hub:18660" || len(f.starts("hub")) != 0 ||
		!strings.Contains(f.out.String(), "assistant.mode is not service") {
		t.Fatalf("binds %v, hub %v\n%s", f.binds, f.starts("hub"), f.out.String())
	}
}

// A program that does not stop on SIGTERM is killed after stopWait.
func TestStubbornProgramKilled(t *testing.T) {
	f := newFixture(t, `{"assistant":{"mode":"service"}}`)
	stopWait = 200 * time.Millisecond
	if err := os.WriteFile(filepath.Join(f.dir, "assistant.stubborn"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	stop := running(t, f.e.hubContainer)
	waitFor(t, "both", func() bool { return len(f.starts("hub")) == 1 && len(f.starts("assistant")) == 1 })
	began := time.Now()
	_ = stop()
	if time.Since(began) < stopWait || !strings.Contains(f.out.String(), "manager did not stop within 200ms: killing it") {
		t.Fatalf("took %s\n%s", time.Since(began), f.out.String())
	}
}

func TestLoopback(t *testing.T) {
	for listen, want := range map[string]string{"": "http://127.0.0.1:18660", ":18765": "http://127.0.0.1:18765",
		"0.0.0.0:19000": "http://127.0.0.1:19000", "[::]:19001": "http://127.0.0.1:19001", "[fd00::20]:19002": "http://[fd00::20]:19002"} {
		if got := loopback(listen); got != want {
			t.Errorf("%q: %s, want %s", listen, got, want)
		}
	}
}
