package hub

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/update"
)

// What runs the programs a self-update swaps beside personad (docs/operations/hub-update.md "Programs swapped
// together"). A release without one of them for this platform leaves that file as it was. A service that runs it would
// go on starting the old build beside the new personad, so the hub does not install such a release (unreplaced). When
// nothing runs it, the update goes ahead and leaves the old file.

// serviceFinder looks for what runs a program: the systemd units of each scope and the launchd property lists in
// plists, and the systemd unit this process runs in (unit). home is what %h stands for. state says whether systemd
// runs a unit (enabled, or active now); known is false when systemd cannot be asked, and the unit then counts. The
// tests replace it.
type serviceFinder struct {
	scopes []unitScope
	plists []string
	home   string
	unit   func() (name string, user bool)
	state  func(user bool, name string) (on, known bool)
}

// unitScope is the directories one systemd manager (the user's or the machine's) loads units from, the one whose unit
// wins over a unit of the same name first.
type unitScope struct {
	user bool
	dirs []string
}

// systemServices looks where the hub's setup, hub-systemd.sh, packages and an administrator put services: the user's
// systemd units and the machine's, or the user's launchd agents and the machine's agents and daemons.
func systemServices() serviceFinder {
	home, _ := os.UserHomeDir()
	f := serviceFinder{home: home, unit: ownUnit, state: systemdState}
	switch runtime.GOOS {
	case "linux":
		config, data := os.Getenv("XDG_CONFIG_HOME"), os.Getenv("XDG_DATA_HOME")
		if config == "" {
			config = filepath.Join(home, ".config")
		}
		if data == "" {
			data = filepath.Join(home, ".local", "share")
		}
		f.scopes = []unitScope{
			{user: true, dirs: []string{filepath.Join(config, "systemd", "user"), "/etc/systemd/user", filepath.Join(data, "systemd", "user"), "/usr/lib/systemd/user"}},
			{dirs: []string{"/etc/systemd/system", "/usr/lib/systemd/system"}},
		}
	case "darwin":
		f.plists = []string{filepath.Join(home, "Library", "LaunchAgents"), "/Library/LaunchAgents", "/Library/LaunchDaemons"}
	}
	return f
}

// ownUnit is the systemd service this process runs in, from its cgroup ("…/personad.service"); "" when it runs in
// none (started by hand, from a login session or a terminal).
func ownUnit() (string, bool) {
	b, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return "", false
	}
	return cgroupUnit(string(b))
}

// cgroupUnit is the systemd service in a /proc/<pid>/cgroup (cgroup v1 or v2), "" when there is none; user says it is
// one of the user's own units (under user@<uid>.service).
func cgroupUnit(cgroup string) (name string, user bool) {
	for _, line := range strings.Split(strings.TrimSpace(cgroup), "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 {
			continue
		}
		name := filepath.Base(parts[2])
		if strings.HasSuffix(name, ".service") && !strings.HasPrefix(name, "user@") {
			return name, strings.Contains(parts[2], "/user@")
		}
	}
	return "", false
}

// systemdState asks systemd whether it runs a unit: on when the unit is enabled (or static, generated…: started without
// being asked for) or active now; known is false when systemctl does not answer (no user manager to ask, say).
func systemdState(user bool, name string) (on, known bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	args := []string{"show", "-p", "UnitFileState", "-p", "ActiveState", "--", name}
	if user {
		args = append([]string{"--user"}, args...)
	}
	out, err := exec.CommandContext(ctx, "systemctl", args...).Output()
	if err != nil {
		return false, false
	}
	return unitOn(string(out))
}

// unitOn reads `systemctl show -p UnitFileState -p ActiveState`.
func unitOn(show string) (on, known bool) {
	props := map[string]string{}
	for _, line := range strings.Split(show, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			props[k] = v
		}
	}
	file, okFile := props["UnitFileState"]
	active, okActive := props["ActiveState"]
	if !okFile && !okActive {
		return false, false
	}
	switch file {
	case "enabled", "enabled-runtime", "static", "alias", "indirect", "generated", "transient":
		return true, true
	}
	switch active {
	case "active", "activating", "reloading", "refreshing", "deactivating":
		return true, true
	}
	return false, true
}

