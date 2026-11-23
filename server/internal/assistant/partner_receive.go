package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/bus"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// partner.receive: an envelope a paired hub sent, as this hub hands it over (FromHub from the link's credential,
// FromName from its own pairing record). It is kept once per message_id and answered at once; delivery comes after: a
// message into the main conversation, a reply into the conversation that asked, a status onto the message it is about.
// A partner's words go in through the relay queue as another assistant's request (prompts/assistant/partner_message.md)
// with a message source of kind hub, never as the owner's: no client_msg_id, no quote or hand-over of their words, and
// none of the provenance the hub attests for a person's own request.

// errPartnerRefused: the conversation refused a partner's envelope for good, and relayRefused handled it: a reply moved
// to the main conversation (noted delivered there, partnerMovedIn), anything else told the partner it failed.
var errPartnerRefused = errors.New("the conversation refused it for good")

// partnerCodeOf is code, a partner's (a refusal's, a failed status's), when it is one of the codes the contract has, else
// fallback: what a partner says goes into this hub's notices and lists only as one of those words, never as text of its
// own.
func partnerCodeOf(code, fallback string) string {
	switch code {
	case assistantwire.PartnerCodeInvalid, assistantwire.PartnerCodeTooLong, assistantwire.PartnerCodeHopLimit,
		assistantwire.PartnerCodeRateLimited, assistantwire.PartnerCodeNoMain, assistantwire.PartnerCodeUnpaired,
		assistantwire.PartnerCodeUndelivered:
		return code
	}
	return fallback
}

// partnerRequest answers partner.receive; false for any other method.
func (s *Service) partnerRequest(ctx context.Context, method string, params json.RawMessage) (any, bool, error) {
	if method != assistantwire.MethodPartnerReceive {
		return nil, false, nil
	}
	var p assistantwire.PartnerReceive
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, true, &rpc.Error{Code: bus.CodeInvalidParams, Message: method + ": " + err.Error()}
	}
	res, err := s.partnerReceive(ctx, p)
	return res, true, err
}

func partnerUnavailable(err error) error {
	return &rpc.Error{Code: assistantwire.CodePartnerUnavailable, Message: "partner messages can't be kept now: " + err.Error()}
}

