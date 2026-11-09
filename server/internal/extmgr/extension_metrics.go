package extmgr

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/extension/host"
	"github.com/nwverse/persona/server/internal/lazyre"
)

// How the extensions are running (docs/extensions/extensions.md "How they run", #1857): the hub counts each extension's calls,
// errors and latency by method (the HTTP it relays by route), its runs and why they ended, and keeps the last lines of
// its log. The counts are in 5-minute buckets: 24 hours in memory, each closed bucket written to the database once an
// hour and kept there 7 days. The admin UI reads them (admin_extensions.go).

const (
	metricsBucket     = 5 * time.Minute
	metricsInMemory   = 24 * time.Hour
	metricsKept       = 7 * 24 * time.Hour
	metricsFlushEvery = time.Hour
	metricsMostMethod = 64   // methods (routes) counted apart per extension; the rest are counted as metricsOther
	metricsMostReason = 16   // exit reasons counted apart per bucket
	metricsMostExits  = 20   // recent exits kept with their time
	logLinesKept      = 2000 // per extension
	logLineMost       = 4 << 10
	metricsOther      = "other"
	legacyStatsID     = "-legacy" // the old paths' calls (#1924), kept as an extension's counters
)

// latencyBounds are the histogram's upper bounds, in milliseconds; the last bucket is everything slower.
var latencyBounds = []float64{1, 2, 5, 10, 20, 50, 100, 200, 500, 1000, 2000, 5000, 10000, 30000, 60000}

// methodCounts are one method's calls in a bucket.
type methodCounts struct {
	Calls  int64            `json:"calls"`
	Errors int64            `json:"errors"`
	Codes  map[string]int64 `json:"codes,omitempty"`
	// Hist counts the calls by latency (latencyBounds, and one more for the slower); a call without a latency
	// (a WebSocket) is not in it.
	Hist []int64 `json:"hist,omitempty"`
}

// metricsBucketData is one extension's counts over one bucket: what is written to the database.
type metricsBucketData struct {
	Methods map[string]*methodCounts `json:"methods,omitempty"`
	Starts  int64                    `json:"starts,omitempty"`
	Stops   int64                    `json:"stops,omitempty"`
	Exits   int64                    `json:"exits,omitempty"`
	Reasons map[string]int64         `json:"reasons,omitempty"`
	// Counters are other counts by name: the old paths' calls by route (legacyStatsID), later the network proxy's.
	Counters map[string]int64 `json:"counters,omitempty"`
}

type metricsBucketRow struct {
	start   time.Time
	data    metricsBucketData
	flushed bool // written as it is now
}

// LogLine is a line of an extension's log.
type LogLine struct {
	Seq    int64     `json:"seq"`
	At     time.Time `json:"at"`
	Source string    `json:"source"` // program: its own; hub: the hub's about it
	Text   string    `json:"text"`
}

// ExitNote is a run that ended in error.
type ExitNote struct {
	At     time.Time `json:"at"`
	Reason string    `json:"reason"`
}

// extensionStats is one extension's (host.Observer).
type extensionStats struct {
	id  string
	now func() time.Time

	mu      sync.Mutex
	buckets []*metricsBucketRow // oldest first, at most metricsInMemory of them
	exits   []ExitNote          // the latest last
	logs    []LogLine           // a ring: logs[next % len] is the oldest once full
	next    int64               // the next line's seq
	subs    map[chan LogLine]struct{}
	redact  func(string) string
}

var _ host.Observer = (*extensionStats)(nil)

// bucketLocked is the bucket of t, made when it is the newest.
func (s *extensionStats) bucketLocked(t time.Time) *metricsBucketData {
	start := t.Truncate(metricsBucket)
	if n := len(s.buckets); n > 0 && !s.buckets[n-1].start.Before(start) {
		for i := n - 1; i >= 0; i-- {
			if s.buckets[i].start.Equal(start) {
				s.buckets[i].flushed = false
				return &s.buckets[i].data
			}
			if s.buckets[i].start.Before(start) {
				break
			}
		}
		// A clock gone back: count it in the newest.
		s.buckets[n-1].flushed = false
		return &s.buckets[n-1].data
	}
	s.buckets = append(s.buckets, &metricsBucketRow{start: start})
	cut := 0
	for cut < len(s.buckets) && start.Sub(s.buckets[cut].start) >= metricsInMemory {
		cut++
	}
	s.buckets = s.buckets[cut:]
	return &s.buckets[len(s.buckets)-1].data
}

