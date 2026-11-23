package assistant

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// The card ledger: rule 5 of the four-layer card design (docs/designs/cards-four-layers-20261123.md). Every card or
// push the manager sends to the owner is recorded with its layer, its source and its owner, counted per day of the
// owner's time zone, and the briefing says in one line how many reached the owner and how many of those were not
// layer 4. The ledger only records: which cards are pushed is decided elsewhere.
//
// The layers:
//
//	1 should not exist at all
//	2 the supervisor (a direction's lead) handles it
//	3 the main conversation handles it
//	4 only the owner can handle it
//
// A card's layer is fixed in code by its source (cardLayers), never by a model: the source is a constant named where
// the card is made (withCard on the push's ctx, deliverCard, or the kind of an Agent's card, inputCardSource).

const (
	cardLayerUnclassified = 0 // a source the table does not know: counted as not layer 4
	cardLayerNone         = 1
	cardLayerLead         = 2
	cardLayerMain         = 3
	cardLayerOwner        = 4
)

// The sources: one constant per code point that pushes to the owner.
const (
	// Layer 4, Appendix B (a): the owner must act in person.
	cardSecretForm      = "secret_form"       // the manager's own secret or form card (secret_run, secret_store, secret_handoff)
	cardAgentSecretForm = "agent_secret_form" // an Agent's card asking for a password, fields or an MCP form (elicitation)
	// Layer 4, Appendix B (b): irreversible or seen outside.
	cardConfirmOutbound = "confirm_outbound" // health.go confirmOutbound: words leaving the conversation
	// Layer 4, Appendix B (c): a supervisor's task_review needs_user, and the decision it raises.
	cardLeaderNeedsUser = "leader_needs_user" // supervise_events.go: a task waits on the owner (needs_user)
	cardDecision        = "decision"          // todo_ask.go pushDecision: a to-do's decision with a recommendation
	// Appendix B (d), acceptance the owner must see (heldDelivery unblock=user, user_check), has no push of its own
	// today: it reaches the owner through leader_needs_user or decision.

	// Layer 4, proposed addition to Appendix B (e): what the owner set up or asked for themself.
	cardTodoRemind    = "todo_remind"    // todo_plan.go: a reminder the owner set
	cardHealthWater   = "health_water"   // health_water.go: the water reminders the owner switched on
	cardMinutesReady  = "minutes_ready"  // kb_minutes.go: a recording's minutes
	cardDayRecap      = "day_recap"      // kb_day.go: the day's recap
	cardBriefFallback = "brief_fallback" // briefing_fallback.go: the briefing itself, when its run failed
	cardAgentDone     = "agent_done"     // fleet_push.go: a turn finished (the devices' "done" pushes setting)

	// Not on the whitelist: layers 1-3.
	cardPlanWaiting        = "plan_waiting"         // tasks_plan.go: a plan waits for Start (LEAD-COMMON: layer 1)
	cardAgentPlan          = "agent_plan"           // an Agent's plan approval card
	cardTaskFailed         = "task_failed"          // supervise_events.go notifyTaskFailed
	cardAgentPermission    = "agent_permission"     // an Agent's tool approval
	cardAgentQuestion      = "agent_question"       // an Agent's question or screen dialog
	cardAgentReply         = "agent_reply"          // agents_reply.go: an Agent still waits for an answer
	cardRelayRefusal       = "relay_refusal"        // relay_refusal.go
	cardAskEscalated       = "ask_escalated"        // ask_route.go: an Agent's card rung up the ladder to the owner
	cardAskHeld            = "ask_held"             // ask_route.go quietRemind
	cardAskUser            = "ask_user"             // a conversation's ask_user question
	cardManagerQuestion    = "manager_question"     // another question card of the manager's own tools
	cardToolPermission     = "tool_permission"      // an approval card of the manager's own tools
	cardNotifyUser         = "notify_user"          // tools.go: notify_user
	cardUrgentCall         = "urgent_call"          // urgent_call_tool.go
	cardDecisionDeadline   = "decision_deadline"    // decision_deadline.go: an ask_user decision past its deadline
	cardScheduleFailed     = "schedule_failed"      // schedule.go: a scheduled run failed
	cardScheduleSkipped    = "schedule_skipped"     // schedule.go tellWaitingSkip
	cardScheduleStreak     = "schedule_streak"      // schedule_streak.go
	cardScheduleLimitRetry = "schedule_limit_retry" // schedule_limit_retry.go
	cardMemoryMerge        = "memory_merge"         // memory_merge.go
	cardCaptureSorted      = "capture_sorted"       // capture_sort.go
	cardKBHistory          = "kb_history"           // kbhistory_service.go

	// cardUntagged is a push no source was named for: layer 0, unclassified.
	cardUntagged = "untagged"
)

