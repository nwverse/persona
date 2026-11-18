package projection

import (
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// What the rules read off the to-do's runs and its other facts: how each linked session stands, the last activity,
// the PR facts, the due day. Ported from the legacy triage (internal/assistant/todo_triage.go, which reads the same
// sessions the way the apps do) and kept apart from it until that copy retires.

// Windows the rules use. Each is half open: the view changes at the window's end, which next_change_at names.
const (
	failedStartFor  = 48 * time.Hour   // row 1: a failed start needs the owner this long after the failure
	justFinishedFor = 24 * time.Hour   // rows 6 and 9: closed work unseen, or finished work after it was seen
	inactiveAfter   = 48 * time.Hour   // row 13: no activity this long gets the Inactive mark
	usageLimitGrace = 10 * time.Minute // a usage limit still holds this long past its reset
	prFreshFor      = 15 * time.Minute // PR facts count this long after they were checked
)

// runKind is how one linked run stands, as far as the rules care; the order is the order runReading checks them in.
type runKind int

const (
	runIdle        runKind = iota // stopped and seen, or nothing the rules read
	runOffline                    // the hub does not list its session
	runAsks                       // waits on the owner (a question, a plan, a tool approval)
	runLimited                    // stopped on a usage limit that has not reset yet
	runInterrupted                // cut off by a model error that is not a usage limit
	runAwaitsReply                // stopped on a plain-text question nobody answered
	runUnread                     // finished a turn nobody has seen
	runBackground                 // its turn ended, and background work or a wake brings it back by itself
	runStarting                   // Persona is opening it
	runRunning                    // an Agent works now
	runPersona                    // one of Persona's own conversations works now
)

// run is one active linked run: its link, its session when the hub lists it, and how it stands.
type run struct {
	link  Agent
	s     proto.Session
	found bool
	kind  runKind
}

// title is the run's title as the session list has it, else as the link recorded it.
func (r run) title() string {
	if r.found && r.s.Title != "" {
		return r.s.Title
	}
	return r.link.Title
}

// since is when the run got where it is: its session's state_since, else its updated_at.
func (r run) since() time.Time {
	if !r.s.StateSince.IsZero() {
		return r.s.StateSince
	}
	return r.s.UpdatedAt
}

// activeRuns are the to-do's runs that still work for it (not released, not dismissed, started), in the list's order
// (the newest link first), each read off its session.
func activeRuns(f *Facts, now time.Time) []run {
	var out []run
	for _, a := range f.Agents {
		if a.ReleasedAt != nil || a.DismissedAt != nil || a.StartError != "" {
			continue // a run whose start failed counts as stopped
		}
		r := run{link: a}
		for _, s := range f.Sessions {
			if s.MachineID == a.MachineID && s.SessionID == a.SessionID {
				r.s, r.found = s, true
				break
			}
		}
		r.kind = runReading(r, now)
		out = append(out, r)
	}
	return out
}

// runReading is how run r stands: the first that holds of offline, asks, a usage limit, interrupted, awaits a reply,
// unread, background, starting, running.
func runReading(r run, now time.Time) runKind {
	if !r.found {
		return runOffline
	}
	s := r.s
	st := stateOf(s)
	switch {
	case st == proto.StateNeedsYou:
		return runAsks
	case usageLimited(s, now):
		return runLimited
	case interrupted(s, now):
		return runInterrupted
	case s.AwaitsReply && stopped(s):
		return runAwaitsReply
	case st == proto.StateUnread:
		return runUnread
	case waitsByItself(s):
		return runBackground
	case s.Starting:
		return runStarting
	case st == proto.StateWorking && s.Assistant:
		return runPersona
	case st == proto.StateWorking:
		return runRunning
	}
	return runIdle
}

// stateOf is a session's state as the apps read it: the hub's, else worked out from its status.
func stateOf(s proto.Session) string {
	switch {
	case s.State != "":
		return s.State
	case s.Status == proto.StatusOffline:
		return "offline"
	case s.Status == proto.StatusWorking:
		return proto.StateWorking
	case s.Status == proto.StatusBlocked || s.PendingInteractions > 0:
		return proto.StateNeedsYou
	}
	return proto.StateIdle
}

// stopped: its turn is over, unread or seen.
func stopped(s proto.Session) bool {
	if s.State == proto.StateUnread || s.State == proto.StateIdle {
		return true
	}
	if s.State != "" {
		return false
	}
	return s.Status != proto.StatusWorking && s.Status != proto.StatusOffline && s.Status != proto.StatusBlocked &&
		s.PendingInteractions == 0
}

// usageLimitReset is when the usage limit that stopped s resets; nil when it is not known.
func usageLimitReset(s proto.Session) *time.Time {
	if s.TurnErrorResetsAt != nil {
		return s.TurnErrorResetsAt
	}
	if s.Work != nil {
		return s.Work.ResetsAt
	}
	return nil
}

// usageLimited: stopped on a usage limit whose reset (with its grace) is ahead or not known.
func usageLimited(s proto.Session, now time.Time) bool {
	if s.TurnError == "" || s.TurnErrorKind != proto.TurnErrorUsageLimit || !stopped(s) {
		return false
	}
	reset := usageLimitReset(s)
	return reset == nil || now.Before(reset.Add(usageLimitGrace))
}

// interrupted: cut off by a model error that is not a usage limit.
func interrupted(s proto.Session, now time.Time) bool {
	if s.TurnError == "" || usageLimited(s, now) {
		return false
	}
	st := stateOf(s)
	return st == proto.StateUnread || st == proto.StateIdle
}

// waitsByItself: its turn ended and something brings it back without the owner: background work still running, or
// the hub waking it for another Agent, an outside event or a moment's model failure.
func waitsByItself(s proto.Session) bool {
	st := stateOf(s)
	if st == proto.StateWorking {
		return s.BackgroundWorking
	}
	w := s.Work
	return st != proto.StateNeedsYou && st != proto.StateUnread && w != nil && w.Phase == proto.WorkBlocked &&
		(w.On == proto.WaitAgent || w.On == proto.WaitExternal || w.On == proto.WaitTransient)
}

// firstRun is the first of runs (in the list's order) of kind k.
func firstRun(runs []run, k runKind) (run, bool) {
	for _, r := range runs {
		if r.kind == k {
			return r, true
		}
	}
	return run{}, false
}

// later is the later of a and *b, a nil b being none.
func later(a time.Time, b *time.Time) time.Time {
	if b != nil && b.After(a) {
		return *b
	}
	return a
}

// latest is the later of a and b.
func latest(a, b time.Time) time.Time { return later(a, &b) }

// earliest is the earlier of a and b, a zero time being none.
func earliest(a, b time.Time) time.Time {
	if a.IsZero() || !b.IsZero() && b.Before(a) {
		return b
	}
	return a
}

// lastActivity is the last moment at now that the to-do or any of its active runs moved: its own times, its ledger's,
// its delivery's and ask's, the owner's restore, its return from Someday once that passed, and each active run's
// session (docs/assistant/todo-status-sop.md "Principle: real signals only").
func lastActivity(f *Facts, now time.Time) time.Time {
	last := later(f.UpdatedAt, &f.CreatedAt)
	last = later(last, f.ReopenedAt)
	last = later(last, f.DoneAt)
	if v := f.Supervision; v != nil {
		last = later(last, &v.UpdatedAt)
		if v.Progress != nil {
			last = later(last, &v.Progress.At)
		}
		if r := latestReview(v); r != nil {
			last = later(last, &r.At)
		}
	}
	if f.Delivery != nil {
		last = later(last, &f.Delivery.At)
	}
	if f.Ask != nil {
		last = later(last, &f.Ask.AskedAt)
	}
	last = later(last, f.RestoredAt)
	if u := f.SomedayUntil; u != nil && !now.Before(*u) {
		last = later(last, u)
	}
	for _, a := range f.Agents {
		if a.ReleasedAt != nil || a.DismissedAt != nil {
			continue
		}
		for _, s := range f.Sessions {
			if s.MachineID == a.MachineID && s.SessionID == a.SessionID {
				last = later(last, &s.UpdatedAt)
				last = later(last, &s.StateSince)
				break
			}
		}
	}
	return last
}

// latestReview is the ledger's latest review; nil when there is none.
func latestReview(v *Supervision) *Review {
	if v == nil || len(v.Reviews) == 0 {
		return nil
	}
	return &v.Reviews[len(v.Reviews)-1]
}

// acceptanceStands: the latest review accepted it and none came since, even when the hub reopened it
// (docs/assistant/todo-status-sop.md "Accepted tasks").
func acceptanceStands(f *Facts) bool {
	r := latestReview(f.Supervision)
	return f.Supervision != nil && f.Supervision.State == reviewAccepted && r != nil && r.Verdict == reviewAccepted
}

// reviewState is the review state that holds now: an accepted task reopened after a newer review, or a cancelled one
// reopened, goes on as if unreviewed.
func reviewState(f *Facts) string {
	if f.Supervision == nil {
		return ""
	}
	st := f.Supervision.State
	if f.DoneAt == nil && (st == reviewAccepted && !acceptanceStands(f) || st == reviewCancelled) {
		return ""
	}
	return st
}

// openAsk is the to-do's own ask when a whole one waits.
func openAsk(f *Facts) *Ask {
	if a := f.Ask; a != nil && a.Verdict != "" && !a.AskedAt.IsZero() {
		return a
	}
	return nil
}

// normalizePRURL is a PR's URL as two of them compare: trimmed, without a fragment or query, without a trailing
// slash, lower case.
func normalizePRURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if i := strings.IndexAny(raw, "#?"); i >= 0 {
		raw = raw[:i]
	}
	return strings.ToLower(strings.TrimRight(raw, "/"))
}

