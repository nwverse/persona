package node

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

// gitBlobEnv gives a test its own cache folder (cacheEnv) and git identity (gitEnv), and answers a folder for its
// repository.
func gitBlobEnv(t *testing.T) string {
	t.Helper()
	cacheEnv(t, true)
	gitEnv(t)
	return t.TempDir()
}

func TestGitBlobWritesEachVersionForComparing(t *testing.T) {
	base := gitBlobEnv(t)
	repo := filepath.Join(base, "repo")
	os.MkdirAll(filepath.Join(repo, "docs"), 0o700)
	runPullGit(t, base, "init", "--initial-branch=main", repo)
	v1 := append([]byte("%PDF-1.4 one\x00\xff"), bytes.Repeat([]byte{7}, 100)...)
	os.WriteFile(filepath.Join(repo, "docs", "spec.pdf"), v1, 0o600)
	runPullGit(t, repo, "add", ".")
	runPullGit(t, repo, "commit", "-m", "one")
	first := strings.TrimSpace(runPullGit(t, repo, "rev-parse", "HEAD"))
	v2 := []byte("%PDF-1.4 two\x00\xfe")
	os.WriteFile(filepath.Join(repo, "docs", "spec.pdf"), v2, 0o600)
	runPullGit(t, repo, "commit", "-am", "two")
	second := strings.TrimSpace(runPullGit(t, repo, "rev-parse", "HEAD"))
	v3 := []byte("%PDF-1.4 staged")
	os.WriteFile(filepath.Join(repo, "docs", "spec.pdf"), v3, 0o600)
	runPullGit(t, repo, "add", ".")

	n := &Node{home: base, state: newState()}
	read := func(rev string, want []byte) {
		t.Helper()
		b, err := n.gitBlob(context.Background(), proto.GitBlobRequest{Cwd: filepath.Join(repo, "docs"), Path: "docs/spec.pdf", Rev: rev})
		if err != nil {
			t.Fatalf("%s: %v", rev, err)
		}
		got, _ := os.ReadFile(b.Path)
		if !bytes.Equal(got, want) || b.Size != int64(len(want)) || filepath.Ext(b.Path) != ".pdf" {
			t.Fatalf("%s: %q (%s)", rev, got, b.Path)
		}
	}
	read("HEAD", v2)
	read("INDEX", v3)
	read(first[:12], v1)
	read(second+"^", v1)
	read("HEAD", v2) // from the cache this time

	if b, err := n.gitBlob(context.Background(), proto.GitBlobRequest{Cwd: repo, Path: "docs/new.pdf", Rev: "HEAD"}); err != nil || !b.Missing {
		t.Fatalf("missing file = %+v, %v", b, err)
	}
	for _, bad := range []proto.GitBlobRequest{
		{Cwd: repo, Path: "docs/spec.pdf", Rev: "--output=/tmp/x"},
		{Cwd: repo, Path: "docs/spec.pdf", Rev: "main~1"},
		{Cwd: repo, Path: "../etc/passwd", Rev: "HEAD"},
		{Cwd: repo, Path: "-x", Rev: "HEAD"},
	} {
		if _, err := n.gitBlob(context.Background(), bad); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
}

func TestGitBlobSameVersionAtOnce(t *testing.T) {
	base := gitBlobEnv(t)
	repo := filepath.Join(base, "repo")
	os.MkdirAll(repo, 0o700)
	runPullGit(t, base, "init", "--initial-branch=main", repo)
	want := make([]byte, 4<<20) // big enough that writing it overlaps the other requests'
	rand.Read(want)
	os.WriteFile(filepath.Join(repo, "deck.pdf"), want, 0o600)
	runPullGit(t, repo, "add", ".")
	runPullGit(t, repo, "commit", "-m", "deck")
	cache, err := privateCacheDir("blobs")
	if err != nil {
		t.Fatal(err)
	}
	n := &Node{home: base, state: newState()}
	// fetch asks for the version and answers the revision a download of the copy it got is pinned to
	fetch := func() (string, error) {
		b, err := n.gitBlob(context.Background(), proto.GitBlobRequest{Cwd: repo, Path: "deck.pdf", Rev: "HEAD"})
		if err != nil {
			return "", err
		}
		head, err := n.readFileChunk(proto.FileChunkRequest{Path: b.Path})
		if err != nil {
			return "", err
		}
		if got, err := os.ReadFile(b.Path); err != nil || !bytes.Equal(got, want) || b.Size != int64(len(want)) {
			return "", fmt.Errorf("read %d of %d bytes (%v)", len(got), len(want), err)
		}
		return head.Revision, nil
	}
	const at = 16
	for round := range 3 {
		os.RemoveAll(cache) // every request misses the cache
		if _, err := privateCacheDir("blobs"); err != nil {
			t.Fatal(err)
		}
		errs := make([]error, at)
		revs := make([]string, at)
		var wg sync.WaitGroup
		for i := range at {
			wg.Add(1)
			go func() {
				defer wg.Done()
				revs[i], errs[i] = fetch()
			}()
		}
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				t.Fatalf("round %d, request %d: %v", round, i, err)
			}
		}
		ents, _ := os.ReadDir(cache)
		if len(ents) != 1 {
			t.Fatalf("round %d: cache holds %v", round, ents)
		}
		info, _ := ents[0].Info()
		for i, rev := range revs {
			if rev != fileRevision(info) {
				t.Fatalf("round %d: the copy request %d got was replaced after it was handed out", round, i)
			}
		}
	}
}
