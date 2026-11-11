package node

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/proto"
)

// Switching a running Claude Code Agent to another launcher (model provider) of the same engine (#5035,
// docs/integrations/claude-integration.md "Switching a running Agent to another provider"). Claude Code takes its
// gateway, key and model from the environment it starts with, so no command typed into it changes them: the agent is
// quit and the same conversation resumed (`--resume <id>`) through the other launcher, in the same terminal. That
// happens with the user's next message, not when the provider is picked:
//
//   - session.modes with a launcher other than the session's own checks the pick (recordSwitch) and keeps it as the
//     session's pending switch (Session.pending_switch); the session's own launcher drops one that waits (launcherModes).
//     Nothing is quit or typed then.
//   - The next session.send, inside its turn (the typing lock held: quitAgent, never stop, which takes it again),
//     after the checks every message gets (a dialog or a draft refuses with nothing done), carries it out
//     (applySwitch): the agent must rest, with no background work a quit would end; it is quit with Ctrl+C, and once
//     only a shell is in front and no claude has the conversation open any more, the launcher's command runs in that
//     shell with --resume, as the user's Resume does in a kept terminal. Then the message is typed to the new agent.
//   - A start that does not come up puts the record back and resumes the agent on its own launcher in the same shell;
//     the message is not typed and the switch stays pending (switch_failed). When even that does not come up, the
//     agent has stopped (switch_failed_stopped): Resume on its terminal page brings it back on its own launcher.
//
// What is refused, and why: only a Claude Code Agent running in a terminal Persona can type into (a Codex or another
// CLI keeps its launcher, a runtime runs its sessions itself, an assistant conversation switches in its own chip); a
// launcher that is on this computer and runs Claude Code; with a model, one that passes --model on (not locked); and
// one whose Claude config directory holds the conversation at its latest: --resume looks there only, and the sign-in
// is kept there too, so a launcher of another profile would answer "No conversation found", or run on another account.
// A launcher whose directory cannot be seen (a script the probe cannot follow) is let through: a resume that finds no
// conversation ends the start, and the switch rolls back.
//
// The pending switch is kept in the session's state record (the hub's Postgres): a node restart neither drops it nor
// carries it out without a message. A session Persona did not start has no record and gets none for this (a record
// means "Persona started it", docs/lessons/agents.md): its switch waits in the node's memory, and a node restart
// forgets it, so its row shows no pending switch any more and nothing is switched behind the user's back.

// switchExitWait bounds how long a switch waits, once its agent left the pane, for the claude process to end and only
// a shell to be in front. rollbackWait bounds the resume on the agent's own launcher after a switch failed: it goes on
// when the caller has stopped waiting for the message, so the agent does not stay stopped because the phone gave up.
const (
	switchExitWait = 10 * time.Second
	rollbackWait   = 3 * time.Minute
)

// ownLauncher is the launcher session id runs on, as its row says it: Persona's record, a runtime's row, else the one
// its process's environment names (inferLauncher), else plain claude, as the apps take it.
func (n *Node) ownLauncher(id string) string {
	if st, ok := n.state.get(id); ok && st.Launcher != "" {
		return st.Launcher
	}
	if s, _, listed := n.runtimes.rowOf(id); listed && s.Launcher != "" {
		return s.Launcher
	}
	return cmp.Or(n.inferLauncher(id), "claude")
}

// pendingSwitch is the switch session id waits with, nil when none: its record's, else the node's memory's.
func (n *Node) pendingSwitch(id string) *proto.PendingSwitch {
	if st, ok := n.state.get(id); ok && st.PendingSwitch != nil {
		ps := *st.PendingSwitch
		return &ps
	}
	return n.unrecordedSwitch(id)
}

// unrecordedSwitch is the switch session id waits with in the node's memory (a session with no record), nil when none.
func (n *Node) unrecordedSwitch(id string) *proto.PendingSwitch {
	n.mu.Lock()
	defer n.mu.Unlock()
	if ps, ok := n.switches[id]; ok {
		return &ps
	}
	return nil
}

