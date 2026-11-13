package node

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/mcpwait"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
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

func TestMCPWaitSocketRequiresClientOptIn(t *testing.T) {
	for _, optedIn := range []bool{false, true} {
		t.Run(map[bool]string{false: "old client", true: "opted in"}[optedIn], func(t *testing.T) {
			n, _ := testNode(t)
			n.hubMCPWait.Store(true)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			n2hR, n2hW := io.Pipe()
			h2nR, h2nW := io.Pipe()
			n.peer = rpc.New("n", n2hW)
			hub := rpc.New("h", h2nW)
			called := make(chan map[string]any, 1)
			release := make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			defer unblock()
			hub.OnRequest = func(ctx context.Context, method string, params json.RawMessage) (any, error) {
				if method != "mcp.call" {
					return map[string]any{}, nil
				}
				var p map[string]any
				_ = json.Unmarshal(params, &p)
				called <- p
				select {
				case <-release:
					return proto.TextResult("done", false), nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			go func() { _ = hub.Serve(ctx, n2hR) }()
			go func() { _ = n.peer.Serve(ctx, h2nR) }()
			defer func() { _ = n2hW.Close(); _ = h2nW.Close() }()

			nodeSide, clientSide := net.Pipe()
			defer nodeSide.Close()
			defer clientSide.Close()
			if err := clientSide.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
				t.Fatal(err)
			}
			go n.serveSock(context.Background(), nodeSide)
			request := map[string]any{"kind": "mcp.call", "name": "example", "args": map[string]any{}}
			if optedIn {
				request["wait_updates"] = true
			}
			line, _ := json.Marshal(request)
			if _, err := clientSide.Write(append(line, '\n')); err != nil {
				t.Fatal(err)
			}
			var forwarded map[string]any
			select {
			case forwarded = <-called:
			case <-time.After(5 * time.Second):
				t.Fatal("the socket call did not reach the hub")
			}
			callID, _ := forwarded["call_id"].(string)
			if !optedIn {
				if _, ok := forwarded["wait_updates"]; ok || callID != "" {
					t.Fatalf("an old client was opted into wait updates: %v", forwarded)
				}
				n.mcpWaitMu.Lock()
				registered := len(n.mcpWaits)
				n.mcpWaitMu.Unlock()
				if registered != 0 {
					t.Fatalf("an old client registered %d pending calls", registered)
				}
			} else if enabled, _ := forwarded["wait_updates"].(bool); !enabled || callID == "" {
				t.Fatalf("the opted-in call lost its correlators: %v", forwarded)
			}

			reader := bufio.NewReader(clientSide)
			readLine := func() map[string]json.RawMessage {
				t.Helper()
				line, err := reader.ReadBytes('\n')
				if err != nil {
					t.Fatal(err)
				}
				var decoded map[string]json.RawMessage
				if err := json.Unmarshal(line, &decoded); err != nil {
					t.Fatal(err)
				}
				return decoded
			}
			handled := make(chan bool, 1)
			if callID != "" {
				params, _ := json.Marshal(proto.MCPWaitEvent{CallID: callID, Kind: proto.MCPWaitApproval})
				go func() { handled <- n.onMCPWait(proto.EventMCPWait, params) }()
			}
			if optedIn {
				if first := readLine(); string(first["wait_kind"]) != `"approval"` {
					t.Fatalf("the opted-in client's first line = %v", first)
				}
			}
			unblock()
			final := readLine()
			if final["wait_kind"] != nil || final["content"] == nil {
				t.Fatalf("the client's final response = %v", final)
			}
			if callID != "" && !<-handled {
				t.Fatal("the wait update was not handled")
			}
		})
	}
}

func TestLCUElicitForwardsOnlyAnOptedInCallID(t *testing.T) {
	n, _ := testNode(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	n2hR, n2hW := io.Pipe()
	h2nR, h2nW := io.Pipe()
	n.peer = rpc.New("n", n2hW)
	hub := rpc.New("h", h2nW)
	called := make(chan map[string]any, 3)
	hub.OnRequest = func(_ context.Context, method string, params json.RawMessage) (any, error) {
		if method != "computer.elicit" {
			t.Fatalf("method = %q", method)
		}
		var p map[string]any
		_ = json.Unmarshal(params, &p)
		called <- p
		return map[string]any{"action": "cancel"}, nil
	}
	go func() { _ = hub.Serve(ctx, n2hR) }()
	go func() { _ = n.peer.Serve(ctx, h2nR) }()
	defer func() { _ = n2hW.Close(); _ = h2nW.Close() }()

	for _, tc := range []struct{ callID, origin string }{{callID: "call-lcu"}, {callID: "call-remote", origin: "origin"}, {}} {
		ctx := mcpwait.WithMachine(mcpwait.WithCall(ctx, tc.callID), tc.origin)
		if _, err := n.elicitLCU(ctx, "conversation", json.RawMessage(`{"message":"private question"}`)); err != nil {
			t.Fatal(err)
		}
		select {
		case params := <-called:
			got, _ := params["call_id"].(string)
			if got != tc.callID {
				t.Fatalf("computer.elicit call ID = %q, want %q: %v", got, tc.callID, params)
			}
			origin, _ := params["wait_machine"].(string)
			if origin != tc.origin {
				t.Fatalf("computer.elicit origin = %q, want %q: %v", origin, tc.origin, params)
			}
			wantFields := 2
			if tc.callID != "" {
				wantFields++
			}
			if tc.origin != "" {
				wantFields++
			}
			if len(params) != wantFields || params["conversation"] != "conversation" || params["params"] == nil {
				t.Fatalf("computer.elicit params = %v", params)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("computer.elicit did not reach the hub")
		}
	}
}
