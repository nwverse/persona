package node

import (
	"context"
	"log"
	"path/filepath"

	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/proto"
)

// Stopped agents are read-only (#4754, docs/integrations/engines.md "Agents run in a terminal"). An agent of a
// terminal engine (herdr, Herdr App, Orca, Tern) that is not running — stopped by Persona (retire.go), closed by the
// user, exited by itself or cut off by a restart — keeps its conversation and its terminal and takes no messages: a
// send to it is refused as agent_stopped with nothing typed, where it used to be resumed (claude --resume, codex
// resume) in a new tab. The one way back is the user's Resume on its terminal page: session.start with
// resume_session_id, which types the resume into the shell the agent left behind when its tab is still there
// (inKeptTerminal), else opens a new tab as before. Left as they were: assistant conversations, sessions of Claude's
// desktop app, of runtimes and of engines that run agents themselves (Paseo, Cindy), and a branch nothing has started
// yet (fork.go), whose first message starts it.

const agentStopped = "agent_stopped"

// stoppedReason is a stopped agent's read_only_reason (the apps show their own words for it).
const stoppedReason = "Agent 已停止：对话和终端可以看，它不再收消息；要它接着做，在终端页点「恢复」"

// desktopSession: Claude's desktop app names transcript id (a session of its sidebar, archived or not, or one it went
// on from): the app's session, which a message continues as before (claudedesktop_open.go), not a stopped agent.
func (n *Node) desktopSession(id string) bool {
	x := readClaudeDesktopIndex(n.home)
	_, named := x.visible[id]
	return named || len(x.earlier[id]) > 0
}

// stoppedRefusal refuses a send to session id's stopped agent: nothing was typed.
func (n *Node) stoppedRefusal(id string) error {
	log.Printf("send %s: the agent has stopped: refused, nothing typed", id)
	return refusal(agentStopped, "这个 Agent 已经停了：对话和终端还在，它不再收消息，这次没有发送。要它接着做，在它的终端页点「恢复」。",
		map[string]any{"delivered": "none"})
}

// freshFork says session id is a branch nothing has started yet (fork.go): its first message starts it.
func (n *Node) freshFork(id string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.freshForks[id]
}

// forkStarted: session id's branch has run, and is an agent like any other from now on.
func (n *Node) forkStarted(id string) {
	n.mu.Lock()
	delete(n.freshForks, id)
	n.mu.Unlock()
}

// markStopped makes a stopped agent's row read-only: it can be read and its terminal opened (the shell it left, or
// Resume when there is none), nothing else.
func (n *Node) markStopped(s *proto.Session) {
	if s.SourceKind != "" || s.Assistant || s.Capabilities != nil || s.PaneID != "" || s.AgentSurface == "desktop" {
		return
	}
	if s.Status != proto.StatusOffline && s.Status != proto.StatusInterrupted {
		return
	}
	switch s.Agent {
	case "", "claude", "codex", traexAgent:
	default:
		return
	}
	if _, runtime := engineRuntimes[n.defaultKind()]; runtime || n.defaultHost() == nil {
		return
	}
	if n.isAssistantSession(s.SessionID) || n.runtimeAssistant(s.SessionID) || n.freshFork(s.SessionID) {
		return
	}
	s.Capabilities = &proto.SessionCapabilities{Read: true, Terminal: true}
	s.ReadOnlyReason = stoppedReason
}

// keptPane is the pane session id's agent left behind: one Persona opened for it (personaTabToken), with no agent in
// it now. ok false when there is none (the user closed it, or a herdr restart dropped its mark).
func (n *Node) keptPane(id string) (p host.Agent, ok bool) {
	if !validID(id) {
		return p, false
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	inUse := map[string]bool{}
	for _, a := range n.agents {
		inUse[a.PaneID] = true
	}
	for _, l := range n.layouts {
		for _, q := range l.panes {
			if q.PaneID != "" && q.Tokens[personaTabToken] == id && q.Agent == "" && !inUse[q.PaneID] {
				return q, true
			}
		}
	}
	return p, false
}

// inKeptTerminal has the resume of session id (the user's Resume) run in the shell its agent left behind, when that
// shell is at rest in the session's directory cwd (claude --resume finds a session by its folder); otherwise ctx as
// it was, and the resume opens a new tab. The pane is the user's from then on, as one picked for "Run Agent Here":
// a resume that fails leaves it open.
func (n *Node) inKeptTerminal(ctx context.Context, id, cwd string) context.Context {
	if adoptedPane(ctx) != nil {
		return ctx
	}
	p, ok := n.keptPane(id) // in the hosts' last snapshots, which every change in the engine refreshes
	if !ok {
		return ctx
	}
	h, ref, err := n.paneHost(p.PaneID)
	if err != nil {
		return ctx
	}
	launch, err := n.launcherFor(n.startKind(ctx))
	if err != nil {
		return ctx
	}
	if lh, ok := launch.(host.Host); !ok || lh.Kind() != h.Kind() {
		return ctx
	}
	f, ok := h.(host.ForegroundNamer)
	if !ok {
		return ctx
	}
	names, err := f.PaneForeground(ctx, ref)
	if err != nil || len(names) == 0 || !allShells(names) {
		return ctx // something else is in front: someone is using it
	}
	if dir := paneDir(p); cwd != "" && (dir == "" || filepath.Clean(dir) != filepath.Clean(cwd)) {
		return ctx
	}
	log.Printf("resume %s: in the terminal it left (%s)", id, p.PaneID)
	return withAdoptedPane(ctx, &host.Created{SlotID: p.TabID, SpaceID: p.WorkspaceID, Pane: ref}, names)
}

// allShells: every program in front of a pane is a shell.
func allShells(names []string) bool {
	for _, name := range names {
		if !shellProgram(name) {
			return false
		}
	}
	return true
}
