package node

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGitProjectAcrossClonesAndWorktrees(t *testing.T) {
	dir := t.TempDir()
	makeRepo := func(name, remote string) string {
		t.Helper()
		root := filepath.Join(dir, name)
		writeGitFile(t, filepath.Join(root, ".git", "HEAD"), "ref: refs/heads/main\n")
		if remote != "" {
			writeGitFile(t, filepath.Join(root, ".git", "config"), "[remote \"origin\"]\n\turl = "+remote+"\n")
		}
		return root
	}
	first := makeRepo("clone-a", "https://demo-user:demo-password@example.com/team/app.git/?access_token=placeholder")
	second := makeRepo("clone-b", "git@example.com:team/app")
	same := makeRepo("clone-c", "ssh://git@EXAMPLE.COM:22/team/app.git")
	otherOwner := makeRepo("clone-d", "https://example.com/other/app.git")
	otherHost := makeRepo("clone-e", "https://example.org/team/app.git")
	local := makeRepo("local-a", "")
	otherLocal := makeRepo("local-b", "")
	makeWorktree := func(root, name string) string {
		t.Helper()
		worktree := filepath.Join(dir, name)
		gitDir := filepath.Join(root, ".git", "worktrees", name)
		writeGitFile(t, filepath.Join(worktree, ".git"), "gitdir: "+gitDir+"\n")
		writeGitFile(t, filepath.Join(gitDir, "commondir"), "../..\n")
		writeGitFile(t, filepath.Join(gitDir, "HEAD"), "ref: refs/heads/topic\n")
		return worktree
	}
	want := readGitPlace(first).project
	if want == nil || want.ProjectKind != "remote" || len(want.ProjectRepo) != 64 {
		t.Fatalf("invalid remote metadata: %+v", want)
	}
	for _, root := range []string{second, same, makeWorktree(first, "shared-tree")} {
		if got := readGitPlace(root).project; got == nil || *got != *want {
			t.Fatalf("%s: %+v != %+v", root, got, want)
		}
	}
	for _, root := range []string{otherOwner, otherHost} {
		if got := readGitPlace(root).project; got == nil || got.ProjectKind != "remote" || got.ProjectRepo == want.ProjectRepo {
			t.Fatalf("unrelated remote merged: %+v", got)
		}
	}
	a, b, worktree := readGitPlace(local).project, readGitPlace(otherLocal).project, readGitPlace(makeWorktree(local, "local-tree")).project
	if a == nil || b == nil || worktree == nil || a.ProjectKind != "local" || b.ProjectRepo == a.ProjectRepo || *worktree != *a {
		t.Fatalf("local repos: a=%+v b=%+v worktree=%+v", a, b, worktree)
	}
	plain := filepath.Join(dir, "notes")
	if err := os.Mkdir(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	// Stop before the ambient checkout if the test's temporary base is inside one.
	if got := readGitPlace(plain).project; got == nil || got.ProjectKind != "non_git" {
		t.Fatalf("plain project: %+v", got)
	}
	if got := readGitPlace(filepath.Join(dir, "gone")).project; got != nil {
		t.Fatalf("missing cwd should remain unknown: %+v", got)
	}
	writeGitFile(t, filepath.Join(plain, ".git"), "gitdir: /missing")
	if got := readGitPlace(plain).project; got != nil {
		t.Fatalf("broken .git should remain unknown: %+v", got)
	}
}

func TestGitProjectDistinguishesSSHHomeFromAbsolutePath(t *testing.T) {
	alice := normalizedRemote("reader-a@example.com:project.git")
	bob := normalizedRemote("reader-b@example.com:project.git")
	absolute := normalizedRemote("reader-a@example.com:/project.git")
	if alice == "" || bob == "" || absolute == "" || alice == bob || alice == absolute {
		t.Fatal("different SSH homes or absolute paths merged")
	}
	if absolute != normalizedRemote("ssh://reader-a@example.com:22/project.git") {
		t.Fatal("equivalent absolute SSH paths differ")
	}
	if normalizedRemote("git@example.com:/team/project.git") == normalizedRemote("git@example.com:team/project.git") {
		t.Fatal("absolute SCP and hosted paths merged")
	}
	if got := normalizedRemote("example.com:project.git"); got != "" {
		t.Fatal("implicit SSH account was guessed")
	}
}

func TestRecordedSessionStartDoesNotUseLaterActivity(t *testing.T) {
	at := "2026-01-02T03:04:05Z"
	for _, opening := range []string{
		`{"type":"session_meta","timestamp":"` + at + `","payload":{"cwd":"/work/app"}}`,
		`{"type":"user","sessionId":"example","parentUuid":null,"timestamp":"` + at + `","cwd":"/work/app","message":{"role":"user","content":"start"}}`,
	} {
		p := filepath.Join(t.TempDir(), "session.jsonl")
		writeGitFile(t, p, opening+"\n"+`{"type":"user","sessionId":"example","parentUuid":"previous","timestamp":"2026-01-03T03:04:05Z"}`+"\n")
		st, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		got := readQuickMeta(p, st).inventory
		if got == nil || got.CreatedAt.Format(time.RFC3339) != at {
			t.Fatalf("start: %+v", got)
		}
	}
	if got := recordedSessionStart([]byte(`{"type":"user","sessionId":"example","parentUuid":"previous","timestamp":"` + at + `"}`)); got != nil {
		t.Fatalf("non-opening message became creation time: %+v", got)
	}
}
