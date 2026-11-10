package assistant

import (
	"cmp"
	"context"
	"hash/fnv"
	"strings"
	"time"
)

// Spot-check reviews (#3128, docs/assistant/todos.md "Spot-check reviews"): with every finished supervised task waiting
// for the main conversation's task_review, the main conversation is the bottleneck once there are many. With
// assistant.review_sample_percent below 100, the done judge also checks each done_when criterion (taskAutoCheck); a
// task whose criteria are all checkable and met and whose work is live is closed by the judge itself (its review is
// the judge's, with the checks), and only a fixed share of those (spotChecked) still comes to the main conversation.
// A task that fails the check waits for task_review as before. At 100 (the default) the judge checks nothing more and
// every finished task waits for task_review.

// reviewerJudge marks a review the done judge made itself (TaskReview.By).
const reviewerJudge = "judge"

// reviewSamplePercent is assistant.review_sample_percent: the share of the finished supervised tasks that passed the
// judge's check whose review still goes to the main conversation; 100 when unset.
func (s *Service) reviewSamplePercent(ctx context.Context) int {
	if p := s.settings(ctx).ReviewSamplePercent; p != nil && *p >= 0 && *p <= 100 {
		return *p
	}
	return 100
}

// taskAutoCheck is the judge's own check of a supervised task's done_when: each criterion (in the ledger's words and
// order) with whether it can be checked from evidence, whether it is met and the evidence, and how far the work went
// out (a delivery state).
type taskAutoCheck struct {
	Checks   []autoCheckItem `json:"checks"`
	Delivery string          `json:"delivery"`
}

type autoCheckItem struct {
	ReviewCheck
	Checkable bool `json:"checkable"`
}

// passed: every criterion is checkable and met with evidence, and the work is live. A task with no criteria never
// passes: there is nothing to check it against.
func (c *taskAutoCheck) passed() bool {
	if c == nil || len(c.Checks) == 0 || c.Delivery != deliveryLive {
		return false
	}
	for _, x := range c.Checks {
		if !x.Checkable || !x.Met || strings.TrimSpace(x.Evidence) == "" {
			return false
		}
	}
	return true
}

// autoCheckOf is the judge's checks matched to criteria: one per criterion in order, the criterion as the ledger has
// it. Fewer checks than criteria leave the missing ones unchecked (not met).
func autoCheckOf(criteria []string, got []autoCheckItem, delivery string) *taskAutoCheck {
	c := &taskAutoCheck{Delivery: delivery}
	for i, crit := range criteria {
		x := autoCheckItem{ReviewCheck: ReviewCheck{Criterion: crit}}
		if i < len(got) {
			x.Checkable, x.Met, x.Evidence = got[i].Checkable, got[i].Met, clip(oneLine(got[i].Evidence), 300)
		}
		c.Checks = append(c.Checks, x)
	}
	return c
}

// spotCheck is spotChecked; a variable for the tests.
var spotCheck = spotChecked

// spotChecked: of the tasks that passed, this stretch of work of task id is one the main conversation still reviews.
// The same task and work always say the same, so a restart or a second judgment does not change it.
func spotChecked(id, work string, pct int) bool {
	if pct >= 100 {
		return true
	}
	if pct <= 0 {
		return false
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(id + "\x00" + work))
	return int(h.Sum32()%100) < pct
}

// judgeClose closes supervised task t, whose work since key passed the judge's check and was not sampled, as the main
// conversation's accepted would: the review is the judge's, with the checks; the task's conclusion card goes to the
// owner. False when the task changed meanwhile (it waits for review instead) or could not be closed.
func (s *Service) judgeClose(ctx context.Context, t Todo, key, reason string, c *taskAutoCheck) bool {
	checks := make([]ReviewCheck, 0, len(c.Checks))
	for _, x := range c.Checks {
		checks = append(checks, x.ReviewCheck)
	}
	note := cmp.Or(clip(oneLine(reason), 80), "判官核对全过、已上线") // i18n-ignore: the review's note, as the supervisor's are
	if _, changed, err := s.updateSupervision(ctx, t.ID, func(v *Supervision) bool {
		if st := v.state(t); st != "" && st != reviewRework {
			return false // reviewed or waiting on the user meanwhile
		}
		v.State = reviewAccepted
		v.Reviews = append(v.Reviews, TaskReview{At: time.Now().UTC(), Verdict: reviewAccepted, By: reviewerJudge, Checks: checks, Note: note})
		if len(v.Reviews) > 20 {
			v.Reviews = v.Reviews[len(v.Reviews)-20:]
		}
		return true
	}); err != nil || !changed {
		if err != nil {
			s.logf("task %s: judge close: %v", t.ID, err)
		}
		return false
	}
	closed, err := s.reviewDoneTodo(ctx, t.ID, "判官核对通过："+note, true) // i18n-ignore: done_reason, as a review's
	if err != nil {
		s.logf("task %s: judge close: %v", t.ID, err)
		return false
	}
	b := &s.supervise
	b.mu.Lock()
	b.init()
	for _, k := range []string{sigReview, sigIdle, sigNeedsYou, sigStalled} {
		delete(b.pending, t.ID+"|"+k)
	}
	b.mu.Unlock()
	_, _ = s.appendFeed(ctx, t.Origin, feedReview, t.ID, reviewFeedBody{Title: t.Title, Verdict: reviewAccepted, Note: note, Checks: checks})
	if closed {
		s.dropTaskReport(t.ID)
		if cur, ok := s.todoNow(t.ID); ok {
			s.askConclusion(ctx, cur, conclusionInput{Outcome: outcomeDone, Reason: reason, Note: note, Checks: checks})
		}
	}
	s.logf("task %s: closed by the judge, its check passed (%d criteria, live; work %s)", t.ID, len(checks), key)
	return true
}
