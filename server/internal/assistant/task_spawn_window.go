package assistant

import (
	"context"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// spawnStarts keeps a task_spawn child from passing for a first-layer Agent before it is recorded as spawned
// (#11999). The node sends a new session its first prompt before session.start answers, and the manager handles
// other RPCs meanwhile, so the child can list and call tools while its parent's start is still in flight. A workspace
// linked to the to-do can link it in that time (autoLinkTodos, source space); its own id is not known until the start
// answers, and no runtime marks it beforehand. So from just before a start until its child is linked as spawned, any
// session newly linked on that computer may be the child: it does not see task_spawn and cannot call it. When the
// child is linked, the others are ordinary Agents again and discovery is woken to list the tool for them. When the
// start fails or its result is unknown, a child may run unlinked to its parent: whatever was newly linked meanwhile
// keeps no task_spawn for the rest of this process.
type spawnStarts struct {
	mu      sync.Mutex
	pending map[*spawnStart]struct{}
	// suspect: sessions linked while a start that did not link its child was in flight, by machine and session.
	suspect map[[2]string]bool
	// unresolved: starts whose answer and session id were both lost. Later session lists are reconciled by identity,
	// not treated as ordinary late Agents merely because the window already ended.
	unresolved map[*spawnStartIdentity]struct{}
}

// spawnStartIdentity is the recoverable identity of one start, even when its RPC answer is gone.
type spawnStartIdentity struct {
	expected  string
	known     map[string]bool
	launcher  string
	cwd       string
	machine   string
	until     time.Time
	exactOnly bool
}

// matches uses the exact expected id when available, then the same launcher/cwd/StartedByPersona heuristic as
// adoption. A false StartedByPersona remains an ordinary late Agent; a matching unresolved child is always suspect.
func (i *spawnStartIdentity) matches(c proto.Session) bool {
	if i == nil || c.SessionID == "" {
		return false
	}
	if i.expected != "" && c.SessionID == i.expected {
		return true
	}
	if i.known[c.SessionID] || !c.StartedByPersona || c.Assistant || c.Launcher != i.launcher {
		return false
	}
	return c.Cwd == i.cwd || c.WorkspaceCwd == i.cwd
}

// resolves is deliberately stronger than matches. A same-shaped heuristic session is suspect, but only the exact
// expected id proves that an unresolved start has found its child. With no expected id, only expiry can retire it
// unless a future identity type supplies separate exact proof.
func (i *spawnStartIdentity) resolves(c proto.Session) bool {
	return i != nil && i.expected != "" && c.SessionID == i.expected
}

// spawnStart is one task_spawn start in flight.
type spawnStart struct {
	machine string
	// linkedBefore: the sessions of machine that had an active to-do link when the start began. The child is not
	// among them; it did not exist yet.
	linkedBefore map[string]bool
	// linkedDuring: new active memberships first seen while pending. Unlike a final snapshot, this retains a session
	// that was unlinked or whose auto-link was rolled back before a failed start ended.
	linkedDuring map[string]bool
	// unconfirmed is the child returned by session.start. If confirmation fails, it is suspected even when stop fails
	// and no membership has appeared yet.
	unconfirmed [2]string
	// identity is retained when startTaskAgent cannot return the child id, so later session lists can reconcile it.
	identity *spawnStartIdentity
}

// spawnDecision is the current, jointly read membership and startup-window decision.
type spawnDecision uint8

const (
	spawnAllowed spawnDecision = iota
	spawnNoActiveTask
	spawnCallerIsChild
	spawnAncestryUnknown
)

// Test-only observation hooks. Production leaves both nil. They run before the authoritative synchronized recheck,
// letting a regression reproduce the old interleaving without holding a lock that would deadlock the other RPC.
var spawnAuthorizationTestHook func()
var spawnBeginTestHook func()
var spawnEndTestHook func()
var spawnLinkTestHook func()

func noteSpawnAuthorizationTestHook() {
	if spawnAuthorizationTestHook != nil {
		spawnAuthorizationTestHook()
	}
}

func noteSpawnBeginTestHook() {
	if spawnBeginTestHook != nil {
		spawnBeginTestHook()
	}
}

func noteSpawnLinkTestHook() {
	if spawnLinkTestHook != nil {
		spawnLinkTestHook()
	}
}

func noteSpawnEndTestHook() {
	if spawnEndTestHook != nil {
		spawnEndTestHook()
	}
}

// spawnAuthorization is the one task_spawn discovery and direct-call gate. The early source check is only an
// optimization and a deterministic observation point; authorization always comes from the synchronized recheck.
func (s *Service) spawnAuthorization(ctx context.Context, machine, session string) (Todo, spawnDecision) {
	t, ok := s.spawnTask(ctx, machine, session)
	if !ok {
		return Todo{}, spawnNoActiveTask
	}
	caller, ok := activeTodoAgent(t, machine, session)
	if !ok {
		return Todo{}, spawnNoActiveTask
	}
	if caller.Source == sourceSpawned {
		return t, spawnCallerIsChild
	}
	noteSpawnAuthorizationTestHook()
	return s.currentSpawnAuthorization(ctx, machine, session, "")
}

// currentSpawnAuthorization re-reads the membership and startup state as one decision. todos.writing is the common
// boundary with every membership and spawn-window transition; spawns.mu protects the maps within that boundary.
func (s *Service) currentSpawnAuthorization(ctx context.Context, machine, session, expectedTodo string) (Todo, spawnDecision) {
	if err := s.ensureTodosLoaded(ctx); err != nil {
		return Todo{}, spawnNoActiveTask
	}
	caller, hasCaller := s.session(ctx, machine, session)
	s.todos.writing.Lock()
	defer s.todos.writing.Unlock()
	if hasCaller {
		// A workspace fanout may publish membership before sessions.changed. Reconcile this caller's typed identity
		// before discovery or a direct call can treat it as an ordinary first-layer Agent.
		s.reconcileUnresolvedStartsLocked(machine, []proto.Session{caller})
	}
	return s.spawnAuthorizationLocked(machine, session, expectedTodo)
}

// spawnAuthorizationLocked requires todos.writing. The returned Todo is a snapshot of the same synchronized decision.
func (s *Service) spawnAuthorizationLocked(machine, session, expectedTodo string) (Todo, spawnDecision) {
	s.todos.mu.RLock()
	id, ok := s.todos.activeBySession[[2]string{machine, session}]
	if !ok || expectedTodo != "" && id != expectedTodo {
		s.todos.mu.RUnlock()
		return Todo{}, spawnNoActiveTask
	}
	t := s.todos.todos[id]
	if t == nil || t.Origin == "" || t.DoneAt != nil || t.Cancelled() || t.Supervision == nil || t.Supervision.Supervisor == "" {
		s.todos.mu.RUnlock()
		return Todo{}, spawnNoActiveTask
	}
	current := copyTodoLocked(t)
	s.todos.mu.RUnlock()
	caller, ok := activeTodoAgent(current, machine, session)
	if !ok {
		return Todo{}, spawnNoActiveTask
	}
	if caller.Source == sourceSpawned {
		return current, spawnCallerIsChild
	}
	s.spawns.mu.Lock()
	unknown := s.spawnAncestryUnknownLocked(machine, session)
	s.spawns.mu.Unlock()
	if unknown {
		return current, spawnAncestryUnknown
	}
	return current, spawnAllowed
}

// completeSpawnStartAuthorized makes the final caller check, child link and successful window close one transaction.
// Store I/O stays inside the established todo write boundary; notifications happen only after it is released.
func (s *Service) completeSpawnStartAuthorized(ctx context.Context, inFlight *spawnStart, machine, session, todoID,
	title, cwd, agent, via, mid, sid string) (spawnDecision, error) {
	if err := s.ensureTodosLoaded(ctx); err != nil {
		return spawnNoActiveTask, err
	}
	noteSpawnLinkTestHook()
	s.todos.writing.Lock()
	_, decision := s.spawnAuthorizationLocked(machine, session, todoID)
	if decision != spawnAllowed {
		s.todos.writing.Unlock()
		return decision, nil
	}
	err := s.writeTodoAgentLink(ctx, todoID, mid, sid, title, cwd, agent, "", "spawned", via, false)
	if err == nil {
		s.endSpawnStartLocked(inFlight, true)
	}
	s.todos.writing.Unlock()
	if err != nil {
		return spawnAllowed, err
	}
	s.linksChanged(ctx)
	s.todosChanged(ctx)
	s.mcpChanges.notify()
	return spawnAllowed, nil
}

// beginSpawnStart records a task_spawn start on machine before anything reaches the node. Tests use it to establish
// a window directly; production callers use beginSpawnStartForCaller so their authorization is rechecked atomically.
func (s *Service) beginSpawnStart(machine string) *spawnStart {
	s.todos.writing.Lock()
	defer s.todos.writing.Unlock()
	return s.beginSpawnStartLocked(machine)
}

// beginSpawnStartForCaller refuses a caller whose task, membership or ancestry changed after the assignment read.
// The final check and registration are one transition: no parent link/window close or workspace auto-link can slip
// between them. No pending start is registered on a refusal.
func (s *Service) beginSpawnStartForCaller(machine, session, todoID string) (*spawnStart, spawnDecision) {
	s.todos.writing.Lock()
	defer s.todos.writing.Unlock()
	if _, decision := s.spawnAuthorizationLocked(machine, session, todoID); decision != spawnAllowed {
		return nil, decision
	}
	return s.beginSpawnStartLocked(machine), spawnAllowed
}

// beginSpawnStartLocked requires todos.writing. An auto-link either belongs in linkedBefore or happens after pending.
func (s *Service) beginSpawnStartLocked(machine string) *spawnStart {
	p := &spawnStart{machine: machine, linkedBefore: s.linkedSessionsOn(machine)}
	s.spawns.mu.Lock()
	if s.spawns.pending == nil {
		s.spawns.pending = map[*spawnStart]struct{}{}
	}
	s.spawns.pending[p] = struct{}{}
	s.spawns.mu.Unlock()
	return p
}

// noteMembershipActivated records a new active membership in every pending window that did not contain it at begin.
// todoBook.activateTodoSession calls this with todos.mu and todos.writing held; spawns.mu only protects the maps.
func (s *spawnStarts) noteMembershipActivated(machine, session string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for p := range s.pending {
		if p.machine != machine || p.linkedBefore[session] {
			continue
		}
		if p.linkedDuring == nil {
			p.linkedDuring = map[string]bool{}
		}
		p.linkedDuring[session] = true
	}
}

// reconcileUnresolvedStartsLocked marks late sessions matching an unanswered start as suspect before auto-linking.
// It shares autoLinkTodos' write boundary, so the new membership and the ancestry record cannot pass each other.
func (s *Service) reconcileUnresolvedStartsLocked(machine string, sessions []proto.Session) bool {
	changed := false
	s.spawns.mu.Lock()
	defer s.spawns.mu.Unlock()
	resolved := map[*spawnStartIdentity]bool{}
	for _, c := range sessions {
		if c.Status == proto.StatusOffline {
			continue
		}
		for identity := range s.spawns.unresolved {
			if identity.machine != machine {
				continue
			}
			if !identity.until.After(time.Now()) {
				resolved[identity] = true
				continue
			}
			if !identity.matches(c) {
				continue
			}
			if s.spawns.suspect == nil {
				s.spawns.suspect = map[[2]string]bool{}
			}
			key := [2]string{machine, c.SessionID}
			if !s.spawns.suspect[key] {
				s.spawns.suspect[key] = true
				changed = true
			}
			if identity.resolves(c) {
				resolved[identity] = true
			}
		}
	}
	for identity := range resolved {
		// Retire exact matches only after the whole list has been examined: the first same-shaped session must not
		// hide a later actual child. Heuristic-only identities remain until expiry; suspect entries remain longer.
		delete(s.spawns.unresolved, identity)
	}
	return changed
}

// endSpawnStart ends p. linked: its child is now linked to the to-do as spawned. Otherwise every session newly linked
// on the machine since p began stays suspect, including one already unlinked again. The membership history, final
// active snapshot, suspect writes and pending deletion share one write boundary: an auto-link is either included or
// happens after the window ended.
func (s *Service) endSpawnStart(p *spawnStart, linked bool) {
	noteSpawnEndTestHook()
	s.todos.writing.Lock()
	s.endSpawnStartLocked(p, linked)
	s.todos.writing.Unlock()
	s.mcpChanges.notify() // Agents hidden from task_spawn while p was in flight list it again
}

// endSpawnStartLocked requires todos.writing. It is also the successful half of completeSpawnStartAuthorized.
func (s *Service) endSpawnStartLocked(p *spawnStart, linked bool) {
	suspects := map[[2]string]bool{}
	if !linked {
		for id := range s.linkedSessionsOn(p.machine) {
			if !p.linkedBefore[id] {
				suspects[[2]string{p.machine, id}] = true
			}
		}
		if p.unconfirmed[1] != "" {
			suspects[p.unconfirmed] = true
		}
	}
	s.spawns.mu.Lock()
	if !linked && p.unconfirmed[1] == "" && p.identity != nil && !p.identity.until.IsZero() {
		if s.spawns.unresolved == nil {
			s.spawns.unresolved = map[*spawnStartIdentity]struct{}{}
		}
		s.spawns.unresolved[p.identity] = struct{}{}
	}
	if !linked {
		for id := range p.linkedDuring {
			if !p.linkedBefore[id] {
				suspects[[2]string{p.machine, id}] = true
			}
		}
	}
	delete(s.spawns.pending, p)
	if len(suspects) > 0 && s.spawns.suspect == nil {
		s.spawns.suspect = map[[2]string]bool{}
	}
	for key := range suspects {
		s.spawns.suspect[key] = true
	}
	s.spawns.mu.Unlock()
}

// spawnAncestryUnknownLocked reports whether machine/session may be a task_spawn child not linked as spawned yet.
// The caller holds todos.writing, making this pending/suspect read part of one membership/window decision.
func (s *Service) spawnAncestryUnknownLocked(machine, session string) bool {
	if s.spawns.suspect[[2]string{machine, session}] {
		return true
	}
	for identity := range s.spawns.unresolved {
		if identity.machine == machine && identity.expected == session {
			return true // exact proof needs no session-list event or heuristic shape
		}
	}
	for p := range s.spawns.pending {
		if p.machine == machine && !p.linkedBefore[session] {
			return true
		}
	}
	return false
}

// linkedSessionsOn lists the sessions of machine that have an active to-do link. Window transitions call it with
// todos.writing held; the read lock also lets a test observe an old, pre-transition snapshot safely.
func (s *Service) linkedSessionsOn(machine string) map[string]bool {
	ids := map[string]bool{}
	s.todos.mu.RLock()
	defer s.todos.mu.RUnlock()
	for key := range s.todos.activeBySession {
		if key[0] == machine {
			ids[key[1]] = true
		}
	}
	return ids
}
