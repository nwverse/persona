package assistant

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
)

func TestRecoveredReplayTriagesOnlySelectedOldDMBeforeQueuing(t *testing.T) {
	for _, tc := range []struct {
		name, tier string
		enabled    bool
		queue      bool
	}{
		{"twin enabled", TierAction, true, true},
		{"twin disabled", TierAction, false, false},
		{"model decides FYI", TierFYI, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model, client := newTriageModel(t, "ok", `{"tier":"`+tc.tier+`","summary":"Review the request","reason":"Synthetic triage verdict"}`)
			h, _ := admitHub(t, client)
			h.setConfig(func(c *assistantwire.InboxConfig) { c.Agent.Enabled = tc.enabled })
			ctx := context.Background()
			if err := h.inboxReady(ctx); err != nil {
				t.Fatal(err)
			}
			at := time.Now().Add(-4 * 24 * time.Hour)
			thing := replayWriteFailureThing("thread:selected-old-dm", "last:selected-old-dm", KindDM, "")
			thing.At, thing.Context = at, "Synthetic selected old direct request"
			unrelated := replayScopeItem("unrelated", "larkcli", "thread:unrelated", "last:unrelated", "Example", time.Now())
			unrelated.Kind, unrelated.Context, unrelated.TriagedAt = KindDM, "Synthetic unrelated request", ""
			h.inbox.mu.Lock()
			h.inbox.items[unrelated.ID] = unrelated
			h.inbox.mu.Unlock()
			h.inbox.handing.Lock()
			t.Cleanup(func() { h.inbox.handing.Unlock() })

			if err := h.InboxCollected(ctx, replayWriteFailureRound(thing)); err != nil {
				t.Fatal(err)
			}
			id := replayWriteFailureItemID(thing)
			waitFor(t, "selected replay triage and admission", func() bool {
				h.inbox.mu.Lock()
				defer h.inbox.mu.Unlock()
				it := h.inbox.items[id]
				return it != nil && it.TriagedAt == it.Last &&
					(!tc.queue || it.Agent != nil && it.Agent.State == AgentQueued)
			})
			h.inbox.mu.Lock()
			defer h.inbox.mu.Unlock()
			selected := h.inbox.items[id]
			if selected.Tier != tc.tier || selected.Summary != "Review the request" || !selected.UpdatedAt.Equal(at) {
				t.Fatalf("selected triage lost its verdict or source date: %+v", selected)
			}
			if !tc.queue && selected.Agent != nil {
				t.Fatalf("recovery ignored twin enablement or the model's tier: %+v", selected.Agent)
			}
			if unrelated.TriagedAt != "" || unrelated.Agent != nil {
				t.Fatalf("recovery processed an unrelated request: %+v", unrelated)
			}
			calls := model.asked()
			if len(calls) != 1 || !strings.Contains(calls[0].User, thing.Context) {
				t.Fatalf("triage did not stay scoped to the selected request: %+v", calls)
			}
		})
	}
}
