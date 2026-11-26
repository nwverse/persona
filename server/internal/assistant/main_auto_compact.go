package assistant

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
	"github.com/nwverse/persona/server/internal/transcript"
)

// Automatic idle compaction for the main conversation (#14686):
//
// Claude Code and Codex managed launchers carry CLAUDE_CODE_AUTO_COMPACT_WINDOW / model_auto_compact_token_limit,
// and Claude also compacts before idle release or unattended delivery on the node. Antigravity (agy) and other
// runtimes have no node-side automatic compaction path, so a long-lived main conversation grows unboundedly until
// the user runs /compact manually.
//
// When a main-conversation turn finishes and settles idle (or a delayed export/usage update arrives shortly after
// turn end while still idle), the manager checks context load:
//   - Primary: context_tokens against 75% of the effective compaction window (min(context_window,
//     assistant.main_compact_window), or context_window when main_compact_window == 0), or an explicitly saved
//     assistant.idle_auto_compact_tokens threshold.
//   - Fallback when context_tokens is unreported or frozen across substantial transcript growth: incremental steps,
//     uncompacted item byte volume since the latest compaction notice, or cumulative output token growth.
//
// Eligible sessions trigger assistant.compact silently with strict idle verification, re-entrancy locking, cooldown
// backoff, and watermark latching so active turns and rapid follow-up messages are never interrupted.

const (
	// 75% of the effective window, matching node/assistant_compaction_policy.go idleThreshold.
	mainAutoCompactRatioNum = 3
	mainAutoCompactRatioDen = 4

	// Fallback thresholds when ContextTokens is missing or frozen across turns.
	mainAutoCompactFallbackSteps        = 160        // incremental transcript steps since last compaction
	mainAutoCompactFallbackBytes        = 512 * 1024 // 512 KiB of uncompacted item content since last compaction
	mainAutoCompactFallbackOutputTokens = 80_000     // cumulative generated output tokens since last compaction
	mainAutoCompactItemsLimit           = 200        // bounded session.items read for fallback volume/boundary scan
	mainAutoCompactOutputHistoryMax     = 32         // recent output samples kept for compaction-boundary attribution

	// Timing guards (overridable on mainAutoCompactBook for deterministic tests).
	defaultMainAutoCompactCooldown        = 15 * time.Minute
	defaultMainAutoCompactFailureCooldown = 10 * time.Minute
	defaultMainAutoCompactBusyCooldown    = time.Minute
	defaultMainAutoCompactPendingTimeout  = 10 * time.Minute
	defaultMainAutoCompactInputQuiet      = 5 * time.Second
	defaultMainAutoCompactDelayedWindow   = 2 * time.Minute
	defaultMainAutoCompactCallTimeout     = time.Minute
	defaultMainAutoCompactProbeTimeout    = 10 * time.Second
)

type mainAutoCompactBook struct {
	mu        sync.Mutex
	bySession map[SessionRef]*mainAutoCompactState

	// Optional overrides for unit tests; zero values use defaults above.
	now             func() time.Time
	cooldown        time.Duration
	failureCooldown time.Duration
	busyCooldown    time.Duration
	pendingTimeout  time.Duration
	inputQuiet      time.Duration
	delayedWindow   time.Duration
	callTimeout     time.Duration
}

type mainCompactSeqOutput struct {
	seq    int64
	output int
}

type mainCompactTimeOutput struct {
	at     time.Time
	output int
}

type mainAutoCompactState struct {
	// Transcript Item.Seq watermarks (strictly from session.items probes; never mixed with Session.Rev).
	baselineSeq        int64
	baselineKnown      bool
	lastProbedSeq      int64
	lastTokenChangeSeq int64
	lastAttemptSeq     int64

	// Cumulative output-token watermarks and unit-separated attribution histories.
	baselineOutputTokens int
	lastOutputTokens     int
	seqOutputHistory     []mainCompactSeqOutput
	timeOutputHistory    []mainCompactTimeOutput

	// Opaque Session.Rev watermarks (change detection and attempt deduplication only; never subtracted as steps).
	observed           bool
	lastRev            int64
	probedOnce         bool
	lastProbeRev       int64
	lastTokenChangeRev int64
	lastTokenChangeAt  time.Time

	lastTokens               int
	lastContextWindow        int
	peakTokens               int
	tokensValidSinceBaseline bool
	seenCompactionAt         time.Time

	sawWork             bool
	pendingDelayedCheck bool
	delayedScheduled    bool
	lastTurnEndAt       time.Time
	lastInputAt         time.Time

	inFlight          bool
	startedAt         time.Time
	cooldownUntil     time.Time
	hasAttempted      bool
	lastAttemptRev    int64
	lastAttemptTokens int
	unsupported       bool
}

func (b *mainAutoCompactBook) clock() time.Time {
	if b.now != nil {
		return b.now()
	}
	return time.Now()
}

