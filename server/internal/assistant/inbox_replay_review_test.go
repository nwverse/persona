package assistant

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/connector"
)

func TestInboxReplayRetainsSelectionBesideOlderOrNewerThreadRow(t *testing.T) {
	for _, offset := range []time.Duration{-time.Minute, time.Minute} {
		t.Run(offset.String(), func(t *testing.T) {
			h := newInboxHub(t)
			ctx := context.Background()
			if err := h.inboxReady(ctx); err != nil {
				t.Fatal(err)
			}
			h.setConfig(func(c *assistantwire.InboxConfig) { c.Agent.Enabled = false })
			thread := "larkcli:chat:oc_example_chat"
			at := time.Now().UTC().Add(-2 * time.Minute)
			prior := replayWriteFailureSeed(t, h, thread, "om_existing", "mention", TierAction)
			h.inbox.mu.Lock()
			prior.UpdatedAt, prior.Count = at.Add(offset), 4
			before := itemState(prior)
			h.inbox.mu.Unlock()
			if failed := h.saveInbox(ctx, []*InboxItem{prior}); len(failed) != 0 {
				t.Fatal(failed)
			}
			selected := replayWriteFailureThing(thread, "om_selected", "mention", connector.TierFYI)
			selected.At = at
			for attempt := 0; attempt < 2; attempt++ {
				if err := h.InboxCollected(ctx, assistantwire.InboxRound{Replay: true,
					Sources: []assistantwire.InboxSource{{ID: "larkcli", Things: []connector.InboxThing{selected}}},
				}); err != nil {
					t.Fatal(err)
				}
				h.inbox.mu.Lock()
				got := h.inbox.items[replayWriteFailureItemID(selected)]
				kept := got != nil && got.Last == selected.Last && got.Context == selected.Context &&
					got.Count == 1 && got.Tier == TierFYI && got.Connector.Thread == thread
				unchanged := string(itemState(prior)) == string(before)
				count := len(h.inbox.items)
				h.inbox.mu.Unlock()
				if !kept || !unchanged || count != 2 {
					t.Fatalf("selected activity was discarded or absorbed: kept=%v prior_unchanged=%v rows=%d", kept, unchanged, count)
				}
			}
		})
	}
}

func TestInboxOrdinaryCollectionAdvancesLatestStoredReplay(t *testing.T) {
	h := newInboxHub(t)
	ctx := context.Background()
	if err := h.inboxReady(ctx); err != nil {
		t.Fatal(err)
	}
	h.setConfig(func(c *assistantwire.InboxConfig) { c.Agent.Enabled = false })
	thread := "larkcli:chat:oc_example_chat:thread:omt_example_topic"
	at := time.Now().UTC().Add(-time.Minute)
	first := replayWriteFailureThing(thread, "om_first", "mention", connector.TierFYI)
	second := replayWriteFailureThing(thread, "om_second", "mention", connector.TierAction)
	first.At, second.At = at, at.Add(time.Second)
	first.Tags = []connector.Tag{{Key: connector.TagSender, Value: "ou_example_first"}}
	second.Tags = []connector.Tag{{Key: connector.TagSender, Value: "ou_example_second"}}
	if err := h.InboxCollected(ctx, replayWriteFailureRound(first, second)); err != nil {
		t.Fatal(err)
	}
	ids := []string{replayWriteFailureItemID(first), replayWriteFailureItemID(second)}
	before := map[string]string{}
	// Read the persisted representation back, not a transient per-batch selection.
	h.st.imu.Lock()
	reloaded := map[string]*InboxItem{}
	for _, id := range ids {
		var it InboxItem
		if err := json.Unmarshal(h.st.rows[id], &it); err != nil {
			h.st.imu.Unlock()
			t.Fatal(err)
		}
		reloaded[id] = &it
		before[id] = string(itemState(&it))
	}
	h.st.imu.Unlock()
	h.inbox.mu.Lock()
	h.inbox.items = reloaded
	h.inbox.mu.Unlock()
	third := replayWriteFailureThing(thread, "om_third", "mention", connector.TierAction)
	third.At = at.Add(2 * time.Second)
	third.Tags = []connector.Tag{{Key: connector.TagSender, Value: "ou_example_third"}}
	round := assistantwire.InboxRound{Sources: []assistantwire.InboxSource{{ID: "larkcli",
		Things: []connector.InboxThing{first, second},
	}}}
	if err := h.InboxCollected(ctx, round); err != nil {
		t.Fatal(err)
	}
	h.inbox.mu.Lock()
	for _, id := range ids {
		if string(itemState(h.inbox.items[id])) != before[id] {
			h.inbox.mu.Unlock()
			t.Fatalf("ordinary collection changed a replayed activity on exact redelivery: %s", id)
		}
	}
	h.inbox.mu.Unlock()
	fourth := third
	fourth.Last, fourth.At = "om_fourth", at.Add(3*time.Second)
	stale := second
	stale.Done = connector.DoneReplied
	round.Sources[0].Things = []connector.InboxThing{third, fourth, stale}
	if err := h.InboxCollected(ctx, round); err != nil {
		t.Fatal(err)
	}
	h.inbox.mu.Lock()
	latest := h.inbox.items[ids[1]]
	advanced := len(h.inbox.items) == 2 && latest != nil && latest.Last == fourth.Last && latest.Count == 3 &&
		latest.open() && !latest.Connector.Replay && slices.Equal(latest.Connector.Tags, fourth.Tags)
	olderKept := string(itemState(h.inbox.items[ids[0]])) == before[ids[0]]
	h.inbox.mu.Unlock()
	if !advanced || !olderKept {
		t.Fatalf("ordinary activity did not advance just the latest stored replay: advanced=%v older_kept=%v", advanced, olderKept)
	}
	if last, saved := replayWriteFailureStoredLast(h.st, ids[1]); !saved || last != fourth.Last {
		t.Fatal("ordinary activity did not persist the advanced thread row")
	}
}

