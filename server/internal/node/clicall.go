package node

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"maps"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nwverse/persona/server/internal/assistantskills"
	"github.com/nwverse/persona/server/internal/proto"
)

// A connector's command line (personad tool, internal/toolcli) reaches the hub through the node's socket like the MCP
// server, but the node, not the caller, says whether the assistant is asking: only a process the node started for the assistant,
// or one of its descendants (its Bash tool), gets the assistant's tools. Any other caller (an agent, a program run by
// hand) is refused; so is a system where the other end of a socket cannot be known.
//
// The MCP server's requests (mcp.tools, mcp.call) are decided the same way (mcpAssistant): its PERSONA_ASSISTANT is
// repeated by every process started with the assistant's environment, an agent in an engine started from the
// assistant's Bash tool among them.

// ancestorsMost bounds the walk up the process tree.
const ancestorsMost = 64

// errNoPeer: this system does not tell the process at the other end of a socket (peer_other.go).
var errNoPeer = errors.New("the process at the other end of a socket is not known on this system")

// assistantCaller is the assistant conversation whose process is c's peer or an ancestor of it.
func (n *Node) assistantCaller(c net.Conn) (string, bool) {
	pid, err := peerPID(c)
	if err != nil {
		return "", false
	}
	return n.assistantOf(pid, parentPID)
}

// assistantOf is the assistant conversation whose process is pid or an ancestor of it (parent gives a process's).
func (n *Node) assistantOf(pid int, parent func(int) (int, error)) (string, bool) {
	return n.assistantIn(lineage(pid, parent))
}

// assistantIn is the assistant conversation whose process is one of pids, the first one found.
func (n *Node) assistantIn(pids []int) (string, bool) {
	roots := n.assistantRoots()
	for _, p := range pids {
		if id, ok := roots[p]; ok {
			return id, true
		}
	}
	return "", false
}

// lineage is pid and its ancestors, nearest first, as far as parent tells: up to ancestorsMost, init not included.
func lineage(pid int, parent func(int) (int, error)) []int {
	var out []int
	for range ancestorsMost {
		out = append(out, pid)
		next, err := parent(pid)
		if err != nil || next <= 1 || next == pid {
			break
		}
		pid = next
	}
	return out
}

// nodeLineage is this node's process and its ancestors (a variable for tests, whose runtimes run in the node's
// process).
var nodeLineage = func() []int { return lineage(os.Getpid(), parentPID) }

