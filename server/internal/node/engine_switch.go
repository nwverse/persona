package node

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/proto"
)

// Switching a running terminal Agent between Claude Code and Codex (#5054, docs/integrations/claude-integration.md
// "Switching a running Agent to another engine", docs/protocol/protocol.md "Switching a running Agent to another
// engine"). It is the launcher switch of launcher_switch.go (#5035) with a launcher of the other engine: picked, it is
// kept as the session's pending switch, now naming the engine it moves to (pending_switch.agent); the user's next
// message from the chat carries it out inside its turn. The agent is quit in its terminal and the other engine started
// in the same shell, with the message behind a handoff of the conversation so far (engine_handoff.go). When the
// conversation ran on that engine before, the session it ran in then (the parked one) is resumed instead, with only
// what happened meanwhile in front of the message; one that cannot be resumed (another profile holds it, something has
// it open, its files are gone) gives way to a fresh session with the whole handoff.
//
// One conversation, one row: the session running it now carries its earlier stretches (Session.engine_history, kept
// in its state record), and the session list leaves out every session named there. They stay on disk, readable and
// resumable, and a switch back resumes the most recent one of that engine.
//
// What fails is rolled back as #5035 does: a parked session that does not come up gives way to a fresh one once; a
// start of the other engine that does not come up has its pane cleared, the agent resumed on its own launcher in the
// same shell (switch_failed), or stopped when even that does not come up (switch_failed_stopped). A failed switch is
// dropped, and its message refused with nothing typed and its words kept by the client.
//
// Only text goes over: a message with photos is refused (switch_unsupported) and the switch waits, rather than the
// photos being lost on the way. Effort is the one picked only (the engines' levels differ); the permission mode goes
// where the other engine has it (plan, and bypassPermissions to Codex).

// engineSwitchBudget bounds a switch to the other engine from the first Ctrl+C on: up to three starts where the
// launcher switch has two (the parked session, a fresh one, the agent back on its own launcher), each within
// switchStartWait, with the quit and the panes cleared between them. With the checks before the quit
// (switchProbeWait) it stays under the hub's wait for a message with apply_switch (switchSendWait, 7 minutes).
const engineSwitchBudget = switchBudget + 30*time.Second

// engineHistoryKey carries the conversation's earlier stretches into the start a switch makes (withEngineHistory): the
// start writes them on the new session's record (control.go, codex_start.go), so the list never shows the session
// started and the one it continues as two rows.
type engineHistoryKey struct{}

func withEngineHistory(ctx context.Context, legs []proto.EngineLeg) context.Context {
	return context.WithValue(ctx, engineHistoryKey{}, legs)
}

// engineHistoryOf is the history withEngineHistory gave a start, ok false when it gave none.
func engineHistoryOf(ctx context.Context) ([]proto.EngineLeg, bool) {
	legs, ok := ctx.Value(engineHistoryKey{}).([]proto.EngineLeg)
	return slices.Clone(legs), ok
}

// agentEngine is the engine a live agent runs: "claude" or "codex", "" for any other.
func agentEngine(a host.Agent) string {
	switch a.Agent {
	case "", "claude":
		return "claude"
	case "codex":
		return "codex"
	}
	return ""
}

// engineOwnLauncher is the launcher session id runs on with engine, and whether that is known: a Claude Code
// session's as ownLauncher says, a Codex session's from its record (Persona started or resumed it); else plain codex,
// not known.
func (n *Node) engineOwnLauncher(id, engine string) (string, bool) {
	if engine != "codex" {
		return n.ownLauncher(id)
	}
	if st, ok := n.state.get(id); ok && st.Launcher != "" {
		return st.Launcher, true
	}
	return "codex", false
}

// engineUnsupported is the refusal of an engine switch for a session that cannot have one, why in the node's words.
func engineUnsupported(why string) error {
	return refusal(proto.CodeSwitchUnsupported, "only a Claude Code or Codex Agent running in a terminal Persona drives can switch to the other engine: "+why, nil)
}

