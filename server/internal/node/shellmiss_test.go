package node

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// Every shell's own line for a command it cannot run, as the shells print them (bash 3.2 and 5, zsh 5.9, dash and
// ksh93 recorded in a terminal; fish, mksh, busybox and the distributions' handlers from their documentation).
func TestParseShellMiss(t *testing.T) {
	for _, tc := range []struct {
		line              string
		word, kind, shell string
	}{
		// bash, as bash, a login shell, a path, a script, Fedora's handler
		{"bash: cc: command not found", "cc", missNotFound, "bash"},
		{"-bash: cc: command not found", "cc", missNotFound, "bash"},
		{"/bin/bash: cc: command not found", "cc", missNotFound, "bash"},
		{"bash: line 1: cc: command not found", "cc", missNotFound, "bash"},
		{"bash: cc: command not found...", "cc", missNotFound, "bash"},
		{"sh: cc: command not found", "cc", missNotFound, "sh"},
		{"bash: exec: claude: not found", "claude", missNotFound, "bash"},
		{"bash: claude: command not found\r", "claude", missNotFound, "bash"},
		// zsh, at the prompt, with a line number, in a launcher function, in an rc file
		{"zsh: command not found: cc", "cc", missNotFound, "zsh"},
		{"zsh:1: command not found: cc", "cc", missNotFound, "zsh"},
		{"cc:2: command not found: claude", "claude", missNotFound, ""},
		{"cc: command not found: claude", "claude", missNotFound, ""},
		{"/home/u/.zshrc:12: command not found: compdef", "compdef", missNotFound, ""},
		// dash and sh as dash
		{"sh: 1: cc: not found", "cc", missNotFound, "sh"},
		{"/bin/dash: 4: cc: not found", "cc", missNotFound, "dash"},
		{"dash: 2: /opt/p/personad: not found", "/opt/p/personad", missNotFound, "dash"},
		// ksh, mksh, busybox
		{"ksh: cc: not found", "cc", missNotFound, "ksh"},
		{"/bin/ksh: cc: not found [No such file or directory]", "cc", missNotFound, "ksh"},
		{"mksh: cc: not found", "cc", missNotFound, "mksh"},
		// fish 3 and fish 2
		{"fish: Unknown command: cc", "cc", missNotFound, "fish"},
		{"fish: Unknown command 'cc'", "cc", missNotFound, "fish"},
		// Ubuntu's handler, without and with a suggestion
		{"cc: command not found", "cc", missNotFound, ""},
		{"Command 'cc' not found, did you mean:", "cc", missNotFound, ""},
		// personad launch of a Persona-managed launcher
		{"personad launch: claude not found in PATH or the usual install locations", "claude", missNotFound, ""},
		// a path that is not there
		{"bash: /opt/p/personad: No such file or directory", "/opt/p/personad", missNoSuchFile, "bash"},
		{"sh: 1: /opt/p/personad: No such file or directory", "/opt/p/personad", missNoSuchFile, "sh"},
		{"zsh: no such file or directory: /opt/p/personad", "/opt/p/personad", missNoSuchFile, "zsh"},
		{"zsh:1: no such file or directory: /opt/p/personad", "/opt/p/personad", missNoSuchFile, "zsh"},
		{"bash: /home/u/bin/cc: /usr/bin/python9: bad interpreter: No such file or directory", "/home/u/bin/cc", missNoSuchFile, "bash"},
		{"zsh: /home/u/bin/cc: bad interpreter: /usr/bin/python9: no such file or directory", "/home/u/bin/cc", missNoSuchFile, "zsh"},
		// a path that cannot be run
		{"bash: /opt/p/personad: Permission denied", "/opt/p/personad", missPermission, "bash"},
		{"/bin/dash: 3: /opt/p/personad: Permission denied", "/opt/p/personad", missPermission, "dash"},
		{"zsh: permission denied: /opt/p/personad", "/opt/p/personad", missPermission, "zsh"},
		{"ksh: /opt/p/personad: cannot execute [Permission denied]", "/opt/p/personad", missPermission, "ksh"},
		{"fish: The file '/opt/p/personad' is not executable by this user", "/opt/p/personad", missPermission, "fish"},
	} {
		m, ok := parseShellMiss(tc.line)
		if !ok || m.Word != tc.word || m.Kind != tc.kind || m.Shell != tc.shell {
			t.Errorf("parseShellMiss(%q) = %+v %v, want word %q kind %q shell %q", tc.line, m, ok, tc.word, tc.kind, tc.shell)
		}
	}
	// Not a shell's own line: Claude's output quoting one, prose, a prompt, the command itself.
	for _, line := range []string{
		"",
		"  ⎿  zsh: command not found: cc",
		"● The build failed: bash: cc: command not found",
		"Error: unknown option '--plugin-dir'",
		"~/proj % cc --session-id 0e000000-0000-4000-8000-000000000001",
		"the file was not found",
		"Permission denied (publickey).",
		"Claude Code v2.1.318",
	} {
		if m, ok := parseShellMiss(line); ok {
			t.Errorf("parseShellMiss(%q) = %+v, want none", line, m)
		}
	}
}

