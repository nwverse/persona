package assistant

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
)

const (
	// partnerClaimMost is how many due envelopes the loop takes at a time.
	partnerClaimMost = 20
	// partnerSendersMost is how many partners the loop sends to at once, one envelope in flight to each: a partner that
	// keeps its link up but never answers holds up its own envelopes only.
	partnerSendersMost = 8
)

// partnerLoop hands the queued envelopes to the hub for their partners and takes up incoming ones that did not get in,
// until ctx ends (Run starts it); it returns once its senders have. It looks every partnerTick, and at once when woken
// (a message allowed, a partner online). At its first look it reads the partner list and asks again for every message
// whose card the manager before it had up: a restart cancels those cards (db ExpireStaleInteractions).
func (s *Service) partnerLoop(ctx context.Context) {
	defer s.partners.senders.Wait()
	started := false
	for {
		more := false
		if st := s.partnerStore(); st != nil {
			if !started {
				started = true
				s.partnerStart(ctx, st)
			}
			more = s.partnerDue(ctx, st)
		}
		wait := partnerTick
		if s.partners.tick > 0 {
			wait = s.partners.tick
		}
		if more {
			wait = 0
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-s.partners.wakeC():
			t.Stop()
		case <-t.C:
		}
	}
}

// partnerStart reads the partner list (the hub also sends it after the manager's hello) and raises again the card of
// every message still confirming, once the fleet took back what the last manager left (restoreInteractions cancels
// every card pending then, a card raised before it too).
func (s *Service) partnerStart(ctx context.Context, st partnerStore) {
	if _, err := s.partnerList(ctx); err != nil && !errors.Is(err, errPartnersOff) && ctx.Err() == nil {
		s.logf("partner hubs: the list: %v", err)
	}
	go func() {
		if s.fleetOn() {
			select {
			case <-s.partners.restoredC():
			case <-ctx.Done():
				return
			}
		}
		rows, err := st.PartnerMessagesInState(ctx, partnerOut, partnerConfirming)
		if err != nil {
			if ctx.Err() == nil {
				s.logf("partner hubs: the messages waiting for a card: %v", err)
			}
			return
		}
		for _, m := range rows {
			go s.partnerAsk(m)
		}
	}()
}

// partnerDue acts on the envelopes whose next try is due; true when there may be more. Queued ones go to their
// partner's sender (partnerSendTo), oldest first; incoming ones are taken up here: one not delivered yet, and one
// delivered or failed whose status may not have been queued (partnerStatusOf).
func (s *Service) partnerDue(ctx context.Context, st partnerStore) bool {
	rows, err := st.ClaimPartnerMessages(ctx, partnerLease, partnerClaimMost)
	if err != nil {
		if ctx.Err() == nil {
			s.logf("partner hubs: claim: %v", err)
		}
		return false
	}
	slices.SortStableFunc(rows, func(a, b db.PartnerMessage) int {
		return cmp.Or(a.CreatedAt.Compare(b.CreatedAt), cmp.Compare(a.ID, b.ID))
	})
	for _, m := range rows {
		switch {
		case m.Direction == partnerOut && m.State == partnerQueued:
			s.partnerSendTo(ctx, st, m)
		case m.Direction == partnerIn && m.State == partnerReceived:
			s.partnerDeliverIn(ctx, st, m, true)
		case m.Direction == partnerIn && m.Kind != assistantwire.PartnerKindStatus &&
			(m.State == partnerDelivered || m.State == partnerFailed || m.State == partnerReplied):
			s.partnerStatusOf(ctx, st, m)
		default: // moved on meanwhile: nothing to do
			_, _, _ = partnerMutate(ctx, st, m, func(r *db.PartnerMessage) bool {
				if r.NextTry == nil {
					return false
				}
				r.NextTry = nil
				return true
			})
		}
	}
	return len(rows) == partnerClaimMost
}

