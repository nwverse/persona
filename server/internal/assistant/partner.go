package assistant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash/fnv"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// Partner hubs (docs/server/partner-hubs.md): another person's hub its owner paired with this one, whose assistant this
// one messages. The hub carries the envelopes over its link and stamps who sent each; everything else is here: the
// owner's OK before a message leaves (partner_tools.go), the durable queue keyed by message_id (partner_loop.go), and
// delivery into a conversation as another assistant's request, never the owner's words (partner_receive.go).

// The relays a partner's envelopes go in with (relayMsg.Tool).
const (
	partnerMessageTool = "partner_message" // a partner's message, into the main conversation
	partnerReplyTool   = "partner_reply"   // a partner's answer, into the conversation that asked
	partnerNoticeTool  = "partner_notice"  // Persona's notice to a conversation of what became of its message
	// partnerFrom starts a partner relay's From: then the partner's hub_id, never a conversation's id.
	partnerFrom = "hub:"
)

// Directions and states of an envelope (db.PartnerMessage).
const (
	partnerOut = "out"
	partnerIn  = "in"

	partnerConfirming = "confirming" // out: waits for the owner's OK on the card
	partnerQueued     = "queued"     // out: may go; waits for the partner to take it
	partnerSent       = "sent"       // out: the partner's manager keeps it
	partnerDelivered  = "delivered"  // out: in the partner's conversation; in: in this hub's
	partnerAnswered   = "answered"   // out: its reply came
	partnerDeclined   = "declined"   // out: the owner said no
	partnerExpired    = "expired"    // out: the card, or the queue, ran out
	partnerFailed     = "failed"     // out, in: not delivered (Code)
	partnerReceived   = "received"   // in: kept, not delivered yet
	partnerReplied    = "replied"    // in: this hub's answer went out
)

// The limits and times of partner messages. The partner's manager holds the envelope limits on its side too.
const (
	partnerRateWindow = 10 * time.Minute
	partnerOutRate    = 20  // messages and replies to one partner a window
	partnerInRate     = 30  // messages and replies from one partner a window
	partnerStatusRate = 120 // statuses from one partner a window
	partnerCardWait   = 24 * time.Hour
	partnerQueueMax   = 7 * 24 * time.Hour // a message queued longer expires
	partnerLease      = time.Minute        // a claimed row's next try while the loop sends it
	partnerSendWait   = 35 * time.Second   // the hub gives the link's call 30 s
	partnerReconcile  = 2 * time.Minute    // an incoming envelope not delivered by then is the loop's again
	partnerTick       = 2 * time.Second
	partnerRetryFirst = 5 * time.Second
	partnerRetryMost  = 5 * time.Minute
)

var partnerMessageIDRe = lazyre.New(`^pm_[0-9a-f]{32}$`)

var errPartnersOff = errors.New("partner hubs are not available on this hub")

// partnerStore is what partner messages keep (db.DB); without it there are none: the tools are not listed and
// partner.receive is answered partner_unavailable.
type partnerStore interface {
	InsertPartnerMessage(ctx context.Context, m db.PartnerMessage) (db.PartnerMessage, bool, error)
	PartnerMessageOf(ctx context.Context, direction, peer, messageID string) (*db.PartnerMessage, error)
	UpdatePartnerMessage(ctx context.Context, m db.PartnerMessage) (bool, error)
	ClaimPartnerMessages(ctx context.Context, lease time.Duration, limit int) ([]db.PartnerMessage, error)
	PartnerMessagesInState(ctx context.Context, direction, state string) ([]db.PartnerMessage, error)
	RecentPartnerMessages(ctx context.Context, peer string, limit int) ([]db.PartnerMessage, error)
	CountPartnerMessages(ctx context.Context, direction, peer string, status bool, since time.Time) (int, error)
	PartnerPrefs(ctx context.Context) ([]db.PartnerPref, error)
	SetPartnerConfirm(ctx context.Context, hub string, confirm bool, pairedAt time.Time) error
}

var _ partnerStore = (*db.DB)(nil)

func (s *Service) partnerStore() partnerStore {
	if s.store == nil {
		return nil
	}
	st, _ := s.store().(partnerStore)
	return st
}

// partnerBus is the hub's partner.send and partner.hubs (BusHub); a hub without it sends nothing.
type partnerBus interface {
	PartnerSend(ctx context.Context, p assistantwire.PartnerSend) (assistantwire.PartnerResult, error)
	ListPartnerHubs(ctx context.Context) (assistantwire.PartnerHubs, error)
}

// PartnerSend is partner.send: the hub hands one envelope to a paired hub over its link.
func (b *BusHub) PartnerSend(ctx context.Context, p assistantwire.PartnerSend) (assistantwire.PartnerResult, error) {
	var res assistantwire.PartnerResult
	err := busError(b.client.Call(ctx, assistantwire.MethodPartnerSend, p, &res))
	return res, err
}

