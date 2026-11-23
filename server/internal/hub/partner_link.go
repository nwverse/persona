package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/link"
	"github.com/nwverse/persona/server/internal/rpc"
)

// A link between two partner hubs (docs/server/partner-hubs.md "The link"): a WebSocket on the TLS port of the hub
// that was dialed, pinned to its key, with the credential it issued. Either hub may dial (or only one: the other's
// dial is never); of two links between the same hubs the one dialed by the smaller hub_id stays. On it runs the node
// protocol's RPC (internal/rpc) with a dispatcher of its own and three calls, nothing else: hello (each side once),
// msg (a manager's envelope, partner_bus.go) and unpair. Heartbeats follow persona-link (link.PingEvery,
// link.PingTimeout).

// Close codes on a partner link besides the standard ones (1001: the hub is stopping), the numbers of a mesh link's.
const (
	partnerCloseDuplicate websocket.StatusCode = 4011 // the two hubs have another link: this one is not kept
	partnerCloseUnpaired  websocket.StatusCode = 4012 // the pairing ended (its credential revoked)
	partnerCloseSlow      websocket.StatusCode = 4013 // the partner has more calls running here than this hub takes
)

// partnerProto is the version of the calls on a partner link.
const partnerProto = 1

// partnerLinkCalls is the most calls a partner may have running on this hub at once (its manager sends one envelope
// at a time, and status envelopes): past it the link is closed (4013), and the partner dials again.
const partnerLinkCalls = 64

// errPartnerNotLinked: the link was not kept because the pairing ended meanwhile, or the hub is stopping.
var errPartnerNotLinked = errors.New("not paired any more, or the hub is stopping")

type partnerLink struct {
	h       *Hub
	hubID   string // the other hub
	dialer  string // the hub that dialed this link
	address string // where it was dialed to (and how), or (accepted) the masked source
	url     string // dialed links: the partner's address that answered
	tokenID string // accepted links: the credential the other hub came with
	conn    *websocket.Conn
	rpc     *rpc.Peer
	ctx     context.Context
	cancel  context.CancelCauseFunc
	since   time.Time
	rtt     atomic.Int64 // the last ping round trip, ns
	calls   atomic.Int64 // the partner's calls running here
	every   time.Duration
	timeout time.Duration
	info    partnerHello // what the other hub said about itself

	closeOnce sync.Once
}

// partnerHello is the first call each way on a link: who the hub is, and its key and addresses for the other's dialing.
type partnerHello struct {
	HubID     string   `json:"hub_id"`
	Name      string   `json:"name"`
	Version   string   `json:"version,omitempty"`
	Proto     int      `json:"proto"`
	FP        string   `json:"fp,omitempty"`
	Endpoints []string `json:"endpoints,omitempty"` // https only
}

func (h *Hub) partnerHello() partnerHello {
	info := h.selfPartnerInfo()
	return partnerHello{HubID: info.HubID, Name: info.Name, Version: h.version, Proto: partnerProto, FP: info.FP, Endpoints: info.Endpoints}
}

func (l *partnerLink) rttMS() int {
	if d := l.rtt.Load(); d > 0 {
		return max(1, int(time.Duration(d).Milliseconds()))
	}
	return 0
}

// close ends the link with a close frame saying why.
func (l *partnerLink) close(code websocket.StatusCode, reason string) {
	l.closeOnce.Do(func() {
		l.cancel(errors.New(reason))
		go func() { _ = l.conn.Close(code, reason) }()
	})
}

func (l *partnerLink) keepAlive() {
	t := time.NewTicker(l.every)
	defer t.Stop()
	for {
		select {
		case <-l.ctx.Done():
			return
		case <-t.C:
			ctx, cancel := context.WithTimeout(context.Background(), l.timeout) // not l.ctx: see link.go send
			start := time.Now()
			err := l.conn.Ping(ctx)
			cancel()
			if err != nil {
				if l.ctx.Err() == nil {
					l.cancel(fmt.Errorf("no pong within %s", l.timeout))
					_ = l.conn.CloseNow()
				}
				return
			}
			l.rtt.Store(int64(time.Since(start)))
		}
	}
}

