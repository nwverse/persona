package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/db"
)

func outwardReviewInbox(t *testing.T) (*inboxHub, *InboxItem, *replyRacingConnector) {
	t.Helper()
	h := newInboxHub(t)
	c := &replyRacingConnector{inboxConnector: inboxConnector{things: []connector.InboxThing{thing("a", time.Now())}}}
	h.addConnector(c, false)
	h.collectInbox(context.Background(), roundPoll)
	it := openItem(t, h)
	h.inbox.mu.Lock()
	it.Connector.URL = "https://github.com/example/project/issues/17"
	h.inbox.mu.Unlock()
	return h, it, c
}

func TestOutboundPromiseInboxLocalRepeatIsNotAnotherDelivery(t *testing.T) {
	for _, source := range []string{"connector", "mail"} {
		t.Run(source, func(t *testing.T) {
			ctx := context.Background()
			h, it, c := outwardReviewInbox(t)
			if source == "mail" {
				h.in.mail = &fakeMail{}
				h.inbox.mu.Lock()
				it.Connector = nil
				it.Mail = &InboxMail{AccountID: "example", Mailbox: "INBOX", UIDs: []uint32{1}}
				h.inbox.mu.Unlock()
			}
			text := "I'll share the results later."
			if _, err := h.replyInboxDo(ctx, it.ID, text, "11111111-1111-4111-8111-111111111111", false, false); err != nil {
				t.Fatal(err)
			}
			original := outwardReceipts(t, h.store())
			if len(original) != 1 || original[0].SentAt.IsZero() {
				t.Fatalf("missing initial delivery: %+v", original)
			}
			// A second client request is a local repeat by text, not a new
			// provider delivery or a readback for that second send ID.
			if _, err := h.replyInboxDo(ctx, it.ID, text, "22222222-2222-4222-8222-222222222222", false, false); err != nil {
				t.Fatal(err)
			}
			got := outwardReceipts(t, h.store())
			if len(got) != 1 || !got[0].SentAt.Equal(original[0].SentAt) || !got[0].FulfilledAt.IsZero() {
				t.Fatalf("local duplicate was recorded as another delivery: %+v", got)
			}
			if source == "connector" && len(c.replies) != 1 || source == "mail" && len(h.in.mail.sent) != 1 {
				t.Fatal("a local duplicate was sent to the provider")
			}
			if _, err := h.replyInboxDo(ctx, it.ID, "Here are the results.", "actual-update", false, false); err != nil {
				t.Fatal(err)
			}
			if got := outwardReceipts(t, h.store()); got[0].FulfilledAt.IsZero() {
				t.Fatal("a genuinely new provider delivery did not fulfill the promise")
			}
		})
	}
}

func TestOutboundPromiseInboxFreshDeliveryUsesActualSendTime(t *testing.T) {
	for _, source := range []string{"connector", "mail"} {
		t.Run(source, func(t *testing.T) {
			ctx := context.Background()
			h, it, _ := outwardReviewInbox(t)
			topic := "github/github.com/example/project#17"
			topicURL := it.Connector.URL
			if source == "mail" {
				h.in.mail = &fakeMail{}
				h.inbox.mu.Lock()
				it.Connector = nil
				it.Mail = &InboxMail{AccountID: "example", Mailbox: "INBOX", UIDs: []uint32{1}}
				h.inbox.mu.Unlock()
				topic = it.Source + "/" + it.Account + "/" + it.ID
				topicURL = "/inbox/" + it.ID
			}
			text := "I'll share the results later."
			const clientID = "44444444-4444-4444-8444-444444444444"
			intent := []map[string]any{{
				"reply":   map[string]any{"id": replySendID(it.ID, text, clientID, nil), "topic": topic, "url": topicURL, "text": text},
				"promise": true, "prepared_at": time.Now().Add(-3 * time.Hour).UTC(),
			}}
			if err := h.store().PutSetting(ctx, "assistant.outbound_commitments", intent); err != nil {
				t.Fatal(err)
			}
			before := time.Now()
			if _, err := h.replyInboxDo(ctx, it.ID, text, clientID, false, false); err != nil {
				t.Fatal(err)
			}
			got := outwardReceipts(t, h.store())
			if len(got) != 1 || got[0].SentAt.Before(before) || got[0].SentAt.After(time.Now()) ||
				!got[0].DueAt.Equal(got[0].SentAt.Add(24*time.Hour)) {
				t.Fatalf("fresh provider delivery was backdated to an older intent: %+v", got)
			}
		})
	}
}

