package assistant

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/proto"
)

type modelOverviewReadKey struct{}

// modelOverviewRead holds the machine inventory for the duration of an overview request.
// Existing model resolution also uses that inventory when it needs a machine or launcher list.
type modelOverviewRead struct {
	devices          []Device
	launchers        []zshrcLaunchers
	catalogCaptured  bool
	catalogWasCached bool
}

func overviewReadFrom(ctx context.Context) *modelOverviewRead {
	read, _ := ctx.Value(modelOverviewReadKey{}).(*modelOverviewRead)
	return read
}

type modelOverview struct {
	Revision    string    `json:"revision"`
	GeneratedAt time.Time `json:"generated_at"`
	modelOverviewData
}

type modelOverviewData struct {
	Snapshot  modelOverviewSnapshot                 `json:"snapshot"`
	Agents    modelOverviewAgents                   `json:"agents"`
	Profile   *modelOverviewProfile                 `json:"profile"`
	Machines  []modelOverviewMachine                `json:"machines"`
	Purposes  []modelOverviewPurpose                `json:"purposes"`
	Providers map[string]modelOverviewProviderIndex `json:"providers"`
	Problems  []modelOverviewProblem                `json:"problems"`
}

type modelOverviewSnapshot struct {
	Refreshing     bool       `json:"refreshing"`
	OldestSyncedAt *time.Time `json:"oldest_synced_at"`
}

type modelOverviewSource struct {
	Code    string `json:"code"`
	From    string `json:"from,omitempty"`
	Profile string `json:"profile,omitempty"`
}

type modelOverviewAgents struct {
	ModelBinding
	Launcher string              `json:"launcher"`
	Source   modelOverviewSource `json:"source"`
}

type modelOverviewPurpose struct {
	ID         string                 `json:"id"`
	StatusCode ModelPurposeStatusCode `json:"status_code"`
	RunsOn     string                 `json:"runs_on"`
}

type modelOverviewMachine struct {
	ID            string                  `json:"id"`
	Name          string                  `json:"name"`
	Online        bool                    `json:"online"`
	LastSeen      time.Time               `json:"last_seen,omitzero"`
	Hub           bool                    `json:"hub"`
	ClaudeVersion string                  `json:"claude_version,omitempty"`
	Node          modelOverviewNode       `json:"node"`
	Sync          LauncherSync            `json:"sync"`
	List          string                  `json:"list"`
	Roles         []string                `json:"roles"`
	NewAgent      modelAgentFit           `json:"new_agent"`
	Chats         []modelOverviewChat     `json:"chats"`
	Providers     []modelOverviewProvider `json:"providers"`
	Tiers         []modelOverviewTierFit  `json:"tiers"`

	catalogKnown bool
}

type modelOverviewNode struct {
	Version            string `json:"version,omitempty"`
	ModelConfiguration bool   `json:"model_configuration"`
}

type modelOverviewChat struct {
	Purpose string              `json:"purpose"`
	Name    string              `json:"name"`
	Runtime string              `json:"runtime,omitempty"`
	Source  modelOverviewSource `json:"source"`
	modelAgentFit
}

type overviewPurpose struct {
	purpose ModelPurpose
	binding ModelBinding
	source  modelOverviewSource
	runsOn  string
}

func (s *Service) modelOverviewRoute(w http.ResponseWriter, r *http.Request) {
	data, err := s.modelOverview(r.Context())
	if err != nil {
		apierr.Call(w, err)
		return
	}
	apierr.WriteJSON(w, data)
}

