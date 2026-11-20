package hub

import (
	"context"
	"testing"
	"time"
)

func TestWriteQuiescerWaitsForInFlightWrites(t *testing.T) {
	var q writeQuiescer
	if !q.begin() {
		t.Fatal("a write was refused before any pause")
	}
	paused := make(chan error, 1)
	go func() { paused <- q.pause(context.Background()) }()
	select {
	case err := <-paused:
		t.Fatalf("pause returned with a write in flight: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	if q.begin() {
		t.Fatal("a write was admitted while paused")
	}
	q.end()
	select {
	case err := <-paused:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("pause did not return after the write finished")
	}
	q.resume()
	if !q.begin() {
		t.Fatal("writes did not resume")
	}
	q.end()
}

func TestWriteQuiescerPauseCanBeCancelled(t *testing.T) {
	var q writeQuiescer
	if !q.begin() {
		t.Fatal("a write was refused before any pause")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := q.pause(ctx); err == nil {
		t.Fatal("pause ignored its cancelled context")
	}
	q.end()
	q.resume()
}

func TestAssistantGatePausesAndResumes(t *testing.T) {
	var g assistantGate
	ctx := context.Background()
	if err := g.pause(ctx); err != nil {
		t.Fatal(err)
	}
	resumed := make(chan bool, 1)
	go func() { resumed <- g.waitResumed(ctx) }()
	select {
	case <-resumed:
		t.Fatal("the supervisor resumed while paused")
	case <-time.After(30 * time.Millisecond):
	}
	if g.register(make(chan struct{}), func() {}) {
		t.Fatal("a child was registered while paused")
	}
	g.start()
	select {
	case ok := <-resumed:
		if !ok {
			t.Fatal("the supervisor did not resume")
		}
	case <-time.After(time.Second):
		t.Fatal("resume did not wake the supervisor")
	}

	// A running child is stopped and pause waits for its exit.
	exit := make(chan struct{})
	cancelled := make(chan struct{})
	if !g.register(exit, func() { close(cancelled) }) {
		t.Fatal("a child was not registered")
	}
	done := make(chan error, 1)
	go func() { done <- g.pause(ctx) }()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("pause did not cancel the child")
	}
	select {
	case err := <-done:
		t.Fatalf("pause returned before the child exited: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	if paused := g.unregister(); !paused {
		t.Fatal("the supervisor lost the pause")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	g.start()
}
