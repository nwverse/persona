package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/ai"
	"github.com/nwverse/persona/server/internal/proto"
)

// slowRunDevs answers model.run as a node whose launcher takes delay to answer would: after delay, or not at all when
// its caller's time runs out first.
type slowRunDevs struct {
	viaDevs
	mu    sync.Mutex
	delay time.Duration
	saw   []proto.ModelRunParams
}

func (d *slowRunDevs) CallNode(ctx context.Context, id, method string, params json.RawMessage) (json.RawMessage, error) {
	if method != proto.MethodModelRun || id != "server" {
		return nil, fmt.Errorf("unexpected %s on %s", method, id)
	}
	var p proto.ModelRunParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	d.mu.Lock()
	d.saw = append(d.saw, p)
	delay := d.delay
	d.mu.Unlock()
	select {
	case <-time.After(delay):
		return json.Marshal(proto.ModelRunAnswer{Text: "release-notes", Model: "m-run"})
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (d *slowRunDevs) runs() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.saw)
}

func (d *slowRunDevs) setDelay(delay time.Duration) {
	d.mu.Lock()
	d.delay = delay
	d.mu.Unlock()
}

// A launcher whose runs take longer than a caller can wait (agy's cold start against a new Agent's 8 s tab name,
// #13443) is still tried until two waits in a row end unanswered, or a run answers slower than the wait. After that
// such a request fails at once and says why: no run starts and no model call is counted. A request that can wait long
// enough always runs, a faster answer lowers what is expected, and what was seen is forgotten after runFloorTTL.
func TestLauncherRunHeldBackWhenItCannotAnswerInTime(t *testing.T) {
	s, _, _ := modelService(t)
	var logs logBuffer
	s.logf = logs.logf
	devs := &slowRunDevs{delay: 400 * time.Millisecond}
	s.devs = devs
	slack := runFloorSlack
	runFloorSlack = 20 * time.Millisecond
	t.Cleanup(func() { runFloorSlack = slack })
	if r := modelRequest(s, "PATCH", "kb", `{"revision":0,"provider":"launcher.claude"}`); r.Code != 200 {
		t.Fatalf("pick claude: %d %s", r.Code, r.Body.String())
	}
	ask := func(wait time.Duration) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), wait)
		defer cancel()
		return s.purposeModel("agent.title").Complete(ctx, "", "name this", 64)
	}

	// The first two short waits run and end unanswered: one slow run does not hold the next back.
	for i := range runFloorMisses {
		if _, err := ask(100 * time.Millisecond); !errors.Is(err, context.DeadlineExceeded) || runHeldBack(err) {
			t.Fatalf("wait %d: %v", i, err)
		}
	}
	if devs.runs() != runFloorMisses {
		t.Fatalf("runs = %d", devs.runs())
	}
	// The next is held back at once, reads as a timeout, says why, and is not counted.
	started := time.Now()
	_, err := ask(100 * time.Millisecond)
	if !runHeldBack(err) || !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > 50*time.Millisecond || devs.runs() != runFloorMisses {
		t.Fatalf("held back: %v after %s, %d runs", err, time.Since(started), devs.runs())
	}
	if msg := err.Error(); !strings.Contains(msg, "claude") || !strings.Contains(msg, "server") {
		t.Fatalf("reason: %q", msg)
	}
	if n := strings.Count(logs.String(), "model_usage "); n != runFloorMisses {
		t.Fatalf("counted %d calls, want the %d that ran:\n%s", n, runFloorMisses, logs.String())
	}

	// A request that can wait for the run goes through; its answer is what to expect from now on.
	if text, err := ask(2 * time.Second); err != nil || text != "release-notes" || devs.runs() != runFloorMisses+1 {
		t.Fatalf("long wait: %q %v (%d runs)", text, err, devs.runs())
	}
	if _, err := ask(100 * time.Millisecond); !runHeldBack(err) || devs.runs() != runFloorMisses+1 {
		t.Fatalf("short wait after a slow answer: %v (%d runs)", err, devs.runs())
	}
	if _, err := ask(time.Second); err != nil || devs.runs() != runFloorMisses+2 {
		t.Fatalf("a wait longer than the answer: %v (%d runs)", err, devs.runs())
	}

	// A faster answer lowers it: short waits run again.
	devs.setDelay(5 * time.Millisecond)
	if _, err := ask(time.Second); err != nil {
		t.Fatalf("fast answer: %v", err)
	}
	if text, err := ask(100 * time.Millisecond); err != nil || text != "release-notes" || devs.runs() != runFloorMisses+4 {
		t.Fatalf("short wait after a fast answer: %q %v (%d runs)", text, err, devs.runs())
	}
}

// What a launcher's runs showed counts per computer and launcher, for runFloorTTL after the last run.
func TestRunFloorBook(t *testing.T) {
	var b runFloorBook
	now := time.Now()
	agy, claude := runFloorKey(ai.Config{ViaMachine: "server", RunLauncher: "agy"}), runFloorKey(ai.Config{ViaMachine: "server", RunLauncher: "claude"})
	if _, ok := b.floor(agy, now); ok {
		t.Fatal("a floor before any run")
	}
	b.note(agy, 8*time.Second, false, now)
	if _, ok := b.floor(agy, now); ok {
		t.Fatal("one unanswered wait held requests back")
	}
	b.note(agy, 9*time.Second, false, now)
	if f, ok := b.floor(agy, now); !ok || f != 8*time.Second {
		t.Fatalf("after two unanswered waits: %s %t", f, ok)
	}
	if _, ok := b.floor(claude, now); ok {
		t.Fatal("another launcher was held back")
	}
	b.note(agy, 20*time.Second, true, now)
	b.note(agy, 12*time.Second, true, now)
	b.note(agy, 30*time.Second, true, now)
	if f, ok := b.floor(agy, now); !ok || f != 12*time.Second {
		t.Fatalf("answered: %s %t, want the fastest answer", f, ok)
	}
	if _, ok := b.floor(agy, now.Add(runFloorTTL+time.Second)); ok {
		t.Fatal("still held back after runFloorTTL")
	}
	b.note(agy, 9*time.Second, false, now.Add(runFloorTTL+2*time.Second))
	if _, ok := b.floor(agy, now.Add(runFloorTTL+2*time.Second)); ok {
		t.Fatal("an expired answer still counted after a new unanswered wait")
	}
}

// A to-do's title is made in the background: on a launcher it waits long enough for a cold CLI, on an API connection
// as before.
func TestTodoTitleWaitsForALauncher(t *testing.T) {
	s, _, _ := modelService(t)
	devs := &runDevs{}
	s.devs = devs
	if r := modelRequest(s, "PATCH", "kb", `{"revision":0,"provider":"launcher.claude"}`); r.Code != 200 {
		t.Fatalf("pick claude: %d %s", r.Code, r.Body.String())
	}
	s.todoTitleFromModel(context.Background(), "Draft the release notes for the next version")
	if len(devs.saw) != 1 {
		t.Fatalf("runs: %d", len(devs.saw))
	}
	if got := time.Duration(devs.saw[0].TimeoutMS) * time.Millisecond; got <= todoTitleWait || got > todoTitleLauncherWait {
		t.Fatalf("a launcher title waits %s, want more than %s and at most %s", got, todoTitleWait, todoTitleLauncherWait)
	}
}
