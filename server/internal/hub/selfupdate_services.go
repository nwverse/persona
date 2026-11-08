package hub

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/update"
)

// What runs the programs a self-update swaps beside personad (docs/operations/hub-update.md "Programs swapped
// together"). A release without one of them for this platform leaves that file as it was. A service that runs it would
// go on starting the old build beside the new personad, so the hub does not install such a release (unreplaced). When
// nothing runs it, the update goes ahead and leaves the old file.

// serviceFinder looks for what runs a program: the systemd units and launchd property lists in dirs, and the systemd
// unit this process runs in (unit). home is what %h and ~ stand for. The tests replace it.
type serviceFinder struct {
	dirs []string
	home string
	unit func() string
}

// systemServices looks where the hub's setup, hub-systemd.sh and an administrator put services: the user's systemd
// units and the machine's, or the user's launchd agents and the machine's agents and daemons.
func systemServices() serviceFinder {
	home, _ := os.UserHomeDir()
	f := serviceFinder{home: home, unit: ownUnit}
	switch runtime.GOOS {
	case "linux":
		base := os.Getenv("XDG_CONFIG_HOME")
		if base == "" {
			base = filepath.Join(home, ".config")
		}
		f.dirs = []string{filepath.Join(base, "systemd", "user"), "/etc/systemd/user", "/etc/systemd/system"}
	case "darwin":
		f.dirs = []string{filepath.Join(home, "Library", "LaunchAgents"), "/Library/LaunchAgents", "/Library/LaunchDaemons"}
	}
	return f
}

// ownUnit is the systemd service this process runs in, from its cgroup ("…/personad.service"); "" when it runs in
// none (started by hand, from a login session or a terminal).
func ownUnit() string {
	b, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return ""
	}
	return cgroupUnit(string(b))
}

// cgroupUnit is the systemd service in a /proc/<pid>/cgroup (cgroup v1 or v2), "" when there is none.
func cgroupUnit(cgroup string) string {
	for _, line := range strings.Split(strings.TrimSpace(cgroup), "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 {
			continue
		}
		name := filepath.Base(parts[2])
		if strings.HasSuffix(name, ".service") && !strings.HasPrefix(name, "user@") {
			return name
		}
	}
	return ""
}

// running is what runs the program at path: the systemd unit this process runs in when self is that program, else the
// first unit file or property list whose program it is; ok is false when nothing does.
func (f serviceFinder) running(path, self string) (by string, ok bool) {
	want := resolvedPath(path)
	if self != "" && resolvedPath(self) == want && f.unit != nil {
		if unit := f.unit(); unit != "" {
			return unit, true
		}
	}
	for _, dir := range f.dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			file := filepath.Join(dir, e.Name())
			var programs []string
			switch filepath.Ext(e.Name()) {
			case ".service":
				programs = unitPrograms(file, f.home)
			case ".plist":
				if b, err := os.ReadFile(file); err == nil {
					if _, args, ok := launchdJob(b); ok && len(args) > 0 {
						programs = []string{homePath(strings.TrimSpace(args[0]), f.home)}
					}
				}
			}
			for _, p := range programs {
				if p != "" && resolvedPath(p) == want {
					return file, true
				}
			}
		}
	}
	return "", false
}

// unitPrograms are the programs a systemd unit file starts: the first word of each ExecStart in its [Service], with its
// drop-ins (<unit>.d/*.conf, by name) read after it, where an empty ExecStart= clears the ones before (systemd's
// order; hub-systemd.sh reads it the same way).
func unitPrograms(file, home string) []string {
	dropins, _ := filepath.Glob(file + ".d/*.conf")
	var starts []string
	for _, path := range append([]string{file}, dropins...) {
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		section := ""
		for _, line := range strings.Split(strings.ReplaceAll(string(b), "\\\n", " "), "\n") {
			line = strings.TrimSpace(line)
			switch {
			case strings.HasPrefix(line, "["):
				section = line
			case section != "[Service]":
			case strings.HasPrefix(line, "ExecStart="):
				if v := strings.TrimSpace(strings.TrimPrefix(line, "ExecStart=")); v == "" {
					starts = nil
				} else {
					starts = append(starts, v)
				}
			}
		}
	}
	programs := make([]string, 0, len(starts))
	for _, s := range starts {
		programs = append(programs, execProgram(s, home))
	}
	return programs
}

// execProgram is the first word of an ExecStart value, unquoted and with %h, %% and $$ replaced as systemd reads it.
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
	return homePath(word, home)
}

// homePath is p with a leading ~ standing for home.
func homePath(p, home string) string {
	if home != "" && (p == "~" || strings.HasPrefix(p, "~/")) {
		return filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	return p
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
		if by, ok := services.running(path, self); ok {
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
