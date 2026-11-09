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

// On a Mac the agent runs `personad assistant` even with a persona-manager of its build beside it, and install says
// why the built-in manager and the hub stay on personad for now. An agent that ran persona-manager (edited by hand) is
// moved back and says so, loaded or not.
func TestInstallBuiltinLaunchdStaysOnPersonad(t *testing.T) {
	e, f, data := splitManager(t, "darwin", "persona 1.0 abc")
	asked := false
	e.version = func(context.Context, string) (string, error) { asked = true; return "persona 1.0 abc", nil }
	personad, pm := filepath.Join(data, "bin", "personad"), filepath.Join(data, "bin", "persona-manager")
	plist := filepath.Join(e.home, "Library", "LaunchAgents", managerconf.BuiltinLabel+".plist")
	if err := os.MkdirAll(filepath.Dir(plist), 0o755); err != nil {
		t.Fatal(err)
	}
	agent, runsPM := builtinPlist(personad, data, e.home), builtinPlist(pm, data, e.home)
	note := "Program: " + personad + " (" + macPersonad + ").\n"
	install := func(text string, loaded bool) string {
		t.Helper()
		if err := os.WriteFile(plist, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		f.active, f.calls = loaded, nil
		out := installOut(t, e)
		if b, _ := os.ReadFile(plist); string(b) != agent {
			t.Fatalf("plist:\n%s\n%s", b, out)
		}
		return out
	}

	if out := install(agent, true); !strings.Contains(out, note) || !strings.Contains(out, "running already, left as it was") || f.called("launchctl bootstrap") {
		t.Fatalf("loaded: %v\n%s", f.calls, out)
	}
	if out := install(agent, false); !strings.Contains(out, note) || !strings.Contains(out, ", started; it runs") || !f.called("launchctl bootstrap") {
		t.Fatalf("not loaded: %v\n%s", f.calls, out)
	}
	for _, loaded := range []bool{true, false} {
		if out := install(runsPM, loaded); !strings.Contains(out, note) || !strings.Contains(out, "started; the agent runs "+personad+" now, not "+pm) ||
			!f.called("launchctl bootstrap") {
			t.Fatalf("ran persona-manager, loaded %v: %v\n%s", loaded, f.calls, out)
		}
	}
	if asked {
		t.Fatal("on a Mac, asked for a build")
	}

	if err := os.Remove(pm); err != nil {
		t.Fatal(err)
	}
	if out := install(agent, true); strings.Contains(out, "macOS") {
		t.Fatalf("no persona-manager: %s", out)
	}
}

// On a Mac whose data directory has no bin/personad, install --builtin run as a persona-manager writes the agent to run
// the personad of its build beside it, and says the Mac stays on personad. With a personad of another build there, or
// none, the agent runs persona-manager: install says so and why, and not that it stays on personad.
func TestInstallBuiltinLaunchdPersonadBesidePersonaManager(t *testing.T) {
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
	runs := func(out, bin string) {
		t.Helper()
		if b, _ := os.ReadFile(plist); string(b) != builtinPlist(bin, data, e.home) {
			t.Fatalf("want the agent to run %s:\n%s\n%s", bin, b, out)
		}
	}

	out := installOut(t, e)
	runs(out, personad)
	if !strings.Contains(out, "Program: "+personad+" ("+macPersonad+").\n") {
		t.Fatal(out)
	}

	e.version = func(_ context.Context, path string) (string, error) {
		if filepath.Base(path) == "personad" {
			return "persona 0.9 old", nil
		}
		return "persona 1.0 abc", nil
	}
	out = installOut(t, e)
	runs(out, pm)
	if !strings.Contains(out, "Program: "+pm+" (") || !strings.Contains(out, personad+" is another build") ||
		!strings.Contains(out, "so the agent runs persona-manager") || strings.Contains(out, macPersonad) {
		t.Fatalf("another build:\n%s", out)
	}

	if err := os.Remove(personad); err != nil {
		t.Fatal(err)
	}
	out = installOut(t, e)
	runs(out, pm)
	if !strings.Contains(out, "has no personad and none is beside it, so the agent runs persona-manager") || strings.Contains(out, macPersonad) {
		t.Fatalf("none:\n%s", out)
	}
}

func TestServiceProgram(t *testing.T) {
	for text, want := range map[string]string{
		builtinUnit(`/home/a "b"/100%/$x/personad`, "/h", false):                  `/home/a "b"/100%/$x/personad`,
		builtinPlist("/Users/a&b/.persona/bin/persona-manager", "/h", "/Users/a"): "/Users/a&b/.persona/bin/persona-manager",
		"[Service]\nExecStart=\nExecStart=/opt/personad assistant\n":              "/opt/personad",
		"": "",
	} {
		if got := serviceProgram(text); got != want {
			t.Errorf("serviceProgram = %q, want %q", got, want)
		}
	}
}
