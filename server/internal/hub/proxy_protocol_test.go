package hub

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proxyproto"
)

func TestProxyProtocolPolicy(t *testing.T) {
	if p, err := (*ProxyProtocolConfig)(nil).policy(); p != nil || err != nil {
		t.Fatalf("unset: %v, %v", p, err)
	}
	for _, tc := range []struct {
		name      string
		cfg       ProxyProtocolConfig
		err       string
		tls, http bool
		from      string
	}{
		{"default is the TLS port", ProxyProtocolConfig{From: []string{"127.0.0.1/32"}}, "", true, false, "127.0.0.1/32"},
		{"bare address", ProxyProtocolConfig{From: []string{"127.0.0.1", "::1"}}, "", true, false, "127.0.0.1/32,::1/128"},
		{"mapped address", ProxyProtocolConfig{From: []string{"::ffff:127.0.0.1"}}, "", true, false, "127.0.0.1/32"},
		{"HTTP behind a proxy elsewhere", ProxyProtocolConfig{From: []string{"192.0.2.10"}, Listeners: []string{"tls", "HTTP"}}, "", true, true, "192.0.2.10/32"},
		{"HTTP only", ProxyProtocolConfig{From: []string{"192.0.2.10"}, Listeners: []string{"http"}}, "", false, true, "192.0.2.10/32"},
		{"empty", ProxyProtocolConfig{}, "from is empty", false, false, ""},
		{"blank", ProxyProtocolConfig{From: []string{" "}}, "from is empty", false, false, ""},
		{"not an address", ProxyProtocolConfig{From: []string{"proxy.example.com"}}, "not a network", false, false, ""},
		{"everyone, IPv4", ProxyProtocolConfig{From: []string{"0.0.0.0/0"}}, "anyone", false, false, ""},
		{"everyone, IPv6", ProxyProtocolConfig{From: []string{"::/0"}}, "anyone", false, false, ""},
		{"unknown listener", ProxyProtocolConfig{From: []string{"127.0.0.1"}, Listeners: []string{"admin"}}, "not tls or http", false, false, ""},
		{"HTTP with loopback", ProxyProtocolConfig{From: []string{"127.0.0.1/32"}, Listeners: []string{"http"}}, "hub pair", false, false, ""},
		{"HTTP with IPv6 loopback", ProxyProtocolConfig{From: []string{"::1"}, Listeners: []string{"tls", "http"}}, "hub pair", false, false, ""},
		{"HTTP with a range around loopback", ProxyProtocolConfig{From: []string{"127.0.0.0/8"}, Listeners: []string{"http"}}, "hub pair", false, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := tc.cfg.policy()
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("err = %v, want %q", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var from []string
			for _, prefix := range p.from {
				from = append(from, prefix.String())
			}
			if p.tls != tc.tls || p.http != tc.http || strings.Join(from, ",") != tc.from {
				t.Fatalf("policy = %+v", p)
			}
		})
	}
}

// syncBuffer is a log the servers' goroutines write to while the test reads it.
type syncBuffer struct {
	mu      sync.Mutex
	b       bytes.Buffer
	changed chan struct{}
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, err := s.b.Write(p)
	if s.changed != nil {
		close(s.changed)
		s.changed = nil
	}
	return n, err
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func (s *syncBuffer) WaitFor(text string, timeout time.Duration) bool {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		s.mu.Lock()
		if strings.Contains(s.b.String(), text) {
			s.mu.Unlock()
			return true
		}
		if s.changed == nil {
			s.changed = make(chan struct{})
		}
		changed := s.changed
		s.mu.Unlock()
		select {
		case <-changed:
		case <-timer.C:
			return false
		}
	}
}

