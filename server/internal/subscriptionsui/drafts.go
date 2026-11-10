package subscriptionsui

import (
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/subscriptions"
)

// Drafts are the model drafts (#2040) in progress or done, kept by the manager for the pages: drafting takes up to four
// minutes, longer than an app waits for a press, so a press starts one and the page shows it running until it is done
// (the manager then redraws the pages; refresh_after is the fallback). Memory only: a manager restart forgets them,
// and the user presses again. One draft runs at a time per connector, whose tools a draft calls outside its rate limit.
type Drafts struct {
	mu   sync.Mutex
	jobs map[string]*draftJob
	// done is called when a draft finishes, for the manager to redraw the pages on every app.
	done func()
}

type draftJob struct {
	connector string
	running   bool
	finished  time.Time
	result    subscriptions.Generated
	err       error
}

// PagesID is the id the apps draw the pages under, as if an extension's (their SubscriptionsUI.id / SUBSCRIPTIONS_UI):
// an `extension_ui` invalidate for it redraws them.
const PagesID = "-subscriptions"

// keepDrafts is how long a finished draft waits to be looked at.
const keepDrafts = 30 * time.Minute

// NewDrafts keeps drafts for the pages; done (may be nil) is called when one finishes.
func NewDrafts(done func()) *Drafts {
	return &Drafts{jobs: map[string]*draftJob{}, done: done}
}

func sourceKey(connector, source string) string { return "src:" + connector + "/" + source }
func subKey(id string) string                   { return "sub:" + id }

// start runs fn in the background under key, unless a draft for the same connector is running: false then.
func (d *Drafts) start(key, connector string, fn func() (subscriptions.Generated, error)) bool {
	d.mu.Lock()
	d.prune()
	for _, j := range d.jobs {
		if j.running && j.connector == connector {
			d.mu.Unlock()
			return false
		}
	}
	j := &draftJob{connector: connector, running: true}
	d.jobs[key] = j
	d.mu.Unlock()
	go func() {
		result, err := fn()
		d.mu.Lock()
		j.running, j.finished, j.result, j.err = false, time.Now(), result, err
		d.mu.Unlock()
		if d.done != nil {
			d.done()
		}
	}()
	return true
}

// get is the draft under key, as it is now.
func (d *Drafts) get(key string) (draftJob, bool) {
	if d == nil {
		return draftJob{}, false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.prune()
	j, ok := d.jobs[key]
	if !ok {
		return draftJob{}, false
	}
	return *j, true
}

func (d *Drafts) forget(key string) {
	if d == nil {
		return
	}
	d.mu.Lock()
	delete(d.jobs, key)
	d.mu.Unlock()
}

func (d *Drafts) prune() {
	for k, j := range d.jobs {
		if !j.running && time.Since(j.finished) > keepDrafts {
			delete(d.jobs, k)
		}
	}
}

// draftRecipe is what a finished draft offers the setup form: the recipe that passed, or as far as a refused one got.
func (j draftJob) draftRecipe() *subscriptions.Recipe {
	if j.err == nil {
		r := j.result.Recipe
		return &r
	}
	if se, ok := j.err.(*subscriptions.Error); ok && se.Draft != nil {
		return se.Draft
	}
	return nil
}

// draftDryRun is the dry run a finished draft brings, if any.
func (j draftJob) draftDryRun() *subscriptions.DryRun {
	if j.err == nil {
		d := j.result.DryRun
		return &d
	}
	if se, ok := j.err.(*subscriptions.Error); ok {
		return se.DryRun
	}
	return nil
}
