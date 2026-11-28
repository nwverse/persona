package extmgr

// The cloud_hosts extension point (docs/design/machine-cloud-ops.md): the routes the apps manage the cloud servers
// their computers run on through. The manager lists the enabled providers without calling a program, asks one program
// at a time in the caller's language, holds every answer to cloud.schema.json and re-checks it (the ids asked for, plain
// IP addresses, https links without credentials, only the standard actions and the manifest's own custom actions, one
// line of text), and holds every write to the hub's safety gate before the program hears of it. Bindings, gates, audit
// and start-and-reconnect live in extension_cloud_ops.go and extension_cloud_recovery.go; the assistant's tools call the
// same functions (cloudHosts, cloudMachineView, cloudAct, cloudSnapshots), so there is one implementation of each gate.
// Only a device with full credentials gets here. Core never names a vendor; logs say ids and outcomes, never addresses.

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/extension"
	"github.com/nwverse/persona/server/internal/extension/host"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/rpc"
)

// How long each call waits on the program (the doc's "Methods" bounds).
var (
	cloudInstancesTimeout = 20 * time.Second
	cloudInstanceTimeout  = 15 * time.Second
	cloudActionTimeout    = 30 * time.Second
	cloudSnapshotsTimeout = 15 * time.Second
)

const (
	// cloudBodyMost bounds a request's body.
	cloudBodyMost = 16 << 10
	// cloudInstancesMost and cloudSnapshotsMost are the most a list may hold (cloud.schema.json).
	cloudInstancesMost = 500
	cloudSnapshotsMost = 200

	codeCloudBadRequest     = "bad_request"
	codeCloudReadOnly       = "read_only"
	codeCloudNotFound       = "not_found"
	codeCloudNotBound       = "not_bound"
	codeCloudAlreadyBound   = "already_bound"
	codeCloudConfirmFirst   = "confirmation_required"
	codeCloudAcknowledge    = "acknowledgement_required"
	codeCloudUnsupported    = "unsupported"
	codeCloudRefused        = "refused"
	codeCloudProviderOff    = "provider_off"
	codeCloudPluginFailed   = "plugin_failed"
	codeCloudDeadline       = "deadline"
	codeCloudNoDatabase     = "no_database"
	cloudI18n               = "hub-extension_cloud."
	cloudAuditReadDefault   = 50
	cloudAuditReadMost      = 200
	cloudTextMost           = 300
	cloudShortTextMost      = 80
	cloudDisplayNameMost    = 200
	cloudProviderWordsMost  = 199
	cloudSnapshotNameMost   = 200
	cloudInstanceRefreshArg = "refresh"
)

// cloudProviderStatus is the route status of each code a provider may answer with that the routes pass on under the
// same code; failed and anything else is plugin_failed.
var cloudProviderStatus = map[wire.CloudErrorCode]int{
	wire.CloudErrorCodeNotFound:     http.StatusNotFound,
	wire.CloudErrorCodeNotSignedIn:  http.StatusConflict,
	wire.CloudErrorCodeUnsupported:  http.StatusUnprocessableEntity,
	wire.CloudErrorCodeInvalidState: http.StatusConflict,
	wire.CloudErrorCodeRateLimited:  http.StatusTooManyRequests,
	wire.CloudErrorCodeCapacity:     http.StatusServiceUnavailable,
	wire.CloudErrorCodeRefused:      http.StatusUnprocessableEntity,
}

// cloudFailure is why a cloud request did not go through: the route's status and CloudError code, the error to show
// (an i18n message, localized when written), and what CloudError carries besides.
type cloudFailure struct {
	status    int
	code      string
	err       error
	safety    wire.CloudSafety
	detail    string
	machineID wire.CloudMachineID
}

func cloudFail(status int, code string, err error) *cloudFailure {
	return &cloudFailure{status: status, code: code, err: err}
}

// cloudErr is the message of key in hub-extension_cloud.json.
func cloudErr(key string, args ...i18n.Args) error { return i18n.Err(cloudI18n+key, args...) }

