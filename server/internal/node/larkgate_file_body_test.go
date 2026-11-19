//go:build !windows

package node

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestLarkFileBodyCannotAuthorizeStaleContent(t *testing.T) {
	dir := t.TempDir()
	var risk larkRisk
	writeBody := func(name, body string) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}

	cfg := olCfg()
	proxiedCfg := olCfg()
	proxiedCfg.Gates[0] = proxiedGate("http://example.invalid")
	independentCfg := olCfg()
	independentCfg.Gates[0].Rules = append([]proto.LarkRule{
		{Method: "POST", Path: "/conversations", Action: "allow"},
	}, independentCfg.Gates[0].Rules...)

	writeBody("relative.json", `{"type":"p2p"}`)
	absolute := writeBody("absolute.json", `{"type":"p2p"}`)
	changed := writeBody("changed.json", `{"type":"p2p"}`)

	tests := []struct {
		name    string
		command string
		cfg     proto.AssistantLark
		want    string
		after   func()
	}{
		{
			name:    "lone file body requires confirmation without an authenticated proxy",
			command: `ex POST /conversations @relative.json`,
			cfg:     cfg,
			want:    "confirm",
		},
		{
			name:    "absolute file rewritten earlier in the command",
			command: `echo '{"type":"group"}' > ` + absolute + `; ex POST /conversations @` + absolute,
			cfg:     cfg,
			want:    "confirm",
		},
		{
			name:    "file changed after its check-time snapshot",
			command: `ex POST /conversations @changed.json`,
			cfg:     cfg,
			want:    "confirm",
			after: func() {
				if err := os.WriteFile(changed, []byte(`{"type":"group"}`), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:    "inline body keeps its body-scoped allow",
			command: `ex POST /conversations '{"type":"p2p"}'`,
			cfg:     cfg,
			want:    "allow",
		},
		{
			name:    "authenticated gate keeps its file-backed allow for proxy comparison",
			command: `ex POST /conversations @relative.json`,
			cfg:     proxiedCfg,
			want:    "allow",
		},
		{
			name:    "file matched by a body-independent allow rule",
			command: `ex POST /conversations @relative.json`,
			cfg:     independentCfg,
			want:    "allow",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			verdict := classifyLark(tc.command, tc.cfg.Gates, dir, risk)
			if tc.after != nil {
				tc.after()
			}
			if len(verdict.calls) != 1 {
				t.Fatalf("classified %d gated calls, want one", len(verdict.calls))
			}
			if got := larkDecision(tc.cfg, verdict.calls[0]); got != tc.want {
				t.Fatalf("decision=%q, want %q", got, tc.want)
			}
		})
	}
}

