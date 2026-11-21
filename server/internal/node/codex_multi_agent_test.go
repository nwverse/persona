package node

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

const stubMultiAgentCodex = `#!/bin/sh
if [ "$#" -eq 4 ] && [ "$1" = '-c' ] && [ "$2" = 'features.multi_agent_v2=true' ] && [ "$3" = features ] && [ "$4" = list ]; then
  printf '%s\n' 'multi_agent_v2   stable   true'
  exit 0
fi
exit 1
`

func TestCodexMultiAgentTerminalStart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a stub Unix executable")
	}
	for _, on := range []bool{true, false} {
		name := "plain"
		if on {
			name = "requested"
		}
		t.Run(name, func(t *testing.T) {
			exe := writeExecutable(t, filepath.Join(t.TempDir(), "codex"), stubMultiAgentCodex)
			withCodexLaunchers(t, map[string][]string{"cwork": {exe}})
			n, _, cwd, _ := codexStartHerdr(t, 0)
			res, err := request(t, n, "session.start", map[string]any{
				"cwd": cwd, "launcher": "cwork", "prompt": "task", "ultracode": on,
			})
			if err != nil {
				t.Fatal(err)
			}
			s := decodeSessions(t, []any{res["session"]})[0]
			if s.CodexMultiAgentV2 != on || s.Ultracode != nil || slices.Contains(ran, codexMultiAgentFlag) != on {
				t.Fatalf("row flag=%t, Claude switch=%v, launch argv=%q, wanted opt-in %t", s.CodexMultiAgentV2, s.Ultracode, ran, on)
			}
			for _, row := range n.sessionList(context.Background(), false) {
				if row.SessionID == s.SessionID {
					if row.CodexMultiAgentV2 != on {
						t.Fatalf("refreshed row flag=%t, want %t", row.CodexMultiAgentV2, on)
					}
					return
				}
			}
			t.Fatal("started thread missing from the refreshed list")
		})
	}
}

func TestCodexMultiAgentUnsupportedBeforeTerminalLaunch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a stub Unix executable")
	}
	for _, tc := range []struct {
		name, stub string
		options    []string
	}{
		{name: "old", stub: "#!/bin/sh\nprintf 'multi_agent deprecated true\\n'\n"},
		{name: "disabled by config", stub: "#!/bin/sh\nprintf 'multi_agent_v2 stable false\\n'\n"},
		{name: "preview feature", stub: "#!/bin/sh\nprintf 'multi_agent_v2 under development true\\n'\n"},
		{name: "external app server", stub: stubMultiAgentCodex, options: []string{"--remote", "unix:///remote"}},
		{name: "non-interactive subcommand", stub: stubMultiAgentCodex, options: []string{"exec"}},
		{name: "explicit disable", stub: stubMultiAgentCodex, options: []string{"--disable=multi_agent_v2"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exe := writeExecutable(t, filepath.Join(t.TempDir(), "codex"), tc.stub)
			withCodexLaunchers(t, map[string][]string{"codex": append([]string{exe}, tc.options...)})
			n, f, cwd, cmds := codexStartHerdr(t, 0)
			_, err := request(t, n, "session.start", map[string]any{"cwd": cwd, "launcher": "codex", "prompt": "task", "ultracode": true})
			if tabs := f.trace("tab.create"); rpcCode(err) != ultracodeUnsupportedCode || len(*cmds) != 0 || len(tabs) != 0 {
				t.Fatalf("refused %v, terminal commands=%q, tab.create=%v", err, *cmds, tabs)
			}
		})
	}
}

func TestCodexMultiAgentManagedOptions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a stub Unix executable")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("requires bash for a startup-free shell")
	}
	n, _, cwd, _ := codexStartHerdr(t, 0)
	bin := filepath.Join(t.TempDir(), "bin")
	exe := writeExecutable(t, filepath.Join(bin, "codex"), stubMultiAgentCodex)
	// The production lookup reads the user's shell startup files, which may change PATH.
	writeExecutable(t, filepath.Join(bin, "bash"), "#!/bin/sh\nexec "+shellQuote(bash)+" --noprofile --norc \"$@\"\n")
	t.Setenv("SHELL", "bash")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	n.managed.byID = map[string]proto.ManagedLauncher{
		"work": {Name: "work", Agent: "codex", Args: []string{"-m", "gpt-test", "--", "launcher tail"}},
	}
	argv := []string{n.bin, "launch", "work"}
	got, err := n.prepareCodexMultiAgent(context.Background(), cwd, "work", nil, argv)
	if err != nil || !slices.Equal(got, append([]string{codexMultiAgentExecEnv + "=" + exe}, argv...)) {
		t.Fatalf("managed startup argv=%q, error=%v", got, err)
	}
	final := launchArgv("codex", nil, n.managed.byID["work"].Args, "", "", nil, "")
	want := []string{"codex", "-m", "gpt-test", "-c", codexMultiAgentFlag, "--", "launcher tail"}
	if got := codexMultiAgentArgs(final); !slices.Equal(got, want) {
		t.Fatalf("managed process argv=%q, want %q", got, want)
	}
}

