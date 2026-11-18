package timeline

import (
	"cmp"
	"maps"
	"slices"
	"strings"

	"github.com/nwverse/persona/server/internal/assistantwire"
)

// maxResults is how many results the paper shows (proposal 3.5 row 9).
const maxResults = 3

const deliveryLive = "live" // the finished work went live, and that was checked

// report is result entry e as a Results chip: what the work came to, its first line, and the first link it named
// after it (the hub's outcome lists them a line each).
func report(e assistantwire.TodoTimelineEntry) *assistantwire.TodoResult {
	out := &assistantwire.TodoResult{ID: e.ID, Kind: assistantwire.TodoResultKindReport, At: e.At, Run: e.Actor.Run}
	for l := range strings.Lines(e.Text) {
		switch l = strings.TrimSpace(l); {
		case l == "":
		case out.Text == "":
			out.Text = l
		case out.URL == "" && (strings.HasPrefix(l, "https://") || strings.HasPrefix(l, "http://")):
			out.URL = l
		}
	}
	return out
}

// results are what the to-do's work produced, newest first, at most maxResults:
//   - each code change: a PR a receipt named, or one the hub looked at for the to-do, at the first receipt that named
//     it (else when the hub last looked), with how the hub last saw it (open, draft, merged, closed);
//   - the work going live;
//   - each report of what the work came to (the hub's outcome of a completion, an extension's, the owner's own, a
//     concern's report) that no later decision superseded.
func (r *reader) results() []assistantwire.TodoResult {
	var out []assistantwire.TodoResult
	t := r.in.Todo
	status := map[string]PRStatus{}
	for _, s := range t.OtherPRs {
		status[s.URL] = s
	}
	if t.PRStatus != nil {
		status[t.PRStatus.URL] = *t.PRStatus
	}
	urls := slices.Sorted(maps.Keys(status))
	for url := range r.changes {
		if _, ok := status[url]; !ok {
			urls = append(urls, url)
		}
	}
	for _, url := range urls {
		if url == "" {
			continue
		}
		res := assistantwire.TodoResult{ID: "change:" + url, Kind: assistantwire.TodoResultKindChange, URL: url}
		s, seen := status[url]
		if seen {
			res.At, res.State = s.CheckedAt.UTC(), s.State
			if s.Draft && s.State == "open" {
				res.State = "draft"
			}
		}
		if c, ok := r.changes[url]; ok {
			res.At, res.Run = c.at.UTC(), c.run
		}
		if !res.At.IsZero() {
			out = append(out, res)
		}
	}
	if d := t.Delivery; d != nil && d.State == deliveryLive {
		out = append(out, assistantwire.TodoResult{ID: "deploy@" + nano(d.At), Kind: assistantwire.TodoResultKindDeploy,
			At: d.At.UTC()})
	}
	for _, it := range r.items {
		if it.result != nil && it.e.SupersededBy == "" {
			out = append(out, *it.result)
		}
	}
	slices.SortFunc(out, func(a, b assistantwire.TodoResult) int {
		return cmp.Or(b.At.Compare(a.At), strings.Compare(a.ID, b.ID))
	})
	if len(out) > maxResults {
		out = out[:maxResults]
	}
	return out
}

// recap is what changed since the owner last looked (seen_at): how many entries of each type came after it, each turn
// of a run counted, and the round of the first of them. The owner's own acts are no news to them, nor is what R1
// folds under one (the hub's relay of their answer, the run their message started). nil when they never looked or
// nothing else came since.
func (r *reader) recap(roundOf map[*item]string) *assistantwire.TodoRecap {
	seen := r.in.Todo.SeenAt
	if seen == nil {
		return nil
	}
	var out *assistantwire.TodoRecap
	for _, it := range r.items { // in time order
		if !it.e.At.After(*seen) || it.e.Actor.Kind == assistantwire.TodoActorKindOwner || it.join != "" {
			continue
		}
		if out == nil {
			out = &assistantwire.TodoRecap{Since: seen.UTC(), Round: roundOf[it], Counts: map[string]int{}}
		}
		out.Counts[it.e.Type]++
	}
	return out
}
