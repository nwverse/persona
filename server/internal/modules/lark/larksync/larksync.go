// Package larksync reads what changed for one Lark user sync binding: the owner's chats (with their threads and
// attachments) and message search, Drive, Docx, Sheets, Bitable, Wiki and Minutes, read as the owner with their user
// access token (internal/larksyncwire, docs/extensions/bots.md "Reading as you: daily sync").
//
// The module owns the open platform here; the manager owns the state. A run never writes anything itself: it asks the
// dedup index what it already holds (Index.Lookup), decides per canonical key (larksyncwire.Decide), and hands back
// only what is new or newer together with the cursors to save and the index records to write (staged on the index
// when it can stage, see stageRecords), so a run that fails to hand its result over moves nothing.
//
//persona:layer application
package larksync

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/botcard"
	"github.com/nwverse/persona/server/internal/larksyncwire"
)

// Defaults for a binding that leaves them out.
const (
	defaultBudget   = 20 * time.Minute
	defaultBackfill = 30 * 24 * time.Hour
)

// Syncer reads one binding. One Syncer per binding: its rate limits (limiter) are its own, so a busy binding never
// slows another down. Safe for concurrent use (a Run and the knowledge base's attachment fetches through Media).
type Syncer struct {
	http  *http.Client
	now   func() time.Time
	sleep func(ctx context.Context, d time.Duration) error
	// jitter is a random duration in [0, d) added to exponential backoff so retries from several bindings spread out.
	jitter func(d time.Duration) time.Duration
	lim    *limiter
}

var (
	_ larksyncwire.Syncer       = (*Syncer)(nil)
	_ larksyncwire.NameResolver = (*Syncer)(nil)
)

// NewSyncer is a binding's reader. httpClient nil uses a client with a 60 s timeout; now nil is time.Now.
func NewSyncer(httpClient *http.Client, now func() time.Time) *Syncer {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 60 * time.Second}
	}
	if now == nil {
		now = time.Now
	}
	s := &Syncer{http: httpClient, now: now, sleep: sleepCtx, jitter: randJitter}
	s.lim = newLimiter(func() time.Time { return s.now() })
	return s
}

// Run reads what changed since cursors (larksyncwire.Syncer). Every resource is read on its own: one that fails
// costs only its own content and cursor (a ResourceError), except a refused token, which stops the run with an
// *larksyncwire.Error of ErrKindAuth (or the token func's own *Error) next to what was finished before it.
func (s *Syncer) Run(ctx context.Context, b larksyncwire.Binding, token func(ctx context.Context) (string, error), cursors map[string]string, idx larksyncwire.Index) (larksyncwire.Result, error) {
	if token == nil {
		return larksyncwire.Result{}, &larksyncwire.Error{Kind: larksyncwire.ErrKindAuth, Op: "run", Msg: "no token"}
	}
	budget := cmpDuration(b.Budget, defaultBudget)
	// The budget is counted on the syncer's clock (tests drive it); the context bounds what a single request can
	// still take once the budget is gone in real time.
	runCtx, cancel := context.WithTimeout(ctx, budget+time.Minute)
	defer cancel()
	r := newRun(s, b, token, cursors, idx, s.now().Add(budget))
	err := r.read(runCtx)
	switch {
	case err != nil && ctx.Err() != nil:
		return larksyncwire.Result{}, ctx.Err() // the caller gave up: nothing of this run counts
	case err != nil && runCtx.Err() != nil:
		r.partial, err = true, nil // the budget's safety net: what finished counts, the rest is read next time
	}
	res, ferr := r.finish(ctx)
	if ferr != nil {
		return larksyncwire.Result{}, ferr
	}
	return res, err
}

// read runs the binding's readers in order. In full mode, IM and search come first so the documents and minutes their
// messages link to are found; Drive and Wiki follow, then linked cloud documents and minutes. In IM incremental mode
// (larksyncwire.ModeIM), only IM chats, p2p chat discovery, and the cloud documents and minutes linked in those new
// messages run, with reserved budget slices so a busy chat backlog cannot starve p2p discovery or linked docs.
func (r *run) read(ctx context.Context) error {
	if r.b.Mode == larksyncwire.ModeIM {
		if r.b.Tier != larksyncwire.TierFull {
			return nil
		}
		budget := r.deadline.Sub(r.start)
		steps := []struct {
			fn       func(context.Context) error
			deadline time.Time
		}{
			{r.readIM, r.start.Add(budget * 60 / 100)},
			{r.readP2PDiscovery, r.start.Add(budget * 80 / 100)},
			{r.readLinkedDocs, r.deadline},
			{r.readMinutes, r.deadline},
		}
		for _, st := range steps {
			if !r.s.now().Before(r.deadline) {
				r.partial = true
				return nil
			}
			r.stepDeadline = st.deadline
			r.sess.deadline = st.deadline
			err := st.fn(ctx)
			r.stepDeadline = time.Time{}
			r.sess.deadline = r.deadline
			if err != nil {
				return err
			}
		}
		return nil
	}
	steps := []func(context.Context) error{r.readIM, r.readSearch, r.readDrive, r.readWiki, r.readLinkedDocs, r.readMinutes}
	for _, step := range steps {
		if r.overBudget() {
			r.partial = true
			return nil
		}
		if err := step(ctx); err != nil {
			return err
		}
	}
	return nil
}

