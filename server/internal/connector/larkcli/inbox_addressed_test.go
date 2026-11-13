package larkcli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/connector"
	adapter "github.com/nwverse/persona/server/internal/larkcli"
)

func TestGroupMessagesAddressedMessages(t *testing.T) {
	auth := adapter.AuthStatus{OpenID: "ou_example_owner", AppID: "cli_example_bot", BotOpenID: "ou_example_persona_bot", UserName: "Example Owner"}
	tests := []struct {
		name      string
		message   adapter.Message
		wantKind  string
		wantTier  connector.Tier
		wantMent  bool
		wantGroup bool
	}{
		{
			name: "owner exact app mention is handoff",
			message: addressedMessage("om_owner_request", "ou_example_owner", "Example Owner", `"Please review this"`, "omt_example_topic",
				[]adapter.Mention{{ID: "cli_example_bot", Name: "Example Bot"}}),
			wantKind: "bot_handoff", wantTier: connector.TierFYI, wantGroup: true,
		},
		{
			name: "peer exact bot mention is actionable",
			message: addressedMessage("om_peer_request", "ou_example_peer", "Example Peer", `"Can you help?"`, "",
				[]adapter.Mention{{ID: "cli_example_bot", Name: "Example Bot"}}),
			wantKind: "mention", wantTier: connector.TierAction, wantMent: true, wantGroup: true,
		},
		{
			name: "owner exact bot open ID is handoff",
			message: addressedMessage("om_owner_open_id_request", "ou_example_owner", "Example Owner", `"Please review this"`, "omt_example_topic",
				[]adapter.Mention{{ID: "ou_example_persona_bot"}}),
			wantKind: "bot_handoff", wantTier: connector.TierFYI, wantGroup: true,
		},
		{
			name: "peer exact bot open ID is actionable",
			message: addressedMessage("om_peer_open_id_request", "ou_example_peer", "Example Peer", `"Can you help?"`, "",
				[]adapter.Mention{{ID: "ou_example_persona_bot"}}),
			wantKind: "mention", wantTier: connector.TierAction, wantMent: true, wantGroup: true,
		},
		{
			name: "interactive owner role is actionable",
			message: func() adapter.Message {
				m := addressedMessage("om_owner_card", "ou_example_peer", "Example Peer",
					`"<card title=\"On-call person: Example Owner\">Incident details</card>"`, "", nil)
				m.MsgType = "interactive"
				return m
			}(),
			wantKind: "mention", wantTier: connector.TierAction, wantMent: true, wantGroup: true,
		},
		{
			name:    "bot membership without mention in unknown group",
			message: addressedMessage("om_unaddressed_bot", "ou_example_peer", "Example Peer", `"Routine update"`, "", nil),
		},
		{
			name:    "owner ordinary text",
			message: addressedMessage("om_owner_text", "ou_example_owner", "Example Owner", `"Routine note"`, "", nil),
		},
		{
			name: "similar bot display name is not exact ID",
			message: addressedMessage("om_similar_bot", "ou_example_peer", "Example Peer", `"Please help"`, "",
				[]adapter.Mention{{ID: "cli_example_bot_copy", Name: "Example Bot"}}),
		},
		{
			name: "role field name prefix is rejected",
			message: addressedMessage("om_role_prefix", "ou_example_peer", "Example Peer",
				`{"header":{"title":{"content":"Owner assistant: Example Owner"}}}`, "", nil),
		},
		{
			name: "owner name outside role field is rejected",
			message: addressedMessage("om_name_elsewhere", "ou_example_peer", "Example Peer",
				`{"text":"Example Owner","tag":"div"}`, "", nil),
		},
		{
			name: "card with non-role person is rejected",
			message: addressedMessage("om_non_role_person", "ou_example_peer", "Example Peer",
				`{"header":{"title":{"content":"Incident details"}},"elements":[{"tag":"person","user_id":"ou_example_owner"}]}`, "", nil),
		},
		{
			name: "structured card role with owner user ID works",
			message: addressedMessage("om_structured_role", "ou_example_peer", "Example Peer",
				`{"elements":[{"tag":"person","text":"On-call person: Example Owner","user_id":"ou_example_owner"}]}`, "", nil),
			wantKind: "mention", wantTier: connector.TierAction, wantMent: true, wantGroup: true,
		},
		{
			name: "deleted exact mention is ignored",
			message: func() adapter.Message {
				m := addressedMessage("om_deleted", "ou_example_peer", "Example Peer", `"Please help"`, "",
					[]adapter.Mention{{ID: "cli_example_bot"}})
				m.Deleted = true
				return m
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := addressedTestConnector(t, []adapter.Message{tt.message}, []adapter.Message{tt.message})
			groups := c.groupMessages([]adapter.Message{tt.message}, auth, nil)
			if !tt.wantGroup {
				if len(groups) != 0 {
					t.Fatalf("unexpected groups: %+v", groups)
				}
				return
			}
			if len(groups) != 1 {
				t.Fatalf("groups = %d, want 1: %+v", len(groups), groups)
			}
			group := groups[0]
			if group.addressed != tt.wantMent || group.mentioned != tt.wantMent || group.handoff != (tt.wantKind == "bot_handoff") {
				t.Fatalf("group classification = addressed:%v mentioned:%v handoff:%v", group.addressed, group.mentioned, group.handoff)
			}
			thing := c.thingForGroup(context.Background(), group, auth)
			if thing.Kind != tt.wantKind || thing.Tier != tt.wantTier {
				t.Fatalf("thing kind/tier = %q/%q, want %q/%q", thing.Kind, thing.Tier, tt.wantKind, tt.wantTier)
			}
			if tt.wantKind == "bot_handoff" {
				if thing.Done != "" {
					t.Fatalf("handoff marked replied: %q", thing.Done)
				}
				if thing.Last != tt.message.MessageID || thing.Thread != "larkcli:chat:oc_example_chat:thread:omt_example_topic:handoff:"+tt.message.MessageID {
					t.Fatalf("handoff source/thread lost: last=%q thread=%q", thing.Last, thing.Thread)
				}
				if thing.Context == "" || thing.Preview == "" {
					t.Fatalf("handoff original context missing: %+v", thing)
				}
				chatID, ok := chatIDFromThread(thing.Thread)
				if !ok || chatID != "oc_example_chat" {
					t.Fatalf("chatIDFromThread(%q) = %q, %v", thing.Thread, chatID, ok)
				}
				if chat, topic, err := conversationIDs(thing.Thread); err != nil || chat != "oc_example_chat" || topic != "omt_example_topic" {
					t.Fatalf("conversationIDs(%q) = %q, %q, %v", thing.Thread, chat, topic, err)
				}
			}
		})
	}
}

