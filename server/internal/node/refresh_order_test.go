package node

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/proto"
)

type orderedSnapshotHost struct {
	host.Host
	entered chan struct{}
	release chan struct{}
	reads   atomic.Int32
	latest  *host.Layout
}

func (h *orderedSnapshotHost) Snapshot(ctx context.Context) (*host.Layout, error) {
	if h.reads.Add(1) == 1 {
		close(h.entered)
		select {
		case <-h.release:
			return &host.Layout{Spaces: []host.Space{{ID: "old", Label: "old"}}}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return h.latest, nil
}

// A poll taken before a new Codex starts must not erase the newer refresh made by
// startCodex. Otherwise the running terminal is relisted as read-only history.
func TestLateHostRefreshCannotDowngradeLiveCodexToHistory(t *testing.T) {
	t.Run("display", func(t *testing.T) { testLateHostRefresh(t, false) })
	t.Run("strict inventory", func(t *testing.T) { testLateHostRefresh(t, true) })
}

func testLateHostRefresh(t *testing.T, strict bool) {
	t.Helper()
	n, _, _, _, home := orcaNode(t)
	n.codices = newCodexRegistry()
	profile := filepath.Join(home, ".codex")
	const id = "0e000000-0000-4000-8000-000000000010"
	const pane = "orca:tab:leaf"
	writeRollout(t, home, id, orcaCwd, time.Now(), time.Now(), event("task_started"), event("task_complete"))
	n.configureCodex(proto.CodexConfig{Homes: []string{profile}, LegacyHome: profile})
	n.bindPane(pane, id)
	live := host.Agent{Agent: "codex", PaneID: pane, Cwd: orcaCwd, WorkspaceID: "current", AgentStatus: proto.StatusDone}
	h := &orderedSnapshotHost{Host: n.hosts.All()[0], entered: make(chan struct{}), release: make(chan struct{}),
		latest: &host.Layout{Agents: []host.Agent{live}, Panes: []host.Agent{live}, Spaces: []host.Space{{ID: "current", Label: "current"}}}}
	n.hosts = host.NewRegistry(h)
	var release sync.Once
	t.Cleanup(func() { release.Do(func() { close(h.release) }) })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- n.refreshHostInventory(ctx, strict) }()
	select {
	case <-h.entered:
	case <-ctx.Done():
		t.Fatal("old snapshot did not start")
	}
	if err := n.refreshHosts(ctx); err != nil {
		t.Fatal(err)
	}
	if s := byID(n.sessionList(ctx, false))[id]; s.PaneID != pane || s.SourceKind != "" {
		t.Fatalf("new refresh did not establish live terminal: %+v", s)
	}
	release.Do(func() { close(h.release) })
	select {
	case err := <-done:
		if strict && err == nil {
			t.Fatalf("superseded inventory was accepted: %v", err)
		}
		if !strict && err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("old snapshot did not complete")
	}
	if s := byID(n.sessionList(ctx, false))[id]; s.PaneID != pane || s.SourceKind != "" {
		t.Fatalf("late old snapshot downgraded the live terminal: source=%q pane=%q capabilities=%+v", s.SourceKind, s.PaneID, s.Capabilities)
	}
	n.mu.Lock()
	_, current := n.layouts[host.Orca].spaces["current"]
	n.mu.Unlock()
	if !current {
		t.Fatal("late old snapshot replaced the current layout")
	}

	// A genuinely newer empty snapshot still removes the terminal. Protecting
	// refresh order must not preserve a session after its process exits.
	h.latest = &host.Layout{}
	if err := n.refreshHosts(ctx); err != nil {
		t.Fatal(err)
	}
	if s := byID(n.sessionList(ctx, false))[id]; s.SourceKind != "codex_history" || s.PaneID != "" || s.Capabilities.Send {
		t.Fatalf("newer exit snapshot did not remove the live terminal: %+v", s)
	}
}
