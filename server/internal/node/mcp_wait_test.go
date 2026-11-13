package node

import (
	"bufio"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestMCPWaitLocalLinesStopAtFinalResponse(t *testing.T) {
	n := &Node{}
	nodeSide, clientSide := net.Pipe()
	defer nodeSide.Close()
	defer clientSide.Close()
	if id, pending := n.beginMCPWait(nodeSide); id != "" || pending != nil {
		t.Fatal("a hub without mcp_wait_v1 must keep the single-response protocol")
	}
	n.hubMCPWait.Store(true)
	id, pending := n.beginMCPWait(nodeSide)
	if id == "" || pending == nil {
		t.Fatal("an opted-in call on a capable hub was not registered")
	}
	lines := make(chan string, 4)
	readDone := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(clientSide)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		readDone <- scanner.Err()
	}()
	next := func() string {
		t.Helper()
		select {
		case line := <-lines:
			return line
		case <-time.After(5 * time.Second):
			t.Fatal("local socket line did not arrive")
			return ""
		}
	}
	writeUpdate := func(kind proto.MCPWaitKind) {
		t.Helper()
		params, _ := json.Marshal(struct {
			proto.MCPWaitEvent
			Question string `json:"question,omitempty"`
		}{MCPWaitEvent: proto.MCPWaitEvent{CallID: id, Kind: kind}, Question: "must not cross the local boundary"})
		if !n.onMCPWait(proto.EventMCPWait, params) {
			t.Fatal("mcp.wait was not handled")
		}
	}
	writeUpdate(proto.MCPWaitApproval)
	if got := next(); got != `{"wait_kind":"approval"}` {
		t.Fatalf("interim line = %s", got)
	}
	writeUpdate(proto.MCPWaitKind("future"))
	if got := next(); got != `{"wait_kind":"unknown"}` {
		t.Fatalf("unknown interim line = %s", got)
	}
	n.finishMCPWait(id, pending, proto.TextResult("done", false))
	var final map[string]json.RawMessage
	if err := json.Unmarshal([]byte(next()), &final); err != nil || final["wait_kind"] != nil || final["content"] == nil {
		t.Fatalf("final response = %v (%v)", final, err)
	}
	writeUpdate(proto.MCPWaitComputer) // late updates find no registered call and write nothing
	select {
	case line := <-lines:
		t.Fatalf("update followed the final response: %s", line)
	default:
	}
	n.mcpWaitMu.Lock()
	remaining := len(n.mcpWaits)
	n.mcpWaitMu.Unlock()
	if remaining != 0 {
		t.Fatalf("%d pending calls remained after the final response", remaining)
	}
}