// launcherEngine is the agent launcher runs, for a pick (launcherModes): one this computer has, not a runtime's.
// Checked in this order so no word that is not a launcher is ever run by a probe.
func (n *Node) launcherEngine(ctx context.Context, launcher string) (string, error) {
	if !nameRe.MatchString(launcher) {
		return "", fmt.Errorf("invalid launcher %q", launcher)
	}
	if err := n.ensureManaged(ctx, launcher); err != nil {
		return "", err
	}
	if !n.knownLauncher(ctx, launcher) {
		return "", refusal(proto.CodeSwitchLauncherMissing, fmt.Sprintf("launcher %s is not on this computer", launcher), nil)
	}
	if h, _ := n.startRuntime(launcher); h != nil {
		return "", refusal(proto.CodeSwitchOtherAgent, fmt.Sprintf("launcher %s belongs to a runtime", launcher), nil)
	}
	return n.launcherModelsAgent(ctx, launcher, ""), nil
}

// enginePlan is what carrying out a switch to the other engine needs, all of it worked out before the agent is quit.
type enginePlan struct {
	a   host.Agent
	h   host.Host
	ref host.PaneRef
	// from and to are the engines ("claude", "codex"); own is the launcher the session runs on, the one a rollback
	// resumes it on.
	from, to, own string
	// cwd is the conversation's directory: where a fresh session of the other engine starts.
	cwd string
	// legs are the conversation's earlier stretches, as the session's record has them.
	legs []proto.EngineLeg
	// parked is the earlier session of the other engine that a switch back resumes ("" for a fresh one), its stretch's
	// index in legs and its own directory.
	parked    string
	parkedLeg int
	parkedCwd string
	// model is the model the handoff names: the one picked, else the one the launcher names for itself ("" when
	// neither says). fresh and resume are what the starts run with.
	model         string
	fresh, resume modelExecution
}

// planEngineSwitch checks the switch ps of session id to the engine ps.Agent and works out how to carry it out: the
// session (switchSession), its own launcher, the conversation's directory, the launcher picked and the parked
// session to resume, when there is one that can be.
func (n *Node) planEngineSwitch(ctx context.Context, id string, ps proto.PendingSwitch) (*enginePlan, error) {
	to := ps.Agent
	if to != "claude" && to != "codex" {
		return nil, fmt.Errorf("unknown engine %q", to)
	}
	if ps.Model != "" && !proto.ValidModelID(ps.Model) {
		return nil, fmt.Errorf("invalid model %q", ps.Model)
	}
	if err := validEngineEffort(to, ps.Effort); err != nil {
		return nil, err
	}
	a, h, ref, err := n.switchSession(ctx, id, true)
	if err != nil {
		return nil, err
	}
	p := &enginePlan{a: a, h: h, ref: ref, from: agentEngine(a), to: to, parkedLeg: -1}
	if p.from == to {
		return nil, engineUnsupported("it runs " + engineName(to) + " already")
	}
	own, known := n.engineOwnLauncher(id, p.from)
	if !known {
		return nil, engineUnsupported("Persona cannot tell which launcher it runs on (it did not start it), so it could not bring it back on it")
	}
	if p.from == "codex" {
		if _, ok := n.codexLauncher(ctx, own); !ok {
			return nil, engineUnsupported(fmt.Sprintf("its launcher %s no longer runs Codex, so it could not be brought back on it", own))
		}
	}
	p.own = own
	if p.from == "claude" {
		p.cwd = n.switchCwd(id, a)
	} else {
		p.cwd = n.codexCwd(id)
	}
	if !dirExists(p.cwd) {
		return nil, engineUnsupported("Persona cannot tell which folder its conversation belongs to")
	}
	if err := n.checkEngineLauncher(ctx, ps.Launcher, to, ps.Model); err != nil {
		return nil, err
	}
	if st, ok := n.state.get(id); ok {
		p.legs = slices.Clone(st.EngineHistory)
	}
	p.fresh = modelExecution{Model: ps.Model}
	if to == "codex" && ps.Effort != effortAuto {
		p.fresh.Effort = ps.Effort // -c model_reasoning_effort (control.go): "auto" is the launcher's own
	}
	p.resume = p.fresh
	for k := len(p.legs) - 1; k >= 0; k-- {
		if p.legs[k].Agent != to {
			continue
		}
		parked := p.legs[k].SessionID
		var why string
		if to == "claude" {
			p.parkedCwd, p.resume.Model, why = n.claudeParked(ctx, parked, ps)
		} else {
			p.parkedCwd, why = n.codexParked(ctx, parked, ps.Launcher)
		}
		if why != "" {
			n.logf("session %s: its earlier %s session %s is not resumed (%s): a fresh one takes the conversation", id, engineName(to), parked, why)
		} else {
			p.parked, p.parkedLeg = parked, k
		}
		break // only the most recent stretch of that engine
	}
	p.model = cmp.Or(ps.Model, n.launcherDefaultModel(ps.Launcher))
	return p, nil
}

