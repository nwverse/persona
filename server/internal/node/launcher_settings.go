package node

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/nwverse/persona/server/internal/identity"
	"github.com/nwverse/persona/server/internal/proto"
)

// A launcher alias can bring its own settings (`alias work="claude --settings ~/.claude/settings.work.json …"`, with
// the gateway, its token and the model in that file's env). Every claude start Persona makes adds its own --settings
// after the launcher's words (the hooks that bring questions, permission requests and turn ends to the phone), and
// Claude Code keeps only the last --settings it is given: the launcher's file was dropped, and the claude it started
// answered every prompt with "Not logged in". So the start passes one file that is both: the launcher's settings
// with Persona's hooks added to its own, as the last --settings. That is for a claude that does not load plugins: one
// that does gets the hooks as a plugin (hooksplugin.go), and nothing of the launcher's is replaced, a function's
// --settings too, which no merge can see.

// pluginWorktreeCwd identifies a working directory below the node's disposable plugin root. The scope helpers
// resolve symlinks on both sides so a link out of the root keeps the user's normal startup behavior.
func (n *Node) pluginWorktreeCwd(cwd string) bool {
	if cwd == "" {
		return false
	}
	// Resolve the existing cwd before the scope helpers clean it. A ".." after a symlink uses the target's parent.
	if resolved, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = resolved
	}
	root := n.dir
	if root == "" {
		root = identity.DataDir(n.home)
	}
	plugins := filepath.Join(root, nodePluginsDir)
	return sessionScopeWithin(plugins, cwd) && !sameScopePath(plugins, cwd)
}

func (n *Node) excludeHomeClaudeInstructions(s map[string]any) {
	if n.home == "" {
		return
	}
	homes := []string{n.home}
	if real, err := filepath.EvalSymlinks(n.home); err == nil && real != filepath.Clean(n.home) {
		homes = append(homes, real)
	}
	var paths []string
	for _, home := range homes {
		claude := filepath.Join(home, ".claude")
		paths = append(paths, filepath.Join(claude, "CLAUDE.md"), filepath.Join(claude, "AGENTS.md"), filepath.Join(claude, "rules", "**"))
	}
	s["claudeMdExcludes"] = mergeUniqueStrings(claudeMdExcludes(s), paths)
}

func claudeMdExcludes(s map[string]any) []string {
	switch list := s["claudeMdExcludes"].(type) {
	case []string:
		return list
	case []any:
		var paths []string
		for _, v := range list {
			if path, ok := v.(string); ok {
				paths = append(paths, path)
			}
		}
		return paths
	}
	return nil
}

// claudeSettings is the --settings a Claude Code launch passes. cwd is where Claude runs and resolves an alias's
// relative settings path. Settings for a plugin worktree have their own filenames.
func (n *Node) claudeSettings(ctx context.Context, launcher, ours, cwd string, configDir ...string) string {
	return n.mergeClaudeStartSettings(ctx, launcher, ours, cwd, n.pluginWorktreeCwd(cwd), configDir...)
}

func (n *Node) mergeClaudeStartSettings(ctx context.Context, launcher, ours, cwd string, pluginWorktree bool, configDir ...string) string {
	if m, ok := n.managedLauncher(launcher); ok {
		// A managed launcher has no settings file of its own, but its gateway and key must beat the user's global
		// settings env (another gateway): put them in ours.
		if path, err := n.mergeManagedSettings(launcher, m, ours, pluginWorktree, configDir...); err != nil {
			n.logf("launcher %s: its gateway not merged with Persona's settings: %v", launcher, err)
			return ours
		} else {
			return path
		}
	}
	own := n.launcherOwnSettings(ctx, launcher)
	if own == "" {
		if pluginWorktree && (launcher == "" || launcher == "claude") {
			if path, err := n.plainPluginWorktreeSettings(ours); err == nil {
				return path
			} else {
				n.logf("Claude plugin worktree settings not written: %v", err)
			}
		}
		return ours
	}
	path, err := n.mergeLauncherSettings(launcher, own, ours, cwd, pluginWorktree)
	if err != nil {
		// Not the value: an inline --settings '{…}' can hold a token.
		n.logf("launcher %s: its own --settings not merged with Persona's, it will be ignored: %v", launcher, err)
		return ours
	}
	return path
}