// putPendingSwitch sets session id's pending switch (nil: none) where it is kept, and tells the hub.
func (n *Node) putPendingSwitch(id string, ps *proto.PendingSwitch) {
	recorded := n.state.setPendingSwitch(id, ps)
	n.mu.Lock()
	if recorded || ps == nil {
		delete(n.switches, id)
	} else {
		if n.switches == nil {
			n.switches = map[string]proto.PendingSwitch{}
		}
		n.switches[id] = *ps
	}
	n.mu.Unlock()
	n.notifySessions()
}

// launcherModes is session.modes with a launcher. One other than the session's own is a switch to it: checked and kept
// as the session's pending switch with the model and effort, the permission mode applied now (handled). The session's
// own launcher drops a switch that waits; handled false then when the request asks for more (a model, mode or effort),
// which goes on as it would without a launcher.
func (n *Node) launcherModes(ctx context.Context, id, launcher, model, mode, effort string) (handled bool, err error) {
	launcher = strings.TrimSpace(launcher)
	if !validID(id) {
		return true, fmt.Errorf("invalid session id %q", id)
	}
	// What the request changes must not be overwritten by the hub's copy of the record still on its way (#5510).
	if err := n.state.loaded(ctx); err != nil {
		return true, err
	}
	// One at a time with the messages: one carrying a switch out writes the record anew, and would drop a pick made
	// meanwhile; this one waits for it, and is then taken against the launcher the agent runs on by then.
	unlock := n.typing.lock(id)
	if launcher == n.ownLauncher(id) {
		if n.pendingSwitch(id) != nil {
			n.putPendingSwitch(id, nil)
			n.logf("session %s: launcher switch cancelled", id)
		}
		unlock()
		return model == "" && mode == "" && effort == "", nil
	}
	err = n.recordSwitch(ctx, id, launcher, model, effort)
	unlock()
	if err != nil || mode == "" {
		return true, err
	}
	return true, n.setModes(ctx, id, mode, "")
}

// recordSwitch checks a switch of session id to launcher, with model and effort ("" for the launcher's default model,
// the effort it runs with now), and keeps it as the session's pending switch, replacing one that waited.
func (n *Node) recordSwitch(ctx context.Context, id, launcher, model, effort string) error {
	model = strings.TrimSpace(model)
	if model != "" && !proto.ValidModelID(model) {
		return fmt.Errorf("invalid model %q", model)
	}
	if err := validModes("", effort); err != nil {
		return err
	}
	if _, _, _, err := n.switchSession(ctx, id); err != nil {
		return err
	}
	if err := n.checkSwitchLauncher(ctx, id, launcher, model); err != nil {
		return err
	}
	n.putPendingSwitch(id, &proto.PendingSwitch{Launcher: launcher, Model: model, Effort: effort, At: time.Now().UTC()})
	n.logf("session %s: switch to launcher %s (model %q, effort %q) waits for the next message", id, launcher, model, effort)
	return nil
}

// switchUnsupported is the refusal of a switch for a session that cannot have one, why in the node's words.
func switchUnsupported(why string) error {
	return refusal(proto.CodeSwitchUnsupported, "only a Claude Code Agent running in a terminal can switch to another launcher: "+why, nil)
}

