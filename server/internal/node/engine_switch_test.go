package node

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// Switching a running terminal Agent between Claude Code and Codex with the user's next message (engine_switch.go,
// #5054), against a fake herdr as launcher_switch_test.go does. The handoff's text is engine_handoff.go's: these tests
// look at the block it is carried in and at the user's words after it, not at its wording.

// switchedThread is the Codex thread a switch to Codex starts.
const switchedThread = "01a19a00-0000-7000-8000-000000005054"

// tellTo is the user's message from the chat (apply_switch) to session id.
func tellTo(t *testing.T, n *Node, id, text string) error {
	t.Helper()
	_, err := request(t, n, "session.send", map[string]any{"session_id": id, "text": text, "apply_switch": true})
	return err
}

// writeThread writes Codex thread id's rollout in ~/.codex: its session_meta in cwd, and first as its first message
// when not empty.
func writeThread(t *testing.T, home, id, cwd, first string) {
	t.Helper()
	day := time.Now()
	dir := mkdir(t, filepath.Join(home, ".codex", "sessions", day.Format("2006"), day.Format("01"), day.Format("02")))
	now := time.Now().UTC().Format(time.RFC3339Nano)
	meta, _ := json.Marshal(map[string]any{"timestamp": now, "type": "session_meta", "payload": map[string]any{"id": id, "cwd": cwd,
		"originator": "codex-tui", "source": "cli"}})
	lines := string(meta) + "\n"
	if first != "" {
		user, _ := json.Marshal(map[string]any{"timestamp": now, "type": "event_msg", "payload": map[string]any{"type": "item_completed",
			"item": map[string]any{"type": "UserMessage", "id": "u1", "content": []any{map[string]any{"type": "text", "text": first}}}}})
		lines += string(user) + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "rollout-2026-11-12T10-00-00-"+id+".jsonl"), []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
}

// codexComesUp plays Codex in pane: once codex-start is typed there, it keeps what the file held (the words, message
// and directory), and, when up, the thread shows: a new one (switchedThread, its rollout beginning with the file's
// message) or the one resumed, listed in the pane from then on.
func codexComesUp(t *testing.T, f *fakeHerdr, home, pane string, up bool) func() []codexStartFile {
	t.Helper()
	var mu sync.Mutex
	var files []codexStartFile
	f.after(func(c herdrCall) {
		if c.Method != "pane.send_input" || str(c.Params["pane_id"]) != pane {
			return
		}
		m := startFileRE.FindStringSubmatch(str(c.Params["text"]))
		if m == nil {
			return
		}
		var file codexStartFile
		if b, err := os.ReadFile(m[1]); err != nil || json.Unmarshal(b, &file) != nil {
			return
		}
		mu.Lock()
		files = append(files, file)
		mu.Unlock()
		if !up {
			return
		}
		thread := switchedThread
		if i := strings.Index(strings.Join(file.Argv, " "), " resume "); i >= 0 {
			thread = file.Argv[len(file.Argv)-1]
		} else {
			writeThread(t, home, thread, file.Dir, file.Prompt)
		}
		f.reply("session.snapshot", snapshot(nil, nil, []any{agentOf("codex", thread, proto.StatusIdle, pane, "w1:t1", "w1", file.Dir, "")}, nil))
	})
	return func() []codexStartFile {
		mu.Lock()
		defer mu.Unlock()
		return append([]codexStartFile(nil), files...)
	}
}

// claudeComesUp lists the claude a start types into pane w1:p1 as the session it starts (--session-id or --resume),
// its empty input box on the screen, when up says yes to the typed command.
func claudeComesUp(t *testing.T, f *fakeHerdr, cwd string, up func(typed string) bool) {
	t.Helper()
	plain, ansi := screenFixture(t, "claude-2.1.312-empty-162.txt"), screenFixture(t, "claude-2.1.312-empty-162.ansi")
	comesUpWhen(f, "w1:p1", up)
	f.after(func(c herdrCall) {
		if c.Method != "pane.send_input" || str(c.Params["pane_id"]) != "w1:p1" {
			return
		}
		typed := str(c.Params["text"])
		if !up(typed) {
			return
		}
		id := ""
		if m := sessionIDArg.FindStringSubmatch(typed); m != nil {
			id = m[1]
		} else if i := strings.Index(typed, "--resume "); i >= 0 {
			id = strings.Fields(typed[i+len("--resume "):])[0]
		}
		if id != "" {
			f.setScreens("w1:p1", plain, ansi)
			f.reply("session.snapshot", snapshot(nil, nil, []any{claudeAgent(id, proto.StatusIdle, "w1:p1", "w1:t1", "w1", cwd, "")}, nil))
		}
	})
}

// promptsTo are the texts the fake herdr was asked to prompt pane w1:p1 with.
func promptsTo(f *fakeHerdr) []string {
	var out []string
	for _, c := range f.calls() {
		if c.Method == "agent.prompt" && str(c.Params["target"]) == "w1:p1" {
			out = append(out, str(c.Params["text"]))
		}
	}
	return out
}

