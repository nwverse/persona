package node

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"

	"github.com/nwverse/persona/server/internal/screen"
	"github.com/nwverse/persona/server/internal/tmux"
)

// The interactive side of `personad assistant-tui`. Claude Code runs in a tmux server of its own (a private socket in
// a private directory): tmux draws its screen, which Persona's screen readers understand (claudeready.go, screen), and
// takes keys and bracketed pastes (internal/tmux). The bridge keeps a tmux client attached through a pseudo-terminal
// of its own, with destroy-unattached on: however the bridge ends, even killed, its terminal closes, the client goes,
// and the session with Claude Code in it goes too. remain-on-exit keeps a pane whose Claude Code exited, so what it
// printed and its exit status can be read.

// tuiNoTranscript is what Claude Code shows when it keeps no transcript (2.1.325).
const tuiNoTranscript = "Transcript saving is off"

// tuiPane is the pane the bridge's session runs in.
const tuiPane = "a"

// tuiConf is the private server's configuration.
const tuiConf = `set -g destroy-unattached on
set -g status off
set -g focus-events on
set -g history-limit 5000
set -g default-terminal screen-256color
set -sg escape-time 0
set -wg remain-on-exit on
`

type tuiBridge struct {
	spec   tuiSpec
	out    *tuiOut
	stderr io.Writer

	dir    string
	tm     tmux.Client
	client *exec.Cmd
	ptmx   *os.File

	mu        sync.Mutex
	tr        *tuiTranslator
	offset    int64     // where the transcript's new lines start
	path      string    // the transcript, once found
	rowAt     time.Time // when the transcript last grew
	tasks     string    // the background tasks last reported, as a key
	tasksSent bool      // a list went out at all
}

// tmuxRun runs a tmux command against the private server.
func (b *tuiBridge) tmuxRun(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, b.tm.Bin, append([]string{"-S", b.tm.Socket}, args...)...)
	cmd.Env = envWithoutTMUX(os.Environ())
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func envWithoutTMUX(env []string) []string {
	out := env[:0:0]
	for _, kv := range env {
		if !strings.HasPrefix(kv, "TMUX=") && !strings.HasPrefix(kv, "TMUX_PANE=") {
			out = append(out, kv)
		}
	}
	return out
}

// launch starts the private tmux server with Claude Code in it, attached through the bridge's pseudo-terminal.
func (b *tuiBridge) launch() error {
	p, err := exec.LookPath("tmux")
	if err != nil {
		return errors.New("tmux is not installed on this computer")
	}
	if b.dir, err = os.MkdirTemp("", "persona-tui-"); err != nil {
		return err
	}
	conf := filepath.Join(b.dir, "tmux.conf")
	if err := os.WriteFile(conf, []byte(tuiConf), 0o600); err != nil {
		return err
	}
	b.tm = tmux.Client{Bin: p, Socket: filepath.Join(b.dir, "s")}
	// Claude Code in the pane must not know it is in tmux: Persona's tmux engine finds the agents in tmux panes by their
	// environment and by what Claude Code writes down of its pane, and this pane is no Agent's, on no server it drives.
	args := append([]string{"-S", b.tm.Socket, "-f", conf, "new-session", "-s", tuiPane, "-c", b.spec.Cwd, "--",
		"env", "-u", "TMUX", "-u", "TMUX_PANE", "-u", "TERM_PROGRAM", "-u", "TERM_PROGRAM_VERSION"}, b.spec.TUI...)
	b.client = exec.Command(p, args...)
	b.client.Dir = b.spec.Cwd
	b.client.Env = append(envWithoutTMUX(os.Environ()), "TERM=xterm-256color")
	if b.ptmx, err = pty.StartWithSize(b.client, &pty.Winsize{Rows: 50, Cols: 200}); err != nil {
		return fmt.Errorf("start tmux: %w", err)
	}
	go func() { _, _ = io.Copy(io.Discard, b.ptmx) }()
	go func() { _ = b.client.Wait() }()
	return nil
}

