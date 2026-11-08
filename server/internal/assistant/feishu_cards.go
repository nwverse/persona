package assistant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/nwverse/persona/server/internal/compat"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/nwverse/persona/server/internal/channel"
	"github.com/nwverse/persona/server/internal/feishubot"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
	"github.com/nwverse/persona/server/internal/transcript"
)

// Cards for what Persona and the agents ask (the bot's cards switch): a question, a plan, a permission or a terminal
// dialog shows in the chat as a card with the same choices as the app's card; answering it in either place answers it
// (interactions.resolve), and the card in Feishu then says how it was answered (interaction.ended). Cards go out for
// what the chat drives (its target, and the conversations of its runs), or for everything with all_cards. And the
// chat's commands, /start, the pushes and the sync mirror.

const (
	maxFormQuestions     = 6     // more questions than this are answered in the app (keeps the form under ~200 components)
	maxFormOptions       = 20    // options shown per question
	maxQuestionRunes     = 600   // runes of one question's text
	maxOptionDescRunes   = 150   // runes of one option's description
	maxOptionDescBudget  = 800   // runes of all option descriptions of one question
	maxDialogRunes       = 2000  // runes of a screen dialog's detail and question
	maxDialogOptions     = 8     // option buttons of a screen dialog
	maxScreenRunes       = 2500  // runes of the terminal screen kept
	maxScreenLineRunes   = 300   // runes per terminal line
	maxPermissionReason  = 1000  // runes of a permission's reason
	maxAnsweredQuestions = 3     // questions shown on an answered card
	maxAnsweredQRunes    = 150   // runes per question on an answered card
	maxCardJSONBytes     = 24000 // serialized size above which safeCard falls back (Feishu rejects ~30 KB)
)

// feishuInteraction: a new card (interaction.announced).
func (s *Service) feishuInteraction(ix proto.Interaction) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if !s.feishuCardWanted(ctx, ix) {
		return
	}
	s.postInteractionCard(ctx, ix)
}

// feishuCardWanted: the bot posts a card for ix (feishuInteraction): it is on with the cards switch, and the chat
// drives the conversation or "all cards" is on (not for a health conversation's).
func (s *Service) feishuCardWanted(ctx context.Context, ix proto.Interaction) bool {
	f, ready := s.feishuFeatures(ctx)
	if !ready || !f.Cards {
		return false
	}
	// "All cards" does not take a health conversation's: only one the chat drives itself (health.go).
	if drives := s.botDrives(ctx, ix.MachineID, ix.SessionID); !drives && (!f.AllCards || s.ixInHealth(ctx, ix)) {
		return false
	}
	return true
}

// feishuInteractionAlways posts ix's card whatever the cards switches say: a task's decision the report put on top
// (reports.go). Answering it goes as any card's (the hub checks the owner pressed it).
func (s *Service) feishuInteractionAlways(ix proto.Interaction) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s.postInteractionCard(ctx, ix)
}

// botCardRecheck is how long after a reconnection the cards that may still be waiting are looked at again (the
// manager restores its interactions meanwhile).
var botCardRecheck = 90 * time.Second

type cardEnd int

const (
	cardWaiting cardEnd = iota // still up
	cardOver                   // ended for good
	cardUnsure                 // not up, not known to be over (not restored yet, the history could not say)
)

// botCardOver is whether interaction id is over for good, as the hub's queue and its history say (InteractionsGet).
func (s *Service) botCardOver(ctx context.Context, id string) cardEnd {
	_, end := s.botCardLook(ctx, id)
	return end
}

// botCardLook is botCardOver with the interaction as the hub's queue has it, while it is up.
func (s *Service) botCardLook(ctx context.Context, id string) (proto.Interaction, cardEnd) {
	var got struct {
		OK          bool              `json:"ok"`
		Status      string            `json:"status"`
		Interaction proto.Interaction `json:"interaction"`
	}
	switch err := s.appCall(ctx, AppInteractions, InteractionsGet, map[string]any{"interaction_id": id}, &got); {
	case err != nil:
		return proto.Interaction{}, cardUnsure
	case got.OK:
		return got.Interaction, cardWaiting
	case finalStatus(got.Status):
		return proto.Interaction{}, cardOver
	}
	return proto.Interaction{}, cardUnsure
}

// finalStatus: how an interaction ended for good (InteractionsGet's status); "pending" (in the history, not up yet)
// and "" (no record, or the history could not be read) are not.
func finalStatus(status string) bool {
	switch status {
	case "resolved", "dismissed", "expired", "cancelled":
		return true
	}
	return false
}

// postInteractionCard posts ix's card once, unless it ended meanwhile.
func (s *Service) postInteractionCard(ctx context.Context, ix proto.Interaction) {
	if s.interactionEnded(ix.ID) { // ended while this looked at the bot: not asked (s.chans before s.fbot, never in it)
		return
	}
	b := &s.fbot
	b.mu.Lock()
	s.loadBotLocked(ctx)
	if _, ok := b.cards[ix.ID]; ok {
		b.mu.Unlock()
		return
	}
	s.keepBotCardLocked(ix.ID, &botCard{}) // claimed: a second announce of the same card does not post twice
	b.mu.Unlock()
	reply, to, topic := s.interactionPlace(ix)
	fallback := ""
	switch ix.Kind {
	case proto.IxQuestion:
		fallback = "❓ " + clip(firstQuestion(ix), 100)
	case proto.IxPermission:
		fallback = "🔐 " + toolDisplayName(ix.ToolName)
	case proto.IxPlan:
		fallback = "📋 " + clip(firstLine(ix.Plan), 100)
	case proto.IxScreen:
		fallback = "🖥 " + i18n.T(s.botLang(ctx), "assistant-feishu_cards.screen_summary")
	}
	card := s.interactionCard(ctx, ix)
	// A card for a topic is a reply to its run's message and belongs under it: if that message is gone it would be a
	// stray card in the group (the hub would send it into the chat), and the owner may not see it there. It goes to the
	// owner instead, as with a chat that refused it.
	to.NoDetour = to.ChatID != ""
	mid, err := s.botPostTo(ctx, to, reply, card, fallback, "")
	if err != nil && to.ChatID != "" && (chatRefused(err) || feishubot.MessageGone(err)) {
		// The topic was refused outright (the group was taken off the owner's list, the bot is not in it, it was
		// dissolved) or the message to reply under is gone: nothing went out there, and the owner's DM gets the card
		// instead, as a message of its own (a reply to the topic's message would land in the topic). Any other failure
		// (a timeout, a gateway error) may have gone out, and a second card would show the same question twice: it is
		// only logged.
		s.logf("bots: card for %s refused in the topic: %v; sending it to the owner", ix.ID, err)
		reply, to = "", feishuTarget{}
		mid, err = s.botPostTo(ctx, to, "", card, fallback, "")
	}
	ended := s.interactionEnded(ix.ID)
	b.mu.Lock()
	if err != nil {
		s.logf("bots: card for %s: %v", ix.ID, err)
		s.keepBotCardLocked(ix.ID, nil)
		b.mu.Unlock()
		return
	}
	if _, still := b.cards[ix.ID]; !still || ended { // answered while it was being posted
		s.keepBotCardLocked(ix.ID, nil)
		b.mu.Unlock()
		s.botCardDone(ctx, mid, ix, i18n.T(s.botLang(ctx), "assistant-feishu_cards.handled"))
		return
	}
	s.keepBotCardLocked(ix.ID, &botCard{MessageID: mid, Ix: ix, At: b.clock(), Thread: reply, ChatID: to.ChatID})
	b.mu.Unlock()
	if topic != nil {
		// The run was asked in a topic and the card went to the DM: the topic is told where it is.
		s.botNoteTo(ctx, topic.to, topic.reply, i18n.T(s.botLang(ctx), "assistant-feishu_cards.sent_to_dm"))
	}
}

// chatRefused: the hub or Feishu refused the send into a chat outright, so nothing went out: the hub's own refusal
// (a code on the bus) or Feishu's "the bot is not in the chat" / "the chat was dissolved".
func chatRefused(err error) bool {
	var re *rpc.Error
	if errors.As(err, &re) && re.Code == CodeChatDenied {
		return true
	}
	return feishubot.ChatRefused(err)
}

// interactionPlace is where the card of ix goes: the message of the run it answers to reply to, and the topic if that
// run was asked in one. A conversation can have a private chat's run and a topic's at once (they are not folded into
// one); then the owner's DM wins, where only the owner sees the card (an approval or a plan must not be shown to a
// group), and of several runs of one place the one asked last decides.
func (s *Service) interactionPlace(ix proto.Interaction) (reply string, to feishuTarget, note *topicNote) {
	private, topic := s.interactionRuns(ix)
	switch {
	case private != nil && topic != nil:
		// The card is not in the topic that asked: a short line there says it went to the DM.
		return private.ReplyTo, feishuTarget{}, &topicNote{reply: topic.ReplyTo, to: runTo(s.runExts(topic, "chat", "thread"))}
	case private != nil:
		return private.ReplyTo, feishuTarget{}, nil
	case topic != nil:
		return topic.ReplyTo, runTo(s.runExts(topic, "chat", "thread")), nil
	}
	return "", feishuTarget{}, nil
}

// interactionRuns are the runs that decide where ix's card goes: the latest asked in the owner's DM and the latest
// asked in a topic, of the conversation it belongs to (either may be nil).
func (s *Service) interactionRuns(ix proto.Interaction) (private, topic *channel.Run) {
	for _, r := range s.channelRuns(channel.Feishu) {
		if r.MachineID != ix.MachineID || r.SessionID != ix.SessionID {
			continue
		}
		if runTo(s.runExts(&r, "chat", "thread")).ChatID == "" {
			if private == nil || !r.Started.Before(private.Started) {
				private = &r
			}
		} else if topic == nil || !r.Started.Before(topic.Started) {
			topic = &r
		}
	}
	return private, topic
}

// topicNote is where a line goes that tells a topic its run's card went to the DM: the run's message, in its topic.
type topicNote struct {
	reply string
	to    feishuTarget
}

// botDrives: the chat drives this conversation (its target now, a run on it, or a thread bound to it).
func (s *Service) botDrives(ctx context.Context, machine, session string) bool {
	for _, r := range s.channelRuns(channel.Feishu) {
		if r.MachineID == machine && r.SessionID == session {
			return true
		}
		// The hub's own approvals (mail_send) do not name the conversation: one on the assistant's machine while a
		// Persona run goes on is most likely that run's.
		if session == "" && r.TargetKind != targetAgent && r.MachineID == machine {
			return true
		}
	}
	b := &s.fbot
	b.mu.Lock()
	s.loadBotLocked(ctx)
	cur := b.st.Current
	for _, t := range b.st.Threads {
		if t.Target.MachineID == machine && t.Target.SessionID == session && b.clock().Sub(t.At) < 24*time.Hour {
			b.mu.Unlock()
			return true
		}
	}
	b.mu.Unlock()
	if session == "" {
		return false
	}
	if cur.Kind == targetMain {
		return s.isMainOn(ctx, machine, session)
	}
	return cur.MachineID == machine && cur.SessionID == session
}

