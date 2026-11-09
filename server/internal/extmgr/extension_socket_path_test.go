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
	"testing"

	"github.com/nwverse/persona/server/internal/extension"
	"github.com/nwverse/persona/server/internal/extension/host"
	"github.com/nwverse/persona/server/internal/extension/sandbox"
	"github.com/nwverse/persona/server/internal/i18n"
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

// socketTestID is an extension id as long as the ones whose socket paths overflow.
const socketTestID = "a-long-extension-id"

// longIDExtension is the installed extension socketTestID, running rt with grants g.
func longIDExtension(rt *extension.Runtime, g extension.Permissions) *extension.Extension {
	p := &extension.Extension{Record: extension.Record{ID: socketTestID, Source: extension.SourceInstalled, Origin: "dir:/x", Granted: g},
		Manifest: &extension.Manifest{ID: socketTestID, Runtime: rt}}
	p.Full = p.Manifest
	return p
}

// withSandbox gives this machine its system's sandbox for the rest of the test.
func withSandbox(t *testing.T) {
	t.Helper()
	old := sandboxProbe
	sandboxProbe = func() sandbox.Support {
		return sandbox.Support{OS: runtime.GOOS, Bwrap: "/bin/false", SandboxExec: true}
	}
	t.Cleanup(func() { sandboxProbe = old })
}

// socketTestExtensions are the extensions of a manager whose directory makes <dir>/extensions/<tail> exactly n bytes
// (an external manager's directory, managers/<id> in the data directory, is where the paths get long), and what they
// have logged.
func socketTestExtensions(t *testing.T, n int, tail string) (*extensions, *syncLog) {
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
	logs := &syncLog{}
	ps := newExtensions(dir, nil, func(format string, args ...any) { logs.add(fmt.Sprintf(format, args...)) })
	t.Cleanup(func() {
		if px := ps.sandboxes.proxyIfStarted(); px != nil {
			px.Close()
		}
	})
	if got := len(filepath.Join(ps.root, tail)); got != n {
		t.Fatalf("the path is %d bytes, not %d", got, n)
	}
	return ps, logs
}

// The sockets the manager binds in a sandboxed extension's run directory: one longer than a Unix socket can be fails
// before the program starts, naming the path and the limit; one at the limit is bound as before.
func TestExtensionSocketPathLimit(t *testing.T) {
	most := unixSocketMost()
	if most == 0 {
		t.Skip("no Unix socket sandbox on " + runtime.GOOS)
	}
	withSandbox(t)
	tail := filepath.Join("run", socketTestID, "runtime.sock") // the longest socket name in the run directory
	p := longIDExtension(&extension.Runtime{HTTP: true}, extension.Permissions{AgentRuntimes: true})
	nodeSock := filepath.Join(shortTempDir(t), "runtime.sock")
	oldNode := nodeRuntimeSocket
	nodeRuntimeSocket = func() string { return nodeSock }
	t.Cleanup(func() { nodeRuntimeSocket = oldNode })

	// At the limit: confined and bound.
	ps, _ := socketTestExtensions(t, most, tail)
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

// An installed extension's HTTP socket outside a sandbox (<id>.sock), which its program binds: too long, one that
// speaks the protocol still starts and its log says why its HTTP side will not; one that serves only HTTP does not
// start, and its status says why in the app's language.
func TestExtensionHTTPSocketPathLimit(t *testing.T) {
	most := unixSocketMost()
	if most == 0 {
		t.Skip("no Unix socket limit known on " + runtime.GOOS)
	}
	for _, rpc := range []string{"", "none"} {
		p := longIDExtension(&extension.Runtime{RPC: rpc, HTTP: true, Command: extension.Command{Path: "bin/prog"}}, extension.Permissions{})
		for _, n := range []int{most, most + 1} {
			ps, logs := socketTestExtensions(t, n, socketTestID+".sock")
			ps.sandboxes.noDefault = true
			sock := filepath.Join(ps.root, socketTestID+".sock")
			s, err := ps.spec(context.Background(), p, &hubExtension{})
			over := n > most
			if over && rpc == "none" {
				if err == nil || !strings.Contains(err.Error(), sock) {
					t.Fatalf("rpc %q, %d bytes: %v", rpc, n, err)
				}
				if got := i18n.ExtensionStatusText(i18n.En, err.Error()); !strings.HasPrefix(got, "The socket path "+sock+" is ") {
					t.Fatalf("its status in English: %q", got)
				}
			} else if err != nil || s.Socket != sock {
				t.Fatalf("rpc %q, %d bytes: %v %q", rpc, n, err, s.Socket)
			}
			got := logs.String()
			if warned := strings.Contains(got, "extension "+socketTestID+": ") && strings.Contains(got, sock); warned != (over && rpc == "") {
				t.Fatalf("rpc %q, %d bytes: the log %q", rpc, n, got)
			}
		}
	}
}

// In a sandbox the HTTP socket is the run directory's http.sock (9 bytes longer than <id>.sock), and only that one is
// checked: the log never names the <id>.sock the program does not use. On Linux the proxy's socket, one byte longer
// still, stops the extension first.
func TestExtensionSandboxHTTPSocketPathLimit(t *testing.T) {
	most := unixSocketMost()
	if most == 0 {
		t.Skip("no Unix socket sandbox on " + runtime.GOOS)
	}
	withSandbox(t)
	p := longIDExtension(&extension.Runtime{HTTP: true, Command: extension.Command{Path: "bin/prog"}}, extension.Permissions{})
	ps, logs := socketTestExtensions(t, most+1, socketTestID+".sock")
	unused := filepath.Join(ps.root, socketTestID+".sock")
	run := filepath.Join(ps.root, "run", socketTestID)
	s, err := ps.spec(context.Background(), p, &hubExtension{})
	got := logs.String()
	if strings.Contains(got, unused) {
		t.Fatalf("the log names the socket the program does not use: %q", got)
	}
	if runtime.GOOS == "darwin" {
		if sock := filepath.Join(run, "http.sock"); err != nil || s.Socket != sock || !strings.Contains(got, sock) {
			t.Fatalf("%v %q, the log %q", err, s.Socket, got)
		}
	} else if err == nil || !strings.Contains(err.Error(), filepath.Join(run, "proxy.sock")) {
		t.Fatalf("the proxy's socket over the limit: %v", err)
	}
}
