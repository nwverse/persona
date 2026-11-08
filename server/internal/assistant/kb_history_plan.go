package assistant

import (
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"time"

	"github.com/nwverse/persona/server/internal/connectorwire"
	"github.com/nwverse/persona/server/internal/kb"
)

// A source's plan (history/plan-<source>.json): which of its chats to import and how. Each chat's rule comes from
// the defaults by its kind (direct messages in full, groups by anchors, muted, official, bot and system chats not at
// all); its mode is the owner's own choice (null: the rule). Regenerating the list from the source updates names,
// kinds and estimates and keeps every mode; a chat not in the list is never imported, and a new one gets the
// defaults. Nothing runs until the owner approves the plan (kb_history_resume records approved_at).

// History modes a plan names: kb.HistoryFull, kb.HistoryAnchor and off.
const historyOff = "off"

// historyUnlisted is the kind of a chat the plan did not list but the source's search found the owner's messages in
// (often one the account left): imported by its anchors, like a group, unless the owner turns it off.
const historyUnlisted = "unlisted"

// historyDefaults are a new plan's defaults, by what a chat is.
var historyDefaults = map[string]string{
	connectorwire.HistoryKindDM: kb.HistoryFull, connectorwire.HistoryKindGroup: kb.HistoryAnchor, "muted": historyOff,
	connectorwire.HistoryKindOfficial: historyOff, connectorwire.HistoryKindBot: historyOff, connectorwire.HistoryKindSystem: historyOff,
	historyUnlisted: kb.HistoryAnchor,
}

type historyPlan struct {
	Source      string            `json:"source"`
	GeneratedAt time.Time         `json:"generated_at"`
	ApprovedAt  *time.Time        `json:"approved_at,omitempty"`
	Defaults    map[string]string `json:"defaults"`
	Chats       []historyPlanChat `json:"chats"`
}

type historyPlanChat struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Muted    bool   `json:"muted"`
	Bot      bool   `json:"bot,omitempty"`
	Estimate int    `json:"estimate"`
	// FiledAs is the source the chat is filed under when it is not the connector's id (HistoryChat.Source).
	FiledAs string  `json:"filed_as,omitempty"`
	Rule    string  `json:"rule"`
	Mode    *string `json:"mode"`
}

// effective is the chat's mode: the owner's, else the rule.
func (c historyPlanChat) effective() string {
	if c.Mode != nil && validHistoryMode(*c.Mode) {
		return *c.Mode
	}
	return c.Rule
}

// filing is the source the chat's knowledge-base chat is under.
func (c historyPlanChat) filing(connector string) string {
	if c.FiledAs != "" {
		return c.FiledAs
	}
	return connector
}

func validHistoryMode(m string) bool {
	return m == kb.HistoryFull || m == kb.HistoryAnchor || m == historyOff
}

func historyPlanPath(data, source string) string {
	return filepath.Join(historyDir(data), "plan-"+kb.SafeID(source)+".json")
}

// loadHistoryPlan reads a source's plan; false when there is none.
func loadHistoryPlan(data, source string) (*historyPlan, bool, error) {
	var p historyPlan
	ok, err := readJSONFile(historyPlanPath(data, source), &p)
	if err != nil || !ok {
		return nil, ok, err
	}
	if p.Defaults == nil {
		p.Defaults = map[string]string{}
	}
	return &p, true, nil
}

func saveHistoryPlan(data string, p *historyPlan) error {
	return writeJSONFile(historyPlanPath(data, p.Source), p)
}

// rule is the mode the defaults give a chat.
func (p *historyPlan) rule(c historyPlanChat) string {
	key := c.Kind
	switch {
	case c.Kind == connectorwire.HistoryKindOfficial || c.Kind == connectorwire.HistoryKindSystem || c.Kind == connectorwire.HistoryKindBot:
	case c.Bot:
		key = connectorwire.HistoryKindBot
	case c.Muted:
		key = "muted"
	case c.Kind != connectorwire.HistoryKindDM && c.Kind != historyUnlisted:
		key = connectorwire.HistoryKindGroup // a kind the source made up: as a group
	}
	if m, ok := p.Defaults[key]; ok && validHistoryMode(m) {
		return m
	}
	return historyDefaults[key]
}

