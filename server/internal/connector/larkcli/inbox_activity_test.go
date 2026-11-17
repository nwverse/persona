package larkcli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/connector"
	adapter "github.com/nwverse/persona/server/internal/larkcli"
)

func TestInboxIgnoresNonConversationActivity(t *testing.T) {
	for _, source := range []string{"search", "direct_sweep"} {
		for _, activity := range []string{"empty_system", "system_with_sender", "senderless"} {
			t.Run(source+"/"+activity, func(t *testing.T) {
				message := adapter.Message{MessageID: "om_activity", ChatID: "oc_example", ChatType: "p2p",
					MsgType: "system", CreateTime: "2030-01-01T00:00:00Z"}
				switch activity {
				case "system_with_sender":
					message.Sender = adapter.Sender{ID: "ou_example_peer"}
					message.Content = json.RawMessage(`"Conversation settings changed"`)
				case "senderless":
					message.MsgType, message.Content = "text", json.RawMessage(`"No identified sender"`)
				}
				search := []adapter.Message{message}
				if source == "direct_sweep" {
					search = nil
				}
				old := adapter.Message{MessageID: "om_old_request", ChatID: "oc_example", ChatType: "p2p",
					CreateTime: "2029-07-01T00:00:00Z", Sender: adapter.Sender{ID: "ou_example_peer"},
					Content: json.RawMessage(`"Old feedback awaiting a reply"`)}
				c := followupConnector(t, search, []adapter.Message{message, old})
				if source == "direct_sweep" {
					file := filepath.Join(t.TempDir(), "chats.json")
					if err := os.WriteFile(file, []byte(`{"ok":true,"data":{"chats":[{"chat_id":"oc_example","chat_type":"p2p"}]}}`), 0600); err != nil {
						t.Fatal(err)
					}
					t.Setenv("LARKCLI_FAKE_CHATS", file)
				}
				store := &inboxStateStore{}
				c.SetStateStore(store)
				if moved, err := c.Poll(context.Background()); err != nil || moved {
					t.Fatalf("non-conversation activity changed Inbox: moved=%v err=%v", moved, err)
				}
				if got := inboxThings(t, c); len(got) != 0 {
					t.Fatalf("non-conversation activity created an item: %+v", got)
				}
				state := c.inboxStateForTests()
				if !slices.Contains(state.Seen, message.MessageID) {
					t.Fatal("ignored activity was not consumed by the seen ledger")
				}
				if source == "search" && state.Cursor != "2029-12-31T23:50:00Z" {
					t.Fatalf("ignored activity did not advance the search cursor: %q", state.Cursor)
				}
				if moved, err := c.Poll(context.Background()); err != nil || moved {
					t.Fatalf("ignored activity was replayed: moved=%v err=%v", moved, err)
				}
			})
		}
	}
}

func TestInboxSystemActivityDoesNotWakeExistingThing(t *testing.T) {
	const thread = "larkcli:chat:oc_example"
	for _, done := range []string{"", connector.DoneReplied} {
		t.Run("done="+done, func(t *testing.T) {
			system := adapter.Message{MessageID: "om_system", ChatID: "oc_example", ChatType: "p2p",
				MsgType: "system", CreateTime: "2030-01-01T00:00:00Z"}
			c := followupConnector(t, []adapter.Message{system}, []adapter.Message{system})
			previous := connector.InboxThing{Thread: thread, Kind: "dm", Last: "om_old_request",
				At: time.Date(2029, 7, 1, 0, 0, 0, 0, time.UTC), Context: "Previously processed request", Done: done}
			c.inboxThings = map[string]connector.InboxThing{thread: previous}
			if moved, err := c.Poll(context.Background()); err != nil || moved {
				t.Fatalf("system activity woke existing thing: moved=%v err=%v", moved, err)
			}
			if got := inboxThings(t, c)[thread]; !reflect.DeepEqual(got, previous) {
				t.Fatalf("system activity rewrote existing thing: %+v", got)
			}
		})
	}
}

func TestInboxHistoryDoesNotPromoteSystemActivity(t *testing.T) {
	request := adapter.Message{MessageID: "om_request", MessagePosition: "10", ChatID: "oc_example",
		ChatType: "p2p", CreateTime: "2030-01-01T00:00:00Z",
		Sender: adapter.Sender{ID: "ou_example_peer"}, Content: json.RawMessage(`"Current request"`)}
	system := adapter.Message{MessageID: "om_system", MessagePosition: "11", ChatID: "oc_example",
		MsgType: "system", CreateTime: "2030-01-01T00:01:00Z",
		Content: json.RawMessage(`"Conversation settings changed"`)}
	c := followupConnector(t, []adapter.Message{request}, []adapter.Message{system, request})
	if _, err := c.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := inboxThings(t, c)["larkcli:chat:oc_example"]
	if got.Last != request.MessageID || !got.At.Equal(messageTime(request)) {
		t.Fatalf("history promoted a system event: %+v", got)
	}
	if strings.Contains(got.Context, "Conversation settings changed") {
		t.Fatalf("system activity entered triage context: %q", got.Context)
	}
}

