package assistant

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/connector"
)

func TestInboxReplayScopesMaintenanceToSelectedItem(t *testing.T) {
	h := newInboxHub(t)
	ctx := context.Background()
	if err := h.inboxReady(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	selected := replayScopeItem("selected", "example", "thread:selected", "last:selected", "Old connector name", now)
	muted := replayScopeItem("muted", "example", "thread:muted", "last:muted", "Old connector name", now)
	muted.Connector.Tags = []connector.Tag{{Key: connector.TagRepo, Value: "org/private", Name: "org/private"}}
	orphan := replayScopeItem("merged", "example", "thread:merged", "last:merged", "Old connector name", now)
	orphan.MergedInto = "forgotten-survivor"
	h.inbox.mu.Lock()
	h.inbox.items[selected.ID], h.inbox.items[muted.ID] = selected, muted
	h.inbox.items[orphan.ID] = orphan
	h.inbox.readErrs = map[string]string{"unrelated": assistantwire.ReadTimeout}
	h.inbox.mailReadErrs = map[string]assistantwire.InboxMailFailed{"acc-work": {ID: "acc-work", Email: "work@example.com", ReadError: assistantwire.ReadFailed}}
	h.inbox.held = map[string]inboxHeld{"extension": {Name: "Extension", Waiting: 3, State: triagePaused, Reason: "busy"}}
	h.inbox.mutes = []InboxMute{{ID: "mute-1", Source: "example", Tag: connector.TagRepo, Value: "org/private", Name: "org/private"}}
	h.inbox.mu.Unlock()

	r := assistantwire.InboxRound{Round: roundPoll, Replay: true, Names: map[string]string{"example": "Renamed connector"}, Sources: []assistantwire.InboxSource{
		{ID: "example", Name: "Renamed connector", Things: []connector.InboxThing{{Thread: "thread:selected", Last: "last:selected", At: now}}},
		{ID: "unrelated", Name: "Unrelated", ReadError: assistantwire.ReadFailed},
	}}
	if err := h.InboxCollected(ctx, r); err != nil {
		t.Fatal(err)
	}
	h.inbox.mu.Lock()
	defer h.inbox.mu.Unlock()
	if h.inbox.readErrs["unrelated"] != assistantwire.ReadTimeout || len(h.inbox.readErrs) != 1 {
		t.Fatalf("connector read errors changed: %v", h.inbox.readErrs)
	}
	wantMailErrs := map[string]assistantwire.InboxMailFailed{"acc-work": {ID: "acc-work", Email: "work@example.com", ReadError: assistantwire.ReadFailed}}
	if !reflect.DeepEqual(h.inbox.mailReadErrs, wantMailErrs) {
		t.Fatalf("mail read errors changed: %v", h.inbox.mailReadErrs)
	}
	wantHeld := map[string]inboxHeld{"extension": {Name: "Extension", Waiting: 3, State: triagePaused, Reason: "busy"}}
	if !reflect.DeepEqual(h.inbox.held, wantHeld) {
		t.Fatalf("held triage state changed: %v", h.inbox.held)
	}
	if !muted.open() || muted.AccountName != "Old connector name" {
		t.Fatalf("unrelated muted item changed: %+v", muted)
	}
	if orphan.MergedInto != "forgotten-survivor" {
		t.Fatalf("replay released an unrelated merged item: %+v", orphan)
	}
}

func TestInboxZeroTargetReplayPreservesInboxState(t *testing.T) {
	h := newInboxHub(t)
	ctx := context.Background()
	if err := h.inboxReady(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	item := replayScopeItem("unmatched", "example", "thread:existing", "last:existing", "Existing name", now)
	h.inbox.mu.Lock()
	h.inbox.items[item.ID] = item
	h.inbox.readErrs = map[string]string{"unrelated": assistantwire.ReadTimeout}
	h.inbox.mailReadErrs = map[string]assistantwire.InboxMailFailed{"acc-work": {ID: "acc-work", Email: "work@example.com", ReadError: assistantwire.ReadFailed}}
	h.inbox.held = map[string]inboxHeld{"extension": {Name: "Extension", Waiting: 2, State: triagePaused, Reason: "held"}}
	h.inbox.mutes = []InboxMute{{ID: "mute-1", Source: "example", Tag: connector.TagRepo, Value: "org/private", Name: "org/private"}}
	h.inbox.mu.Unlock()

	r := assistantwire.InboxRound{Round: roundPoll, Replay: true, Names: map[string]string{"example": "New name"}, Sources: []assistantwire.InboxSource{{
		ID: "example", Name: "New name", Things: []connector.InboxThing{},
	}}}
	if err := h.InboxCollected(ctx, r); err != nil {
		t.Fatal(err)
	}
	h.inbox.mu.Lock()
	defer h.inbox.mu.Unlock()
	if len(h.inbox.items) != 1 || h.inbox.items[item.ID] != item || item.AccountName != "Existing name" || !item.open() {
		t.Fatalf("zero-target replay changed item: %+v", item)
	}
	if h.inbox.readErrs["unrelated"] != assistantwire.ReadTimeout || len(h.inbox.readErrs) != 1 ||
		len(h.inbox.mailReadErrs) != 1 || h.inbox.mailReadErrs["acc-work"].ReadError != assistantwire.ReadFailed ||
		h.inbox.held["extension"].Waiting != 2 || len(h.inbox.mutes) != 1 {
		t.Fatalf("zero-target replay changed state: connector errors=%v mail errors=%v held=%v mutes=%v",
			h.inbox.readErrs, h.inbox.mailReadErrs, h.inbox.held, h.inbox.mutes)
	}
}

func TestInboxExplicitGlobalMuteIgnoresConcurrentReplayScope(t *testing.T) {
	h := newInboxHub(t)
	if err := h.inboxReady(context.Background()); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	one := replayScopeItem("global-mute-one", "example", "thread:one", "last:one", "Example", now)
	two := replayScopeItem("global-mute-two", "example", "thread:two", "last:two", "Example", now)
	h.inbox.mu.Lock()
	h.inbox.items[one.ID], h.inbox.items[two.ID] = one, two
	h.inbox.mutes = []InboxMute{{ID: "mute-1", Source: "example", Tag: connector.TagRepo, Value: "org/private", Name: "org/private"}}
	h.inbox.batch = &inboxBatch{only: map[string]bool{one.ID: true}}
	writes := h.inbox.applyMutes(now, nil)
	h.inbox.mu.Unlock()
	if len(writes) != 2 || one.open() || two.open() {
		t.Fatalf("explicit global mute only filed the replay-selected item: writes=%d first=%v second=%v", len(writes), one.open(), two.open())
	}
}

func TestInboxReplayOnlyHandsSelectedLarkBotMentionToMain(t *testing.T) {
	h, _, _ := handoffHub(t)
	now := time.Now()
	selected := replayScopeItem("selected-handoff", "larkcli", "thread:selected", "last:selected", "Lark", now)
	selected.Kind, selected.Tier, selected.Context = "bot_handoff", TierFYI, "selected bot mention"
	unrelated := replayScopeItem("unrelated-handoff", "larkcli", "thread:unrelated", "last:unrelated", "Lark", now)
	unrelated.Kind, unrelated.Tier, unrelated.Context = "bot_handoff", TierFYI, "unrelated pending bot mention"
	h.inbox.mu.Lock()
	h.inbox.items[selected.ID], h.inbox.items[unrelated.ID] = selected, unrelated
	h.inbox.mu.Unlock()

	err := h.InboxCollected(context.Background(), assistantwire.InboxRound{Round: roundPoll, Replay: true,
		Sources: []assistantwire.InboxSource{{ID: "larkcli", Name: "Lark", Things: []connector.InboxThing{{
			Thread: "thread:selected", Last: "last:selected", At: now,
		}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for len(toMain(h.f)) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if sent := toMain(h.f); len(sent) != 1 || sent[0] == "" || sent[0] == unrelated.Context {
		t.Fatalf("main sends = %v", sent)
	}
	h.inbox.mu.Lock()
	defer h.inbox.mu.Unlock()
	if len(selected.Handoffs) != 1 || len(unrelated.Handoffs) != 0 {
		t.Fatalf("selected handoffs=%+v unrelated handoffs=%+v", selected.Handoffs, unrelated.Handoffs)
	}
}

func TestInboxReplayDoesNotRetierOrRewindNewerConnectorItem(t *testing.T) {
	h := newInboxHub(t)
	ctx := context.Background()
	if err := h.inboxReady(ctx); err != nil {
		t.Fatal(err)
	}
	newestAt := time.Now()
	item := replayScopeItem("newer", "example", "thread:build", "last:newer", "Example", newestAt)
	item.Tier, item.Reason, item.Count = TierAction, "newest decision", 4
	h.inbox.mu.Lock()
	h.inbox.items[item.ID] = item
	h.inbox.mu.Unlock()

	for _, tc := range []struct {
		name string
		at   time.Time
	}{
		{name: "older", at: newestAt.Add(-time.Minute)},
		{name: "equal timestamp", at: newestAt},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := h.InboxCollected(ctx, assistantwire.InboxRound{Round: roundPoll, Replay: true, Sources: []assistantwire.InboxSource{{
				ID: "example", Name: "Changed name", Things: []connector.InboxThing{{Thread: "thread:build", Last: "last:old-replay", At: tc.at, Tier: connector.TierFYI, Why: "stale tier"}},
			}}})
			if err != nil {
				t.Fatal(err)
			}
			h.inbox.mu.Lock()
			defer h.inbox.mu.Unlock()
			if item.Last != "last:newer" || !item.UpdatedAt.Equal(newestAt) || item.Tier != TierAction || item.Reason != "newest decision" || item.Count != 4 || item.AccountName != "Example" {
				t.Fatalf("stale replay changed the newer item: %+v", item)
			}
		})
	}
}

func TestRecoveredReplayQueuesOnlySelectedOldActionWithOriginalDate(t *testing.T) {
	h := newInboxHub(t)
	ctx := context.Background()
	if err := h.inboxReady(ctx); err != nil {
		t.Fatal(err)
	}
	h.setConfig(func(c *assistantwire.InboxConfig) { c.Agent.Enabled = true })
	now := time.Now()
	started := now.Add(-4*24*time.Hour - 15*time.Minute)
	updated := now.Add(-4 * 24 * time.Hour)
	selected := replayScopeItem("recovered-old", "example", "thread:old", "last:old", "Example", updated)
	selected.StartedAt, selected.UpdatedAt, selected.Context, selected.TriagedAt = started, updated, "old selected action context", selected.Last
	recent := replayScopeItem("unrelated-recent", "example", "thread:recent", "last:recent", "Example", now.Add(-time.Minute))
	recent.Context, recent.TriagedAt = "unrelated recent action context", recent.Last
	h.inbox.mu.Lock()
	h.inbox.items[selected.ID], h.inbox.items[recent.ID] = selected, recent
	h.inbox.mu.Unlock()

	h.inbox.handing.Lock()
	t.Cleanup(func() { h.inbox.handing.Unlock() })
	h.Service.handleRecoveredInbox(ctx, []string{selected.ID})
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		h.inbox.mu.Lock()
		queued := selected.Agent != nil && selected.Agent.State == AgentQueued && selected.Agent.Of == selected.Last
		h.inbox.mu.Unlock()
		if queued {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.inbox.mu.Lock()
	defer h.inbox.mu.Unlock()
	if selected.Agent == nil || selected.Agent.State != AgentQueued || selected.Agent.Of != selected.Last {
		t.Fatalf("old selected action was not queued: %+v", selected.Agent)
	}
	if recent.Agent != nil {
		t.Fatalf("unrelated recent action was queued: %+v", recent.Agent)
	}
	if !selected.StartedAt.Equal(started) || !selected.UpdatedAt.Equal(updated) {
		t.Fatalf("recovery changed the selected item's dates: started=%s updated=%s", selected.StartedAt, selected.UpdatedAt)
	}
}

func TestRecoveredReplayDoesNothingWhenTwinIsDisabled(t *testing.T) {
	h := newInboxHub(t)
	ctx := context.Background()
	if err := h.inboxReady(ctx); err != nil {
		t.Fatal(err)
	}
	h.setConfig(func(c *assistantwire.InboxConfig) { c.Agent.Enabled = false })
	updated := time.Now().Add(-4 * 24 * time.Hour)
	selected := replayScopeItem("disabled-old", "example", "thread:old", "last:old", "Example", updated)
	selected.Context, selected.TriagedAt = "old selected action context", selected.Last
	h.inbox.mu.Lock()
	h.inbox.items[selected.ID] = selected
	h.inbox.mu.Unlock()

	h.Service.handleRecoveredInbox(ctx, []string{selected.ID})
	h.inbox.mu.Lock()
	defer h.inbox.mu.Unlock()
	if selected.Agent != nil || selected.Handling != nil || !selected.UpdatedAt.Equal(updated) {
		t.Fatalf("disabled twin changed recovered item: agent=%+v handling=%+v updated=%s", selected.Agent, selected.Handling, selected.UpdatedAt)
	}
}

func replayScopeItem(id, source, thread, last, name string, at time.Time) *InboxItem {
	return &InboxItem{ID: id, Source: source, Account: source, AccountName: name, Kind: "build", Last: last,
		StartedAt: at, UpdatedAt: at, Count: 1, Tier: TierAction,
		Connector: &InboxConnector{Thread: thread, Tags: []connector.Tag{{Key: connector.TagRepo, Value: "org/private", Name: "org/private"}}}}
}
