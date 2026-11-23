package node

import (
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/nodeclient"
	"github.com/nwverse/persona/server/internal/proto"
)

type agyTurnEvidence struct {
	inputDigest string
	inputAt     time.Time
	inputSeq    int
	lastSeq     int
	replyAt     time.Time
	finalSeq    int
	lastText    string
}

// summarizeAgyTurn uses the same explicit inputs and visible assistant text as the pane history.
func summarizeAgyTurn(steps []agyStep) agyTurnEvidence {
	var input, reply, final *agyStep
	var out agyTurnEvidence
	for i := range steps {
		step := &steps[i]
		out.lastSeq = step.seq
		switch step.Type {
		case "USER_INPUT":
			// A system wake or a new input invalidates earlier terminal turn evidence.
			input, reply, final = nil, nil, nil
			if step.Source == "" || step.Source == "USER_EXPLICIT" {
				input = step
			}
		case "PLANNER_RESPONSE":
			final = nil
			if strings.TrimSpace(step.Content) != "" {
				reply = step
				if step.Status == "DONE" && len(step.ToolCalls) == 0 {
					final = step
				}
			}
		case "ERROR_MESSAGE", "GENERIC", "SYSTEM_MESSAGE", "CHECKPOINT":
			final = nil
		}
	}
	if reply != nil {
		out.lastText = snippet(agyAssistantText(reply.Content))
	}
	if input != nil && input.Index != nil {
		out.inputAt, _ = time.Parse(time.RFC3339Nano, input.CreatedAt)
		out.inputDigest = nodeclient.AgyPromptDigest(agyUserText(input.Content))
		out.inputSeq = input.seq
	}
	if final != nil && final.Index != nil && out.lastText != "" {
		out.replyAt, _ = time.Parse(time.RFC3339Nano, final.CreatedAt)
		out.finalSeq = final.seq
	}
	return out
}

// applyAgyTurnEvidence annotates the engine state for readers. The engine still supplies the control state.
func (n *Node) applyAgyTurnEvidence(s *proto.Session, conversation string, turn agyTurnEvidence) {
	if conversation == "" || n.agyConversation(s.SessionID, s.PaneID) != conversation {
		return
	}
	s.LastText = turn.lastText
	n.mu.Lock()
	report := n.agyReports[s.PaneID]
	n.mu.Unlock()
	if report.conversation != conversation || turn.inputDigest == "" || report.promptDigest != turn.inputDigest ||
		turn.inputSeq == 0 || turn.inputAt.IsZero() || turn.inputAt.After(report.at) ||
		report.invocationSteps < turn.inputSeq || report.invocationSteps > turn.lastSeq {
		return
	}
	switch s.Status {
	case proto.StatusWorking:
		s.NativeTurnVerified = report.event == "PreInvocation" && turn.finalSeq <= report.invocationSteps
	case proto.StatusDone:
		s.NativeTurnVerified = report.event == "Stop" && report.settled && turn.finalSeq > report.invocationSteps &&
			!turn.replyAt.IsZero() && !turn.replyAt.After(report.at)
	}
}