// feishuInteractionDone: a card is over (answered anywhere, withdrawn, expired): its card in Feishu says so.
func (s *Service) feishuInteractionDone(ix proto.Interaction, how string) {
	s.feishuInteractionEnded(ix, how, false)
}

// feishuInteractionEnded is feishuInteractionDone; inChat: the card was taken back because the user wrote in the
// conversation, so their next words are no late answer to it.
func (s *Service) feishuInteractionEnded(ix proto.Interaction, how string, inChat bool) {
	b := &s.fbot
	b.mu.Lock()
	s.loadBotLocked(context.Background()) // a card posted before a restart is in the chat's state
	c, ok := b.cards[ix.ID]
	s.keepBotCardLocked(ix.ID, nil)
	if ok && typedAnswerable(c) && !inChat {
		b.noteEndedLocked(c)
	}
	b.mu.Unlock()
	if !ok || c.MessageID == "" {
		return
	}
	if ix.Kind == "" { // the catch-up knows only what was kept of it
		ix = c.Ix
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		s.botCardDone(ctx, c.MessageID, ix, how)
	}()
}

// detachedCard is a Feishu card closed because its node's connection ended (cancelMachineInteractions): the same card
// read again once the node is back opens it again (feishuCardBack). closed is shut once the close went out, so the
// reopening lands after it.
type detachedCard struct {
	card   botCard
	at     time.Time
	closed chan struct{}
}

// feishuDetach: the cards ids are about to end because their node went away. Their Feishu cards say "Ended" as any
// ended card's, and are kept for resumeGrace in case the same card comes back (#4583).
func (s *Service) feishuDetach(ids []string) {
	b := &s.fbot
	b.mu.Lock()
	now := b.clock()
	for id, d := range b.detached {
		if now.Sub(d.at) > resumeGrace {
			delete(b.detached, id)
		}
	}
	var closing []detachedCard
	for _, id := range ids {
		c, ok := b.cards[id]
		if !ok || c.MessageID == "" {
			continue
		}
		s.keepBotCardLocked(id, nil) // closed here, not again by feishuInteractionDone
		if b.detached == nil {
			b.detached = map[string]detachedCard{}
		}
		d := detachedCard{card: c, at: now, closed: make(chan struct{})}
		b.detached[id] = d
		closing = append(closing, d)
	}
	b.mu.Unlock()
	for _, d := range closing {
		go func() {
			defer close(d.closed)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			s.botCardDone(ctx, d.card.MessageID, d.card.Ix, i18n.T(s.botLang(ctx), "assistant-channels.ended"))
		}()
	}
}

// feishuCardBack: ix is a card back on the apps after its node reconnected (shown, not announced). Its Feishu card,
// closed when the node went away, asks it again, in the same message and without a new one. A card that did not come
// back (answered, moved on, away past resumeGrace) stays closed.
func (s *Service) feishuCardBack(ix proto.Interaction) {
	b := &s.fbot
	b.mu.Lock()
	d, ok := b.detached[ix.ID]
	delete(b.detached, ix.ID)
	_, posted := b.cards[ix.ID]
	if !ok || posted || b.clock().Sub(d.at) > resumeGrace {
		b.mu.Unlock()
		return
	}
	s.keepBotCardLocked(ix.ID, &botCard{MessageID: d.card.MessageID, Thread: d.card.Thread, ChatID: d.card.ChatID, Ix: ix, At: b.clock(),
		NudgedAt: d.card.NudgedAt, SkippedAt: d.card.SkippedAt, Tries: d.card.Tries, TriedAt: d.card.TriedAt})
	b.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	select {
	case <-d.closed:
	case <-ctx.Done():
		return
	}
	if err := s.botUpdateCard(ctx, d.card.MessageID, s.interactionCard(ctx, ix)); err != nil {
		s.logf("bots: reopen card %s: %v", ix.ID, err)
		b.mu.Lock()
		if c, still := b.cards[ix.ID]; still && c.MessageID == d.card.MessageID {
			s.keepBotCardLocked(ix.ID, nil)
		}
		b.mu.Unlock()
		return
	}
	b.mu.Lock()
	_, still := b.cards[ix.ID]
	b.mu.Unlock()
	if _, pending := s.pendingIx(ix.ID); !still || !pending { // answered or ended meanwhile: it says so again
		b.mu.Lock()
		s.keepBotCardLocked(ix.ID, nil)
		b.mu.Unlock()
		s.botCardDone(ctx, d.card.MessageID, ix, i18n.T(s.botLang(ctx), "assistant-feishu_cards.handled"))
	}
}

func (s *Service) botCardDone(ctx context.Context, messageID string, ix proto.Interaction, how string) {
	if err := s.botUpdateCard(ctx, messageID, s.answeredCard(ctx, ix, how)); err != nil {
		s.logf("bots: close card %s: %v", ix.ID, err)
		s.botNote(ctx, messageID, "✓ "+how)
	}
}

// endedText says in l why a card is no longer pending, from interactions.get's status: it expired, or it was
// withdrawn (cancelled); otherwise (answered, dismissed, unknown) it is the text of fallback, a key.
func endedText(l i18n.Lang, status, fallback string) string {
	switch status {
	case "expired":
		return i18n.T(l, "assistant-feishu_cards.card_expired")
	case "cancelled":
		return i18n.T(l, "assistant-feishu_cards.card_withdrawn")
	}
	return i18n.T(l, fallback)
}

// resolutionText says in l how an interaction was answered, for its closed card.
func resolutionText(l i18n.Lang, ix proto.Interaction, r proto.Resolution) string {
	switch {
	case r.Dismiss && r.Reason == proto.DismissedInChat && r.AnsweredBy == "":
		return i18n.T(l, "assistant-feishu_cards.replied_in_chat")
	case r.Dismiss:
		return i18n.T(l, "assistant-feishu_cards.dismissed")
	case r.Approve != nil && *r.Approve:
		switch r.Mode {
		case "bypassPermissions":
			return i18n.T(l, "assistant-feishu_cards.approved_run")
		case "acceptEdits":
			return i18n.T(l, "assistant-feishu_cards.approved_edits")
		}
		return i18n.T(l, "assistant-feishu_cards.approved")
	case r.Approve != nil:
		if r.Feedback != "" {
			return i18n.T(l, "assistant-feishu_cards.sent_back_note", i18n.Args{"note": clip(r.Feedback, 200)})
		}
		return i18n.T(l, "assistant-feishu_cards.sent_back")
	case r.Allow != nil && *r.Allow:
		if r.Always {
			return i18n.T(l, "assistant-feishu_cards.allowed_always")
		}
		return i18n.T(l, "assistant-feishu_cards.allowed")
	case r.Allow != nil:
		if r.Message != "" {
			return i18n.T(l, "assistant-feishu_cards.denied_note", i18n.Args{"note": clip(r.Message, 200)})
		}
		return i18n.T(l, "assistant-feishu_cards.denied")
	case len(r.Answers) > 0:
		var parts []string
		for _, q := range ix.Questions {
			if a, ok := r.Answers[q.Question]; ok {
				parts = append(parts, a)
			}
		}
		if len(parts) == 0 {
			for _, a := range r.Answers {
				parts = append(parts, a)
			}
		}
		return i18n.T(l, "assistant-feishu_cards.answered_with", i18n.Args{"answers": clip(strings.Join(parts, i18n.T(l, "assistant-feishu_cards.answer_sep")), 300)})
	case r.Option != "" && ix.Dialog != nil:
		if o := ix.Dialog.Option(r.Option); o != nil {
			return i18n.T(l, "assistant-feishu_cards.picked", i18n.Args{"option": o.Label})
		}
	case len(r.Keys) > 0 || r.Text != "":
		return i18n.T(l, "assistant-feishu_cards.responded")
	}
	return i18n.T(l, "assistant-feishu_cards.handled")
}

