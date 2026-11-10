package manager

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/compat"
	"github.com/nwverse/persona/server/internal/managerconf"
	"github.com/nwverse/persona/server/internal/update"
)

// splitManager is a bound built-in manager with <data>/bin/personad and persona-manager beside it, persona-manager
// answering `version -v` with build (personad: "persona 1.0 abc").
func splitManager(t *testing.T, goos, build string) (e *env, f *fakeServices, data string) {
	t.Helper()
	home := isolate(t)
	data = filepath.Join(home, ".persona")
	if err := managerconf.WriteBinding(data, managerconf.Binding{Hub: "http://127.0.0.1:1", Token: "t"}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(data, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"personad", "persona-manager"} {
		if err := os.WriteFile(filepath.Join(data, "bin", name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f = &fakeServices{}
	e, _ = f.env(t, goos)
	e.version = func(_ context.Context, path string) (string, error) {
		if filepath.Base(path) == "persona-manager" {
			return build, nil
		}
		return "persona 1.0 abc", nil
	}
	return e, f, data
}

func installOut(t *testing.T, e *env) string {
	t.Helper()
	out := e.stdout.(interface {
		String() string
		Reset()
	})
	out.Reset()
	if err := cmdInstallBuiltin(context.Background(), e, []string{"--builtin"}); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	return out.String()
}

// The same build beside personad: the unit runs `persona-manager assistant`; over a unit that ran personad it is
// restarted and says so. PERSONA_SPLIT_PROGRAMS=0 takes it back to personad.
func TestInstallBuiltinRunsPersonaManager(t *testing.T) {
	e, f, data := splitManager(t, "linux", "persona 1.0 abc")
	personad, pm := filepath.Join(data, "bin", "personad"), filepath.Join(data, "bin", "persona-manager")
	unit := filepath.Join(e.home, ".config", "systemd", "user", managerconf.BuiltinService+".service")
	if err := os.MkdirAll(filepath.Dir(unit), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unit, []byte(builtinUnit(personad, data, false)), 0o644); err != nil {
		t.Fatal(err)
	}
	f.active = true
	out := installOut(t, e)
	if b, _ := os.ReadFile(unit); !strings.Contains(string(b), `ExecStart="`+pm+`" assistant`+"\n") {
		t.Fatalf("unit:\n%s", b)
	}
	if !f.called("systemctl --user restart "+managerconf.BuiltinService) || !strings.Contains(out, "restarted; the unit runs "+pm+" now, not "+personad) {
		t.Fatalf("calls %v\n%s", f.calls, out)
	}
	f.calls = nil
	if out := installOut(t, e); f.called("systemctl --user restart") || strings.Contains(out, "now, not") {
		t.Fatalf("again: %v\n%s", f.calls, out)
	}
	e.getenv = func(k string) string {
		if k == update.SplitEnv {
			return "0"
		}
		return ""
	}
	out = installOut(t, e)
	if b, _ := os.ReadFile(unit); !strings.Contains(string(b), `ExecStart="`+personad+`" assistant`+"\n") ||
		!strings.Contains(string(b), "\nEnvironment=PERSONA_SPLIT_PROGRAMS=0\n") || !strings.Contains(out, "the unit runs "+personad+" now, not "+pm) {
		t.Fatalf("opted out:\n%s\n%s", b, out)
	}
	e.getenv = func(string) string { return "" }
	out = installOut(t, e)
	if b, _ := os.ReadFile(unit); !strings.Contains(string(b), `ExecStart="`+pm+`" assistant`+"\n") ||
		strings.Contains(string(b), "PERSONA_SPLIT_PROGRAMS") || !strings.Contains(out, "the unit runs "+pm+" now, not "+personad) {
		t.Fatalf("without it again:\n%s\n%s", b, out)
	}
}

// Another build beside personad (a half-done upgrade), or one that does not run: personad, and why.
func TestInstallBuiltinKeepsPersonadForAnotherBuild(t *testing.T) {
	e, _, data := splitManager(t, "linux", "persona 0.9 old")
	out := installOut(t, e)
	unit := filepath.Join(e.home, ".config", "systemd", "user", managerconf.BuiltinService+".service")
	if b, _ := os.ReadFile(unit); !strings.Contains(string(b), `/bin/personad" assistant`) || !strings.Contains(out, "persona-manager not used") ||
		!strings.Contains(out, "another build") {
		t.Fatalf("%s\n%s", b, out)
	}
	e.version = func(_ context.Context, path string) (string, error) {
		if filepath.Base(path) == "persona-manager" {
			return "", errors.New("exec format error")
		}
		return "persona 1.0 abc", nil
	}
	if out := installOut(t, e); !strings.Contains(out, filepath.Join(data, "bin", "persona-manager")+" does not run") {
		t.Fatal(out)
	}
}

// A service under its former name keeps personad; persona-manager is not even asked.
func TestInstallBuiltinFormerNameKeepsPersonad(t *testing.T) {
	e, _, data := splitManager(t, "linux", "persona 1.0 abc")
	asked := false
	e.version = func(context.Context, string) (string, error) { asked = true; return "persona 1.0 abc", nil }
	legacy := filepath.Join(e.home, ".config", "systemd", "user", compat.LegacyManagerUnit("assistant"))
	if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte("[Service]\nExecStart="+filepath.Join(data, "bin", "personad")+" assistant\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	installOut(t, e)
	if b, _ := os.ReadFile(legacy); !strings.Contains(string(b), `/bin/personad" assistant`) || asked {
		t.Fatalf("asked %v:\n%s", asked, b)
	}
}

// On a Mac as on Linux: a new agent runs `persona-manager assistant` when it is personad's build, and a rerun leaves
// it. PERSONA_SPLIT_PROGRAMS=0 moves it to personad and stays in the agent; a rerun without it moves it back.
func TestInstallBuiltinLaunchdRunsPersonaManager(t *testing.T) {
	e, f, data := splitManager(t, "darwin", "persona 1.0 abc")
	personad, pm := filepath.Join(data, "bin", "personad"), filepath.Join(data, "bin", "persona-manager")
	plist := filepath.Join(e.home, "Library", "LaunchAgents", managerconf.BuiltinLabel+".plist")
	runs := func(out, want string) {
		t.Helper()
		if b, _ := os.ReadFile(plist); string(b) != want {
			t.Fatalf("plist:\n%s\n%s", b, out)
		}
		if strings.Contains(out, "stays on it") {
			t.Fatalf("said it stays on personad:\n%s", out)
		}
	}

	out := installOut(t, e)
	runs(out, builtinPlist(pm, data, e.home, false))
	if !f.called("launchctl bootstrap") || !strings.Contains(out, ", started; it runs") {
		t.Fatalf("new: %v\n%s", f.calls, out)
	}
	f.active, f.calls = true, nil
	out = installOut(t, e)
	runs(out, builtinPlist(pm, data, e.home, false))
	if f.called("launchctl bootstrap") || !strings.Contains(out, "running already, left as it was") {
		t.Fatalf("again: %v\n%s", f.calls, out)
	}

	e.getenv = func(k string) string {
		if k == update.SplitEnv {
			return "0"
		}
		return ""
	}
	f.calls = nil
	out = installOut(t, e)
	optedOut := builtinPlist(personad, data, e.home, true)
	runs(out, optedOut)
	if !strings.Contains(optedOut, "<key>PERSONA_SPLIT_PROGRAMS</key><string>0</string>") ||
		!strings.Contains(out, "the agent runs "+personad+" now, not "+pm) || !f.called("launchctl bootstrap") {
		t.Fatalf("opted out: %v\n%s", f.calls, out)
	}
	e.getenv = func(string) string { return "" }
	out = installOut(t, e)
	runs(out, builtinPlist(pm, data, e.home, false))
	if !strings.Contains(out, "the agent runs "+pm+" now, not "+personad) {
		t.Fatalf("without it again:\n%s", out)
	}
}

// A Mac's agent that ran personad before (an installed Mac) stays on it, persona-manager beside it or not: install
// says why when one is there, and asks no program for its build. Its agent is left as it was when loaded.
func TestInstallBuiltinLaunchdInstalledMacStaysOnPersonad(t *testing.T) {
	e, f, data := splitManager(t, "darwin", "persona 1.0 abc")
	asked := false
	e.version = func(context.Context, string) (string, error) { asked = true; return "persona 1.0 abc", nil }
	personad, pm := filepath.Join(data, "bin", "personad"), filepath.Join(data, "bin", "persona-manager")
	plist := filepath.Join(e.home, "Library", "LaunchAgents", managerconf.BuiltinLabel+".plist")
	if err := os.MkdirAll(filepath.Dir(plist), 0o755); err != nil {
		t.Fatal(err)
	}
	agent := builtinPlist(personad, data, e.home, false)
	if err := os.WriteFile(plist, []byte(agent), 0o644); err != nil {
		t.Fatal(err)
	}
	note := "Program: " + personad + " (this Mac's agent ran personad before and stays on it"
	for _, loaded := range []bool{true, false} {
		f.active, f.calls = loaded, nil
		out := installOut(t, e)
		if b, _ := os.ReadFile(plist); string(b) != agent || !strings.Contains(out, note) || strings.Contains(out, "now, not") {
			t.Fatalf("loaded %v:\n%s\n%s", loaded, b, out)
		}
		if loaded == f.called("launchctl bootstrap") {
			t.Fatalf("loaded %v: %v", loaded, f.calls)
		}
	}
	if asked {
		t.Fatal("asked for a build")
	}
	if err := os.Remove(pm); err != nil {
		t.Fatal(err)
	}
	if out := installOut(t, e); strings.Contains(out, "stays on it") {
		t.Fatalf("no persona-manager: %s", out)
	}
}

// A persona-manager run from elsewhere, with no personad in the data directory: a new agent runs it, as on Linux. An
// installed Mac's agent that runs a personad there stays on that one.
func TestInstallBuiltinLaunchdLoosePersonaManager(t *testing.T) {
	e, _, data := splitManager(t, "darwin", "persona 1.0 abc")
	if err := os.Remove(filepath.Join(data, "bin", "personad")); err != nil {
		t.Fatal(err)
	}
	loose := t.TempDir()
	personad, pm := filepath.Join(loose, "personad"), filepath.Join(loose, "persona-manager")
	for _, p := range []string{personad, pm} {
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	e.exe = func() (string, error) { return pm, nil }
	plist := filepath.Join(e.home, "Library", "LaunchAgents", managerconf.BuiltinLabel+".plist")
	out := installOut(t, e)
	if b, _ := os.ReadFile(plist); string(b) != builtinPlist(pm, data, e.home, false) || strings.Contains(out, "stays on it") {
		t.Fatalf("new:\n%s\n%s", b, out)
	}
	installed := builtinPlist(personad, data, e.home, false)
	if err := os.WriteFile(plist, []byte(installed), 0o644); err != nil {
		t.Fatal(err)
	}
	out = installOut(t, e)
	if b, _ := os.ReadFile(plist); string(b) != installed || !strings.Contains(out, "Program: "+personad+" (this Mac's agent ran personad before") {
		t.Fatalf("installed:\n%s\n%s", b, out)
	}
}

// An agent under its former name keeps personad on a Mac too; persona-manager is not even asked.
func TestInstallBuiltinLaunchdFormerNameKeepsPersonad(t *testing.T) {
	e, _, data := splitManager(t, "darwin", "persona 1.0 abc")
	asked := false
	e.version = func(context.Context, string) (string, error) { asked = true; return "persona 1.0 abc", nil }
	legacy := filepath.Join(e.home, "Library", "LaunchAgents", compat.LegacyManagerLabel("assistant")+".plist")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte("<plist><dict><key>Label</key><string>x</string></dict></plist>\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	installOut(t, e)
	if b, _ := os.ReadFile(legacy); !strings.Contains(string(b), "<string>"+filepath.Join(data, "bin", "personad")+"</string><string>assistant</string>") || asked {
		t.Fatalf("asked %v:\n%s", asked, b)
	}
}

func TestServiceProgram(t *testing.T) {
	for text, want := range map[string]string{
		builtinUnit(`/home/a "b"/100%/$x/personad`, "/h", false):                        `/home/a "b"/100%/$x/personad`,
		builtinPlist("/Users/a&b/.persona/bin/persona-manager", "/h", "/Users/a", true): "/Users/a&b/.persona/bin/persona-manager",
		"[Service]\nExecStart=\nExecStart=/opt/personad assistant\n":                    "/opt/personad",
		"": "",
	} {
		if got := serviceProgram(text); got != want {
			t.Errorf("serviceProgram = %q, want %q", got, want)
		}
	}
}
