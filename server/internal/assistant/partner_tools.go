package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/transcript"
)

// hub_list and hub_send (prompts/mcp/assistant), and the owner's card for a message to a partner hub. hub_send never
// holds the conversation's turn: it keeps the message and returns, the card goes up from a goroutine of its own, and
// what becomes of the message comes back into the conversation as a notice (partnerTell) or the partner's reply.

// partnerCaller refuses a conversation that may not message partner hubs. Only the assistant's own conversations (the
// main one and the others of its space) may: not the health space's, a guest's, one that writes to other people, a
// meeting's Just me turn, nor anything on a work computer.
func (s *Service) partnerCaller(ctx context.Context, machine, self string) error {
	if self == "" {
		return errors.New("only a Persona conversation can message partner hubs")
	}
	if isOutwardReader(ctx) {
		return errors.New("a conversation that writes to other people does not message partner hubs")
	}
	if s.isWorkMachine(machine) {
		return errors.New("a conversation on a work computer does not message partner hubs")
	}
	if _, guest, err := s.guestOf(ctx, machine, self); err != nil || guest {
		return errors.New("a guest conversation does not message partner hubs")
	}
	if s.justMeNow(machine, self) {
		return errors.New("this is a Just me turn of a meeting: nothing of it goes to partner hubs")
	}
	c, ok := s.session(ctx, machine, self)
	switch {
	case !ok || !c.Assistant:
		return errors.New("only the assistant's own conversations can message partner hubs")
	case spaceOf(c) == SpaceHealth:
		return errors.New("the health space doesn't message partner hubs: health information stays in its conversations")
	case spaceOf(c) != "":
		return errors.New("only the assistant's own conversations can message partner hubs")
	}
	return nil
}

// partnerAsks says, for a paired hub, whether a message to it waits for the owner's OK (no setting, one made for an
// earlier pairing under its hub_id, or one that cannot be read: it does).
func partnerAsks(ctx context.Context, st partnerStore) func(h assistantwire.PartnerHub) bool {
	prefs, err := st.PartnerPrefs(ctx)
	if err != nil {
		prefs = nil
	}
	return func(h assistantwire.PartnerHub) bool { return partnerPrefOf(prefs, h) }
}

// partnerPrefOf is whether a message to h waits for the owner's OK, by prefs: a setting holds only for the pairing it
// was made for.
func partnerPrefOf(prefs []db.PartnerPref, h assistantwire.PartnerHub) bool {
	for _, p := range prefs {
		if p.HubID == h.HubID && samePairing(p.PairedAt, h.PairedAt) {
			return p.Confirm
		}
	}
	return true
}

func (s *Service) toolHubList(ctx context.Context, machine, self string) (string, error) {
	st := s.partnerStore()
	if st == nil {
		return "", errPartnersOff
	}
	if err := s.partnerCaller(ctx, machine, self); err != nil {
		return "", err
	}
	list, err := s.partnerList(ctx)
	if err != nil {
		return "", fmt.Errorf("the partner hubs could not be read now: %w", err)
	}
	if !list.Enabled {
		return "Partner hubs are off on this hub.", nil
	}
	if len(list.Hubs) == 0 {
		return "No partner hub is paired with this hub.", nil
	}
	asks := partnerAsks(ctx, st)
	var b strings.Builder
	b.WriteString("Partner hubs (hub_send messages their assistants):")
	for _, h := range list.Hubs {
		state := "offline (a message waits until it is online)"
		if h.Online {
			state = "online"
		}
		confirm := "a message waits for the user's OK on the phone first"
		if !asks(h) {
			confirm = "a message goes without asking the user"
		}
		// A partner names itself: one that took another's name is told apart by its hub_id only.
		same := ""
		if slices.ContainsFunc(list.Hubs, func(o assistantwire.PartnerHub) bool {
			return o.HubID != h.HubID && strings.EqualFold(strings.TrimSpace(o.Name), strings.TrimSpace(h.Name))
		}) {
			same = "; another partner hub has this name too: name it by hub_id, and tell the user which one (its hub_id) you mean"
		}
		fmt.Fprintf(&b, "\n- %q (hub_id %s): %s; %s%s", h.Name, h.HubID, state, confirm, same)
	}
	return b.String(), nil
}

