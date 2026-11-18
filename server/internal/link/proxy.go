package link

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"golang.org/x/net/http/httpproxy"

	"github.com/nwverse/persona/server/internal/i18n"
)

// KindProxy is the route through an outbound HTTP(S) proxy (docs/operations/link.md "Outbound proxy"): Status.Kind
// while a link uses it, and the kind its candidates carry in errors.
const KindProxy = "proxy"

// ProxyOff as Config.Proxy turns the environment's proxy off.
const ProxyOff = "off"

// proxyError is how the outbound proxy failed, never the hub: its text names neither the proxy nor its credentials.
// The cause stays for errors.Is (a race's loser is called off, not failed) and never reaches the text.
type proxyError struct {
	key    string
	status int
	cause  error
}

func (e *proxyError) Error() string { return e.Text(i18n.Default) }
func (e *proxyError) Unwrap() error { return e.cause }
func (e *proxyError) Text(l i18n.Lang) string {
	if e.status != 0 {
		return i18n.T(l, e.key, i18n.Args{"status": e.status})
	}
	return i18n.T(l, e.key)
}

var errProxyInvalid = &proxyError{key: "link-errors.proxy_invalid"}

// ParseProxy reads a proxy URL as Go's net/http reads HTTPS_PROXY (a bare host:port means http://) and takes what a
// CONNECT proxy needs alone: http or https, a host and port, optional credentials. Errors never quote it.
func ParseProxy(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		if v, e := url.Parse("http://" + raw); e == nil {
			u, err = v, nil
		}
	}
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || (u.Port() != "" && !validPort(u.Port())) {
		return nil, errProxyInvalid
	}
	u.Path = ""
	return u, nil
}

// RedactedProxy is a proxy setting to show: the URL without its user name and password, "off" as it is, "" when
// there is none or it cannot be read (nothing of it is shown then).
func RedactedProxy(raw string) string {
	if raw == "" || raw == ProxyOff {
		return raw
	}
	u, err := ParseProxy(raw)
	if err != nil {
		return ""
	}
	u.User = nil
	return u.String()
}

// SetProxy saves link.json's proxy: a URL (ParseProxy), ProxyOff, or "" for the environment's again.
func SetProxy(path, raw string) error {
	raw = strings.TrimSpace(raw)
	if raw != ProxyOff && raw != "" {
		u, err := ParseProxy(raw)
		if err != nil {
			return err
		}
		raw = u.String()
	}
	return Update(path, func(c *Config) error { c.Proxy = raw; return nil })
}

// firstEnv is the first of the variables that is set, as Go's net/http reads them (upper case first).
func firstEnv(names ...string) string {
	for _, name := range names {
		if v := os.Getenv(name); v != "" {
			return v
		}
	}
	return ""
}

// proxied is the proxy c's candidates go through and those candidates, each marked KindProxy (race ranks them):
// link.json's proxy, else HTTPS_PROXY or ALL_PROXY. None when there is no proxy, it is off, or NO_PROXY exempts every
// candidate (as for Go's net/http, localhost and loopback addresses never use one). A proxy that cannot be read is
// the proxy route's error alone: direct candidates are tried all the same.
func proxied(c *Config) (*url.URL, []Endpoint, error) {
	raw := c.Proxy
	if raw == "" {
		raw = firstEnv("HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy")
	}
	if raw == "" || raw == ProxyOff {
		return nil, nil, nil
	}
	p, err := ParseProxy(raw)
	if err != nil {
		return nil, nil, err
	}
	// Only NO_PROXY's answer is wanted here: the stand-in keeps the real URL (and its credentials) out of httpproxy.
	use := (&httpproxy.Config{HTTPSProxy: "http://proxy.invalid", NoProxy: firstEnv("NO_PROXY", "no_proxy")}).ProxyFunc()
	var eps []Endpoint
	for _, ep := range c.Endpoints {
		if u, err := url.Parse(ep.URL); err == nil {
			if via, _ := use(u); via != nil {
				eps = append(eps, Endpoint{URL: ep.URL, Kind: KindProxy, Manual: ep.Manual})
			}
		}
	}
	if len(eps) == 0 {
		return nil, nil, nil
	}
	return p, eps, nil
}

// proxyDial is a DialContext that reaches addr through proxy p with a CONNECT. An https proxy is checked with the
// system's CAs and its own name before anything is sent to it; the hub's TLS then runs inside the tunnel with the
// hub's own check (the transport's TLSClientConfig), so the proxy can never stand in for the hub. onConn sees the TCP
// connection to the proxy.
func proxyDial(p *url.URL, onConn func(net.Conn)) func(context.Context, string, string) (net.Conn, error) {
	d := &net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second}
	port := p.Port()
	if port == "" {
		port = map[string]string{"http": "80", "https": "443"}[p.Scheme]
	}
	at := net.JoinHostPort(p.Hostname(), port)
	return func(ctx context.Context, _, addr string) (net.Conn, error) {
		raw, err := d.DialContext(ctx, "tcp", at)
		if err != nil {
			return nil, &proxyError{key: "link-errors.proxy_unreachable", cause: err}
		}
		// Ending ctx ends the dial: a race's loser, or a link going away, does not wait for a slow proxy.
		stop := context.AfterFunc(ctx, func() { _ = raw.Close() })
		conn, err := connectThrough(ctx, raw, p, addr)
		if !stop() {
			return nil, &proxyError{key: "link-errors.proxy_unreachable", cause: ctx.Err()}
		}
		if err != nil {
			_ = raw.Close()
			return nil, err
		}
		if onConn != nil {
			onConn(raw)
		}
		return conn, nil
	}
}

// connectThrough asks the proxy on raw for a tunnel to addr: TLS to the proxy first when it is an https one.
func connectThrough(ctx context.Context, raw net.Conn, p *url.URL, addr string) (net.Conn, error) {
	deadline := time.Now().Add(dialTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = raw.SetDeadline(deadline)
	conn := raw
	if p.Scheme == "https" {
		t := tls.Client(raw, &tls.Config{MinVersion: tls.VersionTLS12, ServerName: p.Hostname(), RootCAs: systemRoots.Load()})
		if err := t.HandshakeContext(ctx); err != nil {
			return nil, &proxyError{key: "link-errors.proxy_tls_failed", cause: err}
		}
		conn = t
	}
	req := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: addr}, Host: addr, Header: http.Header{}}
	if p.User != nil {
		password, _ := p.User.Password()
		req.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(p.User.Username()+":"+password)))
	}
	if err := req.Write(conn); err != nil {
		return nil, &proxyError{key: "link-errors.proxy_connect_failed", cause: err}
	}
	br := bufio.NewReader(io.LimitReader(conn, 16<<10))
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		return nil, &proxyError{key: "link-errors.proxy_connect_failed", cause: err}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &proxyError{key: "link-errors.proxy_rejected", status: resp.StatusCode}
	}
	// The hub's TLS starts with this side's hello: a proxy that already sent tunnel bytes is not one to trust.
	if br.Buffered() != 0 {
		return nil, &proxyError{key: "link-errors.proxy_connect_failed"}
	}
	_ = raw.SetDeadline(time.Time{})
	return conn, nil
}
