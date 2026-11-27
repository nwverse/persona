package node

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/rpc"
)

func TestAssistantViewStopRepairsOnlyOnceAndBeforeReportingCompletion(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		owned, active, report bool
		agent, reason         string
		fail, badReply        bool
		checks, stops         int64
		block                 bool
	}{
		{name: "invalid", owned: true, reason: "修正 fallback", checks: 1, block: true},
		{name: "retry", owned: true, active: true, reason: "修正 fallback", stops: 1},
		{name: "ordinary", reason: "修正 fallback", stops: 1},
		{name: "subagent", owned: true, agent: "worker", reason: "修正 fallback", stops: 1},
		{name: "report-only", owned: true, report: true, reason: "修正 fallback"},
		{name: "accepted", owned: true, checks: 1, stops: 1},
		{name: "unavailable", owned: true, fail: true, checks: 1, stops: 1},
		{name: "wrong-reply-type", owned: true, badReply: true, checks: 1, stops: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			n2hR, n2hW := io.Pipe()
			h2nR, h2nW := io.Pipe()
			t.Cleanup(func() { n2hW.Close(); h2nW.Close(); n2hR.Close(); h2nR.Close() })
			n, _ := testNode(t)
			n.peer = rpc.New("n", n2hW)
			hub := rpc.New("h", h2nW)
			const sid = "11111111-1111-4111-8111-111111111111"
			if tc.owned {
				n.mu.Lock()
				n.procs[sid] = &assistantProc{id: sid, agent: "claude", exited: make(chan struct{})}
				n.mu.Unlock()
			}
			var checks, stops atomic.Int64
			hub.OnRequest = func(_ context.Context, method string, raw json.RawMessage) (any, error) {
				checks.Add(1)
				var input assistantwire.ViewCheckRequest
				if method != assistantwire.MethodViewCheck || json.Unmarshal(raw, &input) != nil || input.SessionID != sid || input.Message != "```persona-view\ninvalid\n```" {
					t.Errorf("unexpected view check: %s", method)
				}
				if tc.fail {
					return nil, errors.New("check unavailable")
				}
				if tc.badReply {
					return json.RawMessage(`{"reason":42}`), nil
				}
				return assistantwire.ViewCheckResponse{Reason: tc.reason}, nil
			}
			hub.OnEvent = func(method string, raw json.RawMessage) {
				if method == "hook.event" {
					var event struct{ Event string }
					if json.Unmarshal(raw, &event) == nil && event.Event == "Stop" {
						stops.Add(1)
					}
				}
			}
			go hub.Serve(ctx, n2hR)
			go n.peer.Serve(ctx, h2nR)
			input, _ := json.Marshal(hookInput{HookEventName: "Stop", SessionID: sid, AgentID: tc.agent, StopHookActive: tc.active, LastAssistantMessage: "```persona-view\ninvalid\n```"})
			callCtx, done := context.WithTimeout(ctx, 2*time.Second)
			defer done()
			out, err := n.handleHook(callCtx, sockRequest{Kind: "hook", Input: input, ReportOnly: tc.report})
			if err != nil {
				t.Fatal(err)
			}
			if tc.block {
				body, ok := out.(map[string]any)
				if !ok || body["decision"] != "block" || body["reason"] != tc.reason {
					t.Fatalf("repair output: %v", out)
				}
			} else if out != nil {
				t.Fatalf("unexpected repair: %v", out)
			}
			if tc.stops > 0 {
				waitFor(t, "accepted Stop event", func() bool { return stops.Load() == tc.stops })
			}
			if checks.Load() != tc.checks || stops.Load() != tc.stops {
				t.Fatalf("checks=%d stops=%d, want %d/%d", checks.Load(), stops.Load(), tc.checks, tc.stops)
			}
		})
	}
}
