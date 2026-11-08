package assistant

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// Supervised tasks (#3124, docs/assistant/unified-entry.md"Supervision"). A task the main conversation hands out (task_plan, or an
// agent_start that became a quiet task) has the main conversation as its supervisor: the task keeps a ledger (what
// done means, how long it should take, where its review stands) and the hub watches it without a model call. What
// needs the supervisor — the work looks finished and waits for review, an agent stopped short of it, waits for an
// answer too long, makes no progress, was cut off or did not start — becomes a signal; the signals of all tasks go to
// the main conversation as one supervision message (the quiet tasks' notices are among them), at most every few minutes, and
// the turn it starts is not pushed unless the assistant pushes (notify_user). The assistant answers with task_review:
// accepted closes the todo (its agents are closed later, tasks_close.go), rework sends the gaps back to the agent (at
// most maxReworkRounds times), needs_user waits for the user, cancelled ends it.
//
// A supervised todo is never closed by the hub's judge (done.go): a finished stretch of work waits for review instead.

const (
	reviewPending   = "pending_review"
	reviewAccepted  = "accepted"
	reviewRework    = "rework"
	reviewNeedsUser = "needs_user"
	reviewCancelled = "cancelled"
)

// maxReworkRounds is how many times a task is sent back; one more rework goes to the user instead.
const maxReworkRounds = 2

// Supervision is a supervised task's ledger, as the apps get it on the todo (supervision).
type Supervision struct {
	Supervisor    string       `json:"supervisor"` // the conversation that handed it out
	DoneWhen      []string     `json:"done_when"`
	ExpectMinutes int          `json:"expect_minutes,omitempty"`
	State         string       `json:"state"` // "" going on, or one of review*
	Round         int          `json:"round"` // reworks so far
	Reviews       []TaskReview `json:"reviews"`
	UpdatedAt     time.Time    `json:"updated_at"`
	// Progress: what its agent last reported to the supervisor (report_to_supervisor, agent_control.go). A live
	// reading kept in memory only (the next report brings it back after a restart), and apart from UpdatedAt, which
	// names the review a task waits for.
	Progress *TaskProgress `json:"progress,omitempty"`
}

// TaskProgress is a supervised task's agent's latest report: its kind, the step it is on of how many (0: not said),
// what it said in a line, and when.
type TaskProgress struct {
	Kind  string    `json:"kind"`
	Step  int       `json:"step,omitempty"`
	Steps int       `json:"steps,omitempty"`
	Text  string    `json:"text"`
	At    time.Time `json:"at"`
}

// TaskReview is one review: the assistant's verdict, with each criterion's evidence or gap.
type TaskReview struct {
	At      time.Time     `json:"at"`
	Verdict string        `json:"verdict"`
	Round   int           `json:"round,omitempty"` // the rework round it started (rework)
	Checks  []ReviewCheck `json:"checks,omitempty"`
	Note    string        `json:"note,omitempty"`
}

type ReviewCheck struct {
	Criterion string `json:"criterion"`
	Met       bool   `json:"met"`
	Evidence  string `json:"evidence,omitempty"`
}

func (v *Supervision) clone() *Supervision {
	if v == nil {
		return nil
	}
	out := *v
	out.DoneWhen = slices.Clone(v.DoneWhen)
	out.Reviews = make([]TaskReview, len(v.Reviews))
	for i, r := range v.Reviews {
		out.Reviews[i] = r
		out.Reviews[i].Checks = slices.Clone(r.Checks)
	}
	if out.DoneWhen == nil {
		out.DoneWhen = []string{}
	}
	if v.Progress != nil {
		p := *v.Progress
		out.Progress = &p
	}
	return &out
}

// state is the review state that holds now: an accepted or cancelled task the user reopened goes on again.
func (v *Supervision) state(t Todo) string {
	if v == nil {
		return ""
	}
	if t.DoneAt == nil && (v.State == reviewAccepted || v.State == reviewCancelled) {
		return ""
	}
	return v.State
}

// supervisionStore is what the ledgers keep in the database; a store without it keeps them in memory only.
type supervisionStore interface {
	ListTodoSupervision(ctx context.Context) ([]db.TodoSupervisionRow, error)
	PutTodoSupervision(ctx context.Context, r db.TodoSupervisionRow) error
	ReviewDoneTodo(ctx context.Context, id, reason string, at time.Time) (*db.TodoRow, bool, error)
}

var _ supervisionStore = (*db.DB)(nil)

func (s *Service) supervisionDB() supervisionStore {
	st, _ := s.store().(supervisionStore)
	return st
}

func supervisionFromRow(r db.TodoSupervisionRow) *Supervision {
	v := &Supervision{Supervisor: r.Supervisor, DoneWhen: r.DoneWhen, ExpectMinutes: r.ExpectMinutes, State: r.State, Round: r.Round,
		UpdatedAt: r.UpdatedAt}
	_ = json.Unmarshal(r.Reviews, &v.Reviews)
	return v.clone()
}

func (v *Supervision) row(todoID string) db.TodoSupervisionRow {
	raw, _ := json.Marshal(v.Reviews)
	return db.TodoSupervisionRow{TodoID: todoID, Supervisor: v.Supervisor, DoneWhen: v.DoneWhen, ExpectMinutes: v.ExpectMinutes,
		State: v.State, Round: v.Round, Reviews: raw, UpdatedAt: v.UpdatedAt}
}

// cleanDoneWhen is a task's criteria as kept: trimmed, no empty or repeated ones, at most 6 of at most 200 characters.
func cleanDoneWhen(list []string) []string {
	out := []string{}
	for _, c := range list {
		c = clip(oneLine(c), 200)
		if c != "" && !slices.Contains(out, c) && len(out) < 6 {
			out = append(out, c)
		}
	}
	return out
}

// superviseTodo makes todo id a supervised task of conversation supervisor.
func (s *Service) superviseTodo(ctx context.Context, id, supervisor string, doneWhen []string, expect int) {
	_, _, err := s.updateSupervision(ctx, id, func(v *Supervision) bool {
		v.Supervisor, v.DoneWhen, v.ExpectMinutes = supervisor, cleanDoneWhen(doneWhen), max(0, min(expect, 24*60))
		return true
	})
	if err != nil {
		s.logf("task %s: supervision: %v", id, err)
	}
}

var errNotSupervised = errors.New("not a supervised task")

