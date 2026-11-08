package osproctest

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// HomeEnv is an environment for a child of the test: PATH, and HOME and PERSONA_HOME in a new directory of its own
// (short, for the Unix sockets made in it), removed when the test ends; then more, as NAME=value.
func HomeEnv(t testing.TB, more ...string) []string {
	t.Helper()
	home, err := os.MkdirTemp("", "pfd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	return append([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "PERSONA_HOME=" + home + "/.persona"}, more...)
}

// Holder is a process StartHolding started.
type Holder struct {
	Pid    int
	exited chan struct{}
	err    error // how it ended, once exited is closed
}

// StartHolding starts cmd holding a listening TCP socket on a free loopback port at descriptor 3, inheritable there
// as an older hub leaves its own; with stdin a pipe left open, so a process reading it stays; in a process group of
// its own, killed with everything it started when the test ends.
func StartHolding(t testing.TB, cmd *exec.Cmd) *Holder {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	f, err := ln.(*net.TCPListener).File()
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.ExtraFiles = []*os.File{f}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	h := &Holder{Pid: cmd.Process.Pid, exited: make(chan struct{})}
	go func() {
		h.err = cmd.Wait()
		close(h.exited)
	}()
	t.Cleanup(func() {
		_ = syscall.Kill(-h.Pid, syscall.SIGKILL)
		_ = stdin.Close()
		<-h.exited
	})
	return h
}

// CloseOnExec says whether the process holds descriptor fd close-on-exec, from the flags in /proc/<pid>/fdinfo/<fd>.
func (h *Holder) CloseOnExec(fd int) (bool, error) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/fdinfo/%d", h.Pid, fd))
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "flags:"); ok {
			flags, err := strconv.ParseUint(strings.TrimSpace(v), 8, 64)
			return err == nil && flags&syscall.O_CLOEXEC != 0, err
		}
	}
	return false, errors.New("no flags in fdinfo")
}

// WaitSealed waits, for at most timeout, until the process holds descriptor fd close-on-exec. It fails at once when
// the process ends; a descriptor it no longer holds never counts as sealed.
func (h *Holder) WaitSealed(fd int, timeout time.Duration) error {
	deadline := time.After(timeout)
	for {
		sealed, err := h.CloseOnExec(fd)
		if sealed {
			return nil
		}
		select {
		case <-h.exited:
			return fmt.Errorf("it ended (%v) without sealing descriptor %d", h.err, fd)
		case <-deadline:
			if err != nil {
				return fmt.Errorf("descriptor %d after %s: %w", fd, timeout, err)
			}
			return fmt.Errorf("descriptor %d still inheritable after %s", fd, timeout)
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// Running says whether the process has not ended yet.
func (h *Holder) Running() bool {
	select {
	case <-h.exited:
		return false
	default:
		return true
	}
}