// partnerReceive keeps envelope p and says so (accepted), or that it has it (duplicate), or why it will not take it
// (refused): an envelope that does not read, too long, past the hop limit, about no message this hub sent, or one too
// many from that hub in a while. A store that cannot keep it answers partner_unavailable: the partner sends it again.
func (s *Service) partnerReceive(ctx context.Context, p assistantwire.PartnerReceive) (assistantwire.PartnerResult, error) {
	st := s.partnerStore()
	if st == nil {
		return assistantwire.PartnerResult{}, partnerUnavailable(errPartnersOff)
	}
	e, peer := p.Envelope, strings.TrimSpace(p.FromHub)
	refused := func(code string) (assistantwire.PartnerResult, error) {
		s.logf("partner %q: refused %q %q: %s", clip(peer, 64), clip(e.Kind, 16), clip(e.MessageID, 40), code) // the partner's words, quoted
		return assistantwire.PartnerResult{Status: assistantwire.PartnerRefused, Code: code}, nil
	}
	if !partnerHubIDRe.MatchString(peer) || !partnerMessageIDRe.MatchString(e.MessageID) {
		return refused(assistantwire.PartnerCodeInvalid)
	}
	if had, err := st.PartnerMessageOf(ctx, partnerIn, peer, e.MessageID); err != nil {
		return assistantwire.PartnerResult{}, partnerUnavailable(err)
	} else if had != nil {
		return assistantwire.PartnerResult{Status: assistantwire.PartnerDuplicate}, nil
	}
	now := time.Now().UTC()
	m := db.PartnerMessage{ID: "pr_" + randomHex(12), Direction: partnerIn, PeerHub: peer, PeerName: partnerInline(p.FromName, 64),
		MessageID: e.MessageID, Kind: e.Kind, Hop: 1, State: partnerReceived, PairedAt: s.partnerPairedAt(peer), CreatedAt: now}
	if m.PeerName == "" {
		m.PeerName = peer
	}
	status := false
	switch e.Kind {
	case assistantwire.PartnerKindMessage, assistantwire.PartnerKindReply:
		text, _, _, err := partnerScrub(partnerPlain(e.Text))
		if err != nil || text == "" {
			return refused(assistantwire.PartnerCodeInvalid)
		}
		if utf8.RuneCountInString(text) > assistantwire.PartnerTextMax {
			return refused(assistantwire.PartnerCodeTooLong)
		}
		m.Text = text
		if e.Kind == assistantwire.PartnerKindReply {
			out, err := st.PartnerMessageOf(ctx, partnerOut, peer, e.ReplyTo)
			if err != nil {
				return assistantwire.PartnerResult{}, partnerUnavailable(err)
			}
			if out == nil || out.Kind == assistantwire.PartnerKindStatus {
				return refused(assistantwire.PartnerCodeInvalid)
			}
			m.ReplyTo, m.Hop = e.ReplyTo, out.Hop+1
		}
		if m.Hop > assistantwire.PartnerHopMax {
			return refused(assistantwire.PartnerCodeHopLimit)
		}
	case assistantwire.PartnerKindStatus:
		if e.State != assistantwire.PartnerStateDelivered && e.State != assistantwire.PartnerStateFailed {
			return refused(assistantwire.PartnerCodeInvalid)
		}
		out, err := st.PartnerMessageOf(ctx, partnerOut, peer, e.ReplyTo)
		if err != nil {
			return assistantwire.PartnerResult{}, partnerUnavailable(err)
		}
		if out == nil {
			return refused(assistantwire.PartnerCodeInvalid)
		}
		m.ReplyTo, m.Hop, m.Text, status = e.ReplyTo, out.Hop, e.State, true
		if e.State == assistantwire.PartnerStateFailed {
			m.Code = partnerCodeOf(e.Code, assistantwire.PartnerCodeUndelivered)
		}
	default:
		return refused(assistantwire.PartnerCodeInvalid)
	}
	most := partnerInRate
	if status {
		most = partnerStatusRate
	}
	// The count and the row it lets in are one step: a link runs a partner's calls side by side, and envelopes sent
	// all at once get no more past the limit than one after another.
	unlock := s.partnerGate(partnerIn, peer)
	if n, err := st.CountPartnerMessages(ctx, partnerIn, peer, status, now.Add(-partnerRateWindow)); err != nil {
		unlock()
		return assistantwire.PartnerResult{}, partnerUnavailable(err)
	} else if n >= most {
		unlock()
		return refused(assistantwire.PartnerCodeRateLimited)
	}
	again := now.Add(partnerReconcile) // the loop takes it up then, if what follows did not get it in
	m.NextTry = &again
	m, created, err := st.InsertPartnerMessage(ctx, m)
	unlock()
	if err != nil {
		return assistantwire.PartnerResult{}, partnerUnavailable(err)
	}
	if !created {
		return assistantwire.PartnerResult{Status: assistantwire.PartnerDuplicate}, nil
	}
	go s.partnerDeliverIn(s.runCtx(), st, m, false)
	return assistantwire.PartnerResult{Status: assistantwire.PartnerAccepted}, nil
}

// partnerPlain is a partner's text without control characters but line breaks and tabs.
func partnerPlain(text string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || !unicode.IsControl(r) {
			return r
		}
		return -1
	}, text)
}

