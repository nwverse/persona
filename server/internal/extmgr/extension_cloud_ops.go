package extmgr

// A computer's cloud host (docs/design/machine-cloud-ops.md "Binding a computer to an instance", "Actions and safety
// levels", "Audit"): the bindings row, binding and unbinding, actions behind the hub's gate, snapshots and the audit.
// The HTTP handlers and the assistant's tools share these functions, so every gate and every audit row is written in
// one place.

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/extension"
	"github.com/nwverse/persona/server/internal/i18n"
)

// cloudBindingsKey is the manager's settings row holding every binding (wire.CloudBindings).
const cloudBindingsKey = "machine_cloud_bindings"

// The audit's events (plugin_audit, by the provider's extension id).
const (
	auditCloudBind   = "cloud_bind"
	auditCloudUnbind = "cloud_unbind"
	auditCloudAction = "cloud_action"

	cloudOutcomeDone     = "done"
	cloudOutcomeAccepted = "accepted"
	cloudOutcomeRefused  = "refused"
	cloudOutcomeFailed   = "failed"
)

// cloudSeenNote is what was just read of a bound instance, to be kept in its binding.
type cloudSeenNote struct {
	machineID wire.CloudMachineID
	p         cloudProvider
	inst      wire.CloudInstance
}

func cloudStoreFailure(err error) *cloudFailure {
	if errors.Is(err, errCloudNoDatabase) {
		return cloudFail(http.StatusServiceUnavailable, codeCloudNoDatabase, cloudErr("no_database"))
	}
	return cloudFail(http.StatusServiceUnavailable, codeCloudNoDatabase, cloudErr("store_failed"))
}

var errCloudNoDatabase = errors.New("no database")

func nonNilBindings(list []wire.CloudBinding) []wire.CloudBinding {
	if list == nil {
		return []wire.CloudBinding{}
	}
	return list
}

// cloudReadBindings is the row as stored; none without a database. Called with h.cloud.mu held.
func (h *Manager) cloudReadBindings(ctx context.Context) ([]wire.CloudBinding, error) {
	if h.store == nil {
		return nil, nil
	}
	var row wire.CloudBindings
	if _, err := h.store.Setting(ctx, cloudBindingsKey, &row); err != nil {
		return nil, err
	}
	return row.Bindings, nil
}

// cloudWriteBindings replaces the row. Called with h.cloud.mu held.
func (h *Manager) cloudWriteBindings(ctx context.Context, list []wire.CloudBinding) error {
	if h.store == nil {
		return errCloudNoDatabase
	}
	return h.store.PutSetting(context.WithoutCancel(ctx), cloudBindingsKey, wire.CloudBindings{Bindings: nonNilBindings(list)})
}

// cloudBindings is every binding, dropping (and forgetting) those of computers the devices no longer list. A list
// with no computer at all is not taken as every computer removed (the devices may not have loaded yet).
func (h *Manager) cloudBindings(ctx context.Context) ([]wire.CloudBinding, error) {
	h.cloud.mu.Lock()
	defer h.cloud.mu.Unlock()
	list, err := h.cloudReadBindings(ctx)
	if err != nil || len(list) == 0 {
		return list, err
	}
	machines := h.machinesNow()
	if len(machines) == 0 {
		return list, nil
	}
	listed := map[wire.CloudMachineID]bool{}
	for _, m := range machines {
		listed[wire.CloudMachineID(m.cfg.ID)] = true
	}
	kept := slices.DeleteFunc(slices.Clone(list), func(b wire.CloudBinding) bool { return !listed[b.MachineID] })
	if len(kept) != len(list) {
		if err := h.cloudWriteBindings(ctx, kept); err != nil {
			h.logf("cloud bindings: forgetting removed computers: %v", err)
		} else {
			h.logf("cloud bindings: forgot %d removed computers", len(list)-len(kept))
		}
	}
	return kept, nil
}

// cloudBindingOf is mid's binding, nil when it has none.
func (h *Manager) cloudBindingOf(ctx context.Context, mid wire.CloudMachineID) (*wire.CloudBinding, error) {
	list, err := h.cloudBindings(ctx)
	if err != nil {
		return nil, err
	}
	if i := slices.IndexFunc(list, func(b wire.CloudBinding) bool { return b.MachineID == mid }); i >= 0 {
		b := list[i]
		return &b, nil
	}
	return nil, nil
}

