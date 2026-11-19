package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/channel"
	"github.com/nwverse/persona/server/internal/connector"
)

// Use existing send/watch entry points and JSON, not new production symbols:
// restoring the base implementation still compiles and fails these assertions.
type outwardReceipt struct {
	Reply struct {
		ID     string
		Topic  string
		URL    string
		Text   string
		TodoID string `json:"todo_id"`
	}
	Promise     bool
	PreparedAt  time.Time `json:"prepared_at"`
	SentAt      time.Time `json:"sent_at"`
	DueAt       time.Time `json:"due_at"`
	FulfilledAt time.Time `json:"fulfilled_at"`
}

func outwardReceipts(t *testing.T, st Store) []outwardReceipt {
	t.Helper()
	var out []outwardReceipt
	if _, err := st.Setting(context.Background(), "assistant.outbound_commitments", &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func outwardBot(t *testing.T, st Store) (*Service, *fakeHub, *channel.Run, *atomic.Int32) {
	t.Helper()
	s, f := newQuietServiceOn(t, st)
	holdDigest(s)
	posts := &atomic.Int32{}
	f.answerApp(AppFeishu, FeishuPost, func(json.RawMessage) (any, error) {
		posts.Add(1)
		return map[string]any{"message_id": "om_sent"}, nil
	})
	f.answerApp(AppFeishu, FeishuReact, func(json.RawMessage) (any, error) { return map[string]any{}, nil })
	return s, f, &channel.Run{ID: "outward-1", Channel: channel.Feishu, ReplyTo: "om_request", MachineID: "server",
		SessionID: "worker-1", Ext: map[string]string{"chat": "oc_example", "thread": "omt_example"}}, posts
}

func sendOutwardBot(t *testing.T, s *Service, r *channel.Run, text string) {
	t.Helper()
	if err := (feishuSink{s}).answer(context.Background(), r, text, "", time.Second, true, false, channel.EndDone); err != nil {
		t.Fatal(err)
	}
}

func TestOutboundPromiseSendBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		delay      time.Duration
	}{
		{"default", "算完再同步", 24 * time.Hour}, // i18n-ignore: sent-message fixtures
		{"later", "稍后给结果，2小时后。", 2 * time.Hour},
		{"combined", "补完统一同步", 24 * time.Hour},
		{"english", "I'll share the results within 3 hours.", 3 * time.Hour},
		{"minutes", "We will post an update in 45 minutes.", 45 * time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := &memStore{}
			s, _, r, posts := outwardBot(t, st)
			sendOutwardBot(t, s, r, tc.text)
			got := outwardReceipts(t, st)
			if len(got) != 1 || !got[0].Promise || got[0].SentAt.IsZero() || got[0].Reply.Text != tc.text {
				t.Fatalf("successful bot send did not record its exact promise: %+v", got)
			}
			e := got[0]
			if e.DueAt.Sub(e.SentAt) != tc.delay || e.Reply.TodoID == "" ||
				!strings.Contains(e.Reply.URL, "open_thread_id=omt_example") {
				t.Fatalf("deadline/source/task: %+v", e)
			}
			task := todoNow(s, e.Reply.TodoID)
			for _, want := range []string{tc.text, e.Reply.URL, e.DueAt.Format(time.RFC3339)} {
				if !strings.Contains(task.Notes, want) {
					t.Fatalf("task notes omit %q: %s", want, task.Notes)
				}
			}
			if posts.Load() != 1 || task.Supervision == nil {
				t.Fatalf("send/supervision: posts=%d task=%+v", posts.Load(), task)
			}
			// The same delivery cannot fulfill its own promise.
			sendOutwardBot(t, s, r, tc.text)
			got = outwardReceipts(t, st)
			if len(got) != 1 || !got[0].FulfilledAt.IsZero() {
				t.Fatalf("replayed send fulfilled its own promise: %+v", got)
			}
		})
	}
}

