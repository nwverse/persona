package extmgr

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/nwverse/persona/server/internal/extension"
	"github.com/nwverse/persona/server/internal/extension/host"
	"github.com/nwverse/persona/server/internal/extension/sandbox"
)

// unixSocketMost is the longest path a Unix socket binds at here (sockaddr_un's sun_path less its NUL), or 0 where the
// test does not know it.
func unixSocketMost() int {
	switch runtime.GOOS {
	case "darwin":
		return 103
	case "linux":
		return 107
	}
	return 0
}

// socketTestExtensions are the extensions of a manager whose directory makes <dir>/extensions/<tail> exactly n bytes
// (an external manager's directory, managers/<id> in the data directory, is where the paths get long), and what they
// have logged so far.
func socketTestExtensions(t *testing.T, n int, tail string) (*extensions, func() string) {
	t.Helper()
	base := filepath.Join(shortTempDir(t), "managers")
	pad := n - len(filepath.Join(base, "m-abc123", "extensions", tail)) // what the manager's directory still needs
	if pad < 0 {
		t.Fatalf("the temporary directory leaves no room for %d bytes", n)
	}
	dir := filepath.Join(base, "m-abc123"+strings.Repeat("x", pad))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var lines []string
	ps := newExtensions(dir, nil, func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, fmt.Sprintf(format, args...))
	})
	t.Cleanup(func() {
		if px := ps.sandboxes.proxyIfStarted(); px != nil {
			px.Close()
		}
	})
	if got := len(filepath.Join(ps.root, tail)); got != n {
		t.Fatalf("the path is %d bytes, not %d", got, n)
	}
	return ps, func() string {
		mu.Lock()
		defer mu.Unlock()
		return strings.Join(lines, "\n")
	}
}

// The sockets the manager binds in a sandboxed extension's run directory: one longer than a Unix socket can be fails
// before the program starts, naming the path and the limit; one at the limit is bound as before.
func TestExtensionSocketPathLimit(t *testing.T) {
	most := unixSocketMost()
	if most == 0 {
		t.Skip("no Unix socket sandbox on " + runtime.GOOS)
	}
	old := sandboxProbe
	sandboxProbe = func() sandbox.Support {
		return sandbox.Support{OS: runtime.GOOS, Bwrap: "/bin/false", SandboxExec: true}
	}
	t.Cleanup(func() { sandboxProbe = old })
	const id = "a-long-extension-id"
	tail := filepath.Join("run", id, "runtime.sock") // the longest socket name in the run directory
	p := &extension.Extension{Record: extension.Record{ID: id, Source: extension.SourceInstalled, Origin: "dir:/x",
		Granted: extension.Permissions{AgentRuntimes: true}},
		Manifest: &extension.Manifest{ID: id, Runtime: &extension.Runtime{HTTP: true}}}
	p.Full = p.Manifest
	nodeSock := filepath.Join(shortTempDir(t), "runtime.sock")
	oldNode := nodeRuntimeSocket
	nodeRuntimeSocket = func() string { return nodeSock }
	t.Cleanup(func() { nodeRuntimeSocket = oldNode })

	// At the limit: confined and bound.
	ps, _ := socketTestExtensions(t, most, tail)
	ps.sandboxes.noDefault = false
	s := host.Spec{HTTP: true}
	if err := ps.confine(context.Background(), p, &hubExtension{}, &s); err != nil {
		t.Fatalf("a socket path at the limit: %v", err)
	}
	if _, _, _, err := s.Wrap("prog", nil); err != nil {
		t.Fatalf("binding at the limit: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ps.root, tail)); err != nil {
		t.Fatalf("its runtime socket: %v", err)
	}

	// One byte over: the system would not take it, and the extension does not start, saying why.
	ps, _ = socketTestExtensions(t, most+1, tail)
	ps.sandboxes.noDefault = false
	long := filepath.Join(ps.root, tail)
	if err := os.MkdirAll(filepath.Dir(long), 0o700); err != nil {
		t.Fatal(err)
	}
	if ln, err := net.Listen("unix", long); err == nil {
		ln.Close()
		t.Fatalf("the system took %d bytes: the limit is not %d", len(long), most)
	}
	s = host.Spec{HTTP: true}
	err := ps.confine(context.Background(), p, &hubExtension{}, &s)
	if err == nil {
		t.Fatal("confined with a socket path over the limit")
	}
	if msg := err.Error(); !strings.Contains(msg, long) || !strings.Contains(msg, strconv.Itoa(most)) {
		t.Fatalf("the error does not name the path and the limit: %v", err)
	}
	if s.Wrap != nil {
		t.Fatal("wrapped all the same")
	}
}

// An installed extension's HTTP socket, which its program binds: too long, the extension still starts (its protocol
// side works without it), and its log says why its HTTP side will not.
func TestExtensionHTTPSocketPathLimit(t *testing.T) {
	most := unixSocketMost()
	if most == 0 {
		t.Skip("no Unix socket limit known on " + runtime.GOOS)
	}
	const id = "a-long-extension-id"
	p := &extension.Extension{Record: extension.Record{ID: id, Source: extension.SourceInstalled, Origin: "dir:/x"},
		Manifest: &extension.Manifest{ID: id, Runtime: &extension.Runtime{HTTP: true, Command: extension.Command{Path: "bin/prog"}}}}
	p.Full = p.Manifest
	for _, n := range []int{most, most + 1} {
		ps, logs := socketTestExtensions(t, n, id+".sock")
		sock := filepath.Join(ps.root, id+".sock")
		s, err := ps.specOf(context.Background(), p, &hubExtension{})
		if err != nil || s.Socket != sock {
			t.Fatalf("%d bytes: %v %q", n, err, s.Socket)
		}
		got := logs()
		if warned := strings.Contains(got, "extension "+id+": ") && strings.Contains(got, sock); warned != (n > most) {
			t.Fatalf("%d bytes: the log %q", n, got)
		}
	}
}
