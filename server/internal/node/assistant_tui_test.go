package node

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// The transcript an interactive Claude Code writes reads as what claude -p prints: one init as a turn starts (also a
// turn it starts on its own), one message_start per message with its usage, the text a block at a time, the result at
// turn_duration (is_error after an API error), at an interruption, and nothing for a subagent's lines.
func TestTUITranslatorReadsATranscriptAsStreamJSON(t *testing.T) {
	rows := []string{
		`{"type":"permission-mode"}`,
		`{"type":"user","version":"2.1.325","message":{"role":"user","content":"<persona-context>x</persona-context>\n\nhi"}}`,
		`{"type":"assistant","version":"2.1.325","message":{"id":"m1","model":"claude-haiku","content":[{"type":"thinking","thinking":""}],"usage":{"input_tokens":2,"cache_read_input_tokens":100,"cache_creation_input_tokens":5}}}`,
		`{"type":"assistant","message":{"id":"m1","content":[{"type":"text","text":"Hello"}]}}`,
		`{"type":"assistant","isSidechain":true,"message":{"id":"s1","content":[{"type":"text","text":"sub"}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","content":"ok"}]}}`,
		`{"type":"system","subtype":"turn_duration","durationMs":10}`,
		`{"type":"system","subtype":"turn_duration","durationMs":10}`,
		`{"type":"user","message":{"content":"<task-notification>done</task-notification>"}}`,
		`{"type":"assistant","isApiErrorMessage":true,"message":{"id":"m2","content":[{"type":"text","text":"API Error: 429"}]}}`,
		`{"type":"system","subtype":"turn_duration"}`,
		`{"type":"user","message":{"content":"again"}}`,
		`{"type":"system","subtype":"compact_boundary","compactMetadata":{"postTokens":1234}}`,
		`{"type":"user","message":{"content":[{"type":"text","text":"[Request interrupted by user]"}]}}`,
	}
	tr := newTUITranslator()
	var got []string
	for _, r := range rows {
		for _, ev := range tr.row([]byte(r)) {
			b, _ := json.Marshal(ev)
			got = append(got, string(b))
		}
	}
	want := []string{
		`{"claude_code_version":"2.1.325","subtype":"init","type":"system"}`,
		`{"event":{"message":{"model":"claude-haiku","usage":{"cache_creation_input_tokens":5,"cache_read_input_tokens":100,"input_tokens":2}},"type":"message_start"},"type":"stream_event"}`,
		`{"event":{"delta":{"text":"Hello","type":"text_delta"},"type":"content_block_delta"},"type":"stream_event"}`,
		`{"event":{"type":"message_stop"},"type":"stream_event"}`,
		`{"is_error":false,"result":"Hello","subtype":"success","type":"result"}`,
		`{"claude_code_version":"2.1.325","subtype":"init","type":"system"}`,
		`{"event":{"message":{"model":"","usage":{"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"input_tokens":0}},"type":"message_start"},"type":"stream_event"}`,
		`{"event":{"delta":{"text":"API Error: 429","type":"text_delta"},"type":"content_block_delta"},"type":"stream_event"}`,
		`{"event":{"type":"message_stop"},"type":"stream_event"}`,
		`{"is_error":true,"result":"API Error: 429","subtype":"error_during_execution","type":"result"}`,
		`{"claude_code_version":"2.1.325","subtype":"init","type":"system"}`,
		`{"compact_metadata":{"post_tokens":1234},"subtype":"compact_boundary","type":"system"}`,
		`{"is_error":false,"result":"Request interrupted by user","subtype":"success","type":"result"}`,
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if ev := tr.idle(); ev != nil {
		t.Fatalf("an idle screen after the turn ended ended it again: %v", ev)
	}
}

// An interactive start goes without claude -p's flags and takes its permission mode and effort as flags.
func TestTUIArgsDropPrintFlagsAndCarryTheStartModes(t *testing.T) {
	print := []string{"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose",
		"--include-partial-messages", "--append-system-prompt", "be kind", "--settings", "{}", "--session-id", "s1"}
	full := withStartModes(print, "acceptEdits", "high")
	got := tuiArgs(full)
	want := []string{"--append-system-prompt", "be kind", "--settings", "{}", "--session-id", "s1",
		"--permission-mode", "acceptEdits", "--effort", "high"}
	if !slices.Equal(got, want) {
		t.Fatalf("tui args %q", got)
	}
	if !slices.Contains(full, "-p") || !slices.Contains(full, "--effort") {
		t.Fatalf("the print-mode fallback lost its flags or modes: %q", full)
	}
	if got := withStartModes([]string{"--permission-mode", "bypassPermissions"}, "bypassPermissions", effortAuto); len(got) != 2 {
		t.Fatalf("a mode already given or auto effort was added again: %q", got)
	}
}

// An interactive process takes its permission mode and effort at start only: another one restarts it, as
// bypassPermissions does for claude -p. One whose bridge fell back to claude -p takes control requests again.
func TestTUIProcessRestartsForModeAndEffort(t *testing.T) {
	p := &assistantProc{agent: "claude", tui: true, mode: "bypassPermissions", effort: "high", canBypass: true}
	if p.needsRestartFor("bypassPermissions") || p.needsRestartFor("") {
		t.Fatal("the mode it started in restarts it")
	}
	if !p.needsRestartFor("acceptEdits") {
		t.Fatal("another mode does not restart it")
	}
	p.mu.Lock()
	same, other := p.effortNeedsRestartLocked("high"), p.effortNeedsRestartLocked("low")
	p.mu.Unlock()
	if same || !other {
		t.Fatalf("effort restart: same %v other %v", same, other)
	}
	p.tuiNote = "tmux is not installed on this computer"
	if p.needsRestartFor("acceptEdits") {
		t.Fatal("a fallen-back process restarts for a mode claude -p sets by control request")
	}
}

// fakeTUIBridge makes n's own binary a script that records its spec and argv, says the interactive start fell back, and
// then answers each message as claude -p would.
func fakeTUIBridge(t *testing.T, n *Node) (specFile string) {
	t.Helper()
	dir := t.TempDir()
	specFile = filepath.Join(dir, "spec.json")
	script := "#!/bin/sh\necho \"$@\" > " + filepath.Join(dir, "argv") + "\nIFS= read -r spec\nprintf '%s\\n' \"$spec\" > " + specFile + "\n" +
		`echo '{"type":"system","subtype":"persona_tui_fallback","reason":"tmux is not installed on this computer"}'` + "\n" +
		"while IFS= read -r line; do echo '{\"type\":\"system\",\"subtype\":\"init\"}'; echo '{\"type\":\"result\",\"subtype\":\"success\",\"is_error\":false,\"result\":\"ok\"}'; done\n"
	bin := filepath.Join(dir, "personad")
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	n.bin = bin
	return specFile
}

func resetTUICooldown(t *testing.T) {
	t.Helper()
	reset := func() {
		tuiCooldown.Lock()
		tuiCooldown.until, tuiCooldown.reason = time.Time{}, ""
		tuiCooldown.Unlock()
	}
	reset()
	t.Cleanup(reset)
}

// With claude_interactive a conversation starts through the bridge, which gets both commands; its fallback to
// claude -p is visible in the execution report and holds the next starts in print mode for a while.
func TestInteractiveStartGoesThroughTheBridgeAndItsFallbackIsVisible(t *testing.T) {
	fakeClaudeHome(t, "read line\necho '{\"type\":\"system\",\"subtype\":\"init\"}'\nsleep 30\n")
	resetTUICooldown(t)
	n := codexTestNode(t)
	withFakeHub(t, n)
	specFile := fakeTUIBridge(t, n)
	id, err := n.assistantSend(context.Background(), "", t.TempDir(), "claude", "hello", nil, "", "", modelExecution{Interactive: true})
	if err != nil {
		t.Fatal(err)
	}
	defer n.stopAssistant(id)
	var spec tuiSpec
	deadline := time.Now().Add(5 * time.Second)
	for {
		raw, _ := os.ReadFile(specFile)
		if json.Unmarshal(bytes.TrimSpace(raw), &spec) == nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if spec.Session != id || len(spec.TUI) == 0 || len(spec.Print) == 0 {
		t.Fatalf("spec %+v", spec)
	}
	joined := func(a []string) string { return " " + strings.Join(a, " ") + " " }
	if strings.Contains(joined(spec.TUI), " -p ") || strings.Contains(joined(spec.TUI), " stream-json ") {
		t.Fatalf("the interactive command keeps print flags: %q", spec.TUI)
	}
	if !strings.Contains(joined(spec.Print), " -p ") || !strings.Contains(joined(spec.TUI), " --session-id "+id+" ") {
		t.Fatalf("commands: tui %q print %q", spec.TUI, spec.Print)
	}
	var exec proto.AssistantExecution
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if exec = n.assistantExecution(context.Background(), id); exec.Mode == executionModePrint {
			break
		}
	}
	if exec.Mode != executionModePrint || !strings.Contains(exec.ModeNote, "tmux is not installed") {
		t.Fatalf("the fallback is not in the execution report: %+v", exec)
	}
	if reason, cooling := tuiCoolingDown(time.Now()); !cooling || !strings.Contains(reason, "tmux") {
		t.Fatalf("no cooldown after a fallback: %q %v", reason, cooling)
	}
	if want, why := tuiWanted(modelExecution{Interactive: true}, false, false, spawnRound{}, ""); want || why == "" {
		t.Fatalf("a start during the cooldown: %v %q", want, why)
	}
}

// Without the setting nothing changes: claude -p itself, never the bridge.
func TestPrintModeStartNeverRunsTheBridge(t *testing.T) {
	fakeClaudeHome(t, "read line\necho '{\"type\":\"system\",\"subtype\":\"init\"}'\nsleep 30\n")
	resetTUICooldown(t)
	n := codexTestNode(t)
	withFakeHub(t, n)
	specFile := fakeTUIBridge(t, n)
	id, err := n.assistantSend(context.Background(), "", t.TempDir(), "claude", "hello", nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer n.stopAssistant(id)
	if _, err := os.Stat(specFile); err == nil {
		t.Fatal("the bridge ran without claude_interactive")
	}
	if exec := n.assistantExecution(context.Background(), id); exec.Mode != "" {
		t.Fatalf("a print-mode conversation reports a mode: %+v", exec)
	}
}

// A bridge whose interactive Claude Code cannot start (here: no tmux on PATH) says why first, then hands what it was
// sent to the print-mode command and is that command from then on: its output, its exit status.
func TestBridgeFallsBackToPrintModeVisibly(t *testing.T) {
	dir := t.TempDir()
	printer := filepath.Join(dir, "print")
	// claude -p stand-in: echoes each line back and exits 7 when stdin ends
	if err := os.WriteFile(printer, []byte("#!/bin/sh\nwhile IFS= read -r l; do printf 'got %s\\n' \"$l\"; done\nexit 7\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir) // no tmux
	spec, _ := json.Marshal(tuiSpec{Session: "s1", Cwd: dir, TUI: []string{"claude"}, Print: []string{printer}})
	in := string(spec) + "\n" + `{"type":"user","message":{"content":[{"type":"text","text":"hi"}]}}` + "\n"
	var out, errb bytes.Buffer
	r, w := io.Pipe()
	go func() {
		_, _ = io.WriteString(w, in)
		time.Sleep(time.Second) // stdin stays open while the start fails, as the node's does
		_ = w.Close()
	}()
	status := AssistantTUI(nil, r, &out, &errb)
	if status != 7 {
		t.Fatalf("status %d, stderr %s", status, errb.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], tuiFallbackSubtype) || !strings.Contains(lines[0], "tmux is not installed") ||
		lines[1] != `got {"type":"user","message":{"content":[{"type":"text","text":"hi"}]}}` {
		t.Fatalf("output:\n%s", out.String())
	}
	if !strings.Contains(errb.String(), "running claude -p instead") {
		t.Fatalf("stderr: %s", errb.String())
	}
}

// What is typed for a message: its texts, an image as a file Claude Code can read, and no mode switch from its first
// character.
func TestTUIMessageText(t *testing.T) {
	dir := t.TempDir()
	text, err := tuiMessageText([]map[string]any{
		{"type": "text", "text": "!rm -rf is not a command here"},
		{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": "aGk="}},
	}, dir, 1)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "image-1-1.png")
	if text != " !rm -rf is not a command here\n\n[Image attached: "+path+"]" {
		t.Fatalf("text %q", text)
	}
	if b, _ := os.ReadFile(path); string(b) != "hi" {
		t.Fatalf("image %q", b)
	}
}

// Background work is read off the mode line, so the node keeps a process whose shell still runs.
func TestBackgroundTasksFromTheScreen(t *testing.T) {
	plain := "  ◐ Bash: sleep 40\n  ⏵⏵ bypass permissions on · 1 shell · ← 2 agents\n"
	if got := backgroundTasks(plain); !slices.Equal(got, []string{"Bash: sleep 40"}) {
		t.Fatalf("tasks %q", got)
	}
	if got := backgroundTasks("  ⏵⏵ bypass permissions on (shift+tab to cycle)\n"); len(got) != 0 {
		t.Fatalf("tasks on an idle screen %q", got)
	}
	if !tuiSpinnerRe.MatchString("· Zesting…\n") || tuiSpinnerRe.MatchString("✻ Brewed for 4s · done 2:14 AM\n") {
		t.Fatal("spinner")
	}
}