// ListPartnerHubs is partner.hubs: the paired hubs now (not PartnerHubs: the store guard reads that name as the hub table).
func (b *BusHub) ListPartnerHubs(ctx context.Context) (assistantwire.PartnerHubs, error) {
	var res assistantwire.PartnerHubs
	err := busError(b.client.Call(ctx, assistantwire.MethodPartnerHubs, map[string]any{}, &res))
	return res, err
}

// partnerBook is what the manager knows of the paired hubs (the hub's partner.changed), the cards it waits on, and its
// loop's wake-up.
type partnerBook struct {
	mu     sync.Mutex
	known  bool
	list   assistantwire.PartnerHubs
	asking map[string]bool // the rows whose card this process waits on, by id
	once   sync.Once
	wake   chan struct{}
	// restored closes once the fleet took back what the manager before this one left (fleetStart,
	// restoreInteractions): a card raised before that would be cancelled with the stale ones.
	restoredOnce, restoredClose sync.Once
	restored                    chan struct{}
	// gates make one partner's rate check and the row it lets in one step (partnerGate).
	gates [16]sync.Mutex
	// sending is, per partner the loop sends to now, what its sender has still to send, oldest first (partner_loop.go);
	// senders are those senders.
	sendMu  sync.Mutex
	sending map[string][]db.PartnerMessage
	senders sync.WaitGroup
	// tick and retry replace partnerTick and partnerRetryFirst when set (tests).
	tick, retry time.Duration
}

func (b *partnerBook) wakeC() chan struct{} {
	b.once.Do(func() { b.wake = make(chan struct{}, 1) })
	return b.wake
}

func (b *partnerBook) restoredC() chan struct{} {
	b.restoredOnce.Do(func() { b.restored = make(chan struct{}) })
	return b.restored
}

// partnerFleetRestored: the stale cards are gone (fleetStart); the partner messages' cards may go up again.
func (s *Service) partnerFleetRestored() {
	b := &s.partners
	c := b.restoredC()
	b.restoredClose.Do(func() { close(c) })
}

// pairedLocked: at least one hub is paired and partner hubs are on.
func (b *partnerBook) pairedLocked() bool { return b.list.Enabled && len(b.list.Hubs) > 0 }

// partnerWakeUp has the delivery loop look now.
func (s *Service) partnerWakeUp() {
	select {
	case s.partners.wakeC() <- struct{}{}:
	default:
	}
}

// partnersPaired: the hub tools are listed (a store to keep the messages, and a hub paired as the hub last said).
func (s *Service) partnersPaired() bool {
	if s.partnerStore() == nil {
		return false
	}
	b := &s.partners
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.pairedLocked()
}

// partnersChanged takes the hub's list (partner.changed, partner.hubs). The tools come and go with the first partner
// and the last; a partner that came online gets what waits for it now; what waits for a pairing that ended (a hub no
// longer listed, or paired again since) fails as unpaired (partnerSweep).
func (s *Service) partnersChanged(p assistantwire.PartnerHubs) {
	// A partner names itself: its name goes to the model (hub_list, hub_send's answers and errors, the framing) and to
	// the cards only as partnerInline makes it, without hidden characters or marks that could end a quote.
	hubs := make([]assistantwire.PartnerHub, len(p.Hubs))
	for i, h := range p.Hubs {
		h.Name = partnerInline(h.Name, 64)
		hubs[i] = h
	}
	p.Hubs = hubs
	b := &s.partners
	b.mu.Lock()
	before, old, knew := b.pairedLocked(), b.list, b.known
	b.list, b.known = p, true
	after := b.pairedLocked()
	b.mu.Unlock()
	if before != after && s.partnerStore() != nil {
		s.partnerToolsChanged()
	}
	if !p.Enabled {
		if !knew || old.Enabled {
			go s.partnerSweep() // what partners sent and is not in a conversation yet stays out
		}
		return
	}
	if !knew || partnerPairingsEnded(old, p) {
		go s.partnerSweep()
	}
	for _, h := range p.Hubs {
		if h.Online && !partnerOnline(old, h.HubID) {
			go s.partnerHurry(h.HubID)
		}
	}
}

// partnerPairingsEnded: a pairing in old is not in now (unpaired, or paired again under its hub_id).
func partnerPairingsEnded(old, now assistantwire.PartnerHubs) bool {
	if !old.Enabled {
		return true // nothing known of what ended while partner hubs were off
	}
	for _, h := range old.Hubs {
		if n, ok := partnerListed(now, h.HubID); !ok || !samePairing(n.PairedAt, h.PairedAt) {
			return true
		}
	}
	return false
}