// checkHandoffBlock checks that prompt is the handoff block (from, to, model, resumed) followed by the user's words text.
func checkHandoffBlock(t *testing.T, prompt, from, to, model string, resumed bool, text string) {
	t.Helper()
	head := `<persona-context kind="engine-switch" title="Handoff" from="` + from + `" to="` + to + `"`
	if model != "" {
		head += ` model="` + model + `"`
	}
	if resumed {
		head += ` resumed="1"`
	}
	head += ">\n"
	if !strings.HasPrefix(prompt, head) {
		t.Fatalf("the first message does not start with the block %q:\n%s", head, prompt)
	}
	if !strings.HasSuffix(prompt, "</persona-context>\n\n"+text) {
		t.Fatalf("the user's words do not follow the block:\n%s", prompt)
	}
}

// longTurn is finishedTurn with a reply long enough for the transcript to be listed once nothing runs it (an offline
// row: a transcript under 2 KB is a probe's, scanOffline).
func longTurn(cwd string) []string {
	return []string{`{"type":"user","cwd":"` + cwd + `","message":{"role":"user","content":"hi"},"uuid":"u1"}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"` + strings.Repeat("hello ", 500) + `"}]},"uuid":"a1","parentUuid":"u1"}`,
		`{"type":"system","subtype":"turn_duration","uuid":"d1","parentUuid":"a1","durationMs":1000}`}
}

// codexSwitchNode is switchNode after a switch to Codex: Claude Code session sidA (its transcript in ~/proj, its
// record on plain claude) is the conversation's earlier stretch, until an hour ago, and Codex thread sidB runs in pane
// w1:p1 now, Persona's on plain codex, its record naming sidA's stretch. The pane's screen is blank.
func codexSwitchNode(t *testing.T) (n *Node, f *fakeHerdr, home, cwd string, until time.Time) {
	t.Helper()
	n, f, home, cwd, _ = switchNode(t, proto.StatusIdle, longTurn)
	until = time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	n.state.put(sidB, sessionState{Launcher: "codex", Cwd: cwd, StartedAt: until,
		EngineHistory: []proto.EngineLeg{{SessionID: sidA, Agent: "claude", Until: until}}})
	// the Codex stretch's own words: what a switch back hands over (engineHandoff reads them)
	writeRollout(t, home, sidB, cwd, until, until.Add(time.Minute),
		`{"timestamp":"`+until.Add(10*time.Second).Format(time.RFC3339)+`","type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","id":"u1","content":[{"type":"text","text":"make it faster"}]}}}`,
		`{"timestamp":"`+until.Add(20*time.Second).Format(time.RFC3339)+`","type":"event_msg","payload":{"type":"item_completed","item":{"type":"AgentMessage","id":"m1","content":[{"type":"text","text":"Cached the lookup."}]}}}`)
	f.reply("session.snapshot", snapshot(nil, nil, []any{agentOf("codex", sidB, proto.StatusIdle, "w1:p1", "w1:t1", "w1", cwd, "")}, nil))
	f.setScreens("w1:p1", "", "")
	listSessions(t, n)
	f.reset()
	return n, f, home, cwd, until
}

// Picking a launcher of the other engine keeps the switch pending, naming that engine, on the record and the row,
// with nothing done in the terminal; the session's own launcher drops it.
func TestEngineSwitchIsPendingUntilCancelled(t *testing.T) {
	n, f, _, _, persisted := switchNode(t, proto.StatusIdle, nil)
	if err := pick(t, n, map[string]any{"launcher": "cx", "from": "claude", "model": "gpt-6", "effort": "high"}); err != nil {
		t.Fatal(err)
	}
	if got := drove(f); len(got) != 0 {
		t.Fatalf("picking drove the terminal: %q", got)
	}
	ps := rowOf(t, n, sidA).PendingSwitch
	if ps == nil || ps.Launcher != "cx" || ps.Agent != "codex" || ps.Model != "gpt-6" || ps.Effort != "high" || ps.At.IsZero() {
		t.Fatalf("row's pending switch = %+v", ps)
	}
	if puts := persisted(); len(puts) == 0 || puts[len(puts)-1].PendingSwitch == nil || puts[len(puts)-1].PendingSwitch.Agent != "codex" {
		t.Fatalf("the switch was not kept with the record: %+v", puts)
	}
	if err := pick(t, n, map[string]any{"launcher": "claude", "from": "claude"}); err != nil {
		t.Fatal(err)
	}
	if ps := n.pendingSwitch(sidA); ps != nil {
		t.Fatalf("its own launcher left the switch pending: %+v", ps)
	}
	if got := drove(f); len(got) != 0 {
		t.Fatalf("cancelling drove the terminal: %q", got)
	}
}

