package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

const (
	agentPresentationNameRunes = 120
	agentPresentationTagRunes  = 32
	agentPresentationTagLimit  = 12
)

type agentPresentationUpdate struct {
	displayName *string
	tags        *[]string
}

func presentationText(s string, max int) (string, bool) {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > max {
		return "", false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return "", false
		}
	}
	return s, true
}

func normalizeAgentPresentation(displayName *string, tags *[]string) (agentPresentationUpdate, error) {
	var out agentPresentationUpdate
	if displayName != nil {
		name, ok := presentationText(*displayName, agentPresentationNameRunes)
		if !ok {
			return out, i18n.Err("assistant-agent_presentation.invalid_name")
		}
		out.displayName = &name
	}
	if tags != nil {
		normalized := make([]string, 0, len(*tags))
		for _, raw := range *tags {
			tag, ok := presentationText(raw, agentPresentationTagRunes)
			if !ok || tag == "" {
				return out, i18n.Err("assistant-agent_presentation.invalid_tags")
			}
			duplicate := false
			for _, existing := range normalized {
				if strings.EqualFold(existing, tag) {
					duplicate = true
					break
				}
			}
			if !duplicate {
				normalized = append(normalized, tag)
			}
		}
		if len(normalized) > agentPresentationTagLimit {
			return out, i18n.Err("assistant-agent_presentation.too_many_tags")
		}
		out.tags = &normalized
	}
	return out, nil
}

// updateAgentPresentation changes only the manager's display record, never the runtime's title or tab. The target
// lookup accepts live and archived Agents and rejects Persona's own conversations.
func (s *Service) updateAgentPresentation(ctx context.Context, mid, sid string, update agentPresentationUpdate) (proto.Session, error) {
	target, err := s.agentArchiveTarget(ctx, mid, sid)
	if err != nil {
		return proto.Session{}, err
	}
	key := agentNameKey(target.MachineID, target.SessionID)
	b := &s.agentNames
	b.mu.Lock()
	if !s.agentNamesLocked(ctx) {
		b.mu.Unlock()
		return proto.Session{}, i18n.Err("assistant-agent_presentation.unavailable")
	}
	rec := b.records[key]
	rec.Machine, rec.Session = target.MachineID, target.SessionID
	if update.displayName != nil {
		if *update.displayName != "" && rec.DisplayName == "" {
			rec.DisplayFallback = target.Title
		}
		rec.DisplayName = *update.displayName
	}
	if update.tags != nil {
		rec.Tags = append([]string(nil), (*update.tags)...)
	}
	if err := s.putAgentNameLocked(ctx, key, rec); err != nil {
		b.mu.Unlock()
		return proto.Session{}, err
	}
	b.mu.Unlock()

	list := s.publishList(target.MachineID)
	for _, row := range list {
		if row.SessionID == target.SessionID {
			return row, nil
		}
	}
	s.decorateAgentPresentation(target.MachineID, &target, target.Archived)
	return target, nil
}

func (s *Service) agentPresentationRoutes() {
	s.handle("PUT /api/v1/sessions/{mid}/{sid}/presentation", func(w http.ResponseWriter, r *http.Request) {
		if !fullRole(r) {
			apierr.Write(w, http.StatusForbidden, i18n.Err("assistant-agent_presentation.forbidden"))
			return
		}
		var in struct {
			DisplayName *string   `json:"display_name"`
			Tags        *[]string `json:"tags"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil && !errors.Is(err, io.EOF) {
			apierr.Write(w, http.StatusBadRequest, err)
			return
		}
		if in.DisplayName == nil && in.Tags == nil {
			apierr.Write(w, http.StatusBadRequest, i18n.Err("assistant-agent_presentation.empty_update"))
			return
		}
		update, err := normalizeAgentPresentation(in.DisplayName, in.Tags)
		if err != nil {
			apierr.Write(w, http.StatusBadRequest, err)
			return
		}
		row, err := s.updateAgentPresentation(r.Context(), r.PathValue("mid"), r.PathValue("sid"), update)
		if err != nil {
			var refusal *agentArchiveError
			if errors.As(err, &refusal) {
				s.writeAgentArchiveError(w, err)
				return
			}
			apierr.Write(w, http.StatusInternalServerError, i18n.Err("assistant-agent_presentation.storage"))
			return
		}
		apierr.WriteJSON(w, map[string]any{"ok": true, "session": row})
	})
}
