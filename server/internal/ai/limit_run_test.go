package ai

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func resetRunGate(t *testing.T, cfg Config) {
	t.Helper()
	k := runGateKey(cfg)
	gates.Delete(k)
	t.Cleanup(func() { gates.Delete(k) })
}

func TestRunGateHoldsAfterConsecutiveTimeoutsAndRecovers(t *testing.T) {
	clk := fakeClock(t)
	cfg := Config{ViaMachine: "n37-test", RunLauncher: "claude", Model: "kimi-test"}
	resetRunGate(t, cfg)

	var calls atomic.Int32
	fail := true
	cl := NewRun(cfg, func(ctx context.Context, r Request) (Reply, error) {
		calls.Add(1)
		if fail {
			return Reply{}, fmt.Errorf("n37-test 上的 claude 没有及时回答: %w", context.DeadlineExceeded)
		}
		return Reply{Text: "ok", Model: "kimi-test"}, nil
	})

	// First sporadic timeout returns the original timeout error and does not hold the gate.
	ctx30, cancel30 := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel30()
	if _, err := cl.Complete(ctx30, "", "hi", 16); !errors.Is(err, context.DeadlineExceeded) || IsPaused(err) {
		t.Fatalf("first timeout = %v; want raw DeadlineExceeded without pause", err)
	}
	if p := Held(cfg); p != nil {
		t.Fatalf("Held after single timeout = %+v; want nil", p)
	}

	// Second consecutive full-budget timeout trips the shared gate into ReasonBusy.
	if _, err := cl.Complete(ctx30, "", "hi", 16); !IsPaused(err) {
		t.Fatalf("second timeout = %v; want PausedError", err)
	}
	p := Held(cfg)
	if p == nil || p.Reason != ReasonBusy || !p.Until.Equal(clk.t.Add(busyFirst)) {
		t.Fatalf("Held after 2nd timeout = %+v; want busy until %v", p, clk.t.Add(busyFirst))
	}

	// While held, another client sharing the same (machine, launcher, model) is put off without invoking run.
	cl2 := NewRun(cfg, func(ctx context.Context, r Request) (Reply, error) {
		t.Fatal("second client should not run while gate is held")
		return Reply{}, nil
	})
	if _, err := cl2.Complete(ctx30, "", "blocked", 16); !IsPaused(err) {
		t.Fatalf("while held = %v; want PausedError", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("calls while held = %d; want 2", got)
	}

	// Advance past first pause: one probe runs and fails -> pause doubles to 2*busyFirst.
	clk.add(busyFirst)
	if _, err := cl.Complete(ctx30, "", "probe-fail", 16); !IsPaused(err) {
		t.Fatalf("failed probe = %v; want PausedError", err)
	}
	p = Held(cfg)
	if p == nil || !p.Until.Equal(clk.t.Add(2*busyFirst)) {
		t.Fatalf("Held after failed probe = %+v; want until %v", p, clk.t.Add(2*busyFirst))
	}

	// Advance past doubled pause: probe succeeds -> gate opens cleanly and resets timeout counter.
	fail = false
	clk.add(2 * busyFirst)
	got, err := cl.Complete(ctx30, "", "probe-ok", 16)
	if err != nil || got != "ok" {
		t.Fatalf("probe recovery = %q, %v; want ok, nil", got, err)
	}
	if p := Held(cfg); p != nil {
		t.Fatalf("Held after recovery = %+v; want nil", p)
	}

	// A single timeout after recovery must again need 2 consecutive timeouts before holding.
	fail = true
	if _, err := cl.Complete(ctx30, "", "post-recovery-1", 16); !errors.Is(err, context.DeadlineExceeded) || IsPaused(err) {
		t.Fatalf("post-recovery first timeout = %v; want raw DeadlineExceeded", err)
	}
	if p := Held(cfg); p != nil {
		t.Fatalf("Held after post-recovery single timeout = %+v; want nil", p)
	}

	// Verify log lines were emitted on hold and recovery.
	joined := strings.Join(clk.lines(), "\n")
	if !strings.Contains(joined, "n37-test/claude:kimi-test is busy (timeout)") || !strings.Contains(joined, "answers again") {
		t.Fatalf("unexpected gate logs:\n%s", joined)
	}
}

func TestRunGateIgnoresShortBudgetsCanceledAndResetsOnNonTimeout(t *testing.T) {
	fakeClock(t)
	cfg := Config{ViaMachine: "n37-short", RunLauncher: "agy"}
	resetRunGate(t, cfg)

	// Tight best-effort deadlines (< runBusyMinBudget, e.g. 3s info.tag) must not trip the shared gate.
	clTimeout := NewRun(cfg, func(ctx context.Context, r Request) (Reply, error) {
		return Reply{}, context.DeadlineExceeded
	})
	for i := 0; i < 4; i++ {
		shortCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_, err := clTimeout.Complete(shortCtx, "", "tag", 16)
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) || IsPaused(err) {
			t.Fatalf("short-budget call %d = %v; want raw DeadlineExceeded", i, err)
		}
	}
	if p := Held(cfg); p != nil {
		t.Fatalf("short best-effort timeouts held gate: %+v", p)
	}

	// Explicit caller cancellations must not trip the gate.
	for i := 0; i < 3; i++ {
		cctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := clTimeout.Complete(cctx, "", "cancel", 16)
		if IsPaused(err) {
			t.Fatalf("canceled call %d produced PausedError: %v", i, err)
		}
	}
	if p := Held(cfg); p != nil {
		t.Fatalf("canceled calls held gate: %+v", p)
	}

	// A non-timeout error (e.g. offline/logged_out) between two timeouts resets the consecutive timeout streak.
	offlineErr := errors.New("machine offline")
	clOffline := NewRun(cfg, func(ctx context.Context, r Request) (Reply, error) {
		return Reply{}, offlineErr
	})
	if _, err := clTimeout.Complete(context.Background(), "", "timeout-1", 16); !errors.Is(err, context.DeadlineExceeded) || IsPaused(err) {
		t.Fatalf("timeout-1 = %v; want raw DeadlineExceeded", err)
	}
	if _, err := clOffline.Complete(context.Background(), "", "offline", 16); !errors.Is(err, offlineErr) || IsPaused(err) {
		t.Fatalf("offline = %v; want offlineErr", err)
	}
	if _, err := clTimeout.Complete(context.Background(), "", "timeout-2-after-offline", 16); !errors.Is(err, context.DeadlineExceeded) || IsPaused(err) {
		t.Fatalf("timeout after offline = %v; want raw DeadlineExceeded (streak reset)", err)
	}
	if p := Held(cfg); p != nil {
		t.Fatalf("non-consecutive timeout held gate: %+v", p)
	}
}

