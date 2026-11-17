package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/nwverse/persona/server/internal/assistantwire"
)

// session.send's from_mode (the permission-mode class of a message's words, which a node states to a Claude outside
// Persona's terminals) passes only on the assistant's own device.call; an app, a relay or another manager cannot state
// it, and the rest of the params go on unchanged (#9607).
func TestAssistantFromModeOnlyFromTheAssistant(t *testing.T) {
	raw := json.RawMessage(`{"session_id":"s1","text":"hi","from_mode":"bypass"}`)
	if got := assistantFromMode(withCaller(context.Background(), assistantwire.AssistantID), raw); !bytes.Equal(got, raw) {
		t.Errorf("the assistant's from_mode: %s", got)
	}
	for name, ctx := range map[string]context.Context{
		"an app or relay": context.Background(),
		"another manager": withCaller(context.Background(), "extensions"),
	} {
		got := assistantFromMode(ctx, raw)
		var p map[string]any
		if err := json.Unmarshal(got, &p); err != nil || p["from_mode"] != nil || p["text"] != "hi" || p["session_id"] != "s1" {
			t.Errorf("%s: %s", name, got)
		}
	}
	plain := json.RawMessage(`{"session_id":"s1","text":"hi"}`)
	if got := assistantFromMode(context.Background(), plain); !bytes.Equal(got, plain) {
		t.Errorf("params without from_mode changed: %s", got)
	}
}
