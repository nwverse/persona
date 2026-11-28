package assistant

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Existing Inbox and supervisor entry points keep these tests compilable when
// the production change is removed for the red regression check.
func TestInboxReplyApprovalOverdueNeedsYou(t *testing.T) {
	h := newInboxHub(t)
	holdDigest(h.Service)
	if err := h.inboxReady(context.Background()); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	it := &InboxItem{ID: "approval-example", Source: "example", Account: "owner", Last: "message-1",
		Title: "Example question", Sender: "Example sender", Tier: TierAction, StartedAt: now.Add(-20 * time.Hour),
		UpdatedAt: now.Add(-20 * time.Hour), Suggestion: &InboxSuggestion{
			Version: 1, Kind: SuggestReply, Text: "Here is the prepared answer.", At: now.Add(-4 * time.Hour)},
		Connector: &InboxConnector{Thread: "thread-1", URL: "https://example.com/messages/1", CanReply: true}}
	h.inbox.mu.Lock()
	h.inbox.items[it.ID] = it
	h.inbox.mu.Unlock()
	ctx := context.Background()
	h.superviseWatch(ctx, now.Add(-time.Nanosecond))
	if it.Urgent {
		t.Fatal("draft became overdue before its four-hour threshold")
	}
	h.superviseWatch(ctx, now)
	if !it.Urgent || it.status() != StatusAction || it.nowKey() != NowConfirmReply {
		t.Fatalf("overdue draft was not actionable in its existing Needs you home: %+v", it.listed())
	}
	if it.Suggestion.Text != "Here is the prepared answer." || !strings.Contains(it.Suggestion.Note, "20") {
		t.Fatalf("draft was changed or did not show how long the other person waited: %+v", it.Suggestion)
	}
	last := it.Chain[len(it.Chain)-1]
	if last.Link == nil || last.Link.URL != it.Connector.URL || !strings.Contains(last.Text, "20") {
		t.Fatalf("reminder omitted original-message link/wait: %+v", last)
	}
	before := len(it.Chain)
	h.f.mu.Lock()
	pushes := len(h.f.pushes)
	h.f.mu.Unlock()
	if pushes != 1 {
		t.Fatalf("overdue draft sent %d owner notifications, want 1", pushes)
	}
	h.superviseWatch(ctx, now.Add(time.Hour))
	if len(it.Chain) != before {
		t.Fatal("unchanged pending draft was reminded twice")
	}
	h.f.mu.Lock()
	if len(h.f.pushes) != pushes {
		t.Error("unchanged draft sent another owner notification")
	}
	h.f.mu.Unlock()
}

