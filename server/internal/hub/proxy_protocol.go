package hub

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/proxyproto"
)

// ProxyProtocolConfig is security.proxy_protocol (docs/operations/network.md "PROXY protocol"): the hub sits behind a TCP proxy
// or port forward that passes each client's address on with the PROXY protocol, so that sourceOf, the admin UI's
// scope, rate limits and logs see the client rather than the proxy (on the same machine: loopback, trusted with
// everything). Unset, no header is read anywhere, as before.
type ProxyProtocolConfig struct {
	// From are the upstreams, addresses or CIDRs, as the hub sees them connect (the same machine: 127.0.0.1/32).
	// Every connection from them must start with a header; nobody else's header is read.
	From []string `json:"from,omitempty"`
	// Listeners are where: "tls" (public.tls_listen; the default) and "http" (listen). "http" is refused with a
	// loopback upstream: the hub's own tools (hub pair, hub admin, plugins) call that port over loopback, headerless.
	Listeners []string `json:"listeners,omitempty"`
	// Public applies public ingress rules to every relayed client, regardless of its address.
	Public bool `json:"public,omitempty"`
}

const (
	proxyOnTLS  = "tls"
	proxyOnHTTP = "http"
)

// proxyPolicy is security.proxy_protocol checked; nil when it is not set.
type proxyPolicy struct {
	from      []netip.Prefix
	tls, http bool
	public    bool
}

func (c *ProxyProtocolConfig) policy() (*proxyPolicy, error) {
	if c == nil {
		return nil, nil
	}
	from, err := parseCIDRs(c.From)
	if err != nil {
		return nil, fmt.Errorf("security.proxy_protocol.from: %w", err)
	}
	if len(from) == 0 {
		return nil, errors.New("security.proxy_protocol.from is empty: list the addresses the proxy connects to the hub from, e.g. 127.0.0.1/32")
	}
	p := &proxyPolicy{from: from, public: c.Public}
	for _, prefix := range from {
		if prefix.Bits() == 0 {
			return nil, fmt.Errorf("security.proxy_protocol.from: %s would let anyone name any address, loopback included: list the proxy's own address", prefix)
		}
	}
	listeners := c.Listeners
	if len(listeners) == 0 {
		listeners = []string{proxyOnTLS}
	}
	for _, l := range listeners {
		switch strings.ToLower(strings.TrimSpace(l)) {
		case proxyOnTLS:
			p.tls = true
		case proxyOnHTTP:
			p.http = true
		default:
			return nil, fmt.Errorf("security.proxy_protocol.listeners: %q is not tls or http", l)
		}
	}
	if p.http && p.coversLoopback() {
		return nil, errors.New(`security.proxy_protocol: "http" in listeners with a loopback address in from would refuse the hub's own tools (hub pair, hub admin, plugins call the HTTP port over loopback, without a header): the proxy belongs in front of the TLS port`)
	}
	return p, nil
}

func (p *proxyPolicy) coversLoopback() bool {
	for _, prefix := range p.from {
		if prefix.Overlaps(netip.MustParsePrefix("127.0.0.0/8")) || prefix.Contains(netip.IPv6Loopback()) {
			return true
		}
	}
	return false
}

func (p *proxyPolicy) String() string {
	var on []string
	if p.tls {
		on = append(on, "the TLS listener")
	}
	if p.http {
		on = append(on, "the HTTP listener")
	}
	from := make([]string, len(p.from))
	for i, prefix := range p.from {
		from[i] = prefix.String()
	}
	return fmt.Sprintf("on %s, from %s", strings.Join(on, " and "), strings.Join(from, ", "))
}

// proxyListener wraps ln to read PROXY headers when security.proxy_protocol applies to it (on: the policy's tls or
// http); ln as it is otherwise. The socket systemd passed (listen.go) is wrapped the same way as one the hub opened.
func (h *Hub) proxyListener(ln net.Listener, on func(*proxyPolicy) bool) net.Listener {
	p := h.public.proxy
	if p == nil || !on(p) {
		return ln
	}
	return &proxyproto.Listener{Listener: ln, Trusted: p.from, RejectLocal: p.public, Report: h.proxyReport}
}

type publicProxyKey struct{}

// publicProxyConnContext runs in the accept loop. The wrapper identifies the trusted upstream without reading its
// header here. The connection itself validates the header before any request can be served.
func publicProxyConnContext(ctx context.Context, conn net.Conn) context.Context {
	if tlsConn, ok := conn.(*tls.Conn); ok {
		conn = tlsConn.NetConn()
	}
	if _, ok := conn.(*proxyproto.Conn); ok {
		return context.WithValue(ctx, publicProxyKey{}, true)
	}
	return ctx
}

func fromPublicProxy(r *http.Request) bool {
	public, _ := r.Context().Value(publicProxyKey{}).(bool)
	return public
}

// proxyLog keeps the log readable when a proxy keeps sending connections without a header (it was not switched on
// there, or something bypasses it): the first refusal is logged, then at most one line a minute with a count.
type proxyLog struct {
	mu         sync.Mutex
	last       time.Time
	suppressed int
	loopback   bool
}

const proxyLogEvery = time.Minute

func (h *Hub) proxyReport(peer netip.AddrPort, header proxyproto.Header, err error) {
	l := &h.public.proxyLog
	l.mu.Lock()
	defer l.mu.Unlock()
	if err != nil {
		now := time.Now()
		if !l.last.IsZero() && now.Sub(l.last) < proxyLogEvery {
			l.suppressed++
			return
		}
		more := ""
		if l.suppressed > 0 {
			more = fmt.Sprintf(" (and %d more since %s)", l.suppressed, l.last.Format(time.TimeOnly))
		}
		l.last, l.suppressed = now, 0
		h.logf("proxy protocol: refused a connection from %s: %v; every connection from security.proxy_protocol.from must start with a PROXY header%s", peer.Addr(), err, more)
		return
	}
	if !header.Local && header.Source.Addr().Unmap().IsLoopback() && !l.loopback {
		// Once: the proxy itself got the connection from loopback, e.g. from a tunnel's client on this machine that
		// does not pass the address on, which leaves public connections as local as without the header.
		l.loopback = true
		h.logf("proxy protocol: %s passed on a connection from loopback (%s); if it came from the internet, a proxy or tunnel before it does not pass the client's address on (docs/operations/network.md「PROXY protocol」)", peer.Addr(), header.Source.Addr())
	}
}