// interactionCard is the card asking ix, with the app's choices.
func (s *Service) interactionCard(ctx context.Context, ix proto.Interaction) *feishubot.Card {
	l := s.botLang(ctx)
	where := feishubot.EscapeMarkdown(s.ixWhere(ctx, ix))
	val := func(extra map[string]any) map[string]any {
		v := map[string]any{"a": "ix", "id": ix.ID}
		for k, x := range extra {
			v[k] = x
		}
		return v
	}
	open := s.openButton(ctx, ix.MachineID, ix.SessionID)
	switch ix.Kind {
	case proto.IxQuestion:
		c := feishubot.NewCard("❓ "+i18n.T(l, "assistant-feishu_cards.question_title", i18n.Args{"where": s.ixWhere(ctx, ix)}), feishubot.Orange, i18n.T(l, "assistant-feishu_cards.question_summary", i18n.Args{"question": firstQuestion(ix)}))
		if len(ix.Questions) == 1 && !ix.Questions[0].MultiSelect && len(ix.Questions[0].Options) <= 4 {
			q := ix.Questions[0]
			c.Markdown(questionText(l, q))
			var bs []feishubot.Button
			for _, o := range q.Options {
				bs = append(bs, feishubot.Btn(clip(o.Label, 30), feishubot.Default, val(map[string]any{"q": "0", "v": o.Label})))
			}
			c.Buttons(bs...)
			c.Form("other", []any{feishubot.Input("text", "", i18n.T(l, "assistant-feishu_cards.or_write_answer"), false)},
				feishubot.Submit(i18n.T(l, "assistant-feishu_cards.answer"), feishubot.Primary, "q_submit", val(map[string]any{"q": "0", "free": "1"})))
			if len(q.Options) > 0 {
				c.Note(i18n.T(l, "assistant-feishu_cards.answer_by_text"))
			}
		} else if len(ix.Questions) > maxFormQuestions {
			// The submit handler needs an answer to every question, so a form clipped to the first few could never be
			// sent: that many questions are answered in the app.
			c.Note(i18n.T(l, "assistant-feishu_cards.questions_in_app", i18n.Args{"count": len(ix.Questions)}))
		} else {
			var fields []any
			for i, q := range ix.Questions {
				fields = append(fields, feishubot.MarkdownBlock(questionText(l, q)))
				var opts []feishubot.Option
				for j, o := range q.Options {
					if j >= maxFormOptions {
						break
					}
					opts = append(opts, feishubot.Option{Label: clip(o.Label, 60), Value: o.Label})
				}
				if len(opts) > 0 {
					fields = append(fields, feishubot.Select("q"+strconv.Itoa(i), map[bool]string{false: i18n.T(l, "assistant-feishu_cards.pick_one"), true: i18n.T(l, "assistant-feishu_cards.pick_some")}[q.MultiSelect], opts, q.MultiSelect))
				}
				fields = append(fields, feishubot.Input("t"+strconv.Itoa(i), "", i18n.T(l, "assistant-feishu_cards.or_write"), false))
			}
			c.Form("answers", fields, feishubot.Submit(i18n.T(l, "assistant-feishu_cards.submit"), feishubot.Primary, "qs_submit", val(map[string]any{"form": "1"})))
			// A form of one question with options also takes a plain reply; a form of several is submitted as one (a
			// reply is not split over its questions), and a free-text question has no options.
			if len(ix.Questions) == 1 && len(ix.Questions[0].Options) > 0 {
				c.Note(i18n.T(l, "assistant-feishu_cards.answer_by_text"))
			}
		}
		return safeCard(l, c.Buttons(open, feishubot.Btn(i18n.T(l, "assistant-feishu_cards.dismiss"), feishubot.Default, val(map[string]any{"dismiss": "1"}))), open)
	case proto.IxPlan:
		c := feishubot.NewCard("📋 "+i18n.T(l, "assistant-feishu_cards.plan_title", i18n.Args{"where": s.ixWhere(ctx, ix)}), feishubot.Blue, i18n.T(l, "assistant-feishu_cards.plan_summary"))
		plan := ix.Plan
		if r := []rune(plan); len(r) > 5000 {
			plan = string(r[:5000]) + i18n.T(l, "assistant-feishu_cards.plan_clipped")
		}
		c.Markdown(feishubot.CardMarkdown(plan))
		c.Buttons(
			feishubot.Btn(i18n.T(l, "assistant-feishu_cards.approve_run"), feishubot.Primary, val(map[string]any{"approve": "1", "mode": "bypassPermissions"})),
			feishubot.Btn(i18n.T(l, "assistant-feishu_cards.approve_edits"), feishubot.Default, val(map[string]any{"approve": "1", "mode": "acceptEdits"})),
		)
		c.Form("reject", []any{feishubot.Input("feedback", "", i18n.T(l, "assistant-feishu_cards.feedback_placeholder"), true)},
			feishubot.Submit(i18n.T(l, "assistant-feishu_cards.send_back"), feishubot.Danger, "plan_reject", val(map[string]any{"approve": "0"})))
		return safeCard(l, c.Buttons(open), open)
	case proto.IxPermission:
		c := feishubot.NewCard("🔐 "+i18n.T(l, "assistant-feishu_cards.permission_title", i18n.Args{"where": s.ixWhere(ctx, ix)}), feishubot.Orange, i18n.T(l, "assistant-feishu_cards.permission_summary", i18n.Args{"tool": toolDisplayName(ix.ToolName)}))
		var b strings.Builder
		fmt.Fprintf(&b, "**%s**", feishubot.EscapeMarkdown(toolDisplayName(ix.ToolName)))
		if ix.Reason != "" {
			fmt.Fprintf(&b, "\n%s", feishubot.EscapeMarkdown(clipRunes(ix.Reason, maxPermissionReason)))
		}
		c.Markdown(b.String())
		if sum := strings.TrimSpace(ix.Summary); sum != "" {
			c.Markdown("```\n" + strings.ReplaceAll(clip(sum, 2500), "```", "'''") + "\n```")
		} else if in := proto.RedactArgs(ix.ToolInput); in != "" { // a credential's value never goes to the chat (#4734)
			c.Markdown("```json\n" + strings.ReplaceAll(clip(in, 2500), "```", "'''") + "\n```")
		}
		if len(ix.Fields) > 0 {
			// A password or a code is typed in the app, never in a chat.
			c.Note(i18n.T(l, "assistant-feishu_cards.fields_in_app"))
			return safeCard(l, c.Buttons(open), open)
		}
		bs := []feishubot.Button{
			feishubot.Btn(i18n.T(l, "assistant-feishu_cards.allow"), feishubot.Primary, val(map[string]any{"allow": "1"})),
			feishubot.Btn(i18n.T(l, "assistant-feishu_cards.deny"), feishubot.Danger, val(map[string]any{"allow": "0"})),
		}
		// As the app's card (InteractionViews.swift's BUIPermissionForm, web's PermissionCard): Always allow for this session for
		// Claude on a conversation, and for another agent only what it says that always allows — a runtime cannot keep
		// Claude's session rule (node/runtimes.go), so the button there would only allow once and ask again.
		label := ix.AlwaysLabel
		if label == "" && (ix.Agent == "" || ix.Agent == "claude") && ix.SessionID != "" {
			label = i18n.T(l, "assistant-feishu_cards.always_allow")
		}
		if label != "" {
			bs = append(bs, feishubot.Btn(clip(label, 20), feishubot.Default, val(map[string]any{"allow": "1", "always": "1"})))
		}
		return safeCard(l, c.Buttons(bs...).Buttons(open), open)
	case proto.IxScreen:
		c := feishubot.NewCard("🖥 "+i18n.T(l, "assistant-feishu_cards.screen_title", i18n.Args{"where": s.ixWhere(ctx, ix)}), feishubot.Orange, i18n.T(l, "assistant-feishu_cards.screen_summary"))
		if d := ix.Dialog; d != nil {
			q := d.Question
			if d.Detail != "" {
				q = d.Detail + "\n" + q
			}
			if r := []rune(q); len(r) > maxDialogRunes {
				q = string(r[:maxDialogRunes]) + i18n.T(l, "assistant-feishu_cards.clipped")
			}
			c.Markdown(feishubot.EscapeMarkdown(q))
			var bs []feishubot.Button
			for _, o := range d.Options {
				if o.Kind == "type_something" || o.Kind == "chat" {
					continue
				}
				if len(bs) >= maxDialogOptions {
					break
				}
				bs = append(bs, feishubot.Btn(clip(o.Label, 30), feishubot.Default, val(map[string]any{"opt": o.Key})))
			}
			c.Buttons(bs...)
		} else {
			screen := ix.Screen
			lines := strings.Split(screen, "\n")
			if len(lines) > 20 {
				lines = lines[len(lines)-20:]
			}
			for i, line := range lines {
				if r := []rune(line); len(r) > maxScreenLineRunes {
					lines[i] = string(r[:maxScreenLineRunes]) + "…"
				}
			}
			screen = strings.Join(lines, "\n")
			if r := []rune(screen); len(r) > maxScreenRunes {
				screen = "…\n" + string(r[len(r)-maxScreenRunes:])
			}
			c.Markdown("```\n" + strings.ReplaceAll(screen, "```", "'''") + "\n```")
			c.Buttons(
				feishubot.Btn(i18n.T(l, "assistant-feishu_cards.enter"), feishubot.Default, val(map[string]any{"keys": "enter"})),
				feishubot.Btn("Esc", feishubot.Default, val(map[string]any{"keys": "esc"})),
				feishubot.Btn("1", feishubot.Default, val(map[string]any{"keys": "1"})),
				feishubot.Btn("2", feishubot.Default, val(map[string]any{"keys": "2"})),
			)
		}
		if !ix.Secret {
			c.Form("type", []any{feishubot.Input("text", "", i18n.T(l, "assistant-feishu_cards.type_placeholder"), false)},
				feishubot.Submit(i18n.T(l, "assistant-feishu_cards.send"), feishubot.Primary, "screen_text", val(map[string]any{"typed": "1"})))
		} else {
			c.Note(i18n.T(l, "assistant-feishu_cards.secret_in_app"))
		}
		return safeCard(l, c.Buttons(open), open)
	case proto.IxElicitation:
		return safeCard(l, s.elicitationCard(ctx, ix, open), open)
	}
	return safeCard(l, feishubot.NewCard(where, feishubot.Grey, "").Buttons(open), open)
}

// safeCard returns c, or, when it would still be too big for Feishu after the clipping above, a short card that sends
// the owner to the app (with the open button, if any).
func safeCard(l i18n.Lang, c *feishubot.Card, open feishubot.Button) *feishubot.Card {
	if len(c.JSON()) <= maxCardJSONBytes {
		return c
	}
	title := ""
	if c.Header != nil {
		title = c.Header.Title.Content
	}
	fallback := feishubot.NewCard(title, feishubot.Orange, "")
	fallback.Note(i18n.T(l, "assistant-feishu_cards.too_long_in_app"))
	if len(open) > 0 {
		fallback.Buttons(open)
	}
	return fallback
}

// elicitationTarget is the link an elicitation may open and its host: an absolute http(s) URL with a host and no
// user or password (like the apps' urlTarget, web/src/lib/elicitation.ts), exactly as given; "" otherwise. A link
// such as https://github.com@evil.example/ reads as one site and opens another.
func elicitationTarget(raw string) (string, string) {
	target := strings.TrimSpace(raw)
	if !elicitationURLRe.MatchString(target) {
		return "", ""
	}
	u, err := url.Parse(target)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return "", ""
	}
	return target, u.Hostname()
}

// elicitationURLRe: http(s):// and then a host at once (not https:///host or https:host, which parsers forgive).
var elicitationURLRe = regexp.MustCompile(`(?i)^https?://[^/\\?#@]`)

// elicitationCard is an MCP server asking to authorize (a link) or to fill in a form: what it asks on the card, its
// link as a button; a form is filled in the app (its values may be secrets), so only its fields' names are shown.
func (s *Service) elicitationCard(ctx context.Context, ix proto.Interaction, open feishubot.Button) *feishubot.Card {
	l := s.botLang(ctx)
	var msg, link, host string
	var fields []string
	if e := ix.Elicitation; e != nil {
		msg = strings.TrimSpace(e.Message)
		link, host = elicitationTarget(e.URL)
		var schema struct {
			Properties map[string]struct {
				Title string `json:"title"`
			} `json:"properties"`
		}
		if json.Unmarshal(e.RequestedSchema, &schema) == nil {
			for k, p := range schema.Properties {
				if t := strings.TrimSpace(p.Title); t != "" {
					k = t
				}
				fields = append(fields, k)
			}
			slices.Sort(fields)
		}
	}
	c := feishubot.NewCard("🔑 "+i18n.T(l, "assistant-feishu_cards.elicitation_title", i18n.Args{"where": s.ixWhere(ctx, ix)}), feishubot.Orange,
		i18n.T(l, "assistant-feishu_cards.elicitation_summary", i18n.Args{"message": clip(oneLine(msg), 60)}))
	if msg != "" {
		c.Markdown(feishubot.EscapeMarkdown(clip(msg, 2000)))
	}
	if link != "" {
		// The host on its own line: the link's text could look like another site's (as the apps show it).
		c.Markdown(i18n.T(l, "assistant-feishu_cards.elicitation_host", i18n.Args{"host": feishubot.Code(host)}))
		c.Note(i18n.T(l, "assistant-feishu_cards.elicitation_confirm_in_app"))
		return c.Buttons(feishubot.LinkBtn(i18n.T(l, "assistant-feishu_cards.elicitation_open"), link), open)
	}
	if len(fields) > 0 {
		c.Note(i18n.T(l, "assistant-feishu_cards.elicitation_fields", i18n.Args{"fields": clip(strings.Join(fields, "、"), 200)}))
	}
	c.Note(i18n.T(l, "assistant-feishu_cards.elicitation_in_app"))
	return c.Buttons(open)
}

func firstQuestion(ix proto.Interaction) string {
	if len(ix.Questions) > 0 {
		return ix.Questions[0].Question
	}
	return ""
}

func questionText(l i18n.Lang, q proto.Question) string {
	s := ""
	if q.Header != "" {
		s = "**" + feishubot.EscapeMarkdown(clipRunes(q.Header, 60)) + "**\n"
	}
	s += feishubot.EscapeMarkdown(clipRunes(q.Question, maxQuestionRunes))
	descBudget := maxOptionDescBudget
	clippedOptions := 0
	for i, o := range q.Options {
		if i >= maxFormOptions {
			clippedOptions = len(q.Options) - i
			break
		}
		if o.Description != "" && descBudget > 0 {
			desc := clipRunes(o.Description, maxOptionDescRunes)
			if len([]rune(desc)) > descBudget {
				desc = clipRunes(desc, descBudget)
			}
			descBudget -= len([]rune(desc))
			s += "\n- " + feishubot.EscapeMarkdown(clip(o.Label, 40)) + "：" + feishubot.EscapeMarkdown(desc)
		}
	}
	if clippedOptions > 0 {
		s += i18n.T(l, "assistant-feishu_cards.clipped")
	}
	return s
}