// cloudBindingFor is the binding of p's instance (account, id) in list, nil when no computer holds it. p.extension is
// the registry's canonical id; a binding keeps the id that was canonical when it was made, so a renamed extension's
// bindings are matched through its aliases.
func (h *Manager) cloudBindingFor(list []wire.CloudBinding, p cloudProvider, account wire.CloudAccountID, id wire.CloudInstanceID) *wire.CloudBinding {
	i := slices.IndexFunc(list, func(b wire.CloudBinding) bool {
		return h.cloudCanonical(b.Extension) == p.extension && b.Provider == p.contribution && b.Account == account && b.Instance == id
	})
	if i < 0 {
		return nil
	}
	return &list[i]
}

// cloudCanonical is ext's current id in the registry (ext itself when it names no renamed extension).
func (h *Manager) cloudCanonical(ext string) string {
	if h.extensions == nil || h.extensions.reg == nil {
		return ext
	}
	return h.extensions.reg.CanonicalID(ext)
}

// cloudSeen is b with what was just read of its instance. A read without a public address keeps the last one: an
// instance stopped or taken back has none, and the start-and-reconnect compares the address it comes back with against
// the one it last had (ip_changed).
func cloudSeen(b wire.CloudBinding, p cloudProvider, inst wire.CloudInstance, now time.Time) wire.CloudBinding {
	b.ProviderName, b.Vendor, b.DisplayName, b.State, b.SeenAt = p.name, p.vendor, inst.DisplayName, inst.State, now
	if len(inst.PublicIps) > 0 {
		b.PublicIp = inst.PublicIps[0]
	}
	return b
}

// cloudNoteSeen keeps what was just read of bound instances in their bindings, writing the row only when something
// other than the time changed or the last read is a minute old.
func (h *Manager) cloudNoteSeen(ctx context.Context, notes []cloudSeenNote) {
	if len(notes) == 0 || h.store == nil {
		return
	}
	h.cloud.mu.Lock()
	defer h.cloud.mu.Unlock()
	list, err := h.cloudReadBindings(ctx)
	if err != nil {
		return
	}
	now, changed := time.Now().UTC(), false
	for _, n := range notes {
		for i, b := range list {
			if b.MachineID != n.machineID || b.Account != n.inst.Account || b.Instance != n.inst.ID || b.Provider != n.p.contribution {
				continue
			}
			next := cloudSeen(b, n.p, n.inst, now)
			old := b
			old.SeenAt = next.SeenAt
			if old != next || now.Sub(b.SeenAt) > time.Minute {
				list[i], changed = next, true
			}
		}
	}
	if changed {
		if err := h.cloudWriteBindings(ctx, list); err != nil {
			h.logf("cloud bindings: keeping the last read: %v", err)
		}
	}
}

// cloudAudit records a cloud write in plugin_audit by actor, with ids and the outcome only.
func (h *Manager) cloudAudit(ctx context.Context, event, actor, ext string, mid wire.CloudMachineID, provider wire.CloudProviderID,
	instance wire.CloudInstanceID, action string, safety wire.CloudSafety, outcome string) {
	detail := map[string]any{"machine_id": string(mid), "provider": string(provider), "instance": string(instance), "outcome": outcome}
	if action != "" {
		detail["action"], detail["safety"] = action, string(safety)
	}
	h.audit(ctx, ext, event, actor, detail)
}

// cloudMachineView is mid's binding and, when its provider answers, its host now (wire.CloudMachine). A provider that
// cannot be asked or fails is the answer's problem; the binding still stands.
func (h *Manager) cloudMachineView(ctx context.Context, lang i18n.Lang, m *Machine, refresh bool) (wire.CloudMachine, *cloudFailure) {
	mid := wire.CloudMachineID(m.cfg.ID)
	b, err := h.cloudBindingOf(ctx, mid)
	if err != nil {
		return wire.CloudMachine{}, cloudStoreFailure(err)
	}
	if b == nil {
		return wire.CloudMachine{Bound: false}, nil
	}
	out := wire.CloudMachine{Bound: true, Binding: b, Recovery: h.cloudRecoveryOf(mid)}
	t, f := h.cloudTargetOf(b.Extension, b.Provider)
	if f == nil {
		var inst wire.CloudInstance
		if inst, f = h.cloudInstance(ctx, lang, t, wire.CloudInstanceRef{Account: b.Account, ID: b.Instance}, refresh); f == nil {
			host := cloudHostOf(t.p, inst, mid)
			out.Host = &host
			h.cloudNoteSeen(ctx, []cloudSeenNote{{machineID: mid, p: t.p, inst: inst}})
			seen := cloudSeen(*b, t.p, inst, time.Now().UTC())
			out.Binding = &seen
			return out, nil
		}
	}
	name := cmp.Or(t.p.name, b.ProviderName, string(b.Provider))
	pr := cloudProblem(b.Extension, b.Provider, name, f, lang)
	out.Problem = &pr
	return out, nil
}

