package assistant

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"slices"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/assistant/todo/timeline"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/identity"
	"github.com/nwverse/persona/server/internal/proto"
)

// What an extension's token (todos.read, todos.write) is shown of the to-dos (#10941, docs/extensions/extensions.md
// "Never shown to extensions"): nothing of one whose history may hold the health space's words, as of a chat of the
// space. One rule (todosHidden) decides every way a to-do leaves the manager for an extension: GET /api/v1/todos leaves
// it out (extensionRows), every route of one to-do (/api/v1/todos/{id}…) answers 404 as for a to-do that does not
// exist, a write as a read (todoScoped), and neither todo.changed nor todo.decision is sent of it (todoChangedFlush,
// todoDecisionHidden). A
// to-do is hidden with any of its steps, whose asks and line its row's view carries, and with any to-do on its brief,
// whose log and lineage its row carries, so every row an extension is shown is a device's, byte for byte; the list's
// counts are a device's. Nor can an extension take a run in the space, or one off a to-do it is not shown, onto one
// of its own (todoTakeRefused), and what told a to-do's history stays: a link keeps a directory in the space
// (writeTodoAgentLink), and a space's unlink, move or void releases such a link rather than delete it
// (keepHealthLinks).

// feedConversationStore reads the conversations of every to-do's history in one query (db.FeedConversations).
type feedConversationStore interface {
	FeedConversations(ctx context.Context, refs, kinds []string) (map[string][]string, error)
}

// healthLinkStore keeps a to-do's links in the health space when the space they came through goes (keepHealthLinks).
type healthLinkStore interface {
	KeepTodoAgents(ctx context.Context, todoID, machineID string, sessionIDs []string, at time.Time) error
}

// The database tells and keeps both: a store that is it but lacked one would hide every to-do from an extension, or
// lose a link that told one.
var (
	_ feedConversationStore = (*db.DB)(nil)
	_ healthLinkStore       = (*db.DB)(nil)
)

var errNoFeedConversations = errors.New("this store cannot tell the conversations of a to-do's history")

// todoNoticeTimeout bounds the reads that decide whether events are told (todoChangedFlush, todoDecisionHidden).
const todoNoticeTimeout = 10 * time.Second

// todoEvidence is what of one to-do todosHidden decides by, copied under the to-dos' lock.
type todoEvidence struct {
	conversations []string    // its origin and its supervisor
	links         []TodoAgent // every run it ever had: released, dismissed and done included
	open          bool        // its view reads its active runs (todoFactsOf): it is open, or its start failed
}

func evidenceOf(t *Todo) *todoEvidence {
	e := &todoEvidence{conversations: []string{t.Origin}, links: slices.Clone(t.Agents), open: t.DoneAt == nil || t.StartError != ""}
	if t.Supervision != nil {
		e.conversations = append(e.conversations, t.Supervision.Supervisor)
	}
	return e
}

// todosHidden are those of to-dos ids an extension's token is not shown, by id (other to-dos it had to decide too).
// A to-do's own history may hold the health space's words when a conversation it came from or was written in is a
// health conversation (its origin, its supervisor, those of the feed's items about it, todoFeedConversations), or one
// of the runs it ever had works there, its link released, dismissed (which the apps' row leaves out) or done included;
// and a to-do is hidden with any of its steps and any to-do on its brief (above). It is decided from the to-dos as the
// manager keeps them, never only from what is live now, and it fails closed as /interactions/history does
// (extensionHiddenCards): a conversation whose space cannot be told (not listed, and no deletion record keeps it), a
// history whose conversations cannot be read, or a run whose link keeps no directory and whose session cannot be read,
// hides it, and so does a to-do asked about that is no longer kept. read are sessions already read of the active runs of
// the open to-dos (todoFactsOf: a list's view), by computer and id; the others its views would read are read in one
// call a computer, so the verdict is the same whichever route asks, and the conversations are told at once
// (unshownConversations).
func (s *Service) todosHidden(ctx context.Context, ids []string, read map[[2]string]proto.Session) map[string]bool {
	return s.todosHiddenOf(ctx, ids, nil, read)
}