// partnerListed is hub as list has it.
func partnerListed(list assistantwire.PartnerHubs, hub string) (assistantwire.PartnerHub, bool) {
	for _, h := range list.Hubs {
		if h.HubID == hub {
			return h, true
		}
	}
	return assistantwire.PartnerHub{}, false
}

// samePairing: two pairing times are one pairing (the database keeps microseconds).
func samePairing(a, b time.Time) bool {
	return a.Truncate(time.Microsecond).Equal(b.Truncate(time.Microsecond))
}

// partnerListNow is the list as the hub last said it; false before it said one.
func (s *Service) partnerListNow() (assistantwire.PartnerHubs, bool) {
	b := &s.partners
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.list, b.known
}

// partnerStaleIn: list (partner hubs on) shows m, an envelope kept for or from a partner, belongs to no pairing there is
// now: its hub is not paired, or the pairing m was written for (m.PairedAt) is not the one there is (paired again since).
// Both times are the hub's, so the manager's clock never comes into it; one not known (a row written before the hub
// said its pairings, a hub that does not say when it paired) is no evidence of another pairing. With partner hubs off the
// hub refuses what goes when it goes.
func partnerStaleIn(list assistantwire.PartnerHubs, m db.PartnerMessage) bool {
	if !list.Enabled {
		// Partner hubs switched off (the emergency switch), or this manager no longer the hub's default: nothing a
		// partner sent goes into a conversation; what goes out is refused by the hub when it goes.
		return m.Direction == partnerIn
	}
	h, ok := partnerListed(list, m.PeerHub)
	if !ok {
		return true
	}
	return !m.PairedAt.IsZero() && !h.PairedAt.IsZero() && !samePairing(m.PairedAt, h.PairedAt)
}

// partnerPairedAt is the pairing with peer as the hub last said it (zero: not known): what a row written now is for.
func (s *Service) partnerPairedAt(peer string) time.Time {
	list, _ := s.partnerListNow()
	h, _ := partnerListed(list, peer)
	return h.PairedAt
}

// partnerStale is partnerStaleIn on the list as the hub last said it (none yet: not stale).
func (s *Service) partnerStale(m db.PartnerMessage) bool {
	list, known := s.partnerListNow()
	return known && partnerStaleIn(list, m)
}

// partnerSweep fails what waits for a pairing that ended: the messages still waiting for the owner's OK (their cards
// taken back) and those queued for the partner (statuses too), and what that partner sent that is not in a
// conversation yet (partnerInUnpaired; one in a relay queue already is dropped as it would go in, partnerRelayStale). A
// message approved for one pairing never goes to the next one under the same hub_id, nor one taken under it into a
// conversation after it.
func (s *Service) partnerSweep() {
	st := s.partnerStore()
	if st == nil {
		return
	}
	ctx := s.runCtx()
	for _, w := range []struct{ direction, state string }{{partnerOut, partnerConfirming}, {partnerOut, partnerQueued},
		{partnerIn, partnerReceived}} {
		rows, err := st.PartnerMessagesInState(ctx, w.direction, w.state)
		if err != nil {
			if ctx.Err() == nil {
				s.logf("partner hubs: the waiting messages: %v", err)
			}
			return
		}
		for _, m := range rows {
			switch {
			case !s.partnerStale(m):
			case w.direction == partnerIn:
				s.partnerInUnpaired(ctx, st, m)
			default:
				s.partnerUnpaired(ctx, st, m)
			}
		}
	}
}

// partnerUnpaired fails m, a message waiting for the owner's OK or queued for a pairing that ended: its card is taken
// back, and the conversation that sent it is told (a status is not told of), once.
func (s *Service) partnerUnpaired(ctx context.Context, st partnerStore, m db.PartnerMessage) {
	was := ""
	m, ok, err := partnerMutate(ctx, st, m, func(r *db.PartnerMessage) bool {
		if r.State != partnerConfirming && r.State != partnerQueued {
			return false
		}
		was = r.State
		r.State, r.Code, r.NextTry = partnerFailed, assistantwire.PartnerCodeUnpaired, nil
		return true
	})
	if err != nil {
		s.logf("partner %s: unpaired: %v", m.MessageID, err)
	}
	if !ok {
		return
	}
	s.logf("partner %s: %s %s of a pairing that ended: failed", short(m.PeerHub), m.Kind, m.MessageID)
	if was == partnerConfirming {
		s.partnerWithdraw(m.CardID)
	}
	if m.Kind != assistantwire.PartnerKindStatus {
		s.partnerTell(ctx, m, "unpaired", assistantwire.PartnerCodeUnpaired)
	}
}

