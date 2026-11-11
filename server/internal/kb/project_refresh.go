package kb

import (
	"context"
	"sort"
	"time"

	"github.com/nwverse/persona/server/internal/ai"
	"github.com/nwverse/persona/server/internal/prompts"
)

// projectRetry is a project whose rewrite failed: how many times in a row, and when it may be tried again. Kept in the
// engine's memory, not in the project's file: PutProject would bump its Updated time and tell the clients about a
// change that is not one, and after a restart one more try is cheap.
type projectRetry struct {
	failures int
	at       time.Time
	// about is what failed (a recording's revision, an item's title and time): a source changed since is tried at
	// once, its failures forgotten. "" for steps without a cheap change signal (projects, topics' sources).
	about string
}

// retryLater counts one more failure in a row of key, about what it was asked about, in *retries (made on first use)
// and returns its new state: how many failures, and when it may be tried again (retryWait). The knowledge base's steps
// that call the model per source (projects, topics, recordings, items) put a failing one off this way, so it costs a
// call now and then, not every round. A failure about something else starts the count again.
func retryLater(retries *map[string]projectRetry, key, about string) projectRetry {
	if *retries == nil {
		*retries = map[string]projectRetry{}
	}
	r := (*retries)[key]
	if r.about != about {
		r = projectRetry{about: about}
	}
	r.failures++
	r.at = time.Now().Add(retryWait(r.failures))
	(*retries)[key] = r
	return r
}

// retryWaits: key failed about about and its next try is not due at now. One that changed since waits no more.
func retryWaits(retries map[string]projectRetry, key, about string, now time.Time) bool {
	r, ok := retries[key]
	return ok && r.about == about && now.Before(r.at)
}

// deferred: err puts the work off rather than failing it, so nothing waits for it and the step stops: the connection is
// paused, no model is set up for it (the next round asks again, as before; not a source's fault), or the run ended.
func deferred(ctx context.Context, err error) bool {
	return ai.IsPaused(err) || ai.IsNotConfigured(err) || ctx.Err() != nil
}

type projectDigest struct {
	Summary    string     `json:"summary"`
	OneLine    string     `json:"one_line"`
	Goal       string     `json:"goal"`
	Kind       string     `json:"kind"`
	Status     string     `json:"status"`
	Importance string     `json:"importance"`
	Relevance  modelScore `json:"relevance"`
	Tags       []string   `json:"tags"`
	Keywords   []string   `json:"keywords"`
}

// RefreshProjects rewrites the summaries of projects with news, at most max model calls per run, failed ones included
// (they cost a call as much as the others; a project gone meanwhile costs none); it returns how many it rewrote. A project whose rewrite failed waits like a chat
// whose compression failed (retryWait), so a model that keeps failing (2026-11-08: a model name the endpoint did not
// serve, 404 every two minutes) does not cost a call and a log line every round. Only the knowledge base loop calls it.
func (e *Engine) RefreshProjects(ctx context.Context, max int) int {
	if e.Model == nil {
		return 0
	}
	now := time.Now()
	var dirty []*Project
	live := map[string]bool{}
	for _, p := range e.Store.Projects() {
		if p.Dirty && p.Status != "ignored" && p.Live() {
			live[p.Slug] = true
			if retryWaits(e.projectRetries, p.Slug, "", now) {
				continue
			}
			dirty = append(dirty, p)
		}
	}
	for slug := range e.projectRetries { // done, merged or gone in the meantime
		if !live[slug] {
			delete(e.projectRetries, slug)
		}
	}
	sort.Slice(dirty, func(i, j int) bool { return dirty[i].Relevance > dirty[j].Relevance })
	n, calls := 0, 0
	for _, p := range dirty {
		if calls >= max || ctx.Err() != nil {
			break
		}
		called, err := e.refreshProjectCall(ctx, p)
		if called {
			calls++
		}
		if err != nil {
			if deferred(ctx, err) { // the connection is held, no model, or the run ended: the rest wait (still dirty)
				break
			}
			r := retryLater(&e.projectRetries, p.Slug, "")
			e.logf("project %s: %v (failed %d time(s) in a row, next try in %s)", p.Slug, err, r.failures, retryWait(r.failures))
			continue
		}
		delete(e.projectRetries, p.Slug)
		if called {
			n++
		}
	}
	return n
}

func (e *Engine) refreshProject(ctx context.Context, p *Project) error {
	_, err := e.refreshProjectCall(ctx, p)
	return err
}

// refreshProjectCall rewrites p's summary; called: the model was asked (not for a project gone meanwhile).
func (e *Engine) refreshProjectCall(ctx context.Context, p *Project) (called bool, err error) {
	current, ok := e.Store.Project(p.Slug)
	if !ok {
		return false, nil
	}
	p = current
	in := e.projectInput(p)
	var d projectDigest
	cctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	if err := e.modelFor("kb.project").JSONMax(cctx, prompts.RenderKBProject(prompts.KBOwner{Owner: e.Owner}), prompts.RenderKBProjectInput(in), 8000, &d); err != nil {
		return true, err
	}
	return true, e.commitProject(p, in, d)
}

func (e *Engine) projectInput(p *Project) prompts.KBProjectInput {
	in := prompts.KBProjectInput{Pinned: p.Pinned, Name: p.Name, Kind: p.Kind, Aliases: p.Aliases, Tags: e.TopTags(40),
		Summary: e.Store.Summary(KindProject, p.Slug), Log: e.Store.Log(p.Slug, 40)}
	for i, cid := range p.Chats {
		if i >= 8 {
			break
		}
		sum := e.Store.Summary(KindChat, cid)
		if r := []rune(sum); len(r) > 1500 {
			sum = string(r[:1500]) + "…"
		}
		in.Chats = append(in.Chats, prompts.KBProjectChat{ID: cid, Summary: sum})
	}
	in.Moments = e.projectMomentsForPrompt(p, 5)
	return in
}