func TestOutboundPromiseUnsupportedInboxReplyLeavesNoIntent(t *testing.T) {
	for _, source := range []string{"read-only connector", "connector changes", "mail changes", "empty mail", "migrated changes", "unsupported"} {
		t.Run(source, func(t *testing.T) {
			ctx := context.Background()
			h, it, c := outwardReviewInbox(t)
			var changes []DocChange
			h.inbox.mu.Lock()
			switch source {
			case "read-only connector":
				it.Connector.CanReply = false
			case "connector changes":
				changes = []DocChange{{Ref: "row", Field: "status", Value: "done"}}
			case "mail changes", "empty mail":
				it.Connector = nil
				it.Mail = &InboxMail{AccountID: "example", Mailbox: "INBOX"}
				if source == "mail changes" {
					it.Mail.UIDs = []uint32{1}
					changes = []DocChange{{Ref: "row", Field: "status", Value: "done"}}
				}
			case "migrated changes":
				it.Connector = nil
				it.Lark = &InboxLark{AccountID: 1, FeedID: "10"}
				changes = []DocChange{{Ref: "row", Field: "status", Value: "done"}}
			case "unsupported":
				it.Connector = nil
			}
			h.inbox.mu.Unlock()
			const clientID = "33333333-3333-4333-8333-333333333333"
			if _, err := h.replyInboxDo(ctx, it.ID, "I'll share the results later.", clientID, false, false, changes...); err == nil {
				t.Fatal("fixture unexpectedly supported this reply")
			}
			if got := outwardReceipts(t, h.store()); len(got) != 0 {
				t.Fatalf("reply rejected before a provider attempt left an outward intent: %+v", got)
			}
			if len(c.replies) != 0 {
				t.Fatal("unsupported reply reached the provider")
			}
			if it.Connector != nil {
				h.inbox.mu.Lock()
				it.Connector.CanReply = true
				h.inbox.mu.Unlock()
				// Reusing the request ID after correcting the unsupported
				// action must not confirm the text that was never sent.
				if _, err := h.replyInboxDo(ctx, it.ID, "Here are the results.", clientID, false, false); err != nil {
					t.Fatal(err)
				}
				if got := outwardReceipts(t, h.store()); len(got) != 0 {
					t.Fatalf("a later valid reply confirmed an unsent promise: %+v", got)
				}
			}
		})
	}
}

func outwardReviewRecord(t *testing.T, s *Service, task string, receipt map[string]any) error {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"id": task, "outward_reply": receipt})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.toolTodoUpdate(context.Background(), "server", "conv1", raw)
	return err
}