func (n *Node) plainPluginWorktreeSettings(ours string) (string, error) {
	settings := map[string]any{}
	if ours != "" {
		var err error
		if settings, err = readSettingsFile(ours); err != nil {
			return "", err
		}
		if settings == nil { // the file says null
			settings = map[string]any{}
		}
	}
	n.excludeHomeClaudeInstructions(settings)
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return "", err
	}
	return writeLauncherSettings(n.dir, "claude-plugin-worktree-settings.json", out, true)
}

// launcherOwnSettings is the --settings the launcher's alias gives claude, "" for none (plain claude, a managed
// launcher, a function, an alias without one). The list is read first when it has not been since the node started.
func (n *Node) launcherOwnSettings(ctx context.Context, launcher string) string {
	if launcher == "" || launcher == "claude" {
		return ""
	}
	if _, ok := n.managedLauncher(launcher); ok {
		return "" // personad launch sets its environment and execs claude: no settings of its own
	}
	launcherCache.Lock()
	loaded := !launcherCache.at.IsZero()
	launcherCache.Unlock()
	if !loaded {
		_, _ = n.launchers(ctx)
	}
	launcherCache.Lock()
	defer launcherCache.Unlock()
	return launcherCache.settings[launcher]
}

// mergeLauncherSettings writes the launcher's settings (own: what its alias passes) merged with ours, and says where.
// Written again on every start: the launcher's file may have changed. 0600: it holds the launcher's token.
func (n *Node) mergeLauncherSettings(launcher, own, ours, cwd string, pluginWorktree bool) (string, error) {
	theirs, err := readLauncherSettings(own, cwd, n.home)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(ours)
	if err != nil {
		return "", err
	}
	var mine map[string]any
	if err := json.Unmarshal(b, &mine); err != nil {
		return "", err
	}
	settings := mergeClaudeSettings(theirs, mine)
	if pluginWorktree {
		settings["claudeMdExcludes"] = mergeUniqueStrings(claudeMdExcludes(theirs), claudeMdExcludes(mine))
		n.excludeHomeClaudeInstructions(settings)
		launcher += "-plugin-worktree"
	}
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return "", err
	}
	return writeLauncherSettings(n.dir, launcher+"-"+filepath.Base(ours), out, pluginWorktree)
}

// mergeManagedSettings writes ours with the managed launcher's env on top and says where (see writeSettings). ours
// "": the env alone, for a start whose hooks go in a plugin (hooksplugin.go).
func (n *Node) mergeManagedSettings(launcher string, l proto.ManagedLauncher, ours string, pluginWorktree bool, configDir ...string) (string, error) {
	mine := map[string]any{}
	name := "env-settings.json"
	if ours != "" {
		name = filepath.Base(ours)
		b, err := os.ReadFile(ours)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			// Not written yet (the node writes it as it starts serving; a test's node never does): the launcher's env
			// alone, rather than no settings with the gateway at all (#1364).
		case err != nil:
			return "", err
		default:
			if err := json.Unmarshal(b, &mine); err != nil {
				return "", err
			}
			if mine == nil { // the file says null
				mine = map[string]any{}
			}
		}
	}
	env := map[string]any{}
	if cur, ok := mine["env"].(map[string]any); ok {
		for k, v := range cur {
			env[k] = v
		}
	}
	for k, v := range managedSettingsEnv(l) {
		env[k] = v
	}
	// A provider that follows the computer's proxy adds its bypass list to each spelling the user's settings env sets
	// (which Claude Code applies over the environment), pinned here. A spelling they leave alone is the launch
	// environment's, which `personad launch` fills from the shell, else the user's other spelling (withInheritedNoProxy),
	// and which a pinned one here would replace (#6238). Any other list is pinned as it is.
	if ours, _ := env["NO_PROXY"].(string); mergesNoProxy(l) && ours != "" {
		user := n.userNoProxy(l, configDir...)
		for _, k := range []string{"NO_PROXY", "no_proxy"} {
			if theirs := user[k]; theirs != "" {
				env[k] = mergeNoProxy(ours, theirs)
			} else {
				delete(env, k)
			}
		}
	}
	// A proxy the user's settings give every claude that no longer answers would hold this launcher's requests until
	// they time out; one the launcher does not set itself is taken away (deadproxy.go, #5953).
	if len(configDir) > 0 {
		// Config selection precedes settings load. The caller supplies the real
		// profile; do not turn that selection into a settings.env override.
		delete(env, "CLAUDE_CONFIG_DIR")
	}
	for _, k := range n.deadSettingsProxies(env, l.BaseURL, configDir...) {
		env[k] = ""
		n.logf("launcher %s: the proxy in Claude's settings.json (%s) does not answer; it runs without it", launcher, k)
	}
	mine["env"] = env
	mine["enableArtifact"] = false // artifactsOff: a gateway cannot carry them
	// A managed launcher brings its own gateway key, so the computer's user-global apiKeyHelper (~/.claude/settings.json)
	// can only authenticate it against the wrong gateway: Claude Code runs the helper and sends its token in the other
	// auth header, and the launcher's gateway answers 401. An empty string disables the helper for this start.
	mine["apiKeyHelper"] = ""
	if pluginWorktree {
		n.excludeHomeClaudeInstructions(mine)
		name = "plugin-worktree-" + name
	}
	out, err := json.MarshalIndent(mine, "", "  ")
	if err != nil {
		return "", err
	}
	return writeLauncherSettings(n.dir, launcher+"-"+name, out, pluginWorktree)
}

