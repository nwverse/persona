package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"

	"github.com/nwverse/persona/server/internal/bus"
	"github.com/nwverse/persona/server/internal/devicewire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/rpc"
)

// Switching an ssh computer to Persona Link (#10662, docs/operations/link.md "Switching from SSH"). The manager that
// connects the computer runs the switch over its own ssh access; the hub does the two steps only it may do
// (devicewire/link_switch.go):
//
//   - begin: the entry is still what the manager saw (kind ssh, the same ssh_host), the user confirmed the computer,
//     the hub takes Persona Link, and the computer has no link credential or open pairing code. The hub sets the
//     entry's kind to link, every other key as it was (hub.json's, or the manager's own list for one it keeps), takes
//     it at once as a rename is taken (no restart waits for it), and makes the computer's pairing code. Only the
//     manager gets the code, and only to hand it to the computer; it is in no log line, audit row or error.
//   - end: succeeded keeps it all; rolled_back revokes that code and the credential it became (by the code's hash;
//     all of the computer's when the manager has no hash: begin refused a computer that had any), closes what they
//     opened, and puts the kind back to ssh. Either one, repeated, answers the same.
//
// The computer keeps its id, so all that is kept by id (its name, pins, mutes, history, trust) stays. While a switch
// runs, the new credential does not rename the computer after its system (nameAfterSystem).

// linkSwitches are the switches begun and not ended, by machine id, and the last one of each machine that succeeded.
// Kept in memory only: a hub that restarted has none (the audit has what a rollback must know), and a credential made
// then names the computer as any pairing would.
type linkSwitches struct {
	mu        sync.Mutex
	run       map[string]linkSwitchRun
	succeeded map[string]string // machine id -> operation
}

// linkSwitchRun is one switch under way: the manager's id for it, and who asked (device | admin).
type linkSwitchRun struct{ operation, by string }

func (s *linkSwitches) start(mid string, r linkSwitchRun) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.run == nil {
		s.run = map[string]linkSwitchRun{}
	}
	s.run[mid] = r
}

func (s *linkSwitches) stop(mid string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.run, mid)
}

// succeed records that operation op of computer mid succeeded.
func (s *linkSwitches) succeed(mid, op string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.succeeded == nil {
		s.succeeded = map[string]string{}
	}
	s.succeeded[mid] = op
}

// succeededOp: operation op of computer mid succeeded, as this hub saw it.
func (s *linkSwitches) succeededOp(mid, op string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return op != "" && s.succeeded[mid] == op
}

// get is computer mid's switch under way (ok false: none).
func (s *linkSwitches) get(mid string) (linkSwitchRun, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.run[mid]
	return r, ok
}

// What an entry's compare-and-swap finds instead of what it expected (swapMachineKind).
var (
	errSwapKind = errors.New("the computer's entry is not of the kind expected")
	errSwapHost = errors.New("the computer's ssh_host is not the one expected")
	// errSwapDone: the entry already is what the swap would make it; nothing is written.
	errSwapDone = errors.New("the computer's entry already has that kind")
)

func linkSwitchRefusal(code, msg string) *rpc.Error { return &rpc.Error{Code: code, Message: msg} }

// linkSwitchNoMachine: the computer is none of this manager's (gone, or another manager's now).
func linkSwitchNoMachine() *rpc.Error {
	return linkSwitchRefusal(devicewire.LinkSwitchCodeNoMachine, "no such computer of this manager")
}

func init() {
	handleBus(devicewire.MethodLinkSwitchBegin, func(h *Hub, ctx context.Context, c *busConn, params json.RawMessage) (any, error) {
		var p devicewire.LinkSwitchBegin
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpc.Error{Code: bus.CodeInvalidParams, Message: devicewire.MethodLinkSwitchBegin + ": " + err.Error()}
		}
		begun, err := h.beginLinkSwitch(ctx, c, p)
		if err != nil {
			return nil, err
		}
		return begun, nil
	})
	handleBus(devicewire.MethodLinkSwitchEnd, func(h *Hub, ctx context.Context, c *busConn, params json.RawMessage) (any, error) {
		var p devicewire.LinkSwitchEnd
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpc.Error{Code: bus.CodeInvalidParams, Message: devicewire.MethodLinkSwitchEnd + ": " + err.Error()}
		}
		return h.endLinkSwitch(ctx, c, p)
	})
}

