//go:build !windows

package node

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// listenFakeInbox listens on a unix socket shaped like Claude Code's (<dir>/cc-socks/<pid>.sock), in a short directory
// (an AF_UNIX path stays under ~104 bytes; t.TempDir can be longer on macOS).
func listenFakeInbox(t *testing.T) (net.Listener, string) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "ccs")
	if err != nil {
		dir = t.TempDir()
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, fakeSocketName()+".sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	return ln, path
}

// goneInboxPath is an inbox path of the same shape where nothing listens.
func goneInboxPath(path string) string { return strings.TrimSuffix(path, ".sock") + "-gone.sock" }
