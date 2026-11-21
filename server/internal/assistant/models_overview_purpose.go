package assistant

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/compat"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

func (s *Service) overviewPurposes(ctx context.Context, providers []ModelProvider, profile *modelOverviewProfile) ([]overviewPurpose, error) {
	ids := slices.Clone(modelConfigIDs)
	for _, extension := range s.modelExtensions(ctx) {
		ids = append(ids, assistantwire.ExtensionPurposePrefix+extension.StorageKey())
	}
	out := make([]overviewPurpose, 0, len(ids))
	for _, id := range ids {
		p, err := s.modelPurpose(ctx, id, providers)
		if err != nil {
			return nil, err
		}
		// Model settings fills in a provider's display model. The launch rule needs the raw binding,
		// where an empty model still means that the launcher chooses its own default.
		b, err := s.resolveModel(ctx, id)
		if err != nil {
			return nil, err
		}
		if strings.HasPrefix(b.Provider, "launcher.") {
			i := slices.IndexFunc(providers, func(provider ModelProvider) bool { return provider.ID == b.Provider })
			if p.StatusCode == modelStatusProviderIncompatible && i < 0 {
				// The saved catalog cannot establish that a launcher missing from the catalog is incompatible.
				// The per-computer fit reports a known absence or an unknown inventory on its own evidence.
				p.StatusCode, p.Status = modelStatusOther, modelUnverified
			} else if p.StatusCode == modelStatusOK && i >= 0 && providers[i].Hidden {
				p.StatusCode = modelStatusHiddenInUse
			}
		}
		out = append(out, overviewPurpose{purpose: p, binding: b, source: overviewPurposeSource(p, b, profile),
			runsOn: s.overviewPurposeTarget(ctx, p, b)})
	}
	return out, nil
}

func overviewPurposeSource(p ModelPurpose, b ModelBinding, profile *modelOverviewProfile) modelOverviewSource {
	if profile != nil && profile.appliedPurposes[p.ID] && !slices.Contains(profile.Changes, p.ID) {
		return modelOverviewSource{Code: "profile", Profile: cmp.Or(profile.Name, profile.ID)}
	}
	if p.Override != (ModelBinding{}) {
		return modelOverviewSource{Code: "set"}
	}
	if from := modelRecordID(p); from != p.ID {
		return modelOverviewSource{Code: "inherited", From: from}
	}
	if p.Parent != "" {
		return modelOverviewSource{Code: "inherited", From: p.Parent}
	}
	if p.ID == "agent.delegate" || p.ID == purposeTranslate {
		return modelOverviewSource{Code: "inherited", From: "inbox.triage"}
	}
	if p.Kind == "api" && strings.HasPrefix(b.Provider, "launcher.") {
		return modelOverviewSource{Code: "auto"}
	}
	return modelOverviewSource{Code: "default"}
}

func (s *Service) overviewPurposeTarget(ctx context.Context, p ModelPurpose, b ModelBinding) string {
	if p.ID == "agent.new" {
		return "each"
	}
	machine := p.MachineID
	if p.Kind == "api" {
		// The purpose reuses Main chat's computer only when model.run supplies the API. The
		// direct connection has its own ViaMachine. These are settings reads, without opening keys.
		if strings.HasPrefix(b.Provider, "legacy.") {
			machine = s.legacyModelConfig(ctx, b.Provider).ViaMachine
		} else if name, ok := strings.CutPrefix(b.Provider, "launcher."); ok {
			if l, managed := s.managedNamed(ctx, name); managed && (l.Kind == "api" || messagesAPI(l.API)) {
				machine = l.ViaMachine
			}
		} else {
			machine = ""
		}
	}
	if machine != "" {
		if _, ok := s.device(ctx, machine); ok {
			return machine
		}
		return "" // the configured computer is not in this manager's snapshot
	}
	if p.Kind != "launcher" {
		return "api"
	}
	return ""
}

const fitChatRefused launcherFit = "refused"

