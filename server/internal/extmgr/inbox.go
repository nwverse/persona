package extmgr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nwverse/persona/server/internal/ai"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/mail"
	"github.com/nwverse/persona/server/internal/rpc"
)

// The unified inbox (docs/assistant/inbox.md) is the assistant's (internal/assistant's inbox*.go; "the manager" in the
// comments below): the items and their state, triage, events, mutes, the AI double, translations and the
// /api/v1/inbox… routes, which the hub relays to the manager process. The sources are this code's, the extension
// manager in that same process: it reads them when a round is due — its poll, a connector's changed, a pull to refresh
// the assistant asks for (inbox.collect) — and hands what it read to the assistant in-process (inbox.collected through
// callAssistant); it answers what the inbox asks of the sources and of its other parts (app.call inbox.*:
// inboxAppCall, reached through the bus connection's localAppCall, never the hub), and tells the extensions and,
// through the hub's broadcast, the apps when items changed (inbox.changed, taken here by AssistantSaid). It keeps no
// inbox state of its own: while the assistant can't take a round (there is none yet, or it refused or failed the
// hand-over) the sources are still read on schedule and what they had is kept for it (merged, bounded, in memory:
// heldThingsMost, heldMailMost), so what leaves a source's window meanwhile (mail's newest 40, iMessage's newest 30) is
// not lost; a full round with what was kept goes as soon as it takes one again.

const (
	inboxPoll     = 2 * time.Minute // fallback when no Feishu event arrives
	inboxMailPoll = 3 * time.Minute // IMAP is slow; Gmail goes through a proxy
	inboxDebounce = 3 * time.Second // a burst of Feishu events collects once
	// inboxRoundWait bounds handing a round over (the manager reads the new mail's text before it answers).
	inboxRoundWait = 5 * time.Minute
	// inboxMailParallel is how many mail accounts a full round reads at once: a slow server (up to the mail
	// package's 20 s per operation) no longer holds up the others, and the round (which holds the inbox lock the
	// Feishu chat rounds wait on) takes about the slowest account's time instead of their sum.
	inboxMailParallel = 4
)

// InboxConfig tunes the inbox (manager.json "inbox").
type InboxConfig struct {
	// Rules is extra guidance for triage, in the user's words ("anything from my manager needs me", "alerts from
	// group X are noise"…), appended to the prompt.
	Rules string `json:"rules,omitempty"`
	// TriageAI (model 1) sorts and summarizes new items; JudgeAI (model 2) links a Needs you item to
	// one of the user's tasks.
	// Empty fields fall back to manager.json's "ai"; without them both use "ai".
	TriageAI *ai.Config `json:"triage_ai,omitempty"`
	JudgeAI  *ai.Config `json:"judge_ai,omitempty"`
	// Agent (model 3) works on the Needs you items (and those a policy hands it) and drafts the reply (the manager's
	// inbox_agent.go).
	Agent InboxAgentConfig `json:"agent,omitempty"`
}

// InboxAgentConfig is manager.json's inbox.agent, handed to the manager as it is.
type InboxAgentConfig = assistantwire.InboxAgentConfig

// inboxSources is this code's part of the inbox: the wake-ups, one collection at a time, and what the manager
// reported.
type inboxSources struct {
	once    sync.Once
	kick    chan struct{}
	wide    atomic.Bool   // a kick asked for more than the Feishu chats (kickInbox)
	owed    atomic.Bool   // the manager missed a round (it was not connected): the next one is a full one
	running chan struct{} // one collection at a time (held while the manager applies it; lock, unlock)
	told    atomic.Bool   // "the manager is not connected" was logged (until a round goes through again)
	// held is what was read and not handed over (the manager was away, or did not take it), merged, under running:
	// it goes with the next round. epoch and seq stamp the rounds (InboxRound.Epoch, Seq).
	held  *assistantwire.InboxRound
	epoch string
	seq   atomic.Uint64

	tellOnce sync.Once
	changed  chan assistantwire.InboxChanged // inbox.changed waiting for tellInboxChanged

	// The events plugins wrote lately, by append id (groupAppendOnce).
	appendMu    sync.Mutex
	appended    map[string]connector.InboxThing
	appendLocks sync.Map // append id → *sync.Mutex, while it is written
}

func (b *inboxSources) init() {
	b.once.Do(func() {
		b.kick = make(chan struct{}, 1)
		b.running = make(chan struct{}, 1)
		b.epoch = strconv.FormatInt(time.Now().UnixNano(), 36)
	})
}

// inboxRound is what one collection reads.
type inboxRound int

const (
	// roundChats follows a Feishu realtime event: the Feishu chats only. Document and Moments (company feed) notices
	// and the other connectors wait for the next poll; reading them on every event was most of the hub's Lark gateway
	// calls in a busy group (#674).
	roundChats inboxRound = iota
	roundPoll             // every inboxPoll and on a connector's kick: every source but the slow ones
	roundFull             // every inboxMailPoll, when the manager connects and on a refresh: mail too
)

