package extmgr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/extension"
	"github.com/nwverse/persona/server/internal/extension/host"
	"github.com/nwverse/persona/server/internal/rpc"
)

// Agent rules (docs/extensions/extensions.md "Agent rules", #16820): when the assistant starts a new task Agent it asks
// every agent_rules contribution that is on for the rules that Agent should start with (agent_rules.for_start). The
// contributions are asked together, each call within agentRulesCallTimeout and all of them within agentRulesTotal, and
// every answer is re-checked before it is used: one over a limit or of the wrong shape counts as failed, never cut. A
// contribution that fails gives no rules this time and is logged; the start goes on without them.

// The bounds of asking for one start, and of an answer as agent_rules.schema.json states them.
const (
	agentRulesCallTimeout = 1500 * time.Millisecond
	agentRulesTotal       = 2 * time.Second
	agentRulesTextMost    = 4096 // bytes of UTF-8
	agentRulesRevMost     = 64   // characters
	// agentRulesAnswerMost is the most bytes one raw answer may hold: the text JSON-escaped at worst, and the rest.
	agentRulesAnswerMost = 6*agentRulesTextMost + 1024
)

// agentRulesAnswer is what one contribution answered for a start, or why nothing (err).
type agentRulesAnswer struct {
	extension, contribution string
	text, rev               string
	err                     error
}

// AgentRulesFor are the rules the agent_rules contributions that are on answered for start (its contribution is
// filled in per call), one text each, in the order of their extensions' ids, within agentRulesTotal. An empty answer,
// and one from a contribution whose program does not run, that fails or that has not answered in time, adds nothing;
// the failures are logged. Before startup, and with no contribution on, it asks nothing.
func (h *Manager) AgentRulesFor(ctx context.Context, start wire.AgentRulesForStartParams) []string {
	if h.extensions == nil || !h.startedNow() {
		return nil
	}
	var out []string
	for _, a := range h.extensions.askAgentRulesAll(ctx, start) {
		switch {
		case a.err != nil:
			h.extensions.logf("plugin %s: agent_rules.for_start: no rules for this start: %v", a.extension, a.err)
		case a.text != "":
			h.extensions.logf("plugin %s: agent rules %s rev %q (%d bytes) for %s %q on %s", a.extension, a.contribution, a.rev,
				len(a.text), start.Agent, start.Model, start.Machine)
			out = append(out, a.text)
		}
	}
	return out
}

// askAgentRulesAll asks every agent_rules contribution that is on (enabled, without a problem, switched on), at once.
func (ps *extensions) askAgentRulesAll(ctx context.Context, start wire.AgentRulesForStartParams) []agentRulesAnswer {
	var out []agentRulesAnswer
	for _, p := range ps.reg.List() {
		if !p.Record.Enabled || p.Problem != "" || p.Manifest == nil {
			continue
		}
		on := p.Manifest.Items("agent_rules")
		for _, it := range p.Manifest.AgentRules() {
			if slices.Contains(on, it.ID) {
				out = append(out, agentRulesAnswer{extension: p.Record.ID, contribution: it.ID})
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, agentRulesTotal)
	defer cancel()
	var wg sync.WaitGroup
	for i := range out {
		a := &out[i]
		hp := ps.get(a.extension)
		if hp == nil || hp.proc == nil {
			a.err = errors.New("its program does not run")
			continue
		}
		params := start
		params.Contribution = a.contribution
		wg.Add(1)
		go func(proc agentRulesCaller) {
			defer wg.Done()
			a.text, a.rev, a.err = askAgentRules(ctx, proc, params, agentRulesCallTimeout)
		}(hp.proc)
	}
	wg.Wait()
	return out
}

// agentRulesCaller is the program a contribution's call goes to (a *host.Proc; a test's).
type agentRulesCaller interface {
	Call(ctx context.Context, method string, params, out any) error
}

var _ agentRulesCaller = (*host.Proc)(nil)

// askAgentRules is one agent_rules.for_start call within timeout (and ctx), its answer checked (agentRulesAnswerOf).
// A failure gives no text and no rev.
func askAgentRules(ctx context.Context, proc agentRulesCaller, params wire.AgentRulesForStartParams, timeout time.Duration) (text, rev string, err error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var raw json.RawMessage
	if err := proc.Call(cctx, "agent_rules.for_start", params, &raw); err != nil {
		var re *rpc.Error
		switch {
		case ctx.Err() != nil:
			return "", "", fmt.Errorf("no answer within the start's bound: %w", ctx.Err())
		case cctx.Err() != nil:
			return "", "", fmt.Errorf("no answer in %s", timeout)
		case errors.Is(err, host.ErrNotRunning):
			return "", "", errors.New("its program does not run")
		case errors.As(err, &re) && (re.Code == extension.CodeUnknownMethod || re.Code == extension.CodeNoContribution):
			return "", "", errors.New("it does not answer agent_rules.for_start (an older version?)")
		case errors.As(err, &re):
			return "", "", fmt.Errorf("%s: %s", re.Code, repoWords(re.Message))
		}
		return "", "", errors.New(repoWords(err.Error()))
	}
	return agentRulesAnswerOf(raw)
}

// agentRulesShape is an answer's own fields, each told missing from wrong.
type agentRulesShape struct {
	Text *string `json:"text"`
	Rev  *string `json:"rev"`
}

// agentRulesAnswerOf is raw as an answer: refused whole when it is not an object with a text, the text is over 4096
// bytes (as decoded: JSON decoding makes it valid UTF-8), or the rev is over 64 characters. The text is trimmed of
// blank lines around it; one that is then empty adds nothing.
func agentRulesAnswerOf(raw json.RawMessage) (text, rev string, err error) {
	if len(raw) > agentRulesAnswerMost {
		return "", "", fmt.Errorf("an answer of %d bytes, over %d", len(raw), agentRulesAnswerMost)
	}
	var shape agentRulesShape
	if err := json.Unmarshal(raw, &shape); err != nil {
		return "", "", errors.New("an answer that is not an object")
	}
	switch {
	case shape.Text == nil:
		return "", "", errors.New("an answer without its text")
	case len(*shape.Text) > agentRulesTextMost:
		return "", "", fmt.Errorf("a text of %d bytes, over %d", len(*shape.Text), agentRulesTextMost)
	case shape.Rev != nil && utf8.RuneCountInString(*shape.Rev) > agentRulesRevMost:
		return "", "", fmt.Errorf("a rev over %d characters", agentRulesRevMost)
	}
	if shape.Rev != nil {
		rev = repoWords(*shape.Rev)
	}
	return strings.TrimSpace(*shape.Text), rev, nil
}
