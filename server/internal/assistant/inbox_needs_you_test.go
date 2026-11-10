package assistant

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
)

// needsYouTold are the counts the inbox told the hub (inbox.needs_you), in order.
func needsYouTold(t *testing.T, f *fakeHub) []int {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []int
	for _, e := range f.told {
		if e.event != assistantwire.EventInboxNeedsYou {
			continue
		}
		var p assistantwire.InboxNeedsYou
		if err := json.Unmarshal(e.params, &p); err != nil {
			t.Fatalf("%s: %v", e.params, err)
		}
		out = append(out, p.Count)
	}
	return out
}

// The hub is told how many open items are in "Needs you" (the roster's inbox_needs_you): the items the apps list
// under that section, the connectors' and the ones not triaged yet among them, and none of the others. It is told
// when what is stored is first read, after every change, and again when asked (a handshake); never before the read.
func TestInboxNeedsYouToldToTheHub(t *testing.T) {
	f := newFakeHub().withMachine()
	f.inbox = newFakeInbox()
	st := newInboxMemStore()
	s := New(Options{Hub: f, Store: func() Store { return st }, Logf: testLogf(t)})
	s.inbox.init()
	s.tellInboxNeedsYou()
	if got := needsYouTold(t, f); len(got) != 0 {
		t.Fatalf("told %v before what is stored was read", got)
	}
	if err := s.inboxReady(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := needsYouTold(t, f); len(got) != 1 || got[0] != 0 {
		t.Fatalf("an empty inbox read: told %v, want [0]", got)
	}

	done := time.Now()
	item := func(id string, change func(*InboxItem)) *InboxItem {
		it := mailItem(id, "shop@example.com", "Shop")
		change(it)
		return it
	}
	items := []*InboxItem{
		item("untriaged", func(*InboxItem) {}),                                                                         // counts: not triaged yet is "Needs you"
		item("action", func(it *InboxItem) { it.Tier = TierAction }),                                                   // counts
		item("connector", func(it *InboxItem) { it.Source, it.Mail, it.Connector = "github", nil, &InboxConnector{} }), // counts
		item("failed", func(it *InboxItem) { it.Tier, it.Handling = TierFYI, &InboxHandling{State: HandlingFailed} }),  // counts
		item("fyi", func(it *InboxItem) { it.Tier = TierFYI }),
		item("noise", func(it *InboxItem) { it.Tier = TierNoise }),
		item("working", func(it *InboxItem) { it.Tier, it.Handling = TierAction, &InboxHandling{State: HandlingWaiting} }),
		item("done", func(it *InboxItem) { it.Tier, it.DoneAt = TierAction, &done }),
	}
	s.inbox.mu.Lock()
	want := 0
	for _, it := range items {
		s.inbox.items[it.ID] = it
		if listed := it.listed(); listed.Status == StatusAction {
			want++ // what the apps count: open items the list puts under "Needs you"
		}
	}
	s.inbox.mu.Unlock()
	if want != 4 {
		t.Fatalf("the list has %d items under Needs you, want 4", want)
	}
	s.saveInbox(context.Background(), items)
	if got := needsYouTold(t, f); len(got) != 2 || got[1] != want {
		t.Fatalf("after a change: told %v, want [0 %d]", got, want)
	}

	if _, err := s.setInboxDone(context.Background(), "action", true, false); err != nil {
		t.Fatal(err)
	}
	if got := needsYouTold(t, f); got[len(got)-1] != want-1 {
		t.Fatalf("after one was done: told %v, want %d last", got, want-1)
	}
	before := len(needsYouTold(t, f))
	s.tellInboxNeedsYou() // a handshake: the same number again, for the hub's new connection
	if got := needsYouTold(t, f); len(got) != before+1 || got[len(got)-1] != want-1 {
		t.Fatalf("told again: %v", got)
	}
}
