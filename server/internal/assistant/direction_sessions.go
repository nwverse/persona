package assistant

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/identity"
	"github.com/nwverse/persona/server/internal/proto"
)

// Owner-filed sessions of a direction (#11391). The book is a lazy settings record, like conversation tags: one entry
// per canonical computer/session, with an empty direction meaning explicitly on its own. Filing changes no task,
// conversation, Agent, model call or prompt; the Hub only relays the route and advertises the manager's capability.
const directionSessionsKey = "assistant.direction_sessions"

const directionSessionsPerDirection = 50

type directionFiling struct {
	Direction string    `json:"direction"`
	By        string    `json:"by"`
	At        time.Time `json:"at"`
	Title     string    `json:"title"`
	// Parent is the session a Codex sub-agent session ran under when it was filed (proto.Session.ParentSessionID), so
	// placement rule 1 can follow it while its computer does not list it.
	Parent string `json:"parent,omitempty"`
}

type directionSessionsBook struct {
	mu     sync.Mutex
	loaded bool
	// m is by runtimeListID(machine, session). An entry made before its computer's id was absorbed by another keeps the
	// old id until the session's next write (directionEntries).
	m map[string]*directionFiling
}

// directionFilingResult is the write's answer: what the book holds for the session now and held before (null: no
// entry), and whether that changed.
type directionFilingResult struct {
	Direction *string `json:"direction"`
	Previous  *string `json:"previous"`
	Changed   bool    `json:"changed"`
}

var (
	errDirectionRole            = i18n.Err("assistant-direction_sessions.full_access_required")
	errDirectionMachineNotFound = i18n.Err("assistant-direction_sessions.machine_not_found")
	errDirectionSessionNotFound = i18n.Err("assistant-direction_sessions.session_not_found")
	errDirectionNotFound        = i18n.Err("assistant-direction_sessions.direction_not_found")
	errDirectionEnded           = i18n.Err("assistant-direction_sessions.direction_ended")
	errDirectionFull            = i18n.Err("assistant-direction_sessions.direction_full", i18n.Args{"max": directionSessionsPerDirection})
	errDirectionSupervised      = i18n.Err("assistant-direction_sessions.supervised_task")
	errDirectionMain            = i18n.Err("assistant-direction_sessions.main_conversation")
	errDirectionBackground      = i18n.Err("assistant-direction_sessions.background_supervisor")
	errDirectionLeader          = i18n.Err("assistant-direction_sessions.leader_conversation")
	errDirectionSpace           = i18n.Err("assistant-direction_sessions.space_mismatch")
	errDirectionUnavailable     = i18n.Err("assistant-direction_sessions.unavailable")
	errDirectionEndedTaskCursor = i18n.Err("assistant-direction_sessions.ended_task_not_found")
)

func directionFilingRefusal(w http.ResponseWriter, r *http.Request, status int, code string, err error) {
	lang := i18n.OfWriter(w)
	if lang == "" {
		lang = i18n.FromRequest(r)
	}
	apierr.WriteBody(w, status, apierr.LocalizedBody(status, code, err, lang))
}

// directionSessionRoutes: owner filing and the single-direction read. The literal tree and offers routes remain more
// specific than GET …/directions/{id} in net/http's ServeMux.
func (s *Service) directionSessionRoutes() {
	s.handle("PUT /api/v1/assistant/sessions/{mid}/{sid}/direction", s.serveDirectionFiling)
	s.handle("GET /api/v1/assistant/directions/{id}", s.serveDirectionRead)
}

// directionTarget is the write's direction: a direction's id, "" for on its own (also when the body has none, as
// before null was taken), or nil for null, which takes the session's entry out of the book.
func directionTarget(raw json.RawMessage) (*string, error) {
	raw = bytes.TrimSpace(raw)
	switch {
	case len(raw) == 0:
		return new(string), nil
	case string(raw) == "null":
		return nil, nil
	}
	var id string
	if err := json.Unmarshal(raw, &id); err != nil {
		return nil, err
	}
	return &id, nil
}