// todosHiddenOf is todosHidden, with gone the to-dos no longer kept as they last were (a deletion's todo.changed).
func (s *Service) todosHiddenOf(ctx context.Context, ids []string, gone map[string]Todo, read map[[2]string]proto.Session) map[string]bool {
	briefs := s.briefLinksNow()
	asked := make(map[string]bool, len(ids))
	for _, id := range ids {
		asked[id] = true
	}
	// The to-dos to decide: those asked about, and to each its steps and the to-dos on its brief, until none is new.
	ev := map[string]*todoEvidence{} // nil: one not kept
	steps := map[string][]string{}   // by the to-do they are steps of
	s.todos.mu.RLock()
	for id, t := range s.todos.todos {
		if t.PartOf != "" {
			steps[t.PartOf] = append(steps[t.PartOf], id)
		}
	}
	for queue := slices.Clone(ids); len(queue) > 0; queue = queue[1:] {
		id := queue[0]
		if _, ok := ev[id]; ok {
			continue
		}
		if t, ok := s.todos.todos[id]; ok {
			ev[id] = evidenceOf(t)
		} else if t, ok := gone[id]; ok {
			ev[id] = evidenceOf(&t)
		} else {
			ev[id] = nil
			continue
		}
		queue = append(queue, steps[id]...)
		for mate := range briefs.on[briefs.of[id]] {
			queue = append(queue, mate)
		}
	}
	s.todos.mu.RUnlock()
	decided := make([]string, 0, len(ev))
	for id := range ev {
		decided = append(decided, id)
	}
	slices.Sort(decided)

	// own: what each to-do's own history says. A brief's to-do since deleted says nothing; one asked about hides.
	own := make(map[string]bool, len(ev))
	for _, id := range decided {
		e := ev[id]
		if e == nil {
			own[id] = asked[id]
			continue
		}
		// Where a run started, as its link keeps it, is its space for good.
		own[id] = slices.ContainsFunc(e.links, func(a TodoAgent) bool {
			c, seen := read[[2]string{a.MachineID, a.SessionID}]
			return identity.InHealthSpace(a.Cwd) || seen && healthRun(c)
		})
	}
	undecided := func() []string {
		return slices.DeleteFunc(slices.Clone(decided), func(id string) bool { return ev[id] == nil || own[id] })
	}
	// Then the conversations they came from and those of their history.
	if todos := undecided(); len(todos) > 0 {
		feed, err := s.todoFeedConversations(ctx, todos)
		if err != nil {
			s.logf("todos: the conversations of their history: %v", err)
		}
		var conversations []string
		for _, id := range todos {
			conversations = append(conversations, ev[id].conversations...)
			conversations = append(conversations, feed[id]...)
		}
		var bad map[string]bool
		if err == nil {
			bad = s.unshownConversations(ctx, conversations)
		}
		for _, id := range todos {
			own[id] = err != nil || slices.ContainsFunc(ev[id].conversations, func(c string) bool { return bad[c] }) ||
				slices.ContainsFunc(feed[id], func(c string) bool { return bad[c] })
		}
	}
	// Last their runs, each computer asked once: the active runs of the open ones the read did not read, and every run
	// whose link keeps no directory. One whose link keeps a directory outside the space is told by it when its computer
	// does not list it; one that keeps none cannot be told then.
	todos, want := undecided(), map[string]map[string]bool{}
	for _, id := range todos {
		for _, a := range ev[id].links {
			if _, seen := read[[2]string{a.MachineID, a.SessionID}]; !seen && (a.Cwd == "" || ev[id].open && a.active()) {
				if want[a.MachineID] == nil {
					want[a.MachineID] = map[string]bool{}
				}
				want[a.MachineID][a.SessionID] = true
			}
		}
	}
	listed := map[[2]string]proto.Session{}
	for machine, sids := range want {
		list, err := s.todoRunSessions(ctx, machine, sids)
		if err != nil {
			continue
		}
		for _, c := range list {
			if sids[c.SessionID] {
				listed[[2]string{machine, c.SessionID}] = c
			}
		}
	}
	for _, id := range todos {
		own[id] = slices.ContainsFunc(ev[id].links, func(a TodoAgent) bool {
			run := [2]string{a.MachineID, a.SessionID}
			if _, seen := read[run]; seen {
				return false // told above
			}
			c, ok := listed[run]
			return ok && healthRun(c) || !ok && a.Cwd == ""
		})
	}

	// A to-do is hidden with its steps and the to-dos on its brief.
	hidden := maps.Clone(own)
	for changed := true; changed; {
		changed = false
		for _, id := range decided {
			if hidden[id] || ev[id] == nil {
				continue
			}
			if slices.ContainsFunc(steps[id], func(c string) bool { return hidden[c] }) {
				hidden[id], changed = true, true
				continue
			}
			for mate := range briefs.on[briefs.of[id]] {
				if hidden[mate] {
					hidden[id], changed = true, true
					break
				}
			}
		}
	}
	return hidden
}

