package link

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// keepStatus is what `persona-link run` does with every status: link.status.json, which RequestRename reads.
func keepStatus(t *testing.T, path string, ch chan Status) Options {
	t.Helper()
	return Options{Path: path, Status: func(s Status) {
		_ = WriteStatus(path, SavedStatus{Status: s, PID: os.Getpid(), Updated: time.Now()})
		select {
		case ch <- s:
		default:
		}
	}}
}

// The hub says what it calls this computer: link.json keeps it, the status shows it, and a rename asked here goes
// to the hub over the link and comes back with the hub's answer.
func TestRenameThroughTheLink(t *testing.T) {
	asked := make(chan Message, 4)
	srv, _ := fakeHub(t, func(c *websocket.Conn) {
		sendJSON(t, c, `{"type":"endpoints","hub_name":"家里","machine_name":"Laptop"}`)
		for {
			_, b, err := c.Read(context.Background())
			if err != nil {
				return
			}
			var m Message
			if json.Unmarshal(b, &m) != nil || m.Type != "rename" {
				continue
			}
			asked <- m
			if m.MachineName == "bad" {
				sendJSON(t, c, `{"type":"renamed","id":"`+m.ID+`","code":"pending","error":"confirm it first"}`)
				continue
			}
			sendJSON(t, c, `{"type":"renamed","id":"`+m.ID+`","machine_name":"`+m.MachineName+`"}`)
			sendJSON(t, c, `{"type":"endpoints","hub_name":"家里","machine_name":"`+m.MachineName+`"}`)
		}
	})
	path := filepath.Join(t.TempDir(), "link.json")
	release, err := LockRun(path) // as the running persona-link holds it
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ch := make(chan Status, 256)
	o := keepStatus(t, path, ch)
	_ = Update(path, func(c *Config) error {
		*c = Config{Machine: "laptop", FP: pinOf(srv), Token: "jd_x", Endpoints: []Endpoint{{URL: srv.URL, Kind: "lan"}}}
		return nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = Run(ctx, o) }()
	t.Cleanup(func() { cancel(); <-done })
	waitFor(t, ch, func(s Status) bool { return s.State == "connected" && s.MachineName == "Laptop" })
	if c, _ := Load(path); c.MachineName != "Laptop" {
		t.Fatalf("link.json keeps %q", c.MachineName)
	}

	rctx, rcancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer rcancel()
	got, err := RequestRename(rctx, path, "  Studio  ")
	if err != nil || got != "Studio" {
		t.Fatalf("%q %v", got, err)
	}
	if m := <-asked; m.MachineName != "Studio" || m.ID == "" {
		t.Fatalf("the hub was asked %+v", m)
	}
	waitFor(t, ch, func(s Status) bool { return s.MachineName == "Studio" })
	if c, _ := Load(path); c.MachineName != "Studio" {
		t.Fatalf("link.json keeps %q", c.MachineName)
	}

	_, err = RequestRename(rctx, path, "bad")
	var re *RenameError
	if !errors.As(err, &re) || re.Code != RenamePending || !strings.Contains(re.Text("en"), "Confirm") {
		t.Fatalf("refused: %v", err)
	}
	if _, err := RequestRename(rctx, path, strings.Repeat("x", 41)); !errors.As(err, &re) || re.Code != RenameInvalidName {
		t.Fatalf("too long: %v", err)
	}
}

// A rename asked while nothing runs, or with a hub that never said what it calls this computer, is refused at once.
func TestRenameWithoutAHubThatRenames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "link.json")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var re *RenameError
	if _, err := RequestRename(ctx, path, "x"); !errors.As(err, &re) || re.Code != RenameNotRunning {
		t.Fatalf("nothing running: %v", err)
	}

	srv, _ := fakeHub(t, func(c *websocket.Conn) {
		sendJSON(t, c, `{"type":"endpoints","hub_name":"旧的"}`)
		for {
			if _, _, err := c.Read(context.Background()); err != nil {
				return
			}
		}
	})
	release, err := LockRun(path)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ch := make(chan Status, 256)
	o := keepStatus(t, path, ch)
	_ = Update(path, func(c *Config) error {
		*c = Config{Machine: "laptop", FP: pinOf(srv), Token: "jd_x", Endpoints: []Endpoint{{URL: srv.URL, Kind: "lan"}}}
		return nil
	})
	rctx, rcancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = Run(rctx, o) }()
	t.Cleanup(func() { rcancel(); <-done })
	waitFor(t, ch, func(s Status) bool { return s.State == "connected" && s.HubName == "旧的" })
	if _, err := RequestRename(ctx, path, "x"); !errors.As(err, &re) || re.Code != RenameUnsupported {
		t.Fatalf("an older hub: %v", err)
	}
}

// A rename left from long ago is not taken.
func TestAStaleRenameIsDropped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "link.json")
	b, _ := json.Marshal(renameRequest{ID: "old", Name: "x", At: time.Now().Add(-time.Hour)})
	if err := os.WriteFile(RenamePath(path), b, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := takeRename(path, time.Now()); ok {
		t.Fatal("a stale rename was taken")
	}
	if _, err := os.Stat(RenamePath(path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a stale rename stays")
	}
}
