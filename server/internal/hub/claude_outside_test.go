package hub

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// A Claude session outside Persona's terminals (the desktop app, another terminal) takes a message through its peer
// inbox (node/claudepeer.go) once and once only: the hub sends only with a client_msg_id it can record, and a message
// the session did not take (held for approval, dropped) answers delivered "unknown" in the request's language and is
// not sent again on a retry.
func TestClaudeOutsideSendIsGuarded(t *testing.T) {
	for _, source := range []string{proto.SourceClaudeDesktop, proto.SourceClaudeTerminal} {
		h := newTestHub(t)
		h.store = newSentStore()
		var sends atomic.Int32
		pipeNode(t, h, "box", func(context.Context) error {
			sends.Add(1)
			return &rpc.Error{Code: "peer_unconfirmed", Message: "节点原话", Data: json.RawMessage(`{"delivered":"unknown"}`)}
		})
		m := h.byID["box"]
		m.mu.Lock()
		m.sessions = []proto.Session{{SessionID: "s1", Agent: "claude", SourceKind: source, Capabilities: &proto.SessionCapabilities{Read: true, Send: true}}}
		m.mu.Unlock()
		if code, _ := call(t, h.routes(), "POST", sendPath, h.cfg.Token, `{"text":"hi"}`); code == 200 || sends.Load() != 0 {
			t.Fatalf("%s: a send without client_msg_id went (code %d)", source, code)
		}
		body := `{"text":"reply with OK","client_msg_id":"peer-message"}`
		for range 2 {
			code, resp := call(t, h.routes(), "POST", sendPath, h.cfg.Token, body)
			if code == 200 || resp["delivered"] != "unknown" {
				t.Fatalf("%s: held message answered %d %v", source, code, resp)
			}
			if e, _ := resp["error"].(string); e != i18n.T(i18n.En, "rpc-errors.peer_unconfirmed") && e != i18n.T(i18n.ZhHans, "rpc-errors.peer_unconfirmed") {
				t.Fatalf("%s: not the hub's words for it: %q", source, e)
			}
		}
		if sends.Load() != 1 {
			t.Fatalf("%s: sent %d times", source, sends.Load())
		}
	}
}
