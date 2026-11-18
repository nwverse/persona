package node

import (
	"context"

	"github.com/nwverse/persona/server/internal/host"
)

// A tab a start opens runs the user's shell, and its startup files run before anything the node types: oh-my-zsh's
// "Would you like to update? [Y/n]" read its key while ~/.zshrc loaded, so the start waited on it and ended with
// shell_waiting (#2570), and every retry opened another tab that stopped on the same question (#10365). What the
// node types (agentenv.go) comes too late for that. So the tab's shell is given startShellEnv as it is made, where
// the engine takes an environment for a new shell (herdr's tab.create / workspace.create env, the Herdr App's
// terminal.run env; Orca and Tern take none yet): the startup frameworks' own switches for their questions. The
// typed start line unsets them again (startupOnlyEnv), so the Agent and the shell the tab keeps run with the
// user's environment. Only start tabs get them: a terminal the user opens, a split, a preview's server never do.

// startShellEnv is the environment of a start tab's shell: oh-my-zsh's update check off (DISABLE_AUTO_UPDATE wins
// over DISABLE_UPDATE_PROMPT; DISABLE_UPDATE_PROMPT alone would update without asking, a git pull in the tab, so
// they always go together; oh-my-bash reads DISABLE_AUTO_UPDATE too) and powerlevel10k's configuration wizard off
// (it would take the typed line as answers and rewrite ~/.zshrc). Nothing that changes what a shell may do.
var startShellEnv = map[string]string{
	"DISABLE_AUTO_UPDATE":                       "true",
	"DISABLE_UPDATE_PROMPT":                     "true",
	"POWERLEVEL9K_DISABLE_CONFIGURATION_WIZARD": "true",
}

// withStartShellEnv is ctx for a Launcher's CreateSpace / CreateSlot of a start's new tab (host.WithSpawnEnv).
func withStartShellEnv(ctx context.Context) context.Context {
	return host.WithSpawnEnv(ctx, startShellEnv)
}