// partnerDeliverIn delivers incoming envelope m: a message into the main conversation (as it is now), a reply into
// the conversation that sent what it answers (the main one when that is gone), a status onto that message. In went
// means in the conversation or durably queued for it, and the partner is told; a refusal for good tells it too
// (partnerRelayRefused). Anything else leaves m received, for the loop (again: a try before this one may have gone in).
func (s *Service) partnerDeliverIn(ctx context.Context, st partnerStore, m db.PartnerMessage, again bool) {
	if s.partnerStale(m) { // taken under a pairing that ended since: never into a conversation after it
		s.partnerInUnpaired(ctx, st, m)
		return
	}
	if m.Kind == assistantwire.PartnerKindStatus {
		s.partnerStatusIn(ctx, st, m)
		return
	}
	var machine, to string
	var out *db.PartnerMessage
	language := answerLanguage(s.hello(ctx).Locale)
	// The exchange's own hops are the envelopes' (PartnerHopMax); among this hub's conversations it is one hop from
	// elsewhere, so a long exchange does not use up the conversation's relays (relayMaxHops).
	msg := relayMsg{OnceID: partnerOnceID(m.PeerHub, m.MessageID), From: partnerFrom + m.PeerHub, FromTitle: m.PeerName,
		Text: m.Text, Hop: 1, At: m.CreatedAt.UTC(), Partner: m.MessageID}
	switch m.Kind {
	case assistantwire.PartnerKindMessage:
		cfg := s.settings(ctx)
		if cfg.Main == "" {
			s.partnerInEnded(ctx, st, m, partnerFailed, assistantwire.PartnerCodeNoMain, "", "")
			return
		}
		machine, to = cfg.mainRouteMachine(), cfg.Main
		if s.isWorkMachine(machine) {
			s.partnerInEnded(ctx, st, m, partnerFailed, assistantwire.PartnerCodeUndelivered, "", "")
			return
		}
		msg.Tool = partnerMessageTool
		msg.PartnerText = prompts.RenderPartnerMessage(prompts.PartnerMessage{Language: language, Hub: m.PeerName, HubID: m.PeerHub,
			MessageID: m.MessageID, Text: m.Text})
	case assistantwire.PartnerKindReply:
		var err error
		if out, err = st.PartnerMessageOf(ctx, partnerOut, m.PeerHub, m.ReplyTo); err != nil {
			s.logf("partner %s: reply %s: %v", short(m.PeerHub), m.MessageID, err)
			return
		}
		if out == nil {
			s.partnerInEnded(ctx, st, m, partnerFailed, assistantwire.PartnerCodeInvalid, "", "")
			return
		}
		if machine, to = s.partnerConversation(ctx, out.MachineID, out.SessionID); to == "" {
			s.partnerInEnded(ctx, st, m, partnerFailed, assistantwire.PartnerCodeNoMain, "", "")
			return
		}
		msg.Tool, msg.Answer = partnerReplyTool, true
		msg.PartnerText = prompts.RenderPartnerReply(prompts.PartnerReply{Language: language, Hub: m.PeerName, HubID: m.PeerHub,
			MessageID: m.MessageID, Sent: partnerInline(out.Text, 200), Text: m.Text})
	default:
		s.partnerInEnded(ctx, st, m, partnerFailed, assistantwire.PartnerCodeInvalid, "", "")
		return
	}
	if again {
		msg.TriedAt = m.CreatedAt
		if landed, _ := s.relayArrived(machine, to, msg); landed {
			s.partnerInWent(ctx, st, m, out, machine, to)
			return
		}
	}
	err := s.partnerRelay(ctx, machine, to, msg)
	switch {
	case err == nil:
		s.partnerInWent(ctx, st, m, out, machine, to)
	case errors.Is(err, errPartnerRefused): // noted already: moved to the main conversation, or failed and told
	default:
		s.logf("partner %s: %s %s into %s: %v; tried again later", short(m.PeerHub), m.Kind, m.MessageID, short(to), err)
	}
}

