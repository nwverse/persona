package node

import (
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

// An agy subagent names its parent and root by the companion's ids (#14817). While a live herdr pane lists the parent
// conversation, those ids name the pane's row; while the companion's own row lists it (the pane stopped), they stay.
func TestAgyPaneParentsItsSubagents(t *testing.T) {
	n, herdr, home := herdrNode(t)
	const (
		parentRow     = "agy-" + agyTestID
		childRow      = "agy-" + agyOtherTestID
		grandchildRow = "agy-33333333-4444-4555-8666-777777777777"
		askingRow     = "agy-44444444-5555-4666-8777-888888888888"
	)
	n.runtimes = newRuntimeRegistry()
	runtime := n.runtimes.add(nil)
	runtime.ready, runtime.hello.Runtime = true, agyAgent
	asking := proto.Session{SessionID: askingRow, Agent: agyAgent, Cwd: home, Status: proto.StatusBlocked}
	asking.SetAskingParentSessionID(parentRow) // a blocked child, as the node keeps it (runtimes_events.go)
	n.runtimes.setSessions(runtime, []proto.Session{
		{SessionID: parentRow, Agent: agyAgent, Cwd: home, Status: proto.StatusIdle},
		{SessionID: childRow, Agent: agyAgent, Cwd: home, Status: proto.StatusWorking,
			IsSubagent: true, ParentSessionID: parentRow, RootSessionID: parentRow},
		{SessionID: grandchildRow, Agent: agyAgent, Cwd: home, Status: proto.StatusWorking,
			IsSubagent: true, ParentSessionID: childRow, RootSessionID: parentRow},
		asking,
	})
	for _, tc := range []struct {
		name, status, parent string
	}{
		{name: "live pane", status: proto.StatusIdle, parent: agyTestRow},
		{name: "stopped pane", status: proto.StatusOffline, parent: parentRow},
		{name: "no pane", parent: parentRow},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var agents []any
			if tc.status != "" {
				pane := agyTestAgent(agyTestPane, home, agyTestID)
				pane["agent_status"] = tc.status
				agents = append(agents, pane)
			}
			herdr.reply("session.snapshot", snapshot(nil, nil, agents, nil))
			rows := map[string]proto.Session{}
			for _, row := range listSessions(t, n) {
				rows[row.SessionID] = row
			}
			if _, ok := rows[tc.parent]; !ok {
				t.Fatalf("the parent's row %s is not listed: %v", tc.parent, rows)
			}
			for id, want := range map[string][3]string{
				childRow:      {tc.parent, tc.parent, ""},
				grandchildRow: {childRow, tc.parent, ""},
				askingRow:     {"", "", tc.parent},
			} {
				row, ok := rows[id]
				if !ok {
					t.Fatalf("%s is not listed", id)
				}
				if got := [3]string{row.ParentSessionID, row.RootSessionID, row.AskingParentSessionID()}; got != want {
					t.Errorf("%s parent, root, asking parent = %q, want %q", id, got, want)
				}
			}
		})
	}
}
