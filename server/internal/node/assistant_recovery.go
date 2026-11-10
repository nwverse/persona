package node

import (
	"context"
	"regexp"
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
}

type assistantRecoveryContextKey struct{}

func withAssistantRecovery(ctx context.Context) context.Context {
	return context.WithValue(ctx, assistantRecoveryContextKey{}, true)
}

func isAssistantRecovery(ctx context.Context) bool {
	on, _ := ctx.Value(assistantRecoveryContextKey{}).(bool)
	return on
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
	if assistantBadRequest.MatchString(t) {
		return false // a hub/request 400 is not transient, even when its detail mentions an upstream status
	}
	return assistantRateLimit.MatchString(t) || strings.Contains(t, "rate limit") || strings.Contains(t, "rate_limit") ||
		strings.Contains(t, "too many requests") || assistantServerError.MatchString(t)
}

func (n *Node) clearAssistantRecovery(id string) {
	if id == "" {
		return
	}
	n.mu.Lock()
	delete(n.recoveries, id)
	n.mu.Unlock()
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
	current := execution.Model
	if current == "" {
		current = p.replyModel
	}
	p.mu.Unlock()

	n.mu.Lock()
	if n.procs[p.id] != p {
		n.mu.Unlock()
		return false
	}
	r := n.recoveries[p.id]
	if r == nil {
		r = &assistantTurnRecovery{retried: map[string]bool{}, used: map[string]bool{current: true}}
		if n.recoveries == nil {
			n.recoveries = map[string]*assistantTurnRecovery{}
		}
		n.recoveries[p.id] = r
	}
	if r.running {
		n.mu.Unlock()
		return true
	}
	same := !r.retried[current]
	if same {
		r.retried[current] = true
	}
	r.used[current], r.running = true, true
	n.mu.Unlock()

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
		if m.Default || m.ID == "" || m.ID == "default" || !proto.ValidModelID(m.ID) || used[m.ID] || used[m.Resolved] {
			continue
		}
		return m.ID, true
	}
	return "", false
}

// assistantRecoveryModels is launchers.models, replaced by deterministic tests without a CLI or model call.
var assistantRecoveryModels = func(ctx context.Context, n *Node, launcher string) (proto.LauncherModels, error) {
	return n.launcherModels(ctx, launcher, "", false)
}

func (n *Node) runAssistantRecovery(p *assistantProc, r *assistantTurnRecovery, launcher, cwd string, execution modelExecution, current string, same bool) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	valid := func() bool {
		n.mu.Lock()
		defer n.mu.Unlock()
		return n.recoveries[p.id] == r
	}
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
		execution.Model = next
		r.used[next] = true
	}
	if !valid() {
		return // the user's newer message owns the conversation now
	}
	if !same {
		p.mu.Lock()
		tasks := len(p.tasks)
		p.mu.Unlock()
		if tasks != 0 {
			// Switching models resumes in another process. Do not kill background work to manufacture a fallback.
			n.finishAssistantRecovery(p, r)
			return
		}
		n.stopAssistant(p.id)
	}
	_, err := n.assistantSend(withAssistantRecovery(ctx), p.id, cwd, launcher, prompts.RenderModelRetry(), nil, "", "", execution)
	n.mu.Lock()
	if n.recoveries[p.id] == r {
		r.running = false
	}
	n.mu.Unlock()
	if err == nil {
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
