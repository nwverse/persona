package hub

import (
	"context"
	"crypto/tls"
	"errors"
	"github.com/nwverse/persona/server/internal/compat"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/link"
)

// HTTP between hubs over their links (docs/server/mesh.md "Members forward Apps"). Each stream a hub opens on a link is one HTTP/1.1
// connection to the other hub's peer server (no socket: it only takes streams from authenticated links). Two kinds
// of request travel this way, both passed on hop by hop toward where they go (TTL 8):
//   - an app's request a member got: everything under /api/ but what the member answers itself (its health, persona-link,
//     the links between hubs, its admin page) goes to the center as it came, Authorization included. The member has
//     judged where it came from (LAN or internet, TLS, a source blocked for failed credentials) and says so in
//     X-Persona-Peer-Source; the center takes its word (the member is paired) and serves it like its own app's.
//   - a node stream the center asks a member for (peer_node.go).

const (
	hdrPeerTTL    = "X-Persona-Peer-Ttl"
	hdrPeerSource = "X-Persona-Peer-Source" // a forwarded app request: "<lan|public> <client address> <tls|plain>"
	hdrPeerNode   = "X-Persona-Peer-Node"   // a node stream: "<hub_id>/<machine id>", asked by hdrPeerFrom
	hdrPeerFrom   = "X-Persona-Peer-From"
)

type meshTTLKey struct{}

// forwardedKey marks a request a member passed on: the source it judged (ingress.go reads it).
type forwardedKey struct{}

type forwardedSource struct {
	src source
	ip  netip.Addr
	tls bool
}

func forwardedOf(r *http.Request) (forwardedSource, bool) {
	f, ok := r.Context().Value(forwardedKey{}).(forwardedSource)
	return f, ok
}

// peerServer serves the streams other hubs open: each is one HTTP connection to this hub.
type peerServer struct {
	ln  *chanListener
	srv *http.Server
}

type chanListener struct {
	ch   chan net.Conn
	done chan struct{}
	once sync.Once
}

func (l *chanListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.ch:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *chanListener) Close() error {
	l.once.Do(func() { close(l.done) })
	return nil
}

func (l *chanListener) Addr() net.Addr { return pipeAddr{} }

type pipeAddr struct{}

func (pipeAddr) Network() string { return "pipe" }
func (pipeAddr) String() string  { return "peer" }

func (h *Hub) startPeerServer(ctx context.Context) {
	ms := &h.mesh
	ms.mu.Lock()
	if ms.server != nil {
		ms.mu.Unlock()
		return
	}
	ps := &peerServer{ln: &chanListener{ch: make(chan net.Conn), done: make(chan struct{})}}
	ps.srv = &http.Server{Handler: http.HandlerFunc(h.servePeerHTTP), ReadHeaderTimeout: 30 * time.Second, IdleTimeout: 2 * time.Minute,
		BaseContext: func(net.Listener) context.Context { return context.WithoutCancel(ctx) }}
	ms.server = ps
	ms.mu.Unlock()
	go func() { _ = ps.srv.Serve(ps.ln) }()
	go func() {
		<-ctx.Done()
		_ = ps.srv.Close()
	}()
}

// acceptPeerStream takes a stream the other hub opened: one connection to this hub's peer server.
func (h *Hub) acceptPeerStream(l *peerLink, id string) error {
	ms := &h.mesh
	ms.mu.Lock()
	ps := ms.server
	ms.mu.Unlock()
	if ps == nil {
		return errors.New("not serving other hubs")
	}
	if err := l.mux.Open(id); err != nil {
		return err
	}
	mine, theirs := net.Pipe()
	l.mux.Start(id, theirs)
	select {
	case ps.ln.ch <- mine:
		return nil
	case <-ps.ln.done:
	case <-time.After(5 * time.Second):
	}
	_ = mine.Close()
	return errors.New("not serving other hubs")
}

// dialStream opens a stream to the other hub's peer server.
func (l *peerLink) dialStream(ctx context.Context) (net.Conn, error) {
	side := "a"
	if l.dialer == l.h.hubID() {
		side = "d"
	}
	id := side + itoa(int(l.streamN.Add(1))) + "_" + randomHex(4)
	if err := l.mux.Open(id); err != nil {
		return nil, err
	}
	if err := l.rpc.Call(ctx, "stream.open", map[string]any{"id": id}, nil); err != nil {
		l.mux.Abort(id, err)
		return nil, err
	}
	mine, theirs := net.Pipe()
	l.mux.Start(id, theirs)
	return mine, nil
}

