package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/channel"
)

func outwardMigratedInbox(t *testing.T) (*inboxHub, *InboxItem) {
	t.Helper()
	return outwardMigratedInboxOn(t, newInboxMemStore())
}

func outwardMigratedInboxOn(t *testing.T, store Store) (*inboxHub, *InboxItem) {
	t.Helper()
	s, f := newQuietServiceOn(t, store)
	in := newFakeInbox()
	f.inbox, in.svc = in, s
	h := &inboxHub{Service: s, f: f, in: in}
	holdDigest(h.Service)
	if err := h.inboxReady(context.Background()); err != nil {
		t.Fatal(err)
	}
	h.addConnector(&extensionLark{}, false)
	it := &InboxItem{ID: "legacy-example-1", Source: "lark", Account: "feishu", Kind: KindGroup,
		Title: "Example question", Tier: TierAction, Last: "om_request", UpdatedAt: time.Now(),
		Lark: &InboxLark{AccountID: 1, FeedID: "oc_example", ReplyTo: "om_request", IsGroup: true, ThreadKind: "topic"}}
	h.inbox.mu.Lock()
	h.inbox.items[it.ID] = it
	h.inbox.mu.Unlock()
	h.f.answerApp(AppFeishu, FeishuMessages, func(p json.RawMessage) (any, error) {
		var q struct {
			MessageID string `json:"message_id"`
		}
		_ = json.Unmarshal(p, &q)
		return map[string]any{"items": []map[string]any{{"message_id": q.MessageID,
			"chat_id": "oc_example", "thread_id": "omt_example"}}}, nil
	})
	for _, method := range []string{FeishuPost, FeishuReact, FeishuText} {
		h.f.answerApp(AppFeishu, method, func(json.RawMessage) (any, error) {
			return map[string]any{"message_id": "om_sent"}, nil
		})
	}
	return h, it
}

func TestOutboundPromiseMigratedFeishuCrossPathFulfillment(t *testing.T) {
	for _, path := range []string{"bot answer", "bot note", "source report", "legacy source report", "reverse Inbox reply"} {
		t.Run(path, func(t *testing.T) {
			h, it := outwardMigratedInbox(t)
			ctx := context.Background()
			r := &channel.Run{ID: "native-example", Channel: channel.Feishu, ReplyTo: "om_request",
				Ext: map[string]string{"chat": "oc_example", "thread": "omt_example"}}
			if path == "reverse Inbox reply" {
				sendOutwardBot(t, h.Service, r, "I'll share the results later.")
			} else if _, err := h.replyInboxDo(ctx, it.ID, "I'll share the results later.", "legacy-promise", false, false); err != nil {
				t.Fatal(err)
			}
			receipts := outwardReceipts(t, h.store())
			if len(receipts) != 1 || !receipts[0].Promise || receipts[0].SentAt.IsZero() {
				t.Fatalf("missing migrated promise: %+v", receipts)
			}
			if path != "reverse Inbox reply" && receipts[0].Reply.URL != "/inbox/"+it.ID {
				t.Fatalf("source view was replaced with a guessed provider link: %s", receipts[0].Reply.URL)
			}
			switch path {
			case "bot answer":
				sendOutwardBot(t, h.Service, r, "Here are the results.")
			case "bot note":
				h.botNoteTo(ctx, feishuTarget{ChatID: "oc_example", ThreadID: "omt_example", InThread: true},
					"om_request", "Here are the results.")
			case "source report", "legacy source report":
				src := TodoSource{Channel: channel.Feishu, ChatID: "oc_example", MessageID: "om_request",
					ThreadID: "omt_example", ReportText: "Here are the results."}
				if path == "legacy source report" {
					src.ThreadID = ""
				}
				if err := h.sendSourceReport(ctx, src, receipts[0].Reply.TodoID); err != nil {
					t.Fatal(err)
				}
			case "reverse Inbox reply":
				if _, err := h.replyInboxDo(ctx, it.ID, "Here are the results.", "legacy-update", false, false); err != nil {
					t.Fatal(err)
				}
			}
			got := outwardReceipts(t, h.store())
			if len(got) != 2 || got[0].FulfilledAt.IsZero() || got[1].Reply.Topic != got[0].Reply.Topic {
				t.Fatalf("%s did not fulfill the promise in its original Feishu topic: %+v", path, got)
			}
		})
	}
}