// unshownConversations are those of conversations (a to-do's origin, supervisor or history; "" the assistant's own)
// that are health conversations or whose space cannot be told (conversationSpace), each told once: the assistant's
// computer lists the ones it keeps in one call, and only one it does not list is told on its own (its main
// conversation routed to another computer, the record of its deletion), as is a task leader's agent (leaderSpace).
func (s *Service) unshownConversations(ctx context.Context, conversations []string) map[string]bool {
	bad, plain := map[string]bool{}, map[string]bool{}
	for _, c := range conversations {
		if _, ok := bad[c]; ok || c == "" || plain[c] {
			continue
		}
		if machine, sid, ok := parseLeaderRef(c); ok && s.isLeaderSession(machine, sid) {
			space, known := s.leaderSpace(ctx, s.canonical(machine), sid)
			bad[c] = !known || space == SpaceHealth
			continue
		}
		plain[c] = true
	}
	listed := map[string]proto.Session{}
	if len(plain) > 0 {
		if list, err := s.todoRunSessions(ctx, s.settings(ctx).MachineID, plain); err == nil {
			for _, c := range list {
				listed[c.SessionID] = c
			}
		}
	}
	for c := range plain {
		if l, ok := listed[c]; ok {
			bad[c] = spaceOf(l) == SpaceHealth
			continue
		}
		space, known := s.conversationSpace(ctx, c)
		bad[c] = !known || space == SpaceHealth
	}
	return bad
}

// todoFeedConversations are the conversations the feed's items about each of to-dos ids were written in, by to-do
// (every item of the kinds the timeline reads, not only the newest todoTimelineFeedCap): from the store in one query,
// else from memory. An error when the store cannot tell.
func (s *Service) todoFeedConversations(ctx context.Context, ids []string) (map[string][]string, error) {
	kinds := timeline.FeedKinds()
	if st := s.store(); st != nil {
		fc, ok := st.(feedConversationStore)
		if !ok {
			return nil, errNoFeedConversations
		}
		return fc.FeedConversations(ctx, ids, kinds)
	}
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	out := map[string][]string{}
	s.tasks.mu.Lock()
	for _, list := range s.tasks.feed {
		for _, it := range list {
			if want[it.Ref] && slices.Contains(kinds, it.Kind) && !slices.Contains(out[it.Ref], it.Conversation) {
				out[it.Ref] = append(out[it.Ref], it.Conversation)
			}
		}
	}
	s.tasks.mu.Unlock()
	return out, nil
}

// extensionRows are the rows of list (GET /api/v1/todos, p its projection) an extension's token is shown: those of
// the to-dos todosHidden does not hide, each as a device reads it. The views' sessions are not read again.
func (s *Service) extensionRows(ctx context.Context, list []listedTodo, p todoProjection) []listedTodo {
	read := map[[2]string]proto.Session{}
	for _, f := range p.facts {
		for _, c := range f.Sessions {
			read[[2]string{c.MachineID, c.SessionID}] = c
		}
	}
	ids := make([]string, len(list))
	for i := range list {
		ids[i] = list[i].ID
	}
	hidden := s.todosHidden(ctx, ids, read)
	out := make([]listedTodo, 0, len(list))
	for _, t := range list {
		if !hidden[t.ID] {
			out = append(out, t)
		}
	}
	return out
}

// todoScoped is route fn of one to-do (/api/v1/todos/{id}…) as an extension's token reaches it: a to-do it is not
// shown answers 404 as one that does not exist, a write as a read, before the route reads or changes anything (a
// health run's title echoed by a write, a link that told its history taken off). s.handle puts every such route
// behind it, so a route added later is too.
func (s *Service) todoScoped(fn http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(RoleHeader) == roleExtension {
			ctx, id := r.Context(), r.PathValue("id")
			if err := s.ensureTodosLoaded(ctx); err != nil {
				apierr.Write(w, http.StatusServiceUnavailable, errTodosUnavailable)
				return
			}
			s.todos.mu.RLock()
			_, kept := s.todos.todos[id]
			s.todos.mu.RUnlock()
			if kept && s.todosHidden(ctx, []string{id}, nil)[id] {
				apierr.WriteCode(w, http.StatusNotFound, "todo_not_found", i18n.Err("assistant-todos.not_found"))
				return
			}
		}
		fn(w, r)
	}
}

