package node

import (
	"html"
	"strings"
	"time"
)

// The handoff summary of a conversation moving between Claude Code and Codex (#5054, engine_switch.go): what the
// other engine is given, in front of the user's message, to go on with the conversation. Built by code from the
// conversation's transcripts, bounded, the same for the same transcripts; no model is asked.

// handoffSpan is one stretch of the conversation a handoff covers: the items of session SessionID (on engine Agent,
// "claude" or "codex") after After and up to Until (zero: unbounded on that side).
type handoffSpan struct {
	SessionID string
	Agent     string
	After     time.Time
	Until     time.Time
}

// engineHandoff is the handoff text for a switch of the conversation from engine from to engine to ("claude" or
// "codex"), covering spans, oldest first. delta: the conversation goes back to a session of to that ran it before
// (resumed), which remembers what came before it left, so spans are only what happened meanwhile.
func (n *Node) engineHandoff(spans []handoffSpan, from, to string, delta bool) (string, error) {
	return "", nil // stub: filled in by the handoff builder
}

// engineSwitchKind is the kind of the <persona-context> block a handoff is carried in (handoffBlock).
const engineSwitchKind = "engine-switch"

// handoffBlock is the handoff text as the block put in front of the user's message: a <persona-context
// kind="engine-switch"> naming the engines (from, to: "claude" / "codex"), the model the new engine runs ("" when not
// known) and whether the conversation's earlier session of to was resumed. The apps draw the switch divider from it
// and show the text behind "View handoff context"; the model reads the text.
func handoffBlock(text, from, to, model string, resumed bool) string {
	var b strings.Builder
	b.WriteString(`<persona-context kind="` + engineSwitchKind + `" title="Handoff" from="` + html.EscapeString(from) + `" to="` + html.EscapeString(to) + `"`)
	if model != "" {
		b.WriteString(` model="` + html.EscapeString(model) + `"`)
	}
	if resumed {
		b.WriteString(` resumed="1"`)
	}
	b.WriteString(">\n")
	b.WriteString(strings.TrimSpace(text))
	b.WriteString("\n</persona-context>\n\n")
	return b.String()
}