// validEngineEffort refuses an effort the start of engine to cannot take ("" and "auto": the launcher's own).
func validEngineEffort(to, effort string) error {
	if to == "codex" && effort != "" && effort != effortAuto && !slices.Contains(codexEffortLevels, effort) {
		return fmt.Errorf("unknown effort level %q", effort)
	}
	return validModes("", effort)
}

// codexCwd is the directory Codex session id runs in: its record's, else the one its rollout's session_meta names.
func (n *Node) codexCwd(id string) string {
	if st, ok := n.state.get(id); ok && st.Cwd != "" {
		return st.Cwd
	}
	if src, ok := n.codexSource(id); ok && src.path != "" {
		return metaOf(src.home, src.path).cwd
	}
	if p := n.codexPath(id); p != "" {
		return metaOf(n.codexHome(), p).cwd
	}
	return ""
}

// checkEngineLauncher checks that launcher can start engine to on this computer (with model, when one is given).
func (n *Node) checkEngineLauncher(ctx context.Context, launcher, to, model string) error {
	agent, err := n.launcherEngine(ctx, launcher)
	if err != nil {
		return err
	}
	if agent != to {
		return refusal(proto.CodeSwitchOtherAgent, fmt.Sprintf("launcher %s runs %s, not %s", launcher, agent, engineName(to)), nil)
	}
	if to == "codex" {
		if _, ok := n.codexLauncher(ctx, launcher); !ok {
			// a shell function that runs Codex: codex-start cannot run it without a shell (codex_start.go)
			if err := n.codexFunctionLauncher(ctx, launcher, true); err != nil {
				return engineUnsupported(err.Error())
			}
			return refusal(proto.CodeSwitchOtherAgent, fmt.Sprintf("launcher %s does not run Codex", launcher), nil)
		}
	} else if err := n.codexFunctionLauncher(ctx, launcher, true); err != nil {
		return refusal(proto.CodeSwitchOtherAgent, err.Error(), nil)
	}
	if model != "" {
		if _, err := n.launcherModelCheck(ctx, launcher, model); err != nil {
			return refusal(proto.CodeSwitchModelLocked, fmt.Sprintf("launcher %s would not run model %s: %v", launcher, model, err), nil)
		}
	}
	return nil
}

// launcherDefaultModel is the model launcher names for itself, as its list says ("" when it names none).
func (n *Node) launcherDefaultModel(launcher string) string {
	model := ""
	if l, ok := n.managedLauncher(launcher); ok {
		model = l.Model
	} else {
		launcherCache.Lock()
		for _, l := range launcherCache.list {
			if l.Name == launcher {
				model = l.Model
				break
			}
		}
		launcherCache.Unlock()
	}
	if !proto.ValidModelID(model) {
		return "" // the list's placeholder for a launcher that names no model (launchers.go), not one to show
	}
	return model
}

// claudeParked says whether Claude Code session id, the conversation's earlier stretch, can be resumed through the
// launcher ps picks: its directory, the model to resume with, and why not ("" when it can). It needs its transcript,
// its project's folder, nothing having it open, and a launcher whose config directory holds its newest copy
// (checkSwitchConfigDir); without a model picked, one the launcher runs by default that can be seen, unless the session
// ran on that launcher (--resume brings back the model it ran last, #2487).
func (n *Node) claudeParked(ctx context.Context, id string, ps proto.PendingSwitch) (cwd, model, why string) {
	switch {
	case !validID(id):
		return "", "", "not a Claude Code session"
	case n.transcriptPath(id) == "":
		return "", "", "its transcript is gone"
	}
	if _, live := n.agent(id); live {
		return "", "", "it runs"
	}
	if r := n.openElsewhereRefusal(id); r != nil {
		return "", "", r.Error()
	}
	cwd = n.switchCwd(id, host.Agent{})
	if !dirExists(cwd) {
		return "", "", "its folder cannot be told"
	}
	dir, known := n.launcherClaudeDir(ctx, ps.Launcher)
	if err := n.checkSwitchConfigDir(id, ps.Launcher, dir, known); err != nil {
		return "", "", err.Error()
	}
	model = ps.Model
	if model == "" {
		m, ok := n.switchModel(ps.Launcher, cwd, dir, known)
		st, _ := n.state.get(id)
		if !ok && st.Launcher != ps.Launcher {
			return "", "", "the model launcher " + ps.Launcher + " runs by default cannot be seen"
		}
		model = m
	}
	return cwd, model, ""
}

