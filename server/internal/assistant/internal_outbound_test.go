package assistant

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestSensitiveOwnedAgentDelegationWaitsForSupervisor(t *testing.T) {
	e := newRouteEnv(t, nil, true)
	e.f.infoTagModel = true
	e.f.mu.Lock()
	e.f.infoTag = `{"categories":["legal_id"]}`
	e.f.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan bool, 1)
	go func() {
		ok, _, err := e.s.confirmOutbound(ctx, "server", "side-chat", "agent_send", "synthetic sensitive identity text",
			"Delegate verification", json.RawMessage(`{"session_id":"owned-agent","text":"synthetic sensitive identity text"}`))
		done <- ok && err == nil
	}()
	var ix proto.Interaction
	waitFor(t, "sensitive internal gate", func() bool {
		for _, q := range e.s.pendingQuestions() {
			if q.ToolName == "agent_send" {
				ix = q
				_, up := e.s.pendingIx(ix.ID)
				return up
			}
		}
		return false
	})
	e.noticeWith("conv1", "interaction_id="+ix.ID)
	select {
	case <-done:
		t.Fatal("sensitive delegation passed without approval")
	default:
	}
	if len(e.f.agentSent) != 0 || e.pushesOf(ix.ID) != 0 {
		t.Fatal("unapproved content was sent or owner was pushed")
	}
	for _, notice := range e.f.noticesTo("conv1") {
		if strings.Contains(notice, "synthetic sensitive identity text") {
			t.Fatal("reviewer wake-up leaked the content instead of the purpose")
		}
	}
	if _, err := runJSON(t, e.s, "conv1", "interaction_answer", map[string]any{"machine": "server", "session_id": "side-chat",
		"interaction_id": ix.ID, "allow": true, "reason": "Existing task authorizes this owned Agent delegation"}); err != nil {
		t.Fatal(err)
	}
	if !<-done {
		t.Fatal("explicit supervisor approval did not release the gate")
	}
}