// cloudProvider is one installed cloud_hosts contribution, on or off (extension.Registry.EachCloudHost): the extension's
// canonical id, the item's id, what the apps call it, the badge beside a bound computer, the manifest's own actions
// with their safety levels, and on when it takes effect.
type cloudProvider struct {
	extension     string
	contribution  wire.CloudProviderID
	name          string
	vendor        string
	customActions []wire.CloudCustomAction
	on            bool
}

// cloudProviders are every installed provider, on or off, in the registry's order.
func (h *Manager) cloudProviders() []cloudProvider {
	if h.extensions == nil || h.extensions.reg == nil {
		return nil
	}
	var out []cloudProvider
	h.extensions.reg.EachCloudHost(func(ext string, it extension.CloudHostItem, on bool) {
		out = append(out, cloudProvider{extension: ext, contribution: it.ID, name: it.Name, vendor: it.Vendor,
			customActions: it.CustomActions, on: on})
	})
	return out
}

// cloudProviderOf is the contribution of ext (followed through renames), on or off.
func (h *Manager) cloudProviderOf(ext string, contribution wire.CloudProviderID) (cloudProvider, bool) {
	if h.extensions == nil || h.extensions.reg == nil {
		return cloudProvider{}, false
	}
	id := h.extensions.reg.CanonicalID(ext)
	for _, p := range h.cloudProviders() {
		if p.extension == id && p.contribution == contribution {
			return p, true
		}
	}
	return cloudProvider{}, false
}

// cloudTarget is a provider and its program.
type cloudTarget struct {
	p  cloudProvider
	hp *hubExtension
}

// cloudState is the manager's own cloud state: the lock around the bindings row's read-modify-write, and the
// start-and-reconnects it runs or last ran, by machine id (in memory only).
type cloudState struct {
	mu         sync.Mutex
	recMu      sync.Mutex
	recoveries map[wire.CloudMachineID]*cloudRun
	gen        uint64
}

// cloudRoutes registers the provider routes beside offloadRoutes.
func (h *Manager) cloudRoutes(api *http.ServeMux) {
	api.HandleFunc("GET /api/v1/extensions/-/cloud", h.cloudGate(h.getCloudProviders))
	api.HandleFunc("GET /api/v1/extensions/-/cloud/instances", h.cloudGate(h.getCloudInstances))
	api.HandleFunc("GET /api/v1/extensions/-/cloud/bindings", h.cloudGate(h.getCloudBindings))
}

// cloudMachineRoutes registers a computer's cloud routes beside the computer, browser and iPhone ones.
func (h *Manager) cloudMachineRoutes(api *http.ServeMux) {
	api.HandleFunc("GET /api/v1/machines/{mid}/cloud", h.cloudGate(h.withCloudMachine(h.getCloudMachine)))
	api.HandleFunc("PUT /api/v1/machines/{mid}/cloud", h.cloudGate(h.withCloudMachine(h.putCloudMachine)))
	api.HandleFunc("DELETE /api/v1/machines/{mid}/cloud", h.cloudGate(h.withCloudMachine(h.deleteCloudMachine)))
	api.HandleFunc("POST /api/v1/machines/{mid}/cloud/actions", h.cloudGate(h.withCloudMachine(h.postCloudAction)))
	api.HandleFunc("GET /api/v1/machines/{mid}/cloud/snapshots", h.cloudGate(h.withCloudMachine(h.getCloudSnapshots)))
	api.HandleFunc("GET /api/v1/machines/{mid}/cloud/audit", h.cloudGate(h.withCloudMachine(h.getCloudAudit)))
}

// cloudGate lets a device with full credentials through: these routes power servers on and off.
func (h *Manager) cloudGate(f http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if pr := principalOf(r.Context()); pr.Role != roleFull || pr.Extension != "" {
			h.writeCloudErr(w, r, cloudFail(http.StatusForbidden, codeCloudReadOnly, cloudErr("read_only")))
			return
		}
		f(w, r)
	}
}