// beginLinkSwitch is devices.link_switch.begin from manager c.
func (h *Hub) beginLinkSwitch(ctx context.Context, c *busConn, p devicewire.LinkSwitchBegin) (*devicewire.LinkSwitchBegun, error) {
	mid, run := p.MachineID, linkSwitchRun{operation: clip(p.Operation, 64), by: clip(p.By, 16)}
	h.machineAdd.Lock() // as a rename: the entry is read and written whole, one change at a time
	defer h.machineAdd.Unlock()
	if err := h.linkSwitchReady(ctx, c, p); err != nil {
		return nil, err
	}
	err := h.swapMachineKind(ctx, mid, kindLink, func(mc MachineConfig) error {
		switch {
		case kindOf(mc) != "ssh":
			return errSwapKind
		case mc.SSHHost != p.SSHHost:
			return errSwapHost
		}
		return nil
	})
	if err != nil {
		return nil, linkSwitchSwapErr(err)
	}
	h.linkSwitch.start(mid, run)
	// Its begun row before its code: a rollback after this hub restarted (its memory of the switch gone) without the
	// code's hash still finds that this begin took effect (endLinkSwitch).
	if err := h.linkSwitchAuditNow(ctx, c, mid, run, "begun"); err != nil {
		h.undoLinkSwitchBegin(ctx, mid, run)
		return nil, fmt.Errorf("the switch's audit: %w", err)
	}
	code, err := h.newPairCode(ctx, mid)
	if err != nil {
		h.undoLinkSwitchBegin(ctx, mid, run)
		h.linkSwitchAudit(ctx, c, mid, run, "begin_failed", nil)
		return nil, fmt.Errorf("the computer's pairing code: %w", err)
	}
	h.logf("[%s] link switch %s begun (asked by %s, through %s)", mid, run.operation, run.by, c.managerID())
	return &devicewire.LinkSwitchBegun{Link: code.PairURL, PairHash: hashToken(code.Token)}, nil
}

// undoLinkSwitchBegin puts a begin that could not finish back to ssh. Its switch is kept when that failed too, so that
// the manager's rollback (it was told begin failed) finds it and tries again. h.machineAdd is held.
func (h *Hub) undoLinkSwitchBegin(ctx context.Context, mid string, run linkSwitchRun) {
	if err := h.swapMachineKind(context.WithoutCancel(ctx), mid, "ssh", linkSwitchBack); err != nil && !errors.Is(err, errSwapDone) {
		h.logf("[%s] link switch %s: its begin failed, and its kind could not be put back to ssh: %v", mid, run.operation, err)
		return
	}
	h.linkSwitch.stop(mid)
}

// linkSwitchReady refuses a begin the hub cannot do now (devicewire's LinkSwitchCode*). h.machineAdd is held.
func (h *Hub) linkSwitchReady(ctx context.Context, c *busConn, p devicewire.LinkSwitchBegin) error {
	mid := p.MachineID
	if mid == "" || h.ownsMachine(c, mid) != nil {
		return linkSwitchNoMachine()
	}
	mc := h.knownEntry(mid)
	switch {
	case mc == nil:
		return linkSwitchNoMachine()
	case kindOf(*mc) != "ssh":
		return linkSwitchRefusal(devicewire.LinkSwitchCodeNotSSH, "the computer is not reached over ssh")
	case mc.SSHHost != p.SSHHost:
		return linkSwitchRefusal(devicewire.LinkSwitchCodeChanged, "the computer's entry changed")
	case h.machineUntrusted(mid):
		return linkSwitchRefusal(devicewire.LinkSwitchCodePending, i18n.T(h.userLang(), "hub-machine_trust.pending"))
	case !h.linkPairable():
		return linkSwitchRefusal(devicewire.LinkSwitchCodeUnavailable, i18n.T(h.userLang(), "hub-pairing.tls_required"))
	case h.store == nil:
		return errors.New("no database")
	}
	inUse, err := h.store.MachineLinkInUse(ctx, mid)
	if err != nil {
		return err
	}
	if inUse {
		return linkSwitchRefusal(devicewire.LinkSwitchCodeExistingLink, "the computer has a Persona Link credential or pairing code already")
	}
	return nil
}

