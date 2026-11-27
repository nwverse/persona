package assistant

import (
	"testing"
	"time"
)

// The header card rule (header_card.go): only a concern closed by concern_report and not confirmed since is ever
// "finished". A task never is: one its supervisor or the done judge accepted does not wait for the owner (#3356),
// inside a matter or not. These are table tests over the pure rule.
func TestHeaderCardRule(t *testing.T) {
	done := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	confirmed := func(todo Todo, at time.Time) Todo {
		todo.ConfirmedAt = &at
		return todo
	}
	concern := func(isDone bool) Todo {
		todo := Todo{ID: "todo_c", Kind: "concern"}
		if isDone {
			todo.DoneAt = &done
			todo.DoneBy = "reported"
		}
		return todo
	}
	task := func(state string, doneAt *time.Time) Todo {
		todo := Todo{ID: "todo_t"}
		if state != "" {
			todo.Supervision = &Supervision{State: state}
		}
		todo.DoneAt = doneAt
		return todo
	}
	for _, c := range []struct {
		name       string
		todo       Todo
		ask        *todoAsk
		concernAsk string
		want       string
	}{
		// Concerns.
		{"open concern, no ask", concern(false), nil, "", ""},
		{"open concern with an ask", concern(false), nil, "which way?", headerNeedsUser},
		{"reported concern, not confirmed", concern(true), nil, "", headerFinished},
		{"reported concern, confirmed when closed", confirmed(concern(true), done), nil, "", ""},
		{"reported concern, confirmed before the report", confirmed(concern(true), done.Add(-time.Minute)), nil, "", headerFinished},
		{"reported concern's ask never overrides the tell card", concern(true), nil, "still asking", headerFinished},
		// Tasks: finished never waits, whatever accepted it.
		{"open task, no ask", task("", nil), nil, "", ""},
		{"open task, a decision waits", task("", nil), &todoAsk{Outcome: outcomeDecide}, "", headerNeedsUser},
		{"open task, reviewed needs_user", task(reviewNeedsUser, nil), nil, "", headerNeedsUser},
		{"open task, a verify-only ask waits for nothing", task("", nil), &todoAsk{}, "", ""},
		{"cancelled task waits for nothing", task(reviewCancelled, nil), &todoAsk{Outcome: outcomeDecide}, "", ""},
		{"done task, accepted by its supervisor", task(reviewAccepted, &done), nil, "", ""},
		{"done task, no review", task("", &done), nil, "", ""},
		{"done task, confirmed anyway", confirmed(task(reviewAccepted, &done), done), nil, "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := headerCard(c.todo, c.ask, c.concernAsk); got != c.want {
				t.Fatalf("header_card %q, want %q", got, c.want)
			}
		})
	}
}

// reportedConcern and the confirmed-since-report reading the rule uses.
func TestReportedConcernAndConfirmedSinceReport(t *testing.T) {
	done := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	plain := Todo{ID: "todo_t", DoneAt: &done}
	if reportedConcern(plain) {
		t.Fatal("a task is never a reported concern")
	}
	closed := Todo{ID: "todo_c", Kind: "concern", DoneAt: &done, DoneBy: "reported"}
	if !reportedConcern(closed) {
		t.Fatal("a concern closed by concern_report is a reported concern")
	}
	open := Todo{ID: "todo_c2", Kind: "concern"}
	if reportedConcern(open) || confirmedSinceReport(open) {
		t.Fatal("an open concern is neither reported nor confirmed-since-report")
	}
	if confirmedSinceReport(closed) {
		t.Fatal("a report without a confirm still waits")
	}
	at := done.Add(time.Minute)
	if got := confirmedSinceReport(Todo{ConfirmedAt: &at, DoneAt: &done, DoneBy: "reported", Kind: "concern"}); !got {
		t.Fatal("a confirm at or after the report counts")
	}
	before := done.Add(-time.Minute)
	if got := confirmedSinceReport(Todo{ConfirmedAt: &before, DoneAt: &done, DoneBy: "reported", Kind: "concern"}); got {
		t.Fatal("a confirm before the report does not cover it")
	}
}
