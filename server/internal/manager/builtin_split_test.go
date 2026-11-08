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
	if err := os.WriteFile(unit, []byte(builtinUnit(personad, data)), 0o644); err != nil {
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
		!strings.Contains(out, "the unit runs "+personad+" now, not "+pm) {
		t.Fatalf("opted out:\n%s\n%s", b, out)
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

// On a Mac the agent runs `persona-manager assistant`.
func TestInstallBuiltinLaunchdRunsPersonaManager(t *testing.T) {
	e, f, data := splitManager(t, "darwin", "persona 1.0 abc")
	plist := filepath.Join(e.home, "Library", "LaunchAgents", managerconf.BuiltinLabel+".plist")
	if err := os.MkdirAll(filepath.Dir(plist), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plist, []byte(builtinPlist(filepath.Join(data, "bin", "personad"), data, e.home)), 0o644); err != nil {
		t.Fatal(err)
	}
	f.active = true
	out := installOut(t, e)
	pm := filepath.Join(data, "bin", "persona-manager")
	if b, _ := os.ReadFile(plist); !strings.Contains(string(b), "<string>"+pm+"</string><string>assistant</string>") ||
		!f.called("launchctl bootstrap") || !strings.Contains(out, "the agent runs "+pm+" now") {
		t.Fatalf("%v\n%s\n%s", f.calls, b, out)
	}
}

func TestServiceProgram(t *testing.T) {
	for text, want := range map[string]string{
		builtinUnit(`/home/a "b"/100%/$x/personad`, "/h"):                         `/home/a "b"/100%/$x/personad`,
		builtinPlist("/Users/a&b/.persona/bin/persona-manager", "/h", "/Users/a"): "/Users/a&b/.persona/bin/persona-manager",
		"[Service]\nExecStart=\nExecStart=/opt/personad assistant\n":              "/opt/personad",
		"": "",
	} {
		if got := serviceProgram(text); got != want {
			t.Errorf("serviceProgram = %q, want %q", got, want)
		}
	}
}
