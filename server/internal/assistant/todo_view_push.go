package assistant

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
)

// The To-dos view stays fresh (#10226, D8; docs/assistant/todos.md "Live WebSocket pushes"). A client files each row
// by its view, and what files a row can change with no write to its to-do: one of its runs starts asking, stops or
// finishes, a turn is seen, or a window of the view ends (Just finished's 24 hours, the 48 hours to the Inactive
// mark). The manager then sends the settled {"type":"todos"} a write sends (todosPushSoon), so every client reads the
// list again; an old client, which works the rows out itself, just reads more often when it matters. The manager
// names todo_view in every hello (bus.go), so this always runs.
//
// What is compared is a row's classification (todoClass), never its line's arguments, since, next_change_at or rank:
// a working run's latest line and activity change on every step, and every client reading the whole list on each was
// the storm #5995 ended. The list's bytes still follow them whenever a client reads.
//
// told is each to-do's classification as first worked out after the last {"type":"todos"} went out: what every client
// reads from then on until it changes. Three things work views out and hold them against it: every list read (the
// whole list), a change of the runs on a computer (only the to-dos with a run there, todoRunsRecheck) and the clock
// (todoViewWake, at the earliest next_change_at). A view that reads differently sends the push; the push clears told,
// and the first views worked out after it fill it again without one. Views worked out from to-dos read before a push
// went out (an older gen) are not held against it.

// todoViewSettle is how long a change of the runs waits for the rest before the views it touches are worked out: the
// lists of several computers, and the card that comes with a run's question, land within it, and one push goes out.
const todoViewSettle = time.Second

// The clock's check of the views never runs more often than todoViewWakeEvery, nor sleeps longer than todoViewWakeCap:
// a far next_change_at (a Someday a year ahead) or a computer that slept never leaves it behind.
const (
	todoViewWakeEvery = time.Minute
	todoViewWakeCap   = time.Hour
)

// todoClass is what a client files a to-do's row under: its phase, section and reason, its asks by id, its marks
// (Inactive, Can wait, seen) and its steps' count. Comparable: two rows filed alike are ==.
type todoClass struct {
	phase, section, reason string
	asks                   string // the ids, in order, one per line
	inactive               int64  // the Inactive mark's time in Unix nanoseconds; 0 without one
	canWait, seen          bool
	steps                  assistantwire.TodoStepCount
}

// todoClassOf is the classification of view v.
func todoClassOf(v assistantwire.TodoSummary) todoClass {
	c := todoClass{phase: v.Phase, section: v.Section, reason: v.Reason, canWait: v.CanWait, seen: v.Seen}
	if !v.Inactive.IsZero() {
		c.inactive = v.Inactive.UnixNano()
	}
	if v.Steps != nil {
		c.steps = *v.Steps
	}
	if len(v.Asks) > 0 {
		ids := make([]string, len(v.Asks))
		for i, a := range v.Asks {
			ids[i] = a.ID
		}
		c.asks = strings.Join(ids, "\n")
	}
	return c
}

// todoViewBook is what the apps were told of the views and when the views are looked at next.
type todoViewBook struct {
	mu   sync.Mutex
	gen  uint64               // the {"type":"todos"} gone out so far
	told map[string]todoClass // by to-do id, since the last of them
	next map[string]time.Time // each to-do's next_change_at, as last worked out

	machines map[string]bool // the computers whose runs changed, waiting for todoViewSettle
	armed    bool            // todoRunsRecheck waits for it
	settle   *time.Timer     // its real-clock timer

	wakeAt  time.Time   // when the clock's check is due; zero: none waits
	wakeSeq uint64      // the waiting check's: one replaced does nothing
	wake    *time.Timer // its real-clock timer
	woke    time.Time   // when the clock's check last worked the views out

	runs  sync.WaitGroup                  // real-clock callbacks scheduled or running: fixtures wait before closing
	now   func() time.Time                // tests: a fake clock
	after func(d time.Duration, f func()) // tests: a fake clock's timers
}

// clock is the views' now.
func (b *todoViewBook) clock() time.Time {
	if b.now != nil {
		return b.now()
	}
	return time.Now()
}

// scheduleLocked runs f after d: on the tests' clock, else on a real timer, which it answers (b.mu held).
func (b *todoViewBook) scheduleLocked(d time.Duration, f func()) *time.Timer {
	if b.after != nil {
		b.after(d, f)
		return nil
	}
	b.runs.Add(1)
	return time.AfterFunc(d, func() {
		defer b.runs.Done()
		f()
	})
}

