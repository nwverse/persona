package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/botcard"
	"github.com/nwverse/persona/server/internal/channel"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
	"github.com/nwverse/persona/server/internal/transcript"
)

// Cards for what Persona and the agents ask (the bot's cards switch): a question, a plan, a permission or a terminal
// dialog shows in the chat as a card with the same choices as the app's card; answering it in either place answers it
// (interactions.resolve), and the card in Feishu then says how it was answered (interaction.ended). Cards go out for
// what the chat drives (its target, and the conversations of its runs), or for everything with all_cards. The card
// buttons and the chat's commands are the lark module's (card_actions.go, commands.go); the pushes and sync mirror are
// in feishu_push.go.

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
	maxPlanRunes         = 5000  // runes of a plan shown on its card (clipPlan)
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
// drives the conversation or "all cards" is on (not for a card that says something sensitive).
func (s *Service) feishuCardWanted(ctx context.Context, ix proto.Interaction) bool {
	f, ready := s.chat().Features(ctx)
	if !ready || !f.Cards {
		return false
	}
	if ix.Private {
		return false // a Just me turn's card: never on Lark (meeting_justme.go)
	}
	// "All cards" does not take a card that says something sensitive (its words and its track's default categories):
	// only one the chat drives itself (health.go, info_use.go).
	if drives := s.botDrives(ctx, ix.MachineID, ix.SessionID); !drives &&
		(!f.AllCards || s.exitPolicy(ctx, interactionText(ix), ix.SessionID, s.ixSpace(ctx, ix)).Redact) {
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
	if s.interactionEnded(ix.ID) { // ended while this looked at the bot: not asked (s.chans before the chat, never in it)
		return
	}
	chat := s.larkModule()
	claimed := false
	chat.Book(ctx, func(b assistantwire.ChatBook) {
		if _, ok := b.Card(ix.ID); !ok {
			b.KeepCard(ix.ID, &botCard{}) // claimed: a second announce of the same card does not post twice
			claimed = true
		}
	})
	if !claimed {
		return
	}
	reply, to, topic := s.interactionPlace(ix)
	ctx = feishuSendAs(ctx, string(ix.Kind), ix.MachineID, ix.SessionID)
	fallback := ""
	switch ix.Kind {
	case proto.IxQuestion:
		fallback = "❓ " + clip(firstQuestion(ix), 100)
	case proto.IxPermission:
		fallback = "🔐 " + ixToolName(ix)
	case proto.IxPlan:
		fallback = "📋 " + clip(firstLine(ix.Plan), 100)
	case proto.IxScreen:
		fallback = "🖥 " + i18n.T(s.botLang(ctx), "assistant-feishu_cards.screen_summary")
		if terminalQuestion(ix) {
			fallback = "🖥 " + s.ixWhere(ctx, ix) + " · " + terminalQuestionText(ix)
		}
	}
	card := s.interactionCard(ctx, ix)
	if s.interactionEnded(ix.ID) {
		// Answered while its card was being made (a program that answers cards at once): nobody needs to look at it,
		// so nothing is posted and the claim goes (#5430). One that ends during the post is closed after it, below.
		chat.Book(ctx, func(b assistantwire.ChatBook) { b.KeepCard(ix.ID, nil) })
		return
	}
	// A card for a topic is a reply to its run's message and belongs under it: if that message is gone it would be a
	// stray card in the group (the hub would send it into the chat), and the owner may not see it there. It goes to the
	// owner instead, as with a chat that refused it.
	to.NoDetour = to.ChatID != ""
	mid, err := s.botPostTo(ctx, to, reply, card, fallback, "")
	if err != nil && to.ChatID != "" && (chatRefused(err) || botcard.MessageGone(err)) {
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
	if err != nil {
		s.logf("bots: card for %s: %v", ix.ID, err)
		chat.Book(ctx, func(b assistantwire.ChatBook) { b.KeepCard(ix.ID, nil) })
		return
	}
	kept := false
	chat.Book(ctx, func(b assistantwire.ChatBook) {
		if _, still := b.Card(ix.ID); !still || ended { // answered while it was being posted
			b.KeepCard(ix.ID, nil)
			return
		}
		b.KeepCard(ix.ID, &botCard{MessageID: mid, Ix: ix, At: b.Now(), Thread: reply, ChatID: to.ChatID})
		kept = true
	})
	if !kept {
		s.botCardDone(ctx, mid, ix, i18n.T(s.botLang(ctx), "assistant-feishu_cards.handled"))
		return
	}
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
	return botcard.ChatRefused(err)
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
// asked in a topic, of the conversation it belongs to (either may be nil). While a /read of the conversation goes on,
// the card may be that run's, and a /read's group gets its answer and nothing else: the card goes to the owner's DM,
// so no topic is returned either (another topic's run of the same conversation would take the /read's question into
// its group, and a line in that topic would point at a card it has nothing to do with).
func (s *Service) interactionRuns(ix proto.Interaction) (private, topic *channel.Run) {
	read := false
	for _, r := range s.channelRuns(channel.Feishu) {
		if r.MachineID != ix.MachineID || r.SessionID != ix.SessionID {
			continue
		}
		if readRun(s.runExts(&r, readChatKey)) {
			read = true
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
	if read {
		topic = nil
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
	bound := false
	s.larkModule().Book(ctx, func(b assistantwire.ChatBook) { bound = b.Bound(machine, session, 24*time.Hour) })
	if bound {
		return true
	}
	cur := s.larkModule().Route("", "")
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
	var c botCard
	ok := false
	// A card posted before a restart is in the chat's state, which Book reads first.
	s.larkModule().Book(context.Background(), func(b assistantwire.ChatBook) {
		c, ok = b.Card(ix.ID)
		b.KeepCard(ix.ID, nil)
		if ok && !inChat {
			b.NoteEnded(c) // a question a plain message could have answered: an option typed for it late is no new message
		}
	})
	if !ok || c.MessageID == "" {
		return
	}
	if ix.Kind == "" { // the catch-up knows only what was kept of it
		ix = c.Ix
	} else if ix.TaskTitle == "" {
		ix.TaskTitle = c.Ix.TaskTitle
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
	dc := &s.fdetached
	dc.mu.Lock()
	var closing []detachedCard
	s.larkModule().Book(context.Background(), func(b assistantwire.ChatBook) {
		now := b.Now()
		for id, d := range dc.cards {
			if now.Sub(d.at) > resumeGrace {
				delete(dc.cards, id)
			}
		}
		for _, id := range ids {
			c, ok := b.Card(id)
			if !ok || c.MessageID == "" {
				continue
			}
			b.KeepCard(id, nil) // closed here, not again by feishuInteractionDone
			if dc.cards == nil {
				dc.cards = map[string]detachedCard{}
			}
			d := detachedCard{card: c, at: now, closed: make(chan struct{})}
			dc.cards[id] = d
			closing = append(closing, d)
		}
	})
	dc.mu.Unlock()
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
	chat := s.larkModule()
	dc := &s.fdetached
	dc.mu.Lock()
	d, ok := dc.cards[ix.ID]
	delete(dc.cards, ix.ID)
	back := false
	chat.Book(context.Background(), func(b assistantwire.ChatBook) {
		if _, posted := b.Card(ix.ID); !ok || posted || b.Now().Sub(d.at) > resumeGrace {
			return
		}
		b.KeepCard(ix.ID, &botCard{MessageID: d.card.MessageID, Thread: d.card.Thread, ChatID: d.card.ChatID, Ix: ix, At: b.Now(),
			NudgedAt: d.card.NudgedAt, SkippedAt: d.card.SkippedAt, Tries: d.card.Tries, TriedAt: d.card.TriedAt})
		back = true
	})
	dc.mu.Unlock()
	if !back {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	select {
	case <-d.closed:
	case <-ctx.Done():
		return
	}
	if err := s.botUpdateCard(ctx, d.card.MessageID, s.interactionCard(ctx, ix)); err != nil {
		s.logf("bots: reopen card %s: %v", ix.ID, err)
		chat.Book(ctx, func(b assistantwire.ChatBook) {
			if c, still := b.Card(ix.ID); still && c.MessageID == d.card.MessageID {
				b.KeepCard(ix.ID, nil)
			}
		})
		return
	}
	still := false
	chat.Book(ctx, func(b assistantwire.ChatBook) { _, still = b.Card(ix.ID) })
	if _, pending := s.pendingIx(ix.ID); !still || !pending { // answered or ended meanwhile: it says so again
		chat.Book(ctx, func(b assistantwire.ChatBook) { b.KeepCard(ix.ID, nil) })
		s.botCardDone(ctx, d.card.MessageID, ix, i18n.T(s.botLang(ctx), "assistant-feishu_cards.handled"))
	}
}

func (s *Service) botCardDone(ctx context.Context, messageID string, ix proto.Interaction, how string) {
	if err := s.botUpdateCard(ctx, messageID, s.answeredCard(ctx, ix, how)); err != nil {
		s.logf("bots: close card %s: %v", ix.ID, err)
		s.botNote(ctx, messageID, "✓ "+how)
	}
}

// interactionCard is the card asking ix, with the app's choices.
func (s *Service) interactionCard(ctx context.Context, ix proto.Interaction) *botcard.Card {
	l := s.botLang(ctx)
	place, secondary := s.feishuAgentPlace(ctx, ix)
	place = s.ixTaskPlace(ctx, ix, place)
	where := botcard.EscapeMarkdown(place)
	val := func(extra map[string]any) map[string]any {
		v := map[string]any{"a": "ix", "id": ix.ID}
		for k, x := range extra {
			v[k] = x
		}
		return v
	}
	open := s.openButton(ctx, ix.MachineID, ix.SessionID)
	// The "more" menu every card ends its row of actions with: dismissing it where it can be, and its conversation.
	menu := func(dismiss bool) botcard.Button {
		var items []botcard.MenuItem
		if dismiss {
			items = append(items, botcard.MenuItem{Label: i18n.T(l, "assistant-feishu_cards.dismiss"), Value: map[string]any{"dismiss": "1"}})
		}
		if label, web := s.openLink(ctx, ix.MachineID, ix.SessionID); web != "" {
			items = append(items, botcard.MenuItem{Label: label, URL: web, PC: botcard.SidebarURL(web)})
		}
		return botcard.Overflow(val(nil), items...)
	}
	switch ix.Kind {
	case proto.IxQuestion:
		c := s.ixCard(ctx, ix, "assistant-feishu_cards.tag_question", i18n.T(l, "assistant-feishu_cards.question_summary", i18n.Args{"question": firstQuestion(ix)}))
		if len(ix.Questions) == 1 && !ix.Questions[0].MultiSelect && len(ix.Questions[0].Options) <= 4 {
			q := ix.Questions[0]
			c.Markdown(questionText(l, q))
			var bs []botcard.Button
			submit := botcard.Primary // the card's one primary: the recommended option, else the written answer
			for _, o := range q.Options {
				style := botcard.Default
				if o.Label == q.Recommend { // what the asker recommends is the one to press
					style, submit = botcard.Primary, botcard.Default
				}
				bs = append(bs, botcard.Btn(clip(o.Label, 30), style, val(map[string]any{"q": "0", "v": o.Label})))
			}
			c.Buttons(append(bs, menu(true))...)
			c.Form("other", []any{botcard.Input("text", "", i18n.T(l, "assistant-feishu_cards.or_write_answer"), false)},
				botcard.Submit(i18n.T(l, "assistant-feishu_cards.answer"), submit, "q_submit", val(map[string]any{"q": "0", "free": "1"})))
			if len(q.Options) > 0 {
				c.Note(i18n.T(l, "assistant-feishu_cards.answer_by_text"))
			}
			return safeCard(l, c, open)
		} else if len(ix.Questions) > maxFormQuestions {
			// The submit handler needs an answer to every question, so a form clipped to the first few could never be
			// sent: that many questions are answered in the app.
			c.Note(i18n.T(l, "assistant-feishu_cards.questions_in_app", i18n.Args{"count": len(ix.Questions)}))
			if open != nil {
				open["type"] = botcard.Primary
			}
			return safeCard(l, c.Buttons(open, menu(true)), open)
		} else {
			var fields []any
			for i, q := range ix.Questions {
				fields = append(fields, botcard.MarkdownBlock(questionText(l, q)))
				var opts []botcard.Option
				for j, o := range q.Options {
					if j >= maxFormOptions {
						break
					}
					opts = append(opts, botcard.Option{Label: clip(o.Label, 60), Value: o.Label})
				}
				if len(opts) > 0 {
					fields = append(fields, botcard.Select("q"+strconv.Itoa(i), map[bool]string{false: i18n.T(l, "assistant-feishu_cards.pick_one"), true: i18n.T(l, "assistant-feishu_cards.pick_some")}[q.MultiSelect], opts, q.MultiSelect))
				}
				fields = append(fields, botcard.Input("t"+strconv.Itoa(i), "", i18n.T(l, "assistant-feishu_cards.or_write"), false))
			}
			c.Form("answers", fields, botcard.Submit(i18n.T(l, "assistant-feishu_cards.submit"), botcard.Primary, "qs_submit", val(map[string]any{"form": "1"})))
			// A form of one question with options also takes a plain reply; a form of several is submitted as one (a
			// reply is not split over its questions), and a free-text question has no options.
			if len(ix.Questions) == 1 && len(ix.Questions[0].Options) > 0 {
				c.Note(i18n.T(l, "assistant-feishu_cards.answer_by_text"))
			}
		}
		return safeCard(l, c.Buttons(menu(true)), open)
	case proto.IxPlan:
		c := s.ixCard(ctx, ix, "assistant-feishu_cards.tag_plan", i18n.T(l, "assistant-feishu_cards.plan_summary"))
		c.Markdown(botcard.CardMarkdown(clipPlan(ix.Plan, i18n.T(l, "assistant-feishu_cards.plan_clipped"))))
		// Approving to run on its own lets the agent go on without asking: Feishu's dialog asks first.
		c.Buttons(
			botcard.Confirm(botcard.Btn(i18n.T(l, "assistant-feishu_cards.approve_run"), botcard.Primary, val(map[string]any{"approve": "1", "mode": "bypassPermissions"})),
				i18n.T(l, "assistant-feishu_cards.confirm_run_title"), i18n.T(l, "assistant-feishu_cards.confirm_run_text")),
			botcard.Btn(i18n.T(l, "assistant-feishu_cards.approve_edits"), botcard.Default, val(map[string]any{"approve": "1", "mode": "acceptEdits"})),
			menu(false),
		)
		c.Form("reject", []any{botcard.Input("feedback", "", i18n.T(l, "assistant-feishu_cards.feedback_placeholder"), true)},
			botcard.Submit(i18n.T(l, "assistant-feishu_cards.send_back"), botcard.Danger, "plan_reject", val(map[string]any{"approve": "0"})))
		return safeCard(l, c, open)
	case proto.IxPermission:
		c := s.ixCard(ctx, ix, "assistant-feishu_cards.tag_permission", i18n.T(l, "assistant-feishu_cards.permission_summary", i18n.Args{"tool": ixToolName(ix)}))
		var b strings.Builder
		fmt.Fprintf(&b, "**%s**", botcard.EscapeMarkdown(ixToolName(ix)))
		if ix.Reason != "" {
			fmt.Fprintf(&b, "\n%s", botcard.EscapeMarkdown(clip(ix.Reason, maxPermissionReason)))
		}
		c.Markdown(b.String())
		if sum := strings.TrimSpace(ix.Summary); sum != "" {
			if !ownerActionProposal(ix) {
				sum = clip(sum, 2500)
			}
			c.Markdown("```\n" + strings.ReplaceAll(sum, "```", "'''") + "\n```")
		} else if in := proto.RedactArgs(ix.ToolInput); in != "" { // a credential's value never goes to the chat (#4734)
			c.Markdown("```json\n" + strings.ReplaceAll(clip(in, 2500), "```", "'''") + "\n```")
		}
		if ix.CanClarify && !ix.Private {
			// Question-only form: no Resolution.Values, Allow or Deny travels with
			// it. Field-bearing cards keep their fields App-only but may still ask.
			c.Form("clarify", []any{botcard.Input("question", "", i18n.T(l, "assistant-feishu_cards.question_placeholder"), false)},
				botcard.Submit(i18n.T(l, "assistant-feishu_cards.have_question"), botcard.Default, "permission_clarify", val(map[string]any{"clarify": "1"})))
		}
		if len(ix.Fields) > 0 {
			// A password or a code is typed in the app, never in a chat.
			c.Note(i18n.T(l, "assistant-feishu_cards.fields_in_app"))
			if open != nil {
				open["type"] = botcard.Primary
			}
			return safeCard(l, c.Buttons(open), open)
		}
		bs := []botcard.Button{
			botcard.Btn(i18n.T(l, "assistant-feishu_cards.allow"), botcard.Primary, val(map[string]any{"allow": "1"})),
			botcard.Btn(i18n.T(l, "assistant-feishu_cards.deny"), botcard.Danger, val(map[string]any{"allow": "0"})),
		}
		// As the app's card (InteractionViews.swift's BUIPermissionForm, web's PermissionCard): Always allow for this session for
		// Claude on a conversation, and for another agent only what it says that always allows — a runtime cannot keep
		// Claude's session rule (node/runtimes.go), so the button there would only allow once and ask again.
		label := ix.AlwaysLabel
		if label == "" && (ix.Agent == "" || ix.Agent == "claude") && ix.SessionID != "" {
			label = i18n.T(l, "assistant-feishu_cards.always_allow")
		}
		// The manager's word wins where it gave one (#3590): no button that would only allow once.
		if kept := ix.AlwaysHonored; kept != nil && !*kept {
			label = ""
		} else if kept != nil && label == "" {
			label = i18n.T(l, "assistant-feishu_cards.always_allow")
		}
		if label != "" {
			// A permission that stays for the session asks first.
			bs = append(bs, botcard.Confirm(botcard.Btn(clip(label, 20), botcard.Default, val(map[string]any{"allow": "1", "always": "1"})),
				i18n.T(l, "assistant-feishu_cards.confirm_always_title"), i18n.T(l, "assistant-feishu_cards.confirm_always_text")))
		}
		return safeCard(l, c.Buttons(append(bs, menu(false))...), open)
	case proto.IxScreen:
		summary := i18n.T(l, "assistant-feishu_cards.screen_summary")
		if terminalQuestion(ix) {
			summary = ix.Dialog.Question + " · " + place
		}
		c := s.ixCard(ctx, ix, "assistant-feishu_cards.tag_screen", summary)
		if d := ix.Dialog; d != nil {
			q := d.Question
			if d.Detail != "" {
				q = d.Detail + "\n" + q
			}
			if r := []rune(q); len(r) > maxDialogRunes {
				q = string(r[:maxDialogRunes]) + i18n.T(l, "assistant-feishu_cards.clipped")
			}
			c.Markdown(botcard.EscapeMarkdown(q))
			var bs []botcard.Button
			for _, o := range d.Options {
				if o.Kind == "type_something" || o.Kind == "chat" {
					continue
				}
				if len(bs) >= maxDialogOptions {
					break
				}
				bs = append(bs, botcard.Btn(clip(o.Label, 30), botcard.Default, val(map[string]any{"opt": o.Key})))
			}
			c.Buttons(append(bs, menu(false))...)
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
				botcard.Btn(i18n.T(l, "assistant-feishu_cards.enter"), botcard.Default, val(map[string]any{"keys": "enter"})),
				botcard.Btn("Esc", botcard.Default, val(map[string]any{"keys": "esc"})),
				botcard.Btn("1", botcard.Default, val(map[string]any{"keys": "1"})),
				botcard.Btn("2", botcard.Default, val(map[string]any{"keys": "2"})),
				menu(false),
			)
		}
		if !ix.Secret {
			c.Form("type", []any{botcard.Input("text", "", i18n.T(l, "assistant-feishu_cards.type_placeholder"), false)},
				botcard.Submit(i18n.T(l, "assistant-feishu_cards.send"), botcard.Primary, "screen_text", val(map[string]any{"typed": "1"})))
		} else {
			c.Note(i18n.T(l, "assistant-feishu_cards.secret_in_app"))
		}
		return safeCard(l, c, open)
	case proto.IxElicitation:
		return safeCard(l, s.elicitationCard(ctx, ix, open, menu(false)), open)
	}
	return safeCard(l, botcard.NewCard(where, botcard.Grey, "").Subtitle(secondary).Buttons(open), open)
}

// clipPlan is a plan longer than maxPlanRunes cut to its first channel.Split piece, then note. The piece ends at a line
// break where it can and closes a code block it cuts into, so the note reads as text, not as the code's last line.
func clipPlan(plan, note string) string {
	if len([]rune(plan)) <= maxPlanRunes {
		return plan
	}
	head := channel.Split(plan, maxPlanRunes)[0]
	if r := []rune(head); len(r) > maxPlanRunes { // Split hands back a plan of only line breaks whole
		head = string(r[:maxPlanRunes])
	}
	return head + note
}

// safeCard returns c, or, when it would still be too big for Feishu after the clipping above, a short card that sends
// the owner to the app (with the open button, if any).
func safeCard(l i18n.Lang, c *botcard.Card, open botcard.Button) *botcard.Card {
	if len(c.JSON()) <= maxCardJSONBytes {
		return c
	}
	title := ""
	if c.Header != nil {
		title = c.Header.Title.Content
	}
	fallback := botcard.NewCard(title, botcard.Orange, "")
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
var elicitationURLRe = lazyre.New(`(?i)^https?://[^/\\?#@]`)

// elicitationCard is an MCP server asking to authorize (a link) or to fill in a form: what it asks on the card, its
// link as a button; a form is filled in the app (its values may be secrets), so only its fields' names are shown.
func (s *Service) elicitationCard(ctx context.Context, ix proto.Interaction, open, menu botcard.Button) *botcard.Card {
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
	c := s.ixCard(ctx, ix, "assistant-feishu_cards.tag_elicitation",
		i18n.T(l, "assistant-feishu_cards.elicitation_summary", i18n.Args{"message": clip(oneLine(msg), 60)}))
	if msg != "" {
		c.Markdown(botcard.EscapeMarkdown(clip(msg, 2000)))
	}
	if link != "" {
		// The host on its own line: the link's text could look like another site's (as the apps show it).
		c.Markdown(i18n.T(l, "assistant-feishu_cards.elicitation_host", i18n.Args{"host": botcard.Code(host)}))
		c.Note(i18n.T(l, "assistant-feishu_cards.elicitation_confirm_in_app"))
		authorize := botcard.LinkBtn(i18n.T(l, "assistant-feishu_cards.elicitation_open"), link)
		authorize["type"] = botcard.Primary
		return c.Buttons(authorize, menu)
	}
	if len(fields) > 0 {
		c.Note(i18n.T(l, "assistant-feishu_cards.elicitation_fields", i18n.Args{"fields": clip(strings.Join(fields, "、"), 200)}))
	}
	c.Note(i18n.T(l, "assistant-feishu_cards.elicitation_in_app"))
	if open != nil {
		open["type"] = botcard.Primary
	}
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
		s = "**" + botcard.EscapeMarkdown(clip(q.Header, 60)) + "**\n"
	}
	s += botcard.EscapeMarkdown(clip(q.Question, maxQuestionRunes))
	descBudget := maxOptionDescBudget
	clippedOptions := 0
	for i, o := range q.Options {
		if i >= maxFormOptions {
			clippedOptions = len(q.Options) - i
			break
		}
		if o.Description != "" && descBudget > 0 {
			desc := clip(o.Description, maxOptionDescRunes)
			if len([]rune(desc)) > descBudget {
				desc = clip(desc, descBudget)
			}
			descBudget -= len([]rune(desc))
			s += "\n- " + botcard.EscapeMarkdown(clip(o.Label, 40)) + "：" + botcard.EscapeMarkdown(desc)
		}
	}
	if clippedOptions > 0 {
		s += i18n.T(l, "assistant-feishu_cards.clipped")
	}
	return s
}

// ixKindTags are the tags that say what an interaction's card is.
var ixKindTags = map[string]string{proto.IxQuestion: "assistant-feishu_cards.tag_question", proto.IxPlan: "assistant-feishu_cards.tag_plan",
	proto.IxPermission: "assistant-feishu_cards.tag_permission", proto.IxScreen: "assistant-feishu_cards.tag_screen",
	proto.IxElicitation: "assistant-feishu_cards.tag_elicitation"}

// ixWho is who asks, for a card's header: the task its agent works on, with the task's direction (ixTask), or else the
// agent; and under it the agent, its computer and the session's other name (feishuAgentPlaceParts), the parts it has.
func (s *Service) ixWho(ctx context.Context, ix proto.Interaction) (title, sub string) {
	agent, machine, secondary := s.feishuAgentPlaceParts(ctx, ix)
	task, same := s.ixTask(ctx, ix)
	switch {
	case task == "":
		return agent, dotJoin(machine, secondary)
	case same: // the agent is named after its task: the direction before it
		return task + agent, dotJoin(machine, secondary)
	}
	return task, dotJoin(agent, machine, secondary)
}

// dotJoin joins the parts that are not empty with " · ".
func dotJoin(parts ...string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " · ")
}

// ixCard starts the card asking ix: who asks in the header, orange (the owner is to act), its kind as a tag.
func (s *Service) ixCard(ctx context.Context, ix proto.Interaction, kind, summary string) *botcard.Card {
	title, sub := s.ixWho(ctx, ix)
	return botcard.NewCard(title, botcard.Orange, summary).Subtitle(sub).Tag(i18n.T(s.botLang(ctx), kind), botcard.Orange)
}

// answeredCard is an interaction's card once it is over: grey and small, what it asked in a line and how it ended,
// its conversation's page as a link in small text; no buttons.
func (s *Service) answeredCard(ctx context.Context, ix proto.Interaction, how string) *botcard.Card {
	l := s.botLang(ctx)
	title, sub := s.ixWho(ctx, ix)
	c := botcard.NewCard(title, botcard.Grey, how).Subtitle(sub)
	if kind := ixKindTags[ix.Kind]; kind != "" {
		c.Tag(i18n.T(l, kind), botcard.Neutral)
	}
	var asked []string
	switch ix.Kind {
	case proto.IxQuestion:
		qs := ix.Questions
		more := 0
		if len(qs) > maxAnsweredQuestions {
			more = len(qs) - maxAnsweredQuestions
			qs = qs[:maxAnsweredQuestions]
		}
		for _, q := range qs {
			asked = append(asked, botcard.EscapeMarkdown(clip(oneLine(q.Question), maxAnsweredQRunes)))
		}
		if more > 0 {
			asked = append(asked, strings.TrimSpace(i18n.T(l, "assistant-feishu_cards.clipped")))
		}
	case proto.IxPermission:
		asked = append(asked, botcard.EscapeMarkdown(ixToolName(ix)+" "+clip(oneLine(ix.Summary), 200)))
	case proto.IxPlan:
		asked = append(asked, botcard.EscapeMarkdown(clip(planTitle(ix.Plan), 200)))
	}
	if len(asked) > 0 {
		c.Markdown("<font color='grey'>" + strings.Join(asked, "\n") + "</font>")
	}
	c.Markdown("**✓ " + botcard.EscapeMarkdown(how) + "**")
	// The conversation's page rides on the card as a link, and the button on safeCard's short card, which takes its
	// place when this one would exceed Feishu's size budget.
	if label, web := s.openLink(ctx, ix.MachineID, ix.SessionID); web != "" && boardLinkOK(web) {
		c.Meta("[" + botcard.EscapeMarkdown(label) + "](" + web + ")")
	}
	return safeCard(l, c, s.openButton(ctx, ix.MachineID, ix.SessionID))
}

func (s *Service) ixWhere(ctx context.Context, ix proto.Interaction) string {
	place, _ := s.feishuAgentPlace(ctx, ix)
	return s.ixTaskPlace(ctx, ix, place)
}

func (s *Service) ixTaskPlace(ctx context.Context, ix proto.Interaction, place string) string {
	task, same := s.ixTask(ctx, ix)
	switch {
	case task == "":
		return place
	case same:
		return task + place
	}
	return task + " · " + place
}

// ixTask is the task ix's agent works on as the cards name it: its direction and title; "" when it works on none. same:
// the agent's own title is the task's, so the label is the direction alone, to go before the agent. A card closing after
// its task ended or its session moved on keeps the task it was posted for when that name is available.
func (s *Service) ixTask(ctx context.Context, ix proto.Interaction) (label string, same bool) {
	machine := s.canonical(ix.MachineID)
	t, ok := s.taskOf(ctx, machine, ix.SessionID)
	if ix.TaskTitle != "" && (!ok || t.Title != ix.TaskTitle) {
		t, ok = s.closedTaskOf(ctx, machine, ix.SessionID)
		if !ok || t.Title != ix.TaskTitle {
			return clip(oneLine(ix.TaskTitle), 70), false
		}
	}
	task := clip(oneLine(t.Title), 70)
	if !ok || task == "" {
		return "", false
	}
	direction := s.feishuDirection(ctx, t, s.feishuDirections(ctx))
	if transcript.Title(ix.SessionTitle) == t.Title {
		return direction, true
	}
	return direction + task, false
}

// jsonStrings are the string values in raw (a tool's input, its secrets already redacted), not its keys or braces.
func jsonStrings(raw string) []string {
	var v any
	if json.Unmarshal([]byte(raw), &v) != nil {
		return nil
	}
	var out []string
	var walk func(any)
	walk = func(v any) {
		switch v := v.(type) {
		case string:
			out = append(out, v)
		case []any:
			for _, e := range v {
				walk(e)
			}
		case map[string]any:
			keys := slices.Sorted(maps.Keys(v))
			for _, k := range keys {
				walk(v[k])
			}
		}
	}
	walk(v)
	return out
}

// pendingQuestion is one line saying what a pending interaction waits for; "" when it says nothing.
func pendingQuestion(ix proto.Interaction) string {
	switch ix.Kind {
	case proto.IxQuestion:
		return strings.TrimSpace(questionSummary(ix))
	case proto.IxPlan:
		return strings.TrimSpace(planTitle(ix.Plan))
	case proto.IxPermission:
		return strings.TrimSpace(ixToolName(ix) + " " + oneLine(ix.Summary))
	}
	return strings.TrimSpace(oneLine(ix.Summary))
}
