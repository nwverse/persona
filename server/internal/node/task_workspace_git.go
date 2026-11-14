package node

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/nwverse/persona/server/internal/assistant/repository"
	"github.com/nwverse/persona/server/internal/taskworkspace"
)

func (e *workspaceExecutor) source(ctx context.Context, r *taskworkspace.Instance) string {
	if r.ResolvedCommit != "" {
		if !e.owned(r.CommonDir) {
			return "identity_mismatch"
		}
		if _, err := workspaceReadGit(ctx, r.CommonDir, "cat-file", "-e", r.ResolvedCommit+"^{commit}"); err != nil {
			return "source_missing"
		}
		return ""
	}
	if !e.n.workspaceSourceAllowed(r.Input) {
		return "source_invalid"
	}
	transport := r.Input.SourceSnapshot.CloneURL
	ref, err := repository.NormalizeRef(r.Input.RequestedRef)
	if err != nil {
		return "ref_invalid"
	}
	listing, err := e.git(ctx, e.journal.Plan.RootPath, "ls-remote", "--symref", "--", transport, "HEAD")
	if err != nil {
		return "source_unavailable"
	}
	if listing == "" {
		if ref.Kind == "default" {
			heads, err := e.git(ctx, e.journal.Plan.RootPath, "ls-remote", "--heads", "--", transport)
			if err != nil {
				return "source_unavailable"
			}
			if heads == "" {
				return "no_commits"
			}
			return "default_branch_unresolved"
		}
		requested := ref.Value
		if ref.Kind == "branch" {
			requested = "refs/heads/" + strings.TrimPrefix(requested, "refs/heads/")
		}
		if ref.Kind == "tag" {
			requested = "refs/tags/" + strings.TrimPrefix(requested, "refs/tags/")
		}
		if ref.Kind == "commit" {
			listing = ref.Value + "\t" + ref.Value
		} else {
			listing, err = e.git(ctx, e.journal.Plan.RootPath, "ls-remote", "--", transport, requested)
			if err != nil {
				return "source_unavailable"
			}
			if listing == "" {
				return "ref_not_found"
			}
		}
	}
	objectFormat := "sha1"
	for _, line := range strings.Split(listing, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && len(fields[0]) == 64 {
			objectFormat = "sha256"
		}
	}
	if !e.owned(r.CommonDir) {
		if err = e.claimDir(r.CommonDir); err != nil {
			return "path_conflict"
		}
	}
	for _, path := range []string{filepath.Join(r.CommonDir, "config"), filepath.Join(r.CommonDir, "objects"), filepath.Join(r.CommonDir, "refs")} {
		if workspaceNoLinks(path) != nil {
			return "git_layout_mismatch"
		}
	}
	if _, err = e.git(ctx, r.CommonDir, "init", "--bare", "--object-format="+objectFormat, "."); err != nil {
		return "source_failed"
	}
	if _, err = e.git(ctx, r.CommonDir, "config", "remote.origin.url", transport); err != nil {
		return "source_failed"
	}
	if _, err = e.git(ctx, r.CommonDir, "fetch", "--no-recurse-submodules", "--tags", "origin", "+refs/heads/*:refs/remotes/origin/*"); err != nil {
		return "source_fetch_failed"
	}
	var resolve string
	switch ref.Kind {
	case "default":
		for _, line := range strings.Split(listing, "\n") {
			fields := strings.Fields(line)
			if len(fields) == 3 && fields[0] == "ref:" && fields[2] == "HEAD" && strings.HasPrefix(fields[1], "refs/heads/") {
				r.ResolvedRef = fields[1]
				resolve = "refs/remotes/origin/" + strings.TrimPrefix(fields[1], "refs/heads/")
				break
			}
		}
		if resolve == "" {
			return "default_branch_unresolved"
		}
	case "branch":
		r.ResolvedRef = "refs/heads/" + strings.TrimPrefix(ref.Value, "refs/heads/")
		resolve = "refs/remotes/origin/" + strings.TrimPrefix(r.ResolvedRef, "refs/heads/")
	case "tag":
		r.ResolvedRef = "refs/tags/" + strings.TrimPrefix(ref.Value, "refs/tags/")
		resolve = r.ResolvedRef
	case "commit", "ref":
		r.ResolvedRef = ref.Value
		if _, err = e.git(ctx, r.CommonDir, "fetch", "--no-recurse-submodules", "origin", ref.Value); err != nil {
			return "ref_not_found"
		}
		resolve = "FETCH_HEAD"
	}
	commit, err := workspaceReadGit(ctx, r.CommonDir, "rev-parse", "--verify", resolve+"^{commit}")
	if err != nil {
		return "ref_not_found"
	}
	normalized, err := repository.NormalizeRef(repository.RequestedRef{Kind: "commit", Value: commit})
	if err != nil {
		return "source_failed"
	}
	// Pin the actual object, independently of later remote movement and GC.
	if _, err = e.git(ctx, r.CommonDir, "update-ref", "refs/persona/base", normalized.Value); err != nil {
		return "source_failed"
	}
	r.ResolvedCommit = normalized.Value
	r.State = "source_ready"
	if err = e.save(); err != nil {
		return "persistence_failed"
	}
	return ""
}
func (e *workspaceExecutor) materialize(ctx context.Context, r *taskworkspace.Instance) string {
	if !e.owned(e.journal.Plan.RootPath) || !e.owned(r.CommonDir) {
		return "identity_mismatch"
	}
	// A completed command whose receipt was lost is adopted only through its
	// exact private Git registration, not because a directory happens to exist.
	if r.State == "materializing" {
		if e.checkRegistration(ctx, r) == "" {
			id, err := workspaceFileID(r.Path)
			if err != nil {
				return "identity_mismatch"
			}
			e.journal.Identities[r.Path] = id
			r.State = "ready"
			return e.inspect(ctx, r)
		}
	}
	if r.Path != e.journal.Plan.RootPath {
		if _, err := os.Lstat(r.Path); !errors.Is(err, os.ErrNotExist) {
			return "path_conflict"
		}
	}
	args := []string{"worktree", "add", "-b", r.TaskBranch, "--", r.Path, r.ResolvedCommit}
	if tip, err := workspaceReadGit(ctx, r.CommonDir, "rev-parse", "--verify", "refs/heads/"+r.TaskBranch); err == nil {
		if r.State != "materializing" || tip != r.ResolvedCommit {
			return "branch_conflict"
		}
		listing, err := workspaceReadGit(ctx, r.CommonDir, "worktree", "list", "--porcelain", "-z")
		if err != nil || strings.Contains("\x00"+listing+"\x00", "\x00branch refs/heads/"+r.TaskBranch+"\x00") {
			return "branch_conflict"
		}
		// Git may create the branch before checkout fails. Resume that exact
		// unmodified branch only after proving no worktree has it checked out.
		args = []string{"worktree", "add", "--", r.Path, r.TaskBranch}
	}
	r.State = "materializing"
	if err := e.save(); err != nil {
		return "persistence_failed"
	}
	if _, err := e.git(ctx, r.CommonDir, args...); err != nil {
		return "worktree_failed"
	}
	id, err := workspaceFileID(r.Path)
	if err != nil {
		return "identity_mismatch"
	}
	e.journal.Identities[r.Path] = id
	if code := e.checkRegistration(ctx, r); code != "" {
		return code
	}
	head, err := workspaceReadGit(ctx, r.Path, "rev-parse", "HEAD")
	if err != nil || head != r.ResolvedCommit {
		return "git_layout_mismatch"
	}
	r.State = "ready"
	if err = e.save(); err != nil {
		return "persistence_failed"
	}
	return e.inspect(ctx, r)
}
func (e *workspaceExecutor) checkRegistration(ctx context.Context, r *taskworkspace.Instance) string {
	if workspaceNoLinks(r.Path) != nil || workspaceNoLinks(r.CommonDir) != nil || !e.owned(r.CommonDir) {
		return "identity_mismatch"
	}
	for _, path := range []string{filepath.Join(r.Path, ".git"), filepath.Join(r.CommonDir, "config"), filepath.Join(r.CommonDir, "objects"), filepath.Join(r.CommonDir, "refs"), filepath.Join(r.CommonDir, "worktrees")} {
		if workspaceNoLinks(path) != nil {
			return "git_layout_mismatch"
		}
	}
	common, err := workspaceReadGit(ctx, r.Path, "rev-parse", "--git-common-dir")
	if err != nil {
		return "workspace_drift"
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(r.Path, common)
	}
	if filepath.Clean(common) != r.CommonDir {
		return "git_layout_mismatch"
	}
	dir, err := workspaceReadGit(ctx, r.Path, "rev-parse", "--git-dir")
	if err != nil {
		return "git_layout_mismatch"
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(r.Path, dir)
	}
	rel, err := filepath.Rel(filepath.Join(r.CommonDir, "worktrees"), dir)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) || workspaceNoLinks(filepath.Clean(dir)) != nil {
		return "git_layout_mismatch"
	}
	top, err := workspaceReadGit(ctx, r.Path, "rev-parse", "--show-toplevel")
	if err != nil || filepath.Clean(top) != r.Path {
		return "git_layout_mismatch"
	}
	registered, err := workspaceReadGit(ctx, r.CommonDir, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return "git_layout_mismatch"
	}
	found := false
	for _, field := range strings.Split(registered, "\x00") {
		if strings.TrimSpace(field) == "worktree "+r.Path {
			found = true
		}
	}
	if !found {
		return "git_layout_mismatch"
	}
	for _, path := range []string{filepath.Join(r.CommonDir, "objects", "info", "alternates"), filepath.Join(r.CommonDir, "shallow")} {
		data, err := os.ReadFile(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) || len(data) > 0 {
			return "git_layout_mismatch"
		}
	}
	return ""
}
func (e *workspaceExecutor) inspect(ctx context.Context, r *taskworkspace.Instance) string {
	if !e.owned(e.journal.Plan.RootPath) || !e.owned(r.Path) {
		return "identity_mismatch"
	}
	if code := e.checkRegistration(ctx, r); code != "" {
		return code
	}
	branch, err := workspaceReadGit(ctx, r.Path, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil || branch != r.TaskBranch {
		return "workspace_drift"
	}
	head, err := workspaceReadGit(ctx, r.Path, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return "workspace_drift"
	}
	status, err := workspaceReadGit(ctx, r.Path, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return "inspection_failed"
	}
	r.CurrentHead = head
	r.Dirty = status != ""
	r.Error = nil
	return ""
}
func (e *workspaceExecutor) cleanup(ctx context.Context, req taskworkspace.Request) taskworkspace.Record {
	j := &e.journal
	if j.State == "removed" {
		return j.Record
	}
	if len(j.Uses) > 0 || e.n.agentRunsIn(j.Plan.RootPath) {
		return e.fail("worktree_in_use", "cleanup", "", j.Plan.RootPath)
	}
	if !e.owned(j.Plan.RootPath) {
		if _, err := os.Lstat(j.Plan.RootPath); !errors.Is(err, os.ErrNotExist) || j.Cleanup == nil {
			return e.fail("identity_mismatch", "cleanup", "", j.Plan.RootPath)
		}
	}
	if j.Cleanup != nil {
		for i := range j.Repositories {
			r := &j.Repositories[i]
			if !r.Removed && r.State == "ready" {
				if _, err := os.Lstat(r.Path); errors.Is(err, os.ErrNotExist) && e.owned(r.CommonDir) {
					listing, err := workspaceReadGit(ctx, r.CommonDir, "worktree", "list", "--porcelain", "-z")
					if err == nil && !strings.Contains("\x00"+listing+"\x00", "\x00worktree "+r.Path+"\x00") {
						r.Removed = true
					}
				}
			}
		}
	}
	for i := range j.Repositories {
		r := &j.Repositories[i]
		if r.State == "materializing" && e.checkRegistration(ctx, r) == "" {
			id, err := workspaceFileID(r.Path)
			if err != nil {
				return e.fail("identity_mismatch", "cleanup", r.RepoID, r.Path)
			}
			j.Identities[r.Path] = id
			r.State = "ready"
		}
	}
	// Inspect all selected instances before removing any. Additional worktrees
	// remain registered and are never recursively removed with their object store.
	for i := range j.Repositories {
		r := &j.Repositories[i]
		if r.Removed || r.State != "ready" {
			continue
		}
		if code := e.inspect(ctx, r); code != "" {
			return e.fail(code, "cleanup", r.RepoID, r.Path)
		}
		if r.Dirty && !req.Force {
			return e.fail("worktree_dirty", "cleanup", r.RepoID, r.Path)
		}
		if req.DeleteBranch && !req.Force {
			if _, err := workspaceReadGit(ctx, r.CommonDir, "merge-base", "--is-ancestor", "refs/heads/"+r.TaskBranch, "refs/persona/base"); err != nil {
				return e.fail("worktree_unmerged", "cleanup", r.RepoID, r.Path)
			}
		}
	}
	j.Cleanup = &taskworkspace.CleanupResult{RetainedPaths: []string{}}
	for i := range j.Repositories {
		r := &j.Repositories[i]
		if !e.owned(r.CommonDir) {
			if j.Identities[r.CommonDir] != "" {
				return e.fail("identity_mismatch", "cleanup", r.RepoID, r.CommonDir)
			}
			continue
		}
		if !r.Removed && r.State == "ready" {
			args := []string{"worktree", "remove"}
			if req.Force {
				args = append(args, "--force")
			}
			if _, err := e.git(ctx, r.CommonDir, append(args, "--", r.Path)...); err != nil {
				return e.fail("cleanup_failed", "cleanup", r.RepoID, r.Path)
			}
			if _, err := os.Lstat(r.Path); !errors.Is(err, os.ErrNotExist) {
				return e.fail("cleanup_failed", "cleanup", r.RepoID, r.Path)
			}
			r.Removed = true
			if err := e.save(); err != nil {
				return e.fail("persistence_failed", "cleanup", r.RepoID, r.Path)
			}
		}
		if req.DeleteBranch && r.Removed && !r.BranchDeleted {
			if _, exists := workspaceReadGit(ctx, r.CommonDir, "show-ref", "--verify", "refs/heads/"+r.TaskBranch); exists == nil {
				// The non-force preflight checks ancestry against the frozen source,
				// equivalent to the old main-checkout check. A bare HEAD has no
				// checked-out main branch, so the checked deletion uses -D here.
				if _, err := e.git(ctx, r.CommonDir, "branch", "-D", "--", r.TaskBranch); err != nil {
					return e.fail("cleanup_failed", "branch", r.RepoID, r.Path)
				}
			}
			r.BranchDeleted = true
		}
		j.Cleanup.RetainedPaths = append(j.Cleanup.RetainedPaths, r.CommonDir)
	}
	// Only remove an empty container. Unknown files survive even with force,
	// which has the same worktree/branch scope as the existing product command.
	if _, err := os.Lstat(j.Plan.RootPath); err == nil {
		if err = os.Remove(j.Plan.RootPath); err != nil {
			return e.fail("cleanup_retained", "cleanup", "", j.Plan.RootPath)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return e.fail("cleanup_failed", "cleanup", "", j.Plan.RootPath)
	}
	j.State = "removed"
	j.Error = nil
	j.Cleanup.Removed = true
	return j.Record
}

func (n *Node) workspaceSourceAllowed(input repository.PreparationRepository) bool {
	transport := input.SourceSnapshot.CloneURL
	if transport == "" || len(transport) > 4096 || strings.ContainsFunc(transport, unicode.IsControl) {
		return false
	}
	identity, err := repository.Parse(input.SourceSnapshot.Identity.CanonicalURL, input.SourceSnapshot.Identity.Provider)
	if err != nil || identity != input.SourceSnapshot.Identity {
		return false
	}
	local := transport
	if strings.HasPrefix(transport, "file://") {
		u, err := url.Parse(transport)
		if err != nil || u.Host != "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return false
		}
		local = u.Path
	}
	if filepath.IsAbs(local) {
		return n.offLimits(local) == nil && workspaceNoLinks(local) == nil
	}
	_, err = repository.CloneTransport(transport, input.SourceSnapshot.Identity)
	return err == nil
}