func TestInboxReplayDoesNotEscalateOrdinaryFollowup(t *testing.T) {
	h := newInboxHub(t)
	ctx := context.Background()
	if err := h.inboxReady(ctx); err != nil {
		t.Fatal(err)
	}
	h.setConfig(func(c *assistantwire.InboxConfig) { c.Agent.Enabled = true })
	h.inbox.handing.Lock()
	t.Cleanup(h.inbox.handing.Unlock)

	at := time.Now().Add(-4 * 24 * time.Hour)
	selected := replayWriteFailureThing("thread:selected", "om_selected", "mention", connector.TierAction)
	sentinel := replayWriteFailureThing("thread:sentinel", "om_sentinel", "mention", connector.TierAction)
	selected.At, sentinel.At = at, at
	followup := selected
	followup.Last, followup.At = "om_followup", at.Add(time.Second)
	func() {
		h.inbox.triaging.Lock()
		defer h.inbox.triaging.Unlock()
		if err := h.InboxCollected(ctx, replayWriteFailureRound(selected, sentinel)); err != nil {
			t.Fatal(err)
		}
		round := assistantwire.InboxRound{
			Sources: []assistantwire.InboxSource{{ID: "larkcli", Things: []connector.InboxThing{followup}}},
		}
		if err := h.InboxCollected(ctx, round); err != nil {
			t.Fatal(err)
		}
	}()
	waitFor(t, "the still-selected replay item was queued", func() bool {
		h.inbox.mu.Lock()
		defer h.inbox.mu.Unlock()
		it := h.inbox.items[replayWriteFailureItemID(sentinel)]
		return it != nil && it.Agent != nil && it.Agent.State == AgentQueued
	})
	h.inbox.mu.Lock()
	defer h.inbox.mu.Unlock()
	it := h.inbox.items[replayWriteFailureItemID(selected)]
	if it == nil || it.Last != followup.Last || it.Agent != nil {
		t.Fatalf("ordinary followup inherited the earlier replay's escalation: %+v", it)
	}
}

