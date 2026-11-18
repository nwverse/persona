package projection

import (
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
)

// The decision table (docs/design/proposals/todos-redesign.md 6.3): the first row that holds places the to-do, except
// row 13, which only sets the Inactive mark on what rows 12 and 14 leave. Closed work comes first (rows 6 and 7), so an
// open to-do never reads as done (6.2 invariant 1). Each function below is one row family and says which rows; the
// fields each sets, and every line key, are listed in spec/fixtures/todo-view/README.md.

// Line keys (spec/fixtures/todo-view/README.md "Line keys"): stable identifiers, never renamed or reused.
const (
	lineFailedStart     = "failed_start"
	lineAgentAsks       = "agent_asks"
	lineInterrupted     = "interrupted"
	lineDecision        = "decision"
	lineDecisionDefault = "decision_default"
	lineCannot          = "cannot"
	lineAwaitsReply     = "awaits_reply"
	lineWorking         = "working"
	linePersonaWorking  = "persona_working"
	lineStarting        = "starting"
	lineBackground      = "background"
	lineParked          = "parked"
	lineFinished        = "finished"
	lineAccepted        = "accepted"
	lineDeployed        = "deployed"
	linePROpen          = "pr_open"
	lineUsageLimit      = "usage_limit"
	lineFinishedSeen    = "finished_seen"
	lineCompleted       = "completed"
	lineCanceled        = "canceled"
	lineReported        = "reported"
	lineSuperseded      = "superseded"
	lineSomeday         = "someday"
)

// reading is one to-do's facts as the rows read them, worked out once.
type reading struct {
	f    *Facts
	now  time.Time
	last time.Time // the last activity
	runs []run
}

func read(f *Facts, now time.Time) *reading {
	return &reading{f: f, now: now, last: lastActivity(f), runs: activeRuns(f, now)}
}

// View is the view of to-do f at now: the first row of the decision table that holds, with the steps it carries
// rolled up (proposal 3.4 "Steps roll up"). Its rank is 0: Rank orders the rows of a list.
func View(f Facts, now time.Time) assistantwire.TodoSummary {
	r := read(&f, now)
	v := r.own()
	return r.withSteps(v)
}

// own places the to-do by its own facts, rows 1 to 14.
func (r *reading) own() assistantwire.TodoSummary {
	if v, ok := r.closedWork(); ok { // rows 6, 7
		return v
	}
	if r.f.StartError != "" { // row 1, then 12 to 14
		return r.failedStart()
	}
	for _, row := range []func() (assistantwire.TodoSummary, bool){
		r.runAsks,        // row 2
		r.ownerDecides,   // row 3
		r.canWait,        // row 4
		r.working,        // row 5
		r.parked,         // row 8
		r.finishedRecent, // row 9
		r.waiting,        // row 10
		r.finishedSeen,   // row 11
		r.someday,        // row 12
	} {
		if v, ok := row(); ok {
			return v
		}
	}
	return r.rest() // rows 13, 14
}

// at is a view in phase, section and reason, seen when the owner looked at it since its last activity (the closed
// rows compare with the close instead).
func (r *reading) at(phase, section, reason string) assistantwire.TodoSummary {
	return assistantwire.TodoSummary{Phase: phase, Section: section, Reason: reason, Seen: r.seenSince(r.last),
		CanWait: section == assistantwire.TodoListSectionCanWait}
}

// seenSince: the owner looked at it at or after at.
func (r *reading) seenSince(at time.Time) bool {
	return r.f.SeenAt != nil && !r.f.SeenAt.Before(at)
}

// line is a line 2 with the arguments that have a value (name, value pairs); an empty args map is left out.
func line(key string, args ...string) *assistantwire.TodoLine {
	l := &assistantwire.TodoLine{Key: key}
	for i := 0; i+1 < len(args); i += 2 {
		if args[i+1] == "" {
			continue
		}
		if l.Args == nil {
			l.Args = map[string]string{}
		}
		l.Args[args[i]] = args[i+1]
	}
	return l
}

// stamp is an absolute time for a line argument or a view field: UTC, as Go writes it.
func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// listed is a time as the list writes it, for an ask's id and round: sent back as it is.
func listed(t time.Time) string { return t.Format(time.RFC3339Nano) }

func utc(t time.Time) time.Time {
	if t.IsZero() {
		return t
	}
	return t.UTC()
}