// openPRs are the to-do's PRs with a fresh observation saying open and not a draft: its current PR, then its earlier
// ones (#7935). Stale, absent or mismatched facts count for nothing.
func openPRs(f *Facts, now time.Time) []PRStatus {
	fresh := func(p PRStatus) bool {
		age := now.Sub(p.CheckedAt)
		return p.State == prOpen && !p.Draft && !p.CheckedAt.IsZero() && age >= 0 && age < prFreshFor
	}
	var out []PRStatus
	if p := f.PRStatus; p != nil && f.PRURL != "" && normalizePRURL(f.PRURL) == normalizePRURL(p.URL) && fresh(*p) {
		out = append(out, *p)
	}
	for _, p := range f.OtherPRs {
		if normalizePRURL(p.URL) != normalizePRURL(f.PRURL) && fresh(p) {
			out = append(out, p)
		}
	}
	return out
}

// dueEnd is the moment the to-do stops being due ahead: its due_at, or the midnight that ends its due day in loc;
// false without either.
func dueEnd(f *Facts) (time.Time, bool) {
	if f.DueAt != nil {
		return *f.DueAt, true
	}
	loc := f.Loc
	if loc == nil {
		loc = time.UTC
	}
	day, err := time.ParseInLocation("2006-01-02", f.DueDate, loc)
	if err != nil {
		return time.Time{}, false
	}
	return day.AddDate(0, 0, 1), true
}
