package node

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// session.interrupt sends Esc to a Claude pane only while its turn runs (working, or blocked on a card). On an idle box
// Esc quits claude (docs/lessons/agents.md "Esc on an idle Claude input box exits claude"): a stop that arrives after the
// turn ended (a stale "working" in the app, a second tap, the assistant's agent_interrupt after a phone confirmation)
// presses nothing and answers that the turn is over (settled, not_running). An unknown status is not proof of rest: Esc
// retains its old behavior there rather than leaving a genuinely running turn alive while reporting it stopped.
func TestInterruptSendsNoEscToAnIdleClaude(t *testing.T) {
	for _, tc := range []struct {
		status string
		esc    bool
	}{
		{proto.StatusIdle, false},
		{proto.StatusDone, false},
		{proto.StatusUnknown, true},
		{proto.StatusWorking, true},
		{proto.StatusBlocked, true},
	} {
		t.Run(tc.status, func(t *testing.T) {
			n, f, _ := herdrNode(t)
			seedAgents(t, n, f, claudeAgent(sidA, tc.status, "w1:p1", "w1:t1", "w1", "/tmp/k", ""))
			f.setScreen("w1:p1", screenFixture(t, "claude-2.1.312-empty-162.txt"))
			res, err := request(t, n, "session.interrupt", map[string]any{"session_id": sidA})
			if err != nil {
				t.Fatal(err)
			}
			if tc.esc {
				assertTrace(t, f.trace(), []string{sendKeys("w1:p1", "esc")})
				return
			}
			if got := f.trace("pane.send_keys"); len(got) != 0 {
				t.Fatalf("Esc went to an idle Claude: %v", got)
			}
			if res["not_running"] != true || res["settled"] != true {
				t.Fatalf("answer %v: want settled and not_running", res)
			}
		})
	}
}

// A host's idle/done can be stale through a long, quiet tool call. Only a transcript with no open turn (or one explicitly
// closed) makes that host state safe for the no-Esc shortcut. A closed turn is the suppression-side control.
func TestInterruptChecksTheTranscriptBeforeSkippingEsc(t *testing.T) {
	for _, tc := range []struct {
		name   string
		closed bool
	}{
		{"an old open turn still takes Esc", false},
		{"a closed turn takes none", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n, f, home := herdrNode(t)
			stamp := time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339Nano)
			lines := []string{
				`{"type":"user","uuid":"u1","message":{"role":"user","content":"run the long check"},"timestamp":"` + stamp + `"}`,
				`{"type":"assistant","uuid":"a1","parentUuid":"u1","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"./check"}}]},"timestamp":"` + stamp + `"}`,
			}
			if tc.closed {
				lines = append(lines, `{"type":"system","subtype":"turn_duration","uuid":"d1","parentUuid":"a1","durationMs":1000,"timestamp":"`+stamp+`"}`)
			}
			writeTranscript(t, home, "/tmp/k", sidA, time.Now().Add(-2*time.Hour), lines...)
			seedAgents(t, n, f, claudeAgent(sidA, proto.StatusIdle, "w1:p1", "w1:t1", "w1", "/tmp/k", ""))
			f.setScreen("w1:p1", screenFixture(t, "claude-2.1.312-empty-162.txt"))
			res, err := request(t, n, "session.interrupt", map[string]any{"session_id": sidA})
			if err != nil {
				t.Fatal(err)
			}
			keys := f.trace("pane.send_keys")
			if !tc.closed {
				assertTrace(t, f.trace("pane.send_keys"), []string{sendKeys("w1:p1", "esc")})
				return
			}
			if len(keys) != 0 || res["not_running"] != true || res["settled"] != true {
				t.Fatalf("closed turn: keys %v, answer %v", keys, res)
			}
		})
	}
}

// paneFor can discover a host agent only after the initial lookup. The interrupt guard must be applied to that refreshed
// agent too, not only to one already cached when the request began.
func TestInterruptGuardsAClaudeDiscoveredByThePaneRefresh(t *testing.T) {
	n, f, _ := herdrNode(t)
	f.reply("session.snapshot", snapshot(nil, nil, []any{
		claudeAgent(sidA, proto.StatusIdle, "w1:p1", "w1:t1", "w1", "/tmp/k", ""),
	}, nil))
	f.setScreen("w1:p1", screenFixture(t, "claude-2.1.312-empty-162.txt"))
	res, err := request(t, n, "session.interrupt", map[string]any{"session_id": sidA})
	if err != nil {
		t.Fatal(err)
	}
	if got := f.trace("pane.send_keys"); len(got) != 0 || res["not_running"] != true || res["settled"] != true {
		t.Fatalf("newly discovered idle Claude: keys %v, answer %v", got, res)
	}
}

// A resting host row can be blocked by a dialog found on its screen. Esc declines that dialog; it is not an idle-box quit.
func TestInterruptStillEscapesADialogOnARestingClaude(t *testing.T) {
	n, f, _ := herdrNode(t)
	seedAgents(t, n, f, claudeAgent(sidA, proto.StatusIdle, "w1:p1", "w1:t1", "w1", "/tmp/k", ""))
	f.setScreen("w1:p1", screenFixture(t, "claude-2.1.316-danger-rm-120.txt"))
	if _, err := request(t, n, "session.interrupt", map[string]any{"session_id": sidA}); err != nil {
		t.Fatal(err)
	}
	assertTrace(t, f.trace("pane.send_keys"), []string{sendKeys("w1:p1", "esc")})
}

