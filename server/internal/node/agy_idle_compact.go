package node

import (
	"context"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/proto"
)

// agyIdleRetryCooldown avoids hammering a session when an automatic compaction attempt fails.
const agyIdleRetryCooldown = 5 * time.Minute

func (n *Node) agyNow() time.Time {
	n.agyIdleMu.Lock()
	clock := n.agyIdleClock
	n.agyIdleMu.Unlock()
	if clock != nil {
		return clock()
	}
	return time.Now()
}

type agyIdleState struct {
	safeIdleSince time.Time
	lastRowTime   time.Time
	lastTokens    int
	attempted     bool
	lastAttempt   time.Time
}

type agyRuntimeSnapshot struct {
	host *runtimeConn
	row  proto.Session
}

func (n *Node) setAgyIdleCompaction(p assistantwire.AgyIdleCompaction) {
	norm := p.Normalize()
	n.agyIdleMu.Lock()
	n.agyIdlePolicy = &norm
	n.agyIdleMu.Unlock()
}

func (n *Node) agyIdleCompactionPolicy() assistantwire.AgyIdleCompaction {
	n.agyIdleMu.Lock()
	defer n.agyIdleMu.Unlock()
	if n.agyIdlePolicy == nil {
		return assistantwire.DefaultAgyIdleCompaction()
	}
	return n.agyIdlePolicy.Normalize()
}

func (n *Node) setPendingInteractionSessions(ids []string) {
	next := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id != "" {
			next[id] = true
		}
	}
	n.agyIdleMu.Lock()
	n.pendingIxSessions = next
	n.agyIdleMu.Unlock()
}

func (n *Node) hasPendingInteraction(id string) bool {
	if id == "" {
		return false
	}
	n.agyIdleMu.Lock()
	defer n.agyIdleMu.Unlock()
	return n.pendingIxSessions[id]
}

