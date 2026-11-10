package hub

import (
	"cmp"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/i18n"
)

// The merged inbox (GET /api/v1/inbox naming no manager) is what every app asks for again after each inbox push and
// on every connect. A push to k apps had each manager list, encode and send its whole inbox k times, and the hub scan,
// localize, merge and gzip it k times, all for the same bytes. So an answer every manager gave with a 2xx is kept a
// moment (inboxAnswerTTL) and given to the requests that would have asked for it again while nothing it came from
// has moved:
//
//   - the request: its path and query (the query's keys sorted), its caller's role, its language, and the managers it
//     asks, in order (listManagers);
//   - each manager's inbox generation, and a global one. A manager's moves when it tells the apps {"type":"inbox"}
//     (relayBroadcastFrom, before they get the push), and before and after any request that is no GET or HEAD passes
//     to it (relayHTTP, relayTo): a client that changed something and then lists sees what it changed. The global one
//     moves with the roster and the default (ownersChanged).
//
// An answer fetched while a generation moved is not kept. Requests that come while the same one is being fetched
// wait for it, each as long as its own context lets it, and fetch for themselves when it did not come out a 2xx from
// every manager. A request that names a manager, an extension's (it is its manager's) and one with a body go the way
// they went (mergeList). What is kept is the status, the merge's own headers and its bytes, none of what the
// middleware set (X-Request-Id, Cache-Control).
//
// inboxAnswerTTL 0 turns it off: every request fetches its own, as before.
const inboxAnswerTTL = 2 * time.Second

const (
	inboxAnswersMax     = 8        // answers kept at most (one per request shape: role, language, query)
	inboxAnswersMaxSize = 32 << 20 // their bytes at most (inboxAnswer.size)
	inboxAnswerMaxBody  = 8 << 20  // a larger answer goes to the requests waiting for it, and is not kept
)

// inboxAnswerKey is what a merged inbox answer depends on besides the generations.
type inboxAnswerKey struct {
	path, query, role string
	lang              i18n.Lang
	managers          string // the managers asked, in order, a newline after each
}

// inboxAnswers are the merged inbox answers kept, and the ones being fetched.
type inboxAnswers struct {
	off bool // tests: every request fetches its own (as inboxAnswerTTL 0)

	mu     sync.Mutex
	global uint64
	gens   map[string]uint64 // manager id -> its inbox generation
	kept   map[inboxAnswerKey]*inboxAnswer
	calls  map[inboxAnswerKey]*inboxCall
	size   int // of kept
}

// inboxCall is one fetch of a merged inbox, for the requests that wait for it.
type inboxCall struct {
	stamp uint64    // the generations it was asked at
	at    time.Time // and when
	done  chan struct{}
	ans   *inboxAnswer // set before done closes

	waiters int // the requests that joined it (under inboxAnswers.mu)
}

// inboxAnswer is one merged inbox answer as writeMerged wrote it.
type inboxAnswer struct {
	status int
	header http.Header // the merge's own headers
	body   []byte
	shared bool // every manager asked answered with a 2xx, and so did the merge: others may have it

	stamp uint64    // the generations it was fetched at (kept answers)
	at    time.Time // and when that fetch began
}

// size is what a kept answer counts against inboxAnswersMaxSize.
func (a *inboxAnswer) size() int { return len(a.body) }

// changed moves manager id's inbox generation: the answers that asked it are not given again.
func (c *inboxAnswers) changed(id string) {
	c.mu.Lock()
	if c.gens == nil {
		c.gens = map[string]uint64{}
	}
	c.gens[id]++
	c.mu.Unlock()
}

// changedAll moves the global generation: no answer kept is given again.
func (c *inboxAnswers) changedAll() {
	c.mu.Lock()
	c.global++
	c.mu.Unlock()
}

// stampOf is the generations an answer of ids depends on, summed: each only grows, so the sum moves when any of
// them does. Under c.mu.
func (c *inboxAnswers) stampOf(ids []string) uint64 {
	s := c.global
	for _, id := range ids {
		s += c.gens[id]
	}
	return s
}

// join is the answer kept for k when it still holds; else the fetch of it under way, leader when there was none and
// this request is to make it (and then finish it).
func (c *inboxAnswers) join(k inboxAnswerKey, ids []string, now time.Time) (kept *inboxAnswer, call *inboxCall, leader bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	stamp := c.stampOf(ids)
	if a := c.kept[k]; a != nil {
		if a.stamp == stamp && now.Sub(a.at) < inboxAnswerTTL {
			return a, nil, false
		}
		c.drop(k)
	}
	if call := c.calls[k]; call != nil && call.stamp == stamp {
		call.waiters++
		return nil, call, false
	}
	if c.calls == nil {
		c.calls = map[inboxAnswerKey]*inboxCall{}
	}
	call = &inboxCall{stamp: stamp, at: now, done: make(chan struct{})}
	c.calls[k] = call
	return nil, call, true
}

