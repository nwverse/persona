package assistant

import (
	"cmp"
	"context"
	"maps"
	"slices"
	"strings"

	"github.com/nwverse/persona/server/internal/compat"
	"github.com/nwverse/persona/server/internal/proto"
)

type modelOverviewProfile struct {
	ID      string              `json:"id"`
	Name    string              `json:"name"`
	Drift   int                 `json:"drift"`
	Changes []string            `json:"changes"`
	Tiers   []modelOverviewTier `json:"tiers"`

	appliedPurposes map[string]bool
}

type modelOverviewTier struct {
	ModelTier
	Name string   `json:"name"`
	On   []string `json:"on"`
}

type modelOverviewTierFit struct {
	Name      string `json:"name"`
	Available bool   `json:"available"`

	known bool
}

type modelOverviewProvider struct {
	ID          string              `json:"-"`
	Name        string              `json:"name"`
	DisplayName string              `json:"display_name"`
	Source      string              `json:"source"`
	RCFile      string              `json:"rc_file,omitempty"`
	Agent       string              `json:"agent,omitempty"`
	Model       string              `json:"model,omitempty"`
	Hidden      bool                `json:"hidden"`
	InScope     bool                `json:"in_scope"`
	HasKey      *bool               `json:"has_key"`
	Health      *launcherHealthView `json:"health"`
	UsedBy      []string            `json:"used_by"`
	InPicker    bool                `json:"in_picker"`

	present      bool
	failing      bool
	tierKnown    bool
	hiddenUsable bool
	missingKey   bool
}

type modelOverviewProviderIndex struct {
	UsedBy    []string `json:"used_by"`
	MissingOn []string `json:"missing_on"`
	FailingOn []string `json:"failing_on"`
	Hidden    bool     `json:"hidden"`
}

func (s *Service) overviewProfile(ctx context.Context) (*modelOverviewProfile, error) {
	if s.store() == nil {
		return nil, nil
	}
	s.models.mu.Lock()
	defer s.models.mu.Unlock()
	profiles, _, err := s.readModelProfiles(ctx)
	if err != nil {
		return nil, err
	}
	state, err := s.readModelProfileState(ctx)
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(profiles.Profiles, func(p ModelProfile) bool { return p.ID == state.Active })
	if state.Active == "" || i < 0 {
		return nil, nil
	}
	profile := profiles.Profiles[i]
	records, err := s.profileRecords(ctx, []ModelProfile{profile})
	if err != nil {
		return nil, err
	}
	stored, err := s.readStoredLaunchers(ctx)
	if err != nil {
		return nil, err
	}
	changes := modelProfileChanges(profile, records, stored)
	out := &modelOverviewProfile{
		ID: profile.ID, Name: profile.Name, Drift: len(changes), Changes: changes,
		Tiers: make([]modelOverviewTier, 0, len(profile.Tiers)), appliedPurposes: map[string]bool{},
	}
	for _, name := range slices.Sorted(maps.Keys(profile.Tiers)) {
		out.Tiers = append(out.Tiers, modelOverviewTier{ModelTier: profile.Tiers[name], Name: name, On: []string{}})
	}
	// The active profile can be applied more than once without leaving it. Retain
	// the choices from applications in that active span, including rollbacks.
	for i := len(state.Events) - 1; i >= 0; i-- {
		event := state.Events[i]
		if event.Action == "seed" {
			break
		}
		if event.Action != "switch" && event.Action != "rollback" {
			continue
		}
		if event.To != state.Active {
			break
		}
		for _, id := range event.Changed {
			if _, ok := profile.Purposes[id]; ok {
				out.appliedPurposes[id] = true
			}
		}
		if event.From != state.Active {
			break
		}
	}
	return out, nil
}

// overviewHealth projects recorded kind and timing. A raw probe error can echo
// the command or its arguments.
func overviewHealth(record LauncherHealth) *launcherHealthView {
	if record.State == "" {
		return nil
	}
	view := &launcherHealthView{State: record.State, Kind: record.Kind, CheckedAt: record.CheckedAt}
	if reset, ok := record.limitReset(); ok {
		view.ResetsAt = &reset
	}
	return view
}

