package hub

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/i18n"
)

type ownerAuthStore interface {
	OwnerSubjects(context.Context, string) ([]db.OwnerSubject, error)
	CreateOwnerSubject(context.Context, string, string, string, string) (db.OwnerSubject, error)
	OwnerBinding(context.Context, string, string) (db.OwnerBinding, error)
	EnrollOwner(context.Context, string, string, string, string, string, int64) (db.OwnerBinding, error)
	WithdrawOwner(context.Context, string, string, string, string, int64) (db.OwnerBinding, error)
	AdmitOwner(context.Context, string, string) (db.OwnerBinding, func(), error)
}

func (h *Hub) ownerAuthStore() ownerAuthStore { s, _ := h.store.(ownerAuthStore); return s }

func sharingJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		writeErrCode(w, 400, "invalid_request", i18n.Err("sharing.invalid_request"))
		return false
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeErrCode(w, 400, "invalid_request", i18n.Err("sharing.invalid_request"))
		return false
	}
	if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		writeErrCode(w, 400, "invalid_request", i18n.Err("sharing.invalid_request"))
		return false
	}
	return true
}

// Only the closed admin mux mounts these routes. It owns persisted-session,
// access-scope and CSRF validation before these handlers run.
func (h *Hub) adminOwnerRoutes(mux *http.ServeMux) {
	wrap := func(fn func(http.ResponseWriter, *http.Request, ownerAuthStore, string)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if h.memberRefuses(w) {
				return
			}
			s := h.ownerAuthStore()
			if s == nil || h.hubID() == "" {
				writeErrCode(w, 503, "authority_unavailable", i18n.Err("sharing.unavailable"))
				return
			}
			admin, ok := h.adminSession(w, r)
			if !ok {
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
			defer cancel()
			fn(w, r.WithContext(ctx), s, admin.IDHash)
		}
	}
	fail := func(w http.ResponseWriter, err error) {
		if errors.Is(err, db.ErrEnrollmentConflict) {
			writeErrCode(w, 409, "enrollment_conflict", i18n.Err("sharing.conflict"))
			return
		}
		// Neither SQL errors nor caller-supplied identifiers reach errors or logs.
		writeErrCode(w, 404, "not_found", i18n.Err("sharing.not_found"))
	}
	mux.HandleFunc("GET /api/v1/admin/owner-subjects", wrap(func(w http.ResponseWriter, r *http.Request, s ownerAuthStore, _ string) {
		subjects, err := s.OwnerSubjects(r.Context(), h.hubID())
		if err != nil {
			fail(w, err)
			return
		}
		if subjects == nil {
			subjects = []db.OwnerSubject{}
		}
		writeJSON(w, map[string]any{"subjects": subjects})
	}))
	mux.HandleFunc("POST /api/v1/admin/owner-subjects", wrap(func(w http.ResponseWriter, r *http.Request, s ownerAuthStore, admin string) {
		var p struct {
			Label  string `json:"label"`
			Reason string `json:"reason"`
		}
		if !sharingJSON(w, r, &p) {
			return
		}
		out, err := s.CreateOwnerSubject(r.Context(), h.hubID(), p.Label, admin, p.Reason)
		if err != nil {
			fail(w, err)
			return
		}
		w.WriteHeader(201)
		writeJSON(w, out)
	}))
	mux.HandleFunc("GET /api/v1/admin/devices/{credential_id}/owner", wrap(func(w http.ResponseWriter, r *http.Request, s ownerAuthStore, _ string) {
		out, err := s.OwnerBinding(r.Context(), h.hubID(), r.PathValue("credential_id"))
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, out)
	}))
	mux.HandleFunc("PUT /api/v1/admin/devices/{credential_id}/owner", wrap(func(w http.ResponseWriter, r *http.Request, s ownerAuthStore, admin string) {
		var p struct {
			Subject  string `json:"owner_subject"`
			Expected int64  `json:"expected_revision"`
			Reason   string `json:"reason"`
		}
		if !sharingJSON(w, r, &p) {
			return
		}
		out, err := s.EnrollOwner(r.Context(), h.hubID(), r.PathValue("credential_id"), p.Subject, admin, p.Reason, p.Expected)
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, out)
	}))
	mux.HandleFunc("DELETE /api/v1/admin/devices/{credential_id}/owner", wrap(func(w http.ResponseWriter, r *http.Request, s ownerAuthStore, admin string) {
		var p struct {
			Expected int64  `json:"expected_revision"`
			Reason   string `json:"reason"`
		}
		if !sharingJSON(w, r, &p) {
			return
		}
		id := r.PathValue("credential_id")
		out, err := s.WithdrawOwner(r.Context(), h.hubID(), id, admin, p.Reason, p.Expected)
		if err != nil {
			fail(w, err)
			return
		}
		h.revoked(id)
		h.sendRevocations(newlyReachable(nil, h.meshView()))
		writeJSON(w, out)
	}))
}
