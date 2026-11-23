package hub

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/http/httpproxy"
	xproxy "golang.org/x/net/proxy"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/link"
)

// Dialing a partner through an outbound proxy (docs/server/partner-hubs.md "Proxies"): hub.json partners.proxy ("off":
// none, not even the environment's), else the environment's (https_proxy, HTTPS_PROXY, all_proxy, ALL_PROXY, in that
// order; a SOCKS URL there is used too, unlike persona-link). http:// and https:// proxies are CONNECT ones (persona-link's
// code, link.DialThrough); socks5:// and socks5h:// have the proxy resolve the name. NO_PROXY applies, and loopback and
// localhost never go through a proxy. Direct and proxied candidates race together, direct first; with
// partners.proxy_always only proxied ones. The tunnel is made under the TLS: the partner's pinned TLS runs inside it, end
// to end, so the proxy can never stand in for the partner (Transport.Proxy stays nil). An explicit proxy that does not
// read fails every dial, never falling back to direct. No error names the proxy or its credentials.

// partnerDialFunc reaches addr (host:port) for a candidate: directly, or through the proxy.
type partnerDialFunc = func(ctx context.Context, network, addr string) (net.Conn, error)

var (
	errPartnerProxyInvalid = i18n.Err("hub-partner_dial.proxy_invalid")
	errPartnerProxyNone    = i18n.Err("hub-partner_dial.proxy_none")
	errPartnerTLSOnly      = errors.New("a partner hub is only dialed over TLS")
)

// partnerProxySetting is the proxy setting partners dial with, whether hub.json gave it, and NO_PROXY.
func (h *Hub) partnerProxySetting() (raw string, explicit bool, noProxy string) {
	env := httpproxy.FromEnvironment() // reads NO_PROXY / no_proxy on every call, as link does
	switch c := strings.TrimSpace(h.cfg.Partners.Proxy); c {
	case link.ProxyOff:
		return "", true, ""
	case "":
		for _, k := range []string{"https_proxy", "HTTPS_PROXY", "all_proxy", "ALL_PROXY"} {
			if v := strings.TrimSpace(os.Getenv(k)); v != "" {
				return v, false, env.NoProxy
			}
		}
		return "", false, env.NoProxy
	default:
		return c, true, env.NoProxy
	}
}

// parsePartnerProxy reads a proxy URL: http:// and https:// (and a bare host:port) as link.ParseProxy reads them,
// socks5:// and socks5h:// with a host, a port (1080 when there is none) and optional credentials. Errors never quote it.
func parsePartnerProxy(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if scheme, _, ok := strings.Cut(raw, "://"); ok && (strings.EqualFold(scheme, "socks5") || strings.EqualFold(scheme, "socks5h")) {
		u, err := url.Parse(raw)
		if err != nil || u.Hostname() == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
			return nil, errPartnerProxyInvalid
		}
		if port := u.Port(); port != "" {
			if n, err := strconv.Atoi(port); err != nil || n <= 0 || n > 65535 {
				return nil, errPartnerProxyInvalid
			}
		}
		u.Scheme, u.Path = strings.ToLower(u.Scheme), ""
		return u, nil
	}
	u, err := link.ParseProxy(raw)
	if err != nil {
		return nil, errPartnerProxyInvalid
	}
	return u, nil
}

// proxyKind is a proxy as the log names it: its scheme alone (never its address or credentials), "" when it does not
// read.
func proxyKind(raw string) string {
	u, err := parsePartnerProxy(raw)
	if err != nil {
		return ""
	}
	return u.Scheme
}

// sayPartnerProxy logs once, at start, what kind of proxy partners are dialed through and whose setting it is (never
// its address or credentials), or that the configured one does not read (every dial fails then).
func (h *Hub) sayPartnerProxy() {
	raw, explicit, _ := h.partnerProxySetting()
	from := "the environment's"
	if explicit {
		from = "partners.proxy's"
	}
	switch kind := proxyKind(raw); {
	case raw == "":
	case kind == "" && explicit:
		h.logf("partners: partners.proxy does not read (http://, https://, socks5:// or socks5h://, host and port): " +
			"every dial of a partner fails until it is fixed")
	case kind == "":
		h.logf("partners: the environment's proxy does not read: partners are dialed without it")
	default:
		h.logf("partners: dialing through %s %s proxy (proxy_always %v)", from, kind, h.cfg.Partners.ProxyAlways)
	}
}