func TestOutboundPromiseIgnoresNonPromisesAndPrivateBotChat(t *testing.T) {
	for _, text := range []string{"The results are ready.", "> 算完再同步", "```\n算完再同步\n```", "I will not share results later.", "不会同步"} { // i18n-ignore: message fixtures
		st := &memStore{}
		s, _, r, _ := outwardBot(t, st)
		sendOutwardBot(t, s, r, text)
		if got := outwardReceipts(t, st); len(got) != 0 {
			t.Fatalf("not a promise %q: %+v", text, got)
		}
	}
	st := &memStore{}
	s, _, r, _ := outwardBot(t, st)
	r.Ext = map[string]string{}
	sendOutwardBot(t, s, r, "I'll share the results later.")
	if got := outwardReceipts(t, st); len(got) != 0 {
		t.Fatalf("owner-only chat created an outward obligation: %+v", got)
	}
}

func TestOutboundPromiseRequiresConfirmedDelivery(t *testing.T) {
	st := &memStore{}
	s, f, r, posts := outwardBot(t, st)
	f.answerApp(AppFeishu, FeishuPost, func(json.RawMessage) (any, error) {
		return nil, errors.New("provider rejected the send")
	})
	_ = (feishuSink{s}).answer(context.Background(), r, "I'll share the results later.", "", time.Second, true, false, channel.EndDone)
	for _, e := range outwardReceipts(t, st) {
		if !e.SentAt.IsZero() {
			t.Fatalf("failed send was counted as delivered: %+v", e)
		}
	}
	st.mu.Lock()
	st.failPut = errors.New("storage unavailable")
	st.mu.Unlock()
	r.ID = "blocked-intent"
	if err := (feishuSink{s}).answer(context.Background(), r, "I'll share the results later.", "", time.Second, true, false, channel.EndDone); err == nil {
		t.Fatal("a promise was sent without a durable intent")
	}
	if posts.Load() != 0 {
		t.Fatal("a failed intent write still sent the message")
	}
}

func TestOutboundPromiseEscalationAndExactTopicFulfillment(t *testing.T) {
	ctx := context.Background()
	st := &memStore{}
	s, _, r, posts := outwardBot(t, st)
	sendOutwardBot(t, s, r, "I'll share the results later.")
	receipts := outwardReceipts(t, st)
	if len(receipts) != 1 {
		t.Fatalf("missing sent promise: %+v", receipts)
	}
	e := receipts[0]
	s.superviseWatch(ctx, e.DueAt.Add(time.Second))
	s.supervise.mu.Lock()
	signalled := false
	for _, g := range s.supervise.pending {
		signalled = signalled || g.Todo == e.Reply.TodoID && strings.Contains(g.Detail, e.Reply.Text) &&
			strings.Contains(g.Detail, e.Reply.URL)
	}
	s.supervise.mu.Unlock()
	if !signalled {
		t.Fatal("expired promise did not signal its supervisor with the original source and wording")
	}
	if task := todoNow(s, e.Reply.TodoID); task.Supervision.State == reviewNeedsUser {
		t.Fatal("first expiry skipped supervisor handling and asked the owner immediately")
	}
	s.superviseWatch(ctx, e.DueAt.Add(24*time.Hour))
	task := todoNow(s, e.Reply.TodoID)
	if task.Supervision.State != reviewNeedsUser || len(task.Supervision.Reviews) == 0 {
		t.Fatalf("second expiry did not enter Needs you: %+v", task.Supervision)
	}
	review := task.Supervision.Reviews[len(task.Supervision.Reviews)-1]
	if review.Recommend == "" || len(review.Options) != 2 || !strings.Contains(review.Reason, e.Reply.URL) {
		t.Fatalf("owner request is not actionable/source-linked: %+v", review)
	}
	if posts.Load() != 1 {
		t.Fatal("expiry sent an unapproved external reminder")
	}
	if closed, err := s.autoDoneTodo(ctx, task.ID, "Work stopped", time.Time{}); closed || err == nil {
		t.Fatal("automatic completion silently retired an unfulfilled outward promise")
	}
	if closed, err := s.reviewDoneTodo(ctx, task.ID, "Accepted", true); closed || err == nil {
		t.Fatal("supervisor acceptance silently retired an unfulfilled outward promise")
	}
	if ask, ok := s.askCopy(task.ID); !ok || ask.Recommend == "" || len(ask.Options) != 2 {
		t.Fatalf("Needs you has no answerable decision: %+v, exists=%v", ask, ok)
	}
	other := *r
	other.ID = "other-topic"
	other.Ext = map[string]string{"chat": "oc_example", "thread": "omt_other"}
	sendOutwardBot(t, s, &other, "Here are the results.")
	if got := outwardReceipts(t, st)[0]; !got.FulfilledAt.IsZero() {
		t.Fatal("an unrelated topic fulfilled the promise")
	}
	update := *r
	update.ID, update.ReplyTo = "follow-up", "om_new_message_in_same_topic"
	sendOutwardBot(t, s, &update, "Here are the results.")
	if got := outwardReceipts(t, st)[0]; got.FulfilledAt.IsZero() {
		t.Fatal("confirmed update in the same topic did not fulfill the promise")
	}
	s.superviseWatch(ctx, e.DueAt.Add(25*time.Hour))
	if task := todoNow(s, e.Reply.TodoID); task.Supervision.State == reviewNeedsUser {
		t.Fatal("fulfilled promise left its owner request active")
	}
	if task := todoNow(s, e.Reply.TodoID); !strings.Contains(task.Notes, e.FulfilledAt.Format(time.RFC3339)) &&
		!strings.Contains(task.Notes, "outward-fulfilled") {
		t.Fatal("fulfilled promise did not update the readable task record")
	}
	if _, holds := s.digestEntry(ctx, superviseSignal{Todo: e.Reply.TodoID, Kind: sigReport, Report: upReportBlocked,
		Key: "promise:" + e.Reply.ID}, ""); holds {
		t.Fatal("fulfilled promise left an obsolete supervisor signal deliverable")
	}
}

