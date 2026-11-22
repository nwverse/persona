package assistant

import (
	"context"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/identity"
)

// The assistant's spaces (#13104, docs/assistant/health.md): a space is a named place the assistant's conversations
// run in, with a directory of its own. The main space ("") is the assistant's own directory and is no entry; the
// health space is the built-in entry. Every decision on how a space's information is used (the lock screen and
// channels, extensions, the knowledge base, handing it out, the other conversations' file tools) reads the space's
// SpacePolicy, never the space's name. Information is to be used by its categories and the scene it goes to (a
// usage decision that replaces SpacePolicy); until then every space other than the main one, registered or not, gets
// the strictest policy: the health space's, as "when unsure, treat it as health" always did.

// SpacePolicy is how a space's information may be used, one switch a use.
type SpacePolicy struct {
	// Redact: pushes, the lock screen, Lark and the plugin channels say only that there is news.
	Redact bool
	// HideFromExtensions: an extension does not see the space's conversations, transcripts, cards, to-dos, feed or
	// memory.
	HideFromExtensions bool
	// NoKBWrite: the space's conversations do not write the shared knowledge base.
	NoKBWrite bool
	// ConfirmOutbound: what the space hands outside it is confirmed on the phone each time, and a task plan it makes
	// does not start on its own.
	ConfirmOutbound bool
	// DenyMainRead: the assistant's other conversations' Read and Edit are denied the space's directory.
	DenyMainRead bool
}

// strictness orders policies: the more switches on, the stricter.
func (p SpacePolicy) strictness() int {
	n := 0
	for _, on := range []bool{p.Redact, p.HideFromExtensions, p.NoKBWrite, p.ConfirmOutbound, p.DenyMainRead} {
		if on {
			n++
		}
	}
	return n
}

// strictPolicy turns every use rule on: the health space's.
var strictPolicy = SpacePolicy{Redact: true, HideFromExtensions: true, NoKBWrite: true, ConfirmOutbound: true, DenyMainRead: true}

// categoryHealth is the health information category, the health space's default.
const categoryHealth = "health"

// Space templates: the instructions a space's conversations get.
const (
	spaceTemplateSpace  = "space"
	spaceTemplateHealth = "health"
)

// spaceEntry is one registered space.
type spaceEntry struct {
	Slug        string
	Title       string
	Icon        string
	Description string
	// DefaultCategories are the information categories everything produced in the space carries at least (the
	// tagging default); the health space's is health.
	DefaultCategories []string
	Template          string
	// DirName is the space's directory in the state directory; the built-in health space's is the manager's
	// (identity.HealthDirName), whatever the row says.
	DirName  string
	Builtin  bool
	Archived bool
	Revision int64
}

// builtinSpaces are the spaces every manager has whatever its store holds: the health space, with the health
// template, in the manager's health directory.
func (s *Service) builtinSpaces() []spaceEntry {
	return []spaceEntry{{Slug: SpaceHealth, DefaultCategories: []string{categoryHealth}, Template: spaceTemplateHealth,
		DirName: identity.HealthDirName(s.id), Builtin: true, Revision: 1}}
}

// spaceStore reads the registry (assistant_spaces).
type spaceStore interface {
	Spaces(ctx context.Context) ([]db.Space, error)
}

var _ spaceStore = (*db.DB)(nil)

// spaceRegistryTTL bounds how long the manager keeps what it read of the registry.
const spaceRegistryTTL = 30 * time.Second

// spaceRegistry caches the registry: read at most once a TTL, and dropped when the registry changes.
type spaceRegistry struct {
	mu   sync.Mutex
	at   time.Time
	list []spaceEntry
}

// spaces are the registered spaces, archived ones included: the built-in ones as the manager has them, then those
// its store registered. A store that cannot be read leaves the built-in ones (and what it read last).
func (s *Service) spaces(ctx context.Context) []spaceEntry {
	r := &s.spaceReg
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.list != nil && time.Since(r.at) < spaceRegistryTTL {
		return r.list
	}
	list := s.builtinSpaces()
	st, ok := s.store().(spaceStore)
	if !ok {
		r.list, r.at = list, time.Now()
		return list
	}
	rows, err := st.Spaces(ctx)
	if err != nil {
		s.logf("spaces: read: %v", err)
		if r.list != nil {
			return r.list
		}
		return list
	}
	for _, row := range rows {
		if row.Builtin || row.Slug == SpaceHealth {
			continue // the built-in ones are the manager's
		}
		list = append(list, spaceEntry{Slug: row.Slug, Title: row.Title, Icon: row.Icon, Description: row.Description,
			DefaultCategories: row.DefaultCategories, Template: row.Template, DirName: row.DirName,
			Archived: row.ArchivedAt != nil, Revision: row.Revision})
	}
	r.list, r.at = list, time.Now()
	return list
}

// spacesChanged drops what the manager read of the registry, so the next lookup reads it again.
func (s *Service) spacesChanged() {
	r := &s.spaceReg
	r.mu.Lock()
	r.list = nil
	r.mu.Unlock()
}

// spaceEntryOf is the registered space slug, archived or not.
func (s *Service) spaceEntryOf(ctx context.Context, slug string) (spaceEntry, bool) {
	if slug == "" {
		return spaceEntry{}, false
	}
	for _, e := range s.spaces(ctx) {
		if e.Slug == slug {
			return e, true
		}
	}
	return spaceEntry{}, false
}

// spacePolicy is the policy of space: the main space ("") has every switch off; every other space, registered or not,
// archived or not, the strictest (strictPolicy) until the per-use grading lands.
func (s *Service) spacePolicy(ctx context.Context, space string) SpacePolicy {
	if space == "" {
		return SpacePolicy{}
	}
	return strictPolicy
}

// spaceKnown: slug is the main space ("") or a registered space that is not archived: what a conversation, a task or a
// list may name.
func (s *Service) spaceKnown(ctx context.Context, slug string) bool {
	if slug == "" {
		return true
	}
	e, ok := s.spaceEntryOf(ctx, slug)
	return ok && !e.Archived
}