func (r inboxRound) wire() string {
	switch r {
	case roundChats:
		return assistantwire.RoundChats
	case roundFull:
		return assistantwire.RoundFull
	}
	return assistantwire.RoundPoll
}

// kickInbox asks for a collection of every source soon (a connector's realtime event, its settings changed).
func (h *Manager) kickInbox() {
	h.inbox.init()
	h.inbox.wide.Store(true)
	h.inbox.signal()
}

// kickLarkChats asks for a collection of the Feishu chats soon (a Feishu realtime event arrived).
func (h *Manager) kickLarkChats() {
	h.inbox.init()
	h.inbox.signal()
}

// kickedRound is the round a kick asks for: the Feishu chats, unless something else asked for every source too.
func (b *inboxSources) kickedRound() inboxRound {
	if b.wide.Swap(false) {
		return roundPoll
	}
	return roundChats
}

// lock waits for the collection before to end, or for ctx: a caller that stops waiting (a refresh the manager gave up
// on) does not stay queued behind a round that is with the manager.
func (b *inboxSources) lock(ctx context.Context) error {
	select {
	case b.running <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *inboxSources) unlock() { <-b.running }

func (b *inboxSources) signal() {
	select {
	case b.kick <- struct{}{}:
	default:
	}
}

// inboxLoop is the inbox's wake-ups: every inboxPoll, a kick (debounced), mail every inboxMailPoll. The first round is
// a full one when the manager connects (inboxManagerConnected).
func (h *Manager) inboxLoop(ctx context.Context) {
	h.inbox.init()
	lastMail := time.Now()
	t := time.NewTicker(inboxPoll)
	defer t.Stop()
	for {
		round := roundPoll
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-h.inbox.kick:
			select { // let the burst finish
			case <-ctx.Done():
				return
			case <-time.After(inboxDebounce):
			}
			round = h.inbox.kickedRound()
		}
		if h.isMember() { // the center keeps the group's inbox; this one's stays as it was (mesh_group.go)
			continue
		}
		// A round the manager missed is made up with a full one once it is back, not while it is away (mail keeps
		// its own pace then).
		if time.Since(lastMail) >= inboxMailPoll || h.inbox.owed.Load() && h.assistantUp() {
			lastMail = time.Now()
			round = roundFull
		}
		_ = h.collectInbox(ctx, round)
	}
}

// inboxManagerConnected: Run has started this code (the assistant runs beside it in this process, started on its own):
// a full round goes to the assistant now, the first since the process started, and GitHub's muted repositories kept in
// its options move to its mutes.
func (h *Manager) inboxManagerConnected() {
	h.inbox.init()
	h.inbox.owed.Store(true)
	h.inbox.wide.Store(true)
	h.inbox.signal()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	h.githubMutes(ctx)
}

// collectInbox reads the sources of one round and hands them, with what was kept from before, to the manager
// (inbox.collected), which applies them before it answers. Not connected, or not taken, what was read is kept for the
// next round (and that is a full one).
func (h *Manager) collectInbox(ctx context.Context, round inboxRound) error {
	h.inbox.init()
	if err := h.inbox.lock(ctx); err != nil {
		return err
	}
	defer h.inbox.unlock()
	r := h.readInbox(ctx, round)
	if h.inbox.held != nil {
		r = mergeRounds(*h.inbox.held, r)
	}
	if !h.assistantUp() {
		h.inbox.held = &r
		h.inbox.owed.Store(true)
		if !h.inbox.told.Swap(true) {
			h.logf("inbox: the manager is not connected: what the sources have is kept for it")
		}
		return errAssistantOffline
	}
	r.Epoch, r.Seq = h.inbox.epoch, h.inbox.seq.Add(1)
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), inboxRoundWait)
	defer cancel()
	if err := h.callAssistant(cctx, assistantwire.MethodInboxCollected, r, nil); err != nil {
		h.inbox.held = &r
		h.inbox.owed.Store(true) // read again, whole, when it is back
		h.logf("inbox: hand the round to the manager: %v", err)
		return err
	}
	h.inbox.held = nil
	h.inbox.told.Store(false)
	if r.Round == assistantwire.RoundFull { // what was kept went with it
		h.inbox.owed.Store(false)
	}
	return nil
}

// What is kept for the manager while it is away, at most: things of one source, messages of one mail account (the
// newest).
const (
	heldThingsMost = 2000
	heldMailMost   = 500
)