// partnerHubFor is the paired hub hub names: its hub_id, else its name if no other has it.
func (s *Service) partnerHubFor(ctx context.Context, hub string) (assistantwire.PartnerHub, error) {
	list, err := s.partnerList(ctx)
	if err != nil {
		return assistantwire.PartnerHub{}, fmt.Errorf("the partner hubs could not be read now: %w", err)
	}
	if !list.Enabled {
		return assistantwire.PartnerHub{}, errors.New("partner hubs are off on this hub")
	}
	hub = strings.TrimSpace(hub)
	if hub == "" {
		return assistantwire.PartnerHub{}, errors.New("hub is required (hub_list lists the partner hubs)")
	}
	var named []assistantwire.PartnerHub
	for _, h := range list.Hubs {
		if h.HubID == hub {
			return h, nil
		}
		if strings.EqualFold(strings.TrimSpace(h.Name), hub) {
			named = append(named, h)
		}
	}
	switch len(named) {
	case 1:
		return named[0], nil
	case 0:
		return assistantwire.PartnerHub{}, fmt.Errorf("no partner hub %q (hub_list lists them)", hub)
	}
	ids := make([]string, len(named))
	for i, h := range named {
		ids[i] = h.HubID
	}
	return assistantwire.PartnerHub{}, fmt.Errorf("%q is the name of %d partner hubs: give its hub_id (%s)", hub, len(named), strings.Join(ids, ", "))
}

// partnerTokenRe is a credential of Persona's own (a device's, a pairing code, a bus's or a connector's token, an
// extension's) that SanitizeShareText does not know.
var partnerTokenRe = lazyre.New(`\b(?:(?:jd|jp|jx|jb|jc|jh)_[A-Za-z0-9_-]{16,}|pext_[A-Za-z0-9_-]{8,})`)

// partnerHidden is a character nobody sees where it is shown but a model reads, or one that reorders what is shown
// around it: every Unicode format character (tag characters, which spell out text of their own; bidirectional
// embeddings, overrides and isolates; zero-width space, word joiner, byte order mark, soft hyphen, the invisible
// operators, the interlinear annotation marks...) and every other default-ignorable one (the fillers, the variation
// selectors past the emoji's two, which carry a byte each after any character, the rest of the tag plane). What
// scripts and emoji need stays, one at a time (partnerVisible): the zero-width joiner and non-joiner, the directional
// marks right-to-left text uses, an emoji's or a Mongolian letter's variation selector; and the marks that stand
// before Arabic and Syriac numbers, which show.
func partnerHidden(r rune) bool {
	switch {
	case partnerMark(r):
		return false
	case r >= 0xE0000 && r <= 0xE0FFF, r >= 0xFE00 && r <= 0xFE0D:
		return true
	case unicode.Is(unicode.Cf, r):
		return !unicode.Is(unicode.Prepended_Concatenation_Mark, r)
	}
	return unicode.Is(unicode.Other_Default_Ignorable_Code_Point, r)
}

// partnerMark is an invisible character scripts and emoji need between two that show: zero-width non-joiner and joiner,
// and the left-to-right, right-to-left and Arabic letter marks.
func partnerMark(r rune) bool {
	switch r {
	case 0x200C, 0x200D, 0x200E, 0x200F, 0x061C:
		return true
	}
	return false
}

