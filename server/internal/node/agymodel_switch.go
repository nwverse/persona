package node

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// Switching the model or reasoning effort of a running Antigravity CLI (agy) session in a herdr pane (#13754):
// agy's TUI accepts `/model <id>` and `/effort <level>` directly through herdr's agent.prompt, printing a confirmation
// ("Model set to …" / "Model already set to …", "Effort set to …" / "Effort already set to …") or a refusal above its
// composer rule. Permission modes on an agy pane remain fixed at start.

func agyModelEffort(model string) string {
	trimmed := strings.TrimSpace(model)
	if trimmed == "" {
		return ""
	}
	base := proto.TrimModelWindow(trimmed)
	lower := strings.ToLower(base)
	for _, level := range proto.EffortLevels {
		if strings.HasSuffix(lower, "-"+level) && len(base) > len(level)+1 {
			return level
		}
	}
	return ""
}

func agyModelWithEffort(model, effort string) string {
	effort = strings.TrimSpace(effort)
	if effort == "" {
		return model
	}
	base := trimAgyEffortSuffix(model)
	if base == "" {
		return model
	}
	window := ""
	trimmed := strings.TrimSpace(model)
	if without := proto.TrimModelWindow(trimmed); len(without) < len(trimmed) {
		window = trimmed[len(without):]
	}
	return base + "-" + effort + window
}

func agyModelCommand(model string) (string, error) {
	model = strings.TrimSpace(model)
	if !proto.ValidModelID(model) {
		return "", fmt.Errorf("invalid model %q", model)
	}
	return "/model " + model, nil
}

func agyEffortCommand(effort string) (string, error) {
	effort = strings.TrimSpace(effort)
	if !slices.Contains(proto.EffortLevels, effort) {
		return "", fmt.Errorf("unknown effort level %q", effort)
	}
	return "/effort " + effort, nil
}

