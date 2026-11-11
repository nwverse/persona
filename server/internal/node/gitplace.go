package node

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/nwverse/persona/server/internal/proto"
)

// gitPlace is where a working directory sits in git: the repository's main checkout (root), the working tree it is in
// (worktree: root itself, or a linked worktree's folder) and the branch checked out there ("" when detached). The apps
// list machine → project (root) → worktree (labelled branch) → sessions with it (#1805).
type gitPlace struct {
	root, worktree, branch string
	project                *proto.SessionInventory
}

// fillGitPlace sets s's RepoRoot (unless its host already named it: Paseo, a Codex project), Worktree and Branch from
// its working directory. Both paths are physical directories (physicalGitPlace), whichever way s names its own, and
// so is the root a host named: a host that writes it through a link would have its main checkout a linked worktree
// of itself (physicalDirs). The assistant's own conversations are not in a project.
func fillGitPlace(s *proto.Session) {
	if s.Cwd == "" || s.Assistant {
		return
	}
	if filepath.IsAbs(s.RepoRoot) {
		if real := physicalDirs.lookup(s.RepoRoot).root; real != "" {
			s.RepoRoot = real
		}
	}
	p := gitPlaces.lookup(s.Cwd)
	project := p.project
	if s.RepoRoot != "" && (project == nil || project.ProjectKind == "non_git") && filepath.Clean(s.RepoRoot) != filepath.Clean(s.Cwd) {
		// Hosts can supply the real checkout even when an offline cwd has gone away.
		project = gitPlaces.lookup(s.RepoRoot).project
	}
	if project != nil {
		if s.Inventory == nil || s.Inventory.CreatedAt == nil {
			s.Inventory = project
		} else {
			s.Inventory = &proto.SessionInventory{
				ProjectKind: project.ProjectKind, ProjectRepo: project.ProjectRepo, CreatedAt: s.Inventory.CreatedAt,
			}
		}
	}
	if p.worktree == "" {
		return
	}
	if s.RepoRoot == "" {
		s.RepoRoot = p.root
	}
	if s.Worktree == "" {
		s.Worktree = p.worktree
	}
	if s.Branch == "" {
		s.Branch = p.branch
	}
}

// readGitPlace finds the .git nearest above cwd and reads it the way git does, without running git (this runs for every
// listed session): a .git folder is a main checkout; a .git file points at a linked worktree's (or a submodule's) git
// dir, whose commondir, when it has one, is the main checkout's .git. Paths are as cwd and git's files write them
// (no symlink resolution: physicalGitPlace does that).
func readGitPlace(cwd string) gitPlace {
	if cwd == "" || !filepath.IsAbs(cwd) {
		return gitPlace{}
	}
	// Use the existing lexical paths for the legacy worktree fields. A second
	// walk for identity is only needed if a symlink hides the real ancestors.
	p := walkGitPlace(filepath.Clean(cwd))
	real, err := filepath.EvalSymlinks(cwd)
	if err == nil && real != filepath.Clean(cwd) {
		p.project = walkGitPlace(real).project
	}
	return p
}

func walkGitPlace(cwd string) gitPlace {
	st, err := os.Stat(cwd)
	// Keep the legacy ancestor labels for a missing cwd, but do not use that
	// as proof of the session's project (it may have been a nested checkout).
	verified := err == nil && st.IsDir()
	project := func(metadata string) *proto.SessionInventory {
		if !verified {
			return nil
		}
		return gitProject(metadata)
	}
	for dir := cwd; ; {
		dotGit := filepath.Join(dir, ".git")
		if st, err := os.Stat(dotGit); err == nil {
			switch {
			case st.IsDir():
				return gitPlace{root: dir, worktree: dir, branch: headBranch(dotGit), project: project(dotGit)}
			case st.Mode().IsRegular():
				gitDir := pointedGitDir(dotGit, dir)
				if gitDir == "" {
					return gitPlace{}
				}
				root, metadata := dir, gitDir
				if common := commonDir(gitDir); common != "" {
					root, metadata = mainCheckout(common), common
				}
				return gitPlace{root: root, worktree: dir, branch: headBranch(gitDir), project: project(metadata)}
			default:
				return gitPlace{}
			}
		} else if !os.IsNotExist(err) {
			return gitPlace{}
		} else if _, err := os.Lstat(dotGit); !os.IsNotExist(err) {
			return gitPlace{} // a broken .git symlink is not evidence of a non-Git directory
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			if verified {
				return gitPlace{project: nonGitProject}
			}
			return gitPlace{}
		}
		dir = parent
	}
}

