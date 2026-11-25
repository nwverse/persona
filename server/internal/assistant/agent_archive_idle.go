package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

const (
	idleArchiveAuditKey        = "sessions.idle_archive_audit"
	idleArchiveAuditMaxEntries = 500
	idleArchiveAuditDefaultLim = 50
	idleArchiveAuditMaxLim     = 200
	idleArchiveMaxBodyBytes    = 8 << 10
	idleArchiveMinMinutes      = 1
	idleArchiveMaxMinutes      = 365 * 24 * 60
	idleArchiveMaxReasonRunes  = 200

	idleArchiveAuditActionSuccess = "session_idle_archive"
	idleArchiveAuditActionRefused = "session_idle_archive_refused"
	idleArchiveAuditCorePlugin    = "core.idle_archive"
)

// Refusal codes returned with HTTP 409 when a guarded idle archive-and-close request is blocked.
const (
	idleArchiveCodeMainConversation      = "protected_main_conversation"
	idleArchiveCodeDirectionLead         = "protected_direction_lead"
	idleArchiveCodeAssistantConversation = "protected_assistant_conversation"
	idleArchiveCodeUnresolvedInteraction = "unresolved_interaction"
	idleArchiveCodeSessionBusy           = "session_busy"
	idleArchiveCodeSessionUnverified     = "session_unverified"
	idleArchiveCodeOpenTodo              = "open_todo_session"
	idleArchiveCodeExternalSession       = "external_session"
	idleArchiveCodeSubagentSession       = "subagent_session"
	idleArchiveCodeIdleUnverified        = "idle_unverified"
	idleArchiveCodeNotIdleLongEnough     = "not_idle_long_enough"
)

// Audit outcome values persisted in sessions.idle_archive_audit and plugin_audit.
const (
	idleArchiveOutcomeArchivedAndClosed    = "archived_and_closed"
	idleArchiveOutcomeArchivedNotRunning   = "archived_not_running"
	idleArchiveOutcomeArchivedCloseRefused = "archived_close_refused"
	idleArchiveOutcomeArchivedCloseOffline = "archived_close_offline"
	idleArchiveOutcomeArchivedCloseFailed  = "archived_close_failed"
	idleArchiveOutcomeArchivedOnly         = "archived_only"
	idleArchiveOutcomeAlreadyArchived      = "already_archived"
	idleArchiveOutcomeRefused              = "refused"
)

type extensionAuditWriter interface {
	AddExtensionAudit(ctx context.Context, id, action, actor string, detail json.RawMessage) error
}

type idleArchiveAuditBook struct {
	mu      sync.Mutex
	loaded  bool
	entries []assistantwire.IdleArchiveAuditEntry
	now     func() time.Time
}

func (svc *Service) idleArchiveNow() time.Time {
	if svc.idleArchiveAudit.now != nil {
		return svc.idleArchiveAudit.now()
	}
	return time.Now()
}

func (svc *Service) idleArchiveRoutes() {
	svc.handle(assistantwire.RouteAgentIdleArchive, svc.handleAgentIdleArchive)
	svc.handle(assistantwire.RouteAgentIdleArchiveAudit, svc.handleAgentIdleArchiveAudit)
}

func idleArchiveCaller(r *http.Request) (actor, extID string, isExt, allowed bool) {
	if fullRole(r) {
		if dev := strings.TrimSpace(r.Header.Get(assistantwire.DeviceNameHeader)); dev != "" {
			return "device:" + dev, "", false, true
		}
		return "full", "", false, true
	}
	if r.Header.Get(RoleHeader) == roleExtension {
		extID = strings.TrimSpace(r.Header.Get(ExtensionHeader))
		if extID == "" {
			return "", "", true, false
		}
		return "extension:" + extID, extID, true, true
	}
	return "", "", false, false
}

