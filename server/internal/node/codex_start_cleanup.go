package node

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/nwverse/persona/server/internal/herdr"
	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// A start already gave its prompt to Codex as an argument. Leaving its dialog open would let it run that prompt
// later without a task tracking it. Close that start's pane before answering, including after cancellation. Unlike
// closeNewTab, failure is retained and reported: a retry must finish this cleanup before creating another pane.
type codexAbandonedStart struct {
	launch    host.Launcher
	created   *host.Created
	workspace string
}

// closeAbandonedCodex is called with the directory/launcher's typing lock held.
func (n *Node) closeAbandonedCodex(ctx context.Context, key string) error {
	n.mu.Lock()
	start, ok := n.codexAbandoned[key]
	n.mu.Unlock()
	if !ok {
		return nil
	}
	org, ok := start.launch.(host.Organizer)
	var err error
	if !ok {
		err = host.ErrUnsupported
	} else {
		space := start.workspace
		if space == "" {
			space = start.created.SpaceID
		}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		err = org.Close(cleanup, start.created.Pane, space, start.created.SlotID)
		var he *herdr.Error
		if errors.As(err, &he) {
			switch he.Code {
			case "pane_not_found":
				err = nil
			case "tab_not_found", "workspace_not_found":
				// A pane may have moved out of the missing tab. Only a fresh layout proving it absent clears it.
				if abandonedCodexPaneGone(cleanup, start) {
					err = nil
				}
			}
		}
		cancel()
	}
	if err != nil {
		return &rpc.Error{Code: proto.CodeAgentStartStalled,
			Message: i18n.T(i18n.Default, "node-codex-start.cleanup_failed", map[string]any{"pane": start.created.Pane.String(), "reason": err.Error()})}
	}
	n.mu.Lock()
	delete(n.codexAbandoned, key)
	n.mu.Unlock()
	return nil
}

func abandonedCodexPaneGone(ctx context.Context, start codexAbandonedStart) bool {
	h, ok := start.launch.(*herdrHost)
	if !ok {
		return false
	}
	target, err := h.target(ctx, start.created.Pane.ID)
	if err != nil {
		return false
	}
	// Host.Snapshot can substitute a cached named-session layout; only query this pane's actual server.
	layout, err := layoutOf(ctx, target.c, target.session)
	if err != nil || layout == nil {
		return false
	}
	matches := func(a host.Agent) bool { return a.PaneID == start.created.Pane.String() }
	return !slices.ContainsFunc(layout.Panes, matches) && !slices.ContainsFunc(layout.Agents, matches)
}