// partnerVisible is text without its hidden characters (partnerHidden), and how many there were. The invisible ones it
// keeps go one at a time: after each character that shows (or a space, or the start), at most a variation selector
// and then at most a joiner or mark (as 🏳️‍🌈 has them); any more of them in a row is dropped too, so no run of them
// spells out text of its own.
func partnerVisible(text string) (string, int) {
	n := 0
	var b strings.Builder
	b.Grow(len(text))
	selector, mark := false, false // what follows the last character that shows
	for _, r := range text {
		switch {
		case partnerHidden(r):
		case unicode.Is(unicode.Variation_Selector, r):
			if !selector && !mark {
				selector = true
				b.WriteRune(r)
				continue
			}
		case partnerMark(r):
			if !mark {
				mark = true
				b.WriteRune(r)
				continue
			}
		default:
			selector, mark = false, false
			b.WriteRune(r)
			continue
		}
		n++
	}
	return b.String(), n
}

// partnerScrub is text as it goes between this hub and a partner, both ways: without the characters nobody would see
// on the owner's card or in the chat (partnerVisible: what goes is what the card shows), and with credentials removed
// (transcript.SanitizeShareText, and Persona's own token kinds); and how many of each were. A text the sanitizer leaves
// as it was, but for its Unicode normalisation, goes as written: that normalisation alone would change the punctuation
// of Chinese text.
func partnerScrub(text string) (clean string, removed, hidden int, err error) {
	text, hidden = partnerVisible(text)
	text = strings.TrimSpace(text)
	clean, err = transcript.SanitizeShareText(text)
	if err != nil {
		return "", 0, 0, err
	}
	if clean == norm.NFKC.String(strings.ReplaceAll(text, "\r\n", "\n")) {
		clean = text
	} else {
		removed = max(strings.Count(clean, "[REDACTED")-strings.Count(text, "[REDACTED"), 1)
	}
	clean = partnerTokenRe.ReplaceAllStringFunc(clean, func(string) string {
		removed++
		return "[REDACTED]"
	})
	return strings.TrimSpace(clean), removed, hidden, nil
}

func newPartnerMessageID() string { return "pm_" + randomHex(16) }