// stopLocked stops both real-clock timers; a callback already running finishes (b.mu held).
func (b *todoViewBook) stopLocked() {
	if b.settle != nil && b.settle.Stop() {
		b.runs.Done()
		b.armed = false
	}
	b.settle = nil
	b.stopWakeLocked()
}

// stopWakeLocked drops the clock's waiting check (b.mu held).
func (b *todoViewBook) stopWakeLocked() {
	if b.wake != nil && b.wake.Stop() {
		b.runs.Done()
	}
	b.wake, b.wakeAt = nil, time.Time{}
	b.wakeSeq++
}

// todoViewNow is the views' now: the list reads and the checks below work them out at it.
func (s *Service) todoViewNow() time.Time { return s.tview.clock() }

// todoViewGen is the number of {"type":"todos"} gone out so far: taken before the to-dos are read, it says whether
// views worked out from them are still held against what the apps were told.
func (s *Service) todoViewGen() uint64 {
	b := &s.tview
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.gen
}

// todoViewPushed: a {"type":"todos"} goes out, so every app reads the list again; what each row is filed under is
// told again by the first views worked out from here.
func (s *Service) todoViewPushed() {
	b := &s.tview
	b.mu.Lock()
	defer b.mu.Unlock()
	b.gen++
	b.told = nil
	s.todoViewArmLocked()
}

// todoViewsSeen holds the views of p, worked out from to-dos read while gen pushes had gone out, against what the
// apps were told, and tells them again ({"type":"todos"}, settled with any write's) when one reads differently. whole:
// p is every to-do, and its next_change_at replace all those kept; else only its to-dos' are.
func (s *Service) todoViewsSeen(gen uint64, p todoProjection, whole bool) {
	b := &s.tview
	b.mu.Lock()
	changed := false
	if gen == b.gen {
		if b.told == nil {
			b.told = make(map[string]todoClass, len(p.facts))
		}
		for i := range p.facts {
			c := todoClassOf(p.views[i])
			if was, ok := b.told[p.facts[i].ID]; ok && was != c {
				changed = true
			}
			b.told[p.facts[i].ID] = c
		}
	}
	if whole || b.next == nil {
		b.next = make(map[string]time.Time, len(p.facts))
	}
	for i := range p.facts {
		if at := p.views[i].NextChangeAt; !at.IsZero() {
			b.next[p.facts[i].ID] = at
		} else {
			delete(b.next, p.facts[i].ID)
		}
	}
	s.todoViewArmLocked()
	b.mu.Unlock()
	if changed {
		s.todosPushSoon()
	}
}

// ---- the runs ----

// todoRunsChanged: what the apps get of machine's sessions may have changed (its node's list, a card, a turn seen).
// The views of the to-dos with a run there are worked out again once todoViewSettle passed (todoRunsRecheck).
func (s *Service) todoRunsChanged(machine string) {
	b := &s.tview
	b.mu.Lock()
	defer b.mu.Unlock()
	if s.runCtx().Err() != nil {
		return
	}
	if b.machines == nil {
		b.machines = map[string]bool{}
	}
	b.machines[machine] = true
	if b.armed {
		return
	}
	b.armed = true
	b.settle = b.scheduleLocked(todoViewSettle, s.todoRunsRecheck)
}

// todoRunsRecheck works out again the views of the to-dos whose runs are on the computers that changed
// (todosOnRuns), each run's session read as the list reads it, and holds them against what the apps were told.
func (s *Service) todoRunsRecheck() {
	b := &s.tview
	b.mu.Lock()
	machines, gen := b.machines, b.gen
	b.machines, b.armed, b.settle = nil, false, nil
	b.mu.Unlock()
	if len(machines) == 0 || !s.todosLoaded() {
		return
	}
	ids, whole := s.todosOnRuns(machines)
	if len(ids) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var only map[string]bool // the rows read: ids, or every to-do when a failed start is among them
	if !whole {
		only = ids
	}
	list, err := s.listedTodosOf(ctx, false, only)
	if err != nil {
		return
	}
	p := s.todoProjectionRunsOf(ctx, list, b.clock(), ids)
	var mine todoProjection
	for i := range p.facts {
		if ids[p.facts[i].ID] {
			mine.facts, mine.views = append(mine.facts, p.facts[i]), append(mine.views, p.views[i])
		}
	}
	s.todoViewsSeen(gen, mine, false)
}

