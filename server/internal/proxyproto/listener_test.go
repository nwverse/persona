package proxyproto

import (
	"errors"
	"io"
	"net"
	"net/netip"
	"os"
	"sync"
	"testing"
	"time"
)

type reported struct {
	peer netip.AddrPort
	h    Header
	err  error
}

// listen is a Listener on loopback trusting trusted; Report goes to the returned channel.
func listen(t *testing.T, timeout time.Duration, trusted ...string) (*Listener, <-chan reported) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	reports := make(chan reported, 16)
	l := &Listener{Listener: ln, Timeout: timeout, Report: func(peer netip.AddrPort, h Header, err error) {
		reports <- reported{peer, h, err}
	}}
	for _, p := range trusted {
		l.Trusted = append(l.Trusted, netip.MustParsePrefix(p))
	}
	t.Cleanup(func() { ln.Close() })
	return l, reports
}

// dial connects to l, writes what, and returns the server's side of the connection.
func dial(t *testing.T, l net.Listener, what string) (client, server net.Conn) {
	t.Helper()
	client, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	if what != "" {
		if _, err := client.Write([]byte(what)); err != nil {
			t.Fatal(err)
		}
	}
	// Other processes on the machine may probe the port: take the connection that is this client's.
	for {
		server, err = l.Accept()
		if err != nil {
			t.Fatal(err)
		}
		if socketPeer(server).String() == client.LocalAddr().String() {
			break
		}
		server.Close()
	}
	t.Cleanup(func() { server.Close() })
	return client, server
}

// socketPeer is the socket's own remote address, without reading a header.
func socketPeer(c net.Conn) net.Addr {
	if w, ok := c.(*Conn); ok {
		return w.Conn.RemoteAddr()
	}
	return c.RemoteAddr()
}

func readN(t *testing.T, c net.Conn, n int) string {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	b := make([]byte, n)
	if _, err := io.ReadFull(c, b); err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Time{})
	return string(b)
}

func TestListenerTrustedUpstream(t *testing.T) {
	l, reports := listen(t, 0, "127.0.0.1/32")
	for _, tc := range []struct {
		name, header, remote string
	}{
		{"v1 IPv4", "PROXY TCP4 203.0.113.7 192.0.2.10 51234 443\r\n", "203.0.113.7:51234"},
		{"v1 IPv6", "PROXY TCP6 2001:db8::7 2001:db8::10 51234 443\r\n", "[2001:db8::7]:51234"},
		{"v2 IPv6", string(v2(1, 0x21, v2inet(netip.MustParseAddrPort("[2001:db8::7]:5"), netip.MustParseAddrPort("[2001:db8::10]:443")))), "[2001:db8::7]:5"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The header and the client's first bytes in one segment: what was read past the header is not lost.
			client, server := dial(t, l, tc.header+"hello")
			if got := server.RemoteAddr().String(); got != tc.remote {
				t.Fatalf("RemoteAddr = %s, want %s", got, tc.remote)
			}
			if got := server.LocalAddr().String(); got != l.Addr().String() {
				t.Fatalf("LocalAddr = %s, want the socket's %s", got, l.Addr())
			}
			if got := readN(t, server, 5); got != "hello" {
				t.Fatalf("first bytes = %q", got)
			}
			_, _ = client.Write([]byte("world"))
			if got := readN(t, server, 5); got != "world" {
				t.Fatalf("later bytes = %q", got)
			}
			if _, err := server.Write([]byte("ok")); err != nil {
				t.Fatal(err)
			}
			if got := readN(t, client, 2); got != "ok" {
				t.Fatalf("reply = %q", got)
			}
			r := <-reports
			if r.err != nil || r.h.Source.String() != tc.remote || r.peer.Addr() != netip.MustParseAddr("127.0.0.1") {
				t.Fatalf("report = %+v", r)
			}
		})
	}
}

func TestListenerLocalKeepsTheSocketAddress(t *testing.T) {
	l, _ := listen(t, 0, "127.0.0.0/8")
	client, server := dial(t, l, string(v2(0, 0x00, nil))+"x")
	if got, want := server.RemoteAddr().String(), client.LocalAddr().String(); got != want {
		t.Fatalf("RemoteAddr = %s, want the socket's %s", got, want)
	}
	if got := readN(t, server, 1); got != "x" {
		t.Fatalf("first byte = %q", got)
	}
}

