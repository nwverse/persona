package assistant

import (
	"context"
	"fmt"
	"time"
)

// A scope or estimate change must not hide an owner wait that exists only in chat (#12648). A supervised task judged
// finished waits in pending_review for its supervisor; when the owner should review or decide but no card stands for
// them — no needs_user conclusion, no user_check ask, no pending worker interaction — extending the estimate or the
// scope with task_scope would push that silent wait out of sight (the estimate_due case). The change is refused until
// the supervisor opens the card, matching the risky-step refusal in applyScope.
//
// It only fires once the review has sat past the same settle window the watch's card-less backstop uses
// (decisionSettle): inside it a card may still be on its way. A parked task is out of scope (its owner-action
// admission and legacy reconciliation live in parked.go), as is needs_user, whose own backstop is undecidableSignal.
func (s *Service) cardlessOwnerWaitGuard(ctx context.Context, t Todo) error {
	v := t.Supervision
	if v == nil || t.DoneAt != nil || v.state(t) != reviewPending {
		return nil
	}
	since := v.UpdatedAt
	if v.PendingSince != nil {
		since = *v.PendingSince
	}
	if time.Now().UTC().Sub(since) < decisionSettle {
		return nil
	}
	if a, ok := s.askCopy(t.ID); ok && !acceptAskStale(&a, t) && a.AcceptedAt.IsZero() {
		return nil // an open ask already gives the owner something to answer
	}
	for _, c := range s.taskAgents(ctx, t) {
		if c.State == "needs_you" || c.PendingInteractions > 0 {
			return nil // a real pending card stands for the owner
		}
	}
	return fmt.Errorf("task %s waits for the owner to review or decide but has no card they can answer "+
		"(no needs_user decision, no user_check verification, no pending question): a chat-only request does not reach Needs you. "+
		"Open the card first — task_review needs_user with why_owner, reason, recommend, options and cost_of_waiting, or accepted with user_check: true — "+
		"and call task_scope again in the turn their answer starts", t.ID)
}
