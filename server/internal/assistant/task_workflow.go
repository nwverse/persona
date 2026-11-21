package assistant

import (
	"github.com/nwverse/persona/server/internal/compat"
	"github.com/nwverse/persona/server/internal/i18n"
)

// taskWorkflowKind describes the launcher on a plan card, before the node proves an option can start.
type taskWorkflowKind string

const (
	workflowClaude      taskWorkflowKind = "claude_ultracode"
	workflowCodex       taskWorkflowKind = "codex_multi_agent_v2"
	workflowUnsupported taskWorkflowKind = "unsupported"
)

func (a startAgent) taskWorkflow() taskWorkflowKind {
	if a.snapshot == "" {
		return ""
	}
	switch compat.Source(a.source) {
	case "", "zshrc", "path", "persona", "builtin":
		switch a.snapshot {
		case "claude":
			return workflowClaude
		case "codex":
			return workflowCodex
		}
	}
	return workflowUnsupported
}

// changedTo keeps a confirmed workflow request tied to the kind shown before launch.
func (kind taskWorkflowKind) changedTo(fitted taskWorkflowKind) bool {
	return (kind == workflowClaude || kind == workflowCodex) && kind != fitted
}

func skippedTaskWorkflow(lang i18n.Lang, kind taskWorkflowKind, reason string) string {
	key := "assistant-tasks.ultracode_skipped"
	if kind == workflowCodex {
		key = "assistant-tasks.codex_multi_agent_skipped"
	}
	return i18n.T(lang, key, i18n.Args{"reason": reason})
}

func (t *PlanTask) skipWorkflow(note string) {
	off := false
	t.Ultracode = &off
	if t.callerPick != nil {
		t.callerPick.ultracode = copyBool(t.Ultracode)
	}
	t.WorkflowNote = note
	t.noteWhy(note)
}
