package assistant

import (
	"cmp"
	"context"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// subagentKeep bounds how far back historical finished A2A launches are loaded into the
// in-memory subagent index on restart; active (non-terminal) launches are always loaded.
const subagentKeep = 30 * 24 * time.Hour

// subagentLink records durable parent provenance for a manager-launched subagent session
// (A2A launch, cross-machine dispatch, or task_spawn) so decoration and cross-machine
// folding survive manager restarts.
type subagentLink struct {
	ParentMachine string
	ParentSession string
	A2ATaskID     string
	Origin        string
}

func (s *Service) ensureSubagentsLoaded(ctx context.Context) {
	f := &s.fleet
	f.subagentsMu.RLock()
	loaded := f.subagentsLoaded
	f.subagentsMu.RUnlock()
	if loaded {
		return
	}
	f.subagentsMu.Lock()
	defer f.subagentsMu.Unlock()
	if f.subagentsLoaded {
		return
	}
	// If there is no store at all (unit tests without Postgres, or zero-value Service), mark loaded
	// immediately so hot paths (such as fleetSnapshot) do zero extra work.
	if s.store == nil || s.store() == nil {
		f.subagentsLoaded = true
		return
	}
	loadOK := true
	if st, err := s.a2aStore(); err == nil {
		rows, err := st.A2ALaunches(ctx, time.Now().Add(-subagentKeep))
		if err != nil {
			s.logf("subagent index: load A2A launches: %v", err)
			loadOK = false
		} else {
			for i := range rows {
				t, err := decodeA2A(&rows[i], nil)
				if err != nil || t == nil || t.Launch == nil || t.SourceSession == "" || t.TargetSession == "" {
					continue
				}
				origin := proto.SubagentOriginA2A
				if t.Launch.Dispatch {
					origin = proto.SubagentOriginDispatch
				}
				s.putSubagentLinkLocked(t.TargetMachine, t.TargetSession, subagentLink{
					ParentMachine: t.SourceMachine,
					ParentSession: t.SourceSession,
					A2ATaskID:     t.ID,
					Origin:        origin,
				})
			}
		}
	}
	for _, dt := range s.dispatched(ctx) {
		if dt.FromSession == "" || dt.Session == "" {
			continue
		}
		key := [2]string{dt.Machine, dt.Session}
		if f.subagents != nil {
			if _, exists := f.subagents[key]; exists {
				continue
			}
		}
		s.putSubagentLinkLocked(dt.Machine, dt.Session, subagentLink{
			ParentMachine: dt.FromMachine,
			ParentSession: dt.FromSession,
			Origin:        proto.SubagentOriginDispatch,
		})
	}
	if s.ensureTodosLoaded(ctx) == nil {
		s.todos.mu.RLock()
		for _, td := range s.todos.todos {
			for _, a := range td.Agents {
				if a.Source != sourceSpawned || a.Via == "" || a.MachineID == "" || a.SessionID == "" {
					continue
				}
				pm, ps, ok := strings.Cut(a.Via, "/")
				if !ok || pm == "" || ps == "" {
					continue
				}
				key := [2]string{a.MachineID, a.SessionID}
				if f.subagents != nil {
					if _, exists := f.subagents[key]; exists {
						continue
					}
				}
				s.putSubagentLinkLocked(a.MachineID, a.SessionID, subagentLink{
					ParentMachine: pm,
					ParentSession: ps,
					Origin:        proto.SubagentOriginSpawned,
				})
			}
		}
		s.todos.mu.RUnlock()
	}
	if loadOK {
		f.subagentsLoaded = true
	}
}

func (s *Service) putSubagentLinkLocked(machine, session string, link subagentLink) {
	if machine == "" || session == "" || link.ParentSession == "" {
		return
	}
	f := &s.fleet
	if f.subagents == nil {
		f.subagents = map[[2]string]subagentLink{}
	}
	key := [2]string{machine, session}
	if link.A2ATaskID != "" {
		if f.subagentsByTask == nil {
			f.subagentsByTask = map[string][2]string{}
		}
		if prev, had := f.subagentsByTask[link.A2ATaskID]; had && prev != key {
			delete(f.subagents, prev)
		}
		f.subagentsByTask[link.A2ATaskID] = key
	}
	f.subagents[key] = link
}

// recordA2ASubagent records a newly created or resolved A2A/dispatch launch in the in-memory
// subagent index so decoration immediately tags the target session without waiting for a reload.
func (s *Service) recordA2ASubagent(t *a2aTask) {
	if t == nil || t.Launch == nil || t.SourceSession == "" || t.TargetMachine == "" || t.TargetSession == "" {
		return
	}
	origin := proto.SubagentOriginA2A
	if t.Launch.Dispatch {
		origin = proto.SubagentOriginDispatch
	}
	s.fleet.subagentsMu.Lock()
	s.putSubagentLinkLocked(t.TargetMachine, t.TargetSession, subagentLink{
		ParentMachine: t.SourceMachine,
		ParentSession: t.SourceSession,
		A2ATaskID:     t.ID,
		Origin:        origin,
	})
	s.fleet.subagentsMu.Unlock()
}

// forgetA2ASubagent removes a reserved launch entry if the launch failed before starting.
func (s *Service) forgetA2ASubagent(taskID string) {
	if taskID == "" {
		return
	}
	s.fleet.subagentsMu.Lock()
	if key, ok := s.fleet.subagentsByTask[taskID]; ok {
		delete(s.fleet.subagentsByTask, taskID)
		if cur, has := s.fleet.subagents[key]; has && cur.A2ATaskID == taskID {
			delete(s.fleet.subagents, key)
		}
	}
	s.fleet.subagentsMu.Unlock()
}

// recordSubagentLink records a non-A2A subagent link (e.g. legacy storeless dispatch or task_spawn).
func (s *Service) recordSubagentLink(machine, session, parentMachine, parentSession, origin string) {
	if machine == "" || session == "" || parentSession == "" {
		return
	}
	s.fleet.subagentsMu.Lock()
	s.putSubagentLinkLocked(machine, session, subagentLink{
		ParentMachine: parentMachine,
		ParentSession: parentSession,
		Origin:        origin,
	})
	s.fleet.subagentsMu.Unlock()
}

func (s *Service) lookupSubagentLink(ctx context.Context, machine, session string) (subagentLink, bool) {
	s.ensureSubagentsLoaded(ctx)
	f := &s.fleet
	f.subagentsMu.RLock()
	link, ok := f.subagents[[2]string{machine, session}]
	f.subagentsMu.RUnlock()
	return link, ok
}

// isA2ASubagent reports whether ref is a session launched as a subagent by an A2A (or A2A-backed dispatch) task.
func (s *Service) isA2ASubagent(ctx context.Context, ref a2aRef) bool {
	if ref.Machine == "" || ref.Session == "" {
		return false
	}
	link, ok := s.lookupSubagentLink(ctx, ref.Machine, ref.Session)
	return ok && link.A2ATaskID != ""
}

// decorateSubagents stamps durable manager-tracked subagent parent provenance (A2A, dispatch,
// task_spawn) onto decorated sessions. Native/runtime node subagents keep their existing wire
// representation untouched, and ordinary rows allocate nothing.
func (s *Service) decorateSubagents(ctx context.Context, machine string, list []proto.Session) {
	s.ensureSubagentsLoaded(ctx)
	f := &s.fleet
	f.subagentsMu.RLock()
	if len(f.subagents) == 0 {
		f.subagentsMu.RUnlock()
		return
	}
	for i := range list {
		row := &list[i]
		if row.Assistant || row.SessionID == "" {
			continue
		}
		link, ok := f.subagents[[2]string{machine, row.SessionID}]
		if !ok {
			continue
		}
		row.IsSubagent = true
		if row.ParentSessionID == "" {
			row.ParentSessionID = link.ParentSession
		}
		parentMachine := ""
		if link.ParentMachine != "" && link.ParentMachine != machine {
			parentMachine = link.ParentMachine
		}
		row.SetSubagentProvenance(
			cmp.Or(row.ParentMachineID(), parentMachine),
			cmp.Or(row.A2ATaskID(), link.A2ATaskID),
			cmp.Or(row.SubagentOrigin(), link.Origin),
		)
	}
	f.subagentsMu.RUnlock()
}
