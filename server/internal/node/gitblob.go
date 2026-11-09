package node

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/proto"
)

// maxBlobBytes: bigger versions are not written out for comparing.
const maxBlobBytes = 200 << 20

// blobWrites lets one request at a time look a version up in the cache and write it there on a miss; requests for it at
// the same time share that one's copy. Each writing its own would rename a second copy over one already handed out,
// under a download pinned to its revision.
var blobWrites singleflight.Group

// blobRev: HEAD, INDEX (the staged version), or a commit (full or short hash) optionally followed by ^ (its parent).
var blobRev = lazyre.New(`^(HEAD|INDEX|[0-9a-f]{4,64}\^?)$`)

// gitBlob writes one version of a file into the node's cache so a client can read it like any file (file/download):
// the web compares a changed PDF / docx / pptx / picture with the version before (docs/protocol/protocol.md "File preview").
// path is relative to the repository's root, as git/status and git/show give it. A version where the file does not
// exist (added, deleted) answers Missing.
func (n *Node) gitBlob(ctx context.Context, p proto.GitBlobRequest) (*proto.GitBlob, error) {
	if !blobRev.MatchString(p.Rev) {
		return nil, fmt.Errorf("bad revision")
	}
	path := filepath.ToSlash(filepath.Clean(strings.TrimSpace(p.Path)))
	if path == "." || path == "" || strings.HasPrefix(path, "../") || strings.HasPrefix(path, "/") || strings.HasPrefix(path, "-") {
		return nil, fmt.Errorf("bad path")
	}
	dir, err := n.gitDir(p.Cwd)
	if err != nil {
		return nil, err
	}
	root, err := git(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("not a git repository")
	}
	root = strings.TrimSpace(root)
	if err := n.guardRepoPath(root, path); err != nil { // its versions are the file's content
		return nil, err
	}
	spec := p.Rev + ":" + path
	if p.Rev == "INDEX" {
		spec = ":" + path // stage 0
	}
	sha, err := git(ctx, root, "rev-parse", "--verify", "--quiet", spec)
	sha = strings.TrimSpace(sha)
	if err != nil || sha == "" {
		return &proto.GitBlob{Missing: true}, nil
	}
	size, err := git(ctx, root, "cat-file", "-s", sha)
	if err != nil {
		return nil, err
	}
	var n64 int64
	fmt.Sscan(strings.TrimSpace(size), &n64)
	if n64 > maxBlobBytes {
		return nil, fmt.Errorf("version is too big to compare (%d bytes)", n64)
	}
	cache, err := privateCacheDir("blobs")
	if err != nil {
		return nil, fmt.Errorf("version cache: %w", err)
	}
	// named by the blob's id (content-addressed: never stale) and the file's extension, which the viewers go by
	out := filepath.Join(cache, sha+strings.ToLower(filepath.Ext(path)))
	_, err, _ = blobWrites.Do(out, func() (any, error) {
		if st, err := os.Stat(out); err == nil && st.Size() == n64 && usedCached(out, st) {
			return nil, nil
		}
		pruneConverted(cache)
		// the waiting requests share this run, so one of them giving up does not end it
		data, err := gitTimed(context.WithoutCancel(ctx), root, time.Minute, "cat-file", "blob", sha)
		if err != nil {
			return nil, err
		}
		return nil, writeAtomic(out, strings.NewReader(data))
	})
	if err != nil {
		return nil, err
	}
	return &proto.GitBlob{Path: out, Size: n64}, nil
}
