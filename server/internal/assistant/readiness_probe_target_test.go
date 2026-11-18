package assistant

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// "Try it" checks the main conversation's next turn (readiness_probe_target.go, #10365): the launcher and model a
// message would run, shaped as a turn of that conversation; a pass on another model, or from a node that ran the plain
// check, never clears what the conversation's own turn said.

const routeText = "There's an issue with the selected model (model-a). It may not exist or you may not have access to it."

// lastCheck is the last launchers.check's params.
func lastCheck(t *testing.T, f *fakeHub) map[string]any {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.checkParams) == 0 {
		t.Fatal("no launchers.check")
	}
	return f.checkParams[len(f.checkParams)-1]
}

func checkCount(f *fakeHub) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.checkParams)
}

// The check runs Main chat's own model on its launcher, as an assistant turn of the main conversation in the profile
// Main chat starts new conversations in, not the launcher's default.
func TestReadinessProbeUsesMainTurnModel(t *testing.T) {
	f, s, _ := readyMachine(t, proto.Session{SessionID: "m1", Assistant: true, Launcher: "claude", State: proto.StateIdle})
	setModel(t, s, "assistant", ModelBinding{Provider: "launcher.claude", Model: "bad-model", Effort: "high", ConfigDir: "legacy"})
	r := getReadiness(t, s, "probe=1", "en")
	if r.Probe == nil || !r.Probe.OK || !r.Probe.AssistantTurn {
		t.Fatalf("probe: %+v", r.Probe)
	}
	p := lastCheck(t, f)
	want := map[string]any{"name": "claude", "model": "bad-model", "effort": "high", "purpose": "assistant",
		"assistant_turn": true, "conversation": "m1", "config_dir": "legacy"}
	for k, v := range want {
		if p[k] != v {
			t.Fatalf("launchers.check %s = %v, want %v (%v)", k, p[k], v, p)
		}
	}
	kept := s.readinessRecordNow(context.Background()).Probe
	if kept == nil || !kept.Targeted || kept.Launcher != "claude" || kept.Model != "bad-model" || kept.Effort != "high" {
		t.Fatalf("kept probe: %+v", kept)
	}
}

// A kept pick of the main conversation is what its next turn runs: the check goes to the pick's launcher and model.
func TestReadinessProbeUsesConversationPick(t *testing.T) {
	f, s, _ := readyMachine(t, proto.Session{SessionID: "m1", Assistant: true, Launcher: "gw", State: proto.StateIdle})
	putLaunchers(t, s, storedLauncher{ManagedLauncher: proto.ManagedLauncher{Name: "gw", BaseURL: "https://gw.example", Model: "m1"}})
	setModel(t, s, "assistant", ModelBinding{Provider: "launcher.claude", Model: "opus"})
	if err := s.setModelPick(context.Background(), "m1", &modelPick{MachineID: "server", Launcher: "gw", Model: "vendor/m2", Effort: "low", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	r := getReadiness(t, s, "probe=1", "en")
	if r.Probe == nil || !r.Probe.OK {
		t.Fatalf("probe: %+v", r.Probe)
	}
	p := lastCheck(t, f)
	if p["name"] != "gw" || p["model"] != "vendor/m2" || p["effort"] != "low" || p["conversation"] != "m1" || p["assistant_turn"] != true {
		t.Fatalf("the check did not run the pick: %v", p)
	}
	// Main chat's launcher is still the one readiness shows; the pick's pass says nothing for it.
	if r.Launcher != "claude" {
		t.Fatalf("readiness launcher: %s", r.Launcher)
	}
}

// A pass on another model of the launcher, or one an older manager kept for the launcher's default, never clears the
// main conversation's model_route; nor is either reused for a probe of the model the turn runs. A pass of that model
// from a node that shaped the check as the turn does; one from a node that ran the plain check does not.
func TestReadinessProbeOtherModelDoesNotClearModelRoute(t *testing.T) {
	for _, seeded := range []struct {
		name  string
		probe readinessProbe
	}{
		{"other model", readinessProbe{Machine: "server", Launcher: "claude", Model: "model-b", Targeted: true,
			Check: proto.LauncherCheck{OK: true, AssistantTurn: true}}},
		{"legacy record", readinessProbe{Machine: "server", Launcher: "claude", Check: proto.LauncherCheck{OK: true}}},
	} {
		t.Run(seeded.name, func(t *testing.T) {
			f, s, _ := readyMachine(t, proto.Session{SessionID: "m1", Assistant: true, Launcher: "claude", State: proto.StateIdle,
				LastText: routeText, UpdatedAt: time.Now().Add(-time.Minute)})
			setModel(t, s, "assistant", ModelBinding{Provider: "launcher.claude", Model: "model-a"})
			if err := s.readinessUpdate(context.Background(), func(r *readinessRecord) {
				p := seeded.probe
				p.At = time.Now()
				r.Probe = &p
			}); err != nil {
				t.Fatal(err)
			}
			if m := readinessStep(t, getReadiness(t, s, "", "en"), StepModel); m.State != StepProblem || m.Code != proto.TurnErrorModelRoute {
				t.Fatalf("a pass of something else cleared model_route: %+v", m)
			}
			// Asked now, within the reuse window, it checks model-a rather than answering the pass it has.
			f.mu.Lock()
			f.oldCheck = true // a node that runs the plain check: its pass is no answer to the turn's failure
			f.mu.Unlock()
			r := getReadiness(t, s, "probe=1", "en")
			if checkCount(f) != 1 || lastCheck(t, f)["model"] != "model-a" {
				t.Fatalf("reused another model's probe: %v", f.checkParams)
			}
			if m := readinessStep(t, r, StepModel); r.Probe == nil || !r.Probe.OK || m.Code != proto.TurnErrorModelRoute {
				t.Fatalf("the plain check's pass cleared model_route: %+v %+v", r.Probe, m)
			}
			f.mu.Lock()
			f.oldCheck = false
			f.mu.Unlock()
			if err := s.readinessUpdate(context.Background(), func(r *readinessRecord) { r.Probe.At = time.Now().Add(-time.Hour) }); err != nil {
				t.Fatal(err)
			}
			r = getReadiness(t, s, "probe=1", "en")
			if m := readinessStep(t, r, StepModel); r.Probe == nil || !r.Probe.AssistantTurn || m.State != StepOK {
				t.Fatalf("a pass of the turn itself did not clear model_route: %+v %+v", r.Probe, m)
			}
		})
	}
}

// A pick the conversation's next message would be refused on is the probe's answer: nothing runs, nothing is kept and
// the first message is not counted as answered.
func TestReadinessProbePickRefusal(t *testing.T) {
	s, f := pickFixture(t)
	if err := s.setModelPick(context.Background(), "main", &modelPick{MachineID: "server", Launcher: "far", Model: "m", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	res := s.readinessProbe(context.Background(), "server", s.settings(context.Background()))
	if res == nil || res.OK || !strings.Contains(res.Error, "far") {
		t.Fatalf("probe: %+v", res)
	}
	if n := checkCount(f); n != 0 {
		t.Fatalf("a refused turn was checked: %v", f.checkParams)
	}
	if rec := s.readinessRecordNow(context.Background()); rec.Probe != nil || rec.AnsweredAt != nil {
		t.Fatalf("a refused turn was kept: %+v", rec)
	}
}