// setupPeerHTTP gives a link its HTTP client: a transport whose connections are streams on it, and a proxy passing
// requests on as they are (the hop's own headers set by whoever sends them).
func (h *Hub) setupPeerHTTP(l *peerLink) {
	l.transport = &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return l.dialStream(ctx)
		},
		MaxIdleConnsPerHost: 8,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  true,
	}
	l.proxy = &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme, pr.Out.URL.Host, pr.Out.Host = "http", "peer", "peer"
			compat.SetHeader(pr.Out.Header, hdrPeerTTL, itoa(meshTTLOf(pr.In.Context())-1))
			// The member's judgment of the source is the one that counts: never what a client wrote there.
			pr.Out.Header.Del("X-Forwarded-For")
		},
		Transport:     l.transport,
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if r.Context().Err() != nil {
				return
			}
			writeCode(w, http.StatusBadGateway, "hub_unreachable", i18n.T(h.requestLang(r), "hub-peer_http.forward_failed", i18n.Args{"err": err.Error()}), nil)
		},
	}
}

func meshTTLOf(ctx context.Context) int {
	if n, ok := ctx.Value(meshTTLKey{}).(int); ok {
		return n
	}
	return meshTTL
}

// servePeerHTTP serves a request another hub passed on. It only ever comes over a link (the streams' listener is no
// socket), so its headers are that hub's word.
func (h *Hub) servePeerHTTP(w http.ResponseWriter, r *http.Request) {
	ttl := atoi(compat.Header(r.Header, hdrPeerTTL))
	if ttl <= 0 {
		writeCode(w, http.StatusLoopDetected, "mesh_loop", i18n.T(h.requestLang(r), "hub-peer_http.loop"), nil)
		return
	}
	r = r.WithContext(context.WithValue(r.Context(), meshTTLKey{}, ttl))
	if target := compat.Header(r.Header, hdrPeerNode); target != "" {
		// A node stream starts at the hub in hdrPeerFrom and goes away from it. One that arrives there either bounced back
		// while the routes change, and its opener tries again, or is a client's, passed on by a member from before
		// memberForward dropped these. The two look the same, and neither should open.
		if compat.Header(r.Header, hdrPeerFrom) == h.hubID() {
			writeCode(w, http.StatusLoopDetected, "mesh_loop", i18n.T(h.requestLang(r), "hub-peer_http.loop"), nil)
			return
		}
		hub, mid, _ := strings.Cut(target, "/")
		if hub != h.hubID() {
			if !h.meshForwardTo(w, r, hub) {
				writeCode(w, http.StatusServiceUnavailable, "hub_unreachable", i18n.T(h.requestLang(r), "hub-peer_http.unreachable"), nil)
			}
			return
		}
		h.serveNodeOpen(w, r, compat.Header(r.Header, hdrPeerFrom), mid)
		return
	}
	fs, ok := parseForwardedSource(compat.Header(r.Header, hdrPeerSource))
	if !ok || !strings.HasPrefix(r.URL.Path, "/api/") {
		writeErr(w, http.StatusBadRequest, errors.New("not a request another hub may pass on"))
		return
	}
	if !h.isCenter() {
		h.forwardToCenter(w, r) // on the way to the center
		return
	}
	api, _ := h.mesh.centerAPI.Load().(http.Handler)
	if api == nil {
		writeErr(w, http.StatusServiceUnavailable, errors.New("not serving yet"))
		return
	}
	compat.DelHeader(r.Header, hdrPeerSource)
	compat.DelHeader(r.Header, hdrPeerTTL)
	r.RemoteAddr = net.JoinHostPort(fs.ip.String(), "0")
	if fs.tls {
		r.TLS = &tls.ConnectionState{HandshakeComplete: true} // it reached the member over TLS
	}
	r = r.WithContext(context.WithValue(r.Context(), forwardedKey{}, fs))
	api.ServeHTTP(w, r)
}

func parseForwardedSource(v string) (forwardedSource, bool) {
	f := strings.Fields(v)
	if len(f) != 3 {
		return forwardedSource{}, false
	}
	var fs forwardedSource
	switch f[0] {
	case "lan":
		fs.src = sourceLAN
	case "public":
		fs.src = sourcePublic
	default:
		return forwardedSource{}, false
	}
	ip, err := netip.ParseAddr(f[1])
	if err != nil {
		return forwardedSource{}, false
	}
	fs.ip, fs.tls = ip, f[2] == "tls"
	return fs, true
}

// meshForwardTo passes the request on toward hub `to`; false (nothing written) when there is no way there.
func (h *Hub) meshForwardTo(w http.ResponseWriter, r *http.Request, to string) bool {
	l := h.nextLink(to)
	if l == nil || l.proxy == nil {
		return false
	}
	if meshTTLOf(r.Context()) <= 1 {
		writeCode(w, http.StatusLoopDetected, "mesh_loop", i18n.T(h.requestLang(r), "hub-peer_http.loop"), nil)
		return true
	}
	l.proxy.ServeHTTP(w, r)
	return true
}

