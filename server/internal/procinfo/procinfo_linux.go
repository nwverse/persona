//go:build linux

package procinfo

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Stat is the fields of /proc/<pid>/stat after the process name (which may hold spaces and parentheses, so it is found
// from the right): index 0 is field 3, the state.
func Stat(pid int) ([]string, error) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return nil, err
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return nil, fmt.Errorf("/proc/%d/stat: no name", pid)
	}
	return strings.Fields(s[i+1:]), nil
}

// Parent is pid's parent process (field 4 of /proc/<pid>/stat).
func Parent(pid int) (int, error) {
	f, err := Stat(pid)
	if err != nil {
		return 0, err
	}
	if len(f) < 2 {
		return 0, fmt.Errorf("/proc/%d/stat: short", pid)
	}
	return strconv.Atoi(f[1])
}

// StartMark is when pid started as "linux:<starttime>" (clock ticks since boot, field 22 of /proc/<pid>/stat), the
// same number Claude Code writes as procStart in its sessions/<pid>.json. A reused pid has another one. "" when the
// process is gone, and also when it is a zombie (state Z or X: killed or exited, not reaped by its parent yet), whose
// stat still reads with the old start: it no longer runs, as on Windows once a process has exited.
func StartMark(pid int) string {
	f, err := Stat(pid)
	if err != nil || len(f) < 20 || f[0] == "Z" || f[0] == "X" {
		return ""
	}
	return "linux:" + f[19] // field 22
}
