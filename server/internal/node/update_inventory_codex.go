package node

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/nwverse/persona/server/internal/proto"
)

// updateCodexObservation is raw, freshly read evidence. It includes ephemeral
// background threads and subagents that a conversation display intentionally omits.
// It does not assert completeness for the machine or any other app-server.
type updateCodexObservation struct {
	Instance   string              `json:"instance"`
	Generation string              `json:"generation"`
	Revision   string              `json:"revision"`
	Threads    []updateCodexThread `json:"threads"`
}

type updateCodexThread struct {
	ID                  string `json:"id"`
	Status              string `json:"status"`
	PendingInteractions int    `json:"pending_interactions"`
	Ephemeral           bool   `json:"ephemeral"`
	ParentThreadID      string `json:"parent_thread_id"`
}

// observeUpdateThreads reads without resuming/joining a thread, canceling asks or
// replacing the normal display cache. An unread loaded thread is unknown, never
// an idle copy from a previous refresh. The caller owns the total read deadline.
func (c *codexRemote) observeUpdateThreads(ctx context.Context) (updateCodexObservation, error) {
	out := updateCodexObservation{Instance: c.spec.ID, Generation: c.generation, Threads: []updateCodexThread{}}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	c.mu.Lock()
	ready := c.ready
	c.mu.Unlock()
	if !ready || c.spec.ID == "" || c.generation == "" {
		return out, fmt.Errorf("app-server source identity is not ready")
	}
	ids, err := c.loaded(ctx)
	if err != nil {
		return out, fmt.Errorf("read complete loaded thread inventory: %w", err)
	}
	slices.Sort(ids)
	for i, id := range ids {
		if !validID(id) || i > 0 && id == ids[i-1] {
			return out, fmt.Errorf("invalid or duplicate loaded thread identity")
		}
		var response struct {
			Thread remoteThreadData `json:"thread"`
		}
		if err := c.wire.call(ctx, "thread/read", map[string]any{"threadId": id, "includeTurns": true}, &response); err != nil {
			return out, fmt.Errorf("loaded thread activity unavailable: %w", err)
		}
		d := response.Thread
		if d.ID != id {
			return out, fmt.Errorf("loaded thread read identity conflicts")
		}
		status := proto.StatusUnknown
		switch d.Status.Type {
		case "idle":
			status = proto.StatusIdle
		case "active":
			status = proto.StatusWorking
		}
		for _, turn := range d.Turns {
			if turn.Status == "inProgress" {
				status = proto.StatusWorking
			}
		}
		// Unknown active flags cannot be interpreted as idleness.
		if len(d.Status.ActiveFlags) != 0 {
			status = proto.StatusBlocked
		}
		// Keep activity and identity only. Conversation previews, prompts, titles
		// and transcript paths do not belong in an update receipt.
		out.Threads = append(out.Threads, updateCodexThread{ID: id, Status: status,
			Ephemeral: d.Ephemeral, ParentThreadID: d.ParentThreadID})
	}
	finalIDs, err := c.loaded(ctx)
	if err != nil {
		return out, fmt.Errorf("recheck loaded thread inventory: %w", err)
	}
	slices.Sort(finalIDs)
	if !slices.Equal(ids, finalIDs) {
		return out, fmt.Errorf("loaded thread inventory changed while being read")
	}
	c.mu.Lock()
	ready = c.ready
	for i := range out.Threads {
		for _, ask := range c.asks {
			if ask.thread == out.Threads[i].ID {
				out.Threads[i].PendingInteractions++
			}
		}
	}
	c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if !ready {
		return out, fmt.Errorf("app-server disconnected during activity read")
	}
	b, err := json.Marshal(out)
	if err != nil {
		return out, err
	}
	digest := sha256.Sum256(b)
	out.Revision = hex.EncodeToString(digest[:])
	return out, nil
}
