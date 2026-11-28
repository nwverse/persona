//go:build !windows

package herdrcore

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func listenRawFake(t *testing.T, handler func(net.Conn)) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "hcrw")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "c.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				handler(c)
			}()
		}
	}()
	return sock
}

func TestSubscribeRequiresMatchingResponseID(t *testing.T) {
	sock := listenRawFake(t, func(c net.Conn) {
		line, err := bufio.NewReader(c).ReadBytes('\n')
		if err != nil {
			return
		}
		var req struct {
			ID json.RawMessage `json:"id"`
		}
		_ = json.Unmarshal(line, &req)
		// Send unrelated / empty JSON objects before the real matching error response.
		_, _ = c.Write([]byte(`{"jsonrpc":"2.0","id":999999,"result":{}}` + "\n"))
		_, _ = c.Write([]byte(`{}` + "\n"))
		resp, _ := json.Marshal(map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"error": map[string]any{
				"code":    -32000,
				"message": "denied",
				"data":    map[string]any{"kind": "denied"},
			},
		})
		_, _ = c.Write(append(resp, '\n'))
	})

	var startedCalls atomic.Int32
	c := &Client{Socket: sock}
	err := c.Subscribe(context.Background(), func() { startedCalls.Add(1) }, func(Event) {})
	if !IsKind(err, "denied") {
		t.Fatalf("Subscribe = %v, want denied error matching request ID", err)
	}
	if n := startedCalls.Load(); n != 0 {
		t.Fatalf("started() called %d times on unmatched response before error", n)
	}
}

func TestSubscribeAllowsNilCallbacks(t *testing.T) {
	f := newFakeCore(t, nil)
	c := &Client{Socket: f.sock}

	// Nil started callback while receiving live events.
	ctx1, cancel1 := context.WithCancel(context.Background())
	got := make(chan Event, 2)
	done1 := make(chan error, 1)
	go func() {
		done1 <- c.Subscribe(ctx1, nil, func(e Event) { got <- e })
	}()
	<-f.sub
	f.push("event", map[string]any{"type": "status", "terminal": "t1"})
	select {
	case ev := <-got:
		if ev.Type != "status" || ev.Terminal != "t1" {
			t.Fatalf("event = %+v", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for event with nil started callback")
	}
	cancel1()
	if err := <-done1; !errors.Is(err, context.Canceled) {
		t.Fatalf("Subscribe(nil, fn) = %v, want context.Canceled", err)
	}

	// Nil fn callback while started fires and events arrive.
	ctx2, cancel2 := context.WithCancel(context.Background())
	started := make(chan struct{}, 1)
	done2 := make(chan error, 1)
	go func() {
		done2 <- c.Subscribe(ctx2, func() { started <- struct{}{} }, nil)
	}()
	<-f.sub
	<-started
	f.push("event", map[string]any{"type": "status", "terminal": "t2"})
	f.push("missed", map[string]any{"seq": 2})
	cancel2()
	select {
	case err := <-done2:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Subscribe(started, nil) = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Subscribe(started, nil) did not return on cancel")
	}
}

func TestCallReturnsContextCanceledAndHandlesNullIDError(t *testing.T) {
	// Part A: canceled context must wrap context.Canceled rather than raw net i/o timeout.
	received := make(chan struct{}, 1)
	hold := make(chan struct{})
	t.Cleanup(func() { close(hold) })
	sockHold := listenRawFake(t, func(c net.Conn) {
		_, _ = bufio.NewReader(c).ReadBytes('\n')
		select {
		case received <- struct{}{}:
		default:
		}
		<-hold
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := (&Client{Socket: sockHold}).List(ctx)
		done <- err
	}()
	<-received
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled List error = %v, want errors.Is(context.Canceled)", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("List did not abort on cancel")
	}

	// Part B: JSON-RPC parse/invalid-request error with id:null must return immediately, not hang.
	sockNull := listenRawFake(t, func(c net.Conn) {
		_, _ = bufio.NewReader(c).ReadBytes('\n')
		_, _ = c.Write([]byte(`{"jsonrpc":"2.0","id":null,"error":{"code":-32700,"message":"parse error","data":{"kind":"parse_error"}}}` + "\n"))
		<-hold
	})
	callDone := make(chan error, 1)
	go func() {
		_, err := (&Client{Socket: sockNull}).List(context.Background())
		callDone <- err
	}()
	select {
	case err := <-callDone:
		if !IsKind(err, "parse_error") {
			t.Fatalf("id:null error = %v, want parse_error", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("List hung on JSON-RPC id:null error response")
	}
}

func TestConfigSocketUnescapesTOMLBasicStrings(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		`socket = "C:\\Users\\alice\\hc.sock"` + "\n": `C:\Users\alice\hc.sock`,
		`socket = "/tmp/my \"hc\"/c.sock"` + "\n":     `/tmp/my "hc"/c.sock`,
		`socket = 'C:\literal\path.sock'` + "\n":      `C:\literal\path.sock`,
		`socket = "/tmp/a # b.sock" # comment` + "\n": `/tmp/a # b.sock`,
	}
	for body, want := range cases {
		p := filepath.Join(dir, "c.toml")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := configSocket(p); got != want {
			t.Errorf("configSocket(%q) = %q, want %q", body, got, want)
		}
	}
}
