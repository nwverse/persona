package assistant

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
)

// transferPendingAsks moves only an undelivered first rung. Delivered cards, timed-out rungs and owner
// escalations keep their state. The original absolute permission deadline is never extended by a move.
// The caller holds digestMu so no old digest can race the successful transfer response.
func (s *Service) transferPendingAsks(ctx context.Context, t Todo) error {
	// A send whose outcome is unknown is already in delivery, not an undelivered card to replay.
	s.relays.mu.Lock()
	var uncertain []struct {
		to  string
		msg relayMsg
	}
	if s.relayLocked(ctx) {
		for to, queue := range s.relays.st.Queue {
			for _, msg := range queue {
				if msg.Tool == superviseTool && !msg.TriedAt.IsZero() {
					uncertain = append(uncertain, struct {
						to  string
						msg relayMsg
					}{to, msg})
				}
			}
		}
	}
	s.relays.mu.Unlock()
	for _, send := range uncertain {
		s.askRelayDelivered(send.to, send.msg)
	}
	s.fleet.mu.Lock()
	var cards []string
	for id, p := range s.fleet.interactions {
		r := p.route
		if !p.claimed && !p.told && r != nil && r.todo == t.ID && r.step == 0 && !r.delivered {
			cards = append(cards, id)
		}
	}
	s.fleet.mu.Unlock()
	for _, id := range cards {
		s.fleet.mu.Lock()
		p := s.fleet.interactions[id]
		if p == nil {
			s.fleet.mu.Unlock()
			continue
		}
		ix := p.ix
		s.fleet.mu.Unlock()
		ladder, _, home, ok := s.askLadder(ctx, ix)
		if !ok {
			continue
		}
		s.fleet.mu.Lock()
		if p := s.fleet.interactions[id]; p != nil && !p.claimed && !p.told && p.route != nil && p.route.step == 0 && !p.route.delivered {
			p.route.ladder, p.route.machine = ladder, home
		}
		s.fleet.mu.Unlock()
	}

	// Keep queue removal/admission and the durable outbox under the existing relay checkpoint. In particular,
	// repeated transfers create no signal. A queued mixed digest is split and rendered at its normal drain boundary.
	b, r := &s.supervise, &s.relays
	b.mu.Lock()
	defer b.mu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	if !s.relayLocked(ctx) {
		return fmt.Errorf("supervisor saved; pending card queue unavailable: retry the transfer")
	}
	state := r.st
	state.Queue = maps.Clone(state.Queue)
	state.SupervisePending = maps.Clone(state.SupervisePending)
	retarget := func(pending map[string]superviseSignal) {
		for id, g := range maps.Clone(pending) {
			next := s.currentAskSignal(g)
			if next.To != g.To {
				delete(pending, id)
				pending[next.id()] = next
			}
		}
	}
	retarget(state.SupervisePending)
	for to, queue := range state.Queue {
		next := slices.Clone(queue)
		for i, msg := range next {
			if msg.Tool != superviseTool || !msg.TriedAt.IsZero() {
				continue // an uncertain send retains its identity and transcript reconciliation
			}
			if signals, ok := decodeDigestSignals(msg.Signals); ok {
				for j, g := range signals {
					signals[j] = s.currentAskSignal(g)
				}
				next[i].Signals = encodeDigestSignals(mergeSignals(nil, signals))
			}
		}
		state.Queue[to] = next
	}
	if st := s.store(); st != nil {
		if err := st.PutSetting(ctx, relayKey, state); err != nil {
			return fmt.Errorf("supervisor saved; pending card checkpoint failed: retry the transfer: %w", err)
		}
	}
	r.st = state
	retarget(b.pending)
	return nil
}

// currentAskSignal also repairs a crash after the supervisor write but before the card queue checkpoint.
// Escalated and already delivered rungs remain explicit destinations.
func (s *Service) currentAskSignal(g superviseSignal) superviseSignal {
	if g.Kind != sigAsk {
		return g
	}
	id, _, _ := strings.Cut(g.Key, "@")
	s.fleet.mu.Lock()
	defer s.fleet.mu.Unlock()
	if p := s.fleet.interactions[id]; p != nil && !p.claimed && !p.told && p.route != nil &&
		p.route.todo == g.Todo && p.route.step == 0 && !p.route.delivered {
		g.To = p.route.at()
		g.batchTo, g.batchUntil = "", time.Time{}
	}
	return g
}

func (s *Service) askRelayDelivered(to string, msg relayMsg) {
	signals, _ := decodeDigestSignals(msg.Signals)
	s.fleet.mu.Lock()
	defer s.fleet.mu.Unlock()
	for _, g := range signals {
		if g.Kind != sigAsk {
			continue
		}
		for _, p := range s.fleet.interactions {
			if r := p.route; r != nil && r.todo == g.Todo && r.at() == to && strings.Contains(msg.Text, "interaction_id="+p.ix.ID+"（") {
				r.delivered = true
			}
		}
	}
}

// A restored card already represented by a durable queued ask must not create a second notification while that
// queue is being retargeted. The structured ask is rendered from the current held cards at the last boundary.
func (s *Service) askAlreadyQueued(todo, to string) bool {
	r := &s.relays
	r.mu.Lock()
	defer r.mu.Unlock()
	if !s.relayLocked(context.Background()) {
		return false
	}
	for _, queue := range r.st.Queue {
		for _, msg := range queue {
			if msg.Tool != superviseTool {
				continue
			}
			list, _ := decodeDigestSignals(msg.Signals)
			for _, g := range list {
				if g.Todo == todo && g.Kind == sigAsk && s.currentAskSignal(g).To == to {
					return true
				}
			}
		}
	}
	return false
}
