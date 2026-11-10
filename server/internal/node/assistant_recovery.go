package node

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// A failed Claude turn is recovered before its idle state reaches the manager and the scheduled-run watcher. The
// budget belongs to the conversation, not assistantProc: selecting another model restarts that process.
type assistantTurnRecovery struct {
	retried map[string]bool // one same-model continuation per model
	used    map[string]bool // models this request has already tried, never selected again
	running bool
	pending *assistantPendingFailure
}

// assistantPendingFailure is a new turn result that arrived while its recovery send was still returning. It is not a
// duplicate of the previous result: swallowing it would leave the conversation working forever.
type assistantPendingFailure struct {
	text  string
	kinds []string
}

type assistantRecoveryContextKey struct{}

type assistantRecoveryCall struct {
	recovery *assistantTurnRecovery
	restart  bool
}

var (
	errAssistantRecoveryCancelled = errors.New("assistant recovery superseded")
	errAssistantRecoveryTasks     = errors.New("assistant background tasks are running or not explicitly known to be empty")
)

func withAssistantRecovery(ctx context.Context, recovery *assistantTurnRecovery, restart bool) context.Context {
	return context.WithValue(ctx, assistantRecoveryContextKey{}, assistantRecoveryCall{recovery: recovery, restart: restart})
}

func assistantRecoveryOf(ctx context.Context) (*assistantTurnRecovery, bool) {
	call, _ := ctx.Value(assistantRecoveryContextKey{}).(assistantRecoveryCall)
	return call.recovery, call.restart
}

var (
	assistantServerError = regexp.MustCompile(`\b5[0-9][0-9]\b`)
	assistantRateLimit   = regexp.MustCompile(`\b429\b`)
	assistantBadRequest  = regexp.MustCompile(`\b400\b`)
)

// assistantRecoverableError is deliberately narrower than the Agents' transient-continuation rule. Only the failure
// classes in #3204 replay a request: an upstream safeguards refusal, throttling, or a 5xx. In particular, the hub's
// own 400s, authentication, model routing, usage limits and over-context requests remain the user's to fix.
func assistantRecoverableError(failed bool, result string) bool {
	if !failed {
		return false
	}
	t := strings.ToLower(strings.TrimSpace(result))
	if strings.Contains(t, "safeguards flagged") {
		return true // the provider's transient refusal may itself be an HTTP 400 (#3204)
	}
	if strings.Contains(t, "usage limit") || strings.Contains(t, "usage_limit") || strings.Contains(t, "account_quota_exhausted") {
		return false // Claude's result has no structured kind: a subscription reset is not an ordinary 429 retry
	}
	if assistantBadRequest.MatchString(t) {
		return false // a hub/request 400 is not transient, even when its detail mentions an upstream status
	}
	return assistantRateLimit.MatchString(t) || strings.Contains(t, "rate limit") || strings.Contains(t, "rate_limit") ||
		strings.Contains(t, "too many requests") || assistantServerError.MatchString(t)
}

