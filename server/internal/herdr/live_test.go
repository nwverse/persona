//go:build herdrlive

package herdr

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Against the herdr running on this machine: go test -tags herdrlive ./internal/herdr/
func TestLiveRPCMatchesCLI(t *testing.T) {
	ctx := context.Background()
	rpc := New()
	cli := &Client{Bin: "herdr"}
	t0 := time.Now()
	a, err := rpc.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rpcTook := time.Since(t0)
	t0 = time.Now()
	b, err := cli.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("snapshot: socket %s, CLI %s; %d agents, %d panes", rpcTook, time.Since(t0), len(a.Agents), len(a.Panes))
	if len(a.Agents) != len(b.Agents) || len(a.Panes) != len(b.Panes) || len(a.Tabs) != len(b.Tabs) {
		t.Fatalf("socket and CLI snapshots differ: %d/%d agents", len(a.Agents), len(b.Agents))
	}
	if len(a.Agents) == 0 {
		t.Skip("no agents to read")
	}
	pane := a.Agents[0].PaneID
	x, err := rpc.PaneRead(ctx, pane, "visible", 60)
	if err != nil {
		t.Fatal(err)
	}
	y, err := cli.PaneRead(ctx, pane, "visible", 60)
	if err != nil {
		t.Fatal(err)
	}
	if len(x) == 0 || len(y) == 0 {
		t.Fatalf("empty reads %d %d", len(x), len(y))
	}
	if _, err := rpc.PaneRead(ctx, pane, "recent-unwrapped", 30); err != nil {
		t.Fatal(err)
	}
	pids, err := rpc.PaneProcesses(ctx, pane)
	t.Logf("pane %s pids %v %v; version %q", pane, pids, err, rpc.Version(ctx))
	if _, err := rpc.AgentGet(ctx, "w999:p999"); !IsNotFound(err) {
		t.Fatalf("missing agent: %v", err)
	}
}

