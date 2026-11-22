package hub

import (
	"errors"
	"io"
	"net/http"
	"sync"
	"time"
)

// The hub's servers have no ReadTimeout (newHubHTTPServer: it would cut WebSockets and long uploads), so once a
// request's headers were in, nothing bounded its body. A client that stopped sending mid-body held its handler, or
// net/http's discard of what the handler left unread, and the connection forever, before any auth: even a 401 was
// never sent.
//
// bodyIdle is how long a read of a request body may wait for the client. bodyDrain bounds that discard, which HTTP/1
// runs (up to 256 KB) right before the response's head goes out and again after the handler. Variables for the tests.
var (
	bodyIdle  = 2 * time.Minute
	bodyDrain = 5 * time.Second
)

// idleBodies bounds the waits for a request body, not the request: the read deadline runs only while a read waits
// for the client, so an upload that keeps moving however slowly, and a handler that pauses between reads (a node
// stream's window, long work after reading its JSON), are never cut. Upgrades and CONNECT tunnels are streams, not
// bodies. It is the outermost layer of both servers (serveHTTP).
func idleBodies(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body == nil || r.Body == http.NoBody || r.ContentLength == 0 || r.Method == http.MethodConnect ||
			r.Header.Get("Upgrade") != "" {
			next.ServeHTTP(w, r)
			return
		}
		b := &idleBody{ReadCloser: r.Body, rc: http.NewResponseController(w), idle: bodyIdle}
		r.Body = b
		defer b.handlerDone()
		if r.ProtoMajor == 1 { // HTTP/2 discards nothing: it resets the stream instead
			b.drain = bodyDrain
			w = &idleWriter{ResponseWriter: w, body: b}
		}
		next.ServeHTTP(w, r)
	})
}

// idleBody sets the connection's (HTTP/2: the stream's) read deadline around each read of the body. Errors setting it
// are ignored: a writer without deadlines (http.ErrNotSupported) keeps net/http's behaviour.
type idleBody struct {
	io.ReadCloser
	rc          *http.ResponseController
	idle, drain time.Duration // drain 0: no discard to bound (HTTP/2)

	mu sync.Mutex // a reverse proxy's transport reads the body on a goroutine of its own
	// over: the body ended, was closed or failed, or its handler returned, and the deadline is net/http's again. At
	// the end net/http watches the connection with no deadline; one set after that would cancel the context of the
	// next request on it. An HTTP/2 writer must not be used once its handler returned.
	over bool
	// armed: a write since the last read set the discard's deadline. Once only: the write that sent the head may have
	// had net/http discard the body to its end (see over).
	armed bool
}

func (b *idleBody) Read(p []byte) (int, error) {
	b.extend(b.idle, false)
	n, err := b.ReadCloser.Read(p)
	// At the end, or once net/http's discard closed the body, it is over. A read that failed (its deadline passed)
	// leaves that deadline, so the discard after the handler gives up at once too.
	ended := errors.Is(err, io.EOF) || errors.Is(err, http.ErrBodyReadAfterClose)
	b.settle(err != nil, err == nil || ended)
	return n, err
}

// Close: on HTTP/1 net/http's Close discards what is left, to use the connection again.
func (b *idleBody) Close() error {
	b.extend(b.drain, false)
	err := b.ReadCloser.Close()
	b.settle(true, true)
	return err
}

// extend sets the read deadline d ahead, unless the body is over; a write does so once until the next read.
func (b *idleBody) extend(d time.Duration, write bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.over || d == 0 || write && b.armed {
		return
	}
	b.armed = write
	_ = b.rc.SetReadDeadline(time.Now().Add(d))
}

// settle after a read: the deadline stops (clear) unless a write meanwhile set the discard's. An HTTP/2 stream's
// deadline fires even while no read waits, and would cut a handler that pauses between reads.
func (b *idleBody) settle(over, clear bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if clear && !b.over && !b.armed {
		_ = b.rc.SetReadDeadline(time.Time{})
	}
	b.over = b.over || over
}

// handlerDone: what the handler left unread is discarded before the connection's next request, under bodyDrain
// unless a write since the last read set it already.
func (b *idleBody) handlerDone() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.over && !b.armed && b.drain > 0 {
		_ = b.rc.SetReadDeadline(time.Now().Add(b.drain))
	}
	b.over = true
}

// idleWriter arms the discard's deadline before a write or flush, either of which may send the head.
type idleWriter struct {
	http.ResponseWriter
	body *idleBody
}

func (w *idleWriter) Write(p []byte) (int, error) {
	w.body.extend(w.body.drain, true)
	return w.ResponseWriter.Write(p)
}

func (w *idleWriter) FlushError() error {
	w.body.extend(w.body.drain, true)
	return http.NewResponseController(w.ResponseWriter).Flush()
}

// Unwrap lets http.ResponseController reach the connection beneath: deadlines and Hijack.
func (w *idleWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
