package node

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
)

// herdr's server is not started on Windows: the node keeps reporting it missing, as before (herdrstart.go).
func launchHerdrServer(context.Context, string, string) (<-chan error, error) {
	return nil, errors.New("starting herdr is not supported on Windows")
}

func lockHerdrDir(string) (func(), error) { return func() {}, nil }

// herdrPaneShell: classic herdr's create answers do not name the pane shell on Windows, and the node does not read
// its [terminal] default_shell. PowerShell is herdr's default there; an adopted or herdr-core pane that names its
// shell is normalized by paneShellKinds. A var for tests.
var herdrPaneShell = func() string { return windowsPowerShellPane }

// paneShellKinds maps a reported Windows PowerShell to the shell kind that selects the PowerShell outer start line.
// A reported Git Bash stays POSIX. Without a report, herdrPaneShell's PowerShell default is the safe choice.
func paneShellKinds(shells []string) []string {
	out := make([]string, len(shells))
	for i, shell := range shells {
		if windowsPowerShellProgram(shell) {
			shell = windowsPowerShellPane
		}
		out[i] = shell
	}
	return out
}

// paneShellName normalizes Windows executable names for shell recognition and POSIX assignment syntax alike.
func paneShellName(name string) string {
	return strings.TrimSuffix(strings.TrimPrefix(strings.ToLower(filepath.Base(name)), "-"), ".exe")
}