// cardLayers is each source's layer. Layer 4 is only the whitelist of Appendix B of
// docs/designs/cards-four-layers-20261123.md ((a) to (d)) and the owner's own subscriptions ((e), proposed there);
// a source added to it is added to that table too, and the platform lead reviews it. A source missing here is layer 0.
var cardLayers = map[string]int{
	cardSecretForm:      cardLayerOwner,
	cardAgentSecretForm: cardLayerOwner,
	cardConfirmOutbound: cardLayerOwner,
	cardLeaderNeedsUser: cardLayerOwner,
	cardDecision:        cardLayerOwner,
	cardTodoRemind:      cardLayerOwner,
	cardHealthWater:     cardLayerOwner,
	cardMinutesReady:    cardLayerOwner,
	cardDayRecap:        cardLayerOwner,
	cardBriefFallback:   cardLayerOwner,
	cardAgentDone:       cardLayerOwner,

	cardPlanWaiting: cardLayerNone,
	cardAgentPlan:   cardLayerNone,

	cardTaskFailed:      cardLayerLead,
	cardAgentPermission: cardLayerLead,
	cardAgentQuestion:   cardLayerLead,
	cardAgentReply:      cardLayerLead,
	cardRelayRefusal:    cardLayerLead,

	cardAskEscalated:       cardLayerMain,
	cardAskHeld:            cardLayerMain,
	cardAskUser:            cardLayerMain,
	cardManagerQuestion:    cardLayerMain,
	cardToolPermission:     cardLayerMain,
	cardNotifyUser:         cardLayerMain,
	cardUrgentCall:         cardLayerMain,
	cardDecisionDeadline:   cardLayerMain,
	cardScheduleFailed:     cardLayerMain,
	cardScheduleSkipped:    cardLayerMain,
	cardScheduleStreak:     cardLayerMain,
	cardScheduleLimitRetry: cardLayerMain,
	cardMemoryMerge:        cardLayerMain,
	cardCaptureSorted:      cardLayerMain,
	cardKBHistory:          cardLayerMain,
}

// cardLayerOf is source's layer, 0 for a source the table does not know.
func cardLayerOf(source string) int { return cardLayers[source] }

type cardSourceKey struct{}

// withCard is ctx for a push (push, pushResult) or a card (ask) made at source.
func withCard(ctx context.Context, source string) context.Context {
	return context.WithValue(ctx, cardSourceKey{}, source)
}

// cardSourceOf is the source withCard put on ctx, "" for none.
func cardSourceOf(ctx context.Context) string {
	s, _ := ctx.Value(cardSourceKey{}).(string)
	return s
}

// ownCardSource is the source of a card the manager asks itself (ask) when its caller named none.
func ownCardSource(ix proto.Interaction) string {
	switch {
	case secretOrForm(ix):
		return cardSecretForm
	case ix.ToolName == "ask_user":
		return cardAskUser
	case ix.Kind == proto.IxPermission:
		return cardToolPermission
	}
	return cardManagerQuestion
}

