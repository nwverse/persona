package node

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// Claude's desktop app keeps an index of its Claude Code sessions, one JSON per session:
// ~/Library/Application Support/Claude/claude-code-sessions/<…>/<…>/local_<id>.json (Claude-3p for the build on a
// third-party provider). Each names the transcript the session runs (cliSessionId), whether the session is archived
// (isArchived), and the transcripts it ran before (priorCliSessionIds: it went on in a new one). Its sidebar lists each
// unarchived session once (a side session nested under its parent), however long it has been quiet. The offline list
// follows it (#3304): it lists the transcript each unarchived session runs whatever its age, on a count of its own (with
// the entry's title and directory where the transcript has none yet), and leaves out the transcripts of archived
// sessions, and a session's earlier transcripts once the one it runs now is listed. Only what an entry says is acted on:
// a transcript no entry names, or an entry that cannot be read, hides nothing; at worst a session shows twice, never not
// at all. The saved-environment mirror supplies native project names and folder associations. Neither reader opens
// the app's config file, which holds credentials.
//
// A session another one started (a side chat, a /btw question, a session spawned for a task) names that one in
// spawnedFrom.sessionId (its local_<id>), and the app's sidebar nests it there. The list draws it the same way (#5935):
// its row names the parent's current transcript in parent_agent_id while both are listed (linkSideSessions), and stays a
// session of its own. An entry without that id, or whose parent is archived or not in the index, links nothing.

// claudeDesktopDirs are the desktop apps' data directories under home (a variable: tests use their own).
// Windows can redirect AppData, but another node's home must never read this user's desktop index.
var claudeDesktopDirs = func(home string) []string {
	local, roaming := "", ""
	if runtime.GOOS == "windows" {
		if own, err := os.UserHomeDir(); err == nil && strings.EqualFold(filepath.Clean(home), filepath.Clean(own)) {
			local, roaming = os.Getenv("LOCALAPPDATA"), os.Getenv("APPDATA")
		}
	}
	return claudeDesktopDataDirs(home, runtime.GOOS, local, roaming)
}

func claudeDesktopDataDirs(home, platform, local, roaming string) []string {
	if home == "" || !filepath.IsAbs(home) {
		return nil
	}
	if platform == "darwin" {
		base := filepath.Join(home, "Library", "Application Support")
		return []string{filepath.Join(base, "Claude"), filepath.Join(base, "Claude-3p")}
	}
	if platform != "windows" {
		return nil
	}
	bases := []string{filepath.Join(home, "AppData", "Local"), filepath.Join(home, "AppData", "Roaming")}
	for _, base := range []string{local, roaming} {
		if filepath.IsAbs(base) {
			bases = append(bases, base)
		}
	}
	// Store installations may virtualize either AppData directory under their package's LocalCache.
	for _, base := range []string{filepath.Join(home, "AppData", "Local"), local} {
		if !filepath.IsAbs(base) {
			continue
		}
		packages, _ := filepath.Glob(filepath.Join(base, "Packages", "Claude_*", "LocalCache"))
		for _, p := range packages {
			bases = append(bases, filepath.Join(p, "Local"), filepath.Join(p, "Roaming"))
		}
	}
	var dirs []string
	seen := map[string]bool{}
	for _, base := range bases {
		for _, name := range []string{"Claude", "Claude-3p", "Claude-Data"} {
			p := filepath.Clean(filepath.Join(base, name))
			key := strings.ToLower(p)
			if !seen[key] {
				seen[key] = true
				dirs = append(dirs, p)
			}
		}
	}
	return dirs
}

type claudeDesktopEntry struct {
	transcript  string   // cliSessionId
	archived    bool     // isArchived
	prior       []string // priorCliSessionIds
	title, cwd  string   // as the sidebar shows it, and where it runs
	originCwd   string   // the original project directory, before the app creates a worktree
	local       string   // the app's own id of the session (sessionId, local_<uuid>): its link opens it (claudedesktop_open.go)
	environment string   // the saved local environment's id, when the app records an explicit association
	parent      string   // the local_<uuid> of the session that started this one (spawnedFrom.sessionId), or ""

	// focused and turnEdge (ms, 0 unknown): when the app last brought the session into view (lastFocusedAt), and when a
	// turn of it last began or ended (lastTurnEdgeAt). claudedesktop_unread.go reads them.
	focused, turnEdge int64
}

type claudeDesktopFile struct {
	mtime time.Time
	size  int64
	entry claudeDesktopEntry // transcript "": unreadable, names nothing
}

