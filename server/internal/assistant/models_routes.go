package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/i18n"
)

// modelPurpose is purpose id as Settings › Model configuration shows it: what it is set to, what it runs on, and how that stands.
func (s *Service) modelPurpose(ctx context.Context, id string, providers []ModelProvider) (ModelPurpose, error) {
	p, err := s.modelPurposeDef(ctx, id)
	if err != nil {
		return p, err
	}
	p.Launch = p.launch()
	recordID := modelRecordID(p)
	r, err := s.modelRecord(ctx, recordID)
	if err != nil {
		return p, err
	}
	p.Override, p.Revision = r.Binding, r.Revision
	readOnly := recordID != id
	if readOnly {
		p.Override = ModelBinding{}
	}
	p.Effective, err = s.resolveModel(ctx, id)
	if err != nil {
		return p, err
	}
	base, from, err := s.modelBaseline(ctx, p)
	if err != nil {
		return p, err
	}
	p.InheritedFrom = inheritedLabel(s.requestCtxLang(ctx), p, base, from, providers)
	if isExtensionPurpose(p.ID) && !readOnly {
		p.Candidates = s.purposeCandidates(ctx, p.ID, r.Candidates, p.Effective)
	}
	if p.Kind == "api" {
		p.MachineID = s.settings(ctx).MachineID // where a launcher picked for it runs (models_run.go)
	}
	stored := s.storedLaunchers(ctx)
	var roomHere []ConfiguredLauncher // the assistant's computer's launchers as last seen: what each runs there
	if p.ID == purposeMeetingRoom {
		roomHere = s.roomLaunchers(ctx, p.MachineID, "", false)
	}
	p.Fields = []string{"provider", "model"}
	p.ProviderIDs = []string{}
	for _, v := range providers {
		offered := s.modelProviderCompatible(p, v, stored)
		if p.ID == purposeMeetingRoom { // Claude Code only, as the assistant's computer runs it (models_room.go)
			here, ok := onRoomMachine(v, stored, roomHere)
			offered = ok && s.modelProviderCompatible(p, here, stored)
		}
		if offered {
			p.ProviderIDs = append(p.ProviderIDs, v.ID)
		}
		if v.ID == p.Effective.Provider {
			if p.Effective.Model == "" {
				p.Effective.Model = v.Model
			}
			if p.Kind == "api" && (v.API == "responses" || v.API == "messages" || v.API == "chat" || v.API == "") {
				p.Fields = append(p.Fields, "effort")
			}
		}
	}
	if p.Candidates != nil {
		for _, provider := range s.candidateProviders(ctx, p.Effective.Provider) {
			id := "launcher." + provider.Name
			if slices.Contains(p.ProviderIDs, id) {
				p.Candidates.DirectProviderIDs = append(p.Candidates.DirectProviderIDs, id)
			}
		}
	}
	if p.Kind == "launcher" {
		p.Fields = append(p.Fields, "effort", "permission_mode")
	}
	if len(p.ConfigDirs) > 0 {
		p.Fields = append(p.Fields, "config_dir")
	}
	l := s.requestCtxLang(ctx)
	s.setModelPurposeStatus(ctx, l, &p, providers, stored, roomHere)
	if readOnly {
		p.Fields = []string{}
		for _, category := range modelPurposes {
			if category.ID == recordID {
				p.InheritedFrom = purposeName(l, category.ID)
				break
			}
		}
	}
	return p, nil
}

