package node

import (
	"context"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/nodeclient"
)

// questionCalls is the AskUserQuestion lifecycle across PreToolUse, PermissionRequest and PostToolUse. PermissionRequest
// omits tool_use_id, so PreToolUse supplies it and attempt keeps it across reconnects. PostToolUse completes only the
// same id: a late receipt for one identical question can never cancel the next (#2382).
type questionCalls struct {
	mu        sync.Mutex
	pending   map[string][]questionID      // PreToolUse ids awaiting PermissionRequest, by permissionKey
	attempts  map[string]questionID        // assigned ids, by permissionKey + hook attempt
	waiting   map[string][]*questionWaiter // live PermissionRequests, by permissionKey
	completed map[string]time.Time         // PostToolUse receipts, by permissionKey + toolUseID
}

type questionID struct {
	id string
	at time.Time
}

type questionWaiter struct {
	id       string
	cancel   context.CancelFunc
	answered bool
}

func questionExpired(at, now time.Time) bool {
	// A phone answer may arrive after the whole hook timeout, and standby may replay several minutes of history.
	return now.Sub(at) > nodeclient.HookTimeout+nodeclient.ResumeWait
}

func freshQuestionIDs(ids []questionID, now time.Time) []questionID {
	i := 0
	for i < len(ids) && questionExpired(ids[i].at, now) {
		i++
	}
	return ids[i:]
}

// forget expires state older than a hook's reconnect window. q.mu is held.
func (q *questionCalls) forget(now time.Time) {
	for key, ids := range q.pending {
		if ids = freshQuestionIDs(ids, now); len(ids) == 0 {
			delete(q.pending, key)
		} else {
			q.pending[key] = ids
		}
	}
	for key, id := range q.attempts {
		if questionExpired(id.at, now) {
			delete(q.attempts, key)
		}
	}
	for key, at := range q.completed {
		if questionExpired(at, now) {
			delete(q.completed, key)
		}
	}
}

// before remembers a PreToolUse id. Duplicate reports of the same call share one entry.
func (q *questionCalls) before(in hookInput, now time.Time) {
	if in.ToolUseID == "" {
		return
	}
	key := permissionKey(in)
	q.mu.Lock()
	defer q.mu.Unlock()
	q.forget(now)
	for _, id := range q.pending[key] {
		if id.id == in.ToolUseID {
			return
		}
	}
	for _, id := range q.attempts {
		if id.id == in.ToolUseID {
			return
		}
	}
	if q.pending == nil {
		q.pending = map[string][]questionID{}
	}
	q.pending[key] = append(q.pending[key], questionID{id: in.ToolUseID, at: now})
}

// pair returns this PermissionRequest's id. A resumed attempt reuses its assignment and never consumes the next
// question's pending id. Spool replay calls pair too, rebuilding the assignment without creating a waiter.
func (q *questionCalls) pair(in hookInput, attempt string, resume bool, now time.Time) string {
	key := permissionKey(in)
	attemptKey := ""
	if attempt != "" {
		attemptKey = key + "\x00attempt:" + attempt
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.forget(now)
	if attemptKey != "" {
		if id, ok := q.attempts[attemptKey]; ok {
			return id.id
		}
	}
	if resume {
		return ""
	}
	var id string
	if ids := q.pending[key]; len(ids) > 0 {
		id = ids[0].id
		if q.pending[key] = ids[1:]; len(ids) == 1 {
			delete(q.pending, key)
		}
	}
	if id != "" && attemptKey != "" {
		if q.attempts == nil {
			q.attempts = map[string]questionID{}
		}
		q.attempts[attemptKey] = questionID{id: id, at: now}
	}
	return id
}

// watch waits for a phone answer or PostToolUse. A completion that arrived before PermissionRequest is consumed here,
// before any card is created.
func (q *questionCalls) watch(ctx context.Context, in hookInput, attempt string, resume bool) (context.Context, func() bool, func(), string) {
	key := permissionKey(in)
	id := q.pair(in, attempt, resume, time.Now())
	ctx, cancel := context.WithCancel(ctx)
	w := &questionWaiter{id: id, cancel: cancel}
	q.mu.Lock()
	q.forget(time.Now())
	if id != "" {
		if _, ok := q.completed[key+"\x00"+id]; ok {
			w.answered = true
			delete(q.completed, key+"\x00"+id)
		}
	}
	if q.waiting == nil {
		q.waiting = map[string][]*questionWaiter{}
	}
	q.waiting[key] = append(q.waiting[key], w)
	answered := w.answered
	q.mu.Unlock()
	if answered {
		cancel()
	}
	return ctx, func() bool {
			q.mu.Lock()
			defer q.mu.Unlock()
			return w.answered
		}, func() {
			q.mu.Lock()
			ws := q.waiting[key]
			for i, other := range ws {
				if other == w {
					ws = append(ws[:i:i], ws[i+1:]...)
					break
				}
			}
			if len(ws) == 0 {
				delete(q.waiting, key)
			} else {
				q.waiting[key] = ws
			}
			q.mu.Unlock()
			cancel()
		}, id
}

// complete records PostToolUse by exact id, canceling only its own waiter. The receipt is also kept for a PermissionRequest
// or replay that has not arrived yet; a different identical question has a different id and is unaffected.
func (q *questionCalls) complete(in hookInput, now time.Time) {
	key, id := permissionKey(in), in.ToolUseID
	q.mu.Lock()
	defer q.mu.Unlock()
	q.forget(now)
	for _, w := range q.waiting[key] {
		if !w.answered && w.id == id {
			w.answered = true
			w.cancel()
			break
		}
	}
	if id != "" {
		if q.completed == nil {
			q.completed = map[string]time.Time{}
		}
		q.completed[key+"\x00"+id] = now
	}
}
