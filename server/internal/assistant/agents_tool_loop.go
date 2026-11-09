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
	since                time.Time
	tool                 string
	cardID               string
	checks, checkedCount int
	pending, silenced    bool
	answering            bool
}

// clearToolLoop withdraws the card and invalidates any model call. The turn-start event resets its budget.
func (s *Service) clearToolLoop(k [2]string, newTurn bool) {
	s.work.mu.Lock()
	s.work.init()
	state := s.work.loops[k]
	changed := state != nil && state.cardID != ""
	if state != nil {
		delete(s.work.cards, state.cardID)
		if newTurn {
			delete(s.work.loops, k)
		} else {
			s.work.loops[k] = &toolLoopState{checks: state.checks}
		}
	}
	s.work.mu.Unlock()
	if changed {
		s.publishSoon()
	}
}

// toolLoopsChanged invalidates cards and model calls as soon as a node reports a changed candidate.
// The event is a complete list for one machine; its turn budget survives a candidate change.
func (s *Service) toolLoopsChanged(d DeviceSessions) {
	byID := make(map[string]proto.Session, len(d.Sessions))
	for _, c := range d.Sessions {
		byID[c.SessionID] = c
	}
	s.work.mu.Lock()
	changed := false
	for k, st := range s.work.loops {
		if k[0] != d.MachineID || st.since.IsZero() {
			continue
		}
		c, ok := byID[k[1]]
		if ok && matchesToolLoop(c, proto.ToolLoop{Tool: st.tool, Since: st.since}) {
			continue
		}
		if st.cardID != "" {
			delete(s.work.cards, st.cardID)
			changed = true
		}
		s.work.loops[k] = &toolLoopState{checks: st.checks}
	}
	s.work.mu.Unlock()
	if changed {
		s.publishSoon()
	}
}

func (s *Service) checkToolLoops(ctx context.Context) {
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
			if c.Assistant || c.Status != proto.StatusWorking || c.ReadOnlyReason != "" || len(c.Queued) != 0 || c.ToolLoop == nil || c.ToolLoop.Since.IsZero() || v.isMuted(d.ID, c.SessionID) {
				s.clearToolLoop(k, false)
				continue
			}
			s.considerToolLoop(ctx, d.ID, c)
		}
	}
	// Only a successful list can prove a session is gone; keep cards while a machine is unreachable.
	s.work.mu.Lock()
	var missing [][2]string
	for k := range s.work.loops {
		if d, ok := v.devices[k[0]]; ok && d.Reachable && !v.failed[k[0]] && !seen[k] {
			missing = append(missing, k)
		}
	}
	s.work.mu.Unlock()
	for _, k := range missing {
		s.clearToolLoop(k, true)
	}
}

func (s *Service) considerToolLoop(ctx context.Context, machine string, c proto.Session) {
	loop := *c.ToolLoop
	k := [2]string{machine, c.SessionID}
	withdrawn := false
	defer func() {
		if withdrawn {
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
		st = &toolLoopState{since: loop.Since, tool: loop.Tool, checks: checks}
		s.work.loops[k] = st
	}
	if st.cardID != "" {
		if card, ok := s.work.cards[st.cardID]; ok && !time.Now().Before(card.ExpiresAt) {
			delete(s.work.cards, st.cardID)
			st.cardID, st.silenced, withdrawn = "", true, true
		}
	}
	if st.cardID != "" || st.silenced || st.pending || loop.Count <= st.checkedCount {
		s.work.mu.Unlock()
		return
	}
	hard := loop.Count >= toolLoopCeiling
	if !hard && (st.checks >= toolLoopMaxChecks || st.checkedCount > 0 && loop.Count < st.checkedCount+3) {
		s.work.mu.Unlock()
		return
	}
	st.pending = true
	st.checkedCount = loop.Count
	if !hard {
		st.checks++
	}
	s.work.mu.Unlock()
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
	st.pending, st.cardID = false, ix.ID
	s.work.cards[ix.ID] = ix
	s.work.mu.Unlock()
	s.publishSoon()
}

func matchesToolLoop(c proto.Session, loop proto.ToolLoop) bool {
	return c.Status == proto.StatusWorking && !c.Assistant && c.ReadOnlyReason == "" && len(c.Queued) == 0 && c.ToolLoop != nil && c.ToolLoop.Since.Equal(loop.Since) && c.ToolLoop.Tool == loop.Tool
}

func (s *Service) toolLoopAnswer(ctx context.Context, id string, r proto.Resolution) (bool, error) {
	if !strings.HasPrefix(id, toolLoopCardPrefix) {
		return false, nil
	}
	if r.Cancel && !r.IsQuestionCancel() {
		return true, errInvalidQuestionCancel
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
	if !time.Now().Before(ix.ExpiresAt) {
		delete(s.work.cards, id)
		st.cardID, st.silenced = "", true
		s.work.mu.Unlock()
		s.publishSoon()
		return true, errGone(id)
	}
	answer := strings.TrimSpace(r.Answers[ix.Questions[0].Question])
	interrupt := !r.Cancel && !r.Dismiss && answer == ix.Questions[0].Options[0].Label
	if !r.Cancel && !r.Dismiss && !interrupt && answer != ix.Questions[0].Options[1].Label {
		s.work.mu.Unlock()
		return true, errGone(id)
	}
	if !interrupt {
		delete(s.work.cards, id)
		st.cardID, st.silenced = "", true
		s.work.mu.Unlock()
		s.publishSoon()
		return true, nil
	}
	loop := proto.ToolLoop{Tool: st.tool, Since: st.since}
	st.answering = true
	s.work.mu.Unlock()

	// A failed request may have reached the node. Replace the card under a new id so an automatic retry of this
	// answer cannot interrupt twice; a fresh user decision can retry after the session has been checked again.
	finish := func(retry bool) {
		s.work.mu.Lock()
		if s.work.loops[k] != st || st.cardID != id {
			s.work.mu.Unlock()
			return
		}
		delete(s.work.cards, id)
		st.answering = false
		if retry {
			ix.ID, ix.CreatedAt = toolLoopCardPrefix+randomHex(6), time.Now().UTC()
			st.cardID = ix.ID
			s.work.cards[ix.ID] = ix
		} else {
			st.cardID, st.silenced = "", true
		}
		s.work.mu.Unlock()
		s.publishSoon()
	}
	c, ok := s.session(ctx, k[0], k[1])
	if !ok {
		finish(true)
		return true, nil
	}
	sw := s.triageSwitches(ctx)
	if !sw.Enabled || !sw.ToolLoop || !matchesToolLoop(c, loop) {
		finish(false)
		return true, nil
	}
	s.work.mu.Lock()
	valid := s.work.loops[k] == st && st.cardID == id && st.answering
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
