package larkcli

import (
	"context"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/connector"
	adapter "github.com/nwverse/persona/server/internal/larkcli"
)

func TestInboxReplaySenderTagsStayWithSelectedMessage(t *testing.T) {
	cases := []struct {
		name       string
		message    adapter.Message
		wantSender string
	}{
		{
			name: "owner handoff",
			message: addressedMessage("om_selected_owner", "ou_example_user", "Example User",
				`"@Example Bot review this"`, "omt_example_topic", []adapter.Mention{{ID: "cli_example"}}),
			wantSender: "ou_example_user",
		},
		{
			name: "peer bot mention",
			message: addressedMessage("om_selected_peer", "ou_example_peer", "Example Peer",
				`"@Example Bot review this"`, "omt_example_topic", []adapter.Mention{{ID: "cli_example"}}),
			wantSender: "ou_example_peer",
		},
		{
			name: "card with application identity only",
			message: func() adapter.Message {
				m := addressedMessage("om_selected_card", "", "Example Monitor",
					`"<card>On-call: Example User</card>"`, "omt_example_topic", nil)
				m.MsgType, m.Sender.AppID = "interactive", "cli_example_monitor"
				return m
			}(),
			wantSender: "cli_example_monitor",
		},
		{
			name: "unknown card sender does not inherit context identity",
			message: func() adapter.Message {
				m := addressedMessage("om_selected_unknown", "", "Example Monitor",
					`"<card>On-call: Example User</card>"`, "omt_example_topic", nil)
				m.MsgType = "interactive"
				return m
			}(),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			selected := tc.message
			selected.CreateTime, selected.MessagePosition = "2030-01-01T00:01:00Z", "21"
			later := addressedMessage("om_unselected_context", "cli_other_monitor", "Other Monitor",
				`"Later context from another sender"`, "omt_example_topic", nil)
			later.CreateTime, later.MessagePosition = "2030-01-01T00:02:00Z", "22"
			c := addressedTestConnector(t, []adapter.Message{selected}, []adapter.Message{selected, later})
			items, err := c.ReplayMessages(context.Background(), selected.ChatID,
				"2030-01-01T00:00:00Z", "2030-01-01T00:05:00Z", []string{selected.MessageID})
			if err != nil || len(items) != 1 {
				t.Fatalf("selected recovery: items=%+v err=%v", items, err)
			}
			thing := items[0]
			if thing.Last != selected.MessageID || !strings.Contains(thing.Context, "Later context from another sender") {
				t.Fatalf("selected activity or context changed: %+v", thing)
			}
			var tags []connector.Tag
			for _, tag := range thing.Tags {
				if tag.Key == connector.TagSender {
					tags = append(tags, tag)
				}
			}
			if tc.wantSender == "" {
				if len(tags) != 0 {
					t.Fatalf("unknown selected sender inherited a mute target: %+v", tags)
				}
			} else if len(tags) != 1 || tags[0].Value != tc.wantSender || tags[0].Name != selected.Sender.Name {
				t.Fatalf("sender tags=%+v, want selected sender %q", tags, tc.wantSender)
			}
		})
	}
}