// firstLine is the first non-empty line of s, trimmed.
func firstLine(s string) string {
	for l := range strings.Lines(s) {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return ""
}

// ---- rows 6 and 7: closed work ----

// closedWork: closed and unseen within 24 hours is Just finished (row 6); then, or once seen, Done with how it ended
// (row 7). A failed start superseded by finished related work reads as closed (proposal 6.3, "Rule 1a").
func (r *reading) closedWork() (assistantwire.TodoSummary, bool) {
	f := r.f
	if !f.closed() {
		return assistantwire.TodoSummary{}, false
	}
	at := *f.DoneAt
	reason, l := r.outcome()
	v := r.at(assistantwire.TodoPhaseDone, assistantwire.TodoListSectionDone, reason)
	if v.Seen = r.seenSince(at); !v.Seen && r.now.Before(at.Add(justFinishedFor)) {
		v.Section, v.Reason, v.NextChangeAt = assistantwire.TodoListSectionJustFinished, assistantwire.TodoReasonClosedUnseen,
			utc(at.Add(justFinishedFor))
	}
	v.Line, v.Since = l, utc(at)
	return v, true
}

// outcome is how a closed to-do ended (row 7's reasons) and its line.
func (r *reading) outcome() (string, *assistantwire.TodoLine) {
	f := r.f
	switch {
	case f.StartError != "": // only a superseded failed start is closed with its error
		return assistantwire.TodoReasonSuperseded, line(lineSuperseded)
	case f.CancelledAt != nil || f.DoneBy == doneByCancelled:
		return assistantwire.TodoReasonCanceled, line(lineCanceled, "reason", f.CancelledReason)
	case f.DoneBy == doneByReported:
		return assistantwire.TodoReasonReported, line(lineReported)
	}
	return assistantwire.TodoReasonCompleted, line(lineCompleted, "by", f.DoneBy)
}

// ---- row 1: a failed start ----

// failedStart: a run failed to start; it stays open and needs the owner for 48 hours after the failure (its retry is
// the ask), then rows 12 to 14 place it as nothing works on it, with the Inactive mark whatever its due date or origin.
func (r *reading) failedStart() assistantwire.TodoSummary {
	f := r.f
	failed := f.UpdatedAt
	if f.DoneAt != nil {
		failed = *f.DoneAt
	}
	if !r.now.Before(failed.Add(failedStartFor)) {
		if v, ok := r.someday(); ok {
			return v
		}
		return r.rest()
	}
	v := r.at(assistantwire.TodoPhaseNeedsYou, assistantwire.TodoListSectionNeedsYou, assistantwire.TodoReasonFailedStart)
	v.Line = line(lineFailedStart, "error", f.StartError)
	v.Since, v.NextChangeAt = utc(failed), utc(failed.Add(failedStartFor))
	v.Asks = []assistantwire.OwnerAsk{{ID: "todo:retry@" + listed(failed), Kind: assistantwire.OwnerAskKindRetry,
		Home: assistantwire.OwnerAskHomeTodo}}
	return v
}

// ---- row 2: a run asks, or was cut off ----

// runAsks: a run waits on the owner (agent_asks; every run that asks is an ask, the oldest first) or was cut off by a
// model error (interrupted). stopped_unfinished needs a run's end reason, which the session list does not carry yet.
func (r *reading) runAsks() (assistantwire.TodoSummary, bool) {
	var asking []run
	for _, rn := range r.runs {
		if rn.kind == runAsks {
			asking = append(asking, rn)
		}
	}
	if len(asking) > 0 {
		slices.SortStableFunc(asking, func(a, b run) int { return a.since().Compare(b.since()) })
		asks := make([]assistantwire.OwnerAsk, len(asking))
		for i, rn := range asking {
			asks[i] = runAsk(rn)
		}
		v := r.at(assistantwire.TodoPhaseNeedsYou, assistantwire.TodoListSectionNeedsYou, assistantwire.TodoReasonAgentAsks)
		v.Line = line(lineAgentAsks, "run", asking[0].title(), "text", asks[0].Text)
		v.Since, v.Asks = utc(asking[0].since()), r.withOwnAsk(asks)
		return v, true
	}
	if rn, ok := firstRun(r.runs, runInterrupted); ok {
		v := r.at(assistantwire.TodoPhaseNeedsYou, assistantwire.TodoListSectionNeedsYou, assistantwire.TodoReasonInterrupted)
		v.Line, v.Since, v.Asks = line(lineInterrupted, "run", rn.title()), utc(rn.since()), r.withOwnAsk(nil)
		return v, true
	}
	return assistantwire.TodoSummary{}, false
}

// runAsk is run rn's ask: a question at home in its session (the session list says that it waits, not which card it
// shows), its id following the moment the session came to wait.
func runAsk(rn run) assistantwire.OwnerAsk {
	text := rn.s.Asks
	if text == "" {
		text = rn.s.AttentionReason
	}
	return assistantwire.OwnerAsk{
		ID:   "run:" + rn.link.MachineID + "/" + rn.link.SessionID + "@" + listed(rn.since()),
		Kind: assistantwire.OwnerAskKindQuestion, Home: assistantwire.OwnerAskHomeRun, Text: text,
		Run: &assistantwire.TodoRunRef{MachineID: rn.link.MachineID, SessionID: rn.link.SessionID, Title: rn.title()},
	}
}

// withOwnAsk is asks, then the to-do's own decision when one also waits on the owner.
func (r *reading) withOwnAsk(asks []assistantwire.OwnerAsk) []assistantwire.OwnerAsk {
	if a, ok := r.ownAsk(); ok {
		asks = append(asks, a)
	}
	return asks
}

// decisionAsk is the to-do's own open decision (a conclusion that needs the owner's decision, or says it cannot be
// done), unless the owner chose Don't handle on its round.
func (r *reading) decisionAsk() *Ask {
	a := openAsk(r.f)
	if a == nil || r.dismissed() || (a.Outcome != outcomeDecide && a.Outcome != outcomeCannot) {
		return nil
	}
	return a
}

// dismissed: the owner chose Don't handle on the decision round, so it no longer waits on them.
func (r *reading) dismissed() bool {
	return r.f.DecisionReview != nil && r.f.DecisionReview.DismissalID != ""
}

// ownAsk is the to-do's own decision as an ask at home on the to-do, its id and round the moment it was asked.
func (r *reading) ownAsk() (assistantwire.OwnerAsk, bool) {
	a := r.decisionAsk()
	if a == nil {
		return assistantwire.OwnerAsk{}, false
	}
	round := listed(a.AskedAt)
	out := assistantwire.OwnerAsk{ID: "todo:decision@" + round, Kind: assistantwire.OwnerAskKindDecision,
		Home: assistantwire.OwnerAskHomeTodo, Round: round, Text: a.Question, Recommend: a.Recommend,
		Options: a.Options, CanWait: canWaitDecision(a)}
	if a.Default == defaultTaken && a.Deadline != nil {
		out.DefaultAt = utc(*a.Deadline)
	}
	return out, true
}

// canWaitDecision: a decision that can wait for the briefing.
func canWaitDecision(a *Ask) bool { return a.Outcome == outcomeDecide && a.ForBriefing }

// ---- row 3: the owner decides ----

// ownerDecides: open, and its review waits for the owner (needs_user) or its ask is a decision or says it cannot be
// done, unless the owner chose Don't handle on it. A decision that can wait is row 4's. verify, steps_proposed,
// signin and a possible duplicate have no input yet (spec/fixtures/todo-view/README.md "Not produced yet").
func (r *reading) ownerDecides() (assistantwire.TodoSummary, bool) {
	a := r.decisionAsk()
	if r.dismissed() || a == nil && reviewState(r.f) != reviewNeedsUser || a != nil && canWaitDecision(a) {
		return assistantwire.TodoSummary{}, false
	}
	v := r.at(assistantwire.TodoPhaseNeedsYou, assistantwire.TodoListSectionNeedsYou, assistantwire.TodoReasonOwnerDecides)
	switch {
	case a == nil: // a needs_user review with no ask listed: its question, nothing to answer here
		v.Line = line(lineDecision)
		if rv := latestReview(r.f.Supervision); rv != nil {
			v.Line, v.Since = line(lineDecision, "text", rv.Reason, "recommend", rv.Recommend), utc(rv.At)
		}
		return v, true
	case a.Outcome == outcomeCannot:
		v.Line = line(lineCannot)
	default:
		v.Line = line(lineDecision, "text", a.Question, "recommend", a.Recommend)
	}
	v.Since, v.Asks = utc(a.AskedAt), r.withOwnAsk(nil)
	return v, true
}

// ---- row 4: it can wait ----

// canWait: a decision marked for the briefing (Persona may take its recommendation at its deadline), or a run that
// ended on a plain-text question; listed under Needs you's fold, not counted.
func (r *reading) canWait() (assistantwire.TodoSummary, bool) {
	if a, ok := r.ownAsk(); ok && a.CanWait {
		v := r.at(assistantwire.TodoPhaseNeedsYou, assistantwire.TodoListSectionCanWait, assistantwire.TodoReasonCanWait)
		v.Line = line(lineDecision, "text", a.Text, "recommend", a.Recommend)
		if !a.DefaultAt.IsZero() {
			v.Line = line(lineDecisionDefault, "text", a.Text, "recommend", a.Recommend, "default_at", stamp(a.DefaultAt))
		}
		v.Since, v.Asks = utc(r.decisionAsk().AskedAt), []assistantwire.OwnerAsk{a}
		return v, true
	}
	if rn, ok := firstRun(r.runs, runAwaitsReply); ok {
		v := r.at(assistantwire.TodoPhaseNeedsYou, assistantwire.TodoListSectionCanWait, assistantwire.TodoReasonAwaitsReply)
		v.Line, v.Since = line(lineAwaitsReply, "run", rn.title(), "text", rn.s.Asks), utc(rn.since())
		return v, true
	}
	return assistantwire.TodoSummary{}, false
}

// ---- row 5: someone works on it ----

// working: Persona or an Agent runs, starts, or works in the background, the first such run in the list's order
// deciding which. queued needs the start's admission queue, which the plan keeps and the list does not carry yet.
func (r *reading) working() (assistantwire.TodoSummary, bool) {
	for _, rn := range r.runs {
		if rn.kind != runRunning && rn.kind != runPersona {
			continue
		}
		text := rn.s.Activity
		if text == "" {
			text = firstLine(rn.s.LastText)
		}
		v := r.at(assistantwire.TodoPhaseWorking, assistantwire.TodoListSectionWorking, assistantwire.TodoReasonAgentRunning)
		v.Line = line(lineWorking, "run", rn.title(), "text", text)
		if rn.kind == runPersona {
			v.Reason, v.Line = assistantwire.TodoReasonPersonaWorking, line(linePersonaWorking, "text", text)
		}
		v.Since = utc(rn.since())
		return v, true
	}
	for _, k := range []struct {
		kind   runKind
		reason string
		key    string
	}{
		{runStarting, assistantwire.TodoReasonStarting, lineStarting},
		{runBackground, assistantwire.TodoReasonBackground, lineBackground},
	} {
		if rn, ok := firstRun(r.runs, k.kind); ok {
			v := r.at(assistantwire.TodoPhaseWorking, assistantwire.TodoListSectionWorking, k.reason)
			v.Line, v.Since = line(k.key, "run", rn.title()), utc(rn.since())
			return v, true
		}
	}
	return assistantwire.TodoSummary{}, false
}

// ---- row 8: parked ----

// parked: its supervisor parked it until something named happens; it waits whatever its run last said. A park on
// someone else says so in its reason. The park's end passing changes nothing by itself (the hub's wake does).
func (r *reading) parked() (assistantwire.TodoSummary, bool) {
	sv := r.f.Supervision
	if sv == nil || sv.State != reviewParked || sv.Parked == nil {
		return assistantwire.TodoSummary{}, false
	}
	reason := assistantwire.TodoReasonParked
	if sv.Parked.WaitingOn == parkSomeoneElse {
		reason = assistantwire.TodoReasonSomeoneElse
	}
	v := r.at(assistantwire.TodoPhaseWaiting, assistantwire.TodoListSectionWaiting, reason)
	v.Line = line(lineParked, "reason", sv.Parked.Reason, "until", stamp(sv.Parked.Until))
	if rv := latestReview(sv); rv != nil && rv.Verdict == reviewParked {
		v.Since = utc(rv.At)
	}
	return v, true
}

// ---- rows 9 and 11: finished work ----

// finish is what says the to-do's work finished, when it did and its line: accepted (even when the hub reopened it),
// deployed, a run's turn nobody has seen, a review not done yet, or its ask saying done or please verify. An open
// concern is never accepted or deployed: only its report closes it.
func (r *reading) finish() (time.Time, *assistantwire.TodoLine, bool) {
	f := r.f
	concern := f.Kind == kindConcern
	switch {
	case !concern && acceptanceStands(f):
		return latestReview(f.Supervision).At, line(lineAccepted), true
	case !concern && f.Delivery != nil && f.Delivery.State == deliveryLive:
		at := f.Delivery.At
		if at.IsZero() {
			at = r.last
		}
		return at, line(lineDeployed), true
	}
	if rn, ok := firstRun(r.runs, runUnread); ok {
		return rn.since(), line(lineFinished, "run", rn.title()), true
	}
	if reviewState(f) == reviewPending {
		at := f.Supervision.UpdatedAt
		if f.Supervision.PendingSince != nil {
			at = *f.Supervision.PendingSince
		}
		return at, line(lineFinished), true
	}
	if a := openAsk(f); a != nil && (a.Outcome == "" || a.Outcome == outcomeDone) {
		return a.AskedAt, line(lineFinished), true
	}
	return time.Time{}, nil, false
}

// settled: accepted or deployed, finished whatever its PR facts say.
func (r *reading) settled() bool {
	f := r.f
	return f.Kind != kindConcern && (acceptanceStands(f) || f.Delivery != nil && f.Delivery.State == deliveryLive)
}

// finishedRecent: finished, and nobody has seen it since it last moved (finished_unseen), or seen less than 24 hours
// ago (finished_recent): ready, in Just finished (row 9).
func (r *reading) finishedRecent() (assistantwire.TodoSummary, bool) {
	at, l, ok := r.finish()
	if !ok {
		return assistantwire.TodoSummary{}, false
	}
	v := r.at(assistantwire.TodoPhaseReady, assistantwire.TodoListSectionJustFinished, assistantwire.TodoReasonFinishedUnseen)
	v.Line, v.Since = l, utc(at)
	if !v.Seen {
		return v, true
	}
	if end := r.f.SeenAt.Add(justFinishedFor); r.now.Before(end) {
		v.Reason, v.NextChangeAt = assistantwire.TodoReasonFinishedRecent, utc(end)
		return v, true
	}
	return assistantwire.TodoSummary{}, false
}

// finishedSeen: finished, seen more than 24 hours ago and still open: a To do to tick (row 11).
func (r *reading) finishedSeen() (assistantwire.TodoSummary, bool) {
	at, _, ok := r.finish()
	if !ok {
		return assistantwire.TodoSummary{}, false
	}
	v := r.at(assistantwire.TodoPhaseTodo, assistantwire.TodoListSectionTodo, assistantwire.TodoReasonFinishedSeen)
	v.Line, v.Since = line(lineFinishedSeen, "at", stamp(at)), utc(at)
	return v, true
}

// ---- row 10: waiting on something named ----

// waiting: fresh facts say one of its PRs is open and waits for a merge (never accepted or deployed work), or a run
// stopped on a usage limit until it resets. computer_offline needs Start When It's Back, scheduled_report a report's
// schedule: neither is kept yet; someone_else comes with a park (row 8).
func (r *reading) waiting() (assistantwire.TodoSummary, bool) {
	if prs := openPRs(r.f, r.now); len(prs) > 0 && !r.settled() {
		v := r.at(assistantwire.TodoPhaseWaiting, assistantwire.TodoListSectionWaiting, assistantwire.TodoReasonPrOpen)
		var checked time.Time
		for _, p := range prs {
			checked = later(checked, &p.CheckedAt)
		}
		v.Line, v.NextChangeAt = line(linePROpen), utc(checked.Add(prFreshFor))
		return v, true
	}
	if rn, ok := firstRun(r.runs, runLimited); ok {
		v := r.at(assistantwire.TodoPhaseWaiting, assistantwire.TodoListSectionWaiting, assistantwire.TodoReasonUsageLimit)
		reset := usageLimitReset(rn.s)
		v.Line, v.Since = line(lineUsageLimit, "run", rn.title()), utc(rn.since())
		if reset != nil {
			v.Line = line(lineUsageLimit, "run", rn.title(), "resets_at", stamp(*reset))
			v.NextChangeAt = utc(reset.Add(usageLimitGrace))
		}
		return v, true
	}
	return assistantwire.TodoSummary{}, false
}

// ---- row 12: Someday ----

// someday: the owner put it off, until a day or with no date; back in its place once the day comes.
func (r *reading) someday() (assistantwire.TodoSummary, bool) {
	until := r.f.SomedayUntil
	if until == nil || !r.now.Before(*until) {
		return assistantwire.TodoSummary{}, false
	}
	v := r.at(assistantwire.TodoPhaseSomeday, assistantwire.TodoListSectionSomeday, assistantwire.TodoReasonDeferred)
	v.Line = line(lineSomeday)
	if until.Before(SomedayNoDate) {
		v.Line, v.NextChangeAt = line(lineSomeday, "until", stamp(*until)), utc(*until)
	}
	return v, true
}

// ---- rows 13 and 14: nothing works on it ----

// rest: a To do with no line (its notes are line 2). Row 13's Inactive mark: a task (an Agent was linked, or a
// conversation handed it out) with no due date ahead, never an open concern, or any failed start, that nothing moved
// for 48 hours; until then next_change_at says when it may get the mark.
func (r *reading) rest() assistantwire.TodoSummary {
	f := r.f
	v := r.at(assistantwire.TodoPhaseTodo, assistantwire.TodoListSectionTodo, assistantwire.TodoReasonRest)
	failed := f.StartError != ""
	linked := slices.ContainsFunc(f.Agents, func(a Run) bool { return a.DismissedAt == nil })
	if !failed && (f.Kind == kindConcern || !linked && f.Origin == "") {
		return v
	}
	from := r.last.Add(inactiveAfter)
	if due, ok := dueEnd(f); ok && !failed && due.After(from) {
		from = due
	}
	if r.now.Before(from) {
		v.NextChangeAt = utc(from)
	} else {
		v.Inactive = utc(r.last)
	}
	return v
}

// ---- steps ----

// rollsUp: the sections a step carries its parent into when they are more urgent than the parent's own.
var rollsUp = map[string]bool{assistantwire.TodoListSectionNeedsYou: true, assistantwire.TodoListSectionCanWait: true,
	assistantwire.TodoListSectionWorking: true, assistantwire.TodoListSectionWaiting: true}

// withSteps counts the to-do's steps and carries the most urgent of them (proposal 3.4 "Steps roll up", 6.2
// invariant 6): an open parent whose step needs the owner, works or waits, ahead of its own section, takes the step's
// phase, section, reason, times and asks; its line names the step, and each ask's id says whose it is. The first step
// in the most urgent section wins. A closed parent stays done.
func (r *reading) withSteps(v assistantwire.TodoSummary) assistantwire.TodoSummary {
	f := r.f
	if len(f.Steps) == 0 {
		return v
	}
	steps := &assistantwire.TodoStepCount{Total: len(f.Steps)}
	var carried *assistantwire.TodoSummary
	var from *Facts
	for i := range f.Steps {
		s := &f.Steps[i]
		if s.closed() {
			steps.Done++
		}
		if f.closed() {
			continue
		}
		sv := View(*s, r.now)
		top := v.Section
		if carried != nil {
			top = carried.Section
		}
		if rollsUp[sv.Section] && sectionIndex(sv.Section) < sectionIndex(top) {
			carried, from = &sv, s
		}
	}
	if carried == nil {
		v.Steps = steps
		return v
	}
	out := *carried
	if out.Line != nil {
		args := map[string]string{"step": from.Title}
		for k, a := range out.Line.Args {
			args[k] = a
		}
		out.Line = &assistantwire.TodoLine{Key: out.Line.Key, Args: args}
	}
	out.Asks = nil
	for _, a := range carried.Asks {
		a.ID = "step:" + from.ID + "/" + a.ID
		out.Asks = append(out.Asks, a)
	}
	if !v.NextChangeAt.IsZero() && (out.NextChangeAt.IsZero() || v.NextChangeAt.Before(out.NextChangeAt)) {
		out.NextChangeAt = v.NextChangeAt
	}
	out.Seen = r.seenSince(later(r.last, ptr(lastActivity(from))))
	out.Inactive, out.Steps, out.Rank = time.Time{}, steps, 0
	return out
}

func ptr[T any](v T) *T { return &v }

// sectionIndex is the section's place in the list's order; an unknown section goes last.
func sectionIndex(section string) int {
	for i, s := range assistantwire.TodoListSections {
		if s == section {
			return i
		}
	}
	return len(assistantwire.TodoListSections)
}
