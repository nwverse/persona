//go:build linux

package osproc

import (
	"errors"
	"os"
	"syscall"

	"github.com/nwverse/persona/server/internal/procinfo"
	"golang.org/x/sys/unix"
)

// identify reads the process's start time from /proc (field 22 of stat, clock
// ticks since boot): a resolution a reused pid cannot land in twice.
func identify(pid int) ProcIdentity {
	id, _ := identifyKnown(pid)
	return id
}

func identifyKnown(pid int) (ProcIdentity, bool) {
	return identifyFromStat(pid, procinfo.Stat)
}

func identifyFromStat(pid int, stat func(int) ([]string, error)) (ProcIdentity, bool) {
	fields, err := stat(pid)
	if err != nil {
		if os.IsNotExist(err) {
			// A missing pid is conclusive only when procfs itself is readable.
			// A missing or masked procfs must still allow the portable fallback.
			self, selfErr := stat(os.Getpid())
			return ProcIdentity{}, selfErr == nil && len(self) >= 20
		}
		return ProcIdentity{}, false
	}
	// Stat locates state after the last ')' in comm, which can itself contain
	// spaces and parentheses. A zombie retains its start time but cannot run.
	if len(fields) > 0 && (fields[0] == "Z" || fields[0] == "X") {
		return ProcIdentity{}, true
	}
	if len(fields) < 20 {
		return ProcIdentity{}, false
	}
	return ProcIdentity{Start: "ticks:" + fields[19]}, true
}

// killPinned pins the instance with a pidfd and signals that: a pid reused
// between the identity check and the kill is never hit.
func (id ProcIdentity) killPinned(pid int) (bool, error) {
	fd, err := unix.PidfdOpen(pid, 0)
	if err != nil {
		// No pidfd here (a kernel before 5.3, or the process already gone):
		// fall back to the checked-by-pid kill.
		return false, nil
	}
	defer unix.Close(fd)
	// The fd names the instance pidfd_open saw; the pid may already name a
	// newer one. Only signal the pinned instance while the pid still does.
	if identify(pid) != id {
		return true, nil
	}
	if err := unix.PidfdSendSignal(fd, unix.SIGKILL, nil, 0); err != nil && !errors.Is(err, syscall.ESRCH) {
		return true, err
	}
	return true, nil
}