func TestListenerRejectLocal(t *testing.T) {
	l, reports := listen(t, 0, "127.0.0.1/32")
	l.RejectLocal = true
	for _, tc := range []struct{ name, header string }{
		{"v1 IPv4", "PROXY TCP4 127.0.0.2 192.0.2.1 1 443\r\n"},
		{"v1 IPv6", "PROXY TCP6 ::1 2001:db8::1 1 443\r\n"},
		{"v2 mapped IPv4", string(AppendV2(nil, netip.MustParseAddrPort("[::ffff:127.0.0.1]:1"), netip.MustParseAddrPort("[2001:db8::1]:443")))},
		{"v2 LOCAL", string(v2(0, 0x00, nil))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, server := dial(t, l, tc.header+"client bytes")
			if n, err := server.Read(make([]byte, 1)); n != 0 || !errors.Is(err, ErrLocalAddress) {
				t.Fatalf("Read = %d, %v; want refusal before delivering client bytes", n, err)
			}
			if r := <-reports; !errors.Is(r.err, ErrLocalAddress) {
				t.Fatalf("report = %+v", r)
			}
			_ = client.SetReadDeadline(time.Now().Add(time.Second))
			if n, err := client.Read(make([]byte, 1)); n != 0 || err == nil || errors.Is(err, os.ErrDeadlineExceeded) {
				t.Fatalf("client read = %d, %v; want the connection closed", n, err)
			}
		})
	}
}

func TestListenerRefusesTrustedWithoutHeader(t *testing.T) {
	l, reports := listen(t, 0, "127.0.0.1/32")
	for _, tc := range []struct {
		name, first string
		want        error
	}{
		{"TLS straight in", "\x16\x03\x01\x00\x05hello", ErrNoHeader},
		{"HTTP straight in", "GET / HTTP/1.1\r\nHost: x\r\n\r\n", ErrNoHeader},
		{"v1 UNKNOWN", "PROXY UNKNOWN\r\nGET / HTTP/1.1\r\n\r\n", ErrNoAddress},
		{"v2 UNSPEC", string(v2(1, 0x00, nil)) + "GET /", ErrNoAddress},
		{"malformed", "PROXY TCP4 203.0.113.7\r\n", ErrMalformed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, server := dial(t, l, tc.first)
			if _, err := server.Read(make([]byte, 8)); !errors.Is(err, tc.want) {
				t.Fatalf("Read: %v, want %v", err, tc.want)
			}
			if _, err := server.Write([]byte("HTTP/1.0 400 Bad Request\r\n\r\n")); err == nil {
				t.Fatal("a refused connection was written to")
			}
			// The upstream sees the connection closed, with nothing written back.
			_ = client.SetReadDeadline(time.Now().Add(5 * time.Second))
			if n, err := client.Read(make([]byte, 64)); n != 0 || err == nil {
				t.Fatalf("client read %d bytes, %v", n, err)
			}
			if r := <-reports; !errors.Is(r.err, tc.want) {
				t.Fatalf("report = %+v", r)
			}
		})
	}
}

func TestListenerIgnoresHeaderFromUntrusted(t *testing.T) {
	l, reports := listen(t, 0, "192.0.2.0/24") // the test's loopback peers are not in it
	header := "PROXY TCP4 127.0.0.1 127.0.0.1 1 2\r\n"
	client, server := dial(t, l, header)
	if _, ok := server.(*Conn); ok {
		t.Fatal("an untrusted connection was wrapped")
	}
	if got, want := server.RemoteAddr().String(), client.LocalAddr().String(); got != want {
		t.Fatalf("RemoteAddr = %s, want the socket's %s", got, want)
	}
	// The header is just the connection's first bytes, for the server to reject.
	if got := readN(t, server, len(header)); got != header {
		t.Fatalf("first bytes = %q", got)
	}
	select {
	case r := <-reports:
		t.Fatalf("untrusted connection reported: %+v", r)
	default:
	}
}

func TestListenerNoTrustedIsPassThrough(t *testing.T) {
	l, _ := listen(t, 0)
	_, server := dial(t, l, "GET / HTTP/1.1\r\n")
	if _, ok := server.(*Conn); ok {
		t.Fatal("wrapped without any trusted upstream")
	}
}

