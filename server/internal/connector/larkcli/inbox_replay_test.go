package larkcli

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

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

func TestInboxReplayKeepsEachSelectedActivityInSameConversation(t *testing.T) {
	for _, tc := range []struct {
		name, chatType, topic string
	}{
		{"direct chat", "p2p", ""},
		{"known topic", "group", "omt_example_topic"},
		{"mention followed by reply", "group", "omt_example_topic"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first := addressedMessage("om_example_first", "ou_example_peer", "Example Peer", `"First request"`, tc.topic, nil)
			second := addressedMessage("om_example_second", "ou_example_other", "Example Other", `"Second request"`, tc.topic, nil)
			unselected := addressedMessage("om_example_context", "ou_example_other", "Example Other", `"Context only"`, tc.topic, nil)
			first.ChatType, second.ChatType, unselected.ChatType = tc.chatType, tc.chatType, tc.chatType
			first.CreateTime, second.CreateTime, unselected.CreateTime = "2030-01-01T00:01:00Z", "2030-01-01T00:02:00Z", "2030-01-01T00:03:00Z"
			if tc.name == "mention followed by reply" {
				first.Mentions = []adapter.Mention{{ID: "cli_example"}}
			}
			c := addressedTestConnector(t, []adapter.Message{second, unselected, first}, []adapter.Message{first, second, unselected})
			if tc.name == "known topic" {
				c.inboxTopics = map[string]struct{}{tc.topic: {}}
			}
			items, err := c.ReplayMessages(context.Background(), first.ChatID, "2030-01-01T00:00:00Z",
				"2030-01-01T00:05:00Z", []string{second.MessageID, first.MessageID})
			if err != nil || len(items) != 2 {
				t.Fatalf("selected activities were combined: items=%+v err=%v", items, err)
			}
			for i, want := range []adapter.Message{first, second} {
				it := items[i]
				if it.Last != want.MessageID || it.Sender != want.Sender.Name || it.Done != "" ||
					!strings.Contains(it.Context, "Context only") {
					t.Fatalf("activity lost its source or shared context: %+v", it)
				}
				wantTier := connector.Tier("")
				if tc.name == "mention followed by reply" && i == 0 {
					wantTier = connector.TierAction
				}
				if it.Tier != wantTier {
					t.Fatalf("activity inherited another message's tier: %+v", it)
				}
			}
			if items[0].Thread != items[1].Thread {
				t.Fatalf("replay changed the source conversation: %+v", items)
			}
		})
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

func TestInboxReplayRespectsSourceTimestampPrecision(t *testing.T) {
	minute := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	from := minute.Add(58 * time.Second)
	to := minute.Add(time.Minute)
	earlier := from.Add(-time.Second)
	for _, tc := range []struct {
		name, source string
		keep         bool
	}{
		{"precise before start", earlier.Format(time.RFC3339), false},
		{"wall clock seconds before start", earlier.In(cliLocation()).Format("2006-01-02 15:04:05"), false},
		{"epoch seconds before start", fmt.Sprint(earlier.Unix()), false},
		{"epoch milliseconds before start", fmt.Sprint(from.Add(-time.Millisecond).UnixMilli()), false},
		{"precise at start", from.Format(time.RFC3339), true},
		{"precise in final second", to.Add(-time.Millisecond).Format(time.RFC3339Nano), true},
		{"precise at end", to.Format(time.RFC3339), false},
		{"overlapping minute", minute.In(cliLocation()).Format("2006-01-02 15:04"), true},
		{"previous minute", minute.Add(-time.Minute).In(cliLocation()).Format("2006-01-02 15:04"), false},
		{"minute at end", to.In(cliLocation()).Format("2006-01-02 15:04"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := addressedMessage("om_example_request", "ou_example_peer", "Example Peer", `"Please help"`, "",
				[]adapter.Mention{{ID: "cli_example"}})
			m.CreateTime = tc.source
			c := addressedTestConnector(t, []adapter.Message{m}, []adapter.Message{m})
			items, err := c.ReplayMessages(context.Background(), m.ChatID, from.Format(time.RFC3339),
				to.Format(time.RFC3339), []string{m.MessageID})
			if tc.keep {
				if err != nil || len(items) != 1 || items[0].Last != m.MessageID {
					t.Fatalf("in-window message missing: items=%+v err=%v", items, err)
				}
			} else if err == nil || len(items) != 0 {
				t.Fatalf("out-of-window source timestamp accepted: %q, items=%+v err=%v", tc.source, items, err)
			}
		})
	}
}

func TestInboxReplayDropsBoundAppMessagesButKeepsEligibleOtherSenders(t *testing.T) {
	for _, tc := range []struct {
		name, chatType, content, kind string
		tier                          connector.Tier
		mention, owner, otherApp      bool
		assigned                      bool
	}{
		{"addressed bound app message", "group", `"Please review"`, "", "", true, false, false, false},
		{"bound app responsibility card", "group", `{"elements":[{"tag":"plain_text","text":"On-call:"},{"tag":"person","user_id":"ou_example_user"}]}`, "", "", false, false, false, false},
		{"direct bound app message", "p2p", `"Bot update"`, "", "", false, false, false, false},
		{"unaddressed bound app group message", "group", `"Routine update"`, "", "", false, false, false, false},
		{"owner identity with app metadata", "group", `"Please review"`, "bot_handoff", connector.TierFYI, true, true, false, false},
		{"addressed other app message", "group", `"Please review"`, "mention", connector.TierAction, true, false, true, false},
		{"other app responsibility card", "group", `{"elements":[{"tag":"plain_text","text":"On-call:"},{"tag":"person","user_id":"ou_example_user"}]}`, "mention", connector.TierAction, false, false, true, true},
		{"direct other app message", "p2p", `"Bot update"`, "bot", "", false, false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			selected := addressedMessage("om_example_selected", "cli_example", "Example Bot", tc.content, "", nil)
			selected.ChatType, selected.CreateTime = tc.chatType, "2030-01-01T00:01:00Z"
			selected.Sender.SenderType, selected.Sender.IDType, selected.Sender.AppID = "app", "app_id", "cli_example"
			if tc.owner {
				selected.Sender.ID, selected.Sender.Name = "ou_example_user", "Example User"
			} else if tc.otherApp {
				selected.Sender.ID, selected.Sender.AppID = "cli_example_other_app", "cli_example_other_app"
			}
			if tc.mention {
				selected.Mentions = []adapter.Mention{{ID: "cli_example"}}
			}
			unselected := addressedMessage("om_example_context", "cli_example", "Example Bot", `"Context only"`, "",
				[]adapter.Mention{{ID: "cli_example"}})
			unselected.Sender.SenderType, unselected.Sender.IDType = "app", "app_id"
			unselected.ChatType, unselected.CreateTime = tc.chatType, "2030-01-01T00:02:00Z"
			c := addressedTestConnector(t, []adapter.Message{selected, unselected}, []adapter.Message{selected, unselected})
			state := persistedInboxState{Cursor: "2030-01-02T12:00:00Z", PageToken: "example-live-page",
				Seen: []string{unselected.MessageID}, Topics: []string{"omt_example_known"}, TopicsSeeded: true}
			raw, err := json.Marshal(state)
			if err != nil {
				t.Fatal(err)
			}
			store := &inboxStateStore{raw: raw}
			c.SetStateStore(store)
			items, err := c.ReplayMessages(context.Background(), selected.ChatID, "2030-01-01T00:00:00Z",
				"2030-01-01T00:05:00Z", []string{selected.MessageID})
			if err != nil {
				t.Fatal(err)
			}
			if string(store.raw) != string(raw) || !reflect.DeepEqual(c.inboxState, state) ||
				!reflect.DeepEqual(c.inboxSeen, map[string]struct{}{unselected.MessageID: {}}) {
				t.Fatalf("replay filter changed live cursor or seen ledger: state=%+v seen=%+v", c.inboxState, c.inboxSeen)
			}
			if tc.kind == "" {
				if len(items) != 0 {
					t.Fatalf("unaddressed app activity entered: %+v", items)
				}
				return
			}
			if len(items) != 1 || items[0].Last != selected.MessageID || items[0].Kind != tc.kind ||
				items[0].Tier != tc.tier || items[0].OwnerAssigned != tc.assigned || items[0].Done != "" {
				t.Fatalf("selected identity or activity was lost: items=%+v", items)
			}
			if !strings.Contains(items[0].Context, "Context only") {
				t.Fatalf("unselected history was not retained as context: %+v", items[0])
			}
		})
	}
}