// Call counts a call (host.Observer); took < 0 is one without a latency.
func (s *extensionStats) Call(method string, took time.Duration, code string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.bucketLocked(s.now())
	if b.Methods == nil {
		b.Methods = map[string]*methodCounts{}
	}
	m := b.Methods[method]
	if m == nil {
		if len(b.Methods) >= metricsMostMethod {
			method = metricsOther
			m = b.Methods[method]
		}
		if m == nil {
			m = &methodCounts{}
			b.Methods[method] = m
		}
	}
	m.Calls++
	if code != "" {
		if m.Codes == nil {
			m.Codes = map[string]int64{}
		}
		m.Codes[code]++
		if isErrorCode(code) {
			m.Errors++
		}
	}
	if took >= 0 {
		if m.Hist == nil {
			m.Hist = make([]int64, len(latencyBounds)+1)
		}
		ms := float64(took) / float64(time.Millisecond)
		i := sort.SearchFloat64s(latencyBounds, ms)
		m.Hist[i]++
	}
}

// isErrorCode says whether a call ending with code counts as an error: not the caller giving up, nor a request the
// extension turned down as the caller's fault (an HTTP 4xx).
func isErrorCode(code string) bool {
	return code != "" && code != host.CodeCanceled && code != "http_4xx"
}

// Run counts a run (host.Observer).
func (s *extensionStats) Run(event, detail string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	b := s.bucketLocked(now)
	switch event {
	case host.RunStart:
		b.Starts++
	case host.RunStop:
		b.Stops++
	case host.RunExit:
		b.Exits++
		reason := s.redactLocked(cut(detail, 300))
		if b.Reasons == nil {
			b.Reasons = map[string]int64{}
		}
		if _, ok := b.Reasons[reason]; ok || len(b.Reasons) < metricsMostReason {
			b.Reasons[reason]++
		} else {
			b.Reasons[metricsOther]++
		}
		s.exits = append(s.exits, ExitNote{At: now, Reason: reason})
		if len(s.exits) > metricsMostExits {
			s.exits = s.exits[len(s.exits)-metricsMostExits:]
		}
	}
}

// Count adds n to a counter.
func (s *extensionStats) Count(name string, n int64) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.bucketLocked(s.now())
	if b.Counters == nil {
		b.Counters = map[string]int64{}
	}
	b.Counters[name] += n
}

// Log keeps a line (host.Observer), and hands it to whoever follows the log.
func (s *extensionStats) Log(source, line string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	l := LogLine{Seq: s.next, At: s.now(), Source: source, Text: s.redactLocked(cut(line, logLineMost))}
	s.next++
	if len(s.logs) < logLinesKept {
		s.logs = append(s.logs, l)
	} else {
		s.logs[l.Seq%logLinesKept] = l
	}
	for ch := range s.subs {
		select {
		case ch <- l:
		default: // a follower that does not keep up misses lines rather than hold the extension up
		}
	}
}

func (s *extensionStats) redactLocked(line string) string {
	if s.redact != nil {
		line = s.redact(line)
	}
	return redactSecrets(line)
}

func cut(s string, most int) string {
	if len(s) <= most {
		return s
	}
	i := most
	for i > 0 && !utf8RuneStart(s[i]) {
		i--
	}
	return s[:i] + "…"
}

func utf8RuneStart(b byte) bool { return b&0xC0 != 0x80 }