func TestLiveSubscribe(t *testing.T) {
	c := New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got := make(chan string, 16)
	go func() {
		_ = c.Subscribe(ctx, []Subscription{{"type": "tab.created"}, {"type": "tab.closed"}, {"type": "tab.renamed"}}, func(e Event) { got <- e.Event })
	}()
	if e := <-got; e != SubscriptionStarted {
		t.Fatalf("first event %q", e)
	}
	ws, err := c.Workspaces(ctx)
	if err != nil || len(ws) == 0 {
		t.Fatal(err)
	}
	created, err := c.TabCreate(ctx, ws[0].ID, "/tmp", "persona-live-test")
	if err != nil {
		t.Fatal(err)
	}
	defer exec.Command("herdr", "tab", "close", created.Tab.ID).Run()
	select {
	case e := <-got:
		if e != "tab_created" { // subscribed as tab.created, reported as tab_created
			t.Fatalf("event %q", e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no tab.created event")
	}
	if err := c.TabClose(ctx, created.Tab.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-got:
		t.Logf("then %q", e)
	case <-time.After(3 * time.Second):
		t.Fatal("no tab.closed event")
	}
}

// An observer of a live pane gets a whole frame at the pane's size first, and the pane keeps its size.
func TestLiveObserve(t *testing.T) {
	ctx := context.Background()
	c := New()
	snap, err := c.Snapshot(ctx)
	if err != nil || len(snap.Agents) == 0 {
		t.Skip("no agent pane to observe")
	}
	pane := snap.Agents[0].PaneID
	cols, rows, err := c.PaneSize(ctx, pane)
	if err != nil {
		t.Fatal(err)
	}
	octx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	var got []Frame
	_ = c.Observe(octx, pane, cols, rows, func(f Frame) { got = append(got, f) })
	if len(got) == 0 || !got[0].Full || got[0].Width != cols || got[0].Height != rows {
		t.Fatalf("frames from %s (%d×%d): %+v", pane, cols, rows, got)
	}
	if c2, r2, _ := c.PaneSize(ctx, pane); c2 != cols || r2 != rows {
		t.Fatalf("observing resized %s: %d×%d → %d×%d", pane, cols, rows, c2, r2)
	}
	t.Logf("%s %d×%d: %d frames in 1.5 s, first %d bytes (base64)", pane, cols, rows, len(got), len(got[0].Data))
}

// The wheel through a control session, against a pane whose program pins a row on top (Codex: the prompt of the turn;
// a wheel over it does nothing): PERSONA_LIVE_WHEEL_PANE=<pane> go test -tags herdrlive -run TestLiveControlWheel.
// The pane needs output above its screen. As the node sends it for an app that names no cell: the middle of the screen.
func TestLiveControlWheel(t *testing.T) {
	pane := os.Getenv("PERSONA_LIVE_WHEEL_PANE")
	if pane == "" {
		t.Skip("PERSONA_LIVE_WHEEL_PANE not set")
	}
	ctx := context.Background()
	c := New()
	s, err := c.StartControl(ctx, pane, 45, 30, false, func(Frame) {})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	screen := func() string {
		time.Sleep(1500 * time.Millisecond)
		out, err := c.PaneRead(ctx, pane, "visible", 30)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	before := screen()
	if err := s.Scroll(5, true, -1, -1); err != nil {
		t.Fatal(err)
	}
	up := screen()
	if err := s.Scroll(5, false, -1, -1); err != nil {
		t.Fatal(err)
	}
	back := screen()
	if up == before {
		t.Fatalf("5 lines up did not scroll %s:\n%s", pane, up)
	}
	if back != before {
		t.Errorf("5 down did not come back:\n%s\n----\n%s", before, back)
	}
	t.Logf("%s scrolled and came back", pane)
}

// The menu actions against a throwaway tab of the workspace named by HERDR_LIVE_WORKSPACE (it creates the tab and
// closes it): go test -tags herdrlive -run TestLiveMenuActions ./internal/herdr/
func TestLiveMenuActions(t *testing.T) {
	ws := os.Getenv("HERDR_LIVE_WORKSPACE")
	if ws == "" {
		t.Skip("HERDR_LIVE_WORKSPACE is not set")
	}
	ctx := context.Background()
	c := New()
	dir := t.TempDir()
	created, err := c.TabCreate(ctx, ws, dir, "menu-live-check")
	if err != nil {
		t.Fatal(err)
	}
	tab, root := created.Tab.ID, created.RootPane.ID
	closed := false
	t.Cleanup(func() {
		if !closed {
			_ = c.TabClose(ctx, tab)
		}
	})
	t.Logf("tab %s pane %s", tab, root)
	if err := c.TabRename(ctx, tab, "menu-live-renamed"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	split, err := c.PaneSplit(ctx, root, "right", dir)
	if err != nil || split == "" || split == root {
		t.Fatalf("split = %q, %v", split, err)
	}
	if n, err := c.TabPanes(ctx, ws, tab); err != nil || n != 2 {
		t.Fatalf("panes after split = %d, %v", n, err)
	}
	down, err := c.PaneSplit(ctx, split, "down", "")
	if err != nil || down == "" {
		t.Fatalf("split down = %q, %v", down, err)
	}
	if err := c.PaneClose(ctx, down); err != nil {
		t.Fatalf("close pane: %v", err)
	}
	if n, _ := c.TabPanes(ctx, ws, tab); n != 2 {
		t.Fatalf("panes after closing one = %d", n)
	}
	if err := c.TabClose(ctx, tab); err != nil {
		t.Fatalf("close tab: %v", err)
	}
	closed = true
	if n, _ := c.TabPanes(ctx, ws, tab); n != 0 {
		t.Fatalf("panes after closing the tab = %d", n)
	}
	// Closing a tab's only pane closes the tab: the node's Close relies on it when it cannot count the panes (#4873).
	lone, err := c.TabCreate(ctx, ws, dir, "menu-live-lone")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.PaneClose(ctx, lone.RootPane.ID); err != nil {
		_ = c.TabClose(ctx, lone.Tab.ID)
		t.Fatalf("close the only pane: %v", err)
	}
	if n, _ := c.TabPanes(ctx, ws, lone.Tab.ID); n != 0 {
		_ = c.TabClose(ctx, lone.Tab.ID)
		t.Fatalf("panes after closing a tab's only pane = %d: the tab stayed", n)
	}
}

// The mouse through a control session, against a pane running vim with the mouse on (`:set mouse=a ruler`) on a file of
// numbered lines, scrolled to the top: PERSONA_LIVE_MOUSE_PANE=<pane> go test -tags herdrlive -run TestLiveControlMouse.
// A click moves vim's cursor to the cell (its ruler says line,column), a drag across a row starts a visual selection.
func TestLiveControlMouse(t *testing.T) {
	pane := os.Getenv("PERSONA_LIVE_MOUSE_PANE")
	if pane == "" {
		t.Skip("PERSONA_LIVE_MOUSE_PANE not set")
	}
	ctx := context.Background()
	c := New()
	s, err := c.StartControl(ctx, pane, 60, 24, false, func(Frame) {})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	screen := func() string {
		time.Sleep(700 * time.Millisecond)
		out, err := c.PaneRead(ctx, pane, "visible", 24)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	press := func(action, button string, col, row, mods int) {
		if err := s.Mouse(MouseEvent{action, button, col, row, mods}); err != nil {
			t.Fatal(err)
		}
	}
	press("down", "left", 3, 5, 0)
	press("up", "left", 3, 5, 0)
	if out := screen(); !strings.Contains(out, "6,4") {
		t.Fatalf("a click at column 3, row 5 did not put vim's cursor on 6,4:\n%s", out)
	}
	t.Log("click moved the cursor to 6,4")
	press("down", "left", 2, 2, 0)
	for col := 3; col <= 8; col++ {
		press("drag", "left", col, 2, 0)
	}
	out := screen()
	if !strings.Contains(out, "VISUAL") || !strings.Contains(out, "3,9") {
		t.Fatalf("a drag along row 2 did not select to 3,9:\n%s", out)
	}
	press("up", "left", 8, 2, 0)
	t.Log("drag selected from 3,3 to 3,9")
	// a click outside Visual mode and a right click elsewhere does not break the session
	press("down", "right", 10, 10, 0)
	press("up", "right", 10, 10, 0)
}
