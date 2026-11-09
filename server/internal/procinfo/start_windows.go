package procinfo

import (
	"strconv"

	"golang.org/x/sys/windows"
)

// StartMark is when pid started as "windows:<creation time>" (100 ns ticks since 1601, the FILETIME GetProcessTimes
// gives), the same number Claude Code writes as procStart in its sessions/<pid>.json on Windows. Windows reuses pids
// quickly; a reused one has another start. "" when the process is gone or cannot be opened.
func StartMark(pid int) string {
	if pid <= 0 {
		return ""
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	var created, exited, kernel, user windows.Filetime
	if windows.GetProcessTimes(h, &created, &exited, &kernel, &user) != nil {
		return ""
	}
	if code := exitCode(h); code != stillActive {
		return "" // it exited; the handle only keeps its record
	}
	ticks := uint64(created.HighDateTime)<<32 | uint64(created.LowDateTime)
	return "windows:" + strconv.FormatUint(ticks, 10)
}

// stillActive is the exit code GetExitCodeProcess reports for a process that is still running (STILL_ACTIVE).
const stillActive = 259

func exitCode(h windows.Handle) uint32 {
	var code uint32
	if windows.GetExitCodeProcess(h, &code) != nil {
		return 0
	}
	return code
}
