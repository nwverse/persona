package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/channel"
)

func TestOutboundPromiseAutomaticBotNoteDoesNotCreatePromise(t *testing.T) {
	st := &memStore{}
	s, f, r, _ := outwardBot(t, st)
	notes := 0
	f.answerApp(AppFeishu, FeishuText, func(json.RawMessage) (any, error) {
		notes++
		return map[string]any{"message_id": "om_note"}, nil
	})
	s.botNoteTo(context.Background(), runTo(r.Ext), r.ReplyTo, "I'll share the results later.")
	if notes != 1 {
		t.Fatalf("automatic note was not delivered: %d", notes)
	}
	if got := outwardReceipts(t, st); len(got) != 0 {
		t.Fatalf("automatic note created a business promise: %+v", got)
	}
}

func TestOutboundPromiseAutomaticBotNoteDoesNotFulfill(t *testing.T) {
	for _, origin := range []string{"bot", "Inbox"} {
		t.Run(origin, func(t *testing.T) {
			h, it := outwardMigratedInbox(t)
			ctx := context.Background()
			r := &channel.Run{ID: "real-promise", Channel: channel.Feishu, ReplyTo: "om_request",
				Ext: map[string]string{"chat": "oc_example", "thread": "omt_example"}}
			if origin == "bot" {
				sendOutwardBot(t, h.Service, r, "I'll share the results later.")
			} else if _, err := h.replyInboxDo(ctx, it.ID, "I'll share the results later.", "promise", false, false); err != nil {
				t.Fatal(err)
			}
			promise := outwardReceipts(t, h.store())[0]
			expired := promise.DueAt.Add(24 * time.Hour)
			h.superviseWatch(ctx, expired)
			h.botNoteTo(ctx, runTo(r.Ext), r.ReplyTo, "The answer could not be sent. Open the app.")
			h.superviseWatch(ctx, expired.Add(time.Second))
			got := outwardReceipts(t, h.store())
			if len(got) != 1 || !got[0].FulfilledAt.IsZero() {
				t.Fatalf("automatic note fulfilled a real %s promise: %+v", origin, got)
			}
			if task := todoNow(h.Service, promise.Reply.TodoID); task.Supervision.State != reviewNeedsUser {
				t.Fatalf("automatic note cleared Needs you: %+v", task.Supervision)
			}
			if _, ok := h.askCopy(promise.Reply.TodoID); !ok {
				t.Fatal("automatic note removed the owner's decision")
			}
			// Real reply content still fulfills across the two sending paths.
			if origin == "bot" {
				if _, err := h.replyInboxDo(ctx, it.ID, "Here are the results.", "real-update", false, false); err != nil {
					t.Fatal(err)
				}
			} else {
				r.ID = "real-update"
				sendOutwardBot(t, h.Service, r, "Here are the results.")
			}
			if got := outwardReceipts(t, h.store()); got[0].FulfilledAt.IsZero() {
				t.Fatal("a real cross-path update no longer fulfills its promise")
			}
		})
	}
}

func TestOutboundPromiseMissingAnswerPiecesDoNotFulfill(t *testing.T) {
	for _, failure := range []string{"all", "partial"} {
		t.Run(failure, func(t *testing.T) {
			st := &memStore{}
			s, f, r, _ := outwardBot(t, st)
			ctx := context.Background()
			sendOutwardBot(t, s, r, "I'll share the results later.")
			promise := outwardReceipts(t, st)[0]
			expired := promise.DueAt.Add(24 * time.Hour)
			s.superviseWatch(ctx, expired)
			posts, notes := 0, 0
			f.answerApp(AppFeishu, FeishuPost, func(json.RawMessage) (any, error) {
				posts++
				if failure == "all" || posts == 2 {
					return nil, errors.New("feishu send: 99991400 rate limited")
				}
				return map[string]any{"message_id": "om_piece"}, nil
			})
			f.answerApp(AppFeishu, FeishuText, func(raw json.RawMessage) (any, error) {
				var p struct {
					ReplyTo  string `json:"reply_to"`
					NoDetour bool   `json:"no_detour"`
				}
				_ = json.Unmarshal(raw, &p)
				if p.ReplyTo != r.ReplyTo || !p.NoDetour {
					t.Errorf("missing-pieces note left the original topic: %+v", p)
				}
				notes++
				return map[string]any{"message_id": "om_missing_note"}, nil
			})
			r.ID = "incomplete-update"
			sendOutwardBot(t, s, r, strings.Repeat("A section of the result.\n\n", 1500))
			if posts < 3 || notes != 1 {
				t.Fatalf("fixture did not send answer pieces and its deferred warning: posts=%d notes=%d", posts, notes)
			}
			s.superviseWatch(ctx, expired.Add(time.Second))
			got := outwardReceipts(t, st)
			if !got[0].FulfilledAt.IsZero() {
				t.Fatalf("%s delivery failure fulfilled the promise via its warning: %+v", failure, got)
			}
			for _, e := range got[1:] {
				if !e.SentAt.IsZero() {
					t.Fatalf("incomplete answer or warning got a successful reply receipt: %+v", e)
				}
			}
			if task := todoNow(s, promise.Reply.TodoID); task.Supervision.State != reviewNeedsUser {
				t.Fatalf("missing-pieces warning cleared Needs you: %+v", task.Supervision)
			}
		})
	}
}
