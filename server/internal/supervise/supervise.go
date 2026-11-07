// Package supervise is `personad supervise`: the hub and the built-in manager as two processes side by side in one
// container (docs/operations/docker.md "The hub and the built-in manager in one container"). Each is started again
// when it stops, after a pause that grows while it keeps stopping, and the other is left running. With
// hub.json's assistant.mode service the built-in manager is a service of its own (docs/assistant/managers.md "The
// built-in manager as its own service"): the supervisor binds it to the hub (a one-time code made from the hub's
// database, traded over loopback) before it starts it. Otherwise the hub starts its own child as it always has, and
// the supervisor becomes the hub (exec): the container is what it was. With --manager-only it runs the built-in
// manager alone, for a container of its own beside the hub's (compose.yaml's commented example).
package supervise

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/nwverse/persona/server/internal/assistant"
	"github.com/nwverse/persona/server/internal/clitext"
	"github.com/nwverse/persona/server/internal/hub"
	"github.com/nwverse/persona/server/internal/identity"
	"github.com/nwverse/persona/server/internal/manager"
)

// SeparateEnv on the hub's container says the built-in manager runs in a container of its own (--manager-only):
// the hub's then runs the hub alone.
const SeparateEnv = "PERSONA_BUILTIN_MANAGER"

// Usage is the command's help.
const Usage = `usage: personad supervise [--manager-only [--hub <hub address>]]
  the hub and, with hub.json's assistant.mode service, the built-in manager beside it, each started again when it
  stops (the hub image runs this); --manager-only: the built-in manager alone, bound to the hub at --hub
  (default http://hub:18660, the hub's compose service)`

var (
	// backoffFirst is the first pause before a program that stopped starts again; it doubles up to backoffMost while
	// the program keeps stopping within steadyAfter of its start.
	backoffFirst = time.Second
	backoffMost  = 30 * time.Second
	steadyAfter  = time.Minute
	// stopWait is how long the programs get to stop after a signal, before they are killed.
	stopWait = 20 * time.Second
	// healthWait bounds one wait for the hub to answer before a binding is tried; bindRetry is the pause after a
	// binding that failed.
	healthWait = 2 * time.Minute
	bindRetry  = 10 * time.Second
	// modeRecheck is how often --manager-only reads hub.json again while the built-in manager is not a service.
	modeRecheck = 30 * time.Second
)

// env is what the supervisor touches outside itself, so that tests stand in for it.
type env struct {
	self string // the program to run the two with
	home string // the data directory
	out  io.Writer
	// execHub replaces this process with the hub (the container as it was).
	execHub func(self string, env []string) error
	// bind makes sure the built-in manager is bound to the hub at hubURL (bind, below).
	bind    func(ctx context.Context, hubURL string) error
	environ []string
}

// Run is `personad supervise [--manager-only [--hub <url>]]`.
func Run(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("supervise", flag.ContinueOnError)
	fs.SetOutput(out)
	only := fs.Bool("manager-only", false, "the built-in manager alone, in a container of its own")
	hubURL := fs.String("hub", "http://hub:18660", "with --manager-only: the hub's address")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return clitext.UsageError(Usage)
	}
	if fs.NArg() != 0 {
		return clitext.UsageError(Usage)
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	e := &env{self: self, home: identity.HomeDir(), out: out, execHub: execHub, environ: os.Environ()}
	e.bind = e.bindBuiltin
	if *only {
		return e.managerOnly(ctx, strings.TrimRight(*hubURL, "/"))
	}
	return e.hubContainer(ctx)
}

func (e *env) logf(format string, args ...any) {
	fmt.Fprintf(e.out, "supervise: "+format+"\n", args...)
}

// hubFile is what the supervisor reads of hub.json (never writing it: the hub does).
type hubFile struct {
	Listen    string `json:"listen"`
	Assistant struct {
		Mode     string `json:"mode"`
		External bool   `json:"external"`
	} `json:"assistant"`
}

// readHubFile reads hub.json in the data directory; a missing one is a hub's first start: its defaults.
func (e *env) readHubFile() (hubFile, error) {
	var f hubFile
	b, err := os.ReadFile(filepath.Join(e.home, "hub.json"))
	if err != nil && !os.IsNotExist(err) {
		return f, err
	}
	if err == nil {
		if err := json.Unmarshal(b, &f); err != nil {
			return f, fmt.Errorf("hub.json: %w", err)
		}
	}
	if v := identity.Getenv("PERSONA_LISTEN"); v != "" { // as the hub takes it (hub.Config.applyEnv)
		f.Listen = v
	}
	return f, nil
}

// serviceMode: the built-in manager is a service of its own here (hub.Config's ServiceBound).
func (f hubFile) serviceMode() bool {
	return !f.Assistant.External && f.Assistant.Mode == hub.AssistantModeService
}

// loopback is the hub's address on this machine for its listen address (hub's hubLocalURL): loopback when it
// listens on every address, else the address it listens on.
func loopback(listen string) string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil || port == "" {
		return "http://127.0.0.1:18660"
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}