func TestOutboundPromiseSurvivesManagerReconstruction(t *testing.T) {
	ctx := context.Background()
	database := taskRecoveryDatabase(t)
	s, _, r, _ := outwardBot(t, database)
	sendOutwardBot(t, s, r, "I'll share the results later.")
	before := outwardReceipts(t, database)
	if len(before) != 1 || before[0].Reply.TodoID == "" {
		t.Fatalf("no durable promise/task: %+v", before)
	}
	restarted, _, update, _ := outwardBot(t, database)
	if err := restarted.ensureTodosLoaded(ctx); err != nil {
		t.Fatal(err)
	}
	restarted.superviseWatch(ctx, before[0].DueAt.Add(24*time.Hour))
	task := todoNow(restarted, before[0].Reply.TodoID)
	if task.Supervision.State != reviewNeedsUser || !strings.Contains(task.Notes, before[0].Reply.Text) {
		t.Fatalf("restart lost the obligation or its escalation: %+v", task)
	}
	update.ID = "after-reconstruction"
	sendOutwardBot(t, restarted, update, "Here are the results.")
	if got := outwardReceipts(t, database)[0]; got.FulfilledAt.IsZero() {
		t.Fatal("reconstructed manager did not fulfill the stored promise")
	}
}

func TestOutboundPromiseGitHubInboxConfirmation(t *testing.T) {
	for _, mode := range []string{"comment", "confirm", "handoff confirm"} {
		t.Run(mode, func(t *testing.T) { testOutwardInboxSend(t, mode) })
	}
}

