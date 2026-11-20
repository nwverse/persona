package assistant

import (
	"context"
	"time"
)

// A direction's lead that a hand-over of the owner's quoted words went into waits for the owner's Start before writable
// work (#11699, docs/assistant/unified-entry.md "Across a relay"). The main conversation picks which lead gets the
// owner's words, and it can pick the wrong one: a writable task the lead plans on them, marked user_requested, would
// start in the wrong repository before the owner sees it. So from the moment such a hand-over went in (its entry with
// that lead as receiver reached delivered), task_plan there ignores user_requested, whatever opened the turn, until the
// owner next writes in that conversation (userSpoke). Read-only tasks and kinds the owner trusts start as before, and
// nothing else changes: an unquoted message, an answer, a conversation that is no lead and the main conversation. The
// mark is kept with the relay state (relayState.Handed), so it survives a restart.

// noteHandedOver: message msg went into conversation to (relayDelivered); when it is a hand-over of the owner's quoted
// words that went into a direction's lead, that lead waits for the owner before writable work.
func (s *Service) noteHandedOver(to string, msg relayMsg) {
	if msg.Handover == "" || !s.isLeader(context.Background(), to) {
		return
	}
	if e, ok := s.handoverEntry(msg.Handover); !ok || e.To != to || e.State != handoverDelivered && e.State != handoverAnswered {
		return
	}
	b := &s.relays
	b.mu.Lock()
	defer b.mu.Unlock()
	if !s.relayLocked(context.Background()) {
		return
	}
	if b.st.Handed == nil {
		b.st.Handed = map[string]time.Time{}
	}
	b.st.Handed[to] = time.Now().UTC()
	s.saveRelayLocked()
	s.logf("lead %s: a hand-over of the owner's words went in; its writable tasks wait for Start until the owner writes there", short(to))
}

// handedOver: a hand-over of the owner's quoted words went into conversation id since the owner last wrote there.
func (s *Service) handedOver(id string) bool {
	b := &s.relays
	b.mu.Lock()
	defer b.mu.Unlock()
	if !s.relayLocked(context.Background()) {
		return false
	}
	_, ok := b.st.Handed[id]
	return ok
}

// handoverEntry is hand-over id as kept now; false when there is none.
func (s *Service) handoverEntry(id string) (handover, bool) {
	b := &s.handovers
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.loadLocked(context.Background(), s) {
		return handover{}, false
	}
	e, ok := b.m[id]
	return e, ok
}