// The tmux route has no initial n.agent entry. Apply the same decision to Claude's own status file and its actual pane:
// idle/shell is settled, busy/waiting takes Esc, and an unknown status keeps the previous Esc behavior. A dialog found on
// an otherwise idle pane remains a blocked-side control.
func TestInterruptAppliesTheIdleGuardToAnOutsideTmuxClaude(t *testing.T) {
	for _, tc := range []struct {
		status string
		dialog bool
		esc    bool
	}{
		{status: "idle", esc: false},
		{status: "shell", esc: false},
		{status: "busy", esc: true},
		{status: "waiting", esc: true},
		{status: "unknown", esc: true},
		{status: "idle", dialog: true, esc: true},
	} {
		t.Run(tc.status+map[bool]string{true: " with dialog", false: ""}[tc.dialog], func(t *testing.T) {
			plain, ansi := emptyOwnerTmuxScreens(t)
			if tc.dialog {
				plain = screenFixture(t, "claude-2.1.316-danger-rm-120.txt")
			}
			env := newOwnerTmuxTestNode(t, tc.status, plain, ansi, false, false)
			res, err := request(t, env.n, "session.interrupt", map[string]any{"session_id": sidA})
			if err != nil {
				t.Fatal(err)
			}
			keys := env.pane.writeCount()
			if tc.esc {
				if keys != 1 || res["not_running"] == true {
					t.Fatalf("%s: writes %d, answer %v; want one Esc", tc.status, keys, res)
				}
				return
			}
			if keys != 0 || res["not_running"] != true || res["settled"] != true {
				t.Fatalf("%s: writes %d, answer %v; want settled without Esc", tc.status, keys, res)
			}
		})
	}
}

// The outside-tmux route has no n.agent for turnOver. Its own idle/shell status supplies the resting evidence instead;
// background work must still produce the #10828 refusal, with no Esc into the idle box.
func TestInterruptOutsideTmuxKeepsTheBackgroundResult(t *testing.T) {
	plain, ansi := emptyOwnerTmuxScreens(t)
	env := newOwnerTmuxTestNode(t, "idle", plain, ansi, false, false)
	now := time.Now().UTC().Format(time.RFC3339)
	appendLines(t, env.transcript,
		`{"type":"assistant","uuid":"a2","timestamp":"`+now+`","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu1","name":"Bash","input":{"command":"sleep 600","run_in_background":true}}]}}`,
		`{"type":"user","uuid":"u2","timestamp":"`+now+`","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu1","content":"Command running in background with ID: b1. Output is being written to: /tmp/tasks/b1.output"}]}}`,
		`{"type":"system","subtype":"turn_duration","uuid":"d2","parentUuid":"u2","durationMs":1000}`,
	)
	_, err := request(t, env.n, "session.interrupt", map[string]any{"session_id": sidA})
	var re *rpc.Error
	if !errors.As(err, &re) || re.Code != proto.CodeTurnOverBackground {
		t.Fatalf("err = %v, want %s", err, proto.CodeTurnOverBackground)
	}
	if got := env.pane.writeCount(); got != 0 {
		t.Fatalf("writes %d: an idle outside Claude got Esc", got)
	}
}

// A transcript that cannot be stat'ed for a reason other than absence is uncertain, not a closed turn. Keep the old Esc
// behavior rather than telling the caller a possibly running turn is settled.
func TestInterruptKeepsEscWhenTheTranscriptStatFails(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		esc  bool
	}{
		{name: "absent", err: &os.PathError{Op: "stat", Path: "transcript", Err: os.ErrNotExist}, esc: false},
		{name: "read failure", err: &os.PathError{Op: "stat", Path: "transcript", Err: os.ErrPermission}, esc: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n, f, home := herdrNode(t)
			stamp := time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339Nano)
			writeTranscript(t, home, "/tmp/k", sidA, time.Now().Add(-2*time.Hour),
				`{"type":"user","uuid":"u1","message":{"role":"user","content":"run the long check"},"timestamp":"`+stamp+`"}`,
				`{"type":"assistant","uuid":"a1","parentUuid":"u1","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"./check"}}]},"timestamp":"`+stamp+`"}`,
				`{"type":"system","subtype":"turn_duration","uuid":"d1","parentUuid":"a1","durationMs":1000,"timestamp":"`+stamp+`"}`,
			)
			seedAgents(t, n, f, claudeAgent(sidA, proto.StatusIdle, "w1:p1", "w1:t1", "w1", "/tmp/k", ""))
			f.setScreen("w1:p1", screenFixture(t, "claude-2.1.312-empty-162.txt"))
			old := interruptStat
			statCalled := false
			interruptStat = func(string) (os.FileInfo, error) {
				statCalled = true
				return nil, tc.err
			}
			t.Cleanup(func() { interruptStat = old })
			res, err := request(t, n, "session.interrupt", map[string]any{"session_id": sidA})
			if err != nil {
				t.Fatal(err)
			}
			if !statCalled {
				t.Fatal("the transcript stat failure was not injected into the interrupt path")
			}
			if tc.esc {
				assertTrace(t, f.trace("pane.send_keys"), []string{sendKeys("w1:p1", "esc")})
				return
			}
			if got := f.trace("pane.send_keys"); len(got) != 0 || res["settled"] != true || res["not_running"] != true {
				t.Fatalf("absent transcript: keys %v, answer %v; want settled without Esc", got, res)
			}
		})
	}
}
