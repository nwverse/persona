package node

import (
	"runtime"
	"strings"
)

// shellScriptGOOS is the platform the scripts below are quoted for (tests set it).
var shellScriptGOOS = runtime.GOOS

// scriptPath quotes a native path for a script the node hands to a shell as an argument (`<shell> -ic <script>`,
// loginShell): the assistant's launch, the Codex app-server and archive, the model list, the launcher and sign-in
// checks. Elsewhere it is shellQuote. On Windows that shell is Git Bash started by a native process, and the argument
// boundary eats the backslashes shellQuote escapes, leaving an unmatched quote (#5487). There the path goes with
// forward slashes, which Git Bash and Windows programs both read as the same path, and without a backslash in its
// quoting: double quotes for a path with an apostrophe when nothing in it expands there; shellQuote otherwise (an
// apostrophe together with $ ` " or ! is the one form still exposed; docs/lessons/windows.md).
// Not for commands typed into a terminal (herdr panes): no argument boundary there, launcherCommand quotes as before.
func scriptPath(path string) string {
	if shellScriptGOOS != "windows" {
		return shellQuote(path)
	}
	path = strings.ReplaceAll(path, `\`, "/")
	if strings.Contains(path, "'") && !strings.ContainsAny(path, "$`\"!") {
		return `"` + path + `"`
	}
	return shellQuote(path)
}

// launcherForScript is launcherCommand for such a script: its paths (a managed launcher's personad, claude_start_dir)
// quoted with scriptPath.
func (n *Node) launcherForScript(name string) string { return n.launcherCommandQ(name, scriptPath) }