func TestLarkFileBodyBypassStillConfirms(t *testing.T) {
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "body.json"), []byte(`{"type":"p2p"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	n := codexTestNode(t)
	fh := withFakeHub(t, n)
	p, c, _ := newFakeCodex(t, n, cwd)
	p.id, c.threadID = testThread, testThread
	n.procs[testThread] = p
	n.larkCfg, n.larkCfgAt = olCfg(), time.Now().Add(time.Hour)
	n.noteModes(testThread, proto.ModeBypass, "")
	fh.answer = func(proto.Interaction) proto.Resolution { no := false; return proto.Resolution{Allow: &no} }
	decision, _, err := n.larkCheck(context.Background(), testThread, cwd, `ex POST /conversations @body.json`, proto.Interaction{SessionID: testThread, ExpiresAt: time.Now().Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(fh.interactions()); got != 1 || decision != "deny" {
		t.Fatalf("bypass decision=%q cards=%d, want one confirmation", decision, got)
	}
}

func TestReadBodyFileRejectsUnsafeFiles(t *testing.T) {
	dir := t.TempDir()
	oversize := filepath.Join(dir, "oversize.json")
	if err := os.WriteFile(oversize, []byte(strings.Repeat("x", 1<<20+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	exact := filepath.Join(dir, "exact.json")
	if err := os.WriteFile(exact, []byte(strings.Repeat("x", 1<<20)), 0o600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(dir, "alias")
	if err := os.Symlink(dir, alias); err != nil {
		t.Fatal(err)
	}
	t.Run("exactly one MiB remains an ordinary body", func(t *testing.T) {
		for _, path := range []string{exact, filepath.Join(alias, "exact.json")} {
			if got := readBodyFile(path, dir); got != strings.Repeat("x", 4000)+"…" {
				t.Fatalf("exactly one MiB through %q returned %d bytes, want ordinary clipping", path, len(got))
			}
		}
	})
	devices := filepath.Join(dir, "devices")
	if err := os.Symlink("/dev", devices); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		path string
	}{
		{name: "over one MiB", path: oversize},
		{name: "device", path: "/dev/null"},
		{name: "symlinked device parent", path: filepath.Join(devices, "null")},
		{name: "proc", path: "/proc/self/cmdline"},
		{name: "sys", path: "/sys/kernel"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := readBodyFile(tc.path, dir)
			if !strings.HasPrefix(got, "（读不到 ") {
				t.Fatalf("readBodyFile(%q) returned %d bytes of usable content", tc.path, len(got))
			}
			if bodyMatches(map[string]any{"type": "p2p"}, got) {
				t.Fatalf("unsafe body %q satisfied a body-scoped allow", tc.path)
			}
		})
	}

	t.Run("fifo does not block", func(t *testing.T) {
		fifo := filepath.Join(dir, "body.fifo")
		if err := syscall.Mkfifo(fifo, 0o600); err != nil {
			t.Fatal(err)
		}
		done := make(chan string, 1)
		go func() { done <- readBodyFile(fifo, dir) }()

		timer := time.NewTimer(2 * time.Second)
		defer timer.Stop()
		select {
		case got := <-done:
			if !strings.HasPrefix(got, "（读不到 ") {
				t.Fatalf("readBodyFile(FIFO) returned usable content %q", got)
			}
		case <-timer.C:
			// A blocking reader is released by a nonblocking writer. Close it before
			// failing so the old implementation cannot leave the goroutine stuck.
			fd, err := syscall.Open(fifo, syscall.O_WRONLY|syscall.O_NONBLOCK, 0)
			if err != nil {
				t.Fatalf("readBodyFile(FIFO) blocked; nonblocking unblock failed: %v", err)
			}
			if err := syscall.Close(fd); err != nil {
				t.Fatalf("close FIFO writer after readBodyFile blocked: %v", err)
			}
			t.Fatal("readBodyFile(FIFO) did not return within two seconds")
		}
	})
}

func TestLarkFiltersRejectWritingAndExecutionFlags(t *testing.T) {
	for _, tc := range []struct {
		command string
		want    bool
	}{
		{command: "sort -o output", want: false},
		{command: "sort -ooutput", want: false},
		{command: "sort -uooutput", want: false},
		{command: "sort --compress-program=helper", want: false},
		{command: "sort --compress-program helper", want: false},
		{command: "date -s tomorrow", want: false},
		{command: "date -stomorrow", want: false},
		{command: "date --set tomorrow", want: false},
		{command: "date --set=tomorrow", want: false},
		{command: "date -I -s tomorrow", want: false},
		{command: "sort -u", want: true},
		{command: "sort -n", want: true},
		{command: "sort -to", want: true},
		{command: "sort -k2o", want: true},
		{command: "sort -Tfoo", want: true},
		{command: "sort -T foo", want: true},
		{command: "sort -T -o", want: true},
		{command: "sort -S10M", want: true},
		{command: "date -u", want: true},
		{command: "date -Iseconds", want: true},
		{command: "date +%Y", want: true},
	} {
		t.Run(tc.command, func(t *testing.T) {
			if got := larkFilterSubstitution(tc.command); got != tc.want {
				t.Fatalf("larkFilterSubstitution(%q)=%v, want %v", tc.command, got, tc.want)
			}
		})
	}

	cfg := olCfg()
	if larkLoneGateCall(`ex GET /links/open | sort -o output`, cfg.Gates) {
		t.Error("a lone gated call piped to sort -o was treated as filter-only")
	}

	cli := exampleGate("http://example.invalid")
	cli.Command = "lark-cli"
	verdict := classifyLark(`lark-cli GET /links/open | sort -o output`, []proto.CommandGate{cli}, t.TempDir(), *new(larkRisk))
	if !verdict.other {
		t.Fatal("lark-cli read piped to sort -o remained an ordinary filter classification")
	}
}