func TestSyncBufferWaitFor(t *testing.T) {
	const target = "refused a connection"
	t.Run("already written", func(t *testing.T) {
		var logs syncBuffer
		_, _ = logs.Write([]byte(target))
		if !logs.WaitFor(target, time.Second) {
			t.Fatal("existing log not found")
		}
	})
	t.Run("wait for the complete delayed log", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			var logs syncBuffer
			done := make(chan bool, 2)
			for range 2 {
				go func() { done <- logs.WaitFor(target, 5*time.Second) }()
			}
			synctest.Wait()
			_, _ = logs.Write([]byte("unrelated log\nrefused a "))
			synctest.Wait()
			select {
			case <-done:
				t.Fatal("wait completed before the target was written")
			default:
			}
			_, _ = logs.Write([]byte("connection\n"))
			for range 2 {
				if !<-done {
					t.Fatal("delayed log not found")
				}
			}
		})
	})
	t.Run("missing log times out despite other writes", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			var logs syncBuffer
			go func() {
				for range 2 {
					time.Sleep(2 * time.Second)
					_, _ = logs.Write([]byte("unrelated log\n"))
				}
			}()
			start := time.Now()
			if logs.WaitFor(target, 5*time.Second) {
				t.Fatal("missing log accepted")
			}
			if elapsed := time.Since(start); elapsed != 5*time.Second {
				t.Fatalf("timeout = %s, want 5s", elapsed)
			}
		})
	})
}

// reserveProxyPlaintext keeps a listener bound until serveHTTP takes ownership.
func reserveProxyPlaintext(t *testing.T, h *Hub) {
	t.Helper()
	h.cfg.Public.TLSListen = "127.0.0.1:0"
	reserved, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reserved.Close() }) // Also covers a startup failure before serveHTTP takes ownership.
	h.public.testPlaintext = reserved
	h.cfg.Listen = reserved.Addr().String()
}

// proxyTestHub is a hub with TLS on loopback, an admin UI open to 192.0.2.0/24 only, and pp as security.proxy_protocol.
// It returns the TLS and plaintext base URLs, the pin and the hub's log.
func proxyTestHub(t *testing.T, pp *ProxyProtocolConfig) (h *Hub, tlsURL, plainURL, pin string, logs *syncBuffer) {
	t.Helper()
	h, logs = prepareProxyTestHub(t, pp)
	runPublicTestHub(t, h)
	port, pin := h.tlsPin()
	return h, fmt.Sprintf("https://127.0.0.1:%d", port), "http://" + h.cfg.Listen, pin, logs
}

func prepareProxyTestHub(t *testing.T, pp *ProxyProtocolConfig) (*Hub, *syncBuffer) {
	t.Helper()
	t.Setenv("PERSONA_ADMIN_ACCESS", "")
	h := newPublicTestHub(t)
	reserveProxyPlaintext(t, h)
	logs := &syncBuffer{}
	h.logger = log.New(logs, "", 0)
	h.cfg.Security = SecurityConfig{AuthFailures: 2, ProxyProtocol: pp}
	h.cfg.Admin = AdminConfig{Access: accessCIDRs, CIDRs: []string{"192.0.2.0/24"}}
	h.admin.container = func() bool { return false }
	h.admin.prefixes = func() []ifacePrefix { return []ifacePrefix{} }
	h.devtok.onLink = func() []netip.Prefix { return []netip.Prefix{} }
	web := filepath.Join(h.cfg.dir, "web")
	if err := os.MkdirAll(filepath.Join(web, "admin"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(web, "index.html"), []byte("CLIENT"), 0o644)
	_ = os.WriteFile(filepath.Join(web, "admin", "index.html"), []byte("ADMIN"), 0o644)
	return h, logs
}

// proxiedClient talks to the hub over TLS pinned to pin, each connection starting with header (none when empty), as
// a proxy in front would send it.
func proxiedClient(t *testing.T, pin string, header []byte) *http.Client {
	t.Helper()
	c := pinnedTestClient(pin)
	tr := c.Transport.(*http.Transport)
	tr.DisableKeepAlives = true
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := (&net.Dialer{}).DialContext(ctx, network, addr)
		if err == nil && len(header) > 0 {
			if _, err = conn.Write(header); err != nil {
				_ = conn.Close()
			}
		}
		return conn, err
	}
	t.Cleanup(c.CloseIdleConnections)
	return c
}

func v1Header(src string) []byte {
	family := "TCP4"
	if strings.Contains(src, ":") {
		family = "TCP6"
	}
	dst := "192.0.2.1"
	if family == "TCP6" {
		dst = "2001:db8::1"
	}
	return []byte(fmt.Sprintf("PROXY %s %s %s 50000 443\r\n", family, src, dst))
}

