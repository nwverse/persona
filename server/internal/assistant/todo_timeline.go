package assistant

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/assistant/todo/timeline"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/proto"
)

// A to-do's timeline (#10465, docs/assistant/todo-status-sop.md "The to-do's timeline"): its conversation grouped into
// rounds, its results and what changed since the owner looked, worked out by the pure todo/timeline (its rules:
// spec/fixtures/todo-rounds) from the stores the manager already keeps. This file is the adapter: once per read it
// reads the to-do as GET /api/v1/todos/{id} lists it, the feed's items about it, its decisions, its runs' sessions and
// the owner's zone, and hands them over as timeline.Input. GET /api/v1/todos/{id}/timeline pages through the rounds;
// GET /api/v1/todos/{id} carries the ones the paper shows (todo_view_routes.go). Neither has a push of its own:
// {"type":"todos"} comes on a decision, a supervision or a scope change, and assistant_feed with every feed item,
// which clients match to the to-do by its ref. The manager names CapabilityTodoTimeline in its hello.

const (
	// todoTimelineFeedCap is how many of a to-do's feed items a read takes, the newest: a to-do has a few hundred at
	// most, and the cap keeps a runaway one's read bounded.
	todoTimelineFeedCap = 1000
	// todoTimelinePage is how many rounds GET …/timeline answers when it is not told, todoTimelineMaxPage at most.
	todoTimelinePage    = 20
	todoTimelineMaxPage = 100
)

// errRoundNotFound: GET …/timeline's before names no round of the to-do (one read from rows the stores no longer keep,
// or never one); the client reads the latest page again.
var errRoundNotFound = errors.New("round not found")

func (s *Service) todoTimelineRoutes() {
	// GET /api/v1/todos/{id}/timeline?before=<round id>&limit=<n>: the to-do's conversation → assistantwire.TodoTimeline,
	// its latest limit rounds (todoTimelinePage when not given, at most todoTimelineMaxPage) before the round named
	// before (none: up to the latest), oldest first, with more while earlier ones are left. An unknown id 404
	// todo_not_found; a before that names no round 409 round_not_found; the to-dos or their history unreadable 503.
	s.handle("GET /api/v1/todos/{id}/timeline", func(w http.ResponseWriter, r *http.Request) {
		ctx, id, q := r.Context(), r.PathValue("id"), r.URL.Query()
		list, err := s.listedTodosOf(ctx, false, map[string]bool{id: true}) // loads the to-dos first
		if err != nil {
			apierr.Write(w, http.StatusServiceUnavailable, errTodosUnavailable)
			return
		}
		if len(list) == 0 {
			apierr.WriteCode(w, http.StatusNotFound, "todo_not_found", errors.New("todo not found"))
			return
		}
		list[0].Todo = s.withBrief(list[0].Todo) // as the item route lists it
		raw, err := json.Marshal(list[0])
		if err != nil {
			apierr.Write(w, http.StatusInternalServerError, err)
			return
		}
		d, err := s.todoTimelineOf(ctx, raw, s.todoFactsOf(ctx, list)[0].Sessions)
		if err != nil {
			s.logf("todo %s: timeline: %v", id, err)
			apierr.Write(w, http.StatusServiceUnavailable, errTodosUnavailable)
			return
		}
		limit, _ := strconv.Atoi(q.Get("limit"))
		if limit <= 0 {
			limit = todoTimelinePage
		}
		rounds, more, ok := timelinePage(d.Rounds, q.Get("before"), min(limit, todoTimelineMaxPage))
		if !ok {
			apierr.WriteCode(w, http.StatusConflict, "round_not_found", errRoundNotFound)
			return
		}
		apierr.WriteJSON(w, assistantwire.TodoTimeline{Rounds: rounds, More: more})
	})
}

// timelinePage is the page GET …/timeline answers of rounds (oldest first): the latest limit before the round whose id
// is before ("": up to the latest), in the same order, and whether earlier ones are left; ok false when no round has
// that id. The page is never nil, so an empty one writes [].
func timelinePage(rounds []assistantwire.TodoRound, before string, limit int) (page []assistantwire.TodoRound, more, ok bool) {
	end := len(rounds)
	if before != "" {
		if end = slices.IndexFunc(rounds, func(r assistantwire.TodoRound) bool { return r.ID == before }); end < 0 {
			return nil, false, false
		}
	}
	start := max(0, end-limit)
	return append([]assistantwire.TodoRound{}, rounds[start:end]...), start > 0, true
}

