package transcript

import "testing"

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
