package assistant

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ---- the result against the brief (#3822) ----

// briefResultOf holds a review's checks against brief's criteria as they stand: each criterion met, not met or not
// checked (matched by its text; a check that names no criterion of the brief is ignored here), then the criteria scope
// changes dropped, each with the change that dropped it, then what was done beyond the brief.
func briefResultOf(brief TaskBrief, checks []ReviewCheck, alsoDone []string, todo, by, verdict string, at time.Time) BriefResult {
	r := BriefResult{At: at.UTC(), Todo: todo, By: by, Verdict: verdict, Criteria: []BriefCriterion{}}
	for _, c := range brief.Criteria {
		row := BriefCriterion{Criterion: c, State: criterionUnchecked}
		for _, k := range checks {
			if clip(oneLine(k.Criterion), 200) != c {
				continue
			}
			row.Evidence = k.Evidence
			if row.State = criterionNotMet; k.Met {
				row.State = criterionMet
			}
		}
		r.Criteria = append(r.Criteria, row)
	}
	r.Criteria = append(r.Criteria, (&taskBrief{rec: brief.briefRecord, st: briefState{Log: brief.Log}}).dropped()...)
	for _, x := range alsoDone {
		if x = clip(oneLine(x), 200); x != "" && len(r.AlsoDone) < 6 {
			r.AlsoDone = append(r.AlsoDone, x)
		}
	}
	return r
}

// recordBriefResult keeps how to-do t's work ended against its brief (a review's accepted or cancelled, the judge's
// own acceptance), and answers it; nil when the to-do has no brief and none can be made, or when a later to-do
// carries the brief's work (closing a superseded task ends nothing).
func (s *Service) recordBriefResult(ctx context.Context, t Todo, checks []ReviewCheck, alsoDone []string, by, verdict string) *BriefResult {
	brief, err := s.ensureTaskBrief(ctx, t)
	if err != nil || brief.ID == "" {
		if err != nil {
			s.logf("task %s: brief result: %v", t.ID, err)
		}
		return nil
	}
	if tip := s.briefTip(brief.ID); tip != "" && tip != t.ID {
		return nil // a later to-do carries the work on (this one was superseded): the brief has not ended
	}
	r := briefResultOf(brief, checks, alsoDone, t.ID, by, verdict, time.Now())
	if _, _, err := s.changeBrief(ctx, brief.ID, func(tb *taskBrief) bool {
		tb.st.Result = &r
		return true
	}); err != nil {
		s.logf("task %s: brief result: %v", t.ID, err)
		return nil
	}
	s.clearOverdue(t.ID)
	return &r
}

// words is the result as a tool's answer says it: one clause per criterion.
func (r *BriefResult) words() string {
	if r == nil || len(r.Criteria) == 0 {
		return ""
	}
	var parts []string
	for _, c := range r.Criteria {
		switch c.State {
		case criterionMet:
			parts = append(parts, fmt.Sprintf("%q met", c.Criterion))
		case criterionNotMet:
			parts = append(parts, fmt.Sprintf("%q NOT met", c.Criterion))
		case criterionDropped:
			parts = append(parts, fmt.Sprintf("%q dropped (%s)", c.Criterion, c.Change))
		default:
			parts = append(parts, fmt.Sprintf("%q not checked", c.Criterion))
		}
	}
	out := " Against its brief: " + strings.Join(parts, "; ") + "."
	if len(r.AlsoDone) > 0 {
		out += " Also done: " + strings.Join(r.AlsoDone, "; ") + "."
	}
	return out
}

// unsettled: an accepted result has a criterion that is not met or was not checked.
func (r *BriefResult) unsettled() []string {
	var out []string
	if r == nil {
		return nil
	}
	for _, c := range r.Criteria {
		if c.State == criterionNotMet || c.State == criterionUnchecked {
			out = append(out, c.Criterion)
		}
	}
	return out
}