// What a switch to the other engine is refused for when it is picked, with nothing kept and nothing typed.
func TestEngineSwitchRefusals(t *testing.T) {
	for _, tc := range []struct {
		name   string
		setup  func(t *testing.T, n *Node, f *fakeHerdr, cwd string)
		params map[string]any
		code   string
	}{
		{"launcher not on this computer", nil, map[string]any{"launcher": "nowhere"}, proto.CodeSwitchLauncherMissing},
		{"an assistant conversation", func(t *testing.T, n *Node, f *fakeHerdr, cwd string) {
			n.mu.Lock()
			n.procs[sidA] = &assistantProc{}
			n.mu.Unlock()
		}, map[string]any{"launcher": "cx"}, proto.CodeSwitchUnsupported},
		{"a runtime's session", func(t *testing.T, n *Node, f *fakeHerdr, cwd string) {
			if n.runtimes == nil {
				n.runtimes = newRuntimeRegistry()
			}
			n.runtimes.mu.Lock()
			if n.runtimes.known == nil {
				n.runtimes.known = map[string]knownSession{}
			}
			n.runtimes.known[sidA] = knownSession{runtime: "rt", label: "Runtime", at: time.Now()}
			n.runtimes.mu.Unlock()
		}, map[string]any{"launcher": "cx"}, proto.CodeSwitchUnsupported},
		{"a session Persona cannot tell the launcher of", func(t *testing.T, n *Node, f *fakeHerdr, cwd string) {
			n.state.mu.Lock()
			delete(n.state.Sessions, sidA)
			n.state.mu.Unlock()
		}, map[string]any{"launcher": "cx"}, proto.CodeSwitchUnsupported},
		{"a model the Codex launcher would not run", nil, map[string]any{"launcher": "cx", "model": "-rf"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n, f, _, cwd, _ := switchNode(t, proto.StatusIdle, nil)
			if tc.setup != nil {
				tc.setup(t, n, f, cwd)
			}
			f.reset()
			err := pick(t, n, tc.params)
			if tc.code == "" {
				if err == nil || !strings.Contains(err.Error(), "invalid model") {
					t.Fatalf("refused with %v", err)
				}
			} else if code, data := refusalOf(t, err); code != tc.code || data["delivered"] != deliveredNone {
				t.Fatalf("refused with %q (%v), want %q", code, err, tc.code)
			}
			if ps := n.pendingSwitch(sidA); ps != nil {
				t.Errorf("a refused switch was kept: %+v", ps)
			}
			if got := drove(f); len(got) != 0 {
				t.Errorf("a refused switch drove the terminal: %q", got)
			}
		})
	}
}

// A Codex session picking another Codex launcher stays refused (one engine's launchers switch within Claude Code
// only, #5035); picking a Claude Code launcher is a switch to it.
func TestEngineSwitchOfACodexSession(t *testing.T) {
	n, _, _, _, _ := codexSwitchNode(t)
	err := pickFor(t, n, sidB, map[string]any{"launcher": "cx", "from": "codex"})
	if code, _ := refusalOf(t, err); code != proto.CodeSwitchUnsupported {
		t.Fatalf("another Codex launcher: %v", err)
	}
	if err := pickFor(t, n, sidB, map[string]any{"launcher": "claude", "from": "codex"}); err != nil {
		t.Fatal(err)
	}
	if ps := n.pendingSwitch(sidB); ps == nil || ps.Agent != "claude" || ps.Launcher != "claude" {
		t.Fatalf("pending switch = %+v", ps)
	}
}

// pickFor is POST …/modes with params for session id.
func pickFor(t *testing.T, n *Node, id string, params map[string]any) error {
	t.Helper()
	p := map[string]any{"session_id": id}
	for k, v := range params {
		p[k] = v
	}
	_, err := request(t, n, "session.modes", p)
	return err
}

// The user's next message moves the conversation to Codex: the claude is quit in its pane and Codex started in the
// same shell through codex-start, in the conversation's folder, with the handoff block and then the user's words as
// its first message (nothing typed a second time). The new thread's record names the Claude Code stretch it continues,
// the old session's names none, and the list shows the conversation once: the Codex row, with the conversation's
// title. The to-do the claude's pane was linked to is handed on (persona_from), and the pane Persona opened for the
// old session is marked as the new one's.
func TestEngineSwitchToCodexOnTheUsersNextMessage(t *testing.T) {
	n, f, home, cwd, _ := switchNode(t, proto.StatusIdle, longTurn)
	agent := claudeAgent(sidA, proto.StatusIdle, "w1:p1", "w1:t1", "w1", cwd, "")
	agent["tokens"] = map[string]any{"persona_todo_ref": "todo-1@m1/" + sidA, personaTabToken: sidA}
	f.reply("session.snapshot", snapshot(nil, nil, []any{agent}, nil))
	title := rowOf(t, n, sidA).Title
	if err := pick(t, n, map[string]any{"launcher": "cx", "model": "gpt-6"}); err != nil {
		t.Fatal(err)
	}
	f.reset()
	quitsToShell(f, cwd)
	files := codexComesUp(t, f, home, "w1:p1", true)
	before := time.Now().UTC()
	if err := tell(t, n, "hello", true); err != nil {
		t.Fatalf("send: %v\n%s", err, strings.Join(f.trace(), "\n"))
	}
	if got := f.trace("tab.create", "workspace.create", "tab.close", "pane.close"); len(got) != 0 {
		t.Errorf("a tab was opened or closed: %q", got)
	}
	if starts := typedStarts(f, "w1:p1"); len(starts) != 1 || !startFileRE.MatchString(starts[0]) {
		t.Fatalf("typed %q", starts)
	}
	if got := promptsTo(f); len(got) != 0 {
		t.Errorf("the message was typed again: %q", got)
	}
	got := files()
	if len(got) != 1 {
		t.Fatalf("codex-start files: %+v", got)
	}
	if got[0].Dir != cwd || !strings.Contains(strings.Join(got[0].Argv, " "), " launch cx --model gpt-6") {
		t.Errorf("codex-start runs %q in %q", got[0].Argv, got[0].Dir)
	}
	checkHandoffBlock(t, got[0].Prompt, "claude", "codex", "gpt-6", false, "hello")
	marks := strings.Join(f.trace("pane.report_metadata"), "\n")
	if !strings.Contains(marks, `"persona_from":"todo-1@m1/`+sidA+`"`) || !strings.Contains(marks, `"persona_tab":"`+switchedThread+`"`) {
		t.Errorf("the pane's marks:\n%s", marks)
	}
	st, ok := n.state.get(switchedThread)
	if !ok || len(st.EngineHistory) != 1 || st.EngineHistory[0].SessionID != sidA || st.EngineHistory[0].Agent != "claude" ||
		st.EngineHistory[0].Until.Before(before) || st.PendingSwitch != nil || st.Launcher != "cx" || st.Model != "gpt-6" {
		t.Fatalf("the new thread's record = %+v (%v)", st, ok)
	}
	if old, _ := n.state.get(sidA); old.EngineHistory != nil || old.PendingSwitch != nil {
		t.Errorf("the old session's record = %+v", old)
	}
	list := byID(listSessions(t, n))
	if _, listed := list[sidA]; listed {
		t.Error("the Claude Code session the conversation ran in is still listed")
	}
	row, ok := list[switchedThread]
	if !ok || len(row.EngineHistory()) != 1 || row.EngineHistory()[0].SessionID != sidA || row.Title != title {
		t.Fatalf("the conversation's row = %+v (history %+v, listed %v), want title %q", row, row.EngineHistory(), ok, title)
	}
	if n.switches().inFlight(sidA) || n.pendingSwitch(sidA) != nil {
		t.Error("the switch is left under way or pending")
	}
}