func (s *Service) toolHubSend(ctx context.Context, machine, self string, args json.RawMessage) (string, error) {
	var a struct {
		Hub     string `json:"hub"`
		Text    string `json:"text"`
		ReplyTo string `json:"reply_to"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("invalid hub_send arguments: %w", err)
	}
	st := s.partnerStore()
	if st == nil {
		return "", errPartnersOff
	}
	if err := s.partnerCaller(ctx, machine, self); err != nil {
		return "", err
	}
	hub, err := s.partnerHubFor(ctx, a.Hub)
	if err != nil {
		return "", err
	}
	text, removed, hidden, err := partnerScrub(a.Text)
	if err != nil {
		return "", fmt.Errorf("the text can't be sent: %w", err)
	}
	if text == "" {
		return "", errors.New("text is empty")
	}
	if n := utf8.RuneCountInString(text); n > assistantwire.PartnerTextMax {
		return "", fmt.Errorf("text is %d characters; a message to a partner hub carries at most %d: shorten it", n, assistantwire.PartnerTextMax)
	}
	kind, hop := assistantwire.PartnerKindMessage, 1
	if replyTo := strings.TrimSpace(a.ReplyTo); replyTo != "" {
		in, err := st.PartnerMessageOf(ctx, partnerIn, hub.HubID, replyTo)
		if err != nil {
			return "", err
		}
		if in == nil || in.Kind == assistantwire.PartnerKindStatus {
			return "", fmt.Errorf("reply_to %q names no message %q sent here", replyTo, hub.Name)
		}
		if s.partnerStale(*in) {
			return "", fmt.Errorf("reply_to %q is a message of an earlier pairing with %q, which ended: it is not answered", replyTo, hub.Name)
		}
		if hop = in.Hop + 1; hop > assistantwire.PartnerHopMax {
			return "", fmt.Errorf("this exchange with %q is %d messages long already (at most %d): tell the user instead of answering", hub.Name, in.Hop, assistantwire.PartnerHopMax)
		}
		kind, a.ReplyTo = assistantwire.PartnerKindReply, replyTo
	} else {
		a.ReplyTo = ""
	}
	asks := partnerAsks(ctx, st)(hub)
	title := s.partnerTitle(ctx, machine, self)
	// The count and the row it lets in are one step: tool calls side by side get no more past the limit.
	unlock := s.partnerGate(partnerOut, hub.HubID)
	now := time.Now().UTC()
	if n, err := st.CountPartnerMessages(ctx, partnerOut, hub.HubID, false, now.Add(-partnerRateWindow)); err != nil {
		unlock()
		return "", err
	} else if n >= partnerOutRate {
		unlock()
		return "", fmt.Errorf("%d messages went to %q in the last %d minutes already (at most %d): wait, or tell the user", n, hub.Name, int(partnerRateWindow.Minutes()), partnerOutRate)
	}
	m := db.PartnerMessage{ID: "pr_" + randomHex(12), Direction: partnerOut, PeerHub: hub.HubID, PeerName: partnerInline(hub.Name, 64),
		MessageID: newPartnerMessageID(), Kind: kind, ReplyTo: a.ReplyTo, Hop: hop, MachineID: machine, SessionID: self,
		Title: title, Text: text, State: partnerConfirming, PairedAt: hub.PairedAt, CreatedAt: now}
	if !asks {
		m.State, m.NextTry = partnerQueued, &now
	}
	m, _, err = st.InsertPartnerMessage(ctx, m)
	unlock()
	if err != nil {
		return "", err
	}
	note := ""
	if removed > 0 {
		note = fmt.Sprintf(" Credentials were removed from the text first (%d found); what goes is the text without them.", removed)
	}
	if hidden > 0 {
		note += fmt.Sprintf(" %d invisible characters were removed from the text: only what the user can see goes.", hidden)
	}
	if asks {
		go s.partnerAsk(m)
		return fmt.Sprintf("Waiting for the user's OK on their phone; it goes to %q once they allow it, and its reply arrives here (message_id %s). Don't send it again.%s",
			hub.Name, m.MessageID, note), nil
	}
	s.partnerWakeUp()
	return fmt.Sprintf("Sent to the queue for %q; it goes as soon as that hub can take it, and its reply arrives here (message_id %s). Don't send it again.%s",
		hub.Name, m.MessageID, note), nil
}

// partnerTitle is what the sending conversation is called (the messages' list shows it).
func (s *Service) partnerTitle(ctx context.Context, machine, self string) string {
	if c, ok := s.session(ctx, machine, self); ok {
		return conversationName(c, s.settings(ctx).Main)
	}
	return ""
}

// partnerAsk raises the owner's card for confirming message m and acts on the answer. It is the row's only card in
// this process; one the row moved past, or another manager's, is not raised. A service that stops ends the wait with
// the row still confirming: the next start asks again (partnerStart).
func (s *Service) partnerAsk(m db.PartnerMessage) {
	st := s.partnerStore()
	if st == nil {
		return
	}
	b := &s.partners
	b.mu.Lock()
	if b.asking[m.ID] {
		b.mu.Unlock()
		return
	}
	if b.asking == nil {
		b.asking = map[string]bool{}
	}
	b.asking[m.ID] = true
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.asking, m.ID)
		b.mu.Unlock()
	}()
	ctx := s.runCtx()
	if s.partnerStale(m) { // its pairing ended before the card went up (a restart asking again)
		s.partnerUnpaired(ctx, st, m)
		return
	}
	now := time.Now().UTC()
	expires := m.CreatedAt.Add(partnerCardWait)
	if !expires.After(now) {
		s.partnerAnswered(ctx, st, m, proto.Resolution{Dismiss: true})
		return
	}
	l := s.lang(ctx)
	// The partner chooses its name: the card names it with the start of its hub_id too, so a partner named like
	// another is told apart.
	names := i18n.Args{"hub": m.PeerName, "id": partnerShortID(m.PeerHub)}
	input, _ := json.Marshal(map[string]string{"hub": m.PeerName, "hub_id": m.PeerHub, "message_id": m.MessageID, "text": m.Text})
	ix := proto.Interaction{ID: "ix_" + randomHex(6), MachineID: m.MachineID, SessionID: m.SessionID, Kind: proto.IxPermission,
		ToolName: "hub_send", Summary: i18n.T(l, "assistant-partners.confirm", names) + "\n\n" + m.Text,
		ToolInput: input, SessionTitle: "Persona", AlwaysLabel: i18n.T(l, "assistant-partners.always", names),
		CreatedAt: now, ExpiresAt: expires}
	if c, ok := s.session(ctx, m.MachineID, m.SessionID); ok {
		ix.Cwd = c.Cwd
	}
	m, ok, err := partnerMutate(ctx, st, m, func(r *db.PartnerMessage) bool {
		if r.State != partnerConfirming {
			return false
		}
		r.CardID = ix.ID
		return true
	})
	if err != nil || !ok {
		if err != nil {
			s.logf("partner %s: card: %v", m.MessageID, err)
		}
		return
	}
	r, err := s.ask(ctx, ix)
	if err != nil {
		return // the service stopped: asked again at its next start
	}
	s.partnerAnswered(context.WithoutCancel(ctx), st, m, r)
}

// partnerAnswered acts on the owner's answer r to message m's card: only their own allow lets it go (and an Always
// with it lets every later message to that hub go without a card); a no is declined; a card that ended unanswered, or
// with a yes that was not the owner's, expired. The sending conversation is told when it does not go.
func (s *Service) partnerAnswered(ctx context.Context, st partnerStore, m db.PartnerMessage, r proto.Resolution) {
	allowed := r.UserAllowed()
	next := partnerExpired
	switch {
	case allowed:
		next = partnerQueued
	case r.Allow != nil && !*r.Allow && !r.Dismiss && !r.Cancel:
		next = partnerDeclined
	}
	now := time.Now().UTC()
	_, ok, err := partnerMutate(ctx, st, m, func(row *db.PartnerMessage) bool {
		if row.State != partnerConfirming {
			return false
		}
		row.State = next
		if allowed {
			row.NextTry = &now
		}
		return true
	})
	if err != nil {
		s.logf("partner %s: answer: %v", m.MessageID, err)
	}
	if !ok {
		return // answered on another card, or by another manager
	}
	if allowed {
		// Always stops asking for this pairing only: a later one under the same hub_id asks again.
		if list, known := s.partnerListNow(); r.Always && (!known || !partnerStaleIn(list, m)) {
			paired := m.PairedAt // the pairing the message was written for, which is the one there is
			if h, _ := partnerListed(list, m.PeerHub); paired.IsZero() {
				paired = h.PairedAt
			}
			if err := st.SetPartnerConfirm(ctx, m.PeerHub, false, paired); err != nil {
				s.logf("partner %s: always: %v", m.PeerHub, err)
			}
		}
		s.partnerWakeUp()
		return
	}
	event := "declined"
	if next == partnerExpired {
		event = "expired"
	}
	s.partnerTell(ctx, m, event, "")
}

// partnerTell tells the conversation that sent m (else the main one, when it is gone) what became of it (event: a
// prompts.PartnerNotice event), once.
func (s *Service) partnerTell(ctx context.Context, m db.PartnerMessage, event, code string) {
	machine, to := s.partnerConversation(ctx, m.MachineID, m.SessionID)
	if to == "" {
		s.logf("partner %s: %s, and no conversation to tell", m.MessageID, event)
		return
	}
	text := prompts.RenderPartnerNotice(prompts.PartnerNotice{Language: answerLanguage(s.hello(ctx).Locale), Event: event,
		Hub: m.PeerName, HubID: m.PeerHub, Code: code, Excerpt: partnerInline(m.Text, 120)})
	msg := relayMsg{OnceID: "partner-notice-" + partnerHash(m.ID, event)[:16], From: partnerFrom + m.PeerHub, FromTitle: m.PeerName,
		Tool: partnerNoticeTool, Text: text, At: time.Now().UTC()}
	if err := s.partnerRelay(ctx, machine, to, msg); err != nil && !errors.Is(err, errPartnerRefused) {
		s.logf("partner %s: telling %s: %v", m.MessageID, short(to), err)
	}
}