// mergeRounds is old with the newer round n over it: what n read replaces what old had of the same thing (a thread,
// an event, a batch, a mail), what only old has stays (it left the source's window meanwhile) within heldThingsMost
// and heldMailMost; a source n read as off, or one this code no longer has, keeps nothing.
func mergeRounds(old, n assistantwire.InboxRound) assistantwire.InboxRound {
	out := n
	if old.Round == assistantwire.RoundFull || n.Round == assistantwire.RoundFull {
		out.Round = assistantwire.RoundFull
	} else if old.Round == assistantwire.RoundPoll {
		out.Round = assistantwire.RoundPoll
	}
	out.Sources = slices.Clone(n.Sources)
	at := map[string]int{}
	for i, s := range out.Sources {
		at[s.ID] = i
	}
	for _, o := range old.Sources {
		i, ok := at[o.ID]
		switch {
		case !ok:
			if _, have := n.Names[o.ID]; have {
				out.Sources = append(out.Sources, o) // not read this time: what it had
			}
		case out.Sources[i].ReadError != "":
			o.ReadError = out.Sources[i].ReadError // not read this time: what it had, and that it failed
			out.Sources[i] = o
		case !out.Sources[i].Off:
			out.Sources[i].Things = mergeHeld(o.Things, out.Sources[i].Things, heldThingsMost, func(t connector.InboxThing) string {
				key := t.Thread + "\x00" + t.ID
				if t.Group != nil {
					key += "\x00" + t.Group.Batch
				}
				return key
			})
		}
	}
	switch {
	case n.Mail == nil:
		out.Mail = old.Mail
	case old.Mail != nil:
		m := &assistantwire.InboxMailRead{Accounts: slices.Clone(n.Mail.Accounts)}
		got := map[string]int{}
		for i, a := range m.Accounts {
			got[a.ID] = i
		}
		for _, a := range old.Mail.Accounts {
			i, ok := got[a.ID]
			if !ok {
				m.Accounts = append(m.Accounts, a) // not read this time
				continue
			}
			m.Accounts[i].Messages = mergeHeld(a.Messages, m.Accounts[i].Messages, heldMailMost, func(s assistantwire.InboxMailSummary) string {
				return s.Mailbox + "\x00" + strconv.FormatUint(uint64(s.UID), 10)
			})
		}
		out.Mail = m
	}
	return out
}

// mergeHeld is what only old has, then n, the newest most of them.
func mergeHeld[T any](old, n []T, most int, key func(T) string) []T {
	have := map[string]bool{}
	for _, v := range n {
		have[key(v)] = true
	}
	out := make([]T, 0, len(old)+len(n))
	for _, v := range old {
		if !have[key(v)] {
			out = append(out, v)
		}
	}
	out = append(out, n...)
	if len(out) > most {
		out = out[len(out)-most:]
	}
	return out
}

// readInbox reads one round's sources: mail on a full round, the connectors but on a round of Feishu chats.
func (h *Manager) readInbox(ctx context.Context, round inboxRound) assistantwire.InboxRound {
	r := assistantwire.InboxRound{Round: round.wire(), Sources: []assistantwire.InboxSource{}, Names: map[string]string{}}
	if h.connectors == nil {
		return r
	}
	for _, c := range h.connectors.All() {
		r.Names[c.ID()] = c.Name()
	}
	if round == roundFull {
		r.Mail = h.readMail(ctx)
	}
	if round != roundChats {
		r.Sources = h.readConnectors(ctx)
	}
	return r
}

// readMail is each mail account's latest inbox page, read up to inboxMailParallel accounts at once and kept in the
// accounts' order (an account that can't be read is left out); nil without mail or when the accounts can't be listed.
func (h *Manager) readMail(ctx context.Context) *assistantwire.InboxMailRead {
	if h.mail == nil || h.connectors.Get("mail") == nil {
		return nil
	}
	accounts, err := h.mail.ListAccounts()
	if err != nil {
		h.logf("inbox: mail accounts: %v", err)
		return nil
	}
	read := make([]*assistantwire.InboxMailAccount, len(accounts))
	slots := make(chan struct{}, inboxMailParallel)
	var wg sync.WaitGroup
	for i, a := range accounts {
		slots <- struct{}{}
		wg.Go(func() {
			defer func() { <-slots }()
			read[i] = h.readMailAccount(ctx, a)
		})
	}
	wg.Wait()
	out := &assistantwire.InboxMailRead{Accounts: []assistantwire.InboxMailAccount{}}
	for _, acc := range read {
		if acc != nil {
			out.Accounts = append(out.Accounts, *acc)
		}
	}
	return out
}

// readMailAccount is one account's latest inbox page; nil when it can't be read.
func (h *Manager) readMailAccount(ctx context.Context, a mail.MailAccount) *assistantwire.InboxMailAccount {
	res, err := h.mail.ListMessages(ctx, mail.ListOptions{AccountID: a.ID, Limit: 40})
	if err != nil {
		h.logf("inbox: mail %s: %v", a.Email, err)
		return nil
	}
	acc := &assistantwire.InboxMailAccount{ID: a.ID, Email: a.Email, Messages: []assistantwire.InboxMailSummary{}}
	for _, m := range res.Messages {
		acc.Messages = append(acc.Messages, assistantwire.InboxMailSummary{Mailbox: m.Mailbox, UID: m.UID, MessageID: m.MessageID,
			Subject: m.Subject, From: mailAddress(m.From), To: mailAddresses(m.To), Date: m.Date, Snippet: m.Snippet, Seen: m.Seen})
	}
	return acc
}