// hubContainer is the hub's container: the hub alone (exec) unless the built-in manager is a service of its own here;
// then the two side by side (the manager not, when SeparateEnv says it has a container of its own).
func (e *env) hubContainer(ctx context.Context) error {
	f, err := e.readHubFile()
	if err != nil {
		return err
	}
	if !f.serviceMode() {
		e.logf("assistant.mode is not service: the hub starts the built-in manager itself, as before")
		return e.execHub(e.self, e.environ)
	}
	progs := []*program{{name: "hub", args: []string{"hub", "run"}}}
	if identity.Getenv(SeparateEnv) == "separate" {
		e.logf("%s=separate: the built-in manager runs in a container of its own; here, the hub alone", SeparateEnv)
	} else {
		hubURL := loopback(f.Listen)
		progs = append(progs, &program{name: "manager", args: []string{"assistant"},
			env:    []string{"PERSONA_ASSISTANT_DEPLOY=" + assistant.DeployService},
			before: func(ctx context.Context) error { return e.bind(ctx, hubURL) }})
	}
	return e.supervise(ctx, progs)
}

// managerOnly is the built-in manager's own container: it waits for hub.json to say assistant.mode service (the hub
// starts its own child otherwise), then runs the manager, bound to the hub at hubURL.
func (e *env) managerOnly(ctx context.Context, hubURL string) error {
	said := false
	for {
		f, err := e.readHubFile()
		if err == nil && f.serviceMode() {
			break
		}
		if !said {
			why := "assistant.mode is not service: the hub starts the built-in manager itself"
			if err != nil {
				why = err.Error()
			}
			e.logf("%s; waiting (hub.json is read again every %s)", why, modeRecheck)
			said = true
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(modeRecheck):
		}
	}
	return e.supervise(ctx, []*program{{name: "manager", args: []string{"assistant"},
		env:    []string{"PERSONA_ASSISTANT_DEPLOY=" + assistant.DeployService},
		before: func(ctx context.Context) error { return e.bind(ctx, hubURL) }}})
}

// program is one of the programs supervised.
type program struct {
	name string
	args []string
	env  []string
	// before runs before each start; an error is said, and it is tried again after bindRetry.
	before func(ctx context.Context) error
}

// supervise runs progs, each started again when it stops, until ctx ends; then they are stopped (SIGTERM, then
// SIGKILL after stopWait) and it returns.
func (e *env) supervise(ctx context.Context, progs []*program) error {
	var wg sync.WaitGroup
	for _, p := range progs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e.keep(ctx, p)
		}()
	}
	wg.Wait()
	return nil
}

// keep runs p until ctx ends, again each time it stops.
func (e *env) keep(ctx context.Context, p *program) {
	pause := backoffFirst
	for ctx.Err() == nil {
		if p.before != nil {
			if err := p.before(ctx); err != nil {
				if ctx.Err() != nil {
					return
				}
				e.logf("%s: %v; trying again in %s", p.name, err, bindRetry)
				sleep(ctx, bindRetry)
				continue
			}
		}
		started := time.Now()
		err := e.run(ctx, p)
		if ctx.Err() != nil {
			return
		}
		if time.Since(started) >= steadyAfter {
			pause = backoffFirst
		}
		e.logf("%s stopped (%v): starting it again in %s; the other runs on", p.name, exitText(err), pause)
		sleep(ctx, pause)
		pause = min(pause*2, backoffMost)
	}
}

// run starts p and waits for it; once ctx ends it is asked to stop (SIGTERM), and killed after stopWait.
func (e *env) run(ctx context.Context, p *program) error {
	cmd := exec.Command(e.self, p.args...)
	cmd.Env = append(append([]string{}, e.environ...), p.env...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	select {
	case err := <-done:
		return err
	case <-time.After(stopWait):
		e.logf("%s did not stop within %s: killing it", p.name, stopWait)
		_ = cmd.Process.Kill()
		return <-done
	}
}

// exitText says how a program ended.
func exitText(err error) string {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ProcessState.String()
	}
	if err == nil {
		return "exit status 0"
	}
	return err.Error()
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

// bindBuiltin waits until the hub at hubURL answers, then binds the built-in manager to it unless its binding works:
// a one-time code made in the hub's database (as `persona-hub manager-code` makes one), traded over hubURL, and
// voided after when it was not.
func (e *env) bindBuiltin(ctx context.Context, hubURL string) error {
	if err := waitHealthy(ctx, hubURL, healthWait); err != nil {
		return err
	}
	var minted string
	traded, err := manager.BindBuiltin(ctx, e.home, hubURL, func(ctx context.Context) (string, error) {
		code, err := hub.MintBuiltinCode(ctx)
		minted = code
		return code, err
	})
	if minted != "" && !traded {
		_ = hub.VoidBuiltinCode(context.WithoutCancel(ctx), minted)
	}
	if err != nil {
		return fmt.Errorf("binding the built-in manager to the hub at %s: %w", hubURL, err)
	}
	if traded {
		e.logf("bound the built-in manager to the hub at %s", hubURL)
	}
	return nil
}

// waitHealthy waits up to most for GET hubURL/api/v1/health to answer 200.
func waitHealthy(ctx context.Context, hubURL string, most time.Duration) error {
	u, err := url.JoinPath(hubURL, "/api/v1/health")
	if err != nil {
		return err
	}
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Timeout: 5 * time.Second, Transport: transport}
	deadline := time.Now().Add(most)
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return err
		}
		if resp, err := client.Do(req); err == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("the hub at %s did not answer within %s", hubURL, most)
		}
		sleep(ctx, time.Second)
	}
}
