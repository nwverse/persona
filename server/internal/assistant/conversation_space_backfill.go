package assistant

import (
	"cmp"
	"context"
	"errors"
	"maps"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

// Noting the conversations to-dos name that were never noted (#10941, conversation_space.go): a manager notes a
// conversation's space when it starts one or a computer lists one, and a computer lists a quiet assistant conversation
// only for a week and only its 100 newest. A conversation quiet since before the manager noted spaces (an upgrade) is
// listed by no computer any more, so the to-dos it opened, supervised or wrote to could never be told (todoUntold): an
// extension was never shown them, and what would tell it of them waited for good. The backfill asks the computers the
// assistant ran on to list such conversations however quiet (the profile's keep, spaceKeep), a batch at a time, so
// each is noted as any listed one is; one not listed after spaceBackfillWait while the assistant's computer is on is
// given up (its transcript is gone) and stays untold, the rule failing closed for it; it is not kept again for
// spaceBackfillRetry, though the manager starts again. The conversations are those of every to-do, open or done (its
// origin, its supervisor and those its history was written in, read once a run of the manager), and any a verdict
// could not tell since (spaceBackfillWant).

// spaceBackfillBatch is how many never noted conversations the computers are asked to list at once.
const spaceBackfillBatch = 50

// spaceBackfillMost bounds the conversations one run of the manager takes up (past it, the rest wait for the next run).
const spaceBackfillMost = 20000

// spaceBackfillGoneKey is the setting that keeps the conversations the backfill gave up, by id with when, so a manager
// that starts again (a redeploy) does not keep them again until spaceBackfillRetry has passed.
const (
	spaceBackfillGoneKey = "assistant.space_backfill_gone"
	spaceBackfillRetry   = 7 * 24 * time.Hour
)

const (
	// spaceBackfillWait is how long a conversation is kept with the assistant's computer on before it is given up.
	spaceBackfillWait = 3 * time.Minute
	// spaceBackfillTick is how often a batch is checked for ones to give up.
	spaceBackfillTick = 30 * time.Second
	// spaceBackfillRescan is how soon a read of the to-dos that failed is tried again.
	spaceBackfillRescan = time.Minute
)

// fillState is where a conversation is in the backfill.
type fillState uint8

const (
	fillWaiting fillState = iota + 1 // queued for a batch
	fillKept                         // in the batch: its computers are asked to list it
	fillOver                         // noted, or given up: not asked again
)

// spaceBackfill is what the backfill has left (above).
type spaceBackfill struct {
	mu      sync.Mutex
	scanned bool                 // the to-dos were read for their conversations
	scanAt  time.Time            // when that was last tried
	queue   []string             // waiting for the batch, in order; one no longer waiting is skipped
	state   map[string]fillState // every conversation taken up
	batch   map[string]time.Time // the kept ones, since when (since the assistant's computer was last seen off)
	armed   bool                 // a check of the batch is scheduled
	// active: the batch holds any (fillLocked keeps it), read without the lock on a list's way.
	active atomic.Bool
	// gone: the conversations given up, as the setting keeps them (spaceBackfillGoneKey), once read (goneRead).
	gone     map[string]time.Time
	goneRead bool
	after    func(d time.Duration, f func()) // tests replace the timer
	clock    func() time.Time                // and the time
}

// now is the time; b.mu is held.
func (b *spaceBackfill) now() time.Time {
	if b.clock != nil {
		return b.clock()
	}
	return time.Now()
}

// later runs f after d; b.mu is not held.
func (b *spaceBackfill) later(d time.Duration, f func()) {
	b.mu.Lock()
	after := b.after
	b.mu.Unlock()
	if after != nil {
		after(d, f)
		return
	}
	time.AfterFunc(d, f)
}

// fillLocked moves what waits into the batch while it has room; it says whether one was. b.mu is held, and every
// change of the batch ends with it.
func (b *spaceBackfill) fillLocked(now time.Time) bool {
	added := false
	for len(b.batch) < spaceBackfillBatch && len(b.queue) > 0 {
		id := b.queue[0]
		b.queue = b.queue[1:]
		if b.state[id] != fillWaiting {
			continue
		}
		if b.batch == nil {
			b.batch = map[string]time.Time{}
		}
		b.state[id], b.batch[id], added = fillKept, now, true
	}
	b.active.Store(len(b.batch) > 0)
	return added
}

// takeLocked queues those of ids not taken up yet, in order; it says whether one was. b.mu is held.
func (b *spaceBackfill) takeLocked(ids []string) bool {
	took := false
	for _, id := range ids {
		if id == "" || b.state[id] != 0 || len(b.state) >= spaceBackfillMost {
			continue
		}
		if _, _, leader := parseLeaderRef(id); leader {
			continue // a task leader's agent: told by its run's directory (leaderSpace), never noted
		}
		if b.state == nil {
			b.state = map[string]fillState{}
		}
		b.state[id] = fillWaiting
		b.queue = append(b.queue, id)
		took = true
	}
	return took
}

// spaceKeep are the sessions their computers are to list however quiet for the spaces to be told, sorted: the task
// leaders' agents that opened or supervise an open task, on their own computer (leaderSpace reads one whose links keep
// no directory from its list), and the backfill's batch on every computer the assistant ran on (its own, the main
// conversation's, the former main ones'). The to-dos are read for the backfill the first time.
func (s *Service) spaceKeep(ctx context.Context, v Settings) []SessionRef {
	s.spaceBackfillScan(ctx, v)
	seen := map[SessionRef]bool{}
	s.todos.mu.RLock()
	for _, t := range s.todos.todos {
		if t.DoneAt != nil {
			continue
		}
		refs := []string{t.Origin}
		if t.Supervision != nil {
			refs = append(refs, t.Supervision.Supervisor)
		}
		for _, ref := range refs {
			if machine, sid, ok := parseLeaderRef(ref); ok {
				seen[SessionRef{MachineID: s.canonical(machine), SessionID: sid}] = true
			}
		}
	}
	s.todos.mu.RUnlock()
	machines := []string{v.MachineID, v.mainRouteMachine()}
	for _, f := range v.Former {
		machines = append(machines, f.MachineID)
	}
	b := &s.spaceFill
	b.mu.Lock()
	for id := range b.batch {
		for _, m := range machines {
			if m != "" {
				seen[SessionRef{MachineID: m, SessionID: id}] = true
			}
		}
	}
	b.mu.Unlock()
	out := make([]SessionRef, 0, len(seen))
	for c := range seen {
		out = append(out, c)
	}
	slices.SortFunc(out, func(a, b SessionRef) int {
		return cmp.Or(cmp.Compare(a.MachineID, b.MachineID), cmp.Compare(a.SessionID, b.SessionID))
	})
	return out
}

// spaceBackfillScan reads, once a run of the manager, the conversations every to-do names (the open ones' first) and
// takes up those never noted and with no deletion record. A read that fails is tried again after spaceBackfillRescan.
func (s *Service) spaceBackfillScan(ctx context.Context, v Settings) {
	b := &s.spaceFill
	b.mu.Lock()
	if b.scanned || !b.scanAt.IsZero() && b.now().Sub(b.scanAt) < spaceBackfillRescan {
		b.mu.Unlock()
		return
	}
	b.scanAt = b.now()
	b.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, conversationSpaceTimeout)
	defer cancel()
	if err := s.ensureTodosLoaded(ctx); err != nil {
		return // tried again with a later profile
	}
	type named struct {
		open bool
		refs []string
	}
	todos := map[string]named{}
	s.todos.mu.RLock()
	for id, t := range s.todos.todos {
		n := named{open: t.DoneAt == nil, refs: []string{t.Origin}}
		if t.Supervision != nil {
			n.refs = append(n.refs, t.Supervision.Supervisor)
		}
		todos[id] = n
	}
	s.todos.mu.RUnlock()
	ids := slices.Sorted(func(yield func(string) bool) {
		for id := range todos {
			if !yield(id) {
				return
			}
		}
	})
	feed, err := s.todoFeedConversations(ctx, ids)
	if err != nil && !errors.Is(err, errNoFeedConversations) {
		s.logf("conversation spaces: the conversations of the to-dos' history: %v", err)
		return
	}
	var open, done []string
	for _, id := range ids {
		n := todos[id]
		refs := slices.Concat(n.refs, feed[id])
		if n.open {
			open = append(open, refs...)
		} else {
			done = append(done, refs...)
		}
	}
	var cands []string
	took := map[string]bool{"": true, v.Main: true} // the main conversation is kept apart (keptSessions)
	for _, c := range append(open, done...) {
		if _, _, leader := parseLeaderRef(c); !took[c] && !leader {
			took[c] = true
			cands = append(cands, c)
		}
	}
	noted, err := s.readNotedSpaces(ctx, cands)
	if err != nil {
		s.logf("conversation spaces: the to-dos' conversations: %v", err)
		return
	}
	cands = slices.DeleteFunc(cands, func(c string) bool { _, ok := noted[c]; return ok })
	deleted := s.deletedConversationsOf(ctx, v.MachineID, cands)
	cands = slices.DeleteFunc(cands, func(c string) bool { _, ok := deleted[c]; return ok })
	s.spaceBackfillReadGone(ctx)
	b.mu.Lock()
	b.scanned = true
	b.takeLocked(cands)
	b.fillLocked(b.now())
	arm := len(b.batch) > 0 && !b.armed
	b.armed = b.armed || arm
	b.mu.Unlock()
	if arm {
		b.later(spaceBackfillTick, s.spaceBackfillCheck)
	}
	// What the batch holds goes in this very profile (spaceKeep reads it next): nothing to publish.
}

