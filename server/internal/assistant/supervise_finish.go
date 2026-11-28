package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// promoteSupervision is monotone within a wake-up: a later silent finish cannot hide a user action.
func (s *Service) promoteSupervision(self string) error {
	ctx := context.Background()
	key := s.supervisionHoldKey(ctx, self)
	b := &s.supervise
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := s.loadSupervisionHoldsLocked(ctx); err != nil {
		return err
	}
	return s.promoteSupervisionLocked(ctx, key)
}

func (s *Service) promoteSupervisionLocked(ctx context.Context, key string) error {
	b := &s.supervise
	now := time.Now()
	if hold := b.holds[key]; hold.active(now) {
		if !hold.Visible {
			hold.Visible = true
			b.holds[key] = hold
			b.holdsDirty = true
		}
		if err := s.saveSupervisionHoldsLocked(ctx); err != nil {
			return err
		}
		return s.markStalledHandledLocked(ctx, hold.Stalled, now)
	}
	if pending, ok := b.pendingHolds[key]; ok && pending.hold.active(now) {
		pending.hold.Visible = true
		b.pendingHolds[key] = pending
	}
	return s.saveSupervisionHoldsLocked(ctx)
}

// A missed transcript check can precede a delivered relay. Stop suppressing pushes,
// but let the finishing tool verify this exact delivery before restoring its hold.
func (s *Service) deferSupervisionHold(machine, id, onceID string, at time.Time) {
	b := &s.supervise
	b.mu.Lock()
	hold, ok := b.holds[id]
	if !ok || hold.At != at {
		b.mu.Unlock()
		return
	}
	if b.pendingHolds == nil {
		b.pendingHolds = make(map[string]pendingSupervisionHold)
	}
	delete(b.holds, id)
	b.pendingHolds[id] = pendingSupervisionHold{hold: hold, machine: machine, clientID: proto.SegmentOnceIDPrefix + onceID}
	b.holdsDirty = true
	ctx := context.Background()
	_ = s.saveSupervisionHoldsLocked(ctx) // the hold sweep retries a failed write
	b.mu.Unlock()
	s.publish(ctx)
}

func (s *Service) pendingSupervisionIsCurrent(ctx context.Context, id string, pending pendingSupervisionHold) bool {
	ctx, cancel := context.WithTimeout(ctx, relayCheckWait)
	defer cancel()
	var page struct{ Items []proto.Item }
	if err := s.call(ctx, pending.machine, DeviceSessionItems, map[string]any{"session_id": id, "turns": 1, "lazy_images": true, "strict_read": true}, &page); err != nil {
		return false
	}
	for i := len(page.Items) - 1; i >= 0; i-- {
		it := page.Items[i]
		if it.Role != "user" || (it.Kind != proto.KindText && it.Kind != proto.KindImage) {
			continue
		}
		src := prompts.ItemSource(it)
		return !it.Queued && it.ClientMsgID == pending.clientID && src != nil && src.Kind == proto.SourceSupervision
	}
	return false
}

// quietInternalAcceptedReview uses only the current plan binding and supervision
// ledger. Unknown provenance fails open as user-visible; titles never participate.
func (s *Service) quietInternalAcceptedReview(ctx context.Context, todoID string, userCheck bool) bool {
	if userCheck {
		return false
	}
	t, ok := s.todoNow(todoID)
	if !ok || t.Supervision == nil {
		return false
	}
	plan, index, err := s.currentPlanAssignment(ctx, t)
	if err != nil || plan == nil || index < 0 || index >= len(plan.Tasks) {
		return false
	}
	task := plan.Tasks[index]
	if task.UserRequested {
		return false
	}
	if task.Parent != "" || t.Supervision.Round > 0 {
		return true
	}
	review := t.Supervision.AutoReview
	return review != nil && review.Round == t.Supervision.Round && (review.State == autoClean || review.State == autoBlocking)
}

// toolSupervisionFinish records the caller's disposition in its persisted MCP result.
// The transcript remains intact; clients fold the entire triage turn, not just its input.
func (s *Service) toolSupervisionFinish(ctx context.Context, self string, args json.RawMessage) (string, error) {
	var a struct {
		Visibility string `json:"visibility"`
		Summary    string `json:"summary"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", err
	}
	a.Summary = strings.TrimSpace(a.Summary)
	if (a.Visibility != "silent" && a.Visibility != "user") || a.Summary == "" || utf8.RuneCountInString(a.Summary) > 1000 {
		return "", errors.New("visibility must be silent or user, with a summary of 1–1000 characters")
	}
	key := s.supervisionHoldKey(ctx, self)
	b := &s.supervise
	b.mu.Lock()
	if err := s.loadSupervisionHoldsLocked(ctx); err != nil {
		b.mu.Unlock()
		return "", err
	}
	hold := b.holds[key]
	pending, waiting := b.pendingHolds[key]
	if !hold.active(time.Now()) && waiting && pending.hold.active(time.Now()) {
		b.mu.Unlock()
		current := s.pendingSupervisionIsCurrent(ctx, key, pending)
		b.mu.Lock()
		// Take the latest visibility, but require the same attempt after the transcript read.
		if latest := b.pendingHolds[key]; current && latest.hold.At == pending.hold.At && latest.hold.active(time.Now()) {
			b.holds[key] = latest.hold
			b.holdsDirty = true
			if err := s.saveSupervisionHoldsLocked(ctx); err != nil {
				delete(b.holds, key)
				b.mu.Unlock()
				return "", err
			}
			delete(b.pendingHolds, key)
			defer s.publish(ctx)
		}
		hold = b.holds[key]
		if hold.At != pending.hold.At {
			hold = supervisionHold{}
		}
	}
	if !hold.active(time.Now()) || a.Visibility == "silent" && hold.Visible {
		b.mu.Unlock()
		return "", errors.New("this is not an unpromoted supervision turn; normal user messages cannot be hidden")
	}
	level := "triage"
	if a.Visibility == "user" {
		level = "user"
		if err := s.promoteSupervisionLocked(ctx, key); err != nil {
			b.mu.Unlock()
			return "", err
		}
	} else if err := s.markStalledHandledLocked(ctx, hold.Stalled, time.Now()); err != nil {
		b.mu.Unlock()
		return "", err
	}
	b.mu.Unlock()
	body, err := json.Marshal(map[string]string{"visibility": a.Visibility, "summary": a.Summary, "level": level})
	return string(body), err
}
