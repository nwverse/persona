package extmgr

// Start and reconnect (docs/design/machine-cloud-ops.md "Start and reconnect"): once a start was accepted, the manager
// reads cloud.instance until the instance runs, then asks the devices to connect the computer's node again, as the
// computer's own Reconnect does, until it is online. The recovery lives in memory only (wire.CloudRecovery, read with
// GET …/cloud): a restart forgets it, and the instance's state and Reconnect say the rest.

import (
	"context"
	"time"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/devices"
	"github.com/nwverse/persona/server/internal/i18n"
)

// How long a recovery waits, and how often it looks (package vars so tests run in milliseconds).
var (
	cloudStartWait      = 5 * time.Minute
	cloudReconnectWait  = 2 * time.Minute
	cloudStartPoll      = 5 * time.Second
	cloudOnlinePoll     = 2 * time.Second
	cloudReconnectEvery = 20 * time.Second
)

// cloudReconnector is the devices' Reconnect (devices.Devices.Reconnect, devices.reconnect): connect a computer's node
// again.
type cloudReconnector interface {
	Reconnect(id string) error
}

var _ cloudReconnector = (*devices.Devices)(nil)

// cloudRun is one machine's recovery: what it says now and the run it belongs to (a later one replaces it).
type cloudRun struct {
	rec    wire.CloudRecovery
	gen    uint64
	cancel context.CancelFunc // stops the run's goroutine
}

// cloudRecoveryOf is mid's recovery in progress or last ended, nil when there is none.
func (h *Manager) cloudRecoveryOf(mid wire.CloudMachineID) *wire.CloudRecovery {
	h.cloud.recMu.Lock()
	defer h.cloud.recMu.Unlock()
	if run := h.cloud.recoveries[mid]; run != nil {
		rec := run.rec
		return &rec
	}
	return nil
}

// cloudForgetRecovery drops mid's recovery (its binding changed) and stops a run still going: it asks the instance it
// followed nothing more and reconnects nothing.
func (h *Manager) cloudForgetRecovery(mid wire.CloudMachineID) {
	h.cloud.recMu.Lock()
	defer h.cloud.recMu.Unlock()
	if run := h.cloud.recoveries[mid]; run != nil && run.cancel != nil {
		run.cancel()
	}
	delete(h.cloud.recoveries, mid)
}

// cloudCurrent reports whether gen is still mid's run.
func (h *Manager) cloudCurrent(mid wire.CloudMachineID, gen uint64) bool {
	h.cloud.recMu.Lock()
	defer h.cloud.recMu.Unlock()
	run := h.cloud.recoveries[mid]
	return run != nil && run.gen == gen
}

// cloudSetRecovery records rec for mid when gen is still mid's run.
func (h *Manager) cloudSetRecovery(mid wire.CloudMachineID, gen uint64, rec wire.CloudRecovery) {
	h.cloud.recMu.Lock()
	defer h.cloud.recMu.Unlock()
	if run := h.cloud.recoveries[mid]; run != nil && run.gen == gen {
		run.rec = rec
	}
}

// cloudRecover starts following m's instance up after a start (unless a recovery is already going, which it
// answers), on a goroutine bound to the manager's run.
func (h *Manager) cloudRecover(lang i18n.Lang, m *Machine, b wire.CloudBinding, t cloudTarget) wire.CloudRecovery {
	mid := wire.CloudMachineID(m.cfg.ID)
	h.cloud.recMu.Lock()
	if run := h.cloud.recoveries[mid]; run != nil && (run.rec.Phase == wire.CloudRecoveryPhaseStarting || run.rec.Phase == wire.CloudRecoveryPhaseReconnecting) {
		rec := run.rec
		h.cloud.recMu.Unlock()
		return rec
	}
	if h.cloud.recoveries == nil {
		h.cloud.recoveries = map[wire.CloudMachineID]*cloudRun{}
	}
	if old := h.cloud.recoveries[mid]; old != nil && old.cancel != nil {
		old.cancel() // ended already; its context goes with it
	}
	parent := h.ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	h.cloud.gen++
	run := &cloudRun{rec: wire.CloudRecovery{Phase: wire.CloudRecoveryPhaseStarting, StartedAt: time.Now().UTC()}, gen: h.cloud.gen, cancel: cancel}
	h.cloud.recoveries[mid] = run
	rec, gen := run.rec, run.gen
	h.cloud.recMu.Unlock()
	go h.cloudFollow(ctx, lang, m, b, t, gen, rec)
	return rec
}

