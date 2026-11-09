package node

import (
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// restoreQuestionFromTranscript recovers an attempt whose Node was replaced before it could keep the PreToolUse
// pairing in memory. PermissionRequest still has no tool_use_id, but the transcript names the call and, when already
// answered in the terminal, its tool_result. Ambiguous calls fail closed rather than borrowing another question's id.
func (n *Node) restoreQuestionFromTranscript(in hookInput, attempt string, now time.Time) {
	if attempt == "" || n.questions.hasPair(in, attempt) {
		return
	}
	id, completed := n.questionFromTranscript(in)
	if id != "" {
		n.questions.restore(in, attempt, id, completed, now)
	}
}

// questionFromTranscript finds the latest matching call in the current turns. One outstanding call is unambiguous;
// with no outstanding call the latest completed one is the resumed request. Multiple outstanding calls, or an older
// outstanding call mixed with a newer completion, have no safe attribution and return no id.
func (n *Node) questionFromTranscript(in hookInput) (id string, completed bool) {
	f, err := n.file(in.SessionID)
	if err != nil || n.refresh(in.SessionID, f) != nil {
		return "", false
	}
	items, _, _ := f.PageTurns(0, 2, 2000)
	results := map[string]bool{}
	for _, it := range items {
		if it.Role == "user" && it.Kind == proto.KindToolResult && it.ToolUseID != "" {
			results[it.ToolUseID] = true
		}
	}
	key := permissionKey(in)
	var calls []proto.Item
	for _, it := range items {
		if it.Role != "assistant" || it.Kind != proto.KindToolUse || it.ToolName != "AskUserQuestion" || it.ToolUseID == "" {
			continue
		}
		candidate := in
		candidate.ToolInput = it.ToolInput
		if permissionKey(candidate) == key {
			calls = append(calls, it)
		}
	}
	if len(calls) == 0 {
		return "", false
	}
	outstanding := 0
	for _, call := range calls {
		if !results[call.ToolUseID] {
			outstanding++
		}
	}
	latest := calls[len(calls)-1]
	if results[latest.ToolUseID] {
		if outstanding != 0 {
			return "", false
		}
		return latest.ToolUseID, true
	}
	if outstanding != 1 {
		return "", false
	}
	return latest.ToolUseID, false
}
