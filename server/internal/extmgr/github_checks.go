package extmgr

import (
	"context"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/connector/github"
)

func githubChecksPublicHost(c *github.Connector) bool {
	hosts := c.Manifest().Hosts
	return len(hosts) == 1 && strings.EqualFold(hosts[0], "github.com")
}

// PRChecks uses the active built-in GitHub connector only when it is enabled and
// signed in. Extensions replacing it do not expose this structured capability.
func (h *Manager) PRChecks(ctx context.Context, repo string, number int) (github.PRCheckSnapshot, time.Time, bool, error) {
	if !h.ready(ctx) || h.connectors == nil || !h.connectors.Enabled("github") {
		return github.PRCheckSnapshot{}, time.Time{}, false, nil
	}
	c := h.githubConnector()
	if c == nil {
		return github.PRCheckSnapshot{}, time.Time{}, false, nil
	}
	// The watched URL is on github.com; a connector signed into another host
	// can have the same owner/repository and PR number with unrelated checks.
	if !githubChecksPublicHost(c) {
		return github.PRCheckSnapshot{}, time.Time{}, false, nil
	}
	status := c.Status(ctx)
	if status.State == connector.StateRateLimited {
		return github.PRCheckSnapshot{}, c.PRChecksRateReset(), true, nil
	}
	if status.State != connector.StateOK {
		return github.PRCheckSnapshot{}, time.Time{}, false, nil
	}
	snapshot, err := c.PRChecks(ctx, repo, number)
	return snapshot, c.PRChecksRateReset(), true, err
}