func testOutwardInboxSend(t *testing.T, mode string) {
	t.Helper()
	ctx := context.Background()
	h := newInboxHub(t)
	now := time.Now()
	c := &replyRacingConnector{inboxConnector: inboxConnector{things: []connector.InboxThing{thing("a", now.Add(-time.Minute))}}}
	h.addConnector(c, false)
	h.collectInbox(ctx, roundPoll)
	it := openItem(t, h)
	task, err := h.createTodo(ctx, "Example linked task", "", "conv1", "")
	if err != nil {
		t.Fatal(err)
	}
	h.todos.mu.Lock()
	h.todos.todos[task.ID].Thread = &TodoThread{Channel: channel.Feishu, ChatID: "oc_work", ThreadID: "omt_work"}
	h.todos.mu.Unlock()
	h.inbox.mu.Lock()
	it.Connector.URL = "https://github.com/example/project/issues/17"
	it.Agent = &InboxAgent{TodoID: task.ID}
	h.inbox.mu.Unlock()
	text := "I'll share the results within 2 hours."
	g := &InboxSuggestion{Kind: SuggestReply, Version: 1, Text: text, At: now}
	switch mode {
	case "comment":
		_, err = h.replyInboxAs(ctx, it.ID, text, "")
	case "confirm":
		_, err = h.confirmReply(ctx, it, g, text, false, it.Last, "")
	case "handoff confirm":
		_, err = h.confirmHandedReply(ctx, it, g, text, false, "", SessionRef{SessionID: "conv1"}, "Example thread")
	}
	if err != nil {
		t.Fatal(err)
	}
	got := outwardReceipts(t, h.store())
	if len(got) != 1 || got[0].Reply.URL != it.Connector.URL || got[0].Reply.Text != text || got[0].Reply.TodoID != task.ID {
		t.Fatalf("successful connector comment omitted its task commitment: %+v", got)
	}
	if len(c.replies) != 1 {
		t.Fatalf("comment was sent %d times", len(c.replies))
	}
}

func TestOutboundPromiseOriginalSourceIsNotTheWorkTopic(t *testing.T) {
	ctx := context.Background()
	st := &memStore{}
	s, f, run, _ := outwardBot(t, st)
	task, err := s.createTodo(ctx, "Example linked task", "", "conv1", "")
	if err != nil {
		t.Fatal(err)
	}
	s.todos.mu.Lock()
	s.todos.todos[task.ID].Thread = &TodoThread{Channel: channel.Feishu, ChatID: "oc_work", ThreadID: "omt_work"}
	s.todos.mu.Unlock()
	var src TodoSource
	if err := json.Unmarshal([]byte(`{"channel":"feishu","chat_id":"oc_original","thread_id":"omt_original","message_id":"om_original","report_text":"I'll share the results later."}`), &src); err != nil {
		t.Fatal(err)
	}
	f.answerApp(AppFeishu, FeishuText, func(json.RawMessage) (any, error) { return map[string]any{}, nil })
	if err := s.sendSourceReport(ctx, src, task.ID); err != nil {
		t.Fatal(err)
	}
	got := outwardReceipts(t, st)
	if len(got) != 1 || !strings.Contains(got[0].Reply.URL, "open_thread_id=omt_original") ||
		strings.Contains(got[0].Reply.URL, "omt_work") {
		t.Fatalf("source reply was attributed to the task's separate work topic: %+v", got)
	}
	run.ID, run.Ext = "work-topic-update", map[string]string{"chat": "oc_work", "thread": "omt_work"}
	sendOutwardBot(t, s, run, "Here are the results.")
	if !outwardReceipts(t, st)[0].FulfilledAt.IsZero() {
		t.Fatal("update to the internal work topic fulfilled the outward promise")
	}
	run.ID, run.Ext = "source-topic-update", map[string]string{"chat": "oc_original", "thread": "omt_original"}
	sendOutwardBot(t, s, run, "Here are the results.")
	if outwardReceipts(t, st)[0].FulfilledAt.IsZero() {
		t.Fatal("update to the original source topic did not fulfill the promise")
	}
}

func TestOutboundPromiseStatedAbsoluteDeadline(t *testing.T) {
	st := &memStore{}
	s, _, r, _ := outwardBot(t, st)
	sendOutwardBot(t, s, r, "I'll share the results at 2030-01-02T13:45:00+08:00.")
	got := outwardReceipts(t, st)
	want, _ := time.Parse(time.RFC3339, "2030-01-02T05:45:00Z")
	if len(got) != 1 || !got[0].DueAt.Equal(want) {
		t.Fatalf("message's explicit deadline was not preserved: %+v", got)
	}
}