// answeredCard is an interaction's card once it is over.
func (s *Service) answeredCard(ctx context.Context, ix proto.Interaction, how string) *feishubot.Card {
	title := map[string]string{proto.IxQuestion: "❓ ", proto.IxPlan: "📋 ", proto.IxPermission: "🔐 ", proto.IxScreen: "🖥 "}[ix.Kind] + s.ixWhere(ctx, ix)
	c := feishubot.NewCard(title, feishubot.Grey, how)
	switch ix.Kind {
	case proto.IxQuestion:
		qs := ix.Questions
		more := 0
		if len(qs) > maxAnsweredQuestions {
			more = len(qs) - maxAnsweredQuestions
			qs = qs[:maxAnsweredQuestions]
		}
		for _, q := range qs {
			c.Markdown(feishubot.EscapeMarkdown(clipRunes(q.Question, maxAnsweredQRunes)))
		}
		if more > 0 {
			c.Note(strings.TrimSpace(i18n.T(s.botLang(ctx), "assistant-feishu_cards.clipped")))
		}
	case proto.IxPermission:
		c.Markdown(feishubot.EscapeMarkdown(ix.ToolName + " " + clip(oneLine(ix.Summary), 200)))
	case proto.IxPlan:
		c.Markdown(feishubot.EscapeMarkdown(clip(planTitle(ix.Plan), 200)))
	}
	// open rides on the card and is handed to safeCard too, so the short fallback card keeps it when this one
	// would exceed Feishu's size budget.
	open := s.openButton(ctx, ix.MachineID, ix.SessionID)
	return safeCard(s.botLang(ctx), c.Note("✓ "+how).Buttons(open), open)
}

func (s *Service) ixWhere(ctx context.Context, ix proto.Interaction) string {
	where := transcript.Title(ix.SessionTitle)
	if where == "" {
		where = "Persona"
	}
	if d, ok := s.device(ctx, ix.MachineID); ok && where != "Persona" {
		where += " · " + d.Name
	}
	return where
}

// openButton opens the conversation in the web app the hub serves; nil (Card.Buttons leaves it out) when the hub has
// no http(s) address for it. Card links are documented for http(s) and AppLink
// only: a persona:// link (the iPhone app) might get the whole card refused, so it is not used.
func (s *Service) openButton(ctx context.Context, machine, session string) feishubot.Button {
	st, _ := s.feishuStatusKept(ctx)
	web := strings.TrimRight(st.PublicURL, "/")
	if session != "" {
		web = sessionLink(web, machine, session, s.isAssistantConversation(ctx, machine, session))
	}
	return s.openLinkButton(ctx, web)
}

// openLinkButton is the "open on web" button for a link already built (sessionLink); nil for an empty or non-http(s)
// one, so a card never carries a dead empty link (#4073).
func (s *Service) openLinkButton(ctx context.Context, web string) feishubot.Button {
	return safeLinkButton(i18n.T(s.botLang(ctx), "assistant-feishu_cards.open_web"), web)
}

// safeLinkButton is a labeled open_url button for web, nil when web is empty or not http(s) with a host: card links
// are documented for http(s) only, so a dirty address (no scheme, empty host) never becomes a dead button.
func safeLinkButton(label, web string) feishubot.Button {
	if u, err := url.Parse(web); web == "" || err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil
	}
	return feishubot.LinkBtn(label, web)
}

// webBase is the hub's web app (hub.json's public_url) without the trailing slash, "" when the hub does not say.
// It only reads feishu.status (which carries the address whether the bot is on or not): unlike feishuStatus it
// does not reconcile the bot's chat state, so the snapshot does not touch it.
func (s *Service) webBase(ctx context.Context) string {
	var st FeishuStatus
	if err := s.appCall(ctx, AppFeishu, FeishuStatusGet, map[string]any{}, &st); err != nil {
		return ""
	}
	return strings.TrimRight(st.PublicURL, "/")
}

// sessionLink is a conversation's page in the web app at web (web/src/lib/router.ts): a Persona conversation is
// /persona/<id>, an agent /s/<machine>/<id>; "" when web is.
func sessionLink(web, machine, session string, assistant bool) string {
	if web == "" {
		return ""
	}
	if assistant {
		return web + "/persona/" + url.PathEscape(session)
	}
	return web + "/s/" + url.PathEscape(machine) + "/" + url.PathEscape(session)
}

// todoLink is a todo's page in the web app at web, "" when web is.
func todoLink(web, id string) string {
	if web == "" {
		return ""
	}
	return web + "/todos?id=" + url.QueryEscape(id)
}

// --- card buttons ---

// FeishuAction is feishu.card_action: the owner pressed one of the bot's cards (the hub checked it is the owner);
// the answer is the toast and the card to show instead. Feishu waits 3 seconds for it.
func (s *Service) FeishuAction(ctx context.Context, p FeishuAction) (*feishubot.CardResponse, error) {
	var a feishubot.CardAction
	if err := json.Unmarshal(p.Event, &a); err != nil {
		return nil, badRequest(err)
	}
	return s.botCardAction(ctx, &a, p.Action), nil
}

// botCardAction answers a press; action is the owner's action for the card it presses (what answers it).
func (s *Service) botCardAction(ctx context.Context, a *feishubot.CardAction, action string) *feishubot.CardResponse {
	l := s.botLang(ctx)
	switch a.Str("a") {
	case "ix":
		return s.answerFromCard(ctx, a, action)
	case "accept":
		// The hub checked the owner pressed it; marking a task verified approves nothing.
		ok, err := false, error(nil)
		ask, _ := s.askCopy(a.Str("id"))
		if at := a.Str("asked_at"); at != "" { // a button of a briefing card: it accepts the round it was asked in (briefing.go)
			ok, err = s.acceptTryFromBrief(ctx, a.Str("id"), at)
		} else {
			ok, err = s.acceptTry(ctx, a.Str("id"), a.Context.MessageID)
		}
		if err != nil { // a recommendation that did not reach the agent: not taken, the button works again
			return toast("error", i18n.T(l, "assistant-reports.accept_not_sent", i18n.Args{"error": clip(oneLine(err.Error()), 120)}))
		}
		if ok {
			return toast("success", acceptedNote(l, &ask)) // "verified", "got it", or the recommendation taken (conclusion.go)
		}
		return toast("info", i18n.T(l, "assistant-feishu_cards.already_handled"))
	case "later_done": // the Handled button of a question that can wait, on a briefing card (later.go)
		if s.laterHandled(ctx, a.Str("id"), time.Now()) {
			return toast("success", i18n.T(l, "assistant-briefing.later_handled"))
		}
		return toast("info", i18n.T(l, "assistant-feishu_cards.already_handled"))
	case "switch":
		f, _ := s.feishuFeatures(ctx)
		kind, m, sess, title := a.Str("k"), a.Str("m"), a.Str("s"), a.Str("t")
		t := botTarget{Kind: kind, MachineID: m, SessionID: sess}
		if kind == targetMain {
			t = botTarget{Kind: targetMain}
		}
		if err := botAllowed(l, f, t); err != nil {
			return toast("error", err.Error())
		}
		s.setBotCurrent(t)
		// The callback only carries the pressed card's own message id (no thread root, no chat beyond its id): bind
		// the card itself. A quote reply to it has it as the parent, and a topic started on it has it as the root,
		// so botTargetFor routes both to this target.
		s.rememberBotThread(a.Context.MessageID, t, title)
		return toast("success", i18n.T(l, "assistant-feishu_cards.now_to_reply", i18n.Args{"title": clip(title, 30)}))
	case "start_form":
		if f, _ := s.feishuFeatures(ctx); !f.Agents {
			return toast("error", i18n.T(l, "assistant-feishu_cards.agents_off_short"))
		}
		// Feishu gives the callback 3 s and the post goes over the bus to the feishu app, so the /start form is
		// posted from a goroutine (as botStartAgent posts its card), quoting the pressed list card.
		mid := a.Context.MessageID
		go func() {
			cctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if _, err := s.botPost(cctx, mid, s.startAgentCard(cctx, ""), ""); err != nil {
				s.logf("bots: start form: %v", err)
			}
		}()
		return toast("info", i18n.T(l, "assistant-feishu_cards.start_form_opening"))
	case "start":
		if f, _ := s.feishuFeatures(ctx); !f.Agents {
			return toast("error", i18n.T(l, "assistant-feishu_cards.agents_off_short"))
		}
		machine, cwd, prompt := a.Form("machine"), strings.TrimSpace(a.Form("cwd")), strings.TrimSpace(a.Form("prompt"))
		if machine == "" || cwd == "" {
			return toast("warning", i18n.T(l, "assistant-feishu_cards.pick_machine_dir"))
		}
		// A double tap, Feishu's 3 s redelivery, and the hub's own resend all arrive as presses of the same card
		// message: within the window only the first starts an agent, the rest get the same toast.
		mid := a.Context.MessageID
		var at time.Time
		if mid != "" {
			var pressed bool
			if pressed, at = s.startCardPressed(mid); pressed {
				return toast("info", i18n.T(l, "assistant-feishu_cards.starting"))
			}
		}
		go func() {
			if err := s.botStartAgent(machine, cwd, prompt); err != nil && mid != "" {
				// Nothing started, so a retry press of the same card must go through. at makes this a compare-and-swap:
				// a failure arriving after the window only frees the slot if a later press has not re-stamped it.
				s.forgetStartCard(mid, at)
			}
		}()
		return toast("info", i18n.T(l, "assistant-feishu_cards.starting"))
	case "stop":
		// The progress card's Stop button: the run's own conversation, carried in the button when the card was built.
		f, _ := s.feishuFeatures(ctx)
		t := botTarget{Kind: a.Str("k"), MachineID: a.Str("m"), SessionID: a.Str("s")}
		out := s.stopConversation(ctx, f, t)
		return toast(out.kind, out.message)
	}
	return toast("info", i18n.T(l, "assistant-feishu_cards.card_expired"))
}

func toast(kind, text string) *feishubot.CardResponse {
	return &feishubot.CardResponse{Toast: &feishubot.Toast{Type: kind, Content: text}}
}

// cardAnswerWait is how long a press waits for its answer to go through before it says "Processing…": the hub has
// Feishu's 3 seconds, and the press came over the bus.
var cardAnswerWait = 1500 * time.Millisecond