// switchSession checks that session id is one a switch can restart: a Claude Code Agent running in a pane of a
// terminal host that can say what runs in it and start an agent there. It returns the agent, its host and its pane.
func (n *Node) switchSession(ctx context.Context, id string) (host.Agent, host.Host, host.PaneRef, error) {
	var a host.Agent
	var ref host.PaneRef
	if !validID(id) {
		return a, nil, ref, fmt.Errorf("invalid session id %q", id)
	}
	switch {
	case n.isAssistantSession(id) || n.runtimeAssistant(id):
		return a, nil, ref, switchUnsupported("an assistant conversation switches in its own chip")
	case n.externalCodex(id) || n.codexPath(id) != "" || n.traexPath(id) != "":
		return a, nil, ref, switchUnsupported("this is a Codex session")
	}
	if h, _, known := n.runtimes.belongs(id); h != nil || known {
		return a, nil, ref, switchUnsupported("a runtime runs this session")
	}
	if _, ok := n.terminalOnly(id); ok {
		return a, nil, ref, switchUnsupported("another agent runs in this terminal")
	}
	if _, err := n.paneFor(ctx, id); err != nil {
		return a, nil, ref, n.noPaneForModes(id)
	}
	a, live := n.agent(id)
	if !live {
		return a, nil, ref, n.noPaneForModes(id) // a claude in a terminal Persona does not drive
	}
	if a.Agent != "" && a.Agent != "claude" {
		return a, nil, ref, switchUnsupported(a.Agent + " runs in this terminal")
	}
	kind := host.ParsePane(a.PaneID).Kind
	if _, runtime := engineRuntimes[kind]; runtime || kind == host.Botmux {
		return a, nil, ref, switchUnsupported(kind + " runs this agent itself")
	}
	h, ref, err := n.paneHost(a.PaneID)
	if err != nil {
		return a, nil, ref, switchUnsupported(err.Error())
	}
	if _, ok := h.(host.ForegroundNamer); !ok {
		return a, nil, ref, switchUnsupported(h.Kind() + " cannot say what runs in the agent's terminal")
	}
	launch, err := n.launcherFor(h.Kind())
	if err != nil {
		return a, nil, ref, switchUnsupported(err.Error())
	}
	if lh, ok := launch.(host.Host); !ok || lh.Kind() != h.Kind() {
		return a, nil, ref, switchUnsupported(h.Kind() + " cannot start an agent in the agent's terminal")
	}
	return a, h, ref, nil
}

// checkSwitchLauncher checks that launcher can resume session id: it is on this computer, runs Claude Code, takes
// model when one is given, and keeps Claude Code's data where the conversation is.
func (n *Node) checkSwitchLauncher(ctx context.Context, id, launcher, model string) error {
	if !nameRe.MatchString(launcher) {
		return fmt.Errorf("invalid launcher %q", launcher)
	}
	// A provider whose secrets this computer has not fetched yet: its environment (which may name a config
	// directory) comes with them; the manager refuses one out of this computer's scope.
	if err := n.ensureManaged(ctx, launcher); err != nil {
		return err
	}
	if !n.knownLauncher(ctx, launcher) {
		return refusal(proto.CodeSwitchLauncherMissing, fmt.Sprintf("launcher %s is not on this computer", launcher), nil)
	}
	if h, _ := n.startRuntime(launcher); h != nil {
		return refusal(proto.CodeSwitchOtherAgent, fmt.Sprintf("launcher %s belongs to a runtime, not to Claude Code", launcher), nil)
	}
	if agent := n.launcherModelsAgent(ctx, launcher, ""); agent != "claude" {
		return refusal(proto.CodeSwitchOtherAgent, fmt.Sprintf("launcher %s runs %s, not Claude Code", launcher, agent), nil)
	}
	if model != "" {
		if _, err := n.launcherModelCheck(ctx, launcher, model); err != nil {
			return refusal(proto.CodeSwitchModelLocked, fmt.Sprintf("launcher %s would not run model %s: %v", launcher, model, err), nil)
		}
	}
	return n.checkSwitchConfigDir(ctx, id, launcher)
}