func v2Header(src, dst netip.AddrPort) []byte {
	b := []byte("\r\n\r\n\x00\r\nQUIT\n\x21\x11\x00\x0c")
	s, d := src.Addr().As4(), dst.Addr().As4()
	b = append(append(b, s[:]...), d[:]...)
	b = binary.BigEndian.AppendUint16(b, src.Port())
	return binary.BigEndian.AppendUint16(b, dst.Port())
}

type probe struct {
	status int
	code   string
	body   string
	fields int // a JSON object's number of keys
	proto  int // HTTP major version
}

func get(t *testing.T, c *http.Client, url, token string) (probe, error) {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.Do(req)
	if err != nil {
		return probe{}, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	p := probe{status: resp.StatusCode, body: string(b), proto: resp.ProtoMajor}
	var obj map[string]any
	if json.Unmarshal(b, &obj) == nil {
		p.fields = len(obj)
		p.code, _ = obj["code"].(string)
	}
	return p, nil
}

func mustGet(t *testing.T, c *http.Client, url, token string) probe {
	t.Helper()
	p, err := get(t, c, url, token)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	return p
}

// The proxy on this machine passes on a client from the internet: the hub judges that client, not loopback.
func TestProxyProtocolEndToEnd(t *testing.T) {
	_, base, plain, pin, logs := proxyTestHub(t, &ProxyProtocolConfig{From: []string{"127.0.0.1/32"}})

	public := proxiedClient(t, pin, v1Header("203.0.113.7"))
	if p := mustGet(t, public, base+"/api/v1/health", ""); p.status != 200 || p.fields != 5 {
		t.Fatalf("public health: %+v (want the 5 public fields)", p)
	}
	if p := mustGet(t, public, base+"/api/v1/machines", "test-token"); p.status != 403 || p.code != "master_token_local_only" {
		t.Fatalf("master token from the internet: %+v", p)
	}
	if p := mustGet(t, public, base+"/admin", ""); p.body != "CLIENT" {
		t.Fatalf("admin UI out of scope: %+v", p)
	}
	// Failed authentication counts against the client: blocked after security.auth_failures (2).
	var last probe
	for range 3 {
		last = mustGet(t, public, base+"/api/v1/machines", "bad-token")
	}
	if last.status != 429 || last.code != "rate_limited" {
		t.Fatalf("failures from the internet not limited: %+v", last)
	}
	// A different client through the same proxy is not blocked with it.
	other := proxiedClient(t, pin, v1Header("2001:db8:77::7"))
	if p := mustGet(t, other, base+"/api/v1/machines", "bad-token"); p.status != 401 {
		t.Fatalf("another client: %+v", p)
	}

	// v2, a client in admin.cidrs: the admin UI by its address, over TLS. Still not local for the master token.
	inScope := proxiedClient(t, pin, v2Header(netip.MustParseAddrPort("192.0.2.7:50000"), netip.MustParseAddrPort("192.0.2.1:443")))
	if p := mustGet(t, inScope, base+"/admin", ""); p.body != "ADMIN" {
		t.Fatalf("admin UI in scope: %+v", p)
	}
	if p := mustGet(t, inScope, base+"/api/v1/machines", "test-token"); p.status != 403 {
		t.Fatalf("master token from 192.0.2.7: %+v", p)
	}

	// Straight to the TLS port from the trusted address, no header: refused, and logged.
	if _, err := get(t, proxiedClient(t, pin, nil), base+"/api/v1/health", ""); err == nil {
		t.Fatal("a connection from the proxy's address without a header was served")
	}
	if !logs.WaitFor("proxy protocol: refused a connection from 127.0.0.1", 5*time.Second) {
		t.Fatalf("refusal not logged:\n%s", logs)
	}

	// The proxy's own client on this machine stays loopback, with a hint in the log once.
	local := proxiedClient(t, pin, v1Header("127.0.0.1"))
	for range 2 {
		if p := mustGet(t, local, base+"/admin", ""); p.body != "ADMIN" {
			t.Fatalf("loopback through the proxy: %+v", p)
		}
	}
	if n := strings.Count(logs.String(), "passed on a connection from loopback"); n != 1 {
		t.Fatalf("loopback hint logged %d times:\n%s", n, logs)
	}

	// The plaintext port is left alone: the hub's own tools reach it over loopback without a header.
	if p := mustGet(t, http.DefaultClient, plain+"/api/v1/health", ""); p.status != 200 || p.fields <= 5 {
		t.Fatalf("plaintext health from loopback: %+v", p)
	}
	if p := mustGet(t, http.DefaultClient, plain+"/api/v1/machines", "test-token"); p.status != 200 {
		t.Fatalf("master token over loopback HTTP: %+v", p)
	}
}

func TestProxyProtocolPublicTLS(t *testing.T) {
	private := netip.AddrFrom4([4]byte{10, 20, 30, 7})
	ula := netip.AddrFrom16([16]byte{0xfd, 1, 0, 1, 0, 1, 0, 1, 15: 7})
	for _, tc := range []struct {
		name, config string
		public       bool
		masterStatus int
		masterCode   string
		failureLimit int
	}{
		{"default", `{"from":["127.0.0.1/32"]}`, false, 200, "", 401},
		{"public", `{"from":["127.0.0.1/32"],"public":true}`, true, 403, "master_token_local_only", 429},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var pp ProxyProtocolConfig
			if err := json.Unmarshal([]byte(tc.config), &pp); err != nil {
				t.Fatal(err)
			}
			h, _ := prepareProxyTestHub(t, &pp)
			h.cfg.Admin.CIDRs = []string{netip.PrefixFrom(private, 24).Masked().String()}
			h.cfg.Security.TrustedCIDRs = []string{netip.PrefixFrom(ula, 64).Masked().String()}
			runPublicTestHub(t, h)
			port, pin := h.tlsPin()
			base := fmt.Sprintf("https://127.0.0.1:%d", port)
			ipv4 := proxiedClient(t, pin, v1Header(private.String()))
			ipv6 := proxiedClient(t, pin, proxyproto.AppendV2(nil, netip.AddrPortFrom(ula, 50000), netip.MustParseAddrPort("[2001:db8::1]:443")))
			ipv6.Transport.(*http.Transport).ForceAttemptHTTP2 = true

			for _, c := range []*http.Client{ipv4, ipv6} {
				p := mustGet(t, c, base+"/api/v1/health", "")
				if p.status != 200 || (p.fields == 5) != tc.public || (c == ipv6 && p.proto != 2) {
					t.Fatalf("health: %+v", p)
				}
				p = mustGet(t, c, base+"/api/v1/machines", h.cfg.Token)
				if p.status != tc.masterStatus || p.code != tc.masterCode {
					t.Fatalf("master token: %+v", p)
				}
			}
			if p := mustGet(t, ipv4, base+"/admin", ""); p.body != "ADMIN" {
				t.Fatalf("client in admin CIDR: %+v", p)
			}
			if p := mustGet(t, ipv6, base+"/admin", ""); p.body != "CLIENT" {
				t.Fatalf("client outside admin CIDR: %+v", p)
			}
			var last probe
			for range 3 {
				last = mustGet(t, ipv4, base+"/api/v1/machines", "bad-token")
			}
			if last.status != tc.failureLimit {
				t.Fatalf("repeated authentication failures: %+v, want %d", last, tc.failureLimit)
			}
			if p := mustGet(t, ipv6, base+"/api/v1/machines", "bad-token"); p.status != 401 {
				t.Fatalf("another client through the same proxy: %+v", p)
			}
			for _, header := range [][]byte{
				v1Header("127.0.0.1"),
				proxyproto.AppendV2(nil, netip.AddrPort{}, netip.AddrPort{}),
			} {
				p, err := get(t, proxiedClient(t, pin, header), base+"/api/v1/health", "")
				if tc.public && err == nil || !tc.public && (err != nil || p.status != 200 || p.fields <= 5) {
					t.Fatalf("local connection: %+v, %v", p, err)
				}
			}
		})
	}
}

