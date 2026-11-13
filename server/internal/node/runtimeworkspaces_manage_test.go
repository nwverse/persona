package node

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// manageAnswers has the fake runtime rename and archive its workspaces as Paseo does: a title, a project's name, and an
// archive that removes a worktree it made from the disk (forced, as Paseo's is), its branch kept. archiveErr, when
// set, is its refusal instead.
func (r *runtimeRepo) manageAnswers(t *testing.T, archiveErr error) {
	t.Helper()
	r.rt.mu.Lock()
	defer r.rt.mu.Unlock()
	prev := r.rt.answer
	r.rt.answer = func(method string, params json.RawMessage) (any, error) {
		var p map[string]any
		_ = json.Unmarshal(params, &p)
		switch method {
		case "workspace.rename":
			if id := str(p["workspace_id"]); id != "" {
				return map[string]any{"workspace": map[string]any{"workspace_id": id, "title": p["title"], "cwd": r.repo, "kind": "checkout",
					"branch": "main", "space_id": "prj_1", "space_name": "proj", "repo_root": r.repo}}, nil
			}
			return map[string]any{"space_name": "space " + str(p["title"])}, nil
		case "workspace.archive":
			if archiveErr != nil {
				return nil, archiveErr
			}
			for _, w := range r.n.runtimeWorkspaces() {
				if w.ID == str(p["workspace_id"]) && w.Owned {
					if _, err := git(context.Background(), r.repo, "worktree", "remove", "--force", w.Cwd); err != nil {
						return nil, err
					}
				}
			}
			return map[string]any{"archived_at": "2026-11-12T00:00:00.000Z"}, nil
		}
		return prev(method, params)
	}
}

// runtimeWorkspaces is every workspace of the machine's engine runtimes (tests).
func (n *Node) runtimeWorkspaces() []runtimeWorkspace {
	runtimes, _ := n.catalogRuntimes()
	var out []runtimeWorkspace
	for _, h := range runtimes {
		out = append(out, h.workspacesCopy()...)
	}
	return out
}

// A Paseo workspace is renamed in Paseo: worktree.rename sets its title, space.rename the name of the space it is in
// (its project, by the project's id or by one of its workspaces', as Android names it), which the node answers; an
// id no engine has never reaches the runtime.
func TestRuntimeWorkspaceRename(t *testing.T) {
	r := runtimeRepoNode(t)
	r.manageAnswers(t, nil)
	if _, err := request(t, r.n, "worktree.rename", map[string]any{"workspace_id": "wks_main", "kind": "paseo", "name": "  Main  work "}); err != nil {
		t.Fatal(err)
	}
	var asked map[string]any
	_ = json.Unmarshal(r.rt.got("workspace.rename")[0], &asked)
	if asked["workspace_id"] != "wks_main" || asked["title"] != "Main work" || asked["space_id"] != nil {
		t.Fatalf("workspace.rename %v", asked)
	}
	if w := catalog(t, r.n)["wks_main"]; w.Title != "Main work" {
		t.Fatalf("the catalog says the new title at once: %+v", w)
	}
	for _, id := range []string{"prj_1", "wks_main"} {
		res, err := request(t, r.n, "space.rename", map[string]any{"workspace_id": id, "name": "App"})
		if err != nil || res["name"] != "space App" {
			t.Fatalf("space.rename %s: %v %v", id, res, err)
		}
		asked = map[string]any{}
		_ = json.Unmarshal(r.rt.got("workspace.rename")[len(r.rt.got("workspace.rename"))-1], &asked)
		if asked["space_id"] != "prj_1" || asked["title"] != "App" || asked["workspace_id"] != nil {
			t.Fatalf("space.rename %s asked %v", id, asked)
		}
	}
	calls := len(r.rt.got("workspace.rename"))
	if _, err := request(t, r.n, "worktree.rename", map[string]any{"workspace_id": "wks_gone", "kind": "paseo", "name": "x"}); err == nil {
		t.Fatal("renamed a workspace Paseo does not list")
	}
	if _, err := request(t, r.n, "space.rename", map[string]any{"workspace_id": "w1", "name": "x"}); err == nil {
		t.Fatal("renamed a space no engine lists")
	}
	if len(r.rt.got("workspace.rename")) != calls {
		t.Fatal("an unknown id reached the runtime")
	}
}

