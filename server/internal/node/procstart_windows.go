package node

import "strings"

// claudeFileStart is the start a sessions/<pid>.json's procStart stands for on Windows: the process's creation time
// as a FILETIME (100 ns ticks since 1601), which is what Claude Code writes there (procinfo.StartMark reads the same).
func claudeFileStart(procStart string) string {
	if procStart == "" || strings.Trim(procStart, "0123456789") != "" {
		return ""
	}
	return "windows:" + procStart
}

// procGone says pid is surely no longer the process seen with start: not running, or running with another start (a
// reused pid).
func procGone(pid int, start string) bool {
	cur := procStartMark(pid)
	if cur == "" {
		return !alive(pid)
	}
	return start != "" && cur != start
}