func decodeIdleArchiveRequest(r *http.Request) (assistantwire.IdleArchiveRequest, error) {
	var req assistantwire.IdleArchiveRequest
	body, err := io.ReadAll(io.LimitReader(r.Body, idleArchiveMaxBodyBytes+1))
	if err != nil {
		return req, errors.New("could not read request body")
	}
	if len(body) > idleArchiveMaxBodyBytes {
		return req, errors.New("request body is too large")
	}
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return req, fmt.Errorf("invalid JSON body: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return req, errors.New("unexpected trailing content in JSON body")
	}
	if req.IdleMinutes < idleArchiveMinMinutes || req.IdleMinutes > idleArchiveMaxMinutes {
		return req, fmt.Errorf("idle_minutes must be between %d and %d", idleArchiveMinMinutes, idleArchiveMaxMinutes)
	}
	req.Reason = strings.TrimSpace(req.Reason)
	if utf8.RuneCountInString(req.Reason) > idleArchiveMaxReasonRunes {
		return req, fmt.Errorf("reason must be at most %d characters", idleArchiveMaxReasonRunes)
	}
	if strings.ContainsFunc(req.Reason, func(r rune) bool { return unicode.Is(unicode.Cc, r) }) {
		return req, errors.New("reason must not contain control characters")
	}
	return req, nil
}

func sessionCandidateIDs(sid string, row proto.Session, found bool, raw proto.Session, listed bool) []string {
	var ids []string
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id != "" && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	add(sid)
	if found {
		add(row.SessionID)
		for _, a := range row.Aliases() {
			add(a)
		}
	}
	if listed {
		add(raw.SessionID)
		for _, a := range raw.Aliases() {
			add(a)
		}
	}
	return ids
}

func (svc *Service) sessionHasPendingInteraction(machineID string, candidateIDs []string) bool {
	for _, ix := range svc.pendingInteractions() {
		if ix.MachineID != machineID {
			continue
		}
		if slices.Contains(candidateIDs, ix.SessionID) {
			return true
		}
	}
	return false
}

func sessionHasUnresolvedCard(s proto.Session) bool {
	return s.PendingInteractions > 0 || s.Status == proto.StatusBlocked || s.State == proto.StateNeedsYou
}

func sessionBusyForIdleArchive(s proto.Session) bool {
	return agentCloseBusy(s) || s.Status == proto.StatusUnknown || wakePending(s)
}

func idleArchiveCloseOutcome(outcome string) string {
	switch outcome {
	case archiveCloseClosed:
		return idleArchiveOutcomeArchivedAndClosed
	case archiveCloseNotRunning:
		return idleArchiveOutcomeArchivedNotRunning
	case archiveCloseRefused:
		return idleArchiveOutcomeArchivedCloseRefused
	case archiveCloseOffline:
		return idleArchiveOutcomeArchivedCloseOffline
	default:
		return idleArchiveOutcomeArchivedCloseFailed
	}
}

func (svc *Service) recordIdleArchiveAudit(ctx context.Context, entry assistantwire.IdleArchiveAuditEntry) (assistantwire.IdleArchiveAuditID, bool) {
	if entry.ID == "" {
		entry.ID = assistantwire.IdleArchiveAuditID("ia_" + randomHex(8))
	}
	if entry.At.IsZero() {
		entry.At = svc.idleArchiveNow().UTC()
	}
	store := svc.store()
	if store == nil {
		return entry.ID, false
	}
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), agentArchiveTimeout)
	defer cancel()

	persisted := false
	svc.idleArchiveAudit.mu.Lock()
	if !svc.idleArchiveAudit.loaded {
		var saved []assistantwire.IdleArchiveAuditEntry
		if _, err := store.Setting(writeCtx, idleArchiveAuditKey, &saved); err != nil {
			svc.logf("idle archive audit load: %v", err)
		} else {
			svc.idleArchiveAudit.entries = saved
			svc.idleArchiveAudit.loaded = true
		}
	}
	if svc.idleArchiveAudit.loaded {
		next := make([]assistantwire.IdleArchiveAuditEntry, 0, min(len(svc.idleArchiveAudit.entries)+1, idleArchiveAuditMaxEntries))
		next = append(next, entry)
		for _, old := range svc.idleArchiveAudit.entries {
			if len(next) >= idleArchiveAuditMaxEntries {
				break
			}
			next = append(next, old)
		}
		if err := store.PutSetting(writeCtx, idleArchiveAuditKey, next); err != nil {
			svc.logf("idle archive audit save: %v", err)
		} else {
			svc.idleArchiveAudit.entries = next
			persisted = true
		}
	}
	svc.idleArchiveAudit.mu.Unlock()

	if extAudit, ok := store.(extensionAuditWriter); ok {
		plugin := entry.Extension
		if plugin == "" {
			plugin = idleArchiveAuditCorePlugin
		}
		action := idleArchiveAuditActionSuccess
		if entry.Outcome == idleArchiveOutcomeRefused {
			action = idleArchiveAuditActionRefused
		}
		detail := map[string]any{
			"audit_id":       entry.ID,
			"machine_id":     entry.MachineID,
			"session_id":     entry.SessionID,
			"idle_minutes":   entry.IdleMinutes,
			"close_terminal": entry.CloseTerminal,
			"outcome":        entry.Outcome,
		}
		if entry.ObservedIdleSeconds != nil {
			detail["observed_idle_seconds"] = *entry.ObservedIdleSeconds
		}
		if entry.RefusalCode != "" {
			detail["refusal_code"] = entry.RefusalCode
		}
		if entry.CloseOutcome != "" {
			detail["close_outcome"] = entry.CloseOutcome
		}
		if entry.Reason != "" {
			detail["reason"] = entry.Reason
		}
		if raw, err := json.Marshal(detail); err == nil {
			if err := extAudit.AddExtensionAudit(writeCtx, plugin, action, entry.Actor, raw); err != nil {
				svc.logf("idle archive plugin_audit: %v", err)
			}
		}
	}

	return entry.ID, persisted
}

