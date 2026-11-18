package hub

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
)

// The To-dos view (#10226, docs/assistant/todo-status-sop.md "The hub's view") is the managers': each works out its own
// to-dos' views, counts and places. The hub relays them and joins several managers' answers, adding their counts up
// field by field and key by key; it never works out a phase itself (docs/server/architecture.md "The hub is only a
// communication center"). Clients order a merged list by rank, then manager, then id: the hub does not rank again.

const (
	// featureTodoView: every row of GET /api/v1/todos carries `view`, the list `counts`, and GET /api/v1/todos/{id}
	// answers one row so (docs/protocol/protocol.md, the to-dos rows).
	featureTodoView = assistantwire.CapabilityTodoView
	// featureTodoPlaces: GET /api/v1/todos/places answers the To-dos sidebar's places with the counts.
	featureTodoPlaces = assistantwire.CapabilityTodoPlaces
	// todoPlacesRoute is answered by every manager the to-dos list merges, joined (mergeTodoPlaces).
	todoPlacesRoute = "GET /api/v1/todos/places"
)

// todoViewFeatures are todo_view and todo_places, each while every manager whose to-dos the list merges (listManagers)
// is connected and names it in its hello: a hub-wide feature cannot promise the view for part of the list, so an older
// or offline manager hides it until it is ready, and clients keep their own rules until then.
func (h *Hub) todoViewFeatures() []string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	view, places := true, true
	for _, id := range h.listManagers(ctx) {
		view = view && h.archiveManagerHas(id, assistantwire.CapabilityTodoView) == http.StatusOK
		places = places && h.archiveManagerHas(id, assistantwire.CapabilityTodoPlaces) == http.StatusOK
	}
	switch {
	case view && places:
		return []string{featureTodoView, featureTodoPlaces}
	case view:
		return []string{featureTodoView}
	case places:
		return []string{featureTodoPlaces}
	}
	return nil
}

// todoListSums are the members of a merged to-dos list the hub adds up over its answers (mergeList, before the list's
// validator is taken): counts, those of every answer the merge takes that has them. None when no such answer has them
// (every manager is older): the merged list then has none either.
func todoListSums(answers []listAnswer) map[string]json.RawMessage {
	var sum *assistantwire.TodoCounts
	for i := range answers {
		raw, ok := mergedMember(&answers[i], "todos", "counts")
		var c *assistantwire.TodoCounts
		if !ok || json.Unmarshal(raw, &c) != nil || c == nil {
			continue
		}
		if sum == nil {
			sum = &assistantwire.TodoCounts{Sections: map[string]int{}}
		}
		addTodoCounts(sum, *c)
	}
	if sum == nil {
		return nil
	}
	b, err := json.Marshal(sum)
	if err != nil {
		return nil
	}
	return map[string]json.RawMessage{"counts": b}
}

// addTodoCounts adds c to sum, field by field and every key of its sections.
func addTodoCounts(sum *assistantwire.TodoCounts, c assistantwire.TodoCounts) {
	sum.Open += c.Open
	sum.NeedsYou += c.NeedsYou
	sum.InProgress += c.InProgress
	for k, n := range c.Sections {
		sum.Sections[k] += n
	}
}

// mergedMember is the top-level member name of a, one answer of a list merged under key, as the merge reads it; false
// when a has none, or is no answer the merge takes (not 2xx, not a JSON object, its list not an array).
func mergedMember(a *listAnswer, key, name string) (json.RawMessage, bool) {
	if !a.ok() {
		return nil, false
	}
	s, use := a.scan(key)
	switch use {
	case scanMerge:
		for _, m := range s.members {
			if string(m.key) == name {
				return m.val, true
			}
		}
	case scanDecode: // as writeDecoded reads it
		var t map[string]json.RawMessage
		if json.Unmarshal(a.body, &t) != nil || t == nil {
			return nil, false
		}
		if raw, has := t[key]; has && json.Unmarshal(raw, new([]json.RawMessage)) != nil {
			return nil, false
		}
		v, has := t[name]
		return v, has
	}
	return nil, false
}

// mergeTodoPlaces answers GET /api/v1/todos/places from the managers a merged list asks (mergedAsk): All To-dos, Today
// and Upcoming once each, their needs_you added up; then each manager's other places (its projects) as it lists them,
// stamped with it, the default's first (a project is its manager's knowledge base's: two managers' project:<slug> are
// two places); and the counts added up as the list's are. A manager without the route (an older one answers 404 or 405)
// adds nothing, nor does one that gives no places. When none gives places: the first other failure as it is (503
// assistant_offline, say), else 404 as a hub without the route.
func (h *Hub) mergeTodoPlaces(w http.ResponseWriter, r *http.Request) {
	ids, body, ok := h.mergedAsk(w, r)
	if !ok {
		return
	}
	var fixed, own []assistantwire.TodoPlace
	counts := assistantwire.TodoCounts{Sections: map[string]int{}}
	answered := false
	var failed *bufferedWriter
	for _, id := range ids {
		bw := newBufferedWriter()
		h.forwardHTTP(bw, withBody(r, body), id)
		bw.status = cmp.Or(bw.status, http.StatusOK)
		var p *assistantwire.TodoPlaces
		if bw.status >= 300 || json.Unmarshal(bw.body.Bytes(), &p) != nil || p == nil {
			if failed == nil && bw.status >= 300 && bw.status != http.StatusNotFound && bw.status != http.StatusMethodNotAllowed {
				failed = bw
			}
			continue
		}
		answered = true
		addTodoCounts(&counts, p.Counts)
		for _, pl := range p.Places {
			switch pl.ID {
			case assistantwire.TodoPlaceKindAll, assistantwire.TodoPlaceKindToday, assistantwire.TodoPlaceKindUpcoming:
				pl.Manager = ""
				if i := slices.IndexFunc(fixed, func(f assistantwire.TodoPlace) bool { return f.ID == pl.ID }); i >= 0 {
					fixed[i].NeedsYou += pl.NeedsYou
				} else {
					fixed = append(fixed, pl)
				}
			default:
				pl.Manager = id
				own = append(own, pl)
			}
		}
	}
	switch {
	case answered:
		writeJSON(w, assistantwire.TodoPlaces{Places: append(append([]assistantwire.TodoPlace{}, fixed...), own...), Counts: counts})
	case failed != nil:
		copyHeader(w.Header(), failed.header)
		w.WriteHeader(failed.status)
		_, _ = w.Write(failed.body.Bytes())
	default:
		writeErr(w, http.StatusNotFound, errors.New(http.StatusText(http.StatusNotFound)))
	}
}
