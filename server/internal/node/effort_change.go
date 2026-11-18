package node

import (
	"context"
	"fmt"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// effortChange keeps a busy Claude Code's requested effort separate from its confirmed effort. The node refuses
// chat sends while it waits so Claude's own queue cannot start them at the old level. The app retains those sends.
type effortChange struct {
	target string
	pane   string
	failed bool
}

func (s *switchShare) effortChange(id string) effortChange {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.efforts[id]
}

func (s *switchShare) setEffortChange(id string, change effortChange) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if change.target == "" {
		delete(s.efforts, id)
		return
	}
	if s.efforts == nil {
		s.efforts = make(map[string]effortChange)
	}
	s.efforts[id] = change
}

func (c effortChange) refusal() error {
	return refusal(proto.CodeEffortChangePending, i18n.T(i18n.Default, "rpc-errors.effort_change_pending"),
		map[string]any{"effort": c.target})
}

// setTerminalEffort runs under the input locks also used by the queued worker.
func (n *Node) setTerminalEffort(ctx context.Context, id, pane, effort string, pending *bool) error {
	if a, live := n.agent(id); live && a.Agent == "claude" && pending != nil {
		h, _, err := n.paneHost(pane)
		if err != nil {
			return err
		}
		if err := n.refreshHosts(ctx, h); err != nil {
			return err
		}
		a, live = n.agent(id)
		if !live || a.Agent != "claude" || a.PaneID != pane {
			return n.noPaneForModes(id)
		}
		share := n.switches()
		if change := share.effortChange(id); change.target != "" {
			if _, current := n.modesOf(id); current == effort {
				share.setEffortChange(id, effortChange{})
				return nil
			}
		}
		if a.AgentStatus != proto.StatusIdle && a.AgentStatus != proto.StatusDone {
			share.setEffortChange(id, effortChange{target: effort, pane: pane})
			*pending = true
			return nil
		}
	} else if live && (a.AgentStatus == proto.StatusWorking || a.AgentStatus == proto.StatusBlocked) {
		return fmt.Errorf("Agent 正在工作，这一轮结束后再改思考强度")
	}

	if err := n.applyTerminalEffort(ctx, id, pane, effort); err != nil {
		return err
	}
	n.switches().setEffortChange(id, effortChange{})
	return nil
}

// Read pending before reading confirmed effort so a row never sees the barrier lifted with the old effort.
func (n *Node) terminalModesRow(s *proto.Session, m quickMeta) {
	change := n.switches().effortChange(s.SessionID)
	s.PendingEffort, s.EffortChangeFailed = change.target, change.failed
	s.PermissionMode, s.Effort = n.currentModes(s.SessionID, m)
}

func (n *Node) wakeEffortChange(s *proto.Session) {
	if s.PendingEffort != "" && !s.EffortChangeFailed && !s.Starting &&
		(s.Status == proto.StatusIdle || s.Status == proto.StatusDone || s.PaneID == "") {
		go n.tryEffortChange(s.SessionID)
	}
}

func (n *Node) tryEffortChange(id string) {
	unlock, ok := n.typing.tryLock(id)
	if !ok {
		return
	}
	defer unlock()
	share := n.switches()
	unlockEffort, ok := share.modesInput.tryLock(id)
	if !ok {
		return
	}
	defer unlockEffort()
	change := share.effortChange(id)
	if change.target == "" || change.failed || share.inFlight(id) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), switchProbeWait)
	defer cancel()
	h, _, err := n.paneHost(change.pane)
	if err == nil {
		if err = n.refreshHosts(ctx, h); err != nil {
			return // a failed refresh is not evidence that the turn ended
		}
		a, live := n.agent(id)
		switch {
		case !live || a.PaneID != change.pane || a.Agent != "claude":
			err = n.noPaneForModes(id)
		case a.AgentStatus != proto.StatusIdle && a.AgentStatus != proto.StatusDone:
			return
		case n.checkPaneFor(ctx, change.pane, "claude", sendOpts{readBox: true}) != nil:
			err = fmt.Errorf("Claude Code's prompt is not available")
		default:
			err = n.applyTerminalEffort(ctx, id, change.pane, change.target)
		}
	}
	if err != nil {
		n.logf("session %s: queued effort change failed: %v", id, err)
		change.failed = true
	} else {
		change = effortChange{}
	}
	share.setEffortChange(id, change)
	n.notifySessionsSoon()
}

// The terminal's mode controls can look blocked to herdr while the node drives them.
func (n *Node) driveModes(id string) func() {
	n.mu.Lock()
	n.driving[id] = true
	n.mu.Unlock()
	return func() {
		n.mu.Lock()
		delete(n.driving, id)
		n.mu.Unlock()
	}
}

func (n *Node) applyTerminalEffort(ctx context.Context, id, pane, effort string) error {
	defer n.driveModes(id)()
	if err := n.tuiSetEffort(ctx, pane, effort); err != nil {
		return err
	}
	n.noteModes(id, "", effort)
	return nil
}
