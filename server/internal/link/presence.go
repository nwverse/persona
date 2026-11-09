package link

import (
	"encoding/json"
	"sync"
	"time"
)

// Presence through Persona Link (docs/operations/link.md "Presence"): the menu bar app says on persona-link's stdin
// whether the user is at this computer and its notifications show, and persona-link passes it on to the hub's manager,
// which lets a phone with "Phone Muted" leave its "Needs you" cards to this computer. Active mode: a presence message on
// the link, once it is up and for every line the app writes. Passive mode: the info answer's presence, read every 30 s.
// Only the menu bar app says it: the command-line persona-link cannot show a notification.

// PresenceTTL is how long a presence counts once said (ttl_ms on the wire). The app says an active one again every
// 60 s, so one lost line does not let it lapse, and one from an app that went quiet stops counting soon.
const PresenceTTL = 2 * time.Minute

// Presence is one presence line of the app's.
type Presence struct {
	Active   bool `json:"active"`
	Notifies bool `json:"notifies"`
}

// PresenceInfo is presence as the manager is told it: the info answer's presence, and an active link's presence
// message. TTLMS is what is left of PresenceTTL since the app said it.
type PresenceInfo struct {
	Active   bool  `json:"active"`
	Notifies bool  `json:"notifies"`
	TTLMS    int64 `json:"ttl_ms"`
}

// ParsePresenceLine reads one line the app wrote: {"presence":{"active":…,"notifies":…}}. Anything else (another
// message, a line that does not decode) is not presence.
func ParsePresenceLine(line []byte) (Presence, bool) {
	var l struct {
		Presence *Presence `json:"presence"`
	}
	if json.Unmarshal(line, &l) != nil || l.Presence == nil {
		return Presence{}, false
	}
	return *l.Presence, true
}

// PresenceBox keeps the app's latest line for either mode and tells the active link each time one comes. A nil box
// (persona-link without the app) never has one.
type PresenceBox struct {
	mu   sync.Mutex
	p    Presence
	at   time.Time // zero until the app said one
	poke chan struct{}
}

func NewPresenceBox() *PresenceBox { return &PresenceBox{poke: make(chan struct{}, 1)} }

// Set takes a line the app wrote at now. Every one counts, the same as the last included: it says the app is still
// there.
func (b *PresenceBox) Set(p Presence, now time.Time) {
	b.mu.Lock()
	b.p, b.at = p, now
	b.mu.Unlock()
	select {
	case b.poke <- struct{}{}:
	default:
	}
}

// Latest is the presence to tell the manager at now; false before the app said any. An active line older than
// PresenceTTL counts as inactive: the app stopped saying it.
func (b *PresenceBox) Latest(now time.Time) (PresenceInfo, bool) {
	if b == nil {
		return PresenceInfo{}, false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.at.IsZero() {
		return PresenceInfo{}, false
	}
	left := PresenceTTL - now.Sub(b.at)
	if !b.p.Active || left < time.Millisecond { // ttl_ms is whole milliseconds, and an active one never says 0
		return PresenceInfo{Notifies: b.p.Notifies}, true
	}
	return PresenceInfo{Active: true, Notifies: b.p.Notifies, TTLMS: left.Milliseconds()}, true
}

// Poked receives once after each Set (a run of them may be one).
func (b *PresenceBox) Poked() <-chan struct{} { return b.poke }
