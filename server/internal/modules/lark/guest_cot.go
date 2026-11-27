package lark

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/botcard"
	"github.com/nwverse/persona/server/internal/i18n"
)

// A guest's process bubble (docs/extensions/bots.md "Group access for teammates"). It is built only from tool calls
// the hub itself observes: a fixed safe title per call, a fixed result line, no arguments, no output, no thinking.
// Only when the owner switched topic COT on, and only for a turn that calls an owner-listed tool. A problem here is
// logged and never returned: it must not fail the tool call or the reply.

// GuestToolStep records one sanitized step and returns the func that finishes it. The func is a no-op when the step
// was not shown (COT off, the turn closed, too many steps, or the agent message could not be made).
func (l *Layer) GuestToolStep(ctx context.Context, key, name string) func(bool) {
	none := func(bool) {}
	st, err := l.Status(ctx)
	if err != nil || !st.Features.TopicCot {
		return none
	}
	s := l.chat()
	r := s.guestGet(key)
	if r == nil || r.Turn == nil || r.Turn.State != guestStateAccepted || r.Turn.CotOff || r.Turn.Steps >= guestCotStepsMax {
		return none
	}
	step := s.guestCotStep(ctx, key, name)
	if step == "" {
		return none
	}
	return func(failed bool) { s.guestCotResult(key, step, failed) }
}

// guestCotStep opens the agent message on the first step and appends one tool call. "" when nothing was shown.
func (s core) guestCotStep(ctx context.Context, key, name string) string {
	b := s.t.guests()
	b.mu.Lock()
	defer b.mu.Unlock()
	r := b.records[key]
	if r == nil || r.Turn == nil || r.Turn.CotOff {
		return ""
	}
	turn := r.Turn
	l := s.botLang(ctx)
	safe := botcard.GuestSafeToolName(name)
	if turn.CotID == "" {
		var created struct {
			CotID     string `json:"cot_id"`
			MessageID string `json:"message_id"`
		}
		if err := s.appCall(ctx, assistantwire.FeishuCotCreate, map[string]any{"guest": turn.ID}, &created); err != nil || created.CotID == "" {
			s.logf("lark: guest %s: cot: %v", turn.ID, err)
			turn.CotOff = true
			_ = s.guestSave(context.Background())
			return ""
		}
		turn.CotID, turn.CotMsg = created.CotID, created.MessageID
		if err := s.appCall(ctx, assistantwire.FeishuCotAppend, map[string]any{"cot_id": turn.CotID, "message_id": turn.CotMsg,
			"events": botcard.CotStart(guestHash(key), turn.ID, "p0")}, nil); err != nil {
			s.guestCotOff(turn)
			return ""
		}
	}
	turn.Steps++
	step := fmt.Sprintf("s%d", turn.Steps)
	title := i18n.T(l, "assistant-feishu.guest_cot_step", i18n.Args{"tool": safe})
	if err := s.appCall(ctx, assistantwire.FeishuCotAppend, map[string]any{"cot_id": turn.CotID, "message_id": turn.CotMsg,
		"events": botcard.CotToolCall(step, "p0", safe, title, "default")}, nil); err != nil {
		s.guestCotOff(turn)
		return ""
	}
	_ = s.guestSave(context.Background())
	return step
}

// guestCotResult appends the fixed result line of one step.
func (s core) guestCotResult(key, step string, failed bool) {
	b := s.t.guests()
	b.mu.Lock()
	defer b.mu.Unlock()
	r := b.records[key]
	if r == nil || r.Turn == nil || r.Turn.CotID == "" || r.Turn.CotOff {
		return
	}
	key2 := "assistant-feishu.guest_cot_done"
	if failed {
		key2 = "assistant-feishu.guest_cot_failed"
	}
	line := i18n.T(s.botLang(context.Background()), key2)
	if err := s.appCall(context.Background(), assistantwire.FeishuCotAppend, map[string]any{"cot_id": r.Turn.CotID,
		"message_id": r.Turn.CotMsg, "events": []botcard.CotEvent{botcard.CotToolResult(step, line, false)}}, nil); err != nil {
		s.guestCotOff(r.Turn)
	}
}

// guestCotFinish closes the agent message: finished when the reply was posted, an error otherwise. A turn that called
// no owner-listed tool has no agent message, and nothing is posted for it.
func (s core) guestCotFinish(ctx context.Context, key string, ok bool) {
	b := s.t.guests()
	b.mu.Lock()
	defer b.mu.Unlock()
	r := b.records[key]
	if r == nil || r.Turn == nil || r.Turn.CotID == "" {
		return
	}
	turn := r.Turn
	if ok {
		_ = s.appCall(ctx, assistantwire.FeishuCotAppend, map[string]any{"cot_id": turn.CotID, "message_id": turn.CotMsg,
			"events": botcard.CotFinish(guestHash(key), turn.ID, "p0")}, nil)
		return
	}
	_ = s.appCall(ctx, assistantwire.FeishuCotComplete, map[string]any{"cot_id": turn.CotID, "message_id": turn.CotMsg, "reason": "error"}, nil)
}

// guestCotOff stops the bubble for the turn and closes the one already opened, so it does not keep spinning.
func (s core) guestCotOff(turn *guestTurnRecord) {
	turn.CotOff = true
	if turn.CotID != "" {
		_ = s.appCall(context.Background(), assistantwire.FeishuCotComplete, map[string]any{"cot_id": turn.CotID, "message_id": turn.CotMsg, "reason": "error"}, nil)
		turn.CotID = ""
	}
	_ = s.guestSave(context.Background())
}

// guestHash is a short hash of the conversation key, so the bubble's thread id carries no chat id.
func guestHash(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:8])
}
