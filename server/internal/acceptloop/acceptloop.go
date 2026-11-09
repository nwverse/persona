// Package acceptloop serves a listener until it closes, riding out the transient Accept errors that would otherwise
// stop the loop or make it spin. It imports only the standard library, so the hub, the managers, the node and the
// extension code can all share it.
package acceptloop

import (
	"context"
	"errors"
	"net"
	"time"
)

// The pause after a failed Accept starts at backoffMin and doubles up to backoffMax, as net/http's Server.Serve does.
// Variables so tests can shrink them.
var (
	backoffMin = 5 * time.Millisecond
	backoffMax = time.Second
)

// Run hands each connection ln accepts to handle until ln is closed or ctx ends. Any other Accept error (EMFILE or
// ENFILE while file descriptors run out, ECONNABORTED) is transient: the loop waits and tries again. Returning on it
// would leave the socket in place with nobody accepting, which nothing watching the socket can see; retrying at once
// would spin while descriptors stay exhausted. One log line per run of failures; a success resets the wait.
// handle runs on the loop's goroutine: anything that takes long starts a goroutine of its own. ctx ending does not
// interrupt a blocked Accept: close ln for that.
func Run(ctx context.Context, ln net.Listener, logf func(format string, args ...any), handle func(net.Conn)) {
	var wait time.Duration
	for {
		c, err := ln.Accept()
		if err == nil {
			wait = 0
			handle(c)
			continue
		}
		if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
			return
		}
		if wait == 0 {
			wait = backoffMin
			logf("accept on %s: %v; retrying", ln.Addr(), err)
		} else {
			wait = min(2*wait, backoffMax)
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
	}
}