func mailAddress(a mail.Address) assistantwire.MailAddress {
	return assistantwire.MailAddress{Name: a.Name, Email: a.Email}
}

func mailAddresses(list []mail.Address) []assistantwire.MailAddress {
	out := []assistantwire.MailAddress{}
	for _, a := range list {
		out = append(out, mailAddress(a))
	}
	return out
}

// readConnectors reads every connector with an inbox: its things and what it can do with them. One the user turned "Add
// to Inbox" off for, not signed in, or a hub.json plugin's waiting for the triage model is read with nothing (nothing
// of it waits); one whose Collect failed is left out (it keeps what it had).
func (h *Manager) readConnectors(ctx context.Context) []assistantwire.InboxSource {
	out := []assistantwire.InboxSource{}
	for _, c := range h.connectors.All() {
		in, ok := connector.As[connector.Inbox](c)
		if !ok {
			continue
		}
		src := assistantwire.InboxSource{ID: c.ID(), Name: c.Name(), Extension: fromExtension(c.ID()), Migrated: isMigrated(c.ID()),
			Things: []connector.InboxThing{}}
		state := connector.StatusOf(ctx, c).State
		if !h.connectors.Feeds(c.ID(), connector.FeedInbox) || state != connector.StateOK && state != connector.StateRateLimited ||
			h.waitsForTriageModel(ctx, c) { // a hub.json plugin's that needs the triage model (extension_inbox.go)
			src.Off = true
			out = append(out, src)
			continue
		}
		things, err := in.Collect(ctx)
		if err != nil {
			h.logf("inbox: %s: %v", c.ID(), err)
			src.ReadError = readErrorOf(err) // the manager keeps what it had and the apps say it is behind
			out = append(out, src)
			continue
		}
		h.vetThings(c, things)
		src.Things = things
		_, src.Group = connector.As[connector.Grouper](c)
		_, src.Meta = connector.As[connector.EventWriter](c)
		src.MetaDescription = strings.TrimSpace(c.Manifest().MetaDescription)
		_, src.Settles = connector.As[connector.InboxSettler](c)
		for _, kind := range c.Manifest().Kinds {
			if d, ok := extensionKinds.Load(c.ID() + "/" + kind); ok {
				if src.Kinds == nil {
					src.Kinds = map[string]string{}
				}
				src.Kinds[kind] = d.(string)
			}
		}
		out = append(out, src)
	}
	return out
}

// readErrorOf is a failed Collect as a short code: its text may hold an address or a token, the code does not.
func readErrorOf(err error) string {
	var re *rpc.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return assistantwire.ReadTimeout
	case errors.As(err, &re) && re.Code == "rate_limited":
		return assistantwire.ReadRateLimited
	}
	return assistantwire.ReadFailed
}

func isMigrated(id string) bool { return slices.Contains(connector.MigratableConnectors, id) }

// thingRulesMost is the most of a thing's rules the model gets (runes; the rest is cut): what the convention's
// triage_rules setting allows (docs/extensions/extensions.md "Agreed settings").
const thingRulesMost = 4000

// vetThings holds a connector's things to its manifest: a tier the inbox does not know is dropped (the model
// decides), a kind it did not declare is logged once (prompts/inbox/kinds/<id>.md may not say what it means); rules
// are cut to thingRulesMost.
func (h *Manager) vetThings(c connector.Connector, things []connector.InboxThing) {
	kinds := c.Manifest().Kinds
	for i := range things {
		t := &things[i]
		t.Rules = clip(strings.TrimSpace(t.Rules), thingRulesMost)
		if t.Tier != "" && !t.Tier.Known() {
			h.logf("inbox: %s: unknown tier %q", c.ID(), t.Tier)
			t.Tier = ""
		}
		if t.Kind != "" && !slices.Contains(kinds, t.Kind) {
			if _, logged := undeclaredKinds.LoadOrStore(c.ID()+"\x00"+t.Kind, true); !logged {
				h.logf("inbox: %s: kind %q is not in its manifest", c.ID(), t.Kind)
			}
		}
	}
}

var undeclaredKinds sync.Map

// ---- the routes: the manager's ----

// ---- what the manager tells this code (in-process, not the hub) ----

func init() { onAssistantEvent(assistantwire.EventInboxChanged, (*Manager).busInboxChanged) }

