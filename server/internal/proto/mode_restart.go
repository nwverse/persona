package proto

// ModeRestartEvent is a node's session.mode_restart event (#10297, docs/protocol/protocol.md「Session mode changes」):
// what became of a queued restart into a permission mode (node/mode_restart.go). The manager keeps it with the
// session's mode changes and passes it on to no one.
type ModeRestartEvent struct {
	SessionID string `json:"session_id"`
	Mode      string `json:"mode"`
	Outcome   string `json:"outcome"` // ModeRestartDone, ModeRestartFailed, ModeRestartDropped
	Reason    string `json:"reason,omitempty"`
}

// The outcomes of a ModeRestartEvent.
const (
	ModeRestartDone    = "done"    // the agent runs in the mode now
	ModeRestartFailed  = "failed"  // it was restarted and did not come up in the mode
	ModeRestartDropped = "dropped" // it never ran: another mode picked, its process gone, picked too long ago
)

// EventModeRestart is the node's event that carries a ModeRestartEvent.
const EventModeRestart = "session.mode_restart"
