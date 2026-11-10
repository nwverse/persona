package node

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeClaudeRun writes a Claude transcript in dir whose lines carry these entrypoints, one exchange each.
func writeClaudeRun(t *testing.T, dir, id string, age time.Duration, entrypoints ...string) {
	t.Helper()
	var b strings.Builder
	for i, ep := range entrypoints {
		fmt.Fprintf(&b, `{"type":"user","uuid":"u%d","timestamp":"2026-11-05T07:31:12Z","cwd":"/src/app","entrypoint":%q,"message":{"role":"user","content":[{"type":"text","text":"summarize the diff"}]}}`+"\n", i, ep)
		fmt.Fprintf(&b, `{"type":"assistant","uuid":"a%d","timestamp":"2026-11-05T07:31:20Z","entrypoint":%q,"message":{"role":"assistant","content":[{"type":"text","text":"done"}]}}`+"\n", i, ep)
	}
	p := filepath.Join(dir, id+".jsonl")
	if err := os.WriteFile(p, []byte(b.String()+strings.Repeat(" ", 4096)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-age)
	_ = os.Chtimes(p, at, at)
}

// A program's run of Claude Code (`claude -p`, an SDK: entrypoint "sdk-…") is no session of the user's: the offline list
// leaves it out, as a running one is left to its program (claudebg.go) and as Codex's program runs are. One the user went
// on with in a terminal, a kept one, Persona's own fork and the assistant's conversations stay.
func TestOfflineListLeavesProgramRunsOut(t *testing.T) {
	n, home := testNode(t)
	n.metas, n.bgScans = map[string]quickMeta{}, map[string]*bgScan{}
	const (
		program   = "5d500000-0000-4000-8000-000000000001"
		resumed   = "5d500000-0000-4000-8000-000000000002" // started by a program, then resumed in a terminal
		cli       = "5d500000-0000-4000-8000-000000000003"
		desktop   = "5d500000-0000-4000-8000-000000000004"
		kept      = "5d500000-0000-4000-8000-000000000005"
		fork      = "5d500000-0000-4000-8000-000000000006"
		assistant = "5d500000-0000-4000-8000-000000000007"
		ts        = "5d500000-0000-4000-8000-000000000008"
	)
	n.kept, n.forked = map[string]bool{kept: true}, map[string]bool{fork: true}
	dir := filepath.Join(home, ".claude", "projects", "-src-app")
	own := filepath.Join(home, ".claude", "projects", n.assistantProject())
	for _, d := range []string{dir, own} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeClaudeRun(t, dir, program, time.Minute, "sdk-cli")
	writeClaudeRun(t, dir, resumed, 2*time.Minute, "sdk-cli", "cli")
	writeClaudeRun(t, dir, cli, 3*time.Minute, "cli")
	writeClaudeRun(t, dir, desktop, 4*time.Minute, "claude-desktop")
	writeClaudeRun(t, dir, kept, 5*time.Minute, "sdk-cli")
	writeClaudeRun(t, dir, fork, 6*time.Minute, "sdk-cli")
	writeClaudeRun(t, own, assistant, 7*time.Minute, "sdk-cli")
	writeClaudeRun(t, dir, ts, 8*time.Minute, "sdk-ts")
	got := map[string]bool{}
	for _, s := range n.scanOffline() {
		got[s.SessionID] = true
	}
	for id, want := range map[string]bool{program: false, ts: false, resumed: true, cli: true, desktop: true, kept: true, fork: true, assistant: true} {
		if got[id] != want {
			t.Errorf("%s listed=%v, want %v", id, got[id], want)
		}
	}
}

// Program runs never count toward the 80 the offline list shows: a script running `claude -p` all day does not push the
// user's older sessions out.
func TestProgramRunsDoNotTakeTheCap(t *testing.T) {
	n, home := testNode(t)
	n.metas, n.bgScans = map[string]quickMeta{}, map[string]*bgScan{}
	dir := filepath.Join(home, ".claude", "projects", "-src-app")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 85; i++ {
		writeClaudeRun(t, dir, fmt.Sprintf("5d5ff000-0000-4000-8000-%012d", i), time.Duration(i+1)*time.Minute, "sdk-cli")
	}
	const older = "aaaaaaaa-0000-4000-8000-0000000000aa"
	writeClaudeRun(t, dir, older, 3*time.Hour, "cli")
	rows := n.scanOffline()
	if len(rows) != 1 || rows[0].SessionID != older {
		t.Fatalf("rows = %d, want only the user's older session", len(rows))
	}
}
