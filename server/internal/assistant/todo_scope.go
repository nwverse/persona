package assistant

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/nwverse/persona/server/internal/assistant/todo/timeline"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/identity"
	"github.com/nwverse/persona/server/internal/proto"
)

// What an extension's token (todos.read) is shown of a to-do whose history may hold the health space's words (#10941,
// docs/extensions/extensions.md "Never shown to extensions"): one rule (todosHidden), worked out once per read, for
// every route. Such a to-do has no conversation (todo_timeline.go) and no decisions (todo_decisions.go), and its row,
// on GET /api/v1/todos and GET /api/v1/todos/{id}, carries nothing of its runs (forExtension): no run's title or
// directory, and in its view no run's question, title or words, while each ask keeps its id, kind and risk, so the row
// still says something waits. The to-do's own fields (its title, notes and brief, its own ask, its reviews and checks)
// are listed as before, and a device's credentials read all of it.

// feedConversationStore reads the conversations of every to-do's history in one query (db.FeedConversations).
type feedConversationStore interface {
	FeedConversations(ctx context.Context, refs, kinds []string) (map[string][]string, error)
}

var errNoFeedConversations = errors.New("this store cannot tell the conversations of a to-do's history")

// todosHidden are those of to-dos ids whose history an extension's token is not shown: one that may hold the health
// space's words, as a conversation it came from or was written in is a health conversation (its origin, its supervisor,
// those of the feed's items about it, todoFeedConversations), or one of the runs it ever had works there, its link
// released, dismissed (which the apps' row leaves out) or done included. It is decided from the to-dos as the manager
// keeps them, never only from what is live now (a done to-do's or a released link's session is not read for its
// view), and it fails closed as /interactions/history does (extensionHiddenCards): a conversation whose space cannot be
// told (not listed, and no deletion record keeps it), a history whose conversations cannot be read, or a run whose link
// keeps no directory and whose session cannot be read, hides it, and so does a to-do no longer kept. read are the
// sessions the read's views already read (todoFactsOf), by computer and id: a run among them is not read again, the
// others are read in one call a computer whatever the number of to-dos, and each conversation is told once.
func (s *Service) todosHidden(ctx context.Context, ids []string, read map[[2]string]proto.Session) map[string]bool {
	hidden := make(map[string]bool, len(ids))
	// What a to-do still to decide names: the conversations it came from, and its runs whose link keeps no directory
	// that the views did not read.
	type undecided struct {
		conversations []string
		unread        [][2]string
	}
	left := map[string]*undecided{}
	s.todos.mu.RLock()
	for _, id := range ids {
		t, ok := s.todos.todos[id]
		if !ok {
			hidden[id] = true
			continue
		}
		u := &undecided{conversations: []string{t.Origin}}
		if t.Supervision != nil {
			u.conversations = append(u.conversations, t.Supervision.Supervisor)
		}
		for _, a := range t.Agents {
			run := [2]string{a.MachineID, a.SessionID}
			c, seen := read[run]
			// Where the run started, as its link keeps it, is its space for good.
			if identity.InHealthSpace(a.Cwd) || seen && healthRun(c) {
				hidden[id] = true
				break
			}
			if !seen && a.Cwd == "" {
				u.unread = append(u.unread, run)
			}
		}
		if !hidden[id] {
			left[id] = u
		}
	}
	s.todos.mu.RUnlock()
	told := map[string]bool{} // by conversation: its space is the health space's, or cannot be told
	health := func(c string) bool {
		if c == "" {
			return false
		}
		bad, ok := told[c]
		if !ok {
			space, known := s.conversationSpace(ctx, c)
			bad = !known || space == SpaceHealth
			told[c] = bad
		}
		return bad
	}
	// still are the to-dos left undecided, in the order asked.
	still := func() []string {
		var out []string
		for _, id := range ids {
			if left[id] != nil && !hidden[id] {
				out = append(out, id)
			}
		}
		return out
	}
	for _, id := range still() {
		hidden[id] = slices.ContainsFunc(left[id].conversations, health)
	}
	// Then the conversations of their history, read for the to-dos the ones they came from did not decide.
	if todos := still(); len(todos) > 0 {
		feed, err := s.todoFeedConversations(ctx, todos)
		if err != nil {
			s.logf("todos: the conversations of their history: %v", err)
		}
		for _, id := range todos {
			hidden[id] = err != nil || slices.ContainsFunc(feed[id], health)
		}
	}
	// Last their runs that keep no directory, each computer asked once: a run is outside the space when its computer
	// lists it outside; one it does not list, or a computer that does not answer, cannot be told.
	todos, want := still(), map[string]map[string]bool{}
	for _, id := range todos {
		for _, r := range left[id].unread {
			if want[r[0]] == nil {
				want[r[0]] = map[string]bool{}
			}
			want[r[0]][r[1]] = true
		}
	}
	outside, inSpace := map[[2]string]bool{}, map[[2]string]bool{}
	for machine, sids := range want {
		list, err := s.todoRunSessions(ctx, machine, sids)
		if err != nil {
			continue
		}
		for _, c := range list {
			switch run := [2]string{machine, c.SessionID}; {
			case !sids[c.SessionID]:
			case healthRun(c):
				inSpace[run] = true
			default:
				outside[run] = true
			}
		}
	}
	for _, id := range todos {
		hidden[id] = slices.ContainsFunc(left[id].unread, func(r [2]string) bool { return !outside[r] || inSpace[r] })
	}
	return hidden
}

