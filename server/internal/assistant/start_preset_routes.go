package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
)

// startPresetRoutes serves the same computer's picker inventory under two independent extension grants.
func (s *Service) startPresetRoutes() {
	s.handle("GET /api/v1/assistant/worker-presets", func(w http.ResponseWriter, r *http.Request) {
		out, status, err := s.workerPresetsForMachine(r.Context(), r.URL.Query().Get("machine"))
		if err != nil {
			apierr.Write(w, status, err)
			return
		}
		raw, _ := json.Marshal(map[string]any{"presets": out})
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write(raw)
	})
	s.handle("GET /api/v1/assistant/dispatch/candidates", s.dispatchCandidates)
}

func (s *Service) workerPresetsForMachine(parent context.Context, machine string) ([]workerPreset, int, error) {
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	m, ok := s.machineByName(ctx, machine)
	if !ok {
		return nil, http.StatusNotFound, errUnknownMachine
	}
	launchers, err := s.pickerLaunchers(ctx, m.ID)
	if err != nil {
		return nil, http.StatusBadGateway, err
	}
	_, tiers := s.pickerTiers(ctx, m.ID, launchers)
	out := make([]workerPreset, 0, len(tiers))
	for _, t := range tiers {
		_, unavailable := s.launcherUnusable(ctx, m.ID, t.Launcher)
		out = append(out, workerPreset{
			Name: t.Name, Launcher: t.Launcher, Model: t.Model, Effort: t.Effort, Speed: t.Speed,
			Description: t.Description, Unavailable: unavailable,
		})
	}
	return out, http.StatusOK, nil
}

func (s *Service) dispatchCandidates(w http.ResponseWriter, r *http.Request) {
	// Go also matches HEAD to a GET pattern. This grant opens only the inventory GET.
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		apierr.Write(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
		return
	}
	machine := r.URL.Query().Get("machine")
	if machine == "" {
		apierr.WriteBody(w, http.StatusNotFound, apierr.Body(http.StatusNotFound, "", errUnknownMachine))
		return
	}
	presets, status, err := s.workerPresetsForMachine(r.Context(), machine)
	if err != nil {
		if status == http.StatusBadGateway {
			err = errors.New("launcher inventory unavailable")
		}
		// This extension API has fixed public errors in every request language.
		apierr.WriteBody(w, status, apierr.Body(status, "", err))
		return
	}
	candidates := make([]dispatchCandidate, 0, len(presets))
	for _, p := range presets {
		candidates = append(candidates, dispatchCandidate{
			Name: p.Name, Launcher: p.Launcher, Model: p.Model, Effort: p.Effort, Speed: p.Speed,
			Description: p.Description, Unavailable: p.Unavailable,
		})
	}
	apierr.WriteJSON(w, map[string]any{"candidates": candidates})
}

// This explicit wire projection limits what an extension granted dispatch.defaults sees as worker presets evolve.
type dispatchCandidate struct {
	Name        string `json:"name"`
	Launcher    string `json:"launcher"`
	Model       string `json:"model,omitempty"`
	Effort      string `json:"effort,omitempty"`
	Speed       string `json:"speed,omitempty"`
	Description string `json:"description,omitempty"`
	Unavailable bool   `json:"unavailable,omitempty"`
}

// workerPreset is one row of GET /api/v1/assistant/worker-presets.
type workerPreset struct {
	Name        string `json:"name"`
	Launcher    string `json:"launcher"`
	Model       string `json:"model,omitempty"`
	Effort      string `json:"effort,omitempty"`
	Speed       string `json:"speed,omitempty"`
	Description string `json:"description,omitempty"`
	// Unavailable: the launcher's latest check says it does not work right now (for example a usage limit with a
	// reset still ahead); absent check results leave it false, so an unchecked preset is offered.
	Unavailable bool `json:"unavailable,omitempty"`
}
