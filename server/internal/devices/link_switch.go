package devices

import (
	"bufio"
	"bytes"
	"cmp"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/bus"
	"github.com/nwverse/persona/server/internal/devicewire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// Switching an ssh computer to Persona Link (#10662, docs/operations/link.md "Switching from SSH"): POST
// /api/v1/machines/{mid}/link-switch from a full device, or the hub's admin page through its twin route. The manager
// does it over its own ssh access to the computer, in the background (the answer is 202 at once), one switch per
// computer at a time:
//
//  1. checking: a read-only look (lsProbeScript): Linux on amd64/arm64, a systemd user manager that keeps running
//     (linger), no Persona Link there yet, a node program to run as a service.
//  2. installing: the hub's Persona Link build for the platform (connectorPackage) goes over ssh to a staged file
//     (lsStageScript, checked); then the hub's begin turns the entry into kind link and makes one pairing code, which
//     ends the ssh connection here (the machine is replaced, startMachines). One more ssh run links the staged build
//     into place and runs `persona-link install --pair -` with the pairing link on its stdin (lsInstallScript).
//  3. connecting: until the computer's node answers through Persona Link; then the hub's end(succeeded).
//  4. rolling_back, on any failure once begin may have happened: the hub's end(rolled_back) revokes this switch's code
//     and credential and puts the kind back to ssh; then only what this switch set up is removed (lsUninstallScript),
//     and the computer must be back over ssh. What cannot be shown to have worked is failed with
//     rollback_unconfirmed, never reported as undone.
//
// A record (link_switch.<machine id> in the settings) is written before each step that changes anything, so a
// manager that restarts during a switch finishes it (resumeLinkSwitches). The pairing link is never written down:
// not in the record, a log line, an answer to the apps or a command line; only the computer's installer reads it.

// linkSwitchRecord is a switch as the settings keep it.
type linkSwitchRecord struct {
	Operation           string    `json:"operation"` // ls_<16 hex>, the hub's audit names it too
	MachineID           string    `json:"machine_id"`
	By                  string    `json:"by"` // device | admin
	Phase               string    `json:"phase"`
	Error               string    `json:"error,omitempty"`
	RollbackUnconfirmed bool      `json:"rollback_unconfirmed,omitempty"`
	StartedAt           time.Time `json:"started_at"`
	At                  time.Time `json:"at"`       // when Phase last changed
	Deadline            time.Time `json:"deadline"` // a restarted manager waits for the link until then
	SSHHost             string    `json:"ssh_host,omitempty"`
	Arch                string    `json:"arch,omitempty"`
	// What the probe found already there: what a rollback never removes.
	LinkExisted     bool `json:"link_existed,omitempty"`
	NodeUnitExisted bool `json:"node_unit_existed,omitempty"`
	// SHA256 is the build sent's: the uninstall removes Persona Link only through that program.
	SHA256        string `json:"sha256,omitempty"`
	BeginIntended bool   `json:"begin_intended,omitempty"`
	PairHash      string `json:"pair_hash,omitempty"` // the hub's begin answered it (not a secret)
	// Install: "" not run, running (it may have placed the build, or not), placed, not_placed (refused before).
	Install       string `json:"install,omitempty"`
	InstallerExit *int   `json:"installer_exit,omitempty"`
}

// Install states of a record.
const (
	lsInstallRunning   = "running"
	lsInstallPlaced    = "placed"
	lsInstallNotPlaced = "not_placed"
)

func (r *linkSwitchRecord) finished() bool {
	return r != nil && (r.Phase == proto.LinkSwitchPhaseSucceeded || r.Phase == proto.LinkSwitchPhaseFailed)
}

func (r *linkSwitchRecord) unfinished() bool { return r != nil && !r.finished() }

// host is where ssh goes, as the machine's node connection does (Machine.command).
func (r *linkSwitchRecord) host() string { return cmp.Or(r.SSHHost, r.MachineID) }

func linkSwitchKey(machine string) string { return "link_switch." + machine }

// linkSwitchTimes bound each step (linkSwitches.times; zero: these).
type linkSwitchTimes struct {
	Probe, Transfer, Begin, Install, Connect, EndRetry, Succeed, Uninstall, SSHBack time.Duration
	// Total is a switch's deadline, Resume how long a restarted manager waits for the link at least; Shown how long a
	// finished switch stays on the machine; Poll how often a wait looks again.
	Total, Resume, Shown, Poll time.Duration
}

var defaultLinkSwitchTimes = linkSwitchTimes{Probe: 30 * time.Second, Transfer: 10 * time.Minute, Begin: 30 * time.Second,
	Install: 3 * time.Minute, Connect: 90 * time.Second, EndRetry: 2 * time.Minute, Succeed: 30 * time.Second,
	Uninstall: time.Minute, SSHBack: 2 * time.Minute, Total: 20 * time.Minute, Resume: time.Minute, Shown: time.Hour,
	Poll: time.Second}

// sshRunner runs ssh's argv (argv[0] is "ssh") with stdin, its output to stdout, and answers ssh's exit code (-1 when
// it did not exit by itself: not started, or stopped by ctx). what names the script, for the log.
type sshRunner func(ctx context.Context, what string, argv []string, stdin io.Reader, stdout io.Writer) (int, error)

// linkSwitches are the manager's switches: the current or last one of each machine, as its record says.
type linkSwitches struct {
	mu      sync.Mutex
	records map[string]*linkSwitchRecord
	loaded  map[string]bool // the stored record was read (or there is none)
	active  map[string]bool // a goroutine of this manager runs the switch
	wg      sync.WaitGroup  // those goroutines (tests wait for them)
	// Tests replace these: the steps' times, the ssh, the build, where the probe looks.
	times linkSwitchTimes
	run   sshRunner
	pkg   func(ctx context.Context, goos, arch string) (path, version string, err error)
	paths lsPaths
}

func (d *Devices) linkSwitchTimes() linkSwitchTimes {
	if t := d.switches.times; t.Poll > 0 {
		return t
	}
	return defaultLinkSwitchTimes
}

func (d *Devices) linkSwitchPaths() lsPaths {
	if p := d.switches.paths; p.systemd != "" {
		return p
	}
	return defaultLSPaths
}

// linkSwitchRecordOf is the machine's record as this manager has it now (a copy; nil: none known).
func (d *Devices) linkSwitchRecordOf(id string) *linkSwitchRecord {
	ls := &d.switches
	ls.mu.Lock()
	defer ls.mu.Unlock()
	if r := ls.records[id]; r != nil {
		c := *r
		return &c
	}
	return nil
}

// linkSwitchUnfinished: a switch of the machine is under way (a deploy waits for it).
func (d *Devices) linkSwitchUnfinished(id string) bool {
	return d.linkSwitchRecordOf(id).unfinished()
}

// loadLinkSwitch reads the machine's stored record into memory, once.
func (d *Devices) loadLinkSwitch(ctx context.Context, id string) error {
	ls := &d.switches
	ls.mu.Lock()
	done := ls.loaded[id] || ls.records[id] != nil
	ls.mu.Unlock()
	if done || d.opts.Store == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var rec linkSwitchRecord
	found, err := d.opts.Store.Setting(ctx, linkSwitchKey(id), &rec)
	if err != nil {
		return err
	}
	ls.mu.Lock()
	defer ls.mu.Unlock()
	if ls.loaded == nil {
		ls.loaded = map[string]bool{}
	}
	ls.loaded[id] = true
	if found && rec.MachineID == id && ls.records[id] == nil {
		if ls.records == nil {
			ls.records = map[string]*linkSwitchRecord{}
		}
		ls.records[id] = &rec
	}
	return nil
}

// saveLinkSwitch keeps rec, in memory and in the settings; the apps get the change.
func (d *Devices) saveLinkSwitch(ctx context.Context, rec linkSwitchRecord) error {
	ls := &d.switches
	ls.mu.Lock()
	if ls.records == nil {
		ls.records = map[string]*linkSwitchRecord{}
	}
	c := rec
	ls.records[rec.MachineID] = &c
	ls.mu.Unlock()
	d.changed()
	if d.opts.Store == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return d.opts.Store.PutSetting(ctx, linkSwitchKey(rec.MachineID), rec)
}

// linkSwitchCandidate: a computer this manager connects itself over ssh, which a switch may start on.
func (d *Devices) linkSwitchCandidate(m *Machine) bool {
	if m.derived || kindOf(m.cfg) != "ssh" {
		return false
	}
	mesh := d.config().Mesh
	return mesh == nil || !mesh.Member // a member's computers are the center's to connect
}

// linkSwitchReason is why a switch cannot start on the candidate m now ("": it can), cheaply: from what the hub's
// handover and the node's node.info say, nothing asked of the computer.
func (d *Devices) linkSwitchReason(m *Machine, info proto.NodeInfo, online, deploying bool, rec *linkSwitchRecord) string {
	cfg := d.config()
	switch {
	case rec.unfinished():
		return proto.LinkSwitchReasonInProgress
	case !cfg.LinkSwitch && len(cfg.Endpoints) == 0:
		return proto.LinkSwitchReasonLinkUnavailable
	case !cfg.LinkSwitch:
		return proto.LinkSwitchReasonHubOld
	case cfg.PendingUnknown || slices.Contains(cfg.Pending, m.cfg.ID):
		return proto.LinkSwitchReasonPending
	case m.cfg.RemoteBin != "" && m.cfg.RemoteBin != "~/.persona/bin/personad",
		info.Home != "" && info.DataDir != info.Home+"/.persona" && info.DataDir != info.Home+`\.persona`:
		return proto.LinkSwitchReasonCustomNode
	case !online:
		return proto.LinkSwitchReasonOffline
	case !strings.EqualFold(info.OS, "linux") || !lsArch(info.Arch):
		return proto.LinkSwitchReasonUnsupportedOS
	case deploying:
		return proto.LinkSwitchReasonBusy
	}
	return ""
}

// lsArch: a platform Persona Link is switched to from here.
func lsArch(arch string) bool { return arch == "amd64" || arch == "arm64" }

// linkSwitchView is the machine's link_switch in the apps' list: for a candidate, whether a switch can start now;
// for any machine, the current switch or one that ended within the hour. nil: nothing to show.
func (d *Devices) linkSwitchView(m *Machine, info proto.NodeInfo, online, deploying bool) *proto.MachineLinkSwitch {
	rec := d.linkSwitchRecordOf(m.cfg.ID)
	if rec != nil && rec.finished() && time.Since(rec.At) >= d.linkSwitchTimes().Shown {
		rec = nil
	}
	candidate := d.linkSwitchCandidate(m)
	if !candidate && rec == nil {
		return nil
	}
	v := &proto.MachineLinkSwitch{}
	if candidate {
		v.Reason = d.linkSwitchReason(m, info, online, deploying, rec)
		v.Available = v.Reason == ""
	} else if rec.unfinished() {
		v.Reason = proto.LinkSwitchReasonInProgress
	}
	if rec != nil {
		v.Phase, v.Error, v.RollbackUnconfirmed, v.At = rec.Phase, rec.Error, rec.RollbackUnconfirmed, rec.At
	}
	return v
}

// linkSwitchFacts is what a switch reads of the machine now.
func (m *Machine) linkSwitchFacts() (info proto.NodeInfo, online, deploying bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.info, m.online, m.deploying
}

// errLinkSwitchDeploy: a node update waits until the switch to Persona Link ends.
var errLinkSwitchDeploy = i18n.Err("devices-link_switch.deploy_refused")

// postLinkSwitch is POST /api/v1/machines/{mid}/link-switch: a full device (or the hub's admin page, relayed with
// AdminHeader) starts a switch; 202 with its link_switch, 409 with the reason it cannot start.
func (d *Devices) postLinkSwitch(w http.ResponseWriter, r *http.Request, m *Machine) {
	w.Header().Set("Cache-Control", "no-store")
	var by string
	switch {
	case r.Header.Get(assistantwire.AdminHeader) == "1":
		by = "admin"
	case r.Header.Get(assistantwire.RoleHeader) == "full":
		by = "device"
	default:
		writeErr(w, http.StatusForbidden, i18n.Err("devices-link_switch.forbidden"))
		return
	}
	view, reason, err := d.startLinkSwitch(r.Context(), m, by)
	switch {
	case err != nil:
		writeErr(w, http.StatusInternalServerError, i18n.Err("devices-link_switch.save_failed"))
	case reason == proto.LinkSwitchReasonInProgress:
		writeErrCode(w, http.StatusConflict, reason, i18n.Err("devices-link_switch.in_progress"))
	case reason != "":
		writeErrCode(w, http.StatusConflict, reason, i18n.Err("devices-link_switch.unavailable"))
	default:
		apierr.WriteBody(w, http.StatusAccepted, map[string]any{"link_switch": view})
	}
}

// startLinkSwitch starts a switch of m (by: device | admin) unless one cannot start now (reason); err: its record
// could not be read or written, and nothing started.
func (d *Devices) startLinkSwitch(ctx context.Context, m *Machine, by string) (*proto.MachineLinkSwitch, string, error) {
	id := m.cfg.ID
	if err := d.loadLinkSwitch(ctx, id); err != nil { // a switch a restart left must be seen first
		d.logf("[%s] link switch: read the last switch: %v", id, err)
		return nil, "", err
	}
	if !d.linkSwitchCandidate(m) {
		if kindOf(m.cfg) == kindLink {
			return nil, proto.LinkSwitchReasonExistingLink, nil
		}
		return nil, proto.LinkSwitchReasonUnknown, nil
	}
	info, online, deploying := m.linkSwitchFacts()
	if reason := d.linkSwitchReason(m, info, online, deploying, d.linkSwitchRecordOf(id)); reason != "" {
		return nil, reason, nil
	}
	t := d.linkSwitchTimes()
	now := time.Now().UTC()
	rec := linkSwitchRecord{Operation: "ls_" + randomHex(8), MachineID: id, By: by, Phase: proto.LinkSwitchPhaseChecking,
		StartedAt: now, At: now, Deadline: now.Add(t.Total), SSHHost: m.cfg.SSHHost}
	ls := &d.switches
	ls.mu.Lock()
	if ls.active[id] || ls.records[id].unfinished() {
		ls.mu.Unlock()
		return nil, proto.LinkSwitchReasonInProgress, nil
	}
	prev := ls.records[id]
	if ls.active == nil {
		ls.active = map[string]bool{}
	}
	ls.active[id] = true
	ls.wg.Add(1)
	ls.mu.Unlock()
	if err := d.saveLinkSwitch(ctx, rec); err != nil {
		ls.mu.Lock()
		if prev == nil {
			delete(ls.records, id)
		} else {
			ls.records[id] = prev
		}
		delete(ls.active, id)
		ls.mu.Unlock()
		ls.wg.Done()
		d.changed()
		d.logf("[%s] link switch: save its record: %v", id, err)
		return nil, "", err
	}
	d.logf("[%s] link switch %s: started (by %s)", id, rec.Operation, by)
	go func() {
		defer d.releaseLinkSwitch(id)
		(&linkSwitchRun{d: d, rec: rec, t: t}).run(d.deployContext())
	}()
	return d.linkSwitchView(m, info, online, deploying), "", nil
}

// releaseLinkSwitch: this manager's goroutine for the machine's switch is done.
func (d *Devices) releaseLinkSwitch(id string) {
	ls := &d.switches
	ls.mu.Lock()
	delete(ls.active, id)
	ls.mu.Unlock()
	ls.wg.Done()
}

// resumeLinkSwitches finishes, once the machines are configured, a switch a restart left unfinished: one that never
// got to the hub's begin fails (its staged build removed); one that may have, waits for the link a while and
// succeeds, or rolls back.
func (d *Devices) resumeLinkSwitches(ctx context.Context) {
	for _, ch := range []chan struct{}{d.gotCh, d.ready} {
		select {
		case <-ch:
		case <-ctx.Done():
			return
		}
	}
	t := d.linkSwitchTimes()
	left := d.cfgMachines()
	for len(left) > 0 {
		var again []*Machine
		for _, m := range left {
			if err := d.loadLinkSwitch(ctx, m.cfg.ID); err != nil {
				again = append(again, m) // the store does not answer: looked at again shortly
				continue
			}
			d.resumeLinkSwitch(ctx, m.cfg.ID, t)
		}
		if left = again; len(left) == 0 {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(10 * t.Poll):
		}
	}
}

// resumeLinkSwitch takes over the machine's unfinished switch, if it has one nobody here runs.
func (d *Devices) resumeLinkSwitch(ctx context.Context, id string, t linkSwitchTimes) {
	ls := &d.switches
	ls.mu.Lock()
	rec := ls.records[id]
	if !rec.unfinished() || ls.active[id] {
		ls.mu.Unlock()
		return
	}
	if ls.active == nil {
		ls.active = map[string]bool{}
	}
	ls.active[id] = true
	ls.wg.Add(1)
	r := &linkSwitchRun{d: d, rec: *rec, t: t}
	ls.mu.Unlock()
	d.logf("[%s] link switch %s: resumed after a restart (phase %s)", id, r.rec.Operation, r.rec.Phase)
	go func() {
		defer d.releaseLinkSwitch(id)
		r.resume(ctx)
	}()
}

// linkSwitchRun is one switch being carried out.
type linkSwitchRun struct {
	d   *Devices
	rec linkSwitchRecord
	t   linkSwitchTimes
}

func (r *linkSwitchRun) logf(format string, args ...any) {
	r.d.logf("[%s] link switch %s: "+format, append([]any{r.rec.MachineID, r.rec.Operation}, args...)...)
}

// save keeps the record as it is now; false (logged) when the settings did not take it.
func (r *linkSwitchRun) save(ctx context.Context) bool {
	if err := r.d.saveLinkSwitch(ctx, r.rec); err != nil {
		r.logf("save the record: %v", err)
		return false
	}
	return true
}

func (r *linkSwitchRun) phase(ctx context.Context, phase string) bool {
	r.rec.Phase, r.rec.At = phase, time.Now().UTC()
	return r.save(ctx)
}

// finish ends the switch: succeeded, or failed with why (and whether the computer could be shown to be back as it
// was).
func (r *linkSwitchRun) finish(ctx context.Context, phase, why string, unconfirmed bool) {
	r.rec.Error, r.rec.RollbackUnconfirmed = why, unconfirmed
	// Written even as the manager stops: what happened is known, and a restart must not undo it.
	r.phase(context.WithoutCancel(ctx), phase)
	r.logf("%s (error %q, rollback unconfirmed %t)", phase, why, unconfirmed)
}

func (r *linkSwitchRun) run(ctx context.Context) {
	reason := r.probe(ctx)
	if ctx.Err() != nil {
		return // the manager stops: the record says where this switch was
	}
	if reason != "" {
		r.finish(ctx, proto.LinkSwitchPhaseFailed, reason, false)
		return
	}
	if !r.phase(ctx, proto.LinkSwitchPhaseInstalling) {
		r.finish(ctx, proto.LinkSwitchPhaseFailed, proto.LinkSwitchReasonUnknown, false)
		return
	}
	pkgOf := r.d.switches.pkg
	if pkgOf == nil {
		pkgOf = r.d.connectorPackage
	}
	path, version, err := pkgOf(ctx, "linux", r.rec.Arch)
	if err == nil {
		r.rec.SHA256, err = fileSHA256(path)
	}
	if ctx.Err() != nil {
		return
	}
	if err != nil { // none anywhere, the release site away, a build that did not check out: none to send
		r.logf("no build: %v", err)
		r.finish(ctx, proto.LinkSwitchPhaseFailed, proto.LinkSwitchReasonNoPackage, false)
		return
	}
	if !r.save(ctx) {
		r.finish(ctx, proto.LinkSwitchPhaseFailed, proto.LinkSwitchReasonUnknown, false)
		return
	}
	if !r.stage(ctx, path) {
		if ctx.Err() != nil {
			return
		}
		r.removeStaged(ctx)
		r.finish(ctx, proto.LinkSwitchPhaseFailed, proto.LinkSwitchReasonTransferFailed, false)
		return
	}
	r.logf("build %s staged", version)
	r.rec.BeginIntended = true
	if !r.save(ctx) { // never asked of the hub without its record
		r.removeStaged(ctx)
		r.finish(ctx, proto.LinkSwitchPhaseFailed, proto.LinkSwitchReasonUnknown, false)
		return
	}
	link, refused, err := r.begin(ctx)
	switch {
	case refused != "": // the hub changed nothing
		r.removeStaged(ctx)
		r.finish(ctx, proto.LinkSwitchPhaseFailed, refused, false)
		return
	case err != nil: // it may have begun, its answer lost
		if ctx.Err() != nil {
			return
		}
		r.rec.Install = lsInstallNotPlaced // nothing was sent to install
		r.rollback(ctx, proto.LinkSwitchReasonUnknown)
		return
	}
	r.rec.Install = lsInstallRunning
	if !r.save(ctx) {
		r.rec.Install = lsInstallNotPlaced
		r.rollback(ctx, proto.LinkSwitchReasonUnknown)
		return
	}
	why := r.install(ctx, link)
	link = ""
	if ctx.Err() != nil {
		return
	}
	if why != "" {
		r.rollback(ctx, why)
		return
	}
	if !r.phase(ctx, proto.LinkSwitchPhaseConnecting) {
		r.logf("the connecting phase was not kept: a restart waits for the link anyway")
	}
	if r.waitLink(ctx, time.Now().Add(r.t.Connect)) {
		r.succeed(ctx)
		return
	}
	if ctx.Err() != nil {
		return
	}
	why = proto.LinkSwitchReasonLinkTimeout
	if r.rec.InstallerExit != nil && *r.rec.InstallerExit != 0 {
		why = proto.LinkSwitchReasonInstallFailed
	}
	r.rollback(ctx, why)
}

// resume carries on a switch a restarted manager found unfinished (resumeLinkSwitches).
func (r *linkSwitchRun) resume(ctx context.Context) {
	if !r.rec.BeginIntended {
		r.removeStaged(ctx)
		if ctx.Err() == nil {
			r.finish(ctx, proto.LinkSwitchPhaseFailed, proto.LinkSwitchReasonInterrupted, false)
		}
		return
	}
	why := proto.LinkSwitchReasonInterrupted
	if r.rec.Phase == proto.LinkSwitchPhaseRollingBack && r.rec.Error != "" {
		why = r.rec.Error // it was undoing already: why it was
	} else {
		until := r.rec.Deadline
		if soon := time.Now().Add(r.t.Resume); soon.After(until) {
			until = soon
		}
		if r.waitLink(ctx, until) {
			r.succeed(ctx)
			return
		}
	}
	if ctx.Err() == nil {
		r.rollback(ctx, why)
	}
}

// ssh runs script on the computer, its output kept up to 64 KiB (and never logged); exit -1 when ssh did not end by
// itself.
func (r *linkSwitchRun) ssh(ctx context.Context, what, script string, stdin io.Reader, timeout time.Duration) (int, []byte) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	argv := []string{"ssh", "-T", "-o", "BatchMode=yes", "-o", "ConnectTimeout=15", "-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=3", r.rec.host(), script}
	run := r.d.switches.run
	if run == nil {
		run = runSSH
	}
	if stdin == nil {
		stdin = strings.NewReader("")
	}
	out := &cappedBuffer{max: 64 << 10}
	exit, err := run(ctx, what, argv, stdin, out)
	if err != nil && exit >= 0 {
		exit = -1
	}
	r.logf("%s: exit %d", what, exit)
	return exit, out.Bytes()
}

