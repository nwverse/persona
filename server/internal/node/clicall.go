package node

import (
	"context"
	"crypto/sha256"
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

// ancestorsMost bounds the walk up the process tree.
const ancestorsMost = 64

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
	roots := map[int]string{}
	n.mu.Lock()
	for id, p := range n.procs {
		if p.cmd != nil && p.cmd.Process != nil {
			roots[p.cmd.Process.Pid] = id
		}
	}
	n.mu.Unlock()
	for range ancestorsMost {
		if id, ok := roots[pid]; ok {
			return id, true
		}
		next, err := parent(pid)
		if err != nil || next <= 1 || next == pid {
			return "", false
		}
		pid = next
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
	if req.Name == toolMailSaveAttachment { // writes on this machine (mailattachment.go)
		return n.saveMailAttachment(ctx, req.Args, session, true, false)
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
