package node

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/nwverse/persona/server/internal/identity"
)

// claudeOnboardingReady judges this launch's environment and shape, not the
// node's environment or an unrelated onboarded profile. Headless runs never need
// the interactive first-run screens. Unknown files do not establish a blocker.
func claudeOnboardingReady(env []string, interactive bool) (ready, known bool) {
	if !interactive {
		return true, true
	}
	path := claudeGlobalConfig(env)
	if !modernClaudeConfig(path) {
		// The legacy .config.json belongs to old Claude Code builds with another onboarding flow; only the modern
		// .claude.json (and the custom-OAuth variant) carries hasCompletedOnboarding.
		return true, false
	}
	return claudeOnboardingVerdict(path)
}

// modernClaudeConfig reports whether path is a global config that carries hasCompletedOnboarding.
func modernClaudeConfig(path string) bool {
	base := filepath.Base(path)
	return base == ".claude.json" || base == ".claude-custom-oauth.json"
}

// claudeOnboardingVerdict reads one global config file. A missing file means claude has never run there: the
// first-run screens are certain. Any other read failure, or malformed JSON, is unknown rather than a verdict.
func claudeOnboardingVerdict(path string) (ready, known bool) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, true
	}
	if err != nil {
		return true, false
	}
	var cfg struct {
		HasCompletedOnboarding bool `json:"hasCompletedOnboarding"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return true, false
	}
	return cfg.HasCompletedOnboarding, true
}

// onboardingBlockHint names an unfinished first-run setup when the global config says so, else "". It turns the
// interactive start's generic readiness timeout into an error that says what to do.
func (n *Node) onboardingBlockHint(ctx context.Context, launcher string) string {
	env := n.claudeProcessEnv(os.Environ(), n.claudeAgentConfigDir())
	if l, managed := n.managedLauncher(launcher); managed {
		env = identity.Environment(env, managedEnv(l)...)
	} else {
		p, err := n.launcherProbe(ctx, launcher)
		if err != nil || p.Agent != "claude" {
			return "" // an unseen wrapper may select a different profile
		}
		env = identity.Environment(env, envList(p.Shell)...)
		if launcher == "claude" && n.claudeAgentConfigDir() != "" {
			env = n.claudeProcessEnv(env, n.claudeAgentConfigDir())
		}
		env = identity.Environment(env, envList(p.Env)...)
	}
	ready, known := claudeOnboardingReady(env, true)
	if known && !ready {
		return "this machine's Claude Code has not completed its first-run setup (hasCompletedOnboarding is unset): " +
			"run claude once in a terminal on that machine and finish its theme and sign-in screens"
	}
	return ""
}
