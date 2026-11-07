package node

import (
	"context"
	"errors"
	"net"
	"time"
)

// The pause after a failed Accept starts at acceptBackoffMin and doubles up to acceptBackoffMax, as net/http's
// Server.Serve does. Variables so tests can shrink them.
var (
	acceptBackoffMin = 5 * time.Millisecond
	acceptBackoffMax = time.Second
)

// acceptLoop hands each connection ln accepts to handle until ln is closed or ctx ends. Any other Accept error (EMFILE
// or ENFILE while file descriptors run out, ECONNABORTED) is transient: the loop waits and tries again. Returning on it
// would leave the socket file in place with nobody accepting, which the socket watchdogs cannot see; retrying at once
// would spin while descriptors stay exhausted. One log line per run of failures; a success resets the wait.
// handle runs on the loop's goroutine: anything that takes long starts a goroutine of its own.
func acceptLoop(ctx context.Context, ln net.Listener, logf func(format string, args ...any), handle func(net.Conn)) {
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
			wait = acceptBackoffMin
			logf("accept on %s: %v; retrying", ln.Addr(), err)
		} else {
			wait = min(2*wait, acceptBackoffMax)
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
