package node

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// Switching a running Claude Code Agent to another launcher with its next message (launcher_switch.go, #5035).

// withShellConfigDir makes the login shell's $CLAUDE_CONFIG_DIR dir ("" for none) without running a shell.
func withShellConfigDir(t *testing.T, dir string) {
	t.Helper()
	old := shellClaudeConfigDir
	shellClaudeConfigDir = func(context.Context) string { return dir }
	t.Cleanup(func() { shellClaudeConfigDir = old })
}

// finishedTurn is a transcript whose last turn ended an hour ago: an idle agent with nothing in the background.
func finishedTurn(cwd string) []string {
	return []string{`{"type":"user","cwd":"` + cwd + `","message":{"role":"user","content":"hi"},"uuid":"u1"}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"hello"}]},"uuid":"a1","parentUuid":"u1"}`,
		`{"type":"system","subtype":"turn_duration","uuid":"d1","parentUuid":"a1","durationMs":1000}`}
}

// switchNode is a node with Claude session sidA in herdr pane w1:p1 with status (Persona started it on plain claude,
// its transcript in ~/.claude: finishedTurn when transcript is nil), the managed launchers work (a gateway running Claude Code), cx (Codex) and elsewhere
// (another Claude profile), no shell launchers, and an empty input box on the pane. persisted collects what the
// record writes through.
func switchNode(t *testing.T, status string, transcript func(cwd string) []string) (n *Node, f *fakeHerdr, home, cwd string, persisted func() []sessionState) {
	t.Helper()
	n, f, home = herdrNode(t)
	withShellConfigDir(t, "")
	withShellLaunchers(t)
	cwd = mkdir(t, filepath.Join(home, "proj"))
	if transcript == nil {
		transcript = finishedTurn
	}
	writeTranscript(t, home, cwd, sidA, time.Now().Add(-time.Hour), transcript(cwd)...)
	var mu sync.Mutex
	var puts []sessionState
	n.state.persist = func(id string, v sessionState) {
		if id == sidA {
			mu.Lock()
			puts = append(puts, v)
			mu.Unlock()
		}
	}
	n.state.put(sidA, sessionState{Launcher: "claude", Cwd: cwd, StartedAt: time.Now().Add(-2 * time.Hour).UTC()})
	// What a start asks of a launcher it does not know runs a login shell (codexFunctionLauncher): known here.
	probeCache.Lock()
	for _, name := range []string{"work", "elsewhere"} {
		probeCache.at[name], probeCache.got[name] = time.Now(), proto.LauncherDetail{Agent: "claude"}
	}
	probeCache.Unlock()
	t.Cleanup(func() {
		probeCache.Lock()
		for _, name := range []string{"work", "elsewhere"} {
			delete(probeCache.at, name)
			delete(probeCache.got, name)
		}
		probeCache.Unlock()
	})
	n.setManaged([]proto.ManagedLauncher{
		{Name: "work", BaseURL: "https://gateway.example.com", Model: "glm-5"},
		{Name: "cx", Agent: "codex", BaseURL: "https://codex.example.com"},
		{Name: "elsewhere", BaseURL: "https://gateway.example.com", Env: "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude-work")},
	})
	f.reply("session.snapshot", snapshot(nil, nil, []any{claudeAgent(sidA, status, "w1:p1", "w1:t1", "w1", cwd, "")}, nil))
	f.reply("pane.process_info", map[string]any{"process_info": map[string]any{"foreground_processes": []any{
		map[string]any{"pid": 1, "name": "zsh", "argv0": "-zsh"}}}})
	f.setScreens("w1:p1", screenFixture(t, "claude-2.1.312-empty-162.txt"), screenFixture(t, "claude-2.1.312-empty-162.ansi"))
	listSessions(t, n)
	// Persona started it: the refresh gives herdr its resume command in the background, once (backfillResume).
	waitCalls(t, f, "pane.report_agent_session", 1)
	f.reset()
	return n, f, home, cwd, func() []sessionState {
		mu.Lock()
		defer mu.Unlock()
		return append([]sessionState(nil), puts...)
	}
}

