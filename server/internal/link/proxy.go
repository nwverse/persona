package link

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"golang.org/x/net/http/httpproxy"

	"github.com/nwverse/persona/server/internal/i18n"
)

const KindProxy = "proxy"

// proxyURL resolves the outbound proxy without exposing credentials in errors.
func proxyURL(c *Config, target string) (*url.URL, error) {
	if c.Proxy == "off" {
		return nil, nil
	}
	raw := c.Proxy
	if raw == "" {
		raw = os.Getenv("HTTPS_PROXY")
		if raw == "" {
			raw = os.Getenv("https_proxy")
		}
		if raw == "" {
			raw = os.Getenv("ALL_PROXY")
		}
		if raw == "" {
			raw = os.Getenv("all_proxy")
		}
	}
	if raw == "" {
		return nil, nil
	}
	p, err := url.Parse(raw)
	if err != nil || p.Hostname() == "" || (p.Scheme != "http" && p.Scheme != "https") || p.RawQuery != "" || p.Fragment != "" || (p.Path != "" && p.Path != "/") {
		return nil, newTextError("link-errors.proxy_invalid", nil)
	}
	t, err := url.Parse(target)
	if err != nil {
		return nil, newTextError("link-errors.proxy_invalid_target", nil)
	}
	noProxy := os.Getenv("NO_PROXY")
	if noProxy == "" {
		noProxy = os.Getenv("no_proxy")
	}
	cfg := &httpproxy.Config{HTTPSProxy: raw, NoProxy: noProxy}
	selected, err := cfg.ProxyFunc()(t)
	if err != nil {
		return nil, newTextError("link-errors.proxy_invalid", nil)
	}
	if selected == nil {
		return nil, nil
	}
	return p, nil
}

// proxyDial makes the CONNECT tunnel itself so the proxy's HTTPS certificate uses
// system trust independently from the hub's pinned TLS configuration.
func proxyDial(p *url.URL, onConn func(net.Conn)) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		port := p.Port()
		if port == "" {
			if p.Scheme == "https" {
				port = "443"
			} else {
				port = "80"
			}
		}
		d := net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second}
		raw, err := d.DialContext(ctx, "tcp", net.JoinHostPort(p.Hostname(), port))
		if err != nil {
			return nil, newTextError("link-errors.proxy_connection_failed", nil)
		}
		conn := raw
		ok := false
		defer func() {
			if !ok {
				raw.Close()
			}
		}()
		deadline := time.Now().Add(dialTimeout)
		if d, has := ctx.Deadline(); has && d.Before(deadline) {
			deadline = d
		}
		_ = conn.SetDeadline(deadline)
		if p.Scheme == "https" {
			t := tls.Client(conn, &tls.Config{MinVersion: tls.VersionTLS12, ServerName: p.Hostname(), RootCAs: systemRoots.Load()})
			if err := t.HandshakeContext(ctx); err != nil {
				return nil, newTextError("link-errors.proxy_tls_failed", nil)
			}
			conn = t
		}
		req := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: addr}, Host: addr, Header: make(http.Header)}
		if p.User != nil {
			password, _ := p.User.Password()
			req.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(p.User.Username()+":"+password)))
		}
		if err := req.Write(conn); err != nil {
			return nil, newTextError("link-errors.proxy_connect_failed", nil)
		}
		br := bufio.NewReader(conn)
		resp, err := http.ReadResponse(br, req)
		if err != nil {
			return nil, newTextError("link-errors.proxy_connect_failed", nil)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, newTextError("link-errors.proxy_rejected", nil, i18n.Args{"status": resp.StatusCode})
		}
		if br.Buffered() != 0 {
			return nil, newTextError("link-errors.proxy_connect_failed", nil)
		}
		_ = conn.SetDeadline(time.Time{})
		if onConn != nil {
			onConn(raw)
		}
		ok = true
		return conn, nil
	}
}

// RedactedProxy removes all user information, including the username.
func RedactedProxy(raw string) string {
	if raw == "" || raw == "off" {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	u.User = nil
	return u.String()
}

// SetProxy validates and saves the proxy; off suppresses environment proxies.
func SetProxy(path, raw string) error {
	raw = strings.TrimSpace(raw)
	if raw != "off" {
		if raw == "" {
			return newTextError("link-errors.proxy_invalid", nil)
		}
		c := &Config{Proxy: raw}
		if _, err := proxyURL(c, "https://example.com:443"); err != nil {
			return err
		}
	}
	return Update(path, func(c *Config) error { c.Proxy = raw; return nil })
}
