package hub

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/i18n"
)

// Partner hubs through an outbound proxy (partner_proxy.go). The partner's address in these tests is 192.0.2.1:443,
// which nothing reaches directly: only the test proxy does, which sends every tunnel to the hub (or to an impostor).

const (
	partnerProxyUser   = "proxy-user"
	partnerProxySecret = "proxy-secret"
	partnerUnroutable  = "https://192.0.2.1:443"
)

// tunnelProxy is what both test proxies keep: where tunnels go, what they were asked for, their connections.
type tunnelProxy struct {
	url      string // with the credentials
	upstream atomic.Value
	mu       sync.Mutex
	targets  []string
	conns    []net.Conn
}

func (p *tunnelProxy) asked() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.targets)
}

func (p *tunnelProxy) noteTarget(target string) {
	p.mu.Lock()
	p.targets = append(p.targets, target)
	p.mu.Unlock()
}

// pipe joins down (the hub dialing) to the upstream, both ways, until one side ends.
func (p *tunnelProxy) pipe(down io.ReadWriteCloser, up net.Conn) {
	p.mu.Lock()
	if c, ok := down.(net.Conn); ok {
		p.conns = append(p.conns, c)
	}
	p.conns = append(p.conns, up)
	p.mu.Unlock()
	go func() { _, _ = io.Copy(up, down); _ = up.Close() }()
	go func() { _, _ = io.Copy(down, up); _ = down.Close() }()
}

// cut closes every tunnel (a proxy restarting).
func (p *tunnelProxy) cut() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.conns {
		_ = c.Close()
	}
	p.conns = nil
}

// newConnectProxy is a minimal HTTP CONNECT proxy (as link's tests have): every tunnel to upstream, after the
// credentials partnerProxyUser / partnerProxySecret.
func newPartnerConnectProxy(t *testing.T, upstream string) *tunnelProxy {
	t.Helper()
	p := &tunnelProxy{}
	p.upstream.Store(upstream)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "CONNECT only", http.StatusMethodNotAllowed)
			return
		}
		if r.Header.Get("Proxy-Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte(partnerProxyUser+":"+partnerProxySecret)) {
			w.Header().Set("Proxy-Authenticate", `Basic realm="test"`)
			http.Error(w, "credentials", http.StatusProxyAuthRequired)
			return
		}
		p.noteTarget(r.Host)
		up, err := net.DialTimeout("tcp", p.upstream.Load().(string), 3*time.Second)
		if err != nil {
			http.Error(w, "upstream", http.StatusBadGateway)
			return
		}
		down, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			_ = up.Close()
			return
		}
		_, _ = io.WriteString(down, "HTTP/1.1 200 Connection established\r\n\r\n")
		p.pipe(down, up)
	}))
	t.Cleanup(func() { srv.Close(); p.cut() })
	u, _ := url.Parse(srv.URL)
	u.User = url.UserPassword(partnerProxyUser, partnerProxySecret)
	p.url = u.String()
	return p
}

// newPartnerSocksProxy is a minimal SOCKS5 proxy (RFC 1928, CONNECT only, username/password auth of RFC 1929): every
// tunnel to upstream, whatever it was asked for.
func newPartnerSocksProxy(t *testing.T, upstream string) *tunnelProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &tunnelProxy{}
	p.upstream.Store(upstream)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go p.serveSocks(c)
		}
	}()
	t.Cleanup(func() { _ = ln.Close(); p.cut() })
	p.url = "socks5h://" + url.UserPassword(partnerProxyUser, partnerProxySecret).String() + "@" + ln.Addr().String()
	return p
}

