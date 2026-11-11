package node

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestForceCloseTerminalConfirmation(t *testing.T) {
	for _, status := range []string{proto.StatusBlocked, proto.StatusWorking} {
		t.Run(status, func(t *testing.T) {
			n, f, _ := herdrNode(t)
			f.reply("session.snapshot", snapshot(nil, nil, []any{claudeAgent(sidA, status, "w1:p1", "w1:t1", "w1", "/tmp", "")}, nil))
			listSessions(t, n)
			f.reset()
			_, err := request(t, n, "session.close", map[string]any{"session_id": sidA, "force": true})
			if status == proto.StatusWorking {
				if code, _ := refusalOf(t, err); code != "agent_busy" {
					t.Fatalf("working close: %v", err)
				}
				if len(f.trace()) != 0 {
					t.Fatalf("working Agent touched: %v", f.trace())
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got := withoutAgentReports(f.trace())
			if len(got) < 3 || got[0] != sendKeys("w1:p1", "ctrl+c") || got[1] != sendKeys("w1:p1", "ctrl+c") {
				t.Fatalf("close did not cancel: %v", got)
			}
			if !strings.Contains(strings.Join(got, "\n"), "tab.close") {
				t.Fatalf("terminal stayed open: %v", got)
			}
		})
	}
}

func TestForceCloseRuntimeConfirmation(t *testing.T) {
	n, _ := runtimeNode(t)
	hello := dshHello()
	hello["caps"].(map[string]any)["close"] = true
	host := dialRuntime(t, n, hello)
	for _, status := range []string{proto.StatusBlocked, proto.StatusWorking} {
		_ = host.peer.Notify("runtime.sessions", map[string]any{"sessions": []map[string]any{{"session_id": "confirm-runtime", "status": status}}})
		waitFor(t, "current runtime status", func() bool { return listed(t, n)["confirm-runtime"].Status == status })
		_, err := nodeCall(t, n, "session.close", map[string]any{"session_id": "confirm-runtime", "force": true})
		if status == proto.StatusBlocked && err != nil {
			t.Fatal(err)
		}
		if status == proto.StatusWorking && err == nil {
			t.Fatal("working runtime was force-closed")
		}
	}
	calls := host.got("session.close")
	if len(calls) != 1 {
		t.Fatalf("runtime closes: %s", calls)
	}
	var params struct {
		Force bool `json:"force"`
	}
	_ = json.Unmarshal(calls[0], &params)
	if !params.Force {
		t.Fatal("runtime did not get guarded close")
	}
}