func TestInboxAddressedPollDeduplicatesAndKeepsAlertContext(t *testing.T) {
	request := addressedMessage("om_example_request", "ou_example_user", "Example User", `"@Example Bot check this alert"`, "omt_example_topic",
		[]adapter.Mention{{ID: "cli_example"}})
	request.CreateTime = "2030-01-01T00:00:00Z"
	root := addressedMessage("om_example_root", "cli_example_monitor", "Example Monitor",
		`"<card title=\"Incident\">On-call: Example User\nAn isolated test alert</card>"`, "omt_example_topic", nil)
	root.MsgType, root.CreateTime = "interactive", "2029-12-31T23:59:00Z"
	c := addressedTestConnector(t, []adapter.Message{request}, []adapter.Message{root, request})
	if moved, err := c.pollChat(context.Background()); err != nil || !moved {
		t.Fatalf("first poll moved=%v err=%v", moved, err)
	}
	items, err := c.Collect(context.Background())
	if err != nil || len(items) != 1 {
		t.Fatalf("collected=%+v err=%v", items, err)
	}
	if items[0].Kind != "bot_handoff" || items[0].Tier != connector.TierFYI ||
		!strings.Contains(items[0].Context, "An isolated test alert") || !strings.Contains(items[0].Context, "@Example Bot check this alert") {
		t.Fatalf("handoff lost its source context: %+v", items[0])
	}
	if !slices.ContainsFunc(items[0].Tags, func(tag connector.Tag) bool {
		return tag.Key == connector.TagSender && tag.Value == request.Sender.ID && tag.Name == request.Sender.Name
	}) {
		t.Fatalf("handoff inherited a context sender's mute target: %+v", items[0].Tags)
	}
	if moved, err := c.pollChat(context.Background()); err != nil || moved {
		t.Fatalf("duplicate poll moved=%v err=%v", moved, err)
	}
}

