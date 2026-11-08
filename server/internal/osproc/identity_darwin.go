//go:build darwin

package osproc

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/unix"
)

// The kern.proc.pid mib levels (sys/sysctl.h), not exported by x/sys.
const (
	ctlKern     = 1
	kernProc    = 14
	kernProcPid = 1
)

// identify asks the kernel for the process's start time (kern.proc.pid), exact
// to the microsecond — far past what a pid reused within the same second can dodge.
func identify(pid int) ProcIdentity {
	mib := [4]int32{ctlKern, kernProc, kernProcPid, int32(pid)}
	var info unix.KinfoProc
	size := unsafe.Sizeof(info)
	_, _, errno := unix.Syscall6(unix.SYS_SYSCTL,
		uintptr(unsafe.Pointer(&mib[0])), 4,
		uintptr(unsafe.Pointer(&info)), uintptr(size), 0, 0)
	if errno != 0 {
		return ProcIdentity{}
	}
	st := info.Proc.P_starttime
	return ProcIdentity{Start: fmt.Sprintf("usec:%d", st.Sec*1000000+int64(st.Usec))}
}

// KillIfStill ends the process pid, but only when it is still the exact
// instance this identity was taken from. macOS has no handle that pins an
// instance, so the check and the kill are two calls with a window one syscall
// wide, guarded by the microsecond identity. The process being gone, or no
// longer that instance, is not an error.
func (id ProcIdentity) KillIfStill(pid int) error {
	if pid < 1 || id.Start == "" {
		return nil
	}
	return id.killUnpinned(pid)
}