func (s *Service) overviewChat(ctx context.Context, device Device, seen []proto.Launcher, entry overviewPurpose) modelOverviewChat {
	p, b := entry.purpose, entry.binding
	name, isLauncher := strings.CutPrefix(b.Provider, "launcher.")
	chat := modelOverviewChat{Purpose: p.ID, Name: p.Name, Source: entry.source,
		modelAgentFit: modelAgentFit{Launcher: name, Model: b.Model, Effort: b.Effort, Fit: fitUnchecked}}
	if !isLauncher || name == "" || proto.IsExternalLauncher(name) {
		return chat
	}
	switch p.ID {
	case "assistant", purposeAgent, purposeLarkAgent, purposeSupervise:
	default:
		return chat // other purposes add separate guards to the ordinary headless launch
	}
	if chat.Model != "" && !proto.ValidModelID(chat.Model) {
		chat.Model = "" // applyModelBinding clears old configured models that are not valid IDs
	}
	if name == "claude" {
		s.setOverviewChatLauncher(ctx, device.ID, &chat, ConfiguredLauncher{Launcher: proto.Launcher{Name: "claude"}}, fitAsSet)
		return chat
	}
	ctx = withUnhidden(ctx, name)
	lang := s.requestCtxLang(ctx)
	managed, isManaged := s.managedNamed(ctx, name)
	if isManaged && managed.Kind == "api" && !messagesAPI(managed.API) {
		chat.Fit = fitChatRefused
		chat.Note = i18n.T(lang, "assistant-models.err_api_as_launcher")
		return chat
	}
	if isManaged && s.overviewOwnServiceUnknown(device.ID, managed) {
		return chat
	}

	picker := s.pickerLaunchersFrom(ctx, device.ID, s.overviewLauncherList(ctx, device.ID, seen))
	catalogKnown := len(seen) > 0 && mainChatCatalogKnown(picker)
	fits, err := s.assistantLaunchersOf(ctx, device.ID, picker)
	if err != nil {
		return chat
	}
	fit, label := mainChatFitOn(name, picker, fits)
	switch {
	case isManaged && !s.usableOn(ctx, managed, device.ID):
		fit = mainChatGone
	case isManaged && (managed.Kind == "api" || managed.Agent != "agy"):
		// The live path can verify an ordinary managed provider even before the shell catalog arrives.
		allowed, err := s.assistantProviders(ctx, device.ID, nil)
		if err != nil {
			return chat
		}
		if allowed[name] {
			s.setOverviewChatLauncher(ctx, device.ID, &chat, ConfiguredLauncher{
				Launcher: proto.Launcher{Name: name, Source: "persona", Agent: managed.AgentType()},
			}, fitAsSet)
			return chat
		}
		fit, label = mainChatRefused, name
	case !catalogKnown:
		return chat
	}

	if fit == mainChatFits {
		i := slices.IndexFunc(fits, func(l ConfiguredLauncher) bool { return l.Name == name })
		s.setOverviewChatLauncher(ctx, device.ID, &chat, fits[i], fitAsSet)
		return chat
	}

	_, substitutes := s.launcherRecovery(ctx, device.ID, name)
	kind := overviewChatLauncherKind(name, picker, managed, isManaged)
	if len(substitutes) > 0 && (kind == "" || !catalogKnown) {
		return chat
	}
	to := mainChatInsteadOn(name, kind, fits, substitutes, func(candidate string) bool {
		_, bad := s.launcherUnusable(ctx, device.ID, candidate)
		return bad
	})
	if to == "" {
		chat.Fit = fitChatRefused
		if fit == mainChatRefused {
			instead, _ := assistantFor(fits, name)
			chat.Note = i18n.Localize(s.mainChatRefusal(ctx, device.ID, label, launcherLabel(instead)), lang)
		} else {
			chat.Note = i18n.T(lang, "assistant-start.launcher_no_substitute",
				i18n.Args{"launcher": name, "machine": firstNonEmpty(device.Name, device.ID)})
		}
		return chat
	}
	if l, ok := s.managedNamed(ctx, to); ok && s.overviewOwnServiceUnknown(device.ID, l) {
		return chat
	}
	if chat.Model != "" && !s.overviewChatModelKnown(ctx, device.ID, to, chat.Model) {
		return chat // a live model list or its aliases may change whether the model stays on this substitute
	}
	i := slices.IndexFunc(fits, func(l ConfiguredLauncher) bool { return l.Name == to })
	s.setOverviewChatLauncher(ctx, device.ID, &chat, fits[i], fitFallback)
	chat.InsteadOf = name
	chat.Note = i18n.T(lang, "assistant-start.launcher_swapped", i18n.Args{
		"launcher": name, "machine": firstNonEmpty(device.Name, device.ID), "to": to, "model": chat.Model,
	})
	return chat
}

func (s *Service) overviewOwnServiceUnknown(machine string, provider storedLauncher) bool {
	if !provider.SkipsOwnService() || s.scopeReason(provider, machine) != "" {
		return false
	}
	st, fresh := s.ownServiceCached(machine, provider.AgentType())
	return !fresh || !st.Known
}

func overviewChatLauncherKind(name string, picker []ConfiguredLauncher, managed storedLauncher, isManaged bool) string {
	if kind := assistantKind(name); kind != "" {
		return kind
	}
	if isManaged {
		return agentKind(managed.AgentType())
	}
	for _, l := range picker {
		if l.Name == name {
			return agentKind(cmp.Or(l.AgentType, l.Agent))
		}
	}
	return ""
}

func (s *Service) overviewChatModelKnown(ctx context.Context, machine, launcher, model string) bool {
	// Only positive cached membership is conclusive here. The live rule also checks folded
	// aliases and remembered IDs when a list does not contain the given spelling.
	key := launcherModelCacheKey(withLauncherPurpose(ctx, "assistant"), machine, launcher)
	s.models.cacheMu.Lock()
	defer s.models.cacheMu.Unlock()
	return time.Since(s.models.cacheAt[key]) < 5*time.Minute && s.models.have[key][model]
}

func (s *Service) setOverviewChatLauncher(ctx context.Context, machine string, chat *modelOverviewChat, l ConfiguredLauncher, fit launcherFit) {
	chat.Launcher, chat.Agent, chat.Fit = l.Name, agentKind(cmp.Or(l.AgentType, l.Agent)), fit
	chat.Runtime = "cli"
	if runtimeSource(l.Source) {
		chat.Runtime = compat.Source(l.Source)
	} else if l.Source == "persona" && chat.Agent == "agy" {
		// The node may hand a managed Agy launcher to its runtime. The managed row does not say
		// whether the shell shadows it on the computer.
		chat.Runtime = ""
	}
	health, _ := s.launcherUnusable(ctx, machine, l.Name)
	chat.Health = overviewHealth(health)
}
