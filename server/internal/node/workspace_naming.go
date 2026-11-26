package node

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/workspacenaming"
)

func init() { nodeRPC("workspace.naming.resolve", (*Node).resolveWorkspaceNaming) }

// workspaceNamingContext resolves symlinks on the node's own home and Persona
// state directory before template expansion so hosts whose home directory lives
// behind a system symlink (such as /home/<user> -> /data/home/<user> or macOS
// /var -> /private/var) yield canonical physical workspace paths while still
// rejecting symlinks at or below the workspace base directory.
func (n *Node) workspaceNamingContext() workspacenaming.NodeContext {
	home := n.home
	if filepath.IsAbs(home) {
		if real, err := n.realPath(filepath.Clean(home)); err == nil || errors.Is(err, fs.ErrNotExist) {
			home = real
		}
	}
	dir := n.dir
	if dir != "" && filepath.IsAbs(dir) {
		if real, err := n.realPath(filepath.Clean(dir)); err == nil || errors.Is(err, fs.ErrNotExist) {
			dir = real
		}
	}
	return workspacenaming.NodeContext{Home: home, PersonaHome: dir}
}

func (n *Node) resolveWorkspacePlan(source workspacenaming.Source) workspacenaming.Preview {
	ctx := n.workspaceNamingContext()
	preview := workspacenaming.Resolve(source, ctx)
	if preview.Plan == nil {
		return preview
	}
	cleanHome := filepath.Clean(n.home)
	if !filepath.IsAbs(cleanHome) || ctx.Home == cleanHome {
		return preview
	}
	base, ok := replacePathPrefix(preview.Plan.BasePath, cleanHome, ctx.Home)
	if !ok {
		return preview
	}
	plan := *preview.Plan
	plan.BasePath = base
	if root, ok := replacePathPrefix(plan.RootPath, cleanHome, ctx.Home); ok {
		plan.RootPath = root
	}
	plan.Repositories = slices.Clone(plan.Repositories)
	for i := range plan.Repositories {
		if abs, ok := replacePathPrefix(plan.Repositories[i].AbsolutePath, cleanHome, ctx.Home); ok {
			plan.Repositories[i].AbsolutePath = abs
		}
	}
	plan.Digest = workspacenaming.Digest(plan)
	preview.Plan = &plan
	return preview
}

// resolveHomeAlias rewrites a path under a symlinked node home to the home's
// resolved physical prefix, leaving paths outside n.home and symlinks below
// home untouched.
func (n *Node) resolveHomeAlias(path string) string {
	cleanHome := filepath.Clean(n.home)
	if !filepath.IsAbs(cleanHome) || !filepath.IsAbs(path) {
		return path
	}
	realHome, err := n.realPath(cleanHome)
	if (err != nil && !errors.Is(err, fs.ErrNotExist)) || realHome == cleanHome {
		return path
	}
	if rewritten, ok := replacePathPrefix(filepath.Clean(path), cleanHome, realHome); ok {
		return rewritten
	}
	return path
}

func replacePathPrefix(path, from, to string) (string, bool) {
	if from == "" || to == "" || from == to || !filepath.IsAbs(path) || !filepath.IsAbs(from) {
		return path, false
	}
	rel, err := filepath.Rel(from, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return path, false
	}
	if rel == "." {
		return to, true
	}
	return filepath.Join(to, rel), true
}

// workspaceStateDir resolves symlinks on the node's own state directory
// (such as a symlinked home ancestor or directory-symlinked PERSONA_HOME)
// before checking that subdirectories created inside it contain no symlinks.
func (n *Node) workspaceStateDir() (string, error) {
	if n.dir == "" {
		return "", errors.New("workspace state path unavailable")
	}
	clean := filepath.Clean(n.dir)
	if !filepath.IsAbs(clean) {
		return "", errors.New("workspace state path unavailable")
	}
	dir, err := n.realPath(clean)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	if workspaceNoLinks(dir) != nil {
		return "", errors.New("workspace state path unavailable")
	}
	return dir, nil
}

func (n *Node) resolveWorkspaceNaming(ctx context.Context, raw json.RawMessage) (any, error) {
	var source workspacenaming.Source
	if err := json.Unmarshal(raw, &source); err != nil {
		return nil, err
	}
	preview := n.resolveWorkspacePlan(source)
	if preview.Plan == nil {
		return preview, nil
	}
	if problem := n.workspacePlanPathProblem(preview.Plan); problem != nil {
		preview.Plan = nil
		preview.Status = "invalid"
		preview.Diagnostics = append(preview.Diagnostics, *problem)
		return preview, nil
	}
	for _, repo := range preview.Plan.Repositories {
		checkCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		err := exec.CommandContext(checkCtx, "git", "check-ref-format", "--branch", repo.TaskBranch).Run()
		cancel()
		if err != nil {
			code := "git_validation_unavailable"
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				code = "branch_invalid"
			}
			preview.Plan = nil
			preview.Status = "invalid"
			preview.Diagnostics = append(preview.Diagnostics, workspacenaming.Problem{Code: code, Field: "branch_template", RepoID: repo.RepoID, Severity: "error"})
			break
		}
	}
	return preview, nil
}

// The same path policy runs before preview and preparation. Inspect resolved
// paths first to preserve diagnostics for links into private or credential paths.
func (n *Node) workspacePlanPathProblem(plan *workspacenaming.Plan) *workspacenaming.Problem {
	scopeDenied := func(repoID string) *workspacenaming.Problem {
		return &workspacenaming.Problem{Code: "base_scope_denied", Field: "base_directory", RepoID: repoID, Severity: "error"}
	}
	state := n.dir
	if state == "" {
		state = filepath.Join(n.home, ".persona")
	}
	state, err := n.realPath(state)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return scopeDenied("")
	}
	check := func(path, field, repoID string) *workspacenaming.Problem {
		real, err := n.realPath(path)
		if err != nil && !errors.Is(err, fs.ErrNotExist) || n.offLimitsResolved(real) != nil || workspacenaming.InPrivateState(real, state) {
			return scopeDenied(repoID)
		}
		switch err = workspaceNoLinks(path); {
		case errors.Is(err, errWorkspaceSymlink):
			return &workspacenaming.Problem{Code: "path_symlink", Field: field, RepoID: repoID, Severity: "error"}
		case err != nil:
			return scopeDenied(repoID)
		}
		return nil
	}
	if problem := check(plan.BasePath, "base_directory", ""); problem != nil {
		return problem
	}
	if problem := check(plan.RootPath, "task_directory_template", ""); problem != nil {
		return problem
	}
	for _, repo := range plan.Repositories {
		if repo.AbsolutePath == plan.RootPath {
			continue
		}
		if problem := check(repo.AbsolutePath, "directory_alias", repo.RepoID); problem != nil {
			return problem
		}
	}
	return nil
}
