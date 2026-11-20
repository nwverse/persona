package assistant

import (
	"cmp"
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/proto"
)

const (
	directionEndedTasksPage    = 20
	directionEndedTasksMaxPage = 100
)

// directionSession is one owner-filed or leader-opened session. State is the listed session's state, offline when its
// computer could not be read, or not_listed (including a quiet or reversibly hub-deleted session).
type directionSession struct {
	MachineID string     `json:"machine_id"`
	SessionID string     `json:"session_id"`
	Title     string     `json:"title"`
	Source    string     `json:"source"`
	At        *time.Time `json:"at"`
	State     string     `json:"state"`
}

type directionEndedTask struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	State       string     `json:"state"`
	DoneAt      *time.Time `json:"done_at,omitempty"`
	CancelledAt *time.Time `json:"cancelled_at,omitempty"`
	end         time.Time
}

type directionReadResult struct {
	ID         string               `json:"id"`
	Name       string               `json:"name"`
	EndedAt    *time.Time           `json:"ended_at,omitempty"`
	Notes      string               `json:"notes"`
	NotesAt    *time.Time           `json:"notes_at,omitempty"`
	Progress   *leaderProgress      `json:"progress,omitempty"`
	Sessions   []directionSession   `json:"sessions"`
	EndedTasks []directionEndedTask `json:"ended_tasks"`
	More       bool                 `json:"more"`
}

type directionCandidate struct {
	direction string
	machine   string
	session   string
	title     string
	source    string
	at        time.Time
}

func (s *Service) serveDirectionRead(w http.ResponseWriter, r *http.Request) {
	ctx, id := r.Context(), r.PathValue("id")
	if err := s.ensureTodosLoaded(ctx); err != nil {
		apierr.Write(w, http.StatusServiceUnavailable, errDirectionUnavailable)
		return
	}
	cfg := s.settings(ctx)
	current := id != "" && id != cfg.Main && slices.Contains(cfg.Leaders, id)
	record, known, ok := s.directionSnapshot(ctx, id)
	if !ok {
		apierr.Write(w, http.StatusServiceUnavailable, errDirectionUnavailable)
		return
	}
	if !current && !known {
		directionFilingRefusal(w, r, http.StatusNotFound, "direction_not_found", errDirectionNotFound)
		return
	}
	titles, titlesOK := s.conversationTitles()
	if !titlesOK {
		apierr.Write(w, http.StatusServiceUnavailable, errDirectionUnavailable)
		return
	}
	projects, projectsOK := s.projectNames(ctx)
	name := directionName(nil, titles[id].title, "")
	if known {
		name = nameOf(&record, titles[id].title, projects, projectsOK)
	}
	out := directionReadResult{ID: id, Name: name, EndedAt: record.EndedAt, Notes: record.Notes, NotesAt: record.NotesAt,
		Progress: record.Progress, Sessions: []directionSession{}, EndedTasks: []directionEndedTask{}}
	tb, _, _ := s.directionTreeParts(ctx, time.Now())
	groups, err := tb.directionSessionGroups(ctx, cfg)
	if err != nil {
		apierr.Write(w, http.StatusServiceUnavailable, errDirectionUnavailable)
		return
	}
	if sessions := groups[id]; sessions != nil {
		out.Sessions = sessions
	}
	query := r.URL.Query()
	limit, _ := strconv.Atoi(query.Get("limit"))
	if limit <= 0 {
		limit = directionEndedTasksPage
	}
	tasks, more, cursorOK := tb.directionEndedTasks(id, query.Get("title"), query.Get("before"), min(limit, directionEndedTasksMaxPage))
	if !cursorOK {
		directionFilingRefusal(w, r, http.StatusConflict, "ended_task_not_found", errDirectionEndedTaskCursor)
		return
	}
	out.EndedTasks, out.More = tasks, more
	apierr.WriteJSON(w, out)
}

