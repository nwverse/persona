package assistant

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

// The same hand-off asked twice while its card waits is one card, answered for both; once allowed it goes again
// without a card; a refusal or a different hand-off asks again.
func TestOutboundOnceAsksOncePerHandOff(t *testing.T) {
	var o outboundOnce
	ctx := context.Background()
	var asked atomic.Int32
	release := make(chan bool)
	ask := func() (bool, error) { asked.Add(1); return <-release, nil }
	key := outboundKey("lead", "agent_send", "words", "to an Agent")
	var wg sync.WaitGroup
	got := make([]bool, 2)
	for i := range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); got[i], _ = o.confirm(ctx, key, ask) }()
	}
	waitFor(t, "the first card", func() bool { return asked.Load() == 1 })
	waitFor(t, "the second call to join the card", func() bool {
		o.mu.Lock()
		defer o.mu.Unlock()
		f := o.flight[key]
		return f != nil && f.joined == 1
	})
	release <- true
	wg.Wait()
	if asked.Load() != 1 || !got[0] || !got[1] {
		t.Fatalf("asked %d times, answers %v", asked.Load(), got)
	}
	if ok, err := o.confirm(ctx, key, func() (bool, error) { return false, errors.New("asked again") }); !ok || err != nil {
		t.Fatalf("an allowed hand-off was asked about again: %v %v", ok, err)
	}
	other := outboundKey("lead", "agent_send", "other words", "to an Agent")
	if ok, _ := o.confirm(ctx, other, func() (bool, error) { return false, nil }); ok {
		t.Fatal("a refused hand-off went")
	}
	if ok, _ := o.confirm(ctx, other, func() (bool, error) { return true, nil }); !ok {
		t.Fatal("a refusal was remembered")
	}
}
