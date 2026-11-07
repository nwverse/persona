//go:build unix

package supervise

import "syscall"

// execHub replaces this process with `self hub run`: the hub runs as it did before the supervisor (its own pid, the
// container's signals straight to it).
func execHub(self string, env []string) error {
	return syscall.Exec(self, []string{self, "hub", "run"}, env)
}
