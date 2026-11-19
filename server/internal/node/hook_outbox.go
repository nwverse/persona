package node

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/rpc"
)

// A resident node's hook events go out in order without holding their hooks (#10663, #10829's review). While the
// transport under its connection is lost nothing is written until the manager resumes it, and an agent waits on every
// hook: none may wait that window out. An event the connection never wrote, because it ended without being resumed,
// goes to the spool as standby would have kept it, and the next connection's Node replays it (spool_replay.go).

// hookEventWait bounds how long a hook waits for its event to be written: a working connection writes it at once, so
// what the hook does next (a card it asks the phone for) reaches the manager after it, as when the hook wrote it itself.
var hookEventWait = time.Second

// hookOutbox is a Node's queue of hook events and its one sender, running while there is something to send.
type hookOutbox struct {
	mu      sync.Mutex
	queue   []*hookOut
	sending chan struct{} // closed when the sender returns; nil while none runs
}

// hookOut is one hook event: what the node tells the manager, and what the spool keeps if the connection never sends it.
type hookOut struct {
	req    sockRequest // as the hook sent it (sentRequest), what standby keeps
	in     hookInput
	at     time.Time
	params map[string]any
	sent   chan struct{} // closed once it was written, kept or dropped
}

// add queues e behind the events before it, starting the sender if none runs.
func (o *hookOutbox) add(n *Node, e *hookOut) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.queue = append(o.queue, e)
	if o.sending == nil {
		o.sending = make(chan struct{})
		go o.send(n, n.peer, o.sending)
	}
}

// send writes the queued events one after the other, waiting through a lost transport for the manager to resume it.
func (o *hookOutbox) send(n *Node, peer *rpc.Peer, done chan struct{}) {
	defer close(done)
	for {
		o.mu.Lock()
		if len(o.queue) == 0 {
			o.sending = nil
			o.mu.Unlock()
			return
		}
		e := o.queue[0]
		o.queue = o.queue[1:]
		o.mu.Unlock()
		if err := peer.Notify("hook.event", e.params); errors.Is(err, rpc.ErrNotSent) {
			n.keepHookEvent(e) // the connection ended with it unwritten (the window ran out, or it was replaced)
		}
		close(e.sent)
	}
}

// wait waits, at most d, for the sender to be done: once the connection ended, so that what it could not send is in
// the spool before the next connection's Node replays it.
func (o *hookOutbox) wait(d time.Duration) {
	o.mu.Lock()
	done := o.sending
	o.mu.Unlock()
	if done == nil {
		return
	}
	select {
	case <-done:
	case <-time.After(d):
	}
}

// keepHookEvent puts an event the connection did not send in the spool, as standby keeps one (spool_standby.go).
func (n *Node) keepHookEvent(e *hookOut) {
	if n.spool == nil || !spooled(e.in, e.req) {
		return
	}
	if dropped, err := n.spool.add(spoolEntry{ID: newUUID(), At: e.at.UTC(), Kind: "hook", Request: e.req}); err != nil {
		n.logger.Printf("spool: keep a hook event: %v", err)
	} else if dropped > 0 {
		n.logger.Printf("spool: full, dropped the %d oldest", dropped)
	}
}

// sentRequestKey carries a hook's request as it came on node.sock, before serveSock resolved its session and pane: what
// the spool keeps, which the replay resolves again (replayHook).
type sentRequestKey struct{}

func withSentRequest(ctx context.Context, req sockRequest) context.Context {
	return context.WithValue(ctx, sentRequestKey{}, req)
}

// sentRequest is the hook's request as it came (withSentRequest), else req.
func sentRequest(ctx context.Context, req sockRequest) sockRequest {
	if sent, ok := ctx.Value(sentRequestKey{}).(sockRequest); ok {
		return sent
	}
	return req
}
