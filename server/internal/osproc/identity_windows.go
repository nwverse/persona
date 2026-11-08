//go:build windows

package osproc

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// identifyOf reads one process handle's creation time: the kernel's own
// instance identity, exact to 100 ns, that a reused pid cannot repeat.
func identifyOf(h windows.Handle) ProcIdentity {
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &created, &exited, &kernel, &user); err != nil {
		return ProcIdentity{}
	}
	return ProcIdentity{Start: fmt.Sprintf("ft:%d", created.Nanoseconds())}
}

// identify opens the process to read the same creation time identifyOf reads.
func identify(pid int) ProcIdentity {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return ProcIdentity{}
	}
	defer windows.CloseHandle(h)
	return identifyOf(h)
}

// KillIfStill ends the process pid, but only when it is still the exact
// instance this identity was taken from. One handle carries the check and the
// kill — a handle names an instance, not a pid — so a pid reused in between is
// never hit. The process being gone, or no longer that instance, is not an error.
func (id ProcIdentity) KillIfStill(pid int) error {
	if pid < 1 || id.Start == "" {
		return nil
	}
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return nil // no such process, or not ours to open
	}
	defer windows.CloseHandle(h)
	if identifyOf(h) != id {
		return nil
	}
	if err := windows.TerminateProcess(h, 1); err != nil && Alive(pid) {
		return err
	}
	return nil
}
