package node

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/nodeclient"
	"github.com/nwverse/persona/server/internal/proto"
)

// The hooks' turn counters decide whether a Claude that runs Persona's hooks is working (hookturn.go): prompts
// outnumbering Stops is an open turn, a Stop with nothing after it is a finished one, and Esc (no Stop) is told from
// the transcript and the title. Every row is one moment of one session.
func TestJudgeHookTurn(t *testing.T) {
	now := time.Now()
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	open := hookTurn{prompts: 2, completed: 1, stopped: true, promptAt: ago(20 * time.Second), stopAt: ago(10 * time.Minute)}
	done := hookTurn{prompts: 2, completed: 2, stopped: true, promptAt: ago(time.Minute), stopAt: ago(30 * time.Second)}
	longAgo := hookTurn{prompts: 2, completed: 1, stopped: true, promptAt: ago(8 * time.Minute), stopAt: ago(20 * time.Minute)} // open since 8 minutes
	longDone := hookTurn{prompts: 2, completed: 2, stopped: true, promptAt: ago(30 * time.Minute), stopAt: ago(10 * time.Minute)}

	for _, tc := range []struct {
		name string
		s    hookSignals
		want hookVerdict
	}{
		{"nothing counted: the heuristics decide", hookSignals{turn: hookTurn{}}, hookSilent},
		{"only a pid known", hookSignals{turn: hookTurn{pid: 4}, spinner: true}, hookSilent},

		{"a prompt no Stop answered, herdr on done", hookSignals{turn: open, hasFile: true, fileMod: ago(2 * time.Second), turnOpen: true}, hookWorking},
		{"the prompt is not in the transcript yet", hookSignals{turn: open, hasFile: true, fileMod: ago(time.Hour), turnClosed: true}, hookWorking},
		{"no transcript at all, just prompted", hookSignals{turn: hookTurn{prompts: 1, promptAt: ago(time.Second)}}, hookWorking},
		{"a long silent tool call: the spinner", hookSignals{turn: open, spinner: true, hasFile: true, fileMod: ago(10 * time.Minute), turnOpen: true}, hookWorking},
		{"interrupted: the transcript closed after the prompt", hookSignals{turn: open, hasFile: true, fileMod: ago(3 * time.Second), turnClosed: true, waiting: true}, hookResting},
		{"interrupted: even with a stale spinner title", hookSignals{turn: open, spinner: true, hasFile: true, fileMod: ago(3 * time.Second), turnClosed: true}, hookResting},
		{"Esc, title says waiting, quiet long enough", hookSignals{turn: open, waiting: true, hasFile: true, fileMod: ago(30 * time.Second), turnOpen: true}, hookResting},
		{"title says waiting, written to a moment ago", hookSignals{turn: open, waiting: true, hasFile: true, fileMod: ago(2 * time.Second), turnOpen: true}, hookWorking},
		{"quiet but no title to tell: not yet", hookSignals{turn: open, hasFile: true, fileMod: ago(60 * time.Second), turnOpen: true}, hookWorking},
		{"quiet for minutes, no spinner, no title", hookSignals{turn: longAgo, hasFile: true, fileMod: ago(5 * time.Minute), turnOpen: true}, hookResting},
		{"a prompt hook with no Stop ever (a local command)", hookSignals{turn: hookTurn{prompts: 1, promptAt: ago(time.Minute)}, waiting: true}, hookResting},

		{"answered", hookSignals{turn: done, hasFile: true, fileMod: ago(30 * time.Second), turnClosed: true}, hookResting},
		{"answered, a stale spinner in the title", hookSignals{turn: done, spinner: true, hasFile: true, fileMod: ago(30 * time.Second), turnClosed: true}, hookResting},
		{"answered, the closing lines are flushed just after the Stop", hookSignals{turn: done, hasFile: true, fileMod: ago(28 * time.Second), turnOpen: true}, hookResting},
		{"answered, no transcript", hookSignals{turn: done}, hookResting},
		{"a Stop after a node restart", hookSignals{turn: hookTurn{stopped: true, stopAt: ago(time.Second)}, spinner: true}, hookResting},
		{"woken by a background task: an open turn written after the Stop", hookSignals{turn: done, hasFile: true, fileMod: ago(2 * time.Second), turnOpen: true}, hookWorking},
		{"woken, a long tool call: the spinner", hookSignals{turn: longDone, spinner: true, hasFile: true, fileMod: ago(5 * time.Minute), turnOpen: true}, hookWorking},
		{"woken, but quiet and no spinner", hookSignals{turn: longDone, hasFile: true, fileMod: ago(5 * time.Minute), turnOpen: true}, hookResting},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.s.now = now
			if got := judgeHookTurn(tc.s); got != tc.want {
				t.Fatalf("got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestHookTurnsCount(t *testing.T) {
	now := time.Now()
	var nilTurns *hookTurns // a node built without hook state
	nilTurns.prompt("s", now)
	nilTurns.stop("s", now)
	nilTurns.agent("s", 4, "", now)
	if _, ok := nilTurns.get("s"); ok {
		t.Fatal("a nil hookTurns has state")
	}

	h := &hookTurns{}
	h.agent("s", 100, "linux:7", now)
	if got, _ := h.get("s"); got.seen() {
		t.Fatalf("a pid alone is not a turn: %+v", got)
	}
	h.prompt("s", now)
	if got, _ := h.get("s"); !got.open() || got.prompts != 1 {
		t.Fatalf("after a prompt: %+v", got)
	}
	h.prompt("s", now.Add(time.Second)) // a second prompt before any Stop (queued input)
	h.stop("s", now.Add(2*time.Second))
	if got, _ := h.get("s"); got.open() || !got.stopped || got.completed != 2 {
		t.Fatalf("a Stop answers every prompt so far: %+v", got)
	}
	h.agent("s", 100, "linux:7", now) // the same process again: nothing resets
	if got, _ := h.get("s"); !got.stopped {
		t.Fatalf("the same process reset the counters: %+v", got)
	}
	h.agent("s", 100, "linux:9", now) // the pid reused by a later process
	if got, _ := h.get("s"); got.seen() || got.start != "linux:9" {
		t.Fatalf("a new process starts from zero: %+v", got)
	}
	h.prompt("s", now)
	h.agent("s", 200, "linux:9", now) // a resume under another pid
	if got, _ := h.get("s"); got.seen() || got.pid != 200 {
		t.Fatalf("a resume starts from zero: %+v", got)
	}
	h.forget("s")
	if _, ok := h.get("s"); ok {
		t.Fatal("forgotten")
	}
	// Quiet sessions are dropped when the table is full.
	for i := 0; i < maxHookTurns; i++ {
		h.prompt(fmt.Sprint("old", i), now.Add(-8*24*time.Hour))
	}
	h.prompt("new", now)
	if len(h.m) != 1 {
		t.Fatalf("%d entries after the sweep, want 1", len(h.m))
	}
}

func TestStopSignature(t *testing.T) {
	a, b := stopSignature("All done."), stopSignature("  All done.\n")
	if a == "" || a != b {
		t.Fatalf("whitespace changed the signature: %q %q", a, b)
	}
	if stopSignature("All done!") == a || stopSignature("") != "" || stopSignature(" \n") != "" {
		t.Fatal("signatures")
	}
}

func TestRecordHookTurn(t *testing.T) {
	n := &Node{hooks: &hookTurns{}}
	const id = "0bd545f1-681d-4d98-bf5a-eb7c1251a0f0"
	now := time.Now()
	for _, tc := range []struct {
		name        string
		req         sockRequest
		in          hookInput
		prompts     int
		stopped     bool
		wantSigOver string // the reply the returned signature is of
	}{
		{"a prompt", sockRequest{AgentPID: 42}, hookInput{HookEventName: "UserPromptSubmit", SessionID: id}, 1, false, ""},
		{"a subagent's prompt", sockRequest{}, hookInput{HookEventName: "UserPromptSubmit", SessionID: id, AgentID: "sub"}, 1, false, ""},
		{"a reconnecting hook's repeat", sockRequest{Resume: true}, hookInput{HookEventName: "UserPromptSubmit", SessionID: id}, 1, false, ""},
		{"not a session id", sockRequest{}, hookInput{HookEventName: "UserPromptSubmit", SessionID: "../x"}, 1, false, ""},
		{"a Notification counts nothing", sockRequest{}, hookInput{HookEventName: "Notification", SessionID: id}, 1, false, ""},
		{"a Stop", sockRequest{}, hookInput{HookEventName: "Stop", SessionID: id, LastAssistantMessage: "found it"}, 1, true, "found it"},
	} {
		got := n.recordHookTurn(tc.req, tc.in, now)
		if got != stopSignature(tc.wantSigOver) {
			t.Errorf("%s: signature %q, want that of %q", tc.name, got, tc.wantSigOver)
		}
		turn, _ := n.hooks.get(id)
		if turn.prompts != tc.prompts || turn.stopped != tc.stopped {
			t.Errorf("%s: prompts %d stopped %v, want %d %v", tc.name, turn.prompts, turn.stopped, tc.prompts, tc.stopped)
		}
	}
	if turn, _ := n.hooks.get(id); turn.pid != 42 || turn.open() {
		t.Errorf("pid %d open %v, want 42 and a closed turn", turn.pid, turn.open())
	}
}

// writeAgentFile writes sessions/<pid>.json of a claude in a config dir, as Claude Code does.
func writeAgentFile(t *testing.T, dir string, pid int, session string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"pid":%d,"sessionId":%q,"kind":"interactive"}`, pid, session)
	if err := os.WriteFile(filepath.Join(dir, "sessions", fmt.Sprintf("%d.json", pid)), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The hook is a child of a shell that is a child of claude: the agent is the ancestor whose sessions/<pid>.json
// names the session, not the first parent.
func TestHookAgentFindsClaudeAmongAncestors(t *testing.T) {
	dir := t.TempDir()
	const id, other = "0bd545f1-681d-4d98-bf5a-eb7c1251a0f0", "2e2f25fd-6a4c-4ebb-8615-788af4aaa7e2"
	writeAgentFile(t, dir, 300, id)    // claude
	writeAgentFile(t, dir, 900, other) // a claude further up that runs another session
	parents := map[int]int{500: 400, 400: 300, 300: 200, 200: 900, 900: 1}
	parent := func(p int) (int, error) {
		if pp, ok := parents[p]; ok {
			return pp, nil
		}
		return 0, os.ErrNotExist
	}
	for _, tc := range []struct {
		name string
		id   string
		from int
		want int
	}{
		{"through a shell and a wrapper", id, 500, 300},
		{"the other session's claude", other, 500, 900},
		{"a hook run by claude directly", id, 300, 300},
		{"a session no ancestor runs", "9f0c0f2e-0000-4000-8000-000000000000", 500, 0},
		{"no session id", "", 500, 0},
		{"a parent that cannot be read", id, 777, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if pid, _ := nodeclient.FindAgent(tc.id, tc.from, dir, parent); pid != tc.want {
				t.Fatalf("pid %d, want %d", pid, tc.want)
			}
		})
	}
	t.Setenv("CLAUDE_CONFIG_DIR", "/c")
	if got := claudeDirOf("/h"); got != "/c" {
		t.Fatalf("CLAUDE_CONFIG_DIR: %q", got)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	if got := claudeDirOf("/h"); got != filepath.Join("/h", ".claude") {
		t.Fatalf("default dir: %q", got)
	}
}

func TestSameProcess(t *testing.T) {
	self := os.Getpid()
	if !sameProcess(self, "") || sameProcess(0, "") || sameProcess(999999, "") {
		t.Fatal("liveness by pid alone")
	}
	prefix := map[string]string{"linux": "linux:", "windows": "windows:"}[runtime.GOOS]
	if prefix == "" {
		return
	}
	start := procStartMark(self)
	if !strings.HasPrefix(start, prefix) {
		t.Fatalf("start of this process: %q", start)
	}
	if !sameProcess(self, start) {
		t.Fatal("the process with its own start")
	}
	if sameProcess(self, start+"1") {
		t.Fatal("a pid whose start differs was taken for the same process (pid reuse)")
	}
	if sameProcess(999999, prefix+"1") {
		t.Fatal("a process that does not exist")
	}
	if claudeFileStart("123") != prefix+"123" || claudeFileStart("") != "" {
		t.Fatal("claudeFileStart")
	}
}

// A session file left by a claude that exited names a pid another program may hold by now. Claude Code writes the
// process' start into it, so a pid with another start is not that claude and the session is not open elsewhere.
func TestOpenElsewhereIgnoresReusedPid(t *testing.T) {
	prefix := map[string]string{"linux": "linux:", "windows": "windows:"}[runtime.GOOS]
	if prefix == "" {
		t.Skip("the process start is only read on Linux and Windows")
	}
	n, _, home := herdrNode(t)
	const reused = "0bd545f1-681d-4d98-bf5a-eb7c1251a0f0"
	self := os.Getpid()
	writeClaudeSession(t, home, self, reused, "cli")
	f := filepath.Join(home, ".claude", "sessions", fmt.Sprintf("%d.json", self))
	b, _ := os.ReadFile(f)
	start := strings.TrimPrefix(procStartMark(self), prefix)
	// Same file, written by a claude that started at a different time than the process now holding the pid.
	if err := os.WriteFile(f, []byte(strings.TrimSuffix(string(b), "}")+`,"procStart":"1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := n.claudeOpenElsewhere(reused); ok {
		t.Fatal("a reused pid counted as the claude that wrote the file")
	}
	if err := os.WriteFile(f, []byte(strings.TrimSuffix(string(b), "}")+`,"procStart":"`+start+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := n.claudeOpenElsewhere(reused); !ok {
		t.Fatal("the running claude (its start matches) was not found")
	}
}

// claudeStatus with the hooks' counters over the host's word (and the heuristics as before for a session without).
func TestClaudeStatusFromHooks(t *testing.T) {
	dir := t.TempDir()
	const (
		prompt    = `{"type":"user","message":{"role":"user","content":"look for the bug"},"uuid":"u1","timestamp":"{{T}}"}`
		toolUse   = `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Grep","input":{"pattern":"x"}}]},"uuid":"a1","parentUuid":"u1","timestamp":"{{T}}"}`
		reply     = `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"found it"}]},"uuid":"a2","parentUuid":"a1","timestamp":"{{T}}"}`
		duration  = `{"type":"system","subtype":"turn_duration","uuid":"d1","parentUuid":"a2","durationMs":2000,"timestamp":"{{T}}"}`
		interrupt = `{"type":"user","message":{"role":"user","content":"[Request interrupted by user]"},"uuid":"u2","timestamp":"{{T}}"}`
	)
	now := time.Now()
	oldAlive := processAlive
	t.Cleanup(func() { processAlive = oldAlive })

	for i, tc := range []struct {
		name   string
		host   string
		title  string
		lines  []string
		age    time.Duration
		turn   *hookTurn
		dead   bool // the process the hooks ran under is gone
		wantSt string
	}{
		{"hooks: a turn open, herdr on done (#2069)", proto.StatusDone, "✳ fix", []string{duration, prompt, toolUse}, 2 * time.Second,
			&hookTurn{prompts: 3, completed: 2, stopped: true, promptAt: now.Add(-10 * time.Second), stopAt: now.Add(-time.Hour)}, false, proto.StatusWorking},
		{"hooks: open, no transcript write yet and herdr idle", proto.StatusIdle, "", []string{duration}, time.Hour,
			&hookTurn{prompts: 1, promptAt: now.Add(-time.Second)}, false, proto.StatusWorking},
		{"hooks: answered, a stale spinner title", proto.StatusDone, "◐ fix", []string{prompt, toolUse, reply, duration}, 2 * time.Second,
			&hookTurn{prompts: 1, completed: 1, stopped: true, promptAt: now.Add(-time.Minute), stopAt: now.Add(-10 * time.Second)}, false, proto.StatusDone},
		{"hooks: answered but the transcript is open and fresh: a turn nobody announced", proto.StatusDone, "✳ fix", []string{prompt, reply, duration, prompt}, time.Second,
			&hookTurn{prompts: 1, completed: 1, stopped: true, promptAt: now.Add(-time.Hour), stopAt: now.Add(-time.Minute)}, false, proto.StatusWorking},
		{"hooks: Esc, the interrupt line is in", proto.StatusIdle, "✳ fix", []string{prompt, toolUse, interrupt}, 3 * time.Second,
			&hookTurn{prompts: 1, promptAt: now.Add(-time.Minute)}, false, proto.StatusIdle},
		{"hooks: Esc, nothing written, the title waits", proto.StatusDone, "✳ fix", []string{prompt, toolUse}, 40 * time.Second,
			&hookTurn{prompts: 1, promptAt: now.Add(-time.Minute)}, false, proto.StatusDone},
		{"hooks: a long tool call, the spinner keeps it working", proto.StatusDone, "◓ fix", []string{prompt, toolUse}, 10 * time.Minute,
			&hookTurn{prompts: 1, promptAt: now.Add(-11 * time.Minute)}, false, proto.StatusWorking},
		{"hooks: only a SessionStart came, the heuristics decide", proto.StatusDone, "◐ fix", []string{prompt, toolUse}, 2 * time.Second,
			&hookTurn{pid: 0}, false, proto.StatusWorking},
		{"hooks: quiet for a minute, no title, the claude alive", proto.StatusDone, "", []string{prompt, toolUse}, time.Minute,
			&hookTurn{prompts: 1, promptAt: now.Add(-2 * time.Minute), pid: 4242, start: "linux:1"}, false, proto.StatusWorking},
		{"hooks: the same, but the claude is gone: its open turn is not counted", proto.StatusDone, "", []string{prompt, toolUse}, time.Minute,
			&hookTurn{prompts: 1, promptAt: now.Add(-2 * time.Minute), pid: 4242, start: "linux:1"}, true, proto.StatusDone},
		{"no hooks: the heuristics as before", proto.StatusDone, "✳ fix", []string{prompt, toolUse}, 2 * time.Second, nil, false, proto.StatusWorking},
		{"hooks: blocked stays blocked", proto.StatusBlocked, "✳ fix", []string{prompt}, time.Second,
			&hookTurn{prompts: 1, completed: 1, stopped: true, promptAt: now.Add(-time.Minute), stopAt: now.Add(-time.Second)}, false, proto.StatusBlocked},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := fmt.Sprintf("%08d-0000-4000-8000-000000000000", i)
			p := filepath.Join(dir, id+".jsonl")
			// the lines were written age ago: freshness is their time, not the file's (#5967)
			body := strings.ReplaceAll(strings.Join(tc.lines, "\n")+"\n", "{{T}}", now.Add(-tc.age).UTC().Format(time.RFC3339Nano))
			if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(p, now.Add(-tc.age), now.Add(-tc.age)); err != nil {
				t.Fatal(err)
			}
			n := &Node{paths: map[string]string{id: p}, metas: map[string]quickMeta{}, bgScans: map[string]*bgScan{}, hooks: &hookTurns{}}
			if tc.turn != nil {
				n.hooks.m = map[string]*hookTurn{id: tc.turn}
			}
			processAlive = func(pid int, start string) bool { return !tc.dead }
			a := host.Agent{Agent: "claude", AgentStatus: tc.host, RawTitle: tc.title}
			if got := n.claudeStatus(id, a, now); got != tc.wantSt {
				t.Fatalf("%s, want %s", got, tc.wantSt)
			}
			if tc.dead {
				if _, ok := n.hooks.get(id); ok {
					t.Fatal("the dead process' hook state was kept")
				}
			}
		})
	}
}
