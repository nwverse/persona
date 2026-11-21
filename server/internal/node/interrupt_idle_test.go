package node

import (
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

// session.interrupt sends Esc to a Claude pane only while its turn runs (working, or blocked on a card). On an idle box
// Esc quits claude (docs/lessons/agents.md "Esc on an idle Claude input box exits claude"): a stop that arrives after the
// turn ended (a stale "working" in the app, a second tap, the assistant's agent_interrupt after a phone confirmation)
// presses nothing and answers that the turn is over (settled, not_running).
func TestInterruptSendsNoEscToAnIdleClaude(t *testing.T) {
	for _, tc := range []struct {
		status string
		esc    bool
	}{
		{proto.StatusIdle, false},
		{proto.StatusDone, false},
		{proto.StatusUnknown, false},
		{proto.StatusWorking, true},
		{proto.StatusBlocked, true},
	} {
		t.Run(tc.status, func(t *testing.T) {
			n, f, _ := herdrNode(t)
			seedAgents(t, n, f, claudeAgent(sidA, tc.status, "w1:p1", "w1:t1", "w1", "/tmp/k", ""))
			res, err := request(t, n, "session.interrupt", map[string]any{"session_id": sidA})
			if err != nil {
				t.Fatal(err)
			}
			if tc.esc {
				assertTrace(t, f.trace(), []string{sendKeys("w1:p1", "esc")})
				return
			}
			assertTrace(t, f.trace(), nil)
			if res["not_running"] != true || res["settled"] != true {
				t.Fatalf("answer %v: want settled and not_running", res)
			}
		})
	}
}
