package hub

import (
	"fmt"
	"net/http"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/errreport"
	"github.com/nwverse/persona/server/internal/extwire"
)

// The hub's own errors (docs/extensions/extensions.md "Error reports"): its log's error lines and the panics of its handlers
// become reports, sent to the managers, which own the pipeline and the extensions. Only while the managers' report
// says error_reports (an extension takes them); otherwise nothing is built.

const (
	errorReportsFeature = "error_reports"
	hubErrorsQueue      = 200
	hubErrorsBatch      = 20
	hubErrorsEvery      = 2 * time.Second
)

// hubErrors is the hub's queue of its own reports.
type hubErrors struct {
	once  sync.Once
	queue chan wire.ErrorReport
	// on: an extension takes error reports, as the managers' last reports said (errorReportsChanged). Read without
	// a lock: the log's writer asks it, under whatever lock the line was logged.
	on atomic.Bool
}

// errorReportsOn says whether an extension takes error reports now.
func (h *Hub) errorReportsOn() bool { return h.errs.on.Load() }

// errorReportsChanged reads it again from the managers' reports (they came, changed or went).
func (h *Hub) errorReportsChanged() {
	h.errs.on.Store(slices.Contains(h.extReport().Features, errorReportsFeature))
}

// errorBuild is what the hub says of itself in a report.
func (h *Hub) errorBuild() errreport.Build {
	b := errreport.Build{Source: wire.ErrorSourceHub, Release: h.build.Version, Commit: h.build.Commit}
	if h.build.Number > 0 {
		b.Build = strconv.FormatInt(h.build.Number, 10)
	}
	return b
}

// teeErrors makes the hub's log also report its error lines.
func (h *Hub) teeErrors() {
	h.logger.SetOutput(&errreport.Tee{W: h.logger.Writer(), Build: h.errorBuild(), On: h.errorReportsOn, Sink: h.reportError})
}

// reportError queues one of the hub's reports. Never blocks and never logs: the log's writer calls it.
func (h *Hub) reportError(r wire.ErrorReport) {
	h.errs.once.Do(func() {
		h.errs.queue = make(chan wire.ErrorReport, hubErrorsQueue)
		go h.sendErrorReports()
	})
	select {
	case h.errs.queue <- r:
	default: // more than the managers take: these are dropped
	}
}

// sendErrorReports sends the queued reports to the managers in batches.
func (h *Hub) sendErrorReports() {
	tick := time.NewTicker(hubErrorsEvery)
	defer tick.Stop()
	var batch []wire.ErrorReport
	for {
		select {
		case r := <-h.errs.queue:
			batch = append(batch, r)
			if len(batch) < hubErrorsBatch {
				continue
			}
		case <-tick.C:
			if len(batch) == 0 {
				continue
			}
		}
		if h.errorReportsOn() {
			_ = h.notifyManagers(extwire.EventErrorReports, extwire.ErrorReports{Reports: batch}) // a manager away: lost
		}
		batch = nil
	}
}

// recoverPanics answers a handler's panic with a 500, logs it and reports it, so one bad request does not take the
// hub's connection (or the hub) down. http.ErrAbortHandler is the handler's way to abort: passed on.
func recoverPanics(h *Hub, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			v := recover()
			if v == nil {
				return
			}
			if v == http.ErrAbortHandler {
				panic(v)
			}
			if strings.HasPrefix(r.URL.Path, "/share/") || strings.HasPrefix(r.URL.Path, publicSharePrefix) || strings.Contains(r.URL.Path, "/agent-shares") {
				// A panic value or captured stack can contain a bearer or prepared
				// content. Sharing never sends either to logs or error telemetry.
				shareHeaders(w)
				h.logf("sharing handler failed on %s", routeOf(r))
				h.shareTransportError(w, r, http.StatusInternalServerError, "internal")
				return
			}
			stack := debug.Stack()
			where := r.Method + " " + routeOf(r)
			// one Write with the marker: the log's tee does not report it again
			h.logf("%s handler %s: %v\n%s", errreport.Reported, where, v, stack)
			if h.errorReportsOn() {
				h.reportError(h.errorBuild().Panic(v, stack, where))
			}
			http.Error(w, `{"error":"internal error","code":"internal"}`, http.StatusInternalServerError)
		}()
		next.ServeHTTP(w, r)
	})
}

// routeOf says which route r is without its ids: the mux's pattern when it has one, else the path's first three
// segments (/api/v1/sessions).
func routeOf(r *http.Request) string {
	if strings.HasPrefix(r.URL.Path, "/share/") || strings.HasPrefix(r.URL.Path, publicSharePrefix) {
		return loggedPath(r.URL.Path)
	}
	if r.Pattern != "" {
		if _, p, ok := strings.Cut(r.Pattern, " "); ok {
			return p
		}
		return r.Pattern
	}
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/"), "/", 4)
	if len(parts) > 3 {
		parts = parts[:3]
	}
	return fmt.Sprintf("/%s", strings.Join(parts, "/"))
}
