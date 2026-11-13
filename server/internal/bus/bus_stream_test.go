package bus

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/nwverse/persona/server/internal/rpc"
)

// wsPair is a WebSocket connection's two ends, compressed with context takeover or not at all.
func wsPair(t testing.TB, compress bool) (client, server *websocket.Conn) {
	t.Helper()
	mode := websocket.CompressionDisabled
	if compress {
		mode = websocket.CompressionContextTakeover
	}
	got := make(chan *websocket.Conn, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: mode})
		if err != nil {
			return
		}
		got <- ws
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, _, err := websocket.Dial(ctx, srv.URL, &websocket.DialOptions{CompressionMode: mode})
	if err != nil {
		t.Fatal(err)
	}
	server = <-got
	t.Cleanup(func() {
		_ = client.CloseNow()
		_ = server.CloseNow()
	})
	return client, server
}

// readLine reads from c up to and including the first newline, size bytes at a time.
func readLine(c *Conn, size int) ([]byte, error) {
	var out []byte
	buf := make([]byte, size)
	for {
		n, err := c.Read(buf)
		out = append(out, buf[:n]...)
		if err != nil || bytes.IndexByte(out, '\n') >= 0 {
			return out, err
		}
	}
}

// Every text message reads as itself and a newline, whatever the reader's buffer; binary messages are skipped.
func TestConnReadsMessagesAsLines(t *testing.T) {
	ctx := context.Background()
	for _, compress := range []bool{false, true} {
		for _, size := range []int{1, 3, 4096} {
			t.Run(fmt.Sprintf("compress=%v/buf=%d", compress, size), func(t *testing.T) {
				cws, sws := wsPair(t, compress)
				c := NewConn(ctx, cws)
				big := strings.Repeat("x", 70000)
				sent := []struct {
					typ  websocket.MessageType
					text string
				}{{websocket.MessageText, "a"}, {websocket.MessageBinary, "skipped"}, {websocket.MessageText, ""},
					{websocket.MessageText, "two\nlines"}, {websocket.MessageText, big}, {websocket.MessageText, "end"}}
				go func() {
					for _, m := range sent {
						_ = sws.Write(ctx, m.typ, []byte(m.text))
					}
				}()
				var got []byte
				buf := make([]byte, size)
				want := "a\n\ntwo\nlines\n" + big + "\nend\n"
				for len(got) < len(want) {
					n, err := c.Read(buf)
					if err != nil {
						t.Fatal(err)
					}
					got = append(got, buf[:n]...)
				}
				if string(got) != want {
					t.Fatalf("read %d bytes, want %d", len(got), len(want))
				}
				go func() { _ = sws.Close(websocket.StatusNormalClosure, "") }()
				if n, err := c.Read(buf); n != 0 || err != io.EOF {
					t.Fatalf("after a normal closure: %d, %v", n, err)
				}
			})
		}
	}
}

// Around the read limit a streamed message ends exactly as a whole read (websocket.Conn.Read, what Conn did before)
// does: the same bytes, or the same error and no line.
func TestConnReadLimitIsTheLibrarys(t *testing.T) {
	ctx := context.Background()
	const limit = 1000
	for _, compress := range []bool{false, true} {
		for _, size := range []int{limit - 1, limit, limit + 1, limit + 2, 3 * limit} {
			for _, typ := range []websocket.MessageType{websocket.MessageText, websocket.MessageBinary} {
				msg := []byte(strings.Repeat("0123456789abcdef", size/16+1)[:size])
				whole, wsend := wsPair(t, compress)
				whole.SetReadLimit(limit)
				streamed, ssend := wsPair(t, compress)
				c := newConn(ctx, streamed, limit)
				for _, ws := range []*websocket.Conn{wsend, ssend} {
					go func() {
						_ = ws.Write(ctx, typ, msg)
						_ = ws.Write(ctx, websocket.MessageText, []byte("next"))
					}()
				}
				_, want, werr := whole.Read(ctx)
				if typ == websocket.MessageBinary && werr == nil { // skipped: the next message is the line
					_, want, werr = whole.Read(ctx)
				}
				got, err := readLine(c, 300)
				name := fmt.Sprintf("compress=%v size=%d type=%v", compress, size, typ)
				switch {
				case werr != nil:
					if err == nil || err.Error() != werr.Error() || bytes.IndexByte(got, '\n') >= 0 {
						t.Errorf("%s: got %d bytes, %v; a whole read: %v", name, len(got), err, werr)
					}
				case err != nil || string(got) != string(want)+"\n":
					t.Errorf("%s: got %d bytes, %v; a whole read: %d bytes", name, len(got), err, len(want))
				}
			}
		}
	}
}