// partnerInUnpaired fails incoming m, taken under a pairing that ended (the hub unpaired, or paired again since) and not
// in a conversation yet (received, or delivered into a relay queue it has not left), as unpaired: it goes in nowhere,
// and the hub it came from, no longer that partner, is sent no status.
func (s *Service) partnerInUnpaired(ctx context.Context, st partnerStore, m db.PartnerMessage) {
	m, ok, err := partnerMutate(ctx, st, m, func(r *db.PartnerMessage) bool {
		if r.State != partnerReceived && r.State != partnerDelivered {
			return false
		}
		r.State, r.Code, r.NextTry = partnerFailed, assistantwire.PartnerCodeUnpaired, nil
		return true
	})
	if err != nil {
		s.logf("partner %s: %s unpaired: %v", short(m.PeerHub), m.MessageID, err)
	}
	if ok {
		s.logf("partner %s: %s %s of a pairing that ended: dropped", short(m.PeerHub), m.Kind, m.MessageID)
	}
}

// partnerRelayStale: msg, a partner's message or reply waiting in a conversation's relay queue, came under a pairing
// that ended since (its row's, partnerStale; one without a row by its hub alone): drainRelays drops it as it would go
// in, and its row fails as unpaired, untold (partnerInUnpaired). A list that cannot be read now lets it go as it is.
func (s *Service) partnerRelayStale(ctx context.Context, msg relayMsg) bool {
	if msg.Tool != partnerMessageTool && msg.Tool != partnerReplyTool {
		return false
	}
	st := s.partnerStore()
	if st == nil {
		return false
	}
	list, err := s.partnerList(ctx)
	if err != nil {
		return false
	}
	m := &db.PartnerMessage{Direction: partnerIn, PeerHub: strings.TrimPrefix(msg.From, partnerFrom), MessageID: msg.Partner}
	if msg.Partner != "" {
		row, err := st.PartnerMessageOf(ctx, partnerIn, m.PeerHub, msg.Partner)
		if err != nil {
			return false
		}
		if row != nil {
			m = row
		}
	}
	if !partnerStaleIn(list, *m) {
		return false
	}
	if m.ID != "" {
		s.partnerInUnpaired(ctx, st, *m)
	} else {
		s.logf("partner %s: queued %s of a pairing that ended: dropped", short(m.PeerHub), msg.Tool)
	}
	return true
}

// partnerInWent notes incoming m delivered into conversation to (a reply's out: the message it answers, answered now)
// and tells the partner.
func (s *Service) partnerInWent(ctx context.Context, st partnerStore, m db.PartnerMessage, out *db.PartnerMessage, machine, to string) {
	s.partnerInEnded(ctx, st, m, partnerDelivered, "", machine, to)
	if out == nil {
		return
	}
	if _, _, err := partnerMutate(ctx, st, *out, func(r *db.PartnerMessage) bool {
		if r.State != partnerQueued && r.State != partnerSent && r.State != partnerDelivered {
			return false
		}
		r.State, r.NextTry = partnerAnswered, nil
		return true
	}); err != nil {
		s.logf("partner %s: answered: %v", out.MessageID, err)
	}
}

// partnerInEnded notes what became of incoming m (delivered into machine's conversation to, or failed with code) and
// tells the partner with a status; once: an envelope past received is left as it is. The row keeps a next try until
// its status is queued, so a status that could not be kept (the database failed, the manager stopped in between) is
// the loop's to queue (partnerStatusOf).
func (s *Service) partnerInEnded(ctx context.Context, st partnerStore, m db.PartnerMessage, state, code, machine, to string) {
	title := ""
	if to != "" {
		title = s.partnerTitle(ctx, machine, to)
	}
	check := time.Now().UTC().Add(partnerReconcile)
	m, ok, err := partnerMutate(ctx, st, m, func(r *db.PartnerMessage) bool {
		if r.State != partnerReceived {
			return false
		}
		r.State, r.Code, r.NextTry = state, code, &check
		if to != "" {
			r.MachineID, r.SessionID, r.Title = machine, to, title
		}
		return true
	})
	if err != nil {
		s.logf("partner %s: %s %s: %v", short(m.PeerHub), m.MessageID, state, err)
	}
	if !ok {
		return
	}
	s.partnerStatusOf(ctx, st, m)
}

