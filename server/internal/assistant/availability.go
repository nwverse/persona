package assistant

import (
	"cmp"
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

// Availability metrics (#4924, docs/assistant/assistant.md "Availability metrics"; step M1 of
// docs/design/proposals/main-chat-availability.md): how long the owner waits for an assistant conversation's reply,
// what its rounds are spent on, how long system messages wait in its queue, and who answers the cards. Everything is
// a work event (session_work_events, kept 30 days like the others): one per answered owner message, per round, per
// delivered queued message and per ended card. Nothing here calls a model or reads a transcript.
//
// GET /api/v1/assistant/availability reads them. It is a route of its own because #4601's north-star read is not on
// main; its top-level fields are named so that they can be served from that route unchanged once it is.
const (
	// replyWaitEvent: the owner's message to an assistant conversation got its first reply text. Detail: state (what the
	// conversation was doing when the message came, the wait* constants), wait (seconds), main; unanswered when no
	// text came within replyWaitMax (wait is then that bound); by "turn_end" when the turn's end stood in for a text
	// the session list never showed changing.
	replyWaitEvent = "reply_wait"
	// roundEvent: an assistant conversation's round ended. Detail: source (who started it, the round* constants and
	// the message sources), seconds, main.
	roundEvent = "round"
	// queueWaitEvent: a queued system message was delivered. Detail: kind (as roundEvent's source), wait (seconds since
	// it was first queued), depth (the queue's length with it when it was added), main.
	queueWaitEvent = "queue_wait"
	// askOutcomeEvent: an agent's or the assistant's card ended. Detail: interaction (its id; not "id", which the
	// delegate's marks of a card are read back by), kind (the card's, "ask_user" for the
	// assistant's own question), by (askByOwner, askByAssistant or askByRemembered), status, after (seconds).
	askOutcomeEvent = "ask_outcome"
)

// What started a round, besides the message sources (proto.SourceSupervision, SourceFollowup, SourcePatrol,
// SourceConversation).
const (
	roundOwner  = "owner"
	roundNotice = "notice" // a task's notice, a late ask_user answer, a spawned conversation's report
	// roundOther: the manager neither sent nor saw the input: a new conversation's first message, a background task
	// reporting back, a round under way when the manager started.
	roundOther = "other"
)

// What the conversation was doing when the owner's message came.
const (
	waitIdle       = "idle"
	waitOwnerRound = "owner_round"
	waitBackground = "background_round"
	// waitUnknownRound: working on a round the manager did not see start (it restarted meanwhile).
	waitUnknownRound = "unknown_round"
)

// Who ended a card, as recorded; the read says which of the assistant's it was (the askWho* constants).
const (
	askByOwner      = "owner"
	askByAssistant  = "assistant"
	askByRemembered = "remembered" // an "Always allow in this session" kept earlier
)

// Variables for the tests.
var (
	// availInputFor: a noted input starts the conversation's next round when that starts within this long (resuming a
	// stopped conversation takes a while).
	availInputFor = 5 * time.Minute
	// availQueuedGrace: an input noted during a round that did not take it starts the next round within this long of
	// the round's end; later, it was taken into the round it came in.
	availQueuedGrace = 15 * time.Second
	// replyWaitMax: an owner message with no reply text this long after it is recorded unanswered.
	replyWaitMax = 30 * time.Minute
)

type availInput struct {
	source string
	until  time.Time
}

type availRound struct {
	source  string
	started time.Time
}

type availWait struct {
	at    time.Time
	state string
}

// availBook is what the metrics need between two events, in memory: a restart loses the rounds and waits under way.
type availBook struct {
	mu     sync.Mutex
	known  map[[2]string]bool       // the conversations an input was noted for: assistant conversations
	inputs map[[2]string]availInput // the input a conversation's next round starts with
	rounds map[[2]string]availRound // the rounds under way
	waits  map[[2]string]availWait  // the owner's earliest message still without a reply
	// waiting is len(waits): the session lists are not looked at while nobody waits.
	waiting atomic.Int32
}

func (b *availBook) init() {
	if b.known == nil {
		b.known, b.inputs = map[[2]string]bool{}, map[[2]string]availInput{}
		b.rounds, b.waits = map[[2]string]availRound{}, map[[2]string]availWait{}
	}
}

// availInput notes that an input from source goes to machine's assistant conversation now.
func (s *Service) availInput(machine, session, source string, now time.Time) {
	if machine == "" || session == "" || source == "" || source == proto.SourceMeeting {
		return // a meeting's batch is a side exchange, not a round (meeting_turns.go)
	}
	b := &s.avail
	b.mu.Lock()
	defer b.mu.Unlock()
	b.init()
	k := [2]string{machine, session}
	b.known[k] = true
	b.inputs[k] = availInput{source: source, until: now.Add(availInputFor)}
}

// relayKind is what a queued message counts as: its message source, else a notice.
func relayKind(msg relayMsg) string {
	if src := msg.source(); src != nil {
		return src.Kind
	}
	return roundNotice
}

// queuedAs is msg taking queued message old's place: it has waited since old was queued.
func (msg relayMsg) queuedAs(old relayMsg) relayMsg {
	msg.QueuedAt, msg.Depth = old.QueuedAt, old.Depth
	return msg
}

// availOwnerSpoke: the owner's message for machine's assistant conversation was accepted. The earliest one without a
// reply is what the reply wait is measured from.
func (s *Service) availOwnerSpoke(r SessionRef, now time.Time) {
	if r.MachineID == "" || r.SessionID == "" {
		return
	}
	k := [2]string{r.MachineID, r.SessionID}
	working := false
	if c, ok := s.nodeSession(r.MachineID, r.SessionID); ok {
		working = c.Status == proto.StatusWorking
	}
	b := &s.avail
	b.mu.Lock()
	b.init()
	if _, waits := b.waits[k]; !waits {
		state := waitIdle
		if round, ok := b.rounds[k]; ok {
			state = waitBackground
			if round.source == roundOwner {
				state = waitOwnerRound
			}
		} else if working {
			state = waitUnknownRound
		}
		b.waits[k] = availWait{at: now, state: state}
		b.waiting.Store(int32(len(b.waits)))
	}
	b.mu.Unlock()
	s.availInput(r.MachineID, r.SessionID, roundOwner, now)
}

// availConversation: k (machine, session) is one of the assistant's conversations, as far as is known without
// asking its node.
func (s *Service) availConversation(k [2]string) bool {
	b := &s.avail
	b.mu.Lock()
	known := b.known[k]
	b.mu.Unlock()
	if known {
		return true
	}
	m, ok := s.fleetMachine(k[0])
	if !ok {
		return false
	}
	c, ok := s.nodeSession(k[0], k[1])
	return ok && m.isAssistant(c)
}

// availTurnStarted: machine's session is seen working; newTurn: it was seen stopped before. An assistant
// conversation's round starts with the input noted for it.
func (s *Service) availTurnStarted(machine, session string, newTurn bool, now time.Time) {
	k := [2]string{machine, session}
	if !s.availConversation(k) {
		return
	}
	b := &s.avail
	b.mu.Lock()
	defer b.mu.Unlock()
	b.init()
	if _, open := b.rounds[k]; open && !newTurn {
		return // the same round seen again (a reconnect)
	}
	source := roundOther
	if in, ok := b.inputs[k]; ok {
		if now.Before(in.until) {
			source = in.source
		}
		delete(b.inputs, k)
	}
	b.rounds[k] = availRound{source: source, started: now} // one that never ended (interrupted, its node gone) is dropped
}

// availTurnEnded: machine's session finished a turn: its round is recorded, and an owner message the round started
// after is answered by now, whether or not the list showed its text.
func (s *Service) availTurnEnded(ctx context.Context, machine, session string, now time.Time) {
	k := [2]string{machine, session}
	b := &s.avail
	b.mu.Lock()
	b.init()
	round, ok := b.rounds[k]
	if !ok {
		b.mu.Unlock()
		return
	}
	delete(b.rounds, k)
	if in, ok := b.inputs[k]; ok {
		// Noted while the round ran: it starts the next round only when that follows at once.
		if grace := now.Add(availQueuedGrace); grace.Before(in.until) {
			in.until = grace
			b.inputs[k] = in
		}
	}
	wait, answered := b.waits[k]
	if answered = answered && !round.started.Before(wait.at); answered {
		delete(b.waits, k)
		b.waiting.Store(int32(len(b.waits)))
	}
	b.mu.Unlock()
	main := s.isMain(ctx, session)
	s.workEvent(machine, session, roundEvent, map[string]any{"source": round.source, "seconds": now.Sub(round.started).Seconds(), "main": main})
	if answered {
		s.workEvent(machine, session, replyWaitEvent, map[string]any{"state": wait.state, "wait": now.Sub(wait.at).Seconds(), "main": main, "by": "turn_end"})
	}
}

// availSeen looks at machine's session as its node lists it now (was: as it listed it before): the first change of
// its last text after the owner's message, with nothing of theirs still queued behind the turn, is the reply.
func (s *Service) availSeen(machine string, sess *proto.Session, was turnPrev, now time.Time) {
	b := &s.avail
	if b.waiting.Load() == 0 {
		return
	}
	k := [2]string{machine, sess.SessionID}
	b.mu.Lock()
	wait, ok := b.waits[k]
	replied := ok && sess.LastText != "" && sess.LastText != was.LastText && len(sess.Queued) == 0
	gaveUp := ok && !replied && now.Sub(wait.at) > replyWaitMax
	if replied || gaveUp {
		delete(b.waits, k)
		b.waiting.Store(int32(len(b.waits)))
	}
	b.mu.Unlock()
	switch {
	case replied:
		s.workEvent(machine, sess.SessionID, replyWaitEvent, map[string]any{"state": wait.state, "wait": now.Sub(wait.at).Seconds(),
			"main": s.isMain(context.Background(), sess.SessionID)})
	case gaveUp:
		s.workEvent(machine, sess.SessionID, replyWaitEvent, map[string]any{"state": wait.state, "wait": replyWaitMax.Seconds(),
			"main": s.isMain(context.Background(), sess.SessionID), "unanswered": true})
	}
}

// availQueueDelivered: queued message msg went into machine's conversation to.
func (s *Service) availQueueDelivered(machine, to string, msg relayMsg, now time.Time) {
	if msg.QueuedAt.IsZero() {
		return // queued by a manager from before the metric
	}
	s.workEvent(machine, to, queueWaitEvent, map[string]any{"kind": relayKind(msg), "wait": now.Sub(msg.QueuedAt).Seconds(),
		"depth": msg.Depth, "main": s.isMain(context.Background(), to)})
}

// availAskEnded: a card ended (InteractionResolved). One whose answer failed is still open and not counted.
func (s *Service) availAskEnded(r InteractionResolved, now time.Time) {
	ix := r.Interaction
	if ix.ID == "" || ix.CreatedAt.IsZero() || r.Status == "failed" {
		return
	}
	by := askByOwner
	switch {
	case r.Resolution.Remembered:
		by = askByRemembered
	case r.Resolution.AnsweredBy != "":
		by = askByAssistant
	}
	kind := ix.Kind
	if ix.ToolName == "ask_user" {
		kind = "ask_user"
	}
	s.workEvent(ix.MachineID, ix.SessionID, askOutcomeEvent, map[string]any{"interaction": ix.ID, "kind": kind, "by": by,
		"status": r.Status, "after": max(now.Sub(ix.CreatedAt).Seconds(), 0)})
}

// ---- the read ----

// availabilityStats is GET /api/v1/assistant/availability: the four metrics over the last hours.
type availabilityStats struct {
	Hours int `json:"hours"`
	// ReplyWait: seconds from the owner's message to the conversation's first reply text.
	ReplyWait replyWaits `json:"reply_wait"`
	// BackgroundShare: per day (the owner's time zone) and conversation, round time and rounds by what started them;
	// newest day first.
	BackgroundShare []roundDay `json:"background_share"`
	// QueueWait: seconds a system message waited in a conversation's queue, by kind, and what waits now.
	QueueWait queueWaits `json:"queue_wait"`
	// AskOutcome: by card kind, then by who answered: how many and after how long.
	AskOutcome map[string]map[string]waitStats `json:"ask_outcome"`
}

type replyWaits struct {
	// All and Main: by what the conversation was doing when the message came (idle, owner_round, background_round,
	// unknown_round); Main is the main conversation alone.
	All  map[string]replyWaitStats `json:"all"`
	Main map[string]replyWaitStats `json:"main"`
	// MainDays: the main conversation's waits per day, whatever it was doing; newest first.
	MainDays []replyWaitDay `json:"main_days"`
}

type replyWaitStats struct {
	waitStats
	// Unanswered: how many of them got no reply text within 30 minutes (counted at that bound).
	Unanswered int `json:"unanswered"`
}

type replyWaitDay struct {
	Day string `json:"day"`
	replyWaitStats
}

type roundDay struct {
	Day          string `json:"day"`
	Conversation string `json:"conversation"`
	Main         bool   `json:"main"`
	// Seconds and Rounds by what started the round: owner, supervision, followup, patrol, conversation, notice, other.
	Seconds map[string]float64 `json:"seconds"`
	Rounds  map[string]int     `json:"rounds"`
	// BackgroundShare: the share of Seconds spent on rounds the owner did not start.
	BackgroundShare float64 `json:"background_share"`
}

type queueWaits struct {
	ByKind map[string]queueWaitStats `json:"by_kind"`
	// QueuedNow: the conversations with system messages waiting now, the longest wait first.
	QueuedNow []queuedNow `json:"queued_now"`
}

type queueWaitStats struct {
	waitStats
	// MaxDepth: the longest the queue was when one of them was added, itself included.
	MaxDepth int `json:"max_depth"`
}

type queuedNow struct {
	Conversation  string  `json:"conversation"`
	Main          bool    `json:"main"`
	Depth         int     `json:"depth"`
	OldestSeconds float64 `json:"oldest_seconds"`
}

// Who answered a card, as the read says it, besides askByOwner and askByRemembered.
const (
	askWhoSupervisor = "supervisor"
	askWhoDelegate   = "delegate"
	askWhoStanding   = "standing_authorization"
	askWhoDefault    = "default"
	askWhoDismissed  = "dismissed" // ended without an answer: dismissed, cancelled, withdrawn
)

// taskAsk is one answered task card of the acceptance book (availabilityAsks), for summarizeAvailability.
type taskAsk struct {
	kind, who string
	after     float64
}

func waitsOf(v []float64) waitStats {
	if len(v) == 0 {
		return waitStats{}
	}
	slices.Sort(v)
	return waitStats{N: len(v), Median: v[len(v)/2], P90: v[min(len(v)-1, len(v)*9/10)]}
}

// summarizeAvailability reads the four metrics out of events (oldest first); days are loc's. asks and queued are
// what is not an event: the answered task cards and the queues as they are now.
func summarizeAvailability(hours int, events []db.WorkEvent, loc *time.Location, asks []taskAsk, queued []queuedNow) availabilityStats {
	out := availabilityStats{Hours: hours, ReplyWait: replyWaits{All: map[string]replyWaitStats{}, Main: map[string]replyWaitStats{}, MainDays: []replyWaitDay{}},
		BackgroundShare: []roundDay{}, QueueWait: queueWaits{ByKind: map[string]queueWaitStats{}, QueuedNow: queued},
		AskOutcome: map[string]map[string]waitStats{}}
	if out.QueueWait.QueuedNow == nil {
		out.QueueWait.QueuedNow = []queuedNow{}
	}
	type waits struct {
		v          []float64
		unanswered int
	}
	add := func(m map[string]*waits, k string, wait float64, unanswered bool) {
		w := m[k]
		if w == nil {
			w = &waits{}
			m[k] = w
		}
		w.v = append(w.v, wait)
		if unanswered {
			w.unanswered++
		}
	}
	replyAll, replyMain, replyDays := map[string]*waits{}, map[string]*waits{}, map[string]*waits{}
	days := map[[2]string]*roundDay{}
	queue, depth := map[string][]float64{}, map[string]int{}
	// The cards a standing authorization answered, and those the delegate was deciding on: which of the assistant's
	// answers were theirs.
	standing, delegated := map[string]bool{}, map[string]bool{}
	type outcome struct {
		id, kind, by, status string
		after                float64
	}
	var outcomes []outcome
	counted := map[string]bool{} // a card's first end counts (an answer changed later ends it again)
	for _, e := range events {
		str := func(k string) string { v, _ := e.Detail[k].(string); return v }
		num := func(k string) float64 {
			switch v := e.Detail[k].(type) {
			case float64:
				return v
			case int: // as recorded, before the store's JSON
				return float64(v)
			}
			return 0
		}
		main, _ := e.Detail["main"].(bool)
		switch e.Kind {
		case replyWaitEvent:
			unanswered, _ := e.Detail["unanswered"].(bool)
			state := cmp.Or(str("state"), waitIdle)
			add(replyAll, state, num("wait"), unanswered)
			if main {
				add(replyMain, state, num("wait"), unanswered)
				add(replyDays, e.At.In(loc).Format(time.DateOnly), num("wait"), unanswered)
			}
		case roundEvent:
			k := [2]string{e.At.In(loc).Format(time.DateOnly), e.SessionID}
			d := days[k]
			if d == nil {
				d = &roundDay{Day: k[0], Conversation: k[1], Seconds: map[string]float64{}, Rounds: map[string]int{}}
				days[k] = d
			}
			source := cmp.Or(str("source"), roundOther)
			d.Main = d.Main || main
			d.Seconds[source] += num("seconds")
			d.Rounds[source]++
		case queueWaitEvent:
			kind := cmp.Or(str("kind"), roundNotice)
			queue[kind] = append(queue[kind], num("wait"))
			depth[kind] = max(depth[kind], int(num("depth")))
		case "standing_authorized":
			standing[str("interaction")] = true
		case "delegate_start":
			delegated[str("id")] = true
		case askOutcomeEvent:
			if id := str("interaction"); !counted[id] {
				counted[id] = true
				outcomes = append(outcomes, outcome{id: id, kind: str("kind"), by: str("by"), status: str("status"), after: num("after")})
			}
		}
	}
	stats := func(w *waits) replyWaitStats {
		return replyWaitStats{waitStats: waitsOf(w.v), Unanswered: w.unanswered}
	}
	for k, w := range replyAll {
		out.ReplyWait.All[k] = stats(w)
	}
	for k, w := range replyMain {
		out.ReplyWait.Main[k] = stats(w)
	}
	for day, w := range replyDays {
		out.ReplyWait.MainDays = append(out.ReplyWait.MainDays, replyWaitDay{Day: day, replyWaitStats: stats(w)})
	}
	slices.SortFunc(out.ReplyWait.MainDays, func(a, b replyWaitDay) int { return cmp.Compare(b.Day, a.Day) })
	for _, d := range days {
		total := 0.0
		for _, sec := range d.Seconds {
			total += sec
		}
		if total > 0 {
			d.BackgroundShare = (total - d.Seconds[roundOwner]) / total
		}
		out.BackgroundShare = append(out.BackgroundShare, *d)
	}
	slices.SortFunc(out.BackgroundShare, func(a, b roundDay) int {
		return cmp.Or(cmp.Compare(b.Day, a.Day), cmp.Compare(a.Conversation, b.Conversation))
	})
	for kind, v := range queue {
		out.QueueWait.ByKind[kind] = queueWaitStats{waitStats: waitsOf(v), MaxDepth: depth[kind]}
	}
	afters := map[[2]string][]float64{}
	for _, o := range outcomes {
		who := o.by
		switch {
		case o.status != "resolved":
			who = askWhoDismissed
		case o.by != askByAssistant:
		case standing[o.id]:
			who = askWhoStanding
		case delegated[o.id]:
			who = askWhoDelegate
		default: // a conversation's interaction_answer
			who = askWhoSupervisor
		}
		afters[[2]string{o.kind, who}] = append(afters[[2]string{o.kind, who}], o.after)
	}
	for _, a := range asks {
		afters[[2]string{a.kind, a.who}] = append(afters[[2]string{a.kind, a.who}], a.after)
	}
	for k, v := range afters {
		if out.AskOutcome[k[0]] == nil {
			out.AskOutcome[k[0]] = map[string]waitStats{}
		}
		out.AskOutcome[k[0]][k[1]] = waitsOf(v)
	}
	return out
}

// availabilityAsks are the task cards (a supervised task's decision or conclusion, a deployed task's "Please verify")
// asked since since and answered, as the acceptance book still has them: by the owner, the task's supervisor, or by
// default at a reversible decision's deadline. A card whose task was closed or asked again is no longer in the book.
func (s *Service) availabilityAsks(ctx context.Context, since time.Time) []taskAsk {
	b := &s.accept
	b.mu.Lock()
	defer b.mu.Unlock()
	if !s.loadAcceptLocked(ctx) {
		return nil
	}
	var out []taskAsk
	for _, a := range b.asks {
		if a == nil || a.AskedAt.Before(since) || a.AcceptedAt.IsZero() {
			continue
		}
		t := taskAsk{kind: "verify", who: askByOwner, after: max(a.AcceptedAt.Sub(a.AskedAt).Seconds(), 0)}
		switch {
		case decideAsk(a):
			t.kind = "decision"
		case a.Conclusion:
			t.kind = "conclusion"
		}
		switch {
		case a.ByDefault:
			t.who = askWhoDefault
		case a.BySupervisor, a.ByAssistant:
			t.who = askWhoSupervisor
		}
		out = append(out, t)
	}
	return out
}

// availabilityQueued are the conversations with system messages waiting now.
func (s *Service) availabilityQueued(ctx context.Context, now time.Time) []queuedNow {
	b := &s.relays
	b.mu.Lock()
	if !s.relayLocked(ctx) {
		b.mu.Unlock()
		return nil
	}
	var out []queuedNow
	for id, q := range b.st.Queue {
		if len(q) == 0 {
			continue
		}
		oldest := now
		for _, msg := range q {
			at := msg.QueuedAt
			if at.IsZero() {
				at = msg.At
			}
			if !at.IsZero() && at.Before(oldest) {
				oldest = at
			}
		}
		out = append(out, queuedNow{Conversation: id, Depth: len(q), OldestSeconds: now.Sub(oldest).Seconds()})
	}
	b.mu.Unlock()
	for i := range out {
		out[i].Main = s.isMain(ctx, out[i].Conversation)
	}
	slices.SortFunc(out, func(a, b queuedNow) int {
		return cmp.Or(cmp.Compare(b.OldestSeconds, a.OldestSeconds), cmp.Compare(a.Conversation, b.Conversation))
	})
	return out
}

func (s *Service) availabilityRoutes() {
	// ?hours=168 (the default: a week; at most 30 days, as long as the events are kept).
	s.handle("GET /api/v1/assistant/availability", func(w http.ResponseWriter, r *http.Request) {
		if !fullRole(r) {
			apierr.Write(w, http.StatusForbidden, errors.New("only a full device reads the availability metrics"))
			return
		}
		hours, _ := strconv.Atoi(r.URL.Query().Get("hours"))
		if hours <= 0 || hours > 24*30 {
			hours = 24 * 7
		}
		st := s.workDB()
		if st == nil {
			apierr.Write(w, http.StatusServiceUnavailable, errors.New("no database"))
			return
		}
		now := time.Now()
		since := now.Add(-time.Duration(hours) * time.Hour)
		events, err := st.WorkEvents(r.Context(), since)
		if err != nil {
			apierr.Write(w, http.StatusServiceUnavailable, err)
			return
		}
		apierr.WriteJSON(w, summarizeAvailability(hours, events, s.location(r.Context()), s.availabilityAsks(r.Context(), since),
			s.availabilityQueued(r.Context(), now)))
	})
}