// withCloudMachine resolves {mid} to a computer of this hub, answering a CloudError when there is none.
func (h *Manager) withCloudMachine(f func(http.ResponseWriter, *http.Request, *Machine)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m := h.localMachine(r.PathValue("mid"))
		if m == nil {
			h.writeCloudErr(w, r, cloudFail(http.StatusNotFound, codeCloudNotFound, cloudErr("no_machine")))
			return
		}
		f(w, r, m)
	}
}

// writeCloudErr answers f as wire.CloudError, the error in the caller's language.
func (h *Manager) writeCloudErr(w http.ResponseWriter, r *http.Request, f *cloudFailure) {
	extra := map[string]any{}
	if f.safety != "" {
		extra["safety"] = string(f.safety)
	}
	if f.detail != "" {
		extra["detail"] = f.detail
	}
	if f.machineID != "" {
		extra["machine_id"] = string(f.machineID)
	}
	writeCode(w, f.status, f.code, i18n.Localize(f.err, h.offloadLang(r)), extra)
}

// cloudRefresh reads ?refresh=1.
func cloudRefresh(r *http.Request) bool {
	v := r.URL.Query().Get(cloudInstanceRefreshArg)
	return v == "1" || v == "true"
}

// getCloudProviders: GET /api/v1/extensions/-/cloud → wire.CloudProviders, the enabled providers. No program is asked.
func (h *Manager) getCloudProviders(w http.ResponseWriter, _ *http.Request) {
	out := wire.CloudProviders{Providers: []wire.CloudProvider{}}
	for _, p := range h.cloudProvidersOn() {
		out.Providers = append(out.Providers, wire.CloudProvider{Extension: p.extension, ID: p.contribution, Name: p.name, Vendor: p.vendor})
	}
	writeJSON(w, out)
}

// getCloudInstances: GET /api/v1/extensions/-/cloud/instances?refresh=1 → wire.CloudHosts.
func (h *Manager) getCloudInstances(w http.ResponseWriter, r *http.Request) {
	out, f := h.cloudHosts(r.Context(), h.offloadLang(r), cloudRefresh(r))
	if f != nil {
		h.writeCloudErr(w, r, f)
		return
	}
	writeJSON(w, out)
}

// getCloudBindings: GET /api/v1/extensions/-/cloud/bindings → wire.CloudBindings. No program is asked.
func (h *Manager) getCloudBindings(w http.ResponseWriter, r *http.Request) {
	list, err := h.cloudBindings(r.Context())
	if err != nil {
		h.writeCloudErr(w, r, cloudStoreFailure(err))
		return
	}
	writeJSON(w, wire.CloudBindings{Bindings: nonNilBindings(list)})
}

// cloudProvidersOn are the providers that are installed and switched on, in the registry's order.
func (h *Manager) cloudProvidersOn() []cloudProvider {
	var out []cloudProvider
	for _, p := range h.cloudProviders() {
		if p.on {
			out = append(out, p)
		}
	}
	return out
}

// cloudTargetOf is the provider (extension, contribution) with its program: 404 when it is not installed, 409
// provider_off when it is switched off, 502 when its program does not take calls.
func (h *Manager) cloudTargetOf(ext string, contribution wire.CloudProviderID) (cloudTarget, *cloudFailure) {
	if h.extensions == nil {
		return cloudTarget{}, cloudFail(http.StatusNotFound, codeCloudNotFound, cloudErr("no_provider"))
	}
	p, ok := h.cloudProviderOf(ext, contribution)
	if !ok {
		return cloudTarget{}, cloudFail(http.StatusNotFound, codeCloudNotFound, cloudErr("no_provider"))
	}
	if !p.on {
		return cloudTarget{p: p}, cloudFail(http.StatusConflict, codeCloudProviderOff, cloudErr("provider_off", i18n.Args{"name": p.name}))
	}
	hp := h.extensions.get(p.extension)
	if hp == nil || hp.proc == nil || !programAnswers(hp.proc) {
		return cloudTarget{p: p}, cloudFail(http.StatusBadGateway, codeCloudPluginFailed, cloudErr("not_running", i18n.Args{"name": p.name}))
	}
	return cloudTarget{p: p, hp: hp}, nil
}

