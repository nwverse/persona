package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/proto"
)

// paseoWorkspacesHello is Paseo keeping workspaces of its own (caps.workspaces, runtimeworkspaces.go).
func paseoWorkspacesHello() map[string]any {
	h := paseoHello()
	caps := h["caps"].(map[string]any)
	caps["workspaces"] = true
	return h
}

// runtimeRepo is a node whose only engine is Paseo, its runtime keeping workspaces, on a real repository: branch main
// with one commit, a second local branch "spare". The fake runtime makes worktrees as Paseo does: git worktree add in
// its own folder (trees), a new branch from base, an existing one as it is.
type runtimeRepo struct {
	n             *Node
	rt            *fakeRuntime
	repo, trees   string
	creates       atomic.Int32
	mainWorkspace map[string]any
}

func runtimeRepoNode(t *testing.T) *runtimeRepo {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_AUTHOR_NAME", "Sample Dev")
	t.Setenv("GIT_AUTHOR_EMAIL", "dev@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "Sample Dev")
	t.Setenv("GIT_COMMITTER_EMAIL", "dev@example.com")
	n, _ := runtimeNode(t)
	if err := n.useEngine(context.Background(), proto.EnginePaseo); err != nil {
		t.Fatal(err)
	}
	r := &runtimeRepo{n: n}
	r.repo = resolved(t, mkdir(t, filepath.Join(t.TempDir(), "proj")))
	r.trees = filepath.Join(resolved(t, t.TempDir()), "worktrees", "abcd1234")
	runPullGit(t, r.repo, "init", "--initial-branch=main")
	if err := os.WriteFile(filepath.Join(r.repo, "app.txt"), []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runPullGit(t, r.repo, "add", "app.txt")
	runPullGit(t, r.repo, "commit", "-m", "first")
	runPullGit(t, r.repo, "branch", "spare")
	r.rt = dialRuntime(t, n, paseoWorkspacesHello())
	r.rt.answer = func(method string, params json.RawMessage) (any, error) {
		var p map[string]any
		_ = json.Unmarshal(params, &p)
		switch method {
		case "workspace.prepare":
			return map[string]any{"checkouts": r.trees}, nil
		case "workspace.create":
			r.creates.Add(1)
			branch, name := str(p["branch"]), str(p["name"])
			if branch == "" {
				branch = fmt.Sprintf("generated-%d", r.creates.Load())
			}
			if name == "" {
				name = branch
			}
			dest := filepath.Join(r.trees, name)
			args := []string{"worktree", "add", "--", dest, branch}
			if p["create_branch"] == true {
				args = []string{"worktree", "add", "-b", branch, "--", dest, str(p["base"])}
			}
			if err := os.MkdirAll(r.trees, 0o755); err != nil {
				return nil, err
			}
			if _, err := git(context.Background(), str(p["cwd"]), args...); err != nil {
				return nil, err
			}
			return map[string]any{"workspace": map[string]any{"workspace_id": fmt.Sprintf("wks_new%d", r.creates.Load()), "title": branch,
				"cwd": dest, "kind": "worktree", "branch": branch, "space_id": "prj_1", "space_name": "proj", "repo_root": r.repo, "owned": true}}, nil
		}
		return map[string]any{"ok": true}, nil
	}
	r.mainWorkspace = map[string]any{"workspace_id": "wks_main", "title": "main", "cwd": r.repo, "kind": "checkout", "branch": "main",
		"space_id": "prj_1", "space_name": "proj", "repo_root": r.repo}
	_ = r.rt.peer.Notify("runtime.workspaces", map[string]any{"workspaces": []any{r.mainWorkspace}})
	waitFor(t, "the runtime's workspaces", func() bool { return n.isRuntimeWorkspace("wks_main") })
	return r
}

// catalog is the node's workspaces.list, by source id.
func catalog(t *testing.T, n *Node) map[string]proto.Workspace {
	t.Helper()
	raw, err := nodeCall(t, n, "workspaces.list", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	var res proto.WorkspacesResponse
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	out := map[string]proto.Workspace{}
	for _, w := range res.Workspaces {
		out[w.SourceID] = w
	}
	return out
}

// An engine runtime that keeps workspaces is the machine's catalog: each workspace it lists, sessions in it or not, as
// the apps read a host's (source, project, main checkout); what it may not list is left out.
func TestRuntimeWorkspacesAreTheCatalog(t *testing.T) {
	n, _ := runtimeNode(t)
	if err := n.useEngine(context.Background(), proto.EnginePaseo); err != nil {
		t.Fatal(err)
	}
	dir := resolved(t, t.TempDir())
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	rt := dialRuntime(t, n, paseoWorkspacesHello())
	_ = rt.peer.Notify("runtime.workspaces", map[string]any{"workspaces": []any{
		map[string]any{"workspace_id": "wks_main", "title": "Main", "cwd": link, "kind": "checkout", "branch": "main", "space_id": "prj_1", "space_name": "app", "repo_root": link},
		map[string]any{"workspace_id": "wks_tree", "cwd": "/w/trees/feat", "kind": "worktree", "branch": "feat", "space_id": "prj_1", "space_name": "app", "repo_root": link, "owned": true},
		map[string]any{"workspace_id": "wks_notes", "title": "Notes", "cwd": "/w/notes", "kind": "directory", "space_id": "prj_2", "space_name": "notes"},
		map[string]any{"workspace_id": "bad id", "cwd": "/w/x"},
		map[string]any{"workspace_id": "wks_rel", "cwd": "relative/dir"},
		map[string]any{"workspace_id": "wks_main", "cwd": "/w/again"},
	}})
	waitFor(t, "the workspaces", func() bool { return len(catalog(t, n)) == 3 })
	got := catalog(t, n)
	main, tree, notes := got["wks_main"], got["wks_tree"], got["wks_notes"]
	if main.Source != "paseo" || main.Title != "Main" || main.Cwd != dir || main.ProjectPath != dir || main.ProjectID != "prj_1" ||
		main.ProjectName != "app" || main.IsMain == nil || !*main.IsMain || main.WorkspaceKind != "git" || !main.Available || main.Branch != "main" {
		t.Fatalf("the main checkout: %+v", main)
	}
	if tree.Title != "feat" || tree.IsMain == nil || *tree.IsMain || tree.WorkspaceKind != "git" || tree.Cwd != "/w/trees/feat" {
		t.Fatalf("the worktree, named by its branch: %+v", tree)
	}
	if notes.IsMain != nil || notes.WorkspaceKind != "folder" || notes.ProjectID != "prj_2" {
		t.Fatalf("a folder: %+v", notes)
	}
	if !n.isRuntimeWorkspace("wks_tree") || n.isRuntimeWorkspace("w1") {
		t.Fatal("the runtime's workspaces are told from a terminal host's")
	}

	// A runtime that does not say it keeps workspaces lists none: its pushes are not taken.
	dsh := dialRuntime(t, n, dshHello())
	_ = dsh.peer.Notify("runtime.workspaces", map[string]any{"workspaces": []any{map[string]any{"workspace_id": "dsh-1", "cwd": "/w/dsh"}}})
	_ = dsh.peer.Notify("runtime.sessions", map[string]any{"sessions": []any{map[string]any{"session_id": "session-x1", "status": "idle"}}})
	waitFor(t, "the dsh list (after its workspaces)", func() bool { _, ok := listed(t, n)["session-x1"]; return ok })
	if _, ok := catalog(t, n)["dsh-1"]; ok || n.isRuntimeWorkspace("dsh-1") {
		t.Fatal("a runtime without caps.workspaces listed its workspaces")
	}
}

// A machine whose only engine is a runtime has a catalog even with the runtime away (none, as a terminal host that is not
// running has none); a runtime that keeps no workspaces gives it none to have.
func TestRuntimeEngineCatalogWhenItCannotList(t *testing.T) {
	n, _ := runtimeNode(t)
	if err := n.useEngine(context.Background(), proto.EnginePaseo); err != nil {
		t.Fatal(err)
	}
	if got := catalog(t, n); len(got) != 0 {
		t.Fatalf("Paseo not running: %v", got)
	}
	rt := dialRuntime(t, n, paseoHello())
	_ = rt.peer.Notify("runtime.workspaces", map[string]any{"workspaces": []any{map[string]any{"workspace_id": "wks_1", "cwd": "/w/app"}}})
	waitFor(t, "connected", func() bool { h, _, _ := n.runtimeEngine(""); return h != nil })
	// Either no catalog at all (no terminal host either) or one without the runtime's workspaces.
	raw, err := nodeCall(t, n, "workspaces.list", map[string]any{})
	var res proto.WorkspacesResponse
	_ = json.Unmarshal(raw, &res)
	if err == nil && len(res.Workspaces) != 0 || err != nil && !strings.Contains(err.Error(), "no workspace host") {
		t.Fatalf("a runtime without caps.workspaces: %s %v", raw, err)
	}
}

// An engine runtime's session names the workspace it is in, which ties it to the catalog's row; another runtime's, or
// one that keeps no workspaces, does not. A move to another workspace changes the list.
func TestRuntimeSessionsKeepTheirWorkspace(t *testing.T) {
	n, _ := runtimeNode(t)
	enablePaseo(n)
	pa := dialRuntime(t, n, paseoWorkspacesHello())
	_ = pa.peer.Notify("runtime.sessions", map[string]any{"sessions": []any{
		map[string]any{"session_id": "paseo-w1", "status": "idle", "workspace_id": "wks_1", "space_id": "prj_1"},
		map[string]any{"session_id": "paseo-w2", "status": "idle", "workspace_id": "../bad"},
	}})
	dsh := dialRuntime(t, n, dshHello())
	_ = dsh.peer.Notify("runtime.sessions", map[string]any{"sessions": []any{map[string]any{"session_id": "session-d1", "status": "idle", "workspace_id": "w9"}}})
	waitFor(t, "listed", func() bool {
		l := listed(t, n)
		return l["paseo-w1"].SessionID != "" && l["session-d1"].SessionID != ""
	})
	l := listed(t, n)
	if l["paseo-w1"].WorkspaceID != "wks_1" || l["paseo-w2"].WorkspaceID != "" || l["session-d1"].WorkspaceID != "" {
		t.Fatalf("workspace ids: %q %q %q", l["paseo-w1"].WorkspaceID, l["paseo-w2"].WorkspaceID, l["session-d1"].WorkspaceID)
	}
	row := proto.Session{SessionID: "paseo-w1", SourceKind: proto.SourceRuntime, WorkspaceID: "wks_1"}
	moved := row
	moved.WorkspaceID = "wks_2"
	if listKey([]proto.Session{row}) == listKey([]proto.Session{moved}) {
		t.Fatal("a session moving to another workspace is no change of the list")
	}
}

// A runtime engine that cannot make worktrees says so: one that keeps no workspaces (Cindy, an older bridge) with the
// code the hub reads as "run the task in its folder" (#4946); one away, as away. A missing terminal host is neither.
func TestWorktreePrepareOnARuntimeEngineIsUnsupported(t *testing.T) {
	n, _ := runtimeNode(t)
	if err := n.useEngine(context.Background(), proto.EnginePaseo); err != nil {
		t.Fatal(err)
	}
	_, err := nodeCall(t, n, "worktree.prepare", map[string]any{"cwd": "/w/app", "locale": "en"})
	if err == nil || rpcCode(err) == proto.CodeWorktreeUnsupported || !strings.Contains(err.Error(), "Paseo is not connected") {
		t.Fatalf("prepare with Paseo away = %v (code %q)", err, rpcCode(err))
	}
	dialRuntime(t, n, paseoHello()) // no caps.workspaces
	waitFor(t, "connected", func() bool { h, _, _ := n.runtimeEngine(""); return h != nil })
	_, err = nodeCall(t, n, "worktree.prepare", map[string]any{"cwd": "/w/app"})
	if rpcCode(err) != proto.CodeWorktreeUnsupported || !errors.Is(err, host.ErrUnsupported) || err.Error() != "unsupported: paseo cannot do this" {
		t.Fatalf("prepare on a runtime keeping no workspaces = %v (code %q)", err, rpcCode(err))
	}
	if _, err := nodeCall(t, n, "worktree.prepare", map[string]any{"cwd": "/w/app", "kind": "orca"}); err == nil || rpcCode(err) != "" {
		t.Fatalf("a missing host is not a runtime without checkouts: %v (code %q)", err, rpcCode(err))
	}
}

// "New worktree…" on a runtime engine: the dialog starts from the runtime's folder; the worktree is the runtime's, made
// as Persona promises one (a new branch from the source checkout's HEAD, an existing one as it is), listed at once.
func TestRuntimeWorktreePrepareAndCreate(t *testing.T) {
	r := runtimeRepoNode(t)
	prep, err := request(t, r.n, "worktree.prepare", map[string]any{"workspace_id": "wks_main", "kind": "paseo"})
	if err != nil || prep["path"] != r.trees || prep["root"] != r.repo || prep["branch"] != "main" || prep["repoName"] != "proj" ||
		prep["automatic_name"] != true || prep["choose_parent"] == true {
		t.Fatalf("prepare: %v %v", prep, err)
	}
	var asked map[string]any
	_ = json.Unmarshal(r.rt.got("workspace.prepare")[0], &asked)
	if asked["cwd"] != r.repo || asked["repo_root"] != r.repo {
		t.Fatalf("the runtime was asked about %v", asked)
	}
	if prep, err := request(t, r.n, "worktree.prepare", map[string]any{"cwd": filepath.Join(r.repo, ".")}); err != nil || prep["path"] != r.trees {
		t.Fatalf("prepare by folder on the default engine: %v %v", prep, err)
	}

	res, err := request(t, r.n, "worktree.create", map[string]any{"workspace_id": "wks_main", "kind": "paseo", "branch": "feat/x",
		"path": filepath.Join(r.trees, "feat-x")})
	if err != nil || res["workspace_id"] != "wks_new1" || res["path"] != filepath.Join(r.trees, "feat-x") || res["branch"] != "feat/x" {
		t.Fatalf("create: %v %v", res, err)
	}
	var made map[string]any
	_ = json.Unmarshal(r.rt.got("workspace.create")[0], &made)
	if made["cwd"] != r.repo || made["workspace_id"] != "wks_main" || made["branch"] != "feat/x" || made["create_branch"] != true ||
		made["base"] != "main" || made["name"] != "feat-x" || !strings.HasPrefix(str(made["key"]), "persona-") {
		t.Fatalf("workspace.create %v", made)
	}
	if head := runPullGit(t, filepath.Join(r.trees, "feat-x"), "rev-parse", "HEAD"); head != runPullGit(t, r.repo, "rev-parse", "HEAD") {
		t.Fatal("the new branch does not start at the source HEAD")
	}
	if w, ok := catalog(t, r.n)["wks_new1"]; !ok || w.Branch != "feat/x" || w.IsMain == nil || *w.IsMain {
		t.Fatalf("the new worktree is listed at once: %+v", w)
	}

	// An existing branch is checked out as it is.
	if _, err := request(t, r.n, "worktree.create", map[string]any{"workspace_id": "wks_main", "kind": "paseo", "branch": "spare",
		"path": filepath.Join(r.trees, "spare")}); err != nil {
		t.Fatal(err)
	}
	made = map[string]any{}
	_ = json.Unmarshal(r.rt.got("workspace.create")[1], &made)
	if made["create_branch"] != false || made["base"] != nil {
		t.Fatalf("an existing branch: %v", made)
	}

	// Refusals, none of which reaches the runtime.
	calls := len(r.rt.got("workspace.create"))
	for name, p := range map[string]map[string]any{
		"checked out elsewhere": {"workspace_id": "wks_main", "branch": "main", "path": filepath.Join(r.trees, "main"), "locale": "en"},
		"another folder":        {"workspace_id": "wks_main", "branch": "other", "path": filepath.Join(r.repo, "..", "other"), "locale": "en"},
		"a folder in use":       {"workspace_id": "wks_main", "branch": "other", "path": filepath.Join(r.trees, "feat-x")},
		"another parent":        {"workspace_id": "wks_main", "branch": "other", "path": filepath.Join(r.trees, "other"), "parent_workspace_id": "wks_new1", "locale": "en"},
		"an unknown workspace":  {"workspace_id": "wks_gone", "branch": "other", "path": filepath.Join(r.trees, "other"), "locale": "en"},
		"a bad branch":          {"workspace_id": "wks_main", "branch": "-x", "path": filepath.Join(r.trees, "x")},
	} {
		_, err := request(t, r.n, "worktree.create", p)
		if err == nil {
			t.Errorf("%s: created", name)
			continue
		}
		want := map[string]string{"checked out elsewhere": "already checked out", "another folder": "Paseo keeps its worktrees in " + r.trees,
			"another parent": "another parent cannot be chosen", "an unknown workspace": "Paseo no longer lists this workspace"}[name]
		if want != "" && !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if len(r.rt.got("workspace.create")) != calls {
		t.Fatal("a refused worktree reached the runtime")
	}

	// Automatic naming: the runtime names the branch and the folder; the branch still starts at the source HEAD.
	res, err = request(t, r.n, "worktree.create", map[string]any{"workspace_id": "wks_main", "kind": "paseo", "auto_name": true})
	if err != nil || res["workspace_id"] == "" || res["branch"] == "" {
		t.Fatalf("automatic: %v %v", res, err)
	}
	made = map[string]any{}
	_ = json.Unmarshal(r.rt.got("workspace.create")[calls], &made)
	if made["branch"] != nil || made["name"] != nil || made["create_branch"] != true || made["base"] != "main" {
		t.Fatalf("automatic workspace.create %v", made)
	}
}

// A new agent's own worktree (#6115) on a runtime engine is the runtime's, named as on a terminal host; worktree.status
// says where the runtime's go, and does not offer it for removal as a bare checkout (the runtime's workspace is in it).
func TestRuntimeAgentWorktree(t *testing.T) {
	r := runtimeRepoNode(t)
	res, err := request(t, r.n, "worktree.add", map[string]any{"cwd": r.repo, "name_hint": "Fix the login"})
	want := filepath.Join(r.trees, "persona-fix-the-login")
	if err != nil || res["path"] != want || res["branch"] != "persona/fix-the-login" || res["root"] != r.repo || res["workspace_id"] != "wks_new1" {
		t.Fatalf("add: %v %v", res, err)
	}
	var made map[string]any
	_ = json.Unmarshal(r.rt.got("workspace.create")[0], &made)
	if made["create_branch"] != true || made["base"] != "main" || made["name"] != "persona-fix-the-login" || made["workspace_id"] != nil {
		t.Fatalf("workspace.create %v", made)
	}
	// Its name taken: the next one, as on a terminal host.
	if res, err := request(t, r.n, "worktree.add", map[string]any{"cwd": r.repo, "name_hint": "Fix the login"}); err != nil || res["branch"] != "persona/fix-the-login-2" {
		t.Fatalf("again: %v %v", res, err)
	}
	st, err := request(t, r.n, "worktree.status", map[string]any{"cwd": want})
	if err != nil || st["worktree"] != true || st["ours"] == true || st["checkouts"] != r.trees || st["branch"] != "persona/fix-the-login" {
		t.Fatalf("status: %v %v", st, err)
	}
}