// secretOrForm: ix asks for a password, for fields or is an MCP server's form (projection cardKind's secret and form).
func secretOrForm(ix proto.Interaction) bool {
	return ix.Secret || len(ix.Fields) > 0 || ix.Kind == proto.IxElicitation
}

// inputCardSource is the source of card ix's push (notifyInteractionAt): the manager's own card by what ask was told,
// an Agent's by its kind. escalated: ask_route rang it up its ladder.
func (s *Service) inputCardSource(ix proto.Interaction, escalated bool) string {
	if v, ok := s.cardAsks.Load(ix.ID); ok {
		return v.(string)
	}
	switch {
	case ix.ToolName == "ask_user": // ask_user with wait false (ask_later.go) does not go through ask
		return cardAskUser
	case secretOrForm(ix):
		return cardAgentSecretForm
	case escalated:
		return cardAskEscalated
	case ix.Kind == proto.IxPermission:
		return cardAgentPermission
	case ix.Kind == proto.IxPlan:
		return cardAgentPlan
	}
	return cardAgentQuestion
}

// cardLedgerKey keeps the counts (Store.Setting), cardLedgerDays days of them.
const (
	cardLedgerKey  = "assistant.card_ledger"
	cardLedgerDays = 8
)

// cardCount is how many cards one source pushed for one owner in a day.
type cardCount struct {
	Source string `json:"source"`
	Layer  int    `json:"layer"`
	Owner  string `json:"owner,omitempty"`
	N      int    `json:"n"`
}

// cardLedger is the counts by the owner's local day (2006-01-02). Without a database they are kept in memory only and
// start again with the manager.
type cardLedger struct {
	mu     sync.Mutex
	loaded bool
	days   map[string][]cardCount
}

// noteCard records push n going to the owner: its source (source, else the one on ctx, else untagged), the layer the
// table gives it and its owner, the session it opens ("" for none). It takes no lock of the caller's, so it can be
// called wherever a push is sent.
func (s *Service) noteCard(ctx context.Context, source string, n Notification) {
	s.noteCardOwner(ctx, source, n.SessionID)
}

// supervisingOwner is the owner of an Agent's push n (deliverCard): the conversation supervising the task of the
// session it opens, else that session. It reads the to-dos: not for a caller that may hold their lock.
func (s *Service) supervisingOwner(ctx context.Context, n Notification) string {
	if n.DeviceID != "" && n.SessionID != "" {
		if t, ok := s.todoOfSession(ctx, n.DeviceID, n.SessionID); ok && t.Supervision != nil && t.Supervision.Supervisor != "" {
			return t.Supervision.Supervisor
		}
	}
	return n.SessionID
}

// noteCardOwner is noteCard with the owner given.
func (s *Service) noteCardOwner(ctx context.Context, source, owner string) {
	source = cmp.Or(source, cardSourceOf(ctx), cardUntagged)
	layer := cardLayerOf(source)
	if s.logf != nil {
		s.logf("assistant: card to the owner: layer %d, source %s, owner %q", layer, source, owner)
	}
	s.countCard(ctx, time.Now().In(s.location(ctx)), cardCount{Source: source, Layer: layer, Owner: owner, N: 1})
}

// countCard adds c to the day of at, drops the days past cardLedgerDays and saves.
func (s *Service) countCard(ctx context.Context, at time.Time, c cardCount) {
	b := &s.cards
	b.mu.Lock()
	defer b.mu.Unlock()
	if !s.cardsLoadedLocked(ctx) {
		return
	}
	day := at.Format(time.DateOnly)
	rows := b.days[day]
	if i := slices.IndexFunc(rows, func(r cardCount) bool { return r.Source == c.Source && r.Owner == c.Owner }); i >= 0 {
		rows[i].N += c.N
		rows[i].Layer = c.Layer // the table may have changed since
	} else {
		rows = append(rows, c)
	}
	b.days[day] = rows
	oldest := at.AddDate(0, 0, -cardLedgerDays).Format(time.DateOnly)
	for d := range b.days {
		if d < oldest {
			delete(b.days, d)
		}
	}
	if st := s.cardStore(); st != nil {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := st.PutSetting(ctx, cardLedgerKey, b.days); err != nil && s.logf != nil {
			s.logf("assistant: save the card ledger: %v", err)
		}
	}
}