// partnerSendTo hands queued envelope m to the sender of its partner: the one sending to it now (a newer claim of a row
// it holds replaces that one), else a new one. With partnerSendersMost partners being sent to already, m keeps its
// lease and is due again after it.
func (s *Service) partnerSendTo(ctx context.Context, st partnerStore, m db.PartnerMessage) {
	b := &s.partners
	b.sendMu.Lock()
	defer b.sendMu.Unlock()
	if q, ok := b.sending[m.PeerHub]; ok {
		if i := slices.IndexFunc(q, func(r db.PartnerMessage) bool { return r.ID == m.ID }); i >= 0 {
			q[i] = m
		} else {
			b.sending[m.PeerHub] = append(q, m)
		}
		return
	}
	if len(b.sending) >= partnerSendersMost {
		return
	}
	if b.sending == nil {
		b.sending = map[string][]db.PartnerMessage{}
	}
	b.sending[m.PeerHub] = []db.PartnerMessage{m}
	b.senders.Add(1)
	go s.partnerSender(ctx, st, m.PeerHub)
}

// partnerSender sends what waits for partner peer, one envelope after another in the order they were written, until
// none is left. One the partner does not take now (no link, its manager away, no answer in time) ends it: what else
// waits for that partner waits as long as that one (and is tried after it), untried meanwhile.
func (s *Service) partnerSender(ctx context.Context, st partnerStore, peer string) {
	b := &s.partners
	defer b.senders.Done()
	for {
		b.sendMu.Lock()
		q := b.sending[peer]
		if len(q) == 0 || ctx.Err() != nil {
			delete(b.sending, peer)
			b.sendMu.Unlock()
			return
		}
		m := q[0]
		b.sending[peer] = q[1:]
		b.sendMu.Unlock()
		next := s.partnerSendOne(ctx, st, m)
		if next == nil {
			continue
		}
		b.sendMu.Lock()
		rest := b.sending[peer]
		delete(b.sending, peer)
		b.sendMu.Unlock()
		for _, r := range rest {
			_, _, _ = partnerMutate(ctx, st, r, func(row *db.PartnerMessage) bool {
				if row.State != partnerQueued {
					return false
				}
				row.NextTry = next
				return true
			})
		}
		return
	}
}

// partnerEnvelope is queued envelope m as it goes.
func partnerEnvelope(m db.PartnerMessage) assistantwire.PartnerEnvelope {
	e := assistantwire.PartnerEnvelope{MessageID: m.MessageID, Kind: m.Kind, ReplyTo: m.ReplyTo, Hop: m.Hop, SentAt: m.CreatedAt.UTC()}
	if m.Kind == assistantwire.PartnerKindStatus {
		e.State, e.Code = m.Text, m.Code // a status's row keeps the state it reports in its text
	} else {
		e.Text = m.Text
	}
	return e
}

// partnerRetry is how long a message waits after its attempts-th try found the partner away: 5 s, doubling to 5 min.
func (s *Service) partnerRetry(attempts int) time.Duration {
	d := partnerRetryFirst
	if s.partners.retry > 0 {
		d = s.partners.retry
	}
	for i := 1; i < attempts && d < partnerRetryMost; i++ {
		d *= 2
	}
	return min(d, partnerRetryMost)
}

