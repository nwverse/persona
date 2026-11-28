package extmgr

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/connector"
)

func TestInboxReplayHandoffDetachedAndBounded(t *testing.T) {
	h := newTestManager(t)
	h.connectors = connector.NewRegistry(nil)
	if err := h.addConnector(&replayInboxConnector{state: connector.StateOK}); err != nil {
		t.Fatal(err)
	}
	h.inbox.owed.Store(true)
	h.inbox.told.Store(true)
	key := struct{ name string }{"trace"}
	ctx, disconnect := context.WithCancel(context.WithValue(context.Background(), key, "example-trace"))
	defer disconnect()
	started := time.Now()
	var handoff context.Context
	h.opts.Assistant = func(ctx context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
		handoff = ctx
		deadline, ok := ctx.Deadline()
		if !ok || deadline.Before(started.Add(inboxRoundWait)) || deadline.After(time.Now().Add(inboxRoundWait)) {
			t.Errorf("handoff deadline=%v present=%v, want the normal round bound", deadline, ok)
		}
		if ctx.Value(key) != "example-trace" {
			t.Error("handoff lost request context values")
		}
		disconnect()
		if ctx.Err() != nil {
			t.Errorf("client disconnect canceled the admitted handoff: %v", ctx.Err())
		}
		return json.RawMessage(`{}`), nil
	}
	if err := h.replayLarkInbox(ctx, assistantwire.InboxLarkReplay{ChatID: "c", Start: "s", End: "e", MessageIDs: []string{"m"}}); err != nil {
		t.Fatal(err)
	}
	if handoff == nil || !errors.Is(handoff.Err(), context.Canceled) {
		t.Fatal("handoff context was not canceled on return")
	}
	if h.inbox.held.Load() != nil || !h.inbox.owed.Load() || h.inbox.told.Load() {
		t.Fatal("replay changed full-round debt or retained an offline notification")
	}
	wait, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := h.inbox.lock(wait); err != nil {
		t.Fatalf("successful handoff retained the collection lock: %v", err)
	}
	h.inbox.unlock()
}

func TestInboxReplayHandoffFailureReleasesCollectionLock(t *testing.T) {
	for _, failure := range []struct {
		name string
		err  error
	}{
		{"RPC timeout", context.DeadlineExceeded},
		{"save failure", errors.New("example save failure")},
	} {
		t.Run(failure.name, func(t *testing.T) {
			h := newTestManager(t)
			h.connectors = connector.NewRegistry(nil)
			lark := &replayInboxConnector{state: connector.StateOK}
			if err := h.addConnector(lark); err != nil {
				t.Fatal(err)
			}
			h.inbox.owed.Store(true)
			h.inbox.told.Store(true)
			var handoff context.Context
			h.opts.Assistant = func(ctx context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
				handoff = ctx
				if _, ok := ctx.Deadline(); !ok {
					t.Error("handoff has no deadline")
				}
				return nil, failure.err
			}
			err := h.replayLarkInbox(context.Background(), assistantwire.InboxLarkReplay{
				ChatID: "c", Start: "s", End: "e", MessageIDs: []string{"m"},
			})
			if !errors.Is(err, failure.err) || handoff == nil || !errors.Is(handoff.Err(), context.Canceled) {
				t.Fatalf("failed handoff was not returned and canceled: %v", err)
			}
			if h.inbox.held.Load() != nil || !h.inbox.owed.Load() || !h.inbox.told.Load() ||
				lark.replays.Load() != 1 || lark.collects.Load() != 0 {
				t.Fatal("failed recovery was retained for automatic replay or changed normal collection state")
			}
			h.opts.Assistant = func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
				return json.RawMessage(`{}`), nil
			}
			// A failed exact recovery must not block the next ordinary refresh,
			// and only that full refresh can discharge the outstanding full round.
			wait, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := h.collectInbox(wait, roundFull); err != nil {
				t.Fatalf("normal refresh remained blocked after recovery failed: %v", err)
			}
			if h.inbox.owed.Load() || h.inbox.held.Load() != nil || h.inbox.told.Load() || lark.collects.Load() != 1 {
				t.Fatal("the following full refresh did not restore normal collection")
			}
		})
	}
}
