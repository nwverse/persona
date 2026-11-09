package agents

import (
	"context"
	"maps"
	"net/http"
	"sync"
	"time"
)

const prefsKey = "agents.prefs"

// View is how the apps lay out the personal-agents list. Empty fields mean the defaults (group by date, sort by
// last update, show all, every provider).
type View struct {
	Group     string   `json:"group,omitempty"` // date | provider | none
	Sort      string   `json:"sort,omitempty"`  // updated | unread | name
	Show      string   `json:"show,omitempty"`  // all | unread | sendable
	Providers []string `json:"providers,omitempty"`
}

// Section is a user-named group; Assign maps a session id to a section id.
type Section struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Prefs is shared by every app of the user: pinned sessions (in pin order), the list view, custom sections, and the
// conversations the user removed from the list.
type Prefs struct {
	Pins     []string          `json:"pins"`
	View     View              `json:"view"`
	Sections []Section         `json:"sections"`
	Assign   map[string]string `json:"assign"`
	// Hidden is what the user removed from the list (a session id → when): the source keeps the conversation, it
	// only leaves Persona's list and unread counts until it gets a new message after that time (hidden.go). An app
	// that does not know the field sends it back empty; PutPrefs keeps the stored ones then.
	Hidden    map[string]int64 `json:"hidden,omitempty"`
	UpdatedAt time.Time        `json:"updatedAt,omitzero"`
}

type prefsStore struct {
	mu  sync.Mutex
	kv  KV
	mem *Prefs
}

func (p *Prefs) normalize() {
	if p.Pins == nil {
		p.Pins = []string{}
	}
	if p.Sections == nil {
		p.Sections = []Section{}
	}
	if p.Assign == nil {
		p.Assign = map[string]string{}
	}
	for k, at := range p.Hidden {
		if k == "" || at <= 0 {
			delete(p.Hidden, k)
		}
	}
	seen := map[string]bool{}
	pins := p.Pins[:0]
	for _, id := range p.Pins {
		if id != "" && !seen[id] {
			seen[id] = true
			pins = append(pins, id)
		}
	}
	p.Pins = pins
	ids := map[string]bool{}
	for _, s := range p.Sections {
		ids[s.ID] = true
	}
	for k, v := range p.Assign {
		if !ids[v] {
			delete(p.Assign, k)
		}
	}
}

func (s *prefsStore) get(ctx context.Context) (Prefs, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readLocked(ctx)
}

func (s *prefsStore) readLocked(ctx context.Context) (Prefs, error) {
	var p Prefs
	if s.kv != nil {
		if _, err := s.kv.Setting(ctx, prefsKey, &p); err != nil {
			return p, err
		}
	} else if s.mem != nil {
		p = *s.mem
		p.Hidden = maps.Clone(p.Hidden)
	}
	p.normalize()
	return p, nil
}

// update reads the stored prefs, lets change edit them and saves the result, all under the store's lock: two apps
// saving at once, or a hide during a pin, cannot drop each other's change.
func (s *prefsStore) update(ctx context.Context, change func(*Prefs) error) (Prefs, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.readLocked(ctx)
	if err != nil {
		return p, err
	}
	if err := change(&p); err != nil {
		return p, err
	}
	if len(p.Pins) > 1000 || len(p.Sections) > 100 || len(p.Assign) > 5000 || len(p.Hidden) > maxHidden {
		return p, fail(http.StatusBadRequest, "agents: prefs too large")
	}
	for _, sec := range p.Sections {
		if sec.ID == "" || sec.Name == "" {
			return p, fail(http.StatusBadRequest, "agents: section needs id and name")
		}
	}
	p.normalize()
	p.UpdatedAt = time.Now().UTC()
	if s.kv != nil {
		if err := s.kv.PutSetting(ctx, prefsKey, p); err != nil {
			return p, err
		}
	} else {
		c := p
		c.Hidden = maps.Clone(p.Hidden)
		s.mem = &c
	}
	return p, nil
}

// Prefs returns the shared list preferences (defaults when never saved).
func (s *Service) Prefs(ctx context.Context) (Prefs, error) { return s.prefs.get(ctx) }

// PutPrefs replaces the shared list preferences. Hidden conversations are changed only through Hide / Unhide: an app
// that does not know them (an older one, the web's pin toggle) must not bring them back by saving its prefs.
func (s *Service) PutPrefs(ctx context.Context, p Prefs) (Prefs, error) {
	return s.prefs.update(ctx, func(cur *Prefs) error {
		hidden := cur.Hidden
		*cur = p
		cur.Hidden = hidden
		return nil
	})
}
