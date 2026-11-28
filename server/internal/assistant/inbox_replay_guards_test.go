package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/proto"
)

func TestInboxReplayRetainsEqualTimestampWithoutReplacingUnselectedMove(t *testing.T) {
	for _, precision := range []time.Duration{time.Second, time.Minute} {
		t.Run(precision.String(), func(t *testing.T) {
			h := newInboxHub(t)
			ctx := context.Background()
			if err := h.inboxReady(ctx); err != nil {
				t.Fatal(err)
			}
			h.setConfig(func(c *assistantwire.InboxConfig) { c.Agent.Enabled = false })
			at := time.Now().UTC().Add(-time.Minute).Truncate(precision)
			thread := "larkcli:chat:oc_example_chat"
			prior := replayScopeItem("unselected", "larkcli", thread, "om_unselected", "Original source", at)
			prior.Context, prior.Reason, prior.Count = "Preserved unselected request", "Original decision", 4
			prior.TriagedAt = prior.Last
			h.inbox.mu.Lock()
			h.inbox.items[prior.ID] = prior
			h.inbox.mu.Unlock()
			if failed := h.saveInbox(ctx, []*InboxItem{prior}); len(failed) != 0 {
				t.Fatalf("could not save original item: %v", failed)
			}
			before, err := json.Marshal(prior)
			if err != nil {
				t.Fatal(err)
			}
			thing := replayWriteFailureThing(thread, "om_recovered", "mention", connector.TierAction)
			thing.At, thing.Context = at, "Selected activity at the same source timestamp"
			round := assistantwire.InboxRound{Replay: true,
				Sources: []assistantwire.InboxSource{{ID: "larkcli", Name: "Recovered source", Things: []connector.InboxThing{thing}}},
			}
			id := replayWriteFailureItemID(thing)
			for attempt := 0; attempt < 2; attempt++ {
				if err := h.InboxCollected(ctx, round); err != nil {
					t.Fatal(err)
				}
				h.inbox.mu.Lock()
				it := h.inbox.items[id]
				after, err := json.Marshal(prior)
				kept := it != nil && it.Last == thing.Last && it.Context == thing.Context && it.Count == 1 && it.UpdatedAt.Equal(at)
				unchanged := err == nil && string(after) == string(before) && h.inbox.items[prior.ID] == prior
				count := len(h.inbox.items)
				h.inbox.mu.Unlock()
				if !kept || !unchanged || count != 2 {
					t.Fatalf("equal-time recovery lost the selected move or changed an unselected one: kept=%v unchanged=%v count=%d", kept, unchanged, count)
				}
				args, err := json.Marshal(inboxReplayToolArgs{ChatID: "oc_example_chat",
					Start: at.Add(-time.Minute).Format(time.RFC3339), End: at.Add(time.Minute).Format(time.RFC3339),
					MessageIDs: []string{thing.Last}, ReadOnly: true})
				if err != nil {
					t.Fatal(err)
				}
				raw, err := h.toolInboxReplay(ctx, args)
				if err != nil {
					t.Fatal(err)
				}
				var receipt struct {
					Items []InboxItem `json:"items"`
				}
				if err := json.Unmarshal([]byte(raw), &receipt); err != nil || len(receipt.Items) != 1 || receipt.Items[0].ID != id {
					t.Fatalf("equal-time recovery missing from retained receipt: %s (%v)", raw, err)
				}
				if last, ok := replayWriteFailureStoredLast(h.st, id); !ok || last != thing.Last {
					t.Fatalf("selected move was not saved: last=%q exists=%v", last, ok)
				}
			}
		})
	}
}