func (n *Node) isKeptSession(id string) bool {
	if id == "" {
		return false
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.kept[id]
}

// isManagedAgySession reports whether id is a Persona-managed Antigravity session (either an assistant/direction
// conversation or a Persona-started task Agent).
func (n *Node) isManagedAgySession(id string) bool {
	if id == "" {
		return false
	}
	if n.isAssistantSession(id) {
		return true
	}
	if n.state != nil {
		if _, ok := n.state.get(id); ok {
			return true
		}
	}
	if n.runtimes != nil && n.runtimes.wasStarted(id) {
		return true
	}
	return n.isKeptSession(id)
}

func agySessionRowSafeIdle(row proto.Session) bool {
	if row.SessionID == "" || row.Archived || row.IsSubagent || row.Starting || row.StartError != "" {
		return false
	}
	if row.Status != proto.StatusIdle && row.Status != proto.StatusDone {
		return false
	}
	if row.State == proto.StateWorking || row.State == proto.StateNeedsYou {
		return false
	}
	if row.PendingInteractions > 0 || len(row.Queued) > 0 {
		return false
	}
	if row.BackgroundWorking || row.BackgroundWaiting || len(row.Background) > 0 || row.WokeByTask || row.Loop != nil {
		return false
	}
	for _, sub := range row.Subagents {
		if sub.Running {
			return false
		}
	}
	for _, wf := range row.Workflows {
		if wf.Running {
			return false
		}
	}
	return true
}

func (n *Node) agySessionSafeIdle(h *runtimeConn, row proto.Session) bool {
	if h == nil || h.peer == nil || h.agent() != agyAgent {
		return false
	}
	if !agySessionRowSafeIdle(row) {
		return false
	}
	if n.reload.Preparing() {
		return false
	}
	id := row.SessionID
	if !n.isManagedAgySession(id) {
		return false
	}
	if n.runtimes != nil && n.runtimes.compacting(id) {
		return false
	}
	if n.activeAsks.active(id) || n.hasPendingInteraction(id) {
		return false
	}
	if n.state != nil {
		if st, ok := n.state.get(id); ok && (st.SegmentRetired || len(st.Tasks) > 0) {
			return false
		}
	}
	if _, _, held := n.agyOpenIn(id); held {
		return false
	}
	return true
}

func (n *Node) observeAgySessions(h *runtimeConn, list []proto.Session) {
	if h == nil || h.agent() != agyAgent {
		return
	}
	now := n.agyNow()
	policy := n.agyIdleCompactionPolicy()
	seen := make(map[string]bool, len(list))
	for _, row := range list {
		if row.SessionID == "" {
			continue
		}
		seen[row.SessionID] = true
		safe := n.agySessionSafeIdle(h, row)
		n.updateAgyIdleState(row, safe, policy.ContextTokens, now)
	}
	n.agyIdleMu.Lock()
	for id := range n.agyIdleSessions {
		if !seen[id] {
			delete(n.agyIdleSessions, id)
		}
	}
	n.agyIdleMu.Unlock()
}

func (n *Node) updateAgyIdleState(row proto.Session, safe bool, tokenThreshold int, now time.Time) time.Time {
	n.agyIdleMu.Lock()
	defer n.agyIdleMu.Unlock()
	if n.agyIdleSessions == nil {
		n.agyIdleSessions = map[string]*agyIdleState{}
	}
	st := n.agyIdleSessions[row.SessionID]
	if st == nil {
		st = &agyIdleState{}
		n.agyIdleSessions[row.SessionID] = st
	}
	if int(row.ContextTokens) < tokenThreshold {
		st.attempted = false
		st.lastAttempt = time.Time{}
	}
	if !safe {
		st.safeIdleSince = time.Time{}
		st.lastRowTime = row.UpdatedAt
		st.lastTokens = int(row.ContextTokens)
		return time.Time{}
	}
	if st.safeIdleSince.IsZero() {
		if !row.UpdatedAt.IsZero() && !row.UpdatedAt.After(now) {
			st.safeIdleSince = row.UpdatedAt
		} else {
			st.safeIdleSince = now
		}
	} else if !row.UpdatedAt.IsZero() && row.UpdatedAt.After(st.lastRowTime) && row.UpdatedAt.After(st.safeIdleSince) {
		if row.UpdatedAt.After(now) {
			st.safeIdleSince = now
		} else {
			st.safeIdleSince = row.UpdatedAt
		}
	}
	st.lastRowTime = row.UpdatedAt
	st.lastTokens = int(row.ContextTokens)
	return st.safeIdleSince
}

func (n *Node) agyIdleReady(h *runtimeConn, row proto.Session, policy assistantwire.AgyIdleCompaction, now time.Time) (time.Duration, bool) {
	if !policy.Enabled || int(row.ContextTokens) < policy.ContextTokens {
		return 0, false
	}
	if !n.agySessionSafeIdle(h, row) {
		n.updateAgyIdleState(row, false, policy.ContextTokens, now)
		return 0, false
	}
	idleSince := n.updateAgyIdleState(row, true, policy.ContextTokens, now)
	if idleSince.IsZero() {
		return 0, false
	}
	idleFor := now.Sub(idleSince)
	if idleFor < time.Duration(policy.IdleMinutes)*time.Minute {
		return idleFor, false
	}
	n.agyIdleMu.Lock()
	st := n.agyIdleSessions[row.SessionID]
	latched := st != nil && st.attempted
	cooling := st != nil && !st.lastAttempt.IsZero() && now.Sub(st.lastAttempt) < agyIdleRetryCooldown
	n.agyIdleMu.Unlock()
	if latched || cooling {
		return idleFor, false
	}
	return idleFor, true
}

func (n *Node) markAgyIdleAttempt(id string, now time.Time) {
	n.agyIdleMu.Lock()
	defer n.agyIdleMu.Unlock()
	if n.agyIdleSessions == nil {
		n.agyIdleSessions = map[string]*agyIdleState{}
	}
	st := n.agyIdleSessions[id]
	if st == nil {
		st = &agyIdleState{}
		n.agyIdleSessions[id] = st
	}
	st.attempted = true
	st.lastAttempt = now
}

func (n *Node) recordAgyIdleFailure(id string, now time.Time) {
	n.agyIdleMu.Lock()
	defer n.agyIdleMu.Unlock()
	if st := n.agyIdleSessions[id]; st != nil {
		st.attempted = false
		st.lastAttempt = now
	}
}

// sweepAgyIdleCompactions proactively compacts Persona-managed Antigravity sessions whose context and safe-idle
// duration have both crossed the configured thresholds.
func (n *Node) sweepAgyIdleCompactions(ctx context.Context, now time.Time) {
	if n.runtimes == nil || n.reload.Preparing() {
		return
	}
	policy := n.agyIdleCompactionPolicy()
	if !policy.Enabled {
		return
	}
	for _, snap := range n.runtimes.agySnapshots() {
		idleFor, ready := n.agyIdleReady(snap.host, snap.row, policy, now)
		if !ready {
			continue
		}
		go n.runAgyIdleCompaction(ctx, snap.host, snap.row.SessionID, int(snap.row.ContextTokens), idleFor)
	}
}

func (n *Node) runAgyIdleCompaction(ctx context.Context, h *runtimeConn, id string, tokens int, idleFor time.Duration) {
	unlock, ok := n.typing.tryLock(id)
	if !ok {
		return
	}
	defer unlock()
	if ctx.Err() != nil {
		return
	}
	row, ok := h.row(id)
	if !ok {
		return
	}
	now := n.agyNow()
	policy := n.agyIdleCompactionPolicy()
	if _, ready := n.agyIdleReady(h, row, policy, now); !ready {
		return
	}
	n.markAgyIdleAttempt(id, now)
	isAssistant := n.isAssistantSession(id)
	compaction, err := n.startRuntimeCompactionLocked(ctx, h, id, isAssistant, true, false)
	if err != nil {
		n.recordAgyIdleFailure(id, n.agyNow())
		n.logger.Printf("agy %s: idle auto-compact start failed: %v", id, err)
		return
	}
	n.logger.Printf("agy %s: idle %s with %d tokens (>= %d), auto-compacting", id, idleFor.Round(time.Second), tokens, policy.ContextTokens)
	go func() {
		<-compaction.done
		if failure := compaction.failureMessage(); failure != "" {
			n.recordAgyIdleFailure(id, n.agyNow())
			n.logger.Printf("agy %s: idle auto-compact failed: %s", id, failure)
		}
	}()
}

func isAgySkipPreInputCompactCommand(text string, images int) bool {
	if images > 0 {
		return false
	}
	fields := strings.Fields(strings.TrimSpace(text))
	if len(fields) == 0 {
		return true
	}
	switch strings.ToLower(fields[0]) {
	case "/compact", "/clear", "/new", "/exit", "/quit", "/reset":
		return true
	default:
		return false
	}
}

// agyIdleCompactBeforeInput ensures an idle Antigravity session over both thresholds is compacted before delivering
// new input. If a silent idle compaction is already in flight, it waits for it to finish so the input lands cleanly
// right after compaction. Failures are logged and never block delivering the caller's message.
func (n *Node) agyIdleCompactBeforeInput(ctx context.Context, h *runtimeConn, id, text string, images []proto.Image, isAssistant, lockHeld bool) error {
	if h == nil || h.agent() != agyAgent || id == "" {
		return nil
	}
	if strings.TrimSpace(text) == "" && len(images) == 0 {
		return nil
	}
	if isAgySkipPreInputCompactCommand(text, len(images)) {
		return nil
	}
	// If an automatic silent compaction is already running (e.g. triggered by the idle sweep right before this input),
	// wait for it to finish before taking the input lock or delivering the message.
	if active := n.runtimes.activeCompaction(id); active != nil && active.silent {
		select {
		case <-active.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if !lockHeld {
		unlock := n.typing.lock(id)
		defer unlock()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if active := n.runtimes.activeCompaction(id); active != nil && active.silent {
			select {
			case <-active.done:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	row, ok := h.row(id)
	if !ok {
		return nil
	}
	now := n.agyNow()
	policy := n.agyIdleCompactionPolicy()
	idleFor, ready := n.agyIdleReady(h, row, policy, now)
	if !ready {
		return nil
	}
	n.markAgyIdleAttempt(id, now)
	compaction, err := n.startRuntimeCompactionLocked(ctx, h, id, isAssistant, true, false)
	if err != nil {
		n.recordAgyIdleFailure(id, n.agyNow())
		n.logger.Printf("agy %s: pre-input auto-compact start failed (continuing send): %v", id, err)
		return nil
	}
	n.logger.Printf("agy %s: pre-input auto-compact after %s idle with %d tokens (>= %d)", id, idleFor.Round(time.Second), row.ContextTokens, policy.ContextTokens)
	select {
	case <-compaction.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	if failure := compaction.failureMessage(); failure != "" {
		n.recordAgyIdleFailure(id, n.agyNow())
		n.logger.Printf("agy %s: pre-input auto-compact failed (continuing send): %s", id, failure)
	}
	return nil
}
