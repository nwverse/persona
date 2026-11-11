package node

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// The real node RPC checks each side's actual path, including a deleted original and a guarded destination.
func TestGitBlobRenamePathsOverRPC(t *testing.T) {
	home := gitBlobEnv(t)
	if err := os.Mkdir(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	for path, contents := range map[string]string{
		".ssh/old.png": "synthetic image content",
		"plain.docx":   "synthetic document content",
		"ordinary.png": "synthetic ordinary content",
	} {
		if err := os.WriteFile(filepath.Join(home, path), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runPullGit(t, home, "init", "-q", "--initial-branch=main")
	runPullGit(t, home, "add", "--", ".")
	runPullGit(t, home, "commit", "-q", "-m", "base")
	runPullGit(t, home, "mv", ".ssh/old.png", "middle.png")
	runPullGit(t, home, "mv", "middle.png", "renamed image.png")
	runPullGit(t, home, "mv", "plain.docx", ".ssh/new.docx")

	n := &Node{home: home, state: newState()}
	hubConn, nodeConn := net.Pipe()
	hubPeer, nodePeer := rpc.New("h", hubConn), rpc.New("n", nodeConn)
	nodePeer.OnRequest = n.handle
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	done := make(chan struct{}, 2)
	go func() { _ = hubPeer.Serve(ctx, hubConn); done <- struct{}{} }()
	go func() { _ = nodePeer.Serve(ctx, nodeConn); done <- struct{}{} }()
	t.Cleanup(func() {
		cancel()
		_ = hubConn.Close()
		_ = nodeConn.Close()
		<-done
		<-done
	})

	var status GitStatus
	if err := hubPeer.Call(ctx, "git.status", map[string]any{"cwd": home}, &status); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"renamed image.png": ".ssh/old.png", ".ssh/new.docx": "plain.docx"}
	for _, f := range status.Files {
		if original, ok := want[f.Path]; ok {
			if f.Index != "R" || f.OrigPath != original {
				t.Fatalf("status for %s: %+v", f.Path, f)
			}
			delete(want, f.Path)
		}
	}
	if len(want) != 0 {
		t.Fatalf("renames absent from status: %v", want)
	}

	for _, c := range []struct {
		path, rev, contents string
		denied, missing     bool
	}{
		{".ssh/old.png", "HEAD", "", true, false},
		{"renamed image.png", "INDEX", "synthetic image content", false, false},
		{"plain.docx", "HEAD", "synthetic document content", false, false},
		{".ssh/new.docx", "INDEX", "", true, false},
		{"ordinary.png", "HEAD", "synthetic ordinary content", false, false},
		{"renamed image.png", "HEAD", "", false, true},
	} {
		var result proto.GitBlob
		err := hubPeer.Call(ctx, "git.blob", proto.GitBlobRequest{Cwd: home, Path: c.path, Rev: c.rev}, &result)
		if c.denied {
			if err == nil || !strings.Contains(err.Error(), errOffLimits.Error()) {
				t.Errorf("%s %s: got %+v, %v; want refusal", c.rev, c.path, result, err)
			}
			continue
		}
		if err != nil || result.Missing != c.missing {
			t.Errorf("%s %s: got %+v, %v", c.rev, c.path, result, err)
			continue
		}
		if !c.missing {
			data, err := os.ReadFile(result.Path)
			if err != nil || string(data) != c.contents {
				t.Errorf("%s %s: got %q, %v", c.rev, c.path, data, err)
			}
		}
	}
}
