package assistant

import (
	"encoding/json"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

// The node lists a native Claude Code conversation without an agent. Its first explicit switch adopts it as a
// Claude Code segment instead of refusing it as an unknown engine (live acceptance, 2026-11-21).
func TestLogicalSwitchAdoptsANativeClaudeRowWithoutAnAgent(t *testing.T) {
	s, f, d, _ := historyFixture(t, func(string, ...any) {})
	ctx := t.Context()
	f.known["fresh-chat"] = true
	f.setSessions("server", proto.Session{SessionID: "fresh-chat", Assistant: true, Rev: 100})
	f.answerNode("assistant.segment.check", func(string, json.RawMessage) (any, error) {
		return segmentTarget{Registered: true, Engine: "codex", SupervisionPolicy: true, Prepare: true}, nil
	})
	if _, err := s.beginLogicalSwitch(ctx, "server", "fresh-chat", logicalSwitchRequest{OperationID: "first", ExpectedEpoch: 1, MachineID: "server", Launcher: "codex"}); err != nil {
		t.Fatalf("native Claude Code source refused: %v", err)
	}
	c, err := d.LogicalConversation(ctx, "server", "fresh-chat")
	if err != nil || c == nil || len(c.Segments) != 1 || c.Segments[0].Engine != "claude" || c.Switch == nil || c.Switch.OperationID != "first" {
		t.Fatalf("adopted source: %+v %v", c, err)
	}
	// A runtime's row without an agent is still unknown: nothing is guessed for it.
	if got := sessionEngineAgent(proto.Session{SourceKind: proto.SourceRuntime}); got != "" {
		t.Fatalf("runtime without agent guessed as %q", got)
	}
	if got := sessionEngineAgent(proto.Session{Agent: "codex"}); got != "codex" {
		t.Fatalf("listed agent replaced: %q", got)
	}
}

// An adopted conversation's row is still read from the manager's fleet, not sent to a node as session.get (which
// no node knows): otherwise the conversation looks gone and its segments state and every later switch refuse it.
func TestLogicalAdoptedConversationRowIsReadFromTheFleet(t *testing.T) {
	s, _, _, _ := logicalPair(t, "codex")
	ctx := t.Context()
	raw := newFleetDevs()
	raw.add("server", "Computer")
	raw.setSessions("server", []proto.Session{
		{SessionID: "side-chat", Cwd: "/home/u/.persona/assistant", Assistant: true, Agent: "claude", Rev: 100},
		{SessionID: "target-chat", Cwd: "/home/u/.persona/assistant", Assistant: true, Agent: "codex", Rev: 100},
	})
	s.devs = &logicalDevices{Devices: raw, s: s}
	s.logicalChanged()
	row, ok := s.session(ctx, "server", "side-chat")
	if !ok || !row.Assistant || row.SessionID != "side-chat" {
		t.Fatalf("adopted conversation row: %+v %v", row, ok)
	}
	if calls := raw.callsTo("server", DeviceSessionGet); len(calls) != 0 {
		t.Fatalf("session.get reached the node: %s", calls)
	}
	if !s.keepsPickOn(ctx, "side-chat", "server") {
		t.Fatal("adopted conversation lost its pick (segments state refuses it)")
	}
}
