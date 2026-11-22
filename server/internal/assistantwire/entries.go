package assistantwire

import "github.com/nwverse/persona/server/internal/entries"

const MethodAssistantEntriesList = "assistant.entries.list"
const EventAssistantEntriesChanged = "assistant.entries.changed"

type AssistantEntriesParams = entries.Params
type AssistantEntriesResult = entries.Result

// Entry changes are invalidation hints, not entry contents. RecordID names the
// appended correction; ID names the entry that was marked wrong.
type AssistantEntriesChanged struct {
	ID       string `json:"id"`
	RecordID string `json:"record_id"`
	Status   string `json:"status"`
	// Personal is internal routing metadata, omitted from plugin notifications.
	Personal bool `json:"personal,omitempty"`
}

func init() {
	Routes = append(Routes, "GET /api/v1/assistant/entries", "POST /api/v1/assistant/entries/{id}/wrong")
}
