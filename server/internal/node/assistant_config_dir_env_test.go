package node

import (
	"slices"
	"strings"
	"testing"
)

// "default" must leave CLAUDE_CONFIG_DIR unset: Claude reads an empty value as a directory and a resume finds nothing.
func TestClaudeProcessEnvDefaultUnsetsConfigDir(t *testing.T) {
	n := &Node{}
	got := n.claudeProcessEnv([]string{"HOME=/h", "CLAUDE_CONFIG_DIR=/old"}, n.assistantConfigDir("claude", "default"))
	if slices.ContainsFunc(got, func(e string) bool { return strings.HasPrefix(e, "CLAUDE_CONFIG_DIR=") }) {
		t.Fatalf("default kept CLAUDE_CONFIG_DIR: %q", got)
	}
	if !slices.Contains(got, "HOME=/h") {
		t.Fatalf("default dropped other variables: %q", got)
	}
	got = n.claudeProcessEnv([]string{"HOME=/h"}, "/p")
	if !slices.Contains(got, "CLAUDE_CONFIG_DIR=/p") {
		t.Fatalf("a chosen directory is not set: %q", got)
	}
}