// Lines are the kept lines after seq since (all when since < 0), oldest first, those with q in them (case
// insensitive), at most limit of the latest.
func (s *extensionStats) Lines(since int64, q string, limit int) (lines []LogLine, next int64) {
	if s == nil {
		return []LogLine{}, 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	q = strings.ToLower(q)
	lines = []LogLine{}
	first := s.next - int64(len(s.logs))
	for seq := max(first, since+1); seq < s.next; seq++ {
		l := s.logs[seq%logLinesKept] // while not full, a line's seq is its index
		if q != "" && !strings.Contains(strings.ToLower(l.Text), q) && !strings.Contains(l.Source, q) {
			continue
		}
		lines = append(lines, l)
	}
	if limit > 0 && len(lines) > limit {
		lines = lines[len(lines)-limit:]
	}
	return lines, s.next
}

// follow hands each new line to ch until stop.
func (s *extensionStats) follow(ch chan LogLine) (stop func()) {
	s.mu.Lock()
	if s.subs == nil {
		s.subs = map[chan LogLine]struct{}{}
	}
	s.subs[ch] = struct{}{}
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		delete(s.subs, ch)
		s.mu.Unlock()
	}
}

// secretPatterns find what looks like a credential in a line: it is replaced before the line is kept.
var secretPatterns = []*lazyre.Regexp{
	lazyre.New(`(?i)((?:authorization|cookie)["']?\s*[:=]\s*["']?)(?:(?:bearer|basic|token)\s+)?[^\s"',;&]{6,}`),
	lazyre.New(`(?i)(bearer\s+)[A-Za-z0-9._~+/=-]{8,}`),
	lazyre.New(`(?i)((?:token|secret|password|passwd|api[_-]?key|access[_-]?key)["']?\s*[:=]\s*["']?)[^\s"',;&]{6,}`),
	lazyre.New(`(?i)([?&](?:token|key|secret|access_token|sig)=)[^\s&"']+`),
}

// redactSecrets is line with what looks like a credential in it replaced by "***".
func redactSecrets(line string) string {
	for _, re := range secretPatterns {
		line = re.ReplaceAllString(line, "${1}***")
	}
	return line
}

// ---- reading the counts ----

// MetricsSummary is the counts over a window, all methods together or one method's.
type MetricsSummary struct {
	Calls     int64            `json:"calls"`
	Errors    int64            `json:"errors"`
	ErrorRate float64          `json:"error_rate"`      // errors / calls, 0 without calls
	P50       *float64         `json:"p50_ms"`          // null without a timed call
	P95       *float64         `json:"p95_ms"`          //
	P99       *float64         `json:"p99_ms"`          //
	Codes     map[string]int64 `json:"codes,omitempty"` // the calls by error code
}

// MethodMetrics is one method's.
type MethodMetrics struct {
	Method string `json:"method"`
	MetricsSummary
}

// MetricsPoint is one step of a window's series.
type MetricsPoint struct {
	At     time.Time `json:"at"`
	Calls  int64     `json:"calls"`
	Errors int64     `json:"errors"`
	P95    *float64  `json:"p95_ms"`
}

// ExtensionMetrics is GET /api/v1/admin/extensions/{id}/metrics.
type ExtensionMetrics struct {
	Extension   string           `json:"extension"`
	Window      string           `json:"window"`
	From        time.Time        `json:"from"`
	To          time.Time        `json:"to"`
	Step        int              `json:"step_seconds"`
	Total       MetricsSummary   `json:"total"`
	Methods     []MethodMetrics  `json:"methods"`
	Series      []MetricsPoint   `json:"series"`
	Starts      int64            `json:"starts"`
	Stops       int64            `json:"stops"`
	Exits       int64            `json:"exits"` // runs that ended in error (each restarted after a backoff)
	Reasons     map[string]int64 `json:"exit_reasons,omitempty"`
	RecentExits []ExitNote       `json:"recent_exits"`
	Counters    map[string]int64 `json:"counters,omitempty"`
}

// metricsWindows are the windows the admin UI asks for, and the step of each one's series.
var metricsWindows = map[string]struct{ span, step time.Duration }{
	"1h":  {time.Hour, 5 * time.Minute},
	"24h": {24 * time.Hour, time.Hour},
	"7d":  {7 * 24 * time.Hour, 6 * time.Hour},
}

