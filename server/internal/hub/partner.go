package hub

import (
	"cmp"
	"context"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
)

// Partner hubs (docs/server/partner-hubs.md): two independent hubs, paired by their owners, whose assistants send each
// other messages. This hub carries the managers' envelopes over one pinned link per partner and keeps nothing of them:
// no group, no records, no App requests, no node streams, no message state. It is not the mesh in any way: partners
// never become peers, never appear in the hello's mesh.hubs, and none of the mesh's state or calls is used.
//
// partner.go is the state and its lifecycle; partner_pair.go pairing and unpairing, partner_link.go the link,
// partner_dial.go dialing, partner_proxy.go the outbound proxy, partner_bus.go the managers' methods,
// partner_routes.go the apps' routes, partner_cli.go the command line.

const (
	rolePartner     = "partner"      // a partner hub's link credential: opens GET /api/v1/partner/link alone
	rolePartnerPair = "partner_pair" // a partner pairing code (jh_…): POST /api/v1/partner/pair alone
	partnerCodeLife = 10 * time.Minute

	partnerLinkPath = "/api/v1/partner/link"
	partnerPairPath = "/api/v1/partner/pair"
)

// partnerState is the partner hubs on h.partners: the pairings (partner_hubs), their links and dialers.
type partnerState struct {
	mu      sync.Mutex
	running bool // initPartners ran and the hub is not stopping
	// loaded: the pairings were read (loadPartners). Until then nothing pairs or links, a send waits (partner_offline)
	// and no manager gets a list: none is not "no partners".
	loaded  bool
	started bool // startPartners ran: the dialers may start
	ctx     context.Context
	rows    map[string]db.PartnerHub   // hub id -> its pairing
	links   map[string]*partnerLink    // hub id -> its live link
	dialers map[string]*peerDialer     // hub id -> the loop dialing it
	dial    map[string]partnerDialFail // hub id -> how dialing it last went
	heard   map[string][]string        // hub id -> the https addresses its last hello named
	pairing map[string]bool            // hub id -> a pairing with it is being made here (pairingPartner)
	// changed is closed and replaced on every change (waiters: the dialers).
	changed chan struct{}
	// kick wakes tellLoop, which tells the default manager (manager) and the apps (apps) the list as it is then, and
	// the managers that are not the default an empty one: those that said their hello since (hellos), and the default
	// the list last went to or was meant for (told) once the default may have changed (recheck).
	kick                   chan struct{}
	manager, apps, recheck bool
	hellos                 map[string]bool
	told                   string
	ping                   [2]time.Duration  // tests: heartbeat interval and timeout
	retry                  [2]time.Duration  // tests: the first and the longest wait before reading the pairings again
	joinStep               func(step string) // tests: called at joinPartner's steps (partnerJoinIssued, partnerJoinAnswered)
}

// partnerDialFail is how the last dial of a partner went: nil err after a success.
type partnerDialFail struct {
	err     error
	at      time.Time
	refused bool // the partner does not take this hub's credential (it unpaired, most likely)
}

// partnersOn: partner hubs are on here: hub.json partners.enabled is not false, the hub has where to keep them, and it
// is not a member of a mesh group (whose assistant stands by for the center's).
func (h *Hub) partnersOn() bool {
	return h.cfg != nil && h.cfg.Partners.enabled() && h.partnerStore() != nil && !h.isMember()
}

// Waiting before reading the pairings again after a read failed: from partnerLoadWait, doubling to partnerLoadWaitMax.
// Settings, not measurements.
const (
	partnerLoadWait    = time.Second
	partnerLoadWaitMax = time.Minute
)

// initPartners loads the pairings, next to initMesh. A read that fails is tried again until it works
// (retryPartnerLoad): a database that did not answer once must not make every pairing look gone.
func (h *Hub) initPartners(ctx context.Context) {
	ps := &h.partners
	ps.mu.Lock()
	ps.running, ps.ctx, ps.loaded, ps.started = true, ctx, false, false
	ps.rows, ps.links, ps.dialers = map[string]db.PartnerHub{}, map[string]*partnerLink{}, map[string]*peerDialer{}
	ps.dial, ps.heard = map[string]partnerDialFail{}, map[string][]string{}
	ps.changed, ps.kick = make(chan struct{}), make(chan struct{}, 1)
	ps.hellos, ps.told = map[string]bool{}, ""
	ps.mu.Unlock()
	go h.tellLoop(ctx)
	if !h.loadPartners(ctx) {
		go h.retryPartnerLoad(ctx)
	}
}

