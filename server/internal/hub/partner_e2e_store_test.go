package hub

import (
	"cmp"
	"context"
	"slices"
	"time"

	"github.com/nwverse/persona/server/internal/assistant"
	"github.com/nwverse/persona/server/internal/db"
)

// The manager's partner messages in memory (partner_e2e_test.go): meshStore keeps partner_messages and partner_prefs
// as *db.DB does in Postgres (db/partner_messages.go), so an in-process hub's real manager has partner messaging (the
// assistant's partnerStore; without it hub_list and hub_send are not listed and partner.receive is refused).

// memPartnerMessages are the rows, in the order they were written, and the prefs by hub_id (each with the pairing it
// was set for).
type memPartnerMessages struct {
	rows  []db.PartnerMessage
	prefs map[string]db.PartnerPref
}

// assistantPartnerStore is the assistant's partnerStore (unexported there): the methods it type-asserts its store for.
type assistantPartnerStore interface {
	InsertPartnerMessage(ctx context.Context, m db.PartnerMessage) (db.PartnerMessage, bool, error)
	PartnerMessageOf(ctx context.Context, direction, peer, messageID string) (*db.PartnerMessage, error)
	UpdatePartnerMessage(ctx context.Context, m db.PartnerMessage) (bool, error)
	ClaimPartnerMessages(ctx context.Context, lease time.Duration, limit int) ([]db.PartnerMessage, error)
	PartnerMessagesInState(ctx context.Context, direction, state string) ([]db.PartnerMessage, error)
	RecentPartnerMessages(ctx context.Context, peer string, limit int) ([]db.PartnerMessage, error)
	CountPartnerMessages(ctx context.Context, direction, peer string, status bool, since time.Time) (int, error)
	PartnerPrefs(ctx context.Context) ([]db.PartnerPref, error)
	SetPartnerConfirm(ctx context.Context, hub string, confirm bool, pairedAt time.Time) error
}

var (
	_ assistantPartnerStore = (*meshStore)(nil)
	_ assistantPartnerStore = (*db.DB)(nil) // the same methods as the database's
	_ assistant.Store       = (*meshStore)(nil)
)

func (s *meshStore) partnerMsgsLocked() *memPartnerMessages {
	if s.partnerMsgs == nil {
		s.partnerMsgs = &memPartnerMessages{prefs: map[string]db.PartnerPref{}}
	}
	return s.partnerMsgs
}

// partnerCopy is m with its own NextTry: a row handed out never shares the stored one's.
func partnerCopy(m db.PartnerMessage) db.PartnerMessage {
	if m.NextTry != nil {
		t := m.NextTry.UTC()
		m.NextTry = &t
	}
	return m
}

func (p *memPartnerMessages) find(direction, peer, messageID string) int {
	return slices.IndexFunc(p.rows, func(r db.PartnerMessage) bool {
		return r.Direction == direction && r.PeerHub == peer && r.MessageID == messageID
	})
}

func (s *meshStore) InsertPartnerMessage(_ context.Context, m db.PartnerMessage) (db.PartnerMessage, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.partnerMsgsLocked()
	if i := p.find(m.Direction, m.PeerHub, m.MessageID); i >= 0 {
		return partnerCopy(p.rows[i]), false, nil
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now()
	}
	m.CreatedAt = m.CreatedAt.UTC()
	if !m.PairedAt.IsZero() {
		m.PairedAt = m.PairedAt.UTC().Truncate(time.Microsecond) // as Postgres keeps it (paired_at)
	}
	m.UpdatedAt, m.Revision = m.CreatedAt, 1
	m = partnerCopy(m)
	p.rows = append(p.rows, m)
	return partnerCopy(m), true, nil
}

func (s *meshStore) PartnerMessageOf(_ context.Context, direction, peer, messageID string) (*db.PartnerMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.partnerMsgsLocked()
	i := p.find(direction, peer, messageID)
	if i < 0 {
		return nil, nil
	}
	m := partnerCopy(p.rows[i])
	return &m, nil
}