func writeSettings(dir, name string, out []byte) (string, error) {
	return writeLauncherSettings(dir, name, out, false)
}

// Write atomically so concurrent starts each get a complete settings file.
func writeLauncherSettings(dir, name string, out []byte, pluginWorktree bool) (string, error) {
	d := filepath.Join(dir, "launcher-settings")
	if pluginWorktree {
		// A launcher name can itself end in "-plugin-worktree".
		d = filepath.Join(d, "plugin-worktrees")
	}
	if err := os.MkdirAll(d, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(d, name)
	tmp, err := os.CreateTemp(d, "."+name+"-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	return path, os.Rename(tmp.Name(), path)
}

// readLauncherSettings reads what an alias passes as --settings: a file (~ and $HOME are the home, a relative path
// is from cwd) or, as Claude Code also takes it, the JSON itself.
func readLauncherSettings(arg, cwd, home string) (map[string]any, error) {
	s := strings.TrimSpace(arg)
	var b []byte
	if strings.HasPrefix(s, "{") {
		b = []byte(s)
	} else {
		path := shellPath(s, home)
		if !filepath.IsAbs(path) {
			path = filepath.Join(cwd, path)
		}
		var err error
		if b, err = os.ReadFile(path); err != nil {
			return nil, err
		}
	}
	var v map[string]any
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, errors.New("not a JSON object")
	}
	if v == nil {
		v = map[string]any{}
	}
	return v, nil
}

// shellPath is a path from an alias's words (they come unexpanded) as the shell would read ~ and $HOME in it.
func shellPath(p, home string) string {
	for _, h := range []string{"${HOME}", "$HOME"} {
		if p == h || strings.HasPrefix(p, h+"/") {
			p = "~" + p[len(h):]
			break
		}
	}
	return proto.ExpandHome(p, home)
}

// mergeClaudeSettings is the launcher's settings (theirs) with ours on top: for each hook event our hooks after its
// own, so both run; any other key of ours in place of its.
func mergeClaudeSettings(theirs, mine map[string]any) map[string]any {
	out := make(map[string]any, len(theirs)+len(mine))
	for k, v := range theirs {
		out[k] = v
	}
	for k, v := range mine {
		ourHooks, ok := v.(map[string]any)
		if k != "hooks" || !ok {
			out[k] = v
			continue
		}
		hooks := map[string]any{}
		if theirHooks, ok := theirs["hooks"].(map[string]any); ok {
			for event, list := range theirHooks {
				hooks[event] = list
			}
		}
		for event, list := range ourHooks {
			prev, _ := hooks[event].([]any)
			add, _ := list.([]any)
			hooks[event] = append(append([]any{}, prev...), add...)
		}
		out["hooks"] = hooks
	}
	return out
}
