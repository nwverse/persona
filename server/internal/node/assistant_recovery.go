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
	proc    *assistantProc  // working-status placeholder while a model switch has no registered process
	done    chan struct{}   // non-nil while runAssistantRecovery can still send; cleared after its final state update
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

// assistantModelServiceFailure is an internal second error-kind entry: Codex's structured error proved a 429/5xx even
// when its human-readable message contains no status. It is not published as the session's TurnErrorKind.
const assistantModelServiceFailure = "model-service"

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

// noteAssistantRecoveryProcess follows the current process through a model replacement. During keeper handover it
// distinguishes a current result from a replaced process's late result; the latter must not overwrite pending work.
func (n *Node) noteAssistantRecoveryProcess(ctx context.Context, p *assistantProc) {
	recovery, _ := assistantRecoveryOf(ctx)
	if recovery == nil || p == nil {
		return
	}
	n.mu.Lock()
	if n.recoveries[p.id] == recovery {
		recovery.proc = p
	}
	n.mu.Unlock()
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
	if n.recoveryClosed && n.procs[p.id] != p {
		owner := p.ownerOr(n)
		n.mu.Unlock()
		if owner != n { // the result handler read the old owner before adoption; finish on the current one
			owner.clearAssistantRecovery(p)
		}
		return
	}
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
	return n.recoverAssistantResultFor(p, nil, failed, result, kinds...)
}

// recoverAssistantResultFor optionally requires the recovery that captured a pending result to still own the session.
// That identity check prevents a result delayed by process replacement from restarting a turn the user has cancelled.
func (n *Node) recoverAssistantResultFor(p *assistantProc, expected *assistantTurnRecovery, failed bool, result string, kinds ...string) bool {
	if len(kinds) > 0 && kinds[0] == proto.TurnErrorUsageLimit {
		return false // a subscription reset has its own continuation; an immediate retry cannot spend it back
	}
	if !assistantRecoverableError(failed, result) && !(failed && slices.Contains(kinds, assistantModelServiceFailure)) {
		return false
	}
	p.mu.Lock()
	if p.manual || p.compacting {
		p.mu.Unlock()
		return false
	}
	launcher, cwd, execution := p.launcher, p.cwd, p.model
	replyModel := p.replyModel
	key := execution.Model // the launcher's default stays one budget even when its concrete name arrives on a later attempt
	current := key
	if current == "" {
		current = replyModel // used only to locate that default in the launcher's ordered list
	}
	p.mu.Unlock()

	n.mu.Lock()
	r := n.recoveries[p.id]
	if n.recoveryClosed {
		if r == nil {
			owner := p.ownerOr(n)
			n.mu.Unlock()
			if owner != n { // a result that read the old owner just before adoption continues on the new one
				return owner.recoverAssistantResultFor(p, expected, failed, result, kinds...)
			}
			return false
		}
		if (expected != nil && r != expected) || (r.proc != nil && r.proc != p) {
			n.mu.Unlock()
			return false
		}
		// No new goroutine runs on the ended connection. The keeper carries this failure and the remaining budget to
		// the next Node, even when the result arrives after the process map has already handed over.
		r.pending = &assistantPendingFailure{text: result, kinds: kinds}
		r.proc = p
		n.mu.Unlock()
		p.mu.Lock()
		p.busy = true
		p.mu.Unlock()
		return true
	}
	if n.procs[p.id] != p || (expected != nil && r != expected) {
		n.mu.Unlock()
		return false
	}
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
	r.proc = p
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
	if replyModel != "" {
		r.used[replyModel] = true // an alias and the concrete model it resolved to share one retry budget
	}
	r.running = true
	done := make(chan struct{})
	r.done = done
	n.mu.Unlock()

	p.mu.Lock()
	p.busy = true // before the goroutine can finish: the caller must never overwrite an already-published final idle
	p.mu.Unlock()
	go func() {
		defer func() {
			n.mu.Lock()
			if r.done == done {
				r.done = nil
			}
			n.mu.Unlock()
			close(done)
		}()
		n.runAssistantRecovery(p, r, launcher, cwd, execution, current, same)
	}()
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
	if current == "" || current == "default" {
		for _, m := range list.Models {
			if m.Default || m.ID == "default" {
				// A failed request may never emit message_start. Resolve the default to avoid selecting its own alias again.
				current = m.Resolved
				if current == "" && m.ID != "default" {
					current = m.ID
				}
				break
			}
		}
	}
	for i, m := range list.Models {
		if current != "" && (m.ID == current || m.Resolved == current) {
			at = i
			break
		}
	}
	for i := at + 1; i < len(list.Models); i++ {
		m := list.Models[i]
		if m.Default || m.ID == "" || m.ID == "default" || !proto.ValidModelID(m.ID) || m.ID == current ||
			(m.Resolved != "" && (m.Resolved == current || used[m.Resolved])) || used[m.ID] {
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

// assistantRecoverySend is the ordinary assistantSend recovery path, replaceable by a deterministic process handoff test.
var assistantRecoverySend func(context.Context, *Node, *assistantProc, string, string, modelExecution) error

func init() {
	assistantRecoverySend = sendAssistantRecovery
}

func sendAssistantRecovery(ctx context.Context, n *Node, p *assistantProc, launcher, cwd string, execution modelExecution) error {
	_, err := n.assistantSend(ctx, p.id, cwd, launcher, prompts.RenderModelRetry(), nil, "", "", execution)
	return err
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
	err := assistantRecoverySend(withAssistantRecovery(ctx, r, !same), n, p, launcher, cwd, execution)
	var pending *assistantPendingFailure
	var currentProc *assistantProc
	n.mu.Lock()
	if n.recoveries[p.id] == r {
		r.running = false
		pending, r.pending = r.pending, nil
		currentProc = n.procs[p.id]
	}
	n.mu.Unlock()
	if err == nil {
		if pending != nil {
			// A model switch replaces assistantProc. Its first result may already be pending when the send returns;
			// retry that current process, not the old process that started this recovery attempt.
			if currentProc != nil && n.recoverAssistantResultFor(currentProc, r, true, pending.text, pending.kinds...) {
				return
			}
			if currentProc == nil {
				currentProc = p
			}
			n.finishAssistantRecovery(currentProc, r)
		}
		return
	}
	n.logger.Printf("assistant %s: model recovery did not start: %v", p.id[:8], err)
	if currentProc == nil {
		currentProc = p
	}
	n.finishAssistantRecovery(currentProc, r)
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
