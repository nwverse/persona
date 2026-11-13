package hub

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/devicewire"
)

func TestBusFileCallCancelReachesManager(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newTestHub(t)
		started, cancelled := make(chan struct{}), make(chan struct{})
		playManager(t, h, func(ctx context.Context, method string, params json.RawMessage) (any, error) {
			if method != devicewire.MethodFileCall {
				return map[string]any{}, nil
			}
			close(started)
			<-ctx.Done()
			close(cancelled)
			return nil, ctx.Err()
		})
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		var res json.RawMessage
		go func() {
			done <- h.callBusAsIs(ctx, assistantwire.AssistantID, devicewire.MethodFileCall,
				devicewire.NodeCall{MachineID: "m1", Method: "file.copy", Params: json.RawMessage(`{}`)}, &res)
		}()
		select {
		case <-started:
		case err := <-done:
			t.Fatalf("file.copy ended before reaching the manager: %v", err)
		case <-time.After(time.Second):
			t.Fatal("manager did not receive file.copy")
		}
		cancel()
		select {
		case <-cancelled:
		case <-time.After(time.Second):
			t.Fatal("manager's file.copy handler was not cancelled")
		}
		select {
		case err := <-done:
			if err == nil {
				t.Fatal("cancelled file.copy succeeded")
			}
		case <-time.After(time.Second):
			t.Fatal("caller did not stop waiting")
		}
	})
}

func TestBusOrdinaryCallRunsOnAfterCallerCancel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newTestHub(t)
		started, ended := make(chan struct{}), make(chan string, 1)
		playManager(t, h, func(ctx context.Context, method string, params json.RawMessage) (any, error) {
			if method != devicewire.MethodCall {
				return map[string]any{}, nil
			}
			close(started)
			select {
			case <-ctx.Done():
				ended <- "cancelled"
			case <-time.After(300 * time.Millisecond):
				ended <- "ran on"
			}
			return map[string]any{}, nil
		})
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			done <- h.callBusAsIs(ctx, assistantwire.AssistantID, devicewire.MethodCall,
				devicewire.NodeCall{MachineID: "m1", Method: "session.send", Params: json.RawMessage(`{}`)}, nil)
		}()
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("manager did not receive session.send")
		}
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("caller: %v, want context.Canceled", err)
		}
		select {
		case got := <-ended:
			if got != "ran on" {
				t.Fatalf("ordinary devices.call was %q; the manager must run on", got)
			}
		case <-time.After(time.Second):
			t.Fatal("manager's ordinary call did not finish")
		}
	})
}

func TestDeviceBusMethod(t *testing.T) {
	for method, want := range map[string]string{
		"file.copy":    devicewire.MethodFileCall,
		"file.zip":     devicewire.MethodFileCall,
		"file.move":    devicewire.MethodCall,
		"session.send": devicewire.MethodCall,
	} {
		if got := deviceBusMethod(method); got != want {
			t.Errorf("%s uses %q, want %q", method, got, want)
		}
	}
}
