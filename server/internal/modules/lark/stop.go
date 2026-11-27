package lark

import (
	"context"

	"github.com/nwverse/persona/server/internal/channel"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// turnOver says whether the conversation's last turn ended (its result unseen or already seen) and nothing is being
// opened: no turn to stop. An interrupt there would only press Escape into an idle agent's terminal (the node's
// session.interrupt sends that key, node/node.go) and the owner would be told a turn was stopped that had already
// finished. A state that cannot be told is not over: the owner asked to stop, so it is tried.
func turnOver(s proto.Session) bool {
	return !s.Starting && (s.State == proto.StateUnread || s.State == proto.StateIdle)
}

// stopConversation is typed /stop: it targets the conversation's current turn, independently of any progress card
// (the progress card's own press is the core's, which checks its exact ownership first). The core's TurnPending says
// whether this chat sent a message that has not been seen working, or seen to end a turn, since: the conversation may
// still be read as quiet, so its state cannot say the turn is over. A /stop right after the message is exactly the
// case it is sent for.
func (s core) stopConversation(ctx context.Context, f FeishuFeatures, t botTarget) (kind, message string) {
	l := s.botLang(ctx)
	if err := s.botAllowed(l, f, t); err != nil {
		return "error", err.Error()
	}
	rt, err := s.h.ResolveTarget(ctx, t)
	if err != nil || rt.IsNew || (turnOver(rt.Session) && !s.h.TurnPending(rt.Machine, rt.Session.SessionID)) {
		return "info", i18n.T(l, "assistant-feishu_cards.nothing_running")
	}
	return s.h.InterruptConversation(ctx, rt)
}

// stopProgressCard accepts the existing button payload only while the callback's
// actual message still belongs to a current, watched Feishu run. History and thread
// bindings cannot grant this ownership. Owner/chat authorization remains in extmgr.
func (s *Service) stopProgressCard(ctx context.Context, f FeishuFeatures, t botTarget, mid string) stopOutcome {
	l := s.botLang(ctx)
	if err := botAllowed(l, f, t); err != nil {
		return stopOutcome{kind: "error", message: err.Error()}
	}
	expired := stopOutcome{kind: "info", message: i18n.T(l, "assistant-feishu_cards.card_expired")}
	if mid == "" || t.MachineID == "" || t.SessionID == "" {
		return expired
	}
	c := &s.chans
	c.mu.Lock()
	var run *channel.Run
	var prog *chanLive
	for _, r := range c.st.Runs {
		if r.Channel == channel.Feishu && botTargetOf(r) == t && r.Ext["card"] == mid {
			run, prog = r, c.live[r.ID]
			break
		}
	}
	c.mu.Unlock()
	if run == nil || prog == nil {
		return expired
	}
	rt, err := s.resolveBotTarget(ctx, t)
	if err != nil || rt.isNew || rt.machine != t.MachineID || rt.session.SessionID != t.SessionID {
		return expired
	}
	// Match the lifecycle's lock order (progress, then channel state). Resolution
	// can yield to a finish, fold, recall or replacement; recheck the actual objects.
	prog.mu.Lock()
	defer prog.mu.Unlock()
	c.mu.Lock()
	current := false
	for _, r := range c.st.Runs {
		if r == run {
			current = c.live[r.ID] == prog && r.Channel == channel.Feishu && botTargetOf(r) == t &&
				r.Ext["card"] == mid && !r.Recalled && !c.answering[r.ID] && !prog.finished && prog.failing.IsZero() &&
				prog.stoppedCard != mid && (!turnOver(rt.session) || !r.Seen && !r.Held)
			break
		}
	}
	c.mu.Unlock()
	if !current {
		return expired
	}
	// Keep the progress lock through delivery: concurrent presses act once, while a
	// failed RPC stays retryable. This is an assistant-side ownership check; the
	// node's conversation-level RPC has no atomic turn-generation precondition.
	out := s.interruptBotConversation(ctx, rt)
	if out.kind == "success" {
		prog.stoppedCard = mid
	}
	return out
}
