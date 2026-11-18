package assistant

import (
	"context"
	"slices"
	"strings"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// External launchers are machine-local advertisements, never configurable defaults or
// fallback providers. Only a persisted pick of the actual main conversation grants scope.
func externalProvider(provider string) bool {
	name, ok := strings.CutPrefix(provider, "launcher.")
	return ok && proto.IsExternalLauncher(name)
}

func checkExternalPurpose(b ModelBinding) error {
	if externalProvider(b.Provider) {
		return i18n.Err("external-adapter.purpose_unsupported")
	}
	return nil
}

func externalUnavailableError(code string) error {
	// Never localize or relay arbitrary adapter diagnostics: only protocol codes.
	switch code {
	case "adapter_disabled", "adapter_scope_required", "adapter_purpose_unsupported", "adapter_runtime_unsupported",
		"adapter_platform_unsupported", "adapter_sandbox_unsupported", "adapter_target_not_found", "adapter_model_not_found",
		"adapter_protocol_unsupported", "adapter_config_conflict", "adapter_credentials_missing", "adapter_revision_mismatch",
		"adapter_timeout", "adapter_inventory_invalid", "adapter_unavailable", "adapter_probe_unsupported", "adapter_busy":
		return i18n.Err("external-adapter." + strings.TrimPrefix(code, "adapter_"))
	}
	return i18n.Err("external-adapter.target_not_found")
}

func externalLauncherError(l proto.Launcher) error {
	e := l.External
	if !proto.IsExternalLauncher(l.Name) || l.Source != proto.ExternalLauncherSource || e == nil ||
		e.AdapterID == "" || e.ID == "" || e.Revision == "" ||
		l.Name != proto.ExternalLauncherName(e.AdapterID, e.ID) {
		return i18n.Err("external-adapter.target_not_found")
	}
	if e.UnavailableCode != "" {
		return externalUnavailableError(e.UnavailableCode)
	}
	if e.CredentialState == "missing" {
		return i18n.Err("external-adapter.credentials_missing")
	}
	if e.CredentialState != "configured" && e.CredentialState != "unknown" {
		return i18n.Err("external-adapter.target_not_found")
	}
	if e.Engine != "claude-code" || !slices.Contains(e.Capabilities.Contexts, "main-chat") {
		return i18n.Err("external-adapter.purpose_unsupported")
	}
	if !slices.Contains(e.Capabilities.Platforms, "posix") {
		return i18n.Err("external-adapter.platform_unsupported")
	}
	if !slices.Contains(e.Capabilities.Runtimes, "cli") {
		return i18n.Err("external-adapter.runtime_unsupported")
	}
	return nil
}

func (s *Service) externalLauncherRefusal(_ context.Context, l proto.Launcher) error {
	return externalLauncherError(l)
}

// externalTarget reads this computer's advertisement, without a global provider view or
// hub-configured overrides. Missing, disabled and unreadable sources all fail closed.
func (s *Service) externalTarget(ctx context.Context, machine, name string) (*proto.ExternalLauncher, error) {
	d, ok := s.device(ctx, machine)
	if !ok {
		return nil, i18n.Err("external-adapter.target_not_found")
	}
	res, err := s.listLaunchers(ctx, d)
	if err != nil {
		return nil, i18n.Err("external-adapter.target_not_found")
	}
	if proto.IsExternalLauncher(name) && !slices.ContainsFunc(res.Launchers, func(l proto.Launcher) bool { return l.Name == name }) {
		// A new node can answer with an empty external snapshot while its first inventory loads.
		// Wait once for this specific selection, bounded by listLaunchers and the caller's context.
		res, err = s.listLaunchers(ctx, d, true)
		if err != nil {
			return nil, i18n.Err("external-adapter.target_not_found")
		}
	}
	for _, l := range res.Launchers {
		if l.Name == name {
			if err := s.externalLauncherRefusal(ctx, l); err != nil {
				return nil, err
			}
			return l.External, nil
		}
	}
	return nil, i18n.Err("external-adapter.target_not_found")
}

func externalModel(e *proto.ExternalLauncher, model string) (string, error) {
	for _, m := range e.Models {
		if proto.ValidModelID(m.ID) && (model != "" && m.ID == model || model == "" && m.Default) {
			return m.ID, nil
		}
	}
	return "", i18n.Err("external-adapter.model_not_found")
}

func (s *Service) externalMainScope(ctx context.Context, machine, sid string) bool {
	cfg, known := s.savedSettings(ctx)
	cfg = s.effective(ctx, cfg)
	if !known || sid == "" || cfg.Main != sid || cfg.mainRouteMachine() != machine || s.isWorkMachine(machine) {
		return false
	}
	_, guest, err := s.guestOf(ctx, machine, sid)
	if err != nil || guest {
		return false
	}
	if sess, ok := s.nodeSession(machine, sid); ok {
		if sess.PaneID != "" || sess.Origin == OriginMeeting || sess.Space != "" {
			return false
		}
		if sess.Cwd != "" {
			m, known := s.fleetMachine(machine)
			if !known || !m.isAssistant(sess) {
				return false
			}
		}
	}
	return true
}

func (s *Service) resolveExternalPick(ctx context.Context, sid string, pick modelPick) (modelPick, error) {
	if !s.externalMainScope(ctx, pick.MachineID, sid) {
		return pick, i18n.Err("external-adapter.scope_required")
	}
	e, err := s.externalTarget(ctx, pick.MachineID, pick.Launcher)
	if err != nil {
		return pick, err
	}
	// An external default is a selection made now, not a future directory default.
	// Persist its exact raw ID so later inventory changes cannot silently switch it.
	pick.Model, err = externalModel(e, pick.Model)
	return pick, err
}

type externalPickKey struct{}

type externalPickScope struct {
	pick      modelPick
	execution proto.ExternalExecution
}

// externalPickContext can only be made from the manager's kept selection and routing.
// Neither a request's external/source/is_main nor a carried prewarm pick is authority.
func (s *Service) externalPickContext(ctx context.Context, machine, sid string, pick modelPick) (context.Context, error) {
	kept, ok := s.modelPickOf(ctx, machine, sid)
	if !ok || kept != pick || !s.externalMainScope(ctx, machine, sid) {
		return ctx, i18n.Err("external-adapter.scope_required")
	}
	if pick.Model == "" {
		return ctx, i18n.Err("external-adapter.model_not_found")
	}
	e, err := s.externalTarget(ctx, machine, pick.Launcher)
	if err != nil {
		return ctx, err
	}
	if _, err := externalModel(e, pick.Model); err != nil {
		return ctx, err
	}
	scope := externalPickScope{pick: pick, execution: proto.ExternalExecution{
		Scope: proto.ExternalMainChatScope, SessionID: sid,
		AdapterID: e.AdapterID, TargetID: e.ID, Revision: e.Revision,
	}}
	return context.WithValue(ctx, externalPickKey{}, scope), nil
}

// applyExternalScope runs at the final model boundary. The directory revision is
// deliberately outside ModelBinding and its fingerprint: refreshing a recipe must not
// restart a live process. The node pins the actual recipe until that process ends.
func (s *Service) applyExternalScope(ctx context.Context, id string, params map[string]any, b ModelBinding) error {
	scope, ok := ctx.Value(externalPickKey{}).(externalPickScope)
	if !ok || id != "assistant" || launcherProvider(scope.pick.Launcher) != b.Provider || scope.pick.Model != b.Model ||
		!s.externalMainScope(ctx, scope.pick.MachineID, scope.execution.SessionID) {
		return i18n.Err("external-adapter.scope_required")
	}
	kept, persisted := s.modelPickOf(ctx, scope.pick.MachineID, scope.execution.SessionID)
	if !persisted || kept != scope.pick {
		return i18n.Err("external-adapter.scope_required")
	}
	if raw, present := params["runtime"]; present {
		if runtime, ok := raw.(string); !ok || runtime != "" && runtime != "cli" {
			return i18n.Err("external-adapter.runtime_unsupported")
		}
	}
	if _, guest := params["guest"]; guest {
		return i18n.Err("external-adapter.scope_required")
	}
	if sid, _ := params["new_session_id"].(string); sid != "" {
		return i18n.Err("external-adapter.scope_required")
	}
	binding := scope.execution
	params["external"], params["is_main"], params["launch"] = &binding, true, "headless"
	params["session_id"] = binding.SessionID
	return nil
}

// externalLauncherSet reads only the existing snapshot, outside health's lock. A
// historical health record cannot turn a known external source into a repair agent.
func (s *Service) externalLauncherSet(machine string) map[string]bool {
	c := &s.models
	c.zshrcMu.Lock()
	defer c.zshrcMu.Unlock()
	var names map[string]bool
	for _, l := range c.zshrc[machine] {
		if proto.IsExternalLauncher(l.Name) || l.Source == proto.ExternalLauncherSource {
			if names == nil {
				names = map[string]bool{}
			}
			names[l.Name] = true
		}
	}
	return names
}

// launchersChanged consumes the generic node event without blocking its read loop.
// Model caches are machine-scoped; the existing snapshot refresher owns persistence.
func (s *Service) launchersChanged(machine string) {
	c := &s.models
	c.cacheMu.Lock()
	for key := range c.cacheAt {
		if strings.HasPrefix(key, machine+"\x00") {
			delete(c.cacheAt, key)
			delete(c.have, key)
		}
	}
	c.cacheMu.Unlock()
	c.zshrcMu.Lock()
	if c.zshrcRefreshing {
		c.zshrcRefreshAgain = true
		c.zshrcMu.Unlock()
		return
	}
	c.zshrcMu.Unlock()
	s.refreshZshrcLaunchers(context.Background())
}