func (b *mainAutoCompactBook) cooldownPeriod() time.Duration {
	if b.cooldown > 0 {
		return b.cooldown
	}
	return defaultMainAutoCompactCooldown
}

func (b *mainAutoCompactBook) failureCooldownPeriod() time.Duration {
	if b.failureCooldown > 0 {
		return b.failureCooldown
	}
	return defaultMainAutoCompactFailureCooldown
}

func (b *mainAutoCompactBook) busyCooldownPeriod() time.Duration {
	if b.busyCooldown > 0 {
		return b.busyCooldown
	}
	return defaultMainAutoCompactBusyCooldown
}

func (b *mainAutoCompactBook) pendingTimeoutPeriod() time.Duration {
	if b.pendingTimeout > 0 {
		return b.pendingTimeout
	}
	return defaultMainAutoCompactPendingTimeout
}

func (b *mainAutoCompactBook) inputQuietPeriod() time.Duration {
	if b.inputQuiet > 0 {
		return b.inputQuiet
	}
	return defaultMainAutoCompactInputQuiet
}

func (b *mainAutoCompactBook) delayedWindowPeriod() time.Duration {
	if b.delayedWindow > 0 {
		return b.delayedWindow
	}
	return defaultMainAutoCompactDelayedWindow
}

func (b *mainAutoCompactBook) callTimeoutPeriod() time.Duration {
	if b.callTimeout > 0 {
		return b.callTimeout
	}
	return defaultMainAutoCompactCallTimeout
}

func (b *mainAutoCompactBook) stateLocked(ref SessionRef) *mainAutoCompactState {
	if b.bySession == nil {
		b.bySession = map[SessionRef]*mainAutoCompactState{}
	}
	st := b.bySession[ref]
	if st == nil {
		st = &mainAutoCompactState{}
		b.bySession[ref] = st
	}
	// Keep only the active main session entry so switching main chats leaves no unbounded map growth.
	for k := range b.bySession {
		if k != ref {
			delete(b.bySession, k)
		}
	}
	return st
}

// isMainSession reports whether (machine, sid) is the currently configured main conversation.
func isMainSession(cfg Settings, machine, sid string) bool {
	return sid != "" && cfg.Main == sid && machine != "" && cfg.mainRouteMachine() == machine
}

// noteMainAutoCompactInput records that new input was accepted for (machine, sid) so a settled idle check
// racing an in-flight user or relayed send does not interrupt it.
func (s *Service) noteMainAutoCompactInput(machine, sid string) {
	if machine == "" || sid == "" {
		return
	}
	cfg := s.settings(context.Background())
	if !isMainSession(cfg, machine, sid) {
		return
	}
	ref := SessionRef{MachineID: machine, SessionID: sid}
	b := &s.mainAutoCompact
	b.mu.Lock()
	st := b.stateLocked(ref)
	at := b.clock()
	if at.After(st.lastInputAt) {
		st.lastInputAt = at
	}
	st.pendingDelayedCheck = false
	b.mu.Unlock()
}

// noteMainAutoCompactTurnEnded records that a main-conversation turn finished so settled and delayed-export
// idle checks know when the turn ended.
func (s *Service) noteMainAutoCompactTurnEnded(ctx context.Context, machine, sid string) {
	cfg := s.settings(ctx)
	if !isMainSession(cfg, machine, sid) {
		return
	}
	ref := SessionRef{MachineID: machine, SessionID: sid}
	b := &s.mainAutoCompact
	b.mu.Lock()
	st := b.stateLocked(ref)
	st.sawWork = true
	st.lastTurnEndAt = b.clock()
	st.pendingDelayedCheck = true
	b.mu.Unlock()
}

// mainAutoCompactSessions observes session-list updates for the main conversation: tracking compaction completions,
// native context drops, and delayed token/rev exports arriving shortly after a turn ends.
func (s *Service) mainAutoCompactSessions(ctx context.Context, machine string, list []proto.Session) {
	cfg := s.settings(ctx)
	if cfg.Main == "" || machine == "" || cfg.mainRouteMachine() != machine {
		return
	}
	var row proto.Session
	found := false
	for i := range list {
		if list[i].SessionID == cfg.Main {
			row, found = list[i], true
			break
		}
	}
	if !found {
		return
	}
	ref := SessionRef{MachineID: machine, SessionID: row.SessionID}
	b := &s.mainAutoCompact
	b.mu.Lock()
	now := b.clock()
	st := b.stateLocked(ref)
	prevTokens, prevRev, prevOutput := st.lastTokens, st.lastRev, st.lastOutputTokens
	b.observeLocked(st, row, now)
	if !st.lastTurnEndAt.IsZero() && now.Sub(st.lastTurnEndAt) > b.delayedWindowPeriod() {
		st.pendingDelayedCheck = false
	}
	grew := int(row.ContextTokens) > prevTokens || row.Rev > prevRev || row.OutputTokens > prevOutput
	scheduleDelayed := grew &&
		st.pendingDelayedCheck &&
		!st.delayedScheduled &&
		!st.inFlight &&
		!st.unsupported &&
		!st.lastTurnEndAt.IsZero() &&
		now.Sub(st.lastTurnEndAt) <= b.delayedWindowPeriod() &&
		(row.Status == proto.StatusIdle || row.Status == proto.StatusDone) &&
		!row.Starting
	if scheduleDelayed {
		st.delayedScheduled = true
	}
	b.mu.Unlock()
	if scheduleDelayed {
		s.afterSettle(context.WithoutCancel(ctx), s.mainAutoCompactDelayedSettled, machine, row.SessionID)
	}
}

