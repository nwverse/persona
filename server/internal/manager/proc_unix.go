//go:build unix

package manager

import (
	"os/exec"
	"syscall"
)

// killGroup has cmd run in a process group of its own, and its cancel kill the whole group.
func killGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
