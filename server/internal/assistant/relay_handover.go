package assistant

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"sync"
	"time"
)

// Hand-overs of the owner's words to and from a direction's lead (#11699, docs/assistant/unified-entry.md "Across a
// relay"). A conversation_send that carries a verified quote (relay_quote.go) and has a direction's lead conversation
// (isLeader, not a task leader Agent) on either end is one hand-over, recorded once the send is accepted: sent at once
// or queued. A send refused at once (its model has the error), one without a quote and one between two conversations
// neither of which is a lead have none. Its state follows what the relay does with the message, written where the relay
// decides it and only once it is true, with one log line per change:
//   - queued: the message waits in the receiver's queue (queueRelay);
//   - delivered: it went in (relayDelivered: sent at once, from the queue, or a try in doubt found in the transcript);
//   - answered: the receiver's first conversation_send back to the sender since then went into the sender
//     (relayMsg.Answers, handoverToAnswer);
//   - not_delivered: it will not be tried again (Code: the refusal's code, or handoverGone).
//
// The entries are a lazy settings record shaped like the verified quotes, kept as long and as many. Nothing reads them
// yet but the log.
const (
	handoversKey   = "assistant.handovers" // settings key of the hand-overs
	handoverMemory = quoteMemory           // an entry is kept this long …
	handoversKept  = quotesKept            // … and at most this many, the oldest dropped first
)

// The states of a hand-over.
const (
	handoverQueued       = "queued"
	handoverDelivered    = "delivered"
	handoverAnswered     = "answered"
	handoverNotDelivered = "not_delivered"
	// handoverGone is not_delivered's code when the receiver is gone.
	handoverGone = "gone"
)

// handoverMoves are the states a hand-over comes to each state from ("": a send not accepted yet). Nothing else moves
// it, so a state is never undone, and an answer needs a hand-over that went in.
var handoverMoves = map[string][]string{
	handoverQueued:       {""},
	handoverDelivered:    {"", handoverQueued},
	handoverAnswered:     {handoverDelivered},
	handoverNotDelivered: {handoverQueued},
}

// handover is one hand-over of the owner's words.
type handover struct {
	ID      string `json:"id"`
	From    string `json:"from"`     // the sending conversation
	To      string `json:"to"`       // the receiving conversation …
	ToTitle string `json:"to_title"` // … by its name when it was sent
	// Conversation and MessageID: where the owner wrote the words (BriefRequest's).
	Conversation string    `json:"conversation"`
	MessageID    string    `json:"message_id"`
	State        string    `json:"state"`
	Code         string    `json:"code,omitempty"` // not_delivered: why
	At           time.Time `json:"at"`             // when it was made
	ChangedAt    time.Time `json:"changed_at"`     // when its state last changed
}

// handoverBook holds the hand-overs. Its lock is taken before the relay lock (handoverToAnswer), never after it.
type handoverBook struct {
	mu     sync.Mutex
	loaded bool
	m      map[string]handover // by id
	// pending: by id, the hand-over of a send not accepted yet, in memory. It becomes an entry when its message is
	// queued or goes in (handoverSet), and is forgotten when the send is refused at once.
	pending map[string]handover
	// replying: the hand-overs a reply being sent now answers (handoverToAnswer), in memory.
	replying map[string]bool
}

func (b *handoverBook) loadLocked(ctx context.Context, s *Service) bool {
	if b.loaded {
		return true
	}
	m := map[string]handover{}
	if st := s.store(); st != nil {
		if _, err := st.Setting(ctx, handoversKey, &m); err != nil {
			s.logf("load hand-overs: %v", err)
			return false
		}
	}
	if m == nil {
		m = map[string]handover{}
	}
	b.m, b.loaded = m, true
	return true
}

// saveLocked forgets the entries older than handoverMemory and, past handoversKept, the oldest, then saves the rest.
func (b *handoverBook) saveLocked(s *Service, now time.Time) {
	ids := make([]string, 0, len(b.m))
	for id, e := range b.m {
		if now.Sub(e.At) > handoverMemory {
			delete(b.m, id)
			continue
		}
		ids = append(ids, id)
	}
	if len(ids) > handoversKept {
		slices.SortFunc(ids, func(x, y string) int { return b.m[x].At.Compare(b.m[y].At) })
		for _, id := range ids[:len(ids)-handoversKept] {
			delete(b.m, id)
		}
	}
	st := s.store()
	if st == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := st.PutSetting(ctx, handoversKey, b.m); err != nil {
		s.logf("save hand-overs: %v", err)
	}
}