func (p *tunnelProxy) serveSocks(c net.Conn) {
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	br := bufio.NewReader(c)
	fail := func() { _ = c.Close() }
	b := make([]byte, 2)
	if _, err := io.ReadFull(br, b); err != nil || b[0] != 5 {
		fail()
		return
	}
	methods := make([]byte, b[1])
	if _, err := io.ReadFull(br, methods); err != nil || !slices.Contains(methods, 2) {
		_, _ = c.Write([]byte{5, 0xff})
		fail()
		return
	}
	_, _ = c.Write([]byte{5, 2})
	readField := func() string {
		n, err := br.ReadByte()
		if err != nil {
			return ""
		}
		f := make([]byte, n)
		_, _ = io.ReadFull(br, f)
		return string(f)
	}
	if v, err := br.ReadByte(); err != nil || v != 1 {
		fail()
		return
	}
	if user, pass := readField(), readField(); user != partnerProxyUser || pass != partnerProxySecret {
		_, _ = c.Write([]byte{1, 1})
		fail()
		return
	}
	_, _ = c.Write([]byte{1, 0})
	req := make([]byte, 4)
	if _, err := io.ReadFull(br, req); err != nil || req[0] != 5 || req[1] != 1 {
		fail()
		return
	}
	var host string
	switch req[3] {
	case 1, 4:
		ip := make([]byte, map[byte]int{1: 4, 4: 16}[req[3]])
		_, _ = io.ReadFull(br, ip)
		host = net.IP(ip).String()
	case 3:
		host = readField()
	}
	port := make([]byte, 2)
	if _, err := io.ReadFull(br, port); err != nil {
		fail()
		return
	}
	p.noteTarget(net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(port)))))
	up, err := net.DialTimeout("tcp", p.upstream.Load().(string), 3*time.Second)
	if err != nil {
		_, _ = c.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
		fail()
		return
	}
	_, _ = c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
	_ = c.SetDeadline(time.Time{})
	p.pipe(struct {
		io.Reader
		io.Writer
		io.Closer
	}{br, c, c}, up)
	p.mu.Lock()
	p.conns = append(p.conns, c)
	p.mu.Unlock()
}

// impostor is a TLS server with a key of its own: a hub the proxy sends tunnels to that is not the one pinned.
func impostor(t *testing.T) string {
	t.Helper()
	cert, _ := partnerCert(t)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) }))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{*cert}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv.Listener.Addr().String()
}

