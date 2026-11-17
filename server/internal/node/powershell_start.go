package node

import (
	"errors"
	"strings"
)

// windowsPowerShellPane identifies a pane that reads PowerShell while its Agent start file is read by Git Bash.
// windowsCmdPane identifies cmd.exe, which cannot read either the POSIX line or the PowerShell handoff.
// The Windows platform files produce these values; other platforms never produce them.
const (
	windowsPowerShellPane = "windows-powershell"
	windowsCmdPane        = "windows-cmd"
)

// windowsShellName normalizes a Windows shell program without depending on the test machine's path semantics.
func windowsShellName(shell string) string {
	shell = strings.ReplaceAll(shell, `\`, "/")
	shell = shell[strings.LastIndex(shell, "/")+1:]
	return strings.TrimSuffix(strings.TrimPrefix(strings.ToLower(shell), "-"), ".exe")
}

// windowsPowerShellProgram reports whether a reported shell program is Windows PowerShell or PowerShell 7.
func windowsPowerShellProgram(shell string) bool {
	shell = windowsShellName(shell)
	return shell == "powershell" || shell == "pwsh"
}

// windowsShellProgram is shellProgram for Windows names, including cmd.exe. The last foreground program must pass it
// before its answer can replace config. Other programs fall through to config rather than being guessed as shells.
func windowsShellProgram(shell string) bool {
	name := windowsShellName(shell)
	return name == "cmd" || shells[name]
}

// windowsPaneShellKind canonicalizes known Windows shells so the existing takesAssign and start-line choices see the
// same shell name on every OS. Unknown programs keep their original value and retain main's direct-command behavior.
func windowsPaneShellKind(shell string) string {
	name := windowsShellName(shell)
	switch {
	case name == "powershell" || name == "pwsh":
		return windowsPowerShellPane
	case name == "cmd":
		return windowsCmdPane
	case shells[name]:
		return name
	default:
		return shell
	}
}

// windowsForegroundUsable says whether the foreground answer can select the pane shell: its innermost/last program is
// a shell the node knows. An error or empty answer reaches this as nil and therefore falls through to config.
func windowsForegroundUsable(foreground []string) bool {
	return len(foreground) > 0 && windowsShellProgram(foreground[len(foreground)-1])
}

// windowsPaneShellDecision is the pure Windows source order: host report, usable foreground, herdr config, then
// PowerShell (herdr's default). The Windows integration supplies config only for classic herdr and only when needed.
func windowsPaneShellDecision(hostShell string, foreground []string, configShell string) []string {
	if hostShell != "" {
		return []string{windowsPaneShellKind(hostShell)}
	}
	if windowsForegroundUsable(foreground) {
		// Only the innermost process receives the typed line. Keeping an ancestor PowerShell in this new-pane
		// answer would also make takesAssign reject the whole stack and lose the start file's environment prefix.
		return []string{windowsPaneShellKind(foreground[len(foreground)-1])}
	}
	if configShell != "" {
		return []string{windowsPaneShellKind(configShell)}
	}
	return []string{windowsPowerShellPane}
}

// windowsPaneShellError refuses cmd.exe before a start file or pane input can be produced.
func windowsPaneShellError(shells ...string) error {
	if len(shells) > 0 && shells[len(shells)-1] == windowsCmdPane {
		return errors.New("the pane shell cmd.exe cannot start an Agent; PowerShell or Git Bash is required")
	}
	return nil
}

// powerShellPane reports whether the foreground pane shell reads PowerShell and therefore needs a PowerShell outer
// start line. Adopted-pane process stacks are parent-first: only the last program receives the typed line, so a
// PowerShell that launched Git Bash must not override the active Bash prompt.
func powerShellPane(shells ...string) bool {
	return len(shells) > 0 && shells[len(shells)-1] == windowsPowerShellPane
}

// startCommandShells are the shells that read the command inside a start file. A Windows PowerShell pane only reads
// the outer line; Git Bash reads the file interactively, just as loginShell reads launcher definitions on Windows.
func startCommandShells(shells []string) []string {
	if powerShellPane(shells...) {
		return []string{"bash"}
	}
	return shells
}

// powerShellQuote makes s one single-quoted PowerShell string. Backslashes, dollars, backticks and operators are
// literal inside single quotes. PowerShell recognizes ASCII apostrophes and four Unicode single-quote variants as
// delimiters; double each one to keep it literal. Double-quote variants need no escape inside a single-quoted string.
func powerShellQuote(s string) string {
	return "'" + strings.NewReplacer(
		"'", "''",
		"‘", "‘‘",
		"’", "’’",
		"‚", "‚‚",
		"‛", "‛‛",
	).Replace(s) + "'"
}

// powerShellSourceCommand is the Bash -c argument that sources path. It uses Bash's ANSI-C quote form rather than
// windowsScriptPath's '"'"' concatenation: embedded double quotes do not survive Windows PowerShell 5.1's legacy
// native-argument reconstruction, and PowerShell 7's MSVC escaping is not Git Bash's MSYS escaping. This form contains
// no double quote at all. PowerShell still applies its own single-quote doubling when the argument is typed.
func powerShellSourceCommand(path string) string {
	path = strings.ReplaceAll(path, `\`, "/")
	path = strings.NewReplacer(`'`, `\'`, "\n", `\n`, "\r", `\r`, "\t", `\t`).Replace(path)
	return ". $'" + path + "'"
}

// powerShellStartLine is the one line typed into a Windows PowerShell pane. & invokes the quoted Git Bash path
// instead of evaluating it as a string expression. -ic is loginShell's mode on Windows: ~/.bashrc supplies launcher
// aliases, functions and PATH. The Bash command sources the start file without an embedded native-boundary quote.
// The anchor is a PowerShell 5.1-compatible no-op after the command; && is not available there before PowerShell 7.
func powerShellStartLine(bash, path, anchor string) string {
	line := "& " + powerShellQuote(bash) + " -ic " + powerShellQuote(powerShellSourceCommand(path))
	if anchor != "" {
		line += "; $null = " + powerShellQuote(anchor)
	}
	return line
}

// powerShellStartShell is the Git Bash used by loginShell on Windows. A variable keeps typed-start tests independent
// of the test machine's operating system and installed shells.
var powerShellStartShell = interactiveShell
