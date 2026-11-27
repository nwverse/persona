package node

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

func TestElicitationRuntimeURL(t *testing.T) {
	n, hub := runtimeNode(t)
	host := dialRuntime(t, n, dshHello())
	_ = host.peer.Notify("runtime.sessions", map[string]any{"sessions": []proto.Session{{SessionID: "url-session", Cwd: t.TempDir()}}})
	waitFor(t, "URL session listed", func() bool { _, ok := listed(t, n)["url-session"]; return ok })
	hub.mu.Lock()
	hub.resolve = func(proto.Interaction) proto.Resolution { return proto.Resolution{Action: "accept"} }
	hub.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	want := &proto.Elicitation{Mode: "url", URL: "https://example.com/confirm", Message: "Confirm access", ID: "request-url"}
	var res proto.Resolution
	err := host.peer.Call(ctx, "interaction.create", map[string]any{"interaction": proto.Interaction{
		Kind: proto.IxElicitation, SessionID: "url-session", Elicitation: want,
	}}, &res)
	if err != nil || res.Action != "accept" {
		t.Fatalf("URL resolution: %+v, %v", res, err)
	}
	hub.mu.Lock()
	defer hub.mu.Unlock()
	got := hub.asked[0]
	if got.Kind != proto.IxElicitation || got.Elicitation == nil || got.Elicitation.Mode != want.Mode ||
		got.Elicitation.URL != want.URL || got.Elicitation.Message != want.Message || got.Elicitation.ID != want.ID {
		t.Fatalf("URL interaction: %+v", got)
	}
}

func TestElicitationHook(t *testing.T) {
	for _, mode := range []string{"form", "url"} {
		for _, action := range []string{"accept", "decline", "cancel", "dismiss", "invalid", "timeout", "disconnect"} {
			t.Run(mode+"/"+action, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				n2hR, n2hW := io.Pipe()
				h2nR, h2nW := io.Pipe()
				t.Cleanup(func() { n2hW.Close(); h2nW.Close(); n2hR.Close(); h2nR.Close() })
				n, _ := testNode(t)
				n.peer = rpc.New("n", n2hW)
				hub := rpc.New("h", h2nW)
				var got proto.Interaction
				hub.OnRequest = func(ctx context.Context, method string, raw json.RawMessage) (any, error) {
					var p struct {
						Interaction proto.Interaction `json:"interaction"`
					}
					if method != "interaction.create" || json.Unmarshal(raw, &p) != nil {
						t.Errorf("unexpected request: %s %s", method, raw)
					}
					got = p.Interaction
					switch action {
					case "timeout":
						return nil, &rpc.Error{Message: "request expired"}
					case "disconnect":
						h2nW.Close()
						return nil, rpc.ErrClosed
					}
					return proto.Resolution{Action: action, Content: map[string]any{"choice": "yes", "enabled": true, "count": 2}, Dismiss: action == "dismiss"}, nil
				}
				go hub.Serve(ctx, n2hR)
				go n.peer.Serve(ctx, h2nR)
				in := hookInput{HookEventName: "Elicitation", SessionID: "test-session", MCPServerName: "example-mcp",
					Message: "请选择", Mode: mode, RequestedSchema: json.RawMessage(`{"type":"object","properties":{"choice":{"type":"string"}}}`),
					URL: "https://example.com/confirm", ElicitationID: "request-example", AgentID: "agent-example", AgentType: "worker"}
				raw, _ := json.Marshal(in)
				callCtx, callCancel := context.WithTimeout(ctx, 5*time.Second)
				defer callCancel()
				out, err := n.handleHook(callCtx, sockRequest{Input: raw, Attempt: "attempt-example"})
				if got.Kind != proto.IxElicitation || got.Elicitation.Mode != mode || got.MCPServerName != in.MCPServerName ||
					got.AgentID != in.AgentID || got.ToolUseID != "" || !got.Resumable {
					t.Fatalf("lost hook fields or invented a tool association: %+v", got)
				}
				if action == "disconnect" {
					if err != errNodeGone || out != nil {
						t.Fatalf("disconnect: %v %v", out, err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if action == "dismiss" || action == "invalid" || action == "timeout" {
					if out != nil {
						t.Fatalf("fallback must leave stdout empty: %v", out)
					}
					return
				}
				output := out.(map[string]any)["hookSpecificOutput"].(map[string]any)
				if output["hookEventName"] != "Elicitation" || output["action"] != action {
					t.Fatalf("output: %v", out)
				}
				_, hasContent := output["content"]
				if hasContent != (mode == "form" && action == "accept") {
					t.Fatalf("content: %v", out)
				}
			})
		}
	}
	var in hookInput
	_ = json.Unmarshal([]byte(`{"hook_event_name":"Elicitation","mcp_server_name":"example-mcp","message":"请选择","requested_schema":{"type":"object"}}`), &in)
	ix := proto.Interaction{MCPServerName: in.MCPServerName}
	if !elicitationInteraction(in, &ix) || ix.Elicitation.Mode != "form" {
		t.Fatal("omitted mode must mean form")
	}
	in.ToolUseID = "call-example"
	if interactionID(in, "one") == interactionID(in, "two") {
		t.Fatal("multiple elicitations in one tool call must stay distinct")
	}
}