// Write sends each complete line as one message, from however many writes it came in; blank lines are not sent.
func TestConnWriteSendsWholeLines(t *testing.T) {
	ctx := context.Background()
	cws, sws := wsPair(t, false)
	sws.SetReadLimit(MaxMessage)
	c := NewConn(ctx, cws)
	big := strings.Repeat("y", 3*keptLine)
	for _, p := range []string{"a\nb\n\n  \nc", "d\ne", "", "\n", big[:keptLine], big[keptLine:], "\nf\n"} {
		if n, err := c.Write([]byte(p)); n != len(p) || err != nil {
			t.Fatalf("Write(%d bytes) = %d, %v", len(p), n, err)
		}
	}
	for _, want := range []string{"a", "b", "cd", "e", big, "f"} {
		typ, got, err := sws.Read(ctx)
		if err != nil || typ != websocket.MessageText || string(got) != want {
			t.Fatalf("got %v %d bytes %v, want %d bytes", typ, len(got), err, len(want))
		}
	}
	if cap(c.wbuf) > keptLine {
		t.Errorf("Write kept %d bytes for the next line", cap(c.wbuf))
	}
	_ = cws.CloseNow()
	if n, err := c.Write([]byte("g\n")); n != 0 || err == nil {
		t.Fatalf("Write after the connection closed = %d, %v", n, err)
	}
}

// tap tells each read's bytes.
type tap struct {
	r     io.Reader
	reads chan []byte
}

func (t tap) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if n > 0 {
		select {
		case t.reads <- bytes.Clone(p[:n]):
		default:
		}
	}
	return n, err
}

// A connection that ends part-way through a message (its ctx canceled) delivers none of it: the rpc peer serving on
// it returns the connection's error, and the half message reaches no handler, even where it reads as JSON.
func TestConnEndsMidMessage(t *testing.T) {
	cws, sws := wsPair(t, false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := NewConn(ctx, cws)
	p := rpc.New("s", c)
	events := make(chan string, 4)
	p.OnEvent = func(event string, _ json.RawMessage) { events <- event }
	reads := make(chan []byte, 1024)
	served := make(chan error, 1)
	go func() { served <- p.Serve(context.Background(), tap{c, reads}) }()

	if err := sws.Write(ctx, websocket.MessageText, []byte(`{"event":"whole","params":{}}`)); err != nil {
		t.Fatal(err)
	}
	if e := <-events; e != "whole" {
		t.Fatalf("event %q", e)
	}
	w, err := sws.Writer(ctx, websocket.MessageText)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte(`{"event":"half","params":{}}` + strings.Repeat(" ", 64<<10))) // never finished
	for seen := false; !seen; {
		select {
		case b := <-reads:
			seen = bytes.Contains(b, []byte("half"))
		case <-time.After(5 * time.Second):
			t.Fatal("the started message was not read")
		}
	}
	cancel()
	select {
	case err := <-served:
		if err == nil || errors.Is(err, io.EOF) {
			t.Fatalf("Serve returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve went on after the connection ended")
	}
	select {
	case e := <-events:
		t.Fatalf("a half message reached the handler as %q", e)
	default:
	}
}

func liveHeap() uint64 {
	var m runtime.MemStats
	runtime.GC()
	runtime.GC()
	runtime.ReadMemStats(&m)
	return m.HeapAlloc
}

// After one 21 MB line each way, neither end keeps a buffer of its size: before, the reader kept the message it read
// whole (28 MB) and the writer its copy of the line.
func TestConnKeepsNoLargeMessage(t *testing.T) {
	ctx := context.Background()
	cws, sws := wsPair(t, false)
	c, s := NewConn(ctx, cws), NewConn(ctx, sws)
	before := liveHeap()
	line := bytes.Repeat([]byte("w"), 21<<20)
	line = append(line, '\n')
	for _, ends := range [][2]*Conn{{c, s}, {s, c}} {
		wrote := make(chan error, 1)
		go func() {
			_, err := ends[0].Write(line)
			wrote <- err
		}()
		got, err := readLine(ends[1], 64<<10)
		if err != nil || len(got) != len(line) {
			t.Fatalf("read %d bytes, %v", len(got), err)
		}
		if err := <-wrote; err != nil {
			t.Fatal(err)
		}
	}
	line = nil
	if grew := int64(liveHeap()) - int64(before); grew > 2<<20 {
		t.Errorf("live heap grew %d bytes after one 21 MB line each way, want at most 2 MiB", grew)
	}
	runtime.KeepAlive(c)
	runtime.KeepAlive(s)
}