// cloudBind binds m to the instance in, which its provider must answer now and no other computer may hold.
func (h *Manager) cloudBind(ctx context.Context, lang i18n.Lang, actor string, m *Machine, in wire.CloudBindRequest) (wire.CloudMachine, *cloudFailure) {
	if h.store == nil {
		return wire.CloudMachine{}, cloudStoreFailure(errCloudNoDatabase)
	}
	t, f := h.cloudTargetOf(in.Extension, in.Provider)
	if f != nil {
		return wire.CloudMachine{}, f
	}
	inst, f := h.cloudInstance(ctx, lang, t, wire.CloudInstanceRef{Account: in.Account, ID: in.Instance}, true)
	if f != nil {
		return wire.CloudMachine{}, f
	}
	mid := wire.CloudMachineID(m.cfg.ID)
	h.cloud.mu.Lock()
	list, err := h.cloudReadBindings(ctx)
	if err != nil {
		h.cloud.mu.Unlock()
		return wire.CloudMachine{}, cloudStoreFailure(err)
	}
	if other := h.cloudBindingFor(list, t.p, in.Account, in.Instance); other != nil && other.MachineID != mid {
		h.cloud.mu.Unlock()
		f := cloudFail(http.StatusConflict, codeCloudAlreadyBound, cloudErr("already_bound", i18n.Args{"machine": h.machineName(string(other.MachineID))}))
		f.machineID = other.MachineID
		return wire.CloudMachine{}, f
	}
	now := time.Now().UTC()
	b := cloudSeen(wire.CloudBinding{MachineID: mid, Extension: t.p.extension, Provider: t.p.contribution, Account: in.Account,
		Instance: in.Instance, BoundAt: now}, t.p, inst, now)
	list = slices.DeleteFunc(list, func(o wire.CloudBinding) bool { return o.MachineID == mid })
	list = append(list, b)
	err = h.cloudWriteBindings(ctx, list)
	h.cloud.mu.Unlock()
	if err != nil {
		return wire.CloudMachine{}, cloudStoreFailure(err)
	}
	h.cloudForgetRecovery(mid)
	h.cloudAudit(ctx, auditCloudBind, actor, t.p.extension, mid, t.p.contribution, in.Instance, "", "", cloudOutcomeDone)
	h.logf("cloud: %s bound to an instance of %s/%s", mid, t.p.extension, t.p.contribution)
	host := cloudHostOf(t.p, inst, mid)
	return wire.CloudMachine{Bound: true, Binding: &b, Host: &host}, nil
}

// cloudUnbind drops m's binding (none is not an error).
func (h *Manager) cloudUnbind(ctx context.Context, actor string, m *Machine) (wire.CloudMachine, *cloudFailure) {
	if h.store == nil {
		return wire.CloudMachine{}, cloudStoreFailure(errCloudNoDatabase)
	}
	mid := wire.CloudMachineID(m.cfg.ID)
	h.cloud.mu.Lock()
	list, err := h.cloudReadBindings(ctx)
	if err != nil {
		h.cloud.mu.Unlock()
		return wire.CloudMachine{}, cloudStoreFailure(err)
	}
	i := slices.IndexFunc(list, func(b wire.CloudBinding) bool { return b.MachineID == mid })
	if i < 0 {
		h.cloud.mu.Unlock()
		return wire.CloudMachine{Bound: false}, nil
	}
	gone := list[i]
	err = h.cloudWriteBindings(ctx, slices.Delete(list, i, i+1))
	h.cloud.mu.Unlock()
	if err != nil {
		return wire.CloudMachine{}, cloudStoreFailure(err)
	}
	h.cloudForgetRecovery(mid)
	h.cloudAudit(ctx, auditCloudUnbind, actor, gone.Extension, mid, gone.Provider, gone.Instance, "", "", cloudOutcomeDone)
	h.logf("cloud: %s unbound", mid)
	return wire.CloudMachine{Bound: false}, nil
}