// cardsLoadedLocked reads the counts once; b.mu is held. False while they cannot be read (nothing is saved over them).
func (s *Service) cardsLoadedLocked(ctx context.Context) bool {
	b := &s.cards
	if b.loaded {
		return true
	}
	m := map[string][]cardCount{}
	if st := s.cardStore(); st != nil {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if _, err := st.Setting(ctx, cardLedgerKey, &m); err != nil {
			if s.logf != nil {
				s.logf("assistant: load the card ledger: %v", err)
			}
			return false
		}
	}
	if m == nil {
		m = map[string][]cardCount{}
	}
	b.days, b.loaded = m, true
	return true
}

// cardStore is the database the counts are kept in, nil for none.
func (s *Service) cardStore() Store {
	if s.store == nil {
		return nil
	}
	return s.store()
}

// cardsOn is a copy of the counts of day (2006-01-02).
func (s *Service) cardsOn(ctx context.Context, day string) []cardCount {
	b := &s.cards
	b.mu.Lock()
	defer b.mu.Unlock()
	if !s.cardsLoadedLocked(ctx) {
		return nil
	}
	return slices.Clone(b.days[day])
}

// cardLedgerDay is the day a briefing at now reconciles: before noon in the owner's zone (the morning briefing) the
// whole of yesterday, from noon on (the evening one) today so far. Each day is so reconciled in full once, the next
// morning. yesterday says which.
func cardLedgerDay(now time.Time, loc *time.Location) (day string, yesterday bool) {
	local := now.In(loc)
	if local.Hour() < 12 {
		return local.AddDate(0, 0, -1).Format(time.DateOnly), true
	}
	return local.Format(time.DateOnly), false
}

// cardLedgerLine is the briefing's reconciliation line for the day a briefing at now covers (cardLedgerDay): how many
// cards reached the owner and how many of those were layers 1-3 (or unclassified), by source. "" when none did.
func (s *Service) cardLedgerLine(ctx context.Context, l i18n.Lang, now time.Time, loc *time.Location) string {
	day, yesterday := cardLedgerDay(now, loc)
	return cardLedgerText(l, s.cardsOn(ctx, day), yesterday)
}

// cardLedgerText is cardLedgerLine of the counts rows.
func cardLedgerText(l i18n.Lang, rows []cardCount, yesterday bool) string {
	total, below := 0, 0
	bySource := map[string]int{}
	for _, r := range rows {
		total += r.N
		if r.Layer != cardLayerOwner {
			below += r.N
			bySource[r.Source] += r.N
		}
	}
	if total == 0 {
		return ""
	}
	key, clean := "assistant-briefing.card_ledger_today", "assistant-briefing.card_ledger_today_clean"
	if yesterday {
		key, clean = "assistant-briefing.card_ledger_yesterday", "assistant-briefing.card_ledger_yesterday_clean"
	}
	if below == 0 {
		return i18n.T(l, clean, i18n.Args{"n": total})
	}
	sources := make([]string, 0, len(bySource))
	for src := range bySource {
		sources = append(sources, src)
	}
	sort.Slice(sources, func(i, j int) bool { // the most first, then by name
		if bySource[sources[i]] != bySource[sources[j]] {
			return bySource[sources[i]] > bySource[sources[j]]
		}
		return sources[i] < sources[j]
	})
	parts := make([]string, len(sources))
	for i, src := range sources {
		parts[i] = fmt.Sprintf("%s×%d", src, bySource[src])
	}
	return i18n.T(l, key, i18n.Args{"n": total, "m": below, "sources": strings.Join(parts, i18n.T(l, "assistant-briefing.card_ledger_sep"))})
}
