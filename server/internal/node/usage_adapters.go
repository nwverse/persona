package node

import (
	"path/filepath"
	"strings"

	"github.com/nwverse/persona/server/internal/tokenuse"
)

// Where each agent keeps its token usage and how to read it (docs/server/machines.md "Token usage"): an adapter per
// agent says which files hold it, which session a file belongs to (its launcher is the session's) and parses its lines
// (tokenuse). The usage meter and the backfill read every adapter's files the same way: what a file gained since the
// last scan, from about the window's start in a big one, a request counted once across files.

// usageAdapter is one agent's records of token usage.
type usageAdapter interface {
	// agent is the agent the records are of (proto.Session.Agent's names).
	agent() string
	// paths are the files that may hold usage; usageFiles keeps those changed since its cutoff.
	paths(n *Node) []string
	// session is the session (or thread) the file at path belongs to.
	session(path string) string
	// parser reads one file's lines.
	parser() tokenuse.Parser
}

// usageAdapters are the agents whose usage this node meters, in the order their files are read (a request in two
// agents' files is counted in the first one's).
func (n *Node) usageAdapters() []usageAdapter {
	return []usageAdapter{claudeUsage{}, codexUsage{tokenuse.Codex, (*Node).codexHome}, codexUsage{tokenuse.Traex, (*Node).traexHome}}
}

// claudeUsage: Claude Code's transcripts in every config directory, their subagents' and their workflow runs' agents'
// (projectTranscripts).
type claudeUsage struct{}

func (claudeUsage) agent() string              { return tokenuse.Claude }
func (claudeUsage) paths(n *Node) []string     { return n.projectTranscripts() }
func (claudeUsage) parser() tokenuse.Parser    { return tokenuse.ClaudeParser() }
func (claudeUsage) session(path string) string { return claudeUsageSession(path) }

// claudeUsageSession is a Claude Code transcript's session: its name (<id>.jsonl), the session folder over a
// subagent's (<id>/subagents/agent-….jsonl) or a workflow run's agent's (<id>/subagents/workflows/<run>/agent-….jsonl).
func claudeUsageSession(path string) string {
	if dir := filepath.Dir(path); filepath.Base(dir) == "subagents" {
		return filepath.Base(filepath.Dir(dir))
	}
	if runs := filepath.Dir(filepath.Dir(path)); filepath.Base(runs) == "workflows" && filepath.Base(filepath.Dir(runs)) == "subagents" {
		return filepath.Base(filepath.Dir(filepath.Dir(runs)))
	}
	return strings.TrimSuffix(filepath.Base(path), ".jsonl")
}

// codexUsage: the rollouts of Codex or of a CLI that writes Codex's (traex), under its home.
type codexUsage struct {
	name string
	home func(*Node) string
}

func (c codexUsage) agent() string           { return c.name }
func (c codexUsage) parser() tokenuse.Parser { return tokenuse.CodexParser(c.name) }

func (c codexUsage) paths(n *Node) []string {
	rs := rollouts(c.home(n))
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.path
	}
	return out
}

// session is a rollout's thread id: its name ends with it.
func (codexUsage) session(path string) string {
	name := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	if strings.HasPrefix(name, "rollout-") && len(name) >= 36 {
		return name[len(name)-36:]
	}
	return name
}
