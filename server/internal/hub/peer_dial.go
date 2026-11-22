package hub

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/nwverse/persona/server/internal/i18n"
)

// Dialing a peer (docs/server/mesh.md "Mesh links"): every paired hub is dialed until there is a link to it, whichever side made
// it. The hub with the larger hub_id waits a moment before each try so the two rarely connect at once. Candidates are
// tried as persona-link does: LAN first, then public IPv6, names, public IPv4, one every 300 ms, the first that works
// wins; the TLS key must be the one pinned at pairing. http:// candidates (mesh.plaintext, peer_plain.go) come after
// the https ones, and only at allowed addresses. Backoff: 1 s doubling to a minute, randomized; 5 minutes after
// the other hub refused the credential. These are settings, not measurements.

const (
	peerStagger     = 300 * time.Millisecond
	peerDialTimeout = 8 * time.Second
	peerRefusedWait = 5 * time.Minute
)

var errPeerPin = i18n.Err("hub-peer_dial.pin_mismatch")

type peerDialer struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// peerDialState is how dialing a peer went, for the admin page.
type peerDialState struct {
	Error string
	At    time.Time
	// Refused: the other hub does not take this hub's credential (it unpaired, most likely).
	Refused bool
}

func (h *Hub) startDialer(hubID string) {
	ms := &h.mesh
	ms.mu.Lock()
	if !ms.running || ms.ctx == nil || ms.dialers[hubID] != nil {
		ms.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(ms.ctx)
	d := &peerDialer{cancel: cancel, done: make(chan struct{})}
	ms.dialers[hubID] = d
	ms.mu.Unlock()
	go func() {
		defer close(d.done)
		h.dialLoop(ctx, hubID)
	}()
}

func (h *Hub) stopDialer(hubID string) {
	ms := &h.mesh
	ms.mu.Lock()
	d := ms.dialers[hubID]
	delete(ms.dialers, hubID)
	ms.mu.Unlock()
	if d != nil {
		d.cancel()
	}
}

func (h *Hub) setDialState(hubID string, st peerDialState) {
	ms := &h.mesh
	ms.mu.Lock()
	if ms.dialErr == nil {
		ms.dialErr = map[string]peerDialState{}
	}
	ms.dialErr[hubID] = st
	ms.mu.Unlock()
}

type peerBackoff struct{ n int }

func (b *peerBackoff) next() time.Duration {
	d := min(time.Second<<b.n, time.Minute)
	if d < time.Minute {
		b.n++
	}
	return d/2 + rand.N(d/2+1)
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func (h *Hub) dialLoop(ctx context.Context, hubID string) {
	var b peerBackoff
	self := h.hubID()
	for ctx.Err() == nil {
		if h.peerLinkTo(hubID) != nil {
			select {
			case <-ctx.Done():
				return
			case <-h.meshWait():
			case <-time.After(5 * time.Second):
			}
			continue
		}
		if self > hubID && !sleepCtx(ctx, 500*time.Millisecond+rand.N(1500*time.Millisecond)) {
			return
		}
		if h.peerLinkTo(hubID) != nil {
			continue
		}
		ms := &h.mesh
		ms.mu.Lock()
		row, ok := ms.peers[hubID]
		ms.mu.Unlock()
		if !ok {
			return
		}
		token, err := h.openKey(row.Token)
		if err != nil {
			h.setDialState(hubID, peerDialState{Error: i18n.T(h.userLang(), "hub-peer_dial.credentials_unreadable", i18n.Args{"err": err.Error()}), At: time.Now()})
			sleepCtx(ctx, peerRefusedWait)
			continue
		}
		conn, address, err := h.dialPeer(ctx, row.FP, token, h.peerEndpoints(hubID))
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			wait := b.next()
			st := peerDialState{Error: i18n.Localize(err, h.userLang()), At: time.Now()}
			var he *peerHTTPError
			if errors.As(err, &he) && (he.status == http.StatusUnauthorized || he.status == http.StatusForbidden) {
				st.Refused, st.Error, wait = true, i18n.T(h.userLang(), "hub-peer_dial.refused"), peerRefusedWait
			}
			h.setDialState(hubID, st)
			if !sleepCtx(ctx, wait) {
				return
			}
			continue
		}
		h.setDialState(hubID, peerDialState{At: time.Now()})
		start := time.Now()
		err = h.runPeerLink(ctx, conn, hubID, self, address, "")
		if time.Since(start) > time.Minute {
			b = peerBackoff{}
		}
		if ctx.Err() != nil {
			return
		}
		switch websocket.CloseStatus(err) {
		case peerCloseUnpaired:
			h.setDialState(hubID, peerDialState{Error: i18n.T(h.userLang(), "hub-peer_dial.unpaired"), At: time.Now(), Refused: true})
			sleepCtx(ctx, peerRefusedWait)
			continue
		}
		if errors.Is(err, errDuplicateLink) || websocket.CloseStatus(err) == peerCloseDuplicate {
			sleepCtx(ctx, time.Second) // the other link stays: wait until it is gone (top of the loop)
			continue
		}
		h.setDialState(hubID, peerDialState{Error: err.Error(), At: time.Now()})
		if !sleepCtx(ctx, b.next()) {
			return
		}
	}
}

// peerEndpoints are a peer's candidates: what its newest record says, then what pairing (or the last link) saved.
func (h *Hub) peerEndpoints(hubID string) []string {
	ms := &h.mesh
	ms.mu.Lock()
	var out []string
	if r := ms.records[hubID]; r != nil {
		out = append(append(out, r.Endpoints...), r.PlainEndpoints...)
	}
	if p, ok := ms.peers[hubID]; ok {
		out = append(out, p.Endpoints...)
	}
	ms.mu.Unlock()
	return h.peerDialable(rankEndpoints(out))
}

func endpointKindOf(raw string) int {
	u, err := url.Parse(raw)
	if err != nil {
		return 2
	}
	ip, err := netip.ParseAddr(u.Hostname())
	if err != nil {
		return 2 // a name
	}
	switch ip = ip.Unmap(); {
	case ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLoopback():
		return 0
	case ip.Is6():
		return 1
	}
	return 3
}

// rankEndpoints keeps the https and http candidates, without duplicates: https first, LAN first within each. Whether
// an http one may be dialed is peerDialable's to say.
func rankEndpoints(eps []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, e := range eps {
		e = strings.TrimRight(strings.TrimSpace(e), "/")
		if !strings.HasPrefix(e, "https://") && !isPlainEndpoint(e) || seen[e] {
			continue
		}
		seen[e] = true
		out = append(out, e)
	}
	rank := func(e string) int {
		if isPlainEndpoint(e) {
			return 10 + endpointKindOf(e)
		}
		return endpointKindOf(e)
	}
	slices.SortStableFunc(out, func(a, b string) int { return rank(a) - rank(b) })
	return out
}

type peerHTTPError struct{ status int }

func (e *peerHTTPError) Error() string { return fmt.Sprintf("HTTP %d", e.status) }

// peerClient is an HTTP/1.1 client that only talks to the hub whose TLS key hashes to fp (link.go's pinning) over
// https, and over http only to addresses mesh.plaintext.allow lists (plainPeerDial).
func (h *Hub) peerClient(fp string) *http.Client {
	d := &net.Dialer{Timeout: peerDialTimeout, KeepAlive: 30 * time.Second}
	return &http.Client{
		Transport: &http.Transport{
			Proxy: nil,
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return h.plainPeerDial(ctx, d, network, addr)
			},
			DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				raw, err := d.DialContext(ctx, network, addr)
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

// raceEndpoints tries the candidates best first, one every peerStagger, and keeps the first that works.
func raceEndpoints[T any](ctx context.Context, eps []string, try func(context.Context, string) (T, error), drop func(T)) (T, string, error) {
	var zero T
	if len(eps) == 0 {
		return zero, "", i18n.Err("hub-peer_dial.no_endpoints")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type result struct {
		v   T
		ep  string
		err error
	}
	results := make(chan result, len(eps))
	for i, ep := range eps {
		go func() {
			select {
			case <-time.After(time.Duration(i) * peerStagger):
			case <-ctx.Done():
				results <- result{ep: ep, err: ctx.Err()}
				return
			}
			actx, acancel := context.WithTimeout(ctx, peerDialTimeout)
			defer acancel()
			v, err := try(actx, ep)
			results <- result{v, ep, err}
		}()
	}
	var errs []error
	for left := len(eps); left > 0; left-- {
		r := <-results
		if r.err == nil {
			go func(n int) {
				for ; n > 0; n-- {
					if late := <-results; late.err == nil {
						drop(late.v)
					}
				}
			}(left - 1)
			return r.v, r.ep, nil
		}
		if !errors.Is(r.err, context.Canceled) {
			errs = append(errs, fmt.Errorf("%s: %w", r.ep, r.err))
		}
	}
	if len(errs) == 0 { // every try was cancelled: never a nil error without a value
		err := context.Cause(ctx)
		if err == nil {
			err = context.Canceled
		}
		return zero, "", err
	}
	return zero, "", errors.Join(errs...)
}

func peerWSURL(base, path string) string {
	if rest, ok := strings.CutPrefix(base, "http://"); ok {
		return "ws://" + rest + path
	}
	return "wss" + strings.TrimPrefix(base, "https") + path
}

// dialPeer opens a link to a peer: the WebSocket only, the hello is runPeerLink's.
func (h *Hub) dialPeer(ctx context.Context, fp, token string, eps []string) (*websocket.Conn, string, error) {
	client := h.peerClient(fp)
	return raceEndpoints(ctx, eps, func(ctx context.Context, ep string) (*websocket.Conn, error) {
		conn, resp, err := websocket.Dial(ctx, peerWSURL(ep, peerPath), &websocket.DialOptions{HTTPClient: client,
			HTTPHeader: http.Header{"Authorization": {"Bearer " + token}}, CompressionMode: websocket.CompressionContextTakeover})
		if err != nil {
			if resp != nil && resp.StatusCode != http.StatusSwitchingProtocols {
				return nil, &peerHTTPError{status: resp.StatusCode}
			}
			return nil, err
		}
		return conn, nil
	}, func(c *websocket.Conn) { _ = c.CloseNow() })
}

// servePeer is GET /api/v1/peer: a paired hub's link, over TLS (or plaintext from an address mesh.plaintext.allow
// lists), with the credential this hub issued it.
func (h *Hub) servePeer(w http.ResponseWriter, r *http.Request) {
	p := principalOf(r.Context())
	if p.Role != rolePeer || p.Machine == "" {
		writeErr(w, http.StatusForbidden, errors.New("only a peer credential opens a mesh link"))
		return
	}
	if r.TLS == nil && !h.plainPeerRequest(r) {
		h.securityError(w, r, http.StatusForbidden, "tls_required", 0)
		return
	}
	ms := &h.mesh
	ms.mu.Lock()
	_, paired := ms.peers[p.Machine]
	running := ms.running
	ms.mu.Unlock()
	if !running {
		writeErr(w, http.StatusServiceUnavailable, errors.New("hub stopping"))
		return
	}
	if !paired {
		writeErr(w, http.StatusForbidden, errors.New("not paired with that hub"))
		return
	}
	if !h.enterWS() {
		http.Error(w, "hub stopping", http.StatusServiceUnavailable)
		return
	}
	defer h.wsHandlers.Done()
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true,
		CompressionMode: websocket.CompressionContextTakeover})
	if err != nil {
		return
	}
	_ = h.runPeerLink(r.Context(), conn, p.Machine, p.Machine, maskedSource(r), p.ID)
}
