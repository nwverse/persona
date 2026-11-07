package manager

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"html"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/assistant"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/managerconf"
)

// The built-in manager's own service (docs/assistant/managers.md "The built-in manager as its own service"): on Linux
// the systemd user unit persona-manager-assistant.service, on a Mac the launchd agent com.persona.manager.assistant
// (managerconf.BuiltinService, BuiltinLabel). Each runs `<binary> assistant` with PERSONA_ASSISTANT_DEPLOY=service on
// the data directory it shares with the hub. `install --builtin` binds it when it has to and installs the service
// (`personad hub setup` runs it with --join), `uninstall --builtin` takes the service away and leaves the rest.

// builtinUnit is the built-in manager's systemd user unit: bin's `assistant` on the data directory home. Exit status 2
// is a configuration mistake (not bound, another one running): restarting cannot fix it.
func builtinUnit(bin, home string) string {
	return `# Written by personad manager install --builtin (personad hub setup runs it); personad manager uninstall
# --builtin removes it (docs/assistant/managers.md "The built-in manager as its own service").
[Unit]
Description=Persona built-in manager
After=network-online.target

[Service]
ExecStart=` + systemdQuote(bin) + ` assistant
Environment=` + systemdEnvQuote("PERSONA_ASSISTANT_DEPLOY="+assistant.DeployService) + `
Environment=` + systemdEnvQuote("PERSONA_HOME="+home) + `
Environment=PATH=%h/.local/bin:%h/bin:/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin
Restart=always
RestartSec=5
RestartPreventExitStatus=2
TimeoutStopSec=20

[Install]
WantedBy=default.target
`
}

