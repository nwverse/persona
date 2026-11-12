//go:build !windows

package herdr

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// ttySize is the window size of the terminal process pid runs on.
func ttySize(ctx context.Context, pid int) (cols, rows int, err error) {
	name, err := ttyOf(ctx, pid)
	if err != nil {
		return 0, 0, err
	}
	// Read only, and never as this process's controlling terminal; nothing is read from it, so the pane's programs
	// lose no input.
	f, err := os.OpenFile(name, os.O_RDONLY|syscall.O_NOCTTY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	ws, err := unix.IoctlGetWinsize(int(f.Fd()), unix.TIOCGWINSZ)
	if err != nil {
		return 0, 0, fmt.Errorf("%s: %w", name, err)
	}
	if ws.Col == 0 || ws.Row == 0 {
		return 0, 0, fmt.Errorf("%s: no window size", name)
	}
	return int(ws.Col), int(ws.Row), nil
}

// ttyOf is the device of the terminal pid runs on: its stdin's on Linux, else what ps says (macOS has no /proc).
func ttyOf(ctx context.Context, pid int) (string, error) {
	if link, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/fd/0"); err == nil && isTTYPath(link) {
		return link, nil
	}
	out, err := exec.CommandContext(ctx, "ps", "-o", "tty=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return "", fmt.Errorf("ps: %w", err)
	}
	tty := strings.TrimSpace(string(out))
	if tty == "" || strings.Trim(tty, "?") == "" || strings.ContainsAny(tty, " .") {
		return "", fmt.Errorf("process %d has no terminal", pid)
	}
	name := "/dev/" + tty
	if !isTTYPath(name) {
		return "", fmt.Errorf("process %d: not a terminal: %s", pid, name)
	}
	return name, nil
}

func isTTYPath(p string) bool {
	return strings.HasPrefix(p, "/dev/pts/") || strings.HasPrefix(p, "/dev/tty")
}