// runSSH is sshRunner over this machine's ssh: its process group is stopped with ctx and swept after, as a node's
// ssh connection's is (machine.go open).
func runSSH(ctx context.Context, _ string, argv []string, stdin io.Reader, stdout io.Writer) (int, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	stopSSHTreeOnCancel(cmd)
	cmd.WaitDelay = shutdownStepTimeout
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, io.Discard
	if err := cmd.Start(); err != nil {
		return -1, err
	}
	err := cmd.Wait()
	sweepSSHProcessTree(cmd.Process.Pid)
	if err == nil {
		return 0, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.Exited() {
		return exit.ExitCode(), nil
	}
	return -1, err
}

// cappedBuffer keeps the first max bytes written to it and takes the rest without keeping it.
type cappedBuffer struct {
	bytes.Buffer
	max int
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if room := b.max - b.Len(); room > 0 {
		b.Buffer.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

// outputOf is a script's key=value lines.
func outputOf(out []byte) map[string]string {
	kv := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		if k, v, ok := strings.Cut(strings.TrimSpace(sc.Text()), "="); ok {
			kv[k] = v
		}
	}
	return kv
}

// sshFailed: ssh itself failed (exit 255), or did not end by itself.
func sshFailed(exit int) bool { return exit < 0 || exit == 255 }

// probe is the checking phase: "" when the computer can be switched (the record has what it found), else why not.
func (r *linkSwitchRun) probe(ctx context.Context) string {
	exit, out := r.ssh(ctx, "probe", lsProbeScript(r.d.linkSwitchPaths()), nil, r.t.Probe)
	kv := outputOf(out)
	switch {
	case sshFailed(exit):
		return proto.LinkSwitchReasonOffline
	case exit != 0 || kv["end"] != "1":
		return proto.LinkSwitchReasonUnknown
	}
	arch := map[string]string{"x86_64": "amd64", "amd64": "amd64", "aarch64": "arm64", "arm64": "arm64"}[kv["arch"]]
	r.rec.Arch = arch
	r.rec.LinkExisted, r.rec.NodeUnitExisted = kv["link"] == "1", kv["node_unit"] == "1"
	switch {
	case kv["os"] != "Linux" || arch == "":
		return proto.LinkSwitchReasonUnsupportedOS
	case kv["systemd"] != "1":
		return proto.LinkSwitchReasonNoSystemd
	case kv["linger"] != "1":
		return proto.LinkSwitchReasonLingerOff
	case r.rec.LinkExisted:
		return proto.LinkSwitchReasonExistingLink
	case kv["custom"] == "1":
		return proto.LinkSwitchReasonCustomNode
	case !r.rec.NodeUnitExisted && kv["node_bin"] != "1":
		return proto.LinkSwitchReasonNoNode
	}
	return ""
}

// fileSHA256 is the hex sha-256 of the file at path.
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// stage sends the build at path, gzipped, to the staged file and has it checked there.
func (r *linkSwitchRun) stage(ctx context.Context, path string) bool {
	f, err := os.Open(path)
	if err != nil {
		r.logf("open the build: %v", err)
		return false
	}
	defer f.Close()
	pr, pw := io.Pipe()
	// Closed once ssh is done, so the writer below never blocks on a reader that is gone (deploy.go, #2377).
	defer pr.Close()
	go func() {
		zw, _ := gzip.NewWriterLevel(pw, gzip.BestSpeed)
		_, err := io.Copy(zw, f)
		if err == nil {
			err = zw.Close()
		}
		pw.CloseWithError(err)
	}()
	exit, out := r.ssh(ctx, "stage", lsStageScript(r.rec.Operation, r.rec.SHA256), pr, r.t.Transfer)
	return exit == 0 && outputOf(out)["staged"] == "1"
}

// removeStaged removes the staged build, if any (a failure before the hub's begin; best effort).
func (r *linkSwitchRun) removeStaged(ctx context.Context) {
	r.ssh(ctx, "remove staged", lsUninstallScript(r.rec.Operation, "", false, false), nil, r.t.Uninstall)
}

// lsBeginRefusal is the reason the apps are told for each refusal of the hub's begin.
var lsBeginRefusal = map[string]string{
	devicewire.LinkSwitchCodeNotSSH:       proto.LinkSwitchReasonConfigChanged,
	devicewire.LinkSwitchCodeChanged:      proto.LinkSwitchReasonConfigChanged,
	devicewire.LinkSwitchCodeNoMachine:    proto.LinkSwitchReasonConfigChanged,
	devicewire.LinkSwitchCodePending:      proto.LinkSwitchReasonPending,
	devicewire.LinkSwitchCodeUnavailable:  proto.LinkSwitchReasonLinkUnavailable,
	devicewire.LinkSwitchCodeExistingLink: proto.LinkSwitchReasonExistingLink,
	bus.CodeUnknownMethod:                 proto.LinkSwitchReasonHubOld,
}

// begin asks the hub to switch the entry and make the pairing code: the pairing link, or refused (why the hub
// changed nothing), or err (it may have changed it: rolled back).
func (r *linkSwitchRun) begin(ctx context.Context) (link, refused string, err error) {
	cctx, cancel := context.WithTimeout(ctx, r.t.Begin)
	defer cancel()
	var out devicewire.LinkSwitchBegun
	err = r.d.opts.Hub.Call(cctx, devicewire.MethodLinkSwitchBegin, devicewire.LinkSwitchBegin{MachineID: r.rec.MachineID,
		Operation: r.rec.Operation, SSHHost: r.rec.SSHHost, By: r.rec.By}, &out)
	var re *rpc.Error
	switch {
	case errors.As(err, &re):
		r.logf("the hub refused begin: %s", re.Code)
		return "", cmp.Or(lsBeginRefusal[re.Code], proto.LinkSwitchReasonUnknown), nil
	case errors.Is(err, rpc.ErrNotSent), errors.Is(err, rpc.ErrNotWritten), errors.Is(err, bus.ErrOffline):
		r.logf("begin did not reach the hub")
		return "", proto.LinkSwitchReasonUnknown, nil
	case err != nil:
		r.logf("begin's answer is not known: rolling back")
		return "", "", err
	case out.Link == "":
		r.logf("the hub's begin answered no pairing link: rolling back")
		return "", "", errors.New("no pairing link")
	}
	r.rec.PairHash = out.PairHash
	if !r.save(ctx) {
		r.logf("the pairing hash was not kept: a rollback after a restart revokes every code of the machine")
	}
	r.logf("the hub began the switch")
	return out.Link, "", nil
}

// install places the staged build and runs its installer with the pairing link on stdin: "" when it ran (its exit is
// in the record), else why the switch fails.
func (r *linkSwitchRun) install(ctx context.Context, link string) string {
	exit, out := r.ssh(ctx, "install", lsInstallScript(r.rec.Operation), strings.NewReader(link+"\n"), r.t.Install)
	kv := outputOf(out)
	placed, paired := kv["placed"] == "1", kv["paired"] == "1"
	switch {
	case placed:
		r.rec.Install = lsInstallPlaced
	case exit == lsExitInstallExists || exit == lsExitInstallStaged || exit == lsExitInstallPlace:
		r.rec.Install = lsInstallNotPlaced
	}
	if exit >= 0 {
		r.rec.InstallerExit = &exit
	}
	r.save(ctx)
	switch {
	case !placed && exit == lsExitInstallExists:
		return proto.LinkSwitchReasonExistingLink
	case !placed && (exit == lsExitInstallStaged || exit == lsExitInstallPlace):
		return proto.LinkSwitchReasonInstallFailed
	case placed && exit > 0 && !sshFailed(exit) && !paired:
		// The installer gave up before it paired (the hub not reachable from there, the code refused): no link comes.
		return proto.LinkSwitchReasonInstallFailed
	}
	// The installer worked, or paired and its link may come still, or ssh failed with it perhaps running: wait.
	return ""
}

// linkOnline: the machine's entry is kind link and its node answered through Persona Link.
func (d *Devices) linkOnline(id string) bool {
	m := d.cfgMachine(id)
	if m == nil || m.cfg.Kind != kindLink {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.online && m.via == kindLink && m.peer != nil
}

// sshOnline: the machine's entry is an ssh one again and its node answers.
func (d *Devices) sshOnline(id string) bool {
	m := d.cfgMachine(id)
	if m == nil || kindOf(m.cfg) != "ssh" {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.online && m.peer != nil
}

// waitFor waits until ok, or until; false when ok never held.
func (r *linkSwitchRun) waitFor(ctx context.Context, until time.Time, ok func() bool) bool {
	for {
		if ok() {
			return true
		}
		if !time.Now().Before(until) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(min(r.t.Poll, max(time.Until(until), time.Millisecond))):
		}
	}
}

func (r *linkSwitchRun) waitLink(ctx context.Context, until time.Time) bool {
	return r.waitFor(ctx, until, func() bool { return r.d.linkOnline(r.rec.MachineID) })
}

// succeed: the computer is on Persona Link. The hub's end(succeeded) is asked a few times; it failing undoes nothing.
func (r *linkSwitchRun) succeed(ctx context.Context) {
	r.logf("connected through Persona Link")
	if !r.end(ctx, devicewire.LinkSwitchSucceeded, r.t.Succeed) {
		r.logf("the hub's end(succeeded) did not answer: the switch stays done")
	}
	r.finish(ctx, proto.LinkSwitchPhaseSucceeded, "", false)
}

// end tells the hub how the switch ended, again until it answers or within passes; true once it did. The hub revokes
// this switch's code and credential before it looks at the entry, so not_ssh (the entry is not kind link any more)
// leaves nothing to undo; so does no_machine (the computer went to another manager meanwhile) with the pairing hash,
// but without it the hub revoked nothing. invalid_params (a hash that names no code of the machine) is not tried
// again: it would only answer the same.
func (r *linkSwitchRun) end(ctx context.Context, outcome string, within time.Duration) bool {
	until := time.Now().Add(within)
	for {
		cctx, cancel := context.WithTimeout(ctx, max(min(r.t.Begin, time.Until(until)), time.Second))
		err := r.d.opts.Hub.Call(cctx, devicewire.MethodLinkSwitchEnd, devicewire.LinkSwitchEnd{MachineID: r.rec.MachineID,
			Operation: r.rec.Operation, PairHash: r.rec.PairHash, Outcome: outcome}, nil)
		cancel()
		var re *rpc.Error
		switch {
		case err == nil:
			return true
		case errors.As(err, &re) && (re.Code == devicewire.LinkSwitchCodeNotSSH ||
			re.Code == devicewire.LinkSwitchCodeNoMachine && r.rec.PairHash != ""):
			r.logf("end(%s): %s, nothing left on the hub", outcome, re.Code)
			return true
		case errors.As(err, &re) && (re.Code == devicewire.LinkSwitchCodeNoMachine || re.Code == bus.CodeInvalidParams):
			r.logf("end(%s) refused: %s, not asked again", outcome, re.Code)
			return false
		case errors.As(err, &re):
			r.logf("end(%s) refused: %s", outcome, re.Code)
		default:
			r.logf("end(%s) not answered", outcome)
		}
		if ctx.Err() != nil || !time.Now().Before(until) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(min(max(r.t.Poll, r.t.EndRetry/20), max(time.Until(until), time.Millisecond))):
		}
	}
}

// rollback undoes the switch that may have reached the hub's begin: the hub's end(rolled_back), then what this switch
// set up on the computer, then the computer must be back over ssh; failed with why, and rollback_unconfirmed unless
// all three were seen to work.
func (r *linkSwitchRun) rollback(ctx context.Context, why string) {
	r.rec.Error = why
	r.phase(ctx, proto.LinkSwitchPhaseRollingBack)
	r.logf("rolling back (%s)", why)
	ended := r.end(ctx, devicewire.LinkSwitchRolledBack, r.t.EndRetry)
	if ctx.Err() != nil {
		return
	}
	// Only what this switch set up: the node service when there was none and its installer may have run, Persona
	// Link when it was not there and its build may have been placed.
	mayHaveRun := r.rec.Install == lsInstallRunning || r.rec.Install == lsInstallPlaced
	node := !r.rec.NodeUnitExisted && mayHaveRun
	purge := !r.rec.LinkExisted && mayHaveRun
	exit, out := r.ssh(ctx, "uninstall", lsUninstallScript(r.rec.Operation, r.rec.SHA256, node, purge), nil, r.t.Uninstall)
	if ctx.Err() != nil {
		return
	}
	clean := exit == 0 && outputOf(out)["clean"] == "1"
	back := r.waitFor(ctx, time.Now().Add(r.t.SSHBack), func() bool { return r.d.sshOnline(r.rec.MachineID) })
	if ctx.Err() != nil {
		return
	}
	r.logf("rollback: hub %t, computer cleaned %t, back over ssh %t", ended, clean, back)
	r.finish(ctx, proto.LinkSwitchPhaseFailed, why, !(ended && clean && back))
}