func TestCodexMultiAgentNativeExec(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a stub Unix executable")
	}
	cwd := t.TempDir()
	exe := writeExecutable(t, filepath.Join(cwd, "codex"), strings.TrimSuffix(stubMultiAgentCodex, "exit 1\n")+`
printf '%s\n' "$CODEX_HOME"
printf '%s\n' "$@"
`)
	argv := []string{"CODEX_HOME=explicit codex home", exe}
	argv, err := (&Node{}).prepareCodexMultiAgent(context.Background(), cwd, "codex", nil, argv)
	if err != nil {
		t.Fatal(err)
	}
	const prompt = "--disable multi_agent_v2"
	file := filepath.Join(cwd, "start.json")
	data, err := json.Marshal(codexStartFile{Argv: argv, Prompt: prompt, Dir: cwd, MultiAgentV2: true})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, file, string(data))
	args, _ := json.Marshal([]string{file})
	cmd := exec.Command(os.Args[0], "-test.run=^TestCodexStartHelper$")
	cmd.Env = append(os.Environ(), "CODEX_HOME=inherited", "PERSONA_AGENT_SANDBOX=off",
		"PERSONA_CODEX_START_HELPER=1", "PERSONA_CODEX_START_ARGS="+string(args))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("native launch failed: %v: %s", err, out)
	}
	want := []string{"explicit codex home", "-c", codexMultiAgentFlag, "--", prompt}
	if got := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n"); !slices.Equal(got, want) {
		t.Fatalf("native launch received %q, want %q", got, want)
	}
}

func TestCodexMultiAgentOnlyWhileBound(t *testing.T) {
	n, _, cwd := herdrNode(t)
	st := sessionState{Cwd: cwd, Launcher: "codex", Pane: "p1", CodexMultiAgentPane: "p1"}
	data, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	var restored sessionState
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	n.state.put(startThread, restored)
	row := proto.Session{SessionID: startThread, Agent: "codex", PaneID: "p1", Status: proto.StatusWorking}
	plainKey := listKey([]proto.Session{row})
	n.decorate(&row)
	if !row.CodexMultiAgentV2 || listKey([]proto.Session{row}) == plainKey {
		t.Fatal("restored start flag did not reach the row and its push key")
	}
	n.state.setPane(startThread, "p2")
	row.PaneID = "p2"
	n.decorate(&row)
	if row.CodexMultiAgentV2 {
		t.Fatal("replacement process inherited the flag")
	}
	row.PaneID = "p1"
	n.decorate(&row)
	if row.CodexMultiAgentV2 {
		t.Fatal("a stale pane reported the flag after replacement")
	}
}

func TestLiveCodexMultiAgentOverride(t *testing.T) {
	bin := os.Getenv("PERSONA_CODEX_BIN")
	if bin == "" {
		t.Skip("set PERSONA_CODEX_BIN to a Codex executable with stable multi-agent v2")
	}
	bin, err := exec.LookPath(bin)
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	config := filepath.Join(home, "config.toml")
	const baseConfig = "[features]\nmulti_agent_v2 = false\n"
	writeFile(t, config, baseConfig)
	env := append(withoutEnv(os.Environ(), []string{"CODEX_HOME"}), "CODEX_HOME="+home)
	ctx := context.Background()
	list := func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, bin, append(args, "features", "list")...)
		cmd.Dir, cmd.Env = home, env
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("features list: %v", err)
		}
		for line := range strings.SplitSeq(string(out), "\n") {
			if fields := strings.Fields(line); len(fields) == 3 && fields[0] == "multi_agent_v2" {
				return strings.Join(fields[1:], " ")
			}
		}
		return ""
	}
	if before, on, after := list(), list("-c", codexMultiAgentFlag), list(); before != "stable false" || on != "stable true" || after != "stable false" {
		t.Fatalf("feature states before, during and after the override: %q, %q, %q", before, on, after)
	}
	if data, err := os.ReadFile(config); err != nil || string(data) != baseConfig {
		t.Fatalf("per-process override affected the user's config file: %v", err)
	}
}