var claudeDesktopFiles = struct {
	sync.Mutex
	byPath map[string]claudeDesktopFile
}{byPath: map[string]claudeDesktopFile{}}

// claudeDesktopIndex is what the desktop apps' session index says about transcripts.
type claudeDesktopIndex struct {
	visible      map[string]bool                     // a session's current transcript → some entry naming it is unarchived
	earlier      map[string][]claudeDesktopNow       // an earlier transcript → the sessions that ran it before
	about        map[string]claudeDesktopAbout       // an unarchived session's current transcript → its title and directory
	environments map[string]claudeDesktopEnvironment // profile/account-qualified saved local environments
	// parents: an unarchived session's current transcript → the current transcript of the unarchived session that
	// started it (spawnedFrom), for linkSideSessions.
	parents map[string]string
	seen    map[string]claudeDesktopSeen // an unarchived session's current transcript → its two times (claudedesktop_unread.go)
	// lastShown (ms) is the latest time the app brought any unarchived session into view: the session it shows now
	// was brought into view then (claudeDesktopIndex.shown).
	lastShown int64
}

type claudeDesktopAbout struct{ title, cwd, originCwd, local, environment string }

// claudeDesktopNow is a session that went on from an earlier transcript: the one it runs now, and whether it is archived.
type claudeDesktopNow struct {
	transcript string
	archived   bool
}

// readClaudeDesktopIndex reads the index under home; a file is parsed again only when it changed.
func readClaudeDesktopIndex(home string) claudeDesktopIndex {
	var paths []string
	x := claudeDesktopIndex{visible: map[string]bool{}, earlier: map[string][]claudeDesktopNow{}, about: map[string]claudeDesktopAbout{},
		environments: map[string]claudeDesktopEnvironment{}, parents: map[string]string{}, seen: map[string]claudeDesktopSeen{}}
	for _, dir := range claudeDesktopDirs(home) {
		found, _ := filepath.Glob(filepath.Join(dir, "claude-code-sessions", "*", "*", "local_*.json"))
		paths = append(paths, found...)
		if environments, err := readClaudeDesktopEnvironments(dir); err == nil {
			for _, environment := range environments {
				x.environments[claudeDesktopEnvironmentID(dir, environment.AccountID, environment.ID)] = environment
			}
		}
	}
	claudeDesktopFiles.Lock()
	defer claudeDesktopFiles.Unlock()
	current := make(map[string]bool, len(paths))
	byLocal := map[string]claudeDesktopEntry{} // the readable entries by the app's own id, to find a side session's parent
	var started []claudeDesktopEntry           // the unarchived entries another session started
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil || st.Size() > 4<<20 {
			continue
		}
		current[p] = true
		f, ok := claudeDesktopFiles.byPath[p]
		if !ok || !f.mtime.Equal(st.ModTime()) || f.size != st.Size() {
			f = claudeDesktopFile{mtime: st.ModTime(), size: st.Size(), entry: readClaudeDesktopEntry(p)}
			claudeDesktopFiles.byPath[p] = f
		}
		e := f.entry
		if e.transcript == "" {
			continue
		}
		x.visible[e.transcript] = x.visible[e.transcript] || !e.archived
		if !e.archived {
			environment := ""
			if e.environment != "" {
				accountDir := filepath.Dir(filepath.Dir(p))
				profileDir := filepath.Dir(filepath.Dir(accountDir))
				environment = claudeDesktopEnvironmentID(profileDir, filepath.Base(accountDir), e.environment)
			}
			x.about[e.transcript] = claudeDesktopAbout{title: e.title, cwd: e.cwd, originCwd: e.originCwd, local: e.local, environment: environment}
			x.seen[e.transcript] = claudeDesktopSeen{focused: e.focused, turnEdge: e.turnEdge}
			x.lastShown = max(x.lastShown, e.focused)
			if e.parent != "" {
				started = append(started, e)
			}
		}
		if e.local != "" {
			byLocal[e.local] = e
		}
		for _, old := range e.prior {
			x.earlier[old] = append(x.earlier[old], claudeDesktopNow{e.transcript, e.archived})
		}
	}
	for _, e := range started {
		if p, ok := byLocal[e.parent]; ok && !p.archived && p.transcript != e.transcript {
			x.parents[e.transcript] = p.transcript
		}
	}
	for p := range claudeDesktopFiles.byPath {
		if !current[p] {
			delete(claudeDesktopFiles.byPath, p)
		}
	}
	return x
}