// cloudHosts is every enabled provider's instances, each with its actions and the computer bound to it. A provider that
// fails is in problems; the others are listed all the same.
func (h *Manager) cloudHosts(ctx context.Context, lang i18n.Lang, refresh bool) (wire.CloudHosts, *cloudFailure) {
	out := wire.CloudHosts{Hosts: []wire.CloudHost{}, Problems: []wire.CloudProviderProblem{}}
	providers := h.cloudProvidersOn()
	type answer struct {
		list wire.CloudInstanceList
		f    *cloudFailure
	}
	answers := make([]answer, len(providers))
	var wg sync.WaitGroup
	for i, p := range providers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			t, f := h.cloudTargetOf(p.extension, p.contribution)
			if f == nil {
				answers[i].list, f = h.cloudInstanceList(ctx, lang, t, refresh)
			}
			answers[i].f = f
		}()
	}
	wg.Wait()
	bindings, err := h.cloudBindings(ctx)
	if err != nil {
		h.logf("cloud bindings: %v", err)
	}
	var seen []cloudSeenNote
	for i, p := range providers {
		a := answers[i]
		if a.f != nil {
			out.Problems = append(out.Problems, cloudProblem(p.extension, p.contribution, p.name, a.f, lang))
			continue
		}
		out.Partial = out.Partial || a.list.Partial
		for _, inst := range a.list.Instances {
			var mid wire.CloudMachineID
			if b := h.cloudBindingFor(bindings, p, inst.Account, inst.ID); b != nil {
				mid = b.MachineID
				seen = append(seen, cloudSeenNote{machineID: mid, p: p, inst: inst})
			}
			out.Hosts = append(out.Hosts, cloudHostOf(p, inst, mid))
		}
	}
	h.cloudNoteSeen(ctx, seen)
	return out, nil
}

// cloudProblem is f as a provider's problem, its error in lang.
func cloudProblem(ext string, provider wire.CloudProviderID, name string, f *cloudFailure, lang i18n.Lang) wire.CloudProviderProblem {
	return wire.CloudProviderProblem{Extension: ext, Provider: provider, ProviderName: name, Code: f.code, Error: wire.CloudMessage(i18n.Localize(f.err, lang))}
}

// cloudInstanceList asks t for every instance (cloud.instances) and re-checks each, dropping a second answer for the
// same instance.
func (h *Manager) cloudInstanceList(ctx context.Context, lang i18n.Lang, t cloudTarget, refresh bool) (wire.CloudInstanceList, *cloudFailure) {
	var list wire.CloudInstanceList
	params := wire.CloudInstancesParams{Contribution: string(t.p.contribution), Locale: string(lang), Refresh: refresh}
	if f := h.cloudCall(ctx, t, cloudInstancesTimeout, "cloud.instances", params, "CloudInstanceList", &list); f != nil {
		return wire.CloudInstanceList{}, f
	}
	if len(list.Instances) > cloudInstancesMost {
		return wire.CloudInstanceList{}, h.badCloudAnswer(t, "cloud.instances", "more than 500 instances")
	}
	out := wire.CloudInstanceList{Partial: list.Partial, Detail: cloudLine(list.Detail, cloudTextMost), Instances: []wire.CloudInstance{}}
	for _, inst := range list.Instances {
		if slices.ContainsFunc(out.Instances, func(o wire.CloudInstance) bool { return o.Account == inst.Account && o.ID == inst.ID }) {
			continue
		}
		out.Instances = append(out.Instances, cloudCheckInstance(t.p, inst))
	}
	return out, nil
}

// cloudInstance asks t how one instance is now (cloud.instance): the answer must be that account and id.
func (h *Manager) cloudInstance(ctx context.Context, lang i18n.Lang, t cloudTarget, ref wire.CloudInstanceRef, refresh bool) (wire.CloudInstance, *cloudFailure) {
	var inst wire.CloudInstance
	params := wire.CloudInstanceParams{Contribution: string(t.p.contribution), Locale: string(lang), Instance: ref, Refresh: refresh}
	if f := h.cloudCall(ctx, t, cloudInstanceTimeout, "cloud.instance", params, "CloudInstance", &inst); f != nil {
		return wire.CloudInstance{}, f
	}
	if inst.Account != ref.Account || inst.ID != ref.ID {
		return wire.CloudInstance{}, h.badCloudAnswer(t, "cloud.instance", "another instance than the one asked for")
	}
	return cloudCheckInstance(t.p, inst), nil
}