// servedByMember: what a member answers itself rather than passing on to the center: its health, its computers'
// persona-links (and pairing them: the codes of `personad hub pair --machine`), the links between hubs, its admin page,
// and pairing codes made on its machine (an app's comes from the center: pairing.go).
func (h *Hub) servedByMember(r *http.Request) bool {
	switch r.URL.Path {
	case "/api/v1/health", link.Path, link.StreamPath, peerPath, peerPairPath, "/api/v1/auth/pair-codes":
		return true
	case "/api/v1/auth/tokens":
		// A persona-link trading this hub's code for its credential (a code made for one of its link machines).
		tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if strings.HasPrefix(tok, "jp_") && h.store != nil {
			if pt, err := h.store.PairTokenByHash(r.Context(), hashToken(tok)); err == nil && pt != nil && pt.MachineID != "" {
				return true
			}
		}
		return false
	}
	return strings.HasPrefix(r.URL.Path, "/api/v1/admin/")
}

// memberForward passes an app's request on to the center while this hub is a member: after the gate (plaintext from
// the internet was refused there), with this hub's judgment of the source; a source blocked for failed credentials is
// refused here, and the center's 401s count against it here.
func (h *Hub) memberForward(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.isMember() || h.servedByMember(r) {
			next.ServeHTTP(w, r)
			return
		}
		src := h.sourceOf(r)
		public := src == sourcePublic
		if public {
			if retry := h.devtok.failures.retry(authSourceKey(r), time.Now(), h.cfg.Security); retry > 0 {
				retryAfter(w, retry)
				h.securityError(w, r, http.StatusTooManyRequests, "rate_limited", 0)
				return
			}
		}
		kind := "lan"
		if public {
			kind = "public"
		}
		ip := remoteIP(r)
		if !ip.IsValid() {
			ip = netip.IPv4Unspecified()
		}
		tlsWord := "plain"
		if r.TLS != nil {
			tlsWord = "tls"
		}
		r2 := r.Clone(r.Context())
		// A hub's word, never a client's: with these the center would open a node stream without asking for a credential.
		compat.DelHeader(r2.Header, hdrPeerNode)
		compat.DelHeader(r2.Header, hdrPeerFrom)
		compat.SetHeader(r2.Header, hdrPeerSource, kind+" "+ip.Unmap().String()+" "+tlsWord)
		cw := &codeWriter{ResponseWriter: w}
		done := h.trackProxied(r2)
		defer done()
		h.forwardToCenter(cw, r2)
		if public && cw.code == http.StatusUnauthorized {
			h.devtok.failures.fail(authSourceKey(r), time.Now(), h.cfg.Security)
		}
	})
}

// forwardToCenter passes a request on toward the center, or answers 503 center_unreachable.
func (h *Hub) forwardToCenter(w http.ResponseWriter, r *http.Request) {
	c := h.centerID()
	if h.meshForwardTo(w, r, c) {
		return
	}
	name := h.hubNameOf(c)
	if name == "" {
		name = shortID(c)
	}
	writeCode(w, http.StatusServiceUnavailable, "center_unreachable", i18n.T(h.requestLang(r), "hub-peer_http.center_unreachable", i18n.Args{"name": name}),
		map[string]any{"center": map[string]any{"hub_id": c, "name": name}})
}

// codeWriter remembers the status written (and lets WebSocket upgrades through).
type codeWriter struct {
	http.ResponseWriter
	code int
}

func (c *codeWriter) WriteHeader(code int) {
	if c.code == 0 {
		c.code = code
	}
	c.ResponseWriter.WriteHeader(code)
}

func (c *codeWriter) Write(b []byte) (int, error) {
	if c.code == 0 {
		c.code = http.StatusOK
	}
	return c.ResponseWriter.Write(b)
}

func (c *codeWriter) Unwrap() http.ResponseWriter { return c.ResponseWriter }

func (c *codeWriter) Flush() {
	if f, ok := c.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// trackProxied keeps a passed-on request (an app's WebSocket above all) so it can be cut when the center changes:
// the app reconnects and reaches the new one.
func (h *Hub) trackProxied(r *http.Request) func() {
	ctx, cancel := context.WithCancel(r.Context())
	*r = *r.WithContext(ctx)
	ms := &h.mesh
	ms.mu.Lock()
	if ms.proxied == nil {
		ms.proxied = map[*context.CancelFunc]struct{}{}
	}
	key := &cancel
	ms.proxied[key] = struct{}{}
	ms.mu.Unlock()
	return func() {
		ms.mu.Lock()
		delete(ms.proxied, key)
		ms.mu.Unlock()
		cancel()
	}
}

// dropProxiedApps cuts every request this hub is passing on (the center changed).
func (h *Hub) dropProxiedApps() {
	ms := &h.mesh
	ms.mu.Lock()
	var cancels []context.CancelFunc
	for k := range ms.proxied {
		cancels = append(cancels, *k)
	}
	ms.mu.Unlock()
	for _, c := range cancels {
		c()
	}
}