// codexParked says whether Codex thread id, the conversation's earlier stretch, can be resumed through launcher: its
// directory and why not ("" when it can). Nothing may have it open (codexResumable), and the launcher must run Codex
// in the home the thread is in (codexResumeLauncher).
func (n *Node) codexParked(ctx context.Context, id, launcher string) (cwd, why string) {
	if !n.codexResumable(ctx, id) {
		return "", "something has it open, or it is not a terminal Codex thread"
	}
	if n.codexResumeLauncher(ctx, launcher) != launcher {
		return "", "launcher " + launcher + " runs Codex in another home"
	}
	if at, ok := codexResumes.Load(id); ok && time.Since(at.(time.Time)) < codexResumeHold {
		return "", "a resume of it may still be coming up"
	}
	cwd = n.codexCwd(id)
	if !dirExists(cwd) {
		return "", "its folder cannot be told"
	}
	return cwd, ""
}

// engineBusy says why a switch of session id (on engine from) waits for a later message, "" when it can go now: a
// Claude Code agent as switchBusy says; a Codex one that works (and not only by herdr's stale word, codexTurnOver),
// waits for the user, runs a turn on its app-server, or still starts.
func (n *Node) engineBusy(ctx context.Context, id, from string) string {
	if from != "codex" {
		return n.switchBusy(ctx, id)
	}
	_ = n.refreshHosts(ctx)
	a, ok := n.agent(id)
	if !ok {
		return ""
	}
	if a.AgentStatus == proto.StatusBlocked || a.AgentStatus == proto.StatusWorking && !n.codexTurnOver(id, time.Now()) {
		return "the agent is " + a.AgentStatus
	}
	if _, t, err := n.codexOwner(id); err == nil && t.turn != "" {
		return "the agent is working"
	}
	if r := n.starts.get(id); r != nil && r.ended.IsZero() || n.switches().inFlight(id) {
		return "the agent is starting"
	}
	return ""
}

// handoffSpans are the stretches a handoff covers, oldest first: those of legs after index after (-1: all of them),
// each from the end of the one before it, then session cur's (on engine from) since the last.
func handoffSpans(legs []proto.EngineLeg, after int, cur, from string) []handoffSpan {
	var out []handoffSpan
	var prev time.Time
	for i, l := range legs {
		if i > after {
			out = append(out, handoffSpan{SessionID: l.SessionID, Agent: l.Agent, After: prev, Until: l.Until})
		}
		prev = l.Until
	}
	return append(out, handoffSpan{SessionID: cur, Agent: from, After: prev})
}

// engineMode is the permission mode engine to starts in, from mode, the one the session runs in now: plan stays plan,
// bypassPermissions goes to Codex as its bypass flags, the rest is the launcher's own.
func engineMode(to, mode string) string {
	switch mode {
	case "plan":
		return mode
	case proto.ModeBypass:
		if to == "codex" {
			return mode
		}
	}
	return ""
}

