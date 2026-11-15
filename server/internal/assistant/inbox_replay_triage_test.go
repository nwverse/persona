package assistant

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/ai"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/connector"
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

func TestRecoveredReplayIncompleteTriageBlocksPreexistingAction(t *testing.T) {
	ai.SetBusyPauseForTest(t, 50*time.Millisecond)
	for _, tc := range []struct {
		name, mode, reply, source string
	}{
		{"Lark busy", "busy", `{}`, "larkcli"},
		{"Lark quota", "quota", `{}`, "larkcli"},
		{"unruled model error", "fail", `{}`, "example"},
		{"unruled empty verdict", "ok", `{}`, "example"},
	} {
		for _, twin := range []string{TwinFirst, TwinNever} {
			t.Run(tc.name+"/"+twin, func(t *testing.T) {
				model, client := newTriageModel(t, tc.mode, tc.reply)
				h, _ := admitHub(t, client)
				h.setConfig(func(c *assistantwire.InboxConfig) { c.Agent.Enabled = true })
				ctx := context.Background()
				if err := h.inboxReady(ctx); err != nil {
					t.Fatal(err)
				}
				at := time.Now().Add(-4 * 24 * time.Hour)
				target := replayScopeItem("untriaged-action", tc.source, "thread:untriaged", "om_untriaged", "Example", at)
				target.Kind, target.Context = KindDM, "Synthetic request awaiting a normal triage verdict"
				sentinel := replayScopeItem("triaged-sentinel", "example", "thread:sentinel", "om_sentinel", "Example", at)
				sentinel.Context, sentinel.TriagedAt = "Already triaged selected request", sentinel.Last
				h.inbox.mu.Lock()
				h.inbox.items[target.ID], h.inbox.items[sentinel.ID] = target, sentinel
				h.inbox.policies = []InboxPolicy{{Source: tc.source, Tag: connector.TagRepo, Value: "org/private", Twin: twin}}
				// The sentinel is progress evidence for this pass, never blocked by the target's policy.
				sentinel.Connector.Tags = nil
				h.inbox.mu.Unlock()
				h.inbox.srcMu.Lock()
				h.inbox.builtin["larkcli"] = true
				h.inbox.srcMu.Unlock()
				h.inbox.handing.Lock()
				t.Cleanup(func() { h.inbox.handing.Unlock() })
				h.handleRecoveredInbox(ctx, []string{target.ID, sentinel.ID})
				waitFor(t, "recovery reached escalation after incomplete triage", func() bool {
					h.inbox.mu.Lock()
					defer h.inbox.mu.Unlock()
					return sentinel.Agent != nil && sentinel.Agent.State == AgentQueued
				})
				h.inbox.mu.Lock()
				blocked := target.TriagedAt != target.Last && target.Agent == nil && target.Tier == TierAction
				h.inbox.mu.Unlock()
				if !blocked || len(model.asked()) == 0 {
					t.Fatal("incomplete triage escalated the old action tier or bypassed the triage call")
				}

				model.reply.Store(`{"tier":"action","summary":"Ready after recovery","reason":"Synthetic verdict"}`)
				model.mode.Store("ok")
				if tc.mode == "busy" || tc.mode == "quota" {
					pauseOver(t, client)
				}
				h.handleRecoveredInbox(ctx, []string{target.ID})
				waitFor(t, "a later explicit recovery retried triage and honored policy", func() bool {
					h.inbox.mu.Lock()
					defer h.inbox.mu.Unlock()
					return target.TriagedAt == target.Last &&
						(twin == TwinNever || target.Agent != nil && target.Agent.State == AgentQueued)
				})
				h.inbox.mu.Lock()
				defer h.inbox.mu.Unlock()
				if twin == TwinNever && target.Agent != nil || !target.UpdatedAt.Equal(at) {
					t.Fatalf("successful retry ignored policy or changed source time: %+v", target)
				}
			})
		}
	}
}

func TestRecoveredReplayIncompleteTriageDoesNotStartLegacyQueuedItem(t *testing.T) {
	_, client := newTriageModel(t, "busy", `{}`)
	h, _ := admitHub(t, client)
	h.setConfig(func(c *assistantwire.InboxConfig) {
		c.Agent.Enabled, c.Agent.Concurrency = true, 2
	})
	h.f.failStart = errors.New("synthetic start stopped")
	ctx := context.Background()
	if err := h.inboxReady(ctx); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-4 * 24 * time.Hour)
	target := replayScopeItem("legacy-queued", "larkcli", "thread:legacy", "om_new_activity", "Example", at)
	target.Kind, target.Context = KindDM, "New activity on a legacy queued request awaiting triage"
	target.Agent = &InboxAgent{State: AgentQueued, Of: "om_previous_activity"}
	sentinel := replayScopeItem("ready-sentinel", "example", "thread:sentinel", "om_ready", "Example", at)
	sentinel.Context, sentinel.TriagedAt = "Selected request already triaged", sentinel.Last
	h.inbox.mu.Lock()
	h.inbox.items[target.ID], h.inbox.items[sentinel.ID] = target, sentinel
	h.inbox.mu.Unlock()
	h.inbox.srcMu.Lock()
	h.inbox.builtin["larkcli"] = true
	h.inbox.srcMu.Unlock()
	h.handleRecoveredInbox(ctx, []string{target.ID, sentinel.ID})
	waitFor(t, "the ready selected item reached the start pass", func() bool {
		h.inbox.mu.Lock()
		defer h.inbox.mu.Unlock()
		return sentinel.Agent != nil && sentinel.Agent.Attempts > 0
	})
	h.inbox.handing.Lock() // The start pass entered above; wait for its complete result, not a sleep.
	h.inbox.handing.Unlock()
	h.inbox.mu.Lock()
	defer h.inbox.mu.Unlock()
	if target.TriagedAt == target.Last || target.Agent.Attempts != 0 || target.Agent.StartedAt != nil {
		t.Fatalf("incomplete triage allowed an old queued record to start: %+v", target)
	}
}
