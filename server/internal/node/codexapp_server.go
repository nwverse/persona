package node

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
)

// codexAppCall performs one metadata request on an isolated app-server. Callers never resume or run a turn in a
// desktop-owned thread: its live owner handles that through the desktop bus.
type codexAppCall func(method string, params any, result any) error

type codexAppServerError struct {
	Method  string
	Code    int
	Message string
}

func (e *codexAppServerError) Error() string { return fmt.Sprintf("%s: %s", e.Method, e.Message) }

// withCodexAppServer starts a short-lived metadata client in the desktop's Codex home. Calls are sequential, bounded
// by the context, and use JSON on standard input rather than a shell command line.
func withCodexAppServer(ctx context.Context, cli, home, cwd string, use func(codexAppCall) error) error {
	ctx, cancel := context.WithTimeout(ctx, codexAppSeedWait)
	defer cancel()
	program, env, err := codexStartProgram(cli, true, append(desktopSeedEnv(os.Environ()), "CODEX_HOME="+home))
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, program, "app-server")
	cmd.Dir, cmd.Env = cwd, env
	prepareDesktopChild(cmd)
	in, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		in.Close()
		return err
	}
	if err := cmd.Start(); err != nil {
		in.Close()
		out.Close()
		return err
	}
	defer func() {
		_ = in.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	lines := bufio.NewScanner(out)
	lines.Buffer(make([]byte, 64<<10), 64<<20)
	id := 0
	call := func(method string, params any, result any) error {
		id++
		b, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
		if err != nil {
			return err
		}
		if _, err := in.Write(append(b, '\n')); err != nil {
			return err
		}
		for lines.Scan() {
			var m struct {
				ID     *int            `json:"id"`
				Result json.RawMessage `json:"result"`
				Error  *struct {
					Code    int    `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if json.Unmarshal(lines.Bytes(), &m) != nil || m.ID == nil || *m.ID != id {
				continue
			}
			if m.Error != nil {
				return &codexAppServerError{Method: method, Code: m.Error.Code, Message: m.Error.Message}
			}
			if result != nil {
				if err := json.Unmarshal(m.Result, result); err != nil {
					return fmt.Errorf("%s: invalid response: %w", method, err)
				}
			}
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := lines.Err(); err != nil {
			return fmt.Errorf("%s: %w", method, err)
		}
		return fmt.Errorf("%s: app-server closed before answering", method)
	}
	if err := call("initialize", map[string]any{
		"clientInfo":   map[string]any{"name": codexAppClient, "title": "Persona", "version": "1"},
		"capabilities": map[string]any{"experimentalApi": true}, // project/* requires this capability
	}, nil); err != nil {
		return err
	}
	if _, err := in.Write([]byte("{\"method\":\"initialized\"}\n")); err != nil {
		return err
	}
	return use(call)
}
