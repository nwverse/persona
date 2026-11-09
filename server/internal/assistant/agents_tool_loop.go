package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"

	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

const (
	toolLoopCardPrefix = "tool_loop_"
	toolLoopCardTool   = "persona_tool_loop"
	toolLoopCeiling    = 30
	toolLoopMaxChecks  = 3
	toolLoopTimeout    = 5 * time.Second
)

// toolLoopState is local to the current turn. Its identity also invalidates an in-flight model call.
type toolLoopState struct {
	since, updatedAt                                  time.Time
	tool                                              string
	cardID                                            string
	pendingCard                                       *proto.Interaction // being committed before the card is published
	checks, checkedCount                              int
	pending, silenced                                 bool
	answering, consuming                              bool
	consumedSaved, pendingSaved, pendingPriorSilenced bool
}

type loopClearReason uint8

const (
	loopCandidateGone loopClearReason = iota
	loopUserInput
	loopTurnChanged
)

// clearToolLoop withdraws the card and invalidates any model call. Only a new turn resets the model budget.
func (s *Service) clearToolLoop(k [2]string, reason loopClearReason) {
	if err := s.loadToolLoops(context.Background()); err != nil {
		s.work.mu.Lock()
		if s.work.loopLoaded {
			s.work.mu.Unlock()
			s.clearToolLoop(k, reason)
			return
		}
		if s.work.loopInvalid == nil {
			s.work.loopInvalid = map[[2]string]loopClearReason{}
		}
		if previous, ok := s.work.loopInvalid[k]; !ok || reason > previous {
			s.work.loopInvalid[k] = reason
		}
		s.work.mu.Unlock()
		s.logf("tool loop: load: %v", err)
		return
	}
	s.work.mu.Lock()
	s.work.init()
	state := s.work.loops[k]
	changed := state != nil && state.cardID != ""
	if state != nil {
		delete(s.work.cards, state.cardID)
		switch {
		case reason != loopCandidateGone && !state.since.IsZero():
			checks := state.checks
			if reason == loopTurnChanged {
				checks = 0
			}
			// Ignore an old session snapshot until the node reports a different loop.
			s.work.loops[k] = &toolLoopState{since: state.since, updatedAt: time.Now().UTC(), tool: state.tool, checks: checks, silenced: true}
		case reason == loopTurnChanged:
			delete(s.work.loops, k)
		case reason == loopUserInput || !state.silenced:
			s.work.loops[k] = &toolLoopState{updatedAt: time.Now().UTC(), checks: state.checks}
			// Keep a silenced candidate even while it briefly disappears from the node's list.
		}
	}
	s.work.mu.Unlock()
	if state != nil {
		if err := s.saveToolLoops(context.Background()); err != nil {
			s.logf("tool loop: save: %v", err)
		}
	}
	if changed {
		s.publishSoon()
	}
}

// toolLoopsChanged invalidates cards and model calls as soon as a node reports a changed candidate.
// The event is a complete list for one machine; its turn budget survives a candidate change.
func (s *Service) toolLoopsChanged(d DeviceSessions) {
	if err := s.loadToolLoops(context.Background()); err != nil {
		s.logf("tool loop: load: %v", err)
		return
	}
	s.work.mu.Lock()
	if len(s.work.loops) == 0 {
		s.work.mu.Unlock()
		return
	}
	byID := make(map[string]proto.Session, len(d.Sessions))
	for _, c := range d.Sessions {
		byID[c.SessionID] = c
	}
	changed, stateChanged := false, false
	for k, st := range s.work.loops {
		if k[0] != d.MachineID || st.since.IsZero() {
			continue
		}
		c, ok := byID[k[1]]
		if ok && matchesToolLoop(c, proto.ToolLoop{Tool: st.tool, Since: st.since}) {
			continue
		}
		// A temporarily missing candidate or session is not proof that a loop
		// ended; nodes may still be reconstructing their transcripts.
		if !ok || len(c.Queued) == 0 && (c.Status == proto.StatusOffline || c.ToolLoop == nil && c.Status == proto.StatusWorking) {
			continue
		}
		if st.silenced && (c.ToolLoop == nil || len(c.Queued) != 0 || c.Status != proto.StatusWorking) {
			continue
		}
		if st.cardID != "" {
			delete(s.work.cards, st.cardID)
			changed = true
		}
		stateChanged = true
		if ok && len(c.Queued) != 0 {
			s.work.loops[k] = &toolLoopState{since: st.since, updatedAt: time.Now().UTC(), tool: st.tool, checks: st.checks, silenced: true}
		} else {
			s.work.loops[k] = &toolLoopState{updatedAt: time.Now().UTC(), checks: st.checks}
		}
	}
	s.work.mu.Unlock()
	if stateChanged {
		if err := s.saveToolLoops(context.Background()); err != nil {
			s.logf("tool loop: save: %v", err)
		}
	}
	if changed {
		s.publishSoon()
	}
}