func (s *Service) mainAutoCompactDelayedSettled(ctx context.Context, machine, sid string) {
	ref := SessionRef{MachineID: machine, SessionID: sid}
	b := &s.mainAutoCompact
	b.mu.Lock()
	if st := b.bySession[ref]; st != nil {
		st.delayedScheduled = false
	}
	b.mu.Unlock()
	s.mainAutoCompactTurnSettled(ctx, machine, sid)
}

func sessionSampleTime(row proto.Session, now time.Time) time.Time {
	if !row.UpdatedAt.IsZero() {
		return row.UpdatedAt
	}
	return now
}

func (b *mainAutoCompactBook) observeLocked(st *mainAutoCompactState, row proto.Session, now time.Time) {
	sampleAt := sessionSampleTime(row, now)
	if !st.baselineKnown && !st.observed {
		// Initialize output-token baseline on first observation so a hub restart mid-session
		// does not treat historical cumulative output tokens as fresh post-compaction growth.
		st.baselineOutputTokens = row.OutputTokens
	}
	st.observed = true
	if row.Status == proto.StatusWorking {
		st.sawWork = true
		st.pendingDelayedCheck = false
	}
	if st.inFlight && !st.startedAt.IsZero() && now.Sub(st.startedAt) >= b.pendingTimeoutPeriod() {
		st.inFlight = false
		st.startedAt = time.Time{}
	}
	if c := row.Compaction; c != nil && !c.StartedAt.IsZero() {
		switch c.State {
		case proto.CompactionRunning:
			st.inFlight = true
			if st.startedAt.IsZero() {
				st.startedAt = c.StartedAt
			}
		case proto.CompactionDone:
			if !c.StartedAt.Equal(st.seenCompactionAt) {
				st.seenCompactionAt = c.StartedAt
				st.inFlight = false
				st.startedAt = time.Time{}
				b.resetCompactionBaselineLocked(st, row, now, true)
			}
		case proto.CompactionFailed:
			if !c.StartedAt.Equal(st.seenCompactionAt) {
				st.seenCompactionAt = c.StartedAt
				st.inFlight = false
				st.startedAt = time.Time{}
				if until := now.Add(b.failureCooldownPeriod()); until.After(st.cooldownUntil) {
					st.cooldownUntil = until
				}
			}
		}
	}
	if row.ContextWindow > 0 {
		st.lastContextWindow = int(row.ContextWindow)
	}
	tokens := int(row.ContextTokens)
	plausible := tokens > 0 && plausibleMainContext(tokens, effectiveMainContextWindow(st, row))
	if plausible {
		if st.peakTokens > 0 && tokens < st.peakTokens/2 && row.Status != proto.StatusWorking {
			// Context shrank by over half outside an active turn: native or manual compaction occurred.
			b.resetCompactionBaselineLocked(st, row, now, false)
		} else if tokens > st.peakTokens {
			st.peakTokens = tokens
		}
		if tokens != st.lastTokens || !st.tokensValidSinceBaseline {
			st.lastTokens = tokens
			st.tokensValidSinceBaseline = true
			st.lastTokenChangeRev = row.Rev
			st.lastTokenChangeAt = sampleAt
			st.lastTokenChangeSeq = 0
		}
	}
	if row.Rev > st.lastRev {
		st.lastRev = row.Rev
	}
	if row.OutputTokens > st.lastOutputTokens {
		st.lastOutputTokens = row.OutputTokens
	}
	st.recordTimeOutputSample(sampleAt, row.OutputTokens)
}

