package node

import (
	"net"
	"testing"

	"github.com/tailscale/go-winio"
)

// listenFakeInbox listens on a named pipe shaped like Claude Code's (\\.\pipe\LOCAL\cc-msg-…).
func listenFakeInbox(t *testing.T) (net.Listener, string) {
	t.Helper()
	path := `\\.\pipe\LOCAL\cc-msg-test-` + fakeSocketName()
	ln, err := winio.ListenPipe(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	return ln, path
}

// goneInboxPath is an inbox path of the same shape where nothing listens.
func goneInboxPath(path string) string { return path + "-gone" }