// handoverFor is the hand-over a conversation_send from → to (toTitle) is when it carries the owner's verified words
// (quote): its id goes on the message (relayMsg.Handover). "" when it is none: no quote, or neither end is a
// direction's lead. It becomes an entry only once the send is accepted; done forgets it if it was not.
func (s *Service) handoverFor(ctx context.Context, from, to, toTitle string, quote *BriefRequest) (string, func()) {
	if quote == nil || !s.isLeader(ctx, from) && !s.isLeader(ctx, to) {
		return "", func() {}
	}
	now := time.Now().UTC()
	e := handover{ID: "handover_" + randomHex(8), From: from, To: to, ToTitle: toTitle, Conversation: quote.Conversation,
		MessageID: quote.MessageID, At: now, ChangedAt: now}
	b := &s.handovers
	b.mu.Lock()
	if b.pending == nil {
		b.pending = map[string]handover{}
	}
	b.pending[e.ID] = e
	b.mu.Unlock()
	return e.ID, func() {
		b.mu.Lock()
		delete(b.pending, e.ID)
		b.mu.Unlock()
	}
}

// handoverSet moves hand-over id to state (code: why it was not delivered) when it may come there from where it is
// (handoverMoves) and check, if any, holds of it; then it is saved and the change logged. A pending one becomes an
// entry here. False when nothing changed.
func (s *Service) handoverSet(id, state, code string, check func(handover) bool) bool {
	if id == "" {
		return false
	}
	b := &s.handovers
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.loadLocked(context.Background(), s) {
		return false
	}
	e, ok := b.m[id]
	if !ok {
		if e, ok = b.pending[id]; !ok {
			return false // never accepted, or forgotten since
		}
	}
	if !slices.Contains(handoverMoves[state], e.State) || check != nil && !check(e) {
		return false
	}
	now := time.Now().UTC()
	e.State, e.Code, e.ChangedAt = state, code, now
	b.m[id] = e
	delete(b.pending, id)
	what := state
	if code != "" {
		what += " (" + code + ")"
	}
	s.logf("hand-over %s from %s to %s: %s", id, short(e.From), short(e.To), what)
	b.saveLocked(s, now)
	return true
}

// handoverWentIn: message msg went into conversation to (relayDelivered). The hand-over it is has been delivered, and
// the one it answers has been answered.
func (s *Service) handoverWentIn(to string, msg relayMsg) {
	s.handoverSet(msg.Handover, handoverDelivered, "", func(e handover) bool { return e.From == msg.From && e.To == to })
	s.handoverSet(msg.Answers, handoverAnswered, "", func(e handover) bool { return e.From == to && e.To == msg.From })
}

// handoverEnded: hand-over id will not be tried again (code: why).
func (s *Service) handoverEnded(id, code string) {
	s.handoverSet(id, handoverNotDelivered, code, nil)
}

// handoverToAnswer is the hand-over a conversation_send from → to answers (relayMsg.Answers): the oldest one from to to
// from that was delivered and that no other reply on its way answers, queued for to or being sent now. "" for none.
// done lets go of it once the reply is queued, went in or was refused: a reply that does not go in leaves it delivered,
// for the next one to answer.
func (s *Service) handoverToAnswer(from, to string) (string, func()) {
	b := &s.handovers
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.loadLocked(context.Background(), s) {
		return "", func() {}
	}
	var open []handover
	for _, e := range b.m {
		if e.State == handoverDelivered && e.From == to && e.To == from && !b.replying[e.ID] {
			open = append(open, e)
		}
	}
	if len(open) == 0 {
		return "", func() {}
	}
	queued := s.queuedAnswers(to)
	slices.SortFunc(open, func(x, y handover) int { return cmp.Or(x.At.Compare(y.At), strings.Compare(x.ID, y.ID)) })
	for _, e := range open {
		if queued[e.ID] {
			continue
		}
		if b.replying == nil {
			b.replying = map[string]bool{}
		}
		b.replying[e.ID] = true
		return e.ID, func() {
			b.mu.Lock()
			delete(b.replying, e.ID)
			b.mu.Unlock()
		}
	}
	return "", func() {}
}

// queuedAnswers are the hand-overs the replies queued for conversation to answer.
func (s *Service) queuedAnswers(to string) map[string]bool {
	b := &s.relays
	b.mu.Lock()
	defer b.mu.Unlock()
	out := map[string]bool{}
	if !s.relayLocked(context.Background()) {
		return out
	}
	for _, m := range b.st.Queue[to] {
		if m.Answers != "" {
			out[m.Answers] = true
		}
	}
	return out
}

// queueLeads are the conversations that are a direction's lead (isLeader), for a drain to rank a hand-over in a lead's
// queue ahead of the rest (relayMsg.rankIn). They are read before the relay lock is taken, and only while a queue holds a
// hand-over, the one message whose rank they change: nil otherwise. One queued meanwhile ranks with the rest until the
// next drain.
func (s *Service) queueLeads(ctx context.Context) map[string]bool {
	b := &s.relays
	b.mu.Lock()
	held := false
	if s.relayLocked(ctx) {
		for _, q := range b.st.Queue {
			if slices.ContainsFunc(q, func(m relayMsg) bool { return m.Handover != "" }) {
				held = true
				break
			}
		}
	}
	b.mu.Unlock()
	if !held {
		return nil
	}
	out := map[string]bool{}
	for _, id := range s.settings(ctx).Leaders {
		if s.isLeader(ctx, id) {
			out[id] = true
		}
	}
	return out
}
