package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/db"
)

type outwardLostUpdateReceiptStore struct {
	*db.DB
	text        string
	armed       bool
	dropped     int
	deliveredAt time.Time
}

func (st *outwardLostUpdateReceiptStore) PutSetting(ctx context.Context, key string, value any) error {
	if key == "assistant.outbound_commitments" && st.armed {
		raw, _ := json.Marshal(value)
		var entries []outwardReceipt
		_ = json.Unmarshal(raw, &entries)
		for _, e := range entries {
			if e.Reply.Text == st.text && !e.SentAt.IsZero() {
				st.armed = false
				st.dropped++
				st.deliveredAt = e.SentAt
				return errors.New("synthetic update receipt write failure")
			}
		}
	}
	return st.DB.PutSetting(ctx, key, value)
}

func outwardReplayReviewHub(t *testing.T, store Store, in *fakeInbox) *inboxHub {
	t.Helper()
	s, f := newQuietServiceOn(t, store)
	holdDigest(s)
	if in == nil {
		in = newFakeInbox()
	}
	f.inbox, in.svc = in, s
	h := &inboxHub{Service: s, f: f, in: in}
	h.inbox.init()
	return h
}

func TestOutboundPromiseReceiptWriteFailureCannotFulfillNewerPromise(t *testing.T) {
	for _, restart := range []bool{false, true} {
		name := "retained manager"
		if restart {
			name = "reconstructed manager"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			st := &outwardLostUpdateReceiptStore{DB: taskRecoveryDatabase(t), text: "The earlier results are available."}
			h := outwardReplayReviewHub(t, st, nil)
			c := &replyRacingConnector{inboxConnector: inboxConnector{things: []connector.InboxThing{thing("a", time.Now())}}}
			h.addConnector(c, false)
			h.collectInbox(ctx, roundPoll)
			it := openItem(t, h)
			send := func(text, id string) {
				t.Helper()
				if _, err := h.replyInboxDo(ctx, it.ID, text, id, false, false); err != nil {
					t.Fatal(err)
				}
			}
			send("I'll share the results later.", "original-promise")
			st.armed = true
			send(st.text, "old-update")
			if st.dropped != 1 {
				t.Fatal("fixture did not lose exactly the old update's receipt")
			}
			entries := outwardReceipts(t, st)
			if len(entries) != 2 || !entries[1].SentAt.IsZero() || entries[1].Promise {
				t.Fatalf("failed receipt did not leave a persisted non-promise intent: %+v", entries)
			}
			const newer = "I'll post an update within 3 hours."
			send(newer, "newer-promise")
			before := outwardReceipts(t, st)
			if len(before) != 3 || !before[2].FulfilledAt.IsZero() || !st.deliveredAt.Before(before[2].SentAt) {
				t.Fatalf("fixture did not establish old delivery before the newer promise: %+v", before)
			}
			if restart {
				h = outwardReplayReviewHub(t, st, h.in)
				if err := h.inboxReady(ctx); err != nil {
					t.Fatal(err)
				}
			}
			// The newer reply replaced the last-text local duplicate guard, so
			// retrying the old ID reaches the provider's Already readback.
			send(st.text, "old-update")
			got := outwardReceipts(t, st)
			if len(c.replies) != 3 || len(got) != 3 || got[1].SentAt.IsZero() || !got[2].FulfilledAt.IsZero() {
				t.Fatalf("old provider readback fulfilled a newer promise or resent: sends=%d entries=%+v", len(c.replies), got)
			}
			if !restart && !got[1].SentAt.Equal(st.deliveredAt) {
				t.Fatal("known successful delivery was redated during bookkeeping retry")
			}
			send("Here are the current results.", "fresh-update")
			got = outwardReceipts(t, st)
			if len(c.replies) != 4 || got[2].FulfilledAt.IsZero() {
				t.Fatal("a genuinely new provider send did not fulfill the newer promise")
			}
		})
	}
}

func TestOutboundPromiseUntrackedOldProviderReplayCannotFulfillNewerPromise(t *testing.T) {
	ctx := context.Background()
	h := outwardReplayReviewHub(t, newInboxMemStore(), nil)
	c := &replyRacingConnector{inboxConnector: inboxConnector{things: []connector.InboxThing{thing("a", time.Now())}}}
	h.addConnector(c, false)
	h.collectInbox(ctx, roundPoll)
	it := openItem(t, h)
	const oldText = "The earlier results are available."
	// The provider remembers a successful update from before tracking started.
	// Its first Persona intent is created only when it is retried later.
	sendID := replySendID(it.ID, oldText, "old-untracked-update", nil)
	if _, err := h.in.sendOnce(sendID, func() error {
		return c.Reply(ctx, connector.InboxReply{Thread: it.Connector.Thread, Text: oldText})
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.replyInboxDo(ctx, it.ID, "I'll share the results later.", "new-promise", false, false); err != nil {
		t.Fatal(err)
	}
	if _, err := h.replyInboxDo(ctx, it.ID, oldText, "old-untracked-update", false, false); err != nil {
		t.Fatal(err)
	}
	got := outwardReceipts(t, h.store())
	if len(c.replies) != 2 || len(got) != 2 || !got[0].FulfilledAt.IsZero() || got[1].SentAt.IsZero() {
		t.Fatalf("old provider readback with a later intent fulfilled the new promise: sends=%d entries=%+v", len(c.replies), got)
	}
}
