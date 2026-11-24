package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// ---- comparing with the configuration ----

// profileRecords reads the records of the categories any of ps names, each once: the view compares every profile
// with them while it holds models.mu.
func (s *Service) profileRecords(ctx context.Context, ps []ModelProfile) (map[string]modelRecord, error) {
	records := map[string]modelRecord{}
	for _, id := range modelCategoryIDs {
		if !slices.ContainsFunc(ps, func(p ModelProfile) bool { _, ok := p.Purposes[id]; return ok }) {
			continue
		}
		r, err := s.modelRecord(ctx, id)
		if err != nil {
			return nil, err
		}
		records[id] = r
	}
	return records, nil
}

// modelProfileChanges is what switching to p would change now: the categories whose choice differs from their records
// (profileRecords) as ids, and the launcher fields (<launcher>.<field>). For the profile in use: where the
// configuration has moved away from it.
func modelProfileChanges(p ModelProfile, records map[string]modelRecord, stored []storedLauncher) []string {
	changes := []string{}
	for _, id := range modelCategoryIDs {
		b, ok := p.Purposes[id]
		if !ok {
			continue
		}
		r := records[id] // a copy: the new conversations' profile is cleared on it only
		r.Binding.ConfigDir = ""
		if r.Binding != b {
			changes = append(changes, id)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(p.Launchers)) {
		i := slices.IndexFunc(stored, func(l storedLauncher) bool { return l.Name == name })
		var l *proto.ManagedLauncher
		if i >= 0 {
			l = &stored[i].ManagedLauncher
		}
		m := p.Launchers[name]
		for _, f := range m.fields(l) {
			if *f.set != nil && (l == nil || *f.launcher != **f.set) { // a launcher no longer there: all of it
				changes = append(changes, name+"."+f.name)
			}
		}
	}
	return changes
}

// ---- the view ----

// ModelProfileView is a profile as GET /api/v1/model-profiles shows it.
type ModelProfileView struct {
	ModelProfile
	Active bool `json:"active,omitempty"`
	// Changes: what switching to it would change now (for the one in use: where the configuration moved away from it).
	Changes []string `json:"changes"`
}

// ModelProfilesView is GET /api/v1/model-profiles.
type ModelProfilesView struct {
	Revision  int64               `json:"revision"`
	Active    string              `json:"active,omitempty"`
	At        *time.Time          `json:"at,omitempty"`
	By        *ProfileActor       `json:"by,omitempty"`
	Rollback  *ModelProfileUndo   `json:"rollback,omitempty"`
	Rollbacks []ModelProfileUndo  `json:"rollbacks,omitempty"` // newest first, up to maxProfileRollbacks
	Profiles  []ModelProfileView  `json:"profiles"`
	Events    []ModelProfileEvent `json:"events"` // newest first
}

// ModelProfileUndo is what a rollback would do: back to profile To ("" none), changing Changes.
type ModelProfileUndo struct {
	ID      string    `json:"id,omitempty"`
	To      string    `json:"to,omitempty"`
	At      time.Time `json:"at"`
	Changes []string  `json:"changes"`
	Redo    bool      `json:"redo,omitempty"`
}

func rollbackUndoPreview(stack []modelProfileImage, targetIdx int) ModelProfileUndo {
	prev := composeRollbackImage(stack, targetIdx)
	changes := make([]string, 0, len(prev.Purposes))
	changes = append(changes, slices.Sorted(maps.Keys(prev.Purposes))...)
	for _, name := range slices.Sorted(maps.Keys(prev.Launchers)) {
		m := prev.Launchers[name]
		for _, f := range m.fields(nil) {
			if *f.set != nil {
				changes = append(changes, name+"."+f.name)
			}
		}
	}
	return ModelProfileUndo{ID: rollbackEntryID(prev), To: prev.Profile, At: prev.At, Changes: changes, Redo: prev.Redo}
}

func (s *Service) modelProfilesView(ctx context.Context) (ModelProfilesView, error) {
	s.models.mu.Lock()
	defer s.models.mu.Unlock()
	ps, seeded, err := s.modelProfilesLocked(ctx)
	if err != nil {
		return ModelProfilesView{}, err
	}
	state, err := s.readModelProfileState(ctx)
	if err != nil {
		return ModelProfilesView{}, err
	}
	if seeded {
		state.seed()
	}
	stored, err := s.readStoredLaunchers(ctx)
	if err != nil {
		return ModelProfilesView{}, err
	}
	ctx = withManagedSnapshot(ctx, stored)
	records, err := s.profileRecords(ctx, ps.Profiles)
	if err != nil {
		return ModelProfilesView{}, err
	}
	normalizedRecords := make(map[string]modelRecord, len(records))
	for _, id := range modelCategoryIDs {
		r, ok := records[id]
		if !ok {
			continue
		}
		if r.Binding.Provider == "" && r.Binding.Model != "" {
			provider, err := s.resolveLegacyModelProvider(ctx, id, nil, records, stored)
			if err != nil {
				return ModelProfilesView{}, err
			}
			if provider != "" {
				r.Binding.Provider = provider
			}
		}
		normalizedRecords[id] = r
	}
	v := ModelProfilesView{Revision: ps.Revision, Active: state.Active, Profiles: []ModelProfileView{}, Events: []ModelProfileEvent{}}
	if state.Active != "" {
		at, by := state.At, state.By
		v.At, v.By = &at, &by
	}
	for _, p := range ps.Profiles {
		p.Purposes, err = s.normalizeProfilePurposes(ctx, p.Purposes, records, stored)
		if err != nil {
			return ModelProfilesView{}, err
		}
		v.Profiles = append(v.Profiles, ModelProfileView{ModelProfile: p, Active: p.ID == state.Active, Changes: modelProfileChanges(p, normalizedRecords, stored)})
	}
	if stack := state.rollbackStack(); len(stack) > 0 {
		v.Rollbacks = make([]ModelProfileUndo, 0, len(stack))
		for i := len(stack) - 1; i >= 0; i-- {
			v.Rollbacks = append(v.Rollbacks, rollbackUndoPreview(stack, i))
		}
		latest := v.Rollbacks[0]
		v.Rollback = &latest
	}
	for i := len(state.Events) - 1; i >= 0 && len(v.Events) < shownProfileEvents; i-- {
		v.Events = append(v.Events, state.Events[i])
	}
	if seeded {
		if err := s.putModelProfileSettings(ctx, map[string]any{modelProfilesKey: ps, modelProfileStateKey: state}); err != nil {
			return ModelProfilesView{}, err
		}
	}
	return v, nil
}

// ---- the routes ----

// profileActor is who sent r: the device the hub names, else the hub's own token.
func profileActor(r *http.Request) ProfileActor {
	if name := strings.TrimSpace(r.Header.Get(assistantwire.DeviceNameHeader)); name != "" {
		return ProfileActor{Kind: "device", Name: name}
	}
	return ProfileActor{Kind: "hub"}
}

// modelProfileStatus is the HTTP status of err from the profiles' functions.
func modelProfileStatus(err error) int {
	var m *i18n.Message
	switch {
	case errors.Is(err, errModelProfileAtomicStore):
		return http.StatusInternalServerError
	case errors.Is(err, errModelProfileNotFound):
		return http.StatusNotFound
	case errors.Is(err, errModelProfileConflict), errors.Is(err, errModelProfileNoRollback):
		return http.StatusConflict
	case errors.As(err, &m) && (strings.HasPrefix(m.Key, "assistant-model_profiles.") ||
		m.Key == "assistant-models.err_permission_mode_launcher_only" ||
		m.Key == "assistant-models.err_permission_mode_unsupported"):
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

// modelProfileInput is PUT /api/v1/model-profiles/{id}: the whole profile, or (capture) the configuration as it is now
// with the tiers kept. Unknown optional fields from newer clients are ignored during decoding while known fields are
// strictly type-checked and validated and credential/endpoint fields are rejected.
type modelProfileInput struct {
	Revision    *int64                    `json:"revision"`
	Name        string                    `json:"name"`
	Description string                    `json:"description"`
	Purposes    map[string]ModelBinding   `json:"purposes"`
	Launchers   map[string]LauncherModels `json:"launchers"`
	Tiers       map[string]ModelTier      `json:"tiers"`
	Capture     bool                      `json:"capture"`
}

// modelProfileRollbackInput is the optional body of POST /api/v1/model-profiles/rollback.
type modelProfileRollbackInput struct {
	ID string `json:"id"`
}

// forbiddenProfileFields are credential, secret, and endpoint keys that model profiles never accept
// (docs/assistant/model-profiles.md).
var forbiddenProfileFields = map[string]bool{
	"api_key":        true,
	"key":            true,
	"key_sealed":     true,
	"headers":        true,
	"headers_sealed": true,
	"env":            true,
	"env_sealed":     true,
	"base_url":       true,
	"token":          true,
	"secret":         true,
	"password":       true,
	"auth":           true,
	"proxy":          true,
	"no_proxy":       true,
}

func checkForbiddenObjectKeys(obj map[string]json.RawMessage) error {
	for _, k := range slices.Sorted(maps.Keys(obj)) {
		if forbiddenProfileFields[strings.ToLower(k)] {
			return i18n.Err("assistant-model_profiles.err_forbidden_field", i18n.Args{"field": k})
		}
	}
	return nil
}

func checkForbiddenProfilePayload(top map[string]json.RawMessage) error {
	if err := checkForbiddenObjectKeys(top); err != nil {
		return err
	}
	for _, key := range slices.Sorted(maps.Keys(top)) {
		if !strings.EqualFold(key, "purposes") && !strings.EqualFold(key, "launchers") && !strings.EqualFold(key, "tiers") {
			continue
		}
		var entries map[string]json.RawMessage
		if err := json.Unmarshal(top[key], &entries); err != nil || entries == nil {
			continue // non-object shapes fail type decoding cleanly in json.Unmarshal
		}
		for _, entryKey := range slices.Sorted(maps.Keys(entries)) {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(entries[entryKey], &fields); err != nil || fields == nil {
				continue
			}
			if err := checkForbiddenObjectKeys(fields); err != nil {
				return err
			}
		}
	}
	return nil
}

// decodeSingleJSONObject reads at most limit bytes and decodes exactly one JSON object into dst, ignoring unknown
// optional fields while rejecting non-object values, malformed JSON, and trailing tokens.
func decodeSingleJSONObject(w http.ResponseWriter, r *http.Request, dst any, limit int64, optional bool, checkObject func(map[string]json.RawMessage) error) error {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		return err
	}
	if optional && len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	var top map[string]json.RawMessage
	if err := dec.Decode(&top); err != nil {
		return err
	}
	if top == nil {
		return errors.New("request body must be a JSON object")
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return errors.New("unexpected trailing data after JSON body")
	}
	if checkObject != nil {
		if err := checkObject(top); err != nil {
			return err
		}
	}
	return json.Unmarshal(raw, dst)
}

func (s *Service) modelProfileRoutes() {
	view := func(w http.ResponseWriter, r *http.Request) {
		v, err := s.modelProfilesView(r.Context())
		if err != nil {
			apierr.Write(w, modelProfileStatus(err), err)
			return
		}
		apierr.WriteJSON(w, v)
	}
	full := func(fn http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if !fullRole(r) {
				apierr.Write(w, http.StatusForbidden, i18n.Err("assistant-model_profiles.err_role"))
				return
			}
			fn(w, r)
		}
	}
	s.handle("GET /api/v1/model-profiles", view)
	s.handle("PUT /api/v1/model-profiles/{id}", full(func(w http.ResponseWriter, r *http.Request) {
		var in modelProfileInput
		if err := decodeSingleJSONObject(w, r, &in, 64<<10, false, checkForbiddenProfilePayload); err != nil {
			writeBodyErr(w, err, err)
			return
		}
		if in.Revision == nil || *in.Revision < 0 {
			apierr.Write(w, http.StatusBadRequest, i18n.Err("assistant-models.err_revision_required"))
			return
		}
		ctx := r.Context()
		_, err := s.saveModelProfile(ctx, in.Revision, r.PathValue("id"), profileActor(r), func(p *ModelProfile, exists bool) error {
			p.Name, p.Description = in.Name, in.Description
			if !in.Capture {
				p.Purposes, p.Launchers, p.Tiers = in.Purposes, in.Launchers, in.Tiers
				return nil
			}
			purposes, launchers, tiers, err := s.captureModels(ctx)
			if err != nil {
				return err
			}
			p.Purposes, p.Launchers = purposes, launchers
			if in.Tiers != nil {
				p.Tiers = in.Tiers
			} else if !exists {
				p.Tiers = tiers
			}
			return nil
		})
		if err != nil {
			apierr.Write(w, modelProfileStatus(err), err)
			return
		}
		view(w, r)
	}))
	s.handle("DELETE /api/v1/model-profiles/{id}", full(func(w http.ResponseWriter, r *http.Request) {
		var revision *int64
		if q := r.URL.Query().Get("revision"); q != "" {
			var n int64
			if err := json.Unmarshal([]byte(q), &n); err != nil {
				apierr.Write(w, http.StatusBadRequest, i18n.Err("assistant-models.err_revision_required"))
				return
			}
			revision = &n
		}
		if err := s.deleteModelProfile(r.Context(), revision, r.PathValue("id"), profileActor(r)); err != nil {
			apierr.Write(w, modelProfileStatus(err), err)
			return
		}
		view(w, r)
	}))
	s.handle("POST /api/v1/model-profiles/{id}/apply", full(func(w http.ResponseWriter, r *http.Request) {
		if _, err := s.applyModelProfile(r.Context(), r.PathValue("id"), profileActor(r)); err != nil {
			apierr.Write(w, modelProfileStatus(err), err)
			return
		}
		view(w, r)
	}))
	s.handle("POST /api/v1/model-profiles/rollback", full(func(w http.ResponseWriter, r *http.Request) {
		var in modelProfileRollbackInput
		if err := decodeSingleJSONObject(w, r, &in, 4096, true, nil); err != nil {
			writeBodyErr(w, err, err)
			return
		}
		if _, err := s.rollbackModelProfileTo(r.Context(), strings.TrimSpace(in.ID), profileActor(r)); err != nil {
			apierr.Write(w, modelProfileStatus(err), err)
			return
		}
		view(w, r)
	}))
}
