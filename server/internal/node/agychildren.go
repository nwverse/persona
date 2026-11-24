package node

import (
	"strings"

	"github.com/nwverse/persona/server/internal/proto"
)

// linkAgyChildren points the Antigravity companion's subagent rows at the row that lists their parent conversation
// (#14817). The companion names a subagent's parent and root by its own ids, agy-<conversation>
// (integrations/antigravity/lib/runtime.js, from parent_conversation_id). While a live herdr pane runs that conversation,
// the list holds the pane's row instead and leaves the companion's out (sessionListIn), so such an id names the pane's
// row here: the manager then folds the subagent under it like any runtime child (docs/integrations/runtimes.md,
// "Sub-Agents and background tasks"). panes is the conversation each agy pane's row shows, by row id. A conversation
// two listed panes show names neither, and an id the list holds is left as it is.
func linkAgyChildren(list []proto.Session, panes map[string]string) {
	if len(panes) == 0 {
		return
	}
	listed := make(map[string]bool, len(list))
	for i := range list {
		listed[list[i].SessionID] = true
	}
	paneOf := make(map[string]string, len(panes)) // agy-<conversation> -> the listed pane row showing it ("" when two do)
	for id, conversation := range panes {
		if !listed[id] {
			continue
		}
		own := agyAgent + "-" + conversation
		if _, two := paneOf[own]; two {
			id = ""
		}
		paneOf[own] = id
	}
	if len(paneOf) == 0 {
		return
	}
	row := func(id string) string {
		if p := paneOf[id]; p != "" && !listed[id] {
			return p
		}
		return id
	}
	for i := range list {
		s := &list[i]
		if !strings.HasPrefix(s.SessionID, agyAgent+"-") {
			continue
		}
		s.ParentSessionID, s.RootSessionID = row(s.ParentSessionID), row(s.RootSessionID)
		if asking := s.AskingParentSessionID(); asking != "" {
			s.SetAskingParentSessionID(row(asking))
		}
	}
}