// updateSupervision changes todo id's ledger (a new one when it has none) with change, which says whether it changed
// anything; the ledger as it is then. Writes are one at a time.
func (s *Service) updateSupervision(ctx context.Context, id string, change func(*Supervision) bool) (Supervision, bool, error) {
	b := &s.supervise
	b.writeMu.Lock()
	defer b.writeMu.Unlock()
	t, ok := s.todoNow(id)
	if !ok {
		return Supervision{}, false, db.ErrTodoNotFound
	}
	v := t.Supervision.clone()
	if v == nil {
		v = (&Supervision{}).clone()
	}
	if !change(v) {
		return *v, false, nil
	}
	v.UpdatedAt = time.Now().UTC()
	if st := s.supervisionDB(); st != nil && s.todoDB() != nil {
		if err := st.PutTodoSupervision(ctx, v.row(id)); err != nil {
			return Supervision{}, false, err
		}
	}
	s.todos.mu.Lock()
	if cur := s.todos.todos[id]; cur != nil {
		next := v.clone()
		if cur.Supervision != nil { // the progress is noteProgress's: a report that came meanwhile stays
			next.Progress = cur.Supervision.clone().Progress
		}
		cur.Supervision = next
	}
	s.todos.mu.Unlock()
	s.todosChanged(ctx)
	return *v, true, nil
}

// noteProgress keeps p as supervised task id's progress (in memory, no ledger write: see Supervision.Progress) and
// tells the apps.
func (s *Service) noteProgress(ctx context.Context, id string, p TaskProgress) {
	s.todos.mu.Lock()
	cur := s.todos.todos[id]
	ok := cur != nil && cur.Supervision != nil
	if ok {
		v := cur.Supervision.clone()
		v.Progress = &p
		cur.Supervision = v
	}
	s.todos.mu.Unlock()
	if ok {
		s.todosChanged(ctx)
	}
}

// ---- signals ----

const (
	sigReview      = "review"      // the work looks finished: review it
	sigIdle        = "idle"        // its agents stopped, and the work is not finished
	sigNeedsYou    = "needs_you"   // an agent waits for an answer
	sigStalled     = "stalled"     // working, but nothing moved for long
	sigInterrupted = "interrupted" // the model service cut its turn off
	sigFailed      = "failed"      // its agent did not start
	sigReport      = "report"      // its agent reported to the supervisor (report_to_supervisor, agent_control.go)
)

var (
	// superviseGather is how long a first signal waits for others to go out with it.
	superviseGather = 15 * time.Second
	// superviseEvery is the least time between two supervision messages; superviseUrgentEvery when one waits on a decision.
	superviseEvery       = 10 * time.Minute
	superviseUrgentEvery = 2 * time.Minute
	// superviseNeedsAfter is how long an agent waits for an answer before the supervisor hears of it (the user was
	// pushed; a quiet task's at once: the user was not).
	superviseNeedsAfter = 3 * time.Minute
	// superviseStallMin is the least time without progress that counts as stuck (or 1.5× what the task should take).
	superviseStallMin = 20 * time.Minute
	// superviseTick is how often the tasks are looked at.
	superviseTick = 30 * time.Second
	// superviseHoldMax bounds how long the main conversation's turns stay unpushed after a supervision message went in.
	superviseHoldMax = 2 * time.Hour
)

// superviseSignal is one thing about one task the supervisor should know. Key names the stretch of state it is about:
// the same signal with the same key is told once.
type superviseSignal struct {
	Todo, Kind, Key  string
	At               time.Time
	Machine, Session string
	Detail, Ask      string
	Minutes          int
	// A report's (sigReport): its kind (progress, question, blocked, done), evidence and the step it is on.
	Report      string
	Evidence    []string
	Step, Steps int
}

// id names what a newer signal replaces while it waits: the task's signal of that kind (a report: of that kind of
// report, so a progress report does not push out a question).
func (g superviseSignal) id() string {
	if g.Kind == sigReport {
		return g.Todo + "|" + g.Kind + ":" + g.Report
	}
	return g.Todo + "|" + g.Kind
}

// urgent: a signal that waits on a decision goes out sooner than the rest.
func (g superviseSignal) urgent() bool {
	switch g.Kind {
	case sigNeedsYou, sigIdle, sigFailed, sigInterrupted:
		return true
	case sigReport:
		return g.Report != upReportProgress
	}
	return false
}

type superviseBook struct {
	mu      sync.Mutex
	writeMu sync.Mutex                 // one ledger write at a time (updateSupervision)
	pending map[string]superviseSignal // by todo|kind
	sent    map[string]string          // todo|kind → the key last told
	asks    map[string]string          // todo → what its agent last asked (InteractionRaised)
	last    time.Time                  // when the last supervision message went out
	queued  []superviseSignal          // the signals of the message still waiting in the main conversation's queue
	timer   *time.Timer
	timerAt time.Time
	hold    map[string]time.Time // conversation → when a supervision message went in: its turn's finish is not pushed
}

func (b *superviseBook) init() {
	if b.pending == nil {
		b.pending, b.sent, b.asks, b.hold = map[string]superviseSignal{}, map[string]string{}, map[string]string{}, map[string]time.Time{}
	}
}

// superviseTimings are how supervision messages gather and space out (Service.superviseTimes; a zero field: the
// package's superviseGather, superviseEvery or superviseUrgentEvery).
type superviseTimings struct {
	gather, every, urgent time.Duration
}

// superviseTimings are s's own timings, the package's for each one it does not set. Tests set them on their Service,
// so that a digest timer left over from one test never reads what another test's cleanup writes.
func (s *Service) superviseTimings() superviseTimings {
	p := s.superviseTimes
	if p.gather <= 0 {
		p.gather = superviseGather
	}
	if p.every <= 0 {
		p.every = superviseEvery
	}
	if p.urgent <= 0 {
		p.urgent = superviseUrgentEvery
	}
	return p
}

// digestWait is how long until the pending signals are due (false: none): each waits p.gather after it came, and
// p.every (p.urgent for an urgent one) after the last message (last; zero: none yet).
func digestWait(now, last time.Time, pending []superviseSignal, p superviseTimings) (time.Duration, bool) {
	var due time.Time
	for _, g := range pending {
		at := g.At.Add(p.gather)
		if !last.IsZero() {
			gap := p.every
			if g.urgent() {
				gap = p.urgent
			}
			if next := last.Add(gap); next.After(at) {
				at = next
			}
		}
		if due.IsZero() || at.Before(due) {
			due = at
		}
	}
	if due.IsZero() {
		return 0, false
	}
	return max(0, due.Sub(now)), true
}

// signal records g for the supervisor, unless the same stretch of state was told already, and sees that the message
// goes out when it is due.
func (s *Service) signal(g superviseSignal) {
	if g.At.IsZero() {
		g.At = time.Now()
	}
	b := &s.supervise
	b.mu.Lock()
	b.init()
	if b.sent[g.id()] == g.Key {
		b.mu.Unlock()
		return
	}
	if cur, ok := b.pending[g.id()]; ok {
		g.At = cur.At // still waiting: it keeps its place
		if g.Ask == "" {
			g.Ask = cur.Ask
		}
		if g.Detail == "" && cur.Key == g.Key {
			g.Detail = cur.Detail // the watch's sight of what an event told already
		}
		if g.Kind == sigReport && cur.Key != g.Key {
			g = joinReports(cur, g) // two questions before the message went: both are asked
		}
	}
	b.pending[g.id()] = g
	s.scheduleDigestLocked(time.Now())
	b.mu.Unlock()
}

