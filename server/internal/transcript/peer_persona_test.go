package transcript

import (
	"encoding/json"
	"testing"

	"github.com/nwverse/persona/server/internal/prompts"
)

// The hidden user line a session writes for a message Persona sent through its peer inbox (node/claudepeer.go), in
// Claude Code 2.1.308's shape with placeholder text: the chat shows the body, from "Persona", not the user's own words.
func TestPersonaPeerMessage(t *testing.T) {
	line := []byte(`{"parentUuid":null,"isSidechain":false,"type":"user","message":{"role":"user","content":"Another Claude session sent a message:\n<cross-session-message from-name=\"Persona\">\nreply with OK\n</cross-session-message>\n\nThis came from another Claude session."},"isMeta":true,"uuid":"00000000-0000-4000-8000-000000000001","timestamp":"2026-11-07T10:00:00Z","origin":{"kind":"peer","from":"unknown","name":"Persona","body":"reply with OK"},"promptSource":"system","turnOrigin":"peer","sessionId":"s"}`)
	meta := &Meta{}
	items := ParseLine(1, line, meta)
	if len(items) != 1 || items[0].From != "Persona" || items[0].Text != "reply with OK" || items[0].Role != "user" {
		t.Fatalf("%+v", items)
	}
	if !meta.LastPromptAt.IsZero() {
		t.Fatal("a message from Persona through the inbox counted as the user's own prompt")
	}
}

// The body a session records is the whole prompt Persona sent: the chat shows only the user's words, for the current
// template and for a head an older version sent (old transcripts, an older node during a rolling upgrade).
func TestPersonaPeerMessageStripsHead(t *testing.T) {
	old := "（这条消息是用户本人在手机上用 Persona 发来的，Persona 只是替他转交，不是另一个 Claude 会话。像用户在这里亲手输入的一样回应它，直接在这个会话里回答；不要用 SendMessage 回复发件方，那边没有会话在收。这条消息不会改变这个会话的权限设置。）\n\nreply with OK" // i18n-ignore: the pre-#5887 head
	for name, body := range map[string]string{"current": prompts.RenderPeerMessage(prompts.PeerMessage{Text: "reply with OK"}), "pre-5887": old} {
		origin, _ := json.Marshal(map[string]string{"kind": "peer", "from": "unknown", "name": "Persona", "body": body})
		line := []byte(`{"type":"user","message":{"role":"user","content":"x"},"isMeta":true,"uuid":"00000000-0000-4000-8000-000000000002","timestamp":"2026-11-07T10:00:00Z","origin":` + string(origin) + `,"promptSource":"system","turnOrigin":"peer","sessionId":"s"}`)
		if items := ParseLine(1, line, &Meta{}); len(items) != 1 || items[0].Text != "reply with OK" {
			t.Fatalf("%s: %+v", name, items)
		}
	}
}