// checkSwitchConfigDir refuses launcher for session id when the Claude config directory it runs claude with does not
// hold the conversation at its latest (the newest copy, transcriptPath): --resume there would not find it, or would
// go on from an older point. A launcher whose directory cannot be told passes (its start fails safely); so does a
// session with no transcript yet (it starts under its id).
func (n *Node) checkSwitchConfigDir(ctx context.Context, id, launcher string) error {
	path := n.transcriptPath(id)
	if path == "" {
		return nil
	}
	project := filepath.Dir(path)
	projects := filepath.Dir(project)
	if filepath.Base(projects) != "projects" {
		return nil
	}
	dir, known := n.launcherClaudeDir(ctx, launcher)
	if !known {
		n.logf("session %s: launcher %s: its Claude config directory cannot be told; the switch goes ahead", id, launcher)
		return nil
	}
	mine := filepath.Join(dir, "projects", filepath.Base(project), filepath.Base(path))
	if fileExists(mine) && holdsNewestCopy(mine, path) {
		return nil
	}
	return refusal(proto.CodeSwitchConfigDir, fmt.Sprintf("launcher %s runs Claude Code with the config directory %s, and this conversation is in %s",
		launcher, dir, filepath.Dir(projects)), nil)
}

// launcherClaudeDir is the Claude config directory a terminal Agent started through launcher runs with: the one the
// launcher sets (a managed provider's environment, the machine's start directory for plain claude, what the probe saw a
// shell launcher export), else the shell's ($CLAUDE_CONFIG_DIR of the login shell, else ~/.claude). known false when
// it cannot be told: a launcher the probe cannot follow, or one that sets the variable empty.
func (n *Node) launcherClaudeDir(ctx context.Context, launcher string) (string, bool) {
	if n.sessionScope != nil {
		return n.sessionScope.claude, true
	}
	shell := func() (string, bool) {
		if d := strings.TrimSpace(shellClaudeConfigDir(ctx)); d != "" {
			return absConfigDir(d, n.home)
		}
		return filepath.Join(n.home, ".claude"), true
	}
	if l, managed := n.managedLauncher(launcher); managed {
		if d, set := providerEnv(l, "", "", false)["CLAUDE_CONFIG_DIR"]; set {
			return absConfigDir(d, n.home)
		}
		return shell()
	}
	if launcher == "claude" {
		if d := n.claudeStartDir.Load(); d != nil && *d != "" {
			return absConfigDir(*d, n.home)
		}
		return shell()
	}
	p, err := n.launcherProbe(ctx, launcher)
	if err != nil {
		return "", false
	}
	if d, set := p.Env["CLAUDE_CONFIG_DIR"]; set {
		return absConfigDir(d, n.home)
	}
	if p.Shell == nil { // a wrapper script, read: it runs in the pane's shell
		return shell()
	}
	if d := p.Shell["CLAUDE_CONFIG_DIR"]; d != "" {
		return absConfigDir(d, n.home)
	}
	return filepath.Join(n.home, ".claude"), true
}

// absConfigDir is a CLAUDE_CONFIG_DIR value as a directory: ~ expanded; not known when empty or relative.
func absConfigDir(d, home string) (string, bool) {
	d = proto.ExpandHome(strings.TrimSpace(d), home)
	if d == "" || !filepath.IsAbs(d) {
		return "", false
	}
	return filepath.Clean(d), true
}

// switchIdle refuses a switch of session id while its agent works, waits for the user or still starts, or has
// background work that quitting it would end (background commands, subagents, workflows, a /loop).
func (n *Node) switchIdle(ctx context.Context, id string) error {
	_ = n.refreshHosts(ctx)
	a, ok := n.agent(id)
	if !ok {
		return n.noPaneForModes(id)
	}
	if a.AgentStatus == proto.StatusWorking || a.AgentStatus == proto.StatusBlocked {
		return refusal(proto.CodeSwitchBusy, "the agent is "+a.AgentStatus+"; the switch waits for a message sent once it rests", nil)
	}
	if r := n.starts.get(id); r != nil && r.ended.IsZero() {
		return refusal(proto.CodeSwitchBusy, "the agent is still starting", nil)
	}
	if why := n.backgroundWork(id); why != "" {
		return refusal(proto.CodeSwitchBackground, "quitting the agent would end its background work: "+why, nil)
	}
	return nil
}