// applyEngineSwitch carries out session id's pending switch ps to the other engine before the user's message is typed,
// inside send's turn (pane: the agent's; from: the engine it runs). handled false leaves the message to go as always:
// a switch that no longer applies (too old, on that engine already) is dropped, one of a busy agent waits. handled
// true: the switch was carried out and the message went in with it (nil), or the message is refused with nothing
// typed (a draft, photos, a failed switch, which is dropped).
func (n *Node) applyEngineSwitch(ctx context.Context, id, pane, from string, ps proto.PendingSwitch, text string, images []proto.Image,
	opts sendOpts) (bool, error) {
	if ps.Agent == from || time.Since(ps.At) > switchMaxAge {
		n.logf("session %s: switch to %s (launcher %s) picked %s dropped: on it already, or picked too long ago", id, engineName(ps.Agent),
			ps.Launcher, ps.At.Format(time.RFC3339))
		n.putPendingSwitch(id, nil)
		return false, nil
	}
	if why := n.engineBusy(ctx, id, from); why != "" {
		n.logf("session %s: switch to %s waits (%s); the message goes to %s", id, engineName(ps.Agent), why, engineName(from))
		return false, nil
	}
	if len(images) > 0 {
		return true, refusal(proto.CodeSwitchUnsupported, "a switch to the other engine takes the message's text only: send the words first and the photos after "+
			"the switch, or pick the current engine again to send them now", nil)
	}
	if err := n.checkPaneFor(ctx, pane, from, opts); err != nil { // as for any message: nothing done, the switch waits
		return true, err
	}
	defer n.markSwitching(id)()
	pctx, cancel := context.WithTimeout(ctx, switchProbeWait)
	plan, err := n.planEngineSwitch(pctx, id, ps)
	var backErr error // why the agent could not be brought back on its own launcher
	if err == nil && from == "claude" {
		_, _, backErr = n.checkSwitchLauncher(pctx, id, plan.own, "")
	}
	cancel()
	var full, delta string
	if err == nil {
		full, err = n.engineHandoff(handoffSpans(plan.legs, -1, id, from), from, ps.Agent, false)
	}
	if err == nil && plan.parked != "" {
		delta, err = n.engineHandoff(handoffSpans(plan.legs, plan.parkedLeg, id, from), from, ps.Agent, true)
	}
	if err != nil {
		n.logf("session %s: switch to %s (launcher %s) dropped: %v", id, engineName(ps.Agent), ps.Launcher, err)
		n.putPendingSwitch(id, nil)
		return true, asSwitchError(err, engineUnsupported)
	}
	prev, had := n.state.get(id)
	var r resumed // what a Claude Code agent runs with: its modes, the model to come back on (launcher_switch.go)
	var back modelExecution
	modeNow := prev.AppMode // a Codex agent's mode is its record's
	if from == "claude" {
		r, back = n.resumeWith(id, sendOpts{}), n.ranExecution(id, prev)
		modeNow = r.mode
	}
	mode := engineMode(ps.Agent, modeNow)
	// The last look before the first Ctrl+C: the checks took a while, and a turn may have started, a dialog come up or
	// someone typed at the computer meanwhile.
	if why := n.engineBusy(ctx, id, from); why != "" {
		n.logf("session %s: switch to %s waits (%s); the message goes to %s", id, engineName(ps.Agent), why, engineName(from))
		return false, nil
	}
	if err := n.checkPaneFor(ctx, pane, from, sendOpts{}); err != nil {
		return true, err
	}
	row := proto.Session{SessionID: id, Cwd: plan.cwd, Launcher: ps.Launcher, Agent: ps.Agent, Status: proto.StatusIdle, Starting: true,
		StartedByPersona: had, UpdatedAt: time.Now().UTC(), WorkspaceID: plan.a.WorkspaceID, PendingSwitch: &ps}
	row.SetEngineHistory(plan.legs)
	if !n.switches().begin(id, row) {
		n.logf("session %s: switch to %s refused: another one is under way", id, engineName(ps.Agent))
		return true, switchUnderWay()
	}
	defer func() {
		if serving := n.switches().end(id); serving != nil && serving != n {
			serving.notifySessions()
		}
		n.notifySessions()
	}()
	// From the first Ctrl+C on, on its own clock (launcher_switch.go).
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), engineSwitchBudget)
	defer cancel()
	c := &engineCarry{id: id, ps: ps, plan: plan, text: text, full: full, delta: delta, mode: mode, backMode: r.mode, backEffort: r.effort,
		back: back, backErr: backErr, prev: prev, had: had}
	newID, err := n.carryOutEngine(wctx, c)
	if err != nil {
		n.putPendingSwitch(id, nil) // dropped: nothing tries it again by itself
		return true, err
	}
	n.engineSwitched(wctx, c, newID)
	n.logf("session %s: the conversation now runs in %s session %s (launcher %s)", id, engineName(ps.Agent), newID, ps.Launcher)
	return true, nil
}

// engineCarry is one switch to the other engine being carried out: the session and its switch, the plan, the user's
// message and the two handoffs (full: the whole conversation; delta: what the parked session has not seen), the modes
// to start with and to come back with, and the record as it was.
type engineCarry struct {
	id                         string
	ps                         proto.PendingSwitch
	plan                       *enginePlan
	text, full, delta          string
	mode, backMode, backEffort string
	back                       modelExecution
	backErr                    error
	prev                       sessionState
	had                        bool
	// legs are the history the new session carries: the plan's, then this session's stretch until the switch began.
	legs []proto.EngineLeg
}