// agyScrollbackLines returns the non-empty trimmed lines above agy's bottom input box (`──` rules around `>`).
func agyScrollbackLines(screen string) []string {
	raw := strings.Split(screen, "\n")
	cut := len(raw)
	end := -1
	for i := len(raw) - 1; i >= 0; i-- {
		if strings.HasPrefix(strings.TrimSpace(raw[i]), "──") {
			end = i
			break
		}
	}
	if end >= 1 {
		for i := end - 1; i >= 0; i-- {
			if strings.HasPrefix(strings.TrimSpace(raw[i]), "──") {
				if i+1 < end {
					prompt := strings.TrimSpace(raw[i+1])
					if strings.HasPrefix(prompt, ">") || strings.HasPrefix(prompt, "❯") {
						cut = i
					}
				}
				break
			}
		}
	}
	out := make([]string, 0, cut)
	for _, l := range raw[:cut] {
		if s := strings.Join(strings.Fields(l), " "); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func isAgyCommandPrompt(line, slash string) bool {
	for _, prefix := range []string{"> " + slash, "❯ " + slash, slash} {
		if line == prefix || strings.HasPrefix(line, prefix+" ") {
			return true
		}
	}
	return false
}

// agyNewCommandBlock isolates the output printed by the newest `slash` command (`/model` or `/effort`) in `after`
// relative to `before`, so an earlier switch still visible in scrollback cannot satisfy the next one prematurely.
func agyNewCommandBlock(before, after, slash string) string {
	prev := agyScrollbackLines(before)
	cur := agyScrollbackLines(after)
	if len(cur) == 0 || slices.Equal(prev, cur) {
		return ""
	}
	lastPrompt := -1
	for i := len(cur) - 1; i >= 0; i-- {
		if isAgyCommandPrompt(cur[i], slash) {
			lastPrompt = i
			break
		}
	}
	if lastPrompt < 0 {
		return ""
	}
	block := cur[lastPrompt:]
	// If `before` ended with the exact same command block and the context immediately above it did not advance,
	// `cur[lastPrompt:]` is still the previous command's output.
	prevPrompt := -1
	for i := len(prev) - 1; i >= 0; i-- {
		if isAgyCommandPrompt(prev[i], slash) {
			prevPrompt = i
			break
		}
	}
	if prevPrompt >= 0 && slices.Equal(prev[prevPrompt:], block) {
		above := min(3, min(prevPrompt, lastPrompt))
		if above == 0 && prevPrompt == lastPrompt || above > 0 && slices.Equal(prev[prevPrompt-above:prevPrompt], cur[lastPrompt-above:lastPrompt]) {
			return ""
		}
	}
	return strings.Join(block, " ")
}

func agyPickerOpen(screen string) bool {
	if modelPicker(screen) {
		return true
	}
	lower := strings.ToLower(screen)
	return strings.Contains(lower, "navigate") && strings.Contains(lower, "enter select")
}

func agyModelOutcome(before, after, model string) error {
	block := agyNewCommandBlock(before, after, "/model")
	if block == "" {
		return modelUnconfirmed{model}
	}
	if strings.Contains(block, "Model set to ") || strings.Contains(block, "Model already set to ") {
		return nil
	}
	for _, bad := range []string{"Invalid model", "invalid model", "Unknown model", "not found", "not available", "does not exist", "No models available"} {
		if strings.Contains(block, bad) {
			return fmt.Errorf("Antigravity 没接受这个模型（%s），请在终端里确认", model)
		}
	}
	return modelUnconfirmed{model}
}

type effortUnconfirmed struct{ effort string }

func (e effortUnconfirmed) Error() string {
	return fmt.Sprintf("思考强度可能没改成 %s，请在终端里确认", e.effort)
}

func agyEffortOutcome(before, after, effort string) error {
	block := agyNewCommandBlock(before, after, "/effort")
	if block == "" {
		return effortUnconfirmed{effort}
	}
	if strings.Contains(block, "Effort set to ") || strings.Contains(block, "Effort already set to ") {
		return nil
	}
	for _, bad := range []string{"Invalid effort", "invalid effort", "has no \"", "isn't adjustable", "not adjustable", "not available"} {
		if strings.Contains(block, bad) {
			return fmt.Errorf("Antigravity 没接受这个思考强度（%s），请在终端里确认", effort)
		}
	}
	return effortUnconfirmed{effort}
}

func (n *Node) agyObservedModel(id, pane string) string {
	if conversation := n.agyConversation(id, pane); conversation != "" {
		if runtimeRow, _, ok := n.runtimes.rowOf(agyAgent + "-" + conversation); ok && runtimeRow.Model != "" {
			return runtimeRow.Model
		}
		if u, ok := n.agyTranscriptMeta(conversation); ok && u.model != "" {
			return u.model
		}
	}
	return ""
}

func (n *Node) agyCurrentModel(id, pane string) string {
	observed := n.agyObservedModel(id, pane)
	var observedAt time.Time
	if conversation := n.agyConversation(id, pane); conversation != "" {
		if u, ok := n.agyTranscriptMeta(conversation); ok {
			observedAt = u.modelAt
		}
	}
	s := proto.Session{SessionID: id, Model: observed}
	n.applyAgyPaneModes(&s, observedAt)
	return s.Model
}

func (n *Node) noteAgyModel(id, model, from, effort string) {
	now := time.Now()
	n.mu.Lock()
	m := n.modes[id]
	m.Model, m.Awaited = &pendingModel{Model: model, From: from, At: now}, nil
	m.Effort, m.EffortAt = effort, now
	n.modes[id] = m
	n.mu.Unlock()
}

func (n *Node) noteAgyEffort(id, effort string) {
	now := time.Now()
	n.mu.Lock()
	m := n.modes[id]
	m.Effort, m.EffortAt = effort, now
	if m.Model != nil && effort != "" {
		m.Model = &pendingModel{Model: agyModelWithEffort(m.Model.Model, effort), From: m.Model.From, At: now}
	}
	n.modes[id] = m
	n.mu.Unlock()
}

func (n *Node) persistAgyPaneModel(ctx context.Context, id, pane, model, effort string) {
	if n.state == nil || id == "" {
		return
	}
	st, ok := n.state.get(id)
	if !ok {
		return
	}
	changed := false
	if model != "" && st.Model != model {
		st.Model = model
		changed = true
	}
	if st.ModelEffort != effort {
		st.ModelEffort = effort
		changed = true
	}
	if !changed {
		return
	}
	n.state.put(id, st)
	if launch, ok := n.hosts.Get(host.ParsePane(pane).Kind).(host.Launcher); ok {
		n.mu.Lock()
		spec, hasSpec := n.launches[pane]
		n.mu.Unlock()
		if !hasSpec || spec.Agent != agyAgent {
			spec = agentLaunch{
				Agent:    agyAgent,
				Launcher: cmp.Or(st.Launcher, agyAgent),
				Mode:     st.AppMode,
				ID:       id,
			}
		}
		spec.Model = st.Model
		spec.Effort = st.ModelEffort
		n.holdResume(ctx, launch, pane, id, spec)
	}
}

func (n *Node) applyAgyPaneModes(s *proto.Session, transcriptModelAt time.Time) {
	if s == nil {
		return
	}
	observed := s.Model
	n.mu.Lock()
	m, ok := n.modes[s.SessionID]
	n.mu.Unlock()
	fresh := func(at time.Time) bool {
		return !at.IsZero() && time.Since(at) <= pendingModelTTL && (transcriptModelAt.IsZero() || !transcriptModelAt.After(at))
	}
	appliedModel := false
	if ok && m.Model != nil && fresh(m.Model.At) && observed == m.Model.From {
		s.Model = m.Model.Model
		s.Effort = agyModelEffort(m.Model.Model)
		appliedModel = true
	}
	if !appliedModel {
		if s.Model == "" && n.state != nil {
			if st, hasState := n.state.get(s.SessionID); hasState && st.Model != "" {
				s.Model = st.Model
				if s.Effort == "" {
					s.Effort = cmp.Or(st.ModelEffort, agyModelEffort(st.Model))
				}
			}
		}
		if s.Effort == "" && s.Model != "" {
			s.Effort = agyModelEffort(s.Model)
		}
	}
	if ok && fresh(m.EffortAt) {
		s.Effort = m.Effort
		if m.Effort != "" && s.Model != "" {
			s.Model = agyModelWithEffort(s.Model, m.Effort)
		}
	}
}

func (n *Node) applyAgyPaneInfo(id, conversation string, info map[string]any) {
	var transcriptModelAt time.Time
	if conversation != "" {
		if u, ok := n.agyTranscriptMeta(conversation); ok {
			transcriptModelAt = u.modelAt
		}
	}
	model, _ := info["model"].(string)
	effort, _ := info["effort"].(string)
	// Do not treat st.Model seeded earlier into info["model"] as a transcript-observed model when matching m.Model.From.
	observed := ""
	if conversation != "" {
		if runtimeRow, _, ok := n.runtimes.rowOf(agyAgent + "-" + conversation); ok && runtimeRow.Model != "" {
			observed = runtimeRow.Model
		} else if u, ok := n.agyTranscriptMeta(conversation); ok && u.model != "" {
			observed = u.model
		}
	}
	row := proto.Session{SessionID: id, Model: observed, Effort: effort}
	n.applyAgyPaneModes(&row, transcriptModelAt)
	if row.Model == "" {
		row.Model = model
	}
	if row.Model != "" {
		info["model"] = row.Model
	}
	if row.Effort != "" {
		info["effort"] = row.Effort
	} else {
		delete(info, "effort")
	}
}

func (n *Node) agyPaneModesCall(ctx context.Context, id string, a host.Agent, raw json.RawMessage) (any, error) {
	var p struct {
		PermissionMode string `json:"permission_mode"`
		Effort         string `json:"effort"`
		Model          string `json:"model"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	model, mode, effort := strings.TrimSpace(p.Model), strings.TrimSpace(p.PermissionMode), strings.TrimSpace(p.Effort)
	if mode != "" || (model == "" && effort == "") {
		return nil, refusal(proto.CodeModesUnsupported, i18n.T(i18n.Default, "rpc-errors.modes_unsupported"), nil)
	}
	var modelCmd, effortCmd string
	var err error
	if model != "" {
		if modelCmd, err = agyModelCommand(model); err != nil {
			return nil, err
		}
	}
	if effort != "" {
		if effortCmd, err = agyEffortCommand(effort); err != nil {
			return nil, err
		}
	}
	if err := n.switchingRefusal(id); err != nil {
		return nil, err
	}
	defer n.typing.lock(id)()
	live, ok := n.agent(id)
	if !ok || live.Agent != agyAgent || live.PaneID == "" || agyPaneStopped(live.AgentStatus) {
		return nil, n.noPaneForModes(id)
	}
	status := n.workAgentStatus(live, time.Now())
	if status == proto.StatusWorking || status == proto.StatusBlocked {
		return nil, fmt.Errorf("Agent 正在工作，这一轮结束后再切换模型")
	}
	if err := n.checkPane(ctx, live.PaneID, agyAgent, ""); err != nil {
		return nil, err
	}
	pane := live.PaneID
	observed := n.agyObservedModel(id, pane)
	activeModel := n.agyCurrentModel(id, pane)
	if model != "" {
		before, _ := n.readPane(ctx, pane, "visible", 60)
		if err := n.hostPrompt(ctx, pane, modelCmd); err != nil {
			if r := deliveryRefusal(err); r != nil {
				return nil, r
			}
			return nil, uncertain(err)
		}
		n.typing.typed(pane, modelCmd)
		if err := n.confirmAgyCommand(ctx, pane, before, func(s string) error { return agyModelOutcome(before, s, model) }); err != nil {
			return nil, err
		}
		inferredEffort := agyModelEffort(model)
		n.noteAgyModel(id, model, observed, inferredEffort)
		n.persistAgyPaneModel(ctx, id, pane, model, inferredEffort)
		activeModel = model
	}
	if effort != "" {
		// An Antigravity variant such as `gemini-4-carbon-high` or `gpt-oss-120b-medium` already selects its own
		// thinking level when `/model` runs, and single-variant models reject a separate `/effort` command.
		if agyModelEffort(activeModel) == effort {
			n.noteAgyEffort(id, effort)
			n.persistAgyPaneModel(ctx, id, pane, activeModel, effort)
		} else {
			before, _ := n.readPane(ctx, pane, "visible", 60)
			if err := n.hostPrompt(ctx, pane, effortCmd); err != nil {
				if r := deliveryRefusal(err); r != nil {
					return nil, r
				}
				return nil, uncertain(err)
			}
			n.typing.typed(pane, effortCmd)
			if err := n.confirmAgyCommand(ctx, pane, before, func(s string) error { return agyEffortOutcome(before, s, effort) }); err != nil {
				return nil, err
			}
			nextModel := agyModelWithEffort(activeModel, effort)
			if model == "" && nextModel != activeModel {
				n.noteAgyModel(id, nextModel, observed, effort)
			} else {
				n.noteAgyEffort(id, effort)
			}
			n.persistAgyPaneModel(ctx, id, pane, nextModel, effort)
		}
	}
	n.notifySessions()
	return okResult(nil), nil
}

func agyPaneStopped(status string) bool {
	return status == proto.StatusOffline || status == proto.StatusInterrupted
}

func (n *Node) confirmAgyCommand(ctx context.Context, pane, before string, check func(string) error) error {
	last := before
	for range 15 {
		time.Sleep(modelLook)
		s, err := n.readPane(ctx, pane, "visible", 60)
		if err != nil {
			continue
		}
		last = s
		if agyPickerOpen(s) && !agyPickerOpen(before) {
			_ = n.sendKeys(ctx, pane, "escape")
			return fmt.Errorf("Antigravity 打开了选择器而没有直接切换，已取消")
		}
		if err := check(s); err == nil {
			return nil
		} else if !errors.As(err, &modelUnconfirmed{}) && !errors.As(err, &effortUnconfirmed{}) {
			return err
		}
	}
	return check(last)
}
