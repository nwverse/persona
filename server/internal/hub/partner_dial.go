package hub

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/coder/websocket"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/i18n"
)

// Dialing a partner (docs/server/partner-hubs.md "The link"): every partner whose dial is auto, with a pin and an https
// address, is dialed until there is a link to it, whichever side made it; one set to never only waits for the partner
// to dial (it cannot be reached from here). Candidates are its https addresses (the pairing's and its last hello's),
// ranked as the mesh ranks them, directly and through the proxy (partner_proxy.go), one every 300 ms, the first that
// works wins. Backoff: 1 s doubling to a minute, randomized, reset after a link that lasted; partnerRefusedWait after the
// partner refused this hub's credential (401, 403, or a 4012 close). These are settings, not measurements (vars: tests
// shorten them).

var (
	partnerRefusedWait   = 5 * time.Minute
	partnerStableLink    = time.Minute     // a link that lasted this long resets the backoff
	partnerDuplicateWait = time.Second     // the other link stays: look again after this
	partnerLinkedWait    = 5 * time.Second // with a link up, look again this often (or at the next change)
)

// partnerDialable: this hub dials p (dial auto, a pin, an https address).
func partnerDialable(p db.PartnerHub) bool {
	return p.Dial != assistantwire.PartnerDialNever && p.FP != "" && len(partnerHTTPS(p.Endpoints)) > 0
}

func (h *Hub) startPartnerDialer(hubID string) {
	on := h.partnersOn() // the mesh's lock, never under the partners'
	ps := &h.partners
	ps.mu.Lock()
	row, ok := ps.rows[hubID]
	if !ps.running || ps.ctx == nil || ps.dialers[hubID] != nil || !ok || !partnerDialable(row) || !on {
		ps.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(ps.ctx)
	d := &peerDialer{cancel: cancel, done: make(chan struct{})}
	ps.dialers[hubID] = d
	ps.mu.Unlock()
	go func() {
		defer close(d.done)
		defer h.partnerDialerDone(ctx, hubID, d)
		h.partnerDialLoop(ctx, hubID)
	}()
}

// partnerDialerDone forgets a dialer that ended on its own (the partner set to never, say), so another may start; one
// set back to auto while it was ending (startPartnerDialer saw it still there) starts again now.
func (h *Hub) partnerDialerDone(ctx context.Context, hubID string, d *peerDialer) {
	ps := &h.partners
	ps.mu.Lock()
	mine := ps.dialers[hubID] == d
	if mine {
		delete(ps.dialers, hubID)
	}
	ps.mu.Unlock()
	ended := ctx.Err() == nil // not stopped: it found nothing to dial
	d.cancel()
	if mine && ended {
		h.startPartnerDialer(hubID) // a no-op unless the partner is dialable again
	}
}

func (h *Hub) stopPartnerDialer(hubID string) {
	ps := &h.partners
	ps.mu.Lock()
	d := ps.dialers[hubID]
	delete(ps.dialers, hubID)
	ps.mu.Unlock()
	if d != nil {
		d.cancel()
	}
}

// setPartnerDial keeps how dialing a partner went; a change of what the apps show is told.
func (h *Hub) setPartnerDial(hubID string, f partnerDialFail) {
	ps := &h.partners
	ps.mu.Lock()
	old, had := ps.dial[hubID]
	if _, paired := ps.rows[hubID]; !paired {
		ps.mu.Unlock()
		return
	}
	ps.dial[hubID] = f
	ps.mu.Unlock()
	if !had || old.refused != f.refused || (old.err == nil) != (f.err == nil) ||
		(old.err != nil && f.err != nil && old.err.Error() != f.err.Error()) {
		h.partnersChanged()
	}
}

// partnerEndpoints are a partner's candidates: what its last hello said, then what pairing (or the last link) saved.
func (h *Hub) partnerEndpoints(hubID string) []string {
	ps := &h.partners
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return partnerHTTPS(slices.Concat(ps.heard[hubID], ps.rows[hubID].Endpoints))
}

func (h *Hub) partnerDialLoop(ctx context.Context, hubID string) {
	var b peerBackoff
	self := h.hubID()
	for ctx.Err() == nil {
		if h.partnerLinkTo(hubID) != nil || !h.partnersOn() {
			select {
			case <-ctx.Done():
				return
			case <-h.partnerWait():
			case <-time.After(partnerLinkedWait):
			}
			continue
		}
		row, ok := h.partnerRow(hubID)
		if !ok || !partnerDialable(row) {
			return // unpaired, or set to never: whoever changes it starts a dialer again
		}
		token, err := h.openKey(row.Token)
		if err != nil {
			h.setPartnerDial(hubID, partnerDialFail{err: i18n.Err("hub-partner_dial.credentials_unreadable", i18n.Args{"err": err.Error()}), at: time.Now()})
			if !sleepCtx(ctx, partnerRefusedWait) {
				return
			}
			continue
		}
		conn, c, err := h.dialPartner(ctx, row.FP, token, h.partnerEndpoints(hubID))
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			wait := b.next()
			f := partnerDialFail{err: err, at: time.Now()}
			// What the partner answered, said plainly: 401 its credential gone (unpaired there, most likely), 403 partner
			// hubs off there, 409 it joined a linked-hubs group. None changes soon: look again in partnerRefusedWait.
			var he *peerHTTPError
			if errors.As(err, &he) {
				switch he.status {
				case http.StatusUnauthorized:
					f.refused, f.err, wait = true, i18n.Err("hub-partner_dial.refused"), partnerRefusedWait
				case http.StatusForbidden:
					f.refused, f.err, wait = true, i18n.Err("hub-partner_dial.partner_off"), partnerRefusedWait
				case http.StatusConflict:
					f.err, wait = i18n.Err("hub-partner_dial.partner_member"), partnerRefusedWait
				}
			}
			h.setPartnerDial(hubID, f)
			if !sleepCtx(ctx, wait) {
				return
			}
			continue
		}
		h.setPartnerDial(hubID, partnerDialFail{at: time.Now()})
		start := time.Now()
		err = h.runPartnerLink(ctx, conn, hubID, self, c.key, c.url, "")
		if time.Since(start) > partnerStableLink {
			b = peerBackoff{}
		}
		if ctx.Err() != nil {
			return
		}
		switch {
		case websocket.CloseStatus(err) == partnerCloseUnpaired:
			h.setPartnerDial(hubID, partnerDialFail{err: i18n.Err("hub-partner_dial.unpaired"), at: time.Now(), refused: true})
			if !sleepCtx(ctx, partnerRefusedWait) {
				return
			}
			continue
		case errors.Is(err, errDuplicateLink) || websocket.CloseStatus(err) == partnerCloseDuplicate:
			if !sleepCtx(ctx, partnerDuplicateWait) { // the other link stays: wait until it is gone (top of the loop)
				return
			}
			continue
		}
		if !sleepCtx(ctx, b.next()) {
			return
		}
	}
}

