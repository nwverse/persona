package node

import (
	"context"
	"errors"

	"github.com/nwverse/persona/server/internal/herdr"
	"github.com/nwverse/persona/server/internal/host"
)

// herdr's server is not started on Windows: the node keeps reporting it missing, as before (herdrstart.go).
func launchHerdrServer(context.Context, string, string) (<-chan error, error) {
	return nil, errors.New("starting herdr is not supported on Windows")
}

func lockHerdrDir(string) (func(), error) { return func() {}, nil }

// herdrPaneShell is the fallback for herdr-owned paths such as its separate resume_argv. New starts resolve the
// actual pane through freshPaneShells below. A var for tests.
var herdrPaneShell = func() string { return windowsPowerShellPane }

// freshPaneShells resolves a new pane in source order. The host report needs no I/O. Otherwise one PaneForeground
// request reuses the adopted-pane reader; only a usable answer suppresses the classic-herdr config lookup. Errors,
// empty answers and unknown foreground programs fall through. Unix has its own unchanged implementation.
func (n *Node) freshPaneShells(ctx context.Context, launch host.Launcher, created *host.Created) []string {
	if created.Shell != "" {
		return windowsPaneShellDecision(created.Shell, nil, "")
	}
	if created.Pane.Kind != host.Herdr {
		// Tern's foreground answer is not classic herdr's parent-first shell stack; do not change its behavior.
		return windowsPaneShellDecision("", nil, "")
	}
	var foreground []string
	if f, ok := launch.(host.ForegroundNamer); ok {
		if names, err := f.PaneForeground(ctx, created.Pane); err == nil {
			foreground = names
		}
	}
	if windowsForegroundUsable(foreground) {
		return windowsPaneShellDecision("", foreground, "")
	}
	return windowsPaneShellDecision("", foreground, herdr.TerminalDefaultShell(n.home))
}

// paneShellKinds canonicalizes reported Windows shells for the shared pure start-line choices.
func paneShellKinds(shells []string) []string {
	out := make([]string, len(shells))
	for i, shell := range shells {
		out[i] = windowsPaneShellKind(shell)
	}
	return out
}

// paneShellName normalizes Windows executable names for shell recognition and POSIX assignment syntax alike.
func paneShellName(name string) string { return windowsShellName(name) }