// partnerProxyDial is the dial through proxy p.
func partnerProxyDial(p *url.URL) partnerDialFunc {
	if p.Scheme == "http" || p.Scheme == "https" {
		return link.DialThrough(p)
	}
	at := p.Host
	if p.Port() == "" {
		at = net.JoinHostPort(p.Hostname(), "1080")
	}
	var auth *xproxy.Auth
	if p.User != nil {
		password, _ := p.User.Password()
		auth = &xproxy.Auth{User: p.User.Username(), Password: password}
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		d, err := xproxy.SOCKS5("tcp", at, auth, &net.Dialer{Timeout: peerDialTimeout, KeepAlive: 30 * time.Second})
		if err != nil {
			return nil, &partnerSocksError{}
		}
		conn, err := d.(xproxy.ContextDialer).DialContext(ctx, network, addr) // the name goes to the proxy, which resolves it
		if err != nil {
			return nil, socksFailed(err)
		}
		return partnerTunnel{conn}, nil
	}
}

// partnerSocksError is a SOCKS proxy that failed: unreachable, or its refusal (no address in it).
type partnerSocksError struct {
	reason string
	cause  error
}

func (e *partnerSocksError) Error() string { return e.Text(i18n.Default) }
func (e *partnerSocksError) Unwrap() error { return e.cause }
func (e *partnerSocksError) Text(l i18n.Lang) string {
	if e.reason != "" {
		return i18n.T(l, "hub-partner_dial.socks_refused", i18n.Args{"reason": e.reason})
	}
	return i18n.T(l, "hub-partner_dial.socks_unreachable")
}

// socksFailed is x/net's SOCKS error without the proxy's address (its net.OpError names it).
func socksFailed(err error) error {
	var oe *net.OpError
	// The proxy's answer (refused, wrong credentials) is the inner error's text; a failed dial to the proxy is a
	// net.OpError of its own there, which names it. The cause stays for errors.Is (a race's loser is called off).
	if errors.As(err, &oe) && oe.Err != nil && !errors.As(oe.Err, new(*net.OpError)) && !errors.Is(err, context.Canceled) &&
		!errors.Is(err, context.DeadlineExceeded) {
		return &partnerSocksError{reason: oe.Err.Error(), cause: err}
	}
	return &partnerSocksError{cause: err}
}

// partnerTunnel is a connection through a SOCKS proxy whose errors leave out the addresses of the proxy's TCP
// connection (link's tunnel does the same for its CONNECT ones).
type partnerTunnel struct{ net.Conn }

func (t partnerTunnel) Read(b []byte) (int, error) {
	n, err := t.Conn.Read(b)
	return n, addrless(err)
}

func (t partnerTunnel) Write(b []byte) (int, error) {
	n, err := t.Conn.Write(b)
	return n, addrless(err)
}

func addrless(err error) error {
	var oe *net.OpError
	if errors.As(err, &oe) && (oe.Source != nil || oe.Addr != nil) {
		return &net.OpError{Op: oe.Op, Net: oe.Net, Err: oe.Err}
	}
	return err
}

// partnerCandidate is one way to try a partner: its address, directly or through the proxy.
type partnerCandidate struct {
	key string // raceEndpoints' name for it (and the log's)
	url string
	via partnerDialFunc // nil: direct
}

// failed is err as this candidate's: its address and route said in the reader's language.
func (c partnerCandidate) failed(err error) error {
	return &partnerTryError{url: c.url, proxied: c.via != nil, err: err}
}

// partnerCandidates are the ways to try eps (https only, ranked): each directly, then each the proxy is used for (LAN
// last: a proxy rarely reaches them); with proxy_always the proxied ones alone.
func (h *Hub) partnerCandidates(eps []string) ([]partnerCandidate, error) {
	raw, explicit, noProxy := h.partnerProxySetting()
	always := h.cfg.Partners.ProxyAlways
	var via partnerDialFunc
	if raw != "" {
		p, err := parsePartnerProxy(raw)
		switch {
		case err != nil && explicit:
			return nil, errPartnerProxyInvalid
		case err == nil:
			via = partnerProxyDial(p)
		}
	}
	// Only NO_PROXY's answer is wanted here: the stand-in keeps the real URL (and its credentials) out of httpproxy.
	use := (&httpproxy.Config{HTTPSProxy: "http://proxy.invalid", NoProxy: noProxy}).ProxyFunc()
	var direct, proxied, lan []partnerCandidate
	for _, ep := range partnerHTTPS(eps) {
		if !always {
			direct = append(direct, partnerCandidate{key: ep, url: ep})
		}
		if via == nil {
			continue
		}
		u, err := url.Parse(ep)
		if err != nil {
			continue
		}
		if p, _ := use(u); p == nil {
			continue
		}
		c := partnerCandidate{key: ep + " via proxy", url: ep, via: via}
		if endpointKindOf(ep) == 0 {
			lan = append(lan, c)
		} else {
			proxied = append(proxied, c)
		}
	}
	out := append(append(direct, proxied...), lan...)
	switch {
	case len(out) > 0:
		return out, nil
	case always && len(partnerHTTPS(eps)) > 0:
		return nil, errPartnerProxyNone
	}
	return nil, i18n.Err("hub-peer_dial.no_endpoints")
}

