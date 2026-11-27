package extension

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/nwverse/persona/sdk/go/wire"
)

// Agent rules (docs/extensions/extensions.md "Agent rules", #16820): an agent_rules contribution answers
// agent_rules.for_start (schema/extension/agent_rules.schema.json), the rules a new task Agent should start with for its
// agent type, launcher, model, directory and computer. Here: the point, its items and the consent; which contributions
// are on and asking them is the manager's (extmgr/agent_rules.go), and so is putting the answers into the start.

// AgentRulesItem is a contribution to agent_rules: what the install page and the contributions list call the rules.
type AgentRulesItem = wire.AgentRulesItem

// AgentRulesNameMost is the longest agent_rules name (runes).
const AgentRulesNameMost = 80

func init() {
	Points["agent_rules"] = Point{Name: "agent_rules", Namespace: "agent_rules", Version: 1, NeedsRPC: true, Item: func(raw json.RawMessage) (string, error) {
		var it AgentRulesItem
		if err := strict(raw, &it); err != nil {
			return "", err
		}
		if !idPattern.MatchString(it.ID) {
			return it.ID, fmt.Errorf("agent_rules id %q: a-z0-9_-", it.ID)
		}
		if !shortText(it.Name, AgentRulesNameMost) {
			return it.ID, fmt.Errorf("agent_rules %s: a name of 1 to %d characters, not only spaces, no control characters", it.ID,
				AgentRulesNameMost)
		}
		return it.ID, nil
	}}
	catalogItemTypes["agent_rules"] = reflect.TypeFor[AgentRulesItem]()
}

// AgentRules are the manifest's contributions to agent_rules.
func (m *Manifest) AgentRules() []AgentRulesItem {
	if m == nil {
		return nil
	}
	var out []AgentRulesItem
	_ = json.Unmarshal(m.Contributes["agent_rules"], &out)
	return out
}

// asksAgentRules says whether m declares agent_rules: being asked about every new task Agent (its agent type, model,
// directory and computer) and adding to its instructions is agreed to like a permission, once for the extension.
func asksAgentRules(m *Manifest) bool { return len(m.AgentRules()) > 0 }

// addsAgentRules says whether m declares agent_rules and old (nil: none installed) did not: an upgrade that starts
// asking needs the user's consent again.
func addsAgentRules(old, m *Manifest) bool { return asksAgentRules(m) && !asksAgentRules(old) }
