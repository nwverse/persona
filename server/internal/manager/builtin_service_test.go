package manager

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistant"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/bus"
	"github.com/nwverse/persona/server/internal/managerconf"
	"github.com/nwverse/persona/server/internal/update"
)

// The names the hub's setup prints are the ones install --builtin uses.
func TestBuiltinServiceNames(t *testing.T) {
	if unitName(assistantwire.AssistantID) != managerconf.BuiltinService+".service" || launchdLabel(assistantwire.AssistantID) != managerconf.BuiltinLabel {
		t.Fatalf("%s %s", unitName(assistantwire.AssistantID), launchdLabel(assistantwire.AssistantID))
	}
	unit := builtinUnit("/home/someone/.persona/bin/personad", "/home/someone/.persona")
	for _, line := range []string{`ExecStart="/home/someone/.persona/bin/personad" assistant`, `Environment="PERSONA_ASSISTANT_DEPLOY=service"`,
		`Environment="PERSONA_HOME=/home/someone/.persona"`, "Restart=always", "RestartPreventExitStatus=2", "WantedBy=default.target"} {
		if !strings.Contains(unit, line+"\n") {
			t.Errorf("unit has no %q:\n%s", line, unit)
		}
	}
	plist := builtinPlist("/Users/someone/.persona/bin/personad", "/Users/someone/.persona", "/Users/someone")
	for _, part := range []string{"<string>" + managerconf.BuiltinLabel + "</string>",
		"<string>/Users/someone/.persona/bin/personad</string><string>assistant</string>",
		"<key>PERSONA_ASSISTANT_DEPLOY</key><string>service</string>", "<key>PERSONA_HOME</key><string>/Users/someone/.persona</string>",
		"<string>/Users/someone/.persona/managers/assistant/manager.log</string>"} {
		if !strings.Contains(plist, part) {
			t.Errorf("plist has no %q:\n%s", part, plist)
		}
	}
}

// joinCounter is builtinHub counting the trades it was asked for.
func joinCounter(t *testing.T) (*httptest.Server, func() int) {
	t.Helper()
	var mu sync.Mutex
	n := 0
	joins := builtinJoins()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		n++
		mu.Unlock()
		joins.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, func() int { mu.Lock(); defer mu.Unlock(); return n }
}

type fakeServices struct {
	calls  []string
	active bool
}

func (f *fakeServices) env(t *testing.T, goos string) (*env, *bytes.Buffer) {
	t.Helper()
	var out bytes.Buffer
	home, _ := os.UserHomeDir()
	return &env{stdout: &out, goos: goos, home: home, uid: 501, exe: func() (string, error) { return "/opt/personad", nil },
		look:   func(name string) (string, error) { return "/usr/bin/" + name, nil },
		getenv: func(string) string { return "" },
		run: func(_ context.Context, name string, args ...string) (string, error) {
			call := name + " " + strings.Join(args, " ")
			f.calls = append(f.calls, call)
			if (strings.Contains(call, "is-active") || strings.HasPrefix(call, "launchctl print")) && !f.active {
				return "inactive\n", errors.New("exit status 3")
			}
			return "", nil
		}}, &out
}