// serveDirectionFiling is the owner's write. Taking an entry out (null) asks nothing of the session, so an entry
// whose session its computer no longer lists, or whose computer left, can always go. Such a session can still be set
// on its own, with every check that needs no row; moving it to a direction needs its row (its space).
func (s *Service) serveDirectionFiling(w http.ResponseWriter, r *http.Request) {
	if !fullRole(r) {
		directionFilingRefusal(w, r, http.StatusForbidden, "full_access_required", errDirectionRole)
		return
	}
	var in struct {
		Direction json.RawMessage `json:"direction"`
	}
	if err := decodeBody(w, r, &in, 1<<16); err != nil {
		apierr.Write(w, http.StatusBadRequest, err)
		return
	}
	target, err := directionTarget(in.Direction)
	if err != nil {
		apierr.Write(w, http.StatusBadRequest, err)
		return
	}
	ctx := r.Context()
	machine, sid := s.canonical(r.PathValue("mid")), r.PathValue("sid")
	if machine == "" || !validSessionID(sid) {
		directionFilingRefusal(w, r, http.StatusNotFound, "session_not_found", errDirectionSessionNotFound)
		return
	}
	key := [2]string{machine, sid}
	if target == nil {
		out, err := s.fileDirectionSession(ctx, key, nil, nil)
		s.answerDirectionFiling(w, out, err)
		return
	}
	records, err := s.directionSessionRecords(ctx)
	if err != nil {
		apierr.Write(w, http.StatusServiceUnavailable, errDirectionUnavailable)
		return
	}
	entry, filed := s.directionEntries(records)[key]
	list, listErr := s.sessions(ctx, machine)
	var row proto.Session
	listed := false
	parents := map[string]string{}
	for _, c := range list {
		if c.ParentSessionID != "" {
			parents[c.SessionID] = c.ParentSessionID
		}
		if c.SessionID == sid && !c.IsDeleted() {
			row, listed = c, true
		}
	}
	switch {
	case listed && identity.IsAssistantDir(row.Cwd) && !identity.IsAssistantDirOf(row.Cwd, s.id):
		directionFilingRefusal(w, r, http.StatusNotFound, "session_not_found", errDirectionSessionNotFound)
		return
	case listed:
	case !filed && errors.Is(listErr, ErrNoDevice):
		directionFilingRefusal(w, r, http.StatusNotFound, "machine_not_found", errDirectionMachineNotFound)
		return
	case !filed && listErr != nil:
		apierr.Call(w, listErr)
		return
	case !filed || *target != "":
		directionFilingRefusal(w, r, http.StatusNotFound, "session_not_found", errDirectionSessionNotFound)
		return
	default: // in the book but not listed now: kept as it was filed
		row.Title, row.ParentSessionID = entry.Title, entry.Parent
	}
	if err := s.ensureTodosLoaded(ctx); err != nil {
		apierr.Write(w, http.StatusServiceUnavailable, errDirectionUnavailable)
		return
	}
	cfg := s.settings(ctx)
	rules, err := s.directionRulesNow(ctx, cfg)
	if err != nil {
		apierr.Write(w, http.StatusServiceUnavailable, errDirectionUnavailable)
		return
	}
	switch {
	case sid == cfg.Main && machine == cfg.mainRouteMachine():
		directionFilingRefusal(w, r, http.StatusConflict, "direction_main_conversation", errDirectionMain)
		return
	case sid == cfg.BackgroundSupervisor && sid != cfg.Main && machine == cmp.Or(cfg.BackgroundSupervisorMachineID, cfg.MachineID):
		directionFilingRefusal(w, r, http.StatusConflict, "direction_background_supervisor", errDirectionBackground)
		return
	case rules.special[key]:
		directionFilingRefusal(w, r, http.StatusConflict, "direction_leader_conversation", errDirectionLeader)
		return
	case rules.followers.follows(key, func(id string) string { return cond(id == sid, row.ParentSessionID, parents[id]) }):
		directionFilingRefusal(w, r, http.StatusConflict, "direction_supervised_task", errDirectionSupervised)
		return
	}
	if *target != "" {
		current := *target != cfg.Main && slices.Contains(cfg.Leaders, *target)
		if !current {
			_, known, ok := s.directionSnapshot(ctx, *target)
			switch {
			case !ok:
				apierr.Write(w, http.StatusServiceUnavailable, errDirectionUnavailable)
			case known:
				directionFilingRefusal(w, r, http.StatusConflict, "direction_ended", errDirectionEnded)
			default:
				directionFilingRefusal(w, r, http.StatusNotFound, "direction_not_found", errDirectionNotFound)
			}
			return
		}
		if space, known := s.conversationSpace(ctx, *target); known && spaceOf(row) != space {
			directionFilingRefusal(w, r, http.StatusConflict, "direction_space_mismatch", errDirectionSpace)
			return
		}
	}
	next := &directionFiling{Direction: *target, By: titleByUser, At: time.Now().UTC(), Title: row.Title, Parent: row.ParentSessionID}
	held := func(records map[string]directionFiling) (int, error) {
		return s.directionHeld(ctx, rules, records, *target, key)
	}
	out, err := s.fileDirectionSession(ctx, key, next, held)
	if errors.Is(err, errDirectionFull) {
		directionFilingRefusal(w, r, http.StatusConflict, "direction_full", errDirectionFull)
		return
	}
	s.answerDirectionFiling(w, out, err)
}