func TestOutboundPromiseExplicitGitHubReceipt(t *testing.T) {
	ctx := context.Background()
	st := &memStore{}
	s, f := newQuietServiceOn(t, st)
	holdDigest(s)
	task, err := s.createTodo(ctx, "Example follow-up", "", "conv1", "")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	due := at.Add(3 * time.Hour)
	topic := "https://github.com/example/project/issues/17"
	receipt := map[string]any{"topic_url": topic, "sent_url": topic + "#issuecomment-101",
		"text": "More details will follow.", "sent_at": at, "due_at": due, "promise": true}
	record := func() error {
		raw, _ := json.Marshal(map[string]any{"id": task.ID, "outward_reply": receipt})
		_, err := s.toolTodoUpdate(ctx, "server", "conv1", raw)
		return err
	}
	if err := record(); err != nil {
		t.Fatal(err)
	}
	got := outwardReceipts(t, st)
	if len(got) != 1 || got[0].Reply.TodoID != task.ID || !got[0].DueAt.Equal(due) {
		t.Fatalf("explicit sender receipt did not attach its promise/deadline to the existing task: %+v", got)
	}
	if err := record(); err != nil {
		t.Fatal(err)
	}
	if got := outwardReceipts(t, st); len(got) != 1 || !got[0].FulfilledAt.IsZero() {
		t.Fatalf("receipt retry fulfilled or duplicated itself: %+v", got)
	}
	receipt["sent_url"] = "https://github.com/example/project/issues/18#issuecomment-102"
	if err := record(); err == nil {
		t.Fatal("accepted a sent receipt from a different topic")
	}
	receipt["sent_url"], receipt["text"], receipt["sent_at"], receipt["promise"] =
		topic+"#issuecomment-103", "Here are the details.", at.Add(time.Second), false
	if err := record(); err != nil {
		t.Fatal(err)
	}
	if got := outwardReceipts(t, st); got[0].FulfilledAt.IsZero() {
		t.Fatal("later sender receipt in the exact GitHub topic did not fulfill the promise")
	}
	if len(f.notices()) != 0 {
		t.Fatal("recording an already-sent receipt sent another outward message")
	}
}

type outwardReceiptFailStore struct {
	Store
	writes int
}

func (st *outwardReceiptFailStore) PutSetting(ctx context.Context, key string, value any) error {
	if key == "assistant.outbound_commitments" {
		st.writes++
		if st.writes == 2 {
			return errors.New("receipt write unavailable")
		}
	}
	return st.Store.PutSetting(ctx, key, value)
}

func TestOutboundPromiseRetriesOnlyBookkeepingAfterSentReceiptFailure(t *testing.T) {
	ctx := context.Background()
	st := &outwardReceiptFailStore{Store: &memStore{}}
	s, _, run, posts := outwardBot(t, st)
	sendOutwardBot(t, s, run, "I'll share the results later.")
	initial := outwardReceipts(t, st)
	if len(initial) != 1 || !initial[0].SentAt.IsZero() || posts.Load() != 1 {
		t.Fatalf("fixture did not fail after exactly one real send: %+v, posts=%d", initial, posts.Load())
	}
	s.superviseWatch(ctx, time.Now())
	got := outwardReceipts(t, st)
	if len(got) != 1 || got[0].SentAt.IsZero() || got[0].Reply.TodoID == "" || posts.Load() != 1 {
		t.Fatalf("receipt retry lost the promise or resent the message: %+v, posts=%d", got, posts.Load())
	}
}

func TestOutboundPromiseUncertainReceiptAfterRestartRequiresSourceReadback(t *testing.T) {
	ctx := context.Background()
	st := &outwardReceiptFailStore{Store: &memStore{}}
	s, _, run, _ := outwardBot(t, st)
	sendOutwardBot(t, s, run, "I'll share the results later.")
	restarted, _, _, posts := outwardBot(t, st)
	restarted.superviseWatch(ctx, time.Now().Add(25*time.Hour))
	got := outwardReceipts(t, st)
	if len(got) != 1 || !got[0].SentAt.IsZero() || got[0].Reply.TodoID == "" || posts.Load() != 0 {
		t.Fatalf("restart inferred delivery or resent an uncertain message: %+v, posts=%d", got, posts.Load())
	}
	task := todoNow(restarted, got[0].Reply.TodoID)
	if !strings.Contains(task.Notes, got[0].Reply.URL) || !strings.Contains(task.Notes, got[0].Reply.Text) {
		t.Fatal("restart silently lost the original source and attempted wording")
	}
}