func partnerKeys(cands []partnerCandidate) []string {
	keys := make([]string, len(cands))
	for i, c := range cands {
		keys[i] = c.key
	}
	return keys
}

func partnerCandidateOf(cands []partnerCandidate, key string) partnerCandidate {
	for _, c := range cands {
		if c.key == key {
			return c
		}
	}
	return partnerCandidate{key: key, url: key}
}

// partnerClient is an HTTP/1.1 client that only talks to the hub whose TLS key hashes to fp, over TLS, reached with
// via (directly when nil). Plain http:// is never dialed.
func (h *Hub) partnerClient(fp string, via partnerDialFunc) *http.Client {
	if via == nil {
		via = (&net.Dialer{Timeout: peerDialTimeout, KeepAlive: 30 * time.Second}).DialContext
	}
	return &http.Client{
		Transport: &http.Transport{
			Proxy: nil, // the proxy, if any, is via's: never one the transport would put in front of the TLS
			DialContext: func(context.Context, string, string) (net.Conn, error) {
				return nil, errPartnerTLSOnly
			},
			DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				raw, err := via(ctx, network, addr)
				if err != nil {
					return nil, err
				}
				host, _, _ := net.SplitHostPort(addr)
				cfg := &tls.Config{MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"},
					InsecureSkipVerify: true, // the pin below is the check
					VerifyConnection: func(cs tls.ConnectionState) error {
						if len(cs.PeerCertificates) == 0 {
							return errPeerPin
						}
						sum := sha256.Sum256(cs.PeerCertificates[0].RawSubjectPublicKeyInfo)
						if fp == "" || base64.RawURLEncoding.EncodeToString(sum[:]) != fp {
							return errPeerPin
						}
						return nil
					}}
				if net.ParseIP(host) == nil {
					cfg.ServerName = host
				}
				c := tls.Client(raw, cfg)
				if err := c.HandshakeContext(ctx); err != nil {
					raw.Close()
					return nil, err
				}
				return c, nil
			},
			ResponseHeaderTimeout: peerDialTimeout,
			MaxIdleConns:          1,
			IdleConnTimeout:       30 * time.Second,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// partnerTryError is one candidate's failure: its address, whether it went through the proxy (never which), and why.
type partnerTryError struct {
	url     string
	proxied bool
	err     error
}

func (e *partnerTryError) Error() string { return e.url + ": " + e.err.Error() }
func (e *partnerTryError) Unwrap() error { return e.err }
func (e *partnerTryError) Text(l i18n.Lang) string {
	key := "hub-partner_dial.direct_failed"
	if e.proxied {
		key = "hub-partner_dial.proxy_failed"
	}
	reason := i18n.Localize(e.err, l)
	var said interface{ Text(i18n.Lang) string } // under the WebSocket's and HTTP's wrapping: the proxy's, the pin's
	if errors.As(e.err, &said) {
		reason = said.Text(l)
	}
	return i18n.T(l, key, i18n.Args{"address": e.url, "err": reason})
}

// partnerDialError is a dial that failed on every candidate: each one's failure, in the reader's language.
type partnerDialError struct{ err error }

func (e *partnerDialError) Error() string           { return e.err.Error() }
func (e *partnerDialError) Unwrap() error           { return e.err }
func (e *partnerDialError) Text(l i18n.Lang) string { return partnerErrText(e.err, l) }

// partnerErrText is err in l: each of a race's failures, one after another.
func partnerErrText(err error, l i18n.Lang) string {
	if de, ok := err.(*partnerDialError); ok {
		err = de.err
	}
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		parts := make([]string, 0, len(j.Unwrap()))
		for _, one := range j.Unwrap() {
			parts = append(parts, partnerErrText(one, l))
		}
		return strings.Join(parts, "; ")
	}
	var te *partnerTryError
	if errors.As(err, &te) {
		return te.Text(l)
	}
	return i18n.Localize(err, l)
}