// With herdr enabled beside Paseo as the default engine, a herdr space is renamed in herdr: the one host that renames,
// though it is not the default engine's, is asked once it lists the space (it used to look for a "paseo" host and fail).
func TestRenameSpaceInHerdrBesidePaseo(t *testing.T) {
	n, _ := testNode(t)
	hh := &renameHost{bareHost: bareHost{kind: host.Herdr}, spaces: []string{"w1"}}
	n.hosts = host.NewRegistry(hh)
	c := proto.EngineChoice{Hosts: []string{"herdr", "paseo"}, Default: "paseo"}
	n.eng.name, n.eng.set = proto.LegacyEngine(c), &c
	res, err := request(t, n, "space.rename", map[string]any{"workspace_id": "w1", "name": "work"})
	if err != nil || res["name"] != "work" || !slices.Equal(hh.renamed, []string{"w1=work"}) {
		t.Fatalf("renamed %v %v, herdr %q", res, err, hh.renamed)
	}
	if _, err := request(t, n, "space.rename", map[string]any{"workspace_id": "w9", "name": "work"}); err == nil ||
		strings.Contains(err.Error(), "no paseo host") || len(hh.renamed) != 1 {
		t.Fatalf("a space herdr does not list: %v, herdr %q", err, hh.renamed)
	}
}