// partnerStatusOf queues the status of incoming m, a message or reply past received, for its partner (delivered, or
// failed with m's code), and then clears m's next try. One that could not be queued keeps it: the loop tries again then.
func (s *Service) partnerStatusOf(ctx context.Context, st partnerStore, m db.PartnerMessage) {
	state, code := assistantwire.PartnerStateDelivered, ""
	if m.State == partnerFailed {
		state, code = assistantwire.PartnerStateFailed, m.Code
	}
	if !s.partnerStatusOut(ctx, st, m, state, code) {
		return
	}
	if _, _, err := partnerMutate(ctx, st, m, func(r *db.PartnerMessage) bool {
		if r.NextTry == nil || r.State != m.State { // moved on since (failed after all): that change queues its own
			return false
		}
		r.NextTry = nil
		return true
	}); err != nil {
		s.logf("partner %s: status of %s queued: %v", short(m.PeerHub), m.MessageID, err)
	}
}

// partnerStatusOut queues the status of incoming m for its partner: what became of it (state: delivered or failed with
// code). Its message_id comes from m and the state, so a second try adds nothing. False when it could not be kept.
func (s *Service) partnerStatusOut(ctx context.Context, st partnerStore, m db.PartnerMessage, state, code string) bool {
	now := time.Now().UTC()
	row := db.PartnerMessage{ID: "pr_" + randomHex(12), Direction: partnerOut, PeerHub: m.PeerHub, PeerName: m.PeerName,
		MessageID: partnerStatusID(m, state), Kind: assistantwire.PartnerKindStatus, ReplyTo: m.MessageID,
		Hop: m.Hop, Text: state, Code: code, State: partnerQueued, NextTry: &now, PairedAt: m.PairedAt, CreatedAt: now}
	if row.PairedAt.IsZero() {
		row.PairedAt = s.partnerPairedAt(m.PeerHub)
	}
	if _, _, err := st.InsertPartnerMessage(ctx, row); err != nil {
		s.logf("partner %s: status of %s: %v", short(m.PeerHub), m.MessageID, err)
		return false
	}
	s.partnerWakeUp()
	return true
}

// partnerStatusID is the message_id of the status (state) of incoming m: the same on every try.
func partnerStatusID(m db.PartnerMessage, state string) string {
	return "pm_" + partnerHash("status", m.ID, state)[:32]
}

// partnerStatusIn applies a partner's status m to the message it is about: in its conversation now, or not delivered
// (the sending conversation is told).
func (s *Service) partnerStatusIn(ctx context.Context, st partnerStore, m db.PartnerMessage) {
	out, err := st.PartnerMessageOf(ctx, partnerOut, m.PeerHub, m.ReplyTo)
	if err != nil {
		s.logf("partner %s: status %s: %v", short(m.PeerHub), m.MessageID, err)
		return
	}
	if out != nil {
		failed := m.Text == assistantwire.PartnerStateFailed
		_, changed, err := partnerMutate(ctx, st, *out, func(r *db.PartnerMessage) bool {
			switch {
			case failed && (r.State == partnerQueued || r.State == partnerSent || r.State == partnerDelivered):
				r.State, r.Code, r.NextTry = partnerFailed, m.Code, nil
			case !failed && (r.State == partnerQueued || r.State == partnerSent):
				r.State, r.NextTry = partnerDelivered, nil
			default:
				return false
			}
			return true
		})
		if err != nil {
			s.logf("partner %s: status of %s: %v", short(m.PeerHub), out.MessageID, err)
			return
		}
		if changed && failed && out.Kind != assistantwire.PartnerKindStatus {
			s.partnerTell(ctx, *out, "undelivered", m.Code)
		}
		if changed && !failed && out.Kind == assistantwire.PartnerKindReply {
			s.partnerReplied(ctx, st, *out) // delivered there before partnerSendOne wrote sent
		}
	}
	if _, _, err := partnerMutate(ctx, st, m, func(r *db.PartnerMessage) bool {
		if r.State != partnerReceived {
			return false
		}
		r.State, r.NextTry = partnerDelivered, nil
		return true
	}); err != nil {
		s.logf("partner %s: status %s: %v", short(m.PeerHub), m.MessageID, err)
	}
}

