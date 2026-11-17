package node

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/host"
)

const powerShellTestBash = `C:\Program Files\Git\bin\bash.exe`

func withPowerShellTestBash(t *testing.T) {
	t.Helper()
	old := powerShellStartShell
	powerShellStartShell = func() (string, error) { return powerShellTestBash, nil }
	t.Cleanup(func() { powerShellStartShell = old })
}

// A Windows herdr pane runs PowerShell unless its configuration says otherwise. The start must not type the
// POSIX-quoted command itself there: PowerShell splits shellQuote's backslash escapes into separate arguments.
func TestPowerShellClaudeStartUsesAStartFile(t *testing.T) {
	n, _ := testNode(t)
	n.dir = `C:\Users\u\.persona`
	shells := []string{windowsPowerShellPane}
	cmd := n.agentStartCommand(n.claudeCommandLine(claudeLaunch{
		Launcher: "claude",
		Settings: `C:\Users\u\.persona\settings.json`,
		Hooks:    `C:\Users\u\.persona\claude-hooks`,
	}, "--session-id "+startID, shellQuote), shellQuote, shells...)
	if !n.typesStartFile(&herdrHost{}, shells...) {
		t.Fatalf("PowerShell would type the POSIX command directly: %s", cmd)
	}
	if !strings.HasPrefix(cmd, "unset ") || !strings.Contains(cmd, "PERSONA_HOME="+shellQuote(n.dir)+" ") {
		t.Fatalf("start file lost the PERSONA_HOME / unset prefix: %s", cmd)
	}
	file, err := writeStartScript(t.TempDir(), cmd)
	if err != nil {
		t.Fatal(err)
	}
	typed := startLineChoice(cmd, file, startID, powerShellTestBash, true, shells...)
	if typed != powerShellStartLine(powerShellTestBash, file, startID) {
		t.Fatalf("typed line = %q", typed)
	}
	if strings.Contains(typed, `'\\'`) || strings.Contains(typed, "&&") {
		t.Fatalf("PowerShell line contains POSIX quoting or &&: %q", typed)
	}
}

func TestPowerShellSecretTicketUsesAStartFile(t *testing.T) {
	n, _ := testNode(t)
	n.dir = `C:\Users\u\.persona`
	n.bin = `C:\Program Files\Persona\personad.exe`
	line := n.claudeCommandLine(claudeLaunch{Launcher: "claude"}, "--session-id "+startID, shellQuote)
	line = n.withSecretTicket(line, "claude", "ticket with spaces", shellQuote)
	cmd := n.agentStartCommand(line, shellQuote, windowsPowerShellPane)
	wrap := shellQuote(n.bin) + " launch --ticket " + shellQuote("ticket with spaces") + " -- claude "
	if !strings.Contains(cmd, wrap) || !strings.Contains(cmd, "PERSONA_HOME="+shellQuote(n.dir)+" ") {
		t.Fatalf("secret-ticket start file = %q", cmd)
	}
	typed := startLineChoice(cmd, `C:\Users\u\.persona\agent-start.sh`, startID, powerShellTestBash, true, windowsPowerShellPane)
	if strings.Contains(typed, `'\\'`) || !strings.HasPrefix(typed, "& ") {
		t.Fatalf("secret-ticket PowerShell line = %q", typed)
	}
}

