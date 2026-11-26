package assistant

import (
	"context"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/prompts"
)

// assistant.supervision_roles supplements direction.RoleInEffect for main, background and side conversations that
// have no direction record. In particular, a manager upgraded while an old full-policy process works must not invent
// a role-filtered fingerprint for it. An idle send records its proposed role with the send's notesCarry: a refusal
// restores the previous role, while acceptance keeps it durably for later working sends and manager restarts.
const supervisionRolesKey = "assistant.supervision_roles"

type supervisionRoleBook struct {
	mu       sync.Mutex
	loaded   bool
	m        map[string]string
	dirty    bool // an accepted or rolled-back in-memory role still needs its durable write retried
	inflight map[string]*supervisionRoleFlight
}

type supervisionRoleFlight struct {
	moved []*supervisionRoleMove
}

// supervisionRoleMove is one in-flight role change. The book's map holds the newest proposed role, just as a direction
// holds its newest notes move; prev restores the last confirmed role when every carrier of this move was refused.
type supervisionRoleMove struct {
	key      string
	prev     string
	hadPrev  bool
	role     string
	carriers int
}

// supervisionRoleKey is unambiguous without NUL, which PostgreSQL jsonb rejects even in an escaped object key.
func supervisionRoleKey(machine, id string) string {
	return strconv.Itoa(len(machine)) + ":" + machine + id
}

func validSupervisionRole(role string) (prompts.SupervisionRole, bool) {
	r := prompts.SupervisionRole(role)
	switch r {
	case prompts.SupervisionRoleMain, prompts.SupervisionRoleLeader, prompts.SupervisionRoleBackground, prompts.SupervisionRoleSide:
		return r, true
	}
	return "", false
}

// supRolesLocked loads the supplement once; b.mu is held. Unknown roles are ignored by its readers and fail closed to
// the complete legacy policy, never to a broader role-filtered one.
func (s *Service) supRolesLocked(ctx context.Context) bool {
	b := &s.supRoles
	if b.loaded {
		return true
	}
	m := map[string]string{}
	if st := s.store(); st != nil {
		if _, err := st.Setting(ctx, supervisionRolesKey, &m); err != nil {
			s.logf("load assistant supervision roles: %v", err)
			return false
		}
	}
	if m == nil {
		m = map[string]string{}
	}
	b.m, b.loaded = m, true
	return true
}

// saveSupervisionRolesLocked persists the newest proposed roles; b.mu is held.
func (s *Service) saveSupervisionRolesLocked(ctx context.Context) error {
	st := s.store()
	if st == nil {
		return nil
	}
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return st.PutSetting(writeCtx, supervisionRolesKey, s.supRoles.m)
}

// workingSupervisionRole returns the role already in effect. A direction record is authoritative once one exists;
// conversations without one use the supplement. A read failure or absent legacy baseline retains the complete policy.
func (s *Service) workingSupervisionRole(ctx context.Context, machine, id string) (prompts.SupervisionRole, bool) {
	s.directions.mu.Lock()
	loaded := s.directionsLocked(ctx)
	if !loaded {
		s.directions.mu.Unlock()
		return "", false
	}
	if d := s.directions.m[id]; d != nil {
		role, ok := validSupervisionRole(d.RoleInEffect)
		s.directions.mu.Unlock()
		return role, ok
	}
	s.directions.mu.Unlock()
	b := &s.supRoles
	b.mu.Lock()
	defer b.mu.Unlock()
	if !s.supRolesLocked(ctx) {
		return "", false
	}
	return validSupervisionRole(b.m[supervisionRoleKey(machine, id)])
}

// recordSupervisionRole proposes an idle role on a send whose outcome its caller reports. Direction records use their
// own notes/role move instead. A policy-only lookup has no carry and therefore cannot create a baseline that a later
// send might present without the node ever accepting it.
func (s *Service) recordSupervisionRole(ctx context.Context, machine, id string, role prompts.SupervisionRole) error {
	return s.recordSupervisionRoleAccepted(ctx, machine, id, role, false)
}

