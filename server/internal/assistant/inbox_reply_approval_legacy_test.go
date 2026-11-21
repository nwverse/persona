package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func legacyApprovalDraft(it *InboxItem) {
	g := it.Suggestion
	it.Agent = &InboxAgent{State: AgentDrafted, Draft: g.Text, Note: g.Note, DraftedAt: &g.At}
	it.Suggestion = nil
}

func TestInboxReplyApprovalLegacyWarningSurvivesRestart(t *testing.T) {
	for _, clock := range []string{"draft time", "legacy source time"} {
		t.Run(clock, func(t *testing.T) {
			ctx := context.Background()
			database := taskRecoveryDatabase(t)
			s, f := newQuietServiceOn(t, database)
			holdDigest(s)
			if err := s.inboxReady(ctx); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			it := &InboxItem{ID: "legacy-approval", Source: "example", Tier: TierAction, Last: "a",
				UpdatedAt: now.Add(-20 * time.Hour), Suggestion: &InboxSuggestion{
					Version: 1, Kind: SuggestReply, Text: "The prepared answer.", Note: "Original explanation",
					At: now.Add(-4 * time.Hour)},
				Connector: &InboxConnector{Thread: "legacy-thread", URL: "https://example.com/messages/legacy"}}
			legacyApprovalDraft(it)
			if clock == "legacy source time" {
				it.Agent.DraftedAt = nil
			}
			s.inbox.mu.Lock()
			s.inbox.items[it.ID] = it
			s.inbox.mu.Unlock()
			if failed := s.saveInbox(ctx, []*InboxItem{it}); failed[it.ID] {
				t.Fatal("could not store the original legacy draft")
			}
			if clock == "draft time" {
				s.superviseWatch(ctx, now.Add(-time.Nanosecond))
				if it.Urgent {
					t.Fatal("legacy draft was reminded before four hours")
				}
			}
			s.superviseWatch(ctx, now)
			g := currentSuggestion(it)
			if it.Suggestion != nil || g == nil || g.Text != "The prepared answer." ||
				!strings.Contains(g.Note, "Original explanation") || !strings.Contains(g.Note, "20") ||
				!strings.Contains(it.Agent.Note, "20") {
				t.Errorf("legacy draft lost its visible waiting-time warning: agent=%+v, current=%+v", it.Agent, g)
			}
			if !it.Urgent || it.nowKey() != NowConfirmReply ||
				it.Chain[len(it.Chain)-1].Detail != it.Connector.URL {
				t.Errorf("legacy reminder lost its existing Needs you home or source: %+v", it.listed())
			}
			f.mu.Lock()
			pushes := len(f.pushes)
			f.mu.Unlock()
			if pushes != 1 {
				t.Fatalf("legacy draft sent %d owner notifications, want 1", pushes)
			}
			restarted, nextHub := newQuietServiceOn(t, database)
			holdDigest(restarted)
			if err := restarted.inboxReady(ctx); err != nil {
				t.Fatal(err)
			}
			restarted.inbox.mu.Lock()
			recovered := restarted.inbox.items[it.ID]
			if recovered == nil || recovered.Suggestion != nil || recovered.Agent == nil ||
				recovered.Agent.Draft != "The prepared answer." || !strings.Contains(recovered.Agent.Note, "20") ||
				!strings.Contains(currentSuggestion(recovered).Note, "20") {
				t.Errorf("Postgres reload lost the legacy draft warning: %+v", recovered)
			}
			restarted.inbox.mu.Unlock()
			restarted.superviseWatch(ctx, now.Add(time.Minute))
			nextHub.mu.Lock()
			if len(nextHub.pushes) != 0 {
				t.Error("manager reconstruction duplicated the persisted legacy reminder")
			}
			nextHub.mu.Unlock()
		})
	}
}