// cloudBound is m's binding with its provider, or why there is none to act on.
func (h *Manager) cloudBound(ctx context.Context, m *Machine) (wire.CloudBinding, cloudTarget, *cloudFailure) {
	b, err := h.cloudBindingOf(ctx, wire.CloudMachineID(m.cfg.ID))
	if err != nil {
		return wire.CloudBinding{}, cloudTarget{}, cloudStoreFailure(err)
	}
	if b == nil {
		return wire.CloudBinding{}, cloudTarget{}, cloudFail(http.StatusNotFound, codeCloudNotBound, cloudErr("not_bound", i18n.Args{"machine": m.name()}))
	}
	t, f := h.cloudTargetOf(b.Extension, b.Provider)
	return *b, t, f
}

// cloudAct asks the instance m is bound to for in.Action, after the hub's gate: the safety level of a standard action
// is the hub's table, a custom action's the manifest's; confirm needs in.Confirm, destructive also in.Acknowledge equal
// to the instance's id. The program hears of the write only after the gate passed; before the gate's verdict a fresh
// cloud.instance read says what the instance offers, and an action it does not offer is refused (unaudited when the
// request would have stopped at the gate). Every write that passed the gate is audited by actor, whatever its outcome,
// and runs on to its answer when the request's client goes away. With in.Reconnect a start begins the start-and-reconnect. The assistant's tool passes a request a person
// already approved, with Confirm and Acknowledge filled in, and the binding the person approved as expect: a computer
// bound to another instance since is not acted on (expect is nil from the routes and before an approval). in is held
// to CloudActionRequest whichever way it came, and a name with control characters is refused, so the name a card shows
// is the one the program gets.
func (h *Manager) cloudAct(ctx context.Context, lang i18n.Lang, actor string, m *Machine, in wire.CloudActionRequest, expect *wire.CloudBinding) (wire.CloudActionOutcome, *cloudFailure) {
	bad := cloudFail(http.StatusBadRequest, codeCloudBadRequest, cloudErr("bad_request"))
	if raw, err := json.Marshal(in); err != nil || wire.ValidateJSON("CloudActionRequest", raw) != nil {
		return wire.CloudActionOutcome{}, bad
	}
	if strings.IndexFunc(in.Name, unicode.IsControl) >= 0 || strings.IndexFunc(string(in.ID), unicode.IsControl) >= 0 {
		return wire.CloudActionOutcome{}, bad
	}
	if !in.Action.Valid() || in.Action == wire.CloudActionKindSnapshotList ||
		(in.Action == wire.CloudActionKindCustom) != (in.ID != "") || (in.Reconnect && in.Action != wire.CloudActionKindStart) ||
		(in.Name != "" && in.Action != wire.CloudActionKindSnapshotCreate) {
		return wire.CloudActionOutcome{}, bad
	}
	b, t, f := h.cloudBound(ctx, m)
	if f != nil {
		return wire.CloudActionOutcome{}, f
	}
	if expect != nil && (h.cloudCanonical(expect.Extension) != h.cloudCanonical(b.Extension) || expect.Provider != b.Provider ||
		expect.Account != b.Account || expect.Instance != b.Instance) {
		return wire.CloudActionOutcome{}, cloudFail(http.StatusBadRequest, codeCloudBadRequest, cloudErr("rebound", i18n.Args{"machine": m.name()}))
	}
	safety, ok := extension.CloudActionSafety(in.Action)
	var custom wire.CloudCustomAction
	if in.Action == wire.CloudActionKindCustom {
		if custom, ok = cloudCustomOf(t.p, in.ID); ok {
			safety = custom.Safety
		}
	}
	if !ok {
		return wire.CloudActionOutcome{}, cloudFail(http.StatusUnprocessableEntity, codeCloudUnsupported, cloudErr("unsupported"))
	}
	// The gate: before the program hears of the write. Its verdict waits for the instance's own offer, so an action
	// the instance does not offer now is refused even when the request would stop at the gate (and the assistant puts
	// no approval card up for it).
	var gate *cloudFailure
	switch {
	case safety != wire.CloudSafetyRead && !in.Confirm:
		gate = cloudFail(http.StatusBadRequest, codeCloudConfirmFirst, cloudErr("confirmation_required"))
	case safety == wire.CloudSafetyDestructive && in.Acknowledge != string(b.Instance):
		gate = cloudFail(http.StatusBadRequest, codeCloudAcknowledge, cloudErr("acknowledgement_required"))
	}
	if gate != nil {
		gate.safety = safety
	}
	mid, kind := wire.CloudMachineID(m.cfg.ID), string(in.Action)
	if in.Action == wire.CloudActionKindCustom {
		kind = string(wire.CloudActionKindCustom) + ":" + string(in.ID)
	}
	// A request that passed the gate is a write: it, its audit and a recovery it starts outlive the request, so a
	// client that went away neither cancels a write under way nor leaves it audited as failed (cloudCall still bounds
	// each call). One stopped at the gate only reads, on the request's own context.
	wctx := context.WithoutCancel(ctx)
	rctx := wctx
	if gate != nil {
		rctx = ctx
	}
	done := func(outcome string) {
		if safety == wire.CloudSafetyRead {
			return
		}
		h.cloudAudit(wctx, auditCloudAction, actor, t.p.extension, mid, t.p.contribution, b.Instance, kind, safety, outcome)
		h.extensions.logf("plugin %s: cloud %s on %s: %s", t.p.extension, kind, mid, outcome)
	}
	failed := func(f *cloudFailure) (wire.CloudActionOutcome, *cloudFailure) {
		if gate != nil {
			return wire.CloudActionOutcome{}, f // stopped before the gate passed: nothing to audit
		}
		outcome := cloudOutcomeFailed
		if f.code == codeCloudRefused || f.code == codeCloudUnsupported || f.code == string(wire.CloudErrorCodeInvalidState) {
			outcome = cloudOutcomeRefused
		}
		done(outcome)
		return wire.CloudActionOutcome{}, f
	}
	ref := wire.CloudInstanceRef{Account: b.Account, ID: b.Instance}
	inst, f := h.cloudInstance(rctx, lang, t, ref, true)
	if f != nil {
		return failed(f)
	}
	offered := slices.Contains(inst.Actions, in.Action)
	if in.Action == wire.CloudActionKindCustom {
		offered = slices.Contains(inst.CustomActions, in.ID)
	}
	if !offered {
		return failed(cloudFail(http.StatusUnprocessableEntity, codeCloudUnsupported, cloudErr("unsupported")))
	}
	if gate != nil {
		return wire.CloudActionOutcome{}, gate
	}
	params := wire.CloudActionParams{Contribution: string(t.p.contribution), Locale: string(lang), Instance: ref, Action: in.Action,
		Name: in.Name, RequestID: wire.CloudRequestID(offloadRequestID())}
	if in.Action == wire.CloudActionKindCustom {
		params.Custom = in.ID
	}
	var res wire.CloudActionResult
	if f := h.cloudCall(wctx, t, cloudActionTimeout, "cloud.action", params, "CloudActionResult", &res); f != nil {
		return failed(f)
	}
	if res.Status == wire.CloudActionStatusRefused {
		f := cloudFail(http.StatusUnprocessableEntity, codeCloudRefused, cloudErr("refused", i18n.Args{"name": t.p.name}))
		f.detail = cloudLine(res.Message, cloudProviderWordsMost)
		return failed(f)
	}
	out := wire.CloudActionOutcome{Status: res.Status, Message: cloudLine(res.Message, cloudTextMost)}
	if res.Instance != nil && res.Instance.Account == ref.Account && res.Instance.ID == ref.ID {
		after := cloudCheckInstance(t.p, *res.Instance)
		host := cloudHostOf(t.p, after, mid)
		out.Host = &host
		h.cloudNoteSeen(wctx, []cloudSeenNote{{machineID: mid, p: t.p, inst: after}})
	}
	if cloudLink(res.URL) {
		out.URL, out.ExpiresAt = res.URL, res.ExpiresAt
	}
	if res.Snapshot != nil {
		s := *res.Snapshot
		s.Name = cloudLine(s.Name, cloudSnapshotNameMost)
		out.Snapshot = &s
	}
	switch {
	case in.Action == wire.CloudActionKindSyncIp && out.Host == nil:
		return failed(h.badCloudAnswer(t, "cloud.action", "sync_ip without the instance"))
	case in.Action == wire.CloudActionKindConsole && out.URL == "":
		return failed(h.badCloudAnswer(t, "cloud.action", "console without an https link"))
	}
	if in.Reconnect {
		rec := h.cloudRecover(lang, m, b, t)
		out.Recovery = &rec
	}
	if res.Status == wire.CloudActionStatusAccepted {
		done(cloudOutcomeAccepted)
	} else {
		done(cloudOutcomeDone)
	}
	return out, nil
}