// loadPartners reads the pairings (none without a store); false when the read failed.
func (h *Hub) loadPartners(ctx context.Context) bool {
	var list []db.PartnerHub
	if st := h.partnerStore(); st != nil {
		var err error
		if list, err = st.PartnerHubs(ctx); err != nil {
			h.logf("partners: load: %v (trying again)", err)
			return false
		}
	}
	ps := &h.partners
	ps.mu.Lock()
	for _, p := range list {
		ps.rows[p.HubID] = p
	}
	ps.loaded = true
	ps.mu.Unlock()
	return true
}

// retryPartnerLoad reads the pairings until that works, then has them dialed (once startPartners ran) and told.
func (h *Hub) retryPartnerLoad(ctx context.Context) {
	ps := &h.partners
	ps.mu.Lock()
	wait, longest := cmp.Or(ps.retry[0], partnerLoadWait), cmp.Or(ps.retry[1], partnerLoadWaitMax)
	ps.mu.Unlock()
	for {
		if !sleepCtx(ctx, wait) {
			return
		}
		if h.loadPartners(ctx) {
			break
		}
		wait = min(2*wait, longest)
	}
	h.logf("partners: loaded")
	ps.mu.Lock()
	started := ps.started
	ps.mu.Unlock()
	if started {
		h.dialPartners()
	}
	h.partnersChanged()
}

// partnersLoaded: the pairings were read.
func (h *Hub) partnersLoaded() bool {
	ps := &h.partners
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return ps.loaded
}

// startPartners starts dialing the partners this hub dials, next to startMesh: it needs the TLS identity.
func (h *Hub) startPartners(context.Context) {
	ps := &h.partners
	ps.mu.Lock()
	ps.started = true
	n := len(ps.rows)
	ps.mu.Unlock()
	if !h.partnersOn() {
		if n > 0 {
			h.logf("partners: off here (partners.enabled, or a mesh member): %d pairing(s) kept, not linked", n)
		}
		return
	}
	h.sayPartnerProxy()
	h.dialPartners()
}

// dialPartners starts a dialer for each partner this hub dials (startPartnerDialer: the others it leaves).
func (h *Hub) dialPartners() {
	ps := &h.partners
	ps.mu.Lock()
	ids := make([]string, 0, len(ps.rows))
	for id := range ps.rows {
		ids = append(ids, id)
	}
	ps.mu.Unlock()
	for _, id := range ids {
		h.startPartnerDialer(id)
	}
}

// stopPartnerLinks is part of the shutdown, next to stopPeerLinks: every link gets a 1001 (an accepted one holds
// enterWS), the dialers stop.
func (h *Hub) stopPartnerLinks() {
	ps := &h.partners
	ps.mu.Lock()
	links := make([]*partnerLink, 0, len(ps.links))
	for _, l := range ps.links {
		links = append(links, l)
	}
	dialers := ps.dialers
	ps.dialers = map[string]*peerDialer{}
	ps.running = false
	ps.mu.Unlock()
	for _, d := range dialers {
		d.cancel()
	}
	for _, l := range links {
		l.close(websocket.StatusGoingAway, "hub stopping")
	}
}

// partnerRow is the pairing with a hub, if there is one.
func (h *Hub) partnerRow(hubID string) (db.PartnerHub, bool) {
	ps := &h.partners
	ps.mu.Lock()
	defer ps.mu.Unlock()
	p, ok := ps.rows[hubID]
	return p, ok
}

// partnerForCredential is the partner a credential was issued to ("" for none).
func (h *Hub) partnerForCredential(id string) string {
	ps := &h.partners
	ps.mu.Lock()
	defer ps.mu.Unlock()
	for hid, p := range ps.rows {
		if p.CredentialID == id {
			return hid
		}
	}
	return ""
}

// partnerWait is closed at the next change.
func (h *Hub) partnerWait() <-chan struct{} {
	ps := &h.partners
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if ps.changed == nil {
		return nil
	}
	return ps.changed
}

// partnersChanged: the list, a link, a dial setting or a pairing changed. The dialers look again; the default manager
// hears partner.changed and the apps {"type":"partner_hubs"}.
func (h *Hub) partnersChanged() {
	ps := &h.partners
	ps.mu.Lock()
	if ps.changed != nil {
		close(ps.changed)
		ps.changed = make(chan struct{})
	}
	ps.manager, ps.apps = true, true
	ps.wakeLocked()
	ps.mu.Unlock()
}

// wakeLocked wakes tellLoop (ps.mu held; the kick is buffered, never waited on).
func (ps *partnerState) wakeLocked() {
	if ps.kick != nil {
		select {
		case ps.kick <- struct{}{}:
		default:
		}
	}
}