func TestInboxReplayMixedSelectionDoesNotPromoteBoundAppMessage(t *testing.T) {
	bot := addressedMessage("om_example_bot", "cli_example", "Example Bot", `"Bot request"`, "",
		[]adapter.Mention{{ID: "cli_example"}})
	bot.Sender.SenderType, bot.Sender.IDType = "app", "app_id"
	peer := addressedMessage("om_example_peer", "ou_example_peer", "Example Peer", `"Peer request"`, "",
		[]adapter.Mention{{ID: "cli_example"}})
	bot.CreateTime, peer.CreateTime = "2030-01-01T00:01:00Z", "2030-01-01T00:02:00Z"
	c := addressedTestConnector(t, []adapter.Message{bot, peer}, []adapter.Message{bot, peer})
	items, err := c.ReplayMessages(context.Background(), peer.ChatID, "2030-01-01T00:00:00Z",
		"2030-01-01T00:05:00Z", []string{bot.MessageID, peer.MessageID})
	if err != nil || len(items) != 1 || items[0].Last != peer.MessageID || items[0].Kind != "mention" ||
		items[0].Tier != connector.TierAction || !strings.Contains(items[0].Context, "Bot request") {
		t.Fatalf("selected bot became activity or displaced peer/context: items=%+v err=%v", items, err)
	}
}
