//go:build !linux && !windows

package node

// claudeFileStart: see procstart_linux.go; nothing to compare with here.
func claudeFileStart(string) string { return "" }

// procGone: see procstart_linux.go; here only whether the pid is alive (a zombie still counts as alive).
func procGone(pid int, _ string) bool { return !alive(pid) }