// runPartnerLink serves one link until it ends, and says why. url is the partner's address a dialed link reached.
func (h *Hub) runPartnerLink(parent context.Context, conn *websocket.Conn, hubID, dialer, address, url, tokenID string) error {
	conn.SetReadLimit(1 << 20) // an envelope's text is at most 64 KiB (partner_bus.go)
	ctx, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
	// The byte stream is not bound to ctx: a link closed with a reason must still get its close frame out.
	nctx, ncancel := context.WithCancel(context.Background())
	defer ncancel()
	l := &partnerLink{h: h, hubID: hubID, dialer: dialer, address: address, url: url, tokenID: tokenID, conn: conn,
		ctx: ctx, cancel: cancel, since: time.Now().UTC(), every: link.PingEvery, timeout: link.PingTimeout}
	ps := &h.partners
	ps.mu.Lock()
	if ps.ping[0] > 0 {
		l.every, l.timeout = ps.ping[0], ps.ping[1]
	}
	ps.mu.Unlock()
	nc := websocket.NetConn(nctx, conn, websocket.MessageBinary)
	l.rpc = rpc.New("x", nc)
	// Bounded like the WebSocket: one line no larger than a message may be, and only so many calls at once (the
	// rpc's own budget, above this one, is the backstop).
	l.rpc.Limits = rpc.Limits{FrameBytes: 1 << 20, ConcurrentRequests: 2 * partnerLinkCalls}
	l.rpc.OnRequest = func(rctx context.Context, method string, params json.RawMessage) (any, error) {
		if l.calls.Add(1) > partnerLinkCalls {
			l.calls.Add(-1)
			l.close(partnerCloseSlow, "too many calls at once")
			return nil, errors.New("too many calls at once")
		}
		defer l.calls.Add(-1)
		return h.partnerRequest(rctx, l, method, params)
	}
	served := make(chan error, 1)
	go func() { served <- l.rpc.Serve(ctx, nc) }()

	hctx, hcancel := context.WithTimeout(ctx, 10*time.Second)
	err := l.rpc.Call(hctx, "hello", h.partnerHello(), &l.info)
	hcancel()
	if err == nil && l.info.HubID != hubID {
		err = fmt.Errorf("the hub there is %q, not %q", l.info.HubID, hubID)
	}
	if err != nil {
		_ = conn.Close(websocket.StatusPolicyViolation, "hello failed")
		return fmt.Errorf("hello: %w", err)
	}
	if err := h.registerPartnerLink(l); errors.Is(err, errDuplicateLink) {
		_ = conn.Close(partnerCloseDuplicate, "the two hubs already have a link")
		return err
	} else if err != nil {
		code := websocket.StatusGoingAway
		if h.partnersRunning() {
			code = partnerCloseUnpaired
		}
		_ = conn.Close(code, "not paired")
		return err
	}
	defer h.unregisterPartnerLink(l)
	go l.keepAlive()
	h.logf("partners: link up with %q (%s), dialed by %s, via %s", l.info.Name, hubID, shortID(dialer), address)
	h.partnerLinkUp(l)
	select {
	case err = <-served:
	case <-l.ctx.Done():
		err = context.Cause(l.ctx)
		if parent.Err() != nil {
			l.close(websocket.StatusGoingAway, "hub stopping")
		}
		select { // the other side's close frame, or the end of the stream
		case <-served:
		case <-time.After(5 * time.Second):
		}
	}
	_ = conn.CloseNow()
	if errors.Is(err, context.Canceled) {
		err = errors.New("link closed")
	}
	h.logf("partners: link down with %q (%s): %v", l.info.Name, hubID, err)
	return err
}

// registerPartnerLink makes l the link to its hub, unless the two hubs already have one that stays (the mesh's rule):
// the one dialed by the smaller hub_id stays (errDuplicateLink); a newer link dialed by the same hub replaces the
// older (it went away unnoticed). A hub unpaired meanwhile, or one stopping, gets none (errPartnerNotLinked).
func (h *Hub) registerPartnerLink(l *partnerLink) error {
	preferred := min(h.hubID(), l.hubID)
	ps := &h.partners
	ps.mu.Lock()
	row, paired := ps.rows[l.hubID]
	// An accepted link came with a credential: the pairing's own, or one a pairing again replaced meanwhile.
	if !ps.running || !paired || l.tokenID != "" && row.CredentialID != l.tokenID {
		ps.mu.Unlock()
		return errPartnerNotLinked
	}
	old := ps.links[l.hubID]
	if old != nil && old.ctx.Err() == nil && old.dialer != l.dialer && l.dialer != preferred {
		ps.mu.Unlock()
		return errDuplicateLink
	}
	ps.links[l.hubID] = l
	ps.mu.Unlock()
	if old != nil {
		old.close(partnerCloseDuplicate, "replaced by another link between the two hubs")
	}
	return nil
}

// partnersRunning: the partner hubs started and the hub is not stopping.
func (h *Hub) partnersRunning() bool {
	ps := &h.partners
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return ps.running
}

