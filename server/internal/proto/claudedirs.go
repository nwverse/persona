package proto

import (
	"path/filepath"
	"slices"
	"strings"
)

// Blocked Claude Code config directories (docs/integrations/claude-integration.md "Blocked config directories"): per machine, patterns the user
// keeps out of Persona — the node neither looks in nor reports a matching directory, the hub does not remember it, and
// its sessions are not listed. Both the hub and the node match with ClaudeDirBlocked.

// CleanClaudeDirPatterns trims spaces and trailing slashes, drops empty ones, ones that would block everything and
// duplicates, and keeps the order.
func CleanClaudeDirPatterns(in []string) []string {
	var out []string
	for _, p := range in {
		p = strings.TrimSpace(p)
		if blocksEverything(p) {
			continue
		}
		p = strings.TrimRight(p, "/")
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}

// blocksEverything: p is empty, or names home, the root or a directory above home however it is spelled ("~/", "~//",
// "//", "/./", "~/..").
func blocksEverything(p string) bool {
	switch c := filepath.Clean(p); {
	case p == "", c == ".", c == "~", c == "/":
		return true
	default:
		return c == ".." || strings.HasPrefix(c, "../")
	}
}

// ValidClaudeDirPattern: a pattern filepath.Match understands, rooted at /, ~/, or starting with *.
func ValidClaudeDirPattern(p string) bool {
	if !strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "~/") && !strings.HasPrefix(p, "*") {
		return false
	}
	_, err := filepath.Match(p, "")
	return err == nil
}

// ClaudeDirBlocked: dir (a CLAUDE_CONFIG_DIR value, a session's cwd, "~/…" or absolute) matches a pattern, or sits
// under a directory that does. A pattern is a path ("~/has_workspaces"), a filepath.Match glob ("~/.automation/*"),
// or a folder name/glob ("*has_workspaces*").
func ClaudeDirBlocked(dir, home string, patterns []string) bool {
	return ClaudeDirBlockedBy(dir, home, patterns) != ""
}

// ClaudeDirBlockedBy is the first pattern that blocks dir ("" when none does), as the user wrote it.
func ClaudeDirBlockedBy(dir, home string, patterns []string) string {
	if dir == "" || len(patterns) == 0 {
		return ""
	}
	d := filepath.Clean(ExpandHome(dir, home))
	for _, raw := range patterns {
		p := filepath.Clean(ExpandHome(raw, home))
		for c := d; ; {
			if ok, _ := filepath.Match(p, c); ok {
				return raw
			}
			if ok, _ := filepath.Match(p, filepath.Base(c)); ok {
				return raw
			}
			parent := filepath.Dir(c)
			if parent == c || parent == "." || parent == "/" || parent == "~" {
				break
			}
			c = parent
		}
	}
	return ""
}

// ClaudeDir is one config directory of a machine as the settings page lists it (GET /api/v1/machines/{mid}/claude-dirs).
type ClaudeDir struct {
	Path    string `json:"path"`              // as Claude Code was given it ("~/…" or absolute)
	Default bool   `json:"default,omitempty"` // the node's own default (~/.claude): never blocked
	Blocked bool   `json:"blocked,omitempty"`
	// BlockedBy: the pattern that blocks it, as written (a glob, or its own path).
	BlockedBy string `json:"blocked_by,omitempty"`
	// Source: "default"; "config" (hub.json machines[].claude_config_dirs); "found" (a running claude used it, now or
	// before: remembered by the hub).
	Source   string `json:"source"`
	Running  int    `json:"running"`            // claudes running from it now (0 when the node did not say)
	Sessions int    `json:"sessions,omitempty"` // transcripts in it (projects/*/*.jsonl)
}
