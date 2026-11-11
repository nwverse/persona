package node

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// A stopped agent's row is read-only (#4754): it can be read and its terminal opened, nothing else; a branch nothing
// has started yet stays writable (its first message starts it).
func TestStoppedAgentRowIsReadOnly(t *testing.T) {
	n, _, home := herdrNode(t)
	cwd := mkdir(t, filepath.Join(home, "proj"))
	for _, id := range []string{sidA, sidB} {
		writeTranscript(t, home, cwd, id, time.Now().Add(-time.Hour), `{"type":"custom-title","customTitle":"fix the build"}`,
			`{"type":"user","timestamp":"2026-01-01T00:00:00Z","cwd":"`+cwd+`","message":{"role":"user","content":"hi"},"uuid":"u1"}`,
			`{"type":"assistant","timestamp":"2026-01-01T00:00:01Z","message":{"role":"assistant","content":[{"type":"text","text":"`+
				strings.Repeat("done ", 600)+`"}]},"uuid":"a1","parentUuid":"u1"}`) // over the 2 KB a listed session has
	}
	n.freshForks = map[string]bool{sidB: true}
	offlineCache.Lock()
	offlineCache.at = time.Time{} // scanned now, not from another test's look
	offlineCache.Unlock()
	rows := byID(n.sessionList(t.Context(), false))
	if s := rows[sidA]; s.Status != proto.StatusOffline || s.Capabilities == nil || s.Capabilities.Send || !s.Capabilities.Read ||
		!s.Capabilities.Terminal || s.ReadOnlyReason != stoppedReason {
		t.Errorf("stopped row = %+v (%+v)", s, s.Capabilities)
	}
	if s := rows[sidB]; s.Capabilities != nil || s.ReadOnlyReason != "" {
		t.Errorf("a fresh branch is read-only: %+v (%+v)", s, s.Capabilities)
	}
}

// keptAgent sets up a stopped Claude whose tab is still open: its pane marked for it, only a zsh in front, in cwd.
func keptAgent(t *testing.T) (*Node, *fakeHerdr, string) {
	t.Helper()
	n, f, home := herdrNode(t)
	cwd := mkdir(t, filepath.Join(home, "proj"))
	writeTranscript(t, home, cwd, sidA, time.Now().Add(-time.Hour),
		`{"type":"user","cwd":"`+cwd+`","message":{"role":"user","content":"hi"},"uuid":"u1"}`)
	n.state.put(sidA, sessionState{Launcher: "claude", Cwd: cwd})
	f.reply("session.snapshot", snapshot(nil, nil, nil, []any{shellPane("w1:p1", "w1:t1", "w1", cwd, sidA)}))
	f.reply("pane.process_info", map[string]any{"process_info": map[string]any{"foreground_processes": []any{
		map[string]any{"pid": 1, "name": "zsh", "argv0": "-zsh"}}}})
	listSessions(t, n)
	f.reset()
	return n, f, cwd
}

// The terminal of a stopped agent is the shell it left: the terminal calls find its pane (not send, which refuses).
func TestStoppedAgentTerminalIsTheKeptShell(t *testing.T) {
	n, _, _ := keptAgent(t)
	if pane, err := n.terminalPane(t.Context(), sidA); err != nil || pane != "w1:p1" {
		t.Fatalf("terminal pane = %q, %v", pane, err)
	}
	if _, err := n.paneFor(t.Context(), sidA); err == nil {
		t.Error("send would type into the kept shell")
	}
}

// The user's Resume (session.start with resume_session_id) runs claude --resume in the shell the agent left, no new
// tab, when that shell is at rest in the session's directory; one that moved elsewhere gets a new tab instead.
func TestResumeRunsInTheKeptShell(t *testing.T) {
	n, f, cwd := keptAgent(t)
	n.claudeStart, n.claudeReady = 200*time.Millisecond, 200*time.Millisecond // the fake shell never runs claude
	_, _ = request(t, n, "session.start", map[string]any{"resume_session_id": sidA})
	if got := f.trace("tab.create", "workspace.create"); len(got) != 0 {
		t.Errorf("opened a tab: %q", got)
	}
	var typed []string
	for _, c := range f.calls() {
		if c.Method == "pane.send_input" {
			typed = append(typed, str(c.Params["pane_id"])+" "+str(c.Params["text"]))
		}
	}
	if len(typed) == 0 || !strings.HasPrefix(typed[0], "w1:p1 ") || !strings.Contains(typed[0], "--resume "+sidA) {
		t.Fatalf("typed %q", typed)
	}
	if got := f.trace("tab.close", "pane.close"); len(got) != 0 {
		t.Errorf("closed the kept terminal: %q", got)
	}

	// The shell moved to another directory: claude --resume would not find the session there.
	n2, f2, _ := keptAgent(t)
	f2.reply("session.snapshot", snapshot(nil, nil, nil, []any{shellPane("w1:p1", "w1:t1", "w1", filepath.Dir(cwd), sidA)}))
	listSessions(t, n2)
	if ctx := n2.inKeptTerminal(t.Context(), sidA, cwd); adoptedPane(ctx) != nil {
		t.Error("resumed in a shell that left the session's directory")
	}
}
