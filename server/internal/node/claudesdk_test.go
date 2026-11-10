package node

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/identity"
)

// writeClaudeRun writes a Claude transcript in dir whose lines carry these entrypoints, one exchange each.
func writeClaudeRun(t *testing.T, dir, id string, age time.Duration, entrypoints ...string) {
	t.Helper()
	var b strings.Builder
	for i, ep := range entrypoints {
		fmt.Fprintf(&b, `{"type":"user","uuid":"u%d","timestamp":"2026-11-05T07:31:12Z","cwd":"/src/app","entrypoint":%q,"message":{"role":"user","content":[{"type":"text","text":"summarize the diff"}]}}`+"\n", i, ep)
		fmt.Fprintf(&b, `{"type":"assistant","uuid":"a%d","timestamp":"2026-11-05T07:31:20Z","entrypoint":%q,"message":{"role":"assistant","content":[{"type":"text","text":"done"}]}}`+"\n", i, ep)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, id+".jsonl")
	if err := os.WriteFile(p, []byte(b.String()+strings.Repeat(" ", 4096)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-age)
	_ = os.Chtimes(p, at, at)
}

// A program running Claude Code's command line once (`claude -p`, entrypoint sdk-cli) is no session of the user's: the
// offline list leaves it out (#3828). Persona's own runs stay — the assistant's, any manager's, a health space's or a
// guest's (started under its state directory), one the node keeps state for, a fork, a kept one — and so do the Agent
// SDKs' (an interactive host's, sdk-ts), a run the user went on with in a terminal, and one whose last entrypoint is not
// within the tail read.
func TestOfflineListLeavesOneShotRunsOut(t *testing.T) {
	t.Setenv("PERSONA_HOME", "")
	n, home := testNode(t)
	n.metas, n.bgScans = map[string]quickMeta{}, map[string]*bgScan{}
	const (
		oneShot   = "5d500000-0000-4000-8000-000000000001"
		resumed   = "5d500000-0000-4000-8000-000000000002" // started by `claude -p`, then resumed in a terminal
		cli       = "5d500000-0000-4000-8000-000000000003"
		sdkHost   = "5d500000-0000-4000-8000-000000000004" // an interactive host on the TypeScript SDK (Paseo)
		kept      = "5d500000-0000-4000-8000-000000000005"
		fork      = "5d500000-0000-4000-8000-000000000006"
		assistant = "5d500000-0000-4000-8000-000000000007" // this manager's assistant
		manager   = "5d500000-0000-4000-8000-000000000008" // another manager's assistant
		health    = "5d500000-0000-4000-8000-000000000009" // another manager's health space
		guest     = "5d500000-0000-4000-8000-00000000000a"
		driven    = "5d500000-0000-4000-8000-00000000000b" // the node keeps state for it
		hugeTail  = "5d500000-0000-4000-8000-00000000000c" // a one-shot run whose last line outgrows the tail read
	)
	n.kept, n.forked = map[string]bool{kept: true}, map[string]bool{fork: true}
	n.state.Sessions[driven] = sessionState{Cwd: "/src/app"}
	projects := filepath.Join(home, ".claude", "projects")
	dir := filepath.Join(projects, "-src-app")
	under := func(sub string) string {
		return filepath.Join(projects, nonAlnum.ReplaceAllString(filepath.Join(identity.DataDir(home), sub), "-"))
	}
	writeClaudeRun(t, dir, oneShot, time.Minute, "sdk-cli")
	writeClaudeRun(t, dir, resumed, 2*time.Minute, "sdk-cli", "cli")
	writeClaudeRun(t, dir, cli, 3*time.Minute, "cli")
	writeClaudeRun(t, dir, sdkHost, 4*time.Minute, "sdk-ts")
	writeClaudeRun(t, dir, kept, 5*time.Minute, "sdk-cli")
	writeClaudeRun(t, dir, fork, 6*time.Minute, "sdk-cli")
	writeClaudeRun(t, filepath.Join(projects, n.assistantProject()), assistant, 7*time.Minute, "sdk-cli")
	writeClaudeRun(t, under("assistant-m-1a2b3c"), manager, 8*time.Minute, "sdk-cli")
	writeClaudeRun(t, under("health-m-1a2b3c"), health, 9*time.Minute, "sdk-cli")
	writeClaudeRun(t, under("guest"), guest, 10*time.Minute, "sdk-cli")
	writeClaudeRun(t, dir, driven, 11*time.Minute, "sdk-cli")
	writeClaudeRun(t, dir, hugeTail, 12*time.Minute, "sdk-cli")
	big := filepath.Join(dir, hugeTail+".jsonl")
	f, _ := os.OpenFile(big, os.O_APPEND|os.O_WRONLY, 0o600)
	fmt.Fprintf(f, `{"type":"user","uuid":"r1","message":{"role":"user","content":[{"type":"tool_result","content":%q}]}}`+"\n", strings.Repeat("x", 2*oneShotTail))
	f.Close()
	_ = os.Chtimes(big, time.Now().Add(-12*time.Minute), time.Now().Add(-12*time.Minute))

	got := map[string]bool{}
	for _, s := range n.scanOffline() {
		got[s.SessionID] = true
	}
	for id, want := range map[string]bool{oneShot: false, resumed: true, cli: true, sdkHost: true, kept: true, fork: true,
		assistant: true, manager: true, health: true, guest: true, driven: true, hugeTail: true} {
		if got[id] != want {
			t.Errorf("%s listed=%v, want %v", id, got[id], want)
		}
	}
}

// One-shot runs never count toward the 80 the offline list shows: a script running `claude -p` all day does not push the
// user's older sessions out.
func TestOneShotRunsDoNotTakeTheCap(t *testing.T) {
	n, home := testNode(t)
	n.metas, n.bgScans = map[string]quickMeta{}, map[string]*bgScan{}
	dir := filepath.Join(home, ".claude", "projects", "-src-app")
	for i := 0; i < 85; i++ {
		writeClaudeRun(t, dir, fmt.Sprintf("5d5ff000-0000-4000-8000-%012d", i), time.Duration(i+1)*time.Minute, "sdk-cli")
	}
	const older = "aaaaaaaa-0000-4000-8000-0000000000aa"
	writeClaudeRun(t, dir, older, 3*time.Hour, "cli")
	rows := n.scanOffline()
	if len(rows) != 1 || rows[0].SessionID != older {
		t.Fatalf("rows = %d, want only the user's older session", len(rows))
	}
	if len(n.metas) > 1 {
		t.Fatalf("parsed %d transcripts, want only the listed one: a one-shot run is told from its tail", len(n.metas))
	}
}
