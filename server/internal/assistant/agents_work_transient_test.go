package assistant

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/proto"
)

// Transient model errors are continued by default, as a rule, also with the triage off (transient_retry, #3208).

// transientService is a manager whose user never turned the triage on: only transient_retry's default is in force.
func transientService(t *testing.T) (*Service, *fakeHub) {
	t.Helper()
	s, f := workService(t)
	s.work.switches = &TriageSwitches{}
	// Any model call would make the turn the user's: none may happen with the triage off.
	f.triage = `{"phase":"blocked","on":"user","line":"选哪个","questions":[{"question":"A 还是 B？","options":[{"label":"A"},{"label":"B"}]}]}`
	return s, f
}

// failAgain: s1 went on (the manager's "Continue", or the user) and its next turn, the i-th, ended on errText.
func failAgain(t *testing.T, s *Service, f *fakeHub, i int, errText string) {
	t.Helper()
	s.workStarted(context.Background(), "box", "s1", time.Now())
	c := apiErrorTurn("s1", errText)
	c.UpdatedAt = turnEnd.Add(time.Duration(i) * time.Minute)
	f.setSessions("box", c)
	triage(t, s, "s1")
}

func TestRetryBackoffSchedule(t *testing.T) {
	var cfg assistantwire.TriageConfig
	for attempt, want := range []time.Duration{15 * time.Second, 30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute} {
		if got := retryBackoff(cfg, attempt, false); got != want {
			t.Errorf("attempt %d: %v, want %v", attempt, got, want)
		}
		if got := retryBackoff(cfg, attempt, true); got != 4*want {
			t.Errorf("rate limited, attempt %d: %v, want %v", attempt, got, 4*want)
		}
	}
	cfg.RetryBaseSeconds = 10
	if got := retryBackoff(cfg, 2, false); got != 40*time.Second {
		t.Errorf("base 10 s, attempt 2: %v", got)
	}
	if got := retryBackoff(cfg, 60, true); got != retryBackoffMost {
		t.Errorf("a very late attempt: %v, want the ceiling %v", got, retryBackoffMost)
	}
	if got := retryBackoff(cfg, -1, false); got != 10*time.Second {
		t.Errorf("a negative attempt: %v", got)
	}
	if maxRetries(assistantwire.TriageConfig{}) != 5 || maxRetries(assistantwire.TriageConfig{MaxRetries: 2}) != 2 {
		t.Error("max_retries defaults to 5 and can be set")
	}
}

func TestRateLimitError(t *testing.T) {
	for text, want := range map[string]bool{
		"API Error: 429 Too Many Requests":    true,
		"API Error: rate_limit_error":         true,
		"API Error: Rate limit reached":       true,
		"API Error: 503 no eligible upstream": false,
		"API Error: Connection error.":        false,
		"API Error: timed out after 4290ms":   false,
	} {
		if got := rateLimitError(text); got != want {
			t.Errorf("rateLimitError(%q) = %v, want %v", text, got, want)
		}
	}
}

// Unset switches: transient_retry on, everything else off; a PUT that leaves it out keeps it.
func TestTransientRetrySwitch(t *testing.T) {
	if sw := (TriageSwitches{}); !sw.transientOn() || !sw.active() || sw.Enabled {
		t.Fatalf("defaults %+v", sw)
	}
	if sw := (TriageSwitches{TransientRetry: new(false)}); sw.transientOn() || sw.active() {
		t.Fatalf("turned off %+v", sw)
	}
	off := false
	f := newFakeHub()
	f.hello.Agents.Triage.TransientRetry = &off
	if sw := newTestService(t, f).triageSwitches(context.Background()); sw.transientOn() {
		t.Fatalf("manager.json's initial value is not taken: %+v", sw)
	}
	transient := proto.WorkState{Phase: proto.WorkBlocked, On: proto.WaitTransient}
	limit := proto.WorkState{Phase: proto.WorkBlocked, On: proto.WaitExternal, ResetsAt: &turnEnd}
	wait := proto.WorkState{Phase: proto.WorkBlocked, On: proto.WaitExternal, Wait: "CI"}
	for _, c := range []struct {
		sw                     TriageSwitches
		transient, limit, wait bool
	}{
		{TriageSwitches{}, true, false, false},
		{TriageSwitches{TransientRetry: new(false), Enabled: true, AutoContinue: true, Wake: true}, false, true, true},
		{TriageSwitches{Enabled: true}, true, false, false},
		{TriageSwitches{AutoContinue: true, Wake: true}, true, false, false}, // the main switch off
	} {
		if c.sw.wakes(transient) != c.transient || c.sw.wakes(limit) != c.limit || c.sw.wakes(wait) != c.wait {
			t.Errorf("%+v: wakes %v %v %v, want %v %v %v", c.sw, c.sw.wakes(transient), c.sw.wakes(limit), c.sw.wakes(wait),
				c.transient, c.limit, c.wait)
		}
	}

	s, _ := workService(t)
	if w := serve(s, "PUT", "/api/v1/agents/triage", `{"enabled":true,"transient_retry":false}`); w.Code != 200 {
		t.Fatalf("PUT %d %s", w.Code, w.Body)
	}
	if w := serve(s, "PUT", "/api/v1/agents/triage", `{"enabled":true,"wake":true}`); w.Code != 200 || !strings.Contains(w.Body.String(), `"transient_retry":false`) {
		t.Fatalf("an older app's PUT dropped transient_retry: %d %s", w.Code, w.Body)
	}
}