func TestOutboundPromiseMigratedFeishuIdentityBoundaries(t *testing.T) {
	for _, boundary := range []string{"other chat", "other thread", "readback failure", "wrong message", "wrong chat", "deleted message", "no topic", "failed update"} {
		t.Run(boundary, func(t *testing.T) {
			h, it := outwardMigratedInbox(t)
			if boundary == "readback failure" {
				h.f.answerApp(AppFeishu, FeishuMessages, func(json.RawMessage) (any, error) {
					return nil, errors.New("synthetic source lookup failure")
				})
			} else if strings.HasPrefix(boundary, "wrong") || boundary == "deleted message" || boundary == "no topic" {
				message, chat, thread := "om_request", "oc_example", "omt_example"
				switch boundary {
				case "wrong message":
					message = "om_other"
				case "wrong chat":
					chat = "oc_other"
				case "no topic":
					thread = ""
				}
				h.f.answerApp(AppFeishu, FeishuMessages, func(json.RawMessage) (any, error) {
					return map[string]any{"items": []map[string]any{{"message_id": message, "chat_id": chat,
						"thread_id": thread, "deleted": boundary == "deleted message"}}}, nil
				})
			}
			if _, err := h.replyInboxDo(context.Background(), it.ID, "I'll share the results later.", "promise", false, false); err != nil {
				t.Fatal(err)
			}
			r := &channel.Run{ID: "other-update", Channel: channel.Feishu, ReplyTo: "om_request",
				Ext: map[string]string{"chat": "oc_example", "thread": "omt_example"}}
			switch boundary {
			case "other chat":
				r.Ext["chat"] = "oc_other"
			case "other thread":
				r.Ext["thread"] = "omt_other"
			case "failed update":
				for _, method := range []string{FeishuPost, FeishuText} {
					h.f.answerApp(AppFeishu, method, func(json.RawMessage) (any, error) {
						return nil, errors.New("synthetic send failure")
					})
				}
			}
			err := (feishuSink{h.Service}).answer(context.Background(), r, "Here are the results.", "", time.Second, true, false, channel.EndDone)
			if boundary != "failed update" && err != nil {
				t.Fatal(err)
			}
			if got := outwardReceipts(t, h.store()); !got[0].FulfilledAt.IsZero() {
				t.Fatalf("unverified or unrelated %s fulfilled the promise: %+v", boundary, got)
			}
		})
	}
}

func TestOutboundPromiseMigratedFeishuFallbackUsesSourceIdentity(t *testing.T) {
	for _, scope := range []string{"same message source report", "opaque source report", "same conversation new Inbox item", "another account", "another topic anchor", "missing topic anchor"} {
		t.Run(scope, func(t *testing.T) {
			h, it := outwardMigratedInbox(t)
			h.f.answerApp(AppFeishu, FeishuMessages, func(json.RawMessage) (any, error) {
				return nil, errors.New("source cannot be read by the bot")
			})
			if scope == "same conversation new Inbox item" || scope == "another account" {
				it.Lark.ThreadKind = ""
				it.Lark.ReplyTo = ""
			} else if scope == "opaque source report" {
				it.Lark.FeedID, it.Lark.ReplyTo = "feed-example", "message-example"
			} else if scope == "missing topic anchor" {
				it.Lark.ReplyTo = ""
			}
			if _, err := h.replyInboxDo(context.Background(), it.ID, "I'll share the results later.", "promise", false, false); err != nil {
				t.Fatal(err)
			}
			before := outwardReceipts(t, h.store())
			if scope == "same message source report" || scope == "opaque source report" {
				if err := h.sendSourceReport(context.Background(), TodoSource{Channel: channel.Feishu, ChatID: it.Lark.FeedID,
					MessageID: it.Lark.ReplyTo, ReportText: "Here are the results."}, before[0].Reply.TodoID); err != nil {
					t.Fatal(err)
				}
			} else {
				next, lark := *it, *it.Lark
				next.ID, next.DoneAt, next.DoneBy, next.Lark = "legacy-example-2", nil, "", &lark
				next.Last = "om_new"
				switch scope {
				case "another account":
					lark.AccountID = 2
				case "another topic anchor":
					lark.ReplyTo = "om_other"
				}
				h.inbox.mu.Lock()
				h.inbox.items[next.ID] = &next
				h.inbox.mu.Unlock()
				if _, err := h.replyInboxDo(context.Background(), next.ID, "Here are the results.", "update", false, false); err != nil {
					t.Fatal(err)
				}
			}
			fulfilled := !outwardReceipts(t, h.store())[0].FulfilledAt.IsZero()
			want := scope == "same message source report" || scope == "opaque source report" || scope == "same conversation new Inbox item"
			if fulfilled != want {
				t.Fatalf("%s: fulfilled=%v, want %v", scope, fulfilled, want)
			}
		})
	}
}

