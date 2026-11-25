package lark

// Cards for what Persona and the agents ask (the bot's cards switch): a question, a plan, a permission or a terminal
// dialog shows in the chat as a card with the same choices as the app's card; answering it in either place answers it
// (interactions.resolve), and the card in Feishu then says how it was answered (interaction.ended). Cards go out for
// what the chat drives (its target, and the conversations of its runs), or for everything with all_cards. The card
// buttons and the chat's commands are the lark module's (card_actions.go, commands.go); the pushes and sync mirror are
// in feishu_push.go.

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


// closeEndedBotCards closes the bot's cards whose interaction is over. Not being in pending is not enough: right
// after a restart the manager's table is empty or partial (interactions are restored, screen and Codex cards rebuilt,
// after this runs), so each is closed only once the history says it ended for good (botCardOver). One that may still
// be waiting is looked at again a little later, once (again).
func (s *Service) closeEndedBotCards(ctx context.Context, pending map[string]bool, again bool) {
	var maybe []botCard
	s.larkModule().Book(ctx, func(b assistantwire.ChatBook) { // cards posted before a restart are in the chat's state
		for id, card := range b.Cards() {
			if !pending[id] && card.MessageID != "" {
				maybe = append(maybe, card)
			}
		}
	})
	var unsure []botCard
	for _, card := range maybe {
		switch s.botCardOver(ctx, card.Ix.ID) {
		case cardOver:
			s.feishuInteractionDone(card.Ix, i18n.T(s.botLang(ctx), "assistant-channels.ended"))
		case cardUnsure:
			unsure = append(unsure, card)
		}
	}
	if again && len(unsure) > 0 {
		time.AfterFunc(botCardRecheck, func() {
			cctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			s.closeEndedBotCards(cctx, nil, false)
		})
	}
}

// detachedCards are the Feishu cards closed because their node's connection ended (feishuDetach), by interaction id,
// kept for resumeGrace in case the same card comes back (feishuCardBack). The chat's lock is taken inside mu, never
// the other way round.
type detachedCards struct {
	mu    sync.Mutex
	cards map[string]detachedCard
}
