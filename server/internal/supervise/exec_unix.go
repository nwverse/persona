//go:build unix

package supervise

import (
	"os/exec"
	"syscall"
)

// execHub replaces this process with `self hub run`: the hub runs as it did before the supervisor (its own pid, the
// container's signals straight to it).
func execHub(self string, env []string) error {
	return syscall.Exec(self, []string{self, "hub", "run"}, env)
}

// ownGroup has cmd start a process group of its own.
func ownGroup(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

// killGroup kills what is left of cmd's process group once it stopped.
func killGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