// busInboxChanged is inbox.changed: the extensions hear of the items (inbox.changed), the apps get {"type":"inbox"}.
// It is told by one worker (tellInboxChanged), off the bus's read loop: an extension slow to take its event (its pipe
// full) must not hold up the manager's other calls and events. A backlog past inboxChangedQueue is not told to the
// extensions; the apps still hear of it.
func (h *Manager) busInboxChanged(params json.RawMessage) {
	var p assistantwire.InboxChanged
	if err := json.Unmarshal(params, &p); err != nil {
		h.logf("inbox.changed: %v", err)
		return
	}
	h.inbox.tellOnce.Do(func() {
		h.inbox.changed = make(chan assistantwire.InboxChanged, inboxChangedQueue)
		go h.tellInboxChanged()
	})
	select {
	case h.inbox.changed <- p:
	default:
		h.logf("inbox: the extensions are behind: %d changed items not told to them", len(p.Items))
		h.broadcast(map[string]any{"type": "inbox"})
	}
}

// inboxChangedQueue is how many inbox.changed wait for the worker at most.
const inboxChangedQueue = 256

// tellInboxChanged tells the extensions of the changed items (inbox.changed) and the apps {"type":"inbox"}.
func (h *Manager) tellInboxChanged() {
	for p := range h.inbox.changed {
		for _, it := range p.Items {
			h.extensions.emit("inbox.changed", map[string]any{"id": it.ID, "source": it.Source, "kind": it.Kind, "title": it.Title})
		}
		h.broadcast(map[string]any{"type": "inbox"})
	}
}

// ---- what this code asks the manager about the inbox ----

// inboxItemRaw is an item as the manager stores it (an extension's inbox_item slot); false when there is none, or
// the manager can't say.
func (h *Manager) inboxItemRaw(ctx context.Context, id string) ([]byte, bool) {
	var res assistantwire.InboxItemResult
	if err := h.callAssistant(ctx, assistantwire.MethodInboxItem, map[string]any{"id": id}, &res); err != nil || !res.OK {
		return nil, false
	}
	return res.Item, true
}

// forgetInbox removes a source's inbox items (the manager's); it says how many went.
func (h *Manager) forgetInbox(ctx context.Context, source string) (int, error) {
	var res struct {
		Deleted int `json:"deleted"`
	}
	err := h.callAssistant(ctx, assistantwire.MethodInboxForget, map[string]any{"source": source}, &res)
	return res.Deleted, err
}

// ---- what the manager asks of the inbox's sources (app.call inbox.*) ----

var errCannotReply = i18n.ErrEnglish("inbox-errors.cannot_reply")

