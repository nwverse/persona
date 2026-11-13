package node

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/proto"
)

func desktopCatalogJSON(t *testing.T, path string, value any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDesktopWorkspacesWithoutTerminalOrSessions(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "code", "app")
	other := filepath.Join(home, "code", "other")
	for _, dir := range []string{root, other} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	desktopCatalogJSON(t, filepath.Join(home, ".codex", ".codex-global-state.json"), map[string]any{
		"local-projects": map[string]codexAppProject{
			"first": {Name: "Renamed project", RootPaths: []string{root}},
			"multi": {Name: "Two directories", RootPaths: []string{other, root, other}},
			"empty": {Name: "No directory yet"},
		},
		"project-order": []string{"empty", "first", "multi"},
	})
	n := &Node{home: home}
	got, err := n.listWorkspaces(context.Background())
	if err != nil || len(got.Workspaces) != 4 || len(got.Errors) != 0 {
		t.Fatalf("desktop-only catalog = %+v, %v", got, err)
	}
	if empty := got.Workspaces[0]; empty.Title != "No directory yet" || empty.Index != 1 || empty.Cwd != "" || empty.Available {
		t.Fatalf("empty project = %+v", empty)
	}
	first := got.Workspaces[1]
	if first.Title != "Renamed project" || first.Source != "codex_app" || first.SourceID != first.ProjectID ||
		first.Cwd != root || !first.Online || !first.Available || first.Index != 2 {
		t.Fatalf("saved project = %+v", first)
	}
	if got.Workspaces[2].ProjectID != got.Workspaces[3].ProjectID || got.Workspaces[2].ID == got.Workspaces[3].ID {
		t.Fatalf("multi-root identity = %+v", got.Workspaces[2:])
	}
}

func TestDesktopWorkspacesKeepPartialSourcesAndEmptyCatalog(t *testing.T) {
	home := t.TempDir()
	state := filepath.Join(home, ".codex", ".codex-global-state.json")
	desktopCatalogJSON(t, state, map[string]any{"local-projects": map[string]any{}})
	failed := &workspaceHost{bareHost: bareHost{kind: host.Orca}, err: errors.New("snapshot failed")}
	n := &Node{home: home, hosts: host.NewRegistry(failed)}
	got, err := n.listWorkspaces(context.Background())
	if err != nil || got.Workspaces == nil || len(got.Workspaces) != 0 || len(got.Errors) != 1 || got.Errors[0].Source != host.Orca {
		t.Fatalf("valid empty desktop source with failed host = %+v, %v", got, err)
	}
	if err := os.Remove(state); err != nil {
		t.Fatal(err)
	}
	// With neither a saved desktop source nor a successful engine, a failure remains a failure.
	if _, err := n.listWorkspaces(context.Background()); err == nil || !strings.Contains(err.Error(), "snapshot failed") {
		t.Fatalf("failed source without desktop = %v", err)
	}
}

func TestDesktopWorkspacesRespectScopeAndBlockedDirectories(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "private")
	desktopCatalogJSON(t, filepath.Join(home, ".codex", ".codex-global-state.json"), map[string]any{
		"local-projects": map[string]codexAppProject{"p": {Name: "Project", RootPaths: []string{root}}},
	})
	n := &Node{home: home, sessionScope: &nodeSessionScope{dir: root}}
	if projects, sources, errs := n.desktopWorkspaces(); len(projects) != 0 || sources != 0 || len(errs) != 0 {
		t.Fatalf("scoped node discovered desktop projects: %+v %d %+v", projects, sources, errs)
	}
	n.sessionScope = nil
	n.cdirs = &claudeDirs{blocked: []string{root}}
	if projects, sources, errs := n.desktopWorkspaces(); len(projects) != 0 || sources != 1 || len(errs) != 0 {
		t.Fatalf("blocked directory listed: %+v %d %+v", projects, sources, errs)
	}
}

