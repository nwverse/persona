package extmgr

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/mail"
)

type replayInboxConnector struct {
	state    string
	replays  atomic.Int32
	collects atomic.Int32
	err      error
	got      assistantwire.InboxLarkReplay
}

func (*replayInboxConnector) ID() string   { return "larkcli" }
func (*replayInboxConnector) Name() string { return "Lark" }
func (*replayInboxConnector) Manifest() connector.Manifest {
	return connector.Manifest{Capabilities: []connector.Capability{connector.CapInbox, connector.CapStatus}}
}
func (c *replayInboxConnector) Status(context.Context) connector.Status {
	return connector.Status{State: c.state}
}
func (c *replayInboxConnector) Collect(context.Context) ([]connector.InboxThing, error) {
	c.collects.Add(1)
	return nil, nil
}
func (*replayInboxConnector) Reply(context.Context, connector.InboxReply) error { return nil }
func (c *replayInboxConnector) ReplayMessages(_ context.Context, chat, start, end string, ids []string) ([]connector.InboxThing, error) {
	c.replays.Add(1)
	c.got = assistantwire.InboxLarkReplay{ChatID: chat, Start: start, End: end, MessageIDs: ids}
	if c.err != nil {
		return nil, c.err
	}
	return []connector.InboxThing{{ID: "larkcli:item", Thread: "chat:room", Last: "message-1"}}, nil
}

func TestInboxReplayReadsOnlyLarkAndHandsNormalRound(t *testing.T) {
	h := newTestManager(t)
	h.connectors = connector.NewRegistry(nil)
	lark := &replayInboxConnector{state: connector.StateOK}
	if err := h.addConnector(lark); err != nil {
		t.Fatal(err)
	}
	other := &inboxConnector{}
	if err := h.addConnector(other); err != nil {
		t.Fatal(err)
	}
	var rounds []assistantwire.InboxRound
	h.opts.Assistant = func(_ context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		if method != assistantwire.MethodInboxCollected {
			t.Fatalf("assistant method = %q", method)
		}
		var r assistantwire.InboxRound
		if err := json.Unmarshal(raw, &r); err != nil {
			return nil, err
		}
		rounds = append(rounds, r)
		return json.RawMessage(`{}`), nil
	}
	mailSource := &inboxMail{msgs: []mail.MailSummary{{UID: 1}}}
	h.mail = mailSource
	in := assistantwire.InboxLarkReplay{ChatID: "chat-1", Start: "2026-11-13T10:00:00Z", End: "2026-11-13T10:30:00Z", MessageIDs: []string{"m1"}}
	raw, _ := json.Marshal(assistantwire.InboxCollectRequest{LarkReplay: &in})
	if _, _, err := h.inboxAppCall(context.Background(), assistantwire.AppCall{AppID: assistantwire.AppInbox, Method: assistantwire.InboxCollect, Params: raw}); err != nil {
		t.Fatal(err)
	}
	if len(rounds) != 1 || rounds[0].Round != assistantwire.RoundPoll || !rounds[0].Replay || rounds[0].Names != nil ||
		len(rounds[0].Sources) != 1 || rounds[0].Sources[0].ID != "larkcli" || len(rounds[0].Sources[0].Things) != 1 {
		t.Fatalf("replay round = %+v", rounds)
	}
	if lark.replays.Load() != 1 || lark.collects.Load() != 0 || other.collect.Load() != 0 || mailSource.lists.Load() != 0 {
		t.Fatalf("reads: replay=%d lark collect=%d other=%d mail=%d", lark.replays.Load(), lark.collects.Load(), other.collect.Load(), mailSource.lists.Load())
	}
	if lark.got.ChatID != in.ChatID || lark.got.Start != in.Start || lark.got.End != in.End || len(lark.got.MessageIDs) != 1 || lark.got.MessageIDs[0] != "m1" {
		t.Fatalf("replay selector = %+v", lark.got)
	}
}

func TestInboxReplayRefusesUnsafeStates(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		offline, held, disabled bool
	}{
		{name: "assistant offline", offline: true}, {name: "pending round", held: true}, {name: "connector disabled", disabled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestManager(t)
			h.connectors = connector.NewRegistry(nil)
			lark := &replayInboxConnector{state: connector.StateOK}
			if err := h.addConnector(lark); err != nil {
				t.Fatal(err)
			}
			if tc.offline {
				h.opts.Assistant = nil
			}
			if tc.held {
				h.inbox.held.Store(&assistantwire.InboxRound{Round: assistantwire.RoundPoll})
			}
			if tc.disabled {
				if err := h.connectors.SetFeed("larkcli", connector.FeedInbox, false); err != nil {
					t.Fatal(err)
				}
			}
			err := h.replayLarkInbox(context.Background(), assistantwire.InboxLarkReplay{ChatID: "c", Start: "s", End: "e", MessageIDs: []string{"m"}})
			if err == nil || lark.replays.Load() != 0 {
				t.Fatalf("error=%v replay calls=%d", err, lark.replays.Load())
			}
		})
	}
}

func TestInboxReplayFailureDoesNotFallBackToCollect(t *testing.T) {
	h := newTestManager(t)
	h.connectors = connector.NewRegistry(nil)
	lark := &replayInboxConnector{state: connector.StateOK, err: errors.New("replay failed")}
	if err := h.addConnector(lark); err != nil {
		t.Fatal(err)
	}
	err := h.replayLarkInbox(context.Background(), assistantwire.InboxLarkReplay{ChatID: "c", Start: "s", End: "e", MessageIDs: []string{"m"}})
	if err == nil || lark.replays.Load() != 1 || lark.collects.Load() != 0 {
		t.Fatalf("error=%v replay=%d collect=%d", err, lark.replays.Load(), lark.collects.Load())
	}
}