func TestInboxReplyApprovalRetriesNotificationWithoutDuplicatingRequest(t *testing.T) {
	h := newInboxHub(t)
	holdDigest(h.Service)
	ctx := context.Background()
	if err := h.inboxReady(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	it := &InboxItem{ID: "approval-retry", Source: "example", Tier: TierAction, Last: "a",
		UpdatedAt: now.Add(-20 * time.Hour), Suggestion: &InboxSuggestion{
			Version: 1, Kind: SuggestReply, Text: "Draft", At: now.Add(-5 * time.Hour)}}
	h.inbox.mu.Lock()
	h.inbox.items[it.ID] = it
	h.inbox.mu.Unlock()
	h.f.mu.Lock()
	h.f.failNotify = 1
	h.f.mu.Unlock()
	h.superviseWatch(ctx, now)
	chain := len(it.Chain)
	h.superviseWatch(ctx, now.Add(time.Minute))
	h.f.mu.Lock()
	pushes := len(h.f.pushes)
	h.f.mu.Unlock()
	if pushes != 1 || len(it.Chain) != chain || it.Suggestion.Text != "Draft" {
		t.Fatalf("retry duplicated the request or lost its notification: pushes=%d chain=%d/%d", pushes, chain, len(it.Chain))
	}
	// Replacement gets its own four-hour window; the old receipt cannot silence it.
	h.inbox.mu.Lock()
	it.Suggestion = &InboxSuggestion{Version: 2, Kind: SuggestReply, Text: "Revised draft", At: now}
	h.inbox.mu.Unlock()
	h.superviseWatch(ctx, now.Add(4*time.Hour-time.Nanosecond))
	if len(it.Chain) != chain-1 || it.Urgent {
		t.Fatal("replacement inherited the old suggestion's deadline")
	}
	h.superviseWatch(ctx, now.Add(4*time.Hour))
	if len(it.Chain) != chain {
		t.Fatal("old reminder receipt suppressed the replacement's reminder")
	}
}

func TestInboxReplyApprovalReceiptSurvivesManagerReconstruction(t *testing.T) {
	ctx := context.Background()
	database := taskRecoveryDatabase(t)
	s, f := newQuietServiceOn(t, database)
	holdDigest(s)
	if err := s.inboxReady(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	it := &InboxItem{ID: "approval-persisted", Source: "example", Tier: TierAction, Last: "a",
		UpdatedAt: now.Add(-20 * time.Hour), Suggestion: &InboxSuggestion{
			Version: 1, Kind: SuggestReply, Text: "Saved draft", At: now.Add(-5 * time.Hour)},
		Connector: &InboxConnector{Thread: "original", URL: "https://example.com/messages/1"}}
	s.inbox.mu.Lock()
	s.inbox.items[it.ID] = it
	s.inbox.mu.Unlock()
	if failed := s.saveInbox(ctx, []*InboxItem{it}); failed[it.ID] {
		t.Fatal("could not prepare the stored Inbox item")
	}
	s.superviseWatch(ctx, now)
	f.mu.Lock()
	pushes := len(f.pushes)
	f.mu.Unlock()
	if pushes != 1 {
		t.Fatalf("initial overdue notification count = %d, want 1", pushes)
	}
	restarted, nextHub := newQuietServiceOn(t, database)
	holdDigest(restarted)
	if err := restarted.inboxReady(ctx); err != nil {
		t.Fatal(err)
	}
	restarted.superviseWatch(ctx, now.Add(time.Hour))
	restarted.inbox.mu.Lock()
	recovered := restarted.inbox.items[it.ID]
	if recovered == nil || !recovered.Urgent || recovered.Suggestion == nil ||
		recovered.Suggestion.Text != "Saved draft" || !strings.Contains(recovered.Suggestion.Note, "20") {
		t.Errorf("restart lost the urgent pending draft or its source wait: %+v", recovered)
	}
	restarted.inbox.mu.Unlock()
	nextHub.mu.Lock()
	if len(nextHub.pushes) != 0 {
		t.Error("restart notified again for the same persisted suggestion")
	}
	nextHub.mu.Unlock()
}

func TestInboxReplyApprovalSkipsResolvedAndUnrelatedDrafts(t *testing.T) {
	for _, name := range []string{"sent", "dismissed", "merged", "agent", "conclusion", "noise", "future", "no timestamp"} {
		t.Run(name, func(t *testing.T) {
			h := newInboxHub(t)
			holdDigest(h.Service)
			if err := h.inboxReady(context.Background()); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			it := &InboxItem{ID: "approval-example", Source: "example", Tier: TierAction,
				Suggestion: &InboxSuggestion{Version: 1, Kind: SuggestReply, Text: "Draft", At: now.Add(-5 * time.Hour)}}
			switch name {
			case "sent":
				it.DoneAt = &now
			case "dismissed":
				it.Suggestion = nil
			case "merged":
				it.MergedInto = "survivor"
			case "agent":
				it.Suggestion.Kind = SuggestAgent
			case "conclusion":
				it.Suggestion.Kind = SuggestConclusion
			case "noise":
				it.Tier = TierNoise
			case "future":
				it.Suggestion.At = now.Add(time.Hour)
			case "no timestamp":
				it.Suggestion.At = time.Time{}
			}
			h.inbox.mu.Lock()
			h.inbox.items[it.ID] = it
			h.inbox.mu.Unlock()
			h.superviseWatch(context.Background(), now)
			if it.Urgent {
				t.Fatalf("%s acquired an obsolete reply reminder", name)
			}
		})
	}
}
