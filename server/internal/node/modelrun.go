package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
	"github.com/nwverse/persona/server/internal/shellcmd"
)

// model.run (docs/assistant/model-config.md "Running API purposes on a launcher", #5823): an API purpose's request
// answered by a launcher's CLI on this computer, with the user's own login here. Run as the launcher check runs
// (launchercheck.go): headless, once, in a temp directory of its own that the session list leaves out and that goes
// afterwards, with no tools, no MCP servers, no hooks and no session kept. The prompt and the answer are never logged.

// modelRunTimeout bounds a run when the manager names no time, and modelRunMax the most it may name, should its
// rpc.cancel never come.
const (
	modelRunTimeout = 2 * time.Minute
	modelRunMax     = 10 * time.Minute
)

// modelRunSlots: at most this many runs at a time on this computer; the others wait within their own time.
// ponytail: a fixed two, a setting if someone's computer takes more.
var modelRunSlots = make(chan struct{}, 2)

// modelRun answers model.run. params are zeroed once read.
func (n *Node) modelRun(ctx context.Context, params json.RawMessage) (any, error) {
	var p proto.ModelRunParams
	err := json.Unmarshal(params, &p)
	clear(params)
	if err != nil {
		return nil, errors.New("model.run: bad params") // the decoder's error is not repeated: it may quote them
	}
	if p.Launcher != "claude" && !shellcmd.NameRe.MatchString(p.Launcher) {
		return nil, fmt.Errorf("model.run: invalid launcher %q", p.Launcher)
	}
	if err := n.runnableLauncher(ctx, p.Launcher, p.Configured); err != nil {
		return nil, err // a word that is no launcher here would run as a command
	}
	if err := (modelExecution{Model: p.Model, Effort: p.Effort}).check(); err != nil {
		return nil, err
	}
	timeout := modelRunTimeout
	if p.TimeoutMS > 0 {
		timeout = min(time.Duration(p.TimeoutMS)*time.Millisecond, modelRunMax)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	timedOut := &rpc.Error{Code: proto.CodeModelRunTimeout, Message: fmt.Sprintf("model.run: no answer in %s", timeout.Round(time.Second))}
	select {
	case modelRunSlots <- struct{}{}:
		defer func() { <-modelRunSlots }()
	case <-ctx.Done():
		return nil, timedOut
	}
	agent := n.launcherAgent(ctx, p.Launcher)
	_, managed := n.managedLauncher(p.Launcher)
	if _, runtime := n.runtimeLauncherModels(p.Launcher); (runtime && !managed) || (agent != "claude" && agent != "codex") {
		return nil, fmt.Errorf("model.run: %s runs %s, which can't answer these requests: choose a Claude Code or Codex launcher", p.Launcher, agent)
	}
	dir, err := os.MkdirTemp("", launcherCheckDir) // a check's name: the session list drops what a run leaves (launchercheck.go)
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	write := func(name, text string) (string, error) {
		path := filepath.Join(dir, name)
		return path, os.WriteFile(path, []byte(text), 0o600)
	}
	var args, answer string
	prompt := p.Prompt
	if agent == "claude" {
		defer n.removeCheckProject(dir)
		args = ` -p --output-format json --max-turns 1 --no-session-persistence --tools '' --strict-mcp-config`
		if p.System != "" {
			path, err := write("system.md", p.System)
			if err != nil {
				return nil, err
			}
			args += " --system-prompt-file " + scriptPath(path)
		}
		if p.Model != "" {
			args += " --model " + shellQuote(p.Model)
		}
		if p.Effort != "" {
			args += " --effort " + p.Effort
		}
		ours, err := write("settings.json", `{"disableAllHooks":true}`)
		if err != nil {
			return nil, err
		}
		args += scriptSettingsFlag(n.claudeSettings(ctx, p.Launcher, ours, dir))
	} else { // Codex has no system prompt flag: it goes before the conversation
		if p.System != "" {
			prompt = p.System + "\n\n" + prompt
		}
		answer = filepath.Join(dir, "answer.md")
		args = " exec --ephemeral --skip-git-repo-check --sandbox read-only --color never -o " + scriptPath(answer)
		if p.Model != "" {
			args += " -m " + shellQuote(p.Model)
		}
		if p.Effort != "" {
			args += " -c model_reasoning_effort=" + p.Effort
		}
		args += " -"
	}
	in, err := write("prompt.md", prompt)
	if err != nil {
		return nil, err
	}
	cmd := loginShell(ctx, "cd "+scriptPath(dir)+" && "+n.launcherForScript(p.Launcher)+args+" < "+scriptPath(in))
	killGroupOnCancel(cmd)
	raw, runErr := cmd.CombinedOutput()
	text := strings.TrimSpace(string(raw))
	if ctx.Err() != nil {
		return nil, timedOut
	}
	if agent == "claude" {
		if r, ok := claudeResult(text); ok && !r.isError {
			return proto.ModelRunAnswer{Text: r.result, Model: r.model}, nil
		} else if ok {
			text = r.result
		}
	} else if b, err := os.ReadFile(answer); runErr == nil && err == nil {
		return proto.ModelRunAnswer{Text: strings.TrimSpace(string(b))}, nil
	}
	if low := strings.ToLower(text); strings.Contains(low, "not logged in") || strings.Contains(low, "run /login") || strings.Contains(low, "codex login") {
		return nil, &rpc.Error{Code: proto.CodeModelRunLoggedOut, Message: "model.run: " + p.Launcher + " is not logged in here"}
	}
	msg := checkOutput(text)
	if msg == "" && runErr != nil {
		msg = clipText(redactCheck(runErr.Error()), 400)
	}
	return nil, errors.New("model.run: " + msg)
}