type proxyTestListener struct {
	net.Listener
	peer net.Addr
}

func (l proxyTestListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return proxyTestPeer{Conn: conn, peer: l.peer}, nil
}

type proxyTestPeer struct {
	net.Conn
	peer net.Addr
}

func (c proxyTestPeer) RemoteAddr() net.Addr { return c.peer }

func TestProxyProtocolPublicPlaintext(t *testing.T) {
	pp := &ProxyProtocolConfig{From: []string{"192.0.2.10"}, Listeners: []string{"http"}, Public: true}
	h, _ := prepareProxyTestHub(t, pp)
	// Give the plaintext listener a nonloopback proxy peer without requiring a second network interface.
	h.public.testPlaintext = proxyTestListener{
		Listener: h.public.testPlaintext,
		peer:     net.TCPAddrFromAddrPort(netip.MustParseAddrPort("192.0.2.10:50000")),
	}
	runPublicTestHub(t, h)
	_, pin := h.tlsPin()
	client := proxiedClient(t, pin, v1Header(ip4(10, 20, 30, 7)))
	if p := mustGet(t, client, "http://"+h.cfg.Listen+"/api/v1/machines", h.cfg.Token); p.status != 403 || p.code != "tls_required" {
		t.Fatalf("relayed plaintext: %+v", p)
	}
}

