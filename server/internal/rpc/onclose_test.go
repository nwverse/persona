package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"
)

// Close tells OnClose the methods of the calls it cuts off, so the hub can log what a dropped connection took with
// it (#7509); a call already answered is not among them.
func TestCloseReportsCallsCutOff(t *testing.T) {
	hub, node := pair(t)
	release := make(chan struct{})
	node.OnRequest = func(ctx context.Context, method string, _ json.RawMessage) (any, error) {
		if method == "quick" {
			return "ok", nil
		}
		select {
		case <-release:
		case <-ctx.Done():
		}
		return nil, nil
	}
	defer close(release)
	type closed struct {
		err      error
		inflight []string
	}
	got := make(chan closed, 1)
	hub.OnClose = func(err error, inflight []string) { got <- closed{err, inflight} }
	if err := hub.Call(context.Background(), "quick", nil, nil); err != nil {
		t.Fatal(err)
	}
	errs := make(chan error, 3)
	for _, method := range []string{"session.start", "ping", "ping"} {
		go func() { errs <- hub.Call(context.Background(), method, nil, nil) }()
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		hub.mu.Lock()
		n := len(hub.methods)
		hub.mu.Unlock()
		if n == 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d calls pending, want 3", n)
		}
		time.Sleep(time.Millisecond)
	}
	why := errors.New("half-open link")
	hub.Close(why)
	select {
	case c := <-got:
		if c.err != why || !reflect.DeepEqual(c.inflight, []string{"ping", "ping", "session.start"}) {
			t.Fatalf("OnClose(%v, %v)", c.err, c.inflight)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("OnClose not called")
	}
	for range 3 {
		if err := <-errs; !errors.Is(err, ErrDropped) {
			t.Fatalf("cut-off call: %v, want ErrDropped", err)
		}
	}
	hub.Close(errors.New("again")) // once only
	select {
	case c := <-got:
		t.Fatalf("OnClose called again: %v", c)
	default:
	}
}