func (s *Service) checkToolLoops(ctx context.Context) {
	if err := s.loadToolLoops(ctx); err != nil {
		s.logf("tool loop: load: %v", err)
		return
	}
	// Retry invalidations which could not be saved on an earlier event.
	s.work.mu.Lock()
	hasLoops := len(s.work.loops) != 0 || s.work.loopSaved
	s.work.mu.Unlock()
	if hasLoops {
		if err := s.saveToolLoops(ctx); err != nil {
			s.logf("tool loop: save: %v", err)
			return
		}
	}
	s.work.mu.Lock()
	var expired []string
	for _, st := range s.work.loops {
		if card, ok := s.work.cards[st.cardID]; ok && !st.answering && !time.Now().Before(card.ExpiresAt) {
			expired = append(expired, st.cardID)
			delete(s.work.cards, st.cardID)
			st.cardID, st.silenced = "", true
		}
	}
	s.work.mu.Unlock()
	if len(expired) > 0 {
		if err := s.saveToolLoops(ctx); err != nil {
			s.logf("tool loop: save expired cards: %v", err)
			return
		}
		for _, id := range expired {
			s.recordEnd(id, "expired", nil)
		}
		s.publishSoon()
	}
	sw := s.triageSwitches(ctx)
	if !sw.Enabled || !sw.ToolLoop {
		return
	}
	v := s.newWorkView(ctx)
	seen := map[[2]string]bool{}
	for _, d := range v.machines() {
		if !d.Reachable {
			continue
		}
		for _, c := range v.sessions(d.ID) {
			k := [2]string{d.ID, c.SessionID}
			seen[k] = true
			if len(c.Queued) != 0 {
				s.clearToolLoop(k, loopUserInput)
				continue
			}
			if c.Status == proto.StatusOffline || c.Status == proto.StatusWorking && c.ToolLoop == nil {
				continue // the node may still be reconstructing its transcript
			}
			if !eligibleToolLoop(c) || v.isMuted(d.ID, c.SessionID) {
				s.clearToolLoop(k, loopCandidateGone)
				continue
			}
			s.considerToolLoop(ctx, d.ID, c)
		}
	}
	// Only a successful list can prove a session is gone; keep cards while a machine is unreachable.
	s.work.mu.Lock()
	removed, pruned := false, false
	for k, st := range s.work.loops {
		if d, ok := v.devices[k[0]]; ok && d.Reachable && !v.failed[k[0]] && !seen[k] &&
			!st.updatedAt.IsZero() && time.Since(st.updatedAt) > workKeep {
			delete(s.work.cards, st.cardID)
			delete(s.work.loops, k)
			pruned = true
			removed = removed || st.cardID != ""
		}
	}
	s.work.mu.Unlock()
	if pruned {
		if err := s.saveToolLoops(ctx); err != nil {
			s.logf("tool loop: save: %v", err)
		}
	}
	if removed {
		s.publishSoon()
	}
}

func (s *Service) considerToolLoop(ctx context.Context, machine string, c proto.Session) {
	if err := s.loadToolLoops(ctx); err != nil {
		s.logf("tool loop: load: %v", err)
		return
	}
	loop := *c.ToolLoop
	k := [2]string{machine, c.SessionID}
	withdrawn := false
	defer func() {
		if withdrawn {
			if err := s.saveToolLoops(context.Background()); err != nil {
				s.logf("tool loop: save: %v", err)
			}
			s.publishSoon()
		}
	}()
	s.work.mu.Lock()
	s.work.init()
	st := s.work.loops[k]
	if st == nil || !st.since.Equal(loop.Since) || st.tool != loop.Tool {
		checks := 0
		if st != nil {
			checks = st.checks
			withdrawn = st.cardID != ""
			delete(s.work.cards, st.cardID)
		}
		st = &toolLoopState{since: loop.Since, updatedAt: time.Now().UTC(), tool: loop.Tool, checks: checks}
		s.work.loops[k] = st
	}
	if st.answering {
		s.work.mu.Unlock()
		return
	}
	if st.cardID != "" {
		if card, ok := s.work.cards[st.cardID]; ok && !time.Now().Before(card.ExpiresAt) {
			delete(s.work.cards, st.cardID)
			st.cardID, st.silenced, withdrawn = "", true, true
		}
	}
	if st.cardID != "" || st.pendingCard != nil || st.silenced || st.pending || loop.Count <= st.checkedCount {
		s.work.mu.Unlock()
		return
	}
	hard := loop.Count >= toolLoopCeiling
	if !hard && (st.checks >= toolLoopMaxChecks || st.checkedCount > 0 && loop.Count < st.checkedCount+3) {
		s.work.mu.Unlock()
		return
	}
	previousChecks, previousCount := st.checks, st.checkedCount
	st.pending = true
	if !hard {
		st.checkedCount = loop.Count
		st.checks++
	}
	s.work.mu.Unlock()
	// Commit the budget before a model call; a restart must not reset it.
	if err := s.saveToolLoops(ctx); err != nil {
		s.work.mu.Lock()
		if s.work.loops[k] == st && st.pending {
			st.pending = false
			st.checks, st.checkedCount = previousChecks, previousCount
		}
		s.work.mu.Unlock()
		s.logf("tool loop: save budget: %v", err)
		return
	}
	go s.reviewToolLoop(context.WithoutCancel(ctx), k, st, loop, hard)
}