func TestOutboundPromiseExplicitReceiptMustStayInItsTopic(t *testing.T) {
	for _, tc := range []struct {
		name, topic, first, wrong, update string
	}{
		{"feishu",
			"https://applink.feishu.cn/client/thread/open?open_chat_id=oc_example&open_thread_id=omt_example",
			"https://applink.feishu.cn/client/thread/open?open_chat_id=oc_example&open_thread_id=omt_example&message_id=om_first",
			"https://applink.feishu.cn/client/thread/open?open_chat_id=oc_example&open_thread_id=omt_other&message_id=om_other",
			"https://applink.feishu.cn/client/thread/open?message_id=om_update&open_thread_id=omt_example&open_chat_id=oc_example"},
		{"lark",
			"https://applink.larksuite.com/client/thread/open?open_chat_id=oc_example&open_thread_id=omt_example",
			"https://applink.larksuite.com/client/thread/open?open_chat_id=oc_example&open_thread_id=omt_example&message_id=om_first",
			"https://applink.larksuite.com/client/thread/open?open_chat_id=oc_other&open_thread_id=omt_example&message_id=om_other",
			"https://applink.larksuite.com/client/thread/open?open_chat_id=oc_example&open_thread_id=omt_example&message_id=om_update"},
		{"generic path", "https://example.com/topics/17", "https://example.com/topics/17#first",
			"https://example.com/topics/18#other", "https://example.com/topics/17#update"},
		{"generic query", "https://example.com/discuss?topic=17", "https://example.com/discuss?topic=17#first",
			"https://example.com/discuss?topic=18#other", "https://example.com/discuss?topic=17#update"},
		{"different source", "https://example.com/topics/17", "https://example.com/topics/17#first",
			"https://github.com/example/project/issues/17#issuecomment-101", "https://example.com/topics/17#update"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			st := &memStore{}
			s, _ := newQuietServiceOn(t, st)
			holdDigest(s)
			task, err := s.createTodo(ctx, "Example follow-up", "", "conv1", "")
			if err != nil {
				t.Fatal(err)
			}
			at := time.Now().Add(-time.Hour).UTC()
			receipt := map[string]any{"topic_url": tc.topic, "sent_url": tc.first,
				"text": "I'll share the results later.", "sent_at": at}
			if err := outwardReviewRecord(t, s, task.ID, receipt); err != nil {
				t.Fatal(err)
			}
			receipt["sent_url"], receipt["text"], receipt["sent_at"] = tc.wrong, "Here are the results.", at.Add(time.Minute)
			if err := outwardReviewRecord(t, s, task.ID, receipt); err == nil {
				t.Fatal("a receipt from another conversation was accepted")
			}
			if got := outwardReceipts(t, st); len(got) != 1 || !got[0].FulfilledAt.IsZero() {
				t.Fatalf("a cross-topic receipt mutated the promise: %+v", got)
			}
			receipt["sent_url"] = tc.update
			if err := outwardReviewRecord(t, s, task.ID, receipt); err != nil {
				t.Fatal(err)
			}
			if got := outwardReceipts(t, st); len(got) != 2 || got[0].FulfilledAt.IsZero() {
				t.Fatalf("valid same-topic update did not fulfill its promise: %+v", got)
			}
		})
	}
}

type outwardAttachmentFailStore struct {
	*db.DB
	failure      atomic.Int32
	ledgerWrites atomic.Int32
}

func (st *outwardAttachmentFailStore) EditTodo(ctx context.Context, id string, e db.TodoEdit, at time.Time) (*db.TodoRow, error) {
	if st.failure.Load() == 1 {
		return nil, errors.New("task notes unavailable")
	}
	return st.DB.EditTodo(ctx, id, e, at)
}

func (st *outwardAttachmentFailStore) PutTodoSupervision(ctx context.Context, r db.TodoSupervisionRow) error {
	if st.failure.Load() == 2 {
		return errors.New("task supervision unavailable")
	}
	return st.DB.PutTodoSupervision(ctx, r)
}

func (st *outwardAttachmentFailStore) PutSetting(ctx context.Context, key string, value any) error {
	if key == "assistant.outbound_commitments" && st.ledgerWrites.Add(1) >= 3 && st.failure.Load() == 3 {
		return errors.New("final ledger write unavailable")
	}
	return st.DB.PutSetting(ctx, key, value)
}

