package assistantwire

// MethodCloseoutBrief: a node asks, as an agent's session starts, for the closeout's form to hand it as starting
// context, so that a turn that does work ends with one without a held stop (docs/assistant/agent-work.md "Closeout").
// The manager answers: it holds the switch.
const MethodCloseoutBrief = "agents.closeout.brief"

type CloseoutBriefRequest struct {
	SessionID string `json:"session_id"`
	Cwd       string `json:"cwd,omitempty"`
}

// CloseoutBriefResponse: Context is what the session is told as it starts; empty tells it nothing.
type CloseoutBriefResponse struct {
	Context string `json:"context,omitempty"`
}