// assistantRoots are the processes this node runs for the assistant, by pid, each with the session its calls go to
// the hub with: a conversation's, the warm spare's (its MCP server lists the tools as it starts, before a conversation
// adopts it), and a Codex process's key until its thread exists (the key its MCP server says, resolveAssistantKey).
func (n *Node) assistantRoots() map[int]string {
	roots := map[int]string{}
	add := func(id string, p *assistantProc) {
		if p == nil || p.cmd == nil || p.cmd.Process == nil {
			return
		}
		if id == "" && p.cx != nil {
			id = p.cx.key
		}
		if id != "" {
			roots[p.cmd.Process.Pid] = id
		}
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	for id, p := range n.procs {
		add(id, p)
	}
	for _, p := range n.akeys {
		add(p.id, p)
	}
	if n.spare != nil {
		add(n.spare.id, n.spare)
	}
	return roots
}

// mcpSession is the session and the assistant's word an MCP server's request (mcp.tools, mcp.call) goes to the hub
// with. The server's word that it is the assistant's is taken only when the node says so too (mcpAssistant); a
// request it refuses goes as an agent's, with the session it names. A request that does not say so is not made one.
func (n *Node) mcpSession(c net.Conn, req sockRequest) (string, bool) {
	if !req.Assistant {
		return req.SessionID, false
	}
	if id, ok := n.mcpAssistant(c, req.SessionID); ok {
		return id, true
	}
	n.logf("%s: the caller says it is the assistant, but its process is not one Persona runs for the assistant: answered as an agent's", req.Kind)
	return req.SessionID, false
}

// mcpAssistant is the session of the assistant conversation whose MCP server is at the other end of c (session: the
// one the request names), or false.
func (n *Node) mcpAssistant(c net.Conn, session string) (string, bool) {
	pid, err := peerPID(c)
	return n.mcpAssistantOf(pid, err, parentPID, session)
}

// mcpAssistantOf is mcpAssistant for the process pid (err: why it is not known; parent gives a process's):
//   - pid or an ancestor is a process this node runs for the assistant: that conversation's session, whatever the
//     request names;
//   - pid or an ancestor is a connected runtime that runs assistant conversations (caps.assistant): they run in the
//     runtime's processes, not the node's (assistant_runtime.go), and the runtime names their sessions, so the request's;
//   - the system does not tell who is at the other end of a socket (errNoPeer): the request's session, if it is one
//     this node runs for the assistant or an assistant conversation of a runtime.
//
// Any other caller is not the assistant.
func (n *Node) mcpAssistantOf(pid int, err error, parent func(int) (int, error), session string) (string, bool) {
	if errors.Is(err, errNoPeer) {
		if session == "" {
			return "", false
		}
		for _, id := range n.assistantRoots() {
			if id == session {
				return session, true
			}
		}
		if n.runtimeAssistant(session) {
			return session, true
		}
		return "", false
	}
	if err != nil {
		return "", false
	}
	line := lineage(pid, parent)
	if id, ok := n.assistantIn(line); ok {
		return id, true
	}
	if n.runtimes.assistantIn(line) {
		return session, true
	}
	return "", false
}

type assistantPrepareState struct {
	connectors []string
	clis       map[string]string // command name → connector id
}

// assistantPrepareDir gives prepare and running sessions the same key, including symlinked data directories.
func assistantPrepareDir(cwd string) string {
	if cwd == "" {
		return ""
	}
	return realPath(filepath.Clean(cwd))
}

// mayCall says whether this directory's command lines may call tool: one of a connector signed in as of its last
// assistant.prepare ("<id>_…", the connector's own tools and its <id>_cli).
func (n *Node) mayCall(cwd, tool string) bool {
	return slices.ContainsFunc(n.signedInConnectors(cwd), func(id string) bool { return strings.HasPrefix(tool, id+"_") })
}

// cliCall runs one of the command lines' calls (personad tool) on the hub as the assistant's, or refuses
// it.
func (n *Node) cliCall(ctx context.Context, c net.Conn, req sockRequest) proto.ToolResult {
	session, ok := n.assistantCaller(c)
	if !ok {
		return proto.ErrorResult(proto.ToolErrorUnavailable, "这个命令只能在助手的对话里用：这个进程不是 Persona 为助手起的", 0)
	}
	n.mu.Lock()
	cwd := ""
	if p := n.procs[session]; p != nil {
		cwd = p.cwd
	}
	n.mu.Unlock()
	if !n.mayCall(cwd, req.Name) {
		return proto.ErrorResult(proto.ToolErrorUnavailable, req.Name+" is not available: the command line calls only the tools of the hub's connectors that are signed in", 0)
	}
	if n.peer == nil {
		return proto.ErrorResult(proto.ToolErrorUnavailable, "Persona hub error: not connected", 0)
	}
	var res proto.ToolResult
	if err := n.peer.Call(ctx, "mcp.call", map[string]any{"name": req.Name, "args": req.Args, "session_id": session, "assistant": true}, &res); err != nil {
		return proto.ErrorResult(proto.ToolErrorUnavailable, "Persona hub error: "+err.Error(), 0)
	}
	return res
}

// signedInConnectors are this directory's signed-in connectors as of its last assistant.prepare: whose built-in
// skills the assistant gets (assistantskills.Requires). An unprepared directory gets none.
func (n *Node) signedInConnectors(cwd string) []string {
	key := assistantPrepareDir(cwd)
	n.mu.Lock()
	defer n.mu.Unlock()
	return slices.Clone(n.assistantPrepared[key].connectors)
}

// syncAssistantSkills puts the assistant's skills in its directory (cwd): the user's, and the built-in ones whose
// connector is signed in; a connector's command line is on the assistant's PATH while it is. A runtime's directory (.agents/skills) is
// kept the same once it is there.
func (n *Node) syncAssistantSkills(cwd string) {
	conns := n.signedInConnectors(cwd)
	dirs := []string{filepath.Join(cwd, ".claude", "skills")}
	if st, err := os.Stat(filepath.Join(cwd, ".agents", "skills")); err == nil && st.IsDir() {
		dirs = append(dirs, filepath.Join(cwd, ".agents", "skills"))
	}
	for _, d := range dirs {
		if err := assistantskills.Sync(d, assistantskills.UserDir(), conns, n.logger.Printf); err != nil {
			n.logger.Printf("assistant: skills: %v", err)
		}
	}
	n.writeCLIs(cwd, n.connectorCLIs(cwd))
}

// connectorCLIs are this directory's signed-in connectors' command lines as of its last assistant.prepare.
func (n *Node) connectorCLIs(cwd string) map[string]string {
	key := assistantPrepareDir(cwd)
	n.mu.Lock()
	defer n.mu.Unlock()
	return maps.Clone(n.assistantPrepared[key].clis)
}

// assistantBin keeps the default assistant's existing PATH; every other directory gets its own wrappers so preparing
// one manager cannot remove another's commands or change which connector a shared command name calls.
func (n *Node) assistantBin(cwd string) string {
	key := assistantPrepareDir(cwd)
	if key == assistantPrepareDir(n.assistantDirOf(".persona/assistant")) {
		return filepath.Join(n.dir, "assistant-bin")
	}
	return filepath.Join(n.dir, "assistant-bins", fmt.Sprintf("%x", sha256.Sum256([]byte(key))))
}

// cliMark is in every command line the node writes on the assistant's PATH: one it wrote and no longer has a
// connector for goes, other files there (a plugin's gate commands) stay.
const cliMark = "# a connector's command line, on the assistant's PATH while it is signed in (node/clicall.go)"

// writeCLIs puts the signed-in connectors' command lines on the assistant's PATH (personad tool --cli <id>) and takes
// away those whose connector is not, and the built-in Telegram's tg (personad tg) it wrote before Telegram became an
// extension (#1704).
func (n *Node) writeCLIs(cwd string, clis map[string]string) {
	bin := n.assistantBin(cwd)
	want := map[string]string{}
	for name, id := range clis {
		want[name] = "#!/bin/sh\n" + cliMark + "\nexec " + shellQuote(n.bin) + " tool --cli " + shellQuote(id) + " \"$@\"\n"
	}
	entries, _ := os.ReadDir(bin)
	for _, e := range entries {
		if _, keep := want[e.Name()]; keep {
			continue
		}
		p := filepath.Join(bin, e.Name())
		if b, err := os.ReadFile(p); err == nil && (strings.Contains(string(b), cliMark) || strings.Contains(string(b), "Telegram through the hub")) {
			_ = os.Remove(p)
		}
	}
	if len(want) == 0 {
		return
	}
	_ = os.MkdirAll(bin, 0o755)
	for name, wrapper := range want {
		p := filepath.Join(bin, name)
		if cur, err := os.ReadFile(p); err == nil && string(cur) == wrapper {
			continue
		}
		_ = os.WriteFile(p, []byte(wrapper), 0o755)
	}
}