func TestOutboundPromiseReceiptRetriesIncompleteAttachment(t *testing.T) {
	for i, name := range []string{"notes", "supervision", "final ledger write"} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			st := &outwardAttachmentFailStore{DB: todoTestDB(t)}
			s, f := newQuietServiceOn(t, st)
			holdDigest(s)
			task, err := s.createTodo(ctx, "Example follow-up", "", "conv1", "")
			if err != nil {
				t.Fatal(err)
			}
			at := time.Now().Add(-time.Hour).UTC()
			topic := "https://github.com/example/project/issues/17"
			receipt := map[string]any{"topic_url": topic, "sent_url": topic + "#issuecomment-101",
				"text": "I'll share the results later.", "sent_at": at}
			st.failure.Store(int32(i + 1))
			if err := outwardReviewRecord(t, s, task.ID, receipt); err == nil {
				t.Fatal("fixture did not fail after persisting the sent receipt")
			}
			before := outwardReceipts(t, st)
			if len(before) != 1 || !before[0].SentAt.Equal(at) {
				t.Fatalf("failed attachment lost the sent receipt: %+v", before)
			}
			if err := outwardReviewRecord(t, s, task.ID, receipt); err == nil {
				t.Fatal("receipt retry silently succeeded while attachment was still unavailable")
			}
			st.failure.Store(0)
			if err := outwardReviewRecord(t, s, task.ID, receipt); err != nil {
				t.Fatal(err)
			}
			got := outwardReceipts(t, st)
			task = todoNow(s, task.ID)
			if len(got) != 1 || !got[0].SentAt.Equal(before[0].SentAt) || !got[0].DueAt.Equal(before[0].DueAt) ||
				strings.Count(task.Notes, "<!-- outward-promise:") != 1 || !strings.Contains(task.Notes, receipt["text"].(string)) ||
				task.Supervision == nil {
				t.Fatalf("retry did not finish the original attachment exactly once: receipts=%+v task=%+v", got, task)
			}
			if len(f.notices()) != 0 {
				t.Fatal("receipt-only recovery sent another outward message")
			}
		})
	}
}

func TestOutboundPromiseReceiptRetryPreservesKnownDeliveryTime(t *testing.T) {
	ctx := context.Background()
	st := &outwardReceiptFailStore{Store: &memStore{}}
	s, _ := newQuietServiceOn(t, st)
	holdDigest(s)
	task, err := s.createTodo(ctx, "Example follow-up", "", "conv1", "")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Hour).UTC()
	topic := "https://github.com/example/project/issues/17"
	receipt := map[string]any{"topic_url": topic, "sent_url": topic + "#issuecomment-101",
		"text": "I'll share the results later.", "sent_at": at}
	if err := outwardReviewRecord(t, s, task.ID, receipt); err == nil {
		t.Fatal("fixture did not lose the initial receipt write")
	}
	// The exact same receipt remains the original delivery, even if its
	// recording is retried later with the caller's current timestamp.
	receipt["sent_at"] = at.Add(30 * time.Minute)
	if err := outwardReviewRecord(t, s, task.ID, receipt); err != nil {
		t.Fatal(err)
	}
	got := outwardReceipts(t, st)
	if len(got) != 1 || !got[0].SentAt.Equal(at) || !got[0].DueAt.Equal(at.Add(24*time.Hour)) {
		t.Fatalf("retry changed the known delivery time or promise deadline: %+v", got)
	}
}

func TestOutboundPromiseReceiptRetryDoesNotReopenCompletedTask(t *testing.T) {
	ctx := context.Background()
	st := &memStore{}
	s, _ := newQuietServiceOn(t, st)
	holdDigest(s)
	task, err := s.createTodo(ctx, "Example follow-up", "", "conv1", "")
	if err != nil {
		t.Fatal(err)
	}
	topic := "https://github.com/example/project/issues/17"
	receipt := map[string]any{"topic_url": topic, "sent_url": topic + "#issuecomment-101",
		"text": "I'll share the results later.", "sent_at": time.Now().Add(-time.Hour).UTC()}
	if err := outwardReviewRecord(t, s, task.ID, receipt); err != nil {
		t.Fatal(err)
	}
	done := true
	if _, err := s.editTodo(ctx, task.ID, db.TodoEdit{Done: &done}); err != nil {
		t.Fatal(err)
	}
	if err := outwardReviewRecord(t, s, task.ID, receipt); err != nil {
		t.Fatal(err)
	}
	got := outwardReceipts(t, st)
	s.todos.mu.RLock()
	tasks := len(s.todos.todos)
	s.todos.mu.RUnlock()
	if len(got) != 1 || got[0].Reply.TodoID != task.ID || tasks != 1 || todoNow(s, task.ID).DoneAt == nil {
		t.Fatalf("receipt retry reopened or replaced the owner's completed task: receipts=%+v tasks=%d", got, tasks)
	}
}
