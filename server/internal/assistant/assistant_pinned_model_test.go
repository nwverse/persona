package assistant

import (
	"context"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestProtectedQuotaWaitContinuesOnConfiguredModel(t *testing.T) {
	for _, leader := range []bool{false, true} {
		name := "main"
		if leader {
			name = "supervisor"
		}
		t.Run(name, func(t *testing.T) {
			s, f := workService(t)
			f.withMachine()
			setSettings(t, s, func(v *Settings) {
				v.MachineID = "server"
				if leader {
					v.Leaders = []string{"s1"}
				} else {
					v.Main = "s1"
				}
			})
			setModel(t, s, "assistant", ModelBinding{Provider: "launcher.claude", Model: "claude-opus-5-5"})
			reset := time.Now().UTC().Add(time.Hour).Truncate(time.Minute)
			failure := "API Error: Request rejected (429) · upstream account quota usage limit has been reached; an account becomes available again at " + reset.Format("15:04") + " UTC (in 60m)"
			c := apiErrorTurn("s1", failure)
			c.Assistant, c.Launcher, c.Model, c.UpdatedAt = true, "claude", "claude-haiku-5-5", time.Now()
			decorateLimit(&c)
			f.setSessions("server", c)
			ctx := context.Background()
			s.triageTurn(ctx, s.newWorkView(ctx), "server", c)
			e, ok := s.workEntry([2]string{"server", "s1"})
			if !ok || e.Work.On != proto.WaitExternal || e.WakeAt == nil || e.WakeAt.Before(reset) {
				t.Fatalf("quota did not wait for reset: %+v", e)
			}
			s.workTick(ctx, reset.Add(-time.Second))
			if len(f.sentToAgents()) != 0 {
				t.Fatal("continued before quota reset")
			}
			s.workTick(ctx, *e.WakeAt)
			s.workTick(ctx, e.WakeAt.Add(time.Second))
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.agentSent) != 1 {
				t.Fatalf("continuations: %v", f.agentSent)
			}
			p := f.agentSent[0]
			if p["model"] != "claude-opus-5-5" || p["launcher"] != "claude" || (leader && p["follows_setting"] != true) || (!leader && p["is_main"] != true) {
				t.Fatalf("quota continuation reused observed fallback: %v", p)
			}
		})
	}
}

func TestProtectedQuotaRuntimeResumeIsLeftAlone(t *testing.T) {
	s, f := workService(t)
	setSettings(t, s, func(v *Settings) { v.MachineID, v.Main = "box", "s1" })
	reset := time.Now().Add(time.Hour)
	c := limitTurn("s1", reset, true)
	c.Assistant, c.UpdatedAt = true, time.Now()
	f.setSessions("box", c)
	triage(t, s, "s1")
	// A runtime-owned assistant can still project as working, so no manager wait row is required.
	if e, tracked := s.workEntry([2]string{"box", "s1"}); tracked && (e.Work.On != proto.WaitExternal || e.WakeAt != nil) {
		t.Fatalf("runtime-owned quota wait: %+v", e)
	}
	s.workTick(context.Background(), reset.Add(time.Hour))
	if len(f.sentToAgents()) != 0 {
		t.Fatal("interrupted the runtime's own quota continuation")
	}
}

func TestProtectedOrdinaryManagerSendKeepsExistingParams(t *testing.T) {
	s, f := workService(t)
	setSettings(t, s, func(v *Settings) { v.MachineID, v.Main = "box", "s1" })
	if err := s.sendToSession(context.Background(), "box", "s1", "ordinary manager message"); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.agentSent) != 1 || len(f.agentSent[0]) != 2 || f.agentSent[0]["text"] != "ordinary manager message" {
		t.Fatalf("ordinary manager send changed: %v", f.agentSent)
	}
}