func TestPowerShellQuote(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"empty", "", "''"},
		{"plain", "plain", "'plain'"},
		{"spaces", `C:\a path\start.sh`, "'C:\\a path\\start.sh'"},
		{"apostrophe", `C:\O'Brien\start.sh`, "'C:\\O''Brien\\start.sh'"},
		{"smart quotes", `C:\‘’‚‛“”\start.sh`, "'C:\\‘‘’’‚‚‛‛“”\\start.sh'"},
		{"dollar", `$HOME\start.sh`, "'$HOME\\start.sh'"},
		{"backtick", "`HOME\\start.sh", "'`HOME\\start.sh'"},
		{"operators", `a&b;c`, "'a&b;c'"},
		{"non-ASCII", `雪😀`, "'雪😀'"},
		{"UNC", `\\server\share\start.sh`, "'\\\\server\\share\\start.sh'"},
		{"drive root", `C:\`, "'C:\\'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := powerShellQuote(tc.in); got != tc.want {
				t.Fatalf("powerShellQuote(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestPowerShellStartLineExact(t *testing.T) {
	path := `C:\Users\O'Brien\agent start.sh`
	want := `& 'C:\Program Files\Git\bin\bash.exe' -ic '. $''C:/Users/O\''Brien/agent start.sh'''; $null = '` + startID + `'`
	got := powerShellStartLine(powerShellTestBash, path, startID)
	if got != want {
		t.Fatalf("line = %q\nwant = %q", got, want)
	}
	if strings.Contains(got, `"`) {
		t.Fatalf("an embedded double quote would not survive PowerShell 5.1's native boundary: %q", got)
	}
}