// directionSessionGroups applies the placement order to every filed and leader-opened session. An Agent on an open
// supervised task follows that task instead of its old filing. The full read includes these owner/leader sessions;
// open-task Agents remain represented by their task rather than being duplicated here.
func (tb *treeBuild) directionSessionGroups(ctx context.Context, cfg Settings) (map[string][]directionSession, error) {
	s := tb.s
	records, err := s.directionSessionRecords(ctx)
	if err != nil {
		return nil, err
	}
	origins, err := s.directionOriginRecords(ctx)
	if err != nil {
		return nil, err
	}
	special, knownDirections, err := s.directionSpecialSessions(ctx, cfg)
	if err != nil {
		return nil, err
	}
	supervised := map[[2]string]bool{}
	for _, id := range tb.open {
		t := tb.todos[id].Todo
		if t.Supervision == nil || t.Supervision.Supervisor == "" {
			continue
		}
		for _, a := range t.Agents {
			if a.active() {
				supervised[[2]string{s.canonical(a.MachineID), a.SessionID}] = true
			}
		}
	}
	var candidates []directionCandidate
	seen := map[[2]string]bool{}
	for _, key := range slices.Sorted(maps.Keys(records)) {
		record := records[key]
		var tuple [2]string
		if json.Unmarshal([]byte(key), &tuple) != nil {
			continue
		}
		machine, sid := s.canonical(tuple[0]), tuple[1]
		k := [2]string{machine, sid}
		if record.Direction == "" || special[k] || supervised[k] || seen[k] {
			continue
		}
		seen[k] = true
		candidates = append(candidates, directionCandidate{direction: record.Direction, machine: machine, session: sid,
			title: record.Title, source: "owner", at: record.At})
	}
	for _, sid := range slices.Sorted(maps.Keys(origins)) {
		origin := origins[sid]
		if origin.Origin != OriginSpawned || !knownDirections[origin.Ref] {
			continue
		}
		machine := s.canonical(cmp.Or(origin.MachineID, cfg.MachineID))
		k := [2]string{machine, sid}
		if _, filed := records[runtimeListID(machine, sid)]; filed || special[k] || supervised[k] || seen[k] {
			continue
		}
		seen[k] = true
		candidates = append(candidates, directionCandidate{direction: origin.Ref, machine: machine, session: sid,
			source: "leader", at: origin.At})
	}
	visible := candidates[:0]
	for _, candidate := range candidates {
		if candidate.machine == cfg.MachineID {
			_, deleted, err := s.deletedConversation(ctx, candidate.machine, candidate.session)
			if err != nil {
				return nil, err
			}
			if deleted {
				continue
			}
		}
		visible = append(visible, candidate)
	}
	candidates = visible
	machines := map[string]bool{}
	for _, candidate := range candidates {
		machines[candidate.machine] = true
	}
	machineErrors := map[string]error{}
	for _, machine := range slices.Sorted(maps.Keys(machines)) {
		if tb.listed[machine] {
			continue
		}
		list, err := s.sessions(ctx, machine)
		if err != nil {
			machineErrors[machine] = err
			continue
		}
		tb.listed[machine] = true
		for _, row := range list {
			tb.sessions[[2]string{machine, row.SessionID}] = row
		}
	}
	titles, _ := s.conversationTitles()
	groups := map[string][]directionSession{}
	for _, candidate := range candidates {
		at := candidate.at.UTC()
		row := directionSession{MachineID: candidate.machine, SessionID: candidate.session, Title: candidate.title,
			Source: candidate.source, At: &at}
		if live, ok := tb.sessions[[2]string{candidate.machine, candidate.session}]; ok {
			row.State = triageStateOf(live)
			if row.Title == "" || candidate.source == "leader" {
				row.Title = live.Title
			}
		} else if machineErrors[candidate.machine] != nil {
			row.State = "offline"
		} else {
			row.State = "not_listed"
		}
		if row.Title == "" {
			row.Title = titles[candidate.session].title
		}
		groups[candidate.direction] = append(groups[candidate.direction], row)
	}
	for direction := range groups {
		slices.SortFunc(groups[direction], func(a, b directionSession) int {
			return cmp.Or(cmp.Compare(a.MachineID, b.MachineID), cmp.Compare(a.SessionID, b.SessionID))
		})
	}
	return groups, nil
}

// directionEndedTasks uses the same raw ownership resolution as the open tree. Only done/cancelled tasks are included;
// a failed start still waiting for its owner is open, not ended.
func (tb *treeBuild) directionEndedTasks(direction, title, before string, limit int) (page []directionEndedTask, more, ok bool) {
	filter := strings.ToLower(strings.TrimSpace(title))
	var all []directionEndedTask
	for _, id := range slices.Sorted(maps.Keys(tb.todos)) {
		t := tb.todos[id].Todo
		if tb.taskOwner(t) != direction || t.DoneAt == nil && t.CancelledAt == nil || !t.closedForGood() && t.CancelledAt == nil {
			continue
		}
		if filter != "" && !strings.Contains(strings.ToLower(t.Title), filter) {
			continue
		}
		end := t.DoneAt
		state := "done"
		if t.Cancelled() {
			state = "cancelled"
		}
		if t.CancelledAt != nil {
			end = t.CancelledAt
		}
		row := directionEndedTask{ID: t.ID, Title: t.Title, State: state, DoneAt: t.DoneAt, CancelledAt: t.CancelledAt}
		if end != nil {
			row.end = *end
		}
		all = append(all, row)
	}
	slices.SortFunc(all, func(a, b directionEndedTask) int {
		return cmp.Or(b.end.Compare(a.end), cmp.Compare(a.ID, b.ID))
	})
	start := 0
	if before != "" {
		start = slices.IndexFunc(all, func(t directionEndedTask) bool { return t.ID == before }) + 1
		if start == 0 {
			return nil, false, false
		}
	}
	end := min(len(all), start+limit)
	page = append([]directionEndedTask{}, all[start:end]...)
	return page, end < len(all), true
}

// directionTreeSessionRows leaves quiet sessions to the single-direction read and never duplicates an Agent already
// listed under an open to-do (including an owner's own, unsupervised to-do).
func (tb *treeBuild) directionTreeSessionRows(groups map[string][]directionSession) {
	onTask := map[[2]string]bool{}
	for _, id := range tb.open {
		for _, a := range tb.todos[id].Agents {
			if a.active() {
				onTask[[2]string{tb.s.canonical(a.MachineID), a.SessionID}] = true
			}
		}
	}
	for direction, sessions := range groups {
		active := sessions[:0]
		for _, session := range sessions {
			if !onTask[[2]string{session.MachineID, session.SessionID}] && (session.State == proto.StateWorking || session.State == proto.StateNeedsYou) {
				active = append(active, session)
			}
		}
		groups[direction] = active
	}
}
