package devices

import (
	"encoding/json"
	"maps"
	"slices"
	"strconv"
	"time"

	"github.com/nwverse/persona/server/internal/devicewire"
	"github.com/nwverse/persona/server/internal/link"
)

// Presence through Persona Link (internal/link/presence.go, docs/operations/link.md "Presence"): a computer's menu bar
// app says the user is at it and its notifications show. The manager keeps what its Persona Link said until the TTL
// runs out (a presence message in active mode, the info answer in passive mode) and reports it in devices.desktops; the
// hub counts the computer as a desktop for "Phone Muted". It counts only while it holds the "Needs you" summary as it
// is now (attentionFor's rule, and the summary delivered to it); and a card's push is left to it only once a summary it
// took rang for that card (LinkRang).

// presence is what a computer's Persona Link said last: active until until (this manager's clock), its notifications
// showing or not.
type presence struct {
	since, until time.Time // since: when this run of active presence began
	notifies     bool
}

// keepPresenceLocked takes what the Persona Link said at now (nil or inactive: nothing). m.mu is held.
func (m *Machine) keepPresenceLocked(p *link.PresenceInfo, now time.Time) {
	if p == nil || !p.Active {
		m.presence = nil
		return
	}
	ttl := link.PresenceTTL
	if p.TTLMS > 0 {
		ttl = time.Duration(min(p.TTLMS, devicewire.DesktopTTLMax.Milliseconds())) * time.Millisecond
	}
	since := now
	if m.presence != nil && m.presence.until.After(now) {
		since = m.presence.since
	}
	m.presence = &presence{since: since, until: now.Add(ttl), notifies: p.Notifies}
}

// takePresence is a presence message of the machine's live link l. Every one is reported, the same as the last
// included: the hub needs each renewal. In the background: the link's reading never waits on the hub.
func (m *Machine) takePresence(l *mgrLink, msg link.Message) {
	m.mu.Lock()
	if m.link != l {
		m.mu.Unlock()
		return // a link being replaced
	}
	m.keepPresenceLocked(&link.PresenceInfo{Active: msg.Active, Notifies: msg.Notifies, TTLMS: msg.TTLMS}, time.Now())
	m.mu.Unlock()
	go m.d.reportDesktops(false)
}

// summaryHeld is what a computer's Persona Link holds of the summary: the Rev it took last (sent), whether the last
// delivery failed, how many deliveries it took (taken), and the cards its summaries listed
// as notifying (rang: true when the first summary listing one so was not the connection's first, which Persona Link
// takes as its baseline and rings nothing of; false when it was). A summary that is not capped forgets the cards it
// does not list, as Persona Link forgets their sessions: one listed again notifies again.
type summaryHeld struct {
	sent   string
	failed bool
	taken  int
	rang   map[string]bool
}

// done: the delivery of a ended, taken or not.
func (h *summaryHeld) done(a *link.Attention, ok bool) {
	if !ok {
		h.failed = true
		return
	}
	baseline := h.sent == ""
	h.sent, h.failed = a.Rev, false
	h.taken++
	if h.rang == nil {
		h.rang = map[string]bool{}
	}
	listed := map[string]bool{}
	for _, it := range a.Items {
		if it.InteractionID == "" {
			continue
		}
		listed[it.InteractionID] = true
		if _, seen := h.rang[it.InteractionID]; it.Notify && !seen {
			h.rang[it.InteractionID] = !baseline
		}
	}
	if a.More == 0 {
		maps.DeleteFunc(h.rang, func(card string, _ bool) bool { return !listed[card] })
	}
}

// holds is whether it keeps up with the summary (cur, the one as it is now, exists): it took one, and no delivery
// failed since. A newer one is then on its way or about to be (every change goes out at once): counting it as held
// keeps a desktop from dropping out and back in at each change. A failure, a refusal or a timeout ends it until a
// delivery is taken again. Which cards a desktop stands in for is LinkRang's, by what it took.
func (h *summaryHeld) holds(cur string) bool {
	return cur != "" && h.sent != "" && !h.failed
}

// desktopNow is a computer that is a desktop now: what the hub is told, and its deadline on this manager's clock.
type desktopNow struct {
	devicewire.Desktop
	until time.Time
}

