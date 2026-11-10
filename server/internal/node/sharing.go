package node

import (
	"context"
	"encoding/json"
	"time"

	"github.com/nwverse/persona/server/internal/devicewire"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/transcript"
)

// creationResult covers synchronous terminal, async registration, launcher
// runtimes and engine runtimes. Resume never establishes new ownership.
func (n *Node) creationResult(s *proto.Session, err error, resume, creation, incarnation string) (any, error) {
	out := map[string]any{"session": s}
	if err != nil || s == nil || resume != "" || creation == "" || incarnation == "" {
		return out, err
	}
	state, ok := n.state.get(s.SessionID)
	if ok && state.CreationID != "" && (state.CreationID != creation || state.Incarnation != incarnation) {
		return out, err
	}
	if !ok {
		state = sessionState{Launcher: s.Launcher, Cwd: s.Cwd, StartedAt: time.Now().UTC()}
	}
	state.CreationID, state.Incarnation = creation, incarnation
	n.state.put(s.SessionID, state)
	out["creation_id"], out["incarnation"] = creation, incarnation
	return out, nil
}

func (n *Node) shareProjection(ctx context.Context, params json.RawMessage) (devicewire.ShareProjection, error) {
	var p devicewire.ShareProjectionRequest
	if err := json.Unmarshal(params, &p); err != nil {
		return devicewire.ShareProjection{}, err
	}
	deny := func(reason string) (devicewire.ShareProjection, error) {
		return devicewire.ShareProjection{Reason: reason, Messages: []devicewire.ShareProjectionMessage{}}, nil
	}
	_, codex := n.codexSource(p.SessionID)
	runtimeTranscript := n.runtimes.transcriptOf(p.SessionID)
	if p.Incarnation == "" || (!validID(p.SessionID) && !codex && runtimeTranscript == "") {
		return deny("source_unsupported")
	}
	if state, ok := n.state.get(p.SessionID); ok && state.Incarnation != "" {
		if p.Inspect {
			p.Incarnation = state.Incarnation
		} else if state.Incarnation != p.Incarnation {
			return deny("source_changed")
		}
	}
	// Runtime-native Claude transcripts use the same complete schema-backed
	// source. Runtime Item streams alone have no complete checkpoint contract.
	path := n.transcriptPath(p.SessionID)
	if path == "" {
		return deny("source_incomplete")
	}
	return transcript.CaptureShareProjection(ctx, path, p.Incarnation, p.Previous)
}