const probeID = "0e000000-0000-4000-8000-000000000001"

// typed is the command line a start types, as the terminal shows it.
func typed(prompt, launcher string) string {
	return prompt + "unset CLAUDECODE && PERSONA_HOME=/home/u/.persona HERDR_AGENT=claude " + launcher +
		" --session-id " + probeID + " --mcp-config /home/u/.persona/mcp.json"
}

func shellBack() bool { return true }
func shellAway() bool { return false }

// Which lines on a start's screen end it, and as what: the command's own name (anywhere after the command, fish
// reprinting the command after its error), a missing claude once the shell is back after the last echo; never the
// echo itself, older scrollback, rc noise before the command's last echo, or another command's line.
func TestStartProbeVerdict(t *testing.T) {
	p := &startProbe{anchor: probeID, program: "cc", launcher: "cc", paneShell: "bash"}
	for _, tc := range []struct {
		name   string
		screen string
		back   func() bool
		code   string
		word   string
		other  string
	}{
		{"bash", typed("$ ", "cc") + "\nbash: cc: command not found\n$ ", shellBack, proto.CodeLauncherMissing, "cc", ""},
		{"zsh", typed("% ", "cc") + "\nzsh: command not found: cc\n% ", shellBack, proto.CodeLauncherMissing, "cc", ""},
		{"dash", typed("$ ", "cc") + "\nsh: 1: cc: not found\n$ ", shellBack, proto.CodeLauncherMissing, "cc", ""},
		{"fish reprints the command", typed("> ", "cc") + "\nfish: Unknown command: cc\n" + typed("", "cc") + "\n^~^\n> ",
			shellBack, proto.CodeLauncherMissing, "cc", ""},
		{"the command's own line needs no shell in front", typed("$ ", "cc") + "\nbash: cc: command not found\n", shellAway,
			proto.CodeLauncherMissing, "cc", ""},
		{"slow rc: the terminal's echo, rc noise, the prompt and the shell's echo", typed("", "cc") +
			"\n/home/u/.bashrc: line 9: nvm: command not found\n" + typed("$ ", "cc") + "\nbash: cc: command not found\n$ ",
			shellBack, proto.CodeLauncherMissing, "cc", ""},
		{"claude missing in the launcher", typed("% ", "cc") + "\ncc:2: command not found: claude\n% ", shellBack,
			proto.CodeClaudeMissing, "claude", ""},
		{"claude missing while the launcher goes on", typed("% ", "cc") + "\ncc:2: command not found: claude\n", shellAway, "", "", ""},
		{"claude missing in rc noise before the shell's echo", typed("", "cc") + "\nbash: claude: command not found\n" +
			typed("$ ", "cc") + "\n", shellBack, "", "", ""},
		{"only the echo", typed("$ ", "cc"), shellBack, "", "", ""},
		{"older scrollback above the command", "$ cc --session-id 0e000000-0000-4000-8000-000000000999\nbash: cc: command not found\n" +
			typed("$ ", "cc") + "\n", shellBack, "", "", ""},
		{"no command on screen", "bash: cc: command not found\n$ ", shellBack, "", "", ""},
		{"a longer name is another command", typed("$ ", "cc") + "\nbash: cc_work: command not found\n$ ", shellBack, "", "",
			"bash: cc_work: command not found"},
		{"PROMPT_COMMAND noise", typed("$ ", "cc") + "\nbash: __git_ps1: command not found\n$ ", shellBack, "", "",
			"bash: __git_ps1: command not found"},
		{"claude's own output quoting a shell", typed("$ ", "cc") + "\n  ⎿  bash: cc: command not found\n", shellBack, "", "", ""},
	} {
		refused, other := p.verdict(tc.screen, tc.back)
		var re *rpc.Error
		if tc.code == "" {
			if refused != nil {
				t.Errorf("%s: refused %v", tc.name, refused)
			}
		} else if !errors.As(refused, &re) || re.Code != tc.code {
			t.Errorf("%s: refused %v, want %s", tc.name, refused, tc.code)
		} else {
			var data map[string]string
			_ = json.Unmarshal(re.Data, &data)
			if data["missing"] != tc.word || data["launcher"] != "cc" || data["where"] != startMissWhere ||
				data["delivered"] != deliveredNone || data["pane_shell"] == "" || data["launcher_shell"] == "" || data["rc_file"] == "" {
				t.Errorf("%s: data %v", tc.name, data)
			}
		}
		if other != tc.other {
			t.Errorf("%s: other %q, want %q", tc.name, other, tc.other)
		}
	}
}

