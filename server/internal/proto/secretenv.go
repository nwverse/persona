package proto

import (
	"regexp"
	"slices"
	"strings"
	"time"
)

// secret.stage: values a provider extension resolved for one Agent start, after the user approved the hand-off card,
// kept by that computer's node until the start's wrapper fetches them (docs/extensions/secret-providers.md "The
// hand-off to one Agent run", docs/protocol/protocol.md "hub ↔ node"). Only the manager that connects the computer
// sends it, over its own connection: the hub never carries a value.

// MethodSecretStage is the node method.
const MethodSecretStage = "secret.stage"

// Limits on one hand-off, the same for the manager that asks and the node that keeps them.
const (
	SecretEnvMost       = 8               // variables in one start
	SecretEnvValueMost  = 16 << 10        // bytes in one value (a provider's values are at most this, extension.SecretMost)
	SecretEnvValueLeast = 8               // bytes: a shorter value could not be told apart from ordinary output to scrub it
	SecretStageKeep     = 2 * time.Minute // a staged ticket is good for at most this long
)

// SecretEnvVar is one variable of a hand-off. Value is base64 in JSON; whoever builds or decodes it zeroes the slice
// once it is no longer needed.
type SecretEnvVar struct {
	Name  string `json:"name"`
	Value []byte `json:"value"`
}

// SecretStageParams are secret.stage's params: Check alone asks whether the node can take a hand-off at all.
type SecretStageParams struct {
	Check bool           `json:"check,omitempty"`
	Env   []SecretEnvVar `json:"env,omitempty"`
}

// SecretStageResult is secret.stage's answer: the one-time ticket the start carries (session.start's secret_ticket),
// good until ExpiresAt. A Check answers neither.
type SecretStageResult struct {
	Ticket    string    `json:"ticket,omitempty"`
	ExpiresAt time.Time `json:"expires_at,omitzero"`
}

var secretEnvName = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,63}$`)

// secretEnvReserved are names a hand-off may not set: what the shell, the node and the launcher set for the Agent,
// and what changes how a program is loaded or run.
var secretEnvReserved = []string{"PATH", "HOME", "USER", "LOGNAME", "SHELL", "PWD", "OLDPWD", "TMPDIR", "TERM",
	"LANG", "IFS", "ENV", "BASH_ENV", "ZDOTDIR", "NODE_OPTIONS", "PYTHONPATH", "PYTHONSTARTUP", "PERL5LIB",
	"RUBYOPT", "CLAUDE_CONFIG_DIR", "CODEX_HOME"}

var secretEnvReservedPrefixes = []string{"PERSONA_", "LD_", "DYLD_", "LC_", "HERDR_", "GIT_CONFIG"}

// SecretEnvNameProblem is why name cannot be a hand-off's variable, "" when it can: an environment variable name
// (capital letters, digits, underscores, not starting with a digit, at most 64), none of the reserved ones. The old
// names' prefix is checked by compat.SecretEnvNameProblem, which callers use.
func SecretEnvNameProblem(name string) string {
	if !secretEnvName.MatchString(name) {
		return "it is not an environment variable name (capital letters, digits and _, at most 64)"
	}
	if slices.Contains(secretEnvReserved, name) {
		return "the computer or Persona sets it"
	}
	for _, p := range secretEnvReservedPrefixes {
		if strings.HasPrefix(name, p) {
			return "names starting with " + p + " are the computer's or Persona's"
		}
	}
	return ""
}