// Back to Claude Code: the conversation's earlier Claude Code session is resumed (--resume) in the same shell, with
// the block saying so (resumed) and the user's words after it; its record carries both earlier stretches, itself
// among them, and it is listed, its own earlier stretch naming it notwithstanding.
func TestEngineSwitchBackResumesTheEarlierSession(t *testing.T) {
	n, f, _, cwd, until := codexSwitchNode(t)
	n.state.setPendingSwitch(sidB, &proto.PendingSwitch{Launcher: "claude", Agent: "claude", At: time.Now()})
	quitsOn(f, "w1:p1", sidB, nil, shellPane("w1:p1", "w1:t1", "w1", cwd, sidB))
	claudeComesUp(t, f, cwd, func(string) bool { return true })
	if err := tellTo(t, n, sidB, "and now?"); err != nil {
		t.Fatalf("send: %v\n%s", err, strings.Join(f.trace(), "\n"))
	}
	starts := typedStarts(f, "w1:p1")
	if len(starts) != 1 || !strings.Contains(starts[0], "--resume "+sidA) {
		t.Fatalf("typed %q", starts)
	}
	got := promptsTo(f)
	if len(got) == 0 || len(got) > 1 && got[1] != got[0] { // typed again, the same, while the transcript does not show it
		t.Fatalf("prompts %q", got)
	}
	checkHandoffBlock(t, got[0], "codex", "claude", "", true, "and now?")
	for _, words := range []string{"make it faster", "Cached the lookup."} { // the Codex stretch, handed back
		if !strings.Contains(got[0], words) {
			t.Errorf("the handoff leaves out %q:\n%s", words, got[0])
		}
	}
	st, _ := n.state.get(sidA)
	if len(st.EngineHistory) != 2 || st.EngineHistory[0] != (proto.EngineLeg{SessionID: sidA, Agent: "claude", Until: until}) ||
		st.EngineHistory[1].SessionID != sidB || st.EngineHistory[1].Agent != "codex" || st.Launcher != "claude" {
		t.Fatalf("the resumed session's record = %+v", st)
	}
	if b, _ := n.state.get(sidB); b.EngineHistory != nil || b.PendingSwitch != nil {
		t.Errorf("the Codex thread's record = %+v", b)
	}
	if row := rowOf(t, n, sidA); len(row.EngineHistory()) != 2 {
		t.Errorf("the conversation's row has history %+v", row.EngineHistory())
	}
}

