package node

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
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
	x := claudeDesktopIndex{visible: map[string]bool{}, earlier: map[string][]claudeDesktopNow{}, about: map[string]claudeDesktopAbout{}, environments: map[string]claudeDesktopEnvironment{}}
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
		}
		for _, old := range e.prior {
			x.earlier[old] = append(x.earlier[old], claudeDesktopNow{e.transcript, e.archived})
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
	}
	if json.Unmarshal(b, &raw) != nil || !validID(raw.Transcript) {
		return claudeDesktopEntry{}
	}
	cwd := raw.Cwd
	if !filepath.IsAbs(cwd) {
		cwd = ""
	}
	origin := raw.OriginCwd
	if !filepath.IsAbs(origin) {
		origin = ""
	}
	local := raw.Local
	if !strings.HasPrefix(local, "local_") || !validID(strings.TrimPrefix(local, "local_")) {
		local = "" // it goes into a link: only the app's own form
	}
	var environment string
	if json.Unmarshal(raw.Environment, &environment) != nil || len(environment) > 128 {
		environment = ""
	}
	return claudeDesktopEntry{transcript: raw.Transcript, archived: raw.Archived, prior: raw.Prior, title: raw.Title, cwd: cwd, originCwd: origin, local: local, environment: environment}
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