// partnerConversation is where to tell or answer machine's conversation session: there while it is there (or its
// computer cannot say), else the main conversation; "" when there is neither.
func (s *Service) partnerConversation(ctx context.Context, machine, session string) (string, string) {
	if machine != "" && session != "" {
		var res struct {
			Session proto.Session `json:"session"`
			OK      bool          `json:"ok"`
		}
		err := s.call(ctx, machine, DeviceSessionGet, map[string]any{"session_id": session}, &res)
		if err != nil || res.OK && !res.Session.IsDeleted() {
			return machine, session
		}
	}
	if cfg := s.settings(ctx); cfg.Main != "" {
		return cfg.mainRouteMachine(), cfg.Main
	}
	return "", ""
}

// partnerRelay puts msg into machine's conversation to: now, or durably queued behind its turn (a copy waiting
// already counts). nil once it went in or waits there; errPartnerRefused when the conversation refused it for good,
// which relayRefused handled.
func (s *Service) partnerRelay(ctx context.Context, machine, to string, msg relayMsg) error {
	c, ok := s.session(ctx, machine, to)
	if !ok || c.Status == proto.StatusWorking || c.Starting || s.relayWaits(to, msg) {
		if err := s.queuePartnerRelay(to, msg); err != nil {
			return err
		}
		s.publish(ctx) // shown waiting above its composer
		if ok && c.Status != proto.StatusWorking && !c.Starting {
			s.drainRelays(machine, []proto.Session{c})
		}
		return nil
	}
	err := s.tryDirectRelay(ctx, machine, to, &msg)
	if err == nil {
		return nil
	}
	if s.refusedDirect(ctx, machine, to, msg, err) {
		return errPartnerRefused
	}
	if qerr := s.queuePartnerRelay(to, msg); qerr != nil {
		return qerr
	}
	s.publish(ctx)
	if !relayInDoubt(err) {
		s.drainAfter(machine, to, relayRetryGap)
	}
	return nil
}

// queuePartnerRelay queues msg for conversation to, saved before it returns, unless a copy waits there already.
func (s *Service) queuePartnerRelay(to string, msg relayMsg) error {
	b := &s.relays
	id := relayMsgID(to, msg)
	b.mu.Lock()
	if !s.relayLocked(context.Background()) {
		b.mu.Unlock()
		return errors.New("the relay queue cannot be read")
	}
	for _, q := range b.st.Queue[to] {
		if relayMsgID(to, q) == id {
			b.mu.Unlock()
			return nil
		}
	}
	b.mu.Unlock()
	_, err := s.queueRelayDurable(to, msg)
	return err
}

// partnerRelayRefused is what follows conversation to refusing a partner's msg for good (relayRefused): a reply goes to
// the main conversation instead when that is another (and counts as delivered there: it waits in its queue); otherwise
// the partner hears it was not delivered.
func (s *Service) partnerRelayRefused(to string, msg relayMsg) (string, func(context.Context)) {
	cfg := s.settings(context.Background())
	if msg.Tool == partnerReplyTool && cfg.Main != "" && cfg.Main != to && s.moveRelay(cfg.Main, msg) {
		return "a partner hub's reply moved to the main conversation", func(ctx context.Context) {
			s.partnerMovedIn(ctx, msg, cfg.mainRouteMachine(), cfg.Main)
			s.publish(ctx)
			if c, ok := s.session(ctx, cfg.mainRouteMachine(), cfg.Main); ok && c.Status != proto.StatusWorking && !c.Starting {
				s.drainRelays(cfg.mainRouteMachine(), []proto.Session{c})
			}
		}
	}
	return "the partner hub is told", func(ctx context.Context) { s.partnerUndelivered(ctx, msg) }
}

