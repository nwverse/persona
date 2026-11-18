package node

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/herdr"
	"github.com/nwverse/persona/server/internal/host"
)

// setHerdrEnvSupport decides whether the tests' herdr (no binary: herdrNode's client) takes env on tab create and
// workspace create, for this test only.
func setHerdrEnvSupport(t *testing.T, ok bool) {
	t.Helper()
	envSupport.Lock()
	before := maps.Clone(envSupport.takes)
	if envSupport.takes == nil {
		envSupport.takes = map[string]bool{}
	}
	envSupport.takes["\x00tab create"], envSupport.takes["\x00workspace create"] = ok, ok
	envSupport.Unlock()
	t.Cleanup(func() {
		envSupport.Lock()
		envSupport.takes = before
		envSupport.Unlock()
	})
}

// spawnEnvOf is a recorded call's env param (nil when it has none).
func spawnEnvOf(c herdrCall) map[string]any {
	env, _ := c.Params["env"].(map[string]any)
	return env
}

// callsOf are the recorded calls of method.
func callsOf(f *fakeHerdr, method string) []herdrCall {
	var out []herdrCall
	for _, c := range f.calls() {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

// wantStartEnv fails unless env is exactly startShellEnv.
func wantStartEnv(t *testing.T, what string, env map[string]any) {
	t.Helper()
	if len(env) != len(startShellEnv) {
		t.Fatalf("%s: env %v, want %v", what, env, startShellEnv)
	}
	for k, v := range startShellEnv {
		if env[k] != v {
			t.Errorf("%s: env %s=%v, want %s", what, k, env[k], v)
		}
	}
}

// The shell a start's new tab runs is told before it reads its startup files that oh-my-zsh's and oh-my-bash's update
// question and powerlevel10k's wizard are off (#10365): tab.create into a workspace and workspace.create for a
// directory with none both carry startShellEnv, on a herdr that takes env.
func TestStartTabCarriesStartupEnv(t *testing.T) {
	withPaneShell(t, "/bin/zsh")
	n, f, home := herdrNode(t)
	setHerdrEnvSupport(t, true)
	withStartWaits(n, 200*time.Millisecond, 200*time.Millisecond) // the fake shell never runs claude
	newShellTab(f, "zsh")
	_, _ = request(t, n, "session.start", map[string]any{"cwd": home, "workspace_id": "w1", "launcher": "claude"})
	tabs := callsOf(f, "tab.create")
	if len(tabs) != 1 {
		t.Fatalf("tab.create %v", f.trace("tab.create"))
	}
	wantStartEnv(t, "tab.create", spawnEnvOf(tabs[0]))

	f.reset()
	fresh := mkdir(t, filepath.Join(home, "fresh"))
	f.reply("workspace.list", map[string]any{"workspaces": []any{}})
	f.reply("workspace.create", map[string]any{"workspace": map[string]any{"workspace_id": "w7"},
		"tab": map[string]any{"tab_id": "w7:t1"}, "root_pane": map[string]any{"pane_id": "w7:p1"}})
	_, _ = request(t, n, "session.start", map[string]any{"cwd": fresh, "launcher": "claude"})
	spaces := callsOf(f, "workspace.create")
	if len(spaces) != 1 {
		t.Fatalf("workspace.create %v", f.trace("workspace.create"))
	}
	wantStartEnv(t, "workspace.create", spawnEnvOf(spaces[0]))
	if got := f.trace("tab.rename"); len(got) != 1 || strings.Contains(got[0], "env") {
		t.Errorf("only the create carries env: %v", got)
	}
}

// A terminal the user opens (New Terminal, Open Terminal Here) and a split of one are the user's own shells: they
// never get a start's environment, though herdr takes one.
func TestUserShellTabHasNoStartupEnv(t *testing.T) {
	n, f, home := herdrNode(t)
	setHerdrEnvSupport(t, true)
	cwd := mkdir(t, filepath.Join(home, "proj"))
	f.reply("session.snapshot", paneSnapshot(cwd))
	if _, err := n.listPanes(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.reply("tab.create", map[string]any{"tab": map[string]any{"tab_id": "w1:t3"}, "root_pane": map[string]any{"pane_id": "w1:p3"}})
	if _, err := request(t, n, "panes.new", map[string]any{"workspace_id": "w1"}); err != nil {
		t.Fatal(err)
	}
	tabs := callsOf(f, "tab.create")
	if len(tabs) != 1 || spawnEnvOf(tabs[0]) != nil {
		t.Errorf("a New Terminal tab: %v", f.trace("tab.create"))
	}

	a, af, _ := actionNode(t)
	af.reply("pane.split", map[string]any{"pane": map[string]any{"pane_id": "w1:p9", "tab_id": "w1:t2"}})
	if _, err := request(t, a, "panes.split", map[string]any{"pane": "w1:p2", "direction": "right"}); err != nil {
		t.Fatal(err)
	}
	if splits := callsOf(af, "pane.split"); len(splits) != 1 || spawnEnvOf(splits[0]) != nil {
		t.Errorf("a split: %v", af.trace("pane.split"))
	}
}

// A herdr whose tab create does not list --env (an older one) is never sent env: it might refuse the whole call.
func TestHerdrWithoutEnvSupportOmitsEnv(t *testing.T) {
	withPaneShell(t, "/bin/zsh")
	n, f, home := herdrNode(t)
	setHerdrEnvSupport(t, false)
	withStartWaits(n, 200*time.Millisecond, 200*time.Millisecond)
	newShellTab(f, "zsh")
	_, _ = request(t, n, "session.start", map[string]any{"cwd": home, "workspace_id": "w1", "launcher": "claude"})
	assertTrace(t, f.trace("tab.create"), []string{
		call("tab.create", map[string]any{"workspace_id": "w1", "cwd": home, "label": "agent-1", "focus": false}),
	})
}

// A server older than the herdr binary the capability was read from may refuse env as a field it does not know: the
// tab is made again without it, and the start goes on.
func TestHerdrRefusingEnvGetsTheTabWithout(t *testing.T) {
	withPaneShell(t, "/bin/zsh")
	n, f, home := herdrNode(t)
	setHerdrEnvSupport(t, true)
	withStartWaits(n, 200*time.Millisecond, 200*time.Millisecond)
	newShellTab(f, "zsh")
	f.handle("tab.create", func(p map[string]any) any {
		if p["env"] != nil {
			return herdrFail{Code: "invalid_request", Message: "invalid request: unknown field `env`"}
		}
		return map[string]any{"tab": map[string]any{"tab_id": "w1:t2"}, "root_pane": map[string]any{"pane_id": "w1:p2"}}
	})
	_, _ = request(t, n, "session.start", map[string]any{"cwd": home, "workspace_id": "w1", "launcher": "claude"})
	tabs := callsOf(f, "tab.create")
	if len(tabs) != 2 || spawnEnvOf(tabs[0]) == nil || spawnEnvOf(tabs[1]) != nil {
		t.Fatalf("tab.create %v", f.trace("tab.create"))
	}
	if len(sent(f, "w1:p2")) != 1 {
		t.Errorf("the start did not go on in the tab made without env: %v", sent(f, "w1:p2"))
	}
}

// Whether a herdr takes env is read from its own help, once per binary: an option line counts, a word in another
// option's description does not, and a binary that fails says no.
func TestHerdrEnvSupportFromItsHelp(t *testing.T) {
	dir := t.TempDir()
	write := func(name, help string) string {
		bin := filepath.Join(dir, name)
		script := "#!/bin/sh\necho run >> " + bin + ".runs\ncat <<'EOF'\n" + help + "\nEOF\n"
		if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		return bin
	}
	takes := write("herdr-new", "Usage: herdr tab create [OPTIONS]\n\nOptions:\n      --cwd <PATH>\n\n      --env <KEY=VALUE>\n          Set an environment variable for the launched process\n      --no-focus")
	old := write("herdr-old", "Usage: herdr tab create [OPTIONS]\n\nOptions:\n      --cwd <PATH>\n          the --env of the shell is the server's\n      --no-focus")
	ctx := withStartShellEnv(context.Background())
	for _, tc := range []struct {
		bin  string
		want bool
	}{{takes, true}, {old, false}, {filepath.Join(dir, "missing"), false}} {
		h := &herdrHost{c: &herdr.Client{Bin: tc.bin}}
		for range 2 {
			if got := h.spawnEnv(ctx, "tab", "create") != nil; got != tc.want {
				t.Errorf("%s: env sent %v, want %v", filepath.Base(tc.bin), got, tc.want)
			}
		}
		if got := h.spawnEnv(context.Background(), "tab", "create"); got != nil {
			t.Errorf("%s: env without a start's: %v", filepath.Base(tc.bin), got)
		}
	}
	if b, _ := os.ReadFile(takes + ".runs"); strings.Count(string(b), "run") != 1 {
		t.Errorf("help read %d times, want once", strings.Count(string(b), "run"))
	}
	t.Cleanup(func() {
		envSupport.Lock()
		for k := range envSupport.takes {
			if strings.HasPrefix(k, dir) {
				delete(envSupport.takes, k)
			}
		}
		envSupport.Unlock()
	})
}

// The Herdr App's new terminal for a start gets the app's terminal identity and the start's environment; one for the
// user's own shell only the identity.
func TestHerdrCoreStartTerminalEnv(t *testing.T) {
	n, _, hc, home := hcNode(t)
	n.claudeStart, n.claudeReady = 200*time.Millisecond, 200*time.Millisecond
	proj := mkdir(t, filepath.Join(home, "proj"))
	hc.doc = appDoc(proj)
	hc.setTerminals(hcTerminal("t1", "", "", 50, "zsh", proj, "S1", "T1", "proj / 1"))
	_, _ = request(t, n, "session.start", map[string]any{"session_id": "0e000000-0000-4000-8000-000000010365", "cwd": proj})
	runs := hc.trace("terminal.run")
	if len(runs) != 1 {
		t.Fatalf("terminal.run %v", runs)
	}
	var run struct {
		Env map[string]string `json:"env"`
	}
	_ = json.Unmarshal([]byte(strings.TrimPrefix(runs[0], "terminal.run ")), &run)
	want := maps.Clone(hcAppTerminalEnv)
	maps.Copy(want, startShellEnv)
	if !maps.Equal(run.Env, want) {
		t.Errorf("a start's terminal env %v, want %v", run.Env, want)
	}

	if got := shellEnv(context.Background()); !maps.Equal(got, hcAppTerminalEnv) {
		t.Errorf("a user's terminal env %v", got)
	}
	// The app's identity is never replaced by a start's variable of the same name.
	if got := shellEnv(host.WithSpawnEnv(context.Background(), map[string]string{"TERM": "dumb", "X": "1"})); got["TERM"] != "xterm-256color" || got["X"] != "1" {
		t.Errorf("merged env %v", got)
	}
}

// The start line unsets the startup variables in the sh family, so the Agent and the shell the tab keeps run with the
// user's environment; fish (no unset there) and shells that take no `VAR=val cmd` get the line as before.
func TestAgentCommandUnsetsStartupEnv(t *testing.T) {
	n, _ := testNode(t)
	n.dir = "/home/u/.persona"
	cmd := "claude --session-id " + startID
	got := n.agentCommand(cmd, shellQuote, "/bin/zsh")
	head, _, ok := strings.Cut(got, " && ")
	if !ok || !strings.HasPrefix(head, "unset ") {
		t.Fatalf("no unset: %q", got)
	}
	names := strings.Fields(strings.TrimPrefix(head, "unset "))
	for k := range startShellEnv {
		if !slices.Contains(names, k) || !slices.Contains(startupOnlyEnv, k) {
			t.Errorf("%s is not unset", k)
		}
	}
	if len(startupOnlyEnv) != len(startShellEnv) {
		t.Errorf("startupOnlyEnv %v is not startShellEnv's names", startupOnlyEnv)
	}
	if got := n.agentCommand(cmd, shellQuote, "/opt/homebrew/bin/fish"); strings.Contains(got, "DISABLE_AUTO_UPDATE") {
		t.Errorf("fish: %q", got)
	}
	for _, sh := range []string{"/bin/tcsh", "/usr/bin/nu"} {
		if got := n.agentCommand(cmd, shellQuote, sh); got != cmd {
			t.Errorf("%s: %q", sh, got)
		}
	}

	// In a real shell started with them, as a start tab's is: the Agent does not see them, nor does the shell after.
	for _, shell := range []string{"bash", "zsh", "sh"} {
		bin, err := exec.LookPath(shell)
		if err != nil {
			continue
		}
		home := t.TempDir()
		path := filepath.Join(home, "bin")
		fakeProgram(t, filepath.Join(path, "claude"), `env >"$HOME/claude-env"`)
		line := n.agentCommand("claude", shellQuote, bin) + `; printf '%s' "${DISABLE_AUTO_UPDATE-unset}" >"$HOME/after"`
		c := exec.Command(bin, "-c", line)
		c.Env = []string{"HOME=" + home, "PATH=" + path + ":/usr/bin:/bin"}
		for k, v := range startShellEnv {
			c.Env = append(c.Env, k+"="+v)
		}
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v: %s", shell, err, out)
		}
		env, _ := os.ReadFile(filepath.Join(home, "claude-env"))
		for k := range startShellEnv {
			if strings.Contains(string(env), "\n"+k+"=") || strings.HasPrefix(string(env), k+"=") {
				t.Errorf("%s: %s reached claude", shell, k)
			}
		}
		if after, _ := os.ReadFile(filepath.Join(home, "after")); string(after) != "unset" {
			t.Errorf("%s: the shell kept DISABLE_AUTO_UPDATE=%s", shell, after)
		}
	}
}
