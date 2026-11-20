package assistant

import (
	"cmp"
	"context"
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
}

type directionSessionsBook struct {
	mu     sync.Mutex
	loaded bool
	m      map[string]*directionFiling // runtimeListID(machine, session)
}

type directionFilingResult struct {
	Direction string `json:"direction"`
	Previous  string `json:"previous"`
	Changed   bool   `json:"changed"`
}

var (
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
	s.handle("PUT /api/v1/assistant/sessions/{mid}/{sid}/direction", func(w http.ResponseWriter, r *http.Request) {
		if !fullRole(r) {
			apierr.Write(w, http.StatusForbidden, i18n.Err("assistant-model_pick.err_role"))
			return
		}
		var in struct {
			Direction string `json:"direction"`
		}
		if err := decodeBody(w, r, &in, 1<<16); err != nil {
			apierr.Write(w, http.StatusBadRequest, err)
			return
		}
		ctx := r.Context()
		machine, sid := s.canonical(r.PathValue("mid")), r.PathValue("sid")
		if machine == "" || !validSessionID(sid) {
			directionFilingRefusal(w, r, http.StatusNotFound, "session_not_found", errDirectionSessionNotFound)
			return
		}
		list, err := s.sessions(ctx, machine)
		if err != nil {
			if errors.Is(err, ErrNoDevice) {
				directionFilingRefusal(w, r, http.StatusNotFound, "machine_not_found", errDirectionMachineNotFound)
			} else {
				apierr.Call(w, err)
			}
			return
		}
		var row proto.Session
		found := false
		for _, c := range list {
			if c.SessionID == sid {
				row, found = c, true
				break
			}
		}
		if !found || identity.IsAssistantDir(row.Cwd) && !identity.IsAssistantDirOf(row.Cwd, s.id) {
			directionFilingRefusal(w, r, http.StatusNotFound, "session_not_found", errDirectionSessionNotFound)
			return
		}
		cfg := s.settings(ctx)
		special, knownDirections, err := s.directionSpecialSessions(ctx, cfg)
		if err != nil {
			apierr.Write(w, http.StatusServiceUnavailable, errDirectionUnavailable)
			return
		}
		key := [2]string{machine, sid}
		switch {
		case sid == cfg.Main && machine == cfg.mainRouteMachine():
			directionFilingRefusal(w, r, http.StatusConflict, "direction_main_conversation", errDirectionMain)
			return
		case sid == cfg.BackgroundSupervisor && sid != cfg.Main && machine == cmp.Or(cfg.BackgroundSupervisorMachineID, cfg.MachineID):
			directionFilingRefusal(w, r, http.StatusConflict, "direction_background_supervisor", errDirectionBackground)
			return
		case special[key]:
			directionFilingRefusal(w, r, http.StatusConflict, "direction_leader_conversation", errDirectionLeader)
			return
		}
		if err := s.ensureTodosLoaded(ctx); err != nil {
			apierr.Write(w, http.StatusServiceUnavailable, errDirectionUnavailable)
			return
		}
		if s.directionSupervisedOpenSession(machine, sid) {
			directionFilingRefusal(w, r, http.StatusConflict, "direction_supervised_task", errDirectionSupervised)
			return
		}
		if in.Direction != "" {
			current := in.Direction != cfg.Main && slices.Contains(cfg.Leaders, in.Direction)
			if !current {
				_, known, ok := s.directionSnapshot(ctx, in.Direction)
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
			if space, known := s.conversationSpace(ctx, in.Direction); known && spaceOf(row) != space {
				directionFilingRefusal(w, r, http.StatusConflict, "direction_space_mismatch", errDirectionSpace)
				return
			}
		}
		previousOrigin, err := s.directionPreviousOrigin(ctx, machine, sid)
		if err != nil {
			apierr.Write(w, http.StatusServiceUnavailable, errDirectionUnavailable)
			return
		}
		if !knownDirections[previousOrigin] {
			previousOrigin = ""
		}
		out, err := s.fileDirectionSession(ctx, machine, sid, in.Direction, previousOrigin, row.Title)
		if err != nil {
			if errors.Is(err, errDirectionFull) {
				directionFilingRefusal(w, r, http.StatusConflict, "direction_full", errDirectionFull)
			} else {
				apierr.Write(w, http.StatusServiceUnavailable, errDirectionUnavailable)
			}
			return
		}
		if out.Changed {
			s.directionTreeSoon()
		}
		apierr.WriteJSON(w, out)
	})
	s.handle("GET /api/v1/assistant/directions/{id}", s.serveDirectionRead)
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

func (s *Service) directionSupervisedOpenSession(machine, sid string) bool {
	s.todos.mu.RLock()
	defer s.todos.mu.RUnlock()
	id, ok := s.todos.activeBySession[[2]string{machine, sid}]
	if !ok {
		return false
	}
	t := s.todos.todos[id]
	return t != nil && t.CancelledAt == nil && !t.closedForGood() && t.Supervision != nil && t.Supervision.Supervisor != ""
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
	out := make(map[string]directionFiling, len(b.m))
	for key, record := range b.m {
		if record != nil {
			out[key] = *record
		}
	}
	return out, nil
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

func (s *Service) directionPreviousOrigin(ctx context.Context, machine, sid string) (string, error) {
	origins, err := s.directionOriginRecords(ctx)
	if err != nil {
		return "", err
	}
	origin, ok := origins[sid]
	if !ok || origin.Origin != OriginSpawned || origin.MachineID != "" && s.canonical(origin.MachineID) != machine {
		return "", nil
	}
	return origin.Ref, nil
}

// fileDirectionSession serializes the complete read/check/write. Copy-on-write keeps a failed save invisible even
// before the next lazy reload; loaded=false also forces that reload, as the other settings books do.
func (s *Service) fileDirectionSession(ctx context.Context, machine, sid, direction, previousOrigin, title string) (directionFilingResult, error) {
	st := s.store()
	if st == nil {
		return directionFilingResult{}, errors.New("no database")
	}
	key := runtimeListID(machine, sid)
	b := &s.directionFilings
	b.mu.Lock()
	defer b.mu.Unlock()
	if !s.directionSessionsLocked(ctx) {
		return directionFilingResult{}, errors.New("direction sessions could not be read")
	}
	current := b.m[key]
	previous := previousOrigin
	if current != nil {
		previous = current.Direction
	}
	out := directionFilingResult{Direction: direction, Previous: previous, Changed: current == nil || current.Direction != direction}
	if !out.Changed {
		return out, nil
	}
	if direction != "" && (current == nil || current.Direction != direction) {
		count := 0
		for _, record := range b.m {
			if record != nil && record.Direction == direction {
				count++
			}
		}
		if count >= directionSessionsPerDirection {
			return directionFilingResult{}, errDirectionFull
		}
	}
	next := maps.Clone(b.m)
	next[key] = &directionFiling{Direction: direction, By: titleByUser, At: time.Now().UTC(), Title: title}
	if err := st.PutSetting(ctx, directionSessionsKey, next); err != nil {
		b.loaded = false
		return directionFilingResult{}, err
	}
	b.m = next
	return out, nil
}

// dropDirectionSession removes only an assistant conversation's filing. Hub-side reversible Agent deletion has no
// manager notice, so those filings deliberately remain and read as not_listed.
func (s *Service) dropDirectionSession(ctx context.Context, machine, sid string) error {
	st := s.store()
	if st == nil {
		return errors.New("no database")
	}
	key := runtimeListID(s.canonical(machine), sid)
	b := &s.directionFilings
	b.mu.Lock()
	if !s.directionSessionsLocked(ctx) {
		b.mu.Unlock()
		return errors.New("direction sessions could not be read")
	}
	if _, ok := b.m[key]; !ok {
		b.mu.Unlock()
		return nil
	}
	next := maps.Clone(b.m)
	delete(next, key)
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