// dialPartner opens a link to a partner, the WebSocket only (the hello is runPartnerLink's), and says which candidate
// it took.
func (h *Hub) dialPartner(ctx context.Context, fp, token string, eps []string) (*websocket.Conn, partnerCandidate, error) {
	cands, err := h.partnerCandidates(eps)
	if err != nil {
		return nil, partnerCandidate{}, err
	}
	conn, key, err := raceEndpoints(ctx, partnerKeys(cands), func(ctx context.Context, key string) (*websocket.Conn, error) {
		c := partnerCandidateOf(cands, key)
		client := h.partnerClient(fp, c.via)
		defer client.CloseIdleConnections() // a refusal's connection: the WebSocket's own was taken over
		conn, resp, err := websocket.Dial(ctx, peerWSURL(c.url, partnerLinkPath), &websocket.DialOptions{HTTPClient: client,
			HTTPHeader: http.Header{"Authorization": {"Bearer " + token}}, CompressionMode: websocket.CompressionContextTakeover})
		if err != nil {
			if resp != nil && resp.StatusCode != http.StatusSwitchingProtocols {
				return nil, c.failed(&peerHTTPError{status: resp.StatusCode})
			}
			return nil, c.failed(err)
		}
		return conn, nil
	}, func(c *websocket.Conn) { _ = c.CloseNow() })
	if err != nil {
		return nil, partnerCandidate{}, &partnerDialError{err}
	}
	return conn, partnerCandidateOf(cands, key), nil
}
