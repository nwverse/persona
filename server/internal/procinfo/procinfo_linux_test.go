//go:build linux

package procinfo

import (
	"errors"
	"os/exec"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// A killed process its parent has not reaped yet is a zombie: its stat still reads with the old start, but it no
// longer runs, so it has no start mark, as an exited process has none on Windows. Reaped, it has none either.
func TestStartMarkOfAZombie(t *testing.T) {
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Skip(err)
	}
	pid := cmd.Process.Pid
	reaped := false
	t.Cleanup(func() {
		if !reaped {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	if m := StartMark(pid); !strings.HasPrefix(m, "linux:") {
		t.Fatalf("a running child has start mark %q", m)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	// Wait until it has exited but leave it unreaped (WNOWAIT): a zombie, with no polling.
	var info unix.Siginfo
	err := unix.Waitid(unix.P_PID, pid, &info, unix.WEXITED|unix.WNOWAIT, nil)
	for errors.Is(err, unix.EINTR) {
		err = unix.Waitid(unix.P_PID, pid, &info, unix.WEXITED|unix.WNOWAIT, nil)
	}
	if err != nil {
		t.Fatal(err)
	}
	if f, err := Stat(pid); err != nil || len(f) == 0 || f[0] != "Z" {
		t.Fatalf("not a zombie: %v %v", f, err)
	}
	if m := StartMark(pid); m != "" {
		t.Fatalf("a killed, unreaped process has start mark %q", m)
	}
	_ = cmd.Wait()
	reaped = true
	if m := StartMark(pid); m != "" {
		t.Fatalf("a reaped process has start mark %q", m)
	}
}
