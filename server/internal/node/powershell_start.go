package node

import "strings"

// windowsPowerShellPane identifies a pane that reads PowerShell while its Agent start file is read by Git Bash.
// The Windows platform files map powershell.exe and pwsh.exe to this value; other platforms never produce it.
const windowsPowerShellPane = "windows-powershell"

// windowsPowerShellProgram reports whether a reported shell program is Windows PowerShell or PowerShell 7.
// It uses slash normalization rather than filepath.Base so the same pure check runs in tests on every OS.
func windowsPowerShellProgram(shell string) bool {
	shell = strings.ReplaceAll(shell, `\`, "/")
	shell = shell[strings.LastIndex(shell, "/")+1:]
	shell = strings.TrimSuffix(strings.TrimPrefix(strings.ToLower(shell), "-"), ".exe")
	return shell == "powershell" || shell == "pwsh"
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