// Deleting a worktree Paseo made goes as a terminal host's does: a preview of what goes (its changes, its branch), a
// confirmation bound to it, then Paseo archives the workspace, which removes the folder; the branch stays unless the
// user lets it go. A main checkout, a folder, a worktree Paseo did not make, and closing are refused.
func TestRuntimeWorktreeDelete(t *testing.T) {
	r := runtimeRepoNode(t)
	r.manageAnswers(t, nil)
	tree := filepath.Join(r.trees, "feat-x")
	if _, err := request(t, r.n, "worktree.create", map[string]any{"workspace_id": "wks_main", "kind": "paseo", "branch": "feat/x", "path": tree}); err != nil {
		t.Fatal(err)
	}
	other := resolved(t, t.TempDir())
	_ = r.rt.peer.Notify("runtime.workspaces", map[string]any{"workspaces": []any{r.mainWorkspace,
		map[string]any{"workspace_id": "wks_new1", "title": "feat/x", "cwd": tree, "kind": "worktree", "branch": "feat/x", "space_id": "prj_1", "repo_root": r.repo, "owned": true},
		map[string]any{"workspace_id": "wks_notes", "cwd": other, "kind": "folder", "space_id": "prj_2"},
		map[string]any{"workspace_id": "wks_theirs", "cwd": other, "kind": "worktree", "branch": "x", "space_id": "prj_1"},
	}})
	waitFor(t, "the list", func() bool { return r.n.isRuntimeWorkspace("wks_theirs") })
	del := func(p map[string]any) (map[string]any, error) {
		p["kind"], p["locale"] = "paseo", "en"
		return request(t, r.n, "worktree.delete", p)
	}
	for id, want := range map[string]string{"wks_main": "main checkout", "wks_notes": "is a folder, not a worktree",
		"wks_theirs": "Paseo did not make this worktree", "wks_gone": "differ from the preview"} {
		if _, err := del(map[string]any{"workspace_id": id, "preview": true}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("preview of %s: %v", id, err)
		}
	}
	if _, err := del(map[string]any{"workspace_id": "wks_new1", "preview": true, "close_only": true}); err == nil {
		t.Error("closing a Paseo workspace from Persona")
	}

	// Its changes: shown, then only with force; a change since the preview needs another.
	if err := os.WriteFile(filepath.Join(tree, "new.txt"), []byte("draft\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	prev, err := del(map[string]any{"workspace_id": "wks_new1", "preview": true})
	if err != nil || prev["path"] != tree || prev["branch"] != "feat/x" || prev["dirty"] != float64(1) || prev["folder"] != false ||
		prev["host_id"] != "local" || prev["kind"] != "paseo" || str(prev["preview_token"]) == "" {
		t.Fatalf("preview: %v %v", prev, err)
	}
	token := prev["preview_token"]
	if _, err := del(map[string]any{"workspace_id": "wks_new1", "preview_token": token}); err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
		t.Fatalf("changes without force: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tree, "new.txt"), []byte("draft 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := del(map[string]any{"workspace_id": "wks_new1", "preview_token": token, "force": true}); err == nil || !strings.Contains(err.Error(), "differ from the preview") {
		t.Fatalf("changed since the preview: %v", err)
	}
	// A session started in it since the preview is one more the archive would end.
	prev, _ = del(map[string]any{"workspace_id": "wks_new1", "preview": true})
	_ = r.rt.peer.Notify("runtime.sessions", map[string]any{"sessions": []any{map[string]any{"session_id": "paseo-in-tree", "status": "idle", "workspace_id": "wks_new1"}}})
	waitFor(t, "the session", func() bool { _, ok := listed(t, r.n)["paseo-in-tree"]; return ok })
	if _, err := del(map[string]any{"workspace_id": "wks_new1", "preview_token": prev["preview_token"], "force": true}); err == nil {
		t.Fatal("deleted a worktree with a session the preview did not show")
	}
	if len(r.rt.got("workspace.archive")) != 0 {
		t.Fatal("a refused deletion reached the runtime")
	}

	prev, _ = del(map[string]any{"workspace_id": "wks_new1", "preview": true})
	res, err := del(map[string]any{"workspace_id": "wks_new1", "preview_token": prev["preview_token"], "force": true})
	if err != nil || res["removed"] != true || res["state"] != "removed" {
		t.Fatalf("delete: %v %v", res, err)
	}
	var asked map[string]any
	_ = json.Unmarshal(r.rt.got("workspace.archive")[0], &asked)
	if asked["workspace_id"] != "wks_new1" || len(asked) != 1 {
		t.Fatalf("workspace.archive %v", asked)
	}
	if _, err := os.Lstat(tree); !os.IsNotExist(err) {
		t.Fatal("the folder is still there")
	}
	if runPullGit(t, r.repo, "branch", "--list", "feat/x") == "" {
		t.Fatal("the branch went too")
	}
	if _, ok := catalog(t, r.n)["wks_new1"]; ok {
		t.Fatal("still in the catalog")
	}
	if res, err := del(map[string]any{"workspace_id": "wks_new1", "preview_token": prev["preview_token"], "reconcile": true}); err != nil || res["state"] != "removed" {
		t.Fatalf("reconcile: %v %v", res, err)
	}
	if _, err := del(map[string]any{"workspace_id": "wks_new1", "preview_token": prev["preview_token"], "force": true}); err == nil {
		t.Fatal("a token was used twice")
	}

	// The branch too, when the user lets it go (git's safe delete: it is merged).
	tree2 := filepath.Join(r.trees, "feat-y")
	made, err := request(t, r.n, "worktree.create", map[string]any{"workspace_id": "wks_main", "kind": "paseo", "branch": "feat/y", "path": tree2})
	if err != nil {
		t.Fatal(err)
	}
	prev, _ = del(map[string]any{"workspace_id": made["workspace_id"], "preview": true})
	res, err = del(map[string]any{"workspace_id": made["workspace_id"], "preview_token": prev["preview_token"], "delete_branch": true})
	if err != nil || res["removed"] != true || res["branch_deleted"] != true || runPullGit(t, r.repo, "branch", "--list", "feat/y") != "" {
		t.Fatalf("delete with its branch: %v %v", res, err)
	}
}

// Paseo refusing the archive is an error, and the worktree is still there: present, not removed.
func TestRuntimeWorktreeDeleteRefused(t *testing.T) {
	r := runtimeRepoNode(t)
	r.manageAnswers(t, &rpc.Error{Code: "archive_failed", Message: "Paseo did not archive the workspace: busy"})
	tree := filepath.Join(r.trees, "feat-z")
	made, err := request(t, r.n, "worktree.create", map[string]any{"workspace_id": "wks_main", "kind": "paseo", "branch": "feat/z", "path": tree})
	if err != nil {
		t.Fatal(err)
	}
	prev, err := request(t, r.n, "worktree.delete", map[string]any{"workspace_id": made["workspace_id"], "kind": "paseo", "preview": true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = request(t, r.n, "worktree.delete", map[string]any{"workspace_id": made["workspace_id"], "kind": "paseo", "preview_token": prev["preview_token"]})
	var refused *rpc.Error
	if !errors.As(err, &refused) || !strings.Contains(err.Error(), "busy") {
		t.Fatalf("refused: %v", err)
	}
	res, err := request(t, r.n, "worktree.delete", map[string]any{"workspace_id": made["workspace_id"], "kind": "paseo", "preview_token": prev["preview_token"], "reconcile": true})
	if err != nil || res["state"] != "present" || res["removed"] == true {
		t.Fatalf("reconcile: %v %v", res, err)
	}
	if _, err := os.Stat(tree); err != nil {
		t.Fatal(err)
	}
}
