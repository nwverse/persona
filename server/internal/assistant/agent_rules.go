package assistant

import (
	"context"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/extmgr"
)

// Agent rules (docs/extensions/extensions.md "Agent rules", #16820): a new task Agent starts with the rules each
// agent_rules extension keeps for its agent type, launcher, model, directory and computer, as framework rules beside
// Persona's own (task_instructions: Claude Code's instruction file, Codex's developer instructions, else the first
// message for an older node). Only a new start asks (startTaskAgent; a retry is a new start); a resumed Agent keeps the
// instructions it started with. The extensions' manager asks them together within its bound (extmgr/agent_rules.go),
// so a start never waits longer for them, and one that fails or does not answer in time adds nothing.

// agentRuleSources is the extensions' manager as a task start asks it for rules.
type agentRuleSources interface {
	AgentRulesFor(ctx context.Context, start wire.AgentRulesForStartParams) []string
}

var _ agentRuleSources = (*extmgr.Manager)(nil)

// startAgentRules are the texts the agent_rules extensions answered for start, each one block; none without a source.
func (s *Service) startAgentRules(ctx context.Context, start wire.AgentRulesForStartParams) []string {
	if s.agentRules == nil {
		return nil
	}
	return s.agentRules.AgentRulesFor(ctx, start)
}