// partnerMainNow is what becomes of msg, a partner's message queued for conversation id, as it is about to go in
// (drainRelays): it goes to the main conversation as it is now. Queued for a main conversation put away since (New
// chat, Clear), it moves to the one now (moved: it leaves id's queue), or, with none yet, waits there (hold: look again
// after it) until the owner's next message starts one. Any other goes as it is.
func (s *Service) partnerMainNow(ctx context.Context, id string, msg relayMsg) (moved bool, hold time.Duration) {
	cfg := s.settings(ctx)
	if id == cfg.Main || !slices.ContainsFunc(cfg.Former, func(f FormerMain) bool { return f.SessionID == id }) {
		return false, 0
	}
	if cfg.Main == "" {
		return false, max(relayRetryGap, time.Second)
	}
	if !s.moveRelay(cfg.Main, msg) {
		return false, max(relayRetryGap, time.Second) // the main conversation's queue could not be saved: tried again
	}
	s.logf("partner %s: %s moved from the former main conversation %s to %s", short(strings.TrimPrefix(msg.From, partnerFrom)),
		msg.Partner, short(id), short(cfg.Main))
	go func() {
		ctx := context.Background()
		s.publish(ctx)
		if c, ok := s.session(ctx, cfg.mainRouteMachine(), cfg.Main); ok && c.Status != proto.StatusWorking && !c.Starting {
			s.drainRelays(cfg.mainRouteMachine(), []proto.Session{c})
		}
	}()
	return true, 0
}

// partnerRelayGone: drainRelays dropped msg, its conversation gone; a partner's envelope goes as when refused.
func (s *Service) partnerRelayGone(to string, msg relayMsg) {
	if msg.Tool != partnerMessageTool && msg.Tool != partnerReplyTool {
		return
	}
	_, then := s.partnerRelayRefused(to, msg)
	go then(context.Background())
}

// partnerUndelivered notes the partner's envelope msg carried as not delivered, and tells the partner.
func (s *Service) partnerUndelivered(ctx context.Context, msg relayMsg) {
	st := s.partnerStore()
	if st == nil || msg.Partner == "" {
		return
	}
	m, err := st.PartnerMessageOf(ctx, partnerIn, strings.TrimPrefix(msg.From, partnerFrom), msg.Partner)
	if err != nil || m == nil {
		return
	}
	check := time.Now().UTC().Add(partnerReconcile) // kept until the status is (partnerStatusOf)
	failed, ok, err := partnerMutate(ctx, st, *m, func(r *db.PartnerMessage) bool {
		if r.State != partnerReceived && r.State != partnerDelivered {
			return false
		}
		r.State, r.Code, r.NextTry = partnerFailed, assistantwire.PartnerCodeUndelivered, &check
		return true
	})
	if err != nil {
		s.logf("partner %s: undelivered: %v", m.MessageID, err)
	}
	if ok {
		s.partnerStatusOf(ctx, st, failed)
	}
}

// partnerMovedIn notes a partner's reply msg, which the conversation that asked refused for good, as delivered into
// conversation to on machine (the main one), where it was queued instead: the partner hears it went in, and the
// message it answers is answered. Once: a reply noted already (it went in from the queue) is left as it is.
func (s *Service) partnerMovedIn(ctx context.Context, msg relayMsg, machine, to string) {
	st := s.partnerStore()
	if st == nil || msg.Partner == "" {
		return
	}
	peer := strings.TrimPrefix(msg.From, partnerFrom)
	m, err := st.PartnerMessageOf(ctx, partnerIn, peer, msg.Partner)
	if err != nil || m == nil {
		if err != nil {
			s.logf("partner %s: reply %s moved: %v", short(peer), msg.Partner, err)
		}
		return
	}
	out, err := st.PartnerMessageOf(ctx, partnerOut, peer, m.ReplyTo)
	if err != nil {
		s.logf("partner %s: reply %s moved: %v", short(peer), msg.Partner, err)
		return
	}
	s.partnerInWent(ctx, st, *m, out, machine, to)
}
