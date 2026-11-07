package assistant

import (
	"context"
	"fmt"

	"github.com/nwverse/persona/server/internal/proto"
)

// Idle says whether the manager can stop now without cutting its own work off, and what holds it when not:
// manager.RunBuiltin asks before the built-in manager's service starts again on a new binary. Its outbox holds
// nothing back: what it keeps for the hub waits in its database and goes once it runs again.
type Idle func(ctx context.Context) (idle bool, why string)

// managerIdle is a running manager's Idle: no request of the hub's being answered (a tool call, an app's call), no
// hub event waiting to be handled, and its service idle.
func managerIdle(ctx context.Context, b *BusHub, s *Service) (bool, string) {
	if n := b.answering.Load(); n > 0 {
		return false, fmt.Sprintf("answering %d request(s) of the hub's (a tool call, an app's call)", n)
	}
	if n := b.pending.Load(); n > 0 {
		return false, fmt.Sprintf("%d event(s) of the hub's not handled yet", n)
	}
	return s.idle(ctx)
}

// idle: none of its own conversations on a turn (when their machine does not answer it cannot tell, so not idle),
// no scheduled task's message and no channel's answer going out.
func (s *Service) idle(ctx context.Context) (bool, string) {
	s.sched.mu.Lock()
	sending := len(s.sched.sending)
	s.sched.mu.Unlock()
	if sending > 0 {
		return false, "a scheduled task's message is going out"
	}
	s.chans.mu.Lock()
	answering := len(s.chans.answering)
	s.chans.mu.Unlock()
	if answering > 0 {
		return false, "a channel's answer is going out"
	}
	machine := s.settings(ctx).MachineID
	if machine == "" {
		return true, "" // no machine: none of its conversations can run
	}
	all, err := s.sessions(ctx, machine)
	if err != nil {
		return false, "its conversations' machine does not answer: " + err.Error()
	}
	for _, c := range all {
		if c.Assistant && (working(c) || (c.Compaction != nil && c.Compaction.State == proto.CompactionRunning)) {
			return false, "its conversation " + c.SessionID + " is on a turn"
		}
	}
	return true, ""
}