// cloudSnapshots are the snapshots of the instance m is bound to, newest first: asked only when the instance offers
// snapshot_list.
func (h *Manager) cloudSnapshots(ctx context.Context, lang i18n.Lang, m *Machine) (wire.CloudSnapshots, *cloudFailure) {
	b, t, f := h.cloudBound(ctx, m)
	if f != nil {
		return wire.CloudSnapshots{}, f
	}
	ref := wire.CloudInstanceRef{Account: b.Account, ID: b.Instance}
	inst, f := h.cloudInstance(ctx, lang, t, ref, false)
	if f != nil {
		return wire.CloudSnapshots{}, f
	}
	if !slices.Contains(inst.Actions, wire.CloudActionKindSnapshotList) {
		return wire.CloudSnapshots{}, cloudFail(http.StatusUnprocessableEntity, codeCloudUnsupported, cloudErr("unsupported"))
	}
	var list wire.CloudSnapshotList
	params := wire.CloudSnapshotsParams{Contribution: string(t.p.contribution), Locale: string(lang), Instance: ref}
	if f := h.cloudCall(ctx, t, cloudSnapshotsTimeout, "cloud.snapshots", params, "CloudSnapshotList", &list); f != nil {
		return wire.CloudSnapshots{}, f
	}
	if len(list.Snapshots) > cloudSnapshotsMost {
		return wire.CloudSnapshots{}, h.badCloudAnswer(t, "cloud.snapshots", "more than 200 snapshots")
	}
	out := wire.CloudSnapshots{Snapshots: []wire.CloudSnapshot{}}
	for _, s := range list.Snapshots {
		s.Name = cloudLine(s.Name, cloudSnapshotNameMost)
		out.Snapshots = append(out.Snapshots, s)
	}
	return out, nil
}

