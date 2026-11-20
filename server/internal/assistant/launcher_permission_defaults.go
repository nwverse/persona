package assistant

import (
	"context"
	"slices"

	"github.com/nwverse/persona/server/internal/proto"
)

// launchPermission is a preference for one launcher. Host fitting can change
// the launcher, in which case a preference for the earlier launcher is omitted.
type launchPermission struct {
	launcher string
	mode     string
}

func (s *Service) launchPermissionDefault(ctx context.Context, purpose, machine, launcher string, fit startAgent, preset launchPermission, headless bool) string {
	if launcher == "" || proto.IsExternalLauncher(launcher) || s.isWorkMachine(machine) {
		return ""
	}
	if preset.launcher == launcher && preset.mode != "" && fit.acceptsConfiguredMode(launcher, preset.mode, headless) {
		return preset.mode
	}
	binding, err := s.resolveModel(ctx, purpose)
	if err != nil || binding.Provider != launcherProvider(launcher) || binding.PermissionMode == "" {
		return ""
	}
	if headless && fit.agent == "" {
		fit.agent = s.launcherAgent(withUnhidden(ctx, launcher), machine, launcher)
	}
	if fit.acceptsConfiguredMode(launcher, binding.PermissionMode, headless) {
		return binding.PermissionMode
	}
	return ""
}

func (fit startAgent) acceptsConfiguredMode(launcher, mode string, headless bool) bool {
	return slices.Contains(fit.startPermissionModes(launcher, headless), mode)
}

func (fit startAgent) startPermissionModes(launcher string, headless bool) []string {
	if proto.IsExternalLauncher(launcher) || (!headless && (fit.source == "codex_app" || fit.source == "claude_desktop")) {
		return nil
	}
	agent := fit.snapshot // a compatibility guess about a shell wrapper is insufficient
	if agent == "" {
		switch {
		case headless:
			agent = fit.agent
		case launcher == "claude" || launcher == "codex":
			agent = launcher
		case fit.permissionModes != 0:
			agent = fit.agent
		}
	}
	if agent == "agy" && !headless {
		// An explicitly requested herdr target starts the CLI even when a
		// companion also advertises it.
		if fit.permissionHost == proto.EngineHerdr || fit.permissionHost == proto.EngineHerdrCore ||
			(fit.source != "agy" && fit.permissionModes == 0) {
			return nil
		}
	}
	if agent == "codex" && !headless && fit.permissionModes == 0 {
		// The terminal implementation enforces these two. Default and
		// Auto have native semantics on Assistant's Codex app-server.
		return []string{"plan", proto.ModeBypass}
	}
	return configuredPermissionModes(agent, fit.permissionModes)
}

func (s *Service) plannedPermissionDefault(ctx context.Context, task PlanTask, machine, launcher string, fit startAgent, preset launchPermission) string {
	if task.Readonly || task.preservePermission || task.InboxHandoff != "" || task.Parent != "" {
		return ""
	}
	return s.launchPermissionDefault(ctx, "agent.new", machine, launcher, fit, preset, false)
}

// A general extension cannot opt into its owner's global mode by opening a
// conversation. The Lark-specific setting is an explicit owner choice for the
// extension's dedicated entry, so its inherited Agents setting isn't used here.
func (s *Service) mayUseHeadlessPermissionDefault(ctx context.Context, params StartParams, purpose string) bool {
	switch params.Origin {
	case OriginExtension:
		return false
	case OriginLarkAgent:
		if purpose != purposeLarkAgent {
			return false
		}
		record, err := s.modelRecord(ctx, purpose)
		return err == nil && record.Binding.PermissionMode != ""
	default:
		return true
	}
}