// An earlier session that does not come up gives way to a fresh one, once: the block then says the conversation goes
// on from the summary (not resumed), and the earlier session's record is put back as it was.
func TestEngineSwitchFallsBackToAFreshSession(t *testing.T) {
	n, f, _, cwd, _ := codexSwitchNode(t)
	n.claudeStart, n.claudeReady = time.Second, time.Second
	before, _ := n.state.get(sidA)
	n.state.setPendingSwitch(sidB, &proto.PendingSwitch{Launcher: "claude", Agent: "claude", At: time.Now()})
	quitsOn(f, "w1:p1", sidB, nil, shellPane("w1:p1", "w1:t1", "w1", cwd, sidB))
	claudeComesUp(t, f, cwd, func(typed string) bool { return !strings.Contains(typed, "--resume") })
	if err := tellTo(t, n, sidB, "and now?"); err != nil {
		t.Fatalf("send: %v\n%s", err, strings.Join(f.trace(), "\n"))
	}
	starts := typedStarts(f, "w1:p1")
	if len(starts) != 2 || !strings.Contains(starts[0], "--resume "+sidA) || !sessionIDArg.MatchString(starts[1]) {
		t.Fatalf("typed %q", starts)
	}
	got := promptsTo(f)
	if len(got) == 0 || len(got) > 1 && got[1] != got[0] {
		t.Fatalf("prompts %q", got)
	}
	checkHandoffBlock(t, got[0], "codex", "claude", "", false, "and now?")
	for _, words := range []string{"make it faster", "Cached the lookup."} { // the Codex stretch, handed back
		if !strings.Contains(got[0], words) {
			t.Errorf("the handoff leaves out %q:\n%s", words, got[0])
		}
	}
	fresh := sessionIDArg.FindStringSubmatch(starts[1])[1]
	if st, _ := n.state.get(fresh); len(st.EngineHistory) != 2 || st.EngineHistory[1].SessionID != sidB {
		t.Errorf("the fresh session's record = %+v", st)
	}
	if st, _ := n.state.get(sidA); st.EngineHistory != nil || st.Cwd != before.Cwd || st.Launcher != before.Launcher {
		t.Errorf("the earlier session's record = %+v, was %+v", st, before)
	}
}

// Codex that does not come up rolls the switch back: the pane (the user's terminal, adopted) stays open, the claude is
// resumed on its own launcher in the same shell, the message is refused with nothing typed (switch_failed), and the
// switch is dropped.
func TestEngineSwitchFailureRollsBack(t *testing.T) {
	old := codexStartWait
	codexStartWait = time.Second
	t.Cleanup(func() { codexStartWait = old })
	n, f, home, cwd, _ := switchNode(t, proto.StatusIdle, nil)
	n.state.setPendingSwitch(sidA, &proto.PendingSwitch{Launcher: "cx", Agent: "codex", At: time.Now()})
	f.reset()
	quitsToShell(f, cwd)
	codexComesUp(t, f, home, "w1:p1", false)
	claudeComesUp(t, f, cwd, func(typed string) bool { return strings.Contains(typed, "--resume "+sidA) })
	err := tell(t, n, "hello", true)
	if code, data := refusalOf(t, err); code != proto.CodeSwitchFailed || data["delivered"] != deliveredNone {
		t.Fatalf("refused with %q (%v)", code, err)
	}
	if got := f.trace("tab.close", "pane.close"); len(got) != 0 {
		t.Errorf("closed the terminal: %q", got)
	}
	starts := typedStarts(f, "w1:p1")
	if len(starts) != 2 || !startFileRE.MatchString(starts[0]) || !strings.Contains(starts[1], "--resume "+sidA) {
		t.Fatalf("typed %q", starts)
	}
	if got := promptsTo(f); len(got) != 0 {
		t.Errorf("the message was typed: %q", got)
	}
	if st, _ := n.state.get(sidA); st.Launcher != "claude" || st.PendingSwitch != nil || st.EngineHistory != nil {
		t.Errorf("record after the failed switch = %+v", st)
	}
	if n.pendingSwitch(sidA) != nil || n.switches().inFlight(sidA) {
		t.Error("the failed switch still waits, or is left under way")
	}
}

// While the agent is busy the user's message goes to it as always and the switch waits; a message with photos is
// refused rather than its photos lost, the switch still waiting.
func TestEngineSwitchWaitsWhileBusy(t *testing.T) {
	n, f, _, _, _ := switchNode(t, proto.StatusWorking, nil)
	n.state.setPendingSwitch(sidA, &proto.PendingSwitch{Launcher: "cx", Agent: "codex", At: time.Now()})
	f.reset()
	if err := tell(t, n, "hello", true); err != nil {
		t.Fatalf("send: %v", err)
	}
	if got := f.trace("pane.send_keys", "pane.send_input"); len(got) != 0 {
		t.Errorf("quit or restarted the agent: %q", got)
	}
	if got := promptsTo(f); len(got) != 1 || got[0] != "hello" {
		t.Errorf("the message did not go to the agent: %q", got)
	}
	if ps := n.pendingSwitch(sidA); ps == nil || ps.Agent != "codex" {
		t.Errorf("the switch no longer waits: %+v", ps)
	}

	n, f, _, _, _ = switchNode(t, proto.StatusIdle, nil)
	n.state.setPendingSwitch(sidA, &proto.PendingSwitch{Launcher: "cx", Agent: "codex", At: time.Now()})
	f.reset()
	_, err := request(t, n, "session.send", map[string]any{"session_id": sidA, "text": "look", "apply_switch": true,
		"images": []any{map[string]any{"mime": "image/png", "data": "aGk="}}})
	if code, data := refusalOf(t, err); code != proto.CodeSwitchUnsupported || data["delivered"] != deliveredNone {
		t.Fatalf("a message with photos: %q (%v)", code, err)
	}
	if got := drove(f); len(got) != 0 {
		t.Errorf("drove the terminal: %q", got)
	}
	if n.pendingSwitch(sidA) == nil {
		t.Error("the switch no longer waits")
	}
}

