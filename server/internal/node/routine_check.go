package node

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"time"

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
	err := cmd.Wait()
	// Also stop children left behind by a shell that exited before its deadline.
	_ = osproc.StopGroup(cmd.Process.Pid, true)
	if ctx.Err() != nil {
		return proto.RoutineCheckResult{}, ctx.Err()
	}
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		return proto.RoutineCheckResult{}, err
	}
	return proto.RoutineCheckResult{
		ExitCode: cmd.ProcessState.ExitCode(),
		Reason:   strings.TrimSpace(strings.ToValidUTF8(string(output.line), "")),
	}, nil
}

type routineCheckLine struct {
	line []byte
	done bool
}

func (w *routineCheckLine) Write(p []byte) (int, error) {
	n := len(p)
	if w.done {
		return n, nil
	}
	if i := bytes.IndexByte(p, '\n'); i >= 0 {
		p = p[:i]
		w.done = true
	}
	room := 512 - len(w.line)
	w.line = append(w.line, p[:min(room, len(p))]...)
	w.done = w.done || len(w.line) == 512
	return n, nil
}