// cloudCall asks t's program within timeout and reads its answer into out after holding it to def
// (cloud.schema.json).
func (h *Manager) cloudCall(ctx context.Context, t cloudTarget, timeout time.Duration, method string, params any, def string, out any) *cloudFailure {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var raw json.RawMessage
	if err := t.hp.proc.Call(ctx, method, params, &raw); err != nil {
		return h.cloudCallFailure(ctx, t, method, err)
	}
	if err := wire.ValidateJSON(def, raw); err != nil {
		return h.badCloudAnswer(t, method, cloudSchemaRule(def, err))
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return h.badCloudAnswer(t, method, "undecodable as "+def)
	}
	return nil
}

// cloudCallFailure is a failed call as the routes answer it: 504 when the program took too long; a provider's own
// code (not_found, not_signed_in, unsupported, invalid_state, rate_limited, capacity, refused) passed on with its one
// line as the detail; 502 plugin_failed for the rest.
func (h *Manager) cloudCallFailure(ctx context.Context, t cloudTarget, method string, err error) *cloudFailure {
	name := i18n.Args{"name": t.p.name}
	var re *rpc.Error
	errors.As(err, &re)
	switch {
	case ctx.Err() != nil && errors.Is(ctx.Err(), context.DeadlineExceeded):
		return cloudFail(http.StatusGatewayTimeout, codeCloudDeadline, cloudErr("deadline", name))
	case errors.Is(err, host.ErrNotRunning):
		return cloudFail(http.StatusBadGateway, codeCloudPluginFailed, cloudErr("not_running", name))
	case re != nil && (re.Code == extension.CodeUnknownMethod || re.Code == extension.CodeNoContribution):
		return cloudFail(http.StatusBadGateway, codeCloudPluginFailed, cloudErr("not_provided", name))
	case re != nil && cloudProviderStatus[wire.CloudErrorCode(re.Code)] != 0:
		f := cloudFail(cloudProviderStatus[wire.CloudErrorCode(re.Code)], re.Code, cloudErr(re.Code, name))
		f.detail = cloudLine(re.Message, cloudProviderWordsMost)
		return f
	}
	why := err.Error()
	if re != nil {
		why = re.Message
	}
	h.extensions.logf("plugin %s: %s failed", t.p.extension, method)
	f := cloudFail(http.StatusBadGateway, codeCloudPluginFailed, cloudErr("failed", name))
	f.detail = cloudLine(why, cloudProviderWordsMost)
	return f
}

// badCloudAnswer logs an answer that broke the schema or a re-check and fails with 502. rule is the hub's own words
// (a fixed reason, or cloudSchemaRule's definition and path), never the answer's: the validator's messages repeat the
// values they refuse, which may be an address or the provider's text.
func (h *Manager) badCloudAnswer(t cloudTarget, method, rule string) *cloudFailure {
	h.extensions.logf("plugin %s: %s answer refused: %s", t.p.extension, method, rule)
	return cloudFail(http.StatusBadGateway, codeCloudPluginFailed, cloudErr("bad_answer", i18n.Args{"name": t.p.name}))
}

// cloudSchemaRule names where an answer broke def: the definition and the JSON path of the first error, never the
// value found there.
func cloudSchemaRule(def string, err error) string {
	var ve *wire.ValidationError
	if !errors.As(err, &ve) || len(ve.Errors) == 0 {
		return "does not match " + def
	}
	path, _, _ := strings.Cut(ve.Errors[0], ": ")
	if !strings.HasPrefix(path, "$") {
		return "does not match " + def
	}
	return "does not match " + cmp.Or(ve.Def, def) + " at " + cloudLine(path, cloudTextMost)
}