// todoHistoryHidden is todosHidden of to-do id alone, sessions its active runs' sessions as its view reads them.
func (s *Service) todoHistoryHidden(ctx context.Context, id string, sessions []proto.Session) bool {
	read := make(map[[2]string]proto.Session, len(sessions))
	for _, c := range sessions {
		read[[2]string{c.MachineID, c.SessionID}] = c
	}
	return s.todosHidden(ctx, []string{id}, read)[id]
}

// todoRunsRead are the sessions of to-do id's active runs as its view reads them (todoFactsOf), for a route that works
// out no view; none for a to-do not kept.
func (s *Service) todoRunsRead(ctx context.Context, id string) []proto.Session {
	t, ok := s.todoNow(id)
	if !ok {
		return nil
	}
	return s.todoFactsOf(ctx, []listedTodo{{Todo: t}})[0].Sessions
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

// forExtension leaves out of the rows of list an extension's token reads (every row, or the row at index only when it
// is not negative), p the list's projection, what the token is not shown of them, and answers the verdicts it read
// (todosHidden), by to-do. Of a to-do whose history is hidden: its runs' titles and directories (agents[]), and in a
// row's view the text of a run's ask and the run's title on it (a step's ask on its parent's row by the step's
// verdict), and line 2's run and what the run says (on a line a step leads, when any of the row's steps is hidden: the
// line does not say which step). It reads the verdicts of the to-dos whose runs the answer shows anything of, with the
// sessions the views read, and of the row asked for in any case, whose history its route reads (todoItemDetail).
func (s *Service) forExtension(ctx context.Context, list []listedTodo, p todoProjection, only int) map[string]bool {
	read := map[[2]string]proto.Session{}
	for _, f := range p.facts {
		for _, c := range f.Sessions {
			read[[2]string{c.MachineID, c.SessionID}] = c
		}
	}
	steps := map[string][]int{} // a parent's steps, by its id
	for i := range list {
		if list[i].PartOf != "" {
			steps[list[i].PartOf] = append(steps[list[i].PartOf], i)
		}
	}
	rows := []int{only}
	if only < 0 {
		rows = make([]int, len(list))
		for i := range list {
			rows[i] = i
		}
	}
	var ids []string
	needed := map[string]bool{}
	need := func(i int, always bool) {
		if id := list[i].ID; !needed[id] && (always || len(list[i].Agents) > 0) {
			needed[id] = true
			ids = append(ids, id)
		}
	}
	for _, i := range rows {
		need(i, i == only)
		for _, j := range steps[list[i].ID] {
			need(j, false)
		}
	}
	hidden := s.todosHidden(ctx, ids, read)
	for _, i := range rows {
		t := &list[i]
		if hidden[t.ID] {
			for k := range t.Agents {
				t.Agents[k].Title, t.Agents[k].Cwd = "", ""
			}
		}
		if t.View == nil {
			continue
		}
		step := slices.ContainsFunc(steps[t.ID], func(j int) bool { return hidden[list[j].ID] })
		if v, changed := viewForExtension(*t.View, hidden, hidden[t.ID], step); changed {
			t.View = &v
		}
	}
	return hidden
}

// runLineText are the line keys whose text is what a run says (todo/projection; spec/fixtures/todo-view/README.md
// "Line keys"): its question, its activity or its last words. Any other line's text is the to-do's own.
var runLineText = map[string]bool{"agent_asks": true, "awaits_reply": true, "working": true, "persona_working": true}

// viewForExtension is view v without the words of the runs of hidden to-dos (forExtension): own whether the row's
// to-do is hidden, step whether any of its steps is; and whether that took anything out. v shares its asks and line
// with the list's projection, so what changes is copied.
func viewForExtension(v assistantwire.TodoSummary, hidden map[string]bool, own, step bool) (assistantwire.TodoSummary, bool) {
	changed := false
	for i, a := range v.Asks {
		if a.Run == nil {
			continue // the to-do's own ask
		}
		of := own
		if rest, ok := strings.CutPrefix(a.ID, askStepPrefix); ok {
			id, _, _ := strings.Cut(rest, "/")
			of = hidden[id]
		}
		if !of || a.Text == "" && a.Run.Title == "" {
			continue
		}
		if !changed {
			v.Asks, changed = slices.Clone(v.Asks), true
		}
		run := *a.Run
		run.Title = ""
		v.Asks[i].Text, v.Asks[i].Run = "", &run
	}
	if l := v.Line; l != nil {
		of := own
		if _, ok := l.Args["step"]; ok {
			of = step
		}
		_, run := l.Args["run"]
		_, text := l.Args["text"]
		if of && (run || text && runLineText[l.Key]) {
			args := map[string]string{}
			for k, a := range l.Args {
				if k != "run" && (k != "text" || !runLineText[l.Key]) {
					args[k] = a
				}
			}
			if len(args) == 0 {
				args = nil
			}
			v.Line, changed = &assistantwire.TodoLine{Key: l.Key, Args: args}, true
		}
	}
	return v, changed
}
