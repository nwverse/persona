package assistant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/i18n"
)

// A direction's name and its leader's own progress (#3679; docs/assistant/unified-entry.md "Names and progress").
//
// A work-log extension shows each direction under its name (leader.changed name, directionName): the one the owner
// (PUT …/directions/{id}/name) or the secretary (direction_name) gave it, else its project's, else its title without
// the "MMDD | kind | " prefix. The leader writes its direction's summary itself, at the goal level (leader_progress):
// what was reached, what is being done and where it is stuck, what the owner has to do, with evidence links; it goes
// to the extensions as leader.report kind progress and the last one stays on the direction.

const (
	directionNameMost = 40  // characters
	progressPartMost  = 200 // characters of each part
	progressLinksMost = 5
)

// leaderProgress is a leader's summary of its direction.
type leaderProgress struct {
	Reached  string    `json:"reached,omitempty"`
	Doing    string    `json:"doing,omitempty"`
	NeedsYou string    `json:"needs_you,omitempty"`
	Links    []string  `json:"links,omitempty"`
	At       time.Time `json:"at"`
}

var errNoDirection = errors.New("no such direction: GET /api/v1/assistant/leaders lists them")

// setDirectionName names direction id (a leader now or before) as by chose; "" clears it (the fallbacks show again).
func (s *Service) setDirectionName(ctx context.Context, id, name, by string) error {
	name = clip(oneLine(name), directionNameMost)
	b := &s.directions
	b.mu.Lock()
	if !s.directionsLocked(ctx) {
		b.mu.Unlock()
		return errors.New("the directions could not be read")
	}
	d := b.m[id]
	if d == nil {
		b.mu.Unlock()
		return errNoDirection
	}
	d.Name, d.NameBy = name, by
	if name == "" {
		d.NameBy = ""
	}
	s.saveDirectionsLocked(ctx)
	b.mu.Unlock()
	s.directionsSoon() // leader.changed with the new name
	return nil
}

// toolDirectionName is direction_name: the secretary names a direction.
func (s *Service) toolDirectionName(ctx context.Context, self string, args json.RawMessage) (string, error) {
	var a struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("arguments: %v", err)
	}
	if !s.isMain(ctx, self) {
		return "", errors.New("only the main conversation names directions")
	}
	if err := s.setDirectionName(ctx, strings.TrimSpace(a.ID), a.Name, directionByAssistant); err != nil {
		return "", err
	}
	if strings.TrimSpace(a.Name) == "" {
		return fmt.Sprintf("Direction %s has no name of its own now: it shows its project's name, else its title.", a.ID), nil
	}
	return fmt.Sprintf("Direction %s is called 「%s」 now (the app and the work-log extension show it).", a.ID, clip(oneLine(a.Name), directionNameMost)), nil
}

// toolLeaderProgress is leader_progress: a leader reports its direction's progress at the goal level.
func (s *Service) toolLeaderProgress(ctx context.Context, self string, args json.RawMessage) (string, error) {
	var a struct {
		Reached  string   `json:"reached"`
		Doing    string   `json:"doing"`
		NeedsYou string   `json:"needs_you"`
		Links    []string `json:"links"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("arguments: %v", err)
	}
	if !s.isLeader(ctx, self) {
		return "", errors.New("only a leader conversation (assistant.leaders) reports its direction's progress: tell the user in your reply instead")
	}
	p := leaderProgress{Reached: clip(oneLine(a.Reached), progressPartMost), Doing: clip(oneLine(a.Doing), progressPartMost),
		NeedsYou: clip(oneLine(a.NeedsYou), progressPartMost), At: time.Now().UTC()}
	for _, l := range a.Links {
		if l = strings.TrimSpace(l); l != "" && !slices.Contains(p.Links, l) && len(p.Links) < progressLinksMost {
			p.Links = append(p.Links, clip(l, 500))
		}
	}
	if p.Reached == "" && p.Doing == "" && p.NeedsYou == "" {
		return "", errors.New("say at least one of reached, doing and needs_you")
	}
	told, err := s.leaderProgressReport(ctx, self, p)
	if err != nil {
		return "", err
	}
	if !told {
		return "Kept as the direction's latest progress (no work-log extension listens on this hub).", nil
	}
	return "Reported as the direction's latest progress (the work-log extension has it).", nil
}

// leaderProgressReport keeps p as leader lead's latest and tells the extensions (false: none listens).
func (s *Service) leaderProgressReport(ctx context.Context, lead string, p leaderProgress) (bool, error) {
	b := &s.directions
	b.mu.Lock()
	if !s.directionsLocked(ctx) {
		b.mu.Unlock()
		return false, errors.New("the directions could not be read")
	}
	d := b.m[lead]
	if d == nil { // a leader the sync has not recorded yet
		d = &direction{MachineID: s.settings(ctx).MachineID, By: directionByAssistant, At: p.At}
		b.m[lead] = d
	}
	cp := p
	d.Progress = &cp
	s.saveDirectionsLocked(ctx)
	b.mu.Unlock()
	if !s.todoEventsOn() {
		return false, nil
	}
	l := s.lang(ctx)
	var parts []string
	for _, x := range []struct{ key, text string }{{"progress_reached", p.Reached}, {"progress_doing", p.Doing}, {"progress_needs_you", p.NeedsYou}} {
		if x.text != "" {
			parts = append(parts, i18n.T(l, "assistant-reports."+x.key, i18n.Args{"text": x.text}))
		}
	}
	key := "progress|" + lead + "|" + p.At.Format(time.RFC3339Nano)
	sum := sha256.Sum256([]byte(key))
	ev := assistantwire.LeaderReportEvent{ID: "lrep_" + hex.EncodeToString(sum[:8]), Leader: lead, Kind: assistantwire.LeaderProgress,
		Text: clip(strings.Join(parts, "\n"), assistantwire.LeaderReportTextMost), At: p.At, Reached: p.Reached, Doing: p.Doing,
		NeedsYou: p.NeedsYou, Links: p.Links}
	if len(p.Links) > 0 {
		ev.Link = p.Links[0]
	}
	s.todoQueue(todoNotice{assistantwire.EventLeaderReport, ev})
	return true, nil
}

// directionNameRoutes: the owner names a direction.
func (s *Service) directionNameRoutes() {
	s.handle("PUT /api/v1/assistant/directions/{id}/name", func(w http.ResponseWriter, r *http.Request) {
		if !fullRole(r) {
			apierr.Write(w, http.StatusForbidden, errors.New("read-only role cannot change directions"))
			return
		}
		var in struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil {
			apierr.Write(w, http.StatusBadRequest, err)
			return
		}
		err := s.setDirectionName(r.Context(), r.PathValue("id"), in.Name, directionByUser)
		switch {
		case errors.Is(err, errNoDirection):
			apierr.Write(w, http.StatusNotFound, err)
			return
		case err != nil:
			apierr.Write(w, http.StatusServiceUnavailable, err)
			return
		}
		apierr.WriteJSON(w, map[string]any{"name": clip(oneLine(in.Name), directionNameMost)})
	})
}
