package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/prompts"
)

func larkBotHandoffHub(t *testing.T) (*inboxHub, *InboxItem) {
	t.Helper()
	h, _, it := handoffHub(t)
	h.inbox.mu.Lock()
	it.Source, it.Kind, it.Tier = "larkcli", "bot_handoff", TierFYI
	it.Context = "Alert: On-call: Example User\nExample User: @Example Bot check this alert"
	h.inbox.mu.Unlock()
	return h, it
}

func TestLarkBotMentionHandoffReusesMainAndDoesNotNeedUser(t *testing.T) {
	h, it := larkBotHandoffHub(t)
	h.forwardLarkMentions(context.Background())
	h.forwardLarkMentions(context.Background())
	sent := toMain(h.f)
	if len(sent) != 1 || !strings.Contains(sent[0], it.Context) || !strings.Contains(sent[0], "Do not send a Lark message yourself") {
		t.Fatalf("main handoff = %v", sent)
	}
	h.inbox.mu.Lock()
	if it.Tier != TierFYI || len(it.Handoffs) != 1 || it.Suggestion != nil || it.Agent != nil {
		t.Fatalf("handoff created a request or second agent: %+v", it)
	}
	h.inbox.mu.Unlock()
	if !h.st.saved(it.ID) {
		t.Fatal("main handoff was not persisted")
	}
	if writes := h.inbox.escalate(time.Now().Add(-time.Hour), time.Now()); len(writes) != 0 {
		t.Fatal("main-chat handoff was also offered to the processing agent")
	}
}

func TestLarkBotMentionFailedHandoffRetriesTheSameOperation(t *testing.T) {
	h, it := larkBotHandoffHub(t)
	h.f.failSendOnce = map[string]error{"server/m1": errors.New("temporarily unavailable")}
	h.forwardLarkMentions(context.Background())
	if len(it.Handoffs) != 0 {
		t.Fatal("failed delivery was recorded as delivered")
	}
	h.f.failSendOnce = nil
	h.forwardLarkMentions(context.Background())
	if len(it.Handoffs) != 1 {
		t.Fatal("failed handoff did not recover")
	}
	calls := sendOnces(h.f)
	if len(calls) != 2 || calls[0]["client_msg_id"] != calls[1]["client_msg_id"] {
		t.Fatalf("retry changed its idempotency key: %+v", calls)
	}
}

func TestLarkBotHandoffIgnoresOrdinaryAndAlreadyFiledItems(t *testing.T) {
	for _, kind := range []string{"mention", "group", "bot_handoff"} {
		t.Run(kind, func(t *testing.T) {
			h, it := larkBotHandoffHub(t)
			it.Kind = kind
			if kind == "bot_handoff" {
				now := time.Now()
				it.DoneAt = &now
			}
			h.forwardLarkMentions(context.Background())
			if len(toMain(h.f)) != 0 {
				t.Fatal("an ordinary or filed item was handed off automatically")
			}
		})
	}
}

func TestLarkBotHandoffDoesNotBecomeActionableThroughChatPolicy(t *testing.T) {
	h, it := larkBotHandoffHub(t)
	it.Connector = &InboxConnector{Tags: []connector.Tag{{Key: connector.TagChat, Value: "example-chat"}}}
	h.inbox.policies = []InboxPolicy{{Source: "larkcli", Tag: connector.TagChat, Value: "example-chat", Tier: TierAction, Twin: TwinFirst}}
	if h.inbox.applyPolicyTier(it) || it.Tier != TierFYI {
		t.Fatal("an actionable chat policy turned an owner handoff into Needs You")
	}
	if writes := h.inbox.escalate(time.Now().Add(-time.Hour), time.Now()); len(writes) != 0 {
		t.Fatal("a Twin First policy created a second processing agent")
	}
}

func TestLarkBotHandoffPrompt(t *testing.T) {
	golden(t, "inbox_handoff.lark_bot", prompts.RenderLarkBotHandoff("Example User: @Example Bot investigate\nAlert: On-call: Example User"))
}

type blockedLarkHandoffHub struct {
	Hub
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (h *blockedLarkHandoffHub) DeviceCall(ctx context.Context, p DeviceCall) (json.RawMessage, error) {
	if p.Method == DeviceSessionSendOnce {
		h.once.Do(func() {
			close(h.entered)
			select {
			case <-h.release:
			case <-ctx.Done():
			}
		})
	}
	return h.Hub.DeviceCall(ctx, p)
}

func TestLarkBotHandoffConcurrentReplayWaitsForGlobalSnapshot(t *testing.T) {
	h, first := larkBotHandoffHub(t)
	blocked := &blockedLarkHandoffHub{Hub: h.f, entered: make(chan struct{}), release: make(chan struct{})}
	h.Service.hub = blocked
	release := sync.OnceFunc(func() { close(blocked.release) })
	defer release()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.handoffLarkMentions(ctx)
	select {
	case <-blocked.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("the global handoff did not reach its send after taking the snapshot")
	}

	selected := replayWriteFailureThing("thread:selected-handoff", "om_selected_handoff", "bot_handoff", connector.TierFYI)
	unrelated := replayScopeItem("unrelated-handoff", "larkcli", "thread:unrelated-handoff", "om_unrelated", "Example", time.Now())
	unrelated.Kind, unrelated.Tier, unrelated.Context = "bot_handoff", TierFYI, "Unselected pending handoff"
	h.inbox.mu.Lock()
	h.inbox.items[unrelated.ID] = unrelated
	h.inbox.mu.Unlock()
	if err := h.InboxCollected(ctx, assistantwire.InboxRound{Replay: true,
		Sources: []assistantwire.InboxSource{{ID: "larkcli", Things: []connector.InboxThing{selected}}},
	}); err != nil {
		t.Fatal(err)
	}
	cancel() // Recovery must outlive the requesting call, including its wait for the active pass.
	release()
	id := replayWriteFailureItemID(selected)
	waitFor(t, "the selected replay handoff without another poll", func() bool {
		h.inbox.mu.Lock()
		defer h.inbox.mu.Unlock()
		it := h.inbox.items[id]
		return it != nil && len(it.Handoffs) == 1 && len(first.Handoffs) == 1
	})
	if sends := toMain(h.f); len(sends) != 2 {
		t.Fatalf("global and selected handoffs were not each sent once: %v", sends)
	}
	h.inbox.mu.Lock()
	defer h.inbox.mu.Unlock()
	it := h.inbox.items[id]
	if len(unrelated.Handoffs) != 0 || it.Tier != TierFYI || it.Agent != nil || it.Suggestion != nil {
		t.Fatalf("scoped recovery forwarded unrelated work or created Needs You: selected=%+v unrelated=%+v", it, unrelated)
	}
}
