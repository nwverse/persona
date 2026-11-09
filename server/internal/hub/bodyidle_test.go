package hub

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/coder/websocket"
)

// withBodyLimits sets bodyIdle and bodyDrain for one test.
func withBodyLimits(t *testing.T, idle, drain time.Duration) {
	t.Helper()
	oldIdle, oldDrain := bodyIdle, bodyDrain
	bodyIdle, bodyDrain = idle, drain
	t.Cleanup(func() { bodyIdle, bodyDrain = oldIdle, oldDrain })
}

// memListener hands a server the far ends of net.Pipe connections. Inside a synctest bubble the deadlines of both
// sides then run on the bubble's clock, so the minutes a stalled or slow client takes pass at once.
type memListener struct {
	conns chan net.Conn
	done  chan struct{}
	once  sync.Once
}

func (l *memListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *memListener) Close() error   { l.once.Do(func() { close(l.done) }); return nil }
func (l *memListener) Addr() net.Addr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)} }

// pipe opens a connection to the server; the test's end closes when the test ends.
func (l *memListener) pipe(t *testing.T) net.Conn {
	near, far := net.Pipe()
	l.conns <- far
	t.Cleanup(func() { _ = near.Close() })
	return near
}

func (l *memListener) dial(t *testing.T) *rawConn {
	c := l.pipe(t)
	return &rawConn{Conn: c, br: bufio.NewReader(c)}
}

// bubbleServer serves handler as serveHTTP does (newHubHTTPServer, idleBodies outermost) inside the calling synctest
// bubble. h2c adds HTTP/2 without TLS, which runs the same stream code as HTTP/2 on the TLS listener.
func bubbleServer(t *testing.T, h2c bool, handler http.Handler) *memListener {
	l := &memListener{conns: make(chan net.Conn), done: make(chan struct{})}
	srv := newHubHTTPServer("", idleBodies(handler))
	if h2c {
		srv.Protocols = new(http.Protocols)
		srv.Protocols.SetHTTP1(true)
		srv.Protocols.SetUnencryptedHTTP2(true)
	}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) }) // after the clients' ends close (cleanups run last first)
	return l
}

// rawConn writes requests by hand, so a test decides when each byte of a body goes.
type rawConn struct {
	net.Conn
	br *bufio.Reader
}

// send writes the parts in order on a goroutine of its own; a time.Duration part waits that long first.
func (c *rawConn) send(parts ...any) {
	go func() {
		for _, p := range parts {
			switch p := p.(type) {
			case time.Duration:
				<-time.After(p)
			case string:
				if _, err := io.WriteString(c.Conn, p); err != nil {
					return
				}
			}
		}
	}()
}

// response reads the next response, which must come within d.
func (c *rawConn) response(t *testing.T, d time.Duration) (*http.Response, string) {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(d))
	resp, err := http.ReadResponse(c.br, nil)
	if err != nil {
		t.Fatalf("no response within %s: %v", d, err)
	}
	b, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("response body: %v", err)
	}
	return resp, string(b)
}

// closedWithin: the server closed the connection within d.
func (c *rawConn) closedWithin(d time.Duration) error {
	_ = c.SetReadDeadline(time.Now().Add(d))
	if _, err := c.br.ReadByte(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("the connection is still open after %s: %v", d, err)
	}
	return nil
}

// stalledPost sends 2 of its 100 body bytes, then nothing.
const stalledPost = "POST /api/v1/x HTTP/1.1\r\nHost: hub\r\nContent-Length: 100\r\n\r\nab"

// A client that stops sending its body gets its answer and loses the connection, however the handler answers: net/http
// discards the body right before the head goes out, after the handler or within its write.
func TestStalledBodyIsLetGo(t *testing.T) {
	for _, c := range []struct {
		name    string
		handler http.HandlerFunc
		want    int
	}{
		{"refused without a word", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) }, http.StatusUnauthorized},
		{"refused", func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "unauthorized", http.StatusUnauthorized) }, http.StatusUnauthorized},
		{"a long answer first", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, strings.Repeat("x", 64<<10)) }, http.StatusOK},
		{"waits for the body", func(w http.ResponseWriter, r *http.Request) {
			if _, err := io.ReadAll(r.Body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
			}
		}, http.StatusBadRequest},
	} {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				withBodyLimits(t, time.Minute, 5*time.Second)
				conn := bubbleServer(t, false, c.handler).dial(t)
				conn.send(stalledPost)
				if resp, _ := conn.response(t, bodyIdle+bodyDrain+time.Second); resp.StatusCode != c.want {
					t.Fatalf("status %d, want %d", resp.StatusCode, c.want)
				}
				if err := conn.closedWithin(time.Second); err != nil {
					t.Fatal(err)
				}
			})
		})
	}
}