// unitFile is a service as systemd loads it: the file from the first directory of its scope that has one by its name,
// then its drop-ins (<name>.d/*.conf from every directory of the scope; of two with the same file name, the first
// directory's) in file-name order.
type unitFile struct {
	path    string
	dropins []string
}

// units are the scope's services by name.
func (s unitScope) units() map[string]unitFile {
	units := map[string]unitFile{}
	for _, dir := range s.dirs {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if name := e.Name(); strings.HasSuffix(name, ".service") && !e.IsDir() {
				if _, ok := units[name]; !ok {
					units[name] = unitFile{path: filepath.Join(dir, name)}
				}
			}
		}
	}
	for name, u := range units {
		byName := map[string]string{}
		for _, dir := range s.dirs {
			entries, _ := os.ReadDir(filepath.Join(dir, name+".d"))
			for _, e := range entries {
				if _, ok := byName[e.Name()]; !ok && strings.HasSuffix(e.Name(), ".conf") {
					byName[e.Name()] = filepath.Join(dir, name+".d", e.Name())
				}
			}
		}
		for _, conf := range sortedKeys(byName) {
			u.dropins = append(u.dropins, byName[conf])
		}
		units[name] = u
	}
	return units
}

// runs says whether the unit's program is one of programs (resolved paths).
func (u unitFile) runs(home string, programs ...string) bool {
	for _, p := range unitPrograms(u.path, u.dropins, home) {
		for _, want := range programs {
			if resolvedPath(p) == want {
				return true
			}
		}
	}
	return false
}

// running is what runs the program at path:
//   - a systemd unit whose program it is, one systemd runs (counts);
//   - a launchd job whose program it is, unless it says Disabled;
//   - the unit this process runs in, when this process is that program and the unit's own program is it or target (a
//     hub switched to it inside a unit that runs personad). In any other unit (a terminal multiplexer's, systemd-run's)
//     it was started by hand.
//
// ok is false when nothing does.
func (f serviceFinder) running(path, self, target string) (by string, ok bool) {
	want := resolvedPath(path)
	units := make([]map[string]unitFile, len(f.scopes))
	for i, s := range f.scopes {
		units[i] = s.units()
	}
	if self != "" && resolvedPath(self) == want && f.unit != nil {
		if name, user := f.unit(); name != "" {
			for i, s := range f.scopes {
				if u, ok := units[i][name]; ok && s.user == user && u.runs(f.home, want, resolvedPath(target)) {
					return u.path, true
				}
			}
		}
	}
	for i, s := range f.scopes {
		for _, name := range sortedKeys(units[i]) {
			if u := units[i][name]; u.runs(f.home, want) && f.counts(s.user, name) {
				return u.path, true
			}
		}
	}
	for _, dir := range f.plists {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if filepath.Ext(e.Name()) != ".plist" {
				continue
			}
			file := filepath.Join(dir, e.Name())
			if job, ok := readLaunchdJob(file); ok && !job.disabled && len(job.args) > 0 &&
				resolvedPath(homePath(strings.TrimSpace(job.args[0]), f.home)) == want {
				return file, true
			}
		}
	}
	return "", false
}

