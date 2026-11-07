package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

const agentArchivesKey = "sessions.archived"
const agentArchiveTimeout = 3 * time.Second

type agentArchive struct {
	Session   proto.Session `json:"session"`
	Archived  bool          `json:"archived"`
	ChangedAt time.Time     `json:"changed_at"`
}

type agentArchiveBook struct {
	once     sync.Once
	gate     chan struct{}
	snapshot atomic.Pointer[map[string]agentArchive]
	loaded   bool
	records  map[string]agentArchive
	// Pending automatic restores survive subsequent idle snapshots if a database
	// write fails. They are retried; they are never reported as persisted success.
	pending map[string]bool
}

// Archive mutations serialize through a cancellable gate. Readers of published
// visibility metadata do not wait for a database operation holding the gate.
func (b *agentArchiveBook) lock(ctx context.Context) error {
	b.once.Do(func() { b.gate = make(chan struct{}, 1) })
	select {
	case b.gate <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-b.gate
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (b *agentArchiveBook) unlock() { <-b.gate }

func (svc *Service) loadAgentArchivesLocked(ctx context.Context) error {
	b := &svc.agentArchives
	if b.loaded {
		return nil
	}
	store := svc.archiveStore()
	if store == nil {
		return errors.New("no database")
	}
	records := map[string]agentArchive{}
	if _, err := store.Setting(ctx, agentArchivesKey, &records); err != nil {
		return err
	}
	if records == nil {
		records = map[string]agentArchive{}
	}
	b.records, b.pending, b.loaded = records, map[string]bool{}, true
	b.snapshot.Store(&records)
	return nil
}

func (svc *Service) saveAgentArchivesLocked(ctx context.Context, records map[string]agentArchive) error {
	store := svc.archiveStore()
	if store == nil {
		return errors.New("no database")
	}
	if err := store.PutSetting(ctx, agentArchivesKey, records); err != nil {
		return err
	}
	svc.agentArchives.records = records
	svc.agentArchives.snapshot.Store(&records)
	return nil
}

func agentArchiveBusy(s proto.Session) bool {
	for _, child := range s.Subagents {
		if child.Running {
			return true
		}
	}
	for _, workflow := range s.Workflows {
		if workflow.Running {
			return true
		}
	}
	return s.Starting || s.Status == proto.StatusWorking || s.Status == proto.StatusBlocked ||
		s.State == proto.StateWorking || s.State == proto.StateNeedsYou || s.BackgroundWorking ||
		s.BackgroundWaiting || s.AwaitsReply || s.PendingInteractions > 0 || len(s.Background) > 0
}

// archiveSnapshot contains identity and display metadata, not stale runtime
// controls. The source transcript remains on its computer and is read by ID.
func archiveSnapshot(s proto.Session) proto.Session {
	preview := []rune(s.LastText)
	if len(preview) > 180 {
		preview = preview[:180]
	}
	return proto.Session{
		MachineID: s.MachineID, SessionID: s.SessionID, Title: s.Title,
		Cwd: s.Cwd, WorkspaceCwd: s.WorkspaceCwd, RepoRoot: s.RepoRoot,
		Agent: s.Agent, AgentLabel: s.AgentLabel, AgentName: s.AgentName,
		SourceKind: s.SourceKind, SourceThreadID: s.SourceThreadID, SourceInstanceID: s.SourceInstanceID,
		ParentThreadID: s.ParentThreadID, ParentSessionID: s.ParentSessionID, RootSessionID: s.RootSessionID, IsSubagent: s.IsSubagent,
		UpdatedAt: s.UpdatedAt, LastText: string(preview),
		Status: proto.StatusOffline, State: proto.StateOffline,
		Capabilities: &proto.SessionCapabilities{Read: true},
	}
}

// archivedSessions overlays the manager's durable visibility on the current
// inventory and retains old archived/restored rows beyond its seven-day window.
// Observed new activity permanently restores a row, including a busy-to-idle
// transition with no timestamp change. Only that transition writes the database.
func (svc *Service) archivedSessions(ctx context.Context, machine string, list []proto.Session) ([]proto.Session, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, agentArchiveTimeout)
	defer cancel()
	b := &svc.agentArchives
	if err := b.lock(ctx); err != nil {
		if records := b.snapshot.Load(); records != nil {
			list = overlayAgentArchives(machine, list, *records, nil)
		}
		return list, false, err
	}
	defer b.unlock()
	if err := svc.loadAgentArchivesLocked(ctx); err != nil {
		return list, false, err
	}
	if len(b.records) == 0 {
		return list, false, nil
	}
	var next map[string]agentArchive
	for _, s := range list {
		key := runtimeListID(machine, s.SessionID)
		rec, ok := b.records[key]
		if !ok || !rec.Archived {
			continue
		}
		if agentArchiveBusy(s) || s.UpdatedAt.After(rec.Session.UpdatedAt) {
			b.pending[key] = true
		}
		if b.pending[key] {
			rec.Archived, rec.ChangedAt = false, time.Now().UTC()
			rec.Session = archiveSnapshot(s)
			rec.Session.MachineID = machine
			if next == nil {
				next = maps.Clone(b.records)
			}
			next[key] = rec
		}
	}
	var saveErr error
	if next != nil {
		saveErr = svc.saveAgentArchivesLocked(ctx, next)
		if saveErr == nil {
			for key := range b.pending {
				if rec, ok := next[key]; ok && !rec.Archived {
					delete(b.pending, key)
				}
			}
		}
	}
	return overlayAgentArchives(machine, list, b.records, b.pending), next != nil && saveErr == nil, saveErr
}

func overlayAgentArchives(machine string, list []proto.Session, records map[string]agentArchive, pending map[string]bool) []proto.Session {
	seen := make(map[string]bool, len(list))
	for i := range list {
		key := runtimeListID(machine, list[i].SessionID)
		seen[key] = true
		if rec, ok := records[key]; ok && !list[i].Assistant {
			// A storage timeout must neither reveal unrelated archived rows nor hide
			// new work. The durable restore remains pending until its write succeeds.
			active := agentArchiveBusy(list[i]) || list[i].UpdatedAt.After(rec.Session.UpdatedAt)
			list[i].Archived = rec.Archived && !pending[key] && !active
			list[i].ArchiveChangedAt = rec.ChangedAt
		}
	}
	keys := make([]string, 0, len(records))
	for key, rec := range records {
		if !seen[key] && rec.Session.MachineID == machine {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys) // stable inventories avoid needless delta/list revisions
	for _, key := range keys {
		rec := records[key]
		s := rec.Session
		s.Archived = rec.Archived
		s.ArchiveChangedAt = rec.ChangedAt
		list = append(list, s)
	}
	return list
}

// A later archive/restore supersedes an older delete tombstone. A subsequent
// explicit delete still works, without a non-atomic write to two settings keys.
func (svc *Service) agentArchiveChanges(machine string) map[string]time.Time {
	out := map[string]time.Time{}
	records := svc.agentArchives.snapshot.Load()
	if records == nil {
		return out
	}
	for _, rec := range *records {
		if rec.Session.MachineID == machine {
			out[rec.Session.SessionID] = rec.ChangedAt
		}
	}
	return out
}

func (svc *Service) archiveStore() Store {
	if svc.store == nil {
		return nil
	}
	return svc.store()
}

func runtimeListID(machine, session string) string {
	raw, _ := json.Marshal([2]string{machine, session})
	return string(raw)
}

// decorated applies manager-owned archive visibility to its final agent states.
func (svc *Service) decorated(machine string) []proto.Session {
	list, _, err := svc.archivedSessions(context.Background(), machine, svc.decoratedUnarchived(machine))
	if err != nil {
		svc.logf("load agent archives: %v", err)
	}
	return list
}

func (svc *Service) listArchivedSessions(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), agentArchiveTimeout)
	defer cancel()
	mid := r.URL.Query().Get("machine_id")
	if mid != "" {
		if m, ok := svc.fleetMachine(mid); !ok {
			apierr.Write(w, http.StatusNotFound, i18n.Err("devices-api.unknown_machine"))
			return
		} else {
			mid = m.ID
		}
	}
	out := []proto.Session{}
	for _, m := range svc.fleetMachines() {
		if mid != "" && mid != m.ID {
			continue
		}
		list, changed, err := svc.archivedSessions(ctx, m.ID, svc.decoratedUnarchived(m.ID))
		if err != nil {
			apierr.Write(w, http.StatusInternalServerError, i18n.Err("hub-agent_archives.storage"))
			return
		}
		if changed {
			svc.publishList(m.ID)
		}
		for _, row := range list {
			if row.Archived && !row.Assistant {
				row.Manager = svc.id
				row.NativeArchive = svc.nativeArchiveView(m.ID, row.SessionID)
				out = append(out, row)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return runtimeListID(out[i].MachineID, out[i].SessionID) < runtimeListID(out[j].MachineID, out[j].SessionID)
		}
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	apierr.WriteJSON(w, map[string]any{"sessions": out})
}

func (svc *Service) setAgentSessionArchive(w http.ResponseWriter, r *http.Request, mid string, archived bool) {
	ctx, cancel := context.WithTimeout(r.Context(), agentArchiveTimeout)
	defer cancel()
	if !fullRole(r) {
		apierr.Write(w, http.StatusForbidden, i18n.Err("hub-agent_archives.forbidden"))
		return
	}
	m, ok := svc.fleetMachine(mid)
	if !ok {
		apierr.Write(w, http.StatusNotFound, i18n.Err("devices-api.unknown_machine"))
		return
	}
	list, _, err := svc.archivedSessions(ctx, m.ID, svc.decoratedUnarchived(m.ID))
	if err != nil {
		apierr.Write(w, http.StatusInternalServerError, i18n.Err("hub-agent_archives.storage"))
		return
	}
	var row proto.Session
	found := false
	for _, candidate := range list {
		if candidate.SessionID == r.PathValue("sid") {
			row, found = candidate, true
			break
		}
	}
	if !found {
		apierr.Write(w, http.StatusNotFound, i18n.Err("hub-deleted_sessions.unknown"))
		return
	}
	if row.Assistant || (row.Agent != "claude" && row.Agent != "codex") {
		apierr.Write(w, http.StatusBadRequest, i18n.Err("hub-agent_archives.unsupported"))
		return
	}
	if archived && agentArchiveBusy(row) {
		apierr.Write(w, http.StatusConflict, i18n.Err("hub-agent_archives.running"))
		return
	}
	row.MachineID = m.ID
	if err := svc.noteAgentArchive(ctx, row, archived); err != nil {
		svc.logf("save agent archive: %v", err)
		apierr.Write(w, http.StatusInternalServerError, i18n.Err("hub-agent_archives.storage"))
		return
	}
	svc.publishList(m.ID)
	apierr.WriteJSON(w, map[string]any{"ok": true, "native": svc.nativeArchiveAwaited(ctx, m.ID, row.SessionID)}) // agent_archive_native.go
}

func (svc *Service) noteAgentArchive(ctx context.Context, s proto.Session, archived bool) error {
	ctx, cancel := context.WithTimeout(ctx, agentArchiveTimeout)
	defer cancel()
	b := &svc.agentArchives
	if err := b.lock(ctx); err != nil {
		return err
	}
	defer b.unlock()
	if err := svc.loadAgentArchivesLocked(ctx); err != nil {
		return err
	}
	key := runtimeListID(s.MachineID, s.SessionID)
	next := maps.Clone(b.records)
	next[key] = agentArchive{Session: archiveSnapshot(s), Archived: archived, ChangedAt: time.Now().UTC()}
	if err := svc.saveAgentArchivesLocked(ctx, next); err != nil {
		return err
	}
	delete(b.pending, key)
	svc.nativeArchiveWanted(s, archived) // the tool's own archive follows (agent_archive_native.go)
	return nil
}

func (svc *Service) agentArchiveRoutes() {
	svc.handle("GET /api/v1/sessions/archived", svc.listArchivedSessions)
	svc.handle("POST /api/v1/sessions/{mid}/{sid}/archive", func(w http.ResponseWriter, r *http.Request) {
		svc.setAgentSessionArchive(w, r, r.PathValue("mid"), true)
	})
	svc.handle("POST /api/v1/sessions/{mid}/{sid}/unarchive", func(w http.ResponseWriter, r *http.Request) {
		svc.setAgentSessionArchive(w, r, r.PathValue("mid"), false)
	})
}