// todoDecisionHidden says whether event n is a todo.decision of a to-do an extension is not shown: it goes only to
// extensions granted todos.read, so it is not sent at all. The queue's worker asks it, off the write that made the
// decision; todoChangedFlush judges the todo.changed it tells the same way.
func (s *Service) todoDecisionHidden(n todoNotice) bool {
	p, ok := n.params.(assistantwire.TodoDecisionEvent)
	if !ok {
		return false
	}
	ctx, cancel := context.WithTimeout(s.runCtx(), todoNoticeTimeout)
	defer cancel()
	return s.todosHidden(ctx, []string{p.ID}, nil)[p.ID]
}

// todoTakeRefused says whether an extension's token may not take run c of computer mid onto a to-do (PUT
// …/agents/{mid}/{sid}, and each run a PUT …/spaces brings): one in the health space, as /sessions answers 404 for a
// chat of the space, and one linked to a to-do it is not shown, since the link would move the run off it.
func (s *Service) todoTakeRefused(ctx context.Context, mid string, c proto.Session) bool {
	if healthRun(c) {
		return true
	}
	var holders []string
	s.todos.mu.RLock()
	for id, t := range s.todos.todos {
		if slices.ContainsFunc(t.Agents, func(a TodoAgent) bool {
			return a.MachineID == mid && a.SessionID == c.SessionID && a.ReleasedAt == nil
		}) {
			holders = append(holders, id)
		}
	}
	s.todos.mu.RUnlock()
	hidden := s.todosHidden(ctx, holders, nil)
	return slices.ContainsFunc(holders, func(id string) bool { return hidden[id] })
}

// spaceTakeRefused says whether an extension's token may not link space mid/space to to-do id: the space is another
// to-do's that it is not shown (the link would take it and its runs off that one), or a run in it now is one
// todoTakeRefused refuses.
func (s *Service) spaceTakeRefused(ctx context.Context, id, mid, space string) bool {
	key := spaceKey(mid, space)
	s.todos.mu.RLock()
	holder := s.todos.spacesByKey[key]
	s.todos.mu.RUnlock()
	if holder != "" && holder != id && s.todosHidden(ctx, []string{holder}, nil)[holder] {
		return true
	}
	list, _ := s.sessions(ctx, mid) // none read: the link takes none of them either (linkTodoSpace)
	return slices.ContainsFunc(list, func(c proto.Session) bool {
		return sessionSpaceKey(mid, c) == key && c.Status != proto.StatusOffline && s.todoTakeRefused(ctx, mid, c)
	})
}

// keepHealthLinks takes to-do id's links through space key (computer machine) whose directory is in the health space
// off the space before its unlink, move or void deletes the others: they stay, released and dismissed as a run taken
// off by hand is (the apps' row leaves them out, as it now has no such run), so what the to-do had stays told
// (todosHidden). The caller holds todoBook.writing.
func (s *Service) keepHealthLinks(ctx context.Context, id, machine, key string) error {
	var sids []string
	s.todos.mu.RLock()
	if t := s.todos.todos[id]; t != nil {
		for _, a := range t.Agents {
			if a.Source == "space" && a.Via == key && a.MachineID == machine && identity.InHealthSpace(a.Cwd) {
				sids = append(sids, a.SessionID)
			}
		}
	}
	s.todos.mu.RUnlock()
	if len(sids) == 0 {
		return nil
	}
	now := time.Now().UTC()
	if st := s.todoDB(); st != nil {
		keep, ok := st.(healthLinkStore)
		if !ok {
			return errors.New("this store cannot keep a to-do's links in the health space")
		}
		if err := keep.KeepTodoAgents(ctx, id, machine, sids, now); err != nil {
			return err
		}
	}
	s.todos.mu.Lock()
	if t := s.todos.todos[id]; t != nil {
		for i := range t.Agents {
			a := &t.Agents[i]
			if a.Source != "space" || a.Via != key || a.MachineID != machine || !slices.Contains(sids, a.SessionID) {
				continue
			}
			a.Source, a.Via = "manual", ""
			if a.ReleasedAt == nil {
				a.ReleasedAt = &now
			}
			if a.DismissedAt == nil {
				a.DismissedAt = &now
			}
			if run := [2]string{machine, a.SessionID}; s.todos.activeBySession[run] == id {
				delete(s.todos.activeBySession, run)
			}
		}
	}
	s.todos.mu.Unlock()
	return nil
}
