package assistant

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/connector"
)

func TestInboxReplayRetainsEachSelectedMoveForReadOnlyReceipt(t *testing.T) {
	for _, thread := range []string{"larkcli:chat:oc_example_chat", "larkcli:chat:oc_example_chat:thread:omt_example_topic"} {
		t.Run(thread, func(t *testing.T) {
			h := newInboxHub(t)
			ctx := context.Background()
			if err := h.inboxReady(ctx); err != nil {
				t.Fatal(err)
			}
			h.setConfig(func(c *assistantwire.InboxConfig) { c.Agent.Enabled = false })
			at := time.Now().Add(-time.Minute)
			things := []connector.InboxThing{
				{Thread: thread, Last: "om_first", At: at, Tier: connector.TierFYI, Context: "First selected activity"},
				{Thread: thread, Last: "om_second", At: at.Add(time.Second), Tier: connector.TierAction, Context: "Second selected activity"},
			}
			check := func() map[string]string {
				t.Helper()
				read := func(ids ...string) []InboxItem {
					t.Helper()
					args, err := json.Marshal(inboxReplayToolArgs{ChatID: "oc_example_chat",
						Start: at.Add(-time.Minute).Format(time.RFC3339), End: at.Add(time.Minute).Format(time.RFC3339),
						MessageIDs: ids, ReadOnly: true})
					if err != nil {
						t.Fatal(err)
					}
					raw, err := h.toolInboxReplay(ctx, args)
					if err != nil {
						t.Fatal(err)
					}
					var result struct {
						Items []InboxItem `json:"items"`
					}
					if err := json.Unmarshal([]byte(raw), &result); err != nil {
						t.Fatal(err)
					}
					return result.Items
				}
				if got := read("om_first", "om_second"); len(got) != 2 {
					t.Fatalf("selected moves missing from read_only receipt: %+v", got)
				}
				ids := map[string]string{}
				for _, last := range []string{"om_first", "om_second"} {
					got := read(last)
					if len(got) != 1 {
						t.Fatalf("selected move %s missing from read_only receipt: %+v", last, got)
					}
					it := got[0]
					ids[last] = it.ID
					wantTier := TierFYI
					if last == "om_second" {
						wantTier = TierAction
					}
					if it.Connector.Thread != thread || it.Tier != wantTier || it.Count != 1 {
						t.Fatalf("selected move inherited another activity: %+v", it)
					}
					if stored, ok := replayWriteFailureStoredLast(h.st, it.ID); !ok || stored != last {
						t.Fatalf("selected move was not retained: id=%s last=%s stored=%s exists=%v", it.ID, last, stored, ok)
					}
				}
				if ids["om_first"] == "" || ids["om_second"] == "" || ids["om_first"] == ids["om_second"] {
					t.Fatalf("selected IDs are not represented independently: %v", ids)
				}
				return ids
			}
			if err := h.InboxCollected(ctx, replayWriteFailureRound(things...)); err != nil {
				t.Fatal(err)
			}
			ids := check()
			// Exact-ID recovery remains stable even if the caller reverses its selection.
			if err := h.InboxCollected(ctx, assistantwire.InboxRound{Replay: true,
				Sources: []assistantwire.InboxSource{{ID: "larkcli", Things: []connector.InboxThing{things[1], things[0]}}},
			}); err != nil {
				t.Fatal(err)
			}
			again := check()
			for last, id := range ids {
				if again[last] != id {
					t.Fatalf("recovery replaced a retained item: before=%v after=%v", ids, again)
				}
			}
			h.inbox.mu.Lock()
			handled := h.inbox.items[ids["om_first"]]
			doneAt := time.Now()
			handled.done(doneAt, "user")
			h.inbox.mu.Unlock()
			if failed := h.saveInbox(ctx, []*InboxItem{handled}); len(failed) != 0 {
				t.Fatalf("could not save synthetic handled item: %v", failed)
			}
			if err := h.InboxCollected(ctx, assistantwire.InboxRound{Replay: true,
				Sources: []assistantwire.InboxSource{{ID: "larkcli", Things: things}},
			}); err != nil {
				t.Fatal(err)
			}
			check()
			h.inbox.mu.Lock()
			defer h.inbox.mu.Unlock()
			if handled.DoneAt == nil || !handled.DoneAt.Equal(doneAt) || len(h.inbox.items) != 2 {
				t.Fatal("recovery reopened or duplicated a handled selected move")
			}
		})
	}
}
