package assistant

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// Force archive is an explicit cancellation of a confirmation-blocked Agent, not a general busy override.
func confirmationArchiveSafe(row proto.Session) bool {
	if row.Assistant || row.Status != proto.StatusBlocked {
		return false
	}
	row.Status = proto.StatusIdle
	if row.State == proto.StateNeedsYou {
		row.State = proto.StateIdle
	}
	row.PendingInteractions = 0
	return !agentCloseBusy(row)
}

func (s *Service) forceArchiveConfirmation(ctx context.Context, mid, sid string) error {
	row, err := s.agentArchiveTarget(ctx, mid, sid)
	if err != nil {
		return err
	}
	raw, listed := s.nodeSession(row.MachineID, sid)
	if !confirmationArchiveSafe(row) || !listed || !confirmationArchiveSafe(raw) {
		return archiveRefusal(http.StatusConflict, "agent_busy", i18n.Err("hub-agent_archives.close_busy"))
	}
	for _, ix := range s.pendingInteractions() {
		if ix.MachineID == row.MachineID && ix.SessionID == sid &&
			ix.Kind != proto.IxPermission && ix.Kind != proto.IxPlan && ix.Kind != proto.IxScreen {
			return archiveRefusal(http.StatusConflict, "agent_busy", i18n.Err("hub-agent_archives.close_busy"))
		}
	}
	closeCtx, cancel := context.WithTimeout(ctx, agentArchiveCloseTimeout)
	defer cancel()
	// Closing kills the terminal or closes the provider runtime. Never press Enter or approve the pending request.
	// The node checks blocked again; a failed close leaves the Agent visible and its cards intact.
	if err := s.nodeCall(closeCtx, row.MachineID, "session.close", map[string]any{"session_id": sid, "force": true}, nil); err != nil {
		return err
	}
	no := false
	for _, ix := range s.pendingInteractions() {
		if ix.MachineID != row.MachineID || ix.SessionID != sid {
			continue
		}
		r := proto.Resolution{Dismiss: true}
		if ix.Kind == proto.IxPermission {
			r = proto.Resolution{Allow: &no}
		} else if ix.Kind == proto.IxPlan {
			r = proto.Resolution{Approve: &no}
		}
		// The runtime may already have withdrawn its cards during close. No error can turn this into an allow.
		if err := s.resolve(ix.ID, r); err != nil && !errors.Is(err, errNotPending) {
			s.logf("force archive %s: cancel confirmation %s after close: %v", short(sid), ix.ID, err)
		}
	}
	if err := s.noteAgentArchive(ctx, row, true); err != nil {
		return err
	}
	if err := s.noteAgentArchiveClosed(ctx, row, time.Now().UTC()); err != nil {
		return err
	}
	s.publishList(row.MachineID)
	return nil
}
