package assistant

import (
	"context"
	"slices"
	"strings"

	"github.com/nwverse/persona/server/internal/compat"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// modelAgentFit describes the selection for a start that follows the configured Agents purpose.
// InsteadOf is also retained when the substitute's health changes its fit to failing.
type modelAgentFit struct {
	Launcher       string              `json:"launcher"`
	Model          string              `json:"model"`
	Effort         string              `json:"effort"`
	PermissionMode string              `json:"permission_mode,omitempty"`
	Agent          string              `json:"agent,omitempty"`
	Fit            launcherFit         `json:"fit"`
	InsteadOf      string              `json:"instead_of,omitempty"`
	Note           string              `json:"note,omitempty"`
	Health         *launcherHealthView `json:"health,omitempty"`
}

// machineLauncherListKnown mirrors launchersHere: a managed or built-in entry can be added by the
// manager, so it alone cannot establish that a shell catalog was read.
func machineLauncherListKnown(list []proto.Launcher) bool {
	return slices.ContainsFunc(list, func(l proto.Launcher) bool {
		source := compat.Source(l.Source)
		return source != "persona" && source != "builtin"
	})
}

// agentPickerLauncherFit fits against the same live read that populates the picker, before hiding
// unhealthy rows. A hidden configured launcher belongs to the fit but stays out of the picker.
func (s *Service) agentPickerLauncherFit(ctx context.Context, machine Device, host string) ([]ConfiguredLauncher, *modelAgentFit, error) {
	b, bindingErr := s.resolveModel(ctx, "agent.new")
	name := strings.TrimPrefix(b.Provider, "launcher.")
	unhide := bindingErr == nil && strings.HasPrefix(b.Provider, "launcher.") &&
		slices.Contains(s.hiddenLaunchers(ctx), name)
	readCtx := ctx
	if unhide {
		readCtx = withUnhidden(ctx, name)
	}
	raw, runtime, known, err := s.machineLaunchersOn(readCtx, machine.ID, host)
	if err != nil {
		return nil, nil, err
	}
	var here *modelAgentFit
	if bindingErr == nil {
		// Hub-configured wrappers supplement an actual node list, as in launchersHere.
		fitList := raw
		hasInventory := known && machineLauncherListKnown(raw)
		if hasInventory {
			fitList = appendConfiguredLaunchers(raw, s.hello(ctx).Models.Launchers[machine.ID])
		}
		fit := s.modelAgentFit(ctx, machine, b, fitList, hasInventory, runtime)
		here = &fit
	}
	if unhide {
		raw = slices.DeleteFunc(raw, func(l proto.Launcher) bool {
			return l.Name == name && compat.Source(l.Source) != "persona"
		})
	}
	return s.pickerLaunchersFrom(ctx, machine.ID, raw), here, nil
}

func appendConfiguredLaunchers(list []proto.Launcher, configured []ConfiguredLauncher) []proto.Launcher {
	if len(configured) == 0 {
		return list
	}
	combined := slices.Clone(list)
	for _, c := range configured {
		if c.Name != "" && !proto.IsExternalLauncher(c.Name) && c.Source != proto.ExternalLauncherSource &&
			!slices.ContainsFunc(combined, func(l proto.Launcher) bool { return l.Name == c.Name }) {
			combined = append(combined, c.Launcher)
		}
	}
	return combined
}

func (s *Service) modelAgentFit(ctx context.Context, machine Device, b ModelBinding, list []proto.Launcher, known, engineRuntime bool) modelAgentFit {
	name := strings.TrimPrefix(b.Provider, "launcher.")
	result := modelAgentFit{
		Launcher: name, Model: b.Model, Effort: b.Effort, PermissionMode: b.PermissionMode, Fit: fitUnchecked,
	}
	if name == "" || !strings.HasPrefix(b.Provider, "launcher.") {
		return result
	}
	if engineRuntime {
		return result // the shell snapshot cannot predict the engine's provider negotiation
	}
	if err := s.launcherScopeErr(ctx, machine.ID, name); err != nil {
		result.Fit, result.Note = fitBlocked, i18n.Localize(err, s.requestCtxLang(ctx))
		return result
	}
	result.Launcher, result.Fit = fitOn(machine, list, known, name, s.launcherEverywhere(ctx, name))
	if result.Fit == fitFallback {
		// The substitute takes its own defaults when the caller supplied no per-start choices.
		result.Model, result.Effort, result.PermissionMode = "", "", ""
		result.InsteadOf = name
		result.Note = i18n.T(s.requestCtxLang(ctx), "assistant-tasks.launcher_swapped", i18n.Args{
			"launcher": name, "machine": firstNonEmpty(machine.Name, machine.ID), "to": result.Launcher,
		})
	} else if result.Fit == fitAsSet && result.Model != "" && !proto.ValidModelID(result.Model) {
		result.Model = "" // the real start drops legacy model IDs it cannot pass as an argv value
	}
	discovered := make([]ConfiguredLauncher, 0, len(list))
	for _, entry := range list {
		discovered = append(discovered, ConfiguredLauncher{Launcher: entry})
	}
	result.Agent = launcherKindSnapshot(discovered, s.hello(ctx).Models.Launchers[machine.ID], result.Launcher)
	if provider, ok := s.managedNamed(ctx, result.Launcher); ok {
		result.Agent = provider.AgentType()
	}
	if health, bad := s.launcherUnusable(ctx, machine.ID, result.Launcher); health.State != "" {
		result.Health = overviewHealth(health)
		if bad && result.Fit != fitUnchecked {
			result.Fit = fitFailing
		}
	}
	return result
}
