package node

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

func desktopEnvironmentFixture(id, name, cwd string, additional ...string) map[string]any {
	return map[string]any{"id": id, "name": name, "createdAt": 1, "lastUsedAt": 2,
		"target": map[string]any{"kind": "local", "cwd": cwd, "additionalFolders": additional}, "settings": map[string]any{}}
}

func TestClaudeNativeEnvironmentsWithoutSessions(t *testing.T) {
	home := t.TempDir()
	profile, _ := useDesktopDirs(t)
	root, other := filepath.Join(home, "source"), filepath.Join(home, "assets")
	for _, dir := range []string{root, other} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	desktopCatalogJSON(t, filepath.Join(profile, "ccd-session-presets.json"), map[string]any{
		"byAccount": map[string]any{"account": map[string]any{"presets": []any{
			desktopEnvironmentFixture("first", "Product research", root),
			desktopEnvironmentFixture("second", "Release work", root, other),
		}}},
	})
	n := &Node{home: home}
	rows, sources, errs := n.desktopWorkspaces()
	if len(rows) != 3 || sources != 1 || len(errs) != 0 {
		t.Fatalf("saved environments without sessions = %+v, %d, %+v", rows, sources, errs)
	}
	if a, b, c := rows[0], rows[1], rows[2]; a.Title != "Product research" || b.Title != "Release work" || c.Title != b.Title ||
		a.SourceID == b.SourceID || b.SourceID != c.SourceID || b.ID == c.ID ||
		a.SourceID != claudeDesktopEnvironmentID(profile, "account", "first") || a.SourceID != a.ProjectID ||
		a.Cwd != root || b.Cwd != root || c.Cwd != other || !a.Available || !c.Available {
		t.Fatalf("native names and root identities = %+v", rows)
	}
	n.sessionScope = &nodeSessionScope{dir: root}
	if rows, sources, _ := n.desktopWorkspaces(); len(rows) != 0 || sources != 0 {
		t.Fatal("scoped node discovered native desktop environments")
	}
}

func TestClaudeNativeEnvironmentMembershipUsesProfileAndAccount(t *testing.T) {
	n, home := testNode(t)
	n.metas, n.bgScans = map[string]quickMeta{}, map[string]*bgScan{}
	first, second := useDesktopDirs(t)
	original, running := filepath.Join(home, "original"), filepath.Join(home, "worktree")
	for _, dir := range []string{original, running} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, profile := range []string{first, second} {
		name := "Other profile"
		if profile == first {
			name = "My native project"
		}
		desktopCatalogJSON(t, filepath.Join(profile, "ccd-session-presets.json"), map[string]any{
			"byAccount": map[string]any{
				"account": map[string]any{"presets": []any{desktopEnvironmentFixture("saved", name, original)}},
				"another": map[string]any{"presets": []any{desktopEnvironmentFixture("saved", "Other account", original)}},
			},
		})
	}
	entry := filepath.Join(first, "claude-code-sessions", "account", "org", "local_"+sidA+".json")
	desktopCatalogJSON(t, entry, map[string]any{"sessionId": "local_" + sidA, "cliSessionId": sidA,
		"cwd": running, "originCwd": original, "startedFromEnvironmentId": "saved", "title": "Session title"})
	line, _ := json.Marshal(map[string]any{"type": "user", "uuid": "u1", "cwd": running, "entrypoint": "claude-desktop",
		"message": map[string]string{"role": "user", "content": "Synthetic request"}})
	writeTranscript(t, home, running, sidA, time.Now(), string(line), `{"type":"custom-title","customTitle":"Session title"}`)
	index := readClaudeDesktopIndex(home)
	wantID := claudeDesktopEnvironmentID(first, "account", "saved")
	if got := index.about[sidA].environment; got != wantID {
		t.Fatalf("native membership = %q, want %q", got, wantID)
	}
	for _, live := range []bool{false, true} {
		var rows []proto.Session
		if live {
			n.state.put(sidA, sessionState{Launcher: "claude", Cwd: running})
			rows = n.claudeSessions([]claudeProc{{SessionID: sidA, Kind: "interactive", Entrypoint: "claude-desktop", Cwd: running, Status: "busy"}}, nil)
		} else {
			rows = n.scanOffline()
		}
		s := byID(rows)[sidA]
		if s.SpaceID != wantID || s.SpaceName != "My native project" || s.SpaceKind != proto.SourceClaudeDesktop ||
			s.RepoRoot != original || s.Cwd != running || s.Title != "Session title" {
			t.Errorf("live=%v native placement = %+v", live, s)
		}
	}
	rows, _, errs := n.desktopWorkspaces()
	if len(rows) != 4 || len(errs) != 0 {
		t.Fatalf("native projects should replace inferred folder rows: %+v, %+v", rows, errs)
	}
}

func TestClaudeNativeEnvironmentFailureKeepsSessionDiscovery(t *testing.T) {
	home := t.TempDir()
	profile, _ := useDesktopDirs(t)
	root := filepath.Join(home, "project")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(profile, "claude-code-sessions", "account", "org", "local_"+sidA+".json")
	desktopCatalogJSON(t, entry, map[string]any{"cliSessionId": sidA, "cwd": root, "startedFromEnvironmentId": true})
	if err := os.WriteFile(filepath.Join(profile, "ccd-session-presets.json"), []byte(`{"byAccount":`), 0o600); err != nil {
		t.Fatal(err)
	}
	n := &Node{home: home}
	rows, sources, errs := n.desktopWorkspaces()
	if len(rows) != 1 || sources != 1 || len(errs) != 1 || rows[0].Cwd != root || rows[0].SourceID != claudeDesktopProjectID(root) {
		t.Fatalf("corrupt mirror must preserve directory discovery: %+v, %d, %+v", rows, sources, errs)
	}
	if !readClaudeDesktopIndex(home).visible[sidA] {
		t.Fatal("invalid optional environment membership hid a readable session")
	}
}
