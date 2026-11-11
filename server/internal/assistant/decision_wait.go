package assistant

import (
	"context"
	"slices"
	"time"
)

// Decisions that can wait (#4930, docs/assistant/todos.md "Decisions that can wait"): a supervisor's needs_user review
// may say its decision can wait (task_review can_wait). Such a decision is raised without a push to the owner's phone
// and without a card: it is on its to-do at once (Needs you in the apps, the board), and the next briefing with facts
// lists it under "To verify" with its recommendation, where it is answered. It is the same ask as any decision's
// (acceptAsk, ForBriefing set while it waits for a briefing), so its default, its recheck and the owner's fixed
// answers are unchanged. A decision that cannot be undone or that other people will see, one whose deadline is nearer
// than decisionWaitDeadline, and every decision while no daily briefing with facts is on, is pushed as before. One
// that no briefing listed is pushed after all (sweepWaitingDecisions), so it is late at worst, never lost.

const (
	// decisionWaitDeadline: a decision whose deadline is nearer than this when it is raised is always pushed.
	decisionWaitDeadline = 12 * time.Hour
	// decisionWaitFor: a decision that can wait and that no briefing listed this long after it was raised is pushed.
	decisionWaitFor = 24 * time.Hour
	// decisionWaitMargin: a decision that can wait and that no briefing listed is pushed this long before its deadline
	// at the latest, so its default is not taken without the owner having been told.
	decisionWaitMargin = time.Hour
)

// Why a decision marked can_wait is pushed all the same, as task_review's answer says it.
const (
	waitNoNeverDefault = "it cannot be undone or other people will see it"
	waitNoDeadline     = "its deadline is within 12 hours"
	waitNoBriefing     = "no daily briefing with facts is on"
)

// decisionWaits says a decision marked can_wait is held for the briefing when raised at now: why only the owner decides
// (why), its deadline (zero: none) and whether taking its recommendation at the deadline can be undone. Else why not,
// in the words task_review answers with.
func (s *Service) decisionWaits(ctx context.Context, why string, deadline time.Time, reversible bool, now time.Time) (bool, string) {
	switch {
	case neverDefault(why) || !deadline.IsZero() && !reversible:
		return false, waitNoNeverDefault
	case !deadline.IsZero() && deadline.Sub(now) < decisionWaitDeadline:
		return false, waitNoDeadline
	case !s.laterBriefed(ctx): // the routine the questions that can wait count on (later.go)
		return false, waitNoBriefing
	}
	return true, ""
}

// waitOver says why decision a, held for a briefing since it was raised, is pushed after all at now ("": it still
// waits). briefed: a daily briefing with facts is still on.
func waitOver(a *acceptAsk, briefed bool, now time.Time) string {
	switch {
	case !briefed:
		return "no daily briefing with facts is on any more"
	case now.Sub(a.AskedAt) >= decisionWaitFor:
		return "no briefing listed it within 24 hours"
	case !a.Deadline.IsZero() && a.DueAt.IsZero() && !now.Before(a.Deadline.Add(-decisionWaitMargin)):
		return "no briefing listed it and its deadline is within the hour"
	}
	return ""
}

// sweepWaitingDecisions pushes each decision held for a briefing that none will list in time (waitOver): its push and
// its card go out as they would have when it was raised, once. One the owner hid has been seen: its wait ends without
// a push.
func (s *Service) sweepWaitingDecisions(now time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	b := &s.accept
	b.mu.Lock()
	if !s.loadAcceptLocked(ctx) {
		b.mu.Unlock()
		return
	}
	held := map[string]acceptAsk{}
	for id, a := range b.asks {
		if decideAsk(a) && a.ForBriefing && !a.AskedAt.IsZero() && a.AcceptedAt.IsZero() {
			held[id] = *a
		}
	}
	b.mu.Unlock()
	if len(held) == 0 {
		return // nothing is held: the schedule is not even read
	}
	briefed := s.laterBriefed(ctx)
	ids := make([]string, 0, len(held))
	for id := range held {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		a := held[id]
		why := waitOver(&a, briefed, now)
		t, ok := s.todoNow(id)
		if why == "" || !ok || acceptAskEnded(&a, t) || acceptAskStale(&a, t) {
			continue // it still waits, or it is over: nothing to push
		}
		dismissed, err := s.decisionDismissed(ctx, t)
		if err != nil {
			continue // not known: the next sweep asks again
		}
		cp, ok := s.endWait(ctx, id, a.AskedAt)
		if !ok || dismissed {
			continue
		}
		s.logf("task %s: its decision could wait, but %s: pushed", id, why)
		s.pushDecision(ctx, id)
		s.postWaitedCard(ctx, t, cp)
	}
}