// cloudFollow runs one recovery to done or failed.
func (h *Manager) cloudFollow(ctx context.Context, lang i18n.Lang, m *Machine, b wire.CloudBinding, t cloudTarget, gen uint64, rec wire.CloudRecovery) {
	mid := wire.CloudMachineID(m.cfg.ID)
	fail := func(err error) {
		rec.Phase, rec.Error = wire.CloudRecoveryPhaseFailed, wire.CloudMessage(i18n.Localize(err, lang))
		h.cloudSetRecovery(mid, gen, rec)
		h.logf("cloud: start-and-reconnect of %s failed", mid)
	}
	machine := i18n.Args{"machine": m.name()}
	ref := wire.CloudInstanceRef{Account: b.Account, ID: b.Instance}
	deadline := time.Now().Add(cloudStartWait)
	var inst wire.CloudInstance
	for {
		if !h.cloudCurrent(mid, gen) {
			return // forgotten: its binding changed
		}
		got, f := h.cloudInstance(ctx, lang, t, ref, true)
		if f == nil {
			if got.State == wire.CloudInstanceStateRunning {
				inst = got
				break
			}
			if got.State == wire.CloudInstanceStateTerminated {
				fail(cloudErr("recovery_gone"))
				return
			}
		}
		if time.Now().After(deadline) {
			fail(cloudErr("recovery_start_timeout"))
			return
		}
		if !cloudSleep(ctx, cloudStartPoll) {
			fail(cloudErr("recovery_stopped"))
			return
		}
	}
	if !h.cloudCurrent(mid, gen) {
		return
	}
	rec.Phase = wire.CloudRecoveryPhaseReconnecting
	rec.IpChanged = len(inst.PublicIps) > 0 && b.PublicIp != "" && inst.PublicIps[0] != b.PublicIp
	h.cloudSetRecovery(mid, gen, rec)
	h.cloudNoteSeen(ctx, []cloudSeenNote{{machineID: mid, p: t.p, inst: inst}})
	rc, ok := h.opts.Devices.(cloudReconnector)
	if !ok {
		fail(cloudErr("recovery_no_reconnect", machine))
		return
	}
	deadline = time.Now().Add(cloudReconnectWait)
	var asked time.Time
	for {
		if !h.cloudCurrent(mid, gen) {
			return
		}
		if h.cloudOnline(mid) {
			rec.Phase = wire.CloudRecoveryPhaseDone
			h.cloudSetRecovery(mid, gen, rec)
			h.logf("cloud: start-and-reconnect of %s done", mid)
			return
		}
		if time.Now().After(deadline) {
			fail(cloudErr("recovery_offline", machine))
			return
		}
		if time.Since(asked) >= cloudReconnectEvery {
			if err := rc.Reconnect(string(mid)); err != nil {
				h.logf("cloud: reconnecting %s: %v", mid, err)
			}
			asked = time.Now()
		}
		if !cloudSleep(ctx, cloudOnlinePoll) {
			fail(cloudErr("recovery_stopped"))
			return
		}
	}
}

// cloudOnline reports whether the devices list mid as online now.
func (h *Manager) cloudOnline(mid wire.CloudMachineID) bool {
	m := h.machineByID(string(mid))
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.online
}

// cloudSleep waits d, false when ctx ended first.
func cloudSleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
