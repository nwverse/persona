package extmgr

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/nwverse/persona/server/internal/extension"
	"github.com/nwverse/persona/server/internal/proto"
)

// The extensions' agent filters (docs/extensions/extensions.md "Agent filtering", #2428): the ones that hold go to every node with
// node.configure agent_filters, in the hub's part of a new connection (devices.connected, machineConnected) and again
// whenever they change (an extension turned on
// or off, its settings saved, installed, upgraded, removed); the node leaves matching sessions out of its list.

// agentFilters are the filters that hold now: of the extensions that are on, each filter whose when field is true.
// Never nil: an empty list tells the node to drop what it had.
func (h *Manager) agentFilters() []proto.AgentFilter {
	out := []proto.AgentFilter{}
	if h.extensions == nil || h.extensions.reg == nil {
		return out
	}
	h.extensions.declared(func(p *extension.Extension) {
		config := h.extensions.reg.Config(p)
		for _, f := range p.Manifest.AgentFilters() {
			if f.Holds(config) {
				out = append(out, proto.AgentFilter{Extension: p.Record.ID, ID: f.ID, Agents: f.Agents, Originators: f.Originators,
					AddedDirs: f.AddedDirs, FirstMessagePrefixes: f.FirstMessagePrefixes})
			}
		}
	})
	slices.SortFunc(out, func(a, b proto.AgentFilter) int {
		if a.Extension != b.Extension {
			if a.Extension < b.Extension {
				return -1
			}
			return 1
		}
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	return out
}

// agentFilterHolds says whether p's filter id takes effect under its settings (the contributions list's switch).
func (ps *extensions) agentFilterHolds(p *extension.Extension, id string) bool {
	for _, f := range p.Full.AgentFilters() {
		if f.ID == id {
			return f.Holds(ps.reg.Config(p))
		}
	}
	return false
}

// agentFiltersChanged hands the filters to every online node when they differ from what was sent last, and takes each
// node's session list again so the apps see the change at once. A node that is offline gets them when it connects.
func (h *Manager) agentFiltersChanged(ctx context.Context) {
	ps := h.extensions
	if ps == nil {
		return
	}
	filters := h.agentFilters()
	b, _ := json.Marshal(filters)
	ps.mu.Lock()
	same := ps.filtersSent != nil && string(ps.filtersSent) == string(b)
	ps.filtersSent = b
	ps.mu.Unlock()
	if same {
		return
	}
	for _, m := range h.machinesNow() {
		if !m.isOnline() {
			continue
		}
		go func(m *Machine) {
			ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			if err := m.call(ctx, "node.configure", map[string]any{"agent_filters": filters}, nil); err != nil {
				h.logf("[%s] configure agent filters: %v", m.cfg.ID, err)
				return
			}
			// The node lists them again with the filters: the manager takes the list (it is its own) and the apps
			// get it from it.
			_ = m.call(ctx, "sessions.list", map[string]any{}, nil)
		}(m)
	}
}