func (s *Service) answerDirectionFiling(w http.ResponseWriter, out directionFilingResult, err error) {
	if err != nil {
		apierr.Write(w, http.StatusServiceUnavailable, errDirectionUnavailable)
		return
	}
	if out.Changed {
		s.directionTreeSoon()
	}
	apierr.WriteJSON(w, out)
}

// directionSpecialSessions includes current and ended leader conversations, plus main and the background supervisor,
// and the ids that are or were directions. Special conversations are never listed as filed sessions, and only a known
// direction's recorded opener counts as a previous or implicit placement.
func (s *Service) directionSpecialSessions(ctx context.Context, cfg Settings) (map[[2]string]bool, map[string]bool, error) {
	out := map[[2]string]bool{}
	knownDirections := map[string]bool{}
	if cfg.Main != "" {
		out[[2]string{cfg.mainRouteMachine(), cfg.Main}] = true
	}
	if cfg.BackgroundSupervisor != "" {
		out[[2]string{cmp.Or(cfg.BackgroundSupervisorMachineID, cfg.MachineID), cfg.BackgroundSupervisor}] = true
	}
	for _, id := range cfg.Leaders {
		if id != "" && id != cfg.Main {
			out[[2]string{cfg.MachineID, id}] = true
			knownDirections[id] = true
		}
	}
	b := &s.directions
	b.mu.Lock()
	defer b.mu.Unlock()
	if !s.directionsLocked(ctx) {
		return nil, nil, errors.New("directions could not be read")
	}
	for id, d := range b.m {
		if d == nil {
			continue
		}
		out[[2]string{cmp.Or(d.MachineID, cfg.MachineID), id}] = true
		knownDirections[id] = true
	}
	return out, knownDirections, nil
}

// directionSnapshot returns a copy of one direction record. A current leader without its first sync has no record yet.
func (s *Service) directionSnapshot(ctx context.Context, id string) (direction, bool, bool) {
	b := &s.directions
	b.mu.Lock()
	defer b.mu.Unlock()
	if !s.directionsLocked(ctx) {
		return direction{}, false, false
	}
	d, ok := b.m[id]
	if !ok || d == nil {
		return direction{}, false, true
	}
	return *d, true, true
}

// openSupervisedTask: a task placement rule 1 holds an Agent to (#11391): not done or cancelled (a failed start still
// waiting for its owner is not done) and supervised. Whatever the To-dos page files it under: a shelved task and one
// the owner has seen hold their Agents too.
func openSupervisedTask(t *Todo) bool {
	return t != nil && t.CancelledAt == nil && !t.closedForGood() && t.Supervision != nil && t.Supervision.Supervisor != ""
}

// taskFollowers are the sessions placement rule 1 gives to their task: the active Agents of the open supervised tasks,
// by canonical computer and session. The write, the single-direction read and the tree ask follows, so the three
// never disagree on which Agents follow a task.
type taskFollowers map[[2]string]bool

