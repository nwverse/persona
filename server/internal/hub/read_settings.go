package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

const readSettingsKey = "sessions.read_settings"

type readSettings struct {
	Manual bool `json:"manual"`
}

func (h *Hub) readSettingsRoutes(api *http.ServeMux) {
	api.HandleFunc("GET /api/v1/read-settings", h.getReadSettings)
	api.HandleFunc("PUT /api/v1/read-settings", h.putReadSettings)
}

func (h *Hub) manualRead(ctx context.Context) (bool, error) {
	if h.store == nil {
		return false, nil
	}
	var set readSettings
	_, err := h.store.Setting(ctx, readSettingsKey, &set)
	return set.Manual, err
}

// One Hub decision for every session owner. Keep automatic marks working if the store cannot be read.
func (h *Hub) allowSeen(ctx context.Context, explicit bool) bool {
	if explicit {
		return true
	}
	manual, err := h.manualRead(ctx)
	if err != nil {
		h.logf("read settings: %v", err)
		return true
	}
	return !manual
}

func (h *Hub) getReadSettings(w http.ResponseWriter, r *http.Request) {
	manual, err := h.manualRead(r.Context())
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, err)
		return
	}
	writeJSON(w, map[string]any{"manual": manual, "can_write": principalOf(r.Context()).Role == roleFull})
}

func (h *Hub) putReadSettings(w http.ResponseWriter, r *http.Request) {
	if principalOf(r.Context()).Role != roleFull {
		writeErr(w, http.StatusForbidden, errors.New("a device that may only answer cannot change how items are marked read"))
		return
	}
	if h.store == nil {
		writeErr(w, http.StatusServiceUnavailable, errors.New("the read setting needs the database"))
		return
	}
	var body struct {
		Manual *bool `json:"manual"`
	}
	if err := decodeJSONBody(w, r, &body, 4<<10); err != nil || body.Manual == nil {
		writeBodyErr(w, err, errors.New("manual is required"))
		return
	}
	if err := h.store.PutSetting(r.Context(), readSettingsKey, readSettings{Manual: *body.Manual}); err != nil {
		writeErr(w, http.StatusServiceUnavailable, err)
		return
	}
	h.broadcast(map[string]any{"type": "read_settings", "manual": *body.Manual})
	writeJSON(w, map[string]any{"manual": *body.Manual, "can_write": true})
}

func (h *Hub) postSeen(w http.ResponseWriter, r *http.Request, _ *Machine) {
	id, ok := h.owner(w, r, h.bySession)
	if !ok {
		return
	}
	// Read the explicit mark before forwarding, preserving the HTTP request for the session owner.
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<10))
	if err != nil {
		writeBodyErr(w, err, err)
		return
	}
	var body readSettings
	if err := json.NewDecoder(bytes.NewReader(raw)).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeBodyErr(w, err, err)
		return
	}
	if !h.allowSeen(r.Context(), body.Manual) {
		writeJSON(w, map[string]any{"ok": true, "recorded": false})
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	r.ContentLength = int64(len(raw))
	h.forwardHTTP(w, r, id)
}