// ResolveUserNames looks up Lark user display names by open_id using the binding's user token and per-binding rate
// limiter (larksyncwire.NameResolver). Non-user ids (such as cli_* bots) and per-user errors are skipped; a missing
// contact scope, refused token, or cancelled context stops lookups and returns that error alongside whatever names were
// already resolved.
func (s *Syncer) ResolveUserNames(ctx context.Context, b larksyncwire.Binding, token func(ctx context.Context) (string, error), openIDs []string) (map[string]string, error) {
	if token == nil {
		return nil, &larksyncwire.Error{Kind: larksyncwire.ErrKindAuth, Op: "user name", Msg: "no token"}
	}
	budget := cmpDuration(b.Budget, 2*time.Minute)
	deadline := s.now().Add(budget)
	runCtx, cancel := context.WithTimeout(ctx, budget+30*time.Second)
	defer cancel()
	sess := &session{s: s, base: botcard.ResolveDomain(b.App.Domain), token: token, deadline: deadline}
	out := map[string]string{}
	seen := map[string]bool{}
	for _, id := range openIDs {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] || !isContactUser(id, "") {
			continue
		}
		seen[id] = true
		if !s.now().Before(deadline) {
			return out, context.DeadlineExceeded
		}
		var data struct {
			User struct {
				Name string `json:"name"`
			} `json:"user"`
		}
		err := sess.getJSON(runCtx, request{
			family: famIM,
			op:     "user name",
			path:   "/open-apis/contact/v3/users/" + pathEsc(id),
			query:  map[string]string{"user_id_type": "open_id"},
		}, &data)
		if err != nil {
			if ctx.Err() != nil {
				return out, ctx.Err()
			}
			if runCtx.Err() != nil {
				return out, runCtx.Err()
			}
			if fatal(err) {
				return out, err
			}
			if e := asSyncError(err); e != nil && e.Kind == larksyncwire.ErrKindScope {
				return out, err
			}
			continue
		}
		if name := strings.TrimSpace(data.User.Name); name != "" {
			out[id] = name
		}
	}
	return out, nil
}

// Media fetches a message's image or file by its attachment Ref ("<binding key>|<message id>|<key>|<image|file>"),
// at most limit bytes (botcard.ErrTooLarge beyond), and returns it with its content type.
func (s *Syncer) Media(ctx context.Context, b larksyncwire.Binding, token func(ctx context.Context) (string, error), ref string, limit int64) ([]byte, string, error) {
	if token == nil {
		return nil, "", &larksyncwire.Error{Kind: larksyncwire.ErrKindAuth, Op: "media", Msg: "no token"}
	}
	binding, messageID, key, kind, ok := parseRef(ref)
	if !ok || binding != b.Key {
		return nil, "", &larksyncwire.Error{Kind: larksyncwire.ErrKindOther, Op: "media", Msg: "not an attachment of this binding"}
	}
	if limit <= 0 {
		limit = 20 << 20
	}
	sess := &session{s: s, base: botcard.ResolveDomain(b.App.Domain), token: token}
	return sess.download(ctx, request{family: famIM, op: "media", path: "/open-apis/im/v1/messages/" + pathEsc(messageID) + "/resources/" + pathEsc(key), query: map[string]string{"type": kind}}, limit)
}

// attachmentRef is the Ref of a message's file or image: the binding it was read through comes first, so the
// knowledge base's lazy fetch goes back to a binding that can read it.
func attachmentRef(binding, messageID, key, kind string) string {
	return binding + "|" + messageID + "|" + key + "|" + kind
}

// parseRef splits an attachmentRef. The parts after the binding key never hold a "|", so they are taken from the
// right.
func parseRef(ref string) (binding, messageID, key, kind string, ok bool) {
	parts := strings.Split(ref, "|")
	if len(parts) < 4 {
		return "", "", "", "", false
	}
	n := len(parts)
	binding = strings.Join(parts[:n-3], "|")
	messageID, key, kind = parts[n-3], parts[n-2], parts[n-1]
	if binding == "" || messageID == "" || key == "" || (kind != "image" && kind != "file") {
		return "", "", "", "", false
	}
	return binding, messageID, key, kind, true
}

// openIDOf is the owner's open id in an account id "<brand>:<tenant_key>:<open_id>", which tells the owner's own
// messages (Message.Mine).
func openIDOf(accountID string) string {
	if i := strings.LastIndex(accountID, ":"); i >= 0 {
		return accountID[i+1:]
	}
	return accountID
}

func cmpDuration(d, def time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return def
}

// asSyncError is err's *larksyncwire.Error, or nil.
func asSyncError(err error) *larksyncwire.Error {
	var e *larksyncwire.Error
	if errors.As(err, &e) {
		return e
	}
	return nil
}