func (svc *Service) loadIdleArchiveAudit(ctx context.Context) ([]assistantwire.IdleArchiveAuditEntry, error) {
	store := svc.store()
	if store == nil {
		return nil, errors.New("no database")
	}
	readCtx, cancel := context.WithTimeout(ctx, agentArchiveTimeout)
	defer cancel()
	svc.idleArchiveAudit.mu.Lock()
	defer svc.idleArchiveAudit.mu.Unlock()
	if !svc.idleArchiveAudit.loaded {
		var saved []assistantwire.IdleArchiveAuditEntry
		if _, err := store.Setting(readCtx, idleArchiveAuditKey, &saved); err != nil {
			return nil, err
		}
		svc.idleArchiveAudit.entries = saved
		svc.idleArchiveAudit.loaded = true
	}
	out := make([]assistantwire.IdleArchiveAuditEntry, len(svc.idleArchiveAudit.entries))
	copy(out, svc.idleArchiveAudit.entries)
	return out, nil
}

func (svc *Service) handleAgentIdleArchive(w http.ResponseWriter, r *http.Request) {
	actor, extID, isExt, allowed := idleArchiveCaller(r)
	if !allowed {
		apierr.Write(w, http.StatusForbidden, i18n.Err("hub-agent_archives.forbidden"))
		return
	}
	req, err := decodeIdleArchiveRequest(r)
	if err != nil {
		apierr.WriteCode(w, http.StatusBadRequest, "invalid_request", err)
		return
	}
	closeTerminal := true
	if req.CloseTerminal != nil {
		closeTerminal = *req.CloseTerminal
	}

	ctx := r.Context()
	if isExt {
		ctx = svc.withExtensionReader(ctx, extID)
	}
	if svc.store() == nil {
		apierr.WriteCode(w, http.StatusServiceUnavailable, "no_database", errors.New("archive audit storage is unavailable"))
		return
	}
	if err := svc.ensureTodosLoaded(ctx); err != nil {
		apierr.WriteCode(w, http.StatusServiceUnavailable, "todos_unavailable", errors.New("could not load open tasks to verify session safety"))
		return
	}

	mid := r.PathValue("mid")
	sid := strings.TrimSpace(r.PathValue("sid"))
	m, ok := svc.fleetMachine(mid)
	if !ok {
		apierr.Write(w, http.StatusNotFound, i18n.Err("devices-api.unknown_machine"))
		return
	}
	mid = m.ID

	list, _, err := svc.archivedInventory(ctx, mid)
	if err != nil {
		apierr.Write(w, http.StatusInternalServerError, i18n.Err("hub-agent_archives.storage"))
		return
	}
	var row proto.Session
	found := false
	for _, s := range list {
		if s.SessionID == sid {
			row = s
			row.MachineID = mid
			found = true
			break
		}
	}
	raw, listed := svc.nodeSession(mid, sid)
	if !listed && found && row.SessionID != sid {
		raw, listed = svc.nodeSession(mid, row.SessionID)
	}

	// Never reveal or act on a session in a privacy space hidden from this extension.
	if isExt && ((found && svc.extensionHides(ctx, runSpace(row))) || (listed && svc.extensionHides(ctx, runSpace(raw)))) {
		apierr.Write(w, http.StatusNotFound, i18n.Err("hub-deleted_sessions.unknown"))
		return
	}

	refuse := func(code, msg string, obsSec *int64) {
		auditID, _ := svc.recordIdleArchiveAudit(ctx, assistantwire.IdleArchiveAuditEntry{
			Actor:               actor,
			Extension:           extID,
			MachineID:           assistantwire.MachineID(mid),
			SessionID:           assistantwire.SessionID(sid),
			IdleMinutes:         req.IdleMinutes,
			ObservedIdleSeconds: obsSec,
			CloseTerminal:       closeTerminal,
			Outcome:             idleArchiveOutcomeRefused,
			RefusalCode:         code,
			Reason:              req.Reason,
		})
		apierr.WriteBody(w, http.StatusConflict, map[string]any{
			"error":    msg,
			"code":     code,
			"audit_id": auditID,
		})
	}

	cfg := svc.settings(ctx)
	candidates := sessionCandidateIDs(sid, row, found, raw, listed)

	// 1. Hard-refuse main conversation, direction leads, and all Persona assistant conversations.
	if cfg.Main != "" && slices.Contains(candidates, cfg.Main) {
		refuse(idleArchiveCodeMainConversation, "the main Persona conversation cannot be archived by idle archive", nil)
		return
	}
	for _, id := range candidates {
		if slices.Contains(cfg.Leaders, id) || svc.isLeader(ctx, id) {
			refuse(idleArchiveCodeDirectionLead, "a direction lead conversation cannot be archived by idle archive", nil)
			return
		}
	}
	if (found && row.Assistant) || (listed && raw.Assistant) {
		refuse(idleArchiveCodeAssistantConversation, "Persona assistant conversations cannot be archived by idle archive", nil)
		return
	}
	if !found {
		apierr.Write(w, http.StatusNotFound, i18n.Err("hub-deleted_sessions.unknown"))
		return
	}

	// 2. Hard-refuse sessions with unresolved cards / pending interactions.
	if sessionHasUnresolvedCard(row) || (listed && sessionHasUnresolvedCard(raw)) || svc.sessionHasPendingInteraction(mid, candidates) {
		refuse(idleArchiveCodeUnresolvedInteraction, "session has an unresolved card or pending interaction", nil)
		return
	}

	// 3. Hard-refuse external-tool-owned sessions and subagent rows.
	if row.SourceKind != "" || (listed && raw.SourceKind != "") {
		refuse(idleArchiveCodeExternalSession, "sessions owned by an external tool cannot be archived by idle archive", nil)
		return
	}
	if row.IsSubagent || (listed && raw.IsSubagent) {
		refuse(idleArchiveCodeSubagentSession, "subagent rows cannot be archived independently by idle archive", nil)
		return
	}

	// 4. Hard-refuse unverified live sessions and any working / busy / wake-pending session.
	if (row.Status != proto.StatusOffline || row.PaneID != "" || row.Host != "") && !listed {
		refuse(idleArchiveCodeSessionUnverified, "live session state could not be verified on its computer", nil)
		return
	}
	if sessionBusyForIdleArchive(row) || (listed && sessionBusyForIdleArchive(raw)) {
		refuse(idleArchiveCodeSessionBusy, "session is working, starting, has background work, queued input, a loop, or running servers", nil)
		return
	}

	// 5. Hard-refuse sessions linked to an open to-do (including active task leaders and workers).
	links := svc.sessionLinks()
	var lastDone time.Time
	for _, id := range candidates {
		open, _, _, doneAt := links.link(mid, id)
		if open {
			refuse(idleArchiveCodeOpenTodo, "session is linked to an open to-do", nil)
			return
		}
		if doneAt.After(lastDone) {
			lastDone = doneAt
		}
	}

	// 6. Strictly verify idle duration > idle_minutes (fail closed when UpdatedAt is zero or in the future).
	lastActivity := row.UpdatedAt
	if listed && raw.UpdatedAt.After(lastActivity) {
		lastActivity = raw.UpdatedAt
	}
	if lastActivity.IsZero() {
		refuse(idleArchiveCodeIdleUnverified, "session idle duration cannot be verified because last activity time is missing", nil)
		return
	}
	if lastDone.After(lastActivity) {
		lastActivity = lastDone
	}
	if row.ArchiveChangedAt != nil && row.ArchiveChangedAt.After(lastActivity) {
		lastActivity = *row.ArchiveChangedAt
	}
	observed := svc.idleArchiveNow().UTC().Sub(lastActivity)
	obsSec := int64(observed.Seconds())
	threshold := time.Duration(req.IdleMinutes) * time.Minute
	if observed <= threshold {
		refuse(idleArchiveCodeNotIdleLongEnough, fmt.Sprintf("session has only been idle for %ds; requires strictly more than %d minute(s)", max(obsSec, 0), req.IdleMinutes), &obsSec)
		return
	}

	// 7. Already archived and offline: idempotent success with audit entry.
	if row.Archived && row.Status == proto.StatusOffline && row.PaneID == "" && row.Host == "" {
		closeOutcome := ""
		var closeRes *assistantwire.IdleArchiveCloseResult
		if closeTerminal {
			closeOutcome = archiveCloseNotRunning
			closeRes = &assistantwire.IdleArchiveCloseResult{Closed: false, Outcome: archiveCloseNotRunning}
		}
		auditID, persisted := svc.recordIdleArchiveAudit(ctx, assistantwire.IdleArchiveAuditEntry{
			Actor:               actor,
			Extension:           extID,
			MachineID:           assistantwire.MachineID(mid),
			SessionID:           assistantwire.SessionID(row.SessionID),
			IdleMinutes:         req.IdleMinutes,
			ObservedIdleSeconds: &obsSec,
			CloseTerminal:       closeTerminal,
			Outcome:             idleArchiveOutcomeAlreadyArchived,
			CloseOutcome:        closeOutcome,
			Reason:              req.Reason,
		})
		apierr.WriteJSON(w, assistantwire.IdleArchiveResponse{
			OK:                  true,
			Archived:            true,
			AlreadyArchived:     true,
			ObservedIdleSeconds: obsSec,
			Close:               closeRes,
			AuditID:             auditID,
			AuditPersisted:      persisted,
		})
		return
	}

	// 8. Perform archive (+ terminal close when requested) using core archive primitives.
	if closeTerminal {
		res, err := svc.archiveAndCloseAgentSession(ctx, mid, row.SessionID)
		if err != nil {
			code := "archive_failed"
			var refusal *agentArchiveError
			if errors.As(err, &refusal) && refusal.code != "" {
				code = refusal.code
			}
			svc.recordIdleArchiveAudit(ctx, assistantwire.IdleArchiveAuditEntry{
				Actor:               actor,
				Extension:           extID,
				MachineID:           assistantwire.MachineID(mid),
				SessionID:           assistantwire.SessionID(row.SessionID),
				IdleMinutes:         req.IdleMinutes,
				ObservedIdleSeconds: &obsSec,
				CloseTerminal:       true,
				Outcome:             idleArchiveOutcomeRefused,
				RefusalCode:         code,
				Reason:              req.Reason,
			})
			svc.writeAgentArchiveError(w, err)
			return
		}
		closeRes := &assistantwire.IdleArchiveCloseResult{
			Closed:  res.Closed,
			Outcome: res.Outcome,
			Detail:  res.Detail,
		}
		if res.ReasonKey != "" {
			closeRes.Reason = i18n.T(i18n.OfWriter(w), res.ReasonKey)
		}
		auditID, persisted := svc.recordIdleArchiveAudit(ctx, assistantwire.IdleArchiveAuditEntry{
			Actor:               actor,
			Extension:           extID,
			MachineID:           assistantwire.MachineID(mid),
			SessionID:           assistantwire.SessionID(row.SessionID),
			IdleMinutes:         req.IdleMinutes,
			ObservedIdleSeconds: &obsSec,
			CloseTerminal:       true,
			Outcome:             idleArchiveCloseOutcome(res.Outcome),
			CloseOutcome:        res.Outcome,
			Reason:              req.Reason,
		})
		apierr.WriteJSON(w, assistantwire.IdleArchiveResponse{
			OK:                  true,
			Archived:            true,
			ObservedIdleSeconds: obsSec,
			Close:               closeRes,
			AuditID:             auditID,
			AuditPersisted:      persisted,
		})
		return
	}

	if _, err := svc.setAgentArchive(ctx, mid, row.SessionID, true); err != nil {
		code := "archive_failed"
		var refusal *agentArchiveError
		if errors.As(err, &refusal) && refusal.code != "" {
			code = refusal.code
		}
		svc.recordIdleArchiveAudit(ctx, assistantwire.IdleArchiveAuditEntry{
			Actor:               actor,
			Extension:           extID,
			MachineID:           assistantwire.MachineID(mid),
			SessionID:           assistantwire.SessionID(row.SessionID),
			IdleMinutes:         req.IdleMinutes,
			ObservedIdleSeconds: &obsSec,
			CloseTerminal:       false,
			Outcome:             idleArchiveOutcomeRefused,
			RefusalCode:         code,
			Reason:              req.Reason,
		})
		svc.writeAgentArchiveError(w, err)
		return
	}
	auditID, persisted := svc.recordIdleArchiveAudit(ctx, assistantwire.IdleArchiveAuditEntry{
		Actor:               actor,
		Extension:           extID,
		MachineID:           assistantwire.MachineID(mid),
		SessionID:           assistantwire.SessionID(row.SessionID),
		IdleMinutes:         req.IdleMinutes,
		ObservedIdleSeconds: &obsSec,
		CloseTerminal:       false,
		Outcome:             idleArchiveOutcomeArchivedOnly,
		Reason:              req.Reason,
	})
	apierr.WriteJSON(w, assistantwire.IdleArchiveResponse{
		OK:                  true,
		Archived:            true,
		ObservedIdleSeconds: obsSec,
		AuditID:             auditID,
		AuditPersisted:      persisted,
	})
}

