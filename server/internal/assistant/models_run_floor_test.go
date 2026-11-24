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
	// The next is held back at once and says why. It is no timeout (nothing was asked and its caller's time has not
	// run out), so nothing retries it as one, and it is not counted.
	started := time.Now()
	_, err := ask(100 * time.Millisecond)
	if !runHeldBack(err) || errors.Is(err, context.DeadlineExceeded) || ai.Transient(err) || time.Since(started) > 50*time.Millisecond || devs.runs() != runFloorMisses {
		t.Fatalf("held back: %v (timeout %t, transient %t) after %s, %d runs", err, errors.Is(err, context.DeadlineExceeded), ai.Transient(err), time.Since(started), devs.runs())
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

// Purposes on one launcher run different models and efforts, which decide how long a run takes: what one of them
// showed never holds back or lets through another's requests. A small model's misses leave a large one's short waits
// alone, and its quick answer does not clear the large one's misses.
func TestLauncherRunFloorIsPerModelAndEffort(t *testing.T) {
	s, _, _ := modelService(t)
	devs := &slowRunDevs{delay: 400 * time.Millisecond}
	s.devs = devs
	slack := runFloorSlack
	runFloorSlack = 20 * time.Millisecond
	t.Cleanup(func() { runFloorSlack = slack })
	// Tab names (agent.title, under kb) on a small model; Sorting on a large one at high effort; both on claude.
	if r := modelRequest(s, "PATCH", "kb", `{"revision":0,"provider":"launcher.claude","model":"m-small"}`); r.Code != 200 {
		t.Fatalf("kb on claude: %d %s", r.Code, r.Body.String())
	}
	if r := modelRequest(s, "PATCH", "inbox.triage", `{"revision":0,"provider":"launcher.claude","model":"m-large","effort":"high"}`); r.Code != 200 {
		t.Fatalf("sorting on claude: %d %s", r.Code, r.Body.String())
	}
	ask := func(purpose string, wait time.Duration) error {
		ctx, cancel := context.WithTimeout(context.Background(), wait)
		defer cancel()
		_, err := s.purposeModel(purpose).Complete(ctx, "", "name this", 64)
		return err
	}

	for range runFloorMisses {
		if err := ask("agent.title", 100*time.Millisecond); runHeldBack(err) {
			t.Fatalf("small model held back before its misses: %v", err)
		}
	}
	if err := ask("agent.title", 100*time.Millisecond); !runHeldBack(err) {
		t.Fatalf("small model after its misses: %v", err)
	}
	before := devs.runs()
	if err := ask("inbox.triage", 100*time.Millisecond); runHeldBack(err) || devs.runs() != before+1 {
		t.Fatalf("the small model's misses held the large one back: %v (%d runs)", err, devs.runs()-before)
	}
	if err := ask("inbox.triage", 100*time.Millisecond); runHeldBack(err) || devs.runs() != before+2 { // its own second miss
		t.Fatalf("large model's second wait: %v (%d runs)", err, devs.runs()-before)
	}

	devs.setDelay(5 * time.Millisecond)
	if err := ask("agent.title", time.Second); err != nil {
		t.Fatalf("small model's quick answer: %v", err)
	}
	if err := ask("agent.title", 100*time.Millisecond); err != nil {
		t.Fatalf("small model's short wait after its quick answer: %v", err)
	}
	before = devs.runs()
	if err := ask("inbox.triage", 100*time.Millisecond); !runHeldBack(err) || devs.runs() != before {
		t.Fatalf("the small model's quick answer let the large one's short wait through: %v (%d runs)", err, devs.runs()-before)
	}
	devs.mu.Lock()
	defer devs.mu.Unlock()
	for _, p := range devs.saw {
		if p.Model != "m-small" && (p.Model != "m-large" || p.Effort != "high") {
			t.Fatalf("a run with neither configuration: %+v", p)
		}
	}
}

// holdBackSorting puts the Sorting purpose (inbox.triage: Agent naming's model) on the computer's claude launcher and
// records runs there as taking an hour, so every naming call is held back before it is sent (runFloorBook).
func holdBackSorting(t *testing.T, s *Service) {
	t.Helper()
	holdBack(t, s, "inbox.triage", "inbox.triage")
}

// holdBack puts the purposes of category (purpose among them) on the computer's claude launcher and records runs of
// purpose there as taking an hour, so every call of it is held back before it is sent (runFloorBook).
func holdBack(t *testing.T, s *Service, category, purpose string) {
	t.Helper()
	if r := modelRequest(s, "PATCH", category, `{"revision":0,"provider":"launcher.claude"}`); r.Code != 200 {
		t.Fatalf("%s on claude: %d %s", category, r.Code, r.Body.String())
	}
	ctx := context.Background()
	b, err := s.resolveModel(ctx, purpose)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.apiConfig(ctx, b)
	if err != nil || c.RunLauncher == "" {
		t.Fatalf("%s runs on %+v: %v", purpose, c, err)
	}
	s.runFloors.note(runFloorKey(c), time.Hour, true, time.Now())
}

// A conversation's title held back because its model's launcher answers slower than a title waits was never asked
// (runFloorBook): no run starts, it is not asked again at once, and it is no final answer either. The conversation
// keeps a stand-in, which a later turn names again, as after a call that ran out of time (#13443).
func TestHeldBackTitleIsAStandIn(t *testing.T) {
	s, _, _ := modelService(t)
	devs := &slowRunDevs{}
	s.devs = devs
	holdBack(t, s, "kb", "assistant.title")
	started := time.Date(2026, 11, 1, 16, 30, 0, 0, time.UTC)
	got, final := s.makeTitle(context.Background(), started, searchAsk, searchReply, "")
	if final || !strings.Contains(got, " | "+titleFallback+" | ") || devs.runs() != 0 {
		t.Fatalf("held-back title: %q (final %t) after %d runs", got, final, devs.runs())
	}
}

// What a launcher's runs showed counts per computer, launcher, model and effort, for runFloorTTL after the last run.
func TestRunFloorBook(t *testing.T) {
	var b runFloorBook
	now := time.Now()
	agy, claude := runFloorKey(ai.Config{ViaMachine: "server", RunLauncher: "agy"}), runFloorKey(ai.Config{ViaMachine: "server", RunLauncher: "claude"})
	if _, ok := b.floor(agy, now); ok {
		t.Fatal("a floor before any run")
	}
	keys := map[string]bool{agy: true, claude: true}
	for _, c := range []ai.Config{
		{ViaMachine: "laptop", RunLauncher: "agy"},
		{ViaMachine: "server", RunLauncher: "agy", Model: "m-small"},
		{ViaMachine: "server", RunLauncher: "agy", Model: "m-large"},
		{ViaMachine: "server", RunLauncher: "agy", Model: "m-small", ReasoningEffort: "high"},
	} {
		if k := runFloorKey(c); keys[k] {
			t.Fatalf("%+v shares a floor with another computer, launcher, model or effort", c)
		} else {
			keys[k] = true
		}
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