// inboxAppCall answers the manager's calls about the inbox's sources; ok false for one that is not theirs.
func (h *Manager) inboxAppCall(ctx context.Context, p assistantwire.AppCall) (json.RawMessage, bool, error) {
	var out any
	var err error
	switch p.AppID + " " + p.Method {
	case assistantwire.AppInbox + " " + assistantwire.InboxCollect:
		var a struct {
			Round string `json:"round"`
		}
		if err = json.Unmarshal(p.Params, &a); err == nil {
			round := roundFull
			switch a.Round {
			case assistantwire.RoundPoll:
				round = roundPoll
			case assistantwire.RoundChats:
				round = roundChats
			}
			err = h.collectInbox(ctx, round)
			out = map[string]any{}
		}
	case assistantwire.AppInbox + " " + assistantwire.InboxKick:
		h.kickInbox()
		out = map[string]any{}
	case assistantwire.AppInbox + " " + assistantwire.InboxConfigMethod:
		out = h.inboxConfigView(ctx)
	case assistantwire.AppInbox + " " + assistantwire.InboxMailBody:
		var ref assistantwire.InboxMailRef
		if err = json.Unmarshal(p.Params, &ref); err == nil {
			out, err = h.inboxMailBody(ctx, ref)
		}
	case assistantwire.AppInbox + " " + assistantwire.InboxReplyMail:
		var a struct {
			assistantwire.InboxMailRef
			Text   string `json:"text"`
			SendID string `json:"send_id"`
		}
		if err = json.Unmarshal(p.Params, &a); err == nil {
			var sent assistantwire.InboxSent
			sent.Already, err = h.sendInboxOnce(ctx, a.SendID, "mail", func(ctx context.Context) error { return h.replyMail(ctx, a.InboxMailRef, a.Text) })
			out = sent
		}
	case assistantwire.AppInbox + " " + assistantwire.InboxReply:
		var a struct {
			Source string               `json:"source"`
			Reply  connector.InboxReply `json:"reply"`
			SendID string               `json:"send_id"`
		}
		if err = json.Unmarshal(p.Params, &a); err == nil {
			in, ok := connector.As[connector.Inbox](h.connectorOf(a.Source))
			if !ok {
				return nil, true, errCannotReply // its connector is not here (the plugin switched off)
			}
			var sent assistantwire.InboxSent
			sent.Already, err = h.sendInboxOnce(ctx, a.SendID, a.Source, func(ctx context.Context) error { return in.Reply(ctx, a.Reply) })
			out = sent
		}
	case assistantwire.AppInbox + " " + assistantwire.InboxSettle:
		var a struct {
			Source  string   `json:"source"`
			Threads []string `json:"threads"`
		}
		if err = json.Unmarshal(p.Params, &a); err == nil {
			over := map[string]string{}
			if s, ok := connector.As[connector.InboxSettler](h.connectorOf(a.Source)); ok {
				over = s.Settle(ctx, a.Threads)
			}
			out = map[string]any{"over": over}
		}
	case assistantwire.AppInbox + " " + assistantwire.InboxGroupTool:
		var a struct {
			Source string                  `json:"source"`
			Call   connector.GroupToolCall `json:"call"`
		}
		if err = json.Unmarshal(p.Params, &a); err == nil {
			g, ok := connector.As[connector.Grouper](h.connectorOf(a.Source))
			if !ok {
				return nil, true, fmt.Errorf("%s does not relate batches", a.Source)
			}
			var raw json.RawMessage
			if raw, err = g.GroupTool(ctx, a.Call); err == nil {
				out = map[string]any{"result": compactJSON(raw, groupToolMost)}
			}
		}
	case assistantwire.AppInbox + " " + assistantwire.InboxGroupFallback:
		var a struct {
			Source   string                  `json:"source"`
			Fallback connector.GroupFallback `json:"fallback"`
		}
		if err = json.Unmarshal(p.Params, &a); err == nil {
			g, ok := connector.As[connector.Grouper](h.connectorOf(a.Source))
			if !ok {
				return nil, true, fmt.Errorf("%s does not relate batches", a.Source)
			}
			out, err = g.GroupFallback(ctx, a.Fallback)
		}
	case assistantwire.AppInbox + " " + assistantwire.InboxGroupAppend:
		var a struct {
			Source   string                `json:"source"`
			Append   connector.GroupAppend `json:"append"`
			AppendID string                `json:"append_id"`
		}
		if err = json.Unmarshal(p.Params, &a); err == nil {
			w, ok := connector.As[connector.EventWriter](h.connectorOf(a.Source))
			if !ok {
				return nil, true, fmt.Errorf("%s does not write events", a.Source)
			}
			if string(a.Append.Meta) == "null" { // a new event: no meta yet
				a.Append.Meta = nil
			}
			var thing connector.InboxThing
			if thing, err = h.groupAppendOnce(ctx, w, a.AppendID, a.Append); err == nil {
				out = map[string]any{"thing": thing}
			}
		}
	default:
		return nil, false, nil
	}
	if err != nil {
		return nil, true, err
	}
	raw, err := json.Marshal(out)
	return raw, true, err
}

// groupAppendWait bounds one group.append at the plugin. It runs to the end however long the manager waits: what it
// wrote is kept for the manager's retry (groupAppendOnce).
const groupAppendWait = time.Minute

// groupAppendsKept is how many plugins' answers to group.append are kept for a retry at most.
const groupAppendsKept = 1000

// groupAppendOnce has the plugin write one event once per append id: the same id again (the answer was lost on the
// way to the manager, or it stopped waiting) is answered with what the plugin said, not written twice. Without an id
// it is just written.
func (h *Manager) groupAppendOnce(ctx context.Context, w connector.EventWriter, appendID string, a connector.GroupAppend) (connector.InboxThing, error) {
	if appendID == "" {
		return w.GroupAppend(ctx, a)
	}
	b := &h.inbox
	l, _ := b.appendLocks.LoadOrStore(appendID, &sync.Mutex{})
	mu := l.(*sync.Mutex)
	defer b.appendLocks.Delete(appendID) // after the unlock: one who comes later finds the answer kept
	mu.Lock()
	defer mu.Unlock()
	b.appendMu.Lock()
	thing, ok := b.appended[appendID]
	b.appendMu.Unlock()
	if ok {
		return thing, nil
	}
	actx, cancel := context.WithTimeout(context.WithoutCancel(ctx), groupAppendWait)
	defer cancel()
	thing, err := w.GroupAppend(actx, a)
	if err != nil {
		return thing, err
	}
	b.appendMu.Lock()
	if b.appended == nil || len(b.appended) >= groupAppendsKept {
		b.appended = map[string]connector.InboxThing{} // crude, but retries come within minutes
	}
	b.appended[appendID] = thing
	b.appendMu.Unlock()
	return thing, nil
}

// groupToolMost is the bytes of a connector tool's answer the model reads.
const groupToolMost = 8 << 10

// connectorOf is the connector registered as id; nil when there is none.
func (h *Manager) connectorOf(id string) connector.Connector {
	if h.connectors == nil || id == "" {
		return nil
	}
	return h.connectors.Get(id)
}