func (s *Service) taskFollowers() taskFollowers {
	var agents [][2]string
	s.todos.mu.RLock()
	for _, t := range s.todos.todos {
		if !openSupervisedTask(t) {
			continue
		}
		for _, a := range t.Agents {
			if a.active() {
				agents = append(agents, [2]string{a.MachineID, a.SessionID})
			}
		}
	}
	s.todos.mu.RUnlock()
	out := make(taskFollowers, len(agents))
	for _, k := range agents {
		out[[2]string{s.canonical(k[0]), k[1]}] = true
	}
	return out
}

// directionParentDepth is how far follows looks up a session's parents: a sub-agent's sub-agents nest that deep at most,
// and a loop of parents ends there.
const directionParentDepth = 8

// follows: session k is an Agent of an open supervised task, or a sub-agent session under one (parentOf names a
// session's parent on the same computer, "" for none), as the tree nests it under that Agent.
func (f taskFollowers) follows(k [2]string, parentOf func(sid string) string) bool {
	for range directionParentDepth {
		if f[k] {
			return true
		}
		parent := parentOf(k[1])
		if parent == "" || parent == k[1] {
			return false
		}
		k[1] = parent
	}
	return false
}

func (s *Service) directionSessionsLocked(ctx context.Context) bool {
	b := &s.directionFilings
	if b.loaded {
		return true
	}
	m := map[string]*directionFiling{}
	if st := s.store(); st != nil {
		if _, err := st.Setting(ctx, directionSessionsKey, &m); err != nil {
			s.logf("load direction sessions: %v", err)
			return false
		}
	}
	if m == nil {
		m = map[string]*directionFiling{}
	}
	b.m, b.loaded = m, true
	return true
}

// directionSessionRecords returns a value copy: readers never hold or mutate the book's live map.
func (s *Service) directionSessionRecords(ctx context.Context) (map[string]directionFiling, error) {
	b := &s.directionFilings
	b.mu.Lock()
	defer b.mu.Unlock()
	if !s.directionSessionsLocked(ctx) {
		return nil, errors.New("direction sessions could not be read")
	}
	return directionRecordsOf(b.m), nil
}

func (s *Service) directionOriginRecords(ctx context.Context) (map[string]convOrigin, error) {
	b := &s.origins
	b.mu.Lock()
	defer b.mu.Unlock()
	if !s.originsLocked(ctx) {
		return nil, errors.New("conversation origins could not be read")
	}
	out := make(map[string]convOrigin, len(b.m))
	for id, origin := range b.m {
		if origin != nil {
			out[id] = *origin
		}
	}
	return out, nil
}

// directionEntry is one of the book's entries and the key it is kept under.
type directionEntry struct {
	key string
	directionFiling
}

// directionEntries are the book's entries by canonical computer and session, one each. A session filed under a computer
// id another computer has absorbed since has its entry under the old id, and a move from before this rule could leave
// a second one under the new id: the newest is the session's (the canonical key's on a tie), and its next write keeps
// only that one, under the canonical id (fileDirectionSession).
func (s *Service) directionEntries(records map[string]directionFiling) map[[2]string]directionEntry {
	out := make(map[[2]string]directionEntry, len(records))
	for key, record := range records {
		var tuple [2]string
		if json.Unmarshal([]byte(key), &tuple) != nil {
			continue
		}
		k := [2]string{s.canonical(tuple[0]), tuple[1]}
		e := directionEntry{key: key, directionFiling: record}
		if had, ok := out[k]; ok && !directionNewer(e, had, runtimeListID(k[0], k[1])) {
			continue
		}
		out[k] = e
	}
	return out
}

// directionNewer: entry a is the session's rather than b (directionEntries); canonical is the key a write keeps.
func directionNewer(a, b directionEntry, canonical string) bool {
	if c := a.At.Compare(b.At); c != 0 {
		return c > 0
	}
	if (a.key == canonical) != (b.key == canonical) {
		return a.key == canonical
	}
	return a.key < b.key
}

