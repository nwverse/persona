package hubcli

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/nwverse/persona/server/internal/osproc"
	"github.com/nwverse/persona/server/internal/update"
)

// runUpdateWatch is `personad update-watch <pending.json>`: the old binary's watchdog over an update (hub/selfupdate.go,
// docs/operations/hub-update.md "Rollback"). The hub starts it just before running the new version; if that does not answer as the
// new build with the update confirmed within 3 minutes, the old binary goes back and the hub restarts on it.
func runUpdateWatch(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return usageError("usage: " + commandName() + " update-watch <pending.json>")
	}
	var p update.Pending
	ok, err := update.Load(args[0], &p)
	if err != nil || !ok {
		return err // nothing under way
	}
	return update.Watch(ctx, &http.Client{Timeout: 5 * time.Second}, p, args[0], time.Now().Add(3*time.Minute), 3*time.Second,
		func() error { return update.RestoreAll(p) }, // the hub and the programs swapped with it
		func() error { return restartHub(p) })
}

// restartHub stops the hub that would not come up; a service manager (systemd, launchd) starts it again on the
// binary just restored, otherwise this starts the program it ran as (Pending.RunPath) with the arguments it had.
func restartHub(p update.Pending) error {
	if p.PID > 1 {
		stopHub(p.PID, 10*time.Second, 500*time.Millisecond)
	}
	if supervised() {
		return nil
	}
	run := p.RunPath()
	cmd := exec.Command(run, update.TargetArgs(p.Args, run)...) // `hub …` when a hub that ran as persona-hub comes back as personad
	if log, err := os.OpenFile(filepath.Join(filepath.Dir(p.Target), "..", "update", "hub.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
		defer log.Close() // the started hub has its own copy
		cmd.Stdout, cmd.Stderr = log, log
	}
	cmd.SysProcAttr = osproc.DetachedAttr()
	return cmd.Start()
}

// A pid alone does not say the hub is still there: once it has exited, its pid can be another process's (a service
// manager's restart, anything in a container's small pid namespace). The watchdog is the hub's child (hub
// startWatchdog), and the hub runs the new version in its own process (exec), so the hub is this process's parent
// until it exits; then this process gets another parent (init, launchd, a subreaper), never again one with that pid.
// Windows keeps the parent's pid after it exits, but no hub updates itself there. Variables so tests can stand in
// for the hub.
var (
	hubIsParent = func(pid int) bool { return os.Getppid() == pid }
	signalHub   = func(p *os.Process, sig os.Signal) error { return p.Signal(sig) }
)

// stopHub asks the hub (pid, this process's parent) to exit with SIGTERM, waits up to grace for it, checking every
// poll, and sends SIGKILL only to a hub that is still there, then waits up to grace again so that a hub started next
// does not find the old one still holding its port. It signals nothing once the hub has gone. The process handle is
// taken before the parent check: on Linux it is a pidfd, and a hub that is still the parent after that means the
// handle is the hub's, so no signal can reach a process that takes the pid later.
func stopHub(pid int, grace, poll time.Duration) {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	defer proc.Release()
	if !hubIsParent(pid) {
		return
	}
	gone := func() bool { // within grace
		deadline := time.Now().Add(grace)
		for hubIsParent(pid) {
			if time.Now().After(deadline) {
				return false
			}
			time.Sleep(poll)
		}
		return true
	}
	if signalHub(proc, syscall.SIGTERM) == nil && gone() { // Windows cannot send SIGTERM: the hub is killed at once
		return
	}
	if hubIsParent(pid) {
		_ = signalHub(proc, os.Kill)
		gone()
	}
}

// supervised: the hub runs under systemd (INVOCATION_ID) or launchd (XPC_SERVICE_NAME, "0" in a Terminal shell).
func supervised() bool {
	x := os.Getenv("XPC_SERVICE_NAME")
	return os.Getenv("INVOCATION_ID") != "" || (x != "" && x != "0")
}
