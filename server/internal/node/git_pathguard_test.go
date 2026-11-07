package node

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

// The Git panel's diff and versions show a file's content, so they refuse what file.read refuses: an untracked key
// in a repository, a home kept in Git with its credentials folders, a pattern for a path (#5252). Other files diff as
// before.
func TestGitDiffAndBlobGuardPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache")) // gitBlob writes there on Linux
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_AUTHOR_NAME", "Sample Dev")
	t.Setenv("GIT_AUTHOR_EMAIL", "dev@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "Sample Dev")
	t.Setenv("GIT_COMMITTER_EMAIL", "dev@example.com")
	n := &Node{home: home}
	write := func(rel, body string) {
		p := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// A home kept in Git (dotfiles), with a project in it.
	runPullGit(t, home, "init", "-q", "--initial-branch=main")
	write("notes.md", "plain\n")
	write(".aws/credentials", "SECRET-AWS\n")
	write("code/repo/main.go", "package main\n")
	runPullGit(t, home, "add", "--", "notes.md", ".aws/credentials")
	runPullGit(t, home, "commit", "-q", "-m", "Add sample files")
	write(".aws/credentials", "SECRET-AWS-2\n") // a change to a tracked secret
	write(".ssh/config", "Host secret\n")       // untracked
	write("code/repo/id_ed25519", "SECRET-KEY\n")
	write("notes.md", "plain, edited\n")
	ctx := context.Background()

	for _, c := range []struct{ cwd, path string }{
		{"~", ".ssh/config"},
		{"~", ".aws/credentials"},
		{"~", "code/repo/id_ed25519"},
		{filepath.Join(home, "code/repo"), "code/repo/id_ed25519"}, // the path is the repository root's
	} {
		for _, staged := range []bool{false, true} {
			d, err := n.gitDiff(ctx, c.cwd, c.path, staged)
			if !errors.Is(err, errOffLimits) {
				t.Errorf("git.diff %s (staged %v): %v %v, want refused", c.path, staged, d, err)
			}
		}
	}
	// A pattern is one file's name, not every changed file (".aws/credentials" among them); a folder is every changed
	// file under it, each guarded.
	runPullGit(t, home, "add", "--", "code/repo/id_ed25519")
	write("code/repo/id_ed25519", "SECRET-KEY-2\n") // tracked, changed again: staged and unstaged diffs
	for _, p := range []string{"*", ":(glob)**", ".aws/*", ".", "./", "code", "code/", "code/repo", ".aws", ""} {
		for _, staged := range []bool{false, true} {
			d, err := n.gitDiff(ctx, "~", p, staged)
			if s, _ := d["diff"].(string); strings.Contains(s, "SECRET") {
				t.Errorf("git.diff %q (staged %v) shows a guarded file: %v", p, staged, err)
			}
		}
	}
	// A guarded file that is gone is still refused (its diff is its whole old content).
	if err := os.Remove(filepath.Join(home, ".aws/credentials")); err != nil {
		t.Fatal(err)
	}
	if d, err := n.gitDiff(ctx, "~", ".aws/credentials", false); !errors.Is(err, errOffLimits) {
		t.Errorf("git.diff of a deleted secret: %v %v", d, err)
	}
	if b, err := n.gitBlob(ctx, proto.GitBlobRequest{Cwd: "~", Path: ".aws/credentials", Rev: "INDEX"}); !errors.Is(err, errOffLimits) {
		t.Errorf("git.blob INDEX of a deleted secret: %+v %v", b, err)
	}
	if got := gitSubcommand([]string{"-c", "pull.rebase=false", "pull", "--ff-only"}); got != "pull" {
		t.Errorf("gitSubcommand: %q", got)
	}
	if d, err := n.gitDiff(ctx, "~", "notes.md", false); err != nil || !strings.Contains(d["diff"].(string), "+plain, edited") {
		t.Errorf("git.diff notes.md: %v %v", d, err)
	}
	if d, err := n.gitDiff(ctx, "~", "code/repo/main.go", false); err != nil || !strings.Contains(d["diff"].(string), "+package main") {
		t.Errorf("git.diff of an untracked plain file: %v %v", d, err)
	}

	if b, err := n.gitBlob(ctx, proto.GitBlobRequest{Cwd: "~", Path: ".aws/credentials", Rev: "HEAD"}); !errors.Is(err, errOffLimits) {
		t.Errorf("git.blob .aws/credentials: %+v %v, want refused", b, err)
	}
	if b, err := n.gitBlob(ctx, proto.GitBlobRequest{Cwd: "~", Path: "notes.md", Rev: "HEAD"}); err != nil || b.Missing || b.Path == "" {
		t.Errorf("git.blob notes.md: %+v %v", b, err)
	}
}
