package node

import (
	"errors"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestShareCreationResultPreservesReceiptAcrossStateUpdates(t *testing.T) {
	n := &Node{state: newState()}
	count := 0
	n.state.persist = func(id string, s sessionState) {
		if s.CreationID != "" {
			count++
		}
	}
	for _, id := range []string{"synthetic-terminal", "synthetic-async", "synthetic-runtime-launcher", "synthetic-engine-runtime"} {
		s := &proto.Session{SessionID: id, Launcher: "synthetic-launcher", Cwd: "/workspace", Starting: id == "synthetic-async"}
		if _, err := n.creationResult(s, nil, "", "creation:"+id, "incarnation:"+id); err != nil {
			t.Fatal(err)
		}
		n.state.put(id, sessionState{Launcher: "synthetic-launcher", Cwd: "/workspace"})
		state, ok := n.state.get(id)
		if !ok || state.CreationID != "creation:"+id || state.Incarnation != "incarnation:"+id {
			t.Fatal("state update lost immutable receipt")
		}
	}
	if count != 8 {
		t.Fatal("creation state was not emitted durably")
	}
	resumed := &proto.Session{SessionID: "synthetic-resume"}
	_, _ = n.creationResult(resumed, nil, "old-session", "new-creation", "new-incarnation")
	if _, ok := n.state.get(resumed.SessionID); ok {
		t.Fatal("resume established ownership receipt")
	}
	_, _ = n.creationResult(&proto.Session{SessionID: "synthetic-failed"}, errors.New("synthetic failure"), "", "new-creation", "new-incarnation")
	if _, ok := n.state.get("synthetic-failed"); ok {
		t.Fatal("failure established receipt")
	}
}