// counts says whether systemd runs a unit, or cannot say.
func (f serviceFinder) counts(user bool, name string) bool {
	if f.state == nil {
		return true
	}
	on, known := f.state(user, name)
	return on || !known
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// unitPrograms are the programs a systemd unit starts: the first word of each ExecStart in its [Service] (key and value
// trimmed), the drop-ins read after the unit file, where an empty ExecStart= clears the ones before (systemd's order).
// hub-systemd.sh reads a unit the same way (unit_program; scripts/test/test_hub_switch_deploy.py runs it over
// testdata/exec_start.json, as TestExecProgram does here).
func unitPrograms(file string, dropins []string, home string) []string {
	var starts []string
	for _, path := range append([]string{file}, dropins...) {
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		section := ""
		for _, line := range strings.Split(strings.ReplaceAll(string(b), "\\\n", " "), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "[") {
				section = line
				continue
			}
			key, value, ok := strings.Cut(line, "=")
			if !ok || section != "[Service]" || strings.TrimSpace(key) != "ExecStart" {
				continue
			}
			if value = strings.TrimSpace(value); value == "" {
				starts = nil
			} else {
				starts = append(starts, value)
			}
		}
	}
	programs := make([]string, 0, len(starts))
	for _, s := range starts {
		if p := execProgram(s, home); p != "" {
			programs = append(programs, p)
		}
	}
	return programs
}

// execProgram is the first word of an ExecStart value, unquoted, with the specifiers systemd replaces in a path (%h,
// %%, $$); "" when that is not an absolute path (systemd looks a bare name up in PATH, and does not expand ~).
func execProgram(value, home string) string {
	value = strings.TrimLeft(value, "-@:+!")
	var word string
	if q := value[:min(len(value), 1)]; q == `"` || q == "'" {
		var b strings.Builder
		for i := 1; i < len(value) && value[i] != q[0]; i++ {
			if value[i] == '\\' && i+1 < len(value) {
				i++
			}
			b.WriteByte(value[i])
		}
		word = b.String()
	} else if fields := strings.Fields(value); len(fields) > 0 {
		word = fields[0]
	}
	word = strings.NewReplacer("%%", "%", "%h", home, "$$", "$").Replace(word)
	if !strings.HasPrefix(word, "/") {
		return ""
	}
	return word
}

// homePath is p with a leading ~ standing for home (a launchd job's program, as hub-systemd.sh reads it).
func homePath(p, home string) string {
	if home != "" && (p == "~" || strings.HasPrefix(p, "~/")) {
		return filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	return p
}

// readLaunchdJob reads a launchd property list file: XML, or binary turned into XML by plutil (on a Mac).
func readLaunchdJob(file string) (plistJob, bool) {
	b, err := os.ReadFile(file)
	if err != nil {
		return plistJob{}, false
	}
	if bytes.HasPrefix(b, []byte("bplist")) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if b, err = exec.CommandContext(ctx, "plutil", "-convert", "xml1", "-o", "-", file).Output(); err != nil {
			return plistJob{}, false
		}
	}
	return launchdJob(b)
}

// unreplaced is why release m must not be installed over target, or nil: a split program (update.SplitPrograms) is
// beside target, m has no build of it for this platform, and a service runs it (serviceFinder.running). self is the
// program this hub runs as.
func unreplaced(m *update.Manifest, target, self string, services serviceFinder) error {
	plat := runtime.GOOS + "-" + runtime.GOARCH
	for _, name := range update.SplitPrograms {
		if _, ok := m.Programs[name][plat]; ok {
			continue
		}
		path := filepath.Join(filepath.Dir(target), name)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		if by, ok := services.running(path, self, target); ok {
			return i18n.Err("hub-selfupdate.err_program_unreplaced", i18n.Args{"version": m.Version, "program": name,
				"platform": runtime.GOOS + "/" + runtime.GOARCH, "service": by})
		}
	}
	return nil
}

// unreplaced is unreplaced for this hub, nil for a release that is not newer (not installed anyway) and when the hub
// does not know where it runs from (the update fails on that itself).
func (u *updater) unreplaced(m *update.Manifest) error {
	if !u.h.build.Newer(m.Build) {
		return nil
	}
	target, err := u.target()
	if err != nil {
		return nil
	}
	self, _ := u.self()
	return unreplaced(m, target, self, u.services)
}
