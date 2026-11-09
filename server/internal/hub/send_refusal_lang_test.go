package hub

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// A node's coded refusal of a send is explained in the request's language and names the machine (docs/assistant/onboarding.md
// 4.6 rule 2).
func TestSendRefusalSaysWhyInTheRequestLanguage(t *testing.T) {
	h := newTestHub(t)
	pipeNode(t, h, "box", func(context.Context) error {
		return &rpc.Error{Code: proto.CodeClaudeMissing, Message: "zsh:1: command not found: claude",
			Data: json.RawMessage(`{"delivered":"none","launcher":"claude"}`)}
	})
	h.byID["box"].setName("Work Laptop")
	ctx := i18n.With(context.Background(), i18n.En)
	status, body, _ := h.deliverOnce(ctx, h.byID["box"], map[string]any{"session": "s1", "text": "hi"})
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("body %s: %v", body, err)
	}
	if status != 409 || out["code"] != proto.CodeClaudeMissing || out["machine_name"] != "Work Laptop" ||
		out["error"] != "Claude Code isn't installed on Work Laptop yet." || out["action"] != "install_claude" {
		t.Fatalf("refusal: %d %v", status, out)
	}
}