// A silent upstream runs into the header deadline in its own connection; meanwhile Accept goes on and others are served.
func TestListenerHeaderTimeoutBlocksNoOne(t *testing.T) {
	l, reports := listen(t, 200*time.Millisecond, "127.0.0.1/32")
	_, silent := dial(t, l, "")
	done := make(chan error, 1)
	go func() {
		_, err := silent.Read(make([]byte, 1))
		done <- err
	}()
	start := time.Now()
	_, other := dial(t, l, "PROXY TCP4 203.0.113.7 192.0.2.10 1 443\r\n")
	if got := other.RemoteAddr().String(); got != "203.0.113.7:1" {
		t.Fatalf("RemoteAddr = %s", got)
	}
	if time.Since(start) > 150*time.Millisecond {
		t.Fatal("the second connection waited for the first one's header")
	}
	select {
	case err := <-done:
		if !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatalf("silent upstream: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the header deadline did not fire")
	}
	for range 2 {
		r := <-reports
		if r.err != nil && !errors.Is(r.err, os.ErrDeadlineExceeded) {
			t.Fatalf("report = %+v", r)
		}
	}
}

func TestListenerDeadlines(t *testing.T) {
	l, _ := listen(t, 100*time.Millisecond, "127.0.0.1/32")
	header := "PROXY TCP4 203.0.113.7 192.0.2.10 1 443\r\n"

	// The header's own deadline is gone once it is in: a client may take longer than it for its first bytes.
	client, server := dial(t, l, header)
	if server.RemoteAddr().String() != "203.0.113.7:1" {
		t.Fatal("header not read")
	}
	time.AfterFunc(300*time.Millisecond, func() { _, _ = client.Write([]byte("late")) })
	b := make([]byte, 4)
	if _, err := io.ReadFull(server, b); err != nil || string(b) != "late" {
		t.Fatalf("read after the header deadline: %q, %v", b, err)
	}

	// A deadline the server set before the header was read still holds after it.
	client, server = dial(t, l, header)
	_ = server.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
	start := time.Now()
	if _, err := server.Read(make([]byte, 1)); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("Read: %v", err)
	}
	if took := time.Since(start); took < 200*time.Millisecond {
		t.Fatalf("the server's deadline was cut to the header's (%s)", took)
	}
	_ = client
}

// pipeConn is one end of a net.Pipe that claims TCP addresses.
type pipeConn struct {
	net.Conn
	local, remote net.Addr
}

func (c pipeConn) LocalAddr() net.Addr  { return c.local }
func (c pipeConn) RemoteAddr() net.Addr { return c.remote }

type oneConnListener struct {
	c    net.Conn
	once sync.Once
}

func (l *oneConnListener) Accept() (net.Conn, error) {
	var c net.Conn
	l.once.Do(func() { c = l.c })
	if c == nil {
		return nil, net.ErrClosed
	}
	return c, nil
}
func (l *oneConnListener) Close() error   { return nil }
func (l *oneConnListener) Addr() net.Addr { return l.c.LocalAddr() }

// An upstream on another machine passing on one of its own loopback clients does not hand out this machine's loopback.
func TestListenerRemoteUpstreamLoopbackClient(t *testing.T) {
	for _, tc := range []struct{ header, want string }{
		{"PROXY TCP4 127.0.0.1 127.0.0.1 1 443\r\n", "192.0.2.10:40000"},
		{"PROXY TCP6 ::1 ::1 1 443\r\n", "192.0.2.10:40000"},
		{"PROXY TCP6 ::ffff:127.0.0.1 ::1 1 443\r\n", "192.0.2.10:40000"},
		{"PROXY TCP4 203.0.113.7 192.0.2.10 1 443\r\n", "203.0.113.7:1"},
	} {
		a, b := net.Pipe()
		conn := pipeConn{Conn: a, local: &net.TCPAddr{IP: net.ParseIP("192.0.2.1"), Port: 443}, remote: &net.TCPAddr{IP: net.ParseIP("192.0.2.10"), Port: 40000}}
		l := &Listener{Listener: &oneConnListener{c: conn}, Trusted: []netip.Prefix{netip.MustParsePrefix("192.0.2.10/32")}}
		server, err := l.Accept()
		if err != nil {
			t.Fatal(err)
		}
		go func() { _, _ = b.Write([]byte(tc.header)) }()
		if got := server.RemoteAddr().String(); got != tc.want {
			t.Errorf("%q: RemoteAddr = %s, want %s", tc.header, got, tc.want)
		}
		a.Close()
		b.Close()
	}
}
