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

	idleArchiveAuditRetryBase = 2 * time.Second
	idleArchiveAuditRetryMax  = 30 * time.Second
)

// Refusal codes returned with HTTP 409 (or 503/502 for close/audit failures) when a guarded idle archive-and-close request is blocked.
const (
	idleArchiveCodeMainConversation         = "protected_main_conversation"
	idleArchiveCodeDirectionLead            = "protected_direction_lead"
	idleArchiveCodeAssistantConversation    = "protected_assistant_conversation"
	idleArchiveCodeUnresolvedInteraction    = "unresolved_interaction"
	idleArchiveCodeSessionBusy              = "session_busy"
	idleArchiveCodeSessionUnverified        = "session_unverified"
	idleArchiveCodeOpenTodo                 = "open_todo_session"
	idleArchiveCodeExternalSession          = "external_session"
	idleArchiveCodeSubagentSession          = "subagent_session"
	idleArchiveCodeIdleUnverified           = "idle_unverified"
	idleArchiveCodeNotIdleLongEnough        = "not_idle_long_enough"
	idleArchiveCodeCloseRefused             = "close_refused"
	idleArchiveCodeCloseOffline             = "close_offline"
	idleArchiveCodeCloseFailed              = "close_failed"
	idleArchiveCodeAuditFinalizationPending = "audit_finalization_pending"
)

// Audit outcome values persisted in sessions.idle_archive_audit and plugin_audit.
const (
	idleArchiveOutcomeCloseAttempted       = "close_attempted"
	idleArchiveOutcomeArchivedAndClosed    = "archived_and_closed"
	idleArchiveOutcomeArchivedNotRunning   = "archived_not_running"
	idleArchiveOutcomeArchivedCloseRefused = "archived_close_refused"
	idleArchiveOutcomeArchivedCloseOffline = "archived_close_offline"
	idleArchiveOutcomeArchivedCloseFailed  = "archived_close_failed"
	idleArchiveOutcomeArchivedOnly         = "archived_only"
	idleArchiveOutcomeAlreadyArchived      = "already_archived"
	idleArchiveOutcomeRefused              = "refused"
)

var errIdleCloseFenceActive = errors.New("guarded idle close in flight")

type extensionAuditWriter interface {
	AddExtensionAudit(ctx context.Context, id, action, actor string, detail json.RawMessage) error
}

type idleArchivePluginAuditDetail struct {
	AuditID             assistantwire.IdleArchiveAuditID `json:"audit_id"`
	MachineID           wireids.MachineID                `json:"machine_id"`
	SessionID           wireids.SessionID                `json:"session_id"`
	IdleMinutes         int                              `json:"idle_minutes"`
	CloseTerminal       bool                             `json:"close_terminal"`
	Outcome             string                           `json:"outcome"`
	ObservedIdleSeconds *int64                           `json:"observed_idle_seconds,omitempty"`
	RefusalCode         string                           `json:"refusal_code,omitempty"`
	CloseOutcome        string                           `json:"close_outcome,omitempty"`
	Reason              string                           `json:"reason,omitempty"`
}

// liveIdleFingerprint captures all live-session attributes whose change indicates activity, state transition, or a new
// node connection. Any change resets the manager's local monotonic idle observation window for that session.
type liveIdleFingerprint struct {
	connNano  int64
	status    string
	state     string
	pane      string
	host      string
	rev       int64
	updatedAt int64
	lastText  string
	activity  string
	actAt     int64
	pendingIx int
	queuedLen int
	bgLen     int
	subRun    int
	wfRun     int
	hasLoop   bool
	portsLen  int
	starting  bool
	woke      bool
}

type idleArchiveLiveObs struct {
	fp          liveIdleFingerprint
	idle        bool
	since       time.Time
	fromConnect bool
}

