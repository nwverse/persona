package node

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/osproc"
	"github.com/nwverse/persona/server/internal/proto"
)

func routineCheck(ctx context.Context, raw json.RawMessage) (proto.RoutineCheckResult, error) {
	var p proto.RoutineCheckParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return proto.RoutineCheckResult{}, err
	}
	if strings.TrimSpace(p.Command) == "" || len(p.Command) > proto.RoutineCheckMaxCommand ||
		p.Timeout < 1 || p.Timeout > proto.RoutineCheckMaxTimeout {
		return proto.RoutineCheckResult{}, errors.New("invalid routine check command or timeout")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(p.Timeout)*time.Second)
	defer cancel()

	cmd := loginShell(ctx, p.Command)
	cmd.Dir, _ = os.UserHomeDir()
	killGroupOnCancel(cmd)
	var output routineCheckLine
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		return proto.RoutineCheckResult{}, err
	}
	// exec stops watching the deadline once the shell exits, even if a child still holds the output open.
	waited := make(chan struct{})
	var watch sync.WaitGroup
	stop := sync.OnceFunc(func() { _ = osproc.StopGroup(cmd.Process.Pid, true) })
	watch.Go(func() {
		select {
		case <-ctx.Done():
			stop()
		case <-waited:
		}
	})
	err := cmd.Wait() // killGroupOnCancel also sets a bounded WaitDelay.
	close(waited)
	watch.Wait()
	stop() // A child may have closed its output and kept running.
	if ctx.Err() != nil {
		return proto.RoutineCheckResult{}, ctx.Err()
	}
	var exit *exec.ExitError
	if err != nil && !errors.Is(err, exec.ErrWaitDelay) && !errors.As(err, &exit) {
		return proto.RoutineCheckResult{}, err
	}
	return proto.RoutineCheckResult{
		ExitCode: cmd.ProcessState.ExitCode(),
		Reason:   strings.TrimSpace(strings.ToValidUTF8(string(output.reason()), "")),
	}, nil
}

// routineCheckLine keeps the check's reason: the first line of its output, up to 512 bytes, that is not the shell's
// own job-control warning. The node runs the check in an interactive login shell (`-ic`, so the user's aliases and
// functions work) and, as a service, has no terminal: bash then starts with "cannot set terminal process group (-1):
// Inappropriate ioctl for device" and "no job control in this shell" on stderr, which is not what the check said (#8876).
type routineCheckLine struct {
	line    []byte
	pending []byte // the line being written, until its newline
	done    bool
}

// shellJobControl is what an interactive shell prints about itself rather than for the check: the job-control warnings
// of a shell with no terminal (bash, zsh), with or without their "bash: " prefix, and the "exit" (or "logout") bash
// writes to stderr when the check ends with the exit builtin, terminal or not. bash translates the warnings: only the
// untranslated ones and bash's zh_CN ones are known, so in another language the reason is still bash's first line
// ("exit" is not translated).
var shellJobControl = lazyre.New(`^(?:(?:[\w./-]+: )?(?:cannot set terminal process group \(-?\d+\): .*|no job control in this shell\.?|can't set tty pgrp: .*|无法设定终端进程组 \(-?\d+\): .*|此 shell 中无任务控制)|exit|logout)$`)

func (w *routineCheckLine) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 && !w.done {
		i := bytes.IndexByte(p, '\n')
		part := p
		if i >= 0 {
			part, p = p[:i], p[i+1:]
		} else {
			p = nil
		}
		w.pending = append(w.pending, part[:min(512-len(w.pending), len(part))]...)
		if i >= 0 || len(w.pending) == 512 {
			w.take()
		}
	}
	return n, nil
}

// take ends the pending line: the reason, unless it is the shell's job-control warning.
func (w *routineCheckLine) take() {
	if !shellJobControl.Match(bytes.TrimSpace(w.pending)) {
		w.line, w.done = w.pending, true
	}
	w.pending = nil
}

// reason is the line kept, or the last line when the output did not end with a newline.
func (w *routineCheckLine) reason() []byte {
	if !w.done && len(w.pending) > 0 {
		w.take()
	}
	return w.line
}
