package node

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/shellcmd"
)

const (
	codexMultiAgentFlag = "features.multi_agent_v2=true"
	// Launch adds the override before the separator in the final managed argv. The same process must run the
	// executable that was checked, even when the pane's PATH differs from the node's.
	codexMultiAgentExecEnv      = "PERSONA_CODEX_MULTI_AGENT_EXEC"
	codexMultiAgentProbeTimeout = 4 * time.Second
)

func codexMultiAgentRefusal(ctx context.Context, key string) error {
	return ultracodeUnsupported(i18n.T(i18n.From(ctx), "node-codex-start."+key))
}

// prepareCodexMultiAgent verifies a fresh process's override before any terminal or backend launch receipt is made.
// The returned argv either applies it directly or carries the decision to the managed wrapper's final exec.
func (n *Node) prepareCodexMultiAgent(ctx context.Context, cwd, launcher string, launch host.Launcher, argv []string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, codexMultiAgentProbeTimeout)
	defer cancel()
	extra := []string{agentMarkEnv}
	extra = append(extra, codexPaneEnv(launch)...)
	words := argv
	for len(words) > 0 && shellcmd.AssignRe.MatchString(words[0]) {
		extra = append(extra, words[0])
		words = words[1:]
	}
	if len(words) == 0 {
		return nil, codexMultiAgentRefusal(ctx, "multi_agent_launcher")
	}
	var path string
	optionArgs := words[1:]
	managed := len(words) >= 3 && slices.Equal(words[:3], []string{n.bin, "launch", launcher})
	if managed {
		l, ok := n.managedLauncher(launcher)
		if !ok || l.AgentType() != "codex" {
			return nil, codexMultiAgentRefusal(ctx, "multi_agent_launcher")
		}
		if l.Withheld {
			if err := n.ensureManaged(ctx, launcher); err != nil {
				return nil, err
			}
			l, _ = n.managedLauncher(launcher)
			if l.AgentType() != "codex" || l.Withheld {
				return nil, codexMultiAgentRefusal(ctx, "multi_agent_launcher")
			}
		}
		env := managedEnv(l)
		optionArgs = launchArgv("codex", words[3:], l.Args, "", l.Model, env, "")[1:]
		if runtime.GOOS == "windows" {
			path, _ = agentExecutable("codex")
		} else {
			path = codexPaneExecutable(ctx, cwd, append(slices.Clone(startShellEnv), extra...))
			if path == "" { // Managed launchers call the executable even when the shell shadows its name.
				path = agentExecutableOnDisk("codex")
			}
		}
		extra = append(extra, env...)
	} else if words[0] == "codex" && runtime.GOOS != "windows" {
		path = codexPaneExecutable(ctx, cwd, append(slices.Clone(startShellEnv), extra...))
	} else {
		path = words[0]
		if !filepath.IsAbs(path) {
			if strings.ContainsRune(path, filepath.Separator) {
				path = filepath.Join(cwd, path)
			} else {
				path, _ = exec.LookPath(path)
			}
		}
	}
	probe, ok := codexMultiAgentProbeArgs(optionArgs)
	if !ok || path == "" {
		return nil, codexMultiAgentRefusal(ctx, "multi_agent_launcher")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	env := append(withoutEnv(os.Environ(), append(agentUnsetNames(), envNames(extra)...)), extra...)
	exe, env, err := codexStartProgram(path, true, env)
	if err != nil || !codexMultiAgentProbe(ctx, exe, cwd, env, probe) {
		if err := ctx.Err(); errors.Is(err, context.Canceled) {
			return nil, err
		}
		return nil, codexMultiAgentRefusal(ctx, "multi_agent_cli")
	}
	if managed {
		return append([]string{codexMultiAgentExecEnv + "=" + path}, argv...), nil
	}
	argv = slices.Clone(argv)
	argv[len(argv)-len(words)] = path
	return codexMultiAgentArgs(argv), nil
}

func codexMultiAgentArgs(argv []string) []string {
	return insertBeforeDashDash(argv, []string{"-c", codexMultiAgentFlag})
}

// Feature inspection consumes config overrides and feature toggles, but not TUI-only arguments. A custom config
// profile, working directory, or remote server cannot be verified by a local features list with default load options.
func codexMultiAgentProbeArgs(args []string) ([]string, bool) {
	var probe []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			break
		}
		if a == "-" || !strings.HasPrefix(a, "-") {
			return nil, false // A positional word can select a subcommand rather than the terminal session.
		}
		flag, value, attached := a, "", false
		if strings.HasPrefix(a, "--") {
			flag, value, attached = strings.Cut(a, "=")
		} else if strings.HasPrefix(a, "-") && len(a) > 2 {
			flag, value, attached = a[:2], strings.TrimPrefix(a[2:], "="), true
		}
		if !attached && codexValueFlags[flag] && i+1 < len(args) {
			i++
			value = args[i]
		}
		switch flag {
		case "--remote", "--profile", "-p", "--cd", "-C":
			return nil, false
		case "--disable":
			if value == "multi_agent_v2" { // Codex folds feature toggles in after every -c override.
				return nil, false
			}
			fallthrough
		case "-c", "--config", "--enable":
			if value == "" || value == "--" {
				return nil, false
			}
			if (flag == "-c" || flag == "--config") && value == codexMultiAgentFlag {
				continue // The verified override is appended once, after any older config values.
			}
			probe = append(probe, flag, value)
		}
	}
	return append(probe, "-c", codexMultiAgentFlag, "features", "list"), true
}

// The verified executable stays pinned. Check its final environment too, so a terminal with different settings
// or a provider updated during start cannot silently run the task without the feature.
func verifyCodexMultiAgentExec(ctx context.Context, exe, cwd string, env, args []string) error {
	ctx, cancel := context.WithTimeout(ctx, codexMultiAgentProbeTimeout)
	defer cancel()
	probe, ok := codexMultiAgentProbeArgs(args)
	if !ok || !codexMultiAgentProbe(ctx, exe, cwd, env, probe) {
		return errors.New(codexStartError + " multi-agent v2 is unavailable in the final Codex launch environment")
	}
	return nil
}

func codexMultiAgentProbe(ctx context.Context, path, cwd string, env, args []string) bool {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir, cmd.Env, cmd.WaitDelay = cwd, env, 250*time.Millisecond
	killGroupOnCancel(cmd)
	var out workspaceOutput
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil || out.overflow {
		return false
	}
	for line := range bytes.SplitSeq(out.Bytes(), []byte("\n")) {
		fields := bytes.Fields(line)
		if len(fields) == 3 && string(fields[0]) == "multi_agent_v2" {
			return string(fields[1]) == "stable" && string(fields[2]) == "true"
		}
	}
	return false
}
