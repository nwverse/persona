package assistant

import (
	"context"
	"fmt"
	"time"
)

// Ten minutes covers reading a result, running a check and sending a follow-up.
// Live app subscriptions protect an open conversation for as long as it stays open.
const taskUserIdle = 10 * time.Minute

func (s *Service) taskCloseActivityError(ctx context.Context, t Todo, now time.Time) error {
	if t.DoneAt != nil {
		return nil
	}
	for _, a := range t.Agents {
		if a.ReleasedAt != nil || a.DismissedAt != nil || a.SessionID == "" {
			continue
		}
		if why := s.sessionActivityReason(ctx, a.MachineID, a.SessionID, now); why != "" {
			return fmt.Errorf("task %s cannot be closed: %s. Leave the task open and retry after the user leaves the "+
				"conversation and has been inactive for 10 minutes; do not stop or archive its Agent", t.ID, why)
		}
	}
	return nil
}

func (s *Service) sessionActivityReason(ctx context.Context, machine, session string, now time.Time) string {
	s.loadSeen(ctx)
	persistent := s.fleetStore() != nil
	s.fleet.mu.Lock()
	defer s.fleet.mu.Unlock()
	for _, ref := range s.fleet.clients.Viewing {
		if ref.MachineID == machine && ref.SessionID == session {
			return fmt.Sprintf("the user is viewing Agent %s", short(session))
		}
	}
	if persistent && !s.fleet.seenLoaded {
		return "the user's recent session activity could not be loaded"
	}
	if at := s.fleet.seen[[2]string{machine, session}]; !at.IsZero() && now.Sub(at) < taskUserIdle {
		return fmt.Sprintf("the user viewed or messaged Agent %s within the last 10 minutes", short(session))
	}
	return ""
}