func TestInboxReplayHandledCardRefreshesAssignmentAndMuteIdentity(t *testing.T) {
	mute := InboxMute{ID: "synthetic-sender-mute", Source: "larkcli", Tag: connector.TagSender,
		Value: ownerAssignmentSender, Name: "Example Alert Sender"}
	h := newReplayMuteHandoffHub(t, mute)
	h.setConfig(func(c *assistantwire.InboxConfig) { c.Agent.Enabled = false })
	ctx := context.Background()
	thing := ownerAssignmentThing(ownerAssignmentThread, ownerAssignmentLast, true, time.Now().UTC())
	if err := h.InboxCollected(ctx, ownerAssignmentRound("larkcli", 1, thing)); err != nil {
		t.Fatal(err)
	}
	id := replayWriteFailureItemID(thing)
	if _, err := h.setInboxDone(ctx, id, true, false); err != nil {
		t.Fatal(err)
	}
	h.inbox.mu.Lock()
	it := h.inbox.items[id]
	doneAt, doneBy, tier, count, last := *it.DoneAt, it.DoneBy, it.Tier, it.Count, it.Last
	h.inbox.mu.Unlock()
	thing.OwnerAssigned = false
	thing.Tags = append(thing.Tags, connector.Tag{Key: connector.TagRepo, Value: "example/reassigned"})
	if err := h.InboxCollected(ctx, ownerAssignmentRound("larkcli", 2, thing)); err != nil {
		t.Fatal(err)
	}
	h.inbox.mu.Lock()
	refreshed := !it.Connector.OwnerAssigned && slices.Equal(it.Connector.Tags, thing.Tags)
	handled := it.DoneAt != nil && it.DoneAt.Equal(doneAt) && it.DoneBy == doneBy &&
		it.Tier == tier && it.Count == count && it.Last == last && len(it.Handoffs) == 0
	h.inbox.mu.Unlock()
	if !refreshed || !handled {
		t.Fatalf("handled-card replay left stale source facts or changed completion: refreshed=%v handled=%v", refreshed, handled)
	}
	h.st.imu.Lock()
	var stored InboxItem
	err := json.Unmarshal(h.st.rows[id], &stored)
	h.st.imu.Unlock()
	if err != nil || stored.Connector == nil || stored.Connector.OwnerAssigned ||
		!slices.Equal(stored.Connector.Tags, thing.Tags) || stored.DoneAt == nil || !stored.DoneAt.Equal(doneAt) {
		t.Fatal("source facts and completion were not saved together")
	}
	// Reopening is an explicit user action, not an effect of replay.
	if _, err := h.setInboxDone(ctx, id, false, false); err != nil {
		t.Fatal(err)
	}
	if err := h.InboxCollected(ctx, ownerAssignmentRoundMode("larkcli", 3, false, thing)); err != nil {
		t.Fatal(err)
	}
	h.inbox.mu.Lock()
	defer h.inbox.mu.Unlock()
	if it.DoneAt == nil || it.DoneBy != "read" || it.Connector.OwnerAssigned {
		t.Fatal("the stale assignment exception bypassed the sender mute on a later round")
	}
}

func TestInboxReplayHandledCardFactsRollbackOnSaveFailure(t *testing.T) {
	h := newInboxHub(t)
	ctx := context.Background()
	if err := h.inboxReady(ctx); err != nil {
		t.Fatal(err)
	}
	h.setConfig(func(c *assistantwire.InboxConfig) { c.Agent.Enabled = false })
	thing := ownerAssignmentThing(ownerAssignmentThread, ownerAssignmentLast, true, time.Now().UTC())
	if err := h.InboxCollected(ctx, ownerAssignmentRound("larkcli", 1, thing)); err != nil {
		t.Fatal(err)
	}
	id := replayWriteFailureItemID(thing)
	if _, err := h.setInboxDone(ctx, id, true, false); err != nil {
		t.Fatal(err)
	}
	h.inbox.mu.Lock()
	before := itemState(h.inbox.items[id])
	h.inbox.mu.Unlock()
	store := replayWriteFailureStore(h, map[string]bool{id: true})
	thing.OwnerAssigned, thing.Tags = false, nil
	if err := h.InboxCollected(ctx, ownerAssignmentRound("larkcli", 2, thing)); err == nil {
		t.Fatal("a failed source-fact refresh reported success")
	}
	h.inbox.mu.Lock()
	unchanged := string(itemState(h.inbox.items[id])) == string(before)
	h.inbox.mu.Unlock()
	if !unchanged {
		t.Fatal("a failed source-fact refresh left memory ahead of storage")
	}
	store.fail[id] = false
	if err := h.InboxCollected(ctx, ownerAssignmentRound("larkcli", 3, thing)); err != nil {
		t.Fatal(err)
	}
	h.inbox.mu.Lock()
	defer h.inbox.mu.Unlock()
	it := h.inbox.items[id]
	if it.Connector.OwnerAssigned || len(it.Connector.Tags) != 0 || it.DoneAt == nil || it.DoneBy != "user" {
		t.Fatal("retry failed to refresh only the handled card's source facts")
	}
}