type idleArchiveAuditBook struct {
	mu             sync.Mutex
	loaded         bool
	entries        []assistantwire.IdleArchiveAuditEntry
	pendingUpdates []assistantwire.IdleArchiveAuditEntry
	inFlight       map[[2]string]chan struct{}
	now            func() time.Time
	retryRunning   bool
	retryWake      chan struct{}
	retryStop      chan struct{}
	retryDone      chan struct{}
	retryInterval  time.Duration

	obsMu   sync.Mutex
	liveObs map[string]map[string]idleArchiveLiveObs
	obsConn map[string]int64

	// fenceMu guards closing, holding active irreversible-close fences keyed by (canonicalMachine, sessionID).
	// Lock order: todos.writing -> fenceMu; directions.mu -> fenceMu. fenceMu is always a leaf lock.
	fenceMu sync.Mutex
	closing map[[2]string]chan struct{}
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

func liveIdleFingerprintOf(connected time.Time, s proto.Session) liveIdleFingerprint {
	subRun := 0
	for _, c := range s.Subagents {
		if c.Running {
			subRun++
		}
	}
	wfRun := 0
	for _, w := range s.Workflows {
		if w.Running {
			wfRun++
		}
	}
	var actAt int64
	if s.ActivityAt != nil {
		actAt = s.ActivityAt.UnixNano()
	}
	return liveIdleFingerprint{
		connNano:  connected.UTC().UnixNano(),
		status:    s.Status,
		state:     s.State,
		pane:      s.PaneID,
		host:      s.Host,
		rev:       s.Rev,
		updatedAt: s.UpdatedAt.UnixNano(),
		lastText:  s.LastText,
		activity:  s.Activity,
		actAt:     actAt,
		pendingIx: s.PendingInteractions,
		queuedLen: len(s.Queued),
		bgLen:     len(s.Background),
		subRun:    subRun,
		wfRun:     wfRun,
		hasLoop:   s.Loop != nil,
		portsLen:  len(s.Ports),
		starting:  s.Starting,
		woke:      s.WokeByTask,
	}
}

// observeIdleSessions updates the manager's local monotonic idle observations for a node's session list on its current
// connection. Any activity version, status, pane, output, or connection change resets the continuous observation window.
func (svc *Service) observeIdleSessions(machine string, list []proto.Session, connected time.Time) {
	mid := svc.canonical(machine)
	now := svc.idleArchiveNow()
	b := &svc.idleArchiveAudit
	b.obsMu.Lock()
	defer b.obsMu.Unlock()
	if connected.IsZero() {
		delete(b.liveObs, mid)
		delete(b.obsConn, mid)
		return
	}
	if b.liveObs == nil {
		b.liveObs = map[string]map[string]idleArchiveLiveObs{}
	}
	if b.obsConn == nil {
		b.obsConn = map[string]int64{}
	}
	connNano := connected.UTC().UnixNano()
	sameConn := b.obsConn[mid] == connNano
	prevMap := b.liveObs[mid]
	nextMap := make(map[string]idleArchiveLiveObs, len(list))
	for i := range list {
		sess := list[i]
		if sess.Status == proto.StatusOffline && sess.PaneID == "" && sess.Host == "" {
			continue
		}
		fp := liveIdleFingerprintOf(connected, sess)
		safeIdle := liveSessionStatusSafe(sess) && !sessionBusyForIdleArchive(sess) && !sessionHasUnresolvedCard(sess)
		if !safeIdle {
			nextMap[sess.SessionID] = idleArchiveLiveObs{fp: fp, idle: false, since: now}
			continue
		}
		if sameConn {
			if prev, ok := prevMap[sess.SessionID]; ok && prev.idle && prev.fp == fp {
				nextMap[sess.SessionID] = prev
				continue
			}
			_, existedOnConn := prevMap[sess.SessionID]
			nextMap[sess.SessionID] = idleArchiveLiveObs{
				fp:          fp,
				idle:        true,
				since:       now,
				fromConnect: !existedOnConn,
			}
			continue
		}
		nextMap[sess.SessionID] = idleArchiveLiveObs{
			fp:          fp,
			idle:        true,
			since:       now,
			fromConnect: true,
		}
	}
	b.liveObs[mid] = nextMap
	b.obsConn[mid] = connNano
}

// resetIdleObservation marks a live session as freshly active on the manager's monotonic clock when a hook event or
// interaction arrives between session list pushes.
func (svc *Service) resetIdleObservation(machine, sid string) {
	if sid == "" {
		return
	}
	mid := svc.canonical(machine)
	now := svc.idleArchiveNow()
	b := &svc.idleArchiveAudit
	b.obsMu.Lock()
	defer b.obsMu.Unlock()
	if m := b.liveObs[mid]; m != nil {
		if obs, ok := m[sid]; ok {
			obs.idle = false
			obs.since = now
			obs.fromConnect = false
			m[sid] = obs
		}
	}
}

func (svc *Service) verifyLiveIdleObservation(mid string, raw proto.Session, now time.Time) (elapsed time.Duration, fromConnect bool, verified bool) {
	var connected time.Time
	if svc.devs != nil {
		connected = svc.devs.ConnectedAt(mid)
	}
	if connected.IsZero() {
		return 0, true, false
	}
	canon := svc.canonical(mid)
	fp := liveIdleFingerprintOf(connected, raw)
	b := &svc.idleArchiveAudit
	b.obsMu.Lock()
	defer b.obsMu.Unlock()
	m := b.liveObs[canon]
	obs, exists := m[raw.SessionID]
	if !exists || !obs.idle || obs.fp != fp {
		if b.liveObs == nil {
			b.liveObs = map[string]map[string]idleArchiveLiveObs{}
		}
		if b.obsConn == nil {
			b.obsConn = map[string]int64{}
		}
		if b.liveObs[canon] == nil || b.obsConn[canon] != fp.connNano {
			b.liveObs[canon] = map[string]idleArchiveLiveObs{}
			b.obsConn[canon] = fp.connNano
		}
		b.liveObs[canon][raw.SessionID] = idleArchiveLiveObs{
			fp:          fp,
			idle:        true,
			since:       now,
			fromConnect: !exists || obs.fp.connNano != fp.connNano,
		}
		return 0, true, false
	}
	elapsed = now.Sub(obs.since)
	if elapsed < 0 {
		elapsed = 0
	}
	return elapsed, obs.fromConnect, true
}

func (svc *Service) idleCloseFenceFor(mid string, sessionIDs ...string) chan struct{} {
	canon := ""
	if mid != "" {
		canon = svc.canonical(mid)
	}
	b := &svc.idleArchiveAudit
	b.fenceMu.Lock()
	defer b.fenceMu.Unlock()
	if len(b.closing) == 0 {
		return nil
	}
	for _, sid := range sessionIDs {
		if sid == "" {
			continue
		}
		if canon != "" {
			if ch := b.closing[[2]string{canon, sid}]; ch != nil {
				return ch
			}
			continue
		}
		for k, ch := range b.closing {
			if k[1] == sid {
				return ch
			}
		}
	}
	return nil
}

func (svc *Service) idleCloseFenceForSessions(sessions [][2]string) chan struct{} {
	if len(sessions) == 0 {
		return nil
	}
	canonPairs := make([][2]string, 0, len(sessions))
	for _, ref := range sessions {
		if ref[1] == "" {
			continue
		}
		mid := ref[0]
		if mid != "" {
			mid = svc.canonical(mid)
		}
		canonPairs = append(canonPairs, [2]string{mid, ref[1]})
	}
	if len(canonPairs) == 0 {
		return nil
	}
	b := &svc.idleArchiveAudit
	b.fenceMu.Lock()
	defer b.fenceMu.Unlock()
	if len(b.closing) == 0 {
		return nil
	}
	for _, ref := range canonPairs {
		if ref[0] != "" {
			if ch := b.closing[ref]; ch != nil {
				return ch
			}
			continue
		}
		for k, ch := range b.closing {
			if k[1] == ref[1] {
				return ch
			}
		}
	}
	return nil
}

func (svc *Service) idleCloseFenceActive(mid, sid string) bool {
	return svc.idleCloseFenceFor(mid, sid) != nil
}

func (svc *Service) waitIdleCloseFence(ctx context.Context, mid string, sessionIDs ...string) error {
	for {
		ch := svc.idleCloseFenceFor(mid, sessionIDs...)
		if ch == nil {
			return nil
		}
		select {
		case <-ch:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (svc *Service) lockTodoWritingWithIdleFence(ctx context.Context, mid string, sessionIDs ...string) error {
	for {
		svc.todos.writing.Lock()
		if ch := svc.idleCloseFenceFor(mid, sessionIDs...); ch != nil {
			svc.todos.writing.Unlock()
			select {
			case <-ch:
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}
}

func (svc *Service) lockTodoWritingForSessions(ctx context.Context, sessions [][2]string) error {
	for {
		svc.todos.writing.Lock()
		if ch := svc.idleCloseFenceForSessions(sessions); ch != nil {
			svc.todos.writing.Unlock()
			select {
			case <-ch:
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}
}

func todoProtectedSessions(t Todo, nextResp *db.TodoResponsible) [][2]string {
	var out [][2]string
	for _, a := range t.Agents {
		if a.ReleasedAt == nil && a.DismissedAt == nil && a.SessionID != "" {
			out = append(out, [2]string{a.MachineID, a.SessionID})
		}
	}
	if t.Responsible != nil && t.Responsible.Kind == db.ResponsibleSession && t.Responsible.SessionID != "" {
		out = append(out, [2]string{t.Responsible.MachineID, t.Responsible.SessionID})
	}
	if nextResp != nil && nextResp.Kind == db.ResponsibleSession && nextResp.SessionID != "" {
		out = append(out, [2]string{nextResp.MachineID, nextResp.SessionID})
	}
	return out
}

// acquireIdleCloseFenceLocked verifies no open to-do (active agent or responsible session) holds any candidate ID while
// svc.todos.writing is already held, and installs the shared close fence before the caller releases todos.writing.
func (svc *Service) acquireIdleCloseFenceLocked(mid string, candidates []string) (func(), string, bool) {
	links := svc.sessionLinks()
	for _, id := range candidates {
		open, _, _, _ := links.link(mid, id)
		if open {
			return nil, "session is linked to an open to-do", false
		}
	}
	if svc.sessionHasOpenResponsibleTodo(mid, candidates) {
		return nil, "session is responsible for an open to-do", false
	}
	canon := svc.canonical(mid)
	keys := make([][2]string, 0, len(candidates))
	for _, id := range candidates {
		if id != "" {
			keys = append(keys, [2]string{canon, id})
		}
	}
	done := make(chan struct{})
	b := &svc.idleArchiveAudit
	b.fenceMu.Lock()
	if b.closing == nil {
		b.closing = map[[2]string]chan struct{}{}
	}
	for _, k := range keys {
		b.closing[k] = done
	}
	b.fenceMu.Unlock()

	var once sync.Once
	release := func() {
		once.Do(func() {
			b.fenceMu.Lock()
			for _, k := range keys {
				if b.closing[k] == done {
					delete(b.closing, k)
				}
			}
			b.fenceMu.Unlock()
			close(done)
		})
	}
	return release, "", true
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

func writeIdleArchiveStatusJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
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

func mergeIdleArchiveAuditEntries(base []assistantwire.IdleArchiveAuditEntry, pending []assistantwire.IdleArchiveAuditEntry, upsert *assistantwire.IdleArchiveAuditEntry) []assistantwire.IdleArchiveAuditEntry {
	next := make([]assistantwire.IdleArchiveAuditEntry, 0, min(len(base)+len(pending)+1, idleArchiveAuditMaxEntries))
	next = append(next, base...)
	apply := func(e assistantwire.IdleArchiveAuditEntry) {
		for i := range next {
			if next[i].ID == e.ID {
				next[i] = e
				return
			}
		}
		if len(next) >= idleArchiveAuditMaxEntries {
			next = append([]assistantwire.IdleArchiveAuditEntry{e}, next[:idleArchiveAuditMaxEntries-1]...)
		} else {
			next = append([]assistantwire.IdleArchiveAuditEntry{e}, next...)
		}
	}
	for _, p := range pending {
		apply(p)
	}
	if upsert != nil {
		apply(*upsert)
	}
	if len(next) > idleArchiveAuditMaxEntries {
		next = next[:idleArchiveAuditMaxEntries]
	}
	return next
}

func (svc *Service) mirrorIdleArchivePluginAudit(ctx context.Context, entry assistantwire.IdleArchiveAuditEntry) {
	store := svc.store()
	extAudit, ok := store.(extensionAuditWriter)
	if !ok {
		return
	}
	plugin := entry.Extension
	if plugin == "" {
		plugin = idleArchiveAuditCorePlugin
	}
	action := idleArchiveAuditActionSuccess
	if entry.Outcome == idleArchiveOutcomeRefused {
		action = idleArchiveAuditActionRefused
	}
	detail := idleArchivePluginAuditDetail{
		AuditID:             entry.ID,
		MachineID:           entry.MachineID,
		SessionID:           entry.SessionID,
		IdleMinutes:         entry.IdleMinutes,
		CloseTerminal:       entry.CloseTerminal,
		Outcome:             entry.Outcome,
		ObservedIdleSeconds: entry.ObservedIdleSeconds,
		RefusalCode:         entry.RefusalCode,
		CloseOutcome:        entry.CloseOutcome,
		Reason:              entry.Reason,
	}
	if raw, err := json.Marshal(detail); err == nil {
		if err := extAudit.AddExtensionAudit(ctx, plugin, action, entry.Actor, raw); err != nil {
			svc.logf("idle archive plugin_audit: %v", err)
		}
	}
}

// persistIdleArchiveAuditEntry writes entry (appending new or updating an existing ID) to sessions.idle_archive_audit
// alongside any queued retry finalizations. When mirrorPlugin is true, entry is also mirrored to plugin_audit on success.
func (svc *Service) persistIdleArchiveAuditEntry(ctx context.Context, entry assistantwire.IdleArchiveAuditEntry, mirrorPlugin bool) (assistantwire.IdleArchiveAuditID, bool) {
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

	var flushed []assistantwire.IdleArchiveAuditEntry
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
		pendingSnap := slices.Clone(svc.idleArchiveAudit.pendingUpdates)
		next := mergeIdleArchiveAuditEntries(svc.idleArchiveAudit.entries, pendingSnap, &entry)
		if err := store.PutSetting(writeCtx, idleArchiveAuditKey, next); err != nil {
			svc.logf("idle archive audit save: %v", err)
		} else {
			svc.idleArchiveAudit.entries = next
			svc.idleArchiveAudit.pendingUpdates = nil
			for _, p := range pendingSnap {
				if p.ID != entry.ID {
					flushed = append(flushed, p)
				}
			}
			persisted = true
		}
	}
	svc.idleArchiveAudit.mu.Unlock()

	if !persisted {
		return "", false
	}
	for _, p := range flushed {
		svc.mirrorIdleArchivePluginAudit(writeCtx, p)
	}
	if mirrorPlugin {
		svc.mirrorIdleArchivePluginAudit(writeCtx, entry)
	}
	return entry.ID, true
}

// recordIdleArchiveAudit durably appends entry to sessions.idle_archive_audit (bounded to 500
// newest entries) and mirrors metadata to plugin_audit when supported. On persistence failure
// it returns ("", false) so callers never expose a dangling audit_id absent from storage.
func (svc *Service) recordIdleArchiveAudit(ctx context.Context, entry assistantwire.IdleArchiveAuditEntry) (assistantwire.IdleArchiveAuditID, bool) {
	return svc.persistIdleArchiveAuditEntry(ctx, entry, true)
}

// recordIdleArchiveAuditAttempt durably writes the pre-close write-ahead attempt record to sessions.idle_archive_audit
// before any irreversible terminal close. Finalization updates this record in place and writes the plugin_audit row.
func (svc *Service) recordIdleArchiveAuditAttempt(ctx context.Context, entry assistantwire.IdleArchiveAuditEntry) (assistantwire.IdleArchiveAuditID, bool) {
	return svc.persistIdleArchiveAuditEntry(ctx, entry, false)
}

// finalizeIdleArchiveAudit updates an already-persisted close attempt record in place with the final outcome and
// mirrors the finalized record to plugin_audit.
func (svc *Service) finalizeIdleArchiveAudit(ctx context.Context, entry assistantwire.IdleArchiveAuditEntry) bool {
	_, ok := svc.persistIdleArchiveAuditEntry(ctx, entry, true)
	return ok
}

func (svc *Service) enqueueIdleArchiveAuditRetry(entry assistantwire.IdleArchiveAuditEntry) {
	b := &svc.idleArchiveAudit
	b.mu.Lock()
	replaced := false
	for i := range b.pendingUpdates {
		if b.pendingUpdates[i].ID == entry.ID {
			b.pendingUpdates[i] = entry
			replaced = true
			break
		}
	}
	if !replaced {
		if len(b.pendingUpdates) >= idleArchiveAuditMaxEntries {
			b.pendingUpdates = append(b.pendingUpdates[1:], entry)
		} else {
			b.pendingUpdates = append(b.pendingUpdates, entry)
		}
	}
	if b.retryWake == nil {
		b.retryWake = make(chan struct{}, 1)
	}
	select {
	case b.retryWake <- struct{}{}:
	default:
	}
	if !b.retryRunning {
		b.retryRunning = true
		b.retryStop = make(chan struct{})
		b.retryDone = make(chan struct{})
		stopCh, doneCh := b.retryStop, b.retryDone
		b.mu.Unlock()
		go svc.runIdleArchiveAuditRetry(stopCh, doneCh)
		return
	}
	b.mu.Unlock()
}

func (svc *Service) stopIdleArchiveAuditRetry() {
	b := &svc.idleArchiveAudit
	b.mu.Lock()
	stopCh := b.retryStop
	doneCh := b.retryDone
	running := b.retryRunning
	if running && stopCh != nil {
		b.retryStop = nil
		close(stopCh)
	}
	b.mu.Unlock()
	if running && doneCh != nil {
		<-doneCh
	}
}

func (svc *Service) flushIdleArchiveAuditRetries(ctx context.Context) bool {
	store := svc.store()
	if store == nil {
		return false
	}
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), agentArchiveTimeout)
	defer cancel()

	b := &svc.idleArchiveAudit
	b.mu.Lock()
	if len(b.pendingUpdates) == 0 {
		b.mu.Unlock()
		return true
	}
	if !b.loaded {
		var saved []assistantwire.IdleArchiveAuditEntry
		if _, err := store.Setting(writeCtx, idleArchiveAuditKey, &saved); err != nil {
			b.mu.Unlock()
			svc.logf("idle archive audit retry load: %v", err)
			return false
		}
		b.entries = saved
		b.loaded = true
	}
	pendingSnap := slices.Clone(b.pendingUpdates)
	next := mergeIdleArchiveAuditEntries(b.entries, pendingSnap, nil)
	if err := store.PutSetting(writeCtx, idleArchiveAuditKey, next); err != nil {
		b.mu.Unlock()
		svc.logf("idle archive audit retry save: %v", err)
		return false
	}
	b.entries = next
	b.pendingUpdates = nil
	b.mu.Unlock()

	for _, p := range pendingSnap {
		svc.mirrorIdleArchivePluginAudit(writeCtx, p)
	}
	return true
}

func (svc *Service) runIdleArchiveAuditRetry(stopCh, doneCh chan struct{}) {
	defer close(doneCh)
	b := &svc.idleArchiveAudit
	b.mu.Lock()
	interval := b.retryInterval
	wake := b.retryWake
	b.mu.Unlock()
	if interval <= 0 {
		interval = idleArchiveAuditRetryBase
	}
	wait := interval
	timer := time.NewTimer(wait)
	defer timer.Stop()
	var runDone <-chan struct{}
	if ptr := svc.running.Load(); ptr != nil {
		runDone = (*ptr).Done()
	}
	for {
		select {
		case <-stopCh:
			b.mu.Lock()
			b.retryRunning = false
			b.mu.Unlock()
			return
		case <-runDone:
			b.mu.Lock()
			b.retryRunning = false
			b.mu.Unlock()
			return
		case <-wake:
			wait = interval
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(wait)
		case <-timer.C:
			if svc.flushIdleArchiveAuditRetries(context.Background()) {
				b.mu.Lock()
				if len(b.pendingUpdates) == 0 {
					b.retryRunning = false
					b.mu.Unlock()
					return
				}
				b.mu.Unlock()
				wait = interval
			} else if wait < idleArchiveAuditRetryMax {
				wait = min(wait*2, idleArchiveAuditRetryMax)
			}
			timer.Reset(wait)
		}
	}
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

func (svc *Service) idleArchiveStageCurrent(ctx context.Context, st idleArchiveStage) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, agentArchiveTimeout)
	defer cancel()
	b := &svc.agentArchives
	if err := b.lock(ctx); err != nil {
		return false, err
	}
	defer b.unlock()
	if err := svc.loadAgentArchivesLocked(ctx); err != nil {
		return false, err
	}
	rec, ok := b.records[st.key]
	return ok && rec.Archived && rec.ChangedAt.Equal(st.changedAt), nil
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
	svc.flushIdleArchiveAuditRetries(ctx)
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

	// 6. Strictly verify idle duration > idle_minutes.
	// Live sessions additionally require manager-local monotonic continuous inactivity observation on the current node
	// connection so cross-machine wall-clock skew or freshly established connections cannot forge long inactivity.
	lastActivity := row.UpdatedAt
	if listed && raw.UpdatedAt.After(lastActivity) {
		lastActivity = raw.UpdatedAt
	}
	if lastActivity.IsZero() {
		return idleArchiveEvaluation{row: row, refuse: idleArchiveCodeIdleUnverified, refuseMsg: "session idle duration cannot be verified because last activity time is missing"}
	}
	localReset := lastDone
	// Only clamp forward by ArchiveChangedAt for restored (!row.Archived) sessions so a freshly
	// restored session gets a full grace period, while already-archived retries remain idempotent.
	if !row.Archived && row.ArchiveChangedAt != nil && row.ArchiveChangedAt.After(localReset) {
		localReset = *row.ArchiveChangedAt
	}
	if localReset.After(lastActivity) {
		lastActivity = localReset
	}

	now := svc.idleArchiveNow()
	wallObserved := now.UTC().Sub(lastActivity)
	threshold := time.Duration(idleMinutes) * time.Minute

	if offline {
		obsSec := int64(wallObserved.Seconds())
		if wallObserved <= threshold {
			return idleArchiveEvaluation{
				row:       row,
				refuse:    idleArchiveCodeNotIdleLongEnough,
				refuseMsg: fmt.Sprintf("session has only been idle for %ds; requires strictly more than %d minute(s)", max(obsSec, 0), idleMinutes),
				refuseObs: &obsSec,
			}
		}
		return idleArchiveEvaluation{row: row, raw: raw, listed: listed, offline: true, obsSec: obsSec}
	}

	localObs, fromConnect, verified := svc.verifyLiveIdleObservation(mid, raw, now)
	if !verified {
		if wallObserved <= threshold {
			obsSec := int64(wallObserved.Seconds())
			return idleArchiveEvaluation{
				row:       row,
				refuse:    idleArchiveCodeNotIdleLongEnough,
				refuseMsg: fmt.Sprintf("session has only been idle for %ds; requires strictly more than %d minute(s)", max(obsSec, 0), idleMinutes),
				refuseObs: &obsSec,
			}
		}
		zero := int64(0)
		return idleArchiveEvaluation{
			row:       row,
			refuse:    idleArchiveCodeIdleUnverified,
			refuseMsg: fmt.Sprintf("live session inactivity has not been continuously observed on the current computer connection; requires strictly more than %d minute(s) of local observation", idleMinutes),
			refuseObs: &zero,
		}
	}
	if !localReset.IsZero() {
		if sinceLocalReset := now.UTC().Sub(localReset); sinceLocalReset < localObs {
			localObs = sinceLocalReset
		}
	}
	effective := min(localObs, wallObserved)
	obsSec := int64(effective.Seconds())
	if effective <= threshold {
		if localObs <= threshold && wallObserved > threshold && fromConnect {
			return idleArchiveEvaluation{
				row:       row,
				refuse:    idleArchiveCodeIdleUnverified,
				refuseMsg: fmt.Sprintf("live session has only been observed idle on the current computer connection for %ds; requires strictly more than %d minute(s) of continuous local observation", max(obsSec, 0), idleMinutes),
				refuseObs: &obsSec,
			}
		}
		return idleArchiveEvaluation{
			row:       row,
			refuse:    idleArchiveCodeNotIdleLongEnough,
			refuseMsg: fmt.Sprintf("session has only been idle for %ds; requires strictly more than %d minute(s)", max(obsSec, 0), idleMinutes),
			refuseObs: &obsSec,
		}
	}

	return idleArchiveEvaluation{row: row, raw: raw, listed: listed, offline: false, obsSec: obsSec}
}

func (svc *Service) reverifyBeforeIrreversibleClose(ctx context.Context, mid, sid string, isExt bool, idleMinutes int, st idleArchiveStage) (idleArchiveEvaluation, func()) {
	ev := svc.evaluateIdleArchiveTarget(ctx, mid, sid, isExt, idleMinutes)
	if ev.storage != nil || ev.notFound || ev.refuse != "" {
		return ev, nil
	}
	current, err := svc.idleArchiveStageCurrent(ctx, st)
	if err != nil {
		return idleArchiveEvaluation{storage: err}, nil
	}
	if !current {
		return idleArchiveEvaluation{row: ev.row, refuse: idleArchiveCodeSessionUnverified, refuseMsg: "session archive state changed before closing terminal"}, nil
	}
	if ev.offline {
		return ev, nil
	}
	candidates := sessionCandidateIDs(sid, ev.row, true, ev.raw, ev.listed)
	svc.todos.writing.Lock()
	releaseFence, openMsg, ok := svc.acquireIdleCloseFenceLocked(mid, candidates)
	svc.todos.writing.Unlock()
	if !ok {
		return idleArchiveEvaluation{row: ev.row, refuse: idleArchiveCodeOpenTodo, refuseMsg: openMsg}, nil
	}
	// Re-check all protection, direction, interaction, and monotonic idle conditions with the close fence held.
	ev2 := svc.evaluateIdleArchiveTarget(ctx, mid, sid, isExt, idleMinutes)
	if ev2.storage != nil || ev2.notFound || ev2.refuse != "" {
		releaseFence()
		return ev2, nil
	}
	current, err = svc.idleArchiveStageCurrent(ctx, st)
	if err != nil {
		releaseFence()
		return idleArchiveEvaluation{storage: err}, nil
	}
	if !current {
		releaseFence()
		return idleArchiveEvaluation{row: ev2.row, refuse: idleArchiveCodeSessionUnverified, refuseMsg: "session archive state changed before closing terminal"}, nil
	}
	return ev2, releaseFence
}

func (svc *Service) reverifyBeforeArchiveOnlyCommit(ctx context.Context, mid, sid string, isExt bool, idleMinutes int, st idleArchiveStage) idleArchiveEvaluation {
	ev := svc.evaluateIdleArchiveTarget(ctx, mid, sid, isExt, idleMinutes)
	if ev.storage != nil || ev.notFound || ev.refuse != "" {
		return ev
	}
	candidates := sessionCandidateIDs(sid, ev.row, true, ev.raw, ev.listed)
	svc.todos.writing.Lock()
	links := svc.sessionLinks()
	openTodo := false
	openMsg := ""
	for _, id := range candidates {
		if open, _, _, _ := links.link(mid, id); open {
			openTodo = true
			openMsg = "session is linked to an open to-do"
			break
		}
	}
	if !openTodo && svc.sessionHasOpenResponsibleTodo(mid, candidates) {
		openTodo = true
		openMsg = "session is responsible for an open to-do"
	}
	svc.todos.writing.Unlock()
	if openTodo {
		return idleArchiveEvaluation{row: ev.row, refuse: idleArchiveCodeOpenTodo, refuseMsg: openMsg}
	}
	current, err := svc.idleArchiveStageCurrent(ctx, st)
	if err != nil {
		return idleArchiveEvaluation{storage: err}
	}
	if !current {
		return idleArchiveEvaluation{row: ev.row, refuse: idleArchiveCodeSessionUnverified, refuseMsg: "session archive state changed before committing archive"}
	}
	return ev
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
	closeTerminal := req.CloseTerminal.ShouldClose()

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
		resp := assistantwire.IdleArchiveRefusalResponse{
			Message:        msg,
			Code:           code,
			AuditPersisted: persisted,
			CloseOutcome:   closeOutcome,
		}
		if persisted && auditID != "" {
			resp.AuditID = auditID
		}
		writeIdleArchiveStatusJSON(w, status, resp)
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
			reEv := svc.reverifyBeforeArchiveOnlyCommit(ctx, mid, sid, isExt, req.IdleMinutes, st)
			if reEv.storage != nil || reEv.notFound || reEv.refuse != "" {
				if rbErr := svc.rollbackIdleArchiveRecord(ctx, st); rbErr != nil {
					svc.logf("idle archive %s/%s: rollback offline archive after reverify failure: %v", mid, short(row.SessionID), rbErr)
				}
				svc.publishList(row.MachineID)
				if reEv.storage != nil {
					apierr.WriteCode(w, http.StatusServiceUnavailable, "storage_unavailable", reEv.storage)
				} else if reEv.notFound {
					apierr.Write(w, http.StatusNotFound, i18n.Err("hub-deleted_sessions.unknown"))
				} else {
					refuse(http.StatusConflict, reEv.refuse, reEv.refuseMsg, reEv.refuseObs, "")
				}
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

		// Live session close:
		// 1. Stage provisional ClosedAt fence before calling the node (so exit-transcript writes cannot unarchive mid-close).
		// 2. Re-verify open to-dos and all protection/idle guards under the shared session close fence before any close.
		// 3. Durably persist the write-ahead attempt audit record BEFORE calling the node; abort & rollback if write fails.
		// 4. Call node retire with activity version/pane guard so concurrent node activity is refused cleanly.
		// 5. Finalize the audit entry with the terminal outcome; if post-close update fails, queue durable retry and return 503.
		wasArchived := row.Archived
		st, err := svc.stageIdleArchiveRecord(ctx, row, true)
		if err != nil {
			refuse(http.StatusInternalServerError, "archive_failed", err.Error(), &obsSec, "")
			return
		}
		row.ArchiveChangedAt = archiveChangedAt(st.changedAt)

		reEv, releaseFence := svc.reverifyBeforeIrreversibleClose(ctx, mid, sid, isExt, req.IdleMinutes, st)
		if reEv.storage != nil || reEv.notFound || reEv.refuse != "" {
			if rbErr := svc.rollbackIdleArchiveRecord(ctx, st); rbErr != nil {
				svc.logf("idle archive %s/%s: rollback provisional archive after reverify: %v", mid, short(row.SessionID), rbErr)
			}
			svc.publishList(row.MachineID)
			if reEv.storage != nil {
				apierr.WriteCode(w, http.StatusServiceUnavailable, "storage_unavailable", reEv.storage)
			} else if reEv.notFound {
				apierr.Write(w, http.StatusNotFound, i18n.Err("hub-deleted_sessions.unknown"))
			} else {
				refuse(http.StatusConflict, reEv.refuse, reEv.refuseMsg, reEv.refuseObs, "")
			}
			return
		}
		obsSec = reEv.obsSec

		// If the session went offline while staging the archive record, commit as offline without calling the node.
		if reEv.offline {
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
					svc.logf("idle archive %s/%s: rollback archive after audit failure: %v", mid, short(row.SessionID), rbErr)
				}
				svc.publishList(row.MachineID)
				apierr.WriteCode(w, http.StatusServiceUnavailable, "audit_unavailable", errors.New("could not persist idle archive audit entry"))
				return
			}
			if _, err := svc.noteAgentArchiveClosed(ctx, row, time.Now().UTC(), true); err != nil {
				svc.logf("idle archive %s/%s: note offline close: %v", row.MachineID, short(row.SessionID), err)
				svc.retryNativeArchiveClosed(ctx, row)
			}
			svc.publishList(row.MachineID)
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
		defer releaseFence()

		attemptEntry := assistantwire.IdleArchiveAuditEntry{
			ID:                  assistantwire.IdleArchiveAuditID("ia_" + randomHex(8)),
			At:                  svc.idleArchiveNow().UTC(),
			Actor:               actor,
			Extension:           extID,
			MachineID:           wireids.MachineID(mid),
			SessionID:           wireids.SessionID(row.SessionID),
			IdleMinutes:         req.IdleMinutes,
			ObservedIdleSeconds: &obsSec,
			CloseTerminal:       true,
			Outcome:             idleArchiveOutcomeCloseAttempted,
			Reason:              req.Reason,
		}
		auditID, attemptPersisted := svc.recordIdleArchiveAuditAttempt(ctx, attemptEntry)
		if !attemptPersisted {
			if rbErr := svc.rollbackIdleArchiveRecord(ctx, st); rbErr != nil {
				svc.logf("idle archive %s/%s: rollback provisional archive after attempt audit failure: %v", mid, short(row.SessionID), rbErr)
			}
			svc.publishList(row.MachineID)
			apierr.WriteCode(w, http.StatusServiceUnavailable, "audit_unavailable", errors.New("could not persist idle archive audit attempt before closing terminal"))
			return
		}
		attemptEntry.ID = auditID

		guard := retireActivityGuard{
			Enabled:   true,
			Rev:       reEv.raw.Rev,
			HasRev:    true,
			UpdatedAt: reEv.raw.UpdatedAt.UTC(),
			Pane:      reEv.raw.PaneID,
		}
		res := svc.retireArchived(ctx, row, guard)
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
			finalRefusal := attemptEntry
			finalRefusal.Outcome = idleArchiveOutcomeRefused
			finalRefusal.RefusalCode = code
			finalRefusal.CloseOutcome = res.Outcome
			finalPersisted := svc.finalizeIdleArchiveAudit(ctx, finalRefusal)
			if !finalPersisted {
				svc.enqueueIdleArchiveAuditRetry(finalRefusal)
			}
			writeIdleArchiveStatusJSON(w, status, assistantwire.IdleArchiveRefusalResponse{
				Message:        msg,
				Code:           code,
				AuditPersisted: finalPersisted,
				AuditID:        auditID,
				CloseOutcome:   res.Outcome,
			})
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
		finalEntry := attemptEntry
		finalEntry.Outcome = outcome
		finalEntry.CloseOutcome = res.Outcome
		finalPersisted := svc.finalizeIdleArchiveAudit(ctx, finalEntry)
		if !finalPersisted {
			svc.enqueueIdleArchiveAuditRetry(finalEntry)
			writeIdleArchiveStatusJSON(w, http.StatusServiceUnavailable, assistantwire.IdleArchiveResponse{
				OK:                       false,
				Code:                     idleArchiveCodeAuditFinalizationPending,
				Message:                  "session was archived and terminal closed, but final audit outcome could not be persisted; durable retry queued",
				Archived:                 true,
				AlreadyArchived:          wasArchived,
				ObservedIdleSeconds:      obsSec,
				Close:                    closeRes,
				AuditID:                  auditID,
				AuditPersisted:           false,
				AuditFinalizationPending: true,
			})
			return
		}
		apierr.WriteJSON(w, assistantwire.IdleArchiveResponse{
			OK:                  true,
			Archived:            true,
			AlreadyArchived:     wasArchived,
			ObservedIdleSeconds: obsSec,
			Close:               closeRes,
			AuditID:             auditID,
			AuditPersisted:      true,
		})
		return
	}

	// Archive-only path (close_terminal: false): stage Persona's archive record under the per-session
	// lock without native-archive/publish side effects, re-verify protected state, and roll back if audit persistence fails.
	st, err := svc.stageIdleArchiveRecord(ctx, row, false)
	if err != nil {
		refuse(http.StatusInternalServerError, "archive_failed", err.Error(), &obsSec, "")
		return
	}
	reEv := svc.reverifyBeforeArchiveOnlyCommit(ctx, mid, sid, isExt, req.IdleMinutes, st)
	if reEv.storage != nil || reEv.notFound || reEv.refuse != "" {
		if rbErr := svc.rollbackIdleArchiveRecord(ctx, st); rbErr != nil {
			svc.logf("idle archive %s/%s: rollback archive after reverify failure: %v", mid, short(row.SessionID), rbErr)
		}
		svc.publishList(row.MachineID)
		if reEv.storage != nil {
			apierr.WriteCode(w, http.StatusServiceUnavailable, "storage_unavailable", reEv.storage)
		} else if reEv.notFound {
			apierr.Write(w, http.StatusNotFound, i18n.Err("hub-deleted_sessions.unknown"))
		} else {
			refuse(http.StatusConflict, reEv.refuse, reEv.refuseMsg, reEv.refuseObs, "")
		}
		return
	}
	row.ArchiveChangedAt = archiveChangedAt(st.changedAt)
	obsSec = reEv.obsSec
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
	apierr.WriteJSON(w, assistantwire.IdleArchiveAuditResponse{Entries: out})
}