// answerFromCard turns a press on an interaction card into the same answer the app would give, and resolves it.
func (s *Service) answerFromCard(ctx context.Context, a *feishubot.CardAction, action string) *feishubot.CardResponse {
	l := s.botLang(ctx)
	id := a.Str("id")
	var got struct {
		Interaction proto.Interaction `json:"interaction"`
		OK          bool              `json:"ok"`
		Status      string            `json:"status"`
	}
	if err := s.appCall(ctx, AppInteractions, InteractionsGet, map[string]any{"interaction_id": id}, &got); err != nil {
		return toast("error", clip(oneLine(err.Error()), 80))
	}
	if !got.OK && !finalStatus(got.Status) {
		// Not up now, but not known to be over either (in the history, not restored yet; or the history could not
		// say): the card stays as it is.
		return toast("info", endedText(l, got.Status, "assistant-feishu_cards.already_handled"))
	}
	if !got.OK {
		// Over for good (answered elsewhere, maybe while the assistant was away): this card is closed as such, its
		// buttons gone.
		b := &s.fbot
		b.mu.Lock()
		s.loadBotLocked(ctx)
		card, had := b.cards[id]
		s.keepBotCardLocked(id, nil)
		b.mu.Unlock()
		ix := proto.Interaction{ID: id}
		if had {
			ix = card.Ix
		}
		ended := endedText(l, got.Status, "assistant-channels.ended")
		return &feishubot.CardResponse{Toast: &feishubot.Toast{Type: "info", Content: endedText(l, got.Status, "assistant-feishu_cards.already_handled")},
			Card: s.answeredCard(ctx, ix, ended).Raw()}
	}
	ix := got.Interaction
	yes, no := true, false
	var r proto.Resolution
	switch {
	case a.Str("dismiss") == "1":
		r.Dismiss = true
	case ix.Kind == proto.IxQuestion && a.Str("form") == "1":
		r.Answers = map[string]string{}
		for i, q := range ix.Questions {
			ans := strings.TrimSpace(a.Form("t" + strconv.Itoa(i)))
			if ans == "" {
				ans = a.Form("q" + strconv.Itoa(i))
			}
			if ans != "" {
				r.Answers[q.Question] = ans
			}
		}
		if len(r.Answers) < len(ix.Questions) {
			return toast("warning", i18n.T(l, "assistant-feishu_cards.answer_each"))
		}
	case ix.Kind == proto.IxQuestion:
		i, _ := strconv.Atoi(a.Str("q"))
		if i < 0 || i >= len(ix.Questions) {
			return toast("error", i18n.T(l, "assistant-feishu_cards.question_mismatch"))
		}
		ans := a.Str("v")
		if a.Str("free") == "1" {
			ans = strings.TrimSpace(a.Form("text"))
		}
		if ans == "" {
			return toast("warning", i18n.T(l, "assistant-feishu_cards.write_answer"))
		}
		r.Answers = map[string]string{ix.Questions[i].Question: ans}
	case ix.Kind == proto.IxPlan && a.Str("approve") == "1":
		r.Approve, r.Mode = &yes, a.Str("mode")
	case ix.Kind == proto.IxPlan:
		r.Approve, r.Feedback = &no, strings.TrimSpace(a.Form("feedback"))
	case ix.Kind == proto.IxPermission:
		if len(ix.Fields) > 0 {
			return toast("warning", i18n.T(l, "assistant-feishu_cards.fill_in_app"))
		}
		if a.Str("allow") == "1" {
			r.Allow, r.Always = &yes, a.Str("always") == "1"
		} else {
			r.Allow = &no
		}
	case ix.Kind == proto.IxScreen && a.Str("opt") != "":
		r.Option = a.Str("opt")
	case ix.Kind == proto.IxScreen && a.Str("typed") == "1":
		text := a.Form("text")
		if text == "" {
			return toast("warning", i18n.T(l, "assistant-feishu_cards.write_text_first"))
		}
		r.Keys, r.Text = []string{"enter"}, text
	case ix.Kind == proto.IxScreen && a.Str("keys") != "":
		r.Keys = []string{a.Str("keys")}
	default:
		return toast("error", i18n.T(l, "assistant-feishu_cards.unknown_action"))
	}
	// This card is closed by the answer below, not again when the interaction ends.
	b := &s.fbot
	b.mu.Lock()
	card, had := b.cards[id]
	s.keepBotCardLocked(id, nil)
	b.mu.Unlock()
	restore := func(ctx context.Context) {
		if had {
			s.botRestoreCard(ctx, id, card)
		}
	}
	how := resolutionText(l, ix, r)
	type outcome struct {
		ok  bool
		err error
	}
	// Screen and Codex cards act on the terminal and can take a few seconds; Feishu waits 3 s for this answer.
	done := make(chan outcome, 1)
	go func() {
		rctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		ok, err := s.resolveInteraction(rctx, channel.Feishu, id, r, action)
		done <- outcome{ok, err}
	}()
	select {
	case o := <-done:
		switch {
		case o.err != nil:
			restore(ctx)
			return toast("error", clip(oneLine(o.err.Error()), 80))
		case !o.ok:
			return toast("info", i18n.T(l, "assistant-feishu_cards.already_handled"))
		}
		return &feishubot.CardResponse{Toast: &feishubot.Toast{Type: "success", Content: how}, Card: s.answeredCard(ctx, ix, how).Raw()}
	case <-time.After(cardAnswerWait):
		msg := a.Context.MessageID
		go func() {
			o := <-done
			if o.err != nil || !o.ok {
				if o.err != nil {
					s.logf("bots: answer %s: %v", id, o.err)
				}
				rctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				restore(rctx)
				return
			}
			if msg != "" {
				cctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				s.botCardDone(cctx, msg, ix, how)
			}
		}()
		return toast("info", i18n.T(l, "assistant-feishu_cards.working_on_it"))
	}
}

// botRestoreCard puts back the card of interaction id, closed optimistically for an answer that did not go through,
// unless the hub says it is no longer pending: it ended while the answer was out, and its interaction.ended found no
// card to close, so putting it back would leave a card waiting that nothing answers (#5012). When that cannot be
// asked either, the card is put back, as before.
func (s *Service) botRestoreCard(ctx context.Context, id string, card botCard) {
	var got struct {
		OK bool `json:"ok"`
	}
	if err := s.appCall(ctx, AppInteractions, InteractionsGet, map[string]any{"interaction_id": id}, &got); err == nil && !got.OK {
		return
	}
	b := &s.fbot
	b.mu.Lock()
	s.keepBotCardLocked(id, &card)
	b.mu.Unlock()
}

// botStartCardWindow is how long a repeat press of the same /start card is ignored: a double tap, Feishu's 3 s
// redelivery, and the hub's ~2 s resend all fall inside it. It is deliberately shorter than botStartAgent's
// 2-minute timeout: if the first start is still running when the window ends, the owner pressing again is a
// deliberate manual retry and may start a second agent. The transport retries above arrive at ~2-3 s, while the
// slot is held for the whole outstanding call, so they can never reach that second start.
var botStartCardWindow = 60 * time.Second

// startCardPressed reports whether the /start card message mid was already pressed within the window, and records
// this press either way. A true answer means the caller must not start a second agent. The returned time is the
// timestamp this press stored (or the existing one for a duplicate), for forgetStartCard's compare-and-swap.
func (s *Service) startCardPressed(mid string) (bool, time.Time) {
	b := &s.fbot
	now := b.clock()
	b.mu.Lock()
	defer b.mu.Unlock()
	key := mid + "start"
	if at, ok := b.startAt[key]; ok && now.Sub(at) < botStartCardWindow {
		return true, at
	}
	if b.startAt == nil {
		b.startAt = map[string]time.Time{}
	}
	for k, at := range b.startAt { // forget presses past the window so the map cannot grow without bound
		if now.Sub(at) >= botStartCardWindow {
			delete(b.startAt, k)
		}
	}
	b.startAt[key] = now
	return false, now
}

// forgetStartCard releases mid's dedup slot after its start failed, so pressing the same card again retries it
// instead of being answered with a bare "Starting". It only deletes the slot this press stamped (at): if the window
// passed and a later manual press re-stamped it while the first start was still hung, that newer slot must survive
// the first start's late failure.
func (s *Service) forgetStartCard(mid string, at time.Time) {
	b := &s.fbot
	b.mu.Lock()
	defer b.mu.Unlock()
	key := mid + "start"
	if cur, ok := b.startAt[key]; ok && cur.Equal(at) {
		delete(b.startAt, key)
	}
}

// botStartAgent starts a coding agent from the /start card and points the chat at it. A non-nil error was already
// told to the owner as a note; it means no agent was started.
func (s *Service) botStartAgent(machine, cwd, prompt string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	l := s.botLang(ctx)
	d, ok := s.device(ctx, machine)
	if !ok {
		s.botSay(ctx, i18n.T(l, "assistant-feishu_cards.no_such_machine"))
		return ErrNoDevice
	}
	cwd = proto.ExpandHome(cwd, d.Home)
	params := map[string]any{"cwd": cwd, "prompt": prompt, "tab_name": s.tabName(ctx, prompt)} // agents_start.go
	if err := s.applyModel(ctx, "agent.new", params); err != nil {
		s.botSay(ctx, i18n.T(l, "assistant-feishu_cards.cannot_start", i18n.Args{"error": err.Error()}))
		return err
	}
	var res struct {
		Session proto.Session `json:"session"`
	}
	if err := s.call(ctx, d.ID, DeviceSessionStart, params, &res); err != nil {
		s.botSay(ctx, i18n.T(l, "assistant-feishu_cards.cannot_start", i18n.Args{"error": clip(oneLine(err.Error()), 200)}))
		return err
	}
	t := botTarget{Kind: targetAgent, MachineID: d.ID, SessionID: res.Session.SessionID}
	label := agentLabel(res.Session)
	if label == "" || label == "." {
		label = cwd
	}
	label += " · " + d.Name
	s.setBotCurrent(t)
	c := feishubot.NewCard("", "", i18n.T(l, "assistant-feishu_cards.started")).Markdown(i18n.T(l, "assistant-feishu_cards.started_body", i18n.Args{"machine": feishubot.EscapeMarkdown(d.Name), "dir": feishubot.Code(cwd)}))
	if prompt != "" {
		c.Note(i18n.T(l, "assistant-feishu_cards.started_working"))
	}
	mid, err := s.botPost(ctx, "", c.Buttons(s.openButton(ctx, d.ID, res.Session.SessionID)), "")
	if err != nil {
		s.logf("bots: started note: %v", err)
	}
	s.rememberBotThread(mid, t, label)
	if prompt != "" && mid != "" {
		s.startChannelRun(&channel.Run{ID: "fb_" + mid, Channel: channel.Feishu, ReplyTo: mid, MachineID: t.MachineID, SessionID: t.SessionID,
			TargetKind: t.Kind, Label: label, Started: time.Now().UTC()})
	}
	return nil
}

// --- pushes and the sync mirror ---

