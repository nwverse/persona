package node

// Renaming and deleting an engine runtime's workspaces (#6297; docs/integrations/runtimes.md "Workspaces"; Paseo:
// docs/integrations/paseo.md "Spaces and workspaces"): a workspace's title (worktree.rename), the space its sessions
// are grouped in (space.rename: Paseo's project), and a worktree the runtime made, deleted the way a terminal host's is
// (worktree.delete: a preview the user confirms with its token, then a reconciliation that trusts only the runtime's
// list and the disk), the runtime archiving it (workspace.archive). The runtime removes such a folder whatever is in
// it (Paseo forces it), so what the user confirmed is checked here first: the changes the preview showed, the sessions
// the archive ends, the checkout's identity. Its branch stays unless the user asked for it to go (and git agrees).

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// workspaceArchiveTimeout bounds workspace.archive: Paseo removes the worktree (git, 2 minutes at most) before it
// answers, within the 150 s the hub gives worktree/delete.
var workspaceArchiveTimeout = 140 * time.Second

// workspaceRenameTimeout bounds workspace.rename, within the 20 s the hub gives space.rename and the 30 s of
// worktree/rename.
var workspaceRenameTimeout = 15 * time.Second

// dropWorkspace takes a workspace the runtime archived off its list at once: its next push says the same.
func (h *runtimeConn) dropWorkspace(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.workspaces = slices.DeleteFunc(h.workspaces, func(w runtimeWorkspace) bool { return w.ID == id })
}

// retitleWorkspace gives a workspace the title it was just renamed to, until the runtime's next push says it.
func (h *runtimeConn) retitleWorkspace(id, title string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := range h.workspaces {
		if h.workspaces[i].ID == id {
			h.workspaces[i].Title = title
		}
	}
}

// listsWorkspaces: the runtime has said its list on this connection (runtime.workspaces), so a workspace missing from
// it is gone, not unheard of yet.
func (h *runtimeConn) listsWorkspaces() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.workspacesSaid
}

// workspaceSessions are the ids of h's sessions in workspace id, sorted.
func (h *runtimeConn) workspaceSessions(id string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for _, s := range h.sessions {
		if s.WorkspaceID == id {
			out = append(out, s.SessionID)
		}
	}
	slices.Sort(out)
	return out
}

// runtimeRenameWorkspace is worktree.rename of runtime h's workspace: its title. Its branch, folder and sessions keep
// their names.
func (n *Node) runtimeRenameWorkspace(ctx context.Context, h *runtimeConn, id, name string) error {
	if _, ok := h.workspaceByID(id); !ok {
		return fmt.Errorf("the selected worktree is no longer available")
	}
	cctx, cancel := context.WithTimeout(ctx, workspaceRenameTimeout)
	defer cancel()
	var res struct {
		Workspace *runtimeWorkspace `json:"workspace"`
	}
	if err := h.peer.Call(cctx, "workspace.rename", map[string]any{"workspace_id": id, "title": name}, &res); err != nil {
		return runtimeErr(h, err)
	}
	if res.Workspace != nil {
		if w := cleanWorkspaces([]runtimeWorkspace{*res.Workspace}); len(w) == 1 && w[0].ID == id {
			h.keepWorkspace(w[0])
			return nil
		}
	}
	h.retitleWorkspace(id, name)
	return nil
}

// runtimeSpace is the engine runtime a space id is one of: the space its sessions are grouped in (Paseo's project), or
// one of its workspaces, which names the space it is in (Android names a session's space by its workspace_id). space
// is the space's own id; h is nil when no engine runtime has it.
func (n *Node) runtimeSpace(id string) (h *runtimeConn, space string) {
	if id == "" {
		return nil, ""
	}
	runtimes, _ := n.catalogRuntimes()
	for _, h := range runtimes {
		for _, w := range h.workspacesCopy() {
			if w.SpaceID != "" && (w.SpaceID == id || w.ID == id) {
				return h, w.SpaceID
			}
		}
		h.mu.Lock()
		found := slices.ContainsFunc(h.sessions, func(s proto.Session) bool { return s.SpaceID == id })
		h.mu.Unlock()
		if found {
			return h, id
		}
	}
	return nil, ""
}