func TestRunGateProbeNonTimeoutAndShortBudgetLeaveHoldForNextProbe(t *testing.T) {
	clk := fakeClock(t)
	cfg := Config{ViaMachine: "n37-probe", RunLauncher: "claude", Model: "m"}
	resetRunGate(t, cfg)

	mode := "timeout"
	offlineErr := errors.New("offline")
	cl := NewRun(cfg, func(ctx context.Context, r Request) (Reply, error) {
		switch mode {
		case "timeout":
			return Reply{}, context.DeadlineExceeded
		case "offline":
			return Reply{}, offlineErr
		default:
			return Reply{Text: "recovered"}, nil
		}
	})

	_, _ = cl.Complete(context.Background(), "", "1", 16)
	if _, err := cl.Complete(context.Background(), "", "2", 16); !IsPaused(err) {
		t.Fatalf("expected hold: %v", err)
	}
	firstUntil := clk.t.Add(busyFirst)
	clk.add(busyFirst)

	// Probe fails with a non-timeout error: gate stays held with expired pause (not doubled), next call probes.
	mode = "offline"
	if _, err := cl.Complete(context.Background(), "", "probe-offline", 16); !errors.Is(err, offlineErr) || IsPaused(err) {
		t.Fatalf("probe offline = %v; want offlineErr", err)
	}
	if p := Held(cfg); p == nil || !p.Until.Equal(firstUntil) {
		t.Fatalf("Held after non-timeout probe = %+v; want unchanged until %v", p, firstUntil)
	}

	// Probe fails with a short-budget timeout: gate stays held with expired pause, next normal call probes.
	mode = "timeout"
	shortCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	_, err := cl.Complete(shortCtx, "", "probe-short", 16)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) || IsPaused(err) {
		t.Fatalf("probe short = %v; want raw DeadlineExceeded", err)
	}
	if p := Held(cfg); p == nil || !p.Until.Equal(firstUntil) {
		t.Fatalf("Held after short-budget probe = %+v; want unchanged until %v", p, firstUntil)
	}

	// Next normal call becomes the probe and recovers the gate cleanly.
	mode = "ok"
	if got, err := cl.Complete(context.Background(), "", "probe-ok", 16); err != nil || got != "recovered" {
		t.Fatalf("probe recovery = %q, %v; want recovered, nil", got, err)
	}
	if p := Held(cfg); p != nil {
		t.Fatalf("Held after recovery = %+v; want nil", p)
	}
}