// carryOutEngine quits session id's agent and starts the other engine in the same shell, as c says: the parked
// session resumed when there is one, else (or when that does not come up) a fresh session; after a start that did not
// come up it clears the pane and resumes the agent on its own launcher (not when backErr says it cannot be). It
// returns the session running the conversation now, or the refusal that says how it ended.
func (n *Node) carryOutEngine(ctx context.Context, c *engineCarry) (string, error) {
	p := c.plan
	a, h, ref := p.a, p.h, p.ref
	c.legs = append(slices.Clone(p.legs), proto.EngineLeg{SessionID: c.id, Agent: p.from, Until: time.Now().UTC()})
	n.logf("session %s: switching from %s (launcher %s) to %s (launcher %s, model %q) in %s", c.id, engineName(p.from), p.own,
		engineName(p.to), c.ps.Launcher, p.model, a.PaneID)
	if quit, err := n.quitAgent(ctx, c.id, a.PaneID); err != nil || !quit {
		return "", refusal(proto.CodeSwitchNotStopped, fmt.Sprintf("the agent did not quit (%v); it runs on %s as before", err, p.own), nil)
	}
	shells, dir, err := n.shellAfterQuit(ctx, c.id, p.from, a, h, ref)
	if err != nil {
		n.logf("session %s: switch: %v", c.id, err)
		return "", refusal(proto.CodeSwitchFailedStopped, fmt.Sprintf("the agent quit, but its terminal did not come back to a shell prompt: %v", err), nil)
	}
	n.carryTodo(ctx, h, ref, a)
	if p.parked != "" {
		id, err := n.startEngine(ctx, c, true, handoffMessage(c.delta, p.from, p.to, p.model, true, c.text), shells, dir)
		if err == nil {
			return id, nil
		}
		n.logf("session %s: its earlier %s session %s did not come up (%v): a fresh one takes the conversation", c.id, engineName(p.to), p.parked, err)
		if shells, dir, err = n.clearPane(ctx, c.id, a, h, ref); err != nil {
			n.logf("session %s: could not clear the terminal: %v", c.id, err)
			return "", refusal(proto.CodeSwitchFailedStopped, fmt.Sprintf("the earlier %s session did not come up, and the terminal did not come back to a shell prompt: %v",
				engineName(p.to), err), nil)
		}
	}
	id, startErr := n.startEngine(ctx, c, false, handoffMessage(c.full, p.from, p.to, p.model, false, c.text), shells, dir)
	if startErr == nil {
		return id, nil
	}
	n.logf("session %s: %s did not come up on launcher %s: %v", c.id, engineName(p.to), c.ps.Launcher, startErr)
	err = c.backErr
	if err == nil {
		if shells, dir, err = n.clearPane(ctx, c.id, a, h, ref); err == nil {
			err = n.comeBack(ctx, c, shells, dir)
		}
	}
	if err != nil {
		n.logf("session %s: could not bring the agent back on launcher %s: %v", c.id, p.own, err)
		return "", refusal(proto.CodeSwitchFailedStopped, fmt.Sprintf("%s did not come up on launcher %s (%v), nor the agent again on %s (%v); it has stopped",
			engineName(p.to), c.ps.Launcher, startErr, p.own, err), nil)
	}
	n.logf("session %s: back on launcher %s", c.id, p.own)
	return "", refusal(proto.CodeSwitchFailed, fmt.Sprintf("%s did not come up on launcher %s (%v); the agent is back on %s", engineName(p.to),
		c.ps.Launcher, startErr, p.own), nil)
}

// startEngine starts the other engine in the switch's pane with prompt as its first message: the parked session
// resumed (resume), else a fresh one. It returns the new session's id. What it wrote of a session that did not come up
// is put back (the parked session's record) or loses the history it was given (a fresh one's), so the list does not
// leave the session the switch comes back to out.
func (n *Node) startEngine(ctx context.Context, c *engineCarry, resume bool, prompt string, shells []string, dir string) (string, error) {
	p, ps := c.plan, c.ps
	cwd := p.cwd
	if resume {
		cwd = p.parkedCwd
	}
	// The conversation's history goes on the new record (control.go, codex_start.go).
	sctx, cancel := switchStartCtx(withEngineHistory(ctx, c.legs), p.a, p.h, p.ref, shells, dir, cwd)
	defer cancel()
	ws := p.a.WorkspaceID
	if resume {
		prev, had := n.state.get(p.parked)
		var err error
		if p.to == "claude" {
			_, err = n.start(sctx, cwd, ps.Launcher, prompt, p.parked, ws, c.mode, ps.Effort, "", "", "claude", p.resume)
		} else {
			err = n.resumeParkedCodex(sctx, c, prompt)
		}
		if err != nil {
			n.state.putBack(p.parked, prev, had)
		}
		return p.parked, err
	}
	if p.to == "codex" {
		s, err := n.start(sctx, cwd, ps.Launcher, prompt, "", ws, c.mode, "", "", "", "codex", p.fresh)
		if err != nil {
			return "", err
		}
		return s.SessionID, nil
	}
	id := newUUID()
	_, err := n.start(sctx, cwd, ps.Launcher, prompt, "", ws, c.mode, ps.Effort, "", id, "claude", p.fresh)
	if err != nil {
		n.state.setEngineHistory(id, nil)
		return "", err
	}
	return id, nil
}