func TestClaudeDesktopCatalogAndRowsShareOriginalProject(t *testing.T) {
	n, home := testNode(t)
	n.metas, n.bgScans = map[string]quickMeta{}, map[string]*bgScan{}
	original, running := filepath.Join(home, "app"), filepath.Join(home, "worktree")
	for _, dir := range []string{original, running} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	_, desktop := useDesktopDirs(t)
	entry := filepath.Join(desktop, "claude-code-sessions", "account", "org", "local_"+sidA+".json")
	desktopCatalogJSON(t, entry, map[string]any{"sessionId": "local_" + sidA, "cliSessionId": sidA,
		"title": "Saved title", "cwd": running, "originCwd": original})
	desktopCatalogJSON(t, filepath.Join(filepath.Dir(entry), "local_"+sidB+".json"), map[string]any{
		"cliSessionId": sidB, "isArchived": true, "cwd": filepath.Join(home, "archived")})
	line, _ := json.Marshal(map[string]any{"type": "user", "uuid": "u1", "cwd": running,
		"entrypoint": "claude-desktop", "message": map[string]string{"role": "user", "content": "Check this project"}})
	writeTranscript(t, home, running, sidA, time.Now(), string(line),
		`{"type":"custom-title","customTitle":"Transcript title"}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Ready"}]}}`)

	index := readClaudeDesktopIndex(home)
	projects, sources, errs := n.desktopWorkspaces()
	if sources != 1 || len(projects) != 1 || len(errs) != 0 {
		t.Fatalf("Claude catalog = %+v, %d, %+v", projects, sources, errs)
	}
	w := projects[0]
	if w.Source != proto.SourceClaudeDesktop || w.Cwd != original || w.Title != "app" || !w.Available {
		t.Fatalf("Claude project = %+v", w)
	}
	offline := byID(n.scanOffline())[sidA]
	if offline.SpaceID != w.SourceID || offline.SpaceKind != w.Source || offline.SourceKind != proto.SourceClaudeDesktop ||
		offline.RepoRoot != original || offline.Cwd != running || offline.Title != "Transcript title" || offline.SessionID != sidA {
		t.Fatalf("offline desktop identity = %+v", offline)
	}
	n.state.put(sidA, sessionState{Launcher: "claude", Cwd: running})
	liveRows := n.claudeSessions([]claudeProc{{SessionID: sidA, Kind: "interactive", Entrypoint: "claude-desktop", Cwd: running, Status: "busy"}}, nil)
	if len(liveRows) != 1 {
		t.Fatalf("live desktop rows = %+v", liveRows)
	}
	live := liveRows[0]
	if live.SpaceID != offline.SpaceID || live.RepoRoot != offline.RepoRoot || live.Cwd != running || live.Title != "Transcript title" || live.Status != proto.StatusWorking {
		t.Fatalf("live desktop placement = %+v", live)
	}
	terminal := proto.Session{SessionID: sidA, Agent: "claude", PaneID: "w1:p1", SpaceID: "terminal", Cwd: running}
	n.decorateClaudeDesktop(&terminal, index, map[string]string{})
	if terminal.SpaceID != "terminal" || terminal.SourceKind != "" {
		t.Fatalf("desktop index took a terminal-owned session: %+v", terminal)
	}
}

func TestClaudeDesktopProjectRootUsesGitSharedDirectory(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "project")
	linked := filepath.Join(home, "linked")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		if out, err := git(context.Background(), root, args...); err != nil {
			t.Fatalf("git %v: %s %v", args, out, err)
		}
	}
	run("init")
	run("-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-m", "Initial")
	run("worktree", "add", "--detach", linked)
	index := claudeDesktopIndex{about: map[string]claudeDesktopAbout{sidA: {cwd: linked}}}
	n := &Node{home: home}
	s := proto.Session{SessionID: sidA, Agent: "claude", Cwd: linked}
	n.decorateClaudeDesktop(&s, index, map[string]string{})
	if s.RepoRoot != root || s.SpaceName != "project" || s.Cwd != linked {
		t.Fatalf("linked desktop session = %+v", s)
	}
}
