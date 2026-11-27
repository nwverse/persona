package node

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// A turn that runs or waits takes Escape. Once the turn is positively over, #10827 sends none: an idle-box Escape can
// quit Claude. If background work still runs, the answer is turn_over_background rather than a successful interruption;
// with none left it is settled + not_running. Working and blocked sessions are answered as before (#10828).
func TestInterruptWithOnlyBackgroundWorkSaysSo(t *testing.T) {
	now := time.Now().UTC().Format(time.RFC3339)
	started := []string{
		`{"type":"assistant","uuid":"a2","timestamp":"` + now + `","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu1","name":"Bash","input":{"command":"sleep 600","run_in_background":true}}]}}`,
		`{"type":"user","uuid":"u2","timestamp":"` + now + `","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu1","content":"Command running in background with ID: b1. Output is being written to: /tmp/tasks/b1.output"}]}}`,
		`{"type":"system","subtype":"turn_duration","uuid":"d2","parentUuid":"u2","durationMs":1000}`,
	}
	// The command's notification woke the session, which answered and ended that turn too: nothing runs any more.
	finished := []string{
		`{"type":"user","uuid":"u3","timestamp":"` + now + `","message":{"role":"user","content":"<task-notification>\n<task-id>b1</task-id>\n<status>completed</status>\n<summary>Background command \"sleep\" completed (exit code 0)</summary>\n</task-notification>"}}`,
		`{"type":"assistant","uuid":"a3","parentUuid":"u3","timestamp":"` + now + `","message":{"role":"assistant","content":[{"type":"text","text":"done"}]}}`,
		`{"type":"system","subtype":"turn_duration","uuid":"d3","parentUuid":"a3","durationMs":1000}`,
	}
	for _, tc := range []struct {
		name    string
		status  string
		lines   []string
		esc     bool
		refused bool
	}{
		{"turn over, the command still runs", proto.StatusIdle, started, false, true},
		{"turn over and done, the command still runs", proto.StatusDone, started, false, true},
		{"working, with the command in the background", proto.StatusWorking, started, true, false},
		{"waiting on the user", proto.StatusBlocked, started, true, false},
		{"turn over, nothing in the background", proto.StatusIdle, nil, false, false},
		{"turn over, the command has finished", proto.StatusIdle, append(append([]string{}, started...), finished...), false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n, f, home := herdrNode(t)
			seedAgents(t, n, f, claudeAgent(sidA, tc.status, "w1:p1", "w1:t1", "w1", "/tmp/m", ""))
			writeTranscript(t, home, "/tmp/m", sidA, time.Now(), append(finishedTurn("/tmp/m"), tc.lines...)...)
			f.reset()
			res, err := request(t, n, "session.interrupt", map[string]any{"session_id": sidA})
			keys := f.trace("pane.send_keys", "pane.send_input")
			if tc.esc && (len(keys) != 1 || !strings.Contains(keys[0], "esc")) {
				t.Fatalf("keys %q: want one Escape", keys)
			}
			if !tc.esc && len(keys) != 0 {
				t.Fatalf("keys %q: a finished turn takes no Escape (#10827)", keys)
			}
			if !tc.refused {
				if err != nil {
					t.Fatalf("err = %v, want the stop taken", err)
				}
				if !tc.esc && (res["settled"] != true || res["not_running"] != true) {
					t.Fatalf("answer %v: want settled and not_running", res)
				}
				return
			}
			var re *rpc.Error
			if !errors.As(err, &re) || re.Code != proto.CodeTurnOverBackground {
				t.Fatalf("err = %v, want %s", err, proto.CodeTurnOverBackground)
			}
		})
	}
}

// The words exist in every language, and are not another language's (a missing key falls back).
func TestTurnOverBackgroundWords(t *testing.T) {
	seen := map[string]i18n.Lang{}
	for _, l := range []i18n.Lang{i18n.En, i18n.ZhHans, i18n.Ja} {
		got := i18n.T(l, "rpc-errors."+proto.CodeTurnOverBackground)
		if got == "" || strings.Contains(got, "rpc-errors.") {
			t.Errorf("%s: no words: %q", l, got)
		}
		if other, dup := seen[got]; dup {
			t.Errorf("%s says the same as %s: a missing translation", l, other)
		}
		seen[got] = l
	}
}