// A hub reaches its partner only through an HTTP CONNECT proxy (proxy_always): pairing and the link go through it,
// to the partner's name as the code gave it, and the link comes back after the proxy cuts its tunnels. The pin is
// still the partner's: an impostor behind the proxy is refused, and nothing is paired with one.
func TestPartnerThroughConnectProxy(t *testing.T) {
	b := startPartnerHub(t, hubB, "Office", partnerHubOpts{endpoints: []string{partnerUnroutable}})
	proxy := newPartnerConnectProxy(t, b.srv.Listener.Addr().String())
	a := startPartnerHub(t, hubA, "家里", partnerHubOpts{noTLS: true, partners: PartnersConfig{Proxy: proxy.url, ProxyAlways: true}})
	pairPartners(t, a, b)
	if asked := proxy.asked(); len(asked) < 2 || !slices.Contains(asked, "192.0.2.1:443") || slices.ContainsFunc(asked, func(s string) bool { return s != "192.0.2.1:443" }) {
		t.Fatalf("the proxy was asked for %v", asked)
	}
	if l := a.h.partnerLinkTo(hubB); !strings.Contains(l.address, "via proxy") || l.url != partnerUnroutable {
		t.Fatalf("the link went %q to %q", l.address, l.url)
	}
	first := a.h.partnerLinkTo(hubB)
	proxy.cut()
	eventually(t, 20*time.Second, "the link back through the proxy", func() bool {
		l := a.h.partnerLinkTo(hubB)
		return l != nil && l != first
	})

	// An impostor behind the proxy: the link is refused on the pin, and stays down.
	proxy.upstream.Store(impostor(t))
	proxy.cut()
	eventually(t, 20*time.Second, "the pin refusing the impostor", func() bool {
		a.h.partners.mu.Lock()
		f := a.h.partners.dial[hubB]
		a.h.partners.mu.Unlock()
		return f.err != nil && strings.Contains(partnerErrText(f.err, i18n.En), i18n.T(i18n.En, "hub-peer_dial.pin_mismatch"))
	})
	if a.h.partnerLinkTo(hubB) != nil {
		t.Fatal("a link to the impostor")
	}
	// Pairing with an impostor in the proxy's place: refused, nothing left behind.
	c := startPartnerHub(t, hubC, "Lab", partnerHubOpts{endpoints: []string{partnerUnroutable}})
	code, err := c.h.newPartnerCode(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.h.joinPartner(context.Background(), code.Code); err == nil || partnerErrCode(err) != "unreachable" {
		t.Fatalf("paired through an impostor: %v", err)
	}
	if _, ok := a.h.partnerRow(hubC); ok {
		t.Fatal("a pairing with an impostor was kept")
	}
	if rows, _ := c.store.PartnerHubs(context.Background()); len(rows) != 0 {
		t.Fatal("the code's hub heard of it")
	}
}

// The same through a SOCKS5 proxy with credentials, from the environment (ALL_PROXY: partners take SOCKS there,
// unlike persona-link); the proxy is told the name and resolves it. Wrong credentials fail, and no error names the
// proxy or its credentials.
func TestPartnerThroughSocksProxy(t *testing.T) {
	b := startPartnerHub(t, hubB, "Office", partnerHubOpts{endpoints: []string{"https://hub.example.com:443"}})
	proxy := newPartnerSocksProxy(t, b.srv.Listener.Addr().String())
	t.Setenv("ALL_PROXY", proxy.url)
	a := startPartnerHub(t, hubA, "家里", partnerHubOpts{noTLS: true, partners: PartnersConfig{ProxyAlways: true}})
	pairPartners(t, a, b)
	if asked := proxy.asked(); len(asked) < 2 || slices.ContainsFunc(asked, func(s string) bool { return s != "hub.example.com:443" }) {
		t.Fatalf("the proxy was asked for %v", asked)
	}
	// Wrong credentials: refused, in words that name neither the proxy nor its credentials.
	u, _ := url.Parse(proxy.url)
	u.User = url.UserPassword(partnerProxyUser, "wrong")
	t.Setenv("ALL_PROXY", u.String())
	c := startPartnerHub(t, hubC, "Lab", partnerHubOpts{endpoints: []string{"https://hub.example.com:443"}})
	code, _ := c.h.newPartnerCode(context.Background())
	_, err := a.h.joinPartner(context.Background(), code.Code)
	if err == nil {
		t.Fatal("paired through a proxy that refused the credentials")
	}
	for _, l := range []i18n.Lang{i18n.En, i18n.ZhHans, i18n.Ja} {
		text := i18n.Localize(err, l)
		if strings.Contains(text, partnerProxySecret) || strings.Contains(text, "wrong") || strings.Contains(text, u.Host) || strings.Contains(text, partnerProxyUser) {
			t.Errorf("%s: the error names the proxy: %s", l, text)
		}
		if !strings.Contains(text, i18n.T(l, "hub-partner_dial.socks_refused", i18n.Args{"reason": "username/password authentication failed"})) {
			t.Errorf("%s: %s", l, text)
		}
	}
}

// An explicit proxy that does not read fails every dial: never direct, though the partner is right there.
func TestPartnerBadProxyNeverGoesDirect(t *testing.T) {
	b := startPartnerHub(t, hubB, "Office", partnerHubOpts{})
	a := startPartnerHub(t, hubA, "家里", partnerHubOpts{partners: PartnersConfig{Proxy: "socks5://" + partnerProxyUser + ":" + partnerProxySecret + "@"}})
	code, _ := b.h.newPartnerCode(context.Background())
	_, err := a.h.joinPartner(context.Background(), code.Code)
	if partnerErrCode(err) != "unreachable" || i18n.Localize(err, i18n.En) != i18n.T(i18n.En, "hub-partner_dial.proxy_invalid") {
		t.Fatalf("pairing with a bad proxy: %v", err)
	}
	if rows, _ := b.store.PartnerHubs(context.Background()); len(rows) != 0 {
		t.Fatal("the pairing went direct")
	}
	// A pairing it had: never dialed.
	sealed, err := a.h.sealKey("jd_" + randomHex(24))
	if err != nil {
		t.Fatal(err)
	}
	a.h.partnerSaved(db.PartnerHub{HubID: hubB, Name: "Office", FP: b.h.public.fp, Endpoints: []string{b.srv.URL}, Token: sealed,
		CredentialID: "dev_x", Dial: "auto"})
	full, _ := a.deviceToken(t, roleFull)
	eventually(t, 10*time.Second, "the dial failing on the proxy", func() bool {
		_, body := a.do(t, "GET", "/api/v1/partner-hubs", full, "")
		return strings.Contains(string(body), "partners.proxy")
	})
	if a.h.partnerLinkTo(hubB) != nil {
		t.Fatal("a link without the proxy")
	}
	_, body := a.do(t, "GET", "/api/v1/partner-hubs", full, "")
	if strings.Contains(string(body), partnerProxySecret) {
		t.Fatalf("the list names the proxy's credentials: %s", body)
	}
}

// NO_PROXY, loopback and localhost: never through the proxy. With proxy_always and nothing the proxy applies to, no
// dial at all; otherwise a loopback address goes direct.
func TestPartnerNoProxy(t *testing.T) {
	b := startPartnerHub(t, hubB, "Office", partnerHubOpts{endpoints: []string{partnerUnroutable}})
	proxy := newPartnerConnectProxy(t, b.srv.Listener.Addr().String())
	t.Setenv("HTTPS_PROXY", proxy.url)
	t.Setenv("NO_PROXY", "192.0.2.1")
	a := startPartnerHub(t, hubA, "家里", partnerHubOpts{noTLS: true, partners: PartnersConfig{ProxyAlways: true}})
	code, _ := b.h.newPartnerCode(context.Background())
	_, err := a.h.joinPartner(context.Background(), code.Code)
	if i18n.Localize(err, i18n.En) != i18n.T(i18n.En, "hub-partner_dial.proxy_none") {
		t.Fatalf("NO_PROXY with proxy_always: %v", err)
	}
	// A partner on loopback: direct, though a proxy is set.
	b2 := startPartnerHub(t, hubC, "Lab", partnerHubOpts{})
	a2 := startPartnerHub(t, "dddddddd-0000-4000-8000-000000000004", "Home2", partnerHubOpts{noTLS: true})
	pairPartners(t, a2, b2)
	if asked := proxy.asked(); len(asked) != 0 {
		t.Fatalf("the proxy was asked for %v", asked)
	}
}

// The candidates: each address directly first (LAN, IPv6, names, IPv4), then through the proxy (LAN last); with
// proxy_always the proxied ones alone; http:// never; loopback and NO_PROXY never through the proxy.
func TestPartnerCandidates(t *testing.T) {
	t.Setenv("NO_PROXY", "skip.example.com")
	h := &Hub{cfg: &Config{Partners: PartnersConfig{Proxy: "http://proxy.example.com:3128"}}}
	eps := []string{"https://hub.example.com:443", "https://[fd00::10]:18662", "http://192.0.2.5:18660", "https://127.0.0.1:18662",
		"https://skip.example.com"}
	keys := func() []string {
		t.Helper()
		cands, err := h.partnerCandidates(eps)
		if err != nil {
			t.Fatal(err)
		}
		return partnerKeys(cands)
	}
	want := []string{"https://[fd00::10]:18662", "https://127.0.0.1:18662", "https://hub.example.com:443", "https://skip.example.com",
		"https://hub.example.com:443 via proxy", "https://[fd00::10]:18662 via proxy"}
	if got := keys(); !slices.Equal(got, want) {
		t.Errorf("candidates %v, want %v", got, want)
	}
	h.cfg.Partners.ProxyAlways = true
	if got := keys(); !slices.Equal(got, want[4:]) {
		t.Errorf("proxy_always: %v, want %v", got, want[4:])
	}
	h.cfg.Partners.Proxy = "off"
	if _, err := h.partnerCandidates(eps); err != errPartnerProxyNone {
		t.Errorf("proxy_always with the proxy off: %v", err)
	}
	h.cfg.Partners = PartnersConfig{Proxy: "off"}
	t.Setenv("HTTPS_PROXY", "http://proxy.example.com:3128")
	if got := keys(); len(got) != 4 || slices.ContainsFunc(got, func(k string) bool { return strings.Contains(k, "proxy") }) {
		t.Errorf("off, with a proxy in the environment: %v", got)
	}
}

// What partners.proxy reads, and how the log names it: its kind, never its address or credentials.
func TestParsePartnerProxy(t *testing.T) {
	for raw, want := range map[string]string{
		"http://u:p@proxy.example.com:3128": "http",
		"proxy.example.com:3128":            "http",
		"https://proxy.example.com":         "https",
		"socks5://u:p@192.0.2.9":            "socks5",
		"SOCKS5H://192.0.2.9:1081":          "socks5h",
		"socks5://":                         "",
		"socks5://192.0.2.9:99999":          "",
		"socks4://192.0.2.9":                "",
		"ftp://192.0.2.9":                   "",
		"https:u:p@proxy.example.com":       "",
	} {
		if got := proxyKind(raw); got != want {
			t.Errorf("%q: %q, want %q", raw, got, want)
		}
	}
	for raw, want := range map[string]string{
		"socks5://u:p@192.0.2.9":   "192.0.2.9", // no port: 1080 when dialed
		"SOCKS5H://192.0.2.9:1081": "192.0.2.9:1081",
	} {
		if u, err := parsePartnerProxy(raw); err != nil || u.Host != want {
			t.Errorf("%q: %v %v", raw, u, err)
		}
	}
}
