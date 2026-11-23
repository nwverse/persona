package node

import (
	"context"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/proto"
)

// session.modes permission_mode on an agy pane, and every session.modes field on a cursor pane, is refused with its code
// (a 409 at the hub), and no key reaches the pane: on the Claude Code path it pressed shift+tab looking for Claude
// Code's input box and answered 502.
func TestWorkAgentPaneRefusesModes(t *testing.T) {
	for _, tc := range []struct {
		name, row string
		cases     []map[string]any
	}{
		{"agy", agyTestRow, []map[string]any{
			{"session_id": agyTestRow, "permission_mode": "acceptEdits"},
			{"session_id": agyTestRow, "permission_mode": proto.ModeBypass},
			{"session_id": agyTestRow, "permission_mode": "auto", "model": "other-model"},
		}},
		{"cursor", "cursor-w1-p1", []map[string]any{
			{"session_id": "cursor-w1-p1", "permission_mode": "acceptEdits"},
			{"session_id": "cursor-w1-p1", "permission_mode": proto.ModeBypass},
			{"session_id": "cursor-w1-p1", "effort": "high"},
			{"session_id": "cursor-w1-p1", "model": "other-model"},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n, f, home := herdrNode(t)
			if tc.name == "agy" {
				seedAgents(t, n, f, agyTestAgent(agyTestPane, home, ""))
			} else {
				seedAgents(t, n, f, agentOf(cursorAgent, "", proto.StatusIdle, agyTestPane, "w1:t1", "w1", home, "cursor"))
			}
			for _, params := range tc.cases {
				_, err := request(t, n, "session.modes", params)
				if refusalCode(err) != proto.CodeModesUnsupported {
					t.Fatalf("%v: err %v, want %s", params, err, proto.CodeModesUnsupported)
				}
			}
			if keys := f.trace("pane.send_keys", "pane.send_input"); len(keys) != 0 {
				t.Fatalf("keys reached the pane: %v", keys)
			}
		})
	}
}

// An agy started in Allow all says so on its row, offers model switching without permission chips, and herdr resumes it with the same flag.
func TestAgyPaneRowAndResumeKeepStartMode(t *testing.T) {
	n, f, home := herdrNode(t)
	seedAgents(t, n, f, agyTestAgent(agyTestPane, home, ""))
	n.noteWorkAgent(context.Background(), n.hosts.Get(host.Herdr).(*herdrHost), agyTestPane, agyTestRow, home, agyAgent, agyAgent, "test-model", "", agyStartMode(""))
	waitFor(t, "the agy resume report", func() bool { return len(f.trace("pane.report_agent_session")) > 0 })
	if got := f.trace("pane.report_agent_session"); !strings.Contains(got[0], `agy --model test-model --dangerously-skip-permissions"`) {
		t.Fatalf("resume command: %v", got)
	}
	var row *proto.Session
	for _, s := range listSessions(t, n) {
		if s.SessionID == agyTestRow {
			row = &s
		}
	}
	if row == nil {
		t.Fatal("no agy row")
	}
	c := row.Capabilities
	if row.PermissionMode != proto.ModeBypass || c == nil || c.Modes == nil || *c.Modes ||
		c.Model == nil || !*c.Model || !c.Read || !c.Send || !c.Interrupt || !c.Approve || !c.Terminal {
		t.Fatalf("row mode %q capabilities %+v", row.PermissionMode, c)
	}
}

// A stopped agy row keeps saying no capabilities: the manager and the apps read that as stopped (stoppedAgent).
func TestStoppedAgyPaneRowSaysNoCapabilities(t *testing.T) {
	n, f, home := herdrNode(t)
	a := agyTestAgent(agyTestPane, home, "")
	a["agent_status"] = proto.StatusOffline
	seedAgents(t, n, f, a)
	found := false
	for _, s := range listSessions(t, n) {
		if s.SessionID == agyTestRow {
			found = true
			if s.Status != proto.StatusOffline || s.Capabilities != nil {
				t.Fatalf("stopped agy row status %q capabilities %+v", s.Status, s.Capabilities)
			}
		}
	}
	if !found {
		t.Fatal("no stopped agy row")
	}
}

func TestAgyModeFlags(t *testing.T) {
	for mode, want := range map[string]string{
		"":               "",
		"default":        "",
		"acceptEdits":    " --mode accept-edits",
		"auto":           " --mode auto",
		proto.ModeBypass: " --dangerously-skip-permissions",
	} {
		if got := agyModeFlags(mode); got != want {
			t.Errorf("agyModeFlags(%q) = %q, want %q", mode, got, want)
		}
	}
	if got := agyStartMode(""); got != proto.ModeBypass {
		t.Errorf("default start mode %q", got)
	}
	if got := agyStartMode("acceptEdits"); got != "acceptEdits" {
		t.Errorf("named start mode %q", got)
	}
}