func (st *mainAutoCompactState) recordSeqOutputSample(seq int64, output int) {
	if seq <= 0 || (output <= 0 && len(st.seqOutputHistory) > 0) {
		return
	}
	for len(st.seqOutputHistory) > 0 && st.seqOutputHistory[len(st.seqOutputHistory)-1].seq > seq {
		st.seqOutputHistory = st.seqOutputHistory[:len(st.seqOutputHistory)-1]
	}
	if n := len(st.seqOutputHistory); n > 0 && st.seqOutputHistory[n-1].seq == seq {
		if output > st.seqOutputHistory[n-1].output {
			st.seqOutputHistory[n-1].output = output
		}
		return
	}
	st.seqOutputHistory = append(st.seqOutputHistory, mainCompactSeqOutput{seq: seq, output: output})
	if len(st.seqOutputHistory) > mainAutoCompactOutputHistoryMax {
		copy(st.seqOutputHistory, st.seqOutputHistory[len(st.seqOutputHistory)-mainAutoCompactOutputHistoryMax:])
		st.seqOutputHistory = st.seqOutputHistory[:mainAutoCompactOutputHistoryMax]
	}
}

func (st *mainAutoCompactState) recordTimeOutputSample(at time.Time, output int) {
	if at.IsZero() || (output <= 0 && len(st.timeOutputHistory) > 0) {
		return
	}
	for len(st.timeOutputHistory) > 0 && st.timeOutputHistory[len(st.timeOutputHistory)-1].at.After(at) {
		st.timeOutputHistory = st.timeOutputHistory[:len(st.timeOutputHistory)-1]
	}
	if n := len(st.timeOutputHistory); n > 0 && st.timeOutputHistory[n-1].at.Equal(at) {
		if output > st.timeOutputHistory[n-1].output {
			st.timeOutputHistory[n-1].output = output
		}
		return
	}
	st.timeOutputHistory = append(st.timeOutputHistory, mainCompactTimeOutput{at: at, output: output})
	if len(st.timeOutputHistory) > mainAutoCompactOutputHistoryMax {
		copy(st.timeOutputHistory, st.timeOutputHistory[len(st.timeOutputHistory)-mainAutoCompactOutputHistoryMax:])
		st.timeOutputHistory = st.timeOutputHistory[:mainAutoCompactOutputHistoryMax]
	}
}

func (st *mainAutoCompactState) outputAtOrBefore(seq int64, at time.Time) (int, bool) {
	if seq > 0 {
		for i := len(st.seqOutputHistory) - 1; i >= 0; i-- {
			if st.seqOutputHistory[i].seq <= seq {
				return st.seqOutputHistory[i].output, true
			}
		}
	}
	if !at.IsZero() {
		for i := len(st.timeOutputHistory) - 1; i >= 0; i-- {
			if !st.timeOutputHistory[i].at.After(at) {
				return st.timeOutputHistory[i].output, true
			}
		}
	}
	return 0, false
}

func (st *mainAutoCompactState) tokenChangedAfterBoundaryLocked(compactSeq int64, compactTS time.Time) bool {
	if st.lastTokenChangeSeq > 0 && st.lastTokenChangeSeq > compactSeq {
		return true
	}
	if !st.lastTokenChangeAt.IsZero() && !compactTS.IsZero() && st.lastTokenChangeAt.After(compactTS) {
		return true
	}
	return false
}

func (b *mainAutoCompactBook) resetCompactionBaselineLocked(st *mainAutoCompactState, row proto.Session, now time.Time, setCooldown bool) {
	sampleAt := sessionSampleTime(row, now)
	tokens := int(row.ContextTokens)
	if !plausibleMainContext(tokens, effectiveMainContextWindow(st, row)) {
		tokens = 0
	}
	st.baselineSeq = st.lastProbedSeq
	st.baselineOutputTokens = row.OutputTokens
	st.baselineKnown = true
	st.peakTokens = tokens
	st.lastTokens = tokens
	st.tokensValidSinceBaseline = tokens > 0
	if tokens > 0 {
		st.lastTokenChangeRev = row.Rev
		st.lastTokenChangeAt = sampleAt
		st.lastTokenChangeSeq = st.lastProbedSeq
	} else {
		st.lastTokenChangeRev = 0
		st.lastTokenChangeAt = time.Time{}
		st.lastTokenChangeSeq = st.lastProbedSeq
	}
	st.lastRev = row.Rev
	st.probedOnce = false
	st.lastProbeRev = 0
	st.pendingDelayedCheck = false
	st.hasAttempted = false
	st.lastAttemptRev = 0
	st.lastAttemptSeq = 0
	st.lastAttemptTokens = 0
	if st.lastProbedSeq > 0 {
		st.seqOutputHistory = []mainCompactSeqOutput{{seq: st.lastProbedSeq, output: row.OutputTokens}}
	} else {
		st.seqOutputHistory = nil
	}
	if !sampleAt.IsZero() {
		st.timeOutputHistory = []mainCompactTimeOutput{{at: sampleAt, output: row.OutputTokens}}
	} else {
		st.timeOutputHistory = nil
	}
	if setCooldown {
		if until := now.Add(b.cooldownPeriod()); until.After(st.cooldownUntil) {
			st.cooldownUntil = until
		}
	}
}