func TestOutboundPromiseMigratedFeishuIdentitySurvivesRestart(t *testing.T) {
	database := taskRecoveryDatabase(t)
	h, it := outwardMigratedInboxOn(t, database)
	if _, err := h.replyInboxDo(context.Background(), it.ID, "I'll share the results later.", "persisted-promise", false, false); err != nil {
		t.Fatal(err)
	}
	before := outwardReceipts(t, database)
	s, _, r, posts := outwardBot(t, database)
	sendOutwardBot(t, s, r, "Here are the results.")
	got := outwardReceipts(t, database)
	if len(got) != 2 || got[0].FulfilledAt.IsZero() || posts.Load() != 1 ||
		got[0].Reply.TodoID != before[0].Reply.TodoID || got[0].Reply.URL != "/inbox/"+it.ID {
		t.Fatalf("restart lost the migrated promise identity or original source: %+v", got)
	}
}

func TestOutboundPromiseUnreadableMigratedDistinctAnchors(t *testing.T) {
	for _, account := range []int{0, 1, 2} {
		for _, kind := range []string{"", LarkThreadTopic, LarkThreadThread} {
			t.Run(fmt.Sprintf("account-%d/%s", account, kind), func(t *testing.T) {
				h, it := outwardMigratedInbox(t)
				h.f.answerApp(AppFeishu, FeishuMessages, func(json.RawMessage) (any, error) {
					return nil, errors.New("synthetic unreadable source")
				})
				h.inbox.mu.Lock()
				it.Lark.AccountID, it.Lark.ThreadKind = account, kind
				h.inbox.mu.Unlock()
				ctx := context.Background()
				if _, err := h.replyInboxDo(ctx, it.ID, "I'll share the results later.", "promise", false, false); err != nil {
					t.Fatal(err)
				}
				next, lark := *it, *it.Lark
				next.ID, next.DoneAt, next.DoneBy, next.Lark = "other-anchor", nil, "", &lark
				lark.ReplyTo = "om_other"
				h.inbox.mu.Lock()
				h.inbox.items[next.ID] = &next
				h.inbox.mu.Unlock()
				if _, err := h.replyInboxDo(ctx, next.ID, "Here are the other results.", "other-update", false, false); err != nil {
					t.Fatal(err)
				}
				if got := outwardReceipts(t, h.store()); !got[0].FulfilledAt.IsZero() {
					t.Fatal("an unreadable different anchor in the same chat fulfilled the promise")
				}
				same, anchor := next, *it.Lark
				same.ID, same.DoneAt, same.DoneBy, same.Lark = "original-anchor", nil, "", &anchor
				h.inbox.mu.Lock()
				h.inbox.items[same.ID] = &same
				h.inbox.mu.Unlock()
				if _, err := h.replyInboxDo(ctx, same.ID, "Here are the original results.", "same-update", false, false); err != nil {
					t.Fatal(err)
				}
				if got := outwardReceipts(t, h.store()); got[0].FulfilledAt.IsZero() {
					t.Fatal("a confirmed update under the exact original anchor did not fulfill the promise")
				}
			})
		}
	}
}
