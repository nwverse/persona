package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
)

func TestInboxReplyApprovalStaleCleanupRemovesOnlyItsWarning(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		for _, action := range []string{"replacement", "rejection"} {
			t.Run(fmt.Sprintf("legacy-%t/%s", legacy, action), func(t *testing.T) {
				s, f, it, _, now := approvalBlockedFixture(t, 0)
				ctx := context.Background()
				explanation := i18n.T(s.inbox.userLang(), "assistant-commitments.approval_overdue", i18n.Args{"hours": 20})
				base := "Original note\n" + explanation
				s.inbox.mu.Lock()
				it.Suggestion.Note = base
				it.Chain = []InboxChainEntry{{ID: "existing-check", Kind: ChainCheck, Text: explanation}}
				if legacy {
					legacyApprovalDraft(it)
				}
				s.inbox.mu.Unlock()
				s.checkInboxReplyApprovals(ctx, now)
				if currentSuggestion(it).Note != base+"\n"+explanation || len(it.Chain) != 2 {
					t.Fatal("fixture did not append one distinct warning")
				}
				reminderID := it.Chain[1].ID
				if action == "rejection" {
					if _, err := s.actOnSuggestion(ctx, it.ID, suggestionReq{Version: 1, Action: suggestReject}, true); err != nil {
						t.Fatal(err)
					}
				} else {
					s.inbox.mu.Lock()
					g := *currentSuggestion(it)
					g.Version, g.At = 2, now
					it.Suggestion = &g
					s.inbox.mu.Unlock()
				}
				s.inbox.mu.Lock()
				legacyNote := base
				if legacy {
					legacyNote = strings.TrimSuffix(it.Agent.Note, "\n"+explanation)
				}
				s.inbox.mu.Unlock()
				s.clearStaleReplyApprovals(ctx)
				s.inbox.mu.Lock()
				if it.ReplyApproval != nil || it.Urgent || it.Reason != "Original reason" {
					t.Error("stale reminder kept its receipt or urgency")
				}
				if it.Suggestion != nil && it.Suggestion.Note != base {
					t.Errorf("replacement lost preexisting text or kept the old warning: %q", it.Suggestion.Note)
				}
				if legacy && it.Agent.Note != legacyNote {
					t.Errorf("legacy note lost preexisting text or kept the old warning: %q", it.Agent.Note)
				}
				if len(it.Chain) == 0 || it.Chain[0].ID != "existing-check" || it.Chain[0].Text != explanation {
					t.Error("cleanup removed an older identical check")
				}
				for _, e := range it.Chain {
					if e.ID == reminderID {
						t.Error("cleanup left its obsolete ChainCheck")
					}
				}
				before, _ := json.Marshal(it)
				s.inbox.mu.Unlock()
				s.clearStaleReplyApprovals(ctx)
				s.inbox.mu.Lock()
				after, _ := json.Marshal(it)
				s.inbox.mu.Unlock()
				if string(before) != string(after) {
					t.Error("repeated cleanup changed the row again")
				}
				f.mu.Lock()
				defer f.mu.Unlock()
				if len(f.pushes) != 1 {
					t.Error("cleanup sent an additional notification")
				}
			})
		}
	}
}

func TestInboxReplyApprovalStaleCleanupFailureProtectsNotesAndChain(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		for _, change := range []string{"unchanged", "new note", "new chain"} {
			t.Run(fmt.Sprintf("legacy-%t/%s", legacy, change), func(t *testing.T) {
				s, _, it, st, now := approvalBlockedFixture(t, 3)
				if legacy {
					s.inbox.mu.Lock()
					legacyApprovalDraft(it)
					s.inbox.mu.Unlock()
				}
				s.checkInboxReplyApprovals(context.Background(), now)
				s.inbox.mu.Lock()
				reminderID := it.Chain[len(it.Chain)-1].ID
				remaining := len(it.Chain) - 1
				if legacy {
					it.Agent.State = AgentSkipped
				} else {
					it.Suggestion.Version = 2
				}
				before, _ := json.Marshal(it)
				s.inbox.mu.Unlock()
				done := make(chan struct{})
				go func() {
					s.clearStaleReplyApprovals(context.Background())
					close(done)
				}()
				approvalAwaitWrite(t, st)
				s.inbox.mu.Lock()
				note := ""
				if legacy {
					note = it.Agent.Note
				} else {
					note = it.Suggestion.Note
				}
				staged := note == "Original note" && len(it.Chain) == remaining
				switch change {
				case "new note":
					if legacy {
						it.Agent.Note = "New explanation"
					} else {
						it.Suggestion.Note = "New explanation"
					}
				case "new chain":
					it.Chain = append(it.Chain, InboxChainEntry{ID: "new-check", Kind: ChainCheck, Text: "A new check"})
				}
				newer, _ := json.Marshal(it)
				s.inbox.mu.Unlock()
				close(st.release)
				<-done
				s.inbox.mu.Lock()
				got, _ := json.Marshal(it)
				s.inbox.mu.Unlock()
				if !staged {
					t.Error("cleanup did not stage removal of its note and history")
				}
				want := newer
				if change == "unchanged" {
					want = before
				}
				if string(got) != string(want) {
					t.Errorf("failed cleanup damaged its old row or a newer mutation:\nwant %s\n got %s", want, got)
				}
				if change == "unchanged" {
					s.clearStaleReplyApprovals(context.Background())
					if it.ReplyApproval != nil || len(it.Chain) != remaining {
						t.Error("cleanup retry left its receipt or chain")
					}
					for _, e := range it.Chain {
						if e.ID == reminderID {
							t.Error("cleanup retry kept its reminder check")
						}
					}
				}
			})
		}
	}
}

