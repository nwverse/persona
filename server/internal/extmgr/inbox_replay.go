package extmgr

import (
	"context"
	"fmt"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/connector"
)

func (h *Manager) replayLarkInbox(ctx context.Context, in assistantwire.InboxLarkReplay) error {
	h.inbox.init()
	if err := h.inbox.lock(ctx); err != nil {
		return err
	}
	defer h.inbox.unlock()
	if !h.assistantUp() {
		return errAssistantOffline
	}
	// A recovery must not pull any earlier, held round into this exact-ID request.
	if h.inbox.held.Load() != nil {
		return fmt.Errorf("an earlier Inbox round is pending; retry after it is delivered")
	}
	if h.connectors == nil || !h.connectors.Feeds("larkcli", connector.FeedInbox) {
		return fmt.Errorf("lark-cli Inbox is not enabled")
	}
	c := h.connectors.Get("larkcli")
	replay, ok := c.(interface {
		ReplayMessages(context.Context, string, string, string, []string) ([]connector.InboxThing, error)
	})
	if !ok || connector.StatusOf(ctx, c).State != connector.StateOK {
		return fmt.Errorf("lark-cli Inbox is not available")
	}
	things, err := replay.ReplayMessages(ctx, in.ChatID, in.Start, in.End, in.MessageIDs)
	if err != nil {
		return err
	}
	h.vetThings(c, things)
	src := assistantwire.InboxSource{ID: c.ID(), Name: connector.DisplayName(ctx, c), Things: things}
	withAbilities(&src, c)
	round := assistantwire.InboxRound{Epoch: h.inbox.epoch, Seq: h.inbox.seq.Add(1), Round: assistantwire.RoundPoll,
		Replay: true, Sources: []assistantwire.InboxSource{src}}
	return h.callAssistant(ctx, assistantwire.MethodInboxCollected, round, nil)
}