func TestPowerShellStartLines(t *testing.T) {
	for _, tc := range []struct{ name, path, script string }{
		{"spaces", `C:\Users\a b\agent start.sh`, ". $'C:/Users/a b/agent start.sh'"},
		{"apostrophe", `C:\Users\O'Brien\start.sh`, `. $'C:/Users/O\'Brien/start.sh'`},
		{"smart quotes", `C:\Users\‘O’Brien‚‛“”\start.sh`, ". $'C:/Users/‘O’Brien‚‛“”/start.sh'"},
		{"dollar", `C:\Users\$HOME\start.sh`, ". $'C:/Users/$HOME/start.sh'"},
		{"backtick", "C:\\Users\\`HOME\\start.sh", ". $'C:/Users/`HOME/start.sh'"},
		{"operators", `C:\a&b;c\start.sh`, ". $'C:/a&b;c/start.sh'"},
		{"non-ASCII", `C:\Users\雪😀\start.sh`, ". $'C:/Users/雪😀/start.sh'"},
		{"UNC", `\\server\share\a b\start.sh`, ". $'//server/share/a b/start.sh'"},
		{"drive root", `C:\`, ". $'C:/'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := "& " + powerShellQuote(powerShellTestBash) + " -ic " + powerShellQuote(tc.script) +
				"; $null = " + powerShellQuote(startID)
			got := powerShellStartLine(powerShellTestBash, tc.path, startID)
			if got != want {
				t.Fatalf("line = %q\nwant = %q", got, want)
			}
			if strings.Contains(got, `'\\'`) || strings.Contains(got, "&&") {
				t.Fatalf("POSIX quoting or && in PowerShell line: %q", got)
			}
		})
	}
	if got := powerShellStartLine(powerShellTestBash, `C:\start.sh`, ""); strings.Contains(got, "$null") {
		t.Fatalf("an empty anchor added a no-op: %q", got)
	}
}

func TestWindowsPowerShellProgram(t *testing.T) {
	for _, tc := range []struct {
		shell string
		want  bool
	}{
		{"powershell", true}, {"powershell.exe", true}, {"POWERSHELL.EXE", true}, {"pwsh", true}, {"-pwsh.exe", true}, {"PWSh.ExE", true},
		{`C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`, true},
		{`C:\Program Files\PowerShell\7\pwsh.exe`, true},
		{"bash", false}, {`C:\Program Files\Git\bin\bash.exe`, false}, {"cmd.exe", false}, {"zsh", false},
	} {
		if got := windowsPowerShellProgram(tc.shell); got != tc.want {
			t.Errorf("%q: %v, want %v", tc.shell, got, tc.want)
		}
	}
}

func TestPowerShellPaneUsesTheForegroundShell(t *testing.T) {
	bash := `C:\Program Files\Git\bin\bash.exe`
	for _, tc := range []struct {
		name   string
		shells []string
		want   bool
	}{
		{"PowerShell", []string{windowsPowerShellPane}, true},
		{"Bash from PowerShell", []string{windowsPowerShellPane, bash}, false},
		{"PowerShell from Bash", []string{bash, windowsPowerShellPane}, true},
		{"none", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := powerShellPane(tc.shells...); got != tc.want {
				t.Fatalf("powerShellPane(%v) = %v, want %v", tc.shells, got, tc.want)
			}
			line := startLineChoice("claude", `C:\node dir\start.sh`, startID, powerShellTestBash, true, tc.shells...)
			if tc.want && !strings.HasPrefix(line, "& ") {
				t.Fatalf("foreground PowerShell did not get its handoff: %q", line)
			}
			if !tc.want && len(tc.shells) > 0 && strings.HasPrefix(line, "& ") {
				t.Fatalf("foreground Bash got PowerShell syntax: %q", line)
			}
		})
	}
}

// The existing Unix choices stay byte-identical: direct commands are unchanged, and POSIX start files keep their
// source operator, quoting and && anchor. The PowerShell choice is the only new one.
func TestStartLineChoiceKeepsPosixBytes(t *testing.T) {
	const cmd = "claude --session-id 0e000000-0000-4000-8000-0000000000aa"
	if got := startLineChoice(cmd, "unused", "anchor", powerShellTestBash, false, "/bin/zsh"); got != cmd {
		t.Fatalf("direct command = %q", got)
	}
	for _, tc := range []struct{ shell, want string }{
		{"/bin/zsh", `. '/tmp/node dir/agent start.sh' && true 'session id'`},
		{"-fish", `source '/tmp/node dir/agent start.sh' && true 'session id'`},
		{"/bin/tcsh", `. '/tmp/node dir/agent start.sh' && true 'session id'`},
	} {
		if got := startLineChoice(cmd, `/tmp/node dir/agent start.sh`, "session id", powerShellTestBash, true, tc.shell); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.shell, got, tc.want)
		}
	}
	n, _ := testNode(t)
	n.dir = "/tmp/node dir"
	for _, shells := range [][]string{{"/bin/zsh"}, {"/opt/homebrew/bin/fish"}, {"/bin/tcsh"}, {"pwsh"}, {}} {
		if got, want := n.agentStartCommand(cmd, shellQuote, shells...), n.agentCommand(cmd, shellQuote, shells...); got != want {
			t.Errorf("%v: agentStartCommand = %q, want existing %q", shells, got, want)
		}
	}
}

// The POSIX file, not the PowerShell outer line, carries Claude's arguments. Run it with a fake claude that records
// every argument and the environment to prove that each Windows path survives as one argument.
func TestWindowsClaudeStartFileArguments(t *testing.T) {
	home := t.TempDir()
	bin := filepath.Join(home, "bin")
	fakeProgram(t, filepath.Join(bin, "claude"), `printf '%s\n' "${PERSONA_HOME-unset}" "${PERSONA_ASSISTANT-unset}" "$@" >"$HOME/args"`)
	n, _ := testNode(t)
	n.dir = `C:\Users\u\.persona`
	spec := claudeLaunch{Launcher: "claude", Settings: `C:\Users\u\settings with space.json`, Hooks: `C:\Users\u\hook's`}
	cmd := n.agentStartCommand(n.claudeCommandLine(spec, "--session-id "+startID, shellQuote), shellQuote, windowsPowerShellPane)
	file, err := writeStartScript(t.TempDir(), cmd)
	if err != nil {
		t.Fatal(err)
	}
	c := exec.Command("sh", file)
	c.Env = []string{"HOME=" + home, "PATH=" + bin + ":/usr/bin:/bin", "PERSONA_ASSISTANT=from-assistant"}
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("start file: %v: %s", err, out)
	}
	got := readArgumentLines(t, filepath.Join(home, "args"))
	want := []string{n.dir, "unset", "--session-id", startID, "--settings", spec.Settings,
		"--mcp-config", filepath.Join(n.dir, "mcp.json"), "--allowedTools", n.agentAllowedTools(false), "--plugin-dir", spec.Hooks}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("claude received\n%q\nwant\n%q", got, want)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("start file left behind: %v", err)
	}
}