// spaceBackfillWant takes up the conversations a verdict could not tell (todoVerdicts: never noted, not listed, no
// deletion record), for their computers to list them; a batch with room asks for them at once.
func (s *Service) spaceBackfillWant(ids []string) {
	if len(ids) == 0 {
		return
	}
	b := &s.spaceFill
	b.mu.Lock()
	read := b.goneRead
	b.mu.Unlock()
	if !read { // those given up before are not taken up again (once a run of the manager)
		ctx, cancel := context.WithTimeout(s.runCtx(), conversationSpaceTimeout)
		s.spaceBackfillReadGone(ctx)
		cancel()
	}
	b.mu.Lock()
	if !b.takeLocked(ids) {
		b.mu.Unlock()
		return
	}
	added := b.fillLocked(b.now())
	arm := len(b.batch) > 0 && !b.armed
	b.armed = b.armed || arm
	b.mu.Unlock()
	if arm {
		b.later(spaceBackfillTick, s.spaceBackfillCheck)
	}
	if added {
		go s.publishSoon() // the profile's keep (a verdict may be read on a write's way)
	}
}

// spaceBackfillNoted takes the conversations ids out of the backfill once their spaces are noted
// (noteConversationSpaces), and fills the batch with what waits.
func (s *Service) spaceBackfillNoted(ids []string) {
	b := &s.spaceFill
	b.mu.Lock()
	if len(b.state) == 0 {
		b.mu.Unlock()
		return
	}
	for _, id := range ids {
		if st := b.state[id]; st == fillWaiting || st == fillKept {
			b.state[id] = fillOver
			delete(b.batch, id)
		}
	}
	added := b.fillLocked(b.now())
	b.mu.Unlock()
	if added {
		go s.publishSoon()
	}
}