// The list leaves out every session named in another listed row's history, never the row itself, and of two records
// that still name each other the one switched away from last wins; the row shows its first stretch's title unless its
// own is manual.
func TestJoinStretches(t *testing.T) {
	t1 := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Hour)
	off := proto.StatusOffline
	list := []proto.Session{{SessionID: sidA, Title: "first", Status: off}, {SessionID: sidB, Title: "codex", Status: off},
		{SessionID: sidC, Title: "other", Status: off}}
	chains := map[string][]proto.EngineLeg{
		sidA: {{SessionID: sidA, Agent: "claude", Until: t1}, {SessionID: sidB, Agent: "codex", Until: t2}},
		sidB: {{SessionID: sidA, Agent: "claude", Until: t1}}, // written before the switch back, not cleared since
	}
	hidden := joinStretches(list, chains)
	if !hidden[sidB] || hidden[sidA] || hidden[sidC] {
		t.Fatalf("hidden = %v", hidden)
	}
	if list[0].Title != "first" {
		t.Errorf("the conversation's own first stretch changed its title: %q", list[0].Title)
	}
	list = []proto.Session{{SessionID: sidA, Title: "first", Status: off}, {SessionID: sidB, Title: "codex", Status: off},
		{SessionID: sidC, Title: "mine", TitleManual: true}}
	chains = map[string][]proto.EngineLeg{sidB: {{SessionID: sidA, Agent: "claude", Until: t1}},
		sidC: {{SessionID: sidA, Agent: "claude", Until: t1}}}
	joinStretches(list, chains)
	if list[1].Title != "first" || list[2].Title != "mine" {
		t.Errorf("titles = %q, %q", list[1].Title, list[2].Title)
	}
	// Only a parked row goes: not one that runs (a pane, a working status), waits for the user (blocked, a card) or was
	// cut off but is a pane's; the row of a switch moving on (starting, a switch to the other engine pending) does.
	switching := &proto.PendingSwitch{Launcher: "cx", Agent: "codex"}
	for _, tc := range []struct {
		name string
		row  proto.Session
		hide bool
	}{
		{"offline", proto.Session{Status: off}, true},
		{"interrupted", proto.Session{Status: proto.StatusInterrupted}, true},
		{"in a pane", proto.Session{Status: proto.StatusIdle, PaneID: "w1:p2"}, false},
		{"offline, but a pane's", proto.Session{Status: off, PaneID: "w1:p2"}, false},
		{"working elsewhere", proto.Session{Status: proto.StatusWorking}, false},
		{"blocked", proto.Session{Status: proto.StatusBlocked}, false},
		{"a card waits", proto.Session{Status: off, PendingInteractions: 1}, false},
		{"switching on", proto.Session{Status: proto.StatusIdle, Starting: true, PendingSwitch: switching}, true},
		{"starting", proto.Session{Status: proto.StatusIdle, Starting: true}, false},
	} {
		tc.row.SessionID = sidA
		list := []proto.Session{tc.row, {SessionID: sidB, Status: proto.StatusIdle, PaneID: "w1:p1"}}
		hidden := joinStretches(list, map[string][]proto.EngineLeg{sidB: {{SessionID: sidA, Agent: "claude", Until: t1}}})
		if hidden[sidA] != tc.hide || hidden[sidB] {
			t.Errorf("%s: hidden = %v", tc.name, hidden)
		}
	}
}

// codex-start runs the words in the file's directory (Dir), whatever the directory it is started in, and says so in
// PWD.
func TestCodexStartRunsInItsDirectory(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	dir, tmp := t.TempDir(), t.TempDir()
	want, _ := filepath.EvalSymlinks(dir)
	file, out := filepath.Join(tmp, "start.json"), filepath.Join(tmp, "out")
	b, _ := json.Marshal(codexStartFile{Argv: []string{"OUT=" + out, sh, "-c", `printf '%s|%s' "$(pwd -P)" "$PWD" > "$OUT"`}, Dir: dir})
	if err := os.WriteFile(file, b, 0o600); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal([]string{file})
	cmd := exec.Command(os.Args[0], "-test.run=^TestCodexStartHelper$")
	cmd.Dir = tmp
	cmd.Env = append(os.Environ(), "PWD="+tmp, "PERSONA_CODEX_START_HELPER=1", "PERSONA_CODEX_START_ARGS="+string(args))
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, b)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if real, pwd, _ := strings.Cut(string(got), "|"); real != want || pwd != dir {
		t.Fatalf("ran in %q (PWD %q), want %q (%q)", real, pwd, want, dir)
	}
}

// A switch to the other engine whose connection went away hands on the record of the session it started too (its
// last stretch names the switch's session), as it does its own session's; any other record is not a switch's.
func TestSwitchShareKeepsTheRecordOfTheSessionAnEngineSwitchStarts(t *testing.T) {
	old, _ := testNode(t)
	share := old.switches()
	share.begin(sidA, proto.Session{SessionID: sidA, Starting: true})
	defer share.end(sidA)
	started := sessionState{Launcher: "cx", Cwd: "/w", EngineHistory: []proto.EngineLeg{{SessionID: sidA, Agent: "claude", Until: time.Now()}}}
	if !share.keep(old, switchedThread, started) {
		t.Fatal("the started session's record was not kept")
	}
	if share.keep(old, sidC, sessionState{Launcher: "claude", Cwd: "/w"}) {
		t.Error("another session's record was kept")
	}
	next, _ := testNode(t)
	next.share.Store(share)
	share.receive(next)
	if st, ok := next.state.get(switchedThread); !ok || len(st.EngineHistory) != 1 || st.EngineHistory[0].SessionID != sidA {
		t.Fatalf("the next Node has %+v (%v)", st, ok)
	}
}