// todoItemDetail is what GET /api/v1/todos/{id} answers beside to-do id's row raw, whose active runs' sessions are
// sessions (assistantwire.TodoItemDetail): the rounds the paper shows (timeline.Latest), how many there are in all, its
// results and what changed since the owner looked. Nothing when its history cannot be read: the row still answers,
// and the paper draws what it has as from an older manager.
func (s *Service) todoItemDetail(ctx context.Context, id string, raw json.RawMessage, sessions []proto.Session) assistantwire.TodoItemDetail {
	d, err := s.todoTimelineOf(ctx, raw, sessions)
	if err != nil {
		s.logf("todo %s: timeline: %v", id, err)
		return assistantwire.TodoItemDetail{}
	}
	total, rounds := len(d.Rounds), timeline.Latest(d.Rounds, timeline.PaperRounds)
	if rounds == nil {
		rounds = []assistantwire.TodoRound{} // a to-do with no rounds yet: [], told apart from an older manager's none
	}
	return assistantwire.TodoItemDetail{Rounds: rounds, RoundsTotal: &total, Recap: d.Recap, Results: d.Results}
}

// todoTimelineOf works out the timeline of the to-do whose row is raw, as GET /api/v1/todos/{id} lists it (with its
// brief and lineage; its view, when there, is not read), with sessions its active runs' sessions as its view reads them
// (todoFactsOf). The row is read from its JSON, as the shared cases give it, so the timeline sees exactly what the
// apps do: an open ask's id is formed from asked_at as listed, as the view forms it. An error when the feed's items
// about it or its decisions cannot be read.
func (s *Service) todoTimelineOf(ctx context.Context, raw json.RawMessage, sessions []proto.Session) (timeline.Derived, error) {
	in := timeline.Input{Sessions: sessions, Loc: s.location(ctx)}
	if err := json.Unmarshal(raw, &in.Todo); err != nil {
		return timeline.Derived{}, err
	}
	var err error
	if in.Feed, err = s.todoFeed(ctx, in.Todo.ID); err != nil {
		return timeline.Derived{}, err
	}
	if in.Decisions, err = s.todoTimelineDecisions(ctx, in.Todo.ID); err != nil {
		return timeline.Derived{}, err
	}
	in.Machines = s.todoMachineNames(ctx, in.Feed)
	return timeline.Derive(in), nil
}

// todoFeed are the feed's items about to-do id of the kinds the timeline reads, in every conversation (the origin's,
// a supervisor's, "" for a hand-written to-do's relays), the newest todoTimelineFeedCap of them: from the store
// (FeedByRef), else from memory.
func (s *Service) todoFeed(ctx context.Context, id string) ([]timeline.FeedItem, error) {
	kinds := timeline.FeedKinds()
	var items []FeedItem
	if st := s.store(); st != nil {
		rows, err := st.FeedByRef(ctx, id, kinds, todoTimelineFeedCap)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			items = append(items, FeedItem{ID: r.ID, At: r.At, Kind: r.Kind, Body: r.Body})
		}
	} else {
		s.tasks.mu.Lock()
		for _, list := range s.tasks.feed {
			for _, it := range list {
				if it.Ref == id && slices.Contains(kinds, it.Kind) {
					items = append(items, it)
				}
			}
		}
		s.tasks.mu.Unlock()
		slices.SortFunc(items, func(a, b FeedItem) int { return cmp.Or(b.At.Compare(a.At), strings.Compare(b.ID, a.ID)) })
		items = items[:min(len(items), todoTimelineFeedCap)]
	}
	out := make([]timeline.FeedItem, len(items))
	for i, it := range items {
		out[i] = timeline.FeedItem{ID: it.ID, At: it.At, Kind: it.Kind, Body: it.Body}
	}
	return out, nil
}

// todoTimelineDecisions are to-do id's decisions, superseded ones included; none without the hub's database, which
// alone keeps them.
func (s *Service) todoTimelineDecisions(ctx context.Context, id string) ([]timeline.Decision, error) {
	st := s.todoDB()
	if st == nil {
		return nil, nil
	}
	rows, err := st.ListTodoDecisions(ctx, id, true)
	if err != nil {
		return nil, err
	}
	out := make([]timeline.Decision, len(rows))
	for i, d := range rows {
		out[i] = timeline.Decision{ID: d.ID, Question: d.Question, Choice: d.Choice, Reason: d.Reason, Source: d.Source,
			SourceMachine: d.SourceMachine, SourceSession: d.SourceSession, SourceMessage: d.SourceMessage,
			SupersededBy: d.SupersededBy, CreatedAt: d.CreatedAt}
	}
	return out, nil
}

// todoMachineNames are the computers' names by id, which a run's receipt written before #10465 names its computer by
// (as the hub names it, finishTurn); nil when every turn's receipt in feed names its run, so only an older history
// costs a read of the devices.
func (s *Service) todoMachineNames(ctx context.Context, feed []timeline.FeedItem) map[string]string {
	older := slices.ContainsFunc(feed, func(f timeline.FeedItem) bool {
		var b struct {
			State     string `json:"state"`
			SessionID string `json:"session_id"`
		}
		return f.Kind == feedReceipt && json.Unmarshal(f.Body, &b) == nil && b.State != "failed" && b.SessionID == ""
	})
	if !older {
		return nil
	}
	names := map[string]string{}
	for _, d := range s.devices(ctx) {
		names[d.ID] = d.Name
	}
	return names
}