// rowOf is session id's row in a fresh list.
func rowOf(t *testing.T, n *Node, id string) proto.Session {
	t.Helper()
	for _, s := range listSessions(t, n) {
		if s.SessionID == id {
			return s
		}
	}
	t.Fatalf("%s is not listed", id)
	return proto.Session{}
}

// typedStarts are the commands starts typed into pane, in order.
func typedStarts(f *fakeHerdr, pane string) []string {
	var out []string
	for _, c := range f.calls() {
		if c.Method == "pane.send_input" && str(c.Params["pane_id"]) == pane {
			out = append(out, str(c.Params["text"]))
		}
	}
	return out
}

// Picking another launcher keeps it as the session's pending switch, in the record (persisted) and on its row, and
// touches nothing in the terminal; a new pick replaces it, and the session's own launcher drops it.
func TestLauncherSwitchIsPendingUntilCancelled(t *testing.T) {
	n, f, _, _, persisted := switchNode(t, proto.StatusIdle, nil)
	if _, err := request(t, n, "session.modes", map[string]any{"session_id": sidA, "launcher": "work", "model": "opus", "effort": "high"}); err != nil {
		t.Fatal(err)
	}
	if got := f.trace("pane.send_keys", "pane.send_input", "pane.send_text", "agent.prompt"); len(got) != 0 {
		t.Fatalf("picking a launcher drove the terminal: %q", got)
	}
	row := rowOf(t, n, sidA)
	if ps := row.PendingSwitch; ps == nil || ps.Launcher != "work" || ps.Model != "opus" || ps.Effort != "high" || ps.At.IsZero() {
		t.Fatalf("row's pending switch = %+v", ps)
	}
	if row.Launcher != "claude" {
		t.Errorf("row's launcher = %q before the switch ran", row.Launcher)
	}
	if puts := persisted(); len(puts) == 0 || puts[len(puts)-1].PendingSwitch == nil || puts[len(puts)-1].PendingSwitch.Launcher != "work" {
		t.Fatalf("the switch was not kept with the record: %+v", puts)
	}

	if _, err := request(t, n, "session.modes", map[string]any{"session_id": sidA, "launcher": "work"}); err != nil {
		t.Fatal(err)
	}
	if ps := n.pendingSwitch(sidA); ps == nil || ps.Model != "" || ps.Effort != "" {
		t.Fatalf("a new pick did not replace the old one: %+v", ps)
	}

	if _, err := request(t, n, "session.modes", map[string]any{"session_id": sidA, "launcher": "claude"}); err != nil {
		t.Fatal(err)
	}
	if ps := n.pendingSwitch(sidA); ps != nil {
		t.Fatalf("its own launcher left the switch pending: %+v", ps)
	}
	if rowOf(t, n, sidA).PendingSwitch != nil {
		t.Error("the row still shows the cancelled switch")
	}
	if puts := persisted(); puts[len(puts)-1].PendingSwitch != nil {
		t.Errorf("the cancel was not written through: %+v", puts[len(puts)-1])
	}
	if got := f.trace("pane.send_keys", "pane.send_input", "pane.send_text", "agent.prompt"); len(got) != 0 {
		t.Fatalf("cancelling drove the terminal: %q", got)
	}
}

// A session Persona did not start gets no record for a switch (a record means Persona started it): the switch waits in
// memory and shows on its row all the same.
func TestLauncherSwitchOfAnUnrecordedSessionStaysInMemory(t *testing.T) {
	n, _, _, _, _ := switchNode(t, proto.StatusIdle, nil)
	n.state.mu.Lock()
	delete(n.state.Sessions, sidA)
	n.state.mu.Unlock()
	if _, err := request(t, n, "session.modes", map[string]any{"session_id": sidA, "launcher": "work"}); err != nil {
		t.Fatal(err)
	}
	if _, recorded := n.state.get(sidA); recorded {
		t.Fatal("a switch made a record for a session Persona did not start")
	}
	if ps := rowOf(t, n, sidA).PendingSwitch; ps == nil || ps.Launcher != "work" {
		t.Fatalf("row's pending switch = %+v", ps)
	}
	if _, err := request(t, n, "session.modes", map[string]any{"session_id": sidA, "launcher": "claude"}); err != nil {
		t.Fatal(err)
	}
	if n.pendingSwitch(sidA) != nil {
		t.Error("its own launcher left the switch pending")
	}
}

