package assistant

import (
	"context"
	"fmt"
	"slices"
	"time"
)

// supervisorVerified says supervised task t was closed accepted with its evidence checked, and the owner was not asked
// to look themselves: its supervisor (the main conversation or a leader) did so with task_review accepted and no
// user_check, or the done judge's spot check did (judgeClose, #3356) on anything but a big feature (high priority,
// #3205), which the owner tries. What it asked the owner is then passed for them, not left for them (#3240). A task with
// no supervisor still asks; so does a big feature's "Please verify" (verifyBySupervisor).
func supervisorVerified(t Todo) bool {
	v := t.Supervision
	if v == nil || v.Supervisor == "" || v.state(t) != reviewAccepted || len(v.Reviews) == 0 {
		return false
	}
	r := v.Reviews[len(v.Reviews)-1]
	if r.Verdict != reviewAccepted || r.UserCheck {
		return false
	}
	return r.By == "" || r.By == reviewerJudge && t.Priority != "high"
}

// verifyBySupervisor passes task id's "Please verify" for its supervisor, as the owner's "Pass" would (the task is
// verified, its cards say who passed it), and so a done or cannot conclusion its owner was to acknowledge; false when
// there is none waiting (a decision, a claim still going out, one passed already or from before the latest reopen) or
// it is a big feature's "Please verify", which the owner tries themselves (high priority, ownerVerifies, #3205).
// errAcceptStore: the asks could not be read or saved.
func (s *Service) verifyBySupervisor(ctx context.Context, id string) (bool, error) {
	t, todoOK := s.todoNow(id)
	b := &s.accept
	b.mu.Lock()
	if !s.loadAcceptLocked(ctx) {
		b.mu.Unlock()
		return false, errAcceptStore
	}
	a, ok := b.asks[id]
	if !ok || decideAsk(a) || a.AskedAt.IsZero() || !a.AcceptedAt.IsZero() || todoOK && acceptAskStale(a, t) ||
		!a.Conclusion && t.Priority == "high" {
		b.mu.Unlock()
		return false, nil
	}
	a.AcceptedAt, a.BySupervisor = time.Now().UTC(), true
	if err := s.saveAcceptErrLocked(ctx); err != nil {
		a.AcceptedAt, a.BySupervisor = time.Time{}, false
		b.mu.Unlock()
		return false, fmt.Errorf("%w: %w", errAcceptStore, err)
	}
	cp := *a
	b.mu.Unlock()
	s.askChanged(ctx)
	if cp.Conclusion {
		s.updateAskCards(ctx, id, cp) // "verified" is for deployed work: a conclusion's card only says it is checked
	} else {
		s.taskVerified(ctx, id, cp)
	}
	return true, nil
}

// verifySupervisedAsks passes every "Please verify" or done conclusion still waiting whose task its supervisor accepted
// (supervisorVerified): the ones asked before #3240, and any a race left behind.
func (s *Service) verifySupervisedAsks(ctx context.Context) {
	if err := s.ensureTodosLoaded(ctx); err != nil {
		s.logf("acceptance: supervisor verify: %v", err)
		return
	}
	b := &s.accept
	b.mu.Lock()
	if !s.loadAcceptLocked(ctx) {
		b.mu.Unlock()
		return
	}
	var ids []string
	for id, a := range b.asks {
		if !decideAsk(a) && !a.AskedAt.IsZero() && a.AcceptedAt.IsZero() {
			ids = append(ids, id)
		}
	}
	b.mu.Unlock()
	slices.Sort(ids)
	for _, id := range ids {
		if t, ok := s.todoNow(id); !ok || !supervisorVerified(t) {
			continue
		}
		if ok, err := s.verifyBySupervisor(ctx, id); err != nil {
			s.logf("task %s: supervisor verify: %v", id, err)
		} else if ok {
			s.logf("task %s: its \"Please verify\" passed by its supervisor's acceptance", id)
		}
	}
}