func TestWindowsOtherAgentStartFileArguments(t *testing.T) {
	home := t.TempDir()
	n, _ := testNode(t)
	n.dir = `C:\Users\u\.persona`
	prompt := "a 'quoted' $HOME ` & ; 雪"
	resume := "00000000-0000-4000-8000-000000000001"
	cmd := otherAgentCommand("wrapper", resume, prompt, windowsPowerShellPane)
	cmd = n.agentStartCommand(cmd, shellQuote, windowsPowerShellPane)
	file, err := writeStartScript(t.TempDir(), cmd)
	if err != nil {
		t.Fatal(err)
	}
	script := `wrapper() { printf '%s\n' "${PERSONA_HOME-unset}" "$#" "$@" >"$HOME/args"; }; . "$1"`
	c := exec.Command("sh", "-c", script, "sh", file)
	c.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin"}
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("start file: %v: %s", err, out)
	}
	want := []string{n.dir, "3", "resume", resume, prompt}
	if got := readArgumentLines(t, filepath.Join(home, "args")); !reflect.DeepEqual(got, want) {
		t.Fatalf("wrapper received %q, want %q", got, want)
	}
	if got := otherAgentCommand("wrapper", "", "one\ntwo", windowsPowerShellPane); !strings.Contains(got, "one\ntwo") {
		t.Fatalf("start-file prompt lost its line break: %q", got)
	}
}

// Git Bash reads ~/.bashrc in the same -ic mode as loginShell, so a launcher defined there works in the start file.
func TestPowerShellStartFileReadsBashrc(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not installed")
	}
	home := t.TempDir()
	bin := filepath.Join(home, "bin")
	fakeProgram(t, filepath.Join(bin, "claude"), `printf '%s\n' "${PERSONA_HOME-unset}" "${PERSONA_ASSISTANT-unset}" "$@" >"$HOME/args"`)
	writeFile(t, filepath.Join(home, ".bashrc"), "ccf() { claude --model from-function \"$@\"; }\n")
	n, _ := testNode(t)
	n.dir = filepath.Join(home, "O'Brien ‘’‚‛“” $HOME ` & ; 雪")
	cmd := n.agentStartCommand(n.claudeCommandLine(claudeLaunch{Launcher: "ccf"}, "--session-id "+startID, shellQuote), shellQuote, windowsPowerShellPane)
	file, err := writeStartScript(n.dir, cmd)
	if err != nil {
		t.Fatal(err)
	}
	c := exec.Command(bash, "-ic", powerShellSourceCommand(file))
	c.Env = []string{"HOME=" + home, "PATH=" + bin + ":/usr/bin:/bin", "PERSONA_ASSISTANT=from-assistant"}
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("Git Bash start command: %v: %s", err, out)
	}
	want := []string{n.dir, "unset", "--model", "from-function", "--session-id", startID,
		"--mcp-config", filepath.Join(n.dir, "mcp.json"), "--allowedTools", n.agentAllowedTools(false)}
	if got := readArgumentLines(t, filepath.Join(home, "args")); !reflect.DeepEqual(got, want) {
		t.Fatalf("rc-file launcher received %q, want %q", got, want)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("start file left behind: %v", err)
	}
}

func TestOrcaRunLinePowerShell(t *testing.T) {
	start := powerShellStartLine(powerShellTestBash, `C:\node dir\agent-start.sh`, startID)
	cwd := `C:\O'Brien\$HOME` + "`" + ` & ; 雪`
	want := "Set-Location -LiteralPath " + powerShellQuote(cwd) + " -ErrorAction Stop; " + start + "\r"
	if got := orcaRunLine("windows", cwd, start); got != want {
		t.Fatalf("Orca PowerShell line = %q, want %q", got, want)
	}
	// A configured Git Bash pane and every Unix start retain Orca's existing POSIX prefix byte-for-byte.
	for _, tc := range []struct{ goos, command string }{{"windows", "claude --session-id x"}, {"linux", start}, {"darwin", start}} {
		want := "cd -- " + shellQuote(cwd) + " && " + tc.command + "\r"
		if got := orcaRunLine(tc.goos, cwd, tc.command); got != want {
			t.Errorf("%s: %q, want %q", tc.goos, got, want)
		}
	}
}