// linkSwitchBack is end's compare-and-swap: kind link goes back to ssh; ssh is so already.
func linkSwitchBack(mc MachineConfig) error {
	switch kindOf(mc) {
	case kindLink:
		return nil
	case "ssh":
		return errSwapDone
	}
	return errSwapKind
}

// linkSwitchSwapErr is what a refused compare-and-swap answers the manager.
func linkSwitchSwapErr(err error) error {
	var re *rpc.Error
	switch {
	case errors.Is(err, errNoMachine), errors.As(err, &re) && re.Code == devicewire.CodeNoDevice:
		return linkSwitchNoMachine()
	case errors.Is(err, errSwapKind):
		return linkSwitchRefusal(devicewire.LinkSwitchCodeNotSSH, "the computer's entry is not of the kind expected")
	case errors.Is(err, errSwapHost):
		return linkSwitchRefusal(devicewire.LinkSwitchCodeChanged, "the computer's entry changed")
	}
	return err
}

// endLinkSwitch is devices.link_switch.end from manager c. A rollback undoes only a switch whose begin took effect:
// the one this hub has under way with that operation, or one whose begun row the audit has (this hub restarted
// since), or the one whose code the hash names. Any other changes nothing: begin was refused, never reached this
// hub, or this is not a switch at all (the computer's own Persona Link stays). One the hub saw succeed is not undone
// either (already_succeeded): a manager's stale record does not take a working Persona Link away.
func (h *Hub) endLinkSwitch(ctx context.Context, c *busConn, p devicewire.LinkSwitchEnd) (any, error) {
	mid, op := p.MachineID, clip(p.Operation, 64)
	run, running := h.linkSwitch.get(mid)
	running = running && op != "" && run.operation == op // this switch, as this hub saw it begin
	audited := false                                     // its begun row is in the audit
	if p.Outcome == devicewire.LinkSwitchRolledBack && mid != "" && op != "" {
		succeeded := h.linkSwitch.succeededOp(mid, op)
		if !succeeded && h.store != nil {
			var err error
			if audited, succeeded, err = h.store.LinkSwitchSteps(ctx, mid, op); err != nil {
				return nil, err
			}
		}
		if succeeded {
			return nil, linkSwitchRefusal(devicewire.LinkSwitchCodeSucceeded, "this switch succeeded; nothing is undone")
		}
	}
	if mid == "" || h.ownsMachine(c, mid) != nil {
		// Not this manager's (any more: moved to another meanwhile). A rollback that names its code still revokes
		// that code and what it became, which only the manager that began it can name; never all of a computer's.
		if mid != "" && p.Outcome == devicewire.LinkSwitchRolledBack && p.PairHash != "" && h.store != nil {
			id, _, err := h.store.RevokeLinkPair(ctx, p.PairHash, mid)
			if err != nil {
				return nil, err
			}
			if id != "" {
				h.revoked(id)
			}
		}
		if running {
			h.linkSwitch.stop(mid)
		}
		return nil, linkSwitchNoMachine()
	}
	run.operation = op
	switch p.Outcome {
	case devicewire.LinkSwitchSucceeded:
		if running {
			h.linkSwitch.stop(mid)
		}
		if op != "" {
			h.linkSwitch.succeed(mid, op)
		}
		// A later rollback of it goes by this row once this hub restarted: its failure is the manager's to retry.
		if err := h.linkSwitchAuditNow(ctx, c, mid, run, "succeeded"); err != nil {
			return nil, fmt.Errorf("the switch's audit: %w", err)
		}
		return map[string]any{}, nil
	case devicewire.LinkSwitchRolledBack:
	default:
		return nil, &rpc.Error{Code: bus.CodeInvalidParams, Message: devicewire.MethodLinkSwitchEnd + ": unknown outcome"}
	}
	h.machineAdd.Lock()
	defer h.machineAdd.Unlock()
	begun, revoked, err := h.revokeLinkSwitch(ctx, mid, p.PairHash, running || audited)
	if err != nil {
		return nil, err
	}
	if !begun {
		h.logf("[%s] link switch %s rolled back before its begin took effect: nothing to undo", mid, op)
		return map[string]any{}, nil
	}
	if err := h.swapMachineKind(ctx, mid, "ssh", linkSwitchBack); err != nil && !errors.Is(err, errSwapDone) {
		return nil, linkSwitchSwapErr(err)
	}
	if running {
		h.linkSwitch.stop(mid)
	}
	h.linkSwitchAudit(ctx, c, mid, run, "rolled_back", map[string]any{"credentials_revoked": revoked})
	h.logf("[%s] link switch %s rolled back: %d credentials revoked, back to ssh", mid, op, revoked)
	return map[string]any{}, nil
}

