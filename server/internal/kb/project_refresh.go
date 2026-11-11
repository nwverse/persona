package kb

import (
	"context"
	"sort"
	"sync"
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
}

// retrySet is a step's sources whose call failed, by key: the knowledge base's steps that call the model per source
// (projects, topics, recordings, items) put a failing one off this way (retryWait), so it costs a call now and then,
// not every round. Safe for concurrent use: the knowledge points lane runs two calls of a step at once (#4247).
type retrySet struct {
	mu sync.Mutex
	m  map[string]projectRetry
}

// waits: key failed and its next try is not due at now.
func (r *retrySet) waits(key string, now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return now.Before(r.m[key].at)
}

// fail counts one more failure in a row of key and returns its new state: how many failures, and when it may be tried
// again.
func (r *retrySet) fail(key string) projectRetry {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.m == nil {
		r.m = map[string]projectRetry{}
	}
	f := r.m[key]
	f.failures++
	f.at = time.Now().Add(retryWait(f.failures))
	r.m[key] = f
	return f
}

// clear forgets key's failures (it succeeded).
func (r *retrySet) clear(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.m, key)
}

// keep forgets the failures of every key not in due (done, merged or gone in the meantime).
func (r *retrySet) keep(due map[string]bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for key := range r.m {
		if !due[key] {
			delete(r.m, key)
		}
	}
}

// get is key's failures (tests).
func (r *retrySet) get(key string) (projectRetry, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	f, ok := r.m[key]
	return f, ok
}

// set replaces key's failures (tests: a wait made over).
func (r *retrySet) set(key string, f projectRetry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.m == nil {
		r.m = map[string]projectRetry{}
	}
	r.m[key] = f
}

// len is how many keys wait (tests).
func (r *retrySet) len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.m)
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

// RefreshProjects rewrites the summaries of projects with news, at most max calls per run, failed ones included (they
// cost a call as much as the others); it returns how many it rewrote. A project whose rewrite failed waits like a chat
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
			if e.projectRetries.waits(p.Slug, now) {
				continue
			}
			dirty = append(dirty, p)
		}
	}
	e.projectRetries.keep(live) // done, merged or gone in the meantime
	sort.Slice(dirty, func(i, j int) bool { return dirty[i].Relevance > dirty[j].Relevance })
	n, calls := 0, 0
	for _, p := range dirty {
		if calls >= max || ctx.Err() != nil {
			break
		}
		calls++
		if err := e.refreshProject(ctx, p); err != nil {
			if ai.IsPaused(err) || ctx.Err() != nil { // the connection is held or the run ended: the rest wait (still dirty)
				break
			}
			r := e.projectRetries.fail(p.Slug)
			e.logf("project %s: %v (failed %d time(s) in a row, next try in %s)", p.Slug, err, r.failures, retryWait(r.failures))
			continue
		}
		e.projectRetries.clear(p.Slug)
		n++
	}
	return n
}

func (e *Engine) refreshProject(ctx context.Context, p *Project) error {
	current, ok := e.Store.Project(p.Slug)
	if !ok {
		return nil
	}
	p = current
	in := e.projectInput(p)
	var d projectDigest
	cctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	if err := e.modelFor("kb.project").JSONMax(cctx, prompts.RenderKBProject(prompts.KBOwner{Owner: e.ownerName()}), prompts.RenderKBProjectInput(in), 8000, &d); err != nil {
		return err
	}
	return e.commitProject(p, in, d)
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
