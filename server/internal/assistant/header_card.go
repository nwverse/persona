package assistant

import (
	"time"

	"github.com/nwverse/persona/server/internal/db"
)

// What may be a card in the main conversation's header (docs/assistant/todos.md "Matters"): the hub's verdict, so every
// client agrees. Two kinds of card, and only a matter (a concern) is ever told as finished:
//
//   - needs_user: the owner must decide. A concern with an ask (concern_file), or an open to-do waiting for the owner's
//     decision (a decide ask, or its supervisor reviewed it needs_user);
//   - finished: a concern the main conversation closed with concern_report and the owner has not confirmed since
//     (confirmed_at at or after the report). It stays, however old, until they press Got it (PUT …/confirm), so the
//     answer to what they asked is never missed.
//
// A task never gets finished, inside a matter or not: one its supervisor or the done judge accepted does not wait for
// the owner (#3356) and raises no card; its result reaches the owner in the matter's report. A matter whose tasks
// are all finished but which nobody reported raises none either.
//
// GET /api/v1/todos carries the verdict on every to-do as `header_card` ("" for none).
const (
	headerNeedsUser = "needs_user"
	headerFinished  = "finished"
)

// headerCard is to-do t's header card (above): ask its open ask (nil for none), concernAsk the question a concern
// holds (concern_file; "" for none).
func headerCard(t Todo, ask *todoAsk, concernAsk string) string {
	if t.isConcern() {
		switch {
		case t.DoneAt == nil && concernAsk != "":
			return headerNeedsUser
		case t.DoneAt != nil && reportedConcern(t) && !confirmedSinceReport(t):
			return headerFinished
		}
		return ""
	}
	if t.DoneAt != nil || t.CancelledReason != "" {
		return "" // a finished task never waits for the owner
	}
	v := t.Supervision
	if v != nil && v.State == reviewCancelled {
		return ""
	}
	if ask != nil && ask.Outcome == outcomeDecide || v != nil && v.State == reviewNeedsUser {
		return headerNeedsUser
	}
	return ""
}

// reportedConcern: concern t is closed, by concern_report (the only way a concern closes).
func reportedConcern(t Todo) bool {
	return t.isConcern() && t.DoneAt != nil && t.DoneBy == db.DoneByReported
}

// confirmedSinceReport: the owner confirmed to-do t at or after it was closed (for a concern, its latest report).
func confirmedSinceReport(t Todo) bool {
	return t.DoneAt != nil && t.ConfirmedAt != nil && !t.ConfirmedAt.Before(finishedAt(t))
}

// finishedAt is when closed to-do t got its conclusion: its close. Zero while it is open.
func finishedAt(t Todo) time.Time {
	if t.DoneAt == nil {
		return time.Time{}
	}
	return *t.DoneAt
}
