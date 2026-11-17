package larkcli

import (
	"context"
	"slices"
	"testing"

	"github.com/nwverse/persona/server/internal/connector"
	adapter "github.com/nwverse/persona/server/internal/larkcli"
)

func TestInboxReplayOwnerDMRequiresBotMentionAndKeepsSender(t *testing.T) {
	for _, handoff := range []bool{false, true} {
		name := "ordinary owner message"
		if handoff {
			name = "explicit bot handoff"
		}
		t.Run(name, func(t *testing.T) {
			owner := addressedMessage("om_example_owner", "ou_example_user", "Example User",
				`"Please investigate"`, "", nil)
			owner.ChatType, owner.CreateTime = "p2p", "2030-01-01T00:01:00Z"
			if handoff {
				owner.Mentions = []adapter.Mention{{ID: "cli_example"}}
			}
			contextMessage := addressedMessage("om_example_context", "ou_example_peer", "Example Peer",
				`"Context from another sender"`, "", nil)
			contextMessage.ChatType, contextMessage.CreateTime = "p2p", "2030-01-01T00:02:00Z"
			c := addressedTestConnector(t, []adapter.Message{owner}, []adapter.Message{owner, contextMessage})
			ctx := context.Background()
			replayed, err := c.ReplayMessages(ctx, owner.ChatID, "2030-01-01T00:00:00Z",
				"2030-01-01T00:05:00Z", []string{owner.MessageID})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := c.pollChat(ctx); err != nil {
				t.Fatal(err)
			}
			collected, err := c.Collect(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if !handoff {
				if len(replayed) != 0 || len(collected) != 0 {
					t.Fatalf("an ordinary outgoing DM became a request: replay=%+v poll=%+v", replayed, collected)
				}
				return
			}
			for _, things := range [][]connector.InboxThing{replayed, collected} {
				if len(things) != 1 || things[0].Kind != "bot_handoff" || things[0].Last != owner.MessageID {
					t.Fatalf("owner's selected handoff was lost: %+v", things)
				}
				sender, ok := latestSenderTag([]adapter.Message{owner}, "")
				if !ok || !slices.Contains(things[0].Tags, sender) ||
					slices.ContainsFunc(things[0].Tags, func(tag connector.Tag) bool {
						return tag.Key == connector.TagSender && tag.Value != owner.Sender.ID
					}) {
					t.Fatalf("owner sender mute identity was replaced by context: %+v", things[0].Tags)
				}
			}
		})
	}
}

func TestCardCompletedAssignmentDoesNotLeakAcrossContainers(t *testing.T) {
	auth := adapter.AuthStatus{OpenID: "ou_example_owner", UserName: "Example Owner"}
	for _, tc := range []struct{ name, content string }{
		{"tagless container", `{"text":"On-call: Other Person","body":{"elements":[{"tag":"person","user_id":"ou_example_owner"}]}}`},
		{"nested text and body", `{"text":{"tag":"plain_text","content":"On-call: Other Person"},"body":{"inner":{"elements":[{"tag":"person","user_id":"ou_example_owner"}]}}}`},
		{"extra container around completed field", `{"body":{"text":"On-call: Other Person","elements":[{"tag":"person","user_id":"ou_example_owner"}]}}`},
		{"completed field in labeled row", `{"elements":[{"tag":"plain_text","text":"On-call:"},{"text":"On-call: Other Person","body":{"elements":[{"tag":"person","user_id":"ou_example_owner"}]}}]}`},
		{"completed child in labeled container", `{"text":"On-call:","body":{"text":"On-call: Other Person","elements":[{"tag":"person","user_id":"ou_example_owner"}]}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			message := adapter.Message{MsgType: "interactive", Content: []byte(tc.content)}
			if cardAssignsOwner(message, auth) || cardAddressesOwner(message, auth) {
				t.Fatal("an unrelated nested person inherited a completed assignment")
			}
		})
	}
}

func TestInboxReplayPinnedSelectionDoesNotPinSubsequentPoll(t *testing.T) {
	for _, topic := range []string{"", "omt_example_topic"} {
		t.Run("topic="+topic, func(t *testing.T) {
			selected := addressedMessage("om_example_selected", "ou_example_peer", "Example Peer",
				`"Selected earlier request"`, topic, []adapter.Mention{{ID: "ou_example_user"}})
			later := addressedMessage("om_example_later", "ou_example_other", "Example Other",
				`"Later ordinary request"`, topic, []adapter.Mention{{ID: "ou_example_user"}})
			selected.ChatType, later.ChatType = "p2p", "p2p"
			selected.CreateTime, later.CreateTime = "2030-01-01T00:01:00Z", "2030-01-01T00:02:00Z"
			selected.MessagePosition, later.MessagePosition = "20", "21"
			c := addressedTestConnector(t, []adapter.Message{selected}, []adapter.Message{selected, later})
			ctx := context.Background()
			replay := func() {
				t.Helper()
				things, err := c.ReplayMessages(ctx, selected.ChatID, "2030-01-01T00:00:00Z",
					"2030-01-01T00:05:00Z", []string{selected.MessageID})
				if err != nil || len(things) != 1 || things[0].Last != selected.MessageID {
					t.Fatalf("explicit replay did not keep its selection: %+v (%v)", things, err)
				}
			}
			replay()
			if _, err := c.pollChat(ctx); err != nil {
				t.Fatal(err)
			}
			check := func() {
				t.Helper()
				things, err := c.Collect(ctx)
				if err != nil || len(things) != 1 || things[0].Last != later.MessageID ||
					!slices.ContainsFunc(things[0].Tags, func(tag connector.Tag) bool {
						return tag.Key == connector.TagSender && tag.Value == later.Sender.ID
					}) {
					t.Fatalf("replay pinned an ordinary snapshot to old activity: %+v (%v)", things, err)
				}
			}
			check()
			replay()
			check()
		})
	}
}