func TestPowerShellStartParseErrorIsRetyped(t *testing.T) {
	line := powerShellStartLine(powerShellTestBash, `C:\node dir\agent-start.sh`, startID)
	probe := &startProbe{anchor: startID}
	probe.typed(&typedStart{line: line})
	for _, tc := range []struct{ name, screen, want string }{
		{"PowerShell 5.1", "PS> " + line + "\r\nAt line:1 char:72\r\n" +
			"+ CategoryInfo          : ParserError: (:) [], ParentContainsErrorRecordException\r\n" +
			"+ FullyQualifiedErrorId : UnexpectedToken\r\n",
			"+ CategoryInfo          : ParserError: (:) [], ParentContainsErrorRecordException"},
		{"PowerShell 7", "PS> " + line + "\nParserError:\nLine |\n   1 | " + line +
			"\n     | Unexpected token '-ic' in expression or statement.\n", "ParserError:"},
		{"unterminated string", "PS> " + line + "\nParserError:\nLine |\n   1 | " + line +
			"\n     | The string is missing the terminator: '.\n     + FullyQualifiedErrorId : TerminatorExpectedAtEndOfString\n", "ParserError:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !mayBeRejected(tc.screen) {
				t.Fatal("the parser error did not trigger a full-screen check")
			}
			if got := probe.rejectedLine(tc.screen); got != tc.want {
				t.Fatalf("rejected line = %q, want %q", got, tc.want)
			}
		})
	}
	if got := probe.rejectedLine("ParserError:\nPS> " + line); got != "" {
		t.Fatalf("an error before the typed line was accepted: %q", got)
	}
}

func readArgumentLines(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
}

func TestTypeStartPowerShellLineStaysShort(t *testing.T) {
	withPowerShellTestBash(t)
	n, _, _ := herdrNode(t)
	n.dir = filepath.Join(t.TempDir(), strings.Repeat("long-directory/", 8))
	created := &host.Created{Pane: host.PaneRef{Kind: host.Herdr, ID: "w1:p2"}, SlotID: "w1:t2", SpaceID: "w1"}
	cmd := strings.Repeat("x", 2048)
	typed, err := n.typeStartMode(context.Background(), n.hosts.Get(host.Herdr).(*herdrHost), created, "w1", startID, cmd, false, windowsPowerShellPane)
	if err != nil {
		t.Fatal(err)
	}
	defer typed.done()
	if len(typed.line)+1 >= 1024 || strings.Contains(typed.line, cmd) {
		t.Fatalf("typed %d bytes: %q", len(typed.line)+1, typed.line)
	}
	b, err := os.ReadFile(typed.path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != startScript(typed.path, cmd) {
		t.Fatalf("start file lost the long command (%d bytes)", len(b))
	}
	if strings.Contains(typed.line, `'\\'`) || !strings.HasPrefix(typed.line, "& ") {
		t.Fatalf("PowerShell line = %q", typed.line)
	}
}

func TestTypeStartPowerShellMissingGitBash(t *testing.T) {
	n, f, _ := herdrNode(t)
	n.dir = t.TempDir()
	old := powerShellStartShell
	powerShellStartShell = func() (string, error) { return "", errors.New("Git Bash (Git for Windows) is missing") }
	t.Cleanup(func() { powerShellStartShell = old })
	created := &host.Created{Pane: host.PaneRef{Kind: host.Herdr, ID: "w1:p2"}, SlotID: "w1:t2", SpaceID: "w1"}
	typed, err := n.typeStartMode(context.Background(), n.hosts.Get(host.Herdr).(*herdrHost), created, "w1", startID, "claude", false, windowsPowerShellPane)
	if err == nil || err.Error() != "Git Bash (Git for Windows) is missing" || typed != nil {
		t.Fatalf("typed = %#v, err = %v", typed, err)
	}
	if left, _ := filepath.Glob(filepath.Join(n.dir, startScriptPrefix+"*")); len(left) != 0 {
		t.Fatalf("start files left behind: %v", left)
	}
	if calls := f.trace("pane.send_input"); len(calls) != 0 {
		t.Fatalf("typed without Git Bash: %v", calls)
	}
}
