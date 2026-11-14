package assistant

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/proto"
)

func replayWriteFailureThing(thread, last, kind string, tier connector.Tier) connector.InboxThing {
	return connector.InboxThing{
		Thread: thread, Last: last, Kind: kind, Tier: tier,
		Title: "Synthetic inbox item", Preview: "Synthetic message", Context: "A synthetic message for a focused test.",
		At: time.Now().UTC(),
	}
}

func replayWriteFailureRound(things ...connector.InboxThing) assistantwire.InboxRound {
	return assistantwire.InboxRound{
		Epoch: "test-epoch", Seq: 1, Round: roundPoll, Replay: true,
		Sources: []assistantwire.InboxSource{{ID: "larkcli", Name: "Synthetic connector", Things: things}},
	}
}

func replayWriteFailureSeed(t *testing.T, h *inboxHub, thread, last, kind, tier string) *InboxItem {
	t.Helper()
	sum := sha1.Sum([]byte(thread + "\n" + last))
	it := &InboxItem{
		ID: "larkcli:" + hex.EncodeToString(sum[:8]), Source: "larkcli", Kind: kind,
		Title: "Synthetic inbox item", Sender: "Synthetic sender", Preview: "Synthetic message",
		Last: last, Count: 1, Tier: tier, TriagedAt: last, Context: "A synthetic message for a focused test.",
		UpdatedAt: time.Now().Add(-time.Minute), Connector: &InboxConnector{Thread: thread},
	}
	h.inbox.mu.Lock()
	h.inbox.items[it.ID] = it
	h.inbox.mu.Unlock()
	if failed := h.Service.saveInbox(context.Background(), []*InboxItem{it}); len(failed) != 0 {
		t.Fatalf("could not seed synthetic inbox item: %v", failed)
	}
	return it
}

func replayWriteFailureItemID(thing connector.InboxThing) string {
	sum := sha1.Sum([]byte(thing.Thread + "\n" + thing.Last))
	return "larkcli:" + hex.EncodeToString(sum[:8])
}

func replayWriteFailureStore(h *inboxHub, fail map[string]bool) *inboxPutFailStore {
	st := &inboxPutFailStore{
		inboxBatchMemStore: &inboxBatchMemStore{inboxMemStore: h.st, batchFail: errors.New("force per-row fallback")},
		fail:               fail,
	}
	h.Service.store = func() Store { return st }
	return st
}

func replayWriteFailureStoredLast(st *inboxMemStore, id string) (string, bool) {
	st.imu.Lock()
	defer st.imu.Unlock()
	raw, ok := st.rows[id]
	if !ok {
		return "", false
	}
	var item InboxItem
	if err := json.Unmarshal(raw, &item); err != nil {
		return "", false
	}
	return item.Last, true
}

func TestInboxReplayWriteFailureReturnsErrorAndStopsFollowup(t *testing.T) {
	h := newInboxHub(t)
	ctx := context.Background()
	if err := h.inboxReady(ctx); err != nil {
		t.Fatal(err)
	}
	h.setConfig(func(c *assistantwire.InboxConfig) { c.Agent.Enabled = true })
	setSettings(t, h.Service, func(s *Settings) { s.Main = "main" })
	h.f.setSessions("server", proto.Session{SessionID: "main", Assistant: true, Status: proto.StatusIdle})
	replayWriteFailureSeed(t, h, "chat:handoff", "old:handoff", "bot_handoff", TierFYI)
	replayWriteFailureSeed(t, h, "chat:action", "old:action", "mention", TierAction)
	things := []connector.InboxThing{
		replayWriteFailureThing("chat:handoff", "new:handoff", "bot_handoff", connector.TierFYI),
		replayWriteFailureThing("chat:action", "new:action", "mention", connector.TierAction),
	}
	h.st.writeFails(true)

	err := h.InboxCollected(ctx, replayWriteFailureRound(things...))
	if err == nil {
		t.Fatal("replay with failed item writes returned nil")
	}
	h.inbox.mu.Lock()
	handoff := h.inbox.items[replayWriteFailureItemID(replayWriteFailureThing("chat:handoff", "old:handoff", "bot_handoff", connector.TierFYI))]
	action := h.inbox.items[replayWriteFailureItemID(replayWriteFailureThing("chat:action", "old:action", "mention", connector.TierAction))]
	goodRollback := handoff != nil && handoff.Last == "old:handoff" && action != nil && action.Last == "old:action" && action.Agent == nil
	h.inbox.mu.Unlock()
	if !goodRollback {
		t.Fatal("failed replay did not restore the prior open rows")
	}
	if last, ok := replayWriteFailureStoredLast(h.st, handoff.ID); !ok || last != "old:handoff" {
		t.Fatalf("failed replay changed the stored handoff row: last=%q present=%v", last, ok)
	}
	h.f.mu.Lock()
	starts, sends := len(h.f.starts), len(h.f.sentOnce)
	h.f.mu.Unlock()
	if starts != 0 || sends != 0 {
		t.Fatalf("failed replay started processing or handed off: starts=%d sends=%d", starts, sends)
	}
}