func TestInboxAddressedCardAndOwnerHandoffStayOpen(t *testing.T) {
	card := addressedMessage("om_example_root", "cli_example_monitor", "Example Monitor",
		`"<card>On-call: Example User\nAn isolated test alert</card>"`, "omt_example_topic", nil)
	card.MsgType, card.CreateTime, card.MessagePosition = "interactive", "2030-01-01T00:00:00Z", "21"
	request := addressedMessage("om_example_request", "ou_example_user", "Example User",
		`"@Example Bot check this alert"`, "omt_example_topic", []adapter.Mention{{ID: "cli_example"}})
	request.CreateTime, request.MessagePosition = "2030-01-01T00:02:00Z", "-3"
	historical := request
	historical.Mentions = []adapter.Mention{{ID: "ou_example_persona_bot"}}
	c := addressedTestConnector(t, []adapter.Message{card, request}, []adapter.Message{card, historical})
	if moved, err := c.pollChat(context.Background()); err != nil || !moved {
		t.Fatalf("poll moved=%v err=%v", moved, err)
	}
	items, err := c.Collect(context.Background())
	if err != nil || len(items) != 2 {
		t.Fatalf("collected=%+v err=%v", items, err)
	}
	for _, item := range items {
		if item.Done != "" {
			t.Fatalf("handing off an alert did not answer it: %+v", item)
		}
		switch item.Last {
		case card.MessageID:
			if item.Kind != "mention" || item.Tier != connector.TierAction {
				t.Fatalf("alert is not actionable: %+v", item)
			}
		case request.MessageID:
			if item.Kind != "bot_handoff" || item.Tier != connector.TierFYI {
				t.Fatalf("owner request is not a handoff: %+v", item)
			}
		default:
			t.Fatalf("unexpected message admitted: %+v", item)
		}
	}
}

