//go:build codexarchivelive

package node

import (
	"context"
	"fmt"
	"net"
	"os"
	osexec "os/exec"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// Native archive against a real Codex (docs/integrations/codex.md "Archive"), in a throwaway CODEX_HOME of its own:
// a thread made there with thread/start (no model call), archived and restored first through a `codex app-server`
// the node is connected to, then, with that app-server gone, with the codex command in the user's shell. Never touches
// the user's threads. Run with -tags codexarchivelive and PERSONA_CODEX_ARCHIVE=1 (codex on PATH, 0.163 or later).
func TestLiveCodexNativeArchive(t *testing.T) {
	if os.Getenv("PERSONA_CODEX_ARCHIVE") == "" {
		t.Skip("PERSONA_CODEX_ARCHIVE not set")
	}
	codex, err := osexec.LookPath("codex")
	if err != nil {
		t.Skip("codex is not on PATH")
	}
	n := remoteTestNode(t)
	n.codices.checked = func() ([]codexProcess, bool) { return nil, true } // nothing else has the throwaway home open
	if err := os.MkdirAll(n.codexHome(), 0o700); err != nil {
		t.Fatal(err)
	}
	home := canonicalCodexHome(n.codexHome(), n.home)

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	ctx, cancel := context.WithCancel(context.Background())
	server := osexec.CommandContext(ctx, codex, "app-server", "--listen", fmt.Sprintf("ws://127.0.0.1:%d", port))
	server.Env = append(os.Environ(), "CODEX_HOME="+home)
	killGroupOnCancel(server) // the npm wrapper's codex too, not just the wrapper
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	stopped := false
	stop := func() {
		if !stopped {
			stopped = true
			cancel()
			_ = server.Wait()
		}
	}
	t.Cleanup(stop)
	spec := proto.CodexServer{ID: "live", Home: home, Endpoint: fmt.Sprintf("ws://127.0.0.1:%d", port)}
	deadline := time.Now().Add(30 * time.Second)
	var c *codexRemote
	for c == nil {
		n.ensureCodexRemote(context.Background(), spec)
		time.Sleep(300 * time.Millisecond)
		n.codices.mu.Lock()
		r := n.codices.remotes["live"]
		n.codices.mu.Unlock()
		if r != nil {
			r.mu.Lock()
			if r.ready {
				c = r
			}
			r.mu.Unlock()
		}
		if c == nil && time.Now().After(deadline) {
			t.Fatal("the app-server did not come up")
		}
	}
	var started struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	cctx, ccancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer ccancel()
	if err := c.wire.call(cctx, "thread/start", map[string]any{"cwd": home}, &started); err != nil {
		t.Fatal(err)
	}
	thread := started.Thread.ID
	if err := c.wire.call(cctx, "thread/name/set", map[string]any{"threadId": thread, "name": "persona archive check"}, nil); err != nil {
		t.Fatal(err) // writes the rollout, so the thread is on disk
	}
	id := n.registerCodex(home, thread, "")
	at := func() string {
		p := rolloutPath(home, thread)
		if p == "" {
			return "missing"
		}
		if codexArchived(home, p) {
			return "archived"
		}
		return "sessions"
	}
	check := func(archive bool, via, want string) {
		t.Helper()
		res, err := n.nativeArchive(context.Background(), id, archive)
		if err != nil || res.Native != nativeArchiveDone || res.Via != via || at() != want {
			t.Fatalf("archive=%v: %+v %v, rollout in %s", archive, res, err, at())
		}
		again, err := n.nativeArchive(context.Background(), id, archive)
		if err != nil || !again.Already {
			t.Fatalf("again archive=%v: %+v %v", archive, again, err)
		}
	}
	if at() != "sessions" {
		t.Fatalf("rollout in %s", at())
	}
	check(true, "app_server", "archived")
	check(false, "app_server", "sessions")

	stop()
	c.close()
	n.codices.mu.Lock()
	delete(n.codices.remotes, "live")
	n.codices.mu.Unlock()
	check(true, "cli", "archived")
	check(false, "cli", "sessions")
	t.Logf("thread %s archived and restored through the app-server and the codex command", thread)
}