func (s *Service) reviewToolLoop(ctx context.Context, k [2]string, st *toolLoopState, loop proto.ToolLoop, hard bool) {
	stop := hard
	if !hard {
		// A tool name can come from a runtime. Bound and sanitize it before giving it to the model.
		name := strings.Map(func(r rune) rune {
			if unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("_.-", r) {
				return r
			}
			return -1
		}, loop.Tool)
		name = clip(name, 64)
		input, _ := json.Marshal(struct {
			Tool  string `json:"tool"`
			Count int    `json:"count"`
		}{name, min(loop.Count, toolLoopCeiling)})
		var out struct {
			Decision string `json:"decision"`
		}
		mctx, cancel := context.WithTimeout(ctx, toolLoopTimeout)
		err := s.purposeModel("agent.triage").JSON(mctx, prompts.RenderAgentToolLoop(), string(input), &out)
		cancel()
		stop = err == nil && strings.TrimSpace(out.Decision) == "STOP"
	}
	if !stop {
		s.work.mu.Lock()
		if s.work.loops[k] == st {
			st.pending = false
		}
		s.work.mu.Unlock()
		return
	}
	c, ok := s.session(ctx, k[0], k[1])
	sw := s.triageSwitches(ctx)
	if !ok || !sw.Enabled || !sw.ToolLoop || !matchesToolLoop(c, loop) {
		s.work.mu.Lock()
		if s.work.loops[k] == st {
			st.pending = false
		}
		s.work.mu.Unlock()
		return
	}
	l := s.lang(ctx)
	question := workT(l, "tool_loop_question")
	now := time.Now().UTC()
	ix := proto.Interaction{ID: toolLoopCardPrefix + randomHex(6), MachineID: k[0], SessionID: k[1], SessionTitle: agentLabel(c), Cwd: c.Cwd,
		Kind: proto.IxQuestion, ToolName: toolLoopCardTool, Summary: question, CanCancel: true, CreatedAt: now, ExpiresAt: now.Add(workCardTTL),
		Questions: []proto.Question{{Question: question, Options: []proto.QuestionOption{{Label: workT(l, "tool_loop_interrupt")}, {Label: workT(l, "tool_loop_continue")}}}}}
	s.work.mu.Lock()
	if s.work.loops[k] != st || !st.pending {
		s.work.mu.Unlock()
		return
	}
	st.pendingCard = &ix
	st.pendingSaved, st.pendingPriorSilenced = false, false
	st.updatedAt = time.Now().UTC()
	s.work.mu.Unlock()
	if err := s.saveToolLoops(ctx, k); err != nil {
		s.work.mu.Lock()
		if s.work.loops[k] == st && st.pendingCard == &ix {
			st.pendingCard, st.pending = nil, false
			if hard {
				st.checkedCount = 0 // retry the ceiling when storage returns
			}
		}
		s.work.mu.Unlock()
		s.logf("tool loop: save card: %v", err)
		return
	}
	s.work.mu.Lock()
	if s.work.loops[k] != st || !st.pending || st.pendingCard != &ix {
		s.work.mu.Unlock()
		return
	}
	st.pending, st.pendingCard, st.cardID = false, nil, ix.ID
	s.work.cards[ix.ID] = ix
	s.work.mu.Unlock()
	s.publishSoon()
}

func eligibleToolLoop(c proto.Session) bool {
	return c.Status == proto.StatusWorking && !c.Assistant && c.ReadOnlyReason == "" && len(c.Queued) == 0 && c.ToolLoop != nil && !c.ToolLoop.Since.IsZero()
}

func matchesToolLoop(c proto.Session, loop proto.ToolLoop) bool {
	return eligibleToolLoop(c) && c.ToolLoop.Since.Equal(loop.Since) && c.ToolLoop.Tool == loop.Tool
}

