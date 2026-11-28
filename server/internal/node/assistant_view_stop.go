package node

import (
	"context"
	"time"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/buswire"
)

func (n *Node) assistantViewStop(parent context.Context, req sockRequest, in hookInput) any {
	if req.ReportOnly || in.HookEventName != "Stop" || in.AgentID != "" || in.StopHookActive || n.peer == nil || !wire.HasAssistantView(in.LastAssistantMessage) || !n.isAssistantSession(in.SessionID) {
		return nil
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	input := assistantwire.ViewCheckRequest{SessionID: in.SessionID, Message: in.LastAssistantMessage}
	result, err := buswire.Call(ctx, n.peer, assistantwire.ViewCheckMethod, input)
	if err != nil || result.Reason == "" {
		return nil
	}
	return map[string]any{"decision": "block", "reason": result.Reason}
}
