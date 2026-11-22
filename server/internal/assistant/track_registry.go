package assistant

import (
	"context"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/compat"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/identity"
)

// The assistant's tracks (#13104, docs/assistant/health.md): a track is a named place the assistant's conversations
// run in, of a kind (a domain space is one kind), with a directory of its own. Its conversations name it by its slug
// in the field the wire calls space (Session.Space, StartParams.Space, ?space=). The main space ("") is the
// assistant's own directory and is no entry; the health space is the built-in entry, which the manager supplies.
// Every decision on how a track's information is used (the lock screen and channels, extensions, the knowledge base,
// handing it out, the other conversations' file tools) reads the track's TrackPolicy, never its name or kind.

// TrackPolicy is how a track's information may be used, one switch a use. The switches are temporary: they give
// every privacy decision one place to read, and are to be replaced by a decision on the information's categories and
// the scene it is used in. Collection is meant to be unrestricted, so the extensions the user owns (the memory
// extension among them) are to read every track then. Until that lands every track other than the main space,
// registered or not, gets the strictest policy: the health space's, as "when unsure, treat it as health" always did.
type TrackPolicy struct {
	// Redact: pushes, the lock screen, Lark and the plugin channels say only that there is news.
	Redact bool
	// HideFromExtensions: an extension does not see the track's conversations, transcripts, cards, to-dos, feed or
	// memory.
	HideFromExtensions bool
	// NoKBWrite: the track's conversations do not write the shared knowledge base.
	NoKBWrite bool
	// ConfirmOutbound: what the track hands outside it is confirmed on the phone each time, and a task plan it makes
	// does not start on its own.
	ConfirmOutbound bool
	// DenyMainRead: the assistant's other conversations' Read and Edit are denied the track's directory.
	DenyMainRead bool
}

// strictness orders policies: the more switches on, the stricter.
func (p TrackPolicy) strictness() int {
	n := 0
	for _, on := range []bool{p.Redact, p.HideFromExtensions, p.NoKBWrite, p.ConfirmOutbound, p.DenyMainRead} {
		if on {
			n++
		}
	}
	return n
}

// strictPolicy turns every use rule on: the health space's.
var strictPolicy = TrackPolicy{Redact: true, HideFromExtensions: true, NoKBWrite: true, ConfirmOutbound: true, DenyMainRead: true}

// The information categories (docs/assistant/health.md), checked here rather than in the database so adding one is a
// code change with its use rules: infoCategoriesVersion goes up with every change to the list.
const (
	categoryHealth        = "health"
	categoryFinance       = "finance"
	categoryRelationships = "relationships"
	categoryEmotion       = "emotion"
	categoryLegalID       = "legal_id"
	infoCategoriesVersion = 1
)

var infoCategories = []string{categoryHealth, categoryFinance, categoryRelationships, categoryEmotion, categoryLegalID}

// validCategories: every one of cats is a known information category.
func validCategories(cats []string) bool {
	for _, c := range cats {
		if !slices.Contains(infoCategories, c) {
			return false
		}
	}
	return true
}

// Track kinds and templates. A kind is the track's type (its defaults); a template the instructions its conversations
// get. Both are checked here, not in the database.
const (
	trackKindSpace = "space"
	// trackTemplateAssistant is kept for the main track's instructions.
	trackTemplateAssistant = "assistant"
	trackTemplateSpace     = "space"
	trackTemplateHealth    = "health"
)

var trackTemplates = []string{trackTemplateAssistant, trackTemplateSpace, trackTemplateHealth}

// trackEntry is one registered track.
type trackEntry struct {
	Slug        string
	Kind        string
	Title       string
	Icon        string
	Description string
	// DefaultCategories are the information categories everything produced in the track carries at least (the
	// tagging default); the health space's is health.
	DefaultCategories []string
	Template          string
	// DirName is the track's directory, relative to the state directory: data, never derived from the kind. The
	// health space's is the manager's (identity.HealthDirName); a new track's defaults to tracks/<slug>.
	DirName  string
	Builtin  bool
	Archived bool
	Revision int64
}

// builtinTracks are the tracks every manager has whatever its store holds: the health space, of kind space, with the
// health template and the health category as its overrides of that kind's defaults, in the manager's health
// directory.
func (s *Service) builtinTracks() []trackEntry {
	return []trackEntry{{Slug: SpaceHealth, Kind: trackKindSpace, DefaultCategories: []string{categoryHealth},
		Template: trackTemplateHealth, DirName: identity.HealthDirName(s.id), Builtin: true, Revision: 1}}
}

// defaultTrackDir is where a new track of this manager keeps its directory: tracks/<slug> (tracks-<id>/<slug>), the
// same whatever its kind.
func (s *Service) defaultTrackDir(slug string) string {
	return path.Join(identity.TracksDirName(s.id), slug)
}

