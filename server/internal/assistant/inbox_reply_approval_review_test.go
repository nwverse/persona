package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// Block one row write after saveInbox has snapshotted it. A newer mutation can
// arrive under inbox.mu while that write is in flight, before its failed undo.
type approvalBlockedStore struct {
	*inboxMemStore
	calls   atomic.Int32
	blockAt int32
	entered chan struct{}
	release chan struct{}
}

func (st *approvalBlockedStore) PutInboxItem(ctx context.Context, id string, raw []byte, at time.Time, done *time.Time) error {
	if st.calls.Add(1) == st.blockAt {
		close(st.entered)
		<-st.release
		return errors.New("synthetic reminder row failure")
	}
	return st.inboxMemStore.PutInboxItem(ctx, id, raw, at, done)
}

func approvalBlockedFixture(t *testing.T, blockAt int32) (*Service, *fakeHub, *InboxItem, *approvalBlockedStore, time.Time) {
	t.Helper()
	st := &approvalBlockedStore{inboxMemStore: newInboxMemStore(), blockAt: blockAt,
		entered: make(chan struct{}), release: make(chan struct{})}
	s, f := newQuietServiceOn(t, st)
	holdDigest(s)
	if err := s.inboxReady(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitNeedsYouIdle(t, s)
	now := time.Now().UTC()
	it := &InboxItem{ID: "approval-concurrent", Source: "example", Tier: TierAction, Last: "a",
		Reason: "Original reason", UpdatedAt: now.Add(-20 * time.Hour),
		Suggestion: &InboxSuggestion{Version: 1, Kind: SuggestReply, Text: "Original draft",
			Note: "Original note", At: now.Add(-5 * time.Hour)}}
	s.inbox.mu.Lock()
	s.inbox.items[it.ID] = it
	s.inbox.mu.Unlock()
	return s, f, it, st, now
}

func approvalAwaitWrite(t *testing.T, st *approvalBlockedStore) {
	t.Helper()
	select {
	case <-st.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("reminder did not reach its row write")
	}
}

func TestInboxReplyApprovalFailedSavePreservesReplacement(t *testing.T) {
	for _, change := range []string{"suggestion pointer", "suggestion value", "receipt pointer", "receipt value", "chain entry", "new version", "unchanged"} {
		t.Run(change, func(t *testing.T) {
			s, f, it, st, now := approvalBlockedFixture(t, 1)
			done := make(chan struct{})
			go func() {
				s.checkInboxReplyApprovals(context.Background(), now)
				close(done)
			}()
			approvalAwaitWrite(t, st)
			s.inbox.mu.Lock()
			switch change {
			case "suggestion pointer":
				g := *it.Suggestion
				it.Suggestion = &g
			case "suggestion value":
				it.Suggestion.Text, it.Suggestion.Note = "New draft", "New explanation"
				it.Urgent, it.Reason = false, "New reason"
			case "receipt pointer":
				r := *it.ReplyApproval
				it.ReplyApproval = &r
			case "receipt value":
				it.ReplyApproval.Explanation = "A newer receipt explanation"
			case "chain entry":
				it.Chain[len(it.Chain)-1].Text = "A newer check"
			case "new version":
				it.Suggestion = &InboxSuggestion{Version: 2, Kind: SuggestReply, Text: "New draft", Note: "New note", At: now}
			}
			want, _ := json.Marshal(it)
			g, receipt := it.Suggestion, it.ReplyApproval
			s.inbox.mu.Unlock()
			close(st.release)
			<-done
			s.inbox.mu.Lock()
			got, _ := json.Marshal(it)
			if change == "unchanged" {
				if it.ReplyApproval != nil || it.Urgent || it.Reason != "Original reason" || it.Suggestion.Note != "Original note" {
					t.Error("the reminder's own failed write was not rolled back")
				}
			} else if string(got) != string(want) || it.Suggestion != g || it.ReplyApproval != receipt {
				t.Errorf("failed old save overwrote newer %s:\nwant %s\n got %s", change, want, got)
			}
			s.inbox.mu.Unlock()
			f.mu.Lock()
			if len(f.pushes) != 0 {
				t.Error("an unpersisted or replaced reminder notified the owner")
			}
			f.mu.Unlock()
			if failed := s.saveInbox(context.Background(), []*InboxItem{it}); failed[it.ID] {
				t.Fatal("the replacement could not be saved after the failed write")
			}
		})
	}
}

func TestInboxReplyApprovalNotificationUndoPreservesReplacement(t *testing.T) {
	for _, change := range []string{"replacement", "unchanged"} {
		t.Run(change, func(t *testing.T) {
			s, _, it, st, now := approvalBlockedFixture(t, 2)
			done := make(chan struct{})
			go func() {
				s.checkInboxReplyApprovals(context.Background(), now)
				close(done)
			}()
			approvalAwaitWrite(t, st)
			s.inbox.mu.Lock()
			if change == "replacement" {
				g, receipt := *it.Suggestion, *it.ReplyApproval
				it.Suggestion, it.ReplyApproval = &g, &receipt
			}
			want, _ := json.Marshal(it)
			s.inbox.mu.Unlock()
			close(st.release)
			<-done
			got, _ := json.Marshal(it)
			if change == "replacement" && (string(got) != string(want) || !it.ReplyApproval.Notified) {
				t.Errorf("failed notification receipt save overwrote a newer receipt:\nwant %s\n got %s", want, got)
			} else if change == "unchanged" && it.ReplyApproval.Notified {
				t.Error("the failed notification receipt was not undone for retry")
			}
		})
	}
}

func TestInboxReplyApprovalStaleUndoPreservesReplacement(t *testing.T) {
	for _, change := range []string{"replacement", "unchanged"} {
		t.Run(change, func(t *testing.T) {
			s, _, it, st, _ := approvalBlockedFixture(t, 1)
			it.ReplyApproval = &inboxReplyApproval{Version: 0, PreviousReason: it.Reason, Explanation: "Old reminder"}
			it.Urgent, it.Reason = true, "Old reminder"
			original := it.ReplyApproval
			done := make(chan struct{})
			go func() {
				s.clearStaleReplyApprovals(context.Background())
				close(done)
			}()
			approvalAwaitWrite(t, st)
			s.inbox.mu.Lock()
			if change == "replacement" {
				g := *it.Suggestion
				g.Note = "New draft explanation"
				it.Suggestion = &g
			}
			want, _ := json.Marshal(it)
			s.inbox.mu.Unlock()
			close(st.release)
			<-done
			got, _ := json.Marshal(it)
			if change == "replacement" && (string(got) != string(want) || it.ReplyApproval != nil) {
				t.Errorf("failed stale cleanup restored an old receipt over a replacement:\nwant %s\n got %s", want, got)
			} else if change == "unchanged" && (it.ReplyApproval != original || !it.Urgent ||
				it.Reason != "Old reminder" || it.Suggestion.Note != "Original note") {
				t.Error("the failed stale cleanup was not undone on its unchanged row")
			}
		})
	}
}
