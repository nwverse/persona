package assistant

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Decisions that can wait (#4930, decision_wait.go). All names, ids and texts are placeholders.

// briefingOn gives s one daily routine with facts on (enabled as given): what a held decision counts on.
func briefingOn(s *Service, enabled bool) {
	setTasks(s, Task{ID: "morning", Title: "morning", Prompt: "p", Repeat: repeatDaily, Time: "09:00", Enabled: enabled, Facts: true})
}

// waitingDecision is rolloutDecision marked can_wait.
func waitingDecision() map[string]any {
	a := rolloutDecision()
	a["can_wait"] = true
	return a
}

const rolloutReason = "先灰度还是直接全量"

// A decision that can wait is raised without a push and without a card, and is in Needs you at once with its default.
func TestDecisionThatCanWaitIsNotPushed(t *testing.T) {
	s, f, todo, posts, _ := conclusionService(t, false)
	briefingOn(s, true)
	waitsForReview(t, s, f, todo.ID, "两种走法：A 先灰度，B 直接全量。")
	res := reviewTask(t, s, todo.ID, waitingDecision())
	if !strings.Contains(res, "can wait (can_wait)") || strings.Contains(res, "A push with your recommendation") {
		t.Fatalf("task_review's answer: %s", res)
	}
	if n := deadlinePushes(f, todo.ID); n != 0 {
		t.Fatalf("%d pushes, want none", n)
	}
	if cards := cardsWith(posts(), rolloutReason); len(cards) != 0 {
		t.Fatalf("a card went out: %q", cards)
	}
	a := askOf(s, todo.ID)
	if a == nil || !a.ForBriefing || !a.NoCard || a.MessageID != "" || !a.Reversible {
		t.Fatalf("ask %+v", a)
	}
	if d := time.Until(a.Deadline); d < 23*time.Hour || d > 24*time.Hour {
		t.Fatalf("its 24-hour default moved: in %v", d)
	}
	ask, ok := s.todoAsks(context.Background())[todo.ID]
	if !ok || ask.Recommend != "先灰度 10%" || ask.Default != defaultTaken {
		t.Fatalf("not in Needs you as any decision: %+v", ask)
	}
	s.retryAcceptance(time.Now().Add(20 * time.Minute)) // no card is due while it waits
	if cards := cardsWith(posts(), rolloutReason); len(cards) != 0 {
		t.Fatalf("the retry posted its card: %q", cards)
	}
}

// can_wait is ignored, and the answer says why, for a decision that is never taken by default, one whose deadline is
// near, and without a daily briefing with facts; without can_wait nothing changes.
func TestDecisionThatCannotWaitIsPushedAsBefore(t *testing.T) {
	for _, c := range []struct {
		name     string
		args     func() map[string]any
		briefing bool
		ignored  string
	}{
		{"cannot be undone", func() map[string]any { a := dropTableDecision(); a["can_wait"] = true; return a }, true, waitNoNeverDefault},
		{"others will see it", func() map[string]any { a := waitingDecision(); a["why_owner"] = whyVisible; return a }, true, waitNoNeverDefault},
		{"a deadline that is not taken by default", func() map[string]any {
			a := waitingDecision()
			a["deadline"], a["reversible"] = "20h", false
			return a
		}, true, waitNoNeverDefault},
		{"a deadline within 12 hours", func() map[string]any {
			a := waitingDecision()
			a["deadline"], a["reversible"] = "11h", true
			return a
		}, true, waitNoDeadline},
		{"the briefing is off", waitingDecision, false, waitNoBriefing},
		{"not marked", rolloutDecision, true, ""},
	} {
		s, f, todo, posts, _ := conclusionService(t, false)
		briefingOn(s, c.briefing)
		waitsForReview(t, s, f, todo.ID, "两种走法。")
		args := c.args()
		res := reviewTask(t, s, todo.ID, args)
		if c.ignored != "" && !strings.Contains(res, "can_wait was ignored ("+c.ignored+")") || c.ignored == "" && strings.Contains(res, "can_wait") {
			t.Errorf("%s: task_review's answer: %s", c.name, res)
		}
		a := askOf(s, todo.ID)
		if a == nil || a.ForBriefing || a.NoCard || a.MessageID == "" {
			t.Errorf("%s: ask %+v", c.name, a)
		}
		if n := deadlinePushes(f, todo.ID); n != 1 {
			t.Errorf("%s: %d pushes, want 1", c.name, n)
		}
		if cards := cardsWith(posts(), args["reason"].(string)); len(cards) != 1 {
			t.Errorf("%s: %d cards, want 1", c.name, len(cards))
		}
	}
	// A 12-hour deadline is not "within 12 hours".
	s, f, todo, _, _ := conclusionService(t, false)
	briefingOn(s, true)
	waitsForReview(t, s, f, todo.ID, "两种走法。")
	args := waitingDecision()
	args["deadline"], args["reversible"] = "13h", true
	reviewTask(t, s, todo.ID, args)
	if a := askOf(s, todo.ID); !a.ForBriefing || deadlinePushes(f, todo.ID) != 0 {
		t.Errorf("a 13-hour deadline did not wait: %+v", a)
	}
}

// can_wait is the decision's: any other verdict with it is refused.
func TestCanWaitIsForDecisionsOnly(t *testing.T) {
	s, f, todo := newSupervisedService(t)
	waitsForReview(t, s, f, todo.ID, "做完了。")
	if _, err := reviewErr(s, todo.ID, map[string]any{"verdict": "accepted", "can_wait": true}); err == nil ||
		!strings.Contains(err.Error(), "can_wait applies to needs_user only") {
		t.Fatalf("accepted with can_wait: %v", err)
	}
}

// The briefing lists a held decision with its recommendation; once a run listed it, it is not pushed later.
func TestBriefingListsADecisionThatWaited(t *testing.T) {
	s, f, todo, _, _ := conclusionService(t, false)
	briefingOn(s, true)
	waitsForReview(t, s, f, todo.ID, "两种走法。")
	reviewTask(t, s, todo.ID, waitingDecision())
	now := time.Now()
	facts := s.briefFactsAt(context.Background(), now, "morning")
	if len(facts.Accept) != 1 || facts.Accept[0].id != todo.ID {
		t.Fatalf("the briefing's rows: %+v", facts.Accept)
	}
	if row := facts.body(s, "morning").Accept[0]; row.Outcome != outcomeDecide || row.Recommend != "先灰度 10%" || row.AskedAt.IsZero() {
		t.Fatalf("the brief item's row: %+v", row)
	}
	// A run whose facts were made for another round changes nothing.
	old := *facts
	old.Accept = []boardRow{{id: todo.ID, asked: now.Add(-time.Hour)}}
	s.briefListed(&old)
	if !askOf(s, todo.ID).ForBriefing {
		t.Fatal("another round's row ended the wait")
	}
	s.briefListed(facts)
	if a := askOf(s, todo.ID); a.ForBriefing || !a.AcceptedAt.IsZero() {
		t.Fatalf("after the briefing: %+v", a)
	}
	s.sweepWaitingDecisions(now.Add(48 * time.Hour))
	if n := deadlinePushes(f, todo.ID); n != 0 {
		t.Fatalf("pushed %d times after a briefing listed it", n)
	}
}

// A held decision no briefing will list is pushed after all, once, with its card: the routine was switched off, or
// none ran within 24 hours.
func TestHeldDecisionIsPushedWhenNoBriefingListsIt(t *testing.T) {
	for _, c := range []struct {
		name  string
		off   bool
		after time.Duration
	}{
		{"the routine was switched off", true, time.Minute},
		{"no briefing ran within 24 hours", false, 24 * time.Hour},
	} {
		s, f, todo, posts, _ := conclusionService(t, false)
		briefingOn(s, true)
		waitsForReview(t, s, f, todo.ID, "两种走法。")
		reviewTask(t, s, todo.ID, waitingDecision())
		s.accept.mu.Lock()
		s.accept.asks[todo.ID].Deadline = time.Time{} // only what is tested ends the wait
		s.accept.mu.Unlock()
		s.sweepWaitingDecisions(time.Now().Add(time.Minute))
		if n := deadlinePushes(f, todo.ID); n != 0 {
			t.Fatalf("%s: pushed while a briefing was still to come", c.name)
		}
		if c.off {
			briefingOn(s, false)
		}
		at := time.Now().Add(c.after)
		s.sweepWaitingDecisions(at)
		s.sweepWaitingDecisions(at.Add(time.Hour))
		if n := deadlinePushes(f, todo.ID); n != 1 {
			t.Errorf("%s: %d pushes, want 1", c.name, n)
		}
		if cards := cardsWith(posts(), rolloutReason); len(cards) != 1 {
			t.Errorf("%s: %d cards, want 1", c.name, len(cards))
		}
		if a := askOf(s, todo.ID); a.ForBriefing || a.NoCard || a.MessageID == "" || !a.AcceptedAt.IsZero() {
			t.Errorf("%s: ask %+v", c.name, a)
		}
	}
}

// A held decision is pushed an hour before its deadline at the latest, so the sweep that takes defaults finds it told.
func TestHeldDecisionIsPushedBeforeItsDefaultIsTaken(t *testing.T) {
	s, f, todo, _, _ := conclusionService(t, false)
	briefingOn(s, true)
	waitsForReview(t, s, f, todo.ID, "两种走法。")
	reviewTask(t, s, todo.ID, waitingDecision())
	sent := len(sentToAgent(f))
	s.sweepDecisionDeadlines(time.Now().Add(2 * time.Hour))
	if n := deadlinePushes(f, todo.ID); n != 0 {
		t.Fatalf("pushed %d times long before its deadline", n)
	}
	s.accept.mu.Lock()
	s.accept.asks[todo.ID].Deadline = time.Now().Add(30 * time.Minute)
	s.accept.mu.Unlock()
	s.sweepDecisionDeadlines(time.Now())
	if n := deadlinePushes(f, todo.ID); n != 1 || len(sentToAgent(f)) != sent {
		t.Fatalf("%d pushes within the hour before its deadline, want 1; sent %q", n, sentToAgent(f)[sent:])
	}
	pastDeadline(t, s, todo.ID) // its default is unchanged
	s.sweepDecisionDeadlines(time.Now())
	if a := askOf(s, todo.ID); !a.ByDefault || len(sentToAgent(f)) != sent+1 {
		t.Fatalf("its default was not taken at the deadline: %+v", a)
	}
}

// Answered from Needs you while it waits, a held decision is taken as any other and never pushed.
func TestHeldDecisionAnsweredInNeedsYouIsNotPushed(t *testing.T) {
	s, f, todo, _, _ := conclusionService(t, false)
	briefingOn(s, true)
	waitsForReview(t, s, f, todo.ID, "两种走法。")
	reviewTask(t, s, todo.ID, waitingDecision())
	sent := len(sentToAgent(f))
	if rec := answerAsk(s, todo.ID, map[string]string{}); rec.Code != 200 {
		t.Fatalf("OK: %d %s", rec.Code, rec.Body.String())
	}
	if got := sentToAgent(f)[sent:]; len(got) != 1 || !strings.Contains(got[0], "先灰度 10%") {
		t.Fatalf("to the agent: %q", got)
	}
	s.sweepWaitingDecisions(time.Now().Add(48 * time.Hour))
	if n := deadlinePushes(f, todo.ID); n != 0 {
		t.Fatalf("pushed %d times after it was answered", n)
	}
}

func TestWaitOver(t *testing.T) {
	now := time.Now()
	for _, c := range []struct {
		name    string
		ask     acceptAsk
		briefed bool
		over    bool
	}{
		{"just raised", acceptAsk{AskedAt: now.Add(-time.Hour), Deadline: now.Add(23 * time.Hour)}, true, false},
		{"the briefing is off", acceptAsk{AskedAt: now.Add(-time.Hour)}, false, true},
		{"a day old", acceptAsk{AskedAt: now.Add(-decisionWaitFor)}, true, true},
		{"its deadline within the hour", acceptAsk{AskedAt: now.Add(-2 * time.Hour), Deadline: now.Add(59 * time.Minute)}, true, true},
		{"its deadline already handled", acceptAsk{AskedAt: now.Add(-2 * time.Hour), Deadline: now.Add(time.Minute), DueAt: now}, true, false},
	} {
		if got := waitOver(&c.ask, c.briefed, now) != ""; got != c.over {
			t.Errorf("%s: over %v, want %v", c.name, got, c.over)
		}
	}
}

// The run itself ends the wait: once a routine's run went into its conversation, the decisions it listed are not held.
func TestBriefingRunEndsTheWait(t *testing.T) {
	s, f, todo, _, _ := conclusionService(t, false)
	briefingOn(s, true)
	waitsForReview(t, s, f, todo.ID, "两种走法。")
	reviewTask(t, s, todo.ID, waitingDecision())
	s.brief.mu.Lock()
	s.brief.byTask = map[string]*briefRun{"morning": {facts: s.briefFactsAt(context.Background(), time.Now(), "morning"), label: "morning"}}
	s.brief.mu.Unlock()
	s.briefSent("morning", "conv-briefing", true)
	if a := askOf(s, todo.ID); a.ForBriefing || !a.AcceptedAt.IsZero() {
		t.Fatalf("after the run: %+v", a)
	}
}
