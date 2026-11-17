package assistant

import (
	"context"
	"reflect"
)

// An external PR object is not a merge-review wait: no reviewer readiness, task PR association, or prose match
// substitutes for this exact binding. A terminal observation asks for reassessment, not acceptance or deployment.
func externalParkPRURL(r *TaskReview) string {
	if r == nil || r.WaitingOn != parkExternalEvent || r.Evidence == nil || r.Evidence.Kind != "external_event" {
		return ""
	}
	source := triageNormalizePRURL(r.Evidence.URL)
	if source == "" || ciPRURL.FindString(source) != source {
		return ""
	}
	return source
}

func (s *Service) applyExternalParkPRStatus(ctx context.Context, t Todo, status TodoPRStatus) error {
	held := t.Supervision.parking()
	if held == nil || held.At.IsZero() || externalParkPRURL(held) != status.URL ||
		status.CheckedAt.IsZero() || status.CheckedAt.Before(held.At) ||
		status.State != "open" && status.State != "merged" && status.State != "closed" {
		return nil
	}
	s.todos.writing.Lock()
	defer s.todos.writing.Unlock()
	v, changed, err := s.updateSupervision(ctx, t.ID, func(v *Supervision) bool {
		p := v.parking()
		cur, ok := s.todoNow(t.ID)
		if !ok || !taskOpen(cur) || p == nil || !p.At.Equal(held.At) || externalParkPRURL(p) != status.URL ||
			p.ExternalPR != nil && status.CheckedAt.Before(p.ExternalPR.CheckedAt) {
			return false
		}
		if p.ExternalPR != nil && reflect.DeepEqual(*p.ExternalPR, status) {
			return false
		}
		receipt := status
		p.ExternalPR = &receipt
		if status.State == "merged" || status.State == "closed" {
			v.State = reviewPending
		}
		return true
	})
	if err != nil {
		return err
	}
	if changed && v.State == reviewPending {
		s.signal(superviseSignal{Todo: t.ID, Kind: sigReview, Key: reviewKey(&v),
			Detail: "Bound external PR " + status.State + ": " + status.URL +
				". Reassess the task; this is not evidence of acceptance, deployment or another dependency succeeding."})
	}
	return nil
}