func (h *Hub) unregisterPartnerLink(l *partnerLink) {
	ps := &h.partners
	ps.mu.Lock()
	gone := ps.links[l.hubID] == l
	if gone {
		delete(ps.links, l.hubID)
	}
	row, paired := ps.rows[l.hubID]
	ps.mu.Unlock()
	l.cancel(errors.New("link closed"))
	if !gone {
		return
	}
	if st := h.partnerStore(); st != nil && paired { // when it was last seen
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = st.UpdatePartnerSeen(ctx, row)
		}()
	}
	h.partnersChanged()
}

// partnerLinkUp keeps what the partner's hello said: its name and addresses (and its key, when this hub had none for
// it: a partner that paired without TLS and has it now; the link came with this hub's credential or to the pinned
// key, so it is that hub's own word), and has the change told.
func (h *Hub) partnerLinkUp(l *partnerLink) {
	eps := partnerHTTPS(slices.Concat(l.info.Endpoints, []string{l.url}))
	ps := &h.partners
	ps.mu.Lock()
	row, ok := ps.rows[l.hubID]
	if ok {
		if n := partnerName(l.info.Name); n != "" {
			row.Name = n
		}
		if row.FP == "" && len(l.info.FP) <= 128 {
			row.FP = l.info.FP
		}
		if len(eps) > 0 {
			row.Endpoints = eps[:min(len(eps), 8)]
		}
		now := time.Now().UTC()
		row.SeenAt = &now
		ps.rows[l.hubID] = row
		ps.heard[l.hubID] = partnerHTTPS(l.info.Endpoints)
	}
	ps.mu.Unlock()
	if st := h.partnerStore(); st != nil && ok {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := st.UpdatePartnerSeen(ctx, row); err != nil {
				h.logf("partners: save %s: %v", row.HubID, err)
			}
		}()
	}
	h.startPartnerDialer(l.hubID) // its key or addresses may be new: it can be dialed when the link goes
	h.partnersChanged()
}

// partnerLinkTo is the live link to a partner.
func (h *Hub) partnerLinkTo(hubID string) *partnerLink {
	ps := &h.partners
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if l := ps.links[hubID]; l != nil && l.ctx.Err() == nil {
		return l
	}
	return nil
}

// partnerRequest is the link's dispatcher: hello, msg and unpair, nothing else.
func (h *Hub) partnerRequest(ctx context.Context, l *partnerLink, method string, params json.RawMessage) (any, error) {
	switch method {
	case "hello":
		return h.partnerHello(), nil
	case "msg":
		return h.partnerInbound(ctx, l, params)
	case "unpair":
		go h.partnerUnpaired(l.hubID)
		return map[string]any{}, nil
	}
	return nil, fmt.Errorf("unknown method %s", method)
}

// closePartnerLinksOf closes the links that came with a credential (one revoked, or replaced by pairing again).
func (h *Hub) closePartnerLinksOf(tokenID string) int {
	ps := &h.partners
	ps.mu.Lock()
	var gone []*partnerLink
	for _, l := range ps.links {
		if l.tokenID != "" && l.tokenID == tokenID {
			gone = append(gone, l)
		}
	}
	ps.mu.Unlock()
	for _, l := range gone {
		l.close(partnerCloseUnpaired, "credential revoked")
	}
	return len(gone)
}

// servePartnerLink is GET /api/v1/partner/link: a partner's link, over TLS only, with the credential this hub issued it.
func (h *Hub) servePartnerLink(w http.ResponseWriter, r *http.Request) {
	p := principalOf(r.Context())
	if p.Role != rolePartner || p.Machine == "" {
		writeErr(w, http.StatusForbidden, errors.New("only a partner hub's credential opens a partner link"))
		return
	}
	if r.TLS == nil {
		h.securityError(w, r, http.StatusForbidden, "tls_required", 0)
		return
	}
	if !h.partnersAvailable(w) {
		return
	}
	ps := &h.partners
	ps.mu.Lock()
	row, paired := ps.rows[p.Machine]
	running := ps.running
	ps.mu.Unlock()
	if !running {
		writeErr(w, http.StatusServiceUnavailable, errors.New("hub stopping"))
		return
	}
	if !paired || row.CredentialID != p.ID {
		// 404, not a refusal: right after pairing the other hub dials before this one saved it (its dialer tries again
		// soon); a credential this hub revoked never gets this far (401).
		writeErrCode(w, http.StatusNotFound, "not_paired", i18n.Err("hub-partner_routes.not_paired"))
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
	_ = h.runPartnerLink(r.Context(), conn, p.Machine, p.Machine, maskedSource(r), "", p.ID)
}