// validTrackDir: dir names a directory below the state directory (relative, clean, no "..").
func validTrackDir(dir string) bool {
	return dir != "" && !path.IsAbs(dir) && path.Clean(dir) == dir && dir != "." && !strings.HasPrefix(dir, "../") &&
		!strings.Contains(dir, "/../") && !strings.Contains(dir, `\`)
}

// trackStore reads the registry (assistant_tracks).
type trackStore interface {
	Tracks(ctx context.Context) ([]db.Track, error)
}

var _ trackStore = (*db.DB)(nil)

// trackRegistryTTL bounds how long the manager keeps what it read of the registry.
const trackRegistryTTL = 30 * time.Second

// trackRegistry caches the registry: read at most once a TTL, and dropped when the registry changes.
type trackRegistry struct {
	mu   sync.Mutex
	at   time.Time
	list []trackEntry
}

// tracks are the registered tracks, archived ones included: the built-in ones as the manager has them, then those its
// store registered. A row that is not valid here (its slug, template, categories or directory) is left out and logged:
// its conversations get the policy of an unknown track, the strictest. A store that cannot be read leaves the
// built-in ones (and what it read last).
func (s *Service) tracks(ctx context.Context) []trackEntry {
	r := &s.trackReg
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.list != nil && time.Since(r.at) < trackRegistryTTL {
		return r.list
	}
	list := s.builtinTracks()
	st, ok := s.store().(trackStore)
	if !ok {
		r.list, r.at = list, time.Now()
		return list
	}
	rows, err := st.Tracks(ctx)
	if err != nil {
		s.logf("tracks: read: %v", err)
		if r.list != nil {
			return r.list
		}
		return list
	}
	for _, row := range rows {
		if row.Builtin || row.Slug == SpaceHealth {
			continue // the built-in ones are the manager's
		}
		e := trackEntry{Slug: row.Slug, Kind: row.Kind, Title: row.Title, Icon: row.Icon, Description: row.Description,
			DefaultCategories: row.DefaultCategories, Template: row.Template, DirName: row.DirName,
			Archived: row.ArchivedAt != nil, Revision: row.Revision}
		if e.DirName == "" {
			e.DirName = s.defaultTrackDir(e.Slug)
		}
		if !identity.ValidTrackSlug(e.Slug) || !slices.Contains(trackTemplates, e.Template) || !validCategories(e.DefaultCategories) ||
			!validTrackDir(e.DirName) {
			s.logf("tracks: %q is not valid here (categories v%d): left out", e.Slug, infoCategoriesVersion)
			continue
		}
		list = append(list, e)
	}
	r.list, r.at = list, time.Now()
	return list
}

// tracksChanged drops what the manager read of the registry, so the next lookup reads it again.
func (s *Service) tracksChanged() {
	r := &s.trackReg
	r.mu.Lock()
	r.list = nil
	r.mu.Unlock()
}

// trackEntryOf is the registered track slug, archived or not.
func (s *Service) trackEntryOf(ctx context.Context, slug string) (trackEntry, bool) {
	if slug == "" {
		return trackEntry{}, false
	}
	for _, e := range s.tracks(ctx) {
		if e.Slug == slug {
			return e, true
		}
	}
	return trackEntry{}, false
}

// trackPolicy is the policy of track space: the main space ("") has every switch off; every other track, registered
// or not, archived or not, the strictest (strictPolicy). The next step decides by the information's categories and the
// scene, then applies what the track's kind tightens; never by a track's name or kind.
func (s *Service) trackPolicy(ctx context.Context, space string) TrackPolicy {
	if space == "" {
		return TrackPolicy{}
	}
	return strictPolicy
}

// trackKnown: slug is the main space ("") or a registered track that is not archived: what a conversation, a task or
// a list may name.
func (s *Service) trackKnown(ctx context.Context, slug string) bool {
	if slug == "" {
		return true
	}
	e, ok := s.trackEntryOf(ctx, slug)
	return ok && !e.Archived
}

// denyDir is one track directory a node denies the assistant's other conversations (assistant.prepare deny_dirs):
// relative to the machine's home, as assistant.prepare's dir, with its track's slug.
type denyDir struct {
	Dir   string `json:"dir"`
	Track string `json:"track"`
}

// trackDenyDirs are the directories of the registered tracks the assistant's other conversations' Read and Edit are
// denied (DenyMainRead; archived ones too: their files stay), for the node to add to the health spaces it finds itself
// (nodeproto.DenyDirs). The health space is not listed: every node finds every manager's. Never nil: an empty list
// tells the node none is.
func (s *Service) trackDenyDirs(ctx context.Context) []denyDir {
	out := []denyDir{}
	for _, e := range s.tracks(ctx) {
		if e.Builtin || !s.trackPolicy(ctx, e.Slug).DenyMainRead {
			continue
		}
		out = append(out, denyDir{Dir: path.Join(compat.StateDir, e.DirName), Track: e.Slug})
	}
	return out
}