func (f *fakeServices) called(prefix string) bool {
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

// install --builtin: not bound and no code, nothing is installed (exit 2). With --join and a code it binds, writes the
// unit and starts it. Again with a code while the hub takes its token: the binding stays (the code is not traded) and
// the running service is left alone. When the hub no longer takes it, the code binds it again and the service
// restarts on the new binding.
func TestInstallBuiltinSystemd(t *testing.T) {
	home := isolate(t)
	data := filepath.Join(home, ".persona")
	srv, trades := joinCounter(t)
	defer func(f func(context.Context, *assistant.ManagerConfig) error) { probeToken = f }(probeToken)
	taken := true
	probeToken = func(_ context.Context, c *assistant.ManagerConfig) error {
		if c.Hub != srv.URL || c.Token != "jb_builtin" {
			t.Errorf("probed %+v", c)
		}
		if taken {
			return nil
		}
		return bus.ErrRevoked
	}
	f := &fakeServices{}
	e, out := f.env(t, "linux")
	unit := filepath.Join(home, ".config", "systemd", "user", "persona-manager-assistant.service")

	if err := cmdInstallBuiltin(context.Background(), e, []string{"--builtin"}); !errors.As(err, new(ConfigError)) || fileExists(unit) {
		t.Fatalf("not bound: %v, unit there: %v", err, fileExists(unit))
	}
	if err := cmdInstallBuiltin(context.Background(), e, []string{"--builtin", "--join", srv.URL}); !errors.As(err, new(ConfigError)) ||
		!strings.Contains(err.Error(), managerconf.BuiltinCodeEnv) || trades() != 0 {
		t.Fatalf("no code: %v", err)
	}

	e.getenv = func(k string) string {
		if k == managerconf.BuiltinCodeEnv {
			return "mi_builtin"
		}
		return ""
	}
	if err := cmdInstallBuiltin(context.Background(), e, []string{"--builtin", "--join", srv.URL}); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if b, err := managerconf.ReadBinding(data); err != nil || b.Token != "jb_builtin" || b.Hub != srv.URL || trades() != 1 {
		t.Fatalf("binding %+v %v, trades %d", b, err, trades())
	}
	text, err := os.ReadFile(unit)
	if err != nil || !strings.Contains(string(text), `ExecStart="/opt/personad" assistant`) || !strings.Contains(string(text), `PERSONA_HOME=`+data+`"`) {
		t.Fatalf("unit %s %v", text, err)
	}
	for _, c := range []string{"systemctl --user daemon-reload", "systemctl --user enable persona-manager-assistant.service", "systemctl --user restart persona-manager-assistant.service"} {
		if !f.called(c) {
			t.Fatalf("no %q in %v", c, f.calls)
		}
	}

	// Again: the hub takes its token, the service runs: nothing traded, nothing restarted.
	f.calls, f.active = nil, true
	out.Reset()
	if err := cmdInstallBuiltin(context.Background(), e, []string{"--builtin", "--join", srv.URL}); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if trades() != 1 || f.called("systemctl --user restart") || !strings.Contains(out.String(), "Binding: kept") {
		t.Fatalf("trades %d, calls %v\n%s", trades(), f.calls, out)
	}

	// The hub refuses its token now (bound again elsewhere, a hub back in child mode): the code binds it again.
	taken = false
	f.calls = nil
	if err := cmdInstallBuiltin(context.Background(), e, []string{"--builtin", "--join", srv.URL}); err != nil {
		t.Fatal(err)
	}
	if trades() != 2 || !f.called("systemctl --user restart persona-manager-assistant.service") {
		t.Fatalf("trades %d, calls %v", trades(), f.calls)
	}

	// status, then uninstall: the unit goes, the binding stays.
	out.Reset()
	if err := cmdStatus(context.Background(), e, nil); err != nil || !strings.Contains(out.String(), "Built-in manager (assistant)") ||
		!strings.Contains(out.String(), "Binding: hub "+srv.URL) {
		t.Fatalf("status %v\n%s", err, out)
	}
	if err := Main(context.Background(), update.Build{}, []string{"uninstall", "--builtin", "extra"}, out); !errors.Is(err, errUsage) {
		t.Fatalf("uninstall with more: %v", err)
	}
	if err := cmdUninstallBuiltin(context.Background(), e, []string{"--builtin"}); err != nil || fileExists(unit) ||
		!f.called("systemctl --user disable --now persona-manager-assistant.service") {
		t.Fatalf("uninstall %v %v", err, f.calls)
	}
	if _, err := managerconf.ReadBinding(data); err != nil {
		t.Fatalf("binding after uninstall: %v", err)
	}
}

// What stops the install stops it before the code is spent: no systemd user instance, another system.
func TestInstallBuiltinChecksFirst(t *testing.T) {
	isolate(t)
	srv, trades := joinCounter(t)
	for _, c := range []struct {
		goos      string
		noSystemd bool
	}{{"linux", true}, {"windows", false}} {
		f := &fakeServices{}
		e, _ := f.env(t, c.goos)
		if c.noSystemd {
			e.look = func(string) (string, error) { return "", os.ErrNotExist }
		}
		j := builtinJoin{hub: srv.URL, code: "mi_builtin", always: true}
		if err := e.installBuiltin(context.Background(), dataDir(), j); err == nil || trades() != 0 {
			t.Fatalf("%s: %v, trades %d", c.goos, err, trades())
		}
	}
}

// join --builtin --install binds and installs; the code is always traded, and a service on the old binding restarts.
func TestJoinBuiltinInstall(t *testing.T) {
	home := isolate(t)
	srv, trades := joinCounter(t)
	f := &fakeServices{active: true}
	e, out := f.env(t, "darwin")
	if err := cmdJoin(context.Background(), e, []string{"--builtin", srv.URL, "mi_builtin", "--install"}); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	plist := filepath.Join(home, "Library", "LaunchAgents", managerconf.BuiltinLabel+".plist")
	if trades() != 1 || !fileExists(plist) || !f.called("launchctl bootstrap gui/501 "+plist) {
		t.Fatalf("trades %d, calls %v", trades(), f.calls)
	}
	// The same again: traded again (join always trades), the agent unchanged and loaded, so kickstarted.
	f.calls = nil
	if err := cmdJoin(context.Background(), e, []string{"--builtin", srv.URL, "mi_builtin", "--install"}); err != nil {
		t.Fatal(err)
	}
	if trades() != 2 || !f.called("launchctl kickstart -k gui/501/"+managerconf.BuiltinLabel) || f.called("launchctl bootstrap") {
		t.Fatalf("trades %d, calls %v", trades(), f.calls)
	}
}

// The built-in manager's service starts again in place when the file it runs from is replaced (an update), and says
// so; when that cannot be done in place, it stops so that its service starts it again.
func TestBuiltinRestartsOnANewBinary(t *testing.T) {
	defer func(c time.Duration, r func([]string) error, b func() (string, os.FileInfo, bool), a func(context.Context, update.Build, io.Reader) error) {
		binaryCheck, restart, runningBinary, runAssistant = c, r, b, a
	}(binaryCheck, restart, runningBinary, runAssistant)
	binaryCheck = 10 * time.Millisecond
	// bound, and running from a file of its own
	prepare := func(t *testing.T) (replace func()) {
		home := isolate(t)
		if err := managerconf.WriteBinding(filepath.Join(home, ".persona"), managerconf.Binding{Hub: "http://127.0.0.1:1", Token: "t"}); err != nil {
			t.Fatal(err)
		}
		bin := filepath.Join(home, "personad")
		if err := os.WriteFile(bin, []byte("old"), 0o755); err != nil {
			t.Fatal(err)
		}
		runningBinary = func() (string, os.FileInfo, bool) {
			fi, err := os.Stat(bin)
			return bin, fi, err == nil
		}
		return func() { // as update.Swap does: a new file renamed over it
			if err := os.WriteFile(bin+".new", []byte("new build"), 0o755); err != nil {
				t.Error(err)
			}
			if err := os.Rename(bin+".new", bin); err != nil {
				t.Error(err)
			}
		}
	}

	t.Run("in place", func(t *testing.T) {
		replace := prepare(t)
		restart = nil // tests: run again in this process
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		runs := 0
		runAssistant = func(run context.Context, _ update.Build, _ io.Reader) error {
			runs++
			if runs == 1 {
				replace()
				select {
				case <-run.Done():
					return run.Err()
				case <-time.After(5 * time.Second):
					return errors.New("not stopped for the new binary")
				}
			}
			cancel()
			return nil
		}
		var out strings.Builder
		if err := RunBuiltin(ctx, update.Build{}, &out); err != nil || runs != 2 || !strings.Contains(out.String(), "was replaced") {
			t.Fatalf("err %v, runs %d\n%s", err, runs, out.String())
		}
	})
	t.Run("not in place", func(t *testing.T) {
		replace := prepare(t)
		restart = func([]string) error { return errors.New("no exec") }
		runAssistant = func(run context.Context, _ update.Build, _ io.Reader) error {
			replace()
			<-run.Done()
			return run.Err()
		}
		if err := RunBuiltin(context.Background(), update.Build{}, &strings.Builder{}); err == nil || !strings.Contains(err.Error(), "no exec") {
			t.Fatalf("err %v", err)
		}
	})
	t.Run("the same file", func(t *testing.T) {
		prepare(t)
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		runAssistant = func(run context.Context, _ update.Build, _ io.Reader) error {
			<-run.Done()
			return nil
		}
		var out strings.Builder
		if err := RunBuiltin(ctx, update.Build{}, &out); err != nil || strings.Contains(out.String(), "replaced") {
			t.Fatalf("err %v\n%s", err, out.String())
		}
	})
}