func (s *meshStore) UpdatePartnerMessage(_ context.Context, m db.PartnerMessage) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.partnerMsgsLocked()
	i := slices.IndexFunc(p.rows, func(r db.PartnerMessage) bool { return r.ID == m.ID })
	if i < 0 || p.rows[i].Revision != m.Revision {
		return false, nil
	}
	r := &p.rows[i]
	m = partnerCopy(m)
	r.State, r.Code, r.CardID, r.Attempts, r.NextTry = m.State, m.Code, m.CardID, m.Attempts, m.NextTry
	r.MachineID, r.SessionID, r.Title = m.MachineID, m.SessionID, m.Title
	r.UpdatedAt = time.Now().UTC()
	r.Revision++
	return true, nil
}

func (s *meshStore) ClaimPartnerMessages(_ context.Context, lease time.Duration, limit int) ([]db.PartnerMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.partnerMsgsLocked()
	now := time.Now().UTC()
	var due []int
	for i, r := range p.rows {
		if r.NextTry != nil && !r.NextTry.After(now) {
			due = append(due, i)
		}
	}
	slices.SortStableFunc(due, func(a, b int) int { return p.rows[a].NextTry.Compare(*p.rows[b].NextTry) })
	var out []db.PartnerMessage
	for _, i := range due[:min(len(due), limit)] {
		next := now.Add(lease)
		p.rows[i].NextTry = &next
		p.rows[i].Revision++
		out = append(out, partnerCopy(p.rows[i]))
	}
	return out, nil
}

func (s *meshStore) PartnerMessagesInState(_ context.Context, direction, state string) ([]db.PartnerMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []db.PartnerMessage
	for _, r := range s.partnerMsgsLocked().rows {
		if r.Direction == direction && r.State == state {
			out = append(out, partnerCopy(r))
		}
	}
	slices.SortStableFunc(out, func(a, b db.PartnerMessage) int {
		return cmp.Or(a.CreatedAt.Compare(b.CreatedAt), cmp.Compare(a.ID, b.ID))
	})
	return out, nil
}

func (s *meshStore) RecentPartnerMessages(_ context.Context, peer string, limit int) ([]db.PartnerMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []db.PartnerMessage
	for _, r := range s.partnerMsgsLocked().rows {
		if (peer == "" || r.PeerHub == peer) && r.Kind != "status" {
			out = append(out, partnerCopy(r))
		}
	}
	slices.SortStableFunc(out, func(a, b db.PartnerMessage) int {
		return cmp.Or(b.CreatedAt.Compare(a.CreatedAt), cmp.Compare(b.ID, a.ID))
	})
	return out[:min(len(out), limit)], nil
}

func (s *meshStore) CountPartnerMessages(_ context.Context, direction, peer string, status bool, since time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, r := range s.partnerMsgsLocked().rows {
		if r.Direction == direction && r.PeerHub == peer && (r.Kind == "status") == status && !r.CreatedAt.Before(since) {
			n++
		}
	}
	return n, nil
}

func (s *meshStore) PartnerPrefs(context.Context) ([]db.PartnerPref, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []db.PartnerPref
	for _, p := range s.partnerMsgsLocked().prefs {
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b db.PartnerPref) int { return cmp.Compare(a.HubID, b.HubID) })
	return out, nil
}

func (s *meshStore) SetPartnerConfirm(_ context.Context, hub string, confirm bool, pairedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !pairedAt.IsZero() {
		pairedAt = pairedAt.UTC().Truncate(time.Microsecond) // as Postgres keeps it
	}
	s.partnerMsgsLocked().prefs[hub] = db.PartnerPref{HubID: hub, Confirm: confirm, PairedAt: pairedAt, UpdatedAt: time.Now().UTC()}
	return nil
}

// partnerMessage is the row direction / peer / messageID as the manager keeps it now (false: none).
func (s *meshStore) partnerMessage(direction, peer, messageID string) (db.PartnerMessage, bool) {
	m, _ := s.PartnerMessageOf(context.Background(), direction, peer, messageID)
	if m == nil {
		return db.PartnerMessage{}, false
	}
	return *m, true
}