// What a switch is refused for when it is picked, with nothing kept and nothing typed.
func TestLauncherSwitchRefusals(t *testing.T) {
	for _, tc := range []struct {
		name   string
		setup  func(t *testing.T, n *Node, f *fakeHerdr, home, cwd string)
		params map[string]any
		code   string
	}{
		{"launcher not on this computer", nil, map[string]any{"launcher": "nowhere"}, proto.CodeSwitchLauncherMissing},
		{"launcher of another agent", nil, map[string]any{"launcher": "cx"}, proto.CodeSwitchOtherAgent},
		{"another config directory", nil, map[string]any{"launcher": "elsewhere"}, proto.CodeSwitchConfigDir},
		{"the shell's own config directory is another", func(t *testing.T, n *Node, f *fakeHerdr, home, cwd string) {
			withShellConfigDir(t, filepath.Join(home, ".claude-team"))
		}, map[string]any{"launcher": "work"}, proto.CodeSwitchConfigDir},
		{"a locked model", func(t *testing.T, n *Node, f *fakeHerdr, home, cwd string) {
			withShellLaunchers(t, "fixed")
			probeCache.Lock()
			probeCache.at["fixed"], probeCache.got["fixed"] = time.Now(), proto.LauncherDetail{Agent: "claude"}
			probeCache.Unlock()
			modelProbeCache.Lock()
			modelProbeCache.results[n.home+"\x00fixed\x00opus"] = modelProbeResult{at: time.Now(), agent: "claude", err: fmt.Errorf("fixed passes its own --model")}
			modelProbeCache.Unlock()
			t.Cleanup(func() {
				probeCache.Lock()
				delete(probeCache.at, "fixed")
				delete(probeCache.got, "fixed")
				probeCache.Unlock()
			})
		}, map[string]any{"launcher": "fixed", "model": "opus"}, proto.CodeSwitchModelLocked},
		{"a Codex agent", func(t *testing.T, n *Node, f *fakeHerdr, home, cwd string) {
			f.reply("session.snapshot", snapshot(nil, nil, []any{agentOf("codex", sidA, proto.StatusIdle, "w1:p1", "w1:t1", "w1", cwd, "")}, nil))
			listSessions(t, n)
		}, map[string]any{"launcher": "work"}, proto.CodeSwitchUnsupported},
		{"an assistant conversation", func(t *testing.T, n *Node, f *fakeHerdr, home, cwd string) {
			n.mu.Lock()
			n.procs[sidA] = &assistantProc{}
			n.mu.Unlock()
		}, map[string]any{"launcher": "work"}, proto.CodeSwitchUnsupported},
		{"not running", func(t *testing.T, n *Node, f *fakeHerdr, home, cwd string) {
			f.reply("session.snapshot", snapshot(nil, nil, nil, nil))
			listSessions(t, n)
		}, map[string]any{"launcher": "work"}, proto.CodeModesNotRunning},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n, f, home, cwd, _ := switchNode(t, proto.StatusIdle, nil)
			if tc.setup != nil {
				tc.setup(t, n, f, home, cwd)
			}
			f.reset()
			params := map[string]any{"session_id": sidA}
			for k, v := range tc.params {
				params[k] = v
			}
			_, err := request(t, n, "session.modes", params)
			if code, data := refusalOf(t, err); code != tc.code || data["delivered"] != deliveredNone {
				t.Fatalf("refused with %q (%v), want %q", code, err, tc.code)
			}
			if ps := n.pendingSwitch(sidA); ps != nil {
				t.Errorf("a refused switch was kept: %+v", ps)
			}
			if got := f.trace("pane.send_keys", "pane.send_input", "pane.send_text", "agent.prompt"); len(got) != 0 {
				t.Errorf("a refused switch drove the terminal: %q", got)
			}
		})
	}
}