// cloudAuditOf reads back what was asked of m's instances, newest first: the cloud rows of every cloud provider's
// extension (and of the one m is bound to) that name m.
func (h *Manager) cloudAuditOf(ctx context.Context, m *Machine, limit int) (wire.CloudAudit, *cloudFailure) {
	out := wire.CloudAudit{Audit: []wire.CloudAuditEntry{}}
	st, _ := h.store.(auditReader)
	if st == nil {
		return out, nil
	}
	mid := wire.CloudMachineID(m.cfg.ID)
	var ids []string
	if b, err := h.cloudBindingOf(ctx, mid); err == nil && b != nil {
		ids = append(ids, b.Extension)
	}
	for _, p := range h.cloudProviders() {
		if !slices.Contains(ids, p.extension) {
			ids = append(ids, p.extension)
		}
	}
	for _, id := range ids {
		rows, err := st.ExtensionAudits(ctx, id, auditReadMost)
		if err != nil {
			return wire.CloudAudit{}, cloudStoreFailure(err)
		}
		for _, row := range rows {
			if !strings.HasPrefix(row.Action, "cloud_") {
				continue
			}
			var d map[string]string // cloudAudit's detail: machine_id, provider, instance, action, safety, outcome
			if json.Unmarshal(row.Detail, &d) != nil || d["machine_id"] != string(mid) {
				continue
			}
			out.Audit = append(out.Audit, wire.CloudAuditEntry{At: row.At, Extension: row.Plugin, Event: row.Action, Actor: row.Actor,
				MachineID: mid, Provider: wire.CloudProviderID(d["provider"]), Instance: wire.CloudInstanceID(d["instance"]), Action: d["action"], Safety: d["safety"], Outcome: d["outcome"]})
		}
	}
	slices.SortStableFunc(out.Audit, func(a, b wire.CloudAuditEntry) int { return b.At.Compare(a.At) })
	if len(out.Audit) > limit {
		out.Audit = out.Audit[:limit]
	}
	return out, nil
}