// revokeLinkSwitch revokes what a switch of computer mid made, once it shows that switch's begin took effect (begun
// false: it did not, nothing done): the pairing code whose hash is pairHash and the credential it became; else (no
// hash) every Persona Link credential of the computer and its codes not used yet, while its entry is kind link. It
// closes what they have open (revoked); the computer's share records stay, as it does, on ssh. proven: this hub has
// the switch under way, or the audit its begun row. How many credentials.
func (h *Hub) revokeLinkSwitch(ctx context.Context, mid, pairHash string, proven bool) (begun bool, n int, err error) {
	if h.store == nil {
		return false, 0, errors.New("no database")
	}
	if pairHash != "" {
		id, found, err := h.store.RevokeLinkPair(ctx, pairHash, mid)
		if err != nil {
			return false, 0, err
		}
		if found {
			if id == "" {
				return true, 0, nil
			}
			h.revoked(id)
			return true, 1, nil
		}
		if !proven {
			return false, 0, nil
		}
		// No code of this computer: one that began says it made is not that one.
		return false, 0, &rpc.Error{Code: bus.CodeInvalidParams, Message: "pair_hash is no pairing code of this computer"}
	}
	if !proven {
		return false, 0, nil
	}
	if mc := h.knownEntry(mid); mc == nil || mc.Kind != kindLink {
		return true, 0, nil // not on Persona Link (any more): what it has of one is not this switch's
	}
	cr, _ := h.store.(credentialRevoker)
	if cr == nil {
		return false, 0, errors.New("this store cannot revoke a computer's credentials")
	}
	ids, err := cr.RevokeMachineCredentials(ctx, mid)
	if err != nil {
		return false, 0, err
	}
	for _, id := range ids {
		h.revoked(id)
	}
	return true, len(ids), nil
}

// linkSwitchAudit writes one row of the computer's audit: the switch, its step, and who asked; never its code.
func (h *Hub) linkSwitchAudit(ctx context.Context, c *busConn, mid string, run linkSwitchRun, step string, more map[string]any) {
	h.machineAudit(ctx, mid, "link_switch", "manager:"+c.managerID(), linkSwitchDetail(run, step, more))
}

// linkSwitchAuditNow is linkSwitchAudit for a row a later step goes by (begun): its failure is the caller's. A store
// that keeps no audit keeps none.
func (h *Hub) linkSwitchAuditNow(ctx context.Context, c *busConn, mid string, run linkSwitchRun, step string) error {
	ts := h.trustStore()
	if ts == nil {
		return nil
	}
	return ts.AddMachineAudit(context.WithoutCancel(ctx), mid, "link_switch", "manager:"+c.managerID(), linkSwitchDetail(run, step, nil))
}