// accumulate adds b into acc.
func accumulate(acc *metricsBucketData, b *metricsBucketData) {
	for name, m := range b.Methods {
		if acc.Methods == nil {
			acc.Methods = map[string]*methodCounts{}
		}
		a := acc.Methods[name]
		if a == nil {
			a = &methodCounts{}
			acc.Methods[name] = a
		}
		a.Calls += m.Calls
		a.Errors += m.Errors
		for c, n := range m.Codes {
			if a.Codes == nil {
				a.Codes = map[string]int64{}
			}
			a.Codes[c] += n
		}
		if len(m.Hist) > 0 {
			if a.Hist == nil {
				a.Hist = make([]int64, len(latencyBounds)+1)
			}
			for i, n := range m.Hist {
				if i < len(a.Hist) {
					a.Hist[i] += n
				}
			}
		}
	}
	acc.Starts += b.Starts
	acc.Stops += b.Stops
	acc.Exits += b.Exits
	for r, n := range b.Reasons {
		if acc.Reasons == nil {
			acc.Reasons = map[string]int64{}
		}
		acc.Reasons[r] += n
	}
	for c, n := range b.Counters {
		if acc.Counters == nil {
			acc.Counters = map[string]int64{}
		}
		acc.Counters[c] += n
	}
}

// quantile is the q-th quantile of a latency histogram, interpolated inside its bucket; nil when it is empty.
func quantile(hist []int64, q float64) *float64 {
	var total int64
	for _, n := range hist {
		total += n
	}
	if total == 0 {
		return nil
	}
	rank := q * float64(total)
	var seen float64
	for i, n := range hist {
		if n == 0 {
			continue
		}
		if seen+float64(n) >= rank {
			lo := 0.0
			if i > 0 {
				lo = latencyBounds[i-1]
			}
			if i >= len(latencyBounds) { // slower than the last bound: the bound is all that is known
				v := latencyBounds[len(latencyBounds)-1]
				return &v
			}
			hi := latencyBounds[i]
			v := lo + (hi-lo)*(rank-seen)/float64(n)
			v = math.Round(v*10) / 10
			return &v
		}
		seen += float64(n)
	}
	v := latencyBounds[len(latencyBounds)-1]
	return &v
}

// summarize is the summary of some methods' counts.
func summarize(ms ...*methodCounts) MetricsSummary {
	var out MetricsSummary
	hist := make([]int64, len(latencyBounds)+1)
	for _, m := range ms {
		out.Calls += m.Calls
		out.Errors += m.Errors
		for c, n := range m.Codes {
			if out.Codes == nil {
				out.Codes = map[string]int64{}
			}
			out.Codes[c] += n
		}
		for i, n := range m.Hist {
			if i < len(hist) {
				hist[i] += n
			}
		}
	}
	if out.Calls > 0 {
		out.ErrorRate = math.Round(float64(out.Errors)/float64(out.Calls)*10000) / 10000
	}
	out.P50, out.P95, out.P99 = quantile(hist, 0.5), quantile(hist, 0.95), quantile(hist, 0.99)
	return out
}

func methodsOf(b *metricsBucketData) []*methodCounts {
	out := make([]*methodCounts, 0, len(b.Methods))
	for _, m := range b.Methods {
		out = append(out, m)
	}
	return out
}

// report is the counts of the buckets (oldest first) over [from, to), with a series of step.
func report(id, window string, rows []*metricsBucketRow, from, to time.Time, step time.Duration, exits []ExitNote) ExtensionMetrics {
	out := ExtensionMetrics{Extension: id, Window: window, From: from, To: to, Step: int(step / time.Second),
		Methods: []MethodMetrics{}, Series: []MetricsPoint{}, RecentExits: []ExitNote{}}
	var all metricsBucketData
	steps := map[time.Time]*metricsBucketData{}
	for _, r := range rows {
		if r.start.Before(from) || !r.start.Before(to) {
			continue
		}
		accumulate(&all, &r.data)
		at := from.Add(r.start.Sub(from).Truncate(step))
		if steps[at] == nil {
			steps[at] = &metricsBucketData{}
		}
		accumulate(steps[at], &r.data)
	}
	for at := from; at.Before(to); at = at.Add(step) {
		p := MetricsPoint{At: at}
		if d := steps[at]; d != nil {
			s := summarize(methodsOf(d)...)
			p.Calls, p.Errors, p.P95 = s.Calls, s.Errors, s.P95
		}
		out.Series = append(out.Series, p)
	}
	out.Total = summarize(methodsOf(&all)...)
	for name, m := range all.Methods {
		out.Methods = append(out.Methods, MethodMetrics{Method: name, MetricsSummary: summarize(m)})
	}
	slices.SortFunc(out.Methods, func(a, b MethodMetrics) int {
		if a.Calls != b.Calls {
			return int(b.Calls - a.Calls)
		}
		return strings.Compare(a.Method, b.Method)
	})
	out.Starts, out.Stops, out.Exits, out.Reasons, out.Counters = all.Starts, all.Stops, all.Exits, all.Reasons, all.Counters
	for _, e := range exits {
		if !e.At.Before(from) {
			out.RecentExits = append(out.RecentExits, e)
		}
	}
	return out
}