// regenerate lays the source's list over the plan: names, kinds, mutes and estimates from the list, rules computed
// again, modes kept; chats new to the plan get the defaults, and chats the list no longer has stay as they were.
// It says how many chats were new and will be imported (effective mode not off).
func (p *historyPlan) regenerate(chats []connectorwire.HistoryChat, now time.Time) (added int) {
	for k, v := range historyDefaults {
		if _, ok := p.Defaults[k]; !ok {
			p.Defaults[k] = v
		}
	}
	at := map[string]int{}
	for i, c := range p.Chats {
		at[c.ID] = i
	}
	for _, hc := range chats {
		if hc.ID == "" {
			continue
		}
		c := historyPlanChat{ID: hc.ID, Name: hc.Name, Kind: hc.Kind, Muted: hc.Muted, Bot: hc.Bot, Estimate: max(hc.Estimate, 0), FiledAs: hc.Source}
		if i, ok := at[hc.ID]; ok {
			c.Mode = p.Chats[i].Mode
			c.Rule = p.rule(c)
			p.Chats[i] = c
			continue
		}
		c.Rule = p.rule(c)
		at[c.ID] = len(p.Chats)
		p.Chats = append(p.Chats, c)
		if c.effective() != historyOff {
			added++
		}
	}
	for i := range p.Chats { // the defaults may have been edited by hand
		p.Chats[i].Rule = p.rule(p.Chats[i])
	}
	p.GeneratedAt = now.UTC()
	return added
}

// addUnlisted adds a chat the plan does not list (a search found the owner's messages in it), with the defaults.
func (p *historyPlan) addUnlisted(id, filedAs string) bool {
	if id == "" || p.chat(id) != nil {
		return false
	}
	c := historyPlanChat{ID: id, Kind: historyUnlisted, FiledAs: filedAs}
	c.Rule = p.rule(c)
	p.Chats = append(p.Chats, c)
	return true
}

func (p *historyPlan) chat(id string) *historyPlanChat {
	for i := range p.Chats {
		if p.Chats[i].ID == id {
			return &p.Chats[i]
		}
	}
	return nil
}

// set changes the owner's mode of a chat ("" or "null": back to the rule).
func (p *historyPlan) set(id, mode string) error {
	c := p.chat(id)
	if c == nil {
		return fmt.Errorf("no chat %q in the plan", id)
	}
	if mode == "" || mode == "null" {
		c.Mode = nil
		return nil
	}
	if !validHistoryMode(mode) {
		return fmt.Errorf("mode %q: full, anchor, off or null", mode)
	}
	c.Mode = &mode
	return nil
}

// grouped are the plan's chats by effective mode, the largest estimate first.
func (p *historyPlan) grouped() map[string][]historyPlanChat {
	out := map[string][]historyPlanChat{}
	for _, c := range p.Chats {
		out[c.effective()] = append(out[c.effective()], c)
	}
	for _, list := range out {
		sort.SliceStable(list, func(i, j int) bool {
			if list[i].Estimate != list[j].Estimate {
				return list[i].Estimate > list[j].Estimate
			}
			return list[i].Name < list[j].Name
		})
	}
	return out
}

// importing are the chats whose effective mode is not off.
func (p *historyPlan) importing() []historyPlanChat {
	return slices.DeleteFunc(slices.Clone(p.Chats), func(c historyPlanChat) bool { return c.effective() == historyOff })
}

// wantsUnlisted: chats the source's search finds that the plan does not list would be imported (their default is
// not off), so the anchors are worth gathering even without an anchor chat in the plan.
func (p *historyPlan) wantsUnlisted() bool {
	return p.rule(historyPlanChat{Kind: historyUnlisted}) != historyOff
}
