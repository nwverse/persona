package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
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

// Two families used up, each with its own reset (Sonnet's unknown, so the limit's is too): a pass of one clears only
// that one, and the next probe asks the other.
func TestUsageLimitRulePassClearsOnlyItsFamily(t *testing.T) {
	s, f := launcherHealthHubOn(t, newStorelessService)
	ctx := context.Background()
	reset := time.Now().Add(7 * 24 * time.Hour)
	seedUsageLimit(t, s, "l4", "You've hit your Opus limit", &reset)
	s.recordHealth(ctx, "l4", "work", healthResult{err: "You've hit your Sonnet limit", kind: errKindUsageLimit, source: healthSourceSession})
	if rec := healthState(s, "l4", "work"); !slices.Equal(rec.limitModels(), []string{"opus", "sonnet"}) || rec.ResetsAt != nil || !rec.LimitResets["opus"].Equal(reset) {
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
		t.Fatal("an Opus pass cleared the Sonnet limit too")
	}
	if rec := healthState(s, "l4", "work"); !slices.Equal(rec.limitModels(), []string{"sonnet"}) || rec.ResetsAt != nil {
		t.Fatalf("after the Sonnet pass: %+v", rec)
	}
	expireRecheckThrottle(s, "l4")
	if _, bad := s.launcherUnusableNow(ctx, "l4", "work"); bad {
		t.Fatal("a Sonnet pass left the limit")
	}
	if !slices.Equal(asked, []string{"opus", "sonnet"}) {
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
	older := LauncherHealth{CheckedAt: time.Now().Add(-time.Hour)}
	s.recordHealth(ctx, "l4", "work", healthResult{err: "You've hit your weekly limit", resetsAt: &earlier, source: healthSourceCheck, observed: &older})
	s.recordHealth(ctx, "l4", "work", healthResult{ok: true, model: "claude-opus-4", source: healthSourceCheck, observed: &older})
	if rec := healthState(s, "l4", "work"); !rec.unusable() || !rec.ResetsAt.Equal(reset.UTC()) || !slices.Equal(rec.limitModels(), []string{"opus"}) {
		t.Fatalf("an older result took from the limit: %+v", rec)
	}
	s.recordHealth(ctx, "l4", "work", healthResult{err: "You've hit your Sonnet limit", resetsAt: &later, source: healthSourceCheck, observed: &older})
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

// Third review's counterexamples. An older failure that adds to the limit is a new version of the record: a pass a
// probe formed against the version before it cannot clear what it added.
func TestUsageLimitRuleLateFoldInvalidatesInFlightPass(t *testing.T) {
	s, f := launcherHealthHubOn(t, newStorelessService)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	short, later := time.Now().Add(time.Hour), time.Now().Add(7*24*time.Hour)
	seedUsageLimit(t, s, "l4", "You've hit your Opus limit", &short)
	editHealth(s, "l4", "work", func(r *LauncherHealth) { r.CheckedAt = time.Now().Add(-2 * time.Minute) })
	older := healthState(s, "l4", "work")
	seedUsageLimit(t, s, "l4", "You've hit your Opus limit", &short)
	entered, release := make(chan struct{}), make(chan struct{})
	f.answerNode(assistantwire.DeviceLaunchersCheck, func(_ string, _ json.RawMessage) (any, error) {
		close(entered)
		select {
		case <-release:
			return proto.LauncherCheck{OK: true, Model: "claude-opus-4-1"}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	done := make(chan bool, 1)
	go func() {
		_, bad := s.launcherUnusableNow(ctx, "l4", "work")
		done <- bad
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("probe did not start")
	}
	merged := s.recordHealth(ctx, "l4", "work", healthResult{err: "You've hit your Opus limit", resetsAt: &later, source: healthSourceCheck, observed: &older})
	if merged.ResetsAt == nil || !merged.ResetsAt.Equal(later.UTC()) {
		t.Fatalf("the older failure's later reset was not merged: %+v", merged)
	}
	close(release)
	select {
	case bad := <-done:
		if !bad {
			mid, _, err := s.startTaskAgent(ctx, &PlanTask{Machine: "l4", Cwd: "/work", Launcher: "work", Model: "opus", MachinePolicy: machineFixed}, false)
			t.Fatalf("a pass formed before the merge erased it: %+v; fixed start machine=%q err=%v", healthState(s, "l4", "work"), mid, err)
		}
	case <-ctx.Done():
		t.Fatal("probe did not finish")
	}
}

// A known Opus reset says nothing of a Sonnet limit whose reset is unknown: once it passes, Sonnet is probed first.
func TestUsageLimitRuleUnknownFamilyOutlivesOtherReset(t *testing.T) {
	s, f := launcherHealthHubOn(t, newStorelessService)
	ctx := context.Background()
	reset := time.Now().Add(300 * time.Millisecond)
	s.recordHealth(ctx, "l4", "work", healthResult{err: "You've hit your Opus limit", resetsAt: &reset, source: healthSourceSession})
	s.recordHealth(ctx, "l4", "work", healthResult{err: "You've hit your Sonnet limit", source: healthSourceSession})
	f.checks["l4/work"] = proto.LauncherCheck{Error: "You've hit your Sonnet limit"}
	<-time.After(time.Until(reset.Add(50 * time.Millisecond)))
	if mid, _, err := s.startTaskAgent(ctx, &PlanTask{Machine: "l4", Cwd: "/work", Launcher: "work", Model: "sonnet", MachinePolicy: machineFixed}, false); err == nil {
		t.Fatalf("Opus's reset admitted a Sonnet limit of unknown reset: machine=%q probes=%d record=%+v", mid, checkedCount(f, "l4/work"), healthState(s, "l4", "work"))
	}
}

// An agent's turn that meets the Opus limit with a later reset is not "known already" for being before another
// family's reset: once Sonnet's pass clears Sonnet, Opus holds until its own new reset.
func TestUsageLimitRuleTurnLimitKeepsItsFamilysReset(t *testing.T) {
	s, f := launcherHealthHubOn(t, newStorelessService)
	ctx := context.Background()
	soon, mid, week := time.Now().Add(time.Hour), time.Now().UTC().Add(72*time.Hour).Truncate(time.Second), time.Now().Add(7*24*time.Hour)
	seedUsageLimit(t, s, "l4", "You've hit your Opus limit", &soon)
	s.recordHealth(ctx, "l4", "work", healthResult{err: "You've hit your Sonnet limit", kind: errKindUsageLimit, resetsAt: &week, source: healthSourceSession})
	f.setSessions("l4", proto.Session{SessionID: "s1", Launcher: "work", Status: proto.StatusIdle, State: proto.StateUnread, UpdatedAt: time.Now(),
		TurnError: "You've hit your Opus limit", TurnErrorKind: proto.TurnErrorUsageLimit, TurnErrorResetsAt: &mid})
	s.launcherLimitTurnEnded(ctx, "l4", "s1")
	s.recordHealth(ctx, "l4", "work", healthResult{ok: true, model: "claude-sonnet-4-5", source: healthSourceCheck})
	if rec := healthState(s, "l4", "work"); rec.ResetsAt == nil || !rec.ResetsAt.Equal(mid) || !slices.Equal(rec.limitModels(), []string{"opus"}) {
		t.Fatalf("the turn's later Opus reset was dropped: %+v", rec)
	}
}

// Fourth review's counterexamples and the persisted format.

func reloadHealthService(t *testing.T, s *Service, f *fakeHub) *Service {
	t.Helper()
	store := s.store()
	return withSettings(t, f, New(Options{Hub: f, Store: func() Store { return store }, Logf: testLogf(t)}))
}

// limit_resets and the unknown resets survive a restart; rev does not, and a probe in the new process compares against
// the version it read there.
func TestUsageLimitRuleRestartKeepsFamilyResets(t *testing.T) {
	s, f := launcherHealthHub(t)
	ctx := context.Background()
	reset := time.Now().UTC().Add(7 * 24 * time.Hour)
	s.recordHealth(ctx, "l4", "work", healthResult{err: "You've hit your Opus limit", resetsAt: &reset, source: healthSourceSession})
	s.recordHealth(ctx, "l4", "work", healthResult{err: "You've hit your Sonnet limit", source: healthSourceSession})
	restarted := reloadHealthService(t, s, f)
	loaded := healthState(restarted, "l4", "work")
	if loaded.rev != 0 || !slices.Equal(loaded.limitModels(), []string{"opus", "sonnet"}) || !loaded.LimitResets["opus"].Equal(reset) || loaded.ResetsAt != nil {
		t.Fatalf("reloaded: %+v", loaded)
	}
	partial := restarted.recordHealth(ctx, "l4", "work", healthResult{ok: true, model: "claude-opus-4-1", source: healthSourceCheck, observed: &loaded})
	if partial.rev == 0 || !partial.unusable() || !slices.Equal(partial.limitModels(), []string{"sonnet"}) || partial.ResetsAt != nil {
		t.Fatalf("a current pass after the restart: %+v", partial)
	}
	if stale := restarted.recordHealth(ctx, "l4", "work", healthResult{ok: true, model: "claude-sonnet-4-5", source: healthSourceCheck, observed: &loaded}); !stale.unusable() || stale.rev != partial.rev {
		t.Fatalf("a pass against the version before was taken: %+v", stale)
	}
}

// A record persisted with resets_at but no limit_resets (main's format: no limit_models either) keeps its reset for
// each family through a bare failure. The variant with limit_models was written only by this PR's first commit,
// never by main; the same rule reads it.
func TestUsageLimitRuleLegacyResetSurvivesBareFailure(t *testing.T) {
	for name, models := range map[string][]string{"main": nil, "first commit": {"opus"}} {
		t.Run(name, func(t *testing.T) {
			s, f := launcherHealthHub(t)
			ctx := context.Background()
			reset := time.Now().UTC().Add(7 * 24 * time.Hour)
			legacy := map[string]any{"machine": "l4", "launcher": "work", "state": healthFailing, "kind": errKindUsageLimit,
				"error": "You've hit your Opus limit", "checked_at": time.Now().UTC().Add(-time.Minute), "source": healthSourceSession, "resets_at": reset}
			if models != nil {
				legacy["limit_models"] = models
			}
			if err := s.store().PutSetting(ctx, launchersHealthKey, map[string]any{"records": []any{legacy}}); err != nil {
				t.Fatal(err)
			}
			reloaded := reloadHealthService(t, s, f)
			after := reloaded.recordHealth(ctx, "l4", "work", healthResult{err: "usage limit reached", source: healthSourceCheck})
			editHealth(reloaded, "l4", "work", func(r *LauncherHealth) { r.CheckedAt = time.Now().Add(-healthStale - time.Minute) })
			if _, bad := reloaded.launcherUnusable(ctx, "l4", "work"); after.ResetsAt == nil || !after.ResetsAt.Equal(reset) || !after.LimitResets["opus"].Equal(reset) || !bad {
				t.Fatalf("a bare failure dropped the persisted reset: %+v, still refused %v", after, bad)
			}
		})
	}
}

// A turn that repeats a family's known limit is nothing new, whatever another family's reset: it neither refreshes
// the evidence nor delays the next probe.
func TestUsageLimitRuleDuplicateTurnWithUnknownSibling(t *testing.T) {
	s, f := launcherHealthHubOn(t, newStorelessService)
	ctx := context.Background()
	reset := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	s.recordHealth(ctx, "l4", "work", healthResult{err: "You've hit your Opus limit", resetsAt: &reset, source: healthSourceSession})
	s.recordHealth(ctx, "l4", "work", healthResult{err: "You've hit your Sonnet limit", source: healthSourceSession})
	editHealth(s, "l4", "work", func(r *LauncherHealth) { r.CheckedAt = time.Now().Add(-time.Minute) })
	before := healthState(s, "l4", "work")
	f.setSessions("l4", proto.Session{SessionID: "dup", Launcher: "work", Status: proto.StatusIdle, State: proto.StateUnread,
		UpdatedAt: time.Now().Add(-time.Minute), TurnError: "You've hit your Opus limit", TurnErrorKind: proto.TurnErrorUsageLimit, TurnErrorResetsAt: &reset})
	s.launcherLimitTurnSettled(ctx, "l4", "dup")
	after := healthState(s, "l4", "work")
	f.checks["l4/work"] = proto.LauncherCheck{OK: true, Model: "claude-opus-4-1"}
	s.launcherUnusableNow(ctx, "l4", "work")
	if after.rev != before.rev || !after.CheckedAt.Equal(before.CheckedAt) || checkedCount(f, "l4/work") != 1 {
		t.Fatalf("a repeated Opus turn refreshed the record (rev %d → %d) and held off the probe (%d probes)", before.rev, after.rev, checkedCount(f, "l4/work"))
	}
}

// The fold rows of the design table (docs/server/launcher-health.md, "Usage-limit record rules"), one case each.
func TestUsageLimitRuleFoldTable(t *testing.T) {
	now := time.Now().UTC()
	past, h1, d3 := now.Add(-time.Minute), now.Add(time.Hour), now.Add(72*time.Hour)
	at := func(t time.Time) *time.Time { return &t }
	type limit struct {
		families []string
		resets   map[string]time.Time
		agg      *time.Time
	}
	for _, tc := range []struct {
		name    string
		rec     LauncherHealth
		event   limit
		want    limit
		changed bool
	}{
		{"no family, later reset", LauncherHealth{ResetsAt: at(h1)}, limit{agg: at(d3)}, limit{agg: at(d3)}, true},
		{"no family, unknown learns reset", LauncherHealth{}, limit{agg: at(h1)}, limit{agg: at(h1)}, true},
		{"no family, unknown says nothing", LauncherHealth{ResetsAt: at(h1)}, limit{}, limit{agg: at(h1)}, false},
		{"bare later reset moves known resets only", LauncherHealth{LimitModels: []string{"opus", "sonnet"}, LimitResets: map[string]time.Time{"opus": h1}},
			limit{agg: at(d3)}, limit{[]string{"opus", "sonnet"}, map[string]time.Time{"opus": d3}, nil}, true},
		{"bare earlier reset says nothing", LauncherHealth{LimitModels: []string{"opus"}, LimitResets: map[string]time.Time{"opus": d3}, ResetsAt: at(d3)},
			limit{agg: at(h1)}, limit{[]string{"opus"}, map[string]time.Time{"opus": d3}, at(d3)}, false},
		{"bare unknown says nothing", LauncherHealth{LimitModels: []string{"opus"}, LimitResets: map[string]time.Time{"opus": d3}, ResetsAt: at(d3)},
			limit{}, limit{[]string{"opus"}, map[string]time.Time{"opus": d3}, at(d3)}, false},
		{"new family with unknown reset", LauncherHealth{LimitModels: []string{"opus"}, LimitResets: map[string]time.Time{"opus": h1}, ResetsAt: at(h1)},
			limit{families: []string{"sonnet"}}, limit{[]string{"opus", "sonnet"}, map[string]time.Time{"opus": h1}, nil}, true},
		{"own family, later reset", LauncherHealth{LimitModels: []string{"opus"}, LimitResets: map[string]time.Time{"opus": h1}, ResetsAt: at(h1)},
			limit{[]string{"opus"}, nil, at(d3)}, limit{[]string{"opus"}, map[string]time.Time{"opus": d3}, at(d3)}, true},
		{"own family, earlier reset", LauncherHealth{LimitModels: []string{"opus"}, LimitResets: map[string]time.Time{"opus": d3}, ResetsAt: at(d3)},
			limit{[]string{"opus"}, nil, at(h1)}, limit{[]string{"opus"}, map[string]time.Time{"opus": d3}, at(d3)}, false},
		{"own family, unknown reset", LauncherHealth{LimitModels: []string{"opus"}, LimitResets: map[string]time.Time{"opus": h1}, ResetsAt: at(h1)},
			limit{families: []string{"opus"}}, limit{[]string{"opus"}, map[string]time.Time{"opus": h1}, at(h1)}, false},
		{"unknown family learns its reset", LauncherHealth{LimitModels: []string{"opus", "sonnet"}, LimitResets: map[string]time.Time{"opus": h1}},
			limit{[]string{"sonnet"}, nil, at(d3)}, limit{[]string{"opus", "sonnet"}, map[string]time.Time{"opus": h1, "sonnet": d3}, at(d3)}, true},
		{"passed reset named again is a new limit", LauncherHealth{LimitModels: []string{"opus", "sonnet"}, LimitResets: map[string]time.Time{"opus": past}},
			limit{families: []string{"opus"}}, limit{[]string{"opus", "sonnet"}, nil, nil}, true},
		{"no-family limit named: its reset is a floor", LauncherHealth{ResetsAt: at(d3)},
			limit{[]string{"opus"}, nil, at(h1)}, limit{[]string{"opus"}, map[string]time.Time{"opus": d3}, at(d3)}, true},
		{"no-family limit named with unknown reset", LauncherHealth{ResetsAt: at(d3)},
			limit{families: []string{"opus"}}, limit{[]string{"opus"}, nil, nil}, true},
		{"main's format, nothing new", LauncherHealth{Error: "You've hit your Opus limit", ResetsAt: at(d3)},
			limit{[]string{"opus"}, nil, at(d3)}, limit{[]string{"opus"}, map[string]time.Time{"opus": d3}, at(d3)}, false},
		{"first commit's format, bare failure", LauncherHealth{LimitModels: []string{"opus"}, ResetsAt: at(d3)},
			limit{}, limit{[]string{"opus"}, map[string]time.Time{"opus": d3}, at(d3)}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := tc.rec
			rec.Kind = errKindUsageLimit
			changed := foldLimit(&rec, tc.event.families, tc.event.agg)
			if changed != tc.changed || !slices.Equal(rec.LimitModels, tc.want.families) ||
				!maps.EqualFunc(rec.LimitResets, tc.want.resets, time.Time.Equal) || !sameReset(rec.ResetsAt, tc.want.agg) {
				t.Fatalf("changed=%v models=%v resets=%v reset=%v; want changed=%v %+v", changed, rec.LimitModels, rec.LimitResets, rec.ResetsAt, tc.changed, tc.want)
			}
		})
	}
}

// The event rows of the design table: what advances the record's rev and check time, and what is dropped.
func TestUsageLimitRuleEventRevisions(t *testing.T) {
	s, _ := launcherHealthHubOn(t, newStorelessService)
	ctx := context.Background()
	reset := time.Now().Add(time.Hour)
	seedUsageLimit(t, s, "l4", "You've hit your Opus limit", &reset)
	step := func(name string, r healthResult, advances, fresh bool) LauncherHealth {
		t.Helper()
		before := healthState(s, "l4", "work")
		after := s.recordHealth(ctx, "l4", "work", r)
		if (after.rev != before.rev) != advances || !after.CheckedAt.Equal(before.CheckedAt) != fresh {
			t.Fatalf("%s: rev %d → %d, checked %v → %v; want advance=%v fresh=%v", name, before.rev, after.rev, before.CheckedAt, after.CheckedAt, advances, fresh)
		}
		return after
	}
	older := LauncherHealth{CheckedAt: time.Now().Add(-time.Hour)}
	step("older failure, nothing new", healthResult{err: "You've hit your Opus limit", source: healthSourceCheck, observed: &older}, false, false)
	if r := step("older failure, new family", healthResult{err: "You've hit your Sonnet limit", source: healthSourceCheck, observed: &older}, true, false); !slices.Equal(r.limitModels(), []string{"opus", "sonnet"}) {
		t.Fatalf("older failure's family: %+v", r)
	}
	step("older pass", healthResult{ok: true, model: "claude-opus-4", source: healthSourceCheck, observed: &older}, false, false)
	step("timeout", healthResult{err: "context deadline exceeded", source: healthSourceCheck}, false, false)
	step("pass of no limited family", healthResult{ok: true, model: "claude-haiku-4", source: healthSourceCheck}, false, false)
	step("current failure", healthResult{err: "You've hit your Opus limit", source: healthSourceCheck}, true, true)
	step("pass of one family", healthResult{ok: true, model: "claude-opus-4", source: healthSourceCheck}, true, true)
	if r := step("pass of the last family", healthResult{ok: true, model: "claude-sonnet-4-5", source: healthSourceCheck}, true, true); r.State != healthOK || r.LimitModels != nil || r.LimitResets != nil || r.ResetsAt != nil {
		t.Fatalf("after the last family's pass: %+v", r)
	}
}