// scheduleDigestLocked sets the timer for when the pending signals are due; s.supervise.mu is held.
func (s *Service) scheduleDigestLocked(now time.Time) {
	b := &s.supervise
	list := make([]superviseSignal, 0, len(b.pending))
	for _, g := range b.pending {
		list = append(list, g)
	}
	wait, ok := digestWait(now, b.last, list, s.superviseTimings())
	if !ok {
		return
	}
	at := now.Add(wait)
	if b.timer != nil && !b.timerAt.After(at) {
		return // set already, for no later
	}
	if b.timer != nil {
		b.timer.Stop()
	}
	b.timerAt = at
	b.timer = time.AfterFunc(wait, func() { s.sendDigest(context.Background()) })
}

// retryDigestLocked tries the digest again in d, unless one is due sooner (b.mu held).
func (s *Service) retryDigestLocked(d time.Duration) {
	b := &s.supervise
	at := time.Now().Add(d)
	if b.timer != nil && !b.timerAt.After(at) {
		return
	}
	if b.timer != nil {
		b.timer.Stop()
	}
	b.timerAt = at
	b.timer = time.AfterFunc(d, func() { s.sendDigest(context.Background()) })
}

// watchSignals are what t and its agents show now that the supervisor should hear of, by their state alone: a task
// waiting for review (told once per review: again after a restart of the assistant, which forgot what it told), an
// agent waiting for an answer (at once for a quiet task), or working without progress for long. Nothing more for a
// task waiting for review or for the user, and nothing for one done or not supervised.
func watchSignals(t Todo, agents []proto.Session, now time.Time) []superviseSignal {
	v := t.Supervision
	if v == nil || t.DoneAt != nil {
		return nil
	}
	switch v.state(t) {
	case reviewPending:
		return []superviseSignal{{Todo: t.ID, Kind: sigReview, Key: reviewKey(v)}}
	case reviewNeedsUser:
		return nil
	}
	var out []superviseSignal
	for _, c := range agents {
		base := superviseSignal{Todo: t.ID, Machine: c.MachineID, Session: c.SessionID}
		switch {
		case c.State == proto.StateNeedsYou || c.PendingInteractions > 0:
			since := cmp.Or(c.StateSince, c.UpdatedAt)
			if wait := now.Sub(since); t.Quiet || wait >= superviseNeedsAfter {
				g := base
				g.Kind, g.Key, g.Minutes = sigNeedsYou, c.SessionID+"@"+since.UTC().Format(time.RFC3339), int(wait.Minutes())
				out = append(out, g)
			}
		case c.State == proto.StateWorking:
			moved := c.UpdatedAt
			if c.ActivityAt != nil && c.ActivityAt.After(moved) {
				moved = *c.ActivityAt
			}
			if still := now.Sub(moved); !moved.IsZero() && still >= stallAfter(v.ExpectMinutes) {
				g := base
				g.Kind, g.Key, g.Minutes = sigStalled, c.SessionID+"@"+moved.UTC().Format(time.RFC3339), int(still.Minutes())
				g.Detail = strings.TrimSpace(c.Activity)
				out = append(out, g)
			}
		}
	}
	return out
}

// reviewKey names one review a task waits for: when it began to wait.
func reviewKey(v *Supervision) string { return "review@" + v.UpdatedAt.UTC().Format(time.RFC3339Nano) }

// stallAfter is how long without progress counts as stuck for a task that should take expect minutes.
func stallAfter(expect int) time.Duration {
	return max(superviseStallMin, time.Duration(expect)*time.Minute*3/2)
}

// superviseLoop looks at the supervised tasks every superviseTick, and lets the supervision messages go out.
func (s *Service) superviseLoop(ctx context.Context) {
	t := time.NewTicker(superviseTick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		s.superviseWatch(ctx, time.Now())
	}
}

// superviseWatch records what the supervised tasks' agents show now (watchSignals), and lets go of a hold on the
// main conversation's pushes that outlived superviseHoldMax.
func (s *Service) superviseWatch(ctx context.Context, now time.Time) {
	for _, t := range s.todosOf(ctx, "*") {
		if t.Supervision == nil || t.DoneAt != nil {
			continue
		}
		for _, g := range watchSignals(t, s.taskAgents(ctx, t), now) {
			s.signal(g)
		}
	}
	b := &s.supervise
	b.mu.Lock()
	changed := false
	for id, at := range b.hold {
		if now.Sub(at) > superviseHoldMax {
			delete(b.hold, id)
			changed = true
		}
	}
	b.mu.Unlock()
	if changed {
		s.publish(ctx)
	}
}

// ---- what the task's events say ----

// taskNotice is what happened to task t (state: done, needs, interrupted, failed; ask, text as quietNotify has them):
// a supervised task's supervisor gets a signal, any other quiet task's conversation its notice.
func (s *Service) taskNotice(ctx context.Context, t Todo, machine, state, ask, text string) {
	if t.Supervision == nil {
		if !t.Quiet {
			if state == "failed" && t.Origin != "" {
				s.notifyTaskFailed(ctx, t, machine, text)
			}
			return
		}
		s.quietNotify(ctx, t, machine, state, ask, text)
		return
	}
	g := superviseSignal{Todo: t.ID, Machine: machine, Detail: text, At: time.Now()}
	switch state {
	case "failed":
		g.Kind, g.Key = sigFailed, text
	case "interrupted":
		g.Kind, g.Key = sigInterrupted, time.Now().UTC().Format(time.RFC3339)
	case "needs":
		b := &s.supervise
		b.mu.Lock()
		b.init()
		b.asks[t.ID] = ask
		b.mu.Unlock()
		if !t.Quiet {
			return // the user was pushed: the supervisor hears of it if it waits (watchSignals)
		}
		g.Kind, g.Key, g.Ask, g.Detail = sigNeedsYou, "ask@"+time.Now().UTC().Format(time.RFC3339), ask, ""
	default:
		return // done: the judge's verdict says what follows (superviseJudged)
	}
	s.signal(g)
}

// superviseJudged: supervised task t's agents all stopped after work since work, and the judge said c (err: it could
// not). Finished: the task waits for review. Not finished: the supervisor hears it stopped short.
func (s *Service) superviseJudged(ctx context.Context, t Todo, work time.Time, c taskDoneCheck, err error) {
	st := t.Supervision.state(t)
	key := work.UTC().Format(time.RFC3339Nano)
	if err == nil && c.Verdict == verdictDone {
		v, _, uerr := s.updateSupervision(ctx, t.ID, func(v *Supervision) bool {
			if v.state(t) == reviewPending {
				return false
			}
			v.State = reviewPending
			return true
		})
		if uerr != nil {
			s.logf("task %s: pending review: %v", t.ID, uerr)
			return
		}
		s.logf("task %s: waits for review: %s", t.ID, c.Reason)
		s.signal(superviseSignal{Todo: t.ID, Kind: sigReview, Key: reviewKey(&v), Detail: c.Reason})
		return
	}
	if st == reviewNeedsUser {
		return // the supervisor knows it waits for the user
	}
	detail := "hub 没判断出来是不是做完了"
	if err == nil {
		detail = verdictWords[c.Verdict]
		if c.Reason != "" {
			detail += "：" + c.Reason
		}
		if d := c.Delivery; c.Verdict == verdictUndelivered && d.held() { // where it is held, to chase (delivery.go)
			detail += "（" + deliveryLabels[d.State]
			if d.Gate != "" {
				detail += "，卡在「" + d.Gate + "」"
			}
			detail += "）"
		}
	}
	s.signal(superviseSignal{Todo: t.ID, Kind: sigIdle, Key: key, Detail: detail})
}

