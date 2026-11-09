//go:build !linux && !windows

package procinfo

// StartMark: only Linux (/proc) and Windows tell when a process started; elsewhere a pid is trusted while it is alive.
func StartMark(int) string { return "" }