// A launcher of another profile whose directory holds the conversation (projects/ shared through a symlink) can take
// it: --resume there finds it at its latest.
func TestLauncherSwitchToAProfileSharingTheConversation(t *testing.T) {
	n, _, home, _, _ := switchNode(t, proto.StatusIdle, nil)
	if err := os.MkdirAll(filepath.Join(home, ".claude-work"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, ".claude", "projects"), filepath.Join(home, ".claude-work", "projects")); err != nil {
		t.Fatal(err)
	}
	if _, err := request(t, n, "session.modes", map[string]any{"session_id": sidA, "launcher": "elsewhere"}); err != nil {
		t.Fatal(err)
	}
	if ps := n.pendingSwitch(sidA); ps == nil || ps.Launcher != "elsewhere" {
		t.Fatalf("pending switch = %+v", ps)
	}
}

// The next message carries the switch out: the agent is quit in its pane (Ctrl+C twice, the pane kept), the
// conversation resumed through the new launcher in the same shell with the model picked, the record moved to it and
// the switch cleared; then the message goes to the new agent.
func TestLauncherSwitchOnTheNextMessage(t *testing.T) {
	n, f, _, cwd, _ := switchNode(t, proto.StatusIdle, nil)
	if _, err := request(t, n, "session.modes", map[string]any{"session_id": sidA, "launcher": "work", "model": "opus"}); err != nil {
		t.Fatal(err)
	}
	f.reset()
	quitsOn(f, "w1:p1", sidA, nil, shellPane("w1:p1", "w1:t1", "w1", cwd, sidA))
	f.reply("agent.get", map[string]any{"agent": map[string]any{"agent": "claude", "agent_status": proto.StatusIdle, "pane_id": "w1:p1",
		"tab_id": "w1:t1", "workspace_id": "w1"}})
	if _, err := request(t, n, "session.send", map[string]any{"session_id": sidA, "text": "hello"}); err != nil {
		t.Fatalf("send: %v\n%s", err, strings.Join(f.trace(), "\n"))
	}
	if got := f.trace("tab.create", "workspace.create", "tab.close", "pane.close"); len(got) != 0 {
		t.Errorf("a tab was opened or closed: %q", got)
	}
	starts := typedStarts(f, "w1:p1")
	if len(starts) != 1 || !strings.Contains(starts[0], " launch work ") || !strings.Contains(starts[0], "--resume "+sidA) ||
		!strings.Contains(starts[0], "--model opus") {
		t.Fatalf("typed %q", starts)
	}
	trace := strings.Join(f.trace("pane.send_keys", "pane.send_input", "agent.prompt"), "\n")
	quit := strings.Index(trace, sendKeys("w1:p1", "ctrl+c"))
	start := strings.Index(trace, "pane.send_input")
	prompt := strings.Index(trace, call("agent.prompt", map[string]any{"target": "w1:p1", "text": "hello"}))
	if quit < 0 || start < quit || prompt < start {
		t.Fatalf("not quit, started, then prompted:\n%s", trace)
	}
	st, _ := n.state.get(sidA)
	if st.Launcher != "work" || st.Model != "opus" || st.PendingSwitch != nil || st.Cwd != cwd {
		t.Errorf("record after the switch = %+v", st)
	}
	if n.pendingSwitch(sidA) != nil {
		t.Error("the switch is still pending")
	}
}

