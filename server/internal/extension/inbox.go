package extension

import (
	"fmt"
	"slices"
)

// The inbox's common rules for a plugin (docs/extensions/extensions.md "General rules for the Inbox"): a connector with an inbox comes with the
// user's own triage rules (a top-level string setting triage_rules, sent as its things' rules), and either brings
// its own fallback tier (capability fallback) or needs the hub's triage model.

// TriageRulesField is the setting that holds a plugin's triage rules.
const TriageRulesField = "triage_rules"

// InboxConnectors are the plugin's connectors with an inbox, and of those the ones without a fallback of their own.
func InboxConnectors(m *Manifest) (inbox, noFallback []string) {
	if m == nil {
		return nil, nil
	}
	for _, c := range m.Connectors() {
		if !slices.Contains(c.Capabilities, "inbox") {
			continue
		}
		inbox = append(inbox, c.ID)
		if !slices.Contains(c.Capabilities, "fallback") {
			noFallback = append(noFallback, c.ID)
		}
	}
	return inbox, noFallback
}

// CheckTriageRules holds a package to the inbox's second rule: with an inbox connector, its settings declare
// triage_rules (a string at the top). Checked when a package is installed or upgraded, not on load: a plugin
// installed before keeps running.
func CheckTriageRules(m *Manifest) error {
	inbox, _ := InboxConnectors(m)
	if len(inbox) == 0 {
		return nil
	}
	s, err := ParseSchema(m.Configuration)
	if err == nil && s != nil && slices.ContainsFunc(s.Fields, func(f Field) bool { return f.Name == TriageRulesField && f.Type == "string" }) {
		return nil
	}
	return fmt.Errorf("%s 的连接器 %s 用收件箱，设置里却没有 %s（分拣规则，字符串）：用收件箱的扩展要带上用户自己的分拣规则，装不上",
		m.ID, inbox[0], TriageRulesField)
}