func TestInboxReplayPartialWriteFailureKeepsSavedRowsAndReturnsError(t *testing.T) {
	h := newInboxHub(t)
	ctx := context.Background()
	if err := h.inboxReady(ctx); err != nil {
		t.Fatal(err)
	}
	failedOld := replayWriteFailureSeed(t, h, "chat:failed", "old:failed", "group", TierFYI)
	savedOld := replayWriteFailureSeed(t, h, "chat:saved", "old:saved", "group", TierFYI)
	failed := replayWriteFailureThing("chat:failed", "new:failed", "group", connector.TierFYI)
	saved := replayWriteFailureThing("chat:saved", "new:saved", "group", connector.TierFYI)
	replayWriteFailureStore(h, map[string]bool{failedOld.ID: true})

	err := h.InboxCollected(ctx, replayWriteFailureRound(failed, saved))
	if err == nil {
		t.Fatal("partially failed replay returned nil")
	}
	failedID, savedID := failedOld.ID, savedOld.ID
	h.inbox.mu.Lock()
	failedItem, failedInMemory := h.inbox.items[failedID]
	savedItem, savedInMemory := h.inbox.items[savedID]
	failedLastInMemory, savedLastInMemory := "", ""
	if failedItem != nil {
		failedLastInMemory = failedItem.Last
	}
	if savedItem != nil {
		savedLastInMemory = savedItem.Last
	}
	h.inbox.mu.Unlock()
	failedLast, failedStored := replayWriteFailureStoredLast(h.st, failedID)
	savedLast, savedStored := replayWriteFailureStoredLast(h.st, savedID)
	if !failedInMemory || failedLastInMemory != "old:failed" || !savedInMemory || savedLastInMemory != "new:saved" ||
		!failedStored || failedLast != "old:failed" || !savedStored || savedLast != "new:saved" {
		t.Fatalf("partial replay rollback/persistence mismatch: failed(memory=%v,last=%q; stored=%v,last=%q) saved(memory=%v,last=%q; stored=%v,last=%q)",
			failedInMemory, failedLastInMemory, failedStored, failedLast, savedInMemory, savedLastInMemory, savedStored, savedLast)
	}
}

func TestInboxReplayWriteSuccessStillPersists(t *testing.T) {
	h := newInboxHub(t)
	ctx := context.Background()
	if err := h.inboxReady(ctx); err != nil {
		t.Fatal(err)
	}
	thing := replayWriteFailureThing("chat:success", "message:success", "group", connector.TierFYI)

	if err := h.InboxCollected(ctx, replayWriteFailureRound(thing)); err != nil {
		t.Fatalf("successful replay returned error: %v", err)
	}
	id := replayWriteFailureItemID(thing)
	h.inbox.mu.Lock()
	_, exists := h.inbox.items[id]
	h.inbox.mu.Unlock()
	if !exists || !h.st.saved(id) {
		t.Fatalf("successful replay item was not retained: memory=%v store=%v", exists, h.st.saved(id))
	}
}

func TestInboxNormalCollectionWriteFailureKeepsExistingReturnBehavior(t *testing.T) {
	h := newInboxHub(t)
	ctx := context.Background()
	if err := h.inboxReady(ctx); err != nil {
		t.Fatal(err)
	}
	h.st.writeFails(true)
	thing := replayWriteFailureThing("chat:poll", "message:poll", "group", connector.TierFYI)
	round := replayWriteFailureRound(thing)
	round.Replay = false

	if err := h.InboxCollected(ctx, round); err != nil {
		t.Fatalf("ordinary collection changed its return behavior: %v", err)
	}
	h.inbox.mu.Lock()
	remaining := len(h.inbox.items)
	h.inbox.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("failed ordinary collection row was not rolled back: %d items remain", remaining)
	}
}