// resumeParkedCodex resumes the parked Codex thread in the switch's pane (ctx: startEngine's) through the launcher
// picked, with prompt as its message: `<launcher words> resume <id> -- <prompt>` through codex-start, in the thread's
// directory, with the mode, model and effort of the switch. Its record then names that launcher and the history.
func (n *Node) resumeParkedCodex(ctx context.Context, c *engineCarry, prompt string) error {
	p, ps := c.plan, c.ps
	id := p.parked
	if path := n.codexPath(id); path != "" && n.codices != nil && n.codexHeldNow(path) {
		return errors.New("a Codex has the thread open")
	}
	launch, argv, err := n.codexResumeArgv(ctx, ps.Launcher, c.mode, p.resume)
	if err != nil {
		return err
	}
	prev, had := n.state.get(id)
	if had {
		n.state.setEngineHistory(id, c.legs) // before it shows in the pane: one row for the conversation from then on
	}
	err = n.resumeCodex(ctx, launch, id, sessionState{Cwd: p.parkedCwd}, argv, prompt, p.a.WorkspaceID, "")
	if err != nil {
		if maybeDelivered(err) && n.paneOf(id) != "" {
			codexResumes.Store(id, time.Now()) // it may still come up in the pane: no second writer of the thread
		}
		return err
	}
	codexResumes.Delete(id)
	st := sessionState{Pane: p.a.PaneID, Launcher: ps.Launcher, Cwd: p.parkedCwd, StartedAt: time.Now().UTC(), Model: p.resume.Model,
		EngineHistory: c.legs}
	if had && !prev.StartedAt.IsZero() {
		st.StartedAt = prev.StartedAt
	}
	if c.mode == "plan" {
		st.AppMode = c.mode
	}
	n.state.put(id, st)
	return nil
}

// comeBack resumes session id on its own launcher in the switch's pane after the other engine did not come up, with
// no message (it is refused, its words kept): a Claude Code agent as #5035 brings it back (the model it last switched
// to, its modes; the record put back as it was), a Codex one with `codex resume <id>` through its launcher with its
// record's model and mode.
func (n *Node) comeBack(ctx context.Context, c *engineCarry, shells []string, dir string) error {
	p := c.plan
	sctx, cancel := switchStartCtx(ctx, p.a, p.h, p.ref, shells, dir, p.cwd)
	defer cancel()
	if p.from == "claude" {
		n.state.restoreLaunch(c.id, c.prev, c.had, p.own)
		_, err := n.start(sctx, p.cwd, p.own, "", c.id, p.a.WorkspaceID, c.backMode, c.backEffort, "", "", "claude", c.back)
		n.state.restoreLaunch(c.id, c.prev, c.had, p.own) // the start wrote a new record, whether it came up or not
		return err
	}
	launch, argv, err := n.codexResumeArgv(sctx, p.own, c.prev.AppMode, modelExecution{Model: c.prev.Model})
	if err != nil {
		return err
	}
	return n.resumeCodex(sctx, launch, c.id, sessionState{Cwd: p.cwd}, argv, "", p.a.WorkspaceID, "")
}

// codexResumeArgv is the host a resume of a Codex thread starts on (ctx's) and the words it runs through launcher
// (resumeCodex adds `resume <id>`): in permission mode mode (plan read-only, bypassPermissions without approvals or
// sandbox) with model execution e.
func (n *Node) codexResumeArgv(ctx context.Context, launcher, mode string, e modelExecution) (host.Launcher, []string, error) {
	launch, err := n.launcherFor(n.startKind(ctx))
	if err != nil {
		return nil, nil, err
	}
	argv, ok := n.codexLauncher(ctx, launcher)
	if !ok {
		return nil, nil, fmt.Errorf("launcher %s does not run Codex", launcher)
	}
	if argv, err = n.codexReadonlyArgs(launcher, argv, mode); err != nil {
		return nil, nil, err
	}
	if mode == proto.ModeBypass {
		argv = n.codexBypassArgv(launcher, argv)
	}
	return launch, codexExecutionArgs(argv, e), nil
}

