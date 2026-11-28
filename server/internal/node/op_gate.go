package node

// The 1Password CLI inside the sessions Persona starts (#17679, the follow-up of the 1Password extension's quiet asks).
// 1Password authorizes `op` per process chain: the resident node and its extension hold one approval, and any Agent,
// terminal or script that starts `op` itself is another chain, so a Mac without Touch ID leaves an approval dialog
// nobody can answer until it expires. An Agent Persona starts may run `op` in Bash; the sessions Persona starts load
// the PreToolUse hooks (doctor.go hookSettingsForOS), and this decides those commands here, at once, with no card. The
// calls that never prompt (`op --version`, `op whoami`) pass, and so does everything when a service-account token is
// set; the rest is refused with the node and extension tools that keep the secret out of the model's context. What a
// script does once it runs, the sessions the user starts themselves (their global hooks only report, globalhooks.go),
// and any other program that starts `op` are not covered; docs/extensions/secret-providers.md says so.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/shellcmd"
)

// opGateOS is the system the gate runs on (tests set it): the per-process approval is the Mac App's, so elsewhere the
// CLI has no such prompt and nothing is refused.
var opGateOS = runtime.GOOS

// opServiceAccountEnv is 1Password's service-account token: it authorizes without the desktop app, so a command that
// carries it never opens a prompt. Persona does not use one, but an owner who does is not blocked.
const opServiceAccountEnv = "OP_SERVICE_ACCOUNT_TOKEN"

// opWrappers run the program named after them (with options of their own first, which are skipped).
var opWrappers = map[string]bool{
	"env": true, "sudo": true, "exec": true, "nohup": true, "time": true, "command": true, "nice": true,
	"timeout": true, "stdbuf": true,
}

// opWrapperValues: the wrapper options that take the next word. An unknown option is skipped alone (the safest reading
// of the command follows it), and an --option=value carries its value already.
var opWrapperValues = map[string]bool{
	"-u": true, "--unset": true, "-C": true, "--chdir": true, "-S": true, "--split-string": true, // env
	"-g": true, "--group": true, "-p": true, "--prompt": true, "-h": true, "--host": true, // sudo
	"-r": true, "--role": true, "-t": true, "--type": true, "-U": true, "--other-user": true, "-D": true,
	"-R": true, "--chroot": true,
	"-a": true, "--argv0": true, // exec
	"-n": true, "--adjustment": true, // nice
	"-s": true, "--signal": true, "-k": true, "--kill-after": true, // timeout
	"-i": true, "-o": true, "-e": true, "--input": true, "--output": true, "--error": true, // stdbuf, time
	"-f": true, "--format": true, // time
}

// opCallText looks like an op command somewhere in a command that shellcmd cannot follow: only then is it refused
// without being read.
var opCallText = lazyre.New(`(?:^|[\s;&|(){}])(?:[^\s;&|(){}]*/)?op(?:\s|$)`)

// opCall is one `op …` a Bash command runs; serviceAccount is set when its segment carries OP_SERVICE_ACCOUNT_TOKEN.
type opCall struct {
	words          []string
	serviceAccount bool
}

// opCalls is the `op …` commands a Bash command runs: op as the program of a simple command (after variable assignments
// and wrappers such as env, sudo or timeout), inside sh / bash / zsh -c, or in a command substitution. A mention of op
// as an argument (grep, a commit message) is none. ok is false when a command that looks like an op call cannot be
// followed (a heredoc, a subshell, unbalanced quotes): the gate then refuses rather than guess. This is a best effort:
// a script can start op in ways a command line does not show.
func opCalls(cmd string) (calls []opCall, ok bool) {
	return opCallsDepth(cmd, false, 0)
}

func opCallsDepth(cmd string, serviceAccount bool, depth int) (calls []opCall, ok bool) {
	if !strings.Contains(cmd, "op") {
		return nil, true
	}
	segs, ok := shellcmd.Split(cmd)
	if !ok {
		return nil, !opCallText.MatchString(cmd)
	}
	for _, s := range segs {
		if depth <= 2 {
			for _, nested := range s.Nested {
				inner, ok := opCallsDepth(nested, serviceAccount, depth+1)
				if !ok {
					return nil, false
				}
				calls = append(calls, inner...)
			}
		}
		words := s.Words
		for {
			for len(words) > 0 && isAssignment(words[0]) {
				if k, v, _ := strings.Cut(words[0], "="); k == opServiceAccountEnv && v != "" {
					serviceAccount = true
				}
				words = words[1:]
			}
			if len(words) == 0 || !opWrappers[filepath.Base(words[0])] {
				break
			}
			name := filepath.Base(words[0])
			words = words[1:]
			for len(words) > 0 && strings.HasPrefix(words[0], "-") {
				w := words[0]
				words = words[1:]
				if strings.Contains(w, "=") {
					continue // --option=value
				}
				if opWrapperValues[w] && len(words) > 0 {
					words = words[1:]
				}
			}
			switch name {
			case "timeout":
				if len(words) > 0 {
					words = words[1:] // the duration before the program
				}
			case "env":
				for len(words) > 0 && isAssignment(words[0]) {
					if k, v, _ := strings.Cut(words[0], "="); k == opServiceAccountEnv && v != "" {
						serviceAccount = true
					}
					words = words[1:]
				}
			}
		}
		if len(words) == 0 {
			continue
		}
		switch filepath.Base(words[0]) {
		case "sh", "bash", "zsh":
			for i := 1; i+1 < len(words); i++ {
				if words[i] == "-c" || (strings.HasPrefix(words[i], "-") && strings.HasSuffix(words[i], "c") && !strings.HasPrefix(words[i], "--")) {
					if depth > 2 {
						return nil, !opCallText.MatchString(words[i+1])
					}
					inner, ok := opCallsDepth(words[i+1], serviceAccount, depth+1)
					if !ok {
						return nil, false
					}
					calls = append(calls, inner...)
					break
				}
			}
			continue
		}
		if filepath.Base(words[0]) == "op" {
			calls = append(calls, opCall{words: words, serviceAccount: serviceAccount})
		}
	}
	return calls, true
}

// opGateApplies: the gate is the Mac App's approval, and a service-account token in the hook's own environment
// authorizes without it.
func opGateApplies() bool {
	return opGateOS == "darwin" && os.Getenv(opServiceAccountEnv) == ""
}

// opBashVerdict is why a Bash command running op is refused, "" to let it go.
func opBashVerdict(cmd string) string {
	if !opGateApplies() {
		return ""
	}
	calls, ok := opCalls(cmd)
	if !ok {
		return "Refused: Persona could not follow this command's op call to check it. Run op as a plain command " +
			"(op vault list), or use the onepassword_* tools."
	}
	for _, call := range calls {
		if call.serviceAccount || opAllowed(call.words[1:]) {
			continue
		}
		return "Refused: an Agent Persona starts must not run the 1Password CLI directly. 1Password authorizes op per " +
			"process chain, so a call from this Agent's shell asks for an approval nobody may be able to answer. " +
			"Use onepassword_vaults, onepassword_items or onepassword_item to look up a reference, agent_start with " +
			"secret_env to read a secret, or `personad tool secret_store --from-file -` to write one."
	}
	return ""
}

// opAllowed: the calls that never open an approval. Everything else is refused.
func opAllowed(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "--version", "-v", "version", "whoami":
		return true
	}
	return false
}
