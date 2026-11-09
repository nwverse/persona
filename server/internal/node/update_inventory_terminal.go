package node

import (
	"context"
	"errors"
	"flag"
	"io"
	"slices"
	"strings"

	"github.com/nwverse/persona/server/internal/host"
)

type updateTerminalSource struct {
	endpoint string
	activity updateCodexObservation
}

// Only an explicit remote resume binds a terminal to the already observed
// app-server. A title, cwd, historical session or default daemon is not a binding.
// Other CLI forms remain unsupported rather than guessing their flag semantics.
func updateRemoteResume(args []string) (endpoint, thread string) {
	if len(args) < 2 || progName(args[0]) != "codex" || args[1] != "resume" {
		return "", ""
	}
	f := flag.NewFlagSet("resume", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	f.StringVar(&endpoint, "remote", "", "")
	if err := f.Parse(args[2:]); err != nil || f.NArg() != 1 || !validID(f.Arg(0)) ||
		!strings.HasPrefix(endpoint, "unix://") && !strings.HasPrefix(endpoint, "ws://") {
		return "", ""
	}
	return endpoint, f.Arg(0)
}

// Host status is display evidence only. The activity belongs to a configured
// app-server and is emitted by that source, including all its loaded children.
func (n *Node) observeUpdateHosts(ctx context.Context, census []updateProcess, authorities map[string]updateTerminalSource, sources map[string]string) (map[int]bool, error) {
	covered := map[int]bool{}
	for _, h := range n.hosts.All() {
		fresh, ok := h.(updateSnapshotSource)
		if !ok {
			return nil, errors.New("configured host lacks a fresh update inventory contract")
		}
		snapshot, err := fresh.SnapshotForUpdate(ctx)
		if errors.Is(err, host.ErrNotRunning) {
			sources["host:"+h.Kind()] = "not-running"
			continue
		}
		if err != nil || snapshot == nil {
			return nil, errors.New("complete host snapshot unavailable")
		}
		bindings := map[int]string{}
		for _, a := range snapshot.Agents {
			if a.Agent != "codex" || a.PaneID == "" || a.AgentSession == nil || a.AgentSession.Kind != "id" || !validID(a.AgentSession.Value) {
				return nil, errors.New("terminal agent lacks authoritative background inventory")
			}
			pane := host.ParsePane(a.PaneID)
			if pane.Kind != h.Kind() {
				return nil, errors.New("terminal host identity conflicts")
			}
			pids, err := h.PaneProcesses(ctx, pane)
			if err != nil {
				return nil, errors.New("terminal foreground identity unavailable")
			}
			matches := 0
			for _, p := range census {
				if !slices.Contains(pids, p.PID) || p.RemoteEndpoint == "" || p.RemoteThread != a.AgentSession.Value {
					continue
				}
				if p.PID <= 0 || !validUpdateRevision(p.Signature) {
					return nil, errors.New("terminal process identity unavailable")
				}
				source, ok := authorities[p.RemoteEndpoint]
				if !ok || source.endpoint != p.RemoteEndpoint || source.activity.Generation == "" || !validUpdateRevision(source.activity.Revision) {
					return nil, errors.New("terminal remote activity authority unavailable")
				}
				found := false
				for _, row := range source.activity.Threads {
					found = found || row.ID == p.RemoteThread
				}
				if !found || covered[p.PID] {
					return nil, errors.New("terminal runtime binding is missing or conflicting")
				}
				matches++
				covered[p.PID] = true
				bindings[p.PID] = updateDigest([]string{a.PaneID, p.Signature, p.RemoteThread, source.activity.Generation, source.activity.Revision})
			}
			if matches != 1 {
				return nil, errors.New("terminal lacks a unique explicit runtime binding")
			}
		}
		sources["host:"+h.Kind()] = updateDigest(struct {
			Layout   *host.Layout
			Bindings map[int]string
		}{snapshot, bindings})
	}
	return covered, ctx.Err()
}
