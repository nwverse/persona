//go:build linux

package node

import (
	"os/exec"
	"syscall"
	"testing"

	"github.com/nwverse/persona/server/internal/procinfo"
)

// A claude killed but not reaped yet is a zombie whose /proc entry still has its start: that is gone, as is one reaped,
// and no longer the same process for its hook turn.
func TestProcGoneZombieAndReaped(t *testing.T) {
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Skip(err)
	}
	pid := cmd.Process.Pid
	start := procStartMark(pid)
	if start == "" || procGone(pid, start) {
		t.Fatalf("a running process reads as gone (start %q)", start)
	}
	if !procGone(pid, "linux:1") {
		t.Fatal("another start (a reused pid) is not gone")
	}
	_ = cmd.Process.Signal(syscall.SIGKILL)
	waitFor(t, "the zombie", func() bool {
		f, err := procinfo.Stat(pid)
		return err == nil && len(f) > 0 && f[0] == "Z"
	})
	if !procGone(pid, start) {
		t.Fatal("a zombie reads as alive")
	}
	if sameProcess(pid, start) {
		t.Fatal("a zombie is still the process its hook turn was seen with")
	}
	_ = cmd.Wait()
	if !procGone(pid, start) {
		t.Fatal("a reaped process reads as alive")
	}
}