// feishuPush sends what notify_user pushed to the phone to Feishu too (the pushes switch).
func (s *Service) feishuPush(machine, session, title, body string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second) // as feishuMirror: a push may be several cards
	defer cancel()
	if f, ready := s.feishuFeatures(ctx); !ready || !f.Pushes {
		return
	}
	// The body laid out like the bot's answers (answerCards: sections, tables, link buttons, more cards when long, at
	// most botAnswerCards); the push's title heads the first card, the open button goes under the last.
	l := s.botLang(ctx)
	cards := answerCards(l, body, false, nil, "")
	head := feishubot.NewCard("", "", title)
	var missing []int // the pieces that did not get out (1-based): said at the end
	for i, ac := range cards {
		if i == 0 {
			// The push's title heads the card; the body's own heading and status ("Please verify" / "No changes") are kept (Retitle).
			ac.card.Retitle("🔔 ", title, feishubot.Wathet)
			ac.card.Config = head.Config
		}
		if i == len(cards)-1 && session != "" {
			ac.card.Buttons(s.openButton(ctx, machine, session))
		}
		mid, err := s.botPost(ctx, "", ac.card, ac.fallback)
		if err != nil {
			s.logf("bots: push: %v", err)
			if i == 0 {
				return // nothing went out: do not mark pushed, so the turn-end mirror delivers the turn
			}
			missing = append(missing, i+1)
			continue
		}
		if i == 0 {
			s.markPushed(session) // only after the first card lands: a failed push must not suppress the mirror
		}
		if session != "" {
			s.rememberBotThread(mid, botTarget{Kind: targetPersona, MachineID: machine, SessionID: session}, title)
		}
	}
	if len(missing) > 0 {
		s.botSay(ctx, missingPiecesNote(l, missing))
	}
}

// feishuMirror sends a turn that ended without being asked for in Feishu: the main conversation's when sync is on
// (what was said in the app, a scheduled task's run), a scheduled task's own conversation's when pushes is on.
func (s *Service) feishuMirror(ctx context.Context, machine, session string) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	f, ready := s.feishuFeatures(ctx)
	if !ready || !(f.Sync || f.Pushes) {
		return
	}
	main := s.isMainOn(ctx, machine, session)
	task := s.taskOfConversation(ctx, machine, session)
	switch {
	case main && f.Sync:
	case task != "" && f.Pushes:
	default:
		return
	}
	if s.pushedLately(session) && f.Pushes {
		return // its notify_user already went to Feishu
	}
	sess, ok := s.session(ctx, machine, session)
	if !ok || sess.WokeByTask && !main || spaceOf(sess) == SpaceHealth { // a health task's runs stay off Feishu (health.go)
		return
	}
	asked, answer, turn, ok := s.channelLastTurnOf(ctx, machine, session)
	if !ok {
		return
	}
	if strings.Contains(asked, "title=\"飞书机器人\"") { // the context tag of the bot's messages (prompts/channels), not text for people
		return // its own message: answered by the run
	}
	l := s.botLang(ctx)
	label := i18n.T(l, "assistant-feishu_cards.main_chat")
	if !main {
		label = i18n.T(l, "assistant-feishu_cards.scheduled_task", i18n.Args{"task": task})
		if run := s.briefClaim(session, asked, sess.TurnError != "" || cutOff(answer)); run != nil { // a routine with facts: one card Persona lays out (briefing.go)
			s.briefMirror(ctx, machine, session, label, run, answer, turn)
			return
		}
	}
	// The whole answer, laid out like the bot's own answers (answerCards: header, tables, link buttons, at most
	// botAnswerCards cards, a note when cut short); where it came from and the button to open it under the last.
	foot := label
	if asked != "" && main {
		foot = i18n.T(l, "assistant-feishu_cards.asked_elsewhere", i18n.Args{"asked": clip(oneLine(stripContext(asked)), 80)}) + "\n" + label
	}
	cards := answerCards(l, answer, false, nil, foot)
	if len(cards) > 0 {
		cards[len(cards)-1].card.Buttons(s.openButton(ctx, machine, session))
	}
	// The turn is in the key: two runs an hour apart that asked and answered alike (a task's "no change") are two
	// sends, not one Feishu drops as a repeat.
	sum := sha256.Sum256([]byte(machine + "\x00" + session + "\x00" + turn + "\x00" + asked + "\x00" + answer))
	m := &mirrorSend{l: l, cards: cards, target: botTarget{Kind: targetPersona, MachineID: machine, SessionID: session}, label: label,
		main: main, answer: answer, turn: turn, key: "m" + hex.EncodeToString(sum[:8])}
	s.feishuMirrorPost(ctx, m, 0)
}

// mirrorSend is one mirrored answer, as it is sent again when its first card did not get out.
type mirrorSend struct {
	l      i18n.Lang
	cards  []botAnswerCard
	target botTarget
	label  string
	main   bool   // the main conversation's (the sync switch), else a scheduled task's own (pushes)
	answer string // the turn's answer: a retry goes only while it is still the conversation's last
	turn   string // the turn's last item: with answer, what tells it from a later turn that answered alike
	key    string // the answer's key: card i is sent with key + i, the same on every try
	sent   func() // when set, called once the first card is out
}

// stillDue: a retry of m may go now: the switch its conversation needs is on, its notify_user did not go to Feishu
// meanwhile, and its turn is still the conversation's last (an old morning brief must not land under a newer answer).
func (s *Service) mirrorStillDue(ctx context.Context, m *mirrorSend) bool {
	f, ready := s.feishuFeatures(ctx)
	switch {
	case !ready, m.main && !f.Sync, !m.main && !f.Pushes:
		return false
	case f.Pushes && s.pushedLately(m.target.SessionID):
		return false
	}
	_, answer, turn, ok := s.channelLastTurnOf(ctx, m.target.MachineID, m.target.SessionID)
	return ok && answer == m.answer && turn == m.turn
}

// feishuMirrorRetry is how long a mirrored answer whose first card did not get out waits before it is sent again, and
// feishuMirrorRetries how many times: the mirror is the last way a morning brief reaches Feishu, so one error must not lose it.
var (
	feishuMirrorRetry   = 30 * time.Second
	feishuMirrorRetries = 3
)

// feishuMirrorPost sends a mirrored answer's cards, each with its own key that is the same on every try (Feishu lands a
// key once: a card that went out although its reply was lost is not shown twice). When the first does not get out,
// nothing did: the same cards go again later, while mirrorStillDue, up to feishuMirrorRetries times; not when Feishu
// refused it for good. Later ones that fail are named at the end.
func (s *Service) feishuMirrorPost(ctx context.Context, m *mirrorSend, attempt int) {
	var missing []int // the pieces that did not get out (1-based): said at the end
	for i, ac := range m.cards {
		mid, err := s.botPostKeyed(ctx, "", ac.card, ac.fallback, m.key+strconv.Itoa(i))
		if err != nil {
			s.logf("bots: mirror: %v", err)
			if i == 0 {
				// Nothing went out: the rest would read as an answer without its beginning. Tried again later.
				if feishubot.Refused(err) || attempt >= feishuMirrorRetries {
					s.logf("bots: mirror: given up after %d tries", attempt+1)
					return
				}
				time.AfterFunc(feishuMirrorRetry, func() {
					rctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					defer cancel()
					if !s.mirrorStillDue(rctx, m) {
						return // switched off, unpaired, pushed meanwhile, or a newer turn
					}
					s.feishuMirrorPost(rctx, m, attempt+1)
				})
				return
			}
			missing = append(missing, i+1)
			continue
		}
		s.rememberBotThread(mid, m.target, m.label) // a reply to any of them goes to this conversation
		if i == 0 && m.sent != nil {
			m.sent()
		}
	}
	if len(missing) > 0 {
		s.botSay(ctx, missingPiecesNote(m.l, missing))
	}
}

// agentDoneClip bounds the result text in a background agent's finished card; the rest is read in the app, so the
// notice is always one card.
const agentDoneClip = 1000

// feishuAgentDone posts the one Feishu card for a background agent's finished turn (completed, failed or cut off)
// that notifyFinished already decided the phone pushes (#4674). Riding that decision keeps it in step with the push:
// seen, muted, a recent notify_user, a turn asked from the bot, an inbox agent's and a quiet or supervised task's
// finish are all dropped before this is called. The sender still leaves out the conversations that have their own
// Feishu path: the main conversation (the sync mirror), a scheduled task's own conversation (its pushes mirror) and
// health, and posts nothing without the pushes switch.
func (s *Service) feishuAgentDone(machine string, sess proto.Session, head, text, place string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	f, ready := s.feishuFeatures(ctx)
	if !ready || !f.Pushes {
		return
	}
	session := sess.SessionID
	if sess.Assistant || s.mainSessionRep(machine, session) || s.taskSessionRep(machine, session) ||
		spaceOf(sess) == SpaceHealth {
		return
	}
	l := s.botLang(ctx)
	color := feishubot.Green
	if strings.HasPrefix(head, "⚠️") || strings.HasPrefix(head, "⏳") { // a failed/cut-off turn or blocked work left
		color = feishubot.Orange
	}
	note := func(clipped bool) string {
		n := i18n.T(l, "assistant-feishu_cards.agent_done_note", i18n.Args{"place": place})
		if clipped {
			n = i18n.T(l, "assistant-feishu_cards.only_beginning") + "\n" + n
		}
		return n
	}
	limit := agentDoneClip
	hint := i18n.T(l, "assistant-feishu_cards.agent_done_reply_hint")
	var (
		card    *feishubot.Card
		body    string
		clipped bool
	)
	for { // a chart-laden answer can render wide: halve the body until the card fits Feishu's limit
		body = clip(text, limit)
		clipped = len([]rune(text)) > limit
		card = feishubot.NewCard(head, color, head).Markdown(feishubot.CardMarkdown(body)).
			Note(note(clipped)).Buttons(s.openButton(ctx, machine, session)).Note(hint)
		if len(card.JSON()) <= answerCardMaxBytes || limit <= agentDoneClip/8 {
			break
		}
		limit /= 2
	}
	fallback := fallbackText.Replace(head) + "\n\n" + fallbackText.Replace(body) + "\n" + fallbackText.Replace(note(clipped)) +
		"\n" + fallbackText.Replace(hint)
	mid, err := s.botPost(ctx, "", card, strings.TrimSpace(fallback))
	if err != nil {
		s.logf("bots: agent done: %v", err)
		return
	}
	// A reply to the card talks to that agent, as with one picked with /agents.
	s.rememberBotThread(mid, botTarget{Kind: targetAgent, MachineID: machine, SessionID: session},
		agentLabel(sess)+" · "+s.machineName(machine))
}

// --- commands (docs/extensions/bots.md "Commands"): which conversation or agent messages go to, the lists to pick one from,
// starting an agent, stopping a turn ---

func botHelp(l i18n.Lang, f FeishuFeatures) string {
	var b strings.Builder
	b.WriteString(i18n.T(l, "assistant-feishu_cards.help_intro"))
	if f.Main {
		b.WriteString(i18n.T(l, "assistant-feishu_cards.help_main"))
	}
	if f.Agents {
		b.WriteString(i18n.T(l, "assistant-feishu_cards.help_agents"))
	}
	b.WriteString(i18n.T(l, "assistant-feishu_cards.help_tasks"))
	b.WriteString(i18n.T(l, "assistant-feishu_cards.help_always"))
	return b.String()
}

