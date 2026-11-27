package assistant

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/bus"
	"github.com/nwverse/persona/server/internal/rpc"
)

func TestHubSessionBusInvalidParams(t *testing.T) {
	b := &BusHub{helloed: make(chan struct{})}
	close(b.helloed)
	for _, tc := range []struct{ method, params string }{
		{assistantwire.MethodFleetAsk, `{"interaction":42}`},
		{assistantwire.MethodAgentTabName, `{"prompt":42}`},
	} {
		t.Run(tc.method, func(t *testing.T) {
			out, err := b.answer(t.Context(), tc.method, json.RawMessage(tc.params))
			var rpcErr *rpc.Error
			if out != nil || !errors.As(err, &rpcErr) || rpcErr.Code != bus.CodeInvalidParams ||
				!strings.HasPrefix(rpcErr.Message, tc.method+": ") {
				t.Fatalf("%s %s = %v, %v, want invalid_params prefixed with %q", tc.method, tc.params, out, err, tc.method+": ")
			}
		})
	}
}

func TestHubAgentTabNameBusReply(t *testing.T) {
	s, f := newQuietService(t)
	f.mu.Lock()
	f.tabReply = "Fix Login"
	f.mu.Unlock()
	b := &BusHub{svc: s, helloed: make(chan struct{})}
	close(b.helloed)
	p := assistantwire.AgentTabNameParams{Prompt: "Fix the login page"}
	out, err := b.answer(t.Context(), assistantwire.AgentTabNameMethod.Name, mustJSON(p))
	if got, ok := out.(assistantwire.AgentTabNameResult); err != nil || !ok || got.Name != "fix-login" {
		t.Fatalf("agent tab name = %+v (%T), %v, want fix-login", out, out, err)
	}
}
