package hub

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/transcript"
)

func TestMessageSourceHistoryStreamAndFull(t *testing.T) {
	for _, runtime := range []string{"claude", "codex"} {
		t.Run(runtime, func(t *testing.T) {
			want := &proto.MessageSource{Kind: proto.SourceConversation, ConversationID: "conversation-1", Title: "Research"}
			text := prompts.MessageSourceText(strings.Repeat("Read the result.\n", 5000), want)
			var line any
			if runtime == "claude" {
				line = map[string]any{"type": "user", "uuid": "source-line", "message": map[string]any{"role": "user", "content": text}}
			} else {
				line = map[string]any{"timestamp": "2026-01-02T09:00:00Z", "type": "event_msg", "payload": map[string]any{"type": "item_completed", "item": map[string]any{"type": "UserMessage", "id": "source-line", "content": []any{map[string]any{"type": "text", "text": text}}}}}
			}
			b, err := json.Marshal(line)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "source.jsonl")
			if err := os.WriteFile(path, append(b, '\n'), 0600); err != nil {
				t.Fatal(err)
			}
			h := &Hub{}
			file := transcript.NewFile(path)
			stream, err := file.Refresh()
			if err != nil || len(stream) != 1 {
				t.Fatalf("stream: %v %+v", err, stream)
			}
			assert := func(name string, items []proto.Item) {
				t.Helper()
				items = h.withInjected(items)
				if len(items) != 1 || !reflect.DeepEqual(items[0].Source, want) || items[0].Role != "user" || items[0].From != "" {
					t.Fatalf("%s lost source or changed role/peer: %+v", name, items)
				}
				wire, err := json.Marshal(map[string]any{"type": "items", "items": items})
				if err != nil || !strings.Contains(string(wire), `"source":{"kind":"conversation","conversation_id":"conversation-1","title":"Research"}`) {
					t.Fatalf("%s source missing on wire: %s %v", name, wire, err)
				}
			}
			assert("live stream", stream)
			page, _, _ := file.Page(0, 10)
			assert("history page", page)
			full, err := file.Full(stream[0].ID)
			if err != nil || full.Text != strings.TrimSpace(text) {
				t.Fatalf("full: %v, bytes %d want %d", err, len(full.Text), len(strings.TrimSpace(text)))
			}
			assert("full item", []proto.Item{full})
			// A new reader models node/hub restart after echo caches have expired.
			restarted := transcript.NewFile(path)
			if _, err := restarted.Refresh(); err != nil {
				t.Fatal(err)
			}
			restored, _, _ := restarted.Page(0, 10)
			assert("restart backlog", restored)
		})
	}
}

func TestMessageSourceHubUserProtection(t *testing.T) {
	h := &Hub{}
	source := &proto.MessageSource{Kind: proto.SourceConversation, ConversationID: "conversation-1", Title: "Research"}
	relay := prompts.RenderRelay(prompts.Relay{FromID: source.ConversationID, FromTitle: source.Title, Tool: "conversation_send", Hop: 1, MaxHops: 4, Text: "Read the result."})
	for _, text := range []string{relay, prompts.MessageSourceText("Read the result.", source), "⏰ Inspection（定时任务）\n\nRead the result."} { // i18n-ignore: exact legacy task header
		for _, item := range []proto.Item{
			{Role: "user", Kind: proto.KindText, Text: text, ClientMsgID: "app-1"},
			{Role: "user", Kind: proto.KindText, Text: prompts.ProtectUserMessage(text)},
		} {
			got := h.withInjected([]proto.Item{item})[0]
			if got.Source != nil || got.Injected != nil || got.Text != item.Text {
				t.Fatalf("ordinary user input became an automated author: %+v", got)
			}
		}
	}
	it := proto.Item{Role: "user", Kind: proto.KindText, Text: "original", Source: &proto.MessageSource{Kind: "future"}, Injected: &proto.Injected{Kind: proto.InjectedFollowup, State: "daily"}}
	if got := h.withInjected([]proto.Item{it})[0]; got.Source.Kind != "future" || got.Injected.Kind != proto.InjectedFollowup {
		t.Fatalf("explicit/legacy metadata was overwritten: %+v", got)
	}
}