// partnerSendOne hands queued envelope m to the hub for its partner. Taken (accepted, or a duplicate of a try that
// went): sent. Refused, or the hubs no longer paired (as the hub says now, or its list did already): failed, and the
// sending conversation is told (a status is not told of). Anything else (no link now, the partner's manager away, an
// answer that never came): it stays queued and is tried again under the same message_id, later each time, and its next
// try is returned; one queued past partnerQueueMax expires (a status too).
func (s *Service) partnerSendOne(ctx context.Context, st partnerStore, m db.PartnerMessage) *time.Time {
	if s.partnerStale(m) { // written for a pairing that ended: never to the hub paired since under its hub_id
		s.partnerUnpaired(ctx, st, m)
		return nil
	}
	now := time.Now().UTC()
	if now.Sub(m.CreatedAt) > partnerQueueMax {
		// A status that waited as long goes too, untold: nobody here waits for it, and kept it would be tried for ever.
		if _, ok, _ := partnerMutate(ctx, st, m, func(r *db.PartnerMessage) bool {
			if r.State != partnerQueued {
				return false
			}
			r.State, r.NextTry = partnerExpired, nil
			return true
		}); ok && m.Kind != assistantwire.PartnerKindStatus {
			s.partnerTell(ctx, m, "stale", "")
		}
		return nil
	}
	state, code := partnerQueued, ""
	var err error
	if pb, ok := s.hub.(partnerBus); ok {
		cctx, cancel := context.WithTimeout(ctx, partnerSendWait)
		var res assistantwire.PartnerResult
		res, err = pb.PartnerSend(cctx, assistantwire.PartnerSend{To: m.PeerHub, Envelope: partnerEnvelope(m)})
		cancel()
		errCode, _ := relayErrCode(err)
		switch {
		case err == nil && (res.Status == assistantwire.PartnerAccepted || res.Status == assistantwire.PartnerDuplicate):
			state = partnerSent
		case err == nil && res.Status == assistantwire.PartnerRefused:
			state, code = partnerFailed, partnerCodeOf(res.Code, assistantwire.PartnerCodeInvalid)
		case err != nil && errCode == assistantwire.CodePartnerUnknown:
			state, code = partnerFailed, assistantwire.PartnerCodeUnpaired
		}
	} else {
		err = errPartnersOff
	}
	if state == partnerQueued && (m.Attempts == 0 || (m.Attempts+1)%10 == 0) && ctx.Err() == nil {
		s.logf("partner %s: %s %s not taken yet (try %d): %v", short(m.PeerHub), m.Kind, m.MessageID, m.Attempts+1, err)
	}
	tried := time.Now().UTC() // the wait for the next try starts after this one, however long it took
	written, changed, merr := partnerMutate(ctx, st, m, func(r *db.PartnerMessage) bool {
		if r.State != partnerQueued {
			return false
		}
		r.Attempts++
		r.State, r.Code, r.NextTry = state, code, nil
		if state == partnerQueued {
			next := tried.Add(s.partnerRetry(r.Attempts))
			r.NextTry = &next
		}
		return true
	})
	if merr != nil {
		s.logf("partner %s: %s: %v", short(m.PeerHub), m.MessageID, merr)
	}
	if state == partnerSent && m.Kind == assistantwire.PartnerKindReply {
		// The partner took the reply: the message it answers is answered, whether this row still read queued (the
		// partner's delivered status can come back over the link before this write).
		s.partnerReplied(ctx, st, m)
	}
	if !changed {
		return nil
	}
	if state == partnerQueued {
		return written.NextTry
	}
	switch {
	case state == partnerFailed && m.Kind != assistantwire.PartnerKindStatus:
		event := "refused"
		if code == assistantwire.PartnerCodeUnpaired {
			event = "unpaired"
		}
		s.partnerTell(ctx, m, event, code)
	}
	return nil
}

// partnerReplied notes the incoming message reply m answers as answered from here.
func (s *Service) partnerReplied(ctx context.Context, st partnerStore, m db.PartnerMessage) {
	in, err := st.PartnerMessageOf(ctx, partnerIn, m.PeerHub, m.ReplyTo)
	if err != nil || in == nil {
		return
	}
	if _, _, err := partnerMutate(ctx, st, *in, func(r *db.PartnerMessage) bool {
		if r.State != partnerDelivered && r.State != partnerReceived {
			return false
		}
		r.State = partnerReplied
		return true
	}); err != nil {
		s.logf("partner %s: replied: %v", in.MessageID, err)
	}
}

// partnerHurry makes what waits for partner peer due now: it came online.
func (s *Service) partnerHurry(peer string) {
	st := s.partnerStore()
	if st == nil {
		return
	}
	ctx := s.runCtx()
	rows, err := st.PartnerMessagesInState(ctx, partnerOut, partnerQueued)
	if err != nil {
		s.logf("partner %s: online: %v", short(peer), err)
		return
	}
	now := time.Now().UTC()
	for _, m := range rows {
		if m.PeerHub != peer || m.NextTry != nil && !m.NextTry.After(now) {
			continue
		}
		_, _, _ = partnerMutate(ctx, st, m, func(r *db.PartnerMessage) bool {
			if r.State != partnerQueued {
				return false
			}
			r.NextTry = &now
			return true
		})
	}
	s.partnerWakeUp()
}