func TestProxyProtocolPublicAdminScope(t *testing.T) {
	t.Setenv("PERSONA_ADMIN_ACCESS", "")
	clientIP := ip4(10, 20, 30, 7)
	clientRange := clientIP + "/32"
	for _, tc := range []struct {
		name    string
		access  string
		cidrs   []string
		url     string
		inScope bool
	}{
		{"loopback", accessLoopback, nil, "https://example.com/admin", false},
		{"lan", accessLAN, nil, "https://example.com/admin", false},
		{"any with TLS", accessAny, nil, "https://example.com/admin", true},
		{"any on plaintext", accessAny, nil, "http://example.com/admin", false},
		{"matching CIDR", accessCIDRs, []string{clientRange}, "https://example.com/admin", true},
		{"different CIDR", accessCIDRs, []string{"192.0.2.0/24"}, "https://example.com/admin", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newPublicTestHub(t)
			h.cfg.Admin = AdminConfig{Access: tc.access, CIDRs: tc.cidrs}
			h.cfg.Security.TrustedCIDRs = []string{clientRange}
			r := httptest.NewRequest(http.MethodGet, tc.url, nil)
			r.RemoteAddr = net.JoinHostPort(clientIP, "50000")
			if !h.adminLANSource(r) {
				t.Fatal("direct trusted source is outside the admin LAN")
			}
			r = r.WithContext(context.WithValue(r.Context(), publicProxyKey{}, true))
			if got := h.adminInScope(r); got != tc.inScope {
				t.Fatalf("adminInScope = %t, want %t", got, tc.inScope)
			}
			if h.adminLANSource(r) {
				t.Fatal("relayed source is exempt from shared public admin limits")
			}
			if h.adminCanSetup(r) != (tc.access == accessCIDRs && tc.inScope) {
				t.Fatal("setup must require an explicit CIDR match")
			}
			if tc.access == accessCIDRs && tc.inScope {
				r.Header.Set("X-Forwarded-For", "192.0.2.7")
				if h.adminInScope(r) {
					t.Fatal("an out-of-scope forwarding address did not narrow access")
				}
			}
		})
	}
}

