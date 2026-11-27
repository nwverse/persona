package assistantwire

import "github.com/nwverse/persona/server/internal/buswire"

const MethodViewCheck = "assistant.view.check"

// ViewCheckMethod binds a node's assistant view check to the hub's reply.
var ViewCheckMethod = buswire.Method[ViewCheckRequest, ViewCheckResponse]{Name: MethodViewCheck}

type ViewCheckRequest struct {
	SessionID string `json:"session_id"`
	Message   string `json:"message"`
}

type ViewCheckResponse struct {
	Reason string `json:"reason,omitempty"`
}