// physicalGitPlace is the Git place of the physical directory cwd is (symbolic links resolved, realPath), with the
// project and the working tree named by their physical directories too. The records the sessions of one repository
// come from do not agree on how to write a directory that is reached through a link (a home on another mount, macOS's
// /tmp): Claude Code records the physical one, Codex the one it was started in, and a host that names the project
// (Paseo, a Codex project) what git says, the physical one again. Read as written, one repository became two
// projects, and a host's main checkout a linked worktree of itself (#4492). The directory is resolved before the
// walk up, as git resolves the one it runs in: a link into a repository (~/.config/nvim → ~/dotfiles/nvim) is in that
// repository, and a link that leads out of one is not in it. The project and the working tree are resolved after
// it, because a linked worktree's .git file still writes its git dir the way `git worktree add` was run. A session's
// own cwd stays as its record has it; workspace_cwd is its physical directory.
func physicalGitPlace(cwd string) gitPlace {
	if cwd == "" || !filepath.IsAbs(cwd) {
		return gitPlace{}
	}
	p := readGitPlace(realPath(filepath.Clean(cwd)))
	if p.worktree == "" {
		return p
	}
	main := p.worktree == p.root
	p.root = realPath(p.root)
	if main {
		p.worktree = p.root
	} else {
		p.worktree = realPath(p.worktree)
	}
	return p
}

// pointedGitDir reads a .git file ("gitdir: <path>", relative to the folder it is in).
func pointedGitDir(file, dir string) string {
	b, err := os.ReadFile(file)
	if err != nil {
		return ""
	}
	line, _, _ := strings.Cut(string(b), "\n")
	path, ok := strings.CutPrefix(strings.TrimSpace(line), "gitdir:")
	if path = strings.TrimSpace(path); !ok || path == "" {
		return ""
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	return filepath.Clean(path)
}

// commonDir is a linked worktree's shared git dir (its "commondir" file, relative to gitDir); "" for a submodule or a
// main checkout, which have none.
func commonDir(gitDir string) string {
	b, err := os.ReadFile(filepath.Join(gitDir, "commondir"))
	if err != nil {
		return ""
	}
	path := strings.TrimSpace(string(b))
	if path == "" {
		return ""
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(gitDir, path)
	}
	return filepath.Clean(path)
}

// mainCheckout is the working tree a linked worktree's shared git dir belongs to: the folder holding a ".git" dir, a
// submodule's checkout (git keeps a submodule's git dir under the parent's .git/modules and names its checkout in
// core.worktree), else the bare repository itself, which has no checkout of its own.
func mainCheckout(common string) string {
	if filepath.Base(common) == ".git" {
		return filepath.Dir(common)
	}
	if tree := coreWorktree(common); tree != "" {
		return tree
	}
	return common
}

// coreWorktree is core.worktree from a git dir's config, relative to that git dir; "" when unset or unreadable.
func coreWorktree(gitDir string) string {
	b, err := os.ReadFile(filepath.Join(gitDir, "config"))
	if err != nil {
		return ""
	}
	core := false
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			core = strings.EqualFold(strings.TrimSpace(strings.Trim(line, "[]")), "core")
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !core || !ok || !strings.EqualFold(strings.TrimSpace(key), "worktree") {
			continue
		}
		path := strings.Trim(strings.TrimSpace(value), `"`)
		if path == "" {
			return ""
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(gitDir, path)
		}
		return filepath.Clean(path)
	}
	return ""
}

// headBranch is the branch HEAD names ("ref: refs/heads/<branch>"); "" when detached or unreadable.
func headBranch(gitDir string) string {
	b, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return ""
	}
	ref, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "ref:")
	if !ok {
		return ""
	}
	branch, _ := strings.CutPrefix(strings.TrimSpace(ref), "refs/heads/")
	return branch
}
