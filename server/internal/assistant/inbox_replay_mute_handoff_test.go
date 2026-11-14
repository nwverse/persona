package assistant

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/connectorwire"
	"github.com/nwverse/persona/server/internal/proto"
)

const (
	replayMuteSource       = "larkcli"
	replayMuteChat         = "synthetic-chat"
	replayMuteAlertThread  = "synthetic-alert-thread"
	replayMuteOwnerThread  = "synthetic-owner-thread"
	replayMuteAlertLast    = "synthetic-alert-message"
	replayMuteOwnerLast    = "synthetic-owner-message"
	replayMuteAlertSender  = "synthetic-alert-bot"
	replayMuteOwnerSender  = "synthetic-owner"
	replayMuteRootContext  = "Example Alert: service needs attention"
	replayMuteOwnerContext = "Example Owner: @Example Bot please review this alert"
)

func newReplayMuteHandoffHub(t *testing.T, mutes ...InboxMute) *inboxHub {
	t.Helper()
	h := newInboxHub(t)
	if err := h.inboxReady(context.Background()); err != nil {
		t.Fatal(err)
	}
	setSettings(t, h.Service, func(s *Settings) { s.Main = "m1" })
	h.f.setSessions("server", proto.Session{SessionID: "m1", Assistant: true, Status: proto.StatusIdle})
	h.inbox.mu.Lock()
	h.inbox.mutes = append([]InboxMute(nil), mutes...)
	h.inbox.mu.Unlock()
	return h
}

func replayMuteHandoffThings() []connectorwire.InboxThing {
	at := time.Now().UTC()
	chat := connector.Tag{Key: connector.TagChat, Value: replayMuteChat, Name: "Example Chat"}
	return []connectorwire.InboxThing{
		{
			Thread: replayMuteAlertThread, Last: replayMuteAlertLast, Kind: "mention", Title: "Example Alert",
			Sender: "Example Alert Bot", Context: replayMuteRootContext, At: at, Tier: connectorwire.TierAction,
			Tags: []connector.Tag{chat, {Key: connector.TagSender, Value: replayMuteAlertSender, Name: "Example Alert Bot"}},
		},
		{
			Thread: replayMuteOwnerThread, Last: replayMuteOwnerLast, Kind: "bot_handoff", Title: "Example Owner Handoff",
			Sender: "Example Owner", Context: replayMuteRootContext + "\n" + replayMuteOwnerContext, At: at, Tier: connectorwire.TierFYI,
			Tags: []connector.Tag{chat, {Key: connector.TagSender, Value: replayMuteOwnerSender, Name: "Example Owner"}},
		},
	}
}

func replayMuteRound(things []connectorwire.InboxThing) assistantwire.InboxRound {
	return assistantwire.InboxRound{
		Replay: true, Epoch: "synthetic-replay-epoch", Seq: 1, Round: assistantwire.RoundPoll,
		Names:   map[string]string{replayMuteSource: "Example Connector"},
		Sources: []assistantwire.InboxSource{{ID: replayMuteSource, Name: "Example Connector", Things: things}},
	}
}

type replayMuteItemState struct {
	found, open, done bool
	doneBy, tier      string
	agent             bool
	handoffs          int
	context           string
}

func replayMuteItemsByThread(h *inboxHub) map[string]replayMuteItemState {
	h.inbox.mu.Lock()
	defer h.inbox.mu.Unlock()
	out := map[string]replayMuteItemState{}
	for _, it := range h.inbox.items {
		if it.Source == replayMuteSource && it.Connector != nil {
			out[it.Connector.Thread] = replayMuteItemState{
				found: true, open: it.open(), done: it.DoneAt != nil, doneBy: it.DoneBy, tier: it.Tier,
				agent: it.Agent != nil, handoffs: len(it.Handoffs), context: it.Context,
			}
		}
	}
	return out
}

func waitReplayMuteHandoff(t *testing.T, h *inboxHub) {
	t.Helper()
	waitFor(t, "the synthetic owner handoff", func() bool {
		items := replayMuteItemsByThread(h)
		if !items[replayMuteOwnerThread].found || items[replayMuteOwnerThread].handoffs != 1 {
			return false
		}
		return len(toMain(h.f)) == 1
	})
}