// finish hands a, call's answer, to the requests waiting for it, and keeps it when others may have it and nothing it
// came from moved while it was fetched.
func (c *inboxAnswers) finish(k inboxAnswerKey, ids []string, call *inboxCall, a *inboxAnswer) {
	c.mu.Lock()
	if c.calls[k] == call {
		delete(c.calls, k)
	}
	if a.shared && len(a.body) <= inboxAnswerMaxBody && c.stampOf(ids) == call.stamp {
		a.stamp, a.at = call.stamp, call.at
		c.keep(k, a)
	}
	c.mu.Unlock()
	call.ans = a
	close(call.done)
}

// keep keeps a for k, dropping the oldest answers beyond the bounds. Under c.mu.
func (c *inboxAnswers) keep(k inboxAnswerKey, a *inboxAnswer) {
	c.drop(k)
	if c.kept == nil {
		c.kept = map[inboxAnswerKey]*inboxAnswer{}
	}
	for len(c.kept) > 0 && (len(c.kept) >= inboxAnswersMax || c.size+a.size() > inboxAnswersMaxSize) {
		var oldest inboxAnswerKey
		var at time.Time
		first := true
		for key, b := range c.kept {
			if first || b.at.Before(at) {
				oldest, at, first = key, b.at, false
			}
		}
		c.drop(oldest)
	}
	c.kept[k] = a
	c.size += a.size()
}

// drop forgets the answer kept for k. Under c.mu.
func (c *inboxAnswers) drop(k inboxAnswerKey) {
	if a := c.kept[k]; a != nil {
		c.size -= a.size()
		delete(c.kept, k)
	}
}

// mergedInbox answers the merged inbox list: what mergeList answers, reused while it holds (see the top of the file).
func (h *Hub) mergedInbox(w http.ResponseWriter, r *http.Request, key string, ask func(w http.ResponseWriter, r *http.Request, id string)) {
	c := &h.inboxAnswers
	if inboxAnswerTTL <= 0 || c.off || r.Method != http.MethodGet || r.ContentLength != 0 {
		h.mergeList(w, r, key, ask)
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	named, nerr := impliedManager(r)
	if err != nil || nerr != nil || named != "" || principalOf(r.Context()).Role == roleExtension {
		h.mergeList(w, r, key, ask)
		return
	}
	ids := h.listManagers(r.Context())
	k := inboxAnswerKey{
		path: r.URL.Path, query: query.Encode(), role: principalOf(r.Context()).Role, lang: h.requestLang(r),
		managers: strings.Join(ids, "\n") + "\n",
	}
	kept, call, leader := c.join(k, ids, time.Now())
	switch {
	case kept != nil:
		kept.write(w)
		return
	case leader:
		a := h.mergedAnswer(r, key, ids, ask)
		c.finish(k, ids, call, a)
		a.write(w)
		return
	}
	select {
	case <-call.done:
		if call.ans.shared {
			call.ans.write(w)
			return
		}
	case <-r.Context().Done():
	}
	h.mergedAnswer(r, key, ids, ask).write(w)
}

// mergedAnswer is what mergeList writes when it asks ids (none named, no body), as one answer.
func (h *Hub) mergedAnswer(r *http.Request, key string, ids []string, ask func(w http.ResponseWriter, r *http.Request, id string)) *inboxAnswer {
	out := newBufferedWriter()
	if len(ids) == 0 { // listManagers always has the default; as mergeList, should it not
		writeErrCode(out, http.StatusServiceUnavailable, assistantwire.CodeAssistantOffline, errAssistantOffline)
		return &inboxAnswer{status: out.status, header: out.header, body: out.body.Bytes()}
	}
	shared := true
	answers := make([]listAnswer, 0, len(ids))
	for _, id := range ids {
		bw := newBufferedWriter()
		ask(bw, withBody(r, nil), id)
		ans := listAnswer{id: id, status: cmp.Or(bw.status, http.StatusOK), header: bw.header, body: bw.body.Bytes(), localize: bw.localize}
		shared = shared && ans.ok()
		answers = append(answers, ans)
	}
	writeMerged(out, key, answers)
	a := &inboxAnswer{status: cmp.Or(out.status, http.StatusOK), header: out.header, body: out.body.Bytes()}
	a.shared = shared && a.status >= 200 && a.status < 300
	return a
}

// write sends a as writeMerged would have sent it to w: its headers over what w has, its status, its bytes. Each
// request gets its own copy of the headers (the layers outside add to them).
func (a *inboxAnswer) write(w http.ResponseWriter) {
	hd := w.Header()
	for k, v := range a.header {
		hd[k] = slices.Clone(v)
	}
	w.WriteHeader(a.status)
	_, _ = w.Write(a.body)
}