// spaceBackfillCheck gives up the conversations of the batch kept spaceBackfillWait with the assistant's computer on
// and still not listed, fills the batch again and tells the computers; while the computer is off, the wait starts
// again. It checks again while the batch holds any.
func (s *Service) spaceBackfillCheck() {
	b := &s.spaceFill
	b.mu.Lock()
	b.armed = false
	empty := len(b.batch) == 0
	b.mu.Unlock()
	if empty || s.runCtx().Err() != nil {
		return
	}
	ctx, cancel := context.WithTimeout(s.runCtx(), conversationSpaceTimeout)
	machine := s.settings(ctx).MachineID
	d, known := s.device(ctx, machine)
	cancel()
	on := known && d.Online
	changed := false
	var gaveUp []string
	b.mu.Lock()
	now := b.now()
	for id, since := range b.batch {
		switch {
		case !on:
			b.batch[id] = now
		case now.Sub(since) >= spaceBackfillWait:
			b.state[id] = fillOver
			delete(b.batch, id)
			changed = true
			gaveUp = append(gaveUp, id)
		}
	}
	if changed {
		b.fillLocked(now)
	}
	arm := len(b.batch) > 0 && !b.armed // a want since may have scheduled one
	b.armed = b.armed || arm
	b.mu.Unlock()
	if len(gaveUp) > 0 {
		s.spaceBackfillGaveUp(gaveUp)
	}
	if changed {
		s.publishSoon() // the batch's keep is another now
	}
	if arm {
		b.later(spaceBackfillTick, s.spaceBackfillCheck)
	}
}

