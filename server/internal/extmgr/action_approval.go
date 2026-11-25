package extmgr

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/extwire"
)

// actionApprovalBook is the short-lived proof that the connector gate already
// let one action through. It deliberately has no long-term signing key: the
// 128-bit request id is the capability and the Agent API persists its consumption.
type actionApprovalBook struct {
	mu      sync.Mutex
	entries map[string]actionApprovalEntry
	now     func() time.Time
}

type actionApprovalEntry struct {
	expires  time.Time
	audience string
	consumed bool
}

type actionApprovalFailure struct {
	status int
	code   string
	err    error
}

func (e *actionApprovalFailure) Error() string { return e.err.Error() }

func actionApprovalErr(status int, code, message string) error {
	return &actionApprovalFailure{status: status, code: code, err: errors.New(message)}
}

func validActionApprovalID(id string) bool {
	if !strings.HasPrefix(id, "odc_act") || len(id) != len("odc_act")+32 {
		return false
	}
	raw, err := hex.DecodeString(strings.TrimPrefix(id, "odc_act"))
	return err == nil && len(raw) == 16
}

func (b *actionApprovalBook) clock() time.Time {
	if b.now != nil {
		return b.now().UTC()
	}
	return time.Now().UTC()
}

func (b *actionApprovalBook) pruneLocked(now time.Time) {
	for id, entry := range b.entries {
		if !entry.expires.After(now) {
			delete(b.entries, id)
		}
	}
}

func validAgentAPIAudience(audience string) bool {
	hash, ok := strings.CutPrefix(audience, "agent-api:")
	raw, err := hex.DecodeString(hash)
	return ok && len(raw) == 32 && err == nil && hash == strings.ToLower(hash)
}

func (b *actionApprovalBook) register(id string, expires time.Time) error {
	return b.registerForAudience(id, expires, "")
}

func (b *actionApprovalBook) registerForAudience(id string, expires time.Time, audience string) error {
	if !validActionApprovalID(id) {
		return actionApprovalErr(http.StatusBadRequest, "invalid_decision", "the action approval id is invalid")
	}
	if audience != "" && !validAgentAPIAudience(audience) {
		return actionApprovalErr(http.StatusBadRequest, "invalid_decision", "the Agent API audience is invalid")
	}
	now := b.clock()
	if !expires.After(now) || expires.After(now.Add(15*time.Minute)) {
		return actionApprovalErr(http.StatusBadRequest, "invalid_decision", "the action approval expiry is invalid")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.entries == nil {
		b.entries = map[string]actionApprovalEntry{}
	}
	b.pruneLocked(now)
	if entry, ok := b.entries[id]; ok && (entry.consumed || entry.audience != audience) {
		return actionApprovalErr(http.StatusConflict, "decision_consumed", "the action approval was already consumed or has a different audience")
	}
	b.entries[id] = actionApprovalEntry{expires: expires.UTC(), audience: audience}
	return nil
}

func (b *actionApprovalBook) consume(id string, in assistantwire.OutsideWriteApproval) (time.Time, error) {
	if !validActionApprovalID(id) {
		return time.Time{}, actionApprovalErr(http.StatusNotFound, "decision_not_found", "the action approval was not found")
	}
	now := b.clock()
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pruneLocked(now)
	entry, ok := b.entries[id]
	if !ok {
		return time.Time{}, actionApprovalErr(http.StatusNotFound, "decision_not_found", "the action approval was not found")
	}
	if entry.consumed {
		return time.Time{}, actionApprovalErr(http.StatusConflict, "decision_consumed", "the action approval was already consumed")
	}
	if !entry.expires.After(now) || !in.ExpiresAt.After(now) || in.ExpiresAt.After(entry.expires) {
		return time.Time{}, actionApprovalErr(http.StatusForbidden, "decision_expired", "the action approval is expired or extends its registered expiry")
	}
	validMethod := false
	switch in.Method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		validMethod = true
	}
	hash, err := hex.DecodeString(in.BodySHA256)
	if !validMethod || in.Version != 1 || !strings.HasPrefix(in.Audience, "agent-api:") ||
		!strings.HasPrefix(in.Path, "/agent/v1/") || strings.ContainsAny(in.Path, "?#\r\n") ||
		len(in.Path) > 8192 || len(in.Query) > 8192 || strings.ContainsAny(in.Query, "\r\n") ||
		strings.TrimSpace(in.Account) != in.Account || len(in.Account) > 64 ||
		err != nil || len(hash) != 32 || in.BodySHA256 != strings.ToLower(in.BodySHA256) || in.ApproveOption != "Approve" ||
		(entry.audience != "" && in.Audience != entry.audience) {
		return time.Time{}, actionApprovalErr(http.StatusForbidden, "decision_scope_mismatch", "the action approval fingerprint is invalid")
	}
	entry.consumed = true
	b.entries[id] = entry
	return in.ExpiresAt, nil
}

func (h *Manager) registerActionApproval(_ context.Context, id string, expires time.Time) error {
	return h.actionApprovals.register(id, expires)
}

func (h *Manager) actionApprovalRoutes(api *http.ServeMux) {
	api.HandleFunc("POST /api/v1/connector-decisions/{id}/consume", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(extwire.HeaderRole) != "decision" {
			writeErrCode(w, http.StatusForbidden, "permission_denied", errors.New("only the approved action service may consume this decision"))
			return
		}
		var in assistantwire.OutsideWriteApproval
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&in); err != nil {
			writeErrCode(w, http.StatusBadRequest, "invalid_decision", err)
			return
		}
		expires, err := h.actionApprovals.consume(r.PathValue("id"), in)
		if err != nil {
			var failure *actionApprovalFailure
			if errors.As(err, &failure) {
				writeErrCode(w, failure.status, failure.code, failure.err)
				return
			}
			writeErrCode(w, http.StatusInternalServerError, "internal_error", err)
			return
		}
		writeJSON(w, map[string]any{"ok": true, "expires_at": expires})
	})
}