// Bodies that keep coming are never cut, however long the request takes, and the connection is used again after each.
func TestBodiesThatKeepComingAreNotCut(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		withBodyLimits(t, time.Minute, 5*time.Second)
		work := func(r *http.Request) bool { // long work in the handler, far past both limits
			select {
			case <-time.After(10 * bodyIdle):
				return true
			case <-r.Context().Done():
				return false
			}
		}
		l := bubbleServer(t, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/echo":
				b, err := io.ReadAll(r.Body)
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				_, _ = w.Write(b)
			case "/json":
				var v struct{ A int }
				if err := json.NewDecoder(r.Body).Decode(&v); err != nil || !work(r) {
					http.Error(w, fmt.Sprint(err), http.StatusBadRequest)
					return
				}
				fmt.Fprintf(w, "done %d", v.A)
			case "/unread":
				if !work(r) {
					http.Error(w, "cancelled", http.StatusBadRequest)
					return
				}
				_, _ = io.WriteString(w, "done")
			case "/stream": // the head and a first part at once, the rest over minutes; the body is never read
				_, _ = io.WriteString(w, strings.Repeat("x", 8<<10))
				_ = http.NewResponseController(w).Flush()
				for range 5 {
					select {
					case <-time.After(bodyIdle):
						_, _ = io.WriteString(w, "y")
						_ = http.NewResponseController(w).Flush()
					case <-r.Context().Done():
						_, _ = io.WriteString(w, "cancelled")
						return
					}
				}
			case "/ctx":
				fmt.Fprint(w, r.Context().Err())
			}
		}))
		c := l.dial(t)
		slow := []any{"POST /echo HTTP/1.1\r\nHost: hub\r\nContent-Length: 10\r\n\r\n"}
		for range 10 { // gaps under bodyIdle, 5 bodyIdles in all
			slow = append(slow, bodyIdle/2, "x")
		}
		for _, step := range []struct {
			name string
			send []any
			want string
		}{
			{"slow upload", slow, strings.Repeat("x", 10)},
			{"JSON, then long work", []any{"POST /json HTTP/1.1\r\nHost: hub\r\nContent-Length: 7\r\n\r\n{\"a\":1}"}, "done 1"},
			{"chunked JSON, then long work", []any{"POST /json HTTP/1.1\r\nHost: hub\r\nTransfer-Encoding: chunked\r\n\r\n7\r\n{\"a\":2}\r\n", "0\r\n\r\n"}, "done 2"},
			{"unread body, long work", []any{"POST /unread HTTP/1.1\r\nHost: hub\r\nContent-Length: 5\r\n\r\nhello"}, "done"},
			{"unread body, long stream", []any{"POST /stream HTTP/1.1\r\nHost: hub\r\nContent-Length: 5\r\n\r\nhello"}, strings.Repeat("x", 8<<10) + "yyyyy"},
			// No deadline left behind cancels the connection's next request.
			{"next request", []any{"GET /ctx HTTP/1.1\r\nHost: hub\r\n\r\n"}, "<nil>"},
		} {
			c.send(step.send...)
			resp, body := c.response(t, time.Hour)
			if resp.StatusCode != http.StatusOK || body != step.want || resp.Close {
				t.Fatalf("%s: %d, %d bytes ending %q (close %v), want 200 and %d bytes ending %q on a kept connection", step.name,
					resp.StatusCode, len(body), body[max(0, len(body)-12):], resp.Close, len(step.want), step.want[max(0, len(step.want)-12):])
			}
		}
	})
}

// A wrapper that flushes through http.Flusher (codeWriter in front of a ReverseProxy) still gets a streamed answer to
// a request with a body out at once.
func TestAWrappedFlushGoesOut(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		withBodyLimits(t, time.Minute, 5*time.Second)
		release := make(chan struct{})
		defer close(release)
		conn := bubbleServer(t, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cw := &codeWriter{ResponseWriter: w}
			_, _ = io.WriteString(cw, "first")
			_ = http.NewResponseController(cw).Flush()
			select {
			case <-release:
			case <-r.Context().Done():
			}
		})).dial(t)
		conn.send("POST /x HTTP/1.1\r\nHost: hub\r\nContent-Length: 5\r\n\r\nhello")
		_ = conn.SetReadDeadline(time.Now().Add(time.Minute))
		resp, err := http.ReadResponse(conn.br, nil)
		if err != nil {
			t.Fatalf("no head within a minute: %v", err)
		}
		first := make([]byte, 5)
		if _, err := io.ReadFull(resp.Body, first); err != nil || string(first) != "first" {
			t.Fatalf("the flushed part: %q, %v", first, err)
		}
	})
}

