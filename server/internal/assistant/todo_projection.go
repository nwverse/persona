package assistant

import (
	"context"
	"time"

	"github.com/nwverse/persona/server/internal/assistant/todo/projection"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/proto"
)

// The To-dos view (#10226): what the manager works out for each to-do and for the list, by the pure projection in
// todo/projection (its rules: spec/fixtures/todo-view and spec/fixtures/todo-counts). This file is the adapter: it reads
// the to-dos as GET /api/v1/todos lists them, their runs' sessions, the owner's zone and the knowledge base's project
// titles once per read, and hands them over as projection.Facts. No route calls it yet; the routes come next. The legacy
// triage (todo_triage.go) keeps placing to-dos for the direction tree and leader reports until they read the view.

// todoProjection is the view of every to-do of a list by id, the list's counts, and its places.
type todoProjection struct {
	Views  map[string]assistantwire.TodoSummary
	Counts assistantwire.TodoCounts
	Places assistantwire.TodoPlaces
}

// todoProjectionOf works out the view of every to-do of list at now, the list's counts and its places. list holds the
// rows as GET /api/v1/todos lists them (formatTodo, with ask, decision_review and orphaned); the sessions are read
// once for each machine an open to-do's runs are on, as the direction tree reads them.
func (s *Service) todoProjectionOf(ctx context.Context, list []listedTodo, now time.Time) todoProjection {
	var open []Todo
	for _, t := range list {
		if t.DoneAt == nil {
			open = append(open, t.Todo)
		}
	}
	lists, _ := s.agentSessionLists(ctx, open) // a machine that did not answer lists nothing: its runs read as offline
	sessions := map[[2]string]proto.Session{}
	for machine, l := range lists {
		for _, c := range l {
			c.MachineID = machine
			sessions[[2]string{machine, c.SessionID}] = c
		}
	}
	loc := s.location(ctx)
	facts := make([]projection.Facts, len(list))
	for i, t := range list {
		facts[i] = projectionFacts(t, sessions, loc)
	}
	views := projection.Views(facts, now)
	out := todoProjection{Views: make(map[string]assistantwire.TodoSummary, len(list)),
		Counts: projection.Counts(facts, views), Places: projection.Places(facts, views, s.todoProjectTitles(ctx, list))}
	for i, t := range list {
		out.Views[t.ID] = views[i]
	}
	return out
}

// todoProjectTitles are the knowledge base's titles of the projects list's to-dos are in, by slug; none when the
// knowledge base cannot be read (the places are then titled by slug).
func (s *Service) todoProjectTitles(ctx context.Context, list []listedTodo) map[string]string {
	e, err := s.kbGet(ctx)
	if err != nil {
		return nil
	}
	out := map[string]string{}
	for _, t := range list {
		if _, done := out[t.Project]; t.Project == "" || done {
			continue
		}
		if p := e.FindProject(t.Project); p != nil && p.Name != "" {
			out[t.Project] = p.Name
		}
	}
	return out
}

// projectionFacts is listed row t as the projection reads it: the fields a decoder of the row's JSON would fill (the
// test TestProjectionFactsMatchTheList holds the two together), the sessions of its active runs, and the owner's zone.
func projectionFacts(t listedTodo, sessions map[[2]string]proto.Session, loc *time.Location) projection.Facts {
	f := projection.Facts{ID: t.ID, Title: t.Title, DoneAt: t.DoneAt, CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt,
		Origin: t.Origin, PlanID: t.PlanID, StartError: t.StartError, DoneBy: t.DoneBy, ReopenedAt: t.ReopenedAt,
		CancelledAt: t.CancelledAt, CancelledReason: t.CancelledReason, Quiet: t.Quiet, PRURL: t.PRURL,
		DueAt: t.DueAt, DueDate: t.DueDate, Priority: t.Priority, Project: t.Project, Kind: t.Kind, SeenAt: t.SeenAt,
		PartOf: t.PartOf, Loc: loc}
	// HOOK(#10226 restores): once Todo carries them, f.RestoredAt, f.SomedayUntil = t.RestoredAt, t.SomedayUntil
	if t.Agents != nil {
		f.Agents = make([]projection.Run, len(t.Agents))
	}
	for i, a := range t.Agents {
		f.Agents[i] = projection.Run{MachineID: a.MachineID, SessionID: a.SessionID, ReleasedAt: a.ReleasedAt,
			Title: a.Title, StartError: a.StartError, DismissedAt: a.DismissedAt}
		if c, ok := sessions[[2]string{a.MachineID, a.SessionID}]; ok && a.ReleasedAt == nil && a.DismissedAt == nil {
			f.Sessions = append(f.Sessions, c)
		}
	}
	if v := t.Supervision; v != nil {
		f.Supervision = &projection.Supervision{State: v.State, PendingSince: v.PendingSince, UpdatedAt: v.UpdatedAt}
		if p := v.Parked; p != nil {
			f.Supervision.Parked = &projection.Parked{Reason: p.Reason, Until: p.Until, WaitingOn: p.WaitingOn}
		}
		if v.Reviews != nil {
			f.Supervision.Reviews = make([]projection.Review, len(v.Reviews))
		}
		for i, r := range v.Reviews {
			f.Supervision.Reviews[i] = projection.Review{At: r.At, Verdict: r.Verdict, Reason: r.Reason, Recommend: r.Recommend}
		}
		if v.Progress != nil {
			f.Supervision.Progress = &projection.Progress{At: v.Progress.At}
		}
	}
	if p := t.PRStatus; p != nil {
		f.PRStatus = &projection.PRStatus{URL: p.URL, State: p.State, Draft: p.Draft, CheckedAt: p.CheckedAt}
	}
	for _, p := range t.OtherPRs {
		f.OtherPRs = append(f.OtherPRs, projection.PRStatus{URL: p.URL, State: p.State, Draft: p.Draft, CheckedAt: p.CheckedAt})
	}
	if d := t.Delivery; d != nil {
		f.Delivery = &projection.Delivery{State: d.State, At: d.At}
	}
	if a := t.Ask; a != nil {
		f.Ask = &projection.Ask{Outcome: a.Outcome, Verdict: a.Verdict, Recommend: a.Recommend, AskedAt: a.AskedAt,
			Question: a.Question, Deadline: a.Deadline, Default: a.Default, ForBriefing: a.ForBriefing}
		for _, o := range a.Options {
			f.Ask.Options = append(f.Ask.Options, assistantwire.OwnerAskOption{Label: o.Label, LeadsTo: o.LeadsTo})
		}
	}
	if r := t.DecisionReview; r != nil {
		f.DecisionReview = &projection.DecisionReview{ReviewAt: r.ReviewAt, DismissalID: r.DismissalID}
	}
	return f
}
