package devices

import (
	"context"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/devicewire"
)

func TestFileCallCancelReachesNode(t *testing.T) {
	d := newTestDevices(t)
	n := &testNode{fileStarted: make(chan struct{}), fileCancelled: make(chan struct{})}
	connectedMachine(t, d, "box", n)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := d.Call(ctx, devicewire.NodeCall{MachineID: "box", Method: "file.copy"})
		done <- err
	}()
	select {
	case <-n.fileStarted:
	case <-time.After(time.Second):
		t.Fatal("node did not receive file.copy")
	}
	cancel()
	select {
	case <-n.fileCancelled:
	case <-time.After(time.Second):
		t.Fatal("node's file.copy handler was not cancelled")
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled file.copy succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("manager did not stop waiting")
	}
}