// The model a launcher names for itself goes into the handoff block for the apps to show: the list's placeholder for a
// launcher that names none is not a model, so the block names none then.
func TestLauncherDefaultModelSkipsThePlaceholder(t *testing.T) {
	n, _ := testNode(t)
	launcherCache.Lock()
	oldAt, oldList := launcherCache.at, launcherCache.list
	launcherCache.at = time.Now()
	launcherCache.list = []proto.Launcher{{Name: "codex", Model: "默认", Agent: "codex"}, {Name: "cx", Model: "gpt-6", Agent: "codex"}} // i18n-ignore: the node's own placeholder
	launcherCache.Unlock()
	t.Cleanup(func() {
		launcherCache.Lock()
		launcherCache.at, launcherCache.list = oldAt, oldList
		launcherCache.Unlock()
	})
	if got := n.launcherDefaultModel("codex"); got != "" {
		t.Errorf("the placeholder came back as a model: %q", got)
	}
	if got := n.launcherDefaultModel("cx"); got != "gpt-6" {
		t.Errorf("cx's own model = %q, want gpt-6", got)
	}
	if got := n.launcherDefaultModel("missing"); got != "" {
		t.Errorf("a launcher not in the list named %q", got)
	}
}

// codexReadsAndRuns plays a Codex that codex-start started in pane w1:p1 but that never shows a thread (a dialog it
// waits on, a thread not seen in time): the line's file is read (and removed, as codex-start does), and from then on
// the pane runs codex in front of its shell.
func codexReadsAndRuns(f *fakeHerdr) {
	var mu sync.Mutex
	running := false
	f.after(func(c herdrCall) {
		if c.Method != "pane.send_input" || str(c.Params["pane_id"]) != "w1:p1" {
			return
		}
		if m := startFileRE.FindStringSubmatch(str(c.Params["text"])); m != nil {
			_ = os.Remove(m[1])
			mu.Lock()
			running = true
			mu.Unlock()
		}
	})
	f.handle("pane.process_info", func(map[string]any) any {
		mu.Lock()
		name := "zsh"
		if running {
			name = "codex"
		}
		mu.Unlock()
		return map[string]any{"process_info": map[string]any{"foreground_processes": []any{map[string]any{"pid": 1, "name": name, "argv0": name}}}}
	})
}

// A Codex start that may have the message (codex-start read it, and a Codex runs in the pane) is not followed by
// another start or a rollback, which would run the message twice or have the user send it again: the switch stops where
// it is, nothing more is pressed or typed, the switch is dropped, and the message is answered as not known to have gone.
func TestEngineSwitchStopsWhenTheStartMayHaveTheMessage(t *testing.T) {
	old := codexStartWait
	codexStartWait = time.Second
	t.Cleanup(func() { codexStartWait = old })
	n, f, _, cwd, _ := switchNode(t, proto.StatusIdle, nil)
	n.state.setPendingSwitch(sidA, &proto.PendingSwitch{Launcher: "cx", Agent: "codex", At: time.Now()})
	f.reset()
	quitsToShell(f, cwd)
	codexReadsAndRuns(f)
	claudeComesUp(t, f, cwd, func(string) bool { return true }) // a rollback would bring the claude back
	err := tell(t, n, "hello", true)
	if code, data := refusalOf(t, err); code != "" || data["delivered"] != deliveredUnknown {
		t.Fatalf("answered %q %v (%v), want delivered unknown", code, data, err)
	}
	starts := typedStarts(f, "w1:p1")
	if len(starts) != 1 || !startFileRE.MatchString(starts[0]) {
		t.Fatalf("typed %q", starts)
	}
	trace := strings.Join(f.trace("pane.send_keys", "pane.send_input"), "\n")
	if i := strings.Index(trace, "codex-start"); strings.Contains(trace[i:], "pane.send_keys") {
		t.Errorf("keys were pressed in the pane after the start:\n%s", trace)
	}
	if got := promptsTo(f); len(got) != 0 {
		t.Errorf("the message was typed: %q", got)
	}
	if n.pendingSwitch(sidA) != nil || n.switches().inFlight(sidA) {
		t.Error("the switch still waits, or is left under way")
	}
}