func TestInboxReplyApprovalLegacyFailedSaveProtectsNote(t *testing.T) {
	for _, replacement := range []string{"unchanged", "new note", "new agent"} {
		t.Run(replacement, func(t *testing.T) {
			s, _, it, st, now := approvalBlockedFixture(t, 1)
			legacyApprovalDraft(it)
			done := make(chan struct{})
			go func() {
				s.checkInboxReplyApprovals(context.Background(), now)
				close(done)
			}()
			approvalAwaitWrite(t, st)
			s.inbox.mu.Lock()
			stagedWarning := strings.Contains(it.Agent.Note, "20")
			switch replacement {
			case "new note":
				it.Agent.Note = "New explanation"
			case "new agent":
				it.Agent = &InboxAgent{State: AgentDrafted, Draft: "New draft", Note: "New explanation", DraftedAt: &now}
			}
			want, _ := json.Marshal(it)
			s.inbox.mu.Unlock()
			close(st.release)
			<-done
			if !stagedWarning {
				t.Error("reminder edited a temporary suggestion instead of the legacy note")
			}
			if replacement == "unchanged" {
				if it.Agent.Note != "Original note" || it.Urgent || it.Reason != "Original reason" {
					t.Errorf("failed legacy reminder did not restore its own note and urgency: %+v", it)
				}
				s.checkInboxReplyApprovals(context.Background(), now)
				if !strings.Contains(it.Agent.Note, "20") || !it.Urgent {
					t.Error("retry did not persist the legacy waiting-time warning")
				}
			} else {
				got, _ := json.Marshal(it)
				if string(got) != string(want) {
					t.Errorf("failed legacy reminder overwrote a replacement:\nwant %s\n got %s", want, got)
				}
			}
		})
	}
}

func TestInboxReplyApprovalLegacyRepairsExistingReceipt(t *testing.T) {
	for _, notified := range []bool{false, true} {
		name := "notification pending"
		if notified {
			name = "already notified"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			database := taskRecoveryDatabase(t)
			s, f := newQuietServiceOn(t, database)
			holdDigest(s)
			if err := s.inboxReady(ctx); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			const explanation = "The other person has waited 20 hours."
			it := &InboxItem{ID: "legacy-reminded", Source: "example", Tier: TierAction, Last: "a",
				Urgent: true, Reason: explanation, UpdatedAt: now.Add(-20 * time.Hour),
				Suggestion: &InboxSuggestion{Version: 1, Kind: SuggestReply, Text: "Original draft",
					Note: "Original note", At: now.Add(-5 * time.Hour)},
				ReplyApproval: &inboxReplyApproval{Version: 1, Notified: notified, Explanation: explanation},
				Chain:         []InboxChainEntry{{ID: "original-check", Kind: ChainCheck, Text: explanation}},
				Connector:     &InboxConnector{Thread: "legacy-thread", URL: "https://example.com/messages/legacy"}}
			legacyApprovalDraft(it)
			s.inbox.mu.Lock()
			s.inbox.items[it.ID] = it
			s.inbox.mu.Unlock()
			if failed := s.saveInbox(ctx, []*InboxItem{it}); failed[it.ID] {
				t.Fatal("could not store the already-reminded legacy draft")
			}
			s.checkInboxReplyApprovals(ctx, now)
			if it.Suggestion != nil || it.Agent.Draft != "Original draft" ||
				it.Agent.Note != "Original note\n"+explanation || len(it.Chain) != 1 ||
				it.Chain[0].ID != "original-check" || !it.ReplyApproval.Notified {
				t.Errorf("existing reminder was not repaired without changing its draft or history: %+v", it)
			}
			f.mu.Lock()
			pushes := len(f.pushes)
			f.mu.Unlock()
			wantPushes := 1
			if notified {
				wantPushes = 0
			}
			if pushes != wantPushes {
				t.Errorf("repair sent %d notifications, want %d", pushes, wantPushes)
			}
			restarted, nextHub := newQuietServiceOn(t, database)
			holdDigest(restarted)
			if err := restarted.inboxReady(ctx); err != nil {
				t.Fatal(err)
			}
			recovered := restarted.inbox.items[it.ID]
			if recovered == nil || recovered.Suggestion != nil || recovered.Agent == nil ||
				recovered.Agent.Note != "Original note\n"+explanation || !recovered.Urgent ||
				recovered.Reason != explanation {
				t.Fatalf("Postgres reload lost the repaired legacy warning: %+v", recovered)
			}
			restarted.checkInboxReplyApprovals(ctx, now.Add(time.Minute))
			nextHub.mu.Lock()
			pushes = len(nextHub.pushes)
			nextHub.mu.Unlock()
			if pushes != 0 || strings.Count(recovered.Agent.Note, explanation) != 1 || len(recovered.Chain) != 1 {
				t.Error("reloaded legacy repair duplicated its notification, explanation or history")
			}
		})
	}
}