func TestInboxContextSeparatesCurrentMessagesFromHistory(t *testing.T) {
	old := adapter.Message{MessageID: "om_old", MessagePosition: "1", ChatID: "oc_example",
		ChatType: "p2p", CreateTime: "2029-07-01T00:00:00Z",
		Sender: adapter.Sender{ID: "ou_example_peer", Name: "Example peer"}, Content: json.RawMessage(`"Old approval request"`)}
	current := old
	current.MessageID, current.MessagePosition, current.CreateTime = "om_current", "2", "2030-01-01T00:00:00Z"
	current.Content = json.RawMessage(`"Thanks, no action needed"`)
	c := followupConnector(t, []adapter.Message{current}, []adapter.Message{current, old})
	if _, err := c.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := inboxThings(t, c)["larkcli:chat:oc_example"]
	parts := strings.Split(got.Context, "Background history (context only, not new requests):")
	if len(parts) != 2 || !strings.Contains(parts[0], "Current messages (new activity):") ||
		!strings.Contains(parts[0], "Thanks, no action needed") || strings.Contains(parts[0], "Old approval request") ||
		!strings.Contains(parts[1], "Old approval request") || strings.Contains(parts[1], "Thanks, no action needed") {
		t.Fatalf("current activity and old requests were mixed: %q", got.Context)
	}
	if !strings.Contains(parts[0], "[2030-01-01T00:00:00Z]") || !strings.Contains(parts[1], "[2029-07-01T00:00:00Z]") {
		t.Fatalf("message timestamps are missing: %q", got.Context)
	}
	golden, err := os.ReadFile("testdata/inbox_context.golden")
	if err != nil {
		t.Fatal(err)
	}
	if got.Context+"\n" != string(golden) {
		t.Fatalf("triage context changed:\n%s", got.Context)
	}
}

func TestInboxCurrentIncludesOnlyUnseenHistoryFollowups(t *testing.T) {
	request := adapter.Message{MessageID: "om_request", MessagePosition: "10", ChatID: "oc_example",
		ChatType: "p2p", CreateTime: "2030-01-01T00:00:00Z",
		Sender: adapter.Sender{ID: "ou_example_peer"}, Content: json.RawMessage(`"New request"`)}
	followup := request
	followup.MessageID, followup.MessagePosition, followup.CreateTime = "om_followup", "11", "2030-01-01T00:01:00Z"
	followup.Content = json.RawMessage(`"Follow-up found in history"`)
	for _, seen := range []bool{false, true} {
		t.Run(map[bool]string{false: "unseen", true: "already_processed"}[seen], func(t *testing.T) {
			c := followupConnector(t, []adapter.Message{request}, []adapter.Message{followup, request})
			if seen {
				c.markSeen(followup.MessageID)
			}
			if _, err := c.Poll(context.Background()); err != nil {
				t.Fatal(err)
			}
			got := inboxThings(t, c)["larkcli:chat:oc_example"]
			wantLast := followup.MessageID
			if seen {
				wantLast = request.MessageID
			}
			current := strings.Split(got.Context, "Background history (context only, not new requests):")[0]
			if got.Last != wantLast || strings.Contains(current, "Follow-up found in history") == seen {
				t.Fatalf("history freshness was lost (seen=%v): %+v", seen, got)
			}
			c.inboxMu.Lock()
			_, recorded := c.inboxSeen[followup.MessageID]
			c.inboxMu.Unlock()
			if !recorded {
				t.Fatal("a newly discovered follow-up was not marked seen")
			}
		})
	}
}

func TestInboxCurrentAttachmentIsNotReplacedByOldText(t *testing.T) {
	old := adapter.Message{MessageID: "om_old", MessagePosition: "1", ChatID: "oc_example",
		ChatType: "p2p", CreateTime: "2029-07-01T00:00:00Z",
		Sender: adapter.Sender{ID: "ou_example_peer"}, Content: json.RawMessage(`"Old request"`)}
	image := old
	image.MessageID, image.MessagePosition, image.CreateTime = "om_image", "2", "2030-01-01T00:00:00Z"
	image.MsgType, image.Content = "image", json.RawMessage(`{"image_key":"img_example"}`)
	c := followupConnector(t, []adapter.Message{image}, []adapter.Message{image, old})
	if _, err := c.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := inboxThings(t, c)["larkcli:chat:oc_example"]
	current := strings.Split(got.Context, "Background history (context only, not new requests):")[0]
	if got.Last != image.MessageID || !strings.Contains(current, "[image]") || strings.Contains(current, "Old request") {
		t.Fatalf("new attachment disappeared behind old text: %+v", got)
	}
}
