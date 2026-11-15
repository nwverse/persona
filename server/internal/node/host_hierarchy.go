package node

import (
	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/proto"
)

// resolveHostHierarchy resolves host pane edges only after public session identities are known. A missing or
// ambiguous parent stays unlinked: a title, working directory or provider thread ID is never a substitute.
func resolveHostHierarchy(list []proto.Session, agents map[string]host.Agent) {
	type pane struct{ machine, manager, id string }
	byPane := map[pane]string{}
	for _, s := range list {
		if s.PaneID == "" {
			continue
		}
		k := pane{s.MachineID, s.Manager, s.PaneID}
		if _, exists := byPane[k]; exists {
			byPane[k] = "" // two sessions claim this pane; neither is a safe parent
		} else {
			byPane[k] = s.SessionID
		}
	}
	for i := range list {
		s := &list[i]
		a, ok := agents[s.SessionID]
		if !ok || a.PaneID != s.PaneID || a.ParentPaneID == "" ||
			host.ParsePane(a.PaneID).Kind != host.ParsePane(a.ParentPaneID).Kind {
			continue
		}
		if id := byPane[pane{s.MachineID, s.Manager, a.ParentPaneID}]; id != "" && id != s.SessionID {
			s.SetParentAgentID(id)
		}
	}
}