// runtimeRenameSpace is space.rename of runtime h's space: the name its sessions show, which the runtime answers
// (what the hub keys pins and to-do links by).
func (n *Node) runtimeRenameSpace(ctx context.Context, h *runtimeConn, space, name string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, workspaceRenameTimeout)
	defer cancel()
	var res struct {
		SpaceName string `json:"space_name"`
	}
	if err := h.peer.Call(cctx, "workspace.rename", map[string]any{"space_id": space, "title": name}, &res); err != nil {
		return "", runtimeErr(h, err)
	}
	return firstOf(strings.TrimSpace(res.SpaceName), name), nil
}

// runtimeWorkspaceDeletion is what a preview of deleting a runtime's worktree binds, besides the checkout's identity and
// its changes (composerDeletion's pathInfo, gitInfo and dirtyState).
type runtimeWorkspaceDeletion struct {
	kind      string // the engine (paseo)
	instance  string // the runtime process that listed it
	workspace runtimeWorkspace
	sessions  string // its sessions, which the archive ends: one started since the preview is another confirmation
	branch    string
	common    string // the repository's git common directory, where a branch the user lets go is deleted
}

// inspectRuntimeDeletion reads what deleting runtime h's workspace id would do: only a worktree the runtime made, as
// its own checkout's top level (never a main checkout, a folder, or a worktree someone else made, which the runtime
// would only archive and leave on the disk), with its changes counted.
func (n *Node) inspectRuntimeDeletion(ctx context.Context, h *runtimeConn, kind, id string) (*composerDeletion, int, error) {
	w, ok := h.workspaceByID(id)
	if !ok {
		return nil, 0, composerError(ctx, "preview_stale")
	}
	engine := i18n.Args{"engine": h.label()}
	switch {
	case w.Kind == workspaceCheckout:
		return nil, 0, composerError(ctx, "main_checkout")
	case w.Kind != workspaceWorktree:
		return nil, 0, composerError(ctx, "runtime_folder_kept", engine)
	case !w.Owned:
		return nil, 0, composerError(ctx, "runtime_not_owned", engine)
	}
	path := w.Cwd
	top, err := git(ctx, path, "rev-parse", "--show-toplevel")
	if err != nil || realPath(strings.TrimSpace(top)) != realPath(path) {
		return nil, 0, composerError(ctx, "main_checkout")
	}
	entry, dirty, err := inspectWorktreeDeletion(ctx, path, false)
	if err != nil {
		return nil, 0, err
	}
	branch, err := git(ctx, path, "branch", "--show-current")
	if err != nil {
		return nil, 0, err
	}
	common, err := absGit(ctx, path, "--git-common-dir")
	if err != nil {
		return nil, 0, err
	}
	h.mu.Lock()
	instance := h.hello.Instance
	h.mu.Unlock()
	entry.node = n
	entry.runtime = &runtimeWorkspaceDeletion{kind: kind, instance: instance, workspace: w,
		sessions: strings.Join(h.workspaceSessions(id), "\n"), branch: strings.TrimSpace(branch), common: common}
	return entry, dirty, nil
}

// sameRuntimeDeletion: nothing a preview showed has changed since: the same runtime process lists the same workspace
// with the same sessions, and the checkout is the same one with the same changes.
func sameRuntimeDeletion(a, b *composerDeletion) bool {
	x, y := a.runtime, b.runtime
	return x != nil && y != nil && x.kind == y.kind && x.instance == y.instance && x.workspace == y.workspace &&
		x.sessions == y.sessions && x.branch == y.branch && x.common == y.common &&
		os.SameFile(a.pathInfo, b.pathInfo) && os.SameFile(a.gitInfo, b.gitInfo) && a.dirtyState == b.dirtyState
}