// partnersToManager has the list told to the default manager, manager id having said its hello (one that is not
// the default gets the empty list: a list it kept from when it was is not its any more).
func (h *Hub) partnersToManager(id string) {
	ps := &h.partners
	ps.mu.Lock()
	ps.manager = true
	if ps.hellos != nil {
		ps.hellos[id] = true
	}
	ps.wakeLocked()
	ps.mu.Unlock()
}

// partnersRecheck: the default manager may be another one now (managersChanged). The new one gets the list, the one
// told before the empty list.
func (h *Hub) partnersRecheck() {
	ps := &h.partners
	ps.mu.Lock()
	ps.recheck = true
	ps.wakeLocked()
	ps.mu.Unlock()
}

// tellLoop sends what partnersChanged asked for, one at a time and as the list is when it goes (a burst of changes
// goes out as one, never an older list after a newer one). The list goes to the default manager alone, once the
// pairings were read; a manager that is not the default (one that said its hello, or the default told before) gets
// the empty list, so it offers no hub tools.
func (h *Hub) tellLoop(ctx context.Context) {
	ps := &h.partners
	for {
		select {
		case <-ctx.Done():
			return
		case <-ps.kick:
		}
		ps.mu.Lock()
		manager, apps, recheck, hellos, told, loaded := ps.manager, ps.apps, ps.recheck, ps.hellos, ps.told, ps.loaded
		ps.manager, ps.apps, ps.recheck, ps.hellos = false, false, false, map[string]bool{}
		ps.mu.Unlock()
		if apps {
			h.broadcast(map[string]any{"type": "partner_hubs"})
		}
		if !manager && !recheck && len(hellos) == 0 {
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		to, err := h.defaultOwnerOrErr(cctx)
		cancel()
		if err != nil && told != "" { // the roster did not answer: not a change of the default
			to = told
		}
		if told != "" && told != to { // the default changed: the new one gets the list
			hellos[told], manager = true, true
		}
		for id := range hellos {
			if id != to {
				h.tellPartners(id, assistantwire.PartnerHubs{Hubs: []assistantwire.PartnerHub{}})
			}
		}
		if !manager || !loaded { // not read yet: told once it is (retryPartnerLoad)
			continue
		}
		h.tellPartners(to, h.partnerHubsWire())
		// The default the list went to, or would have (one not connected hears it at its hello): told, whether it went
		// or not, so that a later default tells it the empty list and itself made the default again hears the list.
		ps.mu.Lock()
		ps.told = to
		ps.mu.Unlock()
	}
}

// tellPartners sends manager id partner.changed with list when it takes partner messages.
func (h *Hub) tellPartners(id string, list assistantwire.PartnerHubs) {
	if h.archiveManagerHas(id, assistantwire.CapabilityPartnerHubs) != http.StatusOK {
		return
	}
	if err := h.notifyBus(id, assistantwire.EventPartnerHubs, list); err != nil {
		h.logf("partners: tell %s: %v", id, err)
	}
}

// partnerHubsWire is the list as the managers get it (partner.hubs, partner.changed): none while partner hubs are off.
// Callers check partnersLoaded first: before the pairings were read, there is no list to give.
func (h *Hub) partnerHubsWire() assistantwire.PartnerHubs {
	out := assistantwire.PartnerHubs{Enabled: h.partnersOn(), Hubs: []assistantwire.PartnerHub{}}
	if !out.Enabled {
		return out
	}
	ps := &h.partners
	ps.mu.Lock()
	for id, p := range ps.rows {
		l := ps.links[id]
		out.Hubs = append(out.Hubs, assistantwire.PartnerHub{HubID: id, Name: p.Name, Online: l != nil && l.ctx.Err() == nil,
			Dial: partnerDialOf(p), PairedAt: p.CreatedAt})
	}
	ps.mu.Unlock()
	slices.SortFunc(out.Hubs, func(a, b assistantwire.PartnerHub) int {
		return cmp.Or(cmp.Compare(a.Name, b.Name), cmp.Compare(a.HubID, b.HubID))
	})
	return out
}

func partnerDialOf(p db.PartnerHub) string {
	if p.Dial == assistantwire.PartnerDialNever {
		return assistantwire.PartnerDialNever
	}
	return assistantwire.PartnerDialAuto
}

// partnerFeatures is partner_hubs while partner hubs are on and the default manager takes their messages.
func (h *Hub) partnerFeatures() []string {
	if !h.partnersOn() || !h.defaultManagerNames(assistantwire.CapabilityPartnerHubs) {
		return nil
	}
	return []string{featurePartnerHubs}
}

const featurePartnerHubs = assistantwire.CapabilityPartnerHubs
