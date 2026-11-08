package assistant

import (
	"context"
	"errors"
	"math"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// Where an agent stands in its work (docs/assistant/agent-work.md, #1686), the manager's: when a turn ends it triages it — rules
// first, then a cheap model reading the last reply — into done or blocked on a named precondition, and acts on what it
// can: "Continue" after a transient model error (with backoff and a cap), a wake when what it waits for should be over, a
// card for what the user is to decide (the answer goes back to the agent). Everything else (pushing code, deploying,
// messaging people, touching someone else's MR) is only ever a card. Each action checks, the moment before, that the
// session is still stopped on the same turn with nothing pending or queued: whatever the user did meanwhile wins.
//
// The hub carries it (assistantwire/agents.go): it says when a turn starts and ends (session.turn_started / ended)
// and when the user looked at a session (session.seen); the work states go to it in the snapshot (Snapshot.Work, put
// on the session lists), the cards among the snapshot's interactions (answered with manager.action); what the manager
// sends an agent goes through device.call session.send.

// TriageSwitches turn the triage and each of its actions on, all off until the user turns them on (Settings › Session Triage,
// settings agents.triage): the triage calls a model on every stopped turn, and a "Continue" or a wake runs a turn of the
// agent on its own model (docs/assistant/agent-work.md "Switches").
type TriageSwitches struct {
	Enabled      bool `json:"enabled"`       // triage stopped turns: the lists' line, done no longer unread
	AutoContinue bool `json:"auto_continue"` // "Continue" after a transient model error
	Wake         bool `json:"wake"`          // wake agents whose wait should be over
	Decide       bool `json:"decide"`        // cards for what the user is to decide
	// ReplyReminder: push once when an agent the user looked at still waits on their answer (agents_reply.go,
	// #5096). Rules only, no tokens: unset is on.
	ReplyReminder *bool `json:"reply_reminder,omitempty"`
	// ReplyReminderMinutes: after how long since the user looked. 0: 30 (a PUT with 0 keeps it, a negative one clears it).
	ReplyReminderMinutes int `json:"reply_reminder_minutes,omitempty"`
}

const triageSettingKey = "agents.triage"

func set(b *bool) bool { return b != nil && *b }

func switchesOf(c assistantwire.TriageConfig) TriageSwitches {
	return TriageSwitches{Enabled: set(c.Enabled), AutoContinue: set(c.AutoContinue), Wake: set(c.Wake), Decide: set(c.Decide)}
}

func maxRetries(c assistantwire.TriageConfig) int { return cmp0(c.MaxRetries, 3) }
func maxWakes(c assistantwire.TriageConfig) int   { return cmp0(c.MaxWakes, 6) }
func settleOf(c assistantwire.TriageConfig) time.Duration {
	return time.Duration(cmp0(c.SettleSeconds, 15)) * time.Second
}

func cmp0(n, def int) int {
	if n > 0 {
		return n
	}
	return def
}

// retryBackoff is how long after a transient error each "Continue" goes (the last repeats).
var retryBackoff = []time.Duration{30 * time.Second, 2 * time.Minute, 8 * time.Minute}

// workEvery is how often the manager looks for due wakes and expired cards; workBackfillEvery how often it looks for
// turns that ended unseen (while it was away, or whose triage failed a while ago).
var (
	workEvery         = 15 * time.Second
	workBackfillEvery = time.Minute
	workRetryTriage   = 10 * time.Minute
)

const (
	workCardTool  = assistantwire.WorkCardTool // the card's tool_name: who asks is Persona, about the agent's turn
	workCardTTL   = 24 * time.Hour
	workKeep      = 30 * 24 * time.Hour
	workBackfill  = 24 * time.Hour // turns that ended this long ago still get triaged when they were missed
	workSampleGap = 5 * time.Minute
)

// continueText (tasks_start.go) is what goes on a turn the model service cut off, as the chat's "Retry" on an API error card.

// Card answers the manager acts on itself instead of handing them to the agent: each one's default-language label,
// which names it here. A card shows answerLabel's (the user's language); canonicalAnswer reads any language's back.
const (
	answerSkip    = "先不管"
	answerRetry   = "再试一次"
	answerHandled = "已处理，继续"
	answerAuthed  = "已授权，继续"
	answerWait    = "再等等"
	answerStop    = "不等了"
	answerGo      = "等好了就去做"
	answerHold    = "先别做"
	answerNotNow  = "先不"
	answerOK      = "好，按你说的做"
)

// answerKeys are the answers' texts in this file's namespace.
var answerKeys = map[string]string{answerSkip: "answer_skip", answerRetry: "answer_retry", answerHandled: "answer_handled",
	answerAuthed: "answer_authed", answerWait: "answer_wait", answerStop: "answer_stop", answerGo: "answer_go",
	answerHold: "answer_hold", answerNotNow: "answer_not_now", answerOK: "answer_ok"}

// answerLabel is answer a's label in l.
func answerLabel(l i18n.Lang, a string) string {
	if k, ok := answerKeys[a]; ok {
		return workT(l, k)
	}
	return a
}

// canonicalAnswer is the answer a card's label a stands for, whatever language the card was shown in (a card stays
// up across a change of language); any other text as it is.
func canonicalAnswer(a string) string {
	for name, k := range answerKeys {
		for _, l := range i18n.Langs {
			if workT(l, k) == a {
				return name
			}
		}
	}
	return a
}

// workT is one of this file's texts in l.
func workT(l i18n.Lang, name string, args ...i18n.Args) string {
	return i18n.T(l, "assistant-agents_work."+name, args...)
}

// workStore is the database's session_work and session_work_events (db/session_work.go): only the manager reads and
// writes them.
type workStore interface {
	PutSessionWork(ctx context.Context, w db.SessionWork) error
	SessionWorks(ctx context.Context, since time.Time) ([]db.SessionWork, error)
	PruneSessionWork(ctx context.Context, before time.Time) error
	AddWorkEvent(ctx context.Context, e db.WorkEvent) error
	WorkEvents(ctx context.Context, since time.Time) ([]db.WorkEvent, error)
}

// workBook is the manager's copy of session_work, plus what only matters while it runs.
type workBook struct {
	mu       sync.Mutex
	entries  map[[2]string]*db.SessionWork
	cards    map[string]proto.Interaction // the cards out, by id (the snapshot's interactions)
	loaded   bool
	triaging map[[2]string]bool
	tried    map[[2]string]triedTurn // the turn a triage was last attempted on, and when
	sent     map[[2]string]time.Time // when the manager itself last sent the session something
	seen     map[[2]string]time.Time // the turn whose first look was recorded
	approved map[[2]string]approval  // a risky next step the user said yes to, for the wait it ends
	// answering marks cards whose answer is being delivered to the agent: a repeated callback in the send window
	// (a redelivered webhook, a second tap, phone and watch together) is the same answer, not a second one.
	answering map[string]bool
	writes    map[[2]string]*sync.Mutex
	switches  *TriageSwitches // the saved switches, once read (nil: not read yet)
	// delegated are the questions the assistant answered for the user, by session (fleet_delegate.go), read back
	// from the events once (delegatedLoaded): the same question again is the user's.
	delegated       map[[2]string][]delegatedAsk
	delegatedLoaded bool

	sampleAt, pruneAt, backfillAt time.Time
	publishing                    atomic.Bool // a snapshot is on its way (publishSoon)
	// testEvents collects the events when there is no store (tests).
	testEvents []db.WorkEvent
	// eventWrites are the event inserts still on their way to the store (waitWorkEvents).
	eventWrites sync.WaitGroup
}

func (b *workBook) init() {
	if b.entries == nil {
		b.entries = map[[2]string]*db.SessionWork{}
		b.cards = map[string]proto.Interaction{}
		b.triaging = map[[2]string]bool{}
		b.tried = map[[2]string]triedTurn{}
		b.sent = map[[2]string]time.Time{}
		b.seen = map[[2]string]time.Time{}
		b.approved = map[[2]string]approval{}
		b.answering = map[string]bool{}
		b.writes = map[[2]string]*sync.Mutex{}
	}
}

// approval is the user's "Do it once it's ready" on a risky wait's card: once the wait is over, the agent may take that step. It
// holds while the agent keeps waiting (a wake, the agent's own check), so the same question is not asked again each
// turn; anything the user does themselves, a turn that is no wait, or a riskier step ends it.
type approval struct{ wait, then string }

// covers: a may stand for w's next step as far as the words go — w waits, and its step is no riskier than the approved
// one (each risky word in it was in the approved step). No link: an authorization is the user's to give each time.
// The words cannot tell a target apart (beta or master, one person or the group): applyApproval also wants the triage
// model to say it is the same wait and the same step.
func (a approval) covers(w *proto.WorkState) bool {
	if a.then == "" || w.Phase != proto.WorkBlocked || w.Wait == "" || w.Link != nil {
		return false
	}
	approved := strings.ToLower(a.then)
	for _, word := range riskyWordsIn(w.Then) {
		if !strings.Contains(approved, word) {
			return false
		}
	}
	return true
}

// triedTurn is a turn a triage was attempted on: a failed one is tried again after workRetryTriage, not every tick.
type triedTurn struct{ turn, at time.Time }

// triageCfg is manager.json's agents.triage, as the hello has it.
func (s *Service) triageCfg(ctx context.Context) assistantwire.TriageConfig {
	return s.hello(ctx).Agents.Triage
}

// triageSwitches are the switches in force: the saved ones, else hub.json's. Read from the database once; while it
// cannot be read, hub.json's (off when unset).
func (s *Service) triageSwitches(ctx context.Context) TriageSwitches {
	sw, _ := s.readTriageSwitches(ctx)
	return sw
}

// readTriageSwitches keeps read failures separate from an unset setting. Internal triage may use the returned
// defaults on failure, but the settings API must report the error so clients cannot save those defaults back.
func (s *Service) readTriageSwitches(ctx context.Context) (TriageSwitches, error) {
	s.work.mu.Lock()
	sw := s.work.switches
	s.work.mu.Unlock()
	if sw != nil {
		return *sw, nil
	}
	def := switchesOf(s.triageCfg(ctx))
	st := s.store()
	if st == nil {
		return def, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var saved TriageSwitches
	ok, err := st.Setting(ctx, triageSettingKey, &saved)
	if err != nil {
		return def, err // read again next time
	}
	if !ok {
		saved = def
	}
	s.work.mu.Lock()
	s.work.switches = &saved
	s.work.mu.Unlock()
	return saved, nil
}

func (s *Service) workOn(ctx context.Context) bool { return s.triageSwitches(ctx).Enabled }

// putTriageSwitches saves the switches. What they turn off stops at once: the cards are withdrawn, due retries and
// wakes dropped; the apps hear of it ({"type":"agents.triage"}) and the lists follow the next snapshot.
func (s *Service) putTriageSwitches(ctx context.Context, sw TriageSwitches) error {
	if st := s.store(); st != nil {
		if err := st.PutSetting(ctx, triageSettingKey, sw); err != nil {
			return err
		}
	}
	s.work.mu.Lock()
	s.work.init()
	s.work.switches = &sw
	var stale []db.SessionWork
	for _, e := range s.work.entries {
		off := !sw.Enabled || e.Work.On == proto.WaitTransient && !sw.AutoContinue ||
			(e.Work.On == proto.WaitExternal || e.Work.On == proto.WaitAgent) && !sw.Wake
		if e.InteractionID != "" && (!sw.Enabled || !sw.Decide) {
			delete(s.work.cards, e.InteractionID)
			e.InteractionID, e.Work.InteractionID = "", ""
			stale = append(stale, *e)
		} else if e.WakeAt != nil && off {
			e.WakeAt, e.Work.WakeAt = nil, nil
			stale = append(stale, *e)
		}
	}
	s.work.mu.Unlock()
	for _, e := range stale {
		s.saveWork(e)
	}
	s.event(ctx, map[string]any{"type": "agents.triage", "triage": sw})
	s.publish(ctx)
	return nil
}

func (s *Service) workDB() workStore {
	st, _ := s.store().(workStore)
	return st
}

// sameTurn: c is still on the turn that ended at turnAt (a stopped transcript does not change).
func sameTurn(c proto.Session, turnAt time.Time) bool {
	d := c.UpdatedAt.Sub(turnAt)
	return d > -2*time.Second && d < 2*time.Second
}

// loadWork reads session_work back (once; false while it cannot be read). A card the hub still has up (only the
// manager restarted) stays up as it is, until it is answered, its agent goes on or it expires, as when the triage was
// the hub's; the others (the hub restarted too, or it was answered meanwhile and the hub keeps the answer) are put back
// under the same id while their turn still waits, those whose machine is not back yet by a later tick (cardTick).
func (s *Service) loadWork(ctx context.Context) bool {
	s.work.mu.Lock()
	s.work.init()
	loaded := s.work.loaded
	s.work.mu.Unlock()
	if loaded {
		return true
	}
	var rows []db.SessionWork
	if st := s.workDB(); st != nil {
		lctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		var err error
		rows, err = st.SessionWorks(lctx, time.Now().Add(-workBackfill*2))
		cancel()
		if err != nil {
			s.logf("work: load: %v", err)
			return false // tried again next time
		}
	}
	v := s.newWorkView(ctx)
	now := time.Now()
	var forgot []db.SessionWork
	var forgotIDs []string
	cards := map[string]proto.Interaction{}
	var up map[string]proto.Interaction
	for i := range rows {
		e := rows[i]
		if e.InteractionID == "" {
			continue
		}
		if up == nil {
			up = s.cardsUp(ctx)
		}
		if ix, ok := up[e.InteractionID]; ok && !now.After(e.TurnAt.Add(workCardTTL)) {
			ix.CanCancel = true // The manager can retire its own cards, including cards restored from older hubs.
			cards[e.InteractionID] = ix
			continue
		}
		switch ix, keep := s.cardBack(v, e, now); {
		case ix != nil:
			cards[e.InteractionID] = *ix
		case !keep:
			forgotIDs = append(forgotIDs, e.InteractionID)
			rows[i].InteractionID, rows[i].Work.InteractionID = "", ""
			forgot = append(forgot, rows[i])
		}
	}
	s.work.mu.Lock()
	if s.work.loaded { // loaded meanwhile by another caller
		s.work.mu.Unlock()
		return true
	}
	for i := range rows {
		r := rows[i]
		k := [2]string{r.MachineID, r.SessionID}
		if _, ok := s.work.entries[k]; !ok {
			s.work.entries[k] = &r
		}
	}
	for id, ix := range cards {
		s.work.cards[id] = ix
	}
	s.work.loaded = true
	s.work.mu.Unlock()
	for _, e := range forgot {
		s.saveWork(e)
	}
	for _, id := range forgotIDs {
		s.recordEnd(id, "cancelled", nil) // kept pending across the restart for this (db ExpireStaleInteractions)
	}
	return true
}

func (s *Service) saveWork(e db.SessionWork) {
	unlock := s.lockWorkWrite([2]string{e.MachineID, e.SessionID})
	defer unlock()
	// A caller may have captured e before a newer answer was committed. Write
	// the current state after acquiring the session's write lock, never that
	// stale snapshot over a durable cancellation.
	if current, ok := s.workEntry([2]string{e.MachineID, e.SessionID}); ok {
		e = current
	}
	st := s.workDB()
	if st == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := st.PutSessionWork(ctx, e); err != nil {
		s.logf("work: save %s/%s: %v", e.MachineID, e.SessionID, err)
	}
}

func (s *Service) workEvent(machine, session, kind string, detail map[string]any) {
	e := db.WorkEvent{At: time.Now().UTC(), MachineID: machine, SessionID: session, Kind: kind, Detail: detail}
	st := s.workDB()
	if st == nil {
		s.work.mu.Lock()
		s.work.testEvents = append(s.work.testEvents, e)
		s.work.mu.Unlock()
		return
	}
	s.work.eventWrites.Add(1)
	go func() {
		defer s.work.eventWrites.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := st.AddWorkEvent(ctx, e); err != nil {
			s.logf("work: event: %v", err)
		}
	}()
}

// waitWorkEvents returns once the events recorded so far are in the store (or failed): what reads the stats right
// after acting sees them. For tests only: the caller must know nothing else is recording events meanwhile (no Run,
// no triage goroutine from workTick), as a WaitGroup's Add from zero must not race its Wait.
func (s *Service) waitWorkEvents() { s.work.eventWrites.Wait() }

// workEntry is a copy of a session's stored work state.
func (s *Service) workEntry(k [2]string) (db.SessionWork, bool) {
	s.work.mu.Lock()
	defer s.work.mu.Unlock()
	s.work.init()
	e, ok := s.work.entries[k]
	if !ok {
		return db.SessionWork{}, false
	}
	return *e, true
}

// putWork stores e (memory and database) and reports it (the snapshot: the hub pushes the lists that changed).
func (s *Service) putWork(_ context.Context, e db.SessionWork) {
	e.UpdatedAt = time.Now().UTC()
	s.work.mu.Lock()
	s.work.init()
	key := [2]string{e.MachineID, e.SessionID}
	if current := s.work.entries[key]; current != nil && current.Work.Deferred && current.TurnAt.Equal(e.TurnAt) {
		e.Work.Deferred = true
		e.InteractionID, e.Work.InteractionID, e.WakeAt, e.Work.WakeAt = "", "", nil, nil
	}
	c := e
	s.work.entries[key] = &c
	s.work.mu.Unlock()
	s.saveWork(e)
	s.publishSoon()
}

// workPublishDelay is how long the changes of a pass (a tick, a triage, an answer) are gathered into one snapshot.
var workPublishDelay = 200 * time.Millisecond

// publishSoon reports the snapshot a moment from now, once for whatever changed meanwhile (at once when the delay is
// 0: unit tests).
func (s *Service) publishSoon() {
	if workPublishDelay == 0 {
		s.publish(context.Background())
		return
	}
	if !s.work.publishing.CompareAndSwap(false, true) {
		return
	}
	time.AfterFunc(workPublishDelay, func() {
		s.work.publishing.Store(false)
		s.publish(context.Background())
	})
}

// ---- what the hub shows ----

// workReport is the snapshot's work: the stored states (with the switches' On), nil while they could not be read.
func (s *Service) workReport(ctx context.Context) *WorkReport {
	if !s.loadWork(ctx) {
		return nil
	}
	r := &WorkReport{On: s.workOn(ctx), Sessions: []SessionWork{}}
	if !r.On {
		return r
	}
	s.work.mu.Lock()
	for _, e := range s.work.entries {
		if e.Work.Phase == "" {
			continue // unsure: nothing shown
		}
		w := e.Work
		w.TurnAt = e.TurnAt
		r.Sessions = append(r.Sessions, SessionWork{MachineID: e.MachineID, SessionID: e.SessionID, Work: w})
	}
	s.work.mu.Unlock()
	sort.Slice(r.Sessions, func(i, j int) bool {
		a, b := r.Sessions[i], r.Sessions[j]
		return a.MachineID+"/"+a.SessionID < b.MachineID+"/"+b.SessionID
	})
	return r
}

// workCards are the cards out (the snapshot's interactions besides ask_user's), oldest first.
func (s *Service) workCards() []proto.Interaction {
	s.work.mu.Lock()
	defer s.work.mu.Unlock()
	out := make([]proto.Interaction, 0, len(s.work.cards))
	for _, ix := range s.work.cards {
		out = append(out, ix)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

// ---- the hub's machines and sessions, as one pass sees them ----

// workView asks the hub for each thing once in a pass: the machines, a machine's sessions as the apps get them (with
// the hub's work on them) and which of them are muted.
type workView struct {
	s       *Service
	ctx     context.Context
	devices map[string]Device // nil: not asked yet
	lists   map[string][]proto.Session
	muted   map[string]map[string]bool
	failed  map[string]bool // the machines whose sessions the hub did not answer with: as if they were away
}

func (s *Service) newWorkView(ctx context.Context) *workView {
	return &workView{s: s, ctx: ctx, lists: map[string][]proto.Session{}, muted: map[string]map[string]bool{},
		failed: map[string]bool{}}
}

func (v *workView) device(id string) (Device, bool) {
	if v.devices == nil {
		v.devices = map[string]Device{}
		for _, d := range v.s.devices(v.ctx) {
			v.devices[d.ID] = d
		}
	}
	d, ok := v.devices[id]
	return d, ok
}

func (v *workView) machines() []Device {
	v.device("")
	out := make([]Device, 0, len(v.devices))
	for _, d := range v.devices {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// sessions are machine's sessions as the apps get them (sessions.list, with muted: which have pushes off).
func (v *workView) sessions(machine string) []proto.Session {
	if list, ok := v.lists[machine]; ok {
		return list
	}
	var res struct {
		Sessions []proto.Session `json:"sessions"`
		Muted    []string        `json:"muted"`
	}
	if err := v.s.call(v.ctx, machine, DeviceSessionsList, map[string]any{}, &res); err != nil {
		v.s.logf("work: sessions of %s: %v", machine, err)
		v.failed[machine] = true
	}
	v.lists[machine] = res.Sessions
	muted := map[string]bool{}
	for _, id := range res.Muted {
		muted[id] = true
	}
	v.muted[machine] = muted
	return res.Sessions
}

func (v *workView) session(machine, sid string) (proto.Session, bool) {
	for _, c := range v.sessions(machine) {
		if c.SessionID == sid {
			return c, true
		}
	}
	return proto.Session{}, false
}

func (v *workView) isMuted(machine, sid string) bool {
	v.sessions(machine)
	return v.muted[machine][sid]
}

// ---- turns ----

// retireTodoWork withdraws the manager's pending follow-ups for the agents of a todo that just ended (completed or a
// failed start): the scheduled wake — one postponed while the machine was offline included — and the open work card
// are cleared, so neither can wake the finished agent nor let a card answer back into the finished turn. The turn's
// own work state is kept, as workStarted keeps it: with no wake and no card the tick never acts on the entry, and
// triage never redoes a turn it already triaged.
func (s *Service) retireTodoWork(keys ...[2]string) {
	now := time.Now().UTC()
	for _, k := range keys {
		s.work.mu.Lock()
		s.work.init()
		e, ok := s.work.entries[k]
		if !ok || (e.WakeAt == nil && e.InteractionID == "") {
			s.work.mu.Unlock()
			continue
		}
		if e.InteractionID != "" {
			delete(s.work.cards, e.InteractionID)
		}
		e.WakeAt, e.Work.WakeAt, e.InteractionID, e.Work.InteractionID = nil, nil, "", ""
		e.UpdatedAt = now
		saved := *e
		s.work.mu.Unlock()
		s.saveWork(saved)
		s.publishSoon()
	}
}

// workStarted: a session began working (again). Whatever the manager was going to do for its last turn is off; a card
// for it is withdrawn. How long the turn waited is recorded.
func (s *Service) workStarted(ctx context.Context, machine, sid string, now time.Time) {
	k := [2]string{machine, sid}
	s.work.mu.Lock()
	s.work.init()
	e, ok := s.work.entries[k]
	if !ok || e.Work.Phase == proto.WorkWorking {
		s.work.mu.Unlock()
		return
	}
	by := "user"
	if now.Sub(s.work.sent[k]) < 2*time.Minute {
		by = "hub"
	}
	if by == "user" {
		delete(s.work.approved, k) // the user took over: their earlier yes is not for what comes next
	}
	card := e.InteractionID
	prev := *e
	if card != "" {
		delete(s.work.cards, card)
	}
	changed := e.WakeAt != nil || card != ""
	e.WakeAt, e.Work.WakeAt, e.InteractionID, e.Work.InteractionID = nil, nil, "", ""
	e.UpdatedAt = now.UTC()
	saved := *e
	s.work.mu.Unlock()
	if card != "" {
		s.workEvent(machine, sid, "withdrawn", map[string]any{"by": by})
	}
	if prev.Work.Phase != "" {
		s.workEvent(machine, sid, "resumed", map[string]any{"after": now.Sub(prev.TurnAt).Seconds(), "phase": prev.Work.Phase,
			"on": prev.Work.On, "by": by})
	}
	s.saveWork(saved)
	if changed {
		s.publishSoon()
	}
}

// workSeen: the user looked at a session. The first look after a turn ended is how long it waited for them.
func (s *Service) workSeen(machine, sid string, at time.Time) {
	k := [2]string{machine, sid}
	s.work.mu.Lock()
	s.work.init()
	e, ok := s.work.entries[k]
	if !ok || e.Work.Phase == "" || !s.work.seen[k].Before(e.TurnAt) {
		s.work.mu.Unlock()
		return
	}
	s.work.seen[k] = at
	phase, on, turn := e.Work.Phase, e.Work.On, e.TurnAt
	s.work.mu.Unlock()
	s.workEvent(machine, sid, "seen", map[string]any{"after": at.Sub(turn).Seconds(), "phase": phase, "on": on})
}

// workTurnEnded: a session on machine stopped. Once it stays stopped a moment, its turn is triaged; agents waiting
// for this one are woken.
func (s *Service) workTurnEnded(ctx context.Context, machine, sid string) {
	if !s.workOn(ctx) || !s.loadWork(ctx) {
		return
	}
	s.wakeWaitersOn(machine, sid)
	time.Sleep(settleOf(s.triageCfg(ctx)))
	v := s.newWorkView(ctx)
	c, ok := v.session(machine, sid)
	if !ok || c.Status == proto.StatusWorking {
		return
	}
	s.triageTurn(ctx, v, machine, c)
}

// triageTurn works out where c stands after its turn and acts on it. Once per turn.
func (s *Service) triageTurn(ctx context.Context, v *workView, machine string, c proto.Session) {
	if c.Assistant || c.Status == proto.StatusOffline || c.Status == proto.StatusWorking || c.Starting {
		return
	}
	k := [2]string{machine, c.SessionID}
	s.work.mu.Lock()
	s.work.init()
	prev, had := s.work.entries[k]
	s.work.tried[k] = triedTurn{c.UpdatedAt, time.Now()}
	if had && sameTurn(c, prev.TurnAt) && (prev.Work.Phase != "" || prev.Work.By != "") || s.work.triaging[k] {
		s.work.mu.Unlock()
		return
	}
	s.work.triaging[k] = true
	var last db.SessionWork
	if had {
		last = *prev
	}
	s.work.mu.Unlock()
	defer func() {
		s.work.mu.Lock()
		delete(s.work.triaging, k)
		s.work.mu.Unlock()
	}()

	start := time.Now()
	var same bool // the model says it still waits on the step the user approved
	w, by, skip := triageRules(c, s.location(ctx), s.lang(ctx))
	if skip {
		return // the agent asks itself (or its terminal does): the list shows that
	}
	if w == nil {
		mctx, cancel := context.WithTimeout(ctx, 90*time.Second)
		defer cancel()
		var err error
		w, same, err = s.triageTurnModel(mctx, v, machine, c)
		if err != nil {
			s.logf("triage %s/%s: %v", machine, short(c.SessionID), err)
			s.workEvent(machine, c.SessionID, "failed", map[string]any{"error": clip(err.Error(), 200)})
			return
		}
		by = "model"
	}
	s.applyApproval(k, w, same)
	w.TurnAt, w.At, w.By = c.UpdatedAt, time.Now().UTC(), by
	e := db.SessionWork{MachineID: machine, SessionID: c.SessionID, TurnAt: c.UpdatedAt, Work: *w}
	// The same wait goes on (the manager woke it, it still waits): its counts carry over.
	if had && last.Work.Phase == proto.WorkBlocked && w.Phase == proto.WorkBlocked && last.Work.On == w.On {
		e.Retries, e.Wakes = last.Retries, last.Wakes
	}
	if now, ok := s.session(ctx, machine, c.SessionID); ok && !sameTurn(now, e.TurnAt) {
		return // it went on while the model read it: the next end decides
	}
	s.actOn(ctx, v, machine, c, &e)
	s.putWork(ctx, e)
	s.logf("triage %s/%s: %s%s (%s %.1fs): %s", machine, short(c.SessionID), w.Phase, orEmpty("/", e.Work.On), by,
		time.Since(start).Seconds(), e.Work.Line)
	s.workEvent(machine, c.SessionID, "triaged", map[string]any{"phase": w.Phase, "on": e.Work.On, "by": by,
		"line": e.Work.Line, "ms": time.Since(start).Milliseconds()})
}

// applyApproval: a turn still waiting on the step the user already said yes to (same: the model says so, and the
// words agree) is a wait the manager ends: no second card for it. A turn that is no such wait ends the approval, so
// what is left in s.work.approved always held for the latest triaged turn.
func (s *Service) applyApproval(k [2]string, w *proto.WorkState, same bool) {
	s.work.mu.Lock()
	defer s.work.mu.Unlock()
	a, ok := s.work.approved[k]
	if !ok {
		return
	}
	if !same || !a.covers(w) || len(w.Questions) > 1 { // a second question is the user's all the same
		delete(s.work.approved, k)
		return
	}
	if w.On == proto.WaitUser && risky(w.Then) {
		w.On, w.Questions = proto.WaitExternal, nil // only the approved step made it the user's
	}
}

func orEmpty(sep, s string) string {
	if s == "" {
		return ""
	}
	return sep + s
}

// triageRules settles what needs no model. skip: the agent waits for the user on its own card or dialog.
// What it says is in l (the user's language).
func triageRules(c proto.Session, loc *time.Location, l i18n.Lang) (w *proto.WorkState, by string, skip bool) {
	if c.PendingInteractions > 0 || c.Status == proto.StatusBlocked {
		return nil, "", true
	}
	if e := c.TurnError; e != "" {
		if turnErrorTransient(c) {
			return &proto.WorkState{Phase: proto.WorkBlocked, On: proto.WaitTransient, Line: workT(l, "transient_retrying"),
				Wait: clip(oneLine(e), 200)}, "rule", false
		}
		return &proto.WorkState{Phase: proto.WorkBlocked, On: proto.WaitUser, Line: clip(oneLine(workT(l, "model_error", i18n.Args{"error": e})), 60),
			Questions: []proto.Question{{Question: workT(l, "model_error_ask", i18n.Args{"error": clip(oneLine(e), 300)}), Header: workT(l, "header_model"),
				Options: []proto.QuestionOption{{Label: answerLabel(l, answerHandled)}, {Label: answerLabel(l, answerSkip)}}}}}, "rule", false
	}
	if n := runningInBackground(c); n > 0 {
		return &proto.WorkState{Phase: proto.WorkBlocked, On: proto.WaitExternal, Line: workT(l, "background_running", i18n.Args{"n": n})},
			"rule", false
	}
	if c.Loop != nil {
		line := workT(l, "loop_running")
		if !c.Loop.NextAt.IsZero() {
			line = workT(l, "loop_running_next", i18n.Args{"time": c.Loop.NextAt.In(loc).Format("15:04")})
		}
		return &proto.WorkState{Phase: proto.WorkBlocked, On: proto.WaitExternal, Line: line}, "rule", false
	}
	return nil, "", false
}

func runningInBackground(c proto.Session) int {
	n := len(c.Background)
	for _, a := range c.Subagents {
		if a.Running {
			n++
		}
	}
	return n
}

// turnErrorTransient: c's turn error is worth a "Continue". The agent's record says so when it gives a kind (Codex's error
// kinds, #2185); otherwise the text does.
func turnErrorTransient(c proto.Session) bool {
	switch c.TurnErrorKind {
	case proto.TurnErrorTransient:
		return true
	case proto.TurnErrorUser:
		return false
	}
	return transientError(c.TurnError)
}

// transientError: a model API error worth a "Continue" (the service failed for a moment), not one the user must fix
// (sign-in, key, the model picked).
func transientError(text string) bool {
	t := strings.ToLower(text)
	for _, w := range []string{"failed to authenticate", "oauth token has expired", "invalid api key", "not logged in", "issue with the selected model",
		" 401", " 403", "permission_error", "authentication_error", "credit balance", "billing",
		"ssl certificate", "self-signed certificate", "certificate verif"} {
		if strings.Contains(t, w) {
			return false
		}
	}
	for _, w := range []string{"overloaded", " 500", " 502", " 503", " 504", " 529", " 429", "rate limit", "rate_limit",
		"connection", "timed out", "timeout", "socket hang up", "econnreset", "fetch failed", "no eligible upstream",
		"internal server error", "service unavailable", "bad gateway", "network", "api_error", "upstream",
		"can't reach the api server", "temporarily unavailable", "no response from api", "unable to connect to api",
		"enotfound", "eai_again", "failedtoopensocket"} {
		if strings.Contains(t, w) {
			return true
		}
	}
	return false
}

// riskyWords mark a next step the manager must not set off itself (docs/assistant/agent-work.md "Safety boundary"): it is only ever a
// card.
var riskyWords = []string{"push", "deploy", "部署", "发布", "上线", "merge", "合并", "合入", "发消息", "回复", "评论", "testflight",
	"release", "推送", "推到", "comment", "发版", "提审", "send"}

func risky(texts ...string) bool {
	for _, t := range texts {
		if len(riskyWordsIn(t)) > 0 {
			return true
		}
	}
	return false
}

// riskyWordsIn are the risky words in t (lower case).
func riskyWordsIn(t string) []string {
	t = strings.ToLower(t)
	var out []string
	for _, w := range riskyWords {
		if strings.Contains(t, w) {
			out = append(out, w)
		}
	}
	return out
}

// ---- acting ----

// actOn decides what the manager does about e's work state and records it on e (wake_at, a card). It sends nothing:
// the tick does, after checking again.
func (s *Service) actOn(ctx context.Context, v *workView, machine string, c proto.Session, e *db.SessionWork) {
	cfg, sw := s.triageCfg(ctx), s.triageSwitches(ctx)
	l := s.lang(ctx)
	w := &e.Work
	now := time.Now()
	k := [2]string{e.MachineID, e.SessionID}
	if w.Phase != proto.WorkBlocked || c.ReadOnlyReason != "" {
		return // nothing to do, or nothing Persona may send it
	}
	switch w.On {
	case proto.WaitTransient:
		if !sw.AutoContinue {
			w.Line = clip(oneLine(workT(l, "model_error", i18n.Args{"error": w.Wait})), 60)
			return
		}
		if e.Retries >= maxRetries(cfg) {
			*w = proto.WorkState{Phase: proto.WorkBlocked, On: proto.WaitUser, TurnAt: w.TurnAt, At: w.At, By: w.By,
				Line: workT(l, "retries_line", i18n.Args{"n": e.Retries}),
				Questions: []proto.Question{{Question: workT(l, "retries_ask", i18n.Args{"n": e.Retries, "error": clip(w.Wait, 300)}),
					Header: workT(l, "header_model"), Options: []proto.QuestionOption{{Label: answerLabel(l, answerRetry)}, {Label: answerLabel(l, answerSkip)}}}}}
			break
		}
		at := now.Add(retryBackoff[min(e.Retries, len(retryBackoff)-1)]).UTC()
		e.WakeAt, w.WakeAt, w.Attempts, w.Max = &at, &at, e.Retries, maxRetries(cfg)
		return
	case proto.WaitExternal, proto.WaitAgent:
		if w.Wait == "" && w.Agent == nil {
			return // a background task or a /loop: it wakes itself
		}
		s.work.mu.Lock()
		a, approved := s.work.approved[k]
		s.work.mu.Unlock()
		if risky(w.Then) && !(approved && a.covers(w)) {
			*w = proto.WorkState{Phase: proto.WorkBlocked, On: proto.WaitUser, TurnAt: w.TurnAt, At: w.At, By: w.By, Wait: w.Wait, Then: w.Then,
				Line: clip(oneLine(workT(l, "risky_line", i18n.Args{"wait": w.Wait, "then": w.Then})), 60),
				Questions: []proto.Question{{Question: workT(l, "risky_ask", i18n.Args{"wait": w.Wait, "then": w.Then}), Header: workT(l, "header_risky"),
					Options: []proto.QuestionOption{{Label: answerLabel(l, answerGo), Description: workT(l, "answer_go_about")}, {Label: answerLabel(l, answerHold)}}}}}
			break
		}
		if !sw.Wake {
			return
		}
		if e.Wakes >= maxWakes(cfg) {
			*w = proto.WorkState{Phase: proto.WorkBlocked, On: proto.WaitUser, TurnAt: w.TurnAt, At: w.At, By: w.By, Wait: w.Wait, Then: w.Then,
				Line: clip(oneLine(workT(l, "wakes_line", i18n.Args{"wait": w.Wait, "n": e.Wakes})), 60),
				Questions: []proto.Question{{Question: workT(l, "wakes_ask", i18n.Args{"wait": w.Wait, "n": e.Wakes}), Header: workT(l, "header_wakes"),
					Options: []proto.QuestionOption{{Label: answerLabel(l, answerWait)}, {Label: answerLabel(l, answerStop)}}}}}
			break
		}
		at := now.Add(wakeAfter(w.Every, e.Wakes)).UTC()
		e.WakeAt, w.WakeAt, w.Attempts, w.Max = &at, &at, e.Wakes, maxWakes(cfg)
		return
	}
	// On the user: a card.
	if w.On != proto.WaitUser || !sw.Decide || v.isMuted(machine, c.SessionID) {
		return
	}
	if len(w.Questions) == 0 {
		w.Questions = []proto.Question{{Question: w.Line, Options: []proto.QuestionOption{{Label: answerLabel(l, answerOK)}, {Label: answerLabel(l, answerSkip)}}}}
	}
	why := ""
	if w.Link != nil {
		var done bool
		if done, why = s.relayAuth(ctx, machine, c, w); done {
			return // authorized with the user's account, and the agent told (agents_auth_relay.go)
		}
	}
	if w.Link != nil {
		w.Questions = append([]proto.Question{{Question: linkQuestion(w, s.location(ctx), l) + why, Header: workT(l, "header_auth"),
			Options: []proto.QuestionOption{{Label: answerLabel(l, answerAuthed)}, {Label: answerLabel(l, answerNotNow)}}}}, w.Questions...)
		if len(w.Questions) > 4 {
			w.Questions = w.Questions[:4]
		}
	}
	e.InteractionID = s.openCard(machine, c, *w)
	w.InteractionID = e.InteractionID
}

// wakeAfter: the first wake after checkMinutes (2–240, else 10), then each one twice as late, at most an hour (or
// checkMinutes, when that is longer).
func wakeAfter(checkMinutes, wakes int) time.Duration {
	m := checkMinutes
	if m < 2 || m > 240 {
		m = 10
	}
	d := time.Duration(m) * time.Minute * time.Duration(math.Pow(2, float64(wakes)))
	if wakes > 0 && d > time.Hour {
		d = max(time.Hour, time.Duration(m)*time.Minute)
	}
	return d
}

func linkQuestion(w *proto.WorkState, loc *time.Location, l i18n.Lang) string {
	if w.Link.ExpiresAt != nil {
		return workT(l, "auth_link_until", i18n.Args{"url": w.Link.URL, "time": w.Link.ExpiresAt.In(loc).Format("15:04")})
	}
	return workT(l, "auth_link", i18n.Args{"url": w.Link.URL})
}

// workCard is the card asking w's questions about c (shown and pushed like any question, answered with
// manager.action); its answer goes to the agent.
func workCard(machine string, c proto.Session, w proto.WorkState, id string) proto.Interaction {
	ix := proto.Interaction{
		ID: id, MachineID: machine, SessionID: c.SessionID, SessionTitle: agentLabel(c), Cwd: c.Cwd,
		Kind: proto.IxQuestion, ToolName: workCardTool, Summary: w.Line, Questions: w.Questions, CanCancel: true,
		CreatedAt: time.Now().UTC(), ExpiresAt: w.TurnAt.Add(workCardTTL).UTC(),
	}
	if c.Agent != "" && c.Agent != "claude" {
		ix.Agent, ix.AgentLabel = c.Agent, c.AgentLabel
	}
	return ix
}

// openCard puts w's questions on a card for c (the next snapshot carries it). Returns the card's id.
func (s *Service) openCard(machine string, c proto.Session, w proto.WorkState) string {
	ix := workCard(machine, c, w, workCardPrefix+randomHex(6))
	s.work.mu.Lock()
	s.work.init()
	s.work.cards[ix.ID] = ix
	s.work.mu.Unlock()
	s.workEvent(machine, c.SessionID, "asked", map[string]any{"line": w.Line, "questions": len(w.Questions)})
	return ix.ID
}

// workCardPrefix starts the id of every card the manager opens.
const workCardPrefix = "work_"

// errGone is an answer to a card or question that is not there any more (answered, withdrawn, expired): final, the
// hub does not keep it to try again.
func errGone(id string) error {
	return &rpc.Error{Code: CodeGone, Message: "no card or question " + id + " any more"}
}

// workAnswer is manager.action for one of the cards (false: id is none of them). What the agent is to hear goes to it;
// the answers the manager acts on itself ("Not now", "Wait a bit"…) do not. The work is read back first: an answer the hub kept
// while the manager was away comes right after it is back. When the agent cannot be sent the answer (its machine just
// went away) the card goes up again, under a new id. A repeated callback while an answer is being sent is answered OK
// and delivered once: the send can take until its timeout, and the entry still names the card until it comes back.
func (s *Service) workAnswer(ctx context.Context, id string, r proto.Resolution) (bool, error) {
	if !strings.HasPrefix(id, workCardPrefix) {
		return false, nil
	}
	if r.Cancel && !r.IsQuestionCancel() {
		return true, errInvalidQuestionCancel
	}
	if !s.loadWork(ctx) {
		return true, errors.New("the agents' work could not be read yet") // not final: the hub tries again
	}
	s.work.mu.Lock()
	s.work.init()
	if s.work.answering[id] { // this callback's answer is already on its way to the agent
		s.work.mu.Unlock()
		return true, nil
	}
	ix, ok := s.work.cards[id]
	delete(s.work.cards, id)
	if !ok { // read back but not up again yet (its machine was away): its turn's work still has it
		for _, e := range s.work.entries {
			if e.InteractionID == id {
				ix, ok = workCard(e.MachineID, proto.Session{SessionID: e.SessionID}, e.Work, id), true
				break
			}
		}
	}
	if ok { // claimed until the answer is delivered, so a repeated callback cannot send it again
		s.work.answering[id] = true
	}
	s.work.mu.Unlock()
	if !ok {
		return true, errGone(id)
	}
	defer func() {
		s.work.mu.Lock()
		delete(s.work.answering, id)
		s.work.mu.Unlock()
	}()
	key := [2]string{ix.MachineID, ix.SessionID}
	turn := ix.ExpiresAt.Add(-workCardTTL)
	after := time.Since(turn).Seconds()
	// done takes the card off its turn's work as the work is now (the agent may have gone on meanwhile), with change.
	done := func(change func(e *db.SessionWork)) {
		s.work.mu.Lock()
		delete(s.work.cards, ix.ID) // a tick may have put it back while the answer was on its way to the agent
		s.work.mu.Unlock()
		e, ok := s.workEntry(key)
		if ok && e.InteractionID == ix.ID {
			e.InteractionID, e.Work.InteractionID = "", ""
			if change != nil {
				change(&e)
			}
			s.putWork(ctx, e)
			return
		}
		s.publishSoon() // the card goes from the snapshot all the same
	}
	if r.Cancel || r.Dismiss {
		if err := s.deferWorkCard(ctx, ix, ""); err != nil {
			return true, err
		}
		s.workEvent(ix.MachineID, ix.SessionID, "answered", map[string]any{"after": after, "dismissed": true})
		return true, nil
	}
	type item struct{ question, answer string }
	var items []item
	only := map[string]bool{}
	for _, q := range ix.Questions {
		a := strings.TrimSpace(r.Answers[q.Question])
		if a == "" {
			continue
		}
		items = append(items, item{q.Question, a})
		if deferredWorkOption(q, a) {
			only[answerSkip] = true
		} else {
			only[canonicalAnswer(a)] = true
		}
	}
	s.workEvent(ix.MachineID, ix.SessionID, "answered", map[string]any{"after": after, "answers": len(items)})
	render := func() string {
		var out []agentAnswerItem
		for _, it := range items {
			out = append(out, agentAnswerItem{it.question, it.answer})
		}
		if r.AnsweredBy == proto.AnsweredByAssistant {
			return renderDelegatedAnswer(out, r.Reason)
		}
		return renderAnswer(out)
	}
	if !only[answerGo] {
		s.work.mu.Lock()
		delete(s.work.approved, key) // any other answer ("Don't do it yet", "Not now"…) takes an earlier yes back
		s.work.mu.Unlock()
	}
	var text string
	var sent func(e *db.SessionWork)
	switch {
	case len(items) == 0:
		done(nil)
		return true, nil
	case len(only) == 1 && (only[answerSkip] || only[answerNotNow] || only[answerStop] || only[answerHold]):
		line := workT(s.lang(ctx), "you_skipped")
		if err := s.deferWorkCard(ctx, ix, line); err != nil {
			return true, err
		}
		return true, nil
	case len(only) == 1 && only[answerWait]:
		l := s.lang(ctx)
		done(func(e *db.SessionWork) {
			at := time.Now().Add(wakeAfter(e.Work.Every, 0)).UTC()
			e.Wakes, e.WakeAt = 0, &at
			e.Work.On, e.Work.WakeAt, e.Work.Attempts, e.Work.Questions = proto.WaitExternal, &at, 0, nil
			e.Work.Line = clip(oneLine(workT(l, "waiting_on", i18n.Args{"wait": e.Work.Wait})), 60)
		})
		return true, nil
	case len(only) == 1 && (only[answerRetry] || only[answerHandled] || only[answerAuthed]):
		text, sent = continueText, func(e *db.SessionWork) { e.Retries = 0 }
	case only[answerGo]:
		if e, ok := s.workEntry(key); ok {
			s.work.mu.Lock()
			s.work.approved[key] = approval{wait: e.Work.Wait, then: e.Work.Then}
			s.work.mu.Unlock()
		}
		text = render()
	default:
		text = render()
	}
	if err := s.sendToSession(ctx, ix.MachineID, ix.SessionID, text); err != nil {
		s.logf("work %s/%s: the answer did not go, the card is up again: %v", ix.MachineID, short(ix.SessionID), err)
		s.workEvent(ix.MachineID, ix.SessionID, "answer_failed", map[string]any{"error": clip(err.Error(), 200)})
		s.work.mu.Lock()
		delete(s.work.approved, key) // the card is asked again: the yes goes with the answer that did not
		s.work.mu.Unlock()
		if e, ok := s.workEntry(key); ok && e.InteractionID == id {
			ix.ID, ix.CreatedAt = workCardPrefix+randomHex(6), time.Now().UTC()
			s.work.mu.Lock()
			delete(s.work.cards, id) // a tick may have put the old card back while the send was failing
			s.work.cards[ix.ID] = ix
			s.work.mu.Unlock()
			e.InteractionID, e.Work.InteractionID = ix.ID, ix.ID
			s.putWork(ctx, e)
		} else {
			s.publishSoon()
		}
		return true, nil
	}
	done(sent)
	return true, nil
}

type agentAnswerItem struct{ question, answer string }

// sendToSession sends text to sid as the manager (the next working is its doing, not the user's).
func (s *Service) sendToSession(ctx context.Context, machine, sid, text string) error {
	s.work.mu.Lock()
	s.work.init()
	s.work.sent[[2]string{machine, sid}] = time.Now()
	s.work.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return s.call(ctx, machine, DeviceSessionSend, map[string]any{"session_id": sid, "text": text}, nil)
}

// ---- the tick ----

// workLoop runs the tick until ctx ends.
func (s *Service) workLoop(ctx context.Context) {
	t := time.NewTicker(workEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			if s.workOn(ctx) {
				s.workTick(ctx, now)
			}
		}
	}
}

// workTick: due wakes go out, cards past their day go, turns that ended unseen while the manager was away (or whose
// triage failed a while ago) are triaged, and the number of unread sessions is sampled.
func (s *Service) workTick(ctx context.Context, now time.Time) {
	if !s.loadWork(ctx) {
		return
	}
	v := s.newWorkView(ctx)
	s.work.mu.Lock()
	s.work.init()
	var due, waits, cards [][2]string
	agents := map[[2]string]proto.WorkAgent{}
	for k, e := range s.work.entries {
		switch {
		case e.WakeAt != nil && !e.WakeAt.After(now):
			due = append(due, k)
		case e.WakeAt != nil && e.Work.On == proto.WaitAgent && e.Work.Agent != nil:
			waits, agents[k] = append(waits, k), *e.Work.Agent
		case e.InteractionID != "":
			cards = append(cards, k)
		}
	}
	sample := now.Sub(s.work.sampleAt) >= workSampleGap
	if sample {
		s.work.sampleAt = now
	}
	prune := now.Sub(s.work.pruneAt) >= 24*time.Hour
	if prune {
		s.work.pruneAt = now
	}
	backfill := now.Sub(s.work.backfillAt) >= workBackfillEvery
	if backfill {
		s.work.backfillAt = now
	}
	s.work.mu.Unlock()
	for _, k := range waits {
		if s.agentStopped(v, agents[k]) {
			due = append(due, k)
		}
	}
	for _, k := range due {
		s.workWake(ctx, v, k, now)
	}
	for _, k := range cards {
		s.cardTick(ctx, v, k, now)
	}
	if backfill {
		s.backfillWork(ctx, v, now)
	}
	if sample {
		s.sampleWork(v)
	}
	if st := s.workDB(); prune && st != nil {
		if err := st.PruneSessionWork(ctx, now.Add(-workKeep)); err != nil {
			s.logf("work: prune: %v", err)
		}
	}
}

// agentStopped: the agent a session waits for is not working any more (or is gone).
func (s *Service) agentStopped(v *workView, a proto.WorkAgent) bool {
	if _, ok := v.device(a.MachineID); !ok {
		return false // its machine is away: the fallback wake decides
	}
	c, ok := v.session(a.MachineID, a.SessionID)
	return !ok || c.Status != proto.StatusWorking
}

// wakeWaitersOn: sid on machine stopped; agents waiting for it are due now.
func (s *Service) wakeWaitersOn(machine, sid string) {
	s.work.mu.Lock()
	defer s.work.mu.Unlock()
	s.work.init()
	now := time.Now().UTC()
	for _, e := range s.work.entries {
		if a := e.Work.Agent; e.WakeAt != nil && e.Work.On == proto.WaitAgent && a != nil && a.MachineID == machine && a.SessionID == sid {
			e.WakeAt, e.Work.WakeAt = &now, &now
		}
	}
}

// workGuard: the manager may act on e now — the session is still stopped on the same turn, nothing pending or queued,
// not looked at in herdr, its machine up. ownCard: the manager's card for it may be pending.
func (s *Service) workGuard(v *workView, e db.SessionWork, ownCard bool) (proto.Session, string) {
	if d, ok := v.device(e.MachineID); !ok || !d.Reachable {
		return proto.Session{}, "offline"
	}
	c, ok := v.session(e.MachineID, e.SessionID)
	if v.failed[e.MachineID] {
		return c, "offline" // the hub could not say: nothing is decided on it now
	}
	switch {
	case !ok:
		return c, "gone"
	case c.ReadOnlyReason != "":
		return c, "read-only"
	case c.Status == proto.StatusWorking || !sameTurn(c, e.TurnAt):
		return c, "moved on"
	case c.Status == proto.StatusIdle:
		return c, "looked at in herdr"
	case c.Status != proto.StatusDone:
		return c, "not stopped"
	case len(c.Queued) > 0:
		return c, "input queued"
	case c.PendingInteractions > 0 && !(ownCard && c.PendingInteractions == 1 && e.InteractionID != ""):
		return c, "asking"
	}
	return c, ""
}

// workWake sends what is due for k: "Continue" after a transient error, or a wake for a wait.
func (s *Service) workWake(ctx context.Context, v *workView, k [2]string, now time.Time) {
	e, ok := s.workEntry(k)
	if !ok || e.WakeAt == nil {
		return
	}
	// Read again right before acting: the pass's view may be a send or two old.
	c, why := s.workGuard(s.newWorkView(ctx), e, false)
	switch why {
	case "":
	case "offline":
		at := now.Add(time.Minute).UTC() // try again when it is back
		e.WakeAt, e.Work.WakeAt = &at, &at
		s.putWork(ctx, e)
		return
	default:
		s.logf("work %s/%s: not woken: %s", k[0], short(k[1]), why)
		e.WakeAt, e.Work.WakeAt = nil, nil
		s.putWork(ctx, e)
		return
	}
	sw := s.triageSwitches(ctx)
	if e.Work.On == proto.WaitTransient && !sw.AutoContinue || e.Work.On != proto.WaitTransient && !sw.Wake {
		e.WakeAt, e.Work.WakeAt = nil, nil // turned off since it was set
		s.putWork(ctx, e)
		return
	}
	var text, kind string
	switch e.Work.On {
	case proto.WaitTransient:
		e.Retries++
		text, kind = continueText, "continued"
	case proto.WaitExternal, proto.WaitAgent:
		e.Wakes++
		wk := agentWake{Attempt: e.Wakes, Wait: e.Work.Wait, Then: e.Work.Then}
		s.work.mu.Lock()
		if a, ok := s.work.approved[k]; ok && risky(e.Work.Then) && a.covers(&e.Work) {
			wk.Approved = a.then // the user said yes to it: the wake must not tell it to ask again
		}
		s.work.mu.Unlock()
		if a := e.Work.Agent; a != nil && s.agentStopped(v, *a) {
			wk.Agent = a.Label
			if as, ok := v.session(a.MachineID, a.SessionID); ok {
				wk.AgentLine = clip(oneLine(as.LastText), 200)
			}
		}
		text, kind = renderWake(wk), "woke"
	default:
		return
	}
	e.WakeAt, e.Work.WakeAt = nil, nil
	s.putWork(ctx, e)
	if err := s.sendToSession(ctx, k[0], c.SessionID, text); err != nil {
		s.logf("work %s/%s: %s: %v", k[0], short(k[1]), kind, err)
		at := now.Add(2 * time.Minute).UTC()
		e.WakeAt, e.Work.WakeAt = &at, &at
		if kind == "continued" {
			e.Retries--
		} else {
			e.Wakes--
		}
		s.putWork(ctx, e)
		return
	}
	s.logf("work %s/%s: %s (%d)", k[0], short(k[1]), kind, max(e.Retries, e.Wakes))
	s.workEvent(k[0], k[1], kind, map[string]any{"attempt": max(e.Retries, e.Wakes), "after": now.Sub(e.TurnAt).Seconds()})
}

// cardsUp are the manager's cards the hub has up now, by id (none when it cannot say).
func (s *Service) cardsUp(ctx context.Context) map[string]proto.Interaction {
	var res struct {
		Interactions []proto.Interaction `json:"interactions"`
	}
	out := map[string]proto.Interaction{}
	if err := s.call(ctx, DeviceAll, DeviceInteractionsList, map[string]any{}, &res); err != nil {
		s.logf("work: the hub's cards: %v", err)
		return out
	}
	for _, ix := range res.Interactions {
		if ix.ToolName == workCardTool {
			out[ix.ID] = ix
		}
	}
	return out
}

// cardBack is e's card put back (its turn still waits; nil when not now), and whether e keeps it meanwhile (false: past
// its day, or the turn went on). Only the agent going on ends the card: working again, or a later turn. A card
// still waits for its answer when the session was looked at (as one the hub kept up does), and is kept for a later
// tick while its machine or session is not back as it was — right after a hub restart the nodes reconnect and report
// their sessions over the next seconds, a node that restarted too before it has read the transcripts.
func (s *Service) cardBack(v *workView, e db.SessionWork, now time.Time) (*proto.Interaction, bool) {
	if now.After(e.TurnAt.Add(workCardTTL)) || len(e.Work.Questions) == 0 {
		return nil, false
	}
	c, why := s.workGuard(v, e, true)
	switch why {
	case "", "looked at in herdr":
		ix := workCard(e.MachineID, c, e.Work, e.InteractionID)
		return &ix, true
	case "moved on":
		if wentOn(c, e) {
			return nil, false
		}
	}
	return nil, true
}

// wentOn: c went on from e's turn — it works again, or a later turn ended. A session that reads older than the turn
// (a node that has not read its transcript yet) did not.
func wentOn(c proto.Session, e db.SessionWork) bool {
	return c.Status == proto.StatusWorking || c.UpdatedAt.After(e.TurnAt.Add(2*time.Second))
}

// cardTick: k's card is past its day, or its turn went on (a turn_started the manager missed while it was away): it
// goes, and the hub withdraws it when the snapshot no longer has it. One read back and not out yet is put back out.
func (s *Service) cardTick(ctx context.Context, v *workView, k [2]string, now time.Time) {
	e, ok := s.workEntry(k)
	if !ok || e.InteractionID == "" {
		return
	}
	s.work.mu.Lock()
	_, out := s.work.cards[e.InteractionID]
	s.work.mu.Unlock()
	var drop bool
	if out {
		c, why := s.workGuard(v, e, true)
		drop = now.After(e.TurnAt.Add(workCardTTL)) || why == "moved on" && wentOn(c, e)
	} else {
		ix, keep := s.cardBack(v, e, now)
		if ix != nil {
			s.work.mu.Lock()
			current, exists := s.work.entries[k]
			if exists && current.InteractionID == ix.ID {
				s.work.cards[ix.ID] = *ix
			}
			s.work.mu.Unlock()
			s.publishSoon()
			return
		}
		drop = !keep
	}
	if !drop {
		return
	}
	s.work.mu.Lock()
	delete(s.work.cards, e.InteractionID)
	s.work.mu.Unlock()
	if !out {
		s.recordEnd(e.InteractionID, "cancelled", nil) // never put back up: nothing else ends it in the history
	}
	e.InteractionID, e.Work.InteractionID = "", ""
	s.putWork(ctx, e)
}

// backfillWork triages turns that ended unseen while the manager could not hear of them (unread, within a day), a
// few per pass.
func (s *Service) backfillWork(ctx context.Context, v *workView, now time.Time) {
	n := 0
	settle := settleOf(s.triageCfg(ctx))
	for _, d := range v.machines() {
		if !d.Reachable {
			continue
		}
		for _, c := range v.sessions(d.ID) {
			if n >= 3 {
				return
			}
			if c.Assistant || c.State != proto.StateUnread || c.Work != nil || now.Sub(c.UpdatedAt) > workBackfill ||
				now.Sub(c.UpdatedAt) < settle {
				continue
			}
			k := [2]string{d.ID, c.SessionID}
			s.work.mu.Lock()
			tried, ok := s.work.tried[k]
			s.work.mu.Unlock()
			if ok && sameTurn(c, tried.turn) && now.Sub(tried.at) < workRetryTriage {
				continue // tried on this turn a moment ago (a failed model call is not retried every pass)
			}
			n++
			go s.triageTurn(context.WithoutCancel(ctx), s.newWorkView(ctx), d.ID, c) // a model call each: the wakes do not wait
		}
	}
}

// sampleWork records how many sessions are in each state now (for the stats: "Unread" over time).
func (s *Service) sampleWork(v *workView) {
	counts := map[string]any{}
	for _, d := range v.machines() {
		for _, c := range v.sessions(d.ID) {
			if c.Assistant || c.State == proto.StateOffline {
				continue
			}
			n, _ := counts[c.State].(int)
			counts[c.State] = n + 1
		}
	}
	s.workEvent("", "", "sample", counts)
}

// httpsLink: u is an http(s) link worth putting on a card.
func httpsLink(u string) bool {
	p, err := url.Parse(strings.TrimSpace(u))
	return err == nil && (p.Scheme == "https" || p.Scheme == "http") && p.Host != ""
}

// ---- the hub's events ----

// workReconnected: the manager is connected again (or for the first time): the hub sent nothing meanwhile (a hub that
// just started serves its bus last, after its nodes came up), so the next tick looks for the turns that ended unseen.
func (s *Service) workReconnected() {
	s.work.mu.Lock()
	s.work.backfillAt = time.Time{}
	s.work.mu.Unlock()
}

// SessionSeen is session.seen: the user looked at a session in an app.
func (s *Service) SessionSeen(_ context.Context, p SessionSeen) {
	s.workSeen(p.MachineID, p.SessionID, p.At)
}