// runtimeWorktreeDelete is worktree.delete of an engine runtime's worktree, the way a terminal host's is: a preview
// (its token), a confirmation that has to match it (and force for changes it showed), then the runtime archives the
// workspace, which removes the worktree; reconcile only reads. Closing (close_only: archiving a workspace and keeping
// its folder) is left to the runtime's own app.
func (n *Node) runtimeWorktreeDelete(ctx context.Context, kind, id string, in workspaceDeletionInput) (any, error) {
	if in.CloseOnly {
		return nil, composerError(ctx, "close_unsupported")
	}
	var prior *composerDeletion
	if !in.Preview {
		composerDeletions.Lock()
		prior = composerDeletions.previews[in.Token]
		dispatched := prior != nil && prior.dispatched
		composerDeletions.Unlock()
		if prior == nil || prior.node != n || prior.runtime == nil || prior.runtime.kind != kind || prior.runtime.workspace.ID != id ||
			time.Since(prior.created) > 24*time.Hour {
			return nil, composerError(ctx, "preview_required")
		}
		if in.Reconcile { // read only, whether or not the runtime is connected now
			return n.reconcileRuntimeDeletion(prior), nil
		}
		if dispatched {
			return nil, composerError(ctx, "delete_dispatched")
		}
	}
	h, _, err := n.worktreeRuntime(ctx, kind)
	if err != nil {
		return nil, err
	}
	current, dirty, err := n.inspectRuntimeDeletion(ctx, h, kind, id)
	if err != nil {
		return nil, err
	}
	if in.Preview {
		token, err := rememberWorkspaceDeletion(current)
		if err != nil {
			return nil, err
		}
		return proto.WorktreeDeletePreview{WorkspaceID: id, Kind: kind, HostID: "local", InstanceID: current.runtime.instance,
			Path: n.catalogDir(current.runtime.workspace.Cwd), Branch: current.runtime.branch, Dirty: dirty, PreviewToken: token}, nil
	}
	if time.Since(prior.created) > 10*time.Minute || !sameRuntimeDeletion(prior, current) {
		return nil, composerError(ctx, "preview_stale")
	}
	if dirty > 0 && !in.Force {
		return nil, composerError(ctx, "dirty")
	}
	composerDeletions.Lock()
	if prior.dispatched {
		composerDeletions.Unlock()
		return nil, composerError(ctx, "delete_dispatched")
	}
	prior.dispatched = true
	composerDeletions.Unlock()

	cctx, cancel := context.WithTimeout(ctx, workspaceArchiveTimeout)
	defer cancel()
	err = h.peer.Call(cctx, "workspace.archive", map[string]any{"workspace_id": id}, nil)
	if err != nil {
		if result := n.reconcileRuntimeDeletion(prior); result.Removed {
			return result, nil
		}
		if !isRPCError(err) || cctx.Err() != nil {
			// No answer: whether the runtime archived anything is not known; reconcile tells.
			return proto.WorktreeDeleteResult{WorkspaceID: id, State: "unknown"}, nil
		}
		return nil, runtimeErr(h, err)
	}
	h.dropWorkspace(id)
	result := n.reconcileRuntimeDeletion(prior)
	if result.Removed && in.DeleteBranch && prior.runtime.branch != "" {
		_, err = git(ctx, prior.runtime.common, "branch", "-d", prior.runtime.branch)
		result.BranchDeleted = err == nil
	}
	return result, nil
}

// reconcileRuntimeDeletion says what became of a runtime worktree a confirmed deletion was sent for: removed once the
// runtime connected now no longer lists it (having said its list) and its folder is gone; present while it lists it;
// unknown otherwise (the runtime away, or archived with its folder left on the disk).
func (n *Node) reconcileRuntimeDeletion(prior *composerDeletion) proto.WorktreeDeleteResult {
	target := prior.runtime
	result := proto.WorktreeDeleteResult{WorkspaceID: target.workspace.ID, State: "unknown"}
	h, _, ok := n.runtimeEngine(target.kind)
	if !ok || h == nil || !h.makesWorkspaces() || !h.listsWorkspaces() {
		return result
	}
	if _, listed := h.workspaceByID(target.workspace.ID); listed {
		result.State = "present"
		return result
	}
	if _, err := os.Lstat(filepath.Clean(target.workspace.Cwd)); !os.IsNotExist(err) {
		return result
	}
	result.Removed, result.State = true, "removed"
	return result
}
