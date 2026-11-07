package node

import (
	"context"
	"github.com/nwverse/persona/server/internal/compat"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/identity"
	"github.com/nwverse/persona/server/internal/proto"
)

// The Claude assistant gets the extensions' MCP servers (mcp_servers, docs/extensions/extensions.md "MCP servers"): a command line
// on this machine, found on PATH or in ~/bin (where a wrapper usually lives and a service's PATH often misses), or at
// the path its Env variable names. Each is added as `<command> <args…>` with no environment of Persona's: the command
// reads its own configuration and Persona never reads or passes its credentials.
//
//   - Allow run without asking;
//   - Deny are never callable (--disallowedTools), whatever the command's own settings say;
//   - any other tool goes by the conversation's permission mode (asks on the phone by default, runs without asking
//     under bypassPermissions).
//
// A command whose `--help` does not mention every word of Requires is left out, so an older build without the
// tools the extension expects is not wired in by accident.

var (
	mcpHelpMu    sync.Mutex
	mcpHelpCache = map[string]mcpHelpEntry{}
)

type mcpHelpEntry struct {
	modTime  time.Time
	size     int64
	requires string
	ok       bool
}

// assistantMCPBin finds s's command on this machine: its Env variable when set (falling back from PERSONA_ to
// PERSONA_ via identity.LookupEnv; "" / 0 / off / false leaves the server out), then PATH, then ~/bin. "" when it is
// not there, turned off, or lacks what Requires asks for.
func assistantMCPBin(s proto.AssistantMCPServer) string {
	if s.Env != "" {
		if raw, ok := identity.LookupEnv(s.Env); ok {
			p := strings.TrimSpace(raw)
			if p == "" || p == "0" || strings.EqualFold(p, "off") || strings.EqualFold(p, "false") {
				return ""
			}
			if mcpHasRequired(p, s.Requires) {
				return p
			}
			return ""
		}
	}
	if s.Command == "" || strings.ContainsAny(s.Command, `/\`) {
		return ""
	}
	if p, err := exec.LookPath(s.Command); err == nil && mcpHasRequired(p, s.Requires) {
		return p
	}
	if home, err := os.UserHomeDir(); err == nil {
		if p := filepath.Join(home, "bin", s.Command); mcpHasRequired(p, s.Requires) {
			return p
		}
	}
	return ""
}

// mcpHasRequired checks that p is an executable whose `--help` output mentions every word of requires. Cached by
// path, mtime, size and the words.
func mcpHasRequired(p string, requires []string) bool {
	st, err := os.Stat(p)
	if err != nil || st.IsDir() || st.Mode()&0o111 == 0 {
		return false
	}
	if len(requires) == 0 {
		return true
	}
	key := strings.Join(requires, "\x00")
	mcpHelpMu.Lock()
	if e, ok := mcpHelpCache[p]; ok && e.modTime.Equal(st.ModTime()) && e.size == st.Size() && e.requires == key {
		mcpHelpMu.Unlock()
		return e.ok
	}
	mcpHelpMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(ctx, p, "--help").CombinedOutput()
	ok := true
	for _, w := range requires {
		if !strings.Contains(string(out), w) {
			ok = false
			break
		}
	}

	mcpHelpMu.Lock()
	mcpHelpCache[p] = mcpHelpEntry{modTime: st.ModTime(), size: st.Size(), requires: key, ok: ok}
	mcpHelpMu.Unlock()
	return ok
}

// assistantMCPEnv pins Persona's socket root for MCP clients that sanitize the
// parent environment. The process key still resolves to this node's conversation.
func assistantMCPEnv(id, home string) map[string]string {
	env := map[string]string{"PERSONA_ASSISTANT": "1", "PERSONA_SESSION_ID": id}
	if home != "" {
		env["PERSONA_HOME"] = home
	}
	return env
}

// assistantMCPServers are the mcpServers entries of the servers in cfg whose command is on this machine, by id, and
// what they allow and deny as tool rules (mcp__<id>__<tool>). The persona server's own name is never taken.
func assistantMCPServers(cfg []proto.AssistantMCPServer) (servers map[string]any, allow, deny []string) {
	servers = map[string]any{}
	for _, s := range cfg {
		if s.ID == "" || compat.IsMCPServer(s.ID) || servers[s.ID] != nil {
			continue
		}
		bin := assistantMCPBin(s)
		if bin == "" {
			continue
		}
		args := s.Args
		if args == nil {
			args = []string{}
		}
		servers[s.ID] = map[string]any{"command": bin, "args": args}
		for _, t := range s.Allow {
			allow = append(allow, "mcp__"+s.ID+"__"+t)
		}
		for _, t := range s.Deny {
			deny = append(deny, "mcp__"+s.ID+"__"+t)
		}
	}
	return servers, allow, deny
}
