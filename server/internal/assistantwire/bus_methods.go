package assistantwire

import (
	"github.com/nwverse/persona/server/internal/buswire"
	"github.com/nwverse/persona/server/internal/proto"
)

// ToolsResult is the tool catalog returned by assistant.tools.
type ToolsResult struct {
	Tools []proto.Tool `json:"tools"`
}

var (
	SnapshotMethod = buswire.Method[Snapshot, SnapshotResult]{Name: MethodSnapshot}
	ToolsMethod    = buswire.Method[SessionRef, ToolsResult]{Name: MethodTools}
	ToolMethod     = buswire.Method[ToolCall, proto.ToolResult]{Name: MethodTool}
	ContextMethod  = buswire.Method[ContextParams, ContextResult]{Name: MethodContext}
	ActionMethod   = buswire.Method[Action, ActionResult]{Name: MethodAction}
)