func TestInboxReplyApprovalLegacyRepairFailureRetriesWithoutChainLoss(t *testing.T) {
	for _, notified := range []bool{false, true} {
		name := "notification pending"
		if notified {
			name = "already notified"
		}
		t.Run(name, func(t *testing.T) {
			s, f, it, st, now := approvalBlockedFixture(t, 0)
			legacyApprovalDraft(it)
			const explanation = "The other person has waited 20 hours."
			it.ReplyApproval = &inboxReplyApproval{Version: 1, Notified: notified, Explanation: explanation}
			it.Urgent, it.Reason = true, explanation
			it.Chain = []InboxChainEntry{{Kind: ChainCheck, Text: "A preexisting legacy check"}}
			it.arrived(now)
			before, _ := json.Marshal(it)
			st.imu.Lock()
			st.down = errors.New("synthetic legacy repair failure")
			st.imu.Unlock()
			s.checkInboxReplyApprovals(context.Background(), now)
			after, _ := json.Marshal(it)
			if string(after) != string(before) {
				t.Errorf("failed note-only repair changed the draft or removed history:\nwant %s\n got %s", before, after)
			}
			f.mu.Lock()
			pushes := len(f.pushes)
			f.mu.Unlock()
			if pushes != 0 {
				t.Fatal("failed repair sent a notification before its note was saved")
			}
			st.imu.Lock()
			st.down = nil
			st.imu.Unlock()
			s.checkInboxReplyApprovals(context.Background(), now)
			if it.Agent.Note != "Original note\n"+explanation || len(it.Chain) != 1 || !it.ReplyApproval.Notified {
				t.Fatal("retry did not repair the stored note while preserving its receipt and history")
			}
			before, _ = json.Marshal(it)
			s.checkInboxReplyApprovals(context.Background(), now.Add(time.Minute))
			after, _ = json.Marshal(it)
			if string(after) != string(before) {
				t.Error("repeated repair changed the already-repaired draft")
			}
			wantPushes := 1
			if notified {
				wantPushes = 0
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.pushes) != wantPushes {
				t.Errorf("repair retry sent %d notifications, want %d", len(f.pushes), wantPushes)
			}
		})
	}
}

func TestInboxReplyApprovalLegacyRepairFailedSavePreservesReplacement(t *testing.T) {
	for _, replacement := range []string{"new note", "new agent", "new suggestion"} {
		t.Run(replacement, func(t *testing.T) {
			s, f, it, st, now := approvalBlockedFixture(t, 1)
			legacyApprovalDraft(it)
			const explanation = "The other person has waited 20 hours."
			it.ReplyApproval = &inboxReplyApproval{Version: 1, Notified: true, Explanation: explanation}
			it.Urgent, it.Reason = true, explanation
			it.Chain = []InboxChainEntry{{Kind: ChainCheck, Text: "A preexisting legacy check"}}
			done := make(chan struct{})
			go func() {
				s.checkInboxReplyApprovals(context.Background(), now)
				close(done)
			}()
			approvalAwaitWrite(t, st)
			s.inbox.mu.Lock()
			staged := it.Agent.Note == "Original note\n"+explanation
			switch replacement {
			case "new note":
				it.Agent.Note = "New explanation"
			case "new agent":
				a := *it.Agent
				it.Agent = &a
			case "new suggestion":
				it.Suggestion = currentSuggestion(it)
			}
			want, _ := json.Marshal(it)
			s.inbox.mu.Unlock()
			close(st.release)
			<-done
			got, _ := json.Marshal(it)
			if !staged || string(got) != string(want) {
				t.Errorf("failed legacy repair overwrote a replacement:\nwant %s\n got %s", want, got)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.pushes) != 0 {
				t.Error("failed repair repeated an already-delivered notification")
			}
		})
	}
}