func (svc *Service) handleAgentIdleArchiveAudit(w http.ResponseWriter, r *http.Request) {
	_, extID, isExt, allowed := idleArchiveCaller(r)
	if !allowed {
		apierr.Write(w, http.StatusForbidden, i18n.Err("hub-agent_archives.forbidden"))
		return
	}
	limit := idleArchiveAuditDefaultLim
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			apierr.WriteCode(w, http.StatusBadRequest, "invalid_limit", fmt.Errorf("limit %q must be a positive integer", raw))
			return
		}
		limit = min(n, idleArchiveAuditMaxLim)
	}
	filterMachine := strings.TrimSpace(r.URL.Query().Get("machine_id"))
	if filterMachine != "" {
		if m, ok := svc.fleetMachine(filterMachine); ok {
			filterMachine = m.ID
		}
	}
	filterSession := strings.TrimSpace(r.URL.Query().Get("session_id"))
	filterOutcome := strings.TrimSpace(r.URL.Query().Get("outcome"))

	entries, err := svc.loadIdleArchiveAudit(r.Context())
	if err != nil {
		apierr.WriteCode(w, http.StatusServiceUnavailable, "audit_unavailable", err)
		return
	}
	out := make([]assistantwire.IdleArchiveAuditEntry, 0, min(len(entries), limit))
	for _, e := range entries {
		if isExt && e.Extension != extID {
			continue
		}
		if filterMachine != "" && string(e.MachineID) != filterMachine {
			continue
		}
		if filterSession != "" && string(e.SessionID) != filterSession {
			continue
		}
		if filterOutcome != "" && e.Outcome != filterOutcome {
			continue
		}
		out = append(out, e)
		if len(out) >= limit {
			break
		}
	}
	apierr.WriteJSON(w, map[string]any{"entries": out})
}