func TestInboxReplyApprovalStaleCleanupSurvivesRestart(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprintf("legacy-%t", legacy), func(t *testing.T) {
			ctx := context.Background()
			database := taskRecoveryDatabase(t)
			s, _ := newQuietServiceOn(t, database)
			holdDigest(s)
			if err := s.inboxReady(ctx); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			it := &InboxItem{ID: "cleanup-restart", Source: "example", Tier: TierAction, Last: "a",
				Suggestion: &InboxSuggestion{Version: 1, Kind: SuggestReply, Text: "Original draft",
					Note: "Original note", At: now.Add(-5 * time.Hour)}}
			if legacy {
				legacyApprovalDraft(it)
			}
			s.inbox.mu.Lock()
			s.inbox.items[it.ID] = it
			s.inbox.mu.Unlock()
			s.checkInboxReplyApprovals(ctx, now)
			restarted, hub := newQuietServiceOn(t, database)
			holdDigest(restarted)
			if err := restarted.inboxReady(ctx); err != nil {
				t.Fatal(err)
			}
			restarted.inbox.mu.Lock()
			recovered := restarted.inbox.items[it.ID]
			if recovered == nil || recovered.ReplyApproval == nil {
				restarted.inbox.mu.Unlock()
				t.Fatal("restart lost the reminder receipt")
			}
			if legacy {
				recovered.Agent.State = AgentSkipped
			} else {
				recovered.Suggestion.Version = 2
			}
			restarted.inbox.mu.Unlock()
			restarted.clearStaleReplyApprovals(ctx)
			next, _ := newQuietServiceOn(t, database)
			holdDigest(next)
			if err := next.inboxReady(ctx); err != nil {
				t.Fatal(err)
			}
			next.inbox.mu.Lock()
			got := next.inbox.items[it.ID]
			if got == nil || got.ReplyApproval != nil {
				next.inbox.mu.Unlock()
				t.Fatal("cleanup was not persisted")
			}
			note := ""
			if legacy {
				note = got.Agent.Note
			} else {
				note = got.Suggestion.Note
			}
			if note != "Original note" {
				t.Errorf("restart kept the obsolete note: %q", note)
			}
			for _, e := range got.Chain {
				if e.Kind == ChainCheck && strings.Contains(e.Text, "hour") {
					t.Error("restart kept the obsolete check")
				}
			}
			next.inbox.mu.Unlock()
			hub.mu.Lock()
			defer hub.mu.Unlock()
			if len(hub.pushes) != 0 {
				t.Error("cleanup after restart sent a notification")
			}
		})
	}
}

func TestInboxReplyApprovalStaleCleanupPreservesUnownedText(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprintf("legacy-%t", legacy), func(t *testing.T) {
			s, _, it, _, _ := approvalBlockedFixture(t, 0)
			const note = "Original note\nAn older waiting-time explanation"
			s.inbox.mu.Lock()
			it.Suggestion.Note = note
			it.ReplyApproval = &inboxReplyApproval{Version: 0, Explanation: "An older waiting-time explanation",
				PreviousReason: it.Reason, Notified: true}
			it.Urgent, it.Reason = true, it.ReplyApproval.Explanation
			it.Chain = []InboxChainEntry{{ID: "existing-check", Kind: ChainCheck, Text: it.ReplyApproval.Explanation}}
			if legacy {
				legacyApprovalDraft(it)
			}
			before, _ := json.Marshal(it.Chain)
			s.inbox.mu.Unlock()
			s.clearStaleReplyApprovals(context.Background())
			s.inbox.mu.Lock()
			got := currentSuggestion(it).Note
			after, _ := json.Marshal(it.Chain)
			s.inbox.mu.Unlock()
			if got != note || string(before) != string(after) {
				t.Error("a receipt without ownership markers removed existing text or history")
			}
		})
	}
}

func TestInboxReplyApprovalStaleCleanupPreservesUnrelatedAgentNote(t *testing.T) {
	s, _, it, _, now := approvalBlockedFixture(t, 0)
	explanation := i18n.T(s.inbox.userLang(), "assistant-commitments.approval_overdue", i18n.Args{"hours": 20})
	note := "An earlier agent's note\n" + explanation
	s.inbox.mu.Lock()
	it.Agent = &InboxAgent{State: AgentSkipped, Note: note}
	s.inbox.mu.Unlock()
	s.checkInboxReplyApprovals(context.Background(), now)
	s.inbox.mu.Lock()
	it.Suggestion.Version = 2
	s.inbox.mu.Unlock()
	s.clearStaleReplyApprovals(context.Background())
	s.inbox.mu.Lock()
	defer s.inbox.mu.Unlock()
	if it.Agent.Note != note || it.Suggestion.Note != "Original note" {
		t.Error("suggestion cleanup removed a suffix that it never added to the agent's note")
	}
}
