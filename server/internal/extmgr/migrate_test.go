package extmgr

import (
	"context"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/subscriptions"
)

// A built-in whose Inbox feed is switched to its default subscription is not read in the round; the subscription's
// rows come under its id instead, once (#6265). Switched back, the built-in is read again.
func TestAMigratedBuiltinIsReadThroughItsSubscription(t *testing.T) {
	h := newTestManager(t)
	ctx := context.Background()
	h.connectors = connector.NewRegistry(nil)
	c := &inboxConnector{things: []connector.InboxThing{{Thread: "chat-1", Kind: "review_requested", Title: "From the built-in", Last: "b1", At: time.Now()}}}
	_ = h.addConnector(c)
	store := subscriptions.NewMemStore()
	now := time.Now().UTC()
	at := now.Add(-time.Minute)
	sub := subscriptions.Subscription{ID: subscriptions.MigrationID(c.ID()), Connector: c.ID(), Source: "notices", Label: "Example",
		Recipe: subscriptions.Recipe{Mode: "items", Tool: "list", Kind: "sub_message", GroupBy: "thread"}, Migrated: &at,
		InboxCatchup: &now, Revision: 1, CreatedAt: now.Add(-time.Hour)}
	if err := store.CreateSubscription(ctx, sub); err != nil {
		t.Fatal(err)
	}
	items := []subscriptions.Item{{UID: "m1", Thread: "chat-1", Title: "From the subscription", Kind: "sub_message"}}
	if err := store.CommitRun(ctx, subscriptions.RunCommit{Sub: sub, Revision: 1, Items: items, Now: now.Add(-30 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	h.subs = subscriptions.New(h.subscriptionOptions(store))
	h.subsLive.Store(h.subs)

	ofExample := func(r assistantwire.InboxRound) []assistantwire.InboxSource {
		var out []assistantwire.InboxSource
		for _, s := range r.Sources {
			if s.ID == c.ID() {
				out = append(out, s)
			}
		}
		return out
	}
	collected := c.collect.Load()
	got := ofExample(h.readInbox(ctx, roundPoll))
	if len(got) != 1 || !got[0].Counts || len(got[0].Things) == 0 || got[0].Things[0].Thread != "chat-1" || got[0].Things[0].Last != "m1" {
		t.Fatalf("the migrated source in the round: %+v", got)
	}
	if c.collect.Load() != collected {
		t.Fatal("the built-in was read while its feed is the subscription")
	}
	if _, err := h.subs.Migrate(ctx, subscriptions.Migration{Connector: c.ID(), Source: "notices", Recipe: sub.Recipe}, false, subscriptions.Actor{}); err != nil {
		t.Fatal(err)
	}
	got = ofExample(h.readInbox(ctx, roundPoll))
	if len(got) != 1 || got[0].Counts || len(got[0].Things) != 1 || got[0].Things[0].Last != "b1" {
		t.Fatalf("the built-in after switching back: %+v", got)
	}
}