func TestOutboundPromiseOldInboxReceiptCannotFulfillANewerPromise(t *testing.T) {
	ctx := context.Background()
	h := newInboxHub(t)
	now := time.Now()
	c := &replyRacingConnector{inboxConnector: inboxConnector{things: []connector.InboxThing{thing("a", now.Add(-time.Minute))}}}
	h.addConnector(c, false)
	h.collectInbox(ctx, roundPoll)
	it := openItem(t, h)
	first := "I'll share the results later."
	h.in.mu.Lock()
	h.in.inDoubt = true
	h.in.mu.Unlock()
	if _, err := h.replyInboxDo(ctx, it.ID, first, "first-receipt", false, false); err == nil {
		t.Fatal("fixture did not lose the first send's confirmation")
	}
	second := "I'll post an update within 3 hours."
	if _, err := h.replyInboxDo(ctx, it.ID, second, "second-receipt", false, false); err != nil {
		t.Fatal(err)
	}
	if _, err := h.replyInboxDo(ctx, it.ID, first, "first-receipt", false, false); err != nil {
		t.Fatal(err)
	}
	got := outwardReceipts(t, h.store())
	found := false
	for _, e := range got {
		if e.Reply.Text == second {
			found = true
			if !e.FulfilledAt.IsZero() {
				t.Fatal("readback of the older send fulfilled the newer promise")
			}
		}
	}
	if !found || len(c.replies) != 2 {
		t.Fatalf("receipt replay lost the newer promise or sent again: %+v, sends=%d", got, len(c.replies))
	}
}

func TestOutboundPromisePlainBotTopicReply(t *testing.T) {
	ctx := context.Background()
	st := &memStore{}
	s, f, _, _ := outwardBot(t, st)
	var sends int
	f.answerApp(AppFeishu, FeishuText, func(raw json.RawMessage) (any, error) {
		var p struct {
			NoDetour bool `json:"no_detour"`
		}
		_ = json.Unmarshal(raw, &p)
		if !p.NoDetour {
			t.Error("a tracked topic reply could silently detour into the group")
		}
		sends++
		return map[string]any{"message_id": "om_plain"}, nil
	})
	to := (&feishuTopic{Chat: "oc_original", Thread: "omt_original"}).target()
	s.botNoteTo(ctx, to, "om_request", "算完再同步") // i18n-ignore: sent-message fixture
	got := outwardReceipts(t, st)
	if len(got) != 1 || got[0].SentAt.IsZero() || got[0].Reply.TodoID == "" {
		t.Fatalf("plain bot reply omitted its task commitment: %+v", got)
	}
	s.botNoteTo(ctx, to, "om_request", "The results are ready.")
	if outwardReceipts(t, st)[0].FulfilledAt.IsZero() || sends != 2 {
		t.Fatal("plain bot follow-up did not fulfill its topic's promise exactly once")
	}
}

func TestOutboundPromiseDoesNotCreditAReplyThatWouldDetour(t *testing.T) {
	ctx := context.Background()
	st := &memStore{}
	s, f, run, _ := outwardBot(t, st)
	sendOutwardBot(t, s, run, "I'll share the results later.")
	f.answerApp(AppFeishu, FeishuPost, func(raw json.RawMessage) (any, error) {
		var p struct {
			NoDetour bool `json:"no_detour"`
		}
		_ = json.Unmarshal(raw, &p)
		if !p.NoDetour {
			t.Error("tracked update allowed delivery outside its original topic")
			return map[string]any{"message_id": "om_wrong_place"}, nil
		}
		return nil, errors.New("original parent is gone")
	})
	run.ID = "update-with-deleted-parent"
	_ = (feishuSink{s}).answer(ctx, run, "Here are the results.", "", time.Second, true, false, channel.EndDone)
	if !outwardReceipts(t, st)[0].FulfilledAt.IsZero() {
		t.Fatal("an update that could not land in the original topic fulfilled the promise")
	}
}
