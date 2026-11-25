package fleetcount

import (
	"strings"

	"github.com/nwverse/persona/server/internal/domainkit/fail"
	"github.com/nwverse/persona/server/internal/domainkit/outcome"
)

// AgentID is an agent id as fleetcount knows it. The zero AgentID is no agent id; ParseAgentID never makes one.
type AgentID struct{ agent string }

// ParseAgentID is the agent id raw names; empty text is invalid.
func ParseAgentID(raw string) (out outcome.Of[AgentID, Fail]) {
	if parts := agentIDRule(nil, raw); len(parts) > 0 {
		return out.Err(invalidParts(parts))
	}
	return out.Ok(AgentID{agent: raw})
}

// agentIDRule adds what is wrong with raw as an agent id to parts.
func agentIDRule(parts []fail.Invalid, raw string) []fail.Invalid {
	return wrong(parts, strings.TrimSpace(raw) != "", "fleetcount.agent.empty", "agent")
}

// Wire is the agent id as the wire carries it. Adapters only.
func (a AgentID) Wire() string { return a.agent }
