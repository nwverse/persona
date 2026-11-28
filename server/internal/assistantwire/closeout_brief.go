package assistantwire

import "github.com/nwverse/persona/server/internal/buswire"

// MethodCloseoutBrief: a node asks, as an agent's session starts, for the closeout's form to hand it as starting
// context, so that a turn that does work ends with one without a held stop (docs/assistant/agent-work.md "Closeout").
// The manager answers: it holds the switch.
const MethodCloseoutBrief = "agents.closeout.brief"

// CloseoutBriefMethod binds the request for a starting session's closeout form to its reply.
var CloseoutBriefMethod = buswire.Method[CloseoutBriefRequest, CloseoutBriefResponse]{Name: MethodCloseoutBrief}

type CloseoutBriefRequest struct {
	SessionID string `json:"session_id"`
	Cwd       string `json:"cwd,omitempty"`
}

// CloseoutBriefResponse: Context is what the session is told as it starts; empty tells it nothing.
type CloseoutBriefResponse struct {
	Context string `json:"context,omitempty"`
}