// With the triage off, a transient error is still continued after its backoff, with no model call; the apps are told
// of the "Continue" on its way, and of nothing else.
func TestTransientContinuedWithTriageOff(t *testing.T) {
	s, f := transientService(t)
	ctx := context.Background()
	f.setSessions("box", apiErrorTurn("s1", "API Error: Connection lost mid-response"), stopped("s2", "A 还是 B？"),
		apiErrorTurn("s3", "API Error: 401 invalid x-api-key"))
	for _, sid := range []string{"s1", "s2", "s3"} {
		triage(t, s, sid)
	}
	for _, sid := range []string{"s2", "s3"} {
		if e, ok := s.workEntry([2]string{"box", sid}); ok {
			t.Fatalf("%s decided with the triage off: %+v", sid, e)
		}
	}
	e := entryOf(t, s, "s1")
	if e.WakeAt == nil || e.Work.On != proto.WaitTransient || e.Work.By != "rule" || e.Work.Max != 5 {
		t.Fatalf("entry %+v", e)
	}
	if d := time.Until(*e.WakeAt); d < 10*time.Second || d > 20*time.Second {
		t.Fatalf("first continue in %v, want about 15 s", d)
	}
	r := s.workReport(ctx)
	if r == nil || r.On || len(r.Sessions) != 1 || r.Sessions[0].SessionID != "s1" || r.Sessions[0].Work.WakeAt == nil {
		t.Fatalf("report %+v", r)
	}
	s.workTick(ctx, time.Now())
	if len(f.sentToAgents()) != 0 {
		t.Fatal("sent before the backoff")
	}
	s.workTick(ctx, time.Now().Add(16*time.Second))
	if got := f.sentToAgents(); len(got) != 1 || got[0] != "s1: 继续" {
		t.Fatalf("sent %v", got)
	}
	if len(s.workCards()) != 0 {
		t.Fatal("a card with the triage off")
	}
}

// Each "Continue" that errs again waits twice as long; after the cap the session is left as it was (its error and
// the apps' Retry), with no card. A turn that ends normally starts the count again.
func TestTransientCapAndReset(t *testing.T) {
	s, f := transientService(t)
	ctx := context.Background()
	const errText = "API Error: 529 {\"type\":\"overloaded_error\"}"
	f.setSessions("box", apiErrorTurn("s1", errText))
	triage(t, s, "s1")
	var waits []time.Duration
	for i := 1; ; i++ {
		e := entryOf(t, s, "s1")
		if e.WakeAt == nil {
			break
		}
		waits = append(waits, e.WakeAt.Sub(e.Work.At).Round(time.Second))
		s.workTick(ctx, time.Now().Add(time.Hour))
		if i > 7 {
			t.Fatal("never stopped continuing")
		}
		failAgain(t, s, f, i, errText)
	}
	want := []time.Duration{15 * time.Second, 30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute}
	if len(waits) != len(want) {
		t.Fatalf("waits %v, want %v", waits, want)
	}
	for i := range want {
		if d := waits[i] - want[i]; d < -time.Second || d > time.Second {
			t.Fatalf("waits %v, want %v", waits, want)
		}
	}
	if got := f.sentToAgents(); len(got) != 5 {
		t.Fatalf("继续 sent %d times: %v", len(got), got)
	}
	e := entryOf(t, s, "s1")
	if e.Work.On != proto.WaitUser || e.InteractionID != "" || len(s.workCards()) != 0 {
		t.Fatalf("after the cap: %+v", e)
	}
	if r := s.workReport(ctx); r == nil || len(r.Sessions) != 0 {
		t.Fatalf("after the cap the apps show the error as before: %+v", r)
	}

	// The user tapped Retry and the turn ended normally: the next transient error counts from the start again.
	s.workStarted(ctx, "box", "s1", time.Now())
	done := stopped("s1", "测试都过了")
	done.UpdatedAt = turnEnd.Add(time.Hour)
	f.setSessions("box", done)
	triage(t, s, "s1")
	if e := entryOf(t, s, "s1"); e.Retries != 0 {
		t.Fatalf("count after a normal turn: %d", e.Retries)
	}
	failAgain(t, s, f, 61, errText)
	e = entryOf(t, s, "s1")
	if e.WakeAt == nil || e.Retries != 0 || e.Work.Attempts != 0 {
		t.Fatalf("after a normal turn: %+v", e)
	}
	if d := e.WakeAt.Sub(e.Work.At); d > 20*time.Second {
		t.Fatalf("first continue again in %v", d)
	}
}

// The user going on first wins: the pending "Continue" is dropped.
func TestTransientContinueYieldsToTheUser(t *testing.T) {
	s, f := transientService(t)
	ctx := context.Background()
	f.setSessions("box", apiErrorTurn("s1", "API Error: 503 no eligible upstream"))
	triage(t, s, "s1")
	if entryOf(t, s, "s1").WakeAt == nil {
		t.Fatal("not scheduled")
	}
	s.workStarted(ctx, "box", "s1", time.Now()) // the user's message, or Retry in the app
	if e := entryOf(t, s, "s1"); e.WakeAt != nil {
		t.Fatalf("still scheduled: %+v", e)
	}
	s.workTick(ctx, time.Now().Add(time.Hour))
	if got := f.sentToAgents(); len(got) != 0 {
		t.Fatalf("sent %v", got)
	}
}

// A rate limit waits four times as long.
func TestRateLimitWaitsLonger(t *testing.T) {
	s, f := transientService(t)
	f.setSessions("box", apiErrorTurn("s1", "API Error: 429 Too Many Requests"))
	triage(t, s, "s1")
	e := entryOf(t, s, "s1")
	if e.WakeAt == nil {
		t.Fatalf("not scheduled: %+v", e)
	}
	if d := e.WakeAt.Sub(e.Work.At); d < 55*time.Second || d > 65*time.Second {
		t.Fatalf("first continue after a rate limit in %v, want about 60 s", d)
	}
}