// The same for a claude the switch's first prompt was typed to without herdr saying it went (another error than one of
// nothing typed): the earlier session it resumed is not given up for a fresh one with the message again; it takes the
// conversation, as far as the records go.
func TestEngineSwitchStopsWhenTheFirstPromptIsUnconfirmed(t *testing.T) {
	n, f, _, cwd, _ := codexSwitchNode(t)
	n.claudeStart, n.claudeReady = 2*time.Second, 2*time.Second
	n.state.setPendingSwitch(sidB, &proto.PendingSwitch{Launcher: "claude", Agent: "claude", At: time.Now()})
	quitsOn(f, "w1:p1", sidB, nil, shellPane("w1:p1", "w1:t1", "w1", cwd, sidB))
	claudeComesUp(t, f, cwd, func(string) bool { return true })
	f.handle("agent.prompt", func(map[string]any) any { return herdrFail{Code: "server_error", Message: "the connection went away"} })
	err := tellTo(t, n, sidB, "and now?")
	if code, data := refusalOf(t, err); code != "" || data["delivered"] != deliveredUnknown {
		t.Fatalf("answered %q %v (%v), want delivered unknown", code, data, err)
	}
	if starts := typedStarts(f, "w1:p1"); len(starts) != 1 || !strings.Contains(starts[0], "--resume "+sidA) {
		t.Fatalf("typed %q", starts)
	}
	if st, _ := n.state.get(sidA); len(st.EngineHistory) != 2 || st.EngineHistory[1].SessionID != sidB || st.PendingSwitch != nil {
		t.Errorf("the resumed session's record = %+v", st)
	}
	if st, _ := n.state.get(sidB); st.EngineHistory != nil || st.PendingSwitch != nil {
		t.Errorf("the Codex thread's record = %+v", st)
	}
}

// The user's Resume of the earlier session while the switch resumes it waits for the switch and finds it running: no
// second claude --resume of it is typed, here or in a new tab.
func TestEngineSwitchHoldsTheEarlierSessionWhileItResumesIt(t *testing.T) {
	n, f, _, cwd, _ := codexSwitchNode(t)
	n.claudeStart, n.claudeReady = 10*time.Second, 10*time.Second
	f.reply("workspace.list", map[string]any{"workspaces": []any{map[string]any{"workspace_id": "w1", "label": "proj"}}})
	f.reply("tab.create", map[string]any{"tab": map[string]any{"tab_id": "w1:t2"}, "root_pane": map[string]any{"pane_id": "w1:p2"}})
	n.state.setPendingSwitch(sidB, &proto.PendingSwitch{Launcher: "claude", Agent: "claude", At: time.Now()})
	quitsOn(f, "w1:p1", sidB, nil, shellPane("w1:p1", "w1:t1", "w1", cwd, sidB))
	// The switch's claude --resume comes up a second after it is typed; the user's Resume goes in meanwhile.
	plain, ansi := screenFixture(t, "claude-2.1.312-empty-162.txt"), screenFixture(t, "claude-2.1.312-empty-162.ansi")
	var mu sync.Mutex
	up := false
	resumed := make(chan error, 1)
	f.after(func(c herdrCall) {
		if c.Method != "pane.send_input" || str(c.Params["pane_id"]) != "w1:p1" || !strings.Contains(str(c.Params["text"]), "--resume "+sidA) {
			return
		}
		go func() {
			_, err := request(t, n, "session.start", map[string]any{"resume_session_id": sidA})
			resumed <- err
		}()
		go func() {
			time.Sleep(time.Second)
			f.setScreens("w1:p1", plain, ansi)
			f.reply("session.snapshot", snapshot(nil, nil, []any{claudeAgent(sidA, proto.StatusIdle, "w1:p1", "w1:t1", "w1", cwd, "")}, nil))
			mu.Lock()
			up = true
			mu.Unlock()
		}()
	})
	f.handle("agent.get", func(p map[string]any) any {
		mu.Lock()
		defer mu.Unlock()
		if !up || str(p["target"]) != "w1:p1" {
			return map[string]any{"agent": nil}
		}
		return map[string]any{"agent": map[string]any{"agent": "claude", "agent_status": proto.StatusIdle, "pane_id": "w1:p1",
			"tab_id": "w1:t1", "workspace_id": "w1"}}
	})
	if err := tellTo(t, n, sidB, "and now?"); err != nil {
		t.Fatalf("send: %v\n%s", err, strings.Join(f.trace(), "\n"))
	}
	select {
	case err := <-resumed:
		if err != nil {
			t.Errorf("the Resume: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the Resume did not end")
	}
	var again []string
	for _, c := range f.calls() {
		if c.Method == "pane.send_input" && strings.Contains(str(c.Params["text"]), "--resume "+sidA) {
			again = append(again, str(c.Params["pane_id"]))
		}
	}
	if len(again) != 1 || len(f.trace("tab.create", "workspace.create")) != 0 {
		t.Fatalf("claude --resume typed in %q, tabs %q", again, f.trace("tab.create", "workspace.create"))
	}
}

// An earlier stretch of a conversation that runs again (resumed by hand in another terminal) is listed as its own row,
// next to the conversation's.
func TestEngineSwitchListsAnEarlierSessionThatRunsAgain(t *testing.T) {
	n, f, _, cwd, _ := codexSwitchNode(t)
	if _, listed := byID(listSessions(t, n))[sidA]; listed {
		t.Fatal("the earlier stretch is listed while it is parked")
	}
	f.reply("session.snapshot", snapshot(nil, nil, []any{agentOf("codex", sidB, proto.StatusIdle, "w1:p1", "w1:t1", "w1", cwd, ""),
		claudeAgent(sidA, proto.StatusIdle, "w1:p2", "w1:t2", "w1", cwd, "")}, nil))
	list := byID(listSessions(t, n))
	if _, listed := list[sidA]; !listed {
		t.Fatal("the earlier stretch, running again, is not listed")
	}
	if _, listed := list[sidB]; !listed {
		t.Fatal("the conversation's row is gone")
	}
}
