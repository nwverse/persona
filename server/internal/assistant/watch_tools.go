package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/i18n"
)

// The watch cards' MCP tools and routes (watch.go; docs/assistant/watch-cards.md).

// toolWatch answers one of the watch tools for conversation self on machine.
func (s *Service) toolWatch(ctx context.Context, machine, self, name string, args json.RawMessage) (string, error) {
	var a struct {
		watchInput
		ID      string          `json:"id"`
		IDs     []string        `json:"ids"`
		Number  *float64        `json:"number"`
		Status  string          `json:"status"`
		Text    string          `json:"text"`
		Table   *WatchTable     `json:"-"`
		RawTbl  json.RawMessage `json:"table"`
		Project *string         `json:"project"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("arguments: %w", err)
	}
	a.watchInput.Project = a.Project
	fail := func(err error) (string, error) { return "", errors.New(watchErrorText(err)) }
	loc := s.location(ctx)
	switch name {
	case "watch_draft":
		if a.Question == nil {
			return "", errors.New("question is required")
		}
		w, err := s.draftWatch(ctx, a.ID, a.watchInput)
		if err != nil {
			return fail(err)
		}
		return fmt.Sprintf("Draft %s, not on Overview yet. Show the user this preview and ask whether to add it (they can also confirm it on Overview); "+
			"after they agree, call watch_confirm.\n%s", w.ID, s.watchPreview(ctx, w)), nil
	case "watch_confirm":
		w, err := s.confirmWatch(ctx, a.ID)
		if err != nil {
			return fail(err)
		}
		return fmt.Sprintf("Watch card %s is on Overview, first; it refreshes %s (scheduled task %s, also listed on the Scheduled tasks page).",
			w.ID, ruleText(watchRuleTask(w), loc), w.TaskID), nil
	case "watch_list":
		project := ""
		if a.Project != nil && strings.TrimSpace(*a.Project) != "" {
			p, err := s.watchProject(ctx, *a.Project)
			if err != nil {
				return fail(err)
			}
			project = p
		}
		vs, err := s.watchViews(ctx, project)
		if err != nil {
			return fail(err)
		}
		if len(vs) == 0 {
			return "No watch cards yet.", nil
		}
		var b strings.Builder
		for _, v := range vs {
			b.WriteString(s.watchPreview(ctx, v.Watch))
			b.WriteString("\n")
		}
		return strings.TrimSpace(b.String()), nil
	case "watch_update":
		w, err := s.updateWatch(ctx, a.ID, a.watchInput)
		if err != nil {
			return fail(err)
		}
		return "Saved.\n" + s.watchPreview(ctx, w), nil
	case "watch_delete":
		ok, err := s.deleteWatch(ctx, a.ID)
		if err != nil {
			return fail(err)
		}
		if !ok {
			return "", fmt.Errorf("no watch card %q (watch_list lists them)", a.ID)
		}
		return "Deleted the card and its scheduled task.", nil
	case "watch_reorder":
		if err := s.reorderWatches(ctx, a.IDs); err != nil {
			return fail(err)
		}
		return "Reordered.", nil
	case "watch_report":
		if len(a.RawTbl) > 0 && string(a.RawTbl) != "null" {
			var t WatchTable
			if err := json.Unmarshal(a.RawTbl, &t); err != nil {
				return fail(errWatchTable)
			}
			a.Table = &t
		}
		_, tripped, err := s.reportWatch(ctx, machine, self, a.ID, WatchReading{Number: a.Number, Status: a.Status, Text: a.Text, Table: a.Table})
		if err != nil {
			return fail(err)
		}
		if tripped {
			return "Recorded. The card's alert rule tripped: Persona pushed it to the user.", nil
		}
		return "Recorded on the card.", nil
	}
	return "", fmt.Errorf("unknown tool %s", name)
}

// watchRuleTask is a task with w's rule, for the schedule's words.
func watchRuleTask(w Watch) Task {
	return Task{Repeat: w.Schedule.Repeat, Time: w.Schedule.Time, Weekdays: w.Schedule.Weekdays, EveryMinutes: w.Schedule.EveryMinutes, Cron: w.Schedule.Cron}
}

// watchPreview is a card in a few lines, for the model to show the user (English; the model writes in the user's words).
func (s *Service) watchPreview(ctx context.Context, w Watch) string {
	alert := "never"
	if a := w.Alert; a != nil {
		switch a.When {
		case "above", "below":
			alert = "when the number is " + a.When + " " + strconv.FormatFloat(*a.Value, 'f', -1, 64)
		case watchBad:
			alert = "when the light turns red (bad)"
		case "change":
			alert = "whenever the answer changes"
		}
	}
	display := w.Display
	if w.Unit != "" {
		display += " (" + w.Unit + ")"
	}
	project := "none"
	if w.Project != "" {
		project = w.Project
	}
	state := w.State
	if w.Paused {
		state += ", paused"
	}
	line := fmt.Sprintf("- %s %q [%s]\n  checks: %s\n  refresh: %s\n  shows: %s\n  push: %s\n  project: %s",
		w.ID, w.Title, state, w.Question, ruleText(watchRuleTask(w), s.location(ctx)), display, alert, project)
	if r := w.Latest; r != nil {
		line += "\n  latest: " + r.Text
	}
	return line
}

// writeWatchError answers a failed read or change of the cards with its status.
func writeWatchError(w http.ResponseWriter, err error) {
	var msg *i18n.Message
	switch {
	case errors.Is(err, errWatchStore), errors.Is(err, errScheduleUnavailable), errors.Is(err, errScheduleSave):
		apierr.Write(w, http.StatusServiceUnavailable, err)
	case errors.Is(err, errWatchNotFound):
		apierr.Write(w, http.StatusNotFound, err)
	case errors.Is(err, errWatchFull):
		apierr.Write(w, http.StatusConflict, err)
	case errors.As(err, &msg):
		apierr.Write(w, http.StatusBadRequest, err)
	default:
		apierr.Write(w, http.StatusInternalServerError, err)
	}
}

func (s *Service) watchRoutes() {
	s.handle("GET /api/v1/assistant/watches", func(w http.ResponseWriter, r *http.Request) {
		vs, err := s.watchViews(r.Context(), strings.TrimSpace(r.URL.Query().Get("project")))
		if err != nil {
			writeWatchError(w, err)
			return
		}
		apierr.WriteJSON(w, map[string]any{"watches": vs})
	})
	full := func(w http.ResponseWriter, r *http.Request) bool {
		if !fullRole(r) {
			apierr.Write(w, http.StatusForbidden, errWatchReadOnly)
			return false
		}
		return true
	}
	view := func(w http.ResponseWriter, r *http.Request, id string) {
		vs, err := s.watchViews(r.Context(), "")
		if err != nil {
			writeWatchError(w, err)
			return
		}
		for _, v := range vs {
			if v.ID == id {
				apierr.WriteJSON(w, map[string]any{"watch": v})
				return
			}
		}
		writeWatchError(w, errWatchNotFound)
	}
	s.handle("POST /api/v1/assistant/watches/{id}/confirm", func(w http.ResponseWriter, r *http.Request) {
		if !full(w, r) {
			return
		}
		c, err := s.confirmWatch(r.Context(), r.PathValue("id"))
		if err != nil {
			writeWatchError(w, err)
			return
		}
		view(w, r, c.ID)
	})
	s.handle("PUT /api/v1/assistant/watches/order", func(w http.ResponseWriter, r *http.Request) {
		if !full(w, r) {
			return
		}
		var in struct {
			IDs []string `json:"ids"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil {
			apierr.Write(w, http.StatusBadRequest, err)
			return
		}
		if err := s.reorderWatches(r.Context(), in.IDs); err != nil {
			writeWatchError(w, err)
			return
		}
		apierr.WriteJSON(w, map[string]any{"ok": true})
	})
	s.handle("PUT /api/v1/assistant/watches/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !full(w, r) {
			return
		}
		var in watchInput
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil {
			apierr.Write(w, http.StatusBadRequest, err)
			return
		}
		c, err := s.updateWatch(r.Context(), r.PathValue("id"), in)
		if err != nil {
			writeWatchError(w, err)
			return
		}
		view(w, r, c.ID)
	})
	s.handle("DELETE /api/v1/assistant/watches/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !full(w, r) {
			return
		}
		ok, err := s.deleteWatch(r.Context(), r.PathValue("id"))
		if err != nil {
			writeWatchError(w, err)
			return
		}
		if !ok {
			writeWatchError(w, errWatchNotFound)
			return
		}
		apierr.WriteJSON(w, map[string]any{"ok": true})
	})
}
