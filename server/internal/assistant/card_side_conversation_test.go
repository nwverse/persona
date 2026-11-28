package assistant

import (
	"encoding/json"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

// The owner's own side conversation is the owner talking to the Assistant: its ordinary permission is shown to
// the owner with Allow/Deny right away, instead of being routed to Main, which interaction_answer refuses for an
// assistant conversation and which would leave the card unanswerable until the standalone hold ran out (#17771).
func TestSideConversationPermissionStaysWithOwner(t *testing.T) {
	e := newRouteEnv(t, nil, true)
	side := proto.Session{SessionID: "side-conv-1", MachineID: "server", Cwd: assistantHome, Assistant: true, Status: proto.StatusWorking}
	convs := []proto.Session{
		{SessionID: "conv1", MachineID: "server", Cwd: assistantHome, Assistant: true, Status: proto.StatusIdle},
		{SessionID: "lead1", MachineID: "server", Cwd: assistantHome, Assistant: true, Status: proto.StatusIdle},
		side,
	}
	e.f.known["side-conv-1"] = true
	e.sessions("server", convs...)
	e.f.setSessions("server", convs...)

	ix := askPermission("ix_side_bash", "side-conv-1")
	ix.MachineID = "server"
	ix.ToolInput = json.RawMessage(`{"command":"mkdir -p notes && ls notes"}`)
	ix.WriteOperation = false
	done := e.raise(ix)

	if shown := e.lastShown(ix.ID); shown.Delegating {
		t.Fatal("the owner's side-conversation permission is held by a reviewer")
	}
	if hasRelayFrom(e.s, "conv1", cardAgentPermission, "interaction_id="+ix.ID) {
		t.Fatal("the owner's side-conversation permission was routed to the main conversation")
	}
	if n := e.pushesOf(ix.ID); n != 1 {
		t.Fatalf("owner push count = %d, want 1", n)
	}

	allow := true
	if err := e.s.resolve(ix.ID, proto.Resolution{Allow: &allow}); err != nil {
		t.Fatal(err)
	}
	answerOf(t, done)
}