// inboxConfigView is what the inbox goes by of this code's: the triage rules and the AI twin's (settings over
// manager.json), manager.json's inbox.agent, the default Feishu account (the Feishu extension's, inboxLarkAccount), the
// plugins' connectors, and the languages the devices want mail translated into.
func (h *Manager) inboxConfigView(ctx context.Context) assistantwire.InboxConfig {
	out := assistantwire.InboxConfig{Rules: h.inboxRules(ctx), AgentRules: h.inboxAgentRules(ctx)}
	extensionConnectors.Range(func(id, _ any) bool { // the plugins' connectors, before a round lists them
		out.Extensions = append(out.Extensions, id.(string))
		return true
	})
	slices.Sort(out.Extensions)
	if h.cfg != nil {
		out.Agent = h.cfg.Inbox.Agent
	}
	out.LarkAccount = h.inboxLarkAccount(ctx)
	if h.mail != nil {
		out.Translate = h.mailTargets(ctx)
	}
	return out
}

// inboxMailBody is one mail read in full, peeking (it stays unread): its text part, else its HTML as plain text
// (readable text, paragraphs and links kept, when the ref asks: translation).
func (h *Manager) inboxMailBody(ctx context.Context, ref assistantwire.InboxMailRef) (assistantwire.InboxMailMessage, error) {
	if h.mail == nil {
		return assistantwire.InboxMailMessage{}, i18n.ErrEnglish("inbox-errors.mail_unconfigured")
	}
	m, err := h.mail.GetMessage(ctx, ref.AccountID, ref.UID, ref.Mailbox)
	if err != nil {
		return assistantwire.InboxMailMessage{}, err
	}
	text := strings.TrimSpace(m.Text)
	switch {
	case text != "":
	case ref.Readable:
		text = strings.TrimSpace(mail.ReadableText(m.HTML))
	default:
		text = strings.TrimSpace(mail.PlainText(m.HTML))
	}
	return assistantwire.InboxMailMessage{From: mailAddress(m.From), To: mailAddresses(m.To), Cc: mailAddresses(m.Cc), Date: m.Date,
		Subject: m.Subject, Text: text}, nil
}

// inboxSendWait bounds one reply's send.
const inboxSendWait = 2 * time.Minute

// errReplyInDoubt: a send under this id was cut off before (the process stopped or a link on the way dropped during
// it, or it ran out of time): it may have gone out, so it is not sent again.
var errReplyInDoubt = &rpc.Error{Code: assistantwire.CodeAssistantInDoubt, Message: "这条回复上次发送时中断了，可能已经发出去了：先看看再决定要不要重发"}

// sendInboxOnce sends one of the manager's replies at most once per send id, with the guard the apps' messages have
// (sent_messages, kept for good): the same id after it went out is already sent (nothing goes again), after a send that was
// cut off it is in doubt (errReplyInDoubt). A send that failed is forgotten: asked again, it is sent. A send once
// begun runs to its end (inboxSendWait) even when the manager stops waiting or drops: what it did is recorded for the
// retry. Without an id or the database it is just sent.
func (h *Manager) sendInboxOnce(ctx context.Context, sendID, source string, send func(context.Context) error) (already bool, err error) {
	st, _ := h.store.(sendStore)
	if sendID == "" || st == nil {
		return false, send(ctx)
	}
	id := "inbox:" + sendID
	state, err := st.ClaimSend(ctx, id, "inbox", source)
	switch {
	case err != nil:
		return false, err
	case state == sendDelivered:
		return true, nil
	case state != "":
		return false, errReplyInDoubt
	}
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), inboxSendWait)
	defer cancel()
	err = send(sctx)
	outcome := inboxSendOutcome(err)
	if serr := st.SettleSend(context.WithoutCancel(ctx), id, outcome); serr != nil {
		h.logf("inbox: settle reply %s: %v", sendID, serr)
	}
	if outcome == sendUnknown {
		return false, &rpc.Error{Code: assistantwire.CodeAssistantInDoubt, Message: err.Error()}
	}
	return false, err
}

// inboxSendOutcome is what a reply's error says of it (sent_messages' state): cut off (the time ran out, a link on the
// way dropped) it may have gone out; refused it did not ("": forgotten).
func inboxSendOutcome(err error) string {
	switch {
	case err == nil:
		return sendDelivered
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled), errors.Is(err, rpc.ErrDropped):
		return sendUnknown
	}
	return ""
}

// replyMail answers a mail from the account that received it.
func (h *Manager) replyMail(ctx context.Context, ref assistantwire.InboxMailRef, text string) error {
	if h.mail == nil {
		return errCannotReply
	}
	orig, err := h.mail.GetMessage(ctx, ref.AccountID, ref.UID, ref.Mailbox)
	if err != nil {
		return err
	}
	return h.mail.Send(ctx, mailReply(h.userLang(), ref.AccountID, orig, text))
}

