package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/proto"
)

// The usage-limit record's merge and clear rules (#9386, recordHealth). The first five are the counterexamples two
// adversarial reviews found in earlier fixes; the rest pin the rules that replace them.

func seedUsageLimit(t *testing.T, s *Service, machine, text string, reset *time.Time) {
	t.Helper()
	s.recordHealth(context.Background(), machine, "work", healthResult{err: text, kind: errKindUsageLimit, resetsAt: reset, source: healthSourceSession})
	editHealth(s, machine, "work", func(r *LauncherHealth) { r.CheckedAt = time.Now().Add(-healthRecheckThrottle - time.Second) })
}

func probeModel(params json.RawMessage) string {
	var p struct{ Model string }
	_ = json.Unmarshal(params, &p)
	return p.Model
}

// A default-model (Sonnet) pass says nothing of the Opus quota: the fixed Opus task stays refused.
func TestUsageLimitRuleOtherModelPassKeepsOpusLimit(t *testing.T) {
	s, f := launcherHealthHubOn(t, newStorelessService)
	ctx := context.Background()
	reset := time.Now().Add(time.Hour)
	seedUsageLimit(t, s, "l4", "You've hit your Opus limit", &reset)
	f.answerNode(assistantwire.DeviceLaunchersCheck, func(_ string, _ json.RawMessage) (any, error) {
		return proto.LauncherCheck{OK: true, Agent: "claude", Model: "claude-sonnet-4-5", Answer: "OK"}, nil
	})
	if mid, _, err := s.startTaskAgent(ctx, &PlanTask{Machine: "l4", Cwd: "/work", Launcher: "work", Model: "opus", MachinePolicy: machineFixed}, false); err == nil {
		t.Fatalf("a Sonnet pass admitted limited Opus: machine=%q starts=%v", mid, taskStartMachines(f))
	}
}