// backgroundWork says what background work session id's agent has going, as its row shows it, "" when none: what a
// quit would end.
func (n *Node) backgroundWork(id string) string {
	p := n.transcriptPath(id)
	if p == "" {
		return ""
	}
	fi, err := os.Stat(p)
	if err != nil {
		return ""
	}
	m := n.meta(p, fi)
	s := proto.Session{SessionID: id}
	n.setBackground(&s, m)
	if len(s.Background) > 0 {
		return fmt.Sprintf("%d background task(s)", len(s.Background))
	}
	for _, w := range s.Workflows {
		if w.Running {
			return "a workflow"
		}
	}
	for _, sub := range n.subagents(id) {
		if sub.Running {
			return "a subagent"
		}
	}
	if m.loop != nil {
		return "a /loop"
	}
	return ""
}

// applySwitch carries out session id's pending switch ps before a message is typed, inside send's turn, and returns
// the pane the message goes to. A refusal leaves the agent as it was; a failure after the quit brings it back on its
// own launcher when it can. Either way the switch stays pending and nothing is typed.
func (n *Node) applySwitch(ctx context.Context, id, pane string, ps proto.PendingSwitch) (string, error) {
	own := n.ownLauncher(id)
	if ps.Launcher == own { // on that launcher by now: nothing to switch
		n.putPendingSwitch(id, nil)
		return pane, nil
	}
	a, h, ref, err := n.switchSession(ctx, id)
	if err != nil {
		return "", err
	}
	if err := n.checkSwitchLauncher(ctx, id, ps.Launcher, ps.Model); err != nil {
		return "", err
	}
	if err := n.switchIdle(ctx, id); err != nil {
		return "", err
	}
	prev, had := n.state.get(id)
	r := n.resumeWith(id, sendOpts{}) // its directory, modes and, on its own launcher, the model it runs (the rollback's)
	effort := cmp.Or(ps.Effort, r.effort)
	execution := modelExecution{Model: ps.Model}
	if execution.Model == "" {
		// Resumed without --model, Claude Code would go on with the model the conversation last ran, the old
		// launcher's (#2487): what a new session of the new launcher runs, unless the launcher names one itself.
		dir, known := n.launcherClaudeDir(ctx, ps.Launcher)
		if !known {
			dir = n.defaultClaudeDir()
		}
		execution.Model = n.resumeModel(ps.Launcher, cmp.Or(r.cwd, a.Cwd), "", dir)
	}
	n.logf("session %s: switching from launcher %s to %s (model %q) in %s", id, own, ps.Launcher, execution.Model, a.PaneID)
	if quit, err := n.quitAgent(ctx, id, a.PaneID); err != nil || !quit {
		return "", refusal(proto.CodeSwitchNotStopped, fmt.Sprintf("the agent did not quit (%v); it runs on %s as before", err, own), nil)
	}
	shells, cwd, err := n.shellAfterQuit(ctx, id, a, h, ref)
	if err != nil {
		n.logf("session %s: switch: %v", id, err)
		return "", refusal(proto.CodeSwitchFailedStopped, fmt.Sprintf("the agent quit, but its terminal did not come back to a shell prompt: %v", err), nil)
	}
	cwd = cmp.Or(cwd, r.cwd, a.Cwd)
	created := &host.Created{SlotID: a.TabID, SpaceID: a.WorkspaceID, Pane: ref}
	sctx := withAdoptedPane(withStartHost(ctx, h.Kind()), created, shells)
	s, startErr := n.start(sctx, cwd, ps.Launcher, "", id, a.WorkspaceID, r.mode, effort, "", "", "claude", execution)
	if startErr == nil {
		n.switched(id, ps)
		n.logf("session %s: now on launcher %s", id, ps.Launcher)
		return s.PaneID, nil
	}
	n.logf("session %s: the agent did not come up on launcher %s: %v", id, ps.Launcher, startErr)
	// What the start wrote over goes back, the switch pending as before: a Resume brings back the launcher it ran on.
	prev.PendingSwitch = &ps
	n.state.restoreLaunch(id, prev, had, own)
	n.putPendingSwitch(id, &ps)
	if err := n.rollbackSwitch(ctx, id, own, a, h, ref, cwd, r, prev, had); err != nil {
		n.logf("session %s: could not bring the agent back on launcher %s: %v", id, own, err)
		return "", refusal(proto.CodeSwitchFailedStopped, fmt.Sprintf("the agent did not come up on launcher %s (%v), nor again on %s (%v); it has stopped",
			ps.Launcher, startErr, own, err), nil)
	}
	n.logf("session %s: back on launcher %s", id, own)
	return "", refusal(proto.CodeSwitchFailed, fmt.Sprintf("the agent did not come up on launcher %s (%v); it is back on %s", ps.Launcher, startErr, own), nil)
}

