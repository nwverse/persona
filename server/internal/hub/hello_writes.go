package hub

import (
	"context"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/i18n"
)

// helloWriteWorkers keeps persistence off an app connection's read loop. There is one consumer for each setting
// group; its queue applies reports in message order and coalesces everything waiting into one snapshot save. Phone
// registrations additionally coalesce by device, so only the newest queued tool set for a device is written.
type helloWriteWorkers struct {
	once                           sync.Once
	cancel                         context.CancelFunc
	appsWake, langsWake, toolsWake chan struct{}
	workers                        sync.WaitGroup
	apps, langs, tools             sync.WaitGroup // reports queued but not yet consumed; tests use wait
}

type clientAppUpdate struct {
	cred   string
	change func(assistantwire.ClientApp, bool) assistantwire.ClientApp
}

type userLangUpdate struct {
	key  string
	lang i18n.Lang
	at   time.Time
}

// phoneToolUpdates has its own lock: phoneToolsMu covers the store write and must never be taken by the read loop.
type phoneToolUpdates struct {
	mu      sync.Mutex
	pending map[string]db.PhoneToolRegistration
	order   []string
	reports int // accepted messages, including several coalesced into one device snapshot
}

func (h *Hub) startHelloWriters() {
	w := &h.helloWrites
	w.once.Do(func() {
		parent := h.deployCtx
		if parent == nil {
			parent = context.Background()
		}
		ctx, cancel := context.WithCancel(parent)
		w.cancel = cancel
		w.appsWake, w.langsWake, w.toolsWake = make(chan struct{}, 1), make(chan struct{}, 1), make(chan struct{}, 1)
		start := func(wake <-chan struct{}, flush func() bool) {
			w.workers.Add(1)
			h.nodeWorkers.Add(1)
			go func() {
				defer w.workers.Done()
				defer h.nodeWorkers.Done()
				helloWriteLoop(ctx, wake, flush)
			}()
		}
		start(w.appsWake, h.flushClientApps)
		start(w.langsWake, h.flushUserLangs)
		start(w.toolsWake, h.flushPhoneTools)
	})
}

func helloWriteLoop(ctx context.Context, wake <-chan struct{}, flush func() bool) {
	for {
		if flush() {
			continue
		}
		select {
		case <-ctx.Done():
			// Store calls have their own bounded contexts. Drain what the connections already reported before joining
			// the hub's other workers; no report accepted before shutdown is silently discarded.
			for flush() {
			}
			return
		case <-wake:
		}
	}
}

func (h *Hub) stopHelloWriters() {
	if h.helloWrites.cancel != nil {
		h.helloWrites.cancel()
	}
	h.helloWrites.workers.Wait()
}

// wait is the consumers' idle signal for tests: every report queued before it has either been saved or skipped after
// a failed load. Tests call it only after their producers have stopped, as required by sync.WaitGroup.
func (h *Hub) waitHelloWrites() {
	h.helloWrites.apps.Wait()
	h.helloWrites.langs.Wait()
	h.helloWrites.tools.Wait()
}

func wakeHelloWriter(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

func (h *Hub) flushClientApps() bool {
	a := &h.apps
	a.pendingMu.Lock()
	waiting := len(a.pending) > 0
	a.pendingMu.Unlock()
	if !waiting {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	// Load before detaching the batch. Reports that arrive while the first load is held join this same snapshot;
	// on failure, everything detached is skipped without a save and the next report retries.
	loaded := h.prepareClientApps(ctx)
	a.pendingMu.Lock()
	batch := a.pending
	a.pending = nil
	a.pendingMu.Unlock()
	if loaded && len(batch) > 0 {
		h.writeClientAppsLoaded(ctx, batch)
	}
	cancel()
	h.helloWrites.apps.Add(-len(batch))
	return true
}

func (h *Hub) flushUserLangs() bool {
	g := &h.langs
	g.pendingMu.Lock()
	pending := append([]userLangUpdate(nil), g.pending...)
	g.pendingMu.Unlock()
	if len(pending) == 0 {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	loaded := h.prepareUserLangs(ctx, pending)
	g.pendingMu.Lock()
	batch := g.pending
	g.pending = nil
	g.pendingMu.Unlock()
	// A keyed report may have arrived behind an initially keyless pending batch. Cover it before applying anything.
	loaded = loaded && h.prepareUserLangs(ctx, batch)
	if loaded && len(batch) > 0 {
		h.writeUserLangsLoaded(ctx, batch)
	}
	cancel()
	h.helloWrites.langs.Add(-len(batch))
	return true
}

func (h *Hub) flushPhoneTools() bool {
	q := &h.phoneToolUpdates
	q.mu.Lock()
	batch := make([]db.PhoneToolRegistration, 0, len(q.order))
	for _, id := range q.order {
		batch = append(batch, q.pending[id])
	}
	n := q.reports
	q.reports = 0
	clear(q.pending)
	q.order = nil
	q.mu.Unlock()
	if len(batch) == 0 {
		return false
	}
	for _, registration := range batch {
		h.writePhoneTools(registration)
	}
	h.helloWrites.tools.Add(-n)
	return true
}