// A message while the agent works, or has background work a quit would end, is refused with nothing pressed or typed,
// and the switch waits on.
func TestLauncherSwitchWaitsForAnIdleAgent(t *testing.T) {
	ts := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	for _, tc := range []struct {
		name, status string
		transcript   func(cwd string) []string
		code         string
	}{
		{"working", proto.StatusWorking, nil, proto.CodeSwitchBusy},
		{"background command", proto.StatusIdle, func(cwd string) []string {
			return append(finishedTurn(cwd),
				`{"type":"assistant","uuid":"a2","timestamp":"`+ts+`","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu1","name":"Bash","input":{"command":"sleep 600","run_in_background":true}}]}}`,
				`{"type":"user","uuid":"u2","timestamp":"`+ts+`","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu1","content":"Command running in background with ID: b1. Output is being written to: /tmp/tasks/b1.output"}]}}`,
				`{"type":"system","subtype":"turn_duration","uuid":"d2","parentUuid":"u2","durationMs":1000}`)
		}, proto.CodeSwitchBackground},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n, f, _, _, _ := switchNode(t, tc.status, tc.transcript)
			n.state.setPendingSwitch(sidA, &proto.PendingSwitch{Launcher: "work", At: time.Now()})
			f.reset()
			_, err := request(t, n, "session.send", map[string]any{"session_id": sidA, "text": "hello"})
			if code, _ := refusalOf(t, err); code != tc.code {
				t.Fatalf("refused with %q (%v), want %q", code, err, tc.code)
			}
			if got := f.trace("pane.send_keys", "pane.send_input", "pane.send_text", "agent.prompt"); len(got) != 0 {
				t.Errorf("drove the terminal: %q", got)
			}
			if ps := n.pendingSwitch(sidA); ps == nil || ps.Launcher != "work" {
				t.Errorf("the switch no longer waits: %+v", ps)
			}
		})
	}
}

// A switch whose new launcher does not come up puts the record back and resumes the agent on its own launcher in the
// same shell: the message is not typed, the switch stays pending, and the refusal says so. When the own launcher does
// not come up either, the agent has stopped, and the refusal says that.
func TestLauncherSwitchFailureRollsBack(t *testing.T) {
	for _, tc := range []struct {
		name   string
		backUp bool
		code   string
	}{
		{"back on its own launcher", true, proto.CodeSwitchFailed},
		{"stopped", false, proto.CodeSwitchFailedStopped},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n, f, _, cwd, _ := switchNode(t, proto.StatusIdle, nil)
			n.claudeStart, n.claudeReady = time.Second, time.Second
			if _, err := request(t, n, "session.modes", map[string]any{"session_id": sidA, "launcher": "work", "model": "opus"}); err != nil {
				t.Fatal(err)
			}
			f.reset()
			quitsOn(f, "w1:p1", sidA, nil, shellPane("w1:p1", "w1:t1", "w1", cwd, sidA))
			var mu sync.Mutex
			own := false // the last start typed is the one of its own launcher
			f.after(func(c herdrCall) {
				if c.Method == "pane.send_input" {
					mu.Lock()
					own = !strings.Contains(str(c.Params["text"]), " launch work ")
					mu.Unlock()
				}
			})
			f.handle("agent.get", func(map[string]any) any {
				mu.Lock()
				defer mu.Unlock()
				if !own || !tc.backUp {
					return map[string]any{"agent": nil} // the shell never runs claude
				}
				return map[string]any{"agent": map[string]any{"agent": "claude", "agent_status": proto.StatusIdle, "pane_id": "w1:p1",
					"tab_id": "w1:t1", "workspace_id": "w1"}}
			})
			_, err := request(t, n, "session.send", map[string]any{"session_id": sidA, "text": "hello"})
			if code, data := refusalOf(t, err); code != tc.code || data["delivered"] != deliveredNone {
				t.Fatalf("refused with %q (%v), want %q", code, err, tc.code)
			}
			if got := f.trace("agent.prompt"); len(got) != 0 {
				t.Fatalf("the message was typed: %q", got)
			}
			if got := f.trace("tab.close", "pane.close"); len(got) != 0 {
				t.Errorf("closed the agent's terminal: %q", got)
			}
			starts := typedStarts(f, "w1:p1")
			if len(starts) != 2 || !strings.Contains(starts[0], " launch work ") ||
				strings.Contains(starts[1], " launch work ") || !strings.Contains(starts[1], "--resume "+sidA) {
				t.Fatalf("typed %q", starts)
			}
			st, _ := n.state.get(sidA)
			if st.Launcher != "claude" || st.Model != "" {
				t.Errorf("record after the failed switch = %+v", st)
			}
			if ps := n.pendingSwitch(sidA); ps == nil || ps.Launcher != "work" || ps.Model != "opus" {
				t.Errorf("the switch no longer waits: %+v", ps)
			}
		})
	}
}