func TestRunGateCapEnforcementBackgroundWaitPostWaitBudgetAndGrowth(t *testing.T) {
	clk := fakeClock(t)
	cfg := Config{ViaMachine: "n37-cap", RunLauncher: "claude", Model: "capped"}
	resetRunGate(t, cfg)

	// Trip hold (sets cap=1), then recover via probe so gate is open with cap=1.
	shouldFail := true
	clInit := NewRun(cfg, func(ctx context.Context, r Request) (Reply, error) {
		if shouldFail {
			return Reply{}, context.DeadlineExceeded
		}
		return Reply{Text: "ok"}, nil
	})
	_, _ = clInit.Complete(context.Background(), "", "1", 16)
	_, _ = clInit.Complete(context.Background(), "", "2", 16)
	clk.add(busyFirst)
	shouldFail = false
	if _, err := clInit.Complete(context.Background(), "", "probe", 16); err != nil {
		t.Fatalf("probe recovery: %v", err)
	}

	g := gateForRun(cfg)
	g.mu.Lock()
	gotCap := g.cap
	g.mu.Unlock()
	if gotCap != 1 {
		t.Fatalf("cap after recovery = %d; want 1", gotCap)
	}

	// Occupy the single cap slot.
	entered := make(chan struct{})
	release := make(chan struct{})
	clHold := NewRun(cfg, func(ctx context.Context, r Request) (Reply, error) {
		close(entered)
		<-release
		return Reply{Text: "holder"}, nil
	})
	holderDone := make(chan error, 1)
	go func() {
		_, err := clHold.Complete(context.Background(), "", "hold", 16)
		holderDone <- err
	}()
	<-entered

	// Background caller waiting under cap gives up with PausedError without calling run.
	bgCtx := withSlotWait(context.Background(), 20*time.Millisecond)
	clBG := NewRun(cfg, func(ctx context.Context, r Request) (Reply, error) {
		t.Fatal("background caller should not run while cap is full")
		return Reply{}, nil
	})
	if _, err := clBG.Complete(bgCtx, "", "bg", 16); !IsPaused(err) {
		t.Fatalf("background wait under cap = %v; want PausedError", err)
	}

	// Regression (#16420): a caller whose initial budget exceeds runBusyMinBudget, but waits in admit under cap until
	// less than runBusyMinBudget remains when c.run starts, must NOT trip the gate even when runTimeouts is already 1.
	oldMin := runBusyMinBudget
	runBusyMinBudget = 500 * time.Millisecond
	t.Cleanup(func() { runBusyMinBudget = oldMin })

	// Release holder1 first, then arm runTimeouts = 1 with an unbounded timeout.
	close(release)
	if err := <-holderDone; err != nil {
		t.Fatalf("holder = %v", err)
	}
	clQueued := NewRun(cfg, func(ctx context.Context, r Request) (Reply, error) {
		return Reply{}, context.DeadlineExceeded
	})
	if _, err := clQueued.Complete(context.Background(), "", "arm-1", 16); !errors.Is(err, context.DeadlineExceeded) || IsPaused(err) {
		t.Fatalf("arm-1 = %v; want raw DeadlineExceeded", err)
	}

	// Re-occupy the single cap slot with a cancelable holder so ending it via context.Canceled preserves runTimeouts == 1.
	entered2 := make(chan struct{})
	release2 := make(chan struct{})
	h2Ctx, cancelH2 := context.WithCancel(context.Background())
	defer cancelH2()
	clHold2 := NewRun(cfg, func(ctx context.Context, r Request) (Reply, error) {
		close(entered2)
		<-release2
		return Reply{}, ctx.Err()
	})
	h2Done := make(chan error, 1)
	go func() {
		_, err := clHold2.Complete(h2Ctx, "", "h2", 16)
		h2Done <- err
	}()
	<-entered2

	// Start with budget > runBusyMinBudget (650ms > 500ms) before admit, wait on a timer until remaining budget is
	// < runBusyMinBudget (~350ms < 500ms), then cancel and release the slot so c.run runs and times out while
	// runTimeouts is still 1. Measuring budget before admit would trip the gate (PausedError); measuring after admit
	// keeps runTimeouts == 1 and leaves the gate open.
	borderCtx, cancelBorder := context.WithTimeout(context.Background(), runBusyMinBudget+150*time.Millisecond)
	defer cancelBorder()
	borderDone := make(chan error, 1)
	go func() {
		_, err := clQueued.Complete(borderCtx, "", "border", 16)
		borderDone <- err
	}()
	<-time.After(300 * time.Millisecond)
	cancelH2()
	close(release2)
	if err := <-h2Done; !errors.Is(err, context.Canceled) {
		t.Fatalf("h2 = %v; want context.Canceled", err)
	}
	if err := <-borderDone; !errors.Is(err, context.DeadlineExceeded) || IsPaused(err) {
		t.Fatalf("post-admit short-remaining timeout = %v; want raw DeadlineExceeded without hold", err)
	}
	g.mu.Lock()
	gotTimeouts := g.runTimeouts
	g.mu.Unlock()
	if gotTimeouts != 1 {
		t.Fatalf("runTimeouts after post-admit short-remaining timeout = %d; want 1 (unchanged)", gotTimeouts)
	}
	if p := Held(cfg); p != nil {
		t.Fatalf("post-admit short-remaining timeout tripped gate: %+v", p)
	}

	// Repeated successes grow cap until it lifts back to 0 (uncapped).
	totalToLift := capLift * growAfter
	for i := 0; i < totalToLift; i++ {
		if _, err := clInit.Complete(context.Background(), "", "grow", 16); err != nil {
			t.Fatalf("grow call %d: %v", i, err)
		}
	}
	g.mu.Lock()
	finalCap := g.cap
	g.mu.Unlock()
	if finalCap != 0 {
		t.Fatalf("cap after %d successes = %d; want 0 (lifted)", totalToLift, finalCap)
	}
}

