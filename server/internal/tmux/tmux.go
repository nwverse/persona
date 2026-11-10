// Package tmux drives a pane of a tmux server through the tmux command: what its screen shows and the keys pressed in
// it. A pane id is tmux's own ("%11"), unique within one server; the server is the default one unless Socket names
// another (a server started with -L or -S), even for a node started inside a pane of another server: tmux runs without
// the node's own TMUX. Nothing here reads tmux's layout or streams its output: a pane Persona did not start is reached
// by the id Claude Code itself wrote down (sessions/<pid>.json "tmux").
package tmux

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync/atomic"
	"time"
)

// Client runs tmux for one server.
type Client struct {
	Bin    string // the tmux to run, "" for the one on PATH
	Socket string // the server's socket, "" for the default server
}

// ErrNoServer marks a tmux that is not running (no server for the socket): the pane is gone.
var ErrNoServer = fmt.Errorf("tmux: no server")

func (c Client) bin() string {
	if c.Bin != "" {
		return c.Bin
	}
	return "tmux"
}

func (c Client) run(ctx context.Context, stdin string, args ...string) (string, error) {
	if c.Socket != "" {
		args = append([]string{"-S", c.Socket}, args...)
	}
	cmd := exec.CommandContext(ctx, c.bin(), args...)
	if c.Socket == "" {
		// tmux without -S runs against the server $TMUX names: a node started inside a pane of another server would
		// read and type into that server's pane of the same id.
		cmd.Env = envWithout(os.Environ(), "TMUX", "TMUX_PANE")
	}
	// A tmux wrapper whose child keeps the output pipe open would otherwise hold Run past ctx (or past its own exit).
	cmd.WaitDelay = time.Second
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	err := cmd.Run()
	if errors.Is(err, exec.ErrWaitDelay) {
		err = nil // tmux itself exited 0; only a child it left behind held the pipe
	}
	if err != nil {
		if strings.Contains(errb.String(), "no server running") || strings.Contains(errb.String(), "error connecting to") {
			return out.String(), fmt.Errorf("%w: %s", ErrNoServer, strings.TrimSpace(errb.String()))
		}
		return out.String(), fmt.Errorf("tmux %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

// envWithout is env without the variables named.
func envWithout(env []string, names ...string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if name, _, _ := strings.Cut(kv, "="); !slices.Contains(names, name) {
			out = append(out, kv)
		}
	}
	return out
}

// Version is the tmux client's version ("tmux 3.6a"), "" when tmux is not installed or not running.
func (c Client) Version(ctx context.Context) string {
	out, err := c.run(ctx, "", "-V")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// Capture is what the pane shows: its visible screen, or its last lines rows of history with lines > 0. ansi keeps
// the colors tmux re-emits.
func (c Client) Capture(ctx context.Context, pane string, lines int, ansi bool) (string, error) {
	args := []string{"capture-pane", "-p", "-t", pane}
	if ansi {
		args = append(args, "-e")
	}
	if lines > 0 {
		args = append(args, "-S", fmt.Sprintf("-%d", lines))
	}
	out, err := c.run(ctx, "", args...)
	if err != nil {
		return "", err
	}
	return out, nil
}

// tmuxKeys translates Persona's key names (host.Host.SendKeys) to tmux's.
var tmuxKeys = map[string]string{
	"enter": "Enter", "esc": "Escape", "tab": "Tab", "shift+tab": "BTab", "backspace": "BSpace", "space": "Space",
	"up": "Up", "down": "Down", "left": "Left", "right": "Right", "home": "Home", "end": "End",
	"pageup": "PageUp", "pagedown": "PageDown", "delete": "DC", "insert": "IC",
}

// Keys presses keys in the pane; an unknown name is refused before any key is pressed.
func (c Client) Keys(ctx context.Context, pane string, keys ...string) error {
	args := []string{"send-keys", "-t", pane}
	for _, k := range keys {
		name, ok := tmuxKeys[k]
		if !ok {
			if rest, cut := strings.CutPrefix(k, "ctrl+"); cut && len(rest) == 1 {
				name = "C-" + rest
			} else if rest, cut := strings.CutPrefix(k, "alt+"); cut && len(rest) == 1 {
				name = "M-" + rest
			} else {
				name = k // an xterm key name tmux knows ("F1", "Escape")
			}
		}
		args = append(args, name)
	}
	_, err := c.run(ctx, "", args...)
	return err
}

// bufferPrefix names this process's tmux buffers: another Persona on the same tmux server (a dev node next to the
// installed one) has a prefix of its own, so neither loads over a buffer the other has yet to paste.
var bufferPrefix = fmt.Sprintf("persona-%d-%s", os.Getpid(), randomHex(4))

var bufferSeq atomic.Uint64

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b) // crypto/rand.Read does not fail
	return hex.EncodeToString(b)
}

// dropBufferTimeout bounds the cleanup of a buffer whose paste failed.
const dropBufferTimeout = 2 * time.Second

// SendText types text into the pane without submitting it, through a tmux buffer so that a line break is text and not
// Enter (bracketed paste when the program takes it). Empty text types nothing. The buffer never outlives the call: the
// paste deletes it, and a paste that fails (the pane closed, ctx ended) is followed by an explicit delete.
func (c Client) SendText(ctx context.Context, pane, text string) error {
	if text == "" {
		return nil
	}
	buf := fmt.Sprintf("%s-%d", bufferPrefix, bufferSeq.Add(1))
	if _, err := c.run(ctx, text, "load-buffer", "-b", buf, "-"); err != nil {
		return err
	}
	if _, err := c.run(ctx, "", "paste-buffer", "-p", "-d", "-b", buf, "-t", pane); err != nil {
		c.dropBuffer(ctx, buf)
		return err
	}
	return nil
}

// dropBuffer deletes a buffer best-effort, on a short context of its own so that it runs after ctx has ended: the text
// it holds would otherwise stay in the user's tmux server (list-buffers, choose-buffer).
func (c Client) dropBuffer(ctx context.Context, buf string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), dropBufferTimeout)
	defer cancel()
	_, _ = c.run(ctx, "", "delete-buffer", "-b", buf) // the paste error is the one that matters
}

// Prompt types text and presses Enter.
func (c Client) Prompt(ctx context.Context, pane, text string) error {
	if err := c.SendText(ctx, pane, text); err != nil {
		return err
	}
	return c.Keys(ctx, pane, "enter")
}