// HTTP/2 bounds each stream's body by its own deadline, which runs even while no read waits: it must stop between
// reads, or a handler that works between two reads would be cut. A read after the handler returned (a proxy's
// transport may still hold the body) must not touch the stream's writer, which is gone by then.
func TestHTTP2StalledStreamIsLetGo(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		withBodyLimits(t, time.Minute, 5*time.Second)
		resume, late := make(chan struct{}), make(chan error, 1)
		l := bubbleServer(t, true, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/late" {
				go func() {
					<-time.After(time.Second)
					_, err := r.Body.Read(make([]byte, 1))
					late <- err
				}()
				_, _ = io.WriteString(w, r.Proto)
				return
			}
			_, err := io.ReadFull(r.Body, make([]byte, 2))
			if err == nil && r.URL.Path == "/pause" {
				<-time.After(10 * bodyIdle)
				close(resume)
			}
			if err == nil {
				_, err = io.ReadAll(r.Body)
			}
			switch {
			case errors.Is(err, os.ErrDeadlineExceeded):
				w.WriteHeader(http.StatusRequestTimeout)
			case err != nil:
				w.WriteHeader(http.StatusBadRequest)
			}
			_, _ = io.WriteString(w, r.Proto)
		}))
		tr := &http.Transport{Protocols: new(http.Protocols),
			DialContext: func(context.Context, string, string) (net.Conn, error) { return l.pipe(t), nil }}
		tr.Protocols.SetUnencryptedHTTP2(true)
		t.Cleanup(tr.CloseIdleConnections)
		client := &http.Client{Transport: tr, Timeout: time.Hour}
		post := func(path string, rest func(*io.PipeWriter)) (int, string) {
			pr, pw := io.Pipe()
			defer pw.Close()
			go func() {
				if _, err := pw.Write([]byte("ab")); err == nil {
					rest(pw)
				}
			}()
			resp, err := client.Post("http://hub"+path, "text/plain", pr)
			if err != nil {
				t.Fatalf("%s: %v", path, err)
			}
			defer resp.Body.Close()
			b, _ := io.ReadAll(resp.Body)
			return resp.StatusCode, string(b)
		}
		if code, proto := post("/stall", func(*io.PipeWriter) {}); code != http.StatusRequestTimeout || proto != "HTTP/2.0" {
			t.Fatalf("stalled stream: %d %s, want 408 over HTTP/2.0", code, proto)
		}
		code, proto := post("/pause", func(pw *io.PipeWriter) {
			<-resume
			_, _ = pw.Write([]byte("cd"))
			_ = pw.Close()
		})
		if code != http.StatusOK || proto != "HTTP/2.0" {
			t.Fatalf("a handler working between reads: %d %s, want 200 over HTTP/2.0", code, proto)
		}
		if code, proto := post("/late", func(*io.PipeWriter) {}); code != http.StatusOK || proto != "HTTP/2.0" {
			t.Fatalf("late reader: %d %s, want 200 over HTTP/2.0", code, proto)
		}
		if err := <-late; err == nil {
			t.Fatal("a read after the handler returned got the body")
		}
	})
}

// Both of the hub's servers have it, on the real routes: a stalled body that the auth layer refuses gets its 401 and
// loses the connection, while a WebSocket opened before stays up.
func TestPublicServersLetAStalledBodyGo(t *testing.T) {
	withBodyLimits(t, 300*time.Millisecond, 300*time.Millisecond)
	h := newPublicTestHub(t)
	h.cfg.Public.TLSListen = "127.0.0.1:0"
	free, err := net.Listen("tcp", "127.0.0.1:0") // serveHTTP does not hand the plaintext port back
	if err != nil {
		t.Fatal(err)
	}
	h.cfg.Listen = free.Addr().String()
	_ = free.Close()
	logs := &syncBuffer{}
	h.logger = log.New(logs, "", 0)
	runPublicTestHub(t, h)
	if !logs.WaitFor("listening on "+h.cfg.Listen, 10*time.Second) {
		t.Fatalf("the plaintext server did not start:\n%s", logs)
	}
	port, pin := h.tlsPin()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, "ws://"+h.cfg.Listen+"/api/v1/ws?token=test-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	go func() { // reads what the hub sends, and the pong
		for {
			if _, _, err := ws.Read(ctx); err != nil {
				return
			}
		}
	}()

	tlsConfig := pinnedTestClient(pin).Transport.(*http.Transport).TLSClientConfig.Clone()
	tlsConfig.NextProtos = []string{"http/1.1"}
	for name, dial := range map[string]func() (net.Conn, error){
		"plain": func() (net.Conn, error) { return net.Dial("tcp", h.cfg.Listen) },
		"TLS": func() (net.Conn, error) {
			return tls.Dial("tcp", net.JoinHostPort("127.0.0.1", fmt.Sprint(port)), tlsConfig)
		},
	} {
		conn, err := dial()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		c := &rawConn{Conn: conn, br: bufio.NewReader(conn)}
		c.send(stalledPost)
		resp, _ := c.response(t, 10*time.Second)
		err = c.closedWithin(10 * time.Second)
		_ = conn.Close()
		if resp.StatusCode != http.StatusUnauthorized || err != nil {
			t.Fatalf("%s: status %d, want 401; %v", name, resp.StatusCode, err)
		}
	}
	if err := ws.Ping(ctx); err != nil {
		t.Fatalf("the WebSocket did not outlive the stalled bodies: %v", err)
	}
}
