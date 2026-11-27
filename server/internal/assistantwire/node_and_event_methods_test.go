package assistantwire_test

import (
	"testing"

	"github.com/nwverse/persona/server/internal/assistantwire"
)

func TestNodeAndEventMethodsWire(t *testing.T) {
	checkMethodWire[assistantwire.CloseoutBriefRequest, assistantwire.CloseoutBriefResponse](t, assistantwire.CloseoutBriefMethod,
		"agents.closeout.brief", `{"session_id":"session-1","cwd":"/code/example"}`, `{"context":"Report completed work."}`)
	checkMethodWire[assistantwire.CloseoutCheckRequest, assistantwire.CloseoutCheckResponse](t, assistantwire.CloseoutCheckMethod,
		"agents.closeout.check", `{"session_id":"session-1"}`, `{"reason":"Report completed work."}`)
	checkMethodWire[assistantwire.ViewCheckRequest, assistantwire.ViewCheckResponse](t, assistantwire.ViewCheckMethod,
		"assistant.view.check", `{"session_id":"session-1","message":"First line\nSecond line"}`, `{"reason":"Add a fallback."}`)
	checkMethodWire[assistantwire.ExtensionEvent, assistantwire.AssistantEventResult](t, assistantwire.ExtensionEventMethod,
		"assistant.extension_event",
		`{"extension":"example-monitor","name":"Example Monitor","params":{"key":"disk","title":"Disk almost full","replace":false}}`,
		`{"delivery":"queued"}`)
}

func TestNodeAndEventMethodsOptionalFieldsWire(t *testing.T) {
	checkJSONRoundTrip[assistantwire.CloseoutBriefRequest](t, `{"session_id":"session-1"}`)
	checkJSONRoundTrip[assistantwire.CloseoutBriefResponse](t, `{}`)
	checkJSONRoundTrip[assistantwire.CloseoutCheckResponse](t, `{}`)
	checkJSONRoundTrip[assistantwire.ViewCheckResponse](t, `{}`)
	checkJSONRoundTrip[assistantwire.ExtensionEvent](t, `{"extension":"example-monitor","name":"Example Monitor","params":null}`)
}
