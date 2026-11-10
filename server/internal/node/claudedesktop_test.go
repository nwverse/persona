package node

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeEntrypointTranscript writes a Claude transcript of the desktop app (or of the command line for "cli").
func writeEntrypointTranscript(t *testing.T, dir, id, entrypoint string, age time.Duration) {
	t.Helper()
	body := fmt.Sprintf(`{"type":"user","uuid":"u1","timestamp":"2026-11-05T07:31:12Z","cwd":"/src/app","entrypoint":%q,"message":{"role":"user","content":[{"type":"text","text":"hello"}]}}`+"\n"+
		`{"type":"assistant","uuid":"a1","timestamp":"2026-11-05T07:31:20Z","entrypoint":%q,"message":{"role":"assistant","content":[{"type":"text","text":"done"}]}}`+"\n", entrypoint, entrypoint)
	p := filepath.Join(dir, id+".jsonl")
	if err := os.WriteFile(p, []byte(body+strings.Repeat(" ", 4096)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-age)
	_ = os.Chtimes(p, at, at)
}

// writeDesktopEntry writes one session of the desktop app's index under appDir (extra: more JSON fields, or "").
func writeDesktopEntry(t *testing.T, appDir, name, transcript string, archived bool, prior []string, extra string) string {
	t.Helper()
	dir := filepath.Join(appDir, "claude-code-sessions", "account", "org")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	priorJSON, _ := json.Marshal(prior)
	body := fmt.Sprintf(`{"sessionId":"local_%s","cliSessionId":%q,"isArchived":%v,"priorCliSessionIds":%s,"title":"t","cwd":"/src/app"%s}`,
		name, transcript, archived, priorJSON, extra)
	p := filepath.Join(dir, "local_"+name+".json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// useDesktopDirs points the desktop app's data directories at two under a test directory: the first-party app's and
// the third-party build's.
func useDesktopDirs(t *testing.T) (first, third string) {
	t.Helper()
	base := t.TempDir()
	first, third = filepath.Join(base, "Claude"), filepath.Join(base, "Claude-3p")
	old := claudeDesktopDirs
	claudeDesktopDirs = func(string) []string { return []string{first, third} }
	t.Cleanup(func() { claudeDesktopDirs = old })
	return first, third
}

// The offline list shows the desktop app's sessions as its sidebar does (#3304): not archived sessions, not a session's
// earlier transcript while its current one is listed. Anything no entry names is listed as before.
func TestOfflineListFollowsTheDesktopSidebar(t *testing.T) {
	const (
		main      = "dddddddd-0000-4000-8000-000000000001"
		archived  = "dddddddd-0000-4000-8000-000000000002"
		btwDone   = "dddddddd-0000-4000-8000-000000000003" // a /btw question the app archived
		btwOpen   = "dddddddd-0000-4000-8000-000000000004" // one it did not: listed (spawnedFrom is not read)
		earlier   = "dddddddd-0000-4000-8000-000000000005" // main's earlier transcript
		gap       = "dddddddd-0000-4000-8000-000000000006" // an earlier transcript whose session's current one is not there
		missing   = "dddddddd-0000-4000-8000-000000000007"
		both      = "dddddddd-0000-4000-8000-000000000008" // named by an archived entry and by an unarchived one
		firstApp  = "dddddddd-0000-4000-8000-000000000009" // archived in the first-party app
		unnamed   = "dddddddd-0000-4000-8000-00000000000a" // a desktop transcript no entry names
		fork      = "dddddddd-0000-4000-8000-00000000000b" // Persona's "Branch to New Chat" copy, archived in the app
		kept      = "dddddddd-0000-4000-8000-00000000000c" // kept, archived in the app
		cli       = "cccccccc-0000-4000-8000-000000000001"
		corrupted = "dddddddd-0000-4000-8000-00000000000d" // its entry cannot be read
	)
	transcripts := []string{main, archived, btwDone, btwOpen, earlier, gap, both, firstApp, unnamed, fork, kept, corrupted}
	listed := func(t *testing.T, withIndex bool) map[string]bool {
		n, home := testNode(t)
		n.metas, n.bgScans = map[string]quickMeta{}, map[string]*bgScan{}
		n.forked, n.kept = map[string]bool{fork: true}, map[string]bool{kept: true}
		dir := filepath.Join(home, ".claude", "projects", "-src-app")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		for i, id := range transcripts {
			writeEntrypointTranscript(t, dir, id, "claude-desktop-3p", time.Duration(i+1)*time.Minute)
		}
		writeEntrypointTranscript(t, dir, cli, "cli", time.Hour)
		first, third := useDesktopDirs(t)
		if withIndex {
			writeDesktopEntry(t, third, "main", main, false, []string{earlier}, "")
			writeDesktopEntry(t, third, "archived", archived, true, nil, "")
			writeDesktopEntry(t, third, "btwdone", btwDone, true, nil, `,"spawnedFrom":{"btw":true,"sideSession":true,"userFork":true}`)
			writeDesktopEntry(t, third, "btwopen", btwOpen, false, nil, `,"spawnedFrom":{"btw":true,"sideSession":true,"userFork":true}`)
			writeDesktopEntry(t, third, "gap", missing, false, []string{gap}, "")
			writeDesktopEntry(t, third, "both1", both, true, nil, "")
			writeDesktopEntry(t, third, "both2", both, false, nil, "")
			writeDesktopEntry(t, first, "first", firstApp, true, nil, "")
			writeDesktopEntry(t, third, "fork", fork, true, nil, "")
			writeDesktopEntry(t, third, "kept", kept, true, nil, "")
			bad := writeDesktopEntry(t, third, "corrupted", corrupted, true, nil, "")
			os.WriteFile(bad, []byte(`{"cliSessionId":"`+corrupted+`","isArchived":tru`), 0o600)
		}
		out := map[string]bool{}
		for _, s := range n.scanOffline() {
			out[s.SessionID] = true
		}
		return out
	}

	got := listed(t, true)
	want := map[string]bool{main: true, btwOpen: true, gap: true, both: true, unnamed: true, fork: true, kept: true, cli: true, corrupted: true,
		archived: false, btwDone: false, earlier: false, firstApp: false}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s listed=%v, want %v", id, got[id], w)
		}
	}
	// Without the app's index every transcript is listed, as before.
	got = listed(t, false)
	for _, id := range append(transcripts, cli) {
		if !got[id] {
			t.Errorf("without an index %s is missing", id)
		}
	}
}

// An entry is read again when its file changes: archiving a session in the app takes it off the list, unarchiving
// brings it back, and a removed entry hides nothing.
func TestClaudeDesktopIndexFollowsChanges(t *testing.T) {
	_, third := useDesktopDirs(t)
	const id = "eeeeeeee-0000-4000-8000-000000000001"
	present := map[string]bool{id: true}
	hidden := func() bool { return readClaudeDesktopIndex("").hidden(id, present) }
	p := writeDesktopEntry(t, third, "s", id, false, nil, "")
	if hidden() {
		t.Fatal("an unarchived session is hidden")
	}
	writeDesktopEntry(t, third, "s", id, true, nil, "")
	later := time.Now().Add(time.Minute)
	_ = os.Chtimes(p, later, later)
	if !hidden() {
		t.Fatal("an archived session is listed")
	}
	os.Remove(p)
	if hidden() {
		t.Fatal("without an entry nothing is hidden")
	}
}

func TestClaudeDesktopDirsAreMacOnly(t *testing.T) {
	dirs := claudeDesktopDirs("/home/u")
	if len(dirs) != 0 && (len(dirs) != 2 || !strings.HasSuffix(dirs[0], filepath.Join("Application Support", "Claude")) || !strings.HasSuffix(dirs[1], "Claude-3p")) {
		t.Fatalf("dirs = %v", dirs)
	}
}