// endWait marks task id's decision asked at as no longer held for a briefing, and returns it; false when it is not
// held (answered, listed or pushed meanwhile, or another round asks) or the book could not be written.
func (s *Service) endWait(ctx context.Context, id string, asked time.Time) (acceptAsk, bool) {
	b := &s.accept
	b.mu.Lock()
	defer b.mu.Unlock()
	cur := b.asks[id]
	if cur == nil || !cur.AskedAt.Equal(asked) || !cur.ForBriefing || !cur.AcceptedAt.IsZero() {
		return acceptAsk{}, false
	}
	cur.ForBriefing = false
	if err := s.saveAcceptErrLocked(ctx); err != nil {
		cur.ForBriefing = true // not written: the next sweep tries again, so it is pushed once
		s.logf("task %s: decision: save: %v", id, err)
		return acceptAsk{}, false
	}
	return *cur, true
}

// postWaitedCard posts the card of decision a, which was held without one, when the bot can (as postAsk would have when
// it was raised). No card of this round ever went out, so the round's key is safe however old the ask is. When it does
// not go out the ask stays card-less: the push went, and the to-do and the briefing list it.
func (s *Service) postWaitedCard(ctx context.Context, t Todo, a acceptAsk) {
	st, err := s.feishuStatus(ctx)
	if err != nil || !st.Ready || !st.Features.Reports {
		return
	}
	a.NoCard = false
	mid, err := s.botPostKeyed(ctx, "", s.acceptCard(ctx, t, a, false), askSummary(s.botLang(ctx), &a), acceptCardKey(t.ID, a.AskedAt))
	if err != nil {
		s.logf("task %s: decision card: %v (it stays without one)", t.ID, err)
		return
	}
	b := &s.accept
	b.mu.Lock()
	cur := b.asks[t.ID]
	if cur == nil || !cur.AskedAt.Equal(a.AskedAt) {
		if cur != nil { // another round asks now: this card is an earlier round's
			cur.Earlier = lastOf(append(slices.Clone(cur.Earlier), mid), acceptEarlierMost)
			s.saveAcceptLocked(ctx)
		}
		b.mu.Unlock()
		return
	}
	cur.MessageID, cur.Messages, cur.NoCard = mid, append(cur.Messages, mid), false
	s.saveAcceptLocked(ctx)
	cp := *cur
	b.mu.Unlock()
	if !cp.AcceptedAt.IsZero() { // answered while the card went out: it says so too
		if err := s.botUpdateCard(ctx, mid, s.acceptCard(ctx, t, cp, false)); err != nil {
			s.logf("task %s: decision card: %v", t.ID, err)
		}
	}
}

// briefListed: a briefing run with the rows of f went into its conversation (the `brief` item, and the card or the
// model's answer from the same rows). The decisions among them that were held for a briefing have now been put to the
// owner: they are not pushed later (sweepWaitingDecisions). Only the round the row was made for counts.
func (s *Service) briefListed(f *briefFacts) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	b := &s.accept
	b.mu.Lock()
	defer b.mu.Unlock()
	if !s.loadAcceptLocked(ctx) {
		return
	}
	changed := false
	for _, r := range f.Accept {
		if a := b.asks[r.id]; a != nil && a.ForBriefing && a.AskedAt.Equal(r.asked) {
			a.ForBriefing, changed = false, true
		}
	}
	if changed {
		s.saveAcceptLocked(ctx)
	}
}
