package devices

import (
	"encoding/json"
	"time"

	"github.com/nwverse/persona/server/internal/devicewire"
	"github.com/nwverse/persona/server/internal/link"
)

// Presence through Persona Link (internal/link/presence.go, docs/operations/link.md "Presence"): a computer's menu bar
// app says the user is at it and its notifications show. The manager keeps what its Persona Link said until the TTL
// runs out (a presence message in active mode, the info answer in passive mode) and reports it in devices.desktops; the
// hub counts the computer as a desktop for "Phone Muted". Only a computer that gets the
// "Needs you" summary as it is counts, by attentionFor's rule: its notifications stand in for the phone's cards.

// presenceTTLMax caps what a Persona Link may ask for: no presence keeps phones quiet longer without being said again.
const presenceTTLMax = 5 * time.Minute

// presence is what a computer's Persona Link said last: active until until, its notifications showing or not.
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
		ttl = time.Duration(min(p.TTLMS, presenceTTLMax.Milliseconds())) * time.Millisecond
	}
	since := now
	if m.presence != nil && m.presence.until.After(now) {
		since = m.presence.since
	}
	m.presence = &presence{since: since, until: now.Add(ttl), notifies: p.Notifies}
}

// takePresence is a presence message of the machine's live link l. Every one is reported, the same as the last
// included: the hub needs each new active_until. In the background: the link's reading never waits on the hub.
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

// desktop is the machine's presence as the hub is told it at now: nil unless its Persona Link said it is active within
// the TTL, it takes the summary (its hello or info listed attention), and the computer is known to get the summary as it
// is. While the hub cannot say which computers wait to be confirmed the summary freezes: no new card reaches the
// computer, so it does not count then (the phone keeps them).
func (m *Machine) desktop(now time.Time) *devicewire.Desktop {
	eligible, _ := m.d.summaryEligible(m.cfg.ID) // false while unknown
	m.mu.Lock()
	takes := m.link != nil && m.link.attention || m.cfg.Kind == kindService && m.svc.attention
	p := m.presence // never changed in place: keepPresenceLocked makes a new one
	counts := p != nil && p.until.After(now) && takes && eligible
	m.mu.Unlock()
	if !counts {
		return nil
	}
	// An entry another one absorbed: the hub knows its computer by that one's id.
	return &devicewire.Desktop{MachineID: m.d.Canonical(m.cfg.ID), Since: p.since, ActiveUntil: p.until, Notifies: p.notifies}
}

// reportDesktops tells the hub which computers are desktops now (devices.desktops), when that changed: a presence said
// again changes its active_until, which the hub needs each of. force: the hub may not have it (a handshake). One at a
// time, each with the list as it is then, so the last one sent is never older than another.
func (d *Devices) reportDesktops(force bool) {
	if d.stopping.Load() {
		return
	}
	d.desktopsMu.Lock()
	defer d.desktopsMu.Unlock()
	now := time.Now()
	list := devicewire.Desktops{Desktops: []devicewire.Desktop{}}
	for _, m := range d.cfgMachines() {
		if p := m.desktop(now); p != nil {
			list.Desktops = append(list.Desktops, *p)
		}
	}
	b, _ := json.Marshal(list)
	if !force && string(b) == d.desktopsSent {
		return
	}
	if err := d.opts.Hub.Notify(devicewire.EventDesktops, json.RawMessage(b)); err == nil {
		d.desktopsSent = string(b)
	}
}
