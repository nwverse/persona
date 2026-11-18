package node

import (
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestModelRetryUsesDurableScheduler(t *testing.T) {
	n := &Node{}
	p := &assistantProc{id: "retry-session", launcher: "claude"}
	for _, text := range []string{"503 unavailable", "429 throttled", "connection lost", "408 timeout", "401 upstream 503", "403 timeout", "model_not_found"} {
		if n.recoverAssistantResult(p, true, text) {
			t.Fatalf("node started an immediate recovery for %q", text)
		}
	}
}

func TestModelRetryRestoredReceipt(t *testing.T) {
	n := &Node{}
	id, msg := "session", "model-retry:session:1:1"
	n.restoreEchoes([]echoRecord{{SessionID: id, ClientMsgID: msg, Line: "user-1", SentAt: time.Now(), In: echoInTranscript}})
	sent := 0
	for i := 0; i < 3; i++ {
		if err := n.sendEchoed(id, msg, "Continue", 0, echoAfter{}, func() error { sent++; return nil }); err != nil {
			t.Fatal(err)
		}
	}
	if sent != 0 {
		t.Fatal("a completed continuation was replayed after receipt restoration")
	}
}

func TestModelRetryRefusesUserStop(t *testing.T) {
	n := &Node{}
	n.state = &stateFile{Sessions: map[string]sessionState{}}
	n.state.put("stopped-session", sessionState{StopCause: proto.StopUser})
	_, err := n.assistantSend(withClientMsgID(t.Context(), "model-retry:stopped-session:1:1"), "stopped-session", "", "claude", "Continue", nil, "", "", modelExecution{})
	if err == nil {
		t.Fatal("automatic continuation bypassed Stop")
	}
}