// recordAcceptedSupervisionRole records a compact startup policy after the node returned its new conversation id.
// If the durable write fails, the accepted in-memory role still prevents this manager from changing a working process
// back to the full fingerprint; the error is logged by the caller and the write is retried by a later idle selection.
func (s *Service) recordAcceptedSupervisionRole(ctx context.Context, machine, id string, role prompts.SupervisionRole) error {
	return s.recordSupervisionRoleAccepted(ctx, machine, id, role, true)
}

func (s *Service) recordSupervisionRoleAccepted(ctx context.Context, machine, id string, role prompts.SupervisionRole, accepted bool) error {
	s.directions.mu.Lock()
	loaded := s.directionsLocked(ctx)
	if loaded && s.directions.m[id] != nil {
		s.directions.mu.Unlock()
		return nil
	}
	s.directions.mu.Unlock()
	if !loaded {
		return &supervisionRoleStoreError{}
	}
	carry, _ := ctx.Value(notesCarryKey{}).(*notesCarry)
	if carry != nil && carry.role != nil {
		return nil // a hub retry's policy step already ran for this send
	}
	b := &s.supRoles
	b.mu.Lock()
	defer b.mu.Unlock()
	if !s.supRolesLocked(ctx) {
		return &supervisionRoleStoreError{}
	}
	key := supervisionRoleKey(machine, id)
	if b.m[key] == string(role) {
		if b.dirty {
			if err := s.saveSupervisionRolesLocked(ctx); err != nil {
				return err
			}
			b.dirty = false
		}
		if carry != nil {
			if f := b.inflight[key]; f != nil {
				for i := len(f.moved) - 1; i >= 0; i-- {
					if m := f.moved[i]; m.role == string(role) {
						m.carriers++
						carry.id, carry.role = id, m
						break
					}
				}
			}
		}
		return nil
	}
	if carry == nil && !accepted {
		return &supervisionRoleStoreError{}
	}
	previous, existed := b.m[key]
	m := &supervisionRoleMove{key: key, prev: previous, hadPrev: existed, role: string(role), carriers: 1}
	b.m[key] = string(role)
	if err := s.saveSupervisionRolesLocked(ctx); err != nil {
		if accepted {
			b.dirty = true
		} else if existed {
			b.m[key] = previous
		} else {
			delete(b.m, key)
		}
		return err
	}
	b.dirty = false
	if carry != nil {
		if b.inflight == nil {
			b.inflight = map[string]*supervisionRoleFlight{}
		}
		f := b.inflight[key]
		if f == nil {
			f = &supervisionRoleFlight{}
			b.inflight[key] = f
		}
		f.moved = append(f.moved, m)
		if len(f.moved) > notesInFlightMost {
			f.moved = f.moved[1:]
		}
		carry.id, carry.role = id, m
	}
	return nil
}

// supervisionRoleSettle settles the supplement's part of a send. It mirrors notesSettleLocked: acceptance confirms
// this and every earlier move; refusal restores the previous role once no other in-flight send carries this one.
func (s *Service) supervisionRoleSettle(ctx context.Context, carry *notesCarry, taken bool) {
	m := carry.role
	carry.role = nil
	b := &s.supRoles
	b.mu.Lock()
	defer b.mu.Unlock()
	f := b.inflight[m.key]
	i := -1
	if f != nil {
		i = slices.Index(f.moved, m)
	}
	switch {
	case i < 0:
		return
	case taken:
		f.moved = f.moved[i+1:]
	case m.carriers > 1:
		m.carriers--
		return
	default:
		f.moved = slices.Delete(f.moved, i, i+1)
		if i < len(f.moved) {
			f.moved[i].prev, f.moved[i].hadPrev = m.prev, m.hadPrev
		} else if b.m[m.key] == m.role {
			if m.hadPrev {
				b.m[m.key] = m.prev
			} else {
				delete(b.m, m.key)
			}
			if err := s.saveSupervisionRolesLocked(ctx); err != nil {
				b.dirty = true
				s.logf("roll back assistant supervision role: %v", err)
			} else {
				b.dirty = false
			}
		}
	}
	if len(f.moved) == 0 {
		delete(b.inflight, m.key)
	}
}

type supervisionRoleStoreError struct{}

func (*supervisionRoleStoreError) Error() string {
	return "the assistant's supervision roles could not be read or no send can settle a new role"
}
