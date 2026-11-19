package assistant

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/channel"
)

func TestOutboundPromiseConcurrentPreparedUpdateOrdering(t *testing.T) {
	for _, oldPath := range []string{"bot", "inbox"} {
		t.Run(oldPath, func(t *testing.T) {
			h, it := outwardMigratedInbox(t)
			ctx := context.Background()
			r := &channel.Run{ID: "ordering-bot", Channel: channel.Feishu, ReplyTo: "om_request",
				Ext: map[string]string{"chat": "oc_example", "thread": "omt_example"}}
			send := func(path, text, id string) error {
				if path == "inbox" {
					_, err := h.replyInboxDo(ctx, it.ID, text, id, false, false)
					return err
				}
				return (feishuSink{h.Service}).answer(ctx, r, text, "", time.Second, true, false, channel.EndDone)
			}
			newPath := "inbox"
			if oldPath == "inbox" {
				newPath = "bot"
			}
			if err := send(newPath, "I'll share the results later.", "original-promise"); err != nil {
				t.Fatal(err)
			}

			const oldText = "The earlier results are available."
			entered, release := make(chan struct{}), make(chan struct{})
			var enteredOnce, releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			hold := func() {
				enteredOnce.Do(func() { close(entered) })
				<-release
			}
			for _, method := range []string{FeishuPost, FeishuText} {
				h.f.answerAppBlocking(AppFeishu, method, func(raw json.RawMessage) (any, error) {
					if strings.Contains(string(raw), oldText) {
						hold()
					}
					return map[string]any{"message_id": "om_sent"}, nil
				})
			}
			if oldPath == "inbox" {
				h.in.mu.Lock()
				h.in.conns[0] = &extensionLarkRacing{extensionLark: &extensionLark{}, during: hold}
				h.in.mu.Unlock()
			}
			done, finished := make(chan error, 1), make(chan struct{})
			go func() {
				defer close(finished)
				done <- send(oldPath, oldText, "old-update")
			}()
			t.Cleanup(func() {
				unblock()
				select {
				case <-finished:
				case <-time.After(10 * time.Second):
					t.Error("held sender did not finish")
				}
			})
			select {
			case <-entered:
			case err := <-done:
				t.Fatalf("old send did not reach the held provider: %v", err)
			case <-time.After(10 * time.Second):
				t.Fatal("old send did not reach the provider")
			}
			pending := outwardReceipts(t, h.store())
			if len(pending) != 2 || pending[1].PreparedAt.IsZero() || !pending[1].SentAt.IsZero() {
				t.Fatalf("missing persisted, unconfirmed old update: %+v", pending)
			}
			if err := send(newPath, "I'll post an update within 3 hours.", "newer-promise"); err != nil {
				t.Fatal(err)
			}
			before := outwardReceipts(t, h.store())
			if len(before) != 3 || !before[2].Promise || before[2].SentAt.IsZero() ||
				!before[1].PreparedAt.Before(before[2].SentAt) || !before[2].FulfilledAt.IsZero() ||
				before[1].Reply.Topic != before[2].Reply.Topic {
				t.Fatalf("fixture did not establish a newer same-topic promise during the old send: %+v", before)
			}
			unblock()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("old send did not complete")
			}
			got := outwardReceipts(t, h.store())
			if len(got) != 3 || !got[1].SentAt.After(got[2].SentAt) || !got[2].FulfilledAt.IsZero() {
				t.Fatalf("earlier-prepared update fulfilled a newer promise on late success: %+v", got)
			}
			if err := send(oldPath, "Here are the current results.", "fresh-update"); err != nil {
				t.Fatal(err)
			}
			got = outwardReceipts(t, h.store())
			if len(got) != 4 || got[2].FulfilledAt.IsZero() || !got[3].PreparedAt.After(got[2].SentAt) {
				t.Fatalf("fresh cross-path update did not fulfill the newer promise: %+v", got)
			}
		})
	}
}

func TestOutboundPromisePreparedAtMustFollowSuccessfulPromise(t *testing.T) {
	at := time.Now().Add(-time.Hour).UTC()
	for _, tc := range []struct {
		name     string
		prepared time.Time
		fulfills bool
	}{
		{"unknown", time.Time{}, false},
		{"before", at.Add(-time.Nanosecond), false},
		{"equal", at, false},
		{"after", at.Add(time.Nanosecond), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			st := &memStore{}
			s, _ := newQuietServiceOn(t, st)
			holdDigest(s)
			promise := outboundReply{ID: "promise", Topic: "example-topic", URL: "https://example.com/topic",
				Text: "I'll share the results later."}
			if err := s.prepareOutbound(ctx, promise, at.Add(-time.Minute)); err != nil {
				t.Fatal(err)
			}
			if err := s.finishOutbound(ctx, promise, at); err != nil {
				t.Fatal(err)
			}
			update := outboundReply{ID: "update", Topic: promise.Topic, URL: promise.URL, Text: "Here are the results."}
			if err := s.prepareOutbound(ctx, update, tc.prepared); err != nil {
				t.Fatal(err)
			}
			if err := s.finishOutbound(ctx, update, at.Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
			got := outwardReceipts(t, st)
			if len(got) != 2 || !got[0].FulfilledAt.IsZero() != tc.fulfills {
				t.Fatalf("preparation must be strictly after the promise's successful send: %+v", got)
			}
		})
	}
}

func TestOutboundPromisePreparedOrderingKeepsExplicitProviderTime(t *testing.T) {
	at := time.Now().Add(-time.Hour).UTC()
	for _, tc := range []struct {
		name     string
		sent     time.Time
		fulfills bool
	}{
		{"before", at.Add(-time.Minute), false},
		{"equal", at, false},
		{"after", at.Add(time.Minute), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := &memStore{}
			s, _ := newQuietServiceOn(t, st)
			holdDigest(s)
			task, err := s.createTodo(context.Background(), "Example follow-up", "", "conv1", "")
			if err != nil {
				t.Fatal(err)
			}
			receipt := map[string]any{"topic_url": "https://github.com/example/project/issues/17",
				"sent_url": "https://github.com/example/project/issues/17#issuecomment-101",
				"text":     "I'll share the results later.", "sent_at": at}
			if err := outwardReviewRecord(t, s, task.ID, receipt); err != nil {
				t.Fatal(err)
			}
			receipt["sent_url"] = "https://github.com/example/project/issues/17#issuecomment-102"
			receipt["text"], receipt["sent_at"] = "Here are the results.", tc.sent
			if err := outwardReviewRecord(t, s, task.ID, receipt); err != nil {
				t.Fatal(err)
			}
			got := outwardReceipts(t, st)
			if len(got) != 2 || !got[0].FulfilledAt.IsZero() != tc.fulfills ||
				!got[1].PreparedAt.Equal(tc.sent) || !got[1].SentAt.Equal(tc.sent) {
				t.Fatalf("explicit receipt lost provider-time ordering: %+v", got)
			}
		})
	}
}
