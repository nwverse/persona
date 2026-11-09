package extension

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/lazyre"
)

// Agent filters (docs/extensions/extensions.md "Agent filters", #2428): an extension says which agent sessions on the computers are
// not the user's to see in Agents (another platform's runs, hundreds a week), by markers the session files carry. The
// hub hands the ones that hold to every node (node.configure agent_filters), which leaves matching sessions out.

// AgentFilterItem is a contribution to agent_filters: a session of one of Agents (none: both) goes when any matcher
// fits it.
type AgentFilterItem struct {
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
	// When: a boolean field of the extension's configuration; the filter holds only while it is true, and that field
	// is its only switch (no contribution switch of its own). Empty: it holds while the extension is on.
	When   string   `json:"when,omitempty"`
	Agents []string `json:"agents,omitempty"`
	// Originators: Codex rollouts whose session_meta.originator is one of these.
	Originators []string `json:"originators,omitempty"`
	// AddedDirs: Claude Code sessions given one of these directories (or one under it) besides their own
	// (additionalWorkingDirectories); "~/" is the home directory of the node.
	AddedDirs []string `json:"added_dirs,omitempty"`
	// FirstMessagePrefixes: the first user message starts with one of these; a fallback for sessions without a marker.
	FirstMessagePrefixes []string `json:"first_message_prefixes,omitempty"`
}

// AgentFilterAgents are the agents a filter can name.
var AgentFilterAgents = []string{"claude", "codex"}

var configField = lazyre.New(`^[A-Za-z_][A-Za-z0-9_]*$`)

// agentFilterMost is how many values each matcher may list.
const agentFilterMost = 16

func init() {
	Points["agent_filters"] = Point{Name: "agent_filters", Namespace: "agent_filters", Version: 1, Item: func(raw json.RawMessage) (string, error) {
		var f AgentFilterItem
		if err := strict(raw, &f); err != nil {
			return "", err
		}
		if !idPattern.MatchString(f.ID) {
			return f.ID, fmt.Errorf("agent filter id %q: a-z0-9_-", f.ID)
		}
		if utf8.RuneCountInString(f.Title) > 40 {
			return f.ID, fmt.Errorf("agent filter %s: title: at most 40 characters", f.ID)
		}
		if f.When != "" && !configField.MatchString(f.When) {
			return f.ID, fmt.Errorf("agent filter %s: when %q: a configuration field's name", f.ID, f.When)
		}
		for _, a := range f.Agents {
			if !slices.Contains(AgentFilterAgents, a) {
				return f.ID, fmt.Errorf("agent filter %s: agent %q: only %s", f.ID, a, strings.Join(AgentFilterAgents, ", "))
			}
		}
		if len(f.Originators)+len(f.AddedDirs)+len(f.FirstMessagePrefixes) == 0 {
			return f.ID, fmt.Errorf("agent filter %s: needs originators, added_dirs or first_message_prefixes", f.ID)
		}
		for name, list := range map[string][]string{"originators": f.Originators, "added_dirs": f.AddedDirs, "first_message_prefixes": f.FirstMessagePrefixes} {
			if len(list) > agentFilterMost {
				return f.ID, fmt.Errorf("agent filter %s: %s: at most %d", f.ID, name, agentFilterMost)
			}
			for _, v := range list {
				if strings.TrimSpace(v) == "" {
					return f.ID, fmt.Errorf("agent filter %s: %s: an empty value", f.ID, name)
				}
			}
		}
		for _, d := range f.AddedDirs {
			// An absolute directory or one under the home directory, deep enough not to take in everything.
			if !strings.HasPrefix(d, "/") && !strings.HasPrefix(d, "~/") || strings.Count(strings.TrimRight(d, "/"), "/") < 2 {
				return f.ID, fmt.Errorf("agent filter %s: added_dirs %q: an absolute path or ~/…, at least two levels deep", f.ID, d)
			}
		}
		for _, p := range f.FirstMessagePrefixes {
			// A prefix short enough to start ordinary prompts would hide the user's own sessions.
			if n := utf8.RuneCountInString(strings.TrimSpace(p)); n < 8 || n > 200 {
				return f.ID, fmt.Errorf("agent filter %s: first_message_prefixes %q: 8 to 200 characters", f.ID, p)
			}
		}
		return f.ID, nil
	}}
}

// AgentFilters are the manifest's contributions to agent_filters.
func (m *Manifest) AgentFilters() []AgentFilterItem {
	var out []AgentFilterItem
	_ = json.Unmarshal(m.Contributes["agent_filters"], &out)
	return out
}

// Holds says whether the filter takes effect under the extension's effective configuration (Registry.Config): always
// without When, else while that field is true. A field that is missing or not a boolean holds nothing back.
func (f AgentFilterItem) Holds(config json.RawMessage) bool {
	if f.When == "" {
		return true
	}
	var c map[string]any
	if json.Unmarshal(config, &c) != nil {
		return false
	}
	on, _ := c[f.When].(bool)
	return on
}

// switchedByConfig: an agent filter with When goes with that configuration field, not a contribution switch.
func (m *Manifest) switchedByConfig(point, id string) bool {
	if point != "agent_filters" {
		return false
	}
	for _, f := range m.AgentFilters() {
		if f.ID == id {
			return f.When != ""
		}
	}
	return false
}

// agentFilterSwitches checks that each agent filter's When is a boolean field of the configuration.
func (m *Manifest) agentFilterSwitches() []string {
	var errs []string
	var fields []Field
	if sc, err := ParseSchema(m.Configuration); err == nil && sc != nil {
		fields = sc.Fields
	}
	for _, f := range m.AgentFilters() {
		if f.When == "" {
			continue
		}
		i := slices.IndexFunc(fields, func(c Field) bool { return c.Name == f.When })
		if i < 0 || fields[i].Type != "boolean" {
			errs = append(errs, fmt.Sprintf("contributes.agent_filters: %s: when %q is not a boolean field of configuration", f.ID, f.When))
		}
	}
	return errs
}