// mainAutoCompactTurnSettled evaluates whether the idle main conversation exceeds compaction thresholds and,
// if so, silently invokes assistant.compact on its node.
func (s *Service) mainAutoCompactTurnSettled(ctx context.Context, machine, sid string) {
	cfg := s.settings(ctx)
	if !s.idleAutoCompact(ctx) || !isMainSession(cfg, machine, sid) {
		return
	}
	sess, ok := s.readMainSession(ctx, machine, sid)
	if !ok || !s.mainAutoCompactSessionIdle(machine, sess) {
		return
	}

	ref := SessionRef{MachineID: machine, SessionID: sid}
	b := &s.mainAutoCompact
	b.mu.Lock()
	now := b.clock()
	st := b.stateLocked(ref)
	b.observeLocked(st, sess, now)
	if !b.canAttemptLocked(st, sess, now) {
		b.mu.Unlock()
		return
	}

	reason, needProbe, tokensStale := evaluateMainCompactLoad(cfg, st, sess)
	b.mu.Unlock()

	if reason == "" && needProbe {
		pageItems, probeErr := s.probeMainAutoCompactItems(ctx, machine, sid)
		b.mu.Lock()
		now = b.clock()
		st = b.stateLocked(ref)
		window := effectiveMainContextWindow(st, sess)
		plausible := int(sess.ContextTokens) > 0 && plausibleMainContext(int(sess.ContextTokens), window)
		effectiveFloor := st.baselineSeq
		if tokensStale && st.lastTokenChangeSeq > effectiveFloor {
			effectiveFloor = st.lastTokenChangeSeq
		}
		var m postCompactMetrics
		if probeErr == nil {
			st.probedOnce = true
			st.lastProbeRev = sess.Rev
			if len(pageItems) > 0 {
				if tokensStale && st.lastTokenChangeSeq == 0 && !st.lastTokenChangeAt.IsZero() {
					if inferred := lastSeqAtOrBefore(pageItems, st.lastTokenChangeAt); inferred > 0 {
						st.lastTokenChangeSeq = inferred
						if inferred > effectiveFloor {
							effectiveFloor = inferred
						}
					}
				}
				m = measurePostCompactItems(pageItems, effectiveFloor)
				if m.maxSeq > st.lastProbedSeq {
					st.lastProbedSeq = m.maxSeq
				}
				if m.compactSeq > st.baselineSeq {
					st.baselineSeq = m.compactSeq
					st.baselineKnown = true
					if st.baselineSeq > effectiveFloor {
						effectiveFloor = st.baselineSeq
					}
					switch {
					case m.steps == 0:
						if sess.OutputTokens > 0 {
							st.baselineOutputTokens = sess.OutputTokens
						}
					case m.outputTokens > 0:
						// Reconstruct cumulative output at the compaction boundary instead of folding post-boundary output into baseline.
						if sess.OutputTokens > 0 {
							st.baselineOutputTokens = max(sess.OutputTokens-int(m.outputTokens), 0)
						}
					case m.hasItemUsage:
						if sess.OutputTokens > 0 {
							st.baselineOutputTokens = sess.OutputTokens
						}
					default:
						// Items omit per-item usage: advance baseline only to output observed at or before the boundary
						// using strictly matching Item.Seq or timestamp histories.
						if preOut, ok := st.outputAtOrBefore(m.compactSeq, m.compactTS); ok && preOut > st.baselineOutputTokens {
							st.baselineOutputTokens = preOut
						}
					}
					if !plausible && !st.tokenChangedAfterBoundaryLocked(m.compactSeq, m.compactTS) {
						st.lastTokens = 0
						st.peakTokens = 0
						st.tokensValidSinceBaseline = false
						st.lastTokenChangeRev = 0
						st.lastTokenChangeSeq = m.compactSeq
						st.lastTokenChangeAt = time.Time{}
					}
					if st.hasAttempted && (st.lastAttemptSeq == 0 || m.compactSeq > st.lastAttemptSeq) {
						st.hasAttempted = false
						st.lastAttemptRev = 0
						st.lastAttemptSeq = 0
						st.lastAttemptTokens = 0
					}
				}
				if m.maxSeq > 0 {
					st.recordSeqOutputSample(m.maxSeq, sess.OutputTokens)
				}
			}
		}
		deltaOutput := max(sess.OutputTokens-st.baselineOutputTokens, 0)
		if int(m.outputTokens) > deltaOutput {
			deltaOutput = int(m.outputTokens)
		}
		if !plausible && probeErr == nil && st.tokensValidSinceBaseline && st.lastTokens > 0 {
			if threshold := mainAutoCompactTokenThreshold(cfg, window); threshold > 0 && st.lastTokens >= threshold {
				reason = fmt.Sprintf("context %d/%d tokens (threshold %d)", st.lastTokens, window, threshold)
			}
		}
		if reason == "" {
			switch {
			case m.bytes >= mainAutoCompactFallbackBytes:
				reason = fmt.Sprintf("incremental volume %d bytes since seq %d", m.bytes, effectiveFloor)
			case m.steps >= mainAutoCompactFallbackSteps:
				reason = fmt.Sprintf("incremental steps %d since seq %d", m.steps, effectiveFloor)
			case deltaOutput >= mainAutoCompactFallbackOutputTokens:
				reason = fmt.Sprintf("incremental output %d tokens", deltaOutput)
			}
		}
		b.mu.Unlock()
	}
	if reason == "" {
		return
	}

	// Re-read the session after any probe to ensure nothing started running in the meantime.
	if needProbe {
		sess, ok = s.readMainSession(ctx, machine, sid)
		if !ok || !s.mainAutoCompactSessionIdle(machine, sess) {
			return
		}
	}

	b.mu.Lock()
	now = b.clock()
	st = b.stateLocked(ref)
	b.observeLocked(st, sess, now)
	if !b.canAttemptLocked(st, sess, now) {
		b.mu.Unlock()
		return
	}
	st.inFlight = true
	st.startedAt = now
	st.pendingDelayedCheck = false
	st.hasAttempted = true
	st.lastAttemptRev = sess.Rev
	st.lastAttemptSeq = st.lastProbedSeq
	st.lastAttemptTokens = effectiveMainContextTokens(st, sess)
	b.mu.Unlock()

	cctx, cancel := context.WithTimeout(ctx, b.callTimeoutPeriod())
	err := s.call(cctx, machine, DeviceAssistantCompact, map[string]any{"session_id": sid}, nil)
	cancel()

	b.mu.Lock()
	doneAt := b.clock()
	st = b.stateLocked(ref)
	if err == nil {
		// Stay inFlight until SessionsChanged reports CompactionDone/Failed (bounded by pendingTimeout),
		// and enforce the minimum cooldown immediately so a fast completion cannot re-trigger at once.
		if until := doneAt.Add(b.cooldownPeriod()); until.After(st.cooldownUntil) {
			st.cooldownUntil = until
		}
		b.mu.Unlock()
		s.logf("assistant main %s: automatic idle compaction started (%s)", short(sid), reason)
		return
	}
	st.inFlight = false
	st.startedAt = time.Time{}
	code := mainCompactRPCErrorCode(err)
	switch {
	case isMainCompactUnsupported(err, code):
		st.unsupported = true
	case code == "busy" || code == "compacting":
		st.cooldownUntil = doneAt.Add(b.busyCooldownPeriod())
		// Allow the next turn end to retry once the active turn or compaction finishes.
		st.hasAttempted = false
		st.lastAttemptRev, st.lastAttemptSeq, st.lastAttemptTokens = 0, 0, 0
	default:
		st.cooldownUntil = doneAt.Add(b.failureCooldownPeriod())
	}
	b.mu.Unlock()
	s.logf("assistant main %s: automatic idle compaction skipped: %v", short(sid), err)
}