// directionRecordsOf is a value copy of the live book (its lock held).
func directionRecordsOf(m map[string]*directionFiling) map[string]directionFiling {
	out := make(map[string]directionFiling, len(m))
	for key, record := range m {
		if record != nil {
			out[key] = *record
		}
	}
	return out
}

// directionKeysOf are the keys the book keeps session k's entries under (the canonical one, and old ones of an
// absorbed computer id).
func (s *Service) directionKeysOf(records map[string]directionFiling, k [2]string) []string {
	var keys []string
	for key := range records {
		var tuple [2]string
		if json.Unmarshal([]byte(key), &tuple) == nil && tuple[1] == k[1] && s.canonical(tuple[0]) == k[0] {
			keys = append(keys, key)
		}
	}
	return keys
}

// fileDirectionSession serializes the complete read/check/write of session k's entry: next nil takes it out. held
// counts the sessions the target direction holds besides k, as the read places them; it runs under the book's lock,
// with the book as it is, only when the session moves into a direction. Copy-on-write keeps a failed save invisible
// even before the next lazy reload; loaded=false also forces that reload, as the other settings books do.
func (s *Service) fileDirectionSession(ctx context.Context, k [2]string, next *directionFiling,
	held func(map[string]directionFiling) (int, error)) (directionFilingResult, error) {
	st := s.store()
	if st == nil {
		return directionFilingResult{}, errors.New("no database")
	}
	b := &s.directionFilings
	b.mu.Lock()
	defer b.mu.Unlock()
	if !s.directionSessionsLocked(ctx) {
		return directionFilingResult{}, errors.New("direction sessions could not be read")
	}
	records := directionRecordsOf(b.m)
	keys := s.directionKeysOf(records, k)
	canonical := runtimeListID(k[0], k[1])
	var out directionFilingResult
	var current *directionFiling
	if e, ok := s.directionEntries(records)[k]; ok {
		current = &e.directionFiling
		out.Previous = &e.Direction
	}
	if next != nil {
		out.Direction = &next.Direction
	}
	out.Changed = (current == nil) != (next == nil) || current != nil && next != nil && current.Direction != next.Direction
	if !out.Changed && (len(keys) == 0 || len(keys) == 1 && keys[0] == canonical) {
		return out, nil
	}
	if out.Changed && next != nil && next.Direction != "" {
		n, err := held(records)
		if err != nil {
			return directionFilingResult{}, err
		}
		if n >= directionSessionsPerDirection {
			return directionFilingResult{}, errDirectionFull
		}
	}
	m := maps.Clone(b.m)
	for _, key := range keys {
		delete(m, key)
	}
	switch {
	case out.Changed && next != nil:
		m[canonical] = next
	case !out.Changed && current != nil: // only its old keys go: the entry stays as it is, under the canonical id
		m[canonical] = current
	}
	if err := st.PutSetting(ctx, directionSessionsKey, m); err != nil {
		b.loaded = false
		return directionFilingResult{}, err
	}
	b.m = m
	return out, nil
}

// dropDirectionSession removes only an assistant conversation's filing (its entries under every id of its computer).
// Hub-side reversible Agent deletion has no manager notice, so those filings deliberately remain.
func (s *Service) dropDirectionSession(ctx context.Context, machine, sid string) error {
	st := s.store()
	if st == nil {
		return errors.New("no database")
	}
	b := &s.directionFilings
	b.mu.Lock()
	if !s.directionSessionsLocked(ctx) {
		b.mu.Unlock()
		return errors.New("direction sessions could not be read")
	}
	keys := s.directionKeysOf(directionRecordsOf(b.m), [2]string{s.canonical(machine), sid})
	if len(keys) == 0 {
		b.mu.Unlock()
		return nil
	}
	next := maps.Clone(b.m)
	for _, key := range keys {
		delete(next, key)
	}
	if err := st.PutSetting(ctx, directionSessionsKey, next); err != nil {
		b.loaded = false
		b.mu.Unlock()
		return err
	}
	b.m = next
	b.mu.Unlock()
	s.directionTreeSoon()
	return nil
}
