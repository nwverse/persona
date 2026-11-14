package larkcli

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/connector"
	adapter "github.com/nwverse/persona/server/internal/larkcli"
)

func TestInboxReplayOnlySelectedMessagesPreservesLiveState(t *testing.T) {
	card := addressedMessage("om_example_root", "cli_example_monitor", "Example Monitor",
		`"<card>On-call: Example User\nAn isolated test alert</card>"`, "omt_example_topic", nil)
	card.MsgType, card.CreateTime, card.MessagePosition = "interactive", "2030-01-01T00:00:00Z", "21"
	request := addressedMessage("om_example_request", "ou_example_user", "Example User",
		`"@Example Bot check this alert"`, "omt_example_topic", []adapter.Mention{{ID: "cli_example"}})
	request.CreateTime, request.MessagePosition = "2030-01-01T00:02:00Z", "-3"
	unrelated := addressedMessage("om_example_unselected", "ou_example_peer", "Example Peer",
		`"@Example Bot another request"`, "omt_example_topic", []adapter.Mention{{ID: "cli_example"}})
	unrelated.CreateTime, unrelated.MessagePosition = "2030-01-01T00:03:00Z", "-3"
	c := addressedTestConnector(t, []adapter.Message{card, request, unrelated}, []adapter.Message{card, request, unrelated})
	state := persistedInboxState{Cursor: "2030-01-02T12:00:00Z", PageToken: "example-live-page",
		Topics: []string{"omt_live_topic"}, Seen: []string{card.MessageID, request.MessageID}, TopicsSeeded: true}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	store := &inboxStateStore{raw: raw}
	c.SetStateStore(store)
	c.inboxThings = map[string]connector.InboxThing{"existing": {Thread: "existing", Last: "om_live"}}
	items, err := c.ReplayMessages(context.Background(), "oc_example_chat", "2030-01-01T00:00:00Z",
		"2030-01-01T00:05:00Z", []string{card.MessageID, request.MessageID})
	if err != nil || len(items) != 2 {
		t.Fatalf("items=%+v err=%v", items, err)
	}
	for _, it := range items {
		if it.Last == unrelated.MessageID || it.Done != "" {
			t.Fatalf("unselected context became activity or the handoff answered the alert: %+v", it)
		}
		if !strings.Contains(it.Context, "another request") {
			t.Fatalf("unselected context should still be readable: %+v", it)
		}
		if it.OwnerAssigned != (it.Last == card.MessageID) {
			t.Fatalf("responsibility exception followed context instead of the selected card: %+v", it)
		}
		wantSender := card.Sender.ID
		if it.Last == request.MessageID {
			wantSender = request.Sender.ID
		}
		if !slices.ContainsFunc(it.Tags, func(tag connector.Tag) bool {
			return tag.Key == connector.TagSender && tag.Value == wantSender
		}) {
			t.Fatalf("selected activity inherited an unselected sender's mute target: last=%q tags=%+v", it.Last, it.Tags)
		}
	}
	if string(store.raw) != string(raw) || !reflect.DeepEqual(c.inboxState, state) ||
		len(c.inboxSeen) != 2 || len(c.inboxTopics) != 1 || len(c.inboxThings) != 1 {
		t.Fatalf("replay changed live collection state: saved=%s state=%+v things=%+v", store.raw, c.inboxState, c.inboxThings)
	}
	again, err := c.ReplayMessages(context.Background(), "oc_example_chat", "2030-01-01T00:00:00Z",
		"2030-01-01T00:05:00Z", []string{card.MessageID, request.MessageID})
	if err != nil || !reflect.DeepEqual(items, again) {
		t.Fatalf("exact replay was not stable: items=%+v err=%v", again, err)
	}
}

func TestInboxReplayRejectsUnboundedOrMissingSelection(t *testing.T) {
	m := addressedMessage("om_example_one", "ou_example_user", "Example User", `"ordinary note"`, "", nil)
	c := addressedTestConnector(t, []adapter.Message{m}, []adapter.Message{m})
	for _, tc := range []struct {
		name, chat, start, end string
		ids                    []string
	}{
		{"empty IDs", "oc_example_chat", "2030-01-01T00:00:00Z", "2030-01-01T00:05:00Z", nil},
		{"duplicate IDs", "oc_example_chat", "2030-01-01T00:00:00Z", "2030-01-01T00:05:00Z", []string{m.MessageID, m.MessageID}},
		{"over one hour", "oc_example_chat", "2030-01-01T00:00:00Z", "2030-01-01T02:00:00Z", []string{m.MessageID}},
		{"invalid time", "oc_example_chat", "yesterday", "now", []string{m.MessageID}},
		{"different chat", "oc_example_other", "2030-01-01T00:00:00Z", "2030-01-01T00:05:00Z", []string{m.MessageID}},
		{"missing ID", "oc_example_chat", "2030-01-01T00:00:00Z", "2030-01-01T00:05:00Z", []string{"om_example_missing"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if items, err := c.ReplayMessages(context.Background(), tc.chat, tc.start, tc.end, tc.ids); err == nil || len(items) != 0 {
				t.Fatalf("unbounded or incomplete replay accepted: items=%+v err=%v", items, err)
			}
		})
	}
}

func TestInboxReplayOrdinaryOwnerAndUnaddressedPeerDoNotEnter(t *testing.T) {
	owner := addressedMessage("om_example_owner_note", "ou_example_user", "Example User", `"ordinary note"`, "", nil)
	peer := addressedMessage("om_example_peer_note", "ou_example_peer", "Example Peer", `"routine update"`, "", nil)
	owner.CreateTime, peer.CreateTime = "2030-01-01T00:01:00Z", "2030-01-01T00:02:00Z"
	c := addressedTestConnector(t, []adapter.Message{owner, peer}, []adapter.Message{owner, peer})
	items, err := c.ReplayMessages(context.Background(), "oc_example_chat", "2030-01-01T00:00:00Z",
		"2030-01-01T00:05:00Z", []string{owner.MessageID, peer.MessageID})
	if err != nil || len(items) != 0 {
		t.Fatalf("ordinary messages entered: items=%+v err=%v", items, err)
	}
}

func TestInboxReplayRejectsUnknownOrOutsideSourceTimestamp(t *testing.T) {
	for _, at := range []string{"", "not a timestamp", "2029-12-31T23:59:00Z", "2030-01-01T00:05:00Z"} {
		t.Run(at, func(t *testing.T) {
			m := addressedMessage("om_example_request", "ou_example_peer", "Example Peer", `"Please help"`, "",
				[]adapter.Mention{{ID: "cli_example"}})
			m.CreateTime = at
			c := addressedTestConnector(t, []adapter.Message{m}, []adapter.Message{m})
			items, err := c.ReplayMessages(context.Background(), m.ChatID, "2030-01-01T00:00:00Z",
				"2030-01-01T00:05:00Z", []string{m.MessageID})
			if err == nil || len(items) != 0 {
				t.Fatalf("unsafe source time accepted: items=%+v err=%v", items, err)
			}
		})
	}
}