// shutdown ends the private server and removes its directory.
func (b *tuiBridge) shutdown() {
	if b.tm.Socket != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_, _ = b.tmuxRun(ctx, "kill-server")
		cancel()
	}
	if b.ptmx != nil {
		_ = b.ptmx.Close()
	}
	if b.client != nil && b.client.Process != nil {
		_ = b.client.Process.Kill()
	}
	if b.dir != "" {
		_ = os.RemoveAll(b.dir)
	}
}

// screens reads the pane, plain and with its colors.
func (b *tuiBridge) screens(ctx context.Context) (plain, ansi string, err error) {
	if plain, err = b.tm.Capture(ctx, tuiPane, "visible", 0, false); err != nil {
		return "", "", err
	}
	ansi, err = b.tm.Capture(ctx, tuiPane, "visible", 0, true)
	return plain, ansi, err
}

// dead says whether Claude Code exited, and with what status.
func (b *tuiBridge) dead(ctx context.Context) (bool, int) {
	out, err := b.tmuxRun(ctx, "display-message", "-p", "-t", tuiPane, "#{pane_dead} #{pane_dead_status}")
	if err != nil {
		return true, 1 // the server is gone
	}
	var d, status int
	if _, err := fmt.Sscan(strings.TrimSpace(out), &d, &status); err != nil {
		status = 0
	}
	return d == 1, status
}

// screenTail are the screen's last non-blank lines, for saying why it ended.
func screenTail(plain string, n int) []string {
	var out []string
	for _, l := range strings.Split(plain, "\n") {
		if t := strings.TrimSpace(l); t != "" {
			out = append(out, t)
		}
	}
	if len(out) > n {
		out = out[len(out)-n:]
	}
	return out
}

// awaitReady waits for Claude Code's input box, answering the start-up screens Persona answers for a terminal start
// (claudeready.go waitReady): the folder's trust, the first-run screens, the Bypass Permissions confirmation when the
// start asked for that mode, and a guide that declines with Esc. Any other screen that waits for a person ends the
// start with what it says.
func (b *tuiBridge) awaitReady(ctx context.Context) error {
	deadline := time.Now().Add(tuiStartTimeout)
	streak, bypassTries, head := 0, 0, ""
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
		plain, _, err := b.screens(ctx)
		if dead, status := b.dead(ctx); dead || err != nil {
			return fmt.Errorf("Claude Code exited before it was ready (status %d): %s", status, strings.Join(screenTail(plain, 5), " | "))
		}
		keys := trustKeys(plain)
		if keys == nil {
			keys = firstRunKeys(plain)
		}
		if keys == nil && b.spec.AcceptBypass {
			if keys = bypassConfirmKeys(plain); keys != nil {
				if bypassTries++; bypassTries > bypassConfirmTries {
					return errors.New(bypassNotAccepted)
				}
			}
		}
		if keys == nil && screen.Guide(plain) != "" {
			keys = screen.GuideKeys
		}
		if keys != nil {
			_ = b.tm.Keys(ctx, tuiPane, keys...)
			streak = 0
			time.Sleep(time.Second)
			continue
		}
		if reason := firstRunScreen(unframed(plain)); reason != "" {
			return errors.New(reason)
		}
		if strings.Contains(plain, tuiNoTranscript) {
			// Started from inside another Claude Code's session (its CLAUDE_CODE_CHILD_SESSION came along): it keeps no
			// transcript, and the bridge reads every turn from it.
			return errors.New("Claude Code is not saving this conversation's transcript here (it was started from inside another Claude Code session)")
		}
		if screen.Choosing(plain) {
			head, streak = screen.DialogHead(plain), 0
			continue
		}
		if inputBoxVisible(plain) {
			if streak++; streak >= 2 {
				return nil
			}
			continue
		}
		streak = 0
	}
	if head != "" {
		return fmt.Errorf("Claude Code is waiting on %q, which Persona does not answer", head)
	}
	return fmt.Errorf("Claude Code showed no input box within %s", tuiStartTimeout)
}