// mailReply is a reply to orig: Re: subject, threading headers, the original quoted below.
func mailReply(lang i18n.Lang, accountID string, orig *mail.MailMessage, text string) mail.SendRequest {
	subject := orig.Subject
	if !strings.HasPrefix(strings.ToLower(subject), "re:") {
		subject = "Re: " + subject
	}
	refs := append(append([]string{}, orig.References...), orig.MessageID)
	var quoted []string
	for i, l := range strings.Split(orig.Text, "\n") {
		if i >= 40 {
			break
		}
		quoted = append(quoted, "> "+l)
	}
	from := orig.From.Name
	if from == "" {
		from = orig.From.Email
	}
	body := text + "\n\n" + i18n.T(lang, "hub-inbox.reply_attribution", i18n.Args{"date": orig.Date, "from": from}) + "\n" + strings.Join(quoted, "\n")
	return mail.SendRequest{AccountID: accountID, To: []mail.Address{orig.From}, Subject: subject, Text: body,
		InReplyTo: orig.MessageID, References: mail.MessageIDs(strings.Join(refs, " "))}
}

// ---- GitHub's muted repositories ----
//
// GitHub's settings page (and older apps) keep muted repositories in its options ("muted_repos"). They are the
// inbox's mutes of source github, tag repo (the manager's): listed in its options from there, set through them into
// there, and the ones kept in its options before are moved over once (githubMutes, when the manager connects).

const githubMutedRepos = "muted_repos"

// githubMutes moves the repositories GitHub's options still mute into the inbox's mutes and empties them there.
func (h *Manager) githubMutes(ctx context.Context) {
	o, ok := connector.As[connector.Options](h.connectorOf("github"))
	if !ok {
		return
	}
	var opts map[string]json.RawMessage
	var repos []string
	if json.Unmarshal(o.Options(), &opts) != nil || json.Unmarshal(opts[githubMutedRepos], &repos) != nil || len(repos) == 0 {
		return
	}
	if err := h.callAssistant(ctx, assistantwire.MethodInboxRepoMutes, assistantwire.InboxRepoMutes{Source: "github", Set: true, Add: true,
		Repos: repos}, nil); err != nil {
		h.logf("inbox: github's muted repositories: %v", err)
		return
	}
	opts[githubMutedRepos] = json.RawMessage(`[]`)
	raw, _ := json.Marshal(opts)
	if err := h.applyConnectorOptions(ctx, "github", raw); err != nil {
		h.logf("inbox: github's muted repositories moved, its options kept them: %v", err)
	}
}

// withGitHubMutes is GitHub's options as listed: the muted repositories are the inbox's mutes. While the manager
// can't say there are none (nil: the apps draw no inbox section for GitHub): options without the mutes would show
// nothing muted, and saving them (another setting changed) would unmute every repository.
func (h *Manager) withGitHubMutes(ctx context.Context, raw json.RawMessage) json.RawMessage {
	var opts map[string]json.RawMessage
	if json.Unmarshal(raw, &opts) != nil {
		return raw
	}
	var res struct {
		Repos []string `json:"repos"`
	}
	if err := h.callAssistant(ctx, assistantwire.MethodInboxRepoMutes, assistantwire.InboxRepoMutes{Source: "github"}, &res); err != nil {
		h.logf("inbox: github's muted repositories: %v", err)
		return nil
	}
	if res.Repos == nil {
		res.Repos = []string{}
	}
	opts[githubMutedRepos], _ = json.Marshal(res.Repos)
	out, _ := json.Marshal(opts)
	return out
}

// setGitHubMutes takes the muted repositories out of options GitHub's page sets: they become the inbox's mutes (the
// ones not listed any more are unmuted). It returns the options without them. Options that do not list them (no
// muted_repos, or null) leave the mutes as they are, the inbox's and those still in GitHub's options: only a list,
// even an empty one, sets them.
func (h *Manager) setGitHubMutes(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var opts map[string]json.RawMessage
	if json.Unmarshal(raw, &opts) != nil {
		return raw, nil // the connector says what is wrong
	}
	field, ok := opts[githubMutedRepos]
	var repos []string
	if !ok || json.Unmarshal(field, &repos) != nil || repos == nil {
		delete(opts, githubMutedRepos)
		if o, ok := connector.As[connector.Options](h.connectorOf("github")); ok {
			var cur map[string]json.RawMessage
			if json.Unmarshal(o.Options(), &cur) == nil && cur[githubMutedRepos] != nil {
				opts[githubMutedRepos] = cur[githubMutedRepos] // not moved to the inbox yet
			}
		}
		out, _ := json.Marshal(opts)
		return out, nil
	}
	if err := h.callAssistant(ctx, assistantwire.MethodInboxRepoMutes, assistantwire.InboxRepoMutes{Source: "github", Set: true,
		Repos: repos}, nil); err != nil {
		return nil, err
	}
	opts[githubMutedRepos] = json.RawMessage(`[]`)
	out, _ := json.Marshal(opts)
	return out, nil
}