func readClaudeDesktopEntry(path string) claudeDesktopEntry {
	b, err := os.ReadFile(path)
	if err != nil {
		return claudeDesktopEntry{}
	}
	var raw struct {
		Transcript  string          `json:"cliSessionId"`
		Archived    bool            `json:"isArchived"`
		Prior       []string        `json:"priorCliSessionIds"`
		Title       string          `json:"title"`
		Cwd         string          `json:"cwd"`
		OriginCwd   string          `json:"originCwd"`
		Local       string          `json:"sessionId"`
		Environment json.RawMessage `json:"startedFromEnvironmentId"`
		// Read on its own: a spawnedFrom of another shape leaves the entry readable, with no parent.
		SpawnedFrom json.RawMessage `json:"spawnedFrom"`
		Focused     float64         `json:"lastFocusedAt"`
		TurnEdge    float64         `json:"lastTurnEdgeAt"`
	}
	if json.Unmarshal(b, &raw) != nil || !validID(raw.Transcript) {
		return claudeDesktopEntry{}
	}
	var spawned struct {
		Local string `json:"sessionId"`
	}
	if len(raw.SpawnedFrom) > 0 && json.Unmarshal(raw.SpawnedFrom, &spawned) != nil {
		spawned.Local = ""
	}
	cwd := raw.Cwd
	if !filepath.IsAbs(cwd) {
		cwd = ""
	}
	origin := raw.OriginCwd
	if !filepath.IsAbs(origin) {
		origin = ""
	}
	var environment string
	if json.Unmarshal(raw.Environment, &environment) != nil || len(environment) > 128 {
		environment = ""
	}
	return claudeDesktopEntry{transcript: raw.Transcript, archived: raw.Archived, prior: raw.Prior, title: raw.Title, cwd: cwd, originCwd: origin, environment: environment,
		local: localID(raw.Local), parent: localID(spawned.Local), focused: int64(raw.Focused), turnEdge: int64(raw.TurnEdge)}
}

// localID is id when it is the app's own form of a session id (local_<uuid>), else "": it goes into a link.
func localID(id string) string {
	if !strings.HasPrefix(id, "local_") || !validID(strings.TrimPrefix(id, "local_")) {
		return ""
	}
	return id
}

// listed: transcript id is the one an unarchived session of the desktop app runs: the sidebar lists it, so the offline
// list does whatever its age (up to maxDesktopOffline, the newest first), on a count of its own (#3304).
func (x claudeDesktopIndex) listed(id string) bool { return x.visible[id] }

// maxDesktopOffline bounds the desktop app's sessions the offline list shows (the newest, whatever their age); they are
// counted on their own, not toward the 80 of the others.
const maxDesktopOffline = 100

// hidden: the offline list leaves transcript id out as the desktop app's sidebar does. shown are the transcripts the
// list has shown so far (newest first, so a session's current one comes before its earlier ones).
func (x claudeDesktopIndex) hidden(id string, shown map[string]bool) bool {
	if visible, named := x.visible[id]; named {
		return !visible // a session's current transcript: hidden while every entry naming it is archived
	}
	sessions := x.earlier[id]
	if len(sessions) == 0 {
		return false
	}
	open := false
	for _, s := range sessions {
		if !s.archived {
			open = true
			if shown[s.transcript] {
				return true // its session is listed through the transcript it runs now
			}
		}
	}
	return !open // only archived sessions ran it
}

// linkSideSessions draws each of the desktop app's side sessions under the session that started it (parent_agent_id,
// #5935): parents maps a side session's transcript to its parent's (claudeDesktopIndex.parents). The parent must be a
// row of list too; a session whose parents lead back to it (a loop the app should never write) stays a root. Each
// link is set on the row's own copy of its hierarchy (SetParentAgentID), never on one another copy shares.
func linkSideSessions(list []proto.Session, parents map[string]string) {
	if len(parents) == 0 {
		return
	}
	listed := make(map[string]bool, len(list))
	for i := range list {
		listed[list[i].SessionID] = true
	}
	for i := range list {
		s := &list[i]
		if p := parents[s.SessionID]; p != "" && listed[p] && !loopsBack(s.SessionID, parents) {
			s.SetParentAgentID(p)
		}
	}
}

// loopsBack: following parents up from id comes back to id.
func loopsBack(id string, parents map[string]string) bool {
	seen := map[string]bool{}
	for p := parents[id]; p != ""; p = parents[p] {
		if p == id {
			return true
		}
		if seen[p] {
			return false // a loop above id, which does not contain it
		}
		seen[p] = true
	}
	return false
}