// todosOnRuns are the to-dos whose views a change of the runs on machines can change: the open ones (a failed start
// counts, as todoProjectionOf reads it) with an active run there, the to-do each of them is a step of, and every step
// of those (a parent's view rolls its steps up; steps are one level deep, todo_pieces.go). whole: a failed start is
// among them, which a related to-do anywhere in the list can supersede, so the whole list is read for how they relate.
// The to-dos are let go before any session is read.
func (s *Service) todosOnRuns(machines map[string]bool) (ids map[string]bool, whole bool) {
	s.todos.mu.RLock()
	defer s.todos.mu.RUnlock()
	heads := map[string]bool{}
	for id, t := range s.todos.todos {
		if t.DoneAt != nil && t.StartError == "" {
			continue
		}
		for _, a := range t.Agents {
			if a.ReleasedAt == nil && a.DismissedAt == nil && machines[a.MachineID] {
				heads[id] = true
				if p := t.PartOf; p != "" && p != id && s.todos.todos[p] != nil {
					heads[p] = true
				}
				break
			}
		}
	}
	if len(heads) == 0 {
		return nil, false
	}
	ids = make(map[string]bool, len(heads))
	for id, t := range s.todos.todos {
		if heads[id] || t.PartOf != "" && t.PartOf != id && heads[t.PartOf] {
			ids[id] = true
			whole = whole || t.StartError != ""
		}
	}
	return ids, whole
}

// ---- the clock ----

// todoViewArmLocked sets the clock's check at the earliest next_change_at kept, no sooner than todoViewWakeEvery
// after it last worked the views out and no later than todoViewWakeCap from now; a check already due sooner stays
// (it finds nothing due and sets the next). Called after every push, list read and check (b.mu held). Like Run's
// loops it runs only while the service does: before Run (a test that never starts it, on the real clock) nothing
// would ever stop the checks, each setting the next.
func (s *Service) todoViewArmLocked() {
	b := &s.tview
	due := b.dueLocked()
	if due.IsZero() || s.runCtx().Err() != nil || s.running.Load() == nil && b.after == nil {
		b.stopWakeLocked()
		return
	}
	now := b.clock()
	at := due
	if !b.woke.IsZero() && at.Before(b.woke.Add(todoViewWakeEvery)) {
		at = b.woke.Add(todoViewWakeEvery)
	}
	if limit := now.Add(todoViewWakeCap); at.After(limit) {
		at = limit
	}
	if !b.wakeAt.IsZero() && !at.Before(b.wakeAt) {
		return
	}
	b.stopWakeLocked()
	seq := b.wakeSeq
	b.wakeAt = at
	b.wake = b.scheduleLocked(max(at.Sub(now), 0), func() { s.todoViewWake(seq) })
}

// dueLocked is the earliest next_change_at kept; zero when none is (b.mu held).
func (b *todoViewBook) dueLocked() time.Time {
	var due time.Time
	for _, at := range b.next {
		if due.IsZero() || at.Before(due) {
			due = at
		}
	}
	return due
}

// todoViewWake is the clock's check seq: once a next_change_at passed, the views of every to-do are worked out as a
// list read works them out and held against what the apps were told; before that it only sets the next check.
func (s *Service) todoViewWake(seq uint64) {
	loaded := s.todosLoaded() // before b.mu: the to-dos' lock is never taken under it
	b := &s.tview
	b.mu.Lock()
	if seq != b.wakeSeq {
		b.mu.Unlock()
		return
	}
	b.wake, b.wakeAt = nil, time.Time{}
	now := b.clock()
	if due := b.dueLocked(); due.IsZero() || now.Before(due) || !loaded {
		s.todoViewArmLocked()
		b.mu.Unlock()
		return
	}
	b.woke = now
	gen := b.gen
	b.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	list, err := s.listedTodos(ctx, false)
	if err != nil {
		b.mu.Lock()
		s.todoViewArmLocked() // again a minute from now (todoViewWakeEvery)
		b.mu.Unlock()
		return
	}
	s.todoViewsSeen(gen, s.todoProjectionOf(ctx, list, now), true)
}