var verdictWords = map[string]string{verdictNotDone: "没做完", verdictWaiting: "在等别人或别的事", verdictNeedsUser: "要用户决定",
	verdictUnsure: "看不出做没做完", verdictUndelivered: "做完了但还没上线"}

// supervisedWorking: one of supervised task t's agents started a turn: a review waiting is moot (the work goes on).
func (s *Service) supervisedWorking(ctx context.Context, t Todo) {
	if t.Supervision.state(t) != reviewPending {
		return
	}
	if _, _, err := s.updateSupervision(ctx, t.ID, func(v *Supervision) bool {
		if v.State != reviewPending {
			return false
		}
		v.State = ""
		if v.Round > 0 {
			v.State = reviewRework
		}
		return true
	}); err != nil {
		s.logf("task %s: supervision: %v", t.ID, err)
	}
	b := &s.supervise
	b.mu.Lock()
	delete(b.pending, t.ID+"|"+sigReview)
	b.mu.Unlock()
}

// ---- the supervision message ----

// superviseTool marks a queued supervision message (relayMsg.Tool).
const superviseTool = "supervise"

// sendDigest puts the due signals into the main conversation as one supervision message: now, or queued behind its turn (a
// message still queued takes these in too). A signal that no longer holds (answered, done, reviewed meanwhile) is
// dropped.
func (s *Service) sendDigest(ctx context.Context) {
	b := &s.supervise
	b.mu.Lock()
	b.timer, b.timerAt = nil, time.Time{}
	list := make([]superviseSignal, 0, len(b.pending))
	for _, g := range b.pending {
		list = append(list, g)
	}
	now := time.Now()
	if wait, ok := digestWait(now, b.last, list, s.superviseTimings()); !ok || wait > time.Second {
		if ok {
			s.scheduleDigestLocked(now)
		}
		b.mu.Unlock()
		return
	}
	b.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	here := s.settings(ctx)
	if here.Main == "" || here.MachineID == "" {
		// Nowhere to tell them now (no main conversation yet, or no machine: the hub's hello while the bus is down).
		// They stay pending and untold, and go out on a later try (#5420).
		retry := s.superviseTimings().urgent
		s.logf("supervise: no main conversation; trying again in %s", retry)
		b.mu.Lock()
		s.retryDigestLocked(retry)
		b.mu.Unlock()
		return
	}
	b.mu.Lock()
	list = list[:0]
	for _, g := range b.pending { // as they are now: one may have come or gone meanwhile
		list = append(list, g)
	}
	if len(list) == 0 {
		b.mu.Unlock()
		return
	}
	b.pending = map[string]superviseSignal{}
	for _, g := range list {
		b.sent[g.id()] = g.Key
	}
	b.last = now
	b.mu.Unlock()

	var entries []prompts.SuperviseEntry
	var kept []superviseSignal
	web := s.webBase(ctx)
	for _, g := range list {
		if e, ok := s.digestEntry(ctx, g, web); ok {
			entries = append(entries, e)
			kept = append(kept, g)
		}
	}
	if len(entries) == 0 {
		return
	}
	b.mu.Lock()
	queued := b.queued
	b.mu.Unlock()
	msg := relayMsg{From: superviseTool, FromTitle: "督办", Tool: superviseTool, At: now.UTC()}
	if len(queued) > 0 {
		// The last message may still wait in the queue: this one takes its place with both.
		all := mergeSignals(queued, kept)
		var allEntries []prompts.SuperviseEntry
		for _, g := range all {
			if e, ok := s.digestEntry(ctx, g, web); ok {
				allEntries = append(allEntries, e)
			}
		}
		msg.Text = prompts.RenderSupervise(prompts.Supervise{Entries: sortEntries(allEntries)})
		if s.replaceQueuedSupervise(here.Main, msg) {
			b.mu.Lock()
			b.queued = all
			b.mu.Unlock()
			s.publish(ctx)
			return
		}
	}
	msg.Text = prompts.RenderSupervise(prompts.Supervise{Entries: sortEntries(entries)})
	b.mu.Lock()
	b.queued = nil
	b.mu.Unlock()
	s.logf("supervise: %d signal(s) to the main conversation", len(entries))
	c, ok := s.session(ctx, here.MachineID, here.Main)
	if !ok || c.Status == proto.StatusWorking || c.Starting || s.queuedFor(here.Main) > 0 {
		s.queueRelay(here.Main, msg)
		b.mu.Lock()
		b.queued = kept
		b.mu.Unlock()
		s.publish(ctx)
		if ok && c.Status != proto.StatusWorking && !c.Starting {
			s.drainRelays(here.MachineID, []proto.Session{c})
		}
		return
	}
	if err := s.deliverRelay(ctx, here.MachineID, here.Main, msg); err != nil {
		s.logf("supervise: %v; queued", err)
		s.queueRelay(here.Main, msg)
		b.mu.Lock()
		b.queued = kept
		b.mu.Unlock()
		s.publish(ctx)
	}
}

// mergeSignals is a with b's signals over its own (one per task and kind), in a's order then b's.
func mergeSignals(a, b []superviseSignal) []superviseSignal {
	out := slices.Clone(a)
	for _, g := range b {
		if i := slices.IndexFunc(out, func(x superviseSignal) bool { return x.id() == g.id() }); i >= 0 {
			if g.Kind == sigReport && out[i].Key != g.Key {
				g = joinReports(out[i], g)
			}
			out[i] = g
		} else {
			out = append(out, g)
		}
	}
	return out
}

// replaceQueuedSupervise: a supervision message still waits in the queue: msg takes its place (true).
func (s *Service) replaceQueuedSupervise(to string, msg relayMsg) bool {
	b := &s.relays
	b.mu.Lock()
	defer b.mu.Unlock()
	if !s.relayLocked(context.Background()) {
		return false
	}
	q := b.st.Queue[to]
	for i := range q {
		if q[i].Tool == superviseTool && (i > 0 || !b.draining[to]) {
			q[i] = msg
			s.saveRelayLocked()
			return true
		}
	}
	return false
}

