package assistant

import (
	"context"
	"time"
)

const supervisionHoldsKey = "assistant.supervision_holds"

type supervisionHold struct {
	At      time.Time `json:"at"`
	Visible bool      `json:"visible,omitempty"`
}

// supervisionEnd carries a turn's hold to the push and channel decisions after the active hold has been cleared.
type supervisionEnd struct {
	key      string
	hold     supervisionHold
	retained bool // the turn still had background work when it stopped
}

type supervisionEndKey struct{}

func (h supervisionHold) active(now time.Time) bool {
	return !h.At.IsZero() && now.Sub(h.At) <= superviseHoldMax
}

// loadSupervisionHoldsLocked reads the manager's hold document before its first use. supervise.mu is held.
func (s *Service) loadSupervisionHoldsLocked(ctx context.Context) error {
	b := &s.supervise
	if b.holdsLoaded {
		return nil
	}
	var holds map[string]supervisionHold
	if st := s.store(); st != nil {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if _, err := st.Setting(cctx, supervisionHoldsKey, &holds); err != nil {
			s.logf("supervise: load push holds: %v", err)
			return err
		}
	}
	if holds == nil {
		holds = make(map[string]supervisionHold)
	}
	b.holds, b.holdsLoaded = holds, true
	return nil
}

// saveSupervisionHoldsLocked serializes each whole-document write with the in-memory transition.
// A failed write remains dirty so the next change or supervision sweep can retry it.
func (s *Service) saveSupervisionHoldsLocked(ctx context.Context) error {
	b := &s.supervise
	if !b.holdsDirty {
		return nil
	}
	if st := s.store(); st != nil {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := st.PutSetting(cctx, supervisionHoldsKey, b.holds); err != nil {
			s.logf("supervise: save push holds: %v", err)
			return err
		}
	}
	b.holdsDirty = false
	return nil
}

// expireSupervisionHolds drops orphaned entries on startup and during the supervision sweep.
func (s *Service) expireSupervisionHolds(ctx context.Context, now time.Time) bool {
	b := &s.supervise
	b.mu.Lock()
	defer b.mu.Unlock()
	loaded := b.holdsLoaded
	if s.loadSupervisionHoldsLocked(ctx) != nil {
		return false
	}
	changed := !loaded && len(b.holds) > 0
	for id, h := range b.holds {
		if !h.active(now) {
			delete(b.holds, id)
			b.holdsDirty = true
			changed = true
		}
	}
	_ = s.saveSupervisionHoldsLocked(ctx)
	return changed
}

// holdPushes records a supervision wake before dispatch so that even its first tool call can see the hold.
func (s *Service) holdPushes(ctx context.Context, id string) error {
	b := &s.supervise
	b.mu.Lock()
	if err := s.loadSupervisionHoldsLocked(ctx); err != nil {
		b.mu.Unlock()
		return err
	}
	previous, existed := b.holds[id]
	b.holds[id] = supervisionHold{At: time.Now().UTC()}
	b.holdsDirty = true
	if err := s.saveSupervisionHoldsLocked(ctx); err != nil {
		if existed {
			b.holds[id] = previous
		} else {
			delete(b.holds, id)
		}
		b.mu.Unlock()
		return err
	}
	delete(b.sideLinks, id)
	b.mu.Unlock()
	s.publish(ctx)
	return nil
}

// releasePushes clears the hold when another message takes over the conversation.
func (s *Service) releasePushes(ctx context.Context, id string) error {
	_, err := s.clearSupervisionHold(ctx, s.supervisionHoldKey(ctx, id), time.Time{})
	return err
}

// clearSupervisionHold takes the ended turn's hold, leaving any hold installed after its end.
// A zero end time clears the conversation for a new input, including any side-supervision links.
func (s *Service) clearSupervisionHold(ctx context.Context, id string, endedAt time.Time) (supervisionHold, error) {
	b := &s.supervise
	b.mu.Lock()
	if err := s.loadSupervisionHoldsLocked(ctx); err != nil {
		b.mu.Unlock()
		return supervisionHold{}, err
	}
	h, held := b.holds[id]
	if endedAt.IsZero() {
		delete(b.sideLinks, id)
	} else if h.At.After(endedAt) {
		h, held = supervisionHold{}, false
	}
	if held {
		delete(b.holds, id)
		b.holdsDirty = true
	}
	err := s.saveSupervisionHoldsLocked(ctx)
	b.mu.Unlock()
	if held {
		s.publish(ctx)
	}
	if !h.active(time.Now()) {
		h = supervisionHold{}
	}
	return h, err
}

func (s *Service) endSupervisionTurn(ctx context.Context, id string, at time.Time, continuing bool) supervisionEnd {
	key := s.supervisionHoldKey(ctx, id)
	if !continuing {
		hold, _ := s.clearSupervisionHold(ctx, key, at)
		return supervisionEnd{key: key, hold: hold}
	}
	b := &s.supervise
	b.mu.Lock()
	defer b.mu.Unlock()
	if hold := b.holds[key]; hold.active(at) && !hold.At.After(at) {
		return supervisionEnd{key: key, hold: hold, retained: true}
	}
	return supervisionEnd{}
}

// supervising reports whether a supervision message started the conversation's current turn.
func (s *Service) supervising(id string) bool {
	b := &s.supervise
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.holds[id].active(time.Now())
}

func (s *Service) heldConversations() []string {
	b := &s.supervise
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	out := make([]string, 0, len(b.holds))
	for id, hold := range b.holds {
		if hold.active(now) {
			out = append(out, id)
		}
	}
	return out
}