func (n *Node) clearAssistantRecovery(p *assistantProc) {
	if p == nil || p.id == "" {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.procs[p.id] == p { // a replaced process's late result cannot clear its replacement's recovery
		delete(n.recoveries, p.id)
	}
}

// cancelAssistantRecovery lets a user's newer message take over. Release the old turn's held busy flag too: if that
// newer send is itself refused before delivery, the old conversation must not remain working forever. No idle event is
// published here; a successful new send marks itself working, and a refused one is returned to its caller as before.
func (n *Node) cancelAssistantRecovery(id string) {
	if id == "" {
		return
	}
	n.mu.Lock()
	r := n.recoveries[id]
	delete(n.recoveries, id)
	p := n.procs[id]
	n.mu.Unlock()
	if r != nil && p != nil {
		p.mu.Lock()
		p.busy = false
		p.mu.Unlock()
	}
}

// recoverAssistantResult reserves this model's same-model retry, or a move to the next model after that retry failed.
// True means the caller must keep the turn working: publishing the error now would let a scheduled run fail (and rerun
// the whole task) before recovery ends.
func (n *Node) recoverAssistantResult(p *assistantProc, failed bool, result string, kinds ...string) bool {
	if len(kinds) > 0 && kinds[0] == proto.TurnErrorUsageLimit {
		return false // a subscription reset has its own continuation; an immediate retry cannot spend it back
	}
	if !assistantRecoverableError(failed, result) {
		return false
	}
	p.mu.Lock()
	if p.manual || p.compacting {
		p.mu.Unlock()
		return false
	}
	launcher, cwd, execution := p.launcher, p.cwd, p.model
	key := execution.Model // the launcher's default stays one budget even when its concrete name arrives on a later attempt
	current := key
	if current == "" {
		current = p.replyModel // used only to locate that default in the launcher's ordered list
	}
	p.mu.Unlock()

	n.mu.Lock()
	if n.procs[p.id] != p {
		n.mu.Unlock()
		return false
	}
	r := n.recoveries[p.id]
	if r == nil {
		r = &assistantTurnRecovery{retried: map[string]bool{}, used: map[string]bool{}}
		if current != "" {
			r.used[current] = true
		}
		if n.recoveries == nil {
			n.recoveries = map[string]*assistantTurnRecovery{}
		}
		n.recoveries[p.id] = r
	}
	if r.running {
		r.pending = &assistantPendingFailure{text: result, kinds: kinds}
		n.mu.Unlock()
		p.mu.Lock()
		p.busy = true
		p.mu.Unlock()
		return true
	}
	same := !r.retried[key]
	if same {
		r.retried[key] = true
	}
	if current != "" {
		r.used[current] = true
	}
	r.running = true
	n.mu.Unlock()

	p.mu.Lock()
	p.busy = true // before the goroutine can finish: the caller must never overwrite an already-published final idle
	p.mu.Unlock()
	go n.runAssistantRecovery(p, r, launcher, cwd, execution, current, same)
	return true
}

// nextAssistantModel is the next concrete entry in the launcher's own ordered list. "default" is a placeholder, not a
// fallback. A configured model ID can match an alias's resolved model; custom entries absent from the list start at its
// first concrete entry. Nothing here invents an order for the named Agent presets in ModelProfile.Tiers.
func nextAssistantModel(list proto.LauncherModels, current string, used map[string]bool) (string, bool) {
	if list.Locked != "" {
		return "", false
	}
	at := -1
	for i, m := range list.Models {
		if current != "" && (m.ID == current || m.Resolved == current) {
			at = i
			break
		}
	}
	for i := at + 1; i < len(list.Models); i++ {
		m := list.Models[i]
		if m.Default || m.ID == "" || m.ID == "default" || !proto.ValidModelID(m.ID) || used[m.ID] || (m.Resolved != "" && used[m.Resolved]) {
			continue
		}
		return m.ID, true
	}
	return "", false
}

// assistantExecutionForModel keeps the requested effort when the fallback supports it. Otherwise it follows the
// launcher's default, matching the model picker instead of making the new model refuse an unsupported level.
func assistantExecutionForModel(execution modelExecution, list proto.LauncherModels, id string) modelExecution {
	execution.Model = id
	if execution.Effort == "" || execution.Effort == effortAuto {
		return execution
	}
	i := slices.IndexFunc(list.Models, func(m proto.AgentModel) bool { return m.ID == id })
	if i < 0 || !slices.Contains(list.Models[i].Efforts, execution.Effort) {
		execution.Effort = effortAuto
	}
	return execution
}

// assistantRecoveryModels is launchers.models, replaced by deterministic tests without a CLI or model call.
var assistantRecoveryModels = func(ctx context.Context, n *Node, launcher string) (proto.LauncherModels, error) {
	return n.launcherModels(ctx, launcher, "", false)
}

func (n *Node) runAssistantRecovery(p *assistantProc, r *assistantTurnRecovery, launcher, cwd string, execution modelExecution, current string, same bool) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if !same {
		list, err := assistantRecoveryModels(ctx, n, launcher)
		if err != nil {
			n.logger.Printf("assistant %s: list fallback models: %v", p.id[:8], err)
			n.finishAssistantRecovery(p, r)
			return
		}
		next, ok := nextAssistantModel(list, current, r.used)
		if !ok {
			n.finishAssistantRecovery(p, r)
			return
		}
		execution = assistantExecutionForModel(execution, list, next)
		r.used[next] = true
	}
	_, err := n.assistantSend(withAssistantRecovery(ctx, r, !same), p.id, cwd, launcher, prompts.RenderModelRetry(), nil, "", "", execution)
	var pending *assistantPendingFailure
	n.mu.Lock()
	if n.recoveries[p.id] == r {
		r.running = false
		pending, r.pending = r.pending, nil
	}
	n.mu.Unlock()
	if err == nil {
		if pending != nil {
			n.recoverAssistantResult(p, true, pending.text, pending.kinds...)
		}
		return
	}
	n.logger.Printf("assistant %s: model recovery did not start: %v", p.id[:8], err)
	n.finishAssistantRecovery(p, r)
}

// finishAssistantRecovery publishes the exhausted or undeliverable recovery's final idle state. Its transcript keeps
// the original upstream error; the scheduled-run watcher can fail only now, not between two recovery attempts.
func (n *Node) finishAssistantRecovery(p *assistantProc, r *assistantTurnRecovery) {
	n.mu.Lock()
	if n.recoveries[p.id] != r {
		n.mu.Unlock()
		return
	}
	r.running = false
	current := n.procs[p.id] == p
	delete(n.recoveries, p.id) // an exhausted chain leaves no per-conversation bookkeeping behind
	n.mu.Unlock()
	if current {
		p.mu.Lock()
		p.busy = false
		p.lastActive = time.Now()
		p.mu.Unlock()
	}
	n.emitDelta(p.id, "")
	n.notifySessions()
}