// parseBotCommand splits a bot message into its /command, lower-cased, and the rest. Any white space ends the command
// (a line break, a tab, a full-width space), a full-width slash starts one like "/", and the rest keeps its own line
// breaks with only the white space around it trimmed. ok is false when the message does not start with a slash.
func parseBotCommand(text string) (cmd, rest string, ok bool) {
	t := strings.TrimLeftFunc(text, unicode.IsSpace)
	if after, found := strings.CutPrefix(t, "／"); found {
		t = "/" + after
	}
	if !strings.HasPrefix(t, "/") {
		return "", "", false
	}
	end := strings.IndexFunc(t, unicode.IsSpace)
	if end < 0 {
		end = len(t)
	}
	return strings.ToLower(t[:end]), strings.TrimFunc(t[end:], unicode.IsSpace), true
}

// botCommand runs a /command. Not handled, the message goes on with the text returned: as it is for a command it does
// not know (it may be the agent's own, like /compact), without the command for "/new <message>". root and parent are
// the message's thread root and the message it replies to: the commands that act on a conversation follow the target
// the thread binds, like a normal message does.
func (s *Service) botCommand(ctx context.Context, f FeishuFeatures, messageID, text, root, parent string) (bool, string) {
	l := s.botLang(ctx)
	cmd, rest, ok := parseBotCommand(text)
	if !ok {
		return false, text
	}
	// fallback goes as a rich-text list when Feishu refuses the card (the list cards carry their own; other command
	// replies have none).
	reply := func(c *feishubot.Card, fallback string) {
		if _, err := s.botPost(ctx, messageID, c, fallback); err != nil {
			s.logf("bots: command reply: %v", err)
		}
	}
	switch compat.LegacyBotCommand(cmd) {
	case "/help", "/帮助":
		reply(feishubot.NewCard(i18n.T(l, "assistant-feishu_cards.help_title"), feishubot.Blue, i18n.T(l, "assistant-feishu_cards.help_title")).Markdown(botHelp(l, f)), "")
	case "/main", "/persona", "/主对话":
		if !f.Main {
			s.botNote(ctx, messageID, i18n.T(l, "assistant-feishu_cards.main_off"))
			return true, ""
		}
		s.setBotCurrent(botTarget{Kind: targetMain})
		s.botNote(ctx, messageID, i18n.T(l, "assistant-feishu_cards.now_to_main"))
	case "/new", "/新对话":
		if !f.Main {
			s.botNote(ctx, messageID, i18n.T(l, "assistant-feishu_cards.main_off"))
			return true, ""
		}
		s.setBotCurrent(botTarget{Kind: targetPersona})
		if rest == "" {
			s.botNote(ctx, messageID, i18n.T(l, "assistant-feishu_cards.next_is_new"))
			return true, ""
		}
		return false, rest // "/new help me…": the rest starts it
	case "/list", "/对话":
		if !f.Main {
			s.botNote(ctx, messageID, i18n.T(l, "assistant-feishu_cards.main_off"))
			return true, ""
		}
		reply(s.conversationsCard(ctx))
	case "/agents", "/agent":
		if !f.Agents {
			s.botNote(ctx, messageID, i18n.T(l, "assistant-feishu_cards.agents_off"))
			return true, ""
		}
		reply(s.agentsCard(ctx, rest))
	case "/start", "/开工":
		if !f.Agents {
			s.botNote(ctx, messageID, i18n.T(l, "assistant-feishu_cards.agents_off"))
			return true, ""
		}
		reply(s.startAgentCard(ctx, rest), "")
	case "/stop", "/停止", "/停":
		out := s.stopConversation(ctx, f, s.botTargetFor(root, parent))
		s.botNote(ctx, messageID, out.message)
		return true, ""
	case "/status", "/状态":
		t := s.botTargetFor(root, parent)
		if err := botAllowed(l, f, t); err != nil {
			s.botNote(ctx, messageID, err.Error())
			return true, ""
		}
		card, fallback := s.statusCard(ctx, t)
		reply(card, fallback)
	case "/tasks", "/进展", "/总览": // i18n-ignore: command aliases typed by the user; the catalogs describe them in /help
		if f.Board && s.standingBoardCommand(ctx) {
			break
		}
		for _, oc := range s.tasksOverview(ctx) {
			reply(oc.card, oc.fallback)
		}
	default:
		// A command the bot does not know goes on as it is (an agent's own, like /compact). A bare one a letter off
		// one of the bot's (/stpo for /stop) sent to a Persona conversation would sit in line behind the turn it
		// meant to stop and be answered: the owner is asked instead. Words after it (/news what is in the news today) make it a
		// message; an agent's may be the project's own (/lint), so it goes. A doubled slash (//stpo) sends the text
		// itself, one slash off, but only before a bot command or a slip of one and outside an agent: other text that
		// starts with // (a "// TODO" note, "//example.com/x") goes as written.
		toAgent := s.botTargetFor(root, parent).agent()
		if word, ok := strings.CutPrefix(cmd, "/"); ok && strings.HasPrefix(word, "/") {
			if literal, ok := strings.CutPrefix(strings.TrimLeftFunc(text, unicode.IsSpace), "/"); ok && !toAgent && (slices.Contains(botCommandNames, word) || botCommandTypo(word) != "") {
				return false, literal
			}
			return false, text
		}
		if want := botCommandTypo(cmd); want != "" && rest == "" && !toAgent {
			s.botNote(ctx, messageID, i18n.T(l, "assistant-feishu_cards.command_typo", i18n.Args{"got": cmd, "want": want}))
			return true, ""
		}
		return false, text
	}
	return true, ""
}

// statusPendingMax bounds a /status card's pending rows so a long queue can't get the card refused.
const statusPendingMax = 5

var ixIcons = map[string]string{proto.IxQuestion: "❓ ", proto.IxPlan: "📋 ", proto.IxPermission: "🔐 ", proto.IxScreen: "🖥 ", proto.IxElicitation: "📝 "}

// pendingQuestion is one line saying what a pending interaction waits for; "" when it says nothing.
func pendingQuestion(ix proto.Interaction) string {
	switch ix.Kind {
	case proto.IxQuestion:
		return strings.TrimSpace(questionSummary(ix))
	case proto.IxPlan:
		return strings.TrimSpace(planTitle(ix.Plan))
	case proto.IxPermission:
		return strings.TrimSpace(toolDisplayName(ix.ToolName) + " " + oneLine(ix.Summary))
	}
	return strings.TrimSpace(oneLine(ix.Summary))
}

// statusCard is /status plus the same content as plain text (the fallback when Feishu refuses the card): the
// target's work state (offline / unknown never reads as idle), last activity, goal and pending questions. With the
// fleet online both sources are local; the web button is built once and is nil without a public_url.
func (s *Service) statusCard(ctx context.Context, t botTarget) (*feishubot.Card, string) {
	l := s.botLang(ctx)
	card := feishubot.NewCard("", "", i18n.T(l, "assistant-feishu_cards.status_title"))
	rt, err := s.resolveBotTarget(ctx, t) // one resolve: the target line reuses its label, not targetLabel's
	label := i18n.T(l, "assistant-feishu.not_found")
	switch {
	case err == nil && t.Kind == targetPersona && rt.isNew:
		label = i18n.T(l, "assistant-feishu.new_chat_next")
	case err == nil:
		label = rt.label
	}
	var head, fb strings.Builder
	to := i18n.T(l, "assistant-feishu_cards.status_to", i18n.Args{"target": feishubot.EscapeMarkdown(label)})
	head.WriteString(to)
	fb.WriteString(fallbackText.Replace(i18n.T(l, "assistant-feishu_cards.status_to", i18n.Args{"target": label}))) // ends its own line
	if running := len(s.channelRuns(channel.Feishu)); running > 0 {
		waiting := i18n.T(l, "assistant-feishu_cards.status_waiting", i18n.Args{"count": running})
		head.WriteString(waiting)
		fb.WriteString(fallbackText.Replace(waiting))
	}
	card.Markdown(strings.TrimSpace(head.String()))
	if err != nil || rt.isNew || rt.session.SessionID == "" {
		return card, strings.TrimSpace(fb.String())
	}

	sess := rt.session
	if list, err := s.sessions(ctx, rt.machine); err == nil {
		for _, x := range list {
			if x.SessionID == sess.SessionID {
				sess = x
				break
			}
		}
	}
	stateKey := "assistant-feishu_cards.status_state_offline" // offline, unknown or unset: never an idle machine
	switch sess.State {
	case proto.StateNeedsYou:
		stateKey = "assistant-feishu_cards.status_state_waiting"
	case proto.StateWorking:
		stateKey = "assistant-feishu_cards.status_state_working"
	case proto.StateUnread, proto.StateIdle:
		stateKey = "assistant-feishu_cards.status_state_idle"
	}
	state := i18n.T(l, stateKey)
	line := state
	fmt.Fprintf(&fb, "%s\n", fallbackText.Replace(state))
	if sess.LastText != "" {
		last := clip(oneLine(sess.LastText), 90)
		line += "\n" + feishubot.EscapeMarkdown(last)
		fb.WriteString(fallbackText.Replace(last) + "\n")
	}
	if g := sess.Goal; g != nil && g.Objective != "" && (g.Status == "" || g.Status == proto.GoalActive) {
		goal := i18n.T(l, "assistant-feishu_cards.status_goal", i18n.Args{"objective": feishubot.EscapeMarkdown(clip(oneLine(g.Objective), 90))})
		line += "\n" + goal
		fb.WriteString(fallbackText.Replace(goal) + "\n")
	}
	card.Markdown(line)

	var pending []proto.Interaction
	for _, ix := range s.pendingInteractions() {
		if ix.MachineID == rt.machine && ix.SessionID == sess.SessionID && pendingQuestion(ix) != "" {
			pending = append(pending, ix)
		}
	}
	pendingTitle := i18n.T(l, "assistant-feishu_cards.status_pending")
	card.Markdown(pendingTitle)
	fb.WriteString(fallbackText.Replace(pendingTitle) + "\n")
	if len(pending) == 0 {
		none := i18n.T(l, "assistant-feishu_cards.status_no_pending")
		card.Markdown(none)
		fb.WriteString(none + "\n")
		return card, strings.TrimSpace(fb.String())
	}
	open := s.openButton(ctx, rt.machine, sess.SessionID) // every row is the same conversation: build it once
	shown := pending
	if len(pending) > statusPendingMax {
		shown = pending[:statusPendingMax]
	}
	for _, ix := range shown {
		q := clip(pendingQuestion(ix), 120)
		card.Markdown(ixIcons[ix.Kind] + "**" + feishubot.EscapeMarkdown(q) + "**").Buttons(open)
		fmt.Fprintf(&fb, "- %s%s\n", ixIcons[ix.Kind], fallbackText.Replace(q))
	}
	if more := len(pending) - len(shown); more > 0 {
		key := "assistant-feishu_cards.status_more_pending"
		if open == nil { // no web app to send the reader to: just count them
			key = "assistant-feishu_cards.status_more_pending_no_web"
		}
		note := i18n.T(l, key, i18n.Args{"count": more})
		card.Note(note)
		fb.WriteString(note + "\n")
	}
	return card, strings.TrimSpace(fb.String())
}

// stopOutcome is what interrupting a conversation's current turn came to; the caller renders it as a command note
// (/stop) or a card toast (the progress card's Stop button).
type stopOutcome struct {
	kind    string // toast kind: success | info | error
	message string
}

