package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
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
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/transcript"
	"github.com/nwverse/persona/server/internal/wireids"
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

// Refusal codes returned with HTTP 409 (or 503/502 for close transport failures) when a guarded idle archive-and-close request is blocked.
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
	idleArchiveCodeCloseRefused          = "close_refused"
	idleArchiveCodeCloseOffline          = "close_offline"
	idleArchiveCodeCloseFailed           = "close_failed"
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
	mu       sync.Mutex
	loaded   bool
	entries  []assistantwire.IdleArchiveAuditEntry
	inFlight map[[2]string]chan struct{}
	now      func() time.Time
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

// lockIdleArchiveSession serializes guarded idle-archive operations for one (machine, session) pair
// so concurrent callers cannot race between verification and terminal retirement / archive writes.
func (svc *Service) lockIdleArchiveSession(ctx context.Context, mid, sid string) (func(), error) {
	key := [2]string{mid, sid}
	for {
		svc.idleArchiveAudit.mu.Lock()
		if svc.idleArchiveAudit.inFlight == nil {
			svc.idleArchiveAudit.inFlight = map[[2]string]chan struct{}{}
		}
		if ch, busy := svc.idleArchiveAudit.inFlight[key]; busy {
			svc.idleArchiveAudit.mu.Unlock()
			select {
			case <-ch:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		done := make(chan struct{})
		svc.idleArchiveAudit.inFlight[key] = done
		svc.idleArchiveAudit.mu.Unlock()
		return func() {
			svc.idleArchiveAudit.mu.Lock()
			delete(svc.idleArchiveAudit.inFlight, key)
			close(done)
			svc.idleArchiveAudit.mu.Unlock()
		}, nil
	}
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

func (svc *Service) isOutsideDirectionLead(ctx context.Context, mid string, candidates []string) (bool, error) {
	b := &svc.directions
	b.mu.Lock()
	defer b.mu.Unlock()
	if !svc.directionsLocked(ctx) {
		return false, errors.New("could not load directions to verify session safety")
	}
	targetMachine := svc.canonical(mid)
	for _, d := range b.m {
		if d == nil || d.EndedAt != nil || d.Outside == nil || d.Outside.SessionID == "" {
			continue
		}
		if d.Outside.MachineID != "" && svc.canonical(d.Outside.MachineID) != targetMachine {
			continue
		}
		if slices.Contains(candidates, d.Outside.SessionID) {
			return true, nil
		}
	}
	return false, nil
}

func (svc *Service) sessionHasOpenResponsibleTodo(mid string, candidates []string) bool {
	targetMachine := svc.canonical(mid)
	svc.todos.mu.RLock()
	defer svc.todos.mu.RUnlock()
	for _, t := range svc.todos.todos {
		if t == nil || t.DoneAt != nil || t.Responsible == nil {
			continue
		}
		if t.Responsible.Kind != db.ResponsibleSession || t.Responsible.SessionID == "" {
			continue
		}
		if t.Responsible.MachineID != "" && svc.canonical(t.Responsible.MachineID) != targetMachine {
			continue
		}
		if slices.Contains(candidates, t.Responsible.SessionID) {
			return true
		}
	}
	return false
}

// machineLiveVerified returns true only when mid is currently reachable with a fresh
// live session list from its active node connection (never stale cached m.sessions
// from before a disconnect or during warm-start reconnect grace).
func (svc *Service) machineLiveVerified(mid string) bool {
	if svc.devs == nil {
		return false
	}
	d, ok := svc.devs.Summary(mid)
	if !ok || !d.Reachable {
		return false
	}
	if shown, ok := svc.devs.Shown(mid); ok && (!shown.Listed || !shown.Until.IsZero()) {
		return false
	}
	return true
}

func sessionHasUnresolvedCard(s proto.Session) bool {
	return s.PendingInteractions > 0 || s.Status == proto.StatusBlocked || s.State == proto.StateNeedsYou
}

func sessionBusyForIdleArchive(s proto.Session) bool {
	return agentCloseBusy(s) || s.Status == proto.StatusUnknown || wakePending(s)
}

// liveSessionStatusSafe requires an explicit idle or done status and forbids StateUnknown
// (including empty or unrecognized future statuses) on any live/non-offline session.
func liveSessionStatusSafe(s proto.Session) bool {
	if s.State == proto.StateUnknown {
		return false
	}
	return s.Status == proto.StatusIdle || s.Status == proto.StatusDone
}

func sessionIsConfirmedOffline(row proto.Session, raw proto.Session, listed bool) bool {
	if row.Status != proto.StatusOffline || row.PaneID != "" || row.Host != "" {
		return false
	}
	if listed && (raw.Status != proto.StatusOffline || raw.PaneID != "" || raw.Host != "") {
		return false
	}
	return true
}

func sessionIsExternalOrDesktop(s proto.Session) bool {
	return s.SourceKind != "" || s.AgentSurface == transcript.SurfaceDesktop
}

func (svc *Service) ensureIdleArchiveAuditLoaded(ctx context.Context) error {
	store := svc.store()
	if store == nil {
		return errors.New("no database")
	}
	readCtx, cancel := context.WithTimeout(ctx, agentArchiveTimeout)
	defer cancel()
	svc.idleArchiveAudit.mu.Lock()
	defer svc.idleArchiveAudit.mu.Unlock()
	if svc.idleArchiveAudit.loaded {
		return nil
	}
	var saved []assistantwire.IdleArchiveAuditEntry
	if _, err := store.Setting(readCtx, idleArchiveAuditKey, &saved); err != nil {
		return err
	}
	svc.idleArchiveAudit.entries = saved
	svc.idleArchiveAudit.loaded = true
	return nil
}

// recordIdleArchiveAudit durably appends entry to sessions.idle_archive_audit (bounded to 500
// newest entries) and mirrors metadata to plugin_audit when supported. On persistence failure
// it returns ("", false) so callers never expose a dangling audit_id absent from storage.
func (svc *Service) recordIdleArchiveAudit(ctx context.Context, entry assistantwire.IdleArchiveAuditEntry) (assistantwire.IdleArchiveAuditID, bool) {
	if entry.ID == "" {
		entry.ID = assistantwire.IdleArchiveAuditID("ia_" + randomHex(8))
	}
	if entry.At.IsZero() {
		entry.At = svc.idleArchiveNow().UTC()
	}
	store := svc.store()
	if store == nil {
		return "", false
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

	if !persisted {
		return "", false
	}

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

	return entry.ID, true
}

type idleArchiveStage struct {
	key       string
	hadPrev   bool
	prev      agentArchive
	changedAt time.Time
	snapshot  proto.Session
}

// stageIdleArchiveRecord writes Persona's archive record under the archive gate without emitting
// native-archive wishes or publishing the session list, recording the prior slot so a failed close
// or audit write can restore exact pre-call state without clobbering a concurrent change.
func (svc *Service) stageIdleArchiveRecord(ctx context.Context, row proto.Session, provisionalClose bool) (idleArchiveStage, error) {
	ctx, cancel := context.WithTimeout(ctx, agentArchiveTimeout)
	defer cancel()
	b := &svc.agentArchives
	if err := b.lock(ctx); err != nil {
		return idleArchiveStage{}, err
	}
	defer b.unlock()
	if err := svc.loadAgentArchivesLocked(ctx); err != nil {
		return idleArchiveStage{}, err
	}
	snapshot := archiveSnapshot(row)
	snapshot.SessionID = archiveSessionOf(row)
	key := runtimeListID(row.MachineID, snapshot.SessionID)
	prev, hadPrev := b.records[key]
	baseChanged := prev.ChangedAt
	if row.ArchiveChangedAt != nil && row.ArchiveChangedAt.After(baseChanged) {
		baseChanged = *row.ArchiveChangedAt
	}
	changedAt := nextAgentArchiveChangedAt(baseChanged)
	rec := agentArchive{Session: snapshot, Archived: true, ChangedAt: changedAt}
	if provisionalClose {
		rec.ClosedAt = time.Now().UTC().Add(agentArchiveCloseTimeout + agentArchiveTimeout)
	}
	next := maps.Clone(b.records)
	next[key] = rec
	if err := svc.saveAgentArchivesLocked(ctx, next); err != nil {
		return idleArchiveStage{}, err
	}
	delete(b.pending, key)
	return idleArchiveStage{
		key:       key,
		hadPrev:   hadPrev,
		prev:      prev,
		changedAt: changedAt,
		snapshot:  snapshot,
	}, nil
}

// rollbackIdleArchiveRecord reverts only the exact generation written by stageIdleArchiveRecord,
// preserving any newer concurrent archive or restore.
func (svc *Service) rollbackIdleArchiveRecord(ctx context.Context, st idleArchiveStage) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), agentArchiveTimeout)
	defer cancel()
	b := &svc.agentArchives
	if err := b.lock(ctx); err != nil {
		return err
	}
	defer b.unlock()
	if err := svc.loadAgentArchivesLocked(ctx); err != nil {
		return err
	}
	rec, ok := b.records[st.key]
	if !ok || !rec.ChangedAt.Equal(st.changedAt) {
		return nil
	}
	next := maps.Clone(b.records)
	if !st.hadPrev {
		delete(next, st.key)
	} else {
		next[st.key] = st.prev
	}
	return svc.saveAgentArchivesLocked(ctx, next)
}

func (svc *Service) loadIdleArchiveAudit(ctx context.Context) ([]assistantwire.IdleArchiveAuditEntry, error) {
	if err := svc.ensureIdleArchiveAuditLoaded(ctx); err != nil {
		return nil, err
	}
	svc.idleArchiveAudit.mu.Lock()
	defer svc.idleArchiveAudit.mu.Unlock()
	out := make([]assistantwire.IdleArchiveAuditEntry, len(svc.idleArchiveAudit.entries))
	copy(out, svc.idleArchiveAudit.entries)
	return out, nil
}

type idleArchiveEvaluation struct {
	row       proto.Session
	raw       proto.Session
	listed    bool
	offline   bool
	obsSec    int64
	notFound  bool
	storage   error
	refuse    string
	refuseMsg string
	refuseObs *int64
}

func (svc *Service) evaluateIdleArchiveTarget(ctx context.Context, mid, sid string, isExt bool, idleMinutes int) idleArchiveEvaluation {
	list, _, err := svc.archivedInventory(ctx, mid)
	if err != nil {
		return idleArchiveEvaluation{storage: err}
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
		return idleArchiveEvaluation{notFound: true}
	}
	// Unknown sessions return 404 before any protection check so arbitrary IDs cannot probe
	// cfg.Main, cfg.Leaders, or outside directions on another machine or hidden space.
	if !found {
		return idleArchiveEvaluation{notFound: true}
	}

	cfg := svc.settings(ctx)
	candidates := sessionCandidateIDs(sid, row, found, raw, listed)

	// 1. Hard-refuse main conversation, direction leads (including outside directions), and assistant conversations.
	if cfg.Main != "" && slices.Contains(candidates, cfg.Main) {
		return idleArchiveEvaluation{row: row, refuse: idleArchiveCodeMainConversation, refuseMsg: "the main Persona conversation cannot be archived by idle archive"}
	}
	for _, id := range candidates {
		if slices.Contains(cfg.Leaders, id) || svc.isLeader(ctx, id) {
			return idleArchiveEvaluation{row: row, refuse: idleArchiveCodeDirectionLead, refuseMsg: "a direction lead conversation cannot be archived by idle archive"}
		}
	}
	outsideLead, err := svc.isOutsideDirectionLead(ctx, mid, candidates)
	if err != nil {
		return idleArchiveEvaluation{storage: err}
	}
	if outsideLead {
		return idleArchiveEvaluation{row: row, refuse: idleArchiveCodeDirectionLead, refuseMsg: "a direction lead conversation cannot be archived by idle archive"}
	}
	if row.Assistant || (listed && raw.Assistant) {
		return idleArchiveEvaluation{row: row, refuse: idleArchiveCodeAssistantConversation, refuseMsg: "Persona assistant conversations cannot be archived by idle archive"}
	}

	// 2. Hard-refuse sessions with unresolved cards / pending interactions.
	if sessionHasUnresolvedCard(row) || (listed && sessionHasUnresolvedCard(raw)) || svc.sessionHasPendingInteraction(mid, candidates) {
		return idleArchiveEvaluation{row: row, refuse: idleArchiveCodeUnresolvedInteraction, refuseMsg: "session has an unresolved card or pending interaction"}
	}

	// 3. Hard-refuse external-tool-owned sessions (including offline desktop app sessions) and subagent rows.
	if sessionIsExternalOrDesktop(row) || (listed && sessionIsExternalOrDesktop(raw)) {
		return idleArchiveEvaluation{row: row, refuse: idleArchiveCodeExternalSession, refuseMsg: "sessions owned by an external tool cannot be archived by idle archive"}
	}
	if row.IsSubagent || (listed && raw.IsSubagent) {
		return idleArchiveEvaluation{row: row, refuse: idleArchiveCodeSubagentSession, refuseMsg: "subagent rows cannot be archived independently by idle archive"}
	}

	// 4. Hard-refuse unverified live sessions (offline/disconnected computer or stale warm-start cache)
	// and any working, busy, wake-pending, or unrecognized/unknown-status live session.
	offline := sessionIsConfirmedOffline(row, raw, listed)
	if !offline {
		if !listed || !svc.machineLiveVerified(mid) {
			return idleArchiveEvaluation{row: row, refuse: idleArchiveCodeSessionUnverified, refuseMsg: "live session state could not be verified on its computer"}
		}
	}
	if sessionBusyForIdleArchive(row) || (listed && sessionBusyForIdleArchive(raw)) {
		return idleArchiveEvaluation{row: row, refuse: idleArchiveCodeSessionBusy, refuseMsg: "session is working, starting, has background work, queued input, a loop, or running servers"}
	}
	if !offline && (!liveSessionStatusSafe(row) || !liveSessionStatusSafe(raw)) {
		return idleArchiveEvaluation{row: row, refuse: idleArchiveCodeSessionUnverified, refuseMsg: "live session status is unknown or unverified"}
	}

	// 5. Hard-refuse sessions linked to an open to-do (agents, activeBySession, or external Responsible session).
	links := svc.sessionLinks()
	var lastDone time.Time
	for _, id := range candidates {
		open, _, _, doneAt := links.link(mid, id)
		if open {
			return idleArchiveEvaluation{row: row, refuse: idleArchiveCodeOpenTodo, refuseMsg: "session is linked to an open to-do"}
		}
		if doneAt.After(lastDone) {
			lastDone = doneAt
		}
	}
	if svc.sessionHasOpenResponsibleTodo(mid, candidates) {
		return idleArchiveEvaluation{row: row, refuse: idleArchiveCodeOpenTodo, refuseMsg: "session is responsible for an open to-do"}
	}

	// 6. Strictly verify idle duration > idle_minutes (fail closed when UpdatedAt is zero or in the future).
	lastActivity := row.UpdatedAt
	if listed && raw.UpdatedAt.After(lastActivity) {
		lastActivity = raw.UpdatedAt
	}
	if lastActivity.IsZero() {
		return idleArchiveEvaluation{row: row, refuse: idleArchiveCodeIdleUnverified, refuseMsg: "session idle duration cannot be verified because last activity time is missing"}
	}
	if lastDone.After(lastActivity) {
		lastActivity = lastDone
	}
	// Only clamp forward by ArchiveChangedAt for restored (!row.Archived) sessions so a freshly
	// restored session gets a full grace period, while already-archived retries remain idempotent.
	if !row.Archived && row.ArchiveChangedAt != nil && row.ArchiveChangedAt.After(lastActivity) {
		lastActivity = *row.ArchiveChangedAt
	}
	observed := svc.idleArchiveNow().UTC().Sub(lastActivity)
	obsSec := int64(observed.Seconds())
	threshold := time.Duration(idleMinutes) * time.Minute
	if observed <= threshold {
		return idleArchiveEvaluation{
			row:       row,
			refuse:    idleArchiveCodeNotIdleLongEnough,
			refuseMsg: fmt.Sprintf("session has only been idle for %ds; requires strictly more than %d minute(s)", max(obsSec, 0), idleMinutes),
			refuseObs: &obsSec,
		}
	}

	return idleArchiveEvaluation{row: row, raw: raw, listed: listed, offline: offline, obsSec: obsSec}
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
	if err := svc.ensureIdleArchiveAuditLoaded(ctx); err != nil {
		apierr.WriteCode(w, http.StatusServiceUnavailable, "audit_unavailable", errors.New("could not load idle archive audit storage"))
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

	unlock, err := svc.lockIdleArchiveSession(ctx, mid, sid)
	if err != nil {
		apierr.WriteCode(w, http.StatusServiceUnavailable, "request_cancelled", err)
		return
	}
	defer unlock()

	refuse := func(status int, code, msg string, obsSec *int64, closeOutcome string) {
		auditID, persisted := svc.recordIdleArchiveAudit(ctx, assistantwire.IdleArchiveAuditEntry{
			Actor:               actor,
			Extension:           extID,
			MachineID:           wireids.MachineID(mid),
			SessionID:           wireids.SessionID(sid),
			IdleMinutes:         req.IdleMinutes,
			ObservedIdleSeconds: obsSec,
			CloseTerminal:       closeTerminal,
			Outcome:             idleArchiveOutcomeRefused,
			RefusalCode:         code,
			CloseOutcome:        closeOutcome,
			Reason:              req.Reason,
		})
		body := map[string]any{
			"error":           msg,
			"code":            code,
			"audit_persisted": persisted,
		}
		if persisted && auditID != "" {
			body["audit_id"] = auditID
		}
		if closeOutcome != "" {
			body["close_outcome"] = closeOutcome
		}
		apierr.WriteBody(w, status, body)
	}

	ev := svc.evaluateIdleArchiveTarget(ctx, mid, sid, isExt, req.IdleMinutes)
	if ev.storage != nil {
		apierr.WriteCode(w, http.StatusServiceUnavailable, "storage_unavailable", ev.storage)
		return
	}
	if ev.notFound {
		apierr.Write(w, http.StatusNotFound, i18n.Err("hub-deleted_sessions.unknown"))
		return
	}
	if ev.refuse != "" {
		refuse(http.StatusConflict, ev.refuse, ev.refuseMsg, ev.refuseObs, "")
		return
	}

	row := ev.row
	obsSec := ev.obsSec

	// 7. Idempotent already-archived path:
	// - Already archived and offline (whether close_terminal is true or false), OR
	// - Already archived and caller asked for archive-only (close_terminal == false).
	if row.Archived && (ev.offline || !closeTerminal) {
		closeOutcome := ""
		var closeRes *assistantwire.IdleArchiveCloseResult
		if closeTerminal {
			closeOutcome = archiveCloseNotRunning
			closeRes = &assistantwire.IdleArchiveCloseResult{Closed: false, Outcome: archiveCloseNotRunning}
		}
		auditID, persisted := svc.recordIdleArchiveAudit(ctx, assistantwire.IdleArchiveAuditEntry{
			Actor:               actor,
			Extension:           extID,
			MachineID:           wireids.MachineID(mid),
			SessionID:           wireids.SessionID(row.SessionID),
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

	// 8. Perform guarded archive (+ fail-closed terminal close when requested).
	if closeTerminal {
		if ev.offline {
			st, err := svc.stageIdleArchiveRecord(ctx, row, false)
			if err != nil {
				refuse(http.StatusInternalServerError, "archive_failed", err.Error(), &obsSec, "")
				return
			}
			row.ArchiveChangedAt = archiveChangedAt(st.changedAt)
			closeRes := &assistantwire.IdleArchiveCloseResult{Closed: false, Outcome: archiveCloseNotRunning}
			auditID, persisted := svc.recordIdleArchiveAudit(ctx, assistantwire.IdleArchiveAuditEntry{
				Actor:               actor,
				Extension:           extID,
				MachineID:           wireids.MachineID(mid),
				SessionID:           wireids.SessionID(row.SessionID),
				IdleMinutes:         req.IdleMinutes,
				ObservedIdleSeconds: &obsSec,
				CloseTerminal:       true,
				Outcome:             idleArchiveOutcomeArchivedNotRunning,
				CloseOutcome:        archiveCloseNotRunning,
				Reason:              req.Reason,
			})
			if !persisted {
				if rbErr := svc.rollbackIdleArchiveRecord(ctx, st); rbErr != nil {
					svc.logf("idle archive %s/%s: rollback offline archive after audit failure: %v", mid, short(row.SessionID), rbErr)
				}
				svc.publishList(row.MachineID)
				apierr.WriteCode(w, http.StatusServiceUnavailable, "audit_unavailable", errors.New("could not persist idle archive audit entry"))
				return
			}
			svc.nativeArchiveWanted(st.snapshot, true)
			svc.publishList(row.MachineID)
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

		// Live session close: always stage a provisional ClosedAt fence before calling the node
		// (including when the session is already archived so exit-transcript writes cannot unarchive
		// it mid-close), and roll back exact pre-call state if the node refuses, is offline, or fails.
		wasArchived := row.Archived
		st, err := svc.stageIdleArchiveRecord(ctx, row, true)
		if err != nil {
			refuse(http.StatusInternalServerError, "archive_failed", err.Error(), &obsSec, "")
			return
		}
		row.ArchiveChangedAt = archiveChangedAt(st.changedAt)

		res := svc.retireArchived(ctx, row)
		if res.Outcome != archiveCloseClosed && res.Outcome != archiveCloseNotRunning {
			if rbErr := svc.rollbackIdleArchiveRecord(ctx, st); rbErr != nil {
				svc.logf("idle archive %s/%s: rollback provisional archive: %v", mid, short(row.SessionID), rbErr)
			}
			svc.publishList(row.MachineID)
			status := http.StatusBadGateway
			code := idleArchiveCodeCloseFailed
			msg := "could not close idle session terminal"
			switch res.Outcome {
			case archiveCloseRefused:
				status = http.StatusConflict
				code = idleArchiveCodeCloseRefused
				msg = "node refused to close session terminal"
			case archiveCloseOffline:
				status = http.StatusServiceUnavailable
				code = idleArchiveCodeCloseOffline
				msg = "computer went offline before closing session terminal"
			}
			if res.Detail != "" {
				msg = msg + ": " + res.Detail
			}
			refuse(status, code, msg, &obsSec, res.Outcome)
			return
		}

		publishMark, err := svc.noteAgentArchiveClosed(ctx, row, time.Now().UTC(), true)
		if err != nil {
			svc.logf("idle archive %s/%s: note close: %v", row.MachineID, short(row.SessionID), err)
			svc.retryNativeArchiveClosed(ctx, row)
		}
		if publishMark != 0 {
			svc.publishListAtArchiveMark(row.MachineID, publishMark)
		} else {
			svc.publishList(row.MachineID)
		}

		closeRes := &assistantwire.IdleArchiveCloseResult{
			Closed:  res.Closed,
			Outcome: res.Outcome,
			Detail:  res.Detail,
		}
		if res.ReasonKey != "" {
			closeRes.Reason = i18n.T(i18n.OfWriter(w), res.ReasonKey)
		}
		outcome := idleArchiveOutcomeArchivedNotRunning
		if res.Closed {
			outcome = idleArchiveOutcomeArchivedAndClosed
		}
		auditID, persisted := svc.recordIdleArchiveAudit(ctx, assistantwire.IdleArchiveAuditEntry{
			Actor:               actor,
			Extension:           extID,
			MachineID:           wireids.MachineID(mid),
			SessionID:           wireids.SessionID(row.SessionID),
			IdleMinutes:         req.IdleMinutes,
			ObservedIdleSeconds: &obsSec,
			CloseTerminal:       true,
			Outcome:             outcome,
			CloseOutcome:        res.Outcome,
			Reason:              req.Reason,
		})
		apierr.WriteJSON(w, assistantwire.IdleArchiveResponse{
			OK:                  true,
			Archived:            true,
			AlreadyArchived:     wasArchived,
			ObservedIdleSeconds: obsSec,
			Close:               closeRes,
			AuditID:             auditID,
			AuditPersisted:      persisted,
		})
		return
	}

	// Archive-only path (close_terminal: false): stage Persona's archive record under the per-session
	// lock without native-archive/publish side effects, and roll back if audit persistence fails.
	st, err := svc.stageIdleArchiveRecord(ctx, row, false)
	if err != nil {
		refuse(http.StatusInternalServerError, "archive_failed", err.Error(), &obsSec, "")
		return
	}
	row.ArchiveChangedAt = archiveChangedAt(st.changedAt)
	auditID, persisted := svc.recordIdleArchiveAudit(ctx, assistantwire.IdleArchiveAuditEntry{
		Actor:               actor,
		Extension:           extID,
		MachineID:           wireids.MachineID(mid),
		SessionID:           wireids.SessionID(row.SessionID),
		IdleMinutes:         req.IdleMinutes,
		ObservedIdleSeconds: &obsSec,
		CloseTerminal:       false,
		Outcome:             idleArchiveOutcomeArchivedOnly,
		Reason:              req.Reason,
	})
	if !persisted {
		if rbErr := svc.rollbackIdleArchiveRecord(ctx, st); rbErr != nil {
			svc.logf("idle archive %s/%s: rollback archive after audit failure: %v", mid, short(row.SessionID), rbErr)
		}
		svc.publishList(row.MachineID)
		apierr.WriteCode(w, http.StatusServiceUnavailable, "audit_unavailable", errors.New("could not persist idle archive audit entry"))
		return
	}
	svc.nativeArchiveWanted(st.snapshot, true)
	svc.publishList(row.MachineID)
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