// switched clears what was kept for session id's switch to ps once its agent runs on the new launcher: the switch (the
// start's new record has none), and the model picked on the old launcher, which the list showed until a reply.
func (n *Node) switched(id string, ps proto.PendingSwitch) {
	n.state.setPendingSwitch(id, nil)
	n.mu.Lock()
	delete(n.switches, id)
	if m, ok := n.modes[id]; ok {
		m.Model = nil
		n.modes[id] = m
	}
	n.mu.Unlock()
	if ps.Model != "" {
		n.noteModel(id, ps.Model, n.transcriptModel(id)) // shown until a reply names the model
	}
	n.notifySessions()
}

// shellAfterQuit waits, once session id's agent left its pane a, until only shells are in front of the pane and no
// claude has the conversation open any more (Claude Code writes into its transcript as it exits), and returns those
// shells and the pane's directory as the host lists it now ("" when it does not say).
func (n *Node) shellAfterQuit(ctx context.Context, id string, a host.Agent, h host.Host, ref host.PaneRef) ([]string, string, error) {
	f, ok := h.(host.ForegroundNamer)
	if !ok {
		return nil, "", fmt.Errorf("%s cannot say what runs in %s", h.Kind(), a.PaneID)
	}
	deadline := time.Now().Add(switchExitWait)
	for {
		names, err := f.PaneForeground(ctx, ref)
		switch {
		case err != nil:
		case len(names) == 0 || !allShells(names):
			err = fmt.Errorf("%q runs in front of %s", names, a.PaneID)
		case n.openElsewhereRefusal(id) != nil:
			err = errors.New("a claude still has the conversation open")
		default:
			dir := ""
			if p, err := n.listedPane(ctx, a.PaneID); err == nil {
				dir = paneDir(p)
			}
			return names, dir, nil
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return nil, "", err
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// rollbackSwitch resumes session id on its own launcher in the shell its agent left, after the start on the new one
// failed: only when that shell is at rest again and the launcher still passes a switch's checks, with the model and
// modes it ran with (r), and with the record as it was (prev) afterwards.
func (n *Node) rollbackSwitch(ctx context.Context, id, own string, a host.Agent, h host.Host, ref host.PaneRef, cwd string,
	r resumed, prev sessionState, had bool) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackWait)
	defer cancel()
	if err := n.refreshHosts(ctx); err == nil {
		if _, live := n.agent(id); live {
			return errors.New("an agent runs the session again")
		}
	}
	shells, _, err := n.shellAfterQuit(ctx, id, a, h, ref)
	if err != nil {
		return err
	}
	if err := n.checkSwitchLauncher(ctx, id, own, ""); err != nil {
		return err
	}
	created := &host.Created{SlotID: a.TabID, SpaceID: a.WorkspaceID, Pane: ref}
	sctx := withAdoptedPane(withStartHost(ctx, h.Kind()), created, shells)
	_, err = n.start(sctx, cwd, own, "", id, a.WorkspaceID, r.mode, r.effort, "", "", "claude", r.execution)
	// The start wrote a new record, whether it came up or not: the one it had goes back, with its switch pending.
	n.state.restoreLaunch(id, prev, had, own)
	if prev.PendingSwitch != nil {
		n.putPendingSwitch(id, prev.PendingSwitch)
	}
	return err
}