// stopConversation interrupts t's current turn. The /stop command and the progress card's Stop button share it: both
// may only act on a target this chat may use (the Agents feature gates agent threads even before the normal message
// gate), and both say so when there is nothing to stop.
func (s *Service) stopConversation(ctx context.Context, f FeishuFeatures, t botTarget) stopOutcome {
	l := s.botLang(ctx)
	if err := botAllowed(l, f, t); err != nil {
		return stopOutcome{kind: "error", message: err.Error()}
	}
	rt, err := s.resolveBotTarget(ctx, t)
	if err != nil || rt.isNew {
		return stopOutcome{kind: "info", message: i18n.T(l, "assistant-feishu_cards.nothing_running")}
	}
	if err := s.call(ctx, rt.machine, "session.interrupt", map[string]any{"session_id": rt.session.SessionID}, nil); err != nil {
		return stopOutcome{kind: "error", message: i18n.T(l, "assistant-feishu_cards.not_stopped", i18n.Args{"error": clip(oneLine(err.Error()), 200)})}
	}
	return stopOutcome{kind: "success", message: i18n.T(l, "assistant-feishu_cards.stopped", i18n.Args{"label": rt.label})}
}

// botCommandNames are the bot's own commands a typo is matched against (the Chinese ones are too short to mistype).
var botCommandNames = []string{"/help", "/main", "/persona", "/new", "/list", "/agents", "/start", "/stop", "/status", "/tasks"}

// agentCommandNames are Claude Code's and Codex's own commands, and plain words, a slip off one of the bot's: they go
// on as they are.
var agentCommandNames = map[string]bool{"/stats": true, "/model": true, "/mcp": true, "/init": true, "/exit": true, "/quit": true,
	"/clear": true, "/cost": true, "/diff": true, "/plan": true, "/undo": true, "/todos": true, "/hooks": true,
	"/man": true, "/star": true, "/stat": true}

// botCommandTypo is the bot's command cmd is a slip of (a letter dropped, or two side by side swapped), "" when none
// or when cmd is an agent's own. A letter added or changed is not counted: those make real words (/news, /now,
// /step, /shop) more often than slips.
func botCommandTypo(cmd string) string {
	if agentCommandNames[cmd] {
		return ""
	}
	for _, want := range botCommandNames {
		if cmd != want && slipOf(cmd, want) {
			return want
		}
	}
	return ""
}

// slipOf: got is want with one rune dropped, or with two adjacent runes swapped.
func slipOf(got, want string) bool {
	x, y := []rune(got), []rune(want)
	switch len(y) - len(x) {
	case 0:
		var diff []int
		for i := range x {
			if x[i] != y[i] {
				diff = append(diff, i)
			}
		}
		if len(diff) != 2 {
			return false
		}
		i, j := diff[0], diff[1]
		return j == i+1 && x[i] == y[j] && x[j] == y[i]
	case 1:
		i := 0
		for i < len(x) && x[i] == y[i] {
			i++
		}
		return string(x[i:]) == string(y[i+1:])
	}
	return false
}

// botListNameMost bounds a conversation / agent / device name in a list card: the title is often a whole command
// line and the switch button carries the name again, so an unclipped name could make Feishu refuse the whole card.
const botListNameMost = 60

var fallbackText = strings.NewReplacer("`", "", "*", "", "_", "", "[", "", "]", "", "~", "", "\\", "") // strips Markdown metacharacters for the post fallback

// conversationsCard lists recent Persona conversations with a button to switch to each. It also returns the same
// list as plain text: the card goes with it as fallback, so a refused card still shows every conversation.
func (s *Service) conversationsCard(ctx context.Context) (*feishubot.Card, string) {
	list, main := s.conversations(ctx, false, "")
	sort.SliceStable(list, func(i, j int) bool { return list[i].UpdatedAt.After(list[j].UpdatedAt) })
	l := s.botLang(ctx)
	title := i18n.T(l, "assistant-feishu_cards.recent_chats")
	c := feishubot.NewCard(title, feishubot.Blue, title)
	if len(list) == 0 {
		empty := i18n.T(l, "assistant-feishu_cards.no_chats")
		return c.Markdown(empty), title + "\n\n" + empty
	}
	var b strings.Builder
	b.WriteString(title + "\n")
	st, _ := s.feishuStatusKept(ctx)
	web := strings.TrimRight(st.PublicURL, "/") // every row's link is on this web app; "" keeps all buttons out
	for i, sess := range list {
		if i == 8 {
			break
		}
		name := clip(oneLine(conversationName(sess, main)), botListNameMost)
		line := "**" + feishubot.EscapeMarkdown(name) + "**"
		if !sess.UpdatedAt.IsZero() {
			line += " · " + botAgo(l, sess.UpdatedAt)
		}
		if sess.LastText != "" {
			line += "\n" + feishubot.EscapeMarkdown(clip(oneLine(sess.LastText), 80))
		}
		kind := targetPersona
		if sess.SessionID == main {
			kind = targetMain
		}
		switchBtn := feishubot.Btn(i18n.T(l, "assistant-feishu_cards.chat_here"), feishubot.Default, map[string]any{"a": "switch", "k": kind, "m": sess.MachineID, "s": sess.SessionID, "t": name})
		// The rows are Persona conversations (conversations lists Assistant sessions), so the link is /persona/<id>.
		openBtn := s.openLinkButton(ctx, sessionLink(web, sess.MachineID, sess.SessionID, sess.Assistant))
		c.Markdown(line).Buttons(switchBtn, openBtn)
		fmt.Fprintf(&b, "- %s\n", fallbackText.Replace(name))
	}
	return c.Note(i18n.T(l, "assistant-feishu_cards.new_hint")), strings.TrimSpace(b.String())
}

// botAgo is how long ago t was, in l (textfmt.Ago's wording in Chinese).
func botAgo(l i18n.Lang, t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return i18n.T(l, "assistant-feishu_cards.ago_now")
	case d < time.Hour:
		return i18n.T(l, "assistant-feishu_cards.ago_minutes", i18n.Args{"count": int(d.Minutes())})
	case d < 24*time.Hour:
		return i18n.T(l, "assistant-feishu_cards.ago_hours", i18n.Args{"count": int(d.Hours())})
	}
	return i18n.T(l, "assistant-feishu_cards.ago_days", i18n.Args{"count": int(d.Hours() / 24)})
}

// agentsCard lists the agents worth talking to: needing you, working, finished unseen, then the ones idle lately.
// It also returns the same list as plain text, used as the card's fallback when Feishu refuses the card.
func (s *Service) agentsCard(ctx context.Context, query string) (*feishubot.Card, string) {
	type row struct {
		d    Device
		sess proto.Session
	}
	var rows []row
	q := strings.ToLower(query)
	for _, d := range s.devices(ctx) {
		list, _ := s.sessions(ctx, d.ID)
		for _, sess := range list {
			if sess.Assistant || sess.State == proto.StateOffline || sess.State == proto.StateUnknown {
				continue
			}
			if sess.State == proto.StateIdle && time.Since(sess.UpdatedAt) > 48*time.Hour {
				continue
			}
			if q != "" && !strings.Contains(strings.ToLower(sess.Title+" "+sess.Cwd+" "+sess.SpaceName+" "+sess.SlotName+" "+d.Name), q) {
				continue
			}
			rows = append(rows, row{d, sess})
		}
	}
	order := map[string]int{proto.StateNeedsYou: 0, proto.StateWorking: 1, proto.StateUnread: 2, proto.StateIdle: 3}
	sort.SliceStable(rows, func(i, j int) bool {
		oi, oj := order[rows[i].sess.State], order[rows[j].sess.State]
		if oi != oj {
			return oi < oj
		}
		return rows[i].sess.UpdatedAt.After(rows[j].sess.UpdatedAt)
	})
	l := s.botLang(ctx)
	title := i18n.T(l, "assistant-feishu_cards.agents_title")
	c := feishubot.NewCard(title, feishubot.Purple, i18n.T(l, "assistant-feishu_cards.agents_summary"))
	startNew := feishubot.Btn(i18n.T(l, "assistant-feishu_cards.start_new"), feishubot.Primary, map[string]any{"a": "start_form"})
	if len(rows) == 0 {
		empty := i18n.T(l, "assistant-feishu_cards.no_agents")
		return c.Markdown(empty).Buttons(startNew), title + "\n\n" + empty
	}
	var b strings.Builder
	b.WriteString(title + "\n")
	icons := map[string]string{proto.StateNeedsYou: "🔴", proto.StateWorking: "🟢", proto.StateUnread: "🔵", proto.StateIdle: "⚪️"}
	for i, r := range rows {
		if i == 10 {
			more := i18n.T(l, "assistant-feishu_cards.more_agents", i18n.Args{"count": len(rows) - 10})
			c.Note(more)
			b.WriteString(more + "\n")
			break
		}
		label := clip(oneLine(agentLabel(r.sess)), botListNameMost)
		name := clip(oneLine(r.d.Name), botListNameMost)
		line := icons[r.sess.State] + " **" + feishubot.EscapeMarkdown(label) + "** · " + feishubot.EscapeMarkdown(name) + " · " + i18n.T(l, "assistant-feishu_cards.state_"+r.sess.State)
		if r.sess.LastText != "" {
			line += "\n" + feishubot.EscapeMarkdown(clip(oneLine(r.sess.LastText), 90))
		}
		c.Markdown(line).Buttons(feishubot.Btn(i18n.T(l, "assistant-feishu_cards.chat_here"), feishubot.Default,
			map[string]any{"a": "switch", "k": targetAgent, "m": r.d.ID, "s": r.sess.SessionID, "t": label + " · " + name}))
		fmt.Fprintf(&b, "- %s %s · %s\n", icons[r.sess.State], fallbackText.Replace(label), fallbackText.Replace(name))
	}
	return c.Buttons(startNew), strings.TrimSpace(b.String())
}

// startAgentCard is a form to start a coding agent on a machine.
func (s *Service) startAgentCard(ctx context.Context, prompt string) *feishubot.Card {
	var opts []feishubot.Option
	for _, d := range s.devices(ctx) {
		if d.Online {
			opts = append(opts, feishubot.Option{Label: d.Name, Value: d.ID})
		}
	}
	l := s.botLang(ctx)
	c := feishubot.NewCard(i18n.T(l, "assistant-feishu_cards.start_title"), feishubot.Purple, i18n.T(l, "assistant-feishu_cards.start_title"))
	if len(opts) == 0 {
		return c.Markdown(i18n.T(l, "assistant-feishu_cards.no_machines"))
	}
	fields := []any{
		feishubot.Select("machine", i18n.T(l, "assistant-feishu_cards.which_machine"), opts, false),
		feishubot.Input("cwd", i18n.T(l, "assistant-feishu_cards.directory"), i18n.T(l, "assistant-feishu_cards.directory_placeholder"), false),
		feishubot.Input("prompt", i18n.T(l, "assistant-feishu_cards.what_to_do"), i18n.T(l, "assistant-feishu_cards.what_to_do_placeholder"), true),
	}
	if prompt != "" {
		fields[2].(map[string]any)["default_value"] = prompt
	}
	return c.Form("start", fields, feishubot.Submit(i18n.T(l, "assistant-feishu_cards.start"), feishubot.Primary, "start_submit", map[string]any{"a": "start"}))
}
