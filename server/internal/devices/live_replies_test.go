package devices

import (
	"context"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/devicewire"
)

// liveRepliesCalls lists what each node.configure said about live replies, and whether that call also carried the
// config directories (a call without them would make the node forget the ones it has).
func liveRepliesCalls(seen []map[string]any) (said []bool, withDirs []bool) {
	for _, c := range seen {
		if c["method"] != "node.configure" {
			continue
		}
		if v, ok := c["claude_live_replies"].(bool); ok {
			said = append(said, v)
			_, dirs := c["claude_dirs"]
			withDirs = append(withDirs, dirs)
		}
	}
	return said, withDirs
}

// Never set (a hub upgraded without anyone touching the switch), live replies are on.
func TestLiveRepliesDefaultOn(t *testing.T) {
	h := newTestDevices(t)
	m, _ := herdrMac(t, h, "mac", false)
	mux := h.Handler(http.NotFoundHandler())
	if code, out := call(t, mux, "GET", "/api/v1/machines/mac/claude-live-replies", ""); code != 200 || out["on"] != true {
		t.Fatalf("default: %d %v", code, out)
	}
	if !m.liveReplies(context.Background()) {
		t.Fatal("configureNode would send off")
	}
}

// The switch is per machine, kept in the hub's settings table (Postgres on a real hub), told to the node at once,
// and the next connection's node.configure carries it.
func TestLiveRepliesSwitch(t *testing.T) {
	h := newTestDevices(t)
	m, seen := herdrMac(t, h, "mac", false)
	other, _ := herdrMac(t, h, "box", false)
	mux := h.Handler(http.NotFoundHandler())

	if code, _ := call(t, mux, "PUT", "/api/v1/machines/mac/claude-live-replies", `{}`); code != 400 {
		t.Fatalf("a body without on: %d", code)
	}
	if code, out := call(t, mux, "PUT", "/api/v1/machines/mac/claude-live-replies", `{"on": false}`); code != 200 || out["on"] != false {
		t.Fatalf("turned off: %d %v", code, out)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		said, withDirs := liveRepliesCalls(seen())
		if len(said) > 0 && !said[len(said)-1] {
			if !withDirs[len(withDirs)-1] {
				t.Fatalf("node.configure without claude_dirs: %v", seen())
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the node was not told: %v", said)
		}
		time.Sleep(10 * time.Millisecond)
	}
	var stored bool
	if ok, err := h.opts.Store.Setting(context.Background(), liveRepliesKey("mac"), &stored); !ok || err != nil || stored {
		t.Fatalf("kept in settings: ok=%v err=%v value=%v", ok, err, stored)
	}
	if code, out := call(t, mux, "GET", "/api/v1/machines/mac/claude-live-replies", ""); code != 200 || out["on"] != false {
		t.Fatalf("read back: %d %v", code, out)
	}
	if m.liveReplies(context.Background()) || !other.liveReplies(context.Background()) {
		t.Fatal("per machine: the other one is still on")
	}
	if code, out := call(t, mux, "PUT", "/api/v1/machines/mac/claude-live-replies", `{"on": true}`); code != 200 || out["on"] != true {
		t.Fatalf("turned on: %d %v", code, out)
	}
	if code, _ := call(t, mux, "GET", "/api/v1/machines/nope/claude-live-replies", ""); code != 404 {
		t.Fatalf("an unknown machine: %d", code)
	}
	for _, want := range []string{"GET /api/v1/machines/{mid}/claude-live-replies", "PUT /api/v1/machines/{mid}/claude-live-replies"} {
		if !slices.Contains(devicewire.Routes, want) {
			t.Errorf("%s is not relayed by the hub", want)
		}
	}
}

// Connecting tells the node whether its Claude starts load the live replies plugin: on unless the user turned it off.
func TestConfigureNodeSaysLiveReplies(t *testing.T) {
	_, _, m, _ := dirsHub(t)
	f := connectDirsNode(t, m)
	ctx := context.Background()
	m.configureNode(ctx, m.peer, nil, true)
	if err := m.d.opts.Store.PutSetting(ctx, liveRepliesKey(m.cfg.ID), false); err != nil {
		t.Fatal(err)
	}
	m.configureNode(ctx, m.peer, nil, true)
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.configure) != 2 || string(f.configure[0]["claude_live_replies"]) != "true" || string(f.configure[1]["claude_live_replies"]) != "false" {
		t.Fatalf("claude_live_replies in node.configure: %v", f.configure)
	}
}