// A limit recorded while a probe ran outlives that probe's pass.
func TestUsageLimitRuleNewerLimitOutlivesInFlightPass(t *testing.T) {
	s, f := launcherHealthHubOn(t, newStorelessService)
	ctx := context.Background()
	oldReset, freshReset := time.Now().Add(time.Hour), time.Now().Add(7*24*time.Hour)
	seedUsageLimit(t, s, "l4", "You've hit your weekly limit", &oldReset)
	entered, release := make(chan struct{}), make(chan struct{})
	f.answerNode(assistantwire.DeviceLaunchersCheck, func(_ string, _ json.RawMessage) (any, error) {
		close(entered)
		select {
		case <-release:
		case <-time.After(3 * time.Second):
			return nil, fmt.Errorf("probe barrier timed out")
		}
		return proto.LauncherCheck{OK: true, Model: "m"}, nil
	})
	answer := make(chan bool, 1)
	go func() {
		_, bad := s.launcherUnusableNow(ctx, "l4", "work")
		answer <- bad
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("the on-demand probe did not run")
	}
	s.recordHealth(ctx, "l4", "work", healthResult{err: "You've hit your weekly limit", kind: errKindUsageLimit, resetsAt: &freshReset, source: healthSourceSession})
	close(release)
	select {
	case bad := <-answer:
		rec := healthState(s, "l4", "work")
		if !bad || rec.Kind != errKindUsageLimit || rec.ResetsAt == nil || !rec.ResetsAt.Equal(freshReset.UTC()) {
			t.Fatalf("an older pass erased a newer limit: bad=%v record=%+v", bad, rec)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the probe's answer never came")
	}
}

// A failed probe that reports a later weekly reset keeps refusing after the old short window passes.
func TestUsageLimitRuleLaterResetOutlivesOldWindow(t *testing.T) {
	s, f := launcherHealthHubOn(t, newStorelessService)
	ctx := context.Background()
	shortReset, weeklyReset := time.Now().Add(300*time.Millisecond), time.Now().UTC().Add(7*24*time.Hour).Truncate(time.Minute)
	seedUsageLimit(t, s, "l4", "You've hit your weekly limit", &shortReset)
	f.checks["l4/work"] = proto.LauncherCheck{Error: fmt.Sprintf("You've hit your weekly limit · resets %s (UTC)", weeklyReset.Format("Jan 2, 3:04pm"))}
	if _, bad := s.launcherUnusableNow(ctx, "l4", "work"); !bad {
		t.Fatal("the weekly limit did not refuse the start")
	}
	time.Sleep(time.Until(shortReset.Add(50 * time.Millisecond)))
	if r, bad := s.launcherUnusableNow(ctx, "l4", "work"); !bad {
		t.Fatalf("the weekly limit was dropped at the old short reset: %+v", r)
	}
}

// A bare "usage limit reached" names neither family nor reset: the known Opus scope and reset stay, so a later Sonnet
// pass (the apps' Test) cannot admit Opus.
func TestUsageLimitRuleBareFailureKeepsFamilyAndReset(t *testing.T) {
	s, f := launcherHealthHub(t)
	ctx := context.Background()
	reset := time.Now().Add(7 * 24 * time.Hour)
	seedUsageLimit(t, s, "l4", "You've hit your Opus limit", &reset)
	f.checks["l4/work"] = proto.LauncherCheck{Agent: "claude", Error: "usage limit reached"}
	if _, bad := s.launcherUnusableNow(ctx, "l4", "work"); !bad {
		t.Fatal("a bare usage-limit failure admitted the start")
	}
	if rec := healthState(s, "l4", "work"); !slices.Equal(rec.limitModels(), []string{"opus"}) || rec.ResetsAt == nil || !rec.ResetsAt.Equal(reset.UTC()) {
		t.Fatalf("a bare failure erased what was known: %+v", rec)
	}
	f.checks["l4/work"] = proto.LauncherCheck{OK: true, Agent: "claude", Model: "claude-sonnet-4-5"}
	postJSON(t, s, "/api/v1/launchers/work/check?machine=l4")
	if mid, _, err := s.startTaskAgent(ctx, &PlanTask{Machine: "l4", Cwd: "/work", Launcher: "work", Model: "opus", MachinePolicy: machineFixed}, false); err == nil {
		t.Fatalf("a Sonnet Test admitted limited Opus: machine=%q starts=%v", mid, taskStartMachines(f))
	}
}

// After a bare failure, quota that came back is still found by the next probe once the throttle passed.
func TestUsageLimitRuleBareFailureKeepsEarlyRecovery(t *testing.T) {
	s, f := launcherHealthHubOn(t, newStorelessService)
	ctx := context.Background()
	reset := time.Now().Add(7 * 24 * time.Hour)
	seedUsageLimit(t, s, "l4", "You've hit your weekly limit", &reset)
	f.checks["l4/work"] = proto.LauncherCheck{Error: "usage limit reached"}
	s.launcherUnusableNow(ctx, "l4", "work")
	expireRecheckThrottle(s, "l4")
	f.checks["l4/work"] = proto.LauncherCheck{OK: true, Model: "m"}
	if mid, _, err := s.startTaskAgent(ctx, &PlanTask{Machine: "l4", Cwd: "/work", Launcher: "work", MachinePolicy: machineFixed}, false); err != nil || mid != "l4" {
		t.Fatalf("recovered quota stayed refused: machine=%q err=%v probes=%d", mid, err, checkedCount(f, "l4/work"))
	}
}

func expireRecheckThrottle(s *Service, machine string) {
	editHealth(s, machine, "work", func(r *LauncherHealth) { r.CheckedAt = time.Now().Add(-healthRecheckThrottle - time.Second) })
	s.health.mu.Lock()
	s.health.recheckAt[healthKey(machine, "work")] = time.Now().Add(-healthRecheckThrottle - time.Second)
	s.health.mu.Unlock()
}

// A limit with a known reset: quota that came back is found and cleared early; quota still used up keeps refusing.
func TestUsageLimitRuleKnownResetRecoversOrHolds(t *testing.T) {
	for _, tc := range []struct {
		name   string
		answer proto.LauncherCheck
		bad    bool
	}{
		{"still used up", proto.LauncherCheck{Error: "You've hit your weekly limit"}, true},
		{"came back", proto.LauncherCheck{OK: true, Agent: "claude", Model: "m"}, false},
		{"no answer", proto.LauncherCheck{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, f := launcherHealthHubOn(t, newStorelessService)
			reset := time.Now().In(time.FixedZone("offset", -7*60*60)).Add(7 * 24 * time.Hour)
			seedUsageLimit(t, s, "l4", "You've hit your weekly limit", &reset)
			f.checks["l4/work"] = tc.answer
			if _, bad := s.launcherUnusableNow(context.Background(), "l4", "work"); bad != tc.bad || checkedCount(f, "l4/work") != 1 {
				t.Fatalf("bad=%v want %v, probes=%d", bad, tc.bad, checkedCount(f, "l4/work"))
			}
			if rec := healthState(s, "l4", "work"); !tc.bad && (rec.State != healthOK || rec.ResetsAt != nil || rec.LimitModels != nil) {
				t.Fatalf("recovery left the limit: %+v", rec)
			}
		})
	}
}

// A limit whose reset is unknown is probed too, throttled: within the throttle no probe; after it, quota that came
// back clears it and quota still used up keeps refusing.
func TestUsageLimitRuleUnknownResetIsRechecked(t *testing.T) {
	for _, tc := range []struct {
		name   string
		answer proto.LauncherCheck
		bad    bool
	}{
		{"still used up", proto.LauncherCheck{Error: "usage limit reached"}, true},
		{"came back", proto.LauncherCheck{OK: true, Model: "m"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, f := launcherHealthHubOn(t, newStorelessService)
			ctx := context.Background()
			s.recordHealth(ctx, "l4", "work", healthResult{err: "usage limit reached", source: healthSourceCheck})
			f.checks["l4/work"] = tc.answer
			if _, bad := s.launcherUnusableNow(ctx, "l4", "work"); !bad || checkedCount(f, "l4/work") != 0 {
				t.Fatalf("fresh evidence: bad=%v probes=%d, want refused without a probe", bad, checkedCount(f, "l4/work"))
			}
			expireRecheckThrottle(s, "l4")
			if _, bad := s.launcherUnusableNow(ctx, "l4", "work"); bad != tc.bad || checkedCount(f, "l4/work") != 1 {
				t.Fatalf("after the throttle: bad=%v want %v, probes=%d", bad, tc.bad, checkedCount(f, "l4/work"))
			}
			if _, bad := s.launcherUnusableNow(ctx, "l4", "work"); bad != tc.bad || checkedCount(f, "l4/work") != 1 {
				t.Fatalf("right after a probe: bad=%v probes=%d, want no second probe", bad, checkedCount(f, "l4/work"))
			}
		})
	}
}

// Two families used up: a pass of one clears only that one, and the next probe asks the other.
func TestUsageLimitRulePassClearsOnlyItsFamily(t *testing.T) {
	s, f := launcherHealthHubOn(t, newStorelessService)
	ctx := context.Background()
	reset := time.Now().Add(7 * 24 * time.Hour)
	seedUsageLimit(t, s, "l4", "You've hit your Opus limit", &reset)
	s.recordHealth(ctx, "l4", "work", healthResult{err: "You've hit your Sonnet limit", kind: errKindUsageLimit, source: healthSourceSession})
	if rec := healthState(s, "l4", "work"); !slices.Equal(rec.limitModels(), []string{"sonnet", "opus"}) || rec.ResetsAt == nil {
		t.Fatalf("second family did not add to the first: %+v", rec)
	}
	var asked []string
	f.answerNode(assistantwire.DeviceLaunchersCheck, func(_ string, params json.RawMessage) (any, error) {
		m := probeModel(params)
		asked = append(asked, m)
		return proto.LauncherCheck{OK: true, Model: "claude-" + m + "-4"}, nil
	})
	expireRecheckThrottle(s, "l4")
	if _, bad := s.launcherUnusableNow(ctx, "l4", "work"); !bad {
		t.Fatal("a Sonnet pass cleared the Opus limit too")
	}
	if rec := healthState(s, "l4", "work"); !slices.Equal(rec.limitModels(), []string{"opus"}) {
		t.Fatalf("after the Sonnet pass: %+v", rec)
	}
	expireRecheckThrottle(s, "l4")
	if _, bad := s.launcherUnusableNow(ctx, "l4", "work"); bad {
		t.Fatal("an Opus pass left the limit")
	}
	if !slices.Equal(asked, []string{"sonnet", "opus"}) {
		t.Fatalf("probes asked %v", asked)
	}
}

// An older failure (a probe that started before the record changed) still adds a later reset and a family, and
// never shortens or narrows the limit; an older pass is dropped.
func TestUsageLimitRuleOlderResultOnlyAdds(t *testing.T) {
	s, _ := launcherHealthHubOn(t, newStorelessService)
	ctx := context.Background()
	reset, later, earlier := time.Now().Add(time.Hour), time.Now().UTC().Add(48*time.Hour), time.Now().Add(time.Minute)
	seedUsageLimit(t, s, "l4", "You've hit your Opus limit", &reset)
	before := time.Now().Add(-time.Hour)
	s.recordHealth(ctx, "l4", "work", healthResult{err: "You've hit your weekly limit", resetsAt: &earlier, source: healthSourceCheck, observedAt: &before})
	s.recordHealth(ctx, "l4", "work", healthResult{ok: true, model: "claude-opus-4", source: healthSourceCheck, observedAt: &before})
	if rec := healthState(s, "l4", "work"); !rec.unusable() || !rec.ResetsAt.Equal(reset.UTC()) || !slices.Equal(rec.limitModels(), []string{"opus"}) {
		t.Fatalf("an older result took from the limit: %+v", rec)
	}
	s.recordHealth(ctx, "l4", "work", healthResult{err: "You've hit your Sonnet limit", resetsAt: &later, source: healthSourceCheck, observedAt: &before})
	if rec := healthState(s, "l4", "work"); !rec.ResetsAt.Equal(later) || !slices.Equal(rec.limitModels(), []string{"opus", "sonnet"}) {
		t.Fatalf("an older failure's later reset and family were lost: %+v", rec)
	}
}

// A failure that did not reach the quota (a timeout) leaves the Opus limit, so a later Sonnet pass cannot admit Opus.
func TestUsageLimitRuleOtherFailureKeepsLimit(t *testing.T) {
	s, _ := launcherHealthHubOn(t, newStorelessService)
	ctx := context.Background()
	reset := time.Now().Add(7 * 24 * time.Hour)
	seedUsageLimit(t, s, "l4", "You've hit your Opus limit", &reset)
	s.recordHealth(ctx, "l4", "work", healthResult{err: "context deadline exceeded", source: healthSourceCheck})
	s.recordHealth(ctx, "l4", "work", healthResult{ok: true, model: "claude-sonnet-4-5", source: healthSourceCheck})
	if rec := healthState(s, "l4", "work"); !rec.unusable() || rec.Kind != errKindUsageLimit || !slices.Equal(rec.limitModels(), []string{"opus"}) {
		t.Fatalf("a timeout erased the Opus limit: %+v", rec)
	}
}

// A record written before limit_models keeps its family from the error's words.
func TestUsageLimitRuleLegacyRecordKeepsFamily(t *testing.T) {
	s, _ := launcherHealthHubOn(t, newStorelessService)
	reset := time.Now().Add(time.Hour)
	seedUsageLimit(t, s, "l4", "You've hit your Opus limit", &reset)
	editHealth(s, "l4", "work", func(r *LauncherHealth) { r.LimitModels = nil })
	s.recordHealth(context.Background(), "l4", "work", healthResult{ok: true, model: "claude-sonnet-4-5", source: healthSourceCheck})
	if rec := healthState(s, "l4", "work"); !rec.unusable() {
		t.Fatalf("a Sonnet pass cleared a legacy Opus record: %+v", rec)
	}
}
