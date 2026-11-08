package hub

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/update"
)

const oldProgram = "#!/bin/sh\necho old\n"

// serviceCase sets up what runs the programs beside target: the files it writes into the hub's service directory
// (name -> content, "%s" standing for target's directory), the unit the hub runs in, and the program it runs as.
type serviceCase struct {
	name     string
	programs []string // the release's
	files    map[string]string
	unit     string
	self     string // beside target; "" is personad
	missing  string // the program a service runs that the release lacks; "" when it installs
}

func (c serviceCase) hub(t *testing.T) (*Hub, string, func() []string) {
	t.Helper()
	h, _, target, execs := updatingHub(t, splitRelease(t, c.programs...), "release")
	h.build.Commit = gitA
	bin := filepath.Dir(target)
	for _, name := range update.SplitPrograms {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(oldProgram), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	for name, content := range c.files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(strings.ReplaceAll(content, "%s", bin)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	self := target
	if c.self != "" {
		self = filepath.Join(bin, c.self)
	}
	h.upd.self = func() (string, error) { return self, nil }
	h.upd.services = serviceFinder{dirs: []string{dir}, home: t.TempDir(), unit: func() string { return c.unit }}
	return h, target, execs
}

const agentRunning = `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
  <key>Label</key><string>com.persona.manager.assistant</string>
  <key>ProgramArguments</key><array><string>%s/persona-manager</string><string>assistant</string></array>
</dict></plist>
`

// A release without persona-hub or persona-manager for this platform is not installed while a service runs that
// program: it would go on starting the old build beside the new personad. The app shows why, the history says it
// once, and installing it by hand is refused for the same reason; nothing is replaced.
func TestUpdateRefusedWhileAServiceRunsAProgramItLacks(t *testing.T) {
	for _, c := range []serviceCase{
		{name: "the hub's unit runs persona-hub", programs: []string{"persona-manager"}, missing: "persona-hub",
			files: map[string]string{"personad.service": "[Unit]\nDescription=hub\n\n[Service]\nExecStart=\"%s/persona-hub\" run\n"}},
		{name: "a drop-in makes the unit run persona-hub", programs: []string{"persona-manager"}, missing: "persona-hub",
			files: map[string]string{
				"personad.service":                          "[Service]\nExecStart=%s/personad hub\n",
				"personad.service.d/90-persona-compat.conf": "[Service]\nExecStart=\nExecStart=%s/persona-hub run\n",
			}},
		{name: "an agent runs persona-manager", programs: []string{"persona-hub"}, missing: "persona-manager",
			files: map[string]string{"com.persona.manager.assistant.plist": agentRunning}},
		{name: "the hub runs as persona-hub in its unit", programs: []string{"persona-manager"}, missing: "persona-hub",
			self: "persona-hub", unit: "personad.service"},
	} {
		t.Run(c.name, func(t *testing.T) {
			h, target, execs := c.hub(t)
			ctx := context.Background()
			if _, err := h.upd.check(ctx); err != nil {
				t.Fatal(err)
			}
			if v := h.upd.view(); v.Latest == nil || !strings.Contains(v.Latest.Held, c.missing) {
				t.Fatalf("latest %+v", v.Latest)
			}
			if h.upd.wantsAuto() {
				t.Fatal("would install it by itself")
			}
			h.upd.noteHeld(ctx)
			h.upd.noteHeld(ctx)
			if hist := h.upd.settings.History; len(hist) != 1 || hist[0].Result != "held" || !strings.Contains(hist[0].Reason, c.missing) {
				t.Fatalf("history %+v", hist)
			}
			if err := h.upd.begin(ctx, true); err == nil || !strings.Contains(err.Error(), c.missing) {
				t.Fatalf("installing it by hand: %v", err)
			}
			h.upd.install(ctx, true) // the hourly check's, had the services changed since it read the release
			if hist := h.upd.settings.History; len(hist) != 2 || hist[1].Result != "failed" || !strings.Contains(hist[1].Reason, c.missing) {
				t.Fatalf("history %+v", hist)
			}
			if b, _ := os.ReadFile(target); string(b) != "#!/bin/sh\necho old\n" || len(execs()) != 0 {
				t.Fatalf("personad replaced or restarted (%v)", execs())
			}
			for _, name := range update.SplitPrograms {
				if b, _ := os.ReadFile(filepath.Join(filepath.Dir(target), name)); string(b) != oldProgram {
					t.Fatalf("%s replaced", name)
				}
			}
		})
	}
}

// When no service runs the program the release lacks, it installs as before: personad and the programs it has are
// replaced, the one it lacks keeps its old file, and the hub goes on as personad.
func TestUpdateLeavesAProgramNoServiceRuns(t *testing.T) {
	for _, c := range []serviceCase{
		{name: "the hub's unit runs personad", programs: []string{"persona-manager"}, unit: "personad.service",
			files: map[string]string{
				"personad.service": "[Service]\nExecStart=\"%s/personad\" hub\n",
				"other.service":    "[Service]\nExecStart=/bin/true\n",
				// persona-hub named outside [Service], and by a unit a drop-in took back to personad
				"notes.service":                    "[Unit]\nExecStart=%s/persona-hub run\n",
				"back.service":                     "[Service]\nExecStart=%s/persona-hub run\n",
				"back.service.d/override.conf":     "[Service]\nExecStart=\nExecStart=%s/personad hub\n",
				"com.persona.hub.plist":            strings.ReplaceAll(agentRunning, "persona-manager", "personad"),
				"persona-hub.service.d/notes.conf": "[Service]\nExecStart=%s/persona-hub run\n",
			}},
		{name: "persona-hub started by hand", programs: []string{"persona-manager"}, self: "persona-hub"},
		{name: "an agent runs persona-manager, the release has it", programs: []string{"persona-manager"},
			files: map[string]string{"com.persona.manager.assistant.plist": agentRunning}},
	} {
		t.Run(c.name, func(t *testing.T) {
			h, target, execs := c.hub(t)
			ctx := context.Background()
			if _, err := h.upd.check(ctx); err != nil {
				t.Fatal(err)
			}
			if v := h.upd.view(); v.Latest == nil || v.Latest.Held != "" || !h.upd.wantsAuto() {
				t.Fatalf("latest %+v, auto %v", v.Latest, h.upd.wantsAuto())
			}
			go h.upd.install(ctx, true)
			waitFor(t, "the restart", func() bool { return len(execs()) == 1 })
			if execs()[0] != target {
				t.Fatalf("restarted into %v, not personad", execs())
			}
			bin := filepath.Dir(target)
			if b, _ := os.ReadFile(filepath.Join(bin, "persona-hub")); string(b) != oldProgram {
				t.Fatal("persona-hub, which the release lacks, was replaced")
			}
			if b, _ := os.ReadFile(filepath.Join(bin, "persona-manager")); string(b) == oldProgram {
				t.Fatal("persona-manager was not replaced")
			}
		})
	}
}

// The program of an ExecStart, read as systemd does and as hub-systemd.sh reads it.
func TestExecProgram(t *testing.T) {
	for value, want := range map[string]string{
		`/opt/p/bin/persona-hub run`:             "/opt/p/bin/persona-hub",
		`"/opt/my p/bin/persona-hub" run`:        "/opt/my p/bin/persona-hub",
		`"/opt/a\"b/persona-hub" run`:            `/opt/a"b/persona-hub`,
		`'/opt/my p/persona-hub' run`:            "/opt/my p/persona-hub",
		`-/opt/p/persona-hub run`:                "/opt/p/persona-hub",
		`@/opt/p/persona-hub persona-hub run`:    "/opt/p/persona-hub",
		`"/opt/50%%/$$x/persona-hub" run`:        "/opt/50%/$x/persona-hub",
		`%h/.persona/bin/persona-hub run`:        "/home/u/.persona/bin/persona-hub",
		`~/.persona/bin/persona-hub run`:         "/home/u/.persona/bin/persona-hub",
		`"%h/.persona/bin/persona-manager" node`: "/home/u/.persona/bin/persona-manager",
	} {
		if got := execProgram(value, "/home/u"); got != want {
			t.Errorf("%s: %q, want %q", value, got, want)
		}
	}
}

// The service the hub runs in, from its cgroup: a hub started by hand (a login session, a terminal) runs in none.
func TestCgroupUnit(t *testing.T) {
	for cgroup, want := range map[string]string{
		"0::/user.slice/user-1000.slice/user@1000.service/app.slice/personad.service\n":         "personad.service",
		"12:pids:/system.slice/persona.service\n1:name=systemd:/system.slice/persona.service\n": "persona.service",
		"0::/user.slice/user-1000.slice/session-3.scope\n":                                      "",
		"0::/user.slice/user-1000.slice/user@1000.service/app.slice/vte-spawn-1.scope\n":        "",
		"0::/user.slice/user-1000.slice/user@1000.service\n":                                    "",
		"0::/\n": "",
	} {
		if got := cgroupUnit(cgroup); got != want {
			t.Errorf("%q: %q, want %q", cgroup, got, want)
		}
	}
}
