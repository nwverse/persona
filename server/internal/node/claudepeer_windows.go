package node

import (
	"context"
	"net"
	"time"

	"github.com/tailscale/go-winio"
)

// peerAuthRequired: a Windows inbox drops a connection whose first line is not a good auth line (Claude Code's
// requireAuth defaults to the platform being Windows), so no key file means no send.
const peerAuthRequired = true

// peerPathUsable: on Windows an inbox is a named pipe (\\.\pipe\LOCAL\cc-msg-<hex>); a unix socket path from another
// platform's claude (a shared config directory) is not dialed.
func peerPathUsable(socket string) bool { return peerPipeRe.MatchString(socket) }

// dialPeer connects to the inbox's named pipe.
func dialPeer(ctx context.Context, socket string) (net.Conn, error) {
	return winio.DialPipeContext(ctx, socket)
}

// peerFinish waits until the inbox read what was written: on a byte-mode pipe FlushFileBuffers returns once the other
// end has read everything (bounded by wait: a stuck reader must not hold the send). Then the end of the stream is
// signalled, so the inbox does not wait for more. A server that already closed its end (it read the line, then
// dropped the connection: a bad auth line) is not an error here; the transcript says whether the line counted.
func peerFinish(c net.Conn, wait time.Duration) error {
	done := make(chan struct{})
	go func() {
		if f, ok := c.(interface{ Flush() error }); ok {
			_ = f.Flush()
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(wait):
	}
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
	}
	return nil
}