// partnerWithdraw takes back card id of a message that will not go now: it closes dismissed, which no allow follows.
func (s *Service) partnerWithdraw(id string) {
	if id == "" {
		return
	}
	s.questions.mu.Lock()
	q := s.questions.m[id]
	s.questions.mu.Unlock()
	if q == nil {
		return
	}
	r := proto.Resolution{Dismiss: true}
	if err := s.resolve(id, r); err != nil {
		select {
		case q.answer <- r:
		default:
		}
	}
}

// partnerGate holds partner peer's gate for direction until the returned func: a rate check and the row it lets in are
// one step however many envelopes come at once (a link runs a partner's calls side by side, a turn its tool calls).
func (s *Service) partnerGate(direction, peer string) func() {
	h := fnv.New32a()
	_, _ = h.Write([]byte(direction + "\x00" + peer))
	mu := &s.partners.gates[h.Sum32()%uint32(len(s.partners.gates))]
	mu.Lock()
	return mu.Unlock
}

// partnerShortID is how the owner's card names a partner beside its name (which the partner chooses): the start of its
// hub_id, so two of one name, or one named like another, tell apart.
func partnerShortID(hub string) string {
	if len(hub) <= 8 {
		return hub
	}
	return hub[:8]
}

func partnerOnline(list assistantwire.PartnerHubs, hub string) bool {
	for _, h := range list.Hubs {
		if h.HubID == hub {
			return list.Enabled && h.Online
		}
	}
	return false
}

// partnerToolsChanged tells the hub the assistant's tools changed (hub_list and hub_send come with a partner), and the
// running conversations' tool watches (tool_loading.go).
func (s *Service) partnerToolsChanged() {
	if err := s.hub.Tell(assistantwire.EventToolsChanged, map[string]any{}); err != nil {
		s.logf("assistant: %s: %v", assistantwire.EventToolsChanged, err)
	}
	s.mcpChanges.notify()
}

// partnerList is the paired hubs: as the hub last said, else asked now.
func (s *Service) partnerList(ctx context.Context) (assistantwire.PartnerHubs, error) {
	b := &s.partners
	b.mu.Lock()
	list, known := b.list, b.known
	b.mu.Unlock()
	if known {
		return list, nil
	}
	pb, ok := s.hub.(partnerBus)
	if !ok {
		return assistantwire.PartnerHubs{}, errPartnersOff
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	list, err := pb.ListPartnerHubs(cctx)
	if err != nil {
		return assistantwire.PartnerHubs{}, err
	}
	s.partnersChanged(list)
	list, _ = s.partnerListNow() // as partnersChanged keeps it: the names made safe to quote
	return list, nil
}

// partnerInline is s as a prompt quotes it outside a context block (a partner's name, how a message began): one line of
// at most n characters, without the characters nobody would see (partnerVisible: a name spelling out hidden text to
// the model), its angle brackets made look-alikes, so it can neither open nor close a block, and its quote marks too,
// so it cannot end the quotes the prompts put it in and go on as their own words.
func partnerInline(s string, n int) string {
	s, _ = partnerVisible(s)
	return partnerInlineMarks.Replace(clip(oneLine(s), n))
}

var partnerInlineMarks = strings.NewReplacer("<", "‹", ">", "›", `"`, "'", "「", "『", "」", "』", "“", "‘", "”", "’") // i18n-ignore: the prompts' quote marks, not words

// partnerOnceID is the once_id a partner's envelope goes into a conversation under, on every try and after restarts.
func partnerOnceID(peer, messageID string) string {
	return "partner-" + partnerHash(peer, messageID)[:24]
}

func partnerHash(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// partnerMutate writes change to envelope m, re-reading it and changing it again when another writer was first;
// change says false when the row is past what it would do. The row as written, and whether it was.
func partnerMutate(ctx context.Context, st partnerStore, m db.PartnerMessage, change func(*db.PartnerMessage) bool) (db.PartnerMessage, bool, error) {
	for range 5 {
		next := m
		if !change(&next) {
			return m, false, nil
		}
		ok, err := st.UpdatePartnerMessage(ctx, next)
		if err != nil {
			return m, false, err
		}
		if ok {
			next.Revision++
			return next, true, nil
		}
		cur, err := st.PartnerMessageOf(ctx, m.Direction, m.PeerHub, m.MessageID)
		if err != nil || cur == nil {
			return m, false, err
		}
		m = *cur
	}
	return m, false, errors.New("partner message: changed by another writer five times")
}

// partnerLine: it is a partner hub's message or reply as a user line (its source kind hub). A summary or a handoff that
// carries the last messages on leaves it out: there every user line reads as the user's own words, without the framing
// that said it was another hub's request (conversation_read still reads it).
func partnerLine(it proto.Item) bool {
	if it.Role != "user" {
		return false
	}
	src := prompts.ItemSource(it)
	return src != nil && src.Kind == proto.SourceHub
}
