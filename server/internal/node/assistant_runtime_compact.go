package node

import (
	"context"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

type runtimeCompaction struct {
	runtime   *runtimeConn
	state     *proto.Compaction
	revision  int64
	turnError string
	sawWork   bool
	settled   chan string
}

func (n *Node) assistantCompactRuntime(ctx context.Context, id string, onlyRunning bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	host, label, _ := n.runtimes.belongs(id)
	if host == nil && !onlyRunning {
		return errRuntimeAway(label)
	}
	compaction, err := n.runtimes.beginCompaction(ctx, host, id, onlyRunning)
	if err != nil {
		return err
	}
	n.publishSessionsSoon()
	callCtx, cancel := context.WithTimeout(ctx, runtimeCallTimeout)
	defer cancel()
	params := map[string]any{"session_id": id, "text": "/compact", "assistant": true}
	if err := host.peer.Call(callCtx, "session.send", params, nil); err != nil {
		n.finishRuntimeCompaction(id, compaction, err.Error())
		return err
	}
	n.contextCompacted(id)
	go n.awaitRuntimeCompaction(id, compaction, i18n.From(ctx))
	return nil
}

func (b *runtimeRegistry) beginCompaction(ctx context.Context, host *runtimeConn, id string, onlyRunning bool) (*runtimeCompaction, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if current := b.compactions[id]; current != nil && current.state.State == proto.CompactionRunning {
		return nil, refusal("compacting", i18n.T(i18n.From(ctx), "assistant-start.compacting"), nil)
	}
	var row proto.Session
	if host != nil {
		row, _ = host.row(id)
	}
	if row.Starting || row.Status == proto.StatusWorking || row.Status == proto.StatusBlocked {
		return nil, refusal("busy", "Wait for the current turn to finish before compacting.", nil)
	}
	if onlyRunning {
		return nil, refusal("not_running", "The runtime cannot confirm this conversation is already running.", nil)
	}
	compaction := &runtimeCompaction{
		runtime: host, revision: row.Rev, turnError: row.TurnError, settled: make(chan string, 1),
		state: &proto.Compaction{State: proto.CompactionRunning, StartedAt: time.Now().UTC()},
	}
	if b.compactions == nil {
		b.compactions = make(map[string]*runtimeCompaction)
	}
	b.compactions[id] = compaction
	return compaction, nil
}

func (c *runtimeCompaction) observe(row proto.Session) {
	if c.state.State != proto.CompactionRunning {
		return
	}
	switch row.Status {
	case proto.StatusWorking, proto.StatusBlocked:
		c.sawWork = true
	case proto.StatusIdle, proto.StatusDone:
		if c.sawWork || row.Rev != c.revision || row.TurnError != c.turnError {
			select {
			case c.settled <- row.TurnError:
			default:
			}
		}
	}
}

func (n *Node) awaitRuntimeCompaction(id string, compaction *runtimeCompaction, lang i18n.Lang) {
	timer := time.NewTimer(assistantCompactTimeout)
	defer timer.Stop()
	var failure string
	select {
	case failure = <-compaction.settled:
	case <-compaction.runtime.peer.Done():
		failure = i18n.T(lang, "rpc-errors.unreachable")
	case <-timer.C:
		failure = i18n.T(lang, "rpc-errors.timeout")
	}
	n.finishRuntimeCompaction(id, compaction, failure)
}

func (n *Node) finishRuntimeCompaction(id string, compaction *runtimeCompaction, failure string) {
	b := n.runtimes
	b.mu.Lock()
	finished := *compaction.state
	now := time.Now().UTC()
	finished.FinishedAt = &now
	finished.State = proto.CompactionDone
	if failure != "" {
		finished.State, finished.Error = proto.CompactionFailed, failure
	}
	compaction.state = &finished
	compaction.runtime = nil
	if _, known := b.known[id]; !known {
		delete(b.compactions, id)
	}
	b.mu.Unlock()
	n.publishSessionsSoon()
}

func (n *Node) assistantHandoffRuntime(ctx context.Context, id string) (Handoff, error) {
	ctx, cancel := context.WithTimeout(ctx, runtimeCallTimeout)
	defer cancel()
	host, err := n.runtimeFor(ctx, "session.items", id)
	if err != nil {
		return Handoff{}, err
	}
	if host == nil {
		return Handoff{}, refusal("gone", "The runtime conversation history is unavailable.", nil)
	}
	var page struct {
		Items []proto.Item `json:"items"`
	}
	params := map[string]any{"session_id": id, "limit": handoffPageLimit}
	if err := host.peer.Call(ctx, "session.items", params, &page); err != nil {
		return Handoff{}, err
	}
	return Handoff{Recent: recentTurns(page.Items, 0)}, nil
}
