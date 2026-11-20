package assistant

import (
	"context"
	"maps"
	"slices"
	"strings"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// configurablePermissionMode is false for the AI twin and meeting rooms: their safety policies fix how they run.
func (p ModelPurpose) configurablePermissionMode() bool {
	return p.Kind == "launcher" && p.ID != purposeAgent && p.ID != purposeMeetingRoom
}

func modelPermissionModesField(modes []string) *[]string {
	if modes == nil {
		return nil
	}
	return &modes
}

// modelPermissionOptions separates a terminal start from an Assistant/headless one. The same Codex or
// Antigravity launcher can be driven through distinct execution paths with different permission capabilities.
type modelPermissionOptions struct {
	engine, headless []string
}

func (m modelPermissionOptions) forPurpose(p ModelPurpose) []string {
	if p.launch() == "headless" {
		return m.headless
	}
	return m.engine
}

func (m modelPermissionOptions) common(other modelPermissionOptions) modelPermissionOptions {
	return modelPermissionOptions{engine: commonPermissionModes(m.engine, other.engine),
		headless: commonPermissionModes(m.headless, other.headless)}
}

func modelProviderPermissionOptions(provider *ModelProvider) modelPermissionOptions {
	m := modelPermissionOptions{}
	if provider == nil {
		return m
	}
	if provider.PermissionModes != nil {
		m.engine = *provider.PermissionModes
	}
	if provider.AssistantPermissionModes != nil {
		m.headless = *provider.AssistantPermissionModes
	}
	return m
}

func discoveredPermissionOptions(launcher proto.Launcher) modelPermissionOptions {
	agent := viewAgent(LauncherView{Name: launcher.Name, Agent: launcher.Agent})
	fit := startAgent{agent: agent, snapshot: agent, source: launcher.Source, permissionModes: launcher.PermissionModes}
	opts := modelPermissionOptions{
		engine:   fit.startPermissionModes(launcher.Name, false),
		headless: fit.startPermissionModes(launcher.Name, true),
	}
	if agent == "agy" {
		if opts.engine == nil {
			opts.engine = []string{} // the CLI path does not accept Persona's mode
		}
		if !launcher.Assistant {
			opts.headless = []string{}
		}
	}
	return opts
}

func commonPermissionModes(a, b []string) []string {
	if a == nil || b == nil {
		return nil
	}
	modes := make([]string, 0, len(a))
	for _, mode := range a {
		if slices.Contains(b, mode) {
			modes = append(modes, mode)
		}
	}
	return modes
}

func checkPermissionModeID(mode string) error {
	if mode != "" && !slices.Contains(proto.PermissionModes, mode) {
		return i18n.Err("assistant-models.err_permission_unsupported")
	}
	return nil
}

func checkModelPermissionMode(mode string, allowed []string) error {
	if mode == "" {
		return nil
	}
	if !slices.Contains(allowed, mode) {
		return i18n.Err("assistant-models.err_permission_unsupported")
	}
	return nil
}

// modelPermissionsByProvider uses the catalog's same supported modes for profile writes. A runtime can supply a
// start tier even if the model-provider catalog does not list it, so include known runtime-only names separately.
func (s *Service) modelPermissionsByProvider(ctx context.Context) map[string]modelPermissionOptions {
	modes := map[string]modelPermissionOptions{}
	for _, p := range s.modelProviders(ctx) {
		modes[p.ID] = modelProviderPermissionOptions(&p)
	}
	runtimes := map[string]modelPermissionOptions{}
	for _, machine := range s.zshrcLaunchers(ctx, false) {
		for _, launcher := range machine.Launchers {
			name := launcherProvider(launcher.Name)
			if _, exists := modes[name]; exists || launcher.PermissionModes == 0 || proto.IsExternalLauncher(launcher.Name) {
				continue
			}
			allowed := discoveredPermissionOptions(launcher)
			if previous, exists := runtimes[name]; exists {
				allowed = allowed.common(previous)
			}
			runtimes[name] = allowed
		}
	}
	for name, allowed := range runtimes {
		modes[name] = allowed
	}
	return modes
}

var errModelProfilePermissionOwner = i18n.Err("assistant-model_profiles.err_permission_owner")

func modelPermissionChange(id string, before, after ModelBinding) (ModelProfilePermissionChange, bool) {
	if before.PermissionMode == after.PermissionMode && (after.PermissionMode == "" || before.Provider == after.Provider) {
		return ModelProfilePermissionChange{}, false
	}
	return ModelProfilePermissionChange{Purpose: id, Before: before.PermissionMode, After: after.PermissionMode,
		BeforeLauncher: strings.TrimPrefix(before.Provider, "launcher."), AfterLauncher: strings.TrimPrefix(after.Provider, "launcher.")}, true
}

func checkProfilePermissionReview(by ProfileActor, expected, actual []ModelProfilePermissionChange) error {
	if by.Kind == "assistant" && len(actual) > 0 {
		return errModelProfilePermissionOwner
	}
	if expected != nil && !slices.Equal(expected, actual) {
		return errModelProfileConflict
	}
	return nil
}

func tiersInProfile(ps modelProfiles, id string) map[string]ModelTier {
	for _, p := range ps.Profiles {
		if p.ID == id {
			return p.Tiers
		}
	}
	return nil
}

func tierPermissionChanges(before, after map[string]ModelTier) []ModelProfilePermissionChange {
	names := map[string]struct{}{}
	for name, tier := range before {
		if tier.PermissionMode != "" {
			names[name] = struct{}{}
		}
	}
	for name, tier := range after {
		if tier.PermissionMode != "" {
			names[name] = struct{}{}
		}
	}
	var changes []ModelProfilePermissionChange
	for _, name := range slices.Sorted(maps.Keys(names)) {
		old, next := before[name], after[name]
		if old.PermissionMode != next.PermissionMode || old.Launcher != next.Launcher {
			changes = append(changes, ModelProfilePermissionChange{Tier: name, Before: old.PermissionMode, After: next.PermissionMode,
				BeforeLauncher: old.Launcher, AfterLauncher: next.Launcher})
		}
	}
	return changes
}

func (s *Service) checkTierPermissionChanges(ctx context.Context, changes []tierChange) error {
	var modes map[string]modelPermissionOptions
	for _, c := range changes {
		if c.After == nil || c.After.PermissionMode == "" {
			continue
		}
		if c.Before != nil && c.Before.PermissionMode == c.After.PermissionMode && c.Before.Launcher == c.After.Launcher {
			continue
		}
		if modes == nil {
			modes = s.modelPermissionsByProvider(ctx)
		}
		if err := checkModelPermissionMode(c.After.PermissionMode, modes[launcherProvider(c.After.Launcher)].engine); err != nil {
			return err
		}
	}
	return nil
}