func (s *Service) overviewProviders(ctx context.Context, device Device, seen zshrcLaunchers, providers []ModelProvider, uses map[string][]string) []modelOverviewProvider {
	raw := s.overviewLauncherList(ctx, device.ID, seen.Launchers)
	listed := make(map[string]bool, len(raw))
	for _, entry := range raw {
		listed[entry.Name] = true
	}
	hidden := s.hiddenLaunchers(ctx)
	// A hidden shell entry can still be used by a configuration that names it.
	for _, entry := range seen.Launchers {
		if !listed[entry.Name] && slices.Contains(hidden, entry.Name) {
			entry.Source = cmp.Or(compat.Source(entry.Source), "zshrc")
			raw = append(raw, entry)
		}
	}
	if !listed["claude"] && device.ClaudeVersion != "" && slices.Contains(hidden, "claude") {
		raw = append(raw, proto.Launcher{Name: "claude", Source: "builtin"})
	}
	catalog := s.pickerLaunchersFrom(ctx, device.ID, raw)
	byName := make(map[string]ConfiguredLauncher, len(catalog))
	for _, entry := range catalog {
		if entry.Name != "" && !proto.IsExternalLauncher(entry.Name) && entry.Source != proto.ExternalLauncherSource {
			byName[entry.Name] = entry
		}
	}
	managed := map[string]storedLauncher{}
	for _, entry := range s.storedLaunchers(ctx) {
		if !proto.IsExternalLauncher(entry.Name) {
			managed[entry.Name] = entry
			if _, ok := byName[entry.Name]; !ok {
				byName[entry.Name] = ConfiguredLauncher{Launcher: proto.Launcher{Name: entry.Name}}
			}
		}
	}
	metadata := make(map[string]ModelProvider, len(providers))
	for _, provider := range providers {
		metadata[provider.ID] = provider
	}
	keys := s.launcherMachineKeys(ctx)
	health := map[string]LauncherHealth{}
	for _, row := range s.healthRecords(ctx, "", device.ID) {
		health[row.Launcher] = row
	}
	terminal := agentStartHost(device, "") == ""
	known := machineLauncherListKnown(seen.Launchers)
	out := make([]modelOverviewProvider, 0, len(byName))
	for _, name := range slices.Sorted(maps.Keys(byName)) {
		id, entry := launcherProvider(name), byName[name]
		info, record := metadata[id], health[name]
		p := modelOverviewProvider{
			ID: id, Name: name, DisplayName: cmp.Or(entry.DisplayName, info.Name, name),
			Source: entry.Source, Agent: entry.AgentType, Model: entry.Model,
			Hidden: slices.Contains(hidden, name), InScope: true, Health: overviewHealth(record),
			UsedBy: nonNil(uses[id]), present: true, failing: record.unusable(),
		}
		if provider, ok := managed[name]; ok {
			p.Source, p.Agent, p.Model = "persona", provider.AgentType(), provider.Model
			p.DisplayName = cmp.Or(provider.DisplayName, name)
			p.Hidden = false
			p.InScope = s.usableOnAsking(ctx, provider, device.ID, false)
			p.present = p.InScope
			if p.InScope {
				hasKey := s.keySealedFor(provider, keys[name], device.ID) != ""
				p.HasKey = &hasKey
				// Native login and gateway credentials carried in custom headers or environment
				// have no separate saved key. Direct API credentials are checked per purpose.
				p.missingKey = provider.Kind != "api" && provider.BaseURL != "" && !hasKey &&
					provider.EnvSealed == "" && provider.HeadersSealed == "" && provider.Env == "" && provider.Headers == ""
			}
		}
		if p.Source == "zshrc" {
			p.RCFile = cmp.Or(seen.Sync.RCFile, defaultRCFile)
		}
		// The live picker keeps the configured default and not_in_terminal rows
		// visible even when their latest health record discourages the start.
		showFailed := record.Kind == errKindNotInTerminal || slices.Contains(p.UsedBy, "agent.new")
		p.InPicker = terminal && listed[name] && p.InScope && !p.Hidden && (!p.failing || showFailed)
		p.hiddenUsable = terminal && known && p.present && p.InScope && p.Hidden && !p.failing
		p.tierKnown = terminal && (!p.Hidden || known)
		out = append(out, p)
	}
	return out
}

func overviewTiers(profile *modelOverviewProfile, machine modelOverviewMachine) []modelOverviewTierFit {
	out := []modelOverviewTierFit{}
	if profile == nil {
		return out
	}
	providers := make(map[string]modelOverviewProvider, len(machine.Providers))
	for _, provider := range machine.Providers {
		providers[provider.Name] = provider
	}
	for i := range profile.Tiers {
		tier := &profile.Tiers[i]
		provider, ok := providers[tier.Launcher]
		fit := modelOverviewTierFit{Name: tier.Name, known: machine.catalogKnown}
		if ok {
			fit.Available = provider.InPicker || provider.hiddenUsable
			fit.known = provider.tierKnown
		}
		if fit.Available {
			tier.On = append(tier.On, machine.ID)
			slices.Sort(tier.On)
		}
		out = append(out, fit)
	}
	return out
}

func mergeOverviewProviderIndex(index map[string]modelOverviewProviderIndex, machine modelOverviewMachine) {
	present := make(map[string]bool, len(machine.Providers))
	for _, provider := range machine.Providers {
		entry := index[provider.ID]
		entry.UsedBy, entry.Hidden = provider.UsedBy, entry.Hidden || provider.Hidden
		present[provider.ID] = true
		if !provider.present {
			entry.MissingOn = append(entry.MissingOn, machine.ID)
		}
		if provider.present && provider.failing {
			entry.FailingOn = append(entry.FailingOn, machine.ID)
		}
		index[provider.ID] = entry
	}
	for id, entry := range index {
		if machine.catalogKnown && strings.HasPrefix(id, "launcher.") && !present[id] {
			entry.MissingOn = append(entry.MissingOn, machine.ID)
		}
		entry.UsedBy = nonNil(entry.UsedBy)
		entry.MissingOn, entry.FailingOn = nonNil(entry.MissingOn), nonNil(entry.FailingOn)
		slices.Sort(entry.MissingOn)
		slices.Sort(entry.FailingOn)
		index[id] = entry
	}
}