// builtinPlist is the built-in manager's launchd agent, with the hub's agent's PATH; its log is manager.log in its
// own directory.
func builtinPlist(bin, home, userHome string) string {
	x := html.EscapeString
	path := strings.Join([]string{filepath.Join(userHome, ".local", "bin"), filepath.Join(userHome, "bin"),
		filepath.Join(userHome, ".orbstack", "bin"), "/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/bin", "/usr/sbin", "/sbin"}, ":")
	log := filepath.Join(managerconf.DefaultDir(home), "manager.log")
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>` + managerconf.BuiltinLabel + `</string>
  <key>ProgramArguments</key><array><string>` + x(bin) + `</string><string>assistant</string></array>
  <key>EnvironmentVariables</key><dict>
    <key>PERSONA_ASSISTANT_DEPLOY</key><string>` + assistant.DeployService + `</string>
    <key>PERSONA_HOME</key><string>` + x(home) + `</string>
    <key>PATH</key><string>` + x(path) + `</string>
  </dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>ThrottleInterval</key><integer>5</integer>
  <key>StandardOutPath</key><string>` + x(log) + `</string>
  <key>StandardErrorPath</key><string>` + x(log) + `</string>
</dict></plist>
`
}

// bootstrapTries and bootstrapWait: how often, and how far apart, launchctl bootstrap is tried after a bootout.
var (
	bootstrapTries = 5
	bootstrapWait  = time.Second
)

// isBuiltin: the command's arguments ask for the built-in manager (--builtin).
func isBuiltin(args []string) bool {
	for _, a := range args {
		if a == "--builtin" || a == "-builtin" {
			return true
		}
	}
	return false
}

// builtinJoin is how install --builtin may bind the built-in manager: the hub's address and the code (from
// managerconf.BuiltinCodeEnv), with the pin or the plaintext allowance a join takes. always: trade the code even when
// the binding works (join --builtin --install).
type builtinJoin struct {
	hub, code, fingerprint string
	plaintext, always      bool
}

// installBuiltinArgs are install --builtin's arguments: `[--join <hub address>] [--fingerprint F] [--allow-plaintext]`,
// or `--check` alone.
type installBuiltinArgs struct {
	join      builtinJoin
	check     bool
	codeTaken bool // the code came from managerconf.BuiltinCodeEnv
}

// parseInstallBuiltin parses `install --builtin …` (managerconf.BuiltinInstallArgs and BuiltinCheckArgs are what the
// hub's setup runs). The code comes from managerconf.BuiltinCodeEnv, read with getenv.
func parseInstallBuiltin(args []string, getenv func(string) string, out io.Writer) (installBuiltinArgs, error) {
	var a installBuiltinArgs
	fs := flag.NewFlagSet("manager install --builtin", flag.ContinueOnError)
	fs.SetOutput(out)
	fs.Bool("builtin", true, "the built-in manager")
	join := fs.String("join", "", "the hub's address: bind it first with the code in "+managerconf.BuiltinCodeEnv+", unless its binding works")
	fp := fs.String("fingerprint", "", "the hub's TLS fingerprint (an https address with the hub's own certificate)")
	plain := fs.Bool("allow-plaintext", false, "allow a plaintext http hub address on the public internet")
	check := fs.Bool("check", false, "only say whether its service can be installed here (exit 0), changing nothing")
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return a, err
	}
	if len(pos) != 0 || (*check && (*join != "" || *fp != "" || *plain)) {
		return a, errUsage
	}
	a.check = *check
	a.join = builtinJoin{hub: *join, fingerprint: *fp, plaintext: *plain}
	if a.join.hub != "" {
		a.join.code = strings.TrimSpace(getenv(managerconf.BuiltinCodeEnv))
		a.codeTaken = a.join.code != ""
	}
	return a, nil
}

// cmdInstallBuiltin is `install --builtin [--join <hub address>] [--fingerprint F] [--allow-plaintext]` or
// `install --builtin --check`.
func cmdInstallBuiltin(ctx context.Context, e *env, args []string) error {
	a, err := parseInstallBuiltin(args, e.getenv, e.stdout)
	if err != nil {
		return err
	}
	if a.codeTaken {
		_ = os.Unsetenv(managerconf.BuiltinCodeEnv) // not handed on to systemctl / launchctl
	}
	if a.check {
		if _, err := e.builtinPreflight(ctx); err != nil {
			return err
		}
		fmt.Fprintln(e.stdout, "The built-in manager's service can be installed here.")
		return nil
	}
	return e.installBuiltin(ctx, dataDir(), a.join)
}

// builtinPreflight is what can stop installing the built-in manager's service, checked before a code is spent; it
// returns the binary the service will run.
func (e *env) builtinPreflight(ctx context.Context) (string, error) {
	if e.goos != "linux" && e.goos != "darwin" {
		return "", fmt.Errorf("no service for it on %s: run it yourself with PERSONA_ASSISTANT_DEPLOY=%s %s assistant",
			e.goos, assistant.DeployService, e.self())
	}
	if err := e.checkCompatibleService(assistantwire.AssistantID); err != nil {
		return "", err
	}
	if e.goos == "linux" {
		if why := e.noSystemd(ctx); why != "" {
			return "", fmt.Errorf("cannot install its service (%s): run it yourself with PERSONA_ASSISTANT_DEPLOY=%s %s assistant",
				why, assistant.DeployService, e.self())
		}
	}
	return e.binary()
}

// installBuiltin installs the built-in manager's service on the data directory home and starts it, binding it first
// when j says how and its binding does not work (or j.always). What can stop the install is checked before a code is
// spent; a service that runs on the binding before a new one is restarted.
func (e *env) installBuiltin(ctx context.Context, home string, j builtinJoin) error {
	bin, err := e.builtinPreflight(ctx)
	if err != nil {
		return err
	}
	b, traded, err := e.builtinBinding(ctx, home, j)
	if err != nil {
		return err
	}
	if traded {
		fmt.Fprintf(e.stdout, "Binding: bound to the hub at %s (in %s).\n", b.Hub, managerconf.DefaultFile(home))
	} else {
		fmt.Fprintf(e.stdout, "Binding: kept, to the hub at %s.\n", b.Hub)
	}
	return e.builtinService(ctx, home, bin, traded)
}

// builtinBinding is the built-in manager's binding: the one in its manager.json when there is no code to trade, or
// when its hub still takes its token (a bare dial, which takes nothing over: probeToken); else a new one for j's code
// (traded true).
func (e *env) builtinBinding(ctx context.Context, home string, j builtinJoin) (b managerconf.Binding, traded bool, err error) {
	b, err = managerconf.ReadBinding(home)
	switch {
	case j.always && j.hub != "" && j.code != "":
	case err == nil:
		if j.hub == "" || j.code == "" {
			return b, false, nil
		}
		c := &assistant.ManagerConfig{ID: assistantwire.AssistantID, Hub: b.Hub, Token: b.Token, Fingerprint: b.Fingerprint}
		if probeToken(ctx, c) == nil {
			return b, false, nil // the code stays unspent and runs out
		}
	case !errors.Is(err, managerconf.ErrNotBound):
		return b, false, ConfigError{fmt.Errorf("%s: %w", managerconf.DefaultFile(home), err)}
	case j.hub == "":
		return b, false, ConfigError{errors.New("the built-in manager is not bound to a hub: persona-hub manager-code on " +
			"the hub's machine prints a code, then PERSONA_MANAGER_CODE=<the code> persona-manager join --builtin <hub address> --install")}
	case j.code == "":
		return b, false, ConfigError{fmt.Errorf("the built-in manager is not bound to a hub, and %s has no code: "+
			"persona-hub manager-code on the hub's machine prints one", managerconf.BuiltinCodeEnv)}
	}
	b, err = JoinBuiltin(ctx, JoinOptions{Hub: j.hub, Code: j.code, Fingerprint: j.fingerprint, AllowPlaintext: j.plaintext}, home)
	return b, err == nil, err
}

// builtinService writes the built-in manager's unit (or agent) and makes sure it runs: started when it was not,
// restarted when the unit changed or bounce says so (a new binding), else left running.
func (e *env) builtinService(ctx context.Context, home, bin string, bounce bool) error {
	id := assistantwire.AssistantID
	switch e.goos {
	case "linux":
		path := e.unitPath(id)
		unit := e.compatUnit(id)
		want := e.compatUnitText(id, builtinUnit(bin, home))
		before, _ := os.ReadFile(path)
		changed := string(before) != want
		if changed {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
				return err
			}
		}
		_, activeErr := e.systemctl(ctx, "is-active", "--quiet", unit)
		steps := [][]string{{"daemon-reload"}, {"enable", unit}}
		what := "running already, left as it was"
		switch {
		case activeErr != nil:
			steps, what = append(steps, []string{"restart", unit}), "started"
		case changed || bounce:
			steps, what = append(steps, []string{"restart", unit}), "restarted"
		}
		for _, step := range steps {
			if out, err := e.systemctl(ctx, step...); err != nil {
				return fmt.Errorf("systemctl --user %s failed: %s", strings.Join(step, " "), firstLine(out+" "+err.Error()))
			}
		}
		fmt.Fprintf(e.stdout, "Service: systemd user unit %s, %s.\n  Log: journalctl --user -u %s -f\n", unit, what, unit)
		if e.user != "" && !fileExists("/var/lib/systemd/linger/"+e.user) {
			fmt.Fprintf(e.stdout, "  Linger is off: it stops when you log out. sudo loginctl enable-linger %s keeps it running\n", e.user)
		}
		return nil
	case "darwin":
		path := e.plistPath(id)
		label := e.compatLabel(id)
		want := e.compatLabelText(id, builtinPlist(bin, home, e.home))
		before, _ := os.ReadFile(path)
		changed := string(before) != want
		if err := os.MkdirAll(managerconf.DefaultDir(home), 0o700); err != nil { // its log's directory
			return err
		}
		if changed {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
				return err
			}
		}
		domain := "gui/" + strconv.Itoa(e.uid)
		_, loadedErr := e.run(ctx, "launchctl", "print", domain+"/"+label)
		what := "running already, left as it was"
		switch {
		case changed || loadedErr != nil:
			_, _ = e.run(ctx, "launchctl", "bootout", domain+"/"+label)
			// Right after bootout, bootstrap can fail for a moment (5: Input/output error, docs/lessons/server.md).
			var out string
			var err error
			for try := 0; try < bootstrapTries; try++ {
				if try > 0 {
					time.Sleep(bootstrapWait)
				}
				if out, err = e.run(ctx, "launchctl", "bootstrap", domain, path); err == nil {
					break
				}
			}
			if err != nil {
				return fmt.Errorf("launchctl bootstrap failed: %s", firstLine(out+" "+err.Error()))
			}
			what = "started"
		case bounce:
			if out, err := e.run(ctx, "launchctl", "kickstart", "-k", domain+"/"+label); err != nil {
				return fmt.Errorf("launchctl kickstart failed: %s", firstLine(out+" "+err.Error()))
			}
			what = "restarted"
		}
		fmt.Fprintf(e.stdout, "Service: launchd agent %s, %s; it runs whenever you are logged in.\n  Log: %s\n", label, what,
			filepath.Join(managerconf.DefaultDir(home), "manager.log"))
		return nil
	}
	return fmt.Errorf("no service for it on %s", e.goos)
}

// cmdUninstallBuiltin is `uninstall --builtin`: the service goes, its binding and its data stay.
func cmdUninstallBuiltin(ctx context.Context, e *env, args []string) error {
	for _, a := range args {
		if !isBuiltin([]string{a}) {
			return errUsage
		}
	}
	if err := e.removeService(ctx, &assistant.ManagerConfig{ID: assistantwire.AssistantID}); err != nil {
		return err
	}
	fmt.Fprintf(e.stdout, "Its binding and its data stay (%s). While the hub has assistant.mode service nothing runs the "+
		"built-in manager now: %s manager install --builtin puts the service back, or the hub starts it itself again "+
		"after persona-hub manager-mode child and persona-hub switch.\n", managerconf.DefaultDir(dataDir()), e.self())
	return nil
}

// builtinStatus prints the built-in manager's binding and service, when it has either.
func (e *env) builtinStatus(ctx context.Context, always bool) {
	home := dataDir()
	id := assistantwire.AssistantID
	installed := (e.goos == "linux" && fileExists(e.unitPath(id))) || (e.goos == "darwin" && fileExists(e.plistPath(id)))
	b, err := managerconf.ReadBinding(home)
	if !always && !installed && errors.Is(err, managerconf.ErrNotBound) {
		return
	}
	binding := "hub " + b.Hub
	switch {
	case errors.Is(err, managerconf.ErrNotBound):
		binding = "not bound"
	case err != nil:
		binding = err.Error()
	}
	service := "not installed (" + e.self() + " manager install --builtin)"
	if installed {
		service = e.serviceState(ctx, id)
	}
	fmt.Fprintf(e.stdout, "Built-in manager (%s)\n  Directory: %s\n  Binding: %s\n  Service: %s\n", id,
		managerconf.DefaultDir(home), binding, service)
}

// --- a new binary ---------------------------------------------------------------------------------------------------

// binaryCheck is how often the built-in manager's service looks at the file it runs from: an update puts a new one
// there (the hub's self-update swaps it with the hub's), and nothing else restarts this service.
var binaryCheck = time.Minute

// runningBinary is the file this process runs from and what it was when looked at; ok false when it cannot tell.
var runningBinary = func() (path string, fi os.FileInfo, ok bool) {
	exe, err := os.Executable()
	if err != nil {
		return "", nil, false
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	fi, err = os.Stat(exe)
	if err != nil {
		return "", nil, false
	}
	return exe, fi, true
}

// binaryReplaced: the file at path is not the one start was any more (another file put there, or this one written
// over). One that is not there right now is not taken for replaced yet.
func binaryReplaced(path string, start os.FileInfo) bool {
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !os.SameFile(fi, start) || !fi.ModTime().Equal(start.ModTime()) || fi.Size() != start.Size()
}

// watchBinary calls replaced (once) when the file at path is replaced, until ctx is done.
func watchBinary(ctx context.Context, path string, start os.FileInfo, replaced func()) {
	t := time.NewTicker(binaryCheck)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if binaryReplaced(path, start) {
				replaced()
				return
			}
		}
	}
}
