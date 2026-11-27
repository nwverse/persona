package extension

import (
	"slices"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/i18n"
)

// agentRulesContrib makes items the manifest's only contributions.
func agentRulesContrib(items ...map[string]any) func(map[string]any) {
	return func(m map[string]any) { m["contributes"] = map[string]any{"agent_rules": items} }
}

// agent_rules is registered (#16820): a program that speaks the protocol, namespace agent_rules, items with an id and a
// name of 1 to 80 characters. Being asked about every new task Agent and adding to its instructions is agreed to like a
// permission: it is in the digest, the install page says so, and an upgrade that starts declaring it asks again (one
// that only adds a second contribution does not).
func TestAgentRulesPoint(t *testing.T) {
	p := Points["agent_rules"]
	if p.Name != "agent_rules" || p.Namespace != "agent_rules" || p.Version != 1 || !p.NeedsRPC || p.NeedsHTTP || catalogItemTypes["agent_rules"] == nil {
		t.Fatalf("point %+v", p)
	}
	m, err := Parse(with(t, agentRulesContrib(map[string]any{"id": "rules", "name": "Validated rules"})))
	if err != nil {
		t.Fatal(err)
	}
	if got := m.AgentRules(); len(got) != 1 || got[0].ID != "rules" || got[0].Name != "Validated rules" {
		t.Fatalf("%+v", got)
	}
	if items := m.Items("agent_rules"); !slices.Equal(items, []string{"rules"}) {
		t.Fatal(items)
	}
	var lines []string
	for _, l := range ExplainIn(i18n.En, m) {
		lines = append(lines, l.Key+": "+l.Title)
	}
	if !slices.Contains(lines, "agent_rules: Add rules to new task Agents") {
		t.Fatalf("the install page: %v", lines)
	}

	plain, err := Parse(with(t, func(map[string]any) {}))
	if err != nil {
		t.Fatal(err)
	}
	withRules, err := Parse(with(t, func(raw map[string]any) {
		raw["contributes"].(map[string]any)["agent_rules"] = []any{map[string]any{"id": "rules", "name": "Validated rules"}}
	}))
	if err != nil {
		t.Fatal(err)
	}
	twoRules, err := Parse(with(t, func(raw map[string]any) {
		raw["contributes"].(map[string]any)["agent_rules"] = []any{map[string]any{"id": "rules", "name": "Validated rules"},
			map[string]any{"id": "more", "name": "More rules"}}
	}))
	if err != nil {
		t.Fatal(err)
	}
	if Digest(plain) == Digest(withRules) || Digest(withRules) != Digest(twoRules) {
		t.Error("the digest holds whether it declares agent_rules, not how many")
	}
	installed := &Extension{Full: plain, Record: Record{Granted: plain.Permissions}}
	if !MorePermissions(installed, withRules) {
		t.Error("an upgrade that starts declaring agent_rules asks again")
	}
	if MorePermissions(&Extension{Full: withRules, Record: Record{Granted: withRules.Permissions}}, twoRules) {
		t.Error("a second contribution asks for nothing more")
	}
	added := AddedIn(i18n.En, plain, withRules)
	if !slices.ContainsFunc(added, func(l PermissionLine) bool { return l.Key == "agent_rules" }) {
		t.Errorf("what the upgrade adds: %+v", added)
	}
	if slices.ContainsFunc(AddedIn(i18n.En, withRules, twoRules), func(l PermissionLine) bool { return l.Key == "agent_rules" }) {
		t.Error("a second contribution adds no line")
	}
	for _, l := range []i18n.Lang{i18n.En, i18n.ZhHans, i18n.Ja} {
		for _, line := range ExplainIn(l, withRules) {
			if line.Key == "agent_rules" && (line.Title == "" || line.Detail == "" || strings.Contains(line.Title, "agent_rules_")) {
				t.Errorf("%s: %+v", l, line)
			}
		}
	}

	for name, items := range map[string][]map[string]any{
		"no name":    {{"id": "rules"}},
		"blank name": {{"id": "rules", "name": "  "}},
		"long name":  {{"id": "rules", "name": strings.Repeat("a", AgentRulesNameMost+1)}},
		"bad id":     {{"id": "Rules", "name": "Validated rules"}},
		"extra":      {{"id": "rules", "name": "Validated rules", "models": []string{"example-model"}}},
	} {
		if _, err := Parse(with(t, agentRulesContrib(items...))); err == nil {
			t.Errorf("%s: parsed", name)
		}
	}
	if _, err := Parse(with(t, func(raw map[string]any) {
		delete(raw, "runtime")
		raw["contributes"] = map[string]any{"agent_rules": []any{map[string]any{"id": "rules", "name": "Validated rules"}}}
	})); err == nil {
		t.Error("agent_rules needs a program that speaks the protocol")
	}
}
