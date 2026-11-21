package assistant

import (
	"context"
	"slices"
	"strings"

	"github.com/nwverse/persona/server/internal/ai"
	"github.com/nwverse/persona/server/internal/i18n"
)

// ModelPurposeStatusCode is the stable counterpart to a model purpose's legacy status text.
type ModelPurposeStatusCode string

const (
	modelStatusOK                   ModelPurposeStatusCode = "ok"
	modelStatusOutOfScope           ModelPurposeStatusCode = "out_of_scope"
	modelStatusProviderIncompatible ModelPurposeStatusCode = "provider_incompatible"
	modelStatusNoAPI                ModelPurposeStatusCode = "no_api"
	modelStatusNoModel              ModelPurposeStatusCode = "no_model"
	modelStatusNoCredentials        ModelPurposeStatusCode = "no_credentials"
	modelStatusPaused               ModelPurposeStatusCode = "paused"
	modelStatusMachineOffline       ModelPurposeStatusCode = "machine_offline"
	modelStatusNodeOutdated         ModelPurposeStatusCode = "node_outdated"
	modelStatusHiddenInUse          ModelPurposeStatusCode = "hidden_in_use"
	modelStatusOther                ModelPurposeStatusCode = "other"
)

// setModelPurposeStatus sets the code at the same decision point as the legacy text.
func (s *Service) setModelPurposeStatus(ctx context.Context, l i18n.Lang, p *ModelPurpose, providers []ModelProvider, stored []storedLauncher, roomHere []ConfiguredLauncher) {
	p.StatusCode, p.Status = modelStatusOK, modelUnverified
	if room := roomNotClaudeStatus(l, *p, providers, stored, roomHere); room != "" {
		p.StatusCode, p.Status = modelStatusProviderIncompatible, room
	} else if code, scope := s.purposeScopeStatus(ctx, l, p.Effective.Provider, p.MachineID); p.Kind == "launcher" && scope != "" {
		p.StatusCode, p.Status = code, scope
	} else if !slices.Contains(p.ProviderIDs, p.Effective.Provider) && !s.mainChatOffers(ctx, *p) {
		// A runtime's launcher can be available in the computer's main-chat picker even when the provider list omits it.
		p.StatusCode, p.Status = modelStatusProviderIncompatible, mc(l, "status_provider_incompatible")
		if p.Kind == "api" && strings.HasPrefix(p.Effective.Provider, "legacy.") && s.legacyModelConfig(ctx, p.Effective.Provider).BaseURL == "" {
			p.StatusCode, p.Status = modelStatusNoAPI, mc(l, s.noAPIStatus(ctx))
		}
	} else if p.Kind == "api" {
		config, err := s.apiConfig(ctx, p.Effective)
		switch {
		case config.RunLauncher != "": // the launcher's own model when none is chosen
			if err != nil {
				p.StatusCode, p.Status = modelStatusOther, i18n.Localize(err, l)
			} else if d, ok := s.device(ctx, config.ViaMachine); ok && !d.Online {
				p.StatusCode, p.Status = modelStatusMachineOffline, mc(l, "status_machine_offline")
			}
		case p.Effective.Model == "":
			p.StatusCode, p.Status = modelStatusNoModel, mc(l, "status_no_model")
		case err == errOverviewSecretUnchecked:
			p.StatusCode = modelStatusOther
		case err != nil:
			p.StatusCode, p.Status = modelStatusOther, i18n.Localize(err, l)
		case config.APIKey == "" && config.APIKeyFile == "":
			p.StatusCode, p.Status = modelStatusNoCredentials, mc(l, "status_no_credentials")
		default:
			if pause := ai.Held(config); pause != nil {
				p.StatusCode, p.Status, p.Limit = modelStatusPaused, pause.Text(), pause
			}
		}
	} else if p.Kind == "plugin" {
		for _, provider := range providers {
			if provider.ID == p.Effective.Provider && provider.Status != "" {
				p.StatusCode, p.Status = modelStatusOther, provider.Status
				break
			}
		}
	} else if p.Kind == "launcher" && p.MachineID != "" {
		if d, ok := s.device(ctx, p.MachineID); ok {
			if !d.Online {
				p.StatusCode, p.Status = modelStatusMachineOffline, mc(l, "status_machine_offline")
			} else if d.Version != "" && !d.ModelConfiguration {
				p.StatusCode, p.Status = modelStatusNodeOutdated, mc(l, "status_node_outdated")
			}
		}
	}
}