func TestRunGateIsolatesTargetsAndSkipsEmptyLauncher(t *testing.T) {
	fakeClock(t)
	cfgA := Config{ViaMachine: "n37-iso", RunLauncher: "claude", Model: "kimi"}
	cfgB := Config{ViaMachine: "n37-iso", RunLauncher: "claude", Model: "haiku"}
	resetRunGate(t, cfgA)
	resetRunGate(t, cfgB)

	clA := NewRun(cfgA, func(ctx context.Context, r Request) (Reply, error) {
		return Reply{}, context.DeadlineExceeded
	})
	clB := NewRun(cfgB, func(ctx context.Context, r Request) (Reply, error) {
		return Reply{Text: "fast"}, nil
	})
	_, _ = clA.Complete(context.Background(), "", "1", 16)
	_, err := clA.Complete(context.Background(), "", "2", 16)
	if !IsPaused(err) || Held(cfgA) == nil {
		t.Fatalf("cfgA did not hold: err=%v held=%+v", err, Held(cfgA))
	}
	if Held(cfgB) != nil {
		t.Fatalf("cfgB should remain independent: held=%+v", Held(cfgB))
	}
	if got, err := clB.Complete(context.Background(), "", "ok", 16); err != nil || got != "fast" {
		t.Fatalf("cfgB = %q, %v; want fast, nil", got, err)
	}

	// Empty RunLauncher stays ungated (backward-compatible with unit test stubs).
	clEmpty := NewRun(Config{}, func(ctx context.Context, r Request) (Reply, error) {
		return Reply{}, context.DeadlineExceeded
	})
	for i := 0; i < 3; i++ {
		if _, err := clEmpty.Complete(context.Background(), "", "x", 16); IsPaused(err) {
			t.Fatalf("empty RunLauncher unexpectedly paused on call %d: %v", i, err)
		}
	}
}

func TestRunGateInFlightTimeoutsDoNotDoubleStrike(t *testing.T) {
	clk := fakeClock(t)
	cfg := Config{ViaMachine: "n37-conc", RunLauncher: "claude", Model: "slow"}
	resetRunGate(t, cfg)

	// First timeout arms the counter (runTimeouts = 1).
	clArm := NewRun(cfg, func(ctx context.Context, r Request) (Reply, error) {
		return Reply{}, context.DeadlineExceeded
	})
	if _, err := clArm.Complete(context.Background(), "", "arm", 16); IsPaused(err) {
		t.Fatalf("first timeout unexpectedly paused: %v", err)
	}

	// Launch two concurrent calls while the gate is still open; both time out.
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	cl := NewRun(cfg, func(ctx context.Context, r Request) (Reply, error) {
		entered <- struct{}{}
		<-release
		return Reply{}, context.DeadlineExceeded
	})
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			_, err := cl.Complete(context.Background(), "", "c", 16)
			errs <- err
		}()
	}
	<-entered
	<-entered
	close(release)
	for i := 0; i < 2; i++ {
		if err := <-errs; !IsPaused(err) {
			t.Fatalf("concurrent timeout %d = %v; want PausedError", i, err)
		}
	}
	// Only the first completing timeout should strike; backoff stays at busyFirst, not 2*busyFirst.
	p := Held(cfg)
	if p == nil || !p.Until.Equal(clk.t.Add(busyFirst)) {
		t.Fatalf("Held after concurrent timeouts = %+v; want single strike until %v", p, clk.t.Add(busyFirst))
	}
}