func TestProxyProtocolPublicMemberForwarding(t *testing.T) {
	center := newPublicTestHub(t)
	clientIP := ip4(10, 20, 30, 7)
	center.mesh.centerAPI.Store(center.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get(hdrPeerSource); got != "public "+clientIP+" tls" {
			t.Errorf("forwarded source = %q", got)
		}
		center.servePeerHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	linkProxy := httputil.NewSingleHostReverseProxy(target)
	linkProxy.Director = func(r *http.Request) {
		r.URL.Scheme, r.URL.Host = target.Scheme, target.Host
		r.Header.Set(hdrPeerTTL, "1")
	}
	member := newPublicTestHub(t)
	centerID := center.hubID()
	member.mesh.running = true
	member.mesh.group = meshGroup{Center: centerID}
	member.mesh.links = map[string]*peerLink{centerID: {ctx: t.Context(), proxy: linkProxy}}
	member.mesh.view.Store(&meshView{self: member.hubID(), hubs: map[string]*viewHub{centerID: {reachable: true, next: centerID}}})
	r := httptest.NewRequest(http.MethodGet, "https://example.com/api/v1/machines", nil)
	r.RemoteAddr = net.JoinHostPort(clientIP, "50000")
	r.Header.Set("Authorization", "Bearer "+center.cfg.Token)
	r.Header.Set(hdrPeerSource, "lan "+clientIP+" tls")
	r = r.WithContext(context.WithValue(r.Context(), publicProxyKey{}, true))
	w := httptest.NewRecorder()
	member.memberForward(http.NotFoundHandler()).ServeHTTP(w, r)
	if w.Code != 403 || !strings.Contains(w.Body.String(), "master_token_local_only") {
		t.Fatalf("master token through a member: %d %s", w.Code, w.Body)
	}
}

// Without security.proxy_protocol nothing changes: a header is garbage to the TLS server, and loopback is loopback.
// The same holds for a header from an address not in from.
func TestProxyProtocolOffOrUntrusted(t *testing.T) {
	for _, pp := range []*ProxyProtocolConfig{nil, {From: []string{"192.0.2.10/32"}}, {From: []string{"192.0.2.10/32"}, Public: true}} {
		_, base, _, pin, _ := proxyTestHub(t, pp)
		if _, err := get(t, proxiedClient(t, pin, v1Header("192.0.2.7")), base+"/api/v1/health", ""); err == nil {
			t.Fatalf("%+v: a header from an untrusted address was read", pp)
		}
		direct := proxiedClient(t, pin, nil)
		if p := mustGet(t, direct, base+"/api/v1/health", ""); p.status != 200 || p.fields <= 5 {
			t.Fatalf("%+v: loopback health: %+v", pp, p)
		}
		if p := mustGet(t, direct, base+"/admin", ""); p.body != "ADMIN" {
			t.Fatalf("%+v: loopback admin UI: %+v", pp, p)
		}
	}
}

func TestProxyProtocolServesReservedPlaintextListener(t *testing.T) {
	_, _, plain, _, _ := proxyTestHub(t, nil)
	if p := mustGet(t, http.DefaultClient, plain+"/api/v1/health", ""); p.status != 200 || p.fields <= 5 {
		t.Fatalf("reserved plaintext listener health: %+v", p)
	}
}

func TestProxyProtocolBadConfigStopsTheHub(t *testing.T) {
	h := newPublicTestHub(t)
	h.cfg.Public.TLSListen = "127.0.0.1:0"
	h.cfg.Security.ProxyProtocol = &ProxyProtocolConfig{From: []string{"0.0.0.0/0"}}
	if err := h.startPublic(context.Background()); err == nil || !strings.Contains(err.Error(), "security.proxy_protocol") {
		t.Fatalf("startPublic: %v", err)
	}
	if h.public.listener != nil {
		t.Fatal("a listener was opened with a bad security.proxy_protocol")
	}
}

func TestDoctorProxyProtocol(t *testing.T) {
	for _, tc := range []struct {
		pp   *ProxyProtocolConfig
		tls  string
		want string
	}{
		{nil, ":19443", ""},
		{&ProxyProtocolConfig{From: []string{"127.0.0.1/32"}}, ":19443", "正常｜security.proxy_protocol：读 127.0.0.1/32 发来的 PROXY 头"},
		{&ProxyProtocolConfig{From: []string{"127.0.0.1/32"}}, "", "有问题｜security.proxy_protocol：只作用于 TLS 端口"},
		{&ProxyProtocolConfig{From: []string{"127.0.0.1/32"}, Listeners: []string{"http"}}, ":19443", "有问题｜security.proxy_protocol：配置有误"},
	} {
		dir := t.TempDir()
		cfg := Config{Listen: ":18660", Public: PublicConfig{TLSListen: tc.tls}}
		cfg.Security.ProxyProtocol = tc.pp
		b, _ := json.Marshal(cfg)
		if err := os.WriteFile(filepath.Join(dir, "hub.json"), b, 0o600); err != nil {
			t.Fatal(err)
		}
		d := &doctorReport{l: i18n.ZhHans} // the wording the report had before it spoke other languages
		d.config(dir)
		out := reportText(d)
		if tc.want == "" {
			if strings.Contains(out, "proxy_protocol") {
				t.Fatalf("unset proxy_protocol reported:\n%s", out)
			}
		} else if !strings.Contains(out, tc.want) {
			t.Fatalf("doctor output lacks %q:\n%s", tc.want, out)
		}
	}
}