// A managed launcher's command is the node's personad: a path the tab cannot run is the launcher missing there; the
// plain claude launcher missing is claude missing.
func TestStartProbeOfManagedAndPlainClaude(t *testing.T) {
	managed := &startProbe{anchor: probeID, program: "/opt/p/personad", launcher: "work", paneShell: "zsh"}
	refused, _ := managed.verdict(typed("% ", "/opt/p/personad launch work")+"\nzsh: no such file or directory: /opt/p/personad\n% ", shellBack)
	var re *rpc.Error
	if !errors.As(refused, &re) || re.Code != proto.CodeLauncherMissing || !strings.Contains(re.Message, "/opt/p/personad") {
		t.Errorf("managed: %v", refused)
	}
	refused, _ = managed.verdict(typed("% ", "/opt/p/personad launch work")+
		"\npersonad launch: claude not found in PATH or the usual install locations\n% ", shellBack)
	if !errors.As(refused, &re) || re.Code != proto.CodeClaudeMissing {
		t.Errorf("managed without claude: %v", refused)
	}
	plain := &startProbe{anchor: probeID, program: "claude", launcher: "claude", paneShell: "bash"}
	refused, _ = plain.verdict(typed("$ ", "claude")+"\nbash: claude: command not found\n$ ", shellAway)
	if !errors.As(refused, &re) || re.Code != proto.CodeClaudeMissing {
		t.Errorf("plain claude: %v", refused)
	}
}

// The refusal says what the shell printed and where the launchers are listed, and names the tab's shell from the line
// (else the shell the tab is thought to run).
func TestStartProbeRefusalWords(t *testing.T) {
	p := &startProbe{anchor: probeID, program: "cc", launcher: "cc", paneShell: "zsh"}
	refused, _ := p.verdict(typed("$ ", "cc")+"\nbash: cc: command not found\n$ ", shellBack)
	var re *rpc.Error
	if !errors.As(refused, &re) {
		t.Fatalf("refused %v", refused)
	}
	lshell, rc := launcherShell()
	for _, want := range []string{"(bash)", `"bash: cc: command not found"`, lshell + " (" + rc + ")", "Define cc"} {
		if !strings.Contains(re.Message, want) {
			t.Errorf("message %q lacks %q", re.Message, want)
		}
	}
	refused, _ = p.verdict(typed("$ ", "cc")+"\ncc: command not found\n$ ", shellBack)
	if !errors.As(refused, &re) || !strings.Contains(string(re.Data), `"pane_shell":"zsh"`) {
		t.Errorf("a line naming no shell: %v %s", refused, re.Data)
	}
}

// The assistant's headless start reads the same lines: dash, ksh and fish name the missing command too.
func TestMissingFromOtherShells(t *testing.T) {
	for _, tc := range []struct {
		launcher string
		line     string
		want     string
	}{
		{"cc_gone", "sh: 1: cc_gone: not found", proto.CodeLauncherMissing},
		{"cc_gone", "ksh: cc_gone: not found", proto.CodeLauncherMissing},
		{"cc_gone", "fish: Unknown command: cc_gone", proto.CodeLauncherMissing},
		{"cc_work", "fish: Unknown command: claude", proto.CodeClaudeMissing},
		{"cc_work", "bash: /opt/x/claude: No such file or directory", proto.CodeClaudeMissing},
	} {
		if got := missingFrom(tc.launcher, []string{tc.line}, true); got != tc.want {
			t.Errorf("missingFrom(%s, %q) = %q, want %q", tc.launcher, tc.line, got, tc.want)
		}
	}
}
