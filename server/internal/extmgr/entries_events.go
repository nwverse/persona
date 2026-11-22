package extmgr

import (
	"encoding/json"
	"github.com/nwverse/persona/server/internal/assistantwire"
)

func init() {
	onAssistantEvent(assistantwire.EventAssistantEntriesChanged, func(h *Manager, raw json.RawMessage) {
		var p assistantwire.AssistantEntriesChanged
		if json.Unmarshal(raw, &p) != nil || p.ID == "" || p.RecordID == "" || p.Status != "wrong" {
			return
		}
		if h.extensions.todoWanted(assistantwire.EventAssistantEntriesChanged) {
			h.extensions.queueEvent(assistantwire.EventAssistantEntriesChanged, fieldsOf(p))
		}
	})
}