func (s *Service) modelOverview(ctx context.Context) (modelOverview, error) {
	ctx = withSettingsMemo(ctx)
	read := &modelOverviewRead{devices: s.devices(ctx)}
	ctx = context.WithValue(ctx, modelOverviewReadKey{}, read)
	read.launchers, read.catalogWasCached = s.zshrcLaunchersLastSeen(ctx)
	read.catalogCaptured = true

	providers := s.modelProviders(ctx)
	profile, err := s.overviewProfile(ctx)
	if err != nil {
		return modelOverview{}, err
	}
	purposes, err := s.overviewPurposes(ctx, providers, profile)
	if err != nil {
		return modelOverview{}, err
	}
	data := modelOverviewData{
		Profile: profile, Machines: []modelOverviewMachine{}, Purposes: []modelOverviewPurpose{},
		Providers: map[string]modelOverviewProviderIndex{}, Problems: []modelOverviewProblem{},
	}
	uses := map[string][]string{}
	mainMachine := ""
	for _, purpose := range purposes {
		p := purpose.purpose
		data.Purposes = append(data.Purposes, modelOverviewPurpose{ID: p.ID, StatusCode: p.StatusCode, RunsOn: purpose.runsOn})
		uses[purpose.binding.Provider] = append(uses[purpose.binding.Provider], p.ID)
		if p.ID == "agent.new" {
			data.Agents = modelOverviewAgents{
				ModelBinding: purpose.binding, Launcher: strings.TrimPrefix(purpose.binding.Provider, "launcher."), Source: purpose.source,
			}
		}
		if p.ID == "assistant" {
			mainMachine = p.MachineID
		}
	}
	if profile != nil {
		for _, tier := range profile.Tiers {
			provider := launcherProvider(tier.Launcher)
			uses[provider] = append(uses[provider], "tier:"+tier.Name)
		}
	}
	for _, provider := range providers {
		data.Providers[provider.ID] = modelOverviewProviderIndex{UsedBy: nonNil(uses[provider.ID]), Hidden: provider.Hidden}
	}
	for i, device := range read.devices {
		machine := s.overviewMachine(ctx, device, read.launchers[i], data.Agents.ModelBinding, purposes, providers, profile, uses)
		if at := machine.Sync.SyncedAt; at != nil && (data.Snapshot.OldestSyncedAt == nil || at.Before(*data.Snapshot.OldestSyncedAt)) {
			data.Snapshot.OldestSyncedAt = at
		}
		data.Machines = append(data.Machines, machine)
		for _, provider := range machine.Providers {
			if _, exists := data.Providers[provider.ID]; !exists {
				data.Providers[provider.ID] = modelOverviewProviderIndex{UsedBy: provider.UsedBy, Hidden: provider.Hidden}
			}
		}
	}
	for _, machine := range data.Machines {
		mergeOverviewProviderIndex(data.Providers, machine)
	}
	slices.SortStableFunc(data.Machines, func(a, b modelOverviewMachine) int {
		switch {
		case (a.ID == mainMachine) != (b.ID == mainMachine):
			if a.ID == mainMachine {
				return -1
			}
			return 1
		case a.Online != b.Online:
			if a.Online {
				return -1
			}
			return 1
		default:
			return cmp.Or(cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), cmp.Compare(a.ID, b.ID))
		}
	})
	s.models.zshrcMu.Lock()
	data.Snapshot.Refreshing = s.models.zshrcRefreshing
	s.models.zshrcMu.Unlock()
	data.Problems = s.overviewProblems(ctx, data, purposes)
	raw, err := json.Marshal(data)
	if err != nil {
		return modelOverview{}, err
	}
	sum := sha256.Sum256(raw)
	return modelOverview{Revision: hex.EncodeToString(sum[:]), GeneratedAt: time.Now().UTC(), modelOverviewData: data}, nil
}

func (s *Service) overviewMachine(ctx context.Context, device Device, seen zshrcLaunchers, agents ModelBinding, purposes []overviewPurpose, providers []ModelProvider, profile *modelOverviewProfile, uses map[string][]string) modelOverviewMachine {
	machine := modelOverviewMachine{
		ID: device.ID, Name: cmp.Or(device.Name, device.ID), Online: device.Online, LastSeen: device.LastSeen, Hub: device.Kind == "local",
		ClaudeVersion: device.ClaudeVersion, Node: modelOverviewNode{device.Version, device.ModelConfiguration},
		Sync: seen.Sync, List: "none", Roles: []string{}, Chats: []modelOverviewChat{},
	}
	machine.Sync.Machine, machine.Sync.Name = device.ID, machine.Name
	machine.Sync.Error = "" // avoid exposing an unstructured shell or transport error in an aggregate view
	if !device.Online {
		machine.Sync.State = "offline"
	}
	known := seen.Launchers != nil || seen.Sync.SyncedAt != nil
	if known {
		machine.List = "fresh"
		if !device.Online || seen.Sync.State != "" && seen.Sync.State != "ok" {
			machine.List = "stale"
		}
	}
	name := strings.TrimPrefix(agents.Provider, "launcher.")
	raw := s.overviewLauncherList(withUnhidden(ctx, name), device.ID, seen.Launchers)
	engineRuntime := agentStartHost(device, "") != ""
	machine.catalogKnown = known && machineLauncherListKnown(raw) && !engineRuntime
	machine.NewAgent = s.modelAgentFit(ctx, device, agents, raw, machine.catalogKnown, engineRuntime)
	for _, purpose := range purposes {
		if purpose.purpose.Kind == "launcher" && purpose.purpose.MachineID == device.ID {
			machine.Roles = append(machine.Roles, purpose.purpose.ID)
			machine.Chats = append(machine.Chats, s.overviewChat(ctx, device, seen.Launchers, purpose))
		}
	}
	machine.Providers = s.overviewProviders(ctx, device, seen, providers, uses)
	machine.Tiers = overviewTiers(profile, machine)
	return machine
}

func (s *Service) overviewLauncherList(ctx context.Context, machine string, seen []proto.Launcher) []proto.Launcher {
	listing := machineLauncherListing{Launchers: make([]proto.LauncherEnvelope, 0, len(seen))}
	for _, item := range seen {
		listing.Launchers = append(listing.Launchers, proto.LauncherEnvelope(item))
	}
	list, _ := s.machineLaunchersFrom(ctx, machine, listing, false)
	return appendConfiguredLaunchers(list, s.hello(ctx).Models.Launchers[machine])
}