// digestEntry is signal g as the message lists it; false when it no longer holds.
func (s *Service) digestEntry(ctx context.Context, g superviseSignal, web string) (prompts.SuperviseEntry, bool) {
	t, ok := s.todoNow(g.Todo)
	if !ok || t.Supervision == nil || t.DoneAt != nil && g.Kind != sigFailed {
		return prompts.SuperviseEntry{}, false
	}
	v := t.Supervision
	st := v.state(t)
	switch g.Kind {
	case sigReview:
		if st != reviewPending {
			return prompts.SuperviseEntry{}, false
		}
	case sigNeedsYou:
		if !s.taskWaits(ctx, t) {
			return prompts.SuperviseEntry{}, false // answered meanwhile
		}
	}
	e := prompts.SuperviseEntry{ID: t.ID, Title: t.Title, Kind: g.Kind, Detail: g.Detail, Minutes: g.Minutes, DoneWhen: v.DoneWhen,
		Round: v.Round, Link: todoLink(web, t.ID), Quiet: t.Quiet}
	if g.Kind == sigReport { // what it said is the report itself, not a reason in brackets
		e.Detail, e.Report, e.Said, e.Evidence, e.Step, e.Steps = "", g.Report, g.Detail, g.Evidence, g.Step, g.Steps
	}
	b := &s.supervise
	b.mu.Lock()
	e.Ask = cmp.Or(g.Ask, b.asks[t.ID])
	b.mu.Unlock()
	if g.Kind != sigNeedsYou {
		e.Ask = ""
	}
	// Where its agent is and what it said last: the whole last answer when it stopped, a line while it works.
	agents := s.taskAgents(ctx, t)
	var last proto.Session
	for _, c := range agents {
		if g.Session != "" && c.SessionID == g.Session || g.Session == "" && c.UpdatedAt.After(last.UpdatedAt) {
			last = c
		}
	}
	if last.SessionID != "" {
		e.Session = short(last.SessionID)
		if m, ok := s.device(ctx, last.MachineID); ok {
			e.Machine = m.Name
		}
		text := strings.TrimSpace(last.LastText)
		if g.Kind == sigReport {
			text = "" // it said what it had to in the report
		} else if g.Kind == sigReview || g.Kind == sigIdle {
			if _, answer := s.lastTurn(ctx, last.MachineID, last.SessionID); answer != "" {
				text = answer
			}
		}
		e.Last = clipMiddle(text, 600)
	} else if g.Machine != "" {
		e.Machine = g.Machine
	}
	return e, true
}

// sortEntries puts what waits on a decision first.
func sortEntries(list []prompts.SuperviseEntry) []prompts.SuperviseEntry {
	rank := map[string]int{sigNeedsYou: 0, sigIdle: 2, sigFailed: 3, sigInterrupted: 4, sigReview: 5, sigStalled: 6, sigReport: 7}
	at := func(e prompts.SuperviseEntry) int {
		switch {
		case e.Kind != sigReport:
		case e.Report == upReportQuestion || e.Report == upReportBlocked: // waits on a decision too
			return 1
		case e.Report == upReportDone: // with the work waiting for review
			return rank[sigReview]
		}
		return rank[e.Kind]
	}
	sort.SliceStable(list, func(i, j int) bool { return at(list[i]) < at(list[j]) })
	return list
}

// ---- the main conversation's pushes ----

// holdPushes: a supervision message went into conversation id: its turn's normal finish is not pushed (the hub and the
// channels read it off the snapshot, SessionView.Supervising) until the user or anything else speaks there.
func (s *Service) holdPushes(ctx context.Context, id string) {
	b := &s.supervise
	b.mu.Lock()
	b.init()
	b.hold[id] = time.Now()
	b.mu.Unlock()
	s.publish(ctx)
}

// releasePushes: someone else speaks in conversation id: its turns are pushed as ever.
func (s *Service) releasePushes(ctx context.Context, id string) {
	b := &s.supervise
	b.mu.Lock()
	_, held := b.hold[id]
	delete(b.hold, id)
	b.mu.Unlock()
	if held {
		s.publish(ctx)
	}
}

// supervising: conversation id's current turn was started by a supervision message.
func (s *Service) supervising(id string) bool {
	b := &s.supervise
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.hold[id]
	return ok
}

func (s *Service) heldConversations() []string {
	b := &s.supervise
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, 0, len(b.hold))
	for id := range b.hold {
		out = append(out, id)
	}
	return out
}

// ---- the assistant's task_review tool ----

