//go:build !windows

package osproc

import (
	"os"
	"strconv"
	"syscall"
)

// SealInherited makes every descriptor this process holds above stderr close-on-exec, and drops socket activation's
// variables (LISTEN_PID, LISTEN_FDS, LISTEN_FDNAMES) when they were meant for another process. Call it first thing in
// a long-running process that starts others and was handed nothing past stderr on purpose (a manager, a node): a
// descriptor a parent left inheritable, such as an older hub's listening socket, then stays here instead of reaching
// every program this process starts, and through them every program those start (docs/operations/hub-restart.md).
//
// This process keeps using what it holds; only an exec drops it. A descriptor handed to a child on purpose through
// exec.Cmd.ExtraFiles still reaches it: the child gets a copy whatever the flag says. Never call it in a process that
// hands an inherited descriptor on by exec without ExtraFiles (the sandbox's launcher passes its mount data that way).
func SealInherited() {
	for _, fd := range openFDs() {
		if fd > 2 {
			syscall.CloseOnExec(fd)
		}
	}
	if os.Getenv("LISTEN_PID") != strconv.Itoa(os.Getpid()) {
		for _, k := range []string{"LISTEN_PID", "LISTEN_FDS", "LISTEN_FDNAMES"} {
			_ = os.Unsetenv(k)
		}
	}
}

// fdScanMost bounds the scan by number when no directory lists the open descriptors.
const fdScanMost = 1 << 16

// openFDs are this process's descriptors as /proc/self/fd (Linux) or /dev/fd (macOS, the BSDs) lists them; where
// neither can be read, every number below the descriptor limit (at most fdScanMost). The listing's own descriptor is
// among them, closed by the time it is returned: marking a closed or reused number does no harm.
func openFDs() []int {
	for _, dir := range []string{"/proc/self/fd", "/dev/fd"} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		fds := make([]int, 0, len(entries))
		for _, e := range entries {
			if fd, err := strconv.Atoi(e.Name()); err == nil {
				fds = append(fds, fd)
			}
		}
		return fds
	}
	most := 1024
	var lim syscall.Rlimit
	if syscall.Getrlimit(syscall.RLIMIT_NOFILE, &lim) == nil && lim.Cur > uint64(most) {
		most = int(min(lim.Cur, fdScanMost))
	}
	fds := make([]int, most)
	for i := range fds {
		fds[i] = i
	}
	return fds
}