// cloudBody reads a request's body held to def (cloud.schema.json) into out.
func cloudBody(w http.ResponseWriter, r *http.Request, def string, out any) bool {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, cloudBodyMost))
	if err != nil || wire.ValidateJSON(def, raw) != nil {
		return false
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	return dec.Decode(out) == nil
}

// getCloudMachine: GET /api/v1/machines/{mid}/cloud?refresh=1 → wire.CloudMachine.
func (h *Manager) getCloudMachine(w http.ResponseWriter, r *http.Request, m *Machine) {
	out, f := h.cloudMachineView(r.Context(), h.offloadLang(r), m, cloudRefresh(r))
	if f != nil {
		h.writeCloudErr(w, r, f)
		return
	}
	writeJSON(w, out)
}

// putCloudMachine: PUT /api/v1/machines/{mid}/cloud wire.CloudBindRequest → wire.CloudMachine.
func (h *Manager) putCloudMachine(w http.ResponseWriter, r *http.Request, m *Machine) {
	var in wire.CloudBindRequest
	if !cloudBody(w, r, "CloudBindRequest", &in) {
		h.writeCloudErr(w, r, cloudFail(http.StatusBadRequest, codeCloudBadRequest, cloudErr("bad_request")))
		return
	}
	out, f := h.cloudBind(r.Context(), h.offloadLang(r), actorOf(r.Context()), m, in)
	if f != nil {
		h.writeCloudErr(w, r, f)
		return
	}
	writeJSON(w, out)
}

// deleteCloudMachine: DELETE /api/v1/machines/{mid}/cloud → wire.CloudMachine {bound: false}.
func (h *Manager) deleteCloudMachine(w http.ResponseWriter, r *http.Request, m *Machine) {
	out, f := h.cloudUnbind(r.Context(), actorOf(r.Context()), m)
	if f != nil {
		h.writeCloudErr(w, r, f)
		return
	}
	writeJSON(w, out)
}

// postCloudAction: POST /api/v1/machines/{mid}/cloud/actions wire.CloudActionRequest → 200 wire.CloudActionOutcome
// (done) or 202 (accepted).
func (h *Manager) postCloudAction(w http.ResponseWriter, r *http.Request, m *Machine) {
	var in wire.CloudActionRequest
	if !cloudBody(w, r, "CloudActionRequest", &in) {
		h.writeCloudErr(w, r, cloudFail(http.StatusBadRequest, codeCloudBadRequest, cloudErr("bad_request")))
		return
	}
	out, f := h.cloudAct(r.Context(), h.offloadLang(r), actorOf(r.Context()), m, in, nil)
	if f != nil {
		h.writeCloudErr(w, r, f)
		return
	}
	if out.Status == wire.CloudActionStatusAccepted {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusAccepted)
	}
	writeJSON(w, out)
}

// getCloudSnapshots: GET /api/v1/machines/{mid}/cloud/snapshots → wire.CloudSnapshots.
func (h *Manager) getCloudSnapshots(w http.ResponseWriter, r *http.Request, m *Machine) {
	out, f := h.cloudSnapshots(r.Context(), h.offloadLang(r), m)
	if f != nil {
		h.writeCloudErr(w, r, f)
		return
	}
	writeJSON(w, out)
}

// getCloudAudit: GET /api/v1/machines/{mid}/cloud/audit?limit= → wire.CloudAudit.
func (h *Manager) getCloudAudit(w http.ResponseWriter, r *http.Request, m *Machine) {
	limit := cloudAuditReadDefault
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			h.writeCloudErr(w, r, cloudFail(http.StatusBadRequest, codeCloudBadRequest, cloudErr("bad_request")))
			return
		}
		limit = min(n, cloudAuditReadMost)
	}
	out, f := h.cloudAuditOf(r.Context(), m, limit)
	if f != nil {
		h.writeCloudErr(w, r, f)
		return
	}
	writeJSON(w, out)
}