// A message while no switch waits, or whose switch names the launcher the agent runs on by now, is sent as always.
func TestLauncherSwitchToItsOwnLauncherIsNoSwitch(t *testing.T) {
	n, f, _, _, _ := switchNode(t, proto.StatusIdle, nil)
	n.state.setPendingSwitch(sidA, &proto.PendingSwitch{Launcher: "claude", At: time.Now()})
	if _, err := request(t, n, "session.send", map[string]any{"session_id": sidA, "text": "hello"}); err != nil {
		t.Fatal(err)
	}
	if got := f.trace("pane.send_keys", "pane.send_input"); len(got) != 0 {
		t.Errorf("quit or restarted the agent: %q", got)
	}
	if got := f.trace("agent.prompt"); len(got) != 1 {
		t.Errorf("prompts = %q", got)
	}
	if n.pendingSwitch(sidA) != nil {
		t.Error("the switch still waits")
	}
}

// The session's own launcher with a model drops a switch that waits and switches the model as without a launcher
// (model_switch.go): an id that is no model is refused there, as always.
func TestLauncherSwitchOwnLauncherWithAModelGoesOn(t *testing.T) {
	n, f, _, _, _ := switchNode(t, proto.StatusIdle, nil)
	n.state.setPendingSwitch(sidA, &proto.PendingSwitch{Launcher: "work", At: time.Now()})
	_, err := request(t, n, "session.modes", map[string]any{"session_id": sidA, "launcher": "claude", "model": "-rf"})
	if err == nil || !strings.Contains(err.Error(), "invalid model") {
		t.Fatalf("own launcher with a model: %v", err)
	}
	if n.pendingSwitch(sidA) != nil {
		t.Error("the switch still waits")
	}
	if got := f.trace("pane.send_keys", "pane.send_input", "agent.prompt"); len(got) != 0 {
		t.Errorf("drove the terminal: %q", got)
	}
}

// The user's Resume of a stopped agent whose switch waits brings it back on the launcher it ran on, the switch still
// waiting for the next message.
func TestResumeKeepsAPendingSwitch(t *testing.T) {
	n, _, _ := keptAgent(t)
	n.claudeStart, n.claudeReady = 200*time.Millisecond, 200*time.Millisecond // the fake shell never runs claude
	n.state.setPendingSwitch(sidA, &proto.PendingSwitch{Launcher: "work", Model: "opus", At: time.Now()})
	_, _ = request(t, n, "session.start", map[string]any{"resume_session_id": sidA})
	st, _ := n.state.get(sidA)
	if st.Launcher != "claude" || st.PendingSwitch == nil || st.PendingSwitch.Launcher != "work" {
		t.Fatalf("record after the resume = %+v", st)
	}
}

// An agent that does not quit runs on as it was: nothing is started or typed, and the switch waits.
func TestLauncherSwitchWhenTheAgentDoesNotQuit(t *testing.T) {
	withStop(t, 1, 100*time.Millisecond)
	n, f, _, _, _ := switchNode(t, proto.StatusIdle, nil)
	n.state.setPendingSwitch(sidA, &proto.PendingSwitch{Launcher: "work", Model: "opus", At: time.Now()})
	_, err := request(t, n, "session.send", map[string]any{"session_id": sidA, "text": "hello"})
	if code, _ := refusalOf(t, err); code != proto.CodeSwitchNotStopped {
		t.Fatalf("refused with %q (%v)", code, err)
	}
	if got := f.trace("pane.send_input", "agent.prompt"); len(got) != 0 {
		t.Errorf("started or typed: %q", got)
	}
	if st, _ := n.state.get(sidA); st.Launcher != "claude" || st.PendingSwitch == nil {
		t.Errorf("record = %+v", st)
	}
}
