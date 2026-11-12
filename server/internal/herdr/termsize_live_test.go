//go:build herdrlive && !windows

package herdr

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
	"time"
)

// Against a throwaway herdr session with no client attached (never the one you work in):
//
//	herdr --session persona-home-test      # start it, then quit the client (the server keeps running)
//	PERSONA_LIVE_HOME_SESSION=persona-home-test go test -tags herdrlive -run TestLiveControlGivesTheSizeBack ./internal/herdr/
//
// It reads the pane's size, controls it at 57 × 41 and lets go twice: with Close (herdr, no client attached, leaves the
// pane at 57 × 41: #5339) and with CloseAt the size from before (the pane gets it back), resize and release back to back.
func TestLiveControlGivesTheSizeBack(t *testing.T) {
	name := os.Getenv("PERSONA_LIVE_HOME_SESSION")
	if name == "" || name == "default" {
		t.Skip("set PERSONA_LIVE_HOME_SESSION to a throwaway session's name (not the default one)")
	}
	out, err := exec.Command("herdr", "session", "list", "--json").Output()
	if err != nil {
		t.Fatal(err)
	}
	var list struct {
		Sessions []struct {
			Name       string `json:"name"`
			SocketPath string `json:"socket_path"`
		} `json:"sessions"`
	}
	_ = json.Unmarshal(out, &list)
	var socket string
	for _, s := range list.Sessions {
		if s.Name == name {
			socket = s.SocketPath
		}
	}
	if socket == "" {
		t.Fatalf("no session %q", name)
	}
	// Session set: the CLI gets this session's socket, never the one in the environment.
	c := &Client{Bin: "herdr", Socket: socket, Session: name}
	ctx := context.Background()
	snap, err := c.Snapshot(ctx)
	if err != nil || len(snap.Panes) == 0 {
		t.Fatalf("snapshot: %v", err)
	}
	pane := snap.Panes[0].PaneID
	cols, rows, err := c.TermSize(ctx, pane)
	if err != nil {
		t.Fatal(err)
	}
	if cols == 57 && rows == 41 {
		t.Fatalf("%s is at 57×41 already (left there by an earlier run?): resize it first", pane)
	}
	lw, lh, _ := c.PaneSize(ctx, pane)
	t.Logf("%s: terminal %d×%d, layout %d×%d", pane, cols, rows, lw, lh)
	control := func() *Control {
		s, err := c.StartControl(ctx, pane, 57, 41, false, func(Frame) {})
		if err != nil {
			t.Fatal(err)
		}
		if w, h, _ := c.TermSize(ctx, pane); w != 57 || h != 41 {
			t.Fatalf("while controlled: %d×%d", w, h)
		}
		return s
	}
	ended := func(s *Control) (int, int) {
		select {
		case <-s.Done():
		case <-time.After(5 * time.Second):
			t.Fatal("the control did not end")
		}
		var w, h int
		deadline := time.Now().Add(5 * time.Second)
		for {
			w, h, err = c.TermSize(ctx, pane)
			if err != nil {
				t.Fatal(err)
			}
			if w == cols && h == rows || time.Now().After(deadline) {
				return w, h
			}
			<-time.After(20 * time.Millisecond)
		}
	}

	s := control()
	s.Close()
	if w, h := ended(s); w == cols && h == rows {
		t.Logf("Close gave %d×%d back: a herdr client is attached (or herdr now does it itself)", w, h)
	} else {
		t.Logf("Close left %d×%d (no client attached)", w, h)
	}
	s = control()
	s.CloseAt(cols, rows)
	if w, h := ended(s); w != cols || h != rows {
		t.Fatalf("after CloseAt(%d, %d): %d×%d", cols, rows, w, h)
	}
}
