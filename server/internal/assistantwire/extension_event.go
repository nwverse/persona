package assistantwire

import "encoding/json"

// Events an extension tells the assistant's main conversation (docs/extensions/extensions.md "Assistant events"). The
// extension's program calls MethodAssistantEvent on its process connection; the manager checks its assistant.events
// grant and its rate, and forwards it to the assistant as MethodExtensionEvent with the extension's identity, which
// the program cannot set (the call is not one an extension can make itself). The params are wire.AssistantEventParams.
const (
	// MethodAssistantEvent is what an extension's program calls.
	MethodAssistantEvent = "assistant.event"
	// MethodExtensionEvent is manager → assistant: ExtensionEvent → wire.AssistantEventResult.
	MethodExtensionEvent = "assistant.extension_event"
)

// ExtensionEvent is MethodExtensionEvent's params: the event as the extension sent it, and who sent it.
type ExtensionEvent struct {
	Extension string          `json:"extension"` // the extension's id: the manager's own word
	Name      string          `json:"name"`      // its name in its manifest, for the people and the assistant to read
	Params    json.RawMessage `json:"params"`    // wire.AssistantEventParams, not yet checked
}

// What assistant.event answers besides the codes every assistant call has (CodeAssistantOffline, CodeMachineOffline).
const (
	// CodeNoMainConversation: the user has no main conversation. Nothing was started, replaced or queued.
	CodeNoMainConversation = "no_main_conversation"
	// CodeRateLimited: too many events from the extension; data {retry_after} is the seconds to wait.
	CodeRateLimited = "rate_limited"
	// CodeQueueFull: the extension already has the most events waiting behind the main conversation's running turn.
	CodeQueueFull = "queue_full"
)

// AssistantEventParams is assistant.event's params (schema/extension/assistant.schema.json, which the conformance test
// holds this to). Lengths are in characters: key 128, title 200, body 4000, action_hint 500, state 64.
type AssistantEventParams struct {
	Key        string `json:"key"`
	Severity   string `json:"severity,omitempty"` // info (the default) | warning | critical
	Title      string `json:"title"`
	Body       string `json:"body,omitempty"`
	ActionHint string `json:"action_hint,omitempty"`
	State      string `json:"state,omitempty"`
	// Replace false keeps an earlier event with the same key as a message of its own; absent is true.
	Replace *bool `json:"replace,omitempty"`
}

// AssistantEventResult is assistant.event's result.
type AssistantEventResult struct {
	Delivery string `json:"delivery"` // EventDelivered | EventQueued | EventReplaced
}

// The severities of an event, and what became of it.
const (
	EventSeverityInfo     = "info"
	EventSeverityWarning  = "warning"
	EventSeverityCritical = "critical"

	EventDelivered = "delivered" // went into the idle main conversation now
	EventQueued    = "queued"    // waits behind the main conversation's running turn
	EventReplaced  = "replaced"  // took the place of a queued event with the same extension and key
)