// desktop is the machine as a desktop at now: nil unless its Persona Link said it is active within the TTL, it takes
// the summary (its hello or info listed attention), the computer is known to get the summary as it is, and it holds
// the summary as it is now. While the hub cannot say which computers wait to be confirmed the summary freezes: no new
// card reaches the computer, so it does not count then (the phone keeps them).
func (m *Machine) desktop(now time.Time) *desktopNow {
	eligible, _ := m.d.summaryEligible(m.cfg.ID) // false while unknown
	if !eligible {
		return nil
	}
	m.mu.Lock()
	l, p := m.link, m.presence // p is never changed in place: keepPresenceLocked makes a new one
	m.mu.Unlock()
	if p == nil || !p.until.After(now) {
		return nil
	}
	var held bool
	switch {
	case l != nil && l.attention:
		cur := l.summary()
		l.heldMu.Lock()
		held = cur != nil && l.held.holds(cur.Rev)
		l.heldMu.Unlock()
	case m.cfg.Kind == kindService:
		cur := m.d.attentionFor(m.cfg.ID, m.d.config().WebURL)
		m.mu.Lock()
		held = m.svc.attention && cur != nil && m.svc.held.holds(cur.Rev)
		m.mu.Unlock()
	}
	if !held {
		return nil
	}
	// An entry another one absorbed: the hub knows its computer by that one's id.
	return &desktopNow{Desktop: devicewire.Desktop{MachineID: m.d.Canonical(m.cfg.ID), Since: p.since, Notifies: p.notifies,
		TTLMS: max(1, p.until.Sub(now).Milliseconds())}, until: p.until}
}

// rang is whether a summary the machine's Persona Link took rang for card.
func (m *Machine) rang(card string) bool {
	m.mu.Lock()
	l, r := m.link, m.svc.held.rang[card]
	m.mu.Unlock()
	if l != nil && !r {
		l.heldMu.Lock()
		r = l.held.rang[card]
		l.heldMu.Unlock()
	}
	return r
}

// reportDesktops tells the hub which computers are desktops now (devices.desktops), when that changed: a presence said
// again moves its deadline, which the hub needs each of. force: the hub may not have it (a handshake). One at a time,
// each with the list as it is then, so the last one sent is never older than another. It says the TTL left; the hub's
// own clock counts it down.
func (d *Devices) reportDesktops(force bool) {
	if d.stopping.Load() {
		return
	}
	d.desktopsMu.Lock()
	defer d.desktopsMu.Unlock()
	now := time.Now()
	list := devicewire.Desktops{Desktops: []devicewire.Desktop{}}
	key := "" // the list but for what time alone changes (the TTL left)
	for _, m := range d.cfgMachines() {
		if p := m.desktop(now); p != nil {
			list.Desktops = append(list.Desktops, p.Desktop)
			key += p.MachineID + " " + strconv.FormatInt(p.Since.UnixNano(), 10) + " " +
				strconv.FormatInt(p.until.UnixNano(), 10) + " " + strconv.FormatBool(p.Notifies) + "\n"
		}
	}
	if !force && key == d.desktopsSent {
		return
	}
	b, _ := json.Marshal(list)
	if err := d.opts.Hub.Notify(devicewire.EventDesktops, json.RawMessage(b)); err == nil {
		d.desktopsSent = key
	}
}

// delivered is told when a computer's Persona Link took a summary, or a delivery failed: who holds what changed. It
// wakes LinkRang, and reports the desktops when that may have changed which ones they are: only a computer whose
// Persona Link said it is active can be one.
func (d *Devices) delivered() {
	d.ringMu.Lock()
	if d.ring != nil {
		close(d.ring)
	}
	d.ring = make(chan struct{})
	d.ringMu.Unlock()
	if slices.ContainsFunc(d.cfgMachines(), func(m *Machine) bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return m.presence != nil
	}) {
		go d.reportDesktops(false)
	}
}

// deliveries is closed at the next delivery.
func (d *Devices) deliveries() <-chan struct{} {
	d.ringMu.Lock()
	defer d.ringMu.Unlock()
	if d.ring == nil {
		d.ring = make(chan struct{})
	}
	return d.ring
}

// LinkRang waits up to within for a summary one of the manager's Persona Link desktops whose notifications show took
// to ring for card (its interaction id): false at once while there is none when it starts, and when the time is up.
// Only such a card's push may a desktop take over from the phone (assistantwire.Notification.LinkCovered): a summary
// merged with newer cards that listed it in More, or one that did not reach the computer, rang for nothing. A desktop
// that stops counting while it waits (a summary newer than the one just delivered has not gone out yet) is waited for.
func (d *Devices) LinkRang(card string, within time.Duration) bool {
	timeout := time.NewTimer(within)
	defer timeout.Stop()
	for first := true; ; first = false {
		wake := d.deliveries()
		now, desktops := time.Now(), false
		for _, m := range d.cfgMachines() {
			if p := m.desktop(now); p == nil || !p.Notifies {
				continue
			}
			desktops = true
			if m.rang(card) {
				d.reportDesktops(false) // the hub hears this desktop before the push that relies on it
				return true
			}
		}
		if first && !desktops {
			return false
		}
		select {
		case <-wake:
		case <-timeout.C:
			return false
		}
	}
}
