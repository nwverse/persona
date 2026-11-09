package main

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/link"
)

// run --parent reads the menu bar app's presence and confirmation lines from stdin, skips what it does not know, and
// returns only when stdin ends (the app went away), also after a line too long to scan.
func TestReadParentTakesPresenceUntilStdinEnds(t *testing.T) {
	r, w := io.Pipe()
	t.Cleanup(func() { _ = w.Close() })
	box, confirms := link.NewPresenceBox(), link.NewConfirms()
	done := make(chan struct{})
	go func() {
		readParent(r, &fromApp{presence: box, confirms: confirms})
		close(done)
	}()
	write := func(s string) {
		t.Helper()
		if _, err := io.WriteString(w, s); err != nil {
			t.Fatal(err)
		}
	}
	eventually := func(ok func() bool) {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); !ok(); time.Sleep(5 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatal("timed out")
			}
		}
	}
	latest := func() link.PresenceInfo {
		p, _ := box.Latest(time.Now())
		return p
	}
	write(`{"presence":{"active":true,"notifies":true}}` + "\n")
	write("not json\n" + `{"other":1}` + "\n")
	eventually(func() bool { return latest().Active })
	write(`{"presence":{"active":false,"notifies":true}}` + "\n")
	eventually(func() bool { p := latest(); return !p.Active && p.Notifies })
	write(`{"rang":{"interaction_id":"ix-1"}}` + "\n")
	select {
	case card := <-confirms.C():
		if card != "ix-1" {
			t.Fatalf("confirmation of %q", card)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the app's confirmation was not queued for the link")
	}

	wrote := make(chan struct{})
	go func() { // a pipe write returns once it is read: a reader that stopped would hold it forever
		defer close(wrote)
		_, _ = io.WriteString(w, strings.Repeat("x", 128<<10)+"\n") // past the scanner's limit
		_, _ = io.WriteString(w, `{"presence":{"active":true,"notifies":true}}`+"\n")
	}()
	select {
	case <-done:
		t.Fatal("stopped reading before stdin ended")
	case <-wrote:
	case <-time.After(5 * time.Second):
		t.Fatal("stdin not read")
	}
	select {
	case <-done:
		t.Fatal("stopped reading before stdin ended")
	default:
	}
	if latest().Active {
		t.Fatal("a line after the scan ended was read as presence")
	}
	w.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("still reading after stdin ended")
	}
}