func (s *Service) toolTaskReview(ctx context.Context, self string, args json.RawMessage) (string, error) {
	var a struct {
		TodoID  string        `json:"todo_id"`
		Verdict string        `json:"verdict"`
		Checks  []ReviewCheck `json:"checks"`
		Note    string        `json:"note"`
		// The conclusion card's (conclusion.go): accepted's outcome (done, or cannot be done), its one sentence of why and
		// its one link; needs_user's recommended option.
		Outcome   string `json:"outcome"`
		Reason    string `json:"reason"`
		Link      string `json:"link"`
		Recommend string `json:"recommend"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("arguments: %v", err)
	}
	a.Note = strings.TrimSpace(a.Note)
	switch a.Verdict {
	case reviewAccepted, reviewRework, reviewNeedsUser, reviewCancelled:
	default:
		return "", fmt.Errorf("verdict must be accepted, rework, needs_user or cancelled")
	}
	switch a.Outcome = strings.TrimSpace(a.Outcome); a.Outcome {
	case "", outcomeDone, outcomeCannot:
	default:
		return "", fmt.Errorf("outcome must be done or cannot")
	}
	if a.Verdict == reviewRework && a.Note == "" && !slices.ContainsFunc(a.Checks, func(c ReviewCheck) bool { return !c.Met }) {
		return "", fmt.Errorf("rework: say what is missing (a check that is not met, or note)")
	}
	if err := s.ensureTodosLoaded(ctx); err != nil {
		return "", err
	}
	t, ok := s.todoNow(strings.TrimSpace(a.TodoID))
	if !ok {
		return "", fmt.Errorf("no task %q", a.TodoID)
	}
	// The main conversation may review any supervised task: after the main conversation changes, the old tasks'
	// supervision messages come to the new one, which has to be able to close them (#4262).
	isMain := s.isMain(ctx, self)
	if t.Origin == "" || t.Origin != self && (t.Supervision == nil || t.Supervision.Supervisor != self && !isMain) {
		return "", fmt.Errorf("task %s was not handed out from this conversation", t.ID)
	}
	if t.DoneAt != nil && (a.Verdict == reviewRework || a.Verdict == reviewNeedsUser) {
		return "", fmt.Errorf("task %s is done already (%s): hand out a new task for what is left", t.ID, cmp.Or(t.DoneReason, "closed"))
	}
	checks := make([]ReviewCheck, 0, len(a.Checks))
	for _, c := range a.Checks {
		c.Criterion, c.Evidence = clip(oneLine(c.Criterion), 200), clip(strings.TrimSpace(c.Evidence), 600)
		if c.Criterion != "" {
			checks = append(checks, c)
		}
	}
	verdict, note := a.Verdict, clip(a.Note, 2000)
	// Only the main conversation supervises (task_plan, quietAgentTask): another conversation's task without a ledger
	// gets none here either, or its signals would go into the main conversation's supervision messages (#4262). It can still
	// be closed; rework and needs_user are what supervision is for.
	ledger := t.Supervision != nil || isMain
	if !ledger && (verdict == reviewRework || verdict == reviewNeedsUser) {
		return "", fmt.Errorf("task %s is not supervised: send what is missing to its agent with agent_send, or ask the user in your answer", t.ID)
	}
	over := false
	var v Supervision
	var err error
	if ledger {
		v, _, err = s.updateSupervision(ctx, t.ID, func(v *Supervision) bool {
			if isMain {
				v.Supervisor = self // the main conversation takes over an old main conversation's task
			} else if v.Supervisor == "" {
				v.Supervisor = t.Origin
			}
			if verdict == reviewRework && v.Round >= maxReworkRounds {
				verdict, over = reviewNeedsUser, true
			}
			r := TaskReview{At: time.Now().UTC(), Verdict: verdict, Checks: checks, Note: note}
			if verdict == reviewRework {
				v.Round++
				r.Round = v.Round
			}
			v.State = verdict
			v.Reviews = append(v.Reviews, r)
			if len(v.Reviews) > 20 {
				v.Reviews = v.Reviews[len(v.Reviews)-20:]
			}
			return true
		})
	}
	if err != nil {
		return "", err
	}
	b := &s.supervise
	b.mu.Lock()
	b.init()
	for _, k := range []string{sigReview, sigIdle, sigNeedsYou, sigStalled} {
		delete(b.pending, t.ID+"|"+k) // reviewed: what waited is answered
	}
	b.mu.Unlock()
	_, _ = s.appendFeed(ctx, t.Origin, feedReview, t.ID, reviewFeedBody{Title: t.Title, Verdict: verdict, Round: v.Round, Note: note, Checks: checks})
	if verdict != reviewNeedsUser && verdict != reviewAccepted {
		s.dropDecision(ctx, t.ID) // the review moved on: a decision still waiting on the owner is over (conclusion.go)
	}
	conclusion := conclusionInput{Outcome: cmp.Or(a.Outcome, outcomeDone), Reason: a.Reason, Link: a.Link, Note: note, Checks: checks}
	switch verdict {
	case reviewAccepted, reviewCancelled:
		reason := cmp.Or(clip(oneLine(note), 80), "验收通过")
		if verdict == reviewCancelled {
			reason = "取消：" + cmp.Or(clip(oneLine(note), 80), "不做了")
		}
		carded := false
		if t.DoneAt == nil {
			closed, err := s.reviewDoneTodo(ctx, t.ID, reason, verdict == reviewAccepted)
			if err != nil {
				return "", err
			}
			switch {
			case closed && ledger && verdict == reviewAccepted:
				// The conclusion is the one push about it: its card, not a line in the task report (#1226).
				s.dropTaskReport(t.ID)
				if cur, ok := s.todoNow(t.ID); ok {
					carded = s.askConclusion(ctx, cur, conclusion)
				}
			case closed:
				s.taskClosedReport(t, reason) // the bot's task report says it is closed, as for the judge's (reports.go)
			}
		}
		if verdict == reviewCancelled {
			return fmt.Sprintf("Task %s is cancelled and closed; its agent is closed once it stays still. Tell the user only if they need to know.", t.ID), nil
		}
		if carded {
			return fmt.Sprintf("Task %s is accepted and closed; its agent is closed once it stays still. Its conclusion card (the verdict, "+
				"why in a sentence, the link) went to the user in Lark: don't push the result again. Hand out or schedule anything that "+
				"follows (deploy, TestFlight, closing an issue).", t.ID), nil
		}
		return fmt.Sprintf("Task %s is accepted and closed; its agent is closed once it stays still. Tell the user the result in a line or two "+
			"if they are waiting for it, and hand out or schedule anything that follows (deploy, TestFlight, closing an issue).", t.ID), nil
	case reviewNeedsUser:
		conclusion.Outcome, conclusion.Recommend = outcomeDecide, a.Recommend
		if over && a.Reason == "" { // the note is the rework asked for, written to the agent: not the owner's question
			conclusion.Reason, conclusion.Note = i18n.T(s.botLang(ctx), "assistant-reports.verdict_over", i18n.Args{"rounds": maxReworkRounds}), ""
		}
		if s.askConclusion(ctx, t, conclusion) {
			said := "a card with what they are to decide went to them in Lark, and their answer goes to the task's agent"
			if conclusion.Recommend != "" {
				said = "a card with what they are to decide and your recommendation went to them in Lark; \"OK\" takes the " +
					"recommendation, and their answer goes to the task's agent"
			}
			if over {
				return fmt.Sprintf("Task %s was sent back %d times already: it now waits for the user (needs_user): %s. Don't ask them "+
					"again; review it once the agent stops.", t.ID, maxReworkRounds, said), nil
			}
			return fmt.Sprintf("Task %s waits for the user: %s. Don't ask them again; review it once the agent stops.", t.ID, said), nil
		}
		if over {
			return fmt.Sprintf("Task %s was sent back %d times already: it now waits for the user (needs_user). Tell the user what is "+
				"still missing and ask how to go on (ask_user or in your answer); review it again once they decide (that also ends the decision "+
				"the briefing lists for it).", t.ID, maxReworkRounds), nil
		}
		return fmt.Sprintf("Task %s waits for the user. Ask them (ask_user, or in your answer, pushed with notify_user if they are away); "+
			"review it again once they decide (that also ends the decision the briefing lists for it).", t.ID), nil
	}
	text := prompts.RenderSuperviseRework(prompts.SuperviseRework{Round: v.Round, Max: maxReworkRounds, Checks: reworkChecks(checks), Note: note})
	sent, err := s.sendToTask(ctx, t, text)
	if err != nil {
		return "", fmt.Errorf("recorded rework round %d, but sending it to the agent failed: %v (send it with agent_send)", v.Round, err)
	}
	return fmt.Sprintf("Rework round %d of %d is %s. When the agent stops again you get a 督办 message to review it again.", v.Round,
		maxReworkRounds, sent), nil
}

func reworkChecks(list []ReviewCheck) []prompts.SuperviseCheck {
	out := make([]prompts.SuperviseCheck, 0, len(list))
	for _, c := range list {
		out = append(out, prompts.SuperviseCheck{Criterion: c.Criterion, Met: c.Met, Evidence: c.Evidence})
	}
	return out
}

// feedReview is a review in the conversation's feed: ref the todo id, body reviewFeedBody.
const feedReview = "review"

type reviewFeedBody struct {
	Title   string        `json:"title"`
	Verdict string        `json:"verdict"`
	Round   int           `json:"round,omitempty"`
	Note    string        `json:"note,omitempty"`
	Checks  []ReviewCheck `json:"checks,omitempty"`
}

// reviewDoneTodo closes todo id after its review (done_by review, with reason); false when it was done already. An
// accepted task's outcome is recorded, as when it closes by itself or the user closes it (todo_outcome.go, #2820); a
// cancelled one came to nothing and records none.
func (s *Service) reviewDoneTodo(ctx context.Context, id, reason string, accepted bool) (bool, error) {
	s.todos.writing.Lock() // memory takes the rows in the order the store committed them (todoBook.writing)
	now := time.Now().UTC()
	var row db.TodoRow
	if st := s.supervisionDB(); st != nil && s.todoDB() != nil {
		r, ok, err := st.ReviewDoneTodo(ctx, id, reason, now)
		if err != nil || !ok {
			s.todos.writing.Unlock()
			return false, err
		}
		row = *r
	} else {
		t, ok := s.todoNow(id)
		if !ok {
			s.todos.writing.Unlock()
			return false, db.ErrTodoNotFound
		}
		row = db.TodoRow{ID: t.ID, Title: t.Title, Notes: t.Notes, CreatedAt: t.CreatedAt, Origin: t.Origin, PlanID: t.PlanID,
			ReopenedAt: t.ReopenedAt, Quiet: t.Quiet}
		row.DoneAt, row.DoneBy, row.DoneReason, row.UpdatedAt = &now, db.DoneByReview, reason, now
	}
	var before Todo
	_, doneKeys, ok := s.setTodoRow(id, row, func(t *Todo) bool {
		if t.DoneAt == nil || s.supervisionDB() != nil {
			before = copyTodoLocked(t) // its agents as they worked on it, before done releases them
			return true
		}
		return false
	})
	s.todos.writing.Unlock()
	if ok {
		s.todoRowSet(ctx, doneKeys)
	}
	if ok && accepted && row.DoneAt != nil {
		s.goRecordTaskOutcome(before, *row.DoneAt)
	}
	return ok, nil
}

// pickTaskAgent is the agent of a task that most needs what is sent to it: one waiting for the user, then working,
// then done unseen, then the latest.
func pickTaskAgent(agents []proto.Session) proto.Session {
	rank := map[string]int{proto.StateNeedsYou: 0, proto.StateWorking: 1, proto.StateUnread: 2}
	list := slices.Clone(agents)
	sort.SliceStable(list, func(i, j int) bool {
		ri, ok := rank[list[i].State]
		if !ok {
			ri = 3
		}
		rj, ok := rank[list[j].State]
		if !ok {
			rj = 3
		}
		if ri != rj {
			return ri < rj
		}
		return list[i].UpdatedAt.After(list[j].UpdatedAt)
	})
	return list[0]
}

// sendToTask sends text to task t's agent (pickTaskAgent) and keeps it in the conversation's feed, as a message on the
// task's card does; an agent closed after a stretch of work is resumed first (that can take minutes: it goes on in
// the background). What it says about it: "sent to the agent" or "being sent".
func (s *Service) sendToTask(ctx context.Context, t Todo, text string) (string, error) {
	agents := s.taskAgents(ctx, t)
	if len(agents) == 0 {
		return "", errors.New("the task has no agent")
	}
	c := pickTaskAgent(agents)
	if _, ok := s.device(ctx, c.MachineID); !ok {
		return "", errors.New("its machine is offline")
	}
	send := func(ctx context.Context) error {
		if err := s.call(ctx, c.MachineID, DeviceSessionSend, map[string]any{"session_id": c.SessionID, "text": text}, nil); err != nil {
			return err
		}
		_, err := s.appendFeed(ctx, t.Origin, feedRouted, t.ID, map[string]string{"text": text})
		return err
	}
	if c.Status == proto.StatusOffline {
		go func() {
			sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
			defer cancel()
			if err := send(sctx); err != nil {
				s.logf("task %s: rework: %v", t.ID, err)
			}
		}()
		return "being sent: its agent was closed and is resuming first", nil
	}
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 90*time.Second)
	defer cancel()
	if err := send(sctx); err != nil {
		return "", err
	}
	return "sent to the agent", nil
}

// ---- the snapshot ----

// taskHealth is how supervised task t stands, for the snapshot: a label and a rank (lower: the more it needs the
// assistant). "" for a task that is not supervised.
func taskHealth(t Todo, agents []proto.Session, now time.Time) (string, int) {
	v := t.Supervision
	if v == nil {
		return "", 5
	}
	switch v.state(t) {
	case reviewNeedsUser:
		return "等你决定", 0
	case reviewPending:
		return "待验收", 1
	case reviewAccepted:
		return "已验收", 8
	case reviewCancelled:
		return "已取消", 8
	}
	if t.DoneAt != nil {
		return "", 8
	}
	for _, c := range agents {
		if c.State == proto.StateNeedsYou || c.PendingInteractions > 0 {
			if wait := now.Sub(cmp.Or(c.StateSince, c.UpdatedAt)); wait >= time.Minute {
				return "等回答 " + humanDuration(wait), 0
			}
			return "等回答", 0
		}
	}
	for _, c := range agents {
		if c.State != proto.StateWorking {
			continue
		}
		moved := c.UpdatedAt
		if c.ActivityAt != nil && c.ActivityAt.After(moved) {
			moved = *c.ActivityAt
		}
		if still := now.Sub(moved); !moved.IsZero() && still >= stallAfter(v.ExpectMinutes) {
			return "卡住 " + humanDuration(still), 2
		}
	}
	if v.state(t) == reviewRework {
		return fmt.Sprintf("返工第 %d 轮", v.Round), 3
	}
	return "正常", 5
}

// ---- duplicates ----

// duplicateHints are the tasks going on and the agents at work that look like the same work as task t: a shared
// issue / PR number, or the same machine and directory with a similar title, or a very similar title anywhere.
func duplicateHints(t PlanTask, todos []Todo, sessions []proto.Session) []string {
	refs := issueRefs(t.Title + " " + t.Prompt)
	var out []string
	seen := map[string]bool{}
	add := func(key, line string) {
		if !seen[key] && len(out) < 4 {
			seen[key] = true
			out = append(out, line)
		}
	}
	for _, o := range todos {
		if o.DoneAt != nil {
			continue
		}
		sameDir := false
		for _, a := range o.Agents {
			if a.ReleasedAt == nil && a.DismissedAt == nil && a.MachineID == t.Machine && dirsOverlap(a.Cwd, t.Cwd) {
				sameDir = true
			}
		}
		if shared := sharedRef(refs, issueRefs(o.Title+" "+o.Notes)); shared != "" {
			add("todo:"+o.ID, fmt.Sprintf("任务「%s」（%s）也提到 %s", o.Title, o.ID, shared))
		} else if sim := titleSimilarity(t.Title, o.Title); sim >= 0.7 || sameDir && sim >= 0.4 {
			add("todo:"+o.ID, fmt.Sprintf("任务「%s」（%s）标题相近%s", o.Title, o.ID, map[bool]string{true: "，在同一个目录", false: ""}[sameDir]))
		}
	}
	for _, c := range sessions {
		if c.Assistant || c.State != proto.StateWorking && c.State != proto.StateNeedsYou {
			continue
		}
		title := cmp.Or(c.Title, c.LastText)
		where := fmt.Sprintf("%s 上的 Agent「%s」（session_id=%s）", c.MachineID, clip(oneLine(title), 40), short(c.SessionID))
		sameDir := c.MachineID == t.Machine && dirsOverlap(c.Cwd, t.Cwd)
		if shared := sharedRef(refs, issueRefs(c.Title+" "+c.LastText)); shared != "" {
			add("agent:"+c.MachineID+"/"+c.SessionID, where+"也在做 "+shared)
		} else if sim := titleSimilarity(t.Title, c.Title); sim >= 0.7 || sameDir && sim >= 0.4 {
			add("agent:"+c.MachineID+"/"+c.SessionID, where+"在做相近的事")
		}
	}
	return out
}

// issueRefs are the issue / PR numbers s names: #123, issues/123, pull/123 (three digits or more: not a list's #1).
func issueRefs(s string) []string {
	var out []string
	for i := 0; i < len(s); i++ {
		var rest string
		switch {
		case s[i] == '#':
			rest = s[i+1:]
		case strings.HasPrefix(s[i:], "issues/"):
			rest = s[i+len("issues/"):]
		case strings.HasPrefix(s[i:], "pull/"):
			rest = s[i+len("pull/"):]
		default:
			continue
		}
		n := 0
		for n < len(rest) && rest[n] >= '0' && rest[n] <= '9' {
			n++
		}
		if n >= 3 && n <= 7 {
			ref := "#" + rest[:n]
			if !slices.Contains(out, ref) {
				out = append(out, ref)
			}
		}
	}
	return out
}

func sharedRef(a, b []string) string {
	for _, x := range a {
		if slices.Contains(b, x) {
			return x
		}
	}
	return ""
}

// dirsOverlap: one directory is the other or inside it.
func dirsOverlap(a, b string) bool {
	a, b = strings.TrimRight(a, "/"), strings.TrimRight(b, "/")
	if a == "" || b == "" {
		return false
	}
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

// titleSimilarity is how alike two short titles are: the Dice coefficient of their character bigrams (spaces and
// punctuation dropped, letters folded), 0 to 1.
func titleSimilarity(a, b string) float64 {
	ga, gb := bigrams(a), bigrams(b)
	if len(ga) == 0 || len(gb) == 0 {
		return 0
	}
	common := 0
	left := map[string]int{}
	for _, g := range gb {
		left[g]++
	}
	for _, g := range ga {
		if left[g] > 0 {
			left[g]--
			common++
		}
	}
	return 2 * float64(common) / float64(len(ga)+len(gb))
}

func bigrams(s string) []string {
	var r []rune
	for _, c := range strings.ToLower(s) {
		if c == ' ' || c == '\t' || utf8.RuneLen(c) == 1 && !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9') ||
			strings.ContainsRune("，。、：；！？「」（）《》【】·…—", c) {
			continue
		}
		r = append(r, c)
	}
	out := make([]string, 0, len(r))
	for i := 0; i+1 < len(r); i++ {
		out = append(out, string(r[i:i+2]))
	}
	return out
}

// planDuplicates is what the task_plan tool adds to its answer about the plan's tasks that look like work already
// going on (duplicateHints); "" when none does. A hint, not a refusal: the plan goes on as ever.
func (s *Service) planDuplicates(ctx context.Context, tasks []PlanTask) string {
	var open []Todo
	if s.ensureTodosLoaded(ctx) == nil {
		own := map[string]bool{} // a todo a task runs on is that task, not a copy of it
		for _, t := range tasks {
			if t.ReuseTodo != "" {
				own[t.ReuseTodo] = true
			}
		}
		s.todos.mu.RLock()
		for _, t := range s.todos.todos {
			if t.DoneAt == nil && !own[t.ID] {
				open = append(open, copyTodoLocked(t))
			}
		}
		s.todos.mu.RUnlock()
	}
	var sessions []proto.Session
	for _, d := range s.devices(ctx) {
		if !d.Reachable {
			continue
		}
		list, err := s.sessions(ctx, d.ID)
		if err != nil {
			continue
		}
		for _, c := range list {
			if c.MachineID == "" {
				c.MachineID = d.ID
			}
			sessions = append(sessions, c)
		}
	}
	var b strings.Builder
	for _, t := range tasks {
		hints := duplicateHints(t, open, sessions)
		if len(hints) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n- 「%s」可能和这些重复：%s", t.Title, strings.Join(hints, "；"))
	}
	if b.Len() == 0 {
		return ""
	}
	return "\n\nPossible duplicates (work already going on): look at them (agent_read, the todo) before this goes on; if it is the " +
		"same work, cancel the new task (task_review cancelled, or dismiss the plan) or send what is new to the agent already on it." + b.String()
}

// notifyTaskFailed pushes a failure notification to the user's phone when a task started from a side conversation
// (neither supervised nor quiet) could not start, so the user is informed with next steps (#5081).
// Muted conversations send nothing; health conversations omit the task title.
func (s *Service) notifyTaskFailed(ctx context.Context, t Todo, machine, reason string) {
	here := s.settings(ctx).MachineID
	if here == "" {
		here = machine
	}
	if t.Origin == "" || s.muted(here, t.Origin) {
		return
	}
	l := s.lang(ctx)
	title := i18n.T(l, "assistant-tasks.task_failed_title", i18n.Args{"name": t.Title})
	if s.inHealth(ctx, t.Origin) {
		title = i18n.T(l, "assistant-tasks.task_failed_title_health")
	}
	r := strings.TrimRight(strings.TrimSpace(reason), ".。") // i18n-ignore: trailing full stops trimmed, not text
	body := i18n.T(l, "assistant-tasks.task_failed_body_no_reason")
	if r != "" {
		body = i18n.T(l, "assistant-tasks.task_failed_body", i18n.Args{"reason": r})
	}
	sub := s.machineName(machine)
	if sub == "" {
		sub = "Persona"
	}
	s.push(ctx, Notification{
		Subtitle:  sub,
		DeviceID:  here,
		SessionID: t.Origin,
		Title:     title,
		Body:      body,
	})
}

// SetSuperviseTimings makes supervision messages gather for gather and come at most every every (urgent: urgent), and
// answers what puts them back: for the hub's end-to-end tests, which cannot reach the package's own.
func SetSuperviseTimings(gather, every, urgent time.Duration) (restore func()) {
	g, e, u := superviseGather, superviseEvery, superviseUrgentEvery
	superviseGather, superviseEvery, superviseUrgentEvery = gather, every, urgent
	return func() { superviseGather, superviseEvery, superviseUrgentEvery = g, e, u }
}