// ---- all the extensions' ----

// extensionMetricsStore keeps the closed buckets (the database).
type extensionMetricsStore interface {
	PutExtensionMetrics(ctx context.Context, extension string, start time.Time, data json.RawMessage) error
	ExtensionMetrics(ctx context.Context, extension string, since time.Time) ([]db.ExtensionMetricsRow, error)
	DeleteExtensionMetricsBefore(ctx context.Context, before time.Time) error
}

var _ extensionMetricsStore = (*db.DB)(nil)

// extensionMetrics are every extension's.
type extensionMetrics struct {
	now    func() time.Time
	redact func(string) string // the hub's own credentials, whatever line they turn up in

	mu   sync.Mutex
	byID map[string]*extensionStats
}

func newExtensionMetrics() *extensionMetrics {
	return &extensionMetrics{now: time.Now, byID: map[string]*extensionStats{}}
}

// of is extension id's, made the first time.
func (m *extensionMetrics) of(id string) *extensionStats {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.byID[id]
	if s == nil {
		s = &extensionStats{id: id, now: m.now, redact: func(line string) string {
			if m.redact != nil { // set once, before the extensions start
				return m.redact(line)
			}
			return line
		}}
		m.byID[id] = s
	}
	return s
}

// known is the extensions with counts.
func (m *extensionMetrics) known() []string {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.byID))
	for id := range m.byID {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

// hubLine keeps a line of the hub's own log that is about an extension ("plugin <id>: …"), with its log.
func (m *extensionMetrics) hubLine(line string) {
	if m == nil {
		return
	}
	rest, ok := strings.CutPrefix(line, "plugin ")
	if !ok {
		if rest, ok = strings.CutPrefix(line, "extension "); !ok {
			return
		}
	}
	id, text, ok := strings.Cut(rest, ": ")
	if !ok || id == "" || strings.ContainsAny(id, " \"") {
		return
	}
	m.mu.Lock()
	s := m.byID[id]
	m.mu.Unlock()
	if s != nil { // only for an extension the hub runs: no stats made for whatever follows "plugin "
		s.Log(host.LogHub, text)
	}
}

// metrics is extension id's over window ("1h", "24h", "7d"): the memory's buckets, with the database's for 7 days.
func (m *extensionMetrics) metrics(ctx context.Context, store extensionMetricsStore, id, window string) ExtensionMetrics {
	w, ok := metricsWindows[window]
	if !ok {
		window, w = "24h", metricsWindows["24h"]
	}
	now := m.now()
	to := now.Truncate(w.step).Add(w.step)
	from := to.Add(-w.span)
	s := m.of(id)
	s.mu.Lock()
	rows := make([]*metricsBucketRow, 0, len(s.buckets))
	have := map[time.Time]bool{}
	for _, b := range s.buckets {
		c := &metricsBucketRow{start: b.start}
		accumulate(&c.data, &b.data)
		rows = append(rows, c)
		have[b.start] = true
	}
	exits := slices.Clone(s.exits)
	s.mu.Unlock()
	if store != nil && w.span > metricsInMemory {
		if saved, err := store.ExtensionMetrics(ctx, id, from); err == nil {
			for _, r := range saved {
				if have[r.Start] {
					continue
				}
				var d metricsBucketData
				if json.Unmarshal(r.Data, &d) == nil {
					rows = append(rows, &metricsBucketRow{start: r.Start, data: d})
				}
			}
		}
	}
	return report(id, window, rows, from, to, w.step, exits)
}

// flush writes the buckets that changed since the last flush and are closed (and the open one too when all), and
// drops what is older than the database keeps.
func (m *extensionMetrics) flush(ctx context.Context, store extensionMetricsStore, all bool) error {
	if m == nil || store == nil {
		return nil
	}
	open := m.now().Truncate(metricsBucket)
	var firstErr error
	for _, id := range m.known() {
		s := m.of(id)
		type pending struct {
			row  *metricsBucketRow
			data []byte
		}
		var todo []pending
		s.mu.Lock()
		for _, b := range s.buckets {
			if b.flushed || (!all && !b.start.Before(open)) {
				continue
			}
			data, err := json.Marshal(b.data)
			if err != nil {
				continue
			}
			todo = append(todo, pending{b, data})
		}
		s.mu.Unlock()
		for _, p := range todo {
			if err := store.PutExtensionMetrics(ctx, id, p.row.start, p.data); err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			s.mu.Lock()
			if d, _ := json.Marshal(p.row.data); string(d) == string(p.data) { // not counted into since
				p.row.flushed = true
			}
			s.mu.Unlock()
		}
	}
	if err := store.DeleteExtensionMetricsBefore(ctx, m.now().Add(-metricsKept)); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

// load takes the database's buckets of the last 24 hours into memory (after a restart), as flushed.
func (m *extensionMetrics) load(ctx context.Context, store extensionMetricsStore, ids []string) {
	if m == nil || store == nil {
		return
	}
	since := m.now().Add(-metricsInMemory)
	for _, id := range ids {
		rows, err := store.ExtensionMetrics(ctx, id, since)
		if err != nil || len(rows) == 0 {
			continue
		}
		s := m.of(id)
		s.mu.Lock()
		have := map[time.Time]bool{}
		for _, b := range s.buckets {
			have[b.start] = true
		}
		for _, r := range rows {
			var d metricsBucketData
			if have[r.Start] || json.Unmarshal(r.Data, &d) != nil {
				continue
			}
			s.buckets = append(s.buckets, &metricsBucketRow{start: r.Start, data: d, flushed: true})
		}
		slices.SortFunc(s.buckets, func(a, b *metricsBucketRow) int { return a.start.Compare(b.start) })
		s.mu.Unlock()
	}
}

// ExtensionOverview is one extension's line of GET /api/v1/admin/extensions/-/metrics.
type ExtensionOverview struct {
	ID       string    `json:"id"`
	Calls    int64     `json:"calls"`
	Errors   int64     `json:"errors"`
	Rate     float64   `json:"error_rate"`
	P95      *float64  `json:"p95_ms"`
	Starts   int64     `json:"starts"`
	Exits    int64     `json:"exits"`
	LastExit *ExitNote `json:"last_exit,omitempty"`
}

// LegacyRoute is one old path's calls (#1924): the window's from the counts, since the hub started from memory.
type LegacyRoute struct {
	Route      string `json:"route"`
	Calls      int64  `json:"calls"`
	SinceStart int64  `json:"since_start"`
}

// MetricsOverview is GET /api/v1/admin/extensions/-/metrics.
type MetricsOverview struct {
	Window     string              `json:"window"`
	From       time.Time           `json:"from"`
	To         time.Time           `json:"to"`
	Extensions []ExtensionOverview `json:"extensions"`
	// Legacy is the calls under the old paths (/api/v1/plugins…): the compatibility layer goes once they stop.
	Legacy []LegacyRoute `json:"legacy_calls"`
}

// overview is every extension in ids (and every one with counts) over window, with the old paths' calls.
func (m *extensionMetrics) overview(ctx context.Context, store extensionMetricsStore, ids []string, window string, legacy map[string]int64) MetricsOverview {
	all := slices.Clone(ids)
	for _, id := range m.known() {
		if id != legacyStatsID && !slices.Contains(all, id) {
			all = append(all, id)
		}
	}
	slices.Sort(all)
	out := MetricsOverview{Extensions: []ExtensionOverview{}, Legacy: []LegacyRoute{}}
	for _, id := range all {
		r := m.metrics(ctx, store, id, window)
		out.Window, out.From, out.To = r.Window, r.From, r.To
		o := ExtensionOverview{ID: id, Calls: r.Total.Calls, Errors: r.Total.Errors, Rate: r.Total.ErrorRate, P95: r.Total.P95,
			Starts: r.Starts, Exits: r.Exits}
		if n := len(r.RecentExits); n > 0 {
			o.LastExit = &r.RecentExits[n-1]
		}
		out.Extensions = append(out.Extensions, o)
	}
	lr := m.metrics(ctx, store, legacyStatsID, window)
	if out.Window == "" {
		out.Window, out.From, out.To = lr.Window, lr.From, lr.To
	}
	routes := map[string]*LegacyRoute{}
	for route, n := range lr.Counters {
		routes[route] = &LegacyRoute{Route: route, Calls: n}
	}
	for route, n := range legacy {
		if routes[route] == nil {
			routes[route] = &LegacyRoute{Route: route}
		}
		routes[route].SinceStart = n
	}
	for _, r := range routes {
		out.Legacy = append(out.Legacy, *r)
	}
	slices.SortFunc(out.Legacy, func(a, b LegacyRoute) int { return strings.Compare(a.Route, b.Route) })
	return out
}

// extensionMetricsLoop writes the counts to the database once an hour (and when the hub stops), after taking the last
// day's back into memory.
func (h *Manager) extensionMetricsLoop(ctx context.Context) {
	ps := h.extensions
	if ps == nil || ps.stats == nil {
		return
	}
	store, _ := h.store.(extensionMetricsStore)
	if store == nil {
		return
	}
	ids := []string{legacyStatsID}
	for _, p := range ps.reg.List() {
		ids = append(ids, p.Record.ID)
	}
	ps.stats.load(ctx, store, ids)
	t := time.NewTicker(metricsFlushEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			fctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			if err := ps.stats.flush(fctx, store, true); err != nil {
				h.logf("extensions: writing the counts: %v", err)
			}
			cancel()
			return
		case <-t.C:
			if err := ps.stats.flush(ctx, store, false); err != nil {
				h.logf("extensions: writing the counts: %v", err)
			}
		}
	}
}

// hostLogf is the log the host writes to: the hub's only (journal), the extension's own through Observe.
func (ps *extensions) hostLogf() func(string, ...any) {
	if ps.journal != nil {
		return ps.journal
	}
	return ps.logf
}

// relayStatusWriter notes the status the extension answered with; Unwrap keeps flushing and WebSocket upgrades working
// through it (http.ResponseController).
type relayStatusWriter struct {
	http.ResponseWriter
	status int
}

func (w *relayStatusWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *relayStatusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

func (w *relayStatusWriter) Flush() {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *relayStatusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// httpRoute is how a relayed request is counted: "HTTP /<first segment>" under the extension's own path, "HTTP
// /api/v1/<prefix>" for its part of the app's API (ids further down would make every request a route of its own).
func httpRoute(id, path string) string {
	for _, base := range []string{"/api/v1/extensions/", "/api/v1/plugins/"} {
		if rest, ok := strings.CutPrefix(path, base+id); ok {
			seg, _, _ := strings.Cut(strings.TrimPrefix(rest, "/"), "/")
			return "HTTP /" + seg
		}
	}
	if rest, ok := strings.CutPrefix(path, "/api/v1/"); ok {
		seg, _, _ := strings.Cut(rest, "/")
		return "HTTP /api/v1/" + seg
	}
	return "HTTP"
}

// httpCode is how a relayed request ended: "" for a success, http_4xx / http_5xx, canceled when the caller left.
func httpCode(r *http.Request, status int) string {
	switch {
	case status == 0 && r.Context().Err() != nil:
		return host.CodeCanceled
	case status >= 500 || status == 0:
		return "http_5xx"
	case status >= 400:
		return "http_4xx"
	}
	return ""
}

// redactOwnSecrets is line without the hub's own token in it.
func (h *Manager) redactOwnSecrets(line string) string {
	if h.cfg != nil && len(h.cfg.Token) >= 8 {
		line = strings.ReplaceAll(line, h.cfg.Token, "***")
	}
	return line
}