func (b *mainAutoCompactBook) canAttemptLocked(st *mainAutoCompactState, sess proto.Session, now time.Time) bool {
	if st.unsupported || st.inFlight {
		return false
	}
	if !st.cooldownUntil.IsZero() && now.Before(st.cooldownUntil) {
		return false
	}
	// Protect against racing an input accepted after the turn ended (or when no turn end was observed yet).
	if !st.lastInputAt.IsZero() && (st.lastTurnEndAt.IsZero() || !st.lastInputAt.Before(st.lastTurnEndAt)) &&
		now.Sub(st.lastInputAt) < b.inputQuietPeriod() {
		return false
	}
	// Do not re-attempt on the exact same session watermark after a previous attempt.
	if st.hasAttempted && st.lastAttemptRev == sess.Rev && st.lastAttemptTokens == effectiveMainContextTokens(st, sess) {
		return false
	}
	return true
}

func (s *Service) readMainSession(ctx context.Context, machine, sid string) (proto.Session, bool) {
	if sess, ok := s.decoratedSession(machine, sid); ok {
		return sess, true
	}
	return s.session(ctx, machine, sid)
}

func (s *Service) mainAutoCompactSessionIdle(machine string, sess proto.Session) bool {
	if !sess.Assistant || sess.PaneID != "" || sess.IsDeleted() || sess.Archived || sess.HiddenByDirectory() {
		return false
	}
	if sess.Starting || (sess.Status != proto.StatusIdle && sess.Status != proto.StatusDone) {
		return false
	}
	if sess.State == proto.StateWorking || sess.State == proto.StateNeedsYou || sess.PendingInteractions > 0 ||
		sess.BackgroundWorking || sess.BackgroundWaiting || backgroundRunning(sess) {
		return false
	}
	if len(sess.Queued) > 0 || (sess.Undelivered != nil && len(*sess.Undelivered) > 0) {
		return false
	}
	if sess.Compaction != nil && sess.Compaction.State == proto.CompactionRunning {
		return false
	}
	if sess.TurnErrorKind == proto.TurnErrorUsageLimit || sess.TurnErrorKind == proto.TurnErrorUnrecoverable || sess.TurnErrorResumes {
		return false
	}
	if s.queuedFor(sess.SessionID) > 0 || s.meetingTurnNow(machine, sess.SessionID) || s.hasPendingMainInteraction(machine, sess.SessionID) {
		return false
	}
	return true
}