func TestInboxReplayClosedSelectionNeverHandsOffOrStartsAgent(t *testing.T) {
	for _, tc := range []struct{ kind, doneBy string }{
		{"mention", "user"}, {"mention", "read"}, {"bot_handoff", "user"},
	} {
		t.Run(tc.kind+"/"+tc.doneBy, func(t *testing.T) {
			h := newInboxHub(t)
			ctx := context.Background()
			if err := h.inboxReady(ctx); err != nil {
				t.Fatal(err)
			}
			h.setConfig(func(c *assistantwire.InboxConfig) { c.Agent.Enabled, c.Agent.Concurrency = true, 2 })
			setSettings(t, h.Service, func(s *Settings) { s.Main = "main" })
			h.f.setSessions("server", proto.Session{SessionID: "main", Assistant: true, Status: proto.StatusIdle})
			h.f.failStart = errors.New("synthetic agent start stopped")
			at := time.Now().UTC().Add(-4 * 24 * time.Hour)
			closed := replayWriteFailureThing("larkcli:chat:oc_example_chat", "om_closed", tc.kind, connector.TierAction)
			closed.At = at
			if tc.kind == "bot_handoff" {
				closed.Tier = connector.TierFYI
			}
			item := replayWriteFailureSeed(t, h, closed.Thread, closed.Last, tc.kind, string(closed.Tier))
			doneAt := time.Now().Add(-time.Hour)
			h.inbox.mu.Lock()
			item.UpdatedAt, item.DoneAt, item.DoneBy = at, &doneAt, tc.doneBy
			item.Suggestion = &InboxSuggestion{}
			item.Agent = &InboxAgent{State: AgentQueued, Of: "om_previous"}
			h.inbox.mu.Unlock()
			if failed := h.saveInbox(ctx, []*InboxItem{item}); len(failed) != 0 {
				t.Fatalf("could not save closed item: %v", failed)
			}
			before, err := json.Marshal(item)
			if err != nil {
				t.Fatal(err)
			}
			ready := replayWriteFailureThing("thread:ready", "om_ready", "mention", connector.TierAction)
			round := replayWriteFailureRound(closed, ready)
			if err := h.InboxCollected(ctx, round); err != nil {
				t.Fatal(err)
			}
			readyID := replayWriteFailureItemID(ready)
			waitFor(t, "open selected item finished the recovered start pass", func() bool {
				h.inbox.mu.Lock()
				defer h.inbox.mu.Unlock()
				it := h.inbox.items[readyID]
				return it != nil && it.Agent != nil && it.Agent.Attempts > 0
			})
			h.inbox.handing.Lock()
			h.inbox.handing.Unlock()
			h.forwardLarkMentions(ctx, item.ID, readyID)
			h.inbox.mu.Lock()
			after, err := json.Marshal(item)
			h.inbox.mu.Unlock()
			if err != nil || string(after) != string(before) || len(toMain(h.f)) != 0 {
				t.Fatalf("closed selected activity was changed or handed off: changed=%v sends=%v err=%v", string(after) != string(before), toMain(h.f), err)
			}
			h.st.imu.Lock()
			stored := append([]byte(nil), h.st.rows[item.ID]...)
			h.st.imu.Unlock()
			var retained InboxItem
			if err := json.Unmarshal(stored, &retained); err != nil || retained.DoneAt == nil ||
				!retained.DoneAt.Equal(doneAt) || retained.DoneBy != tc.doneBy || retained.Agent == nil || retained.Agent.Attempts != 0 {
				t.Fatalf("closed selected activity did not retain its saved completion: %s (%v)", stored, err)
			}
		})
	}
}

func TestInboxReplayEqualTimestampStillRespectsCompletedThread(t *testing.T) {
	for _, offset := range []time.Duration{0, time.Second} {
		t.Run(offset.String(), func(t *testing.T) {
			h := newInboxHub(t)
			ctx := context.Background()
			if err := h.inboxReady(ctx); err != nil {
				t.Fatal(err)
			}
			at := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
			thread := "larkcli:chat:oc_example_chat"
			open := replayScopeItem("unselected-open", "larkcli", thread, "om_open", "Original", at)
			done := replayScopeItem("completed", "larkcli", thread, "om_completed", "Original", at.Add(offset))
			doneAt := time.Now().Add(-time.Second)
			done.DoneAt, done.DoneBy = &doneAt, "user"
			h.inbox.mu.Lock()
			h.inbox.items[open.ID], h.inbox.items[done.ID] = open, done
			h.inbox.mu.Unlock()
			thing := replayWriteFailureThing(thread, "om_recovered", "mention", connector.TierAction)
			thing.At = at
			if err := h.InboxCollected(ctx, replayWriteFailureRound(thing)); err != nil {
				t.Fatal(err)
			}
			h.inbox.mu.Lock()
			defer h.inbox.mu.Unlock()
			if len(h.inbox.items) != 2 || open.Last != "om_open" || done.Last != "om_completed" ||
				done.DoneAt == nil || !done.DoneAt.Equal(doneAt) || h.inbox.items[replayWriteFailureItemID(thing)] != nil {
				t.Fatal("equal-time recovery bypassed completion evidence or changed an unselected item")
			}
		})
	}
}

func TestInboxReplayOpenHandledSelectionCanContinue(t *testing.T) {
	h := newInboxHub(t)
	ctx := context.Background()
	if err := h.inboxReady(ctx); err != nil {
		t.Fatal(err)
	}
	h.setConfig(func(c *assistantwire.InboxConfig) { c.Agent.Enabled = true })
	thing := replayWriteFailureThing("thread:handled-open", "om_handled_open", "group", connector.TierFYI)
	item := replayWriteFailureSeed(t, h, thing.Thread, thing.Last, thing.Kind, string(thing.Tier))
	h.inbox.mu.Lock()
	item.Suggestion = &InboxSuggestion{}
	h.inbox.mu.Unlock()
	h.inbox.handing.Lock()
	t.Cleanup(func() { h.inbox.handing.Unlock() })
	if err := h.InboxCollected(ctx, replayWriteFailureRound(thing)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "open handled selection reached continuation", func() bool {
		h.inbox.mu.Lock()
		defer h.inbox.mu.Unlock()
		return item.Agent != nil && item.Agent.State == AgentQueued && item.DoneAt == nil
	})
}
