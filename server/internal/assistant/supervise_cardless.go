package assistant

import (
	"context"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// A supervised task judged finished waits in pending_review for its supervisor's review, and a supervisor's review is
// not the owner's: it never enters the owner's Needs you by itself (docs/assistant/unified-entry.md "Needs you and In
// progress"). When such a review has sat past the settle time with nothing the owner can answer — no needs_user
// conclusion, no accepted user_check, no ask_user card, no pending worker interaction — it can hide indefinitely
// (#12648). The watch catches that state here once per review round and hands the task back to its supervisor. It
// never synthesizes a question from chat text and cannot tell an owner wait from a late review, so the rendered
// message names the whole review vocabulary; the supervisor picks the verdict the work actually needs.

// sigCardlessWait: a pending_review task has stayed with no card the owner can answer past the settle time.
const sigCardlessWait = "cardless_wait"

// cardlessSettle is how long a card-less review waits before the supervisor is told, matching decisionSettle: a card
// being written or an answer on its way is not mistaken for a silent wait.
const cardlessSettle = 10 * time.Minute

// cardlessSince is when the current pending_review round began: the ledger's PendingSince, else its last write.
func cardlessSince(t Todo) time.Time {
	v := t.Supervision
	if v == nil {
		return time.Time{}
	}
	if v.PendingSince != nil {
		return *v.PendingSince
	}
	return v.UpdatedAt
}

// cardlessWorkersStopped says none of the task's active workers still works or holds a card the owner can answer.
// It judges only the sessions it is given; callers must first prove those sessions are complete with
// cardlessAgentsReachable, because taskAgentsIn drops a machine whose session list cannot be read.
func cardlessWorkersStopped(agents []proto.Session) bool {
	for _, c := range agents {
		if c.State == proto.StateNeedsYou || c.PendingInteractions > 0 {
			return false // a real pending card stands for the owner
		}
		if c.State == proto.StateWorking || c.Starting {
			return false // the work goes on; this is not a settled review wait
		}
	}
	return true
}

// cardlessAgentsReachable reports that every machine hosting one of t's active agents answered its session list this
// pass. taskAgentsIn records a failed read in lists but drops that machine's sessions from the slice, so without this
// check a transient device/RPC failure would look like every worker having stopped and spend the round's one
// hand-back (and its CardlessToldAt stamp) on an unknown state. A failed pass simply retries on a later watch tick.
func cardlessAgentsReachable(t Todo, lists machineSessions) bool {
	for _, a := range t.Agents {
		if a.ReleasedAt != nil || a.DismissedAt != nil {
			continue
		}
		if l, ok := lists[a.MachineID]; ok && l.err != nil {
			return false
		}
	}
	return true
}

// cardlessWaitSignal hands a card-less pending_review task back to its supervisor, once per review round. It changes
// nothing but the round's hand-back marker (CardlessToldAt), so a restart cannot tell the same round twice; a new
// review round clears the marker (cardlessToldSince).
func (s *Service) cardlessWaitSignal(ctx context.Context, t Todo, agents []proto.Session, lists machineSessions, now time.Time) (superviseSignal, bool) {
	v := t.Supervision
	if v == nil || t.DoneAt != nil || v.state(t) != reviewPending || v.CardlessToldAt != nil {
		return superviseSignal{}, false
	}
	since := cardlessSince(t)
	if since.IsZero() || now.Sub(since) < cardlessSettle {
		return superviseSignal{}, false
	}
	// An acceptance/conclusion ask from the current review round that the owner has not answered already gives them
	// something to answer. acceptAskStale covers asks of an earlier round (a reopened or re-reviewed task); the ended
	// test (acceptAskEnded) is deliberately not used: a decision ask written while the task still waits for review is
	// a live card even though its ledger is not needs_user yet.
	if a, ok := s.askCopy(t.ID); ok && !acceptAskStale(&a, t) && a.AcceptedAt.IsZero() {
		return superviseSignal{}, false
	}
	// An unreadable worker is not a stopped worker: hold the hand-back until its machine answers, rather than stamp
	// the round on an unknown state.
	if !cardlessAgentsReachable(t, lists) {
		return superviseSignal{}, false
	}
	if !cardlessWorkersStopped(agents) {
		return superviseSignal{}, false
	}
	_, changed, err := s.updateSupervision(ctx, t.ID, func(v *Supervision) bool {
		if v.state(t) != reviewPending || v.CardlessToldAt != nil {
			return false
		}
		if current, ok := s.todoNow(t.ID); !ok || current.DoneAt != nil {
			return false
		}
		stamped := now.UTC()
		v.CardlessToldAt = &stamped
		return true
	})
	if err != nil || !changed {
		if err != nil {
			s.logf("task %s: card-less wait: %v (retried by the watch)", t.ID, err)
		}
		return superviseSignal{}, false
	}
	s.logf("task %s: waits for review with no card the owner can answer: back to its supervisor to open one", t.ID)
	return superviseSignal{Todo: t.ID, Kind: sigCardlessWait, Key: "cardless@" + since.UTC().Format(time.RFC3339Nano),
		Minutes: int(now.Sub(since).Minutes())}, true
}