// cloudCheckInstance is inst as the hub passes it on: text on one line and clipped, only plain IP addresses, the
// console link only when it is https without credentials, only the standard actions (each once) and only the custom
// actions p's manifest declares.
func cloudCheckInstance(p cloudProvider, inst wire.CloudInstance) wire.CloudInstance {
	inst.DisplayName = cloudLine(inst.DisplayName, cloudDisplayNameMost)
	inst.StateDetail = cloudLine(inst.StateDetail, cloudTextMost)
	inst.Region = cloudLine(inst.Region, cloudShortTextMost)
	inst.Zone = cloudLine(inst.Zone, cloudShortTextMost)
	inst.PublicIps = cloudIPs(inst.PublicIps)
	inst.PrivateIps = cloudIPs(inst.PrivateIps)
	if inst.Spec != nil {
		s := *inst.Spec
		s.Size, s.Arch, s.OS = cloudLine(s.Size, cloudShortTextMost), cloudLine(s.Arch, 20), cloudLine(s.OS, cloudShortTextMost)
		inst.Spec = &s
	}
	if inst.Lifecycle != nil {
		l := *inst.Lifecycle
		inst.Lifecycle = &l
	}
	if !cloudLink(inst.ConsoleURL) {
		inst.ConsoleURL = ""
	}
	var actions []wire.CloudActionKind
	for _, a := range inst.Actions {
		if slices.Contains(extension.CloudStandardActions, a) && !slices.Contains(actions, a) {
			actions = append(actions, a)
		}
	}
	inst.Actions = actions
	var custom []wire.CloudCustomActionID
	for _, id := range inst.CustomActions {
		if _, ok := cloudCustomOf(p, id); ok && !slices.Contains(custom, id) {
			custom = append(custom, id)
		}
	}
	inst.CustomActions = custom
	return inst
}

// cloudCustomOf is p's manifest declaration of the custom action id.
func cloudCustomOf(p cloudProvider, id wire.CloudCustomActionID) (wire.CloudCustomAction, bool) {
	i := slices.IndexFunc(p.customActions, func(a wire.CloudCustomAction) bool { return a.ID == id })
	if i < 0 || id == "" {
		return wire.CloudCustomAction{}, false
	}
	return p.customActions[i], true
}

// cloudHostOf is inst of p as the apps get it: with the actions it takes now, each with the hub's safety level (a
// custom action's title, level and sentence from the manifest), and the computer bound to it.
func cloudHostOf(p cloudProvider, inst wire.CloudInstance, machineID wire.CloudMachineID) wire.CloudHost {
	actions := []wire.CloudHostAction{}
	for _, a := range inst.Actions {
		if s, ok := extension.CloudActionSafety(a); ok {
			actions = append(actions, wire.CloudHostAction{Action: a, Safety: s})
		}
	}
	for _, id := range inst.CustomActions {
		if d, ok := cloudCustomOf(p, id); ok {
			actions = append(actions, wire.CloudHostAction{Action: wire.CloudActionKindCustom, ID: d.ID, Title: d.Title, Safety: d.Safety, ConfirmText: d.Confirm})
		}
	}
	return wire.CloudHost{Extension: p.extension, Provider: p.contribution, ProviderName: p.name, Vendor: p.vendor, Instance: inst,
		Actions: actions, MachineID: machineID}
}

// cloudIPs are the plain IPv4 and IPv6 addresses of list (no zone, no port, no name), each once, in their order.
func cloudIPs(list []string) []string {
	var out []string
	for _, s := range list {
		a, err := netip.ParseAddr(strings.TrimSpace(s))
		if err != nil || a.Zone() != "" {
			continue
		}
		if v := a.String(); !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

// cloudLink reports whether s is an https address with a host and without credentials.
func cloudLink(s string) bool {
	if s == "" || strings.ContainsFunc(s, unicode.IsControl) {
		return false
	}
	u, err := url.Parse(s)
	return err == nil && u.Scheme == "https" && u.User == nil && u.Host != ""
}

// cloudLine is a provider's text on one line with no control characters, clipped to most.
func cloudLine(s string, most int) string {
	if s == "" {
		return ""
	}
	return providerWords(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s), most)
}