func (s *Service) modelConfigRoutes() {
	s.handle("GET /api/v1/model-config/overview", s.modelOverviewRoute)
	s.handle("GET /api/v1/model-config", func(w http.ResponseWriter, r *http.Request) {
		// Every purpose resolves against the same provider list and records: each key is read once (read-only).
		ctx := withSettingsMemo(r.Context())
		providers := s.modelProviders(ctx)
		purposes := []ModelPurpose{}
		for _, id := range modelConfigIDs {
			p, err := s.modelPurpose(ctx, id, providers)
			if err != nil {
				apierr.Write(w, 500, err)
				return
			}
			purposes = append(purposes, p)
		}
		// The extensions' own (model_rpc.go), after the categories: each with its own record, like Lark /agent.
		for _, x := range s.modelExtensions(ctx) {
			p, err := s.modelPurpose(ctx, assistantwire.ExtensionPurposePrefix+x.StorageKey(), providers)
			if err != nil {
				apierr.Write(w, 500, err)
				return
			}
			purposes = append(purposes, p)
		}
		s.addModelUsage(ctx, purposes)
		apierr.WriteJSON(w, map[string]any{"purposes": purposes, "providers": providers})
	})
	detail := func(w http.ResponseWriter, r *http.Request) {
		// Read-only, also after update's write: a memo of its own, made after the write.
		ctx := withSettingsMemo(r.Context())
		providers := s.modelProviders(ctx)
		p, err := s.modelPurpose(ctx, r.PathValue("id"), providers)
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, errModelPurposeNotFound) {
				status = http.StatusNotFound
			}
			apierr.Write(w, status, err)
			return
		}
		apierr.WriteJSON(w, map[string]any{"purpose": p, "providers": providers})
	}
	s.handle("GET /api/v1/model-config/purposes/{id}", detail)
	s.handle("GET /api/v1/model-config/providers/{id}/models", s.providerModels)
	// Both verbs keep sparse updates and the same revision-checked write path.
	update := func(w http.ResponseWriter, r *http.Request) {
		var in modelPatch
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&in); err != nil {
			apierr.Write(w, 400, err)
			return
		}
		if in.Revision == nil || *in.Revision < 0 {
			apierr.Write(w, 400, i18n.Err("assistant-models.err_revision_required"))
			return
		}
		s.models.mu.Lock()
		defer s.models.mu.Unlock()
		ctx, id := r.Context(), r.PathValue("id")
		providers := s.modelProviders(ctx)
		p, err := s.modelPurpose(ctx, id, providers)
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, errModelPurposeNotFound) {
				status = http.StatusNotFound
			}
			apierr.Write(w, status, err)
			return
		}
		if len(p.Fields) == 0 {
			apierr.Write(w, http.StatusBadRequest, i18n.Err("assistant-models.err_purpose_readonly"))
			return
		}
		if p.Revision != *in.Revision {
			apierr.Write(w, 409, errModelConflict)
			return
		}
		if in.ConfigDir != nil && string(in.ConfigDir) != "null" && len(p.ConfigDirs) == 0 {
			apierr.Write(w, 400, i18n.Err("assistant-models.err_config_dir_assistant_only"))
			return
		}
		if in.PermissionMode != nil && string(in.PermissionMode) != "null" && p.Kind != "launcher" {
			apierr.Write(w, 400, i18n.Err("assistant-models.err_permission_mode_launcher_only"))
			return
		}
		b, err := patchModel(p.Override, in)
		if err != nil {
			apierr.Write(w, 400, err)
			return
		}
		var candidates *candidateSetting
		if in.Candidates != nil {
			if candidates, err = s.patchCandidates(ctx, p, in.Candidates); err != nil {
				apierr.Write(w, 400, err)
				return
			}
		}
		if p.Kind == "launcher" { // passed to an agent as --model: a model id (#1644)
			if err := checkModelID(b.Model); err != nil {
				apierr.Write(w, 400, err)
				return
			}
		}
		base, _, err := s.modelBaseline(ctx, p)
		if err != nil {
			apierr.Write(w, 500, err)
			return
		}
		effective := effectiveModel(p, base, b)
		if body, err := s.triageInUse(ctx, id, effective); err != nil {
			apierr.Write(w, http.StatusServiceUnavailable, err)
			return
		} else if body != nil { // plugins that need it are on
			apierr.WriteBody(w, http.StatusUnprocessableEntity, body)
			return
		}
		if in.Effort != nil && string(in.Effort) != "null" && p.Kind == "api" {
			config, e := s.apiConfig(ctx, effective)
			if e != nil {
				apierr.Write(w, 400, e)
				return
			}
			if !messagesAPI(config.API) && config.API != "responses" && config.API != "chat" {
				apierr.Write(w, 400, i18n.Err("assistant-models.err_api_effort"))
				return
			}
		}
		if b != (ModelBinding{}) {
			if err = s.validateModel(ctx, p, effective, providers); err != nil {
				apierr.Write(w, 400, err)
				return
			}
		}
		if err = s.writeModelRecord(ctx, id, p.Revision, b, candidates, in.Candidates != nil); err != nil {
			status := 500
			if errors.Is(err, errModelConflict) {
				status = 409
			}
			apierr.Write(w, status, err)
			return
		}
		detail(w, r)
	}
	s.handle("PATCH /api/v1/model-config/purposes/{id}", update)
	s.handle("PUT /api/v1/model-config/purposes/{id}", update)
}