func (s *Service) toolLoopAnswer(ctx context.Context, id string, r proto.Resolution) (bool, error) {
	if !strings.HasPrefix(id, toolLoopCardPrefix) {
		return false, nil
	}
	if r.Cancel && !r.IsQuestionCancel() {
		return true, errInvalidQuestionCancel
	}
	if err := s.loadToolLoops(ctx); err != nil {
		return true, errors.New("the tool loop state could not be read yet")
	}
	s.work.mu.Lock()
	ix, ok := s.work.cards[id]
	k := [2]string{ix.MachineID, ix.SessionID}
	st := s.work.loops[k]
	if !ok || st == nil || st.cardID != id {
		s.work.mu.Unlock()
		return true, errGone(id)
	}
	if st.answering {
		s.work.mu.Unlock()
		return true, errors.New("the loop card answer is already being processed")
	}
	expired := !time.Now().Before(ix.ExpiresAt)
	answer := strings.TrimSpace(r.Answers[ix.Questions[0].Question])
	interrupt := !expired && !r.Cancel && !r.Dismiss && answer == ix.Questions[0].Options[0].Label
	if !expired && !r.Cancel && !r.Dismiss && !interrupt && answer != ix.Questions[0].Options[1].Label {
		s.work.mu.Unlock()
		return true, errGone(id)
	}
	loop := proto.ToolLoop{Tool: st.tool, Since: st.since}
	st.answering, st.consuming, st.silenced = true, true, true
	st.consumedSaved = false
	st.updatedAt = time.Now().UTC()
	s.work.mu.Unlock()
	// Persist the terminal decision before acknowledging it or sending an
	// interrupt. If delivery is uncertain after a crash, do not send twice.
	if err := s.saveToolLoops(ctx, k); err != nil {
		s.work.mu.Lock()
		if s.work.loops[k] == st && st.cardID == id && st.consuming {
			st.answering, st.consuming, st.silenced = false, false, false
		}
		s.work.mu.Unlock()
		return true, err
	}
	s.work.mu.Lock()
	valid := s.work.loops[k] == st && st.cardID == id && st.answering && st.consuming
	if valid && !interrupt {
		delete(s.work.cards, id)
		st.cardID, st.answering, st.consuming = "", false, false
	}
	s.work.mu.Unlock()
	if !valid {
		return true, nil
	}
	if !interrupt {
		s.publishSoon()
		if expired {
			return true, errGone(id)
		}
		return true, nil
	}

	// A failed request may have reached the node. Replace the card under a new id so an automatic retry of this
	// answer cannot interrupt twice; a fresh user decision can retry after the session has been checked again.
	finish := func(retry bool) {
		s.work.mu.Lock()
		if s.work.loops[k] != st || st.cardID != id {
			s.work.mu.Unlock()
			return
		}
		delete(s.work.cards, id)
		st.cardID, st.answering, st.consuming = "", false, false
		if retry {
			ix.ID, ix.CreatedAt = toolLoopCardPrefix+randomHex(6), time.Now().UTC()
			st.pendingCard = &ix
			st.pendingSaved, st.pendingPriorSilenced = false, true
			st.silenced = false
		}
		s.work.mu.Unlock()
		if retry {
			if err := s.saveToolLoops(context.Background(), k); err != nil {
				s.work.mu.Lock()
				if s.work.loops[k] == st && st.pendingCard == &ix {
					st.pendingCard, st.silenced = nil, true
				}
				s.work.mu.Unlock()
				s.logf("tool loop: save retry card: %v", err)
			} else {
				s.work.mu.Lock()
				if s.work.loops[k] == st && st.pendingCard == &ix {
					st.pendingCard, st.cardID = nil, ix.ID
					s.work.cards[ix.ID] = ix
				}
				s.work.mu.Unlock()
			}
		}
		s.publishSoon()
	}
	c, ok := s.session(ctx, k[0], k[1])
	if !ok {
		finish(true)
		return true, nil
	}
	sw := s.triageSwitches(ctx)
	if sw.Enabled && sw.ToolLoop && len(c.Queued) == 0 && (c.Status == proto.StatusOffline || c.Status == proto.StatusWorking && c.ToolLoop == nil) {
		finish(true) // the node may still be reconstructing its transcript
		return true, nil
	}
	if !sw.Enabled || !sw.ToolLoop || !matchesToolLoop(c, loop) {
		finish(false)
		return true, nil
	}
	s.work.mu.Lock()
	valid = s.work.loops[k] == st && st.cardID == id && st.answering && st.consuming
	s.work.mu.Unlock()
	if !valid {
		return true, nil
	}
	if err := s.call(ctx, k[0], "session.interrupt", map[string]any{"session_id": k[1]}, nil); err != nil {
		s.logf("tool loop %s/%s: interrupt failed: %v", k[0], short(k[1]), err)
		latest, known := s.session(ctx, k[0], k[1])
		finish(!known || matchesToolLoop(latest, loop))
		return true, nil
	}
	finish(false)
	return true, nil
}
