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
func (s *Service) promoteSupervision(self string) {
	key := s.supervisionHoldKey(context.Background(), self)
	b := &s.supervise
	b.mu.Lock()
	defer b.mu.Unlock()
	b.init()
	if _, held := b.hold[key]; held || !b.pendingHold[key].at.IsZero() {
		b.visible[key] = true
	}
}

type pendingSupervisionHold struct {
	at       time.Time
	machine  string
	clientID string
}

func (s *Service) hasSupervisionHold(id string) bool {
	b := &s.supervise
	b.mu.Lock()
	defer b.mu.Unlock()
	_, held := b.hold[id]
	return held || !b.pendingHold[id].at.IsZero()
}

// A missed transcript check is not proof that the relay never started. Stop
// suppressing pushes until the matching logical delivery is visible, while
// remembering which attempt and promotion state a finishing tool can verify.
func (s *Service) deferSupervisionHold(machine, id, onceID string, at time.Time) {
	b := &s.supervise
	b.mu.Lock()
	if held, ok := b.hold[id]; !ok || held != at {
		b.mu.Unlock()
		return
	}
	delete(b.hold, id)
	b.pendingHold[id] = pendingSupervisionHold{at, machine, proto.SegmentOnceIDPrefix + onceID}
	b.mu.Unlock()
	s.publish(context.Background())
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
	_, held := b.hold[key]
	pending := b.pendingHold[key]
	restored := false
	if !held && !pending.at.IsZero() && time.Since(pending.at) <= superviseHoldMax {
		b.mu.Unlock()
		current := s.pendingSupervisionIsCurrent(ctx, key, pending)
		b.mu.Lock()
		// An owner send, another relay, or a newer attempt can replace the hold while the read is in flight.
		if current && b.pendingHold[key] == pending {
			b.hold[key] = pending.at
			delete(b.pendingHold, key)
			restored = true
		}
		held = b.hold[key] == pending.at
	}
	if restored {
		defer s.publish(ctx)
	}
	if !held || a.Visibility == "silent" && b.visible[key] {
		b.mu.Unlock()
		return "", errors.New("this is not an unpromoted supervision turn; normal user messages cannot be hidden")
	}
	level := "triage"
	if a.Visibility == "user" {
		level = "user"
		b.visible[key] = true
	}
	b.mu.Unlock()
	body, err := json.Marshal(map[string]string{"visibility": a.Visibility, "summary": a.Summary, "level": level})
	return string(body), err
}
