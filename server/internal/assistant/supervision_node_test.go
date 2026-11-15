package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/proto"
)

func TestSupervisionNodeCapabilityIsExplicit(t *testing.T) {
	for _, tc := range []struct {
		name   string
		answer any
		err    error
		ok     bool
	}{
		{"supported", map[string]any{"supervision_instructions": true}, nil, true},
		{"older node omits capability", map[string]any{"state": "running"}, nil, false},
		{"explicitly unsupported", map[string]any{"supervision_instructions": false}, nil, false},
		{"older node has no execution query", nil, errors.New("unknown method assistant.execution"), false},
		{"node unreachable", nil, errors.New("offline"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, f := workService(t)
			f.answerNode(assistantwire.DeviceAssistantExecution, func(_ string, raw json.RawMessage) (any, error) {
				var p map[string]string
				if err := json.Unmarshal(raw, &p); err != nil || p["session_id"] != "main" {
					t.Fatalf("capability query: %s", raw)
				}
				return tc.answer, tc.err
			})
			err := s.checkSupervisionNode(context.Background(), "box", "main")
			if (err == nil) != tc.ok {
				t.Fatalf("capability error = %v; want supported=%v", err, tc.ok)
			}
			if len(f.sends) != 0 || len(f.asked) != 0 {
				t.Fatal("capability probe dispatched model work")
			}
		})
	}
}

func TestSupervisionOldNodeRefusesDispatchBeforeModelWork(t *testing.T) {
	s, f := workService(t)
	enabled := true
	setSettings(t, s, func(v *Settings) {
		v.Main, v.MachineID, v.MainMachineID = "main", "box", "box"
		v.MainSupervisesOwnTasks = &enabled
	})
	f.setSessions("box", proto.Session{MachineID: "box", SessionID: "main", Assistant: true})
	f.answerNode(assistantwire.DeviceAssistantExecution, func(string, json.RawMessage) (any, error) {
		return map[string]any{"state": "stopped"}, nil
	})
	err := s.call(context.Background(), "box", DeviceAssistantSend, map[string]any{
		"session_id": "main", "text": "A facts-only supervision event", "supervision_instructions": "Stable policy",
	}, nil)
	if err == nil {
		t.Fatal("facts-only supervision was sent to an older node without system policy support")
	}
	if len(f.sends) != 0 || len(f.asked) != 0 {
		t.Fatalf("old node received model work: sends=%d, calls=%d", len(f.sends), len(f.asked))
	}
}