func linkSwitchDetail(run linkSwitchRun, step string, more map[string]any) json.RawMessage {
	detail := map[string]any{"operation": run.operation, "step": step}
	if run.by != "" {
		detail["by"] = run.by
	}
	for k, v := range more {
		detail[k] = v
	}
	b, _ := json.Marshal(detail)
	return b
}

// linkSwitchRoute is the apps' start of a switch (devicewire.Routes): a signed-in full device's only (deviceRoutesRelayed).
const linkSwitchRoute = "POST /api/v1/machines/{mid}/link-switch"

// errLinkSwitchInProgress: the computer is switching to Persona Link; it is neither moved to another manager nor
// removed until the switch ends (its manager runs it, and undoes it when it fails).
var errLinkSwitchInProgress = refuseAdd(http.StatusConflict, "link_switch_in_progress", i18n.Err("hub-link_switch.in_progress"))

// linkSwitchBusy is errLinkSwitchInProgress while computer mid switches, else nil.
func (h *Hub) linkSwitchBusy(mid string) error {
	if _, ok := h.linkSwitch.get(mid); ok {
		return errLinkSwitchInProgress
	}
	return nil
}

// swapMachineKind sets computer mid's entry kind to kind, every other key as it was, when check (given the entry as
// it is now) says yes: in its manager's own list for one it keeps (machines_kept.go), else in hub.json, then in the
// running hub at once (kindLive). check's error is the answer, nothing written. h.machineAdd is held.
func (h *Hub) swapMachineKind(ctx context.Context, mid, kind string, check func(MachineConfig) error) error {
	decoded := func(raw json.RawMessage) error {
		var mc MachineConfig
		if err := json.Unmarshal(raw, &mc); err != nil {
			return err
		}
		return check(mc)
	}
	if kept, err := h.editKeptMachineIf(ctx, mid, decoded, map[string]any{"kind": kind}); kept {
		if err == nil {
			h.broadcastMachines() // its manager replaces the machine; its next report shows it so
		}
		return err
	}
	err := editHubJSON(h.cfg.dir, func(o *jsonObject) error {
		list, err := machineEntries(o)
		if err != nil {
			return err
		}
		for _, raw := range list {
			if machineEntryID(raw) == mid {
				if err := decoded(raw); err != nil {
					return err
				}
				return setMachineField(o, mid, "kind", kind)
			}
		}
		return errNoMachine
	})
	if err != nil {
		return err
	}
	h.kindLive(mid, kind)
	return nil
}

// kindLive takes a hub.json computer's new kind into the running hub, as renameMachineLive a name: the entry it hands
// the manager (which connects it the new way now), and so what the apps see once the manager reports it. No restart
// waits for it (pendingRestart).
func (h *Hub) kindLive(mid, kind string) {
	h.editEntryLive(mid, func(mc *MachineConfig) { mc.Kind = kind })
	h.devicesConfigChanged()
	h.broadcastMachines() // and the mesh record
}

// adminLinkSwitchRoutes: the admin page's Machines switches a computer to Persona Link. The manager that connects it
// answers, as for a device's POST /api/v1/machines/{mid}/link-switch (devicewire.Routes), told that a signed-in admin
// session asks (assistantwire.AdminHeader). mux is the admin's signed-in routes: the session and the write's CSRF
// token are checked before (admin.go).
func (h *Hub) adminLinkSwitchRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/admin/machines/{mid}/link-switch", func(w http.ResponseWriter, r *http.Request) {
		mid := r.PathValue("mid")
		_ = h.waitDevices(r.Context()) // the hub just started: the manager is on its way (bounded), and its machines
		if h.localMachine(mid) == nil {
			writeErr(w, http.StatusNotFound, i18n.Err("devices-api.unknown_machine"))
			return
		}
		r = r.Clone(r.Context())
		r.URL.Path = "/api/v1/machines/" + mid + "/link-switch"
		r.URL.RawPath = "/api/v1/machines/" + url.PathEscape(mid) + "/link-switch"
		r.Header.Del("X-Persona-CSRF") // the admin session's own, not the manager's business
		h.forwardAdminHTTP(w, r, h.machineOwner(mid))
	})
}
