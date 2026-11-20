package assistant

import (
	"context"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// A cursor is process-local: rebuilding it after a manager restart invalidates old
// watches. Take it before listing tools so a concurrent link cannot be missed.
type mcpToolChanges struct {
	mu      sync.Mutex
	cursor  string
	changed chan struct{}
}

func (c *mcpToolChanges) snapshot() (string, <-chan struct{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.changed == nil {
		c.cursor, c.changed = randomHex(16), make(chan struct{})
	}
	return c.cursor, c.changed
}

func (c *mcpToolChanges) notify() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.changed != nil {
		close(c.changed)
	}
	c.cursor, c.changed = randomHex(16), make(chan struct{})
}

// mcpToolsHeartbeat is a discovery heartbeat, not a legacy startup wait. Revision-aware calls never use leaderListWait.
const mcpToolsHeartbeat = 30 * time.Second

type revisionDiscoveryKey struct{}

func (s *Service) sessionMCPToolsSince(ctx context.Context, machine, session string, assistant, guest bool, since string) map[string]any {
	if since != "" {
		ctx = context.WithValue(ctx, revisionDiscoveryKey{}, true)
	}
	for heartbeat := false; ; heartbeat = true {
		cursor, changed := s.mcpChanges.snapshot()
		// Ordinary Agents have no per-conversation dispatch guard. A matching cursor can wait
		// without resolving their roles, just as it did before the guard was introduced.
		if assistant || cursor != since {
			caller, err := s.toolDiscoveryCaller(ctx, machine, session, assistant, guest)
			if err != nil {
				return map[string]any{"error": err.Error()}
			}
			revision := caller.denied.revision(cursor)
			if revision != since {
				tools, err := s.toolsForDiscoveryCaller(ctx, machine, session, caller, guest)
				if err != nil {
					return map[string]any{"error": err.Error()}
				}
				if tools == nil {
					tools = []proto.Tool{}
				}
				return map[string]any{"tools": tools, "revision": revision}
			}
		}
		if heartbeat {
			return map[string]any{"revision": since, "unchanged": true}
		}
		timer := time.NewTimer(mcpToolsHeartbeat)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return map[string]any{"revision": since, "unchanged": true}
		case <-timer.C:
		case <-changed:
		}
	}
}