func (s *Service) hasPendingMainInteraction(machine, sid string) bool {
	for _, ix := range s.pendingQuestions() {
		if ix.SessionID == sid && (ix.MachineID == "" || ix.MachineID == machine) {
			return true
		}
	}
	if s.fleetOn() {
		for _, ix := range s.pendingInteractions() {
			if ix.SessionID == sid && (ix.MachineID == "" || ix.MachineID == machine) {
				return true
			}
		}
	}
	return false
}

// plausibleMainContext accepts token counts up to the larger of Claude's 1M limit and the model's reported
// context window (e.g. 1,048,576 on Gemini models), rejecting gateway cumulative-input spikes.
func plausibleMainContext(tokens, contextWindow int) bool {
	if tokens <= 0 {
		return false
	}
	if contextWindow > transcript.MaxContextTokens {
		return tokens <= contextWindow
	}
	return transcript.PlausibleContext(tokens, 0)
}

// mainAutoCompactTokenThreshold returns the token count at which the main conversation should compact when idle.
// An explicitly saved assistant.idle_auto_compact_tokens replaces the 75%-of-window default; otherwise 75% of the
// smaller positive of contextWindow and assistant.main_compact_window (0 follows contextWindow).
func mainAutoCompactTokenThreshold(cfg Settings, contextWindow int) int {
	if v := cfg.IdleAutoCompactTokens; v != nil && *v >= minIdleAutoCompactTokens && *v <= maxIdleAutoCompactTokens {
		return *v
	}
	mainWin := mainCompactWindowOf(cfg)
	effective := smallerPositive(contextWindow, mainWin)
	if effective <= 0 {
		return 0
	}
	return effective * mainAutoCompactRatioNum / mainAutoCompactRatioDen
}

func smallerPositive(a, b int) int {
	switch {
	case a > 0 && b > 0:
		return min(a, b)
	case a > 0:
		return a
	case b > 0:
		return b
	default:
		return 0
	}
}

func effectiveMainContextWindow(st *mainAutoCompactState, sess proto.Session) int {
	if sess.ContextWindow > 0 {
		return int(sess.ContextWindow)
	}
	return st.lastContextWindow
}

func effectiveMainContextTokens(st *mainAutoCompactState, sess proto.Session) int {
	tokens := int(sess.ContextTokens)
	if tokens > 0 && plausibleMainContext(tokens, effectiveMainContextWindow(st, sess)) {
		return tokens
	}
	return st.lastTokens
}

func evaluateMainCompactLoad(cfg Settings, st *mainAutoCompactState, sess proto.Session) (reason string, needProbe bool, tokensStale bool) {
	window := effectiveMainContextWindow(st, sess)
	tokens := int(sess.ContextTokens)
	plausible := tokens > 0 && plausibleMainContext(tokens, window)
	if plausible {
		if threshold := mainAutoCompactTokenThreshold(cfg, window); threshold > 0 && tokens >= threshold {
			return fmt.Sprintf("context %d/%d tokens (threshold %d)", tokens, window, threshold), false, false
		}
	}
	tokensUnavailable := !plausible
	// Session.Rev is an opaque revision marker (file byte size on native Claude/Codex, timestamp on Codex app-server,
	// step counter on some runtimes): use it only for change detection, never as a step count or Item.Seq floor.
	tokensStale = plausible && st.tokensValidSinceBaseline && sess.Rev != st.lastTokenChangeRev
	if !tokensUnavailable && !tokensStale {
		return "", false, false
	}
	if tokensUnavailable && st.tokensValidSinceBaseline && st.lastTokens > 0 {
		if threshold := mainAutoCompactTokenThreshold(cfg, window); threshold > 0 && st.lastTokens >= threshold {
			return "", true, false
		}
	}
	revChanged := !st.probedOnce || sess.Rev != st.lastProbeRev
	deltaOutput := max(sess.OutputTokens-st.baselineOutputTokens, 0)
	needProbe = revChanged || deltaOutput >= mainAutoCompactFallbackOutputTokens
	return "", needProbe, tokensStale
}