func TestInboxAddressedHandoffFetchesRootOmittedByThreadList(t *testing.T) {
	root := addressedMessage("om_example_root", "cli_example_monitor", "Example Monitor",
		`"<card>On-call: Example User\nRoot alert details</card>"`, "omt_example_topic", nil)
	root.MsgType, root.CreateTime = "interactive", "2029-12-31T23:59:00Z"
	request := addressedMessage("om_example_request", "ou_example_user", "Example User",
		`"@Example Bot check this alert"`, "omt_example_topic", []adapter.Mention{{ID: "cli_example"}})
	request.CreateTime = "2030-01-01T00:00:00Z"
	c := addressedTestConnector(t, []adapter.Message{request}, []adapter.Message{request})
	path := filepath.Join(t.TempDir(), "root.json")
	raw, err := json.Marshal(map[string]any{"ok": true, "data": adapter.MessagePage{Messages: []adapter.Message{root}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LARKCLI_FAKE_ROOT", path)
	if moved, err := c.pollChat(context.Background()); err != nil || !moved {
		t.Fatalf("poll moved=%v err=%v", moved, err)
	}
	items, err := c.Collect(context.Background())
	if err != nil || len(items) != 1 || items[0].Kind != "bot_handoff" ||
		!strings.Contains(items[0].Context, "Root alert details") || !strings.Contains(items[0].Context, "check this alert") {
		t.Fatalf("handoff lost the root omitted by thread list: %+v err=%v", items, err)
	}
}

func TestCardAddressesOwnerExactRolesAndIdentities(t *testing.T) {
	auth := adapter.AuthStatus{OpenID: "ou_example_owner", UserName: "Example Owner"}
	for _, tt := range []struct {
		name, body string
		want       bool
	}{
		{"structured at", `{"elements":[{"tag":"at","user_id":"ou_example_owner"}]}`, true},
		{"structured other at", `{"elements":[{"tag":"at","user_id":"ou_example_peer"}]}`, false},
		{"structured person after role", `{"elements":[[{"tag":"text","text":"On-call:"},{"tag":"person","user_id":"ou_example_owner"}]]}`, true},
		{"person in unrelated field", `{"elements":[[{"tag":"text","text":"On-call: Example Peer"}],[{"tag":"text","text":"Observers:"},{"tag":"person","user_id":"ou_example_owner"}]]}`, false},
		{"exact name", `"<card>On-call: Example Owner</card>"`, true},
		{"different owner", `"<card>On-call: Example Peer</card>"`, false},
		{"name prefix", `"<card>On-call: Example Owner Jr</card>"`, false},
		{"name in description", `"<card>Reported by Example Owner</card>"`, false},
		{"non-role name", `"<card>Attendees: Example Owner</card>"`, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			message := adapter.Message{MsgType: "interactive", Content: json.RawMessage(tt.body)}
			if got := cardAddressesOwner(message, auth); got != tt.want {
				t.Fatalf("addressed=%v want=%v", got, tt.want)
			}
		})
	}
}

func TestInboxConfirmedHandoffReplyStaysUnderOriginalMessage(t *testing.T) {
	for _, thread := range []string{
		"larkcli:chat:oc_example_chat:handoff:om_example_request",
		"larkcli:chat:oc_example_chat:thread:omt_example_topic:handoff:om_example_request",
	} {
		t.Run(thread, func(t *testing.T) {
			logPath := filepath.Join(t.TempDir(), "args.log")
			t.Setenv("LARKCLI_FAKE_LOG", logPath)
			c := testConnector(t, "inbox")
			if err := c.Reply(context.Background(), connector.InboxReply{
				Thread: thread, Last: "om_example_request", Text: "Confirmed reply", ClientID: "confirmed-handoff",
			}); err != nil {
				t.Fatal(err)
			}
			log, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(log), "+messages-reply --message-id om_example_request") ||
				!strings.Contains(string(log), "--reply-in-thread --as bot") || strings.Contains(string(log), "+messages-send") {
				t.Fatalf("confirmed reply lost its original thread or bot identity: %s", log)
			}
		})
	}
}

func TestGroupMessagesConfirmedTopicReply(t *testing.T) {
	auth := adapter.AuthStatus{OpenID: "ou_example_owner", AppID: "cli_example_bot"}
	request := addressedMessage("om_topic_request", "ou_example_peer", "Example Peer", `"Please investigate"`, "omt_example_topic",
		[]adapter.Mention{{ID: "cli_example_bot"}})
	ownerReply := addressedMessage("om_topic_reply", "ou_example_owner", "Example Owner", `"I will take a look"`, "omt_example_topic", nil)
	ownerReply.MessagePosition = "22"
	request.MessagePosition = "21"
	c := addressedTestConnector(t, []adapter.Message{request, ownerReply}, []adapter.Message{request, ownerReply})
	group := c.groupMessages([]adapter.Message{request, ownerReply}, auth, nil)
	if len(group) != 1 {
		t.Fatalf("groups = %d, want one topic group: %+v", len(group), group)
	}
	thing := c.thingForGroup(context.Background(), group[0], auth)
	if thing.Done != connector.DoneReplied || thing.Last != request.MessageID {
		t.Fatalf("confirmed reply not reflected on original request: %+v", thing)
	}
	if chat, topic, err := conversationIDs(thing.Thread); err != nil || chat != "oc_example_chat" || topic != "omt_example_topic" {
		t.Fatalf("conversationIDs(%q) = %q, %q, %v", thing.Thread, chat, topic, err)
	}
}

func addressedMessage(id, senderID, senderName, content, topic string, mentions []adapter.Mention) adapter.Message {
	msgType := "text"
	if len(content) > 0 && content[0] == '{' {
		msgType = "interactive"
	}
	return adapter.Message{
		MessageID: id, MessagePosition: "20", ChatID: "oc_example_chat", ChatName: "Example Group",
		ChatType: "group", ThreadID: topic, MsgType: msgType,
		Sender: adapter.Sender{ID: senderID, Name: senderName}, Content: json.RawMessage(content), Mentions: mentions,
	}
}

func addressedTestConnector(t *testing.T, search, history []adapter.Message) *Connector {
	t.Helper()
	c := testConnector(t, "inbox_followup")
	for _, fixture := range []struct {
		name, env string
		messages  []adapter.Message
	}{{"search.json", "LARKCLI_FAKE_SEARCH", search}, {"context.json", "LARKCLI_FAKE_CONTEXT", history}} {
		path := filepath.Join(t.TempDir(), fixture.name)
		raw, err := json.Marshal(map[string]any{"ok": true, "data": adapter.MessagePage{Messages: fixture.messages}})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv(fixture.env, path)
	}
	return c
}
