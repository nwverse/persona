package node

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type updateProcess struct {
	PID            int    `json:"pid"`
	Signature      string `json:"signature"`
	Endpoint       string `json:"endpoint"`
	RemoteEndpoint string `json:"remote_endpoint,omitempty"`
	RemoteThread   string `json:"remote_thread,omitempty"`
}

// List the owner's processes without reading environments or emitting command
// lines. Failure to enumerate is not an empty source. Unowned known agent
// processes are rejected by the collector, rather than inferred idle from files.
func updateProcessCensus(ctx context.Context) ([]updateProcess, error) {
	data, err := exec.CommandContext(ctx, "/bin/ps", "-ww", "-o", "pid=,command=", "-U", strconv.Itoa(os.Getuid())).Output()
	if err != nil {
		return nil, fmt.Errorf("complete owner process inventory unavailable")
	}
	out := []updateProcess{}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) < 2 {
			return nil, fmt.Errorf("invalid owner process inventory row")
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil || pid <= 0 {
			return nil, fmt.Errorf("invalid owner process identity")
		}
		args := fields[1:]
		agent := false
		for i, arg := range args {
			if i > 1 {
				break
			}
			name := strings.ToLower(filepath.Base(arg))
			if name == "claude" || name == "codex" || strings.HasSuffix(arg, "/claude-code/cli.js") {
				agent = true
			}
		}
		if !agent {
			continue
		}
		endpoint := ""
		if p, ok := codexProcessArgs(pid, args, ""); ok {
			endpoint = p.endpoint
		}
		p := updateProcess{PID: pid, Signature: updateDigest(args), Endpoint: endpoint}
		if progName(args[0]) == "codex" && endpoint == "" {
			// ps formats a command for display, losing argument boundaries. Only
			// the kernel argv may establish a remote-client binding.
			argv, err := updateProcessArgv(pid)
			if err != nil {
				return nil, fmt.Errorf("terminal process arguments unavailable: pid=%d", pid)
			}
			p.Signature = updateDigest(argv)
			p.RemoteEndpoint, p.RemoteThread = updateRemoteResume(argv)
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PID < out[j].PID })
	return out, ctx.Err()
}