func (s *Service) probeMainAutoCompactItems(ctx context.Context, machine, sid string) ([]proto.Item, error) {
	pctx, cancel := context.WithTimeout(ctx, defaultMainAutoCompactProbeTimeout)
	defer cancel()
	var res struct {
		Items []proto.Item `json:"items"`
	}
	params := map[string]any{"session_id": sid, "limit": mainAutoCompactItemsLimit, "lazy_images": true}
	if err := s.call(pctx, machine, DeviceSessionItems, params, &res); err != nil {
		return nil, err
	}
	return res.Items, nil
}

type postCompactMetrics struct {
	compactSeq   int64
	compactTS    time.Time
	maxSeq       int64
	steps        int
	bytes        int64
	outputTokens int64
	hasItemUsage bool
}

type mainCompactUsageKey struct {
	id    string
	model string
}

func itemEndSeq(it proto.Item) int64 {
	end := int64(it.Seq)
	if it.Fold != nil && int64(it.Fold.ThroughSeq) > end {
		end = int64(it.Fold.ThroughSeq)
	}
	return end
}

func lastSeqAtOrBefore(items []proto.Item, at time.Time) int64 {
	if at.IsZero() {
		return 0
	}
	var best int64
	for i := range items {
		it := items[i]
		ts := it.TS
		if it.Fold != nil && !it.Fold.Until.IsZero() {
			ts = it.Fold.Until
		}
		if ts.IsZero() || ts.After(at) {
			continue
		}
		if end := itemEndSeq(it); end > best {
			best = end
		}
	}
	return best
}

// measurePostCompactItems scans items from newest to oldest until floorSeq or the latest compaction notice,
// returning the compaction notice sequence/timestamp (0 if none was seen), highest item sequence on the page,
// distinct step count (including collapsed fold items), uncompacted byte volume, and post-boundary item output tokens.
func measurePostCompactItems(items []proto.Item, floorSeq int64) postCompactMetrics {
	var m postCompactMetrics
	for i := range items {
		if end := itemEndSeq(items[i]); end > m.maxSeq {
			m.maxSeq = end
		}
	}
	prevSeq := -1
	var seenUsage map[mainCompactUsageKey]struct{}
	addUsage := func(list []proto.TokenUsage) {
		if len(list) == 0 {
			return
		}
		m.hasItemUsage = true
		for _, u := range list {
			if u.Output <= 0 {
				continue
			}
			if u.ID != "" {
				if seenUsage == nil {
					seenUsage = map[mainCompactUsageKey]struct{}{}
				}
				k := mainCompactUsageKey{id: u.ID, model: u.Model}
				if _, dup := seenUsage[k]; dup {
					continue
				}
				seenUsage[k] = struct{}{}
			}
			m.outputTokens += u.Output
		}
	}
	stopIdx := -1
	for i := len(items) - 1; i >= 0; i-- {
		it := items[i]
		endSeq := itemEndSeq(it)
		if floorSeq > 0 && endSeq <= floorSeq {
			break
		}
		if isCompactionNoticeItem(it) {
			m.compactSeq = endSeq
			m.compactTS = it.TS
			stopIdx = i
			break
		}
		m.bytes += itemContentBytes(it)
		addUsage(it.Usage)
		if it.Fold != nil {
			addUsage(it.Fold.Usage)
		}
		if it.Fold != nil && it.Fold.Items > 1 {
			foldSteps := it.Fold.Items
			if floorSeq > 0 && int64(it.Seq) <= floorSeq {
				if tail := int(endSeq - floorSeq); tail < foldSteps {
					foldSteps = max(tail, 1)
				}
				m.steps += foldSteps
				prevSeq = it.Seq
				break
			}
			m.steps += foldSteps
			prevSeq = it.Seq
		} else if it.Seq != prevSeq {
			m.steps++
			prevSeq = it.Seq
		}
	}
	if m.compactSeq > 0 && !m.hasItemUsage {
		for i := stopIdx - 1; i >= 0; i-- {
			it := items[i]
			if len(it.Usage) > 0 || (it.Fold != nil && len(it.Fold.Usage) > 0) {
				m.hasItemUsage = true
				break
			}
		}
	}
	return m
}

func isCompactionNoticeItem(it proto.Item) bool {
	if it.Kind != proto.KindNotice {
		return false
	}
	return it.Compact != nil || it.Text == transcript.CompactedNotice || it.Text == transcript.CompactSummaryNotice
}

func itemContentBytes(it proto.Item) int64 {
	n := int64(len(it.Text))
	if int64(it.FullSize) > n {
		n = int64(it.FullSize)
	}
	n += int64(len(it.ToolInput)) + int64(len(it.ToolName))
	return n
}

func mainCompactRPCErrorCode(err error) string {
	var re *rpc.Error
	if errors.As(err, &re) {
		return re.Code
	}
	return ""
}

func isMainCompactUnsupported(err error, code string) bool {
	if code == "unsupported" {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "unknown method")
}