// engineSwitched writes what a switch carried out leaves: the new session newID carries the conversation's history and
// no switch, the model picked shown until a reply names one; session id, now an earlier stretch of it, has neither
// history nor switch nor pane; the pane, when Persona opened it for id, is marked as the new session's terminal.
func (n *Node) engineSwitched(ctx context.Context, c *engineCarry, newID string) {
	p := c.plan
	n.state.update(newID, func(v *sessionState) bool {
		v.EngineHistory, v.PendingSwitch = slices.Clone(c.legs), nil
		if p.to == "codex" && c.ps.Model != "" && v.Model == "" {
			v.Model = c.ps.Model // what a Resume of it runs (resumeOtherWith)
		}
		return true
	})
	if newID != c.id {
		n.state.update(c.id, func(v *sessionState) bool {
			v.EngineHistory, v.PendingSwitch = nil, nil
			if v.Pane == p.a.PaneID {
				v.Pane = "" // the pane is the new session's: a reload binds it to that one only
			}
			return true
		})
	}
	n.switches().setPending(c.id, nil)
	if p.to == "claude" {
		n.noteSwitchedModel(newID, c.ps)
	}
	if p.a.Tokens[personaTabToken] == c.id {
		if launch, ok := p.h.(host.Launcher); ok {
			markPersonaTab(ctx, launch, p.ref, newID)
		}
	}
	n.notifySessions()
}

// carryTodo hands the to-do the agent in pane ref works on to whatever starts there next, best effort: the pane's
// persona_todo_ref written as its persona_from, as a dispatcher marks a child Agent (docs/assistant/todos.md
// "Following spawned Agents"), so the new session joins the same to-do.
func (n *Node) carryTodo(ctx context.Context, h host.Host, ref host.PaneRef, a host.Agent) {
	todo := a.Tokens["persona_todo_ref"]
	if todo == "" {
		return
	}
	if r, ok := h.(host.MetadataReporter); ok {
		if err := r.ReportMetadata(ctx, ref, "persona", map[string]*string{"persona_from": &todo}); err != nil {
			n.logf("pane %s: could not hand its to-do on: %v", a.PaneID, err)
		}
	}
}

// codexHolds: a Codex CLI process (not an app-server, which keeps a thread it has loaded) has the rollout at path
// open. Processes that cannot be read count as not holding it: the agent has left its pane, and nothing of the other
// engine writes the rollout.
func (n *Node) codexHolds(path string) bool {
	if path == "" || n.codices == nil {
		return false
	}
	open, _ := n.codexOpenNow(path, true)
	return open
}

// joinStretches gives the rows of list whose record carries earlier stretches (chains) those stretches, and returns
// the sessions to leave out: every one named in another listed row's history, so a conversation that moved between
// engines is one row. The most recent conversation counts first, and a row it left out names none: two records that
// still name each other (a write that did not get through) keep the newer one. A row that continues the conversation
// shows its first stretch's title, unless it has a manual one of its own.
func joinStretches(list []proto.Session, chains map[string][]proto.EngineLeg) map[string]bool {
	var rows []int
	for i := range list {
		if legs := chains[list[i].SessionID]; legs != nil {
			list[i].SetEngineHistory(legs)
		}
		if len(list[i].EngineHistory()) > 0 {
			rows = append(rows, i)
		}
	}
	if len(rows) == 0 {
		return nil
	}
	index := make(map[string]int, len(list)) // only now: this runs on every list, and most have no such row
	for i := range list {
		index[list[i].SessionID] = i
	}
	last := func(i int) time.Time { legs := list[i].EngineHistory(); return legs[len(legs)-1].Until }
	sort.SliceStable(rows, func(x, y int) bool { return last(rows[x]).After(last(rows[y])) })
	hidden := map[string]bool{}
	for _, i := range rows {
		s := &list[i]
		if hidden[s.SessionID] {
			continue
		}
		for _, l := range s.EngineHistory() {
			if l.SessionID != s.SessionID {
				hidden[l.SessionID] = true
			}
		}
		first, ok := index[s.EngineHistory()[0].SessionID]
		if !ok || first == i || s.TitleManual || s.TitleSource == proto.TitleSourceManual || list[first].Title == "" {
			continue
		}
		s.Title, s.TitleManual, s.TitleSource = list[first].Title, list[first].TitleManual, list[first].TitleSource
	}
	return hidden
}