// spaceBackfillReadGone reads, once, the conversations given up before (spaceBackfillGoneKey): those given up less than
// spaceBackfillRetry ago are not taken up again. One that cannot be read is tried again with the next scan or give-up.
func (s *Service) spaceBackfillReadGone(ctx context.Context) {
	b := &s.spaceFill
	b.mu.Lock()
	read := b.goneRead
	b.mu.Unlock()
	st := s.store()
	if read || st == nil {
		return
	}
	gone := map[string]time.Time{}
	if _, err := st.Setting(ctx, spaceBackfillGoneKey, &gone); err != nil {
		s.logf("conversation spaces: the conversations given up: %v", err)
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.goneRead {
		return
	}
	b.goneRead = true
	now := b.now()
	for id, at := range gone {
		if now.Sub(at) >= spaceBackfillRetry {
			delete(gone, id)
			continue
		}
		if b.state == nil {
			b.state = map[string]fillState{}
		}
		if b.state[id] == 0 {
			b.state[id] = fillOver
		}
	}
	for id, at := range b.gone { // given up before the read
		gone[id] = at
	}
	b.gone = gone
}

// spaceBackfillGaveUp keeps ids, just given up, in the setting (best effort: a manager that starts again without it
// keeps them once more).
func (s *Service) spaceBackfillGaveUp(ids []string) {
	st := s.store()
	if st == nil {
		return
	}
	ctx, cancel := context.WithTimeout(s.runCtx(), conversationSpaceTimeout)
	defer cancel()
	s.spaceBackfillReadGone(ctx)
	b := &s.spaceFill
	b.mu.Lock()
	if b.gone == nil {
		b.gone = map[string]time.Time{}
	}
	now := b.now()
	for _, id := range ids {
		b.gone[id] = now
	}
	for id, at := range b.gone {
		if now.Sub(at) >= spaceBackfillRetry {
			delete(b.gone, id)
		}
	}
	if !b.goneRead {
		b.mu.Unlock()
		return // what the setting holds is not known: it is not written over
	}
	gone := maps.Clone(b.gone)
	b.mu.Unlock()
	if err := st.PutSetting(ctx, spaceBackfillGoneKey, gone); err != nil {
		s.logf("conversation spaces: keep %d given up: %v", len(ids), err)
	}
}

// spaceBackfillKept are the conversations of the batch now, sorted (tests).
func (s *Service) spaceBackfillKept() []string {
	b := &s.spaceFill
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, 0, len(b.batch))
	for id := range b.batch {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}
