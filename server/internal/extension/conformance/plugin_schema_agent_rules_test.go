package conformance_test

import "github.com/nwverse/persona/sdk/go/wire"

// The agent_rules call (agent_rules.schema.json, #16820): the manager asks with the generated params and reads the
// answer's fields itself before using them (extmgr/agent_rules.go), so the result type is the generated one too. Kept
// in its own file so goTypes' literal is not edited for it.
func init() {
	goTypes["AgentRulesForStartParams"] = wire.AgentRulesForStartParams{}
	goTypes["AgentRulesForStartResult"] = wire.AgentRulesForStartResult{}
}