func TestInboxReplayAlertMuteKeepsOwnerHandoffOpen(t *testing.T) {
	mute := InboxMute{ID: "synthetic-alert-mute", Source: replayMuteSource, Tag: connector.TagSender, Value: replayMuteAlertSender, Name: "Example Alert Bot"}
	h := newReplayMuteHandoffHub(t, mute)
	if err := h.Service.InboxCollected(context.Background(), replayMuteRound(replayMuteHandoffThings())); err != nil {
		t.Fatal(err)
	}
	waitReplayMuteHandoff(t, h)

	items := replayMuteItemsByThread(h)
	alert, owner := items[replayMuteAlertThread], items[replayMuteOwnerThread]
	if !alert.found || !alert.done || alert.doneBy != "read" || alert.tier != TierAction {
		t.Fatalf("alert card did not keep its sender mute filing: %+v", alert)
	}
	if !owner.found || !owner.open || owner.tier != TierFYI || owner.done || owner.agent || owner.handoffs != 1 {
		t.Fatalf("owner handoff was filed, promoted, or processed: %+v", owner)
	}
	if text := strings.Join(toMain(h.f), "\n"); !strings.Contains(text, replayMuteRootContext) {
		t.Fatalf("Main handoff omitted root alert context: %s", text)
	}
	if starts := h.f.started(); len(starts) != 0 {
		t.Fatalf("processing agent started for replayed items: %+v", starts)
	}
	h.inbox.mu.Lock()
	gotMutes := append([]InboxMute(nil), h.inbox.mutes...)
	h.inbox.mu.Unlock()
	if len(gotMutes) != 1 || gotMutes[0] != mute {
		t.Fatalf("replay changed the configured mute: %+v", gotMutes)
	}
}

func TestInboxReplayWithoutMuteKeepsAlertActionableAndHandsOffOwner(t *testing.T) {
	h := newReplayMuteHandoffHub(t)
	if err := h.Service.InboxCollected(context.Background(), replayMuteRound(replayMuteHandoffThings())); err != nil {
		t.Fatal(err)
	}
	waitReplayMuteHandoff(t, h)

	items := replayMuteItemsByThread(h)
	alert, owner := items[replayMuteAlertThread], items[replayMuteOwnerThread]
	if !alert.found || !alert.open || alert.tier != TierAction || alert.done {
		t.Fatalf("unmuted alert card was not left actionable and open: %+v", alert)
	}
	if !owner.found || !owner.open || owner.tier != TierFYI || owner.done || owner.agent || owner.handoffs != 1 {
		t.Fatalf("owner handoff did not remain FYI/open with one Main handoff: %+v", owner)
	}
	if len(toMain(h.f)) != 1 || !strings.Contains(toMain(h.f)[0], replayMuteRootContext) {
		t.Fatalf("Main handoff calls = %v", toMain(h.f))
	}
	if starts := h.f.started(); len(starts) != 0 {
		t.Fatalf("processing agent started for replayed items: %+v", starts)
	}
}

func TestInboxReplayMatchingOwnerMuteSuppressesMainHandoff(t *testing.T) {
	mute := InboxMute{ID: "synthetic-owner-mute", Source: replayMuteSource, Tag: connector.TagSender, Value: replayMuteOwnerSender, Name: "Example Owner"}
	h := newReplayMuteHandoffHub(t, mute)
	things := replayMuteHandoffThings()[1:]
	if err := h.Service.InboxCollected(context.Background(), replayMuteRound(things)); err != nil {
		t.Fatal(err)
	}
	// Drain the same idempotent manager path synchronously as well: the replay's background pass may still be queued.
	h.Service.forwardLarkMentions(context.Background())

	owner := replayMuteItemsByThread(h)[replayMuteOwnerThread]
	if !owner.found || !owner.done || owner.doneBy != "read" || owner.tier != TierFYI || owner.handoffs != 0 {
		t.Fatalf("matching owner mute did not retain general mute priority: %+v", owner)
	}
	if sent := toMain(h.f); len(sent) != 0 {
		t.Fatalf("matching owner mute still sent a Main handoff: %v", sent)
	}
	if starts := h.f.started(); len(starts) != 0 {
		t.Fatalf("processing agent started for muted handoff: %+v", starts)
	}
}

func TestInboxReplayMatchingChatMuteSuppressesMainHandoff(t *testing.T) {
	mute := InboxMute{ID: "synthetic-chat-mute", Source: replayMuteSource, Tag: connector.TagChat, Value: replayMuteChat, Name: "Example Chat"}
	h := newReplayMuteHandoffHub(t, mute)
	things := replayMuteHandoffThings()[1:]
	if err := h.Service.InboxCollected(context.Background(), replayMuteRound(things)); err != nil {
		t.Fatal(err)
	}
	// The replay's background handoff pass may still be queued; the item is already muted in either ordering.
	h.Service.forwardLarkMentions(context.Background())

	owner := replayMuteItemsByThread(h)[replayMuteOwnerThread]
	if !owner.found || !owner.done || owner.doneBy != "read" || owner.tier != TierFYI || owner.handoffs != 0 {
		t.Fatalf("matching chat mute did not retain general mute priority: %+v", owner)
	}
	if sent := toMain(h.f); len(sent) != 0 {
		t.Fatalf("matching chat mute still sent a Main handoff: %v", sent)
	}
	if starts := h.f.started(); len(starts) != 0 {
		t.Fatalf("processing agent started for muted handoff: %+v", starts)
	}
}
