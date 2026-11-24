package assistant

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/proto"
)

func sentTextsTo(f *fakeHub, sessionID string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, m := range f.sends {
		if sid, _ := m["session_id"].(string); sid == sessionID {
			if txt, _ := m["text"].(string); txt != "" {
				out = append(out, txt)
			}
		}
	}
	return out
}

// When a supervisor's owner-only card expires while the user is away, the hub wakes that
// supervisor once as soon as the user's app comes online, and never repeats for the same matter
// across reconnects or subsequent chat messages.
func TestOwnerPresentWakesSupervisorOnAppOnlineAndDedupes(t *testing.T) {
	oldMax := ownerCardMaxWait
	ownerCardMaxWait = 80 * time.Millisecond
	t.Cleanup(func() { ownerCardMaxWait = oldMax })

	ft := newFleetTestNoStore(t)
	s := ft.s
	shortSupervise(t, s)
	ft.f.setSessions("mac", proto.Session{MachineID: "mac", SessionID: "sup1", Assistant: true, Status: proto.StatusIdle})

	// Simulate two expired secret_run cards in the same supervisor conversation (same matter).
	for _, id := range []string{"ix_exp_1", "ix_exp_2"} {
		ix := proto.Interaction{
			ID: id, MachineID: "mac", SessionID: "sup1",
			Kind: proto.IxPermission, ToolName: proto.SecretRunTool,
			Summary: "Run sudo backup on Mac mini",
			Fields:  []proto.Field{{Key: "password", Label: "Password", Secret: true}},
		}
		r, err := s.waitInteraction(context.Background(), ix)
		if err != nil || !r.Dismiss || r.Outcome() != proto.OutcomeExpired {
			t.Fatalf("card %s waitInteraction = %+v, %v; want expired", id, r, err)
		}
	}

	// User opens the app -> hub emits fleet.clients with AppOnline=true.
	onlineRaw, _ := json.Marshal(assistantwire.Clients{AppOnline: true})
	if !s.FleetEvent(context.Background(), assistantwire.EventClients, onlineRaw) {
		t.Fatal("FleetEvent(EventClients) not handled")
	}

	waitFor(t, "supervisor woken on app online", func() bool {
		return len(sentTextsTo(ft.f, "sup1")) == 1
	})
	first := sentTextsTo(ft.f, "sup1")[0]
	if !strings.Contains(first, "<persona-supervision") || !strings.Contains(first, "重新发卡") {
		t.Fatalf("unexpected wake message: %q", first)
	}

	// App reconnects (offline -> online) and user speaks in another conversation: must NOT nudge twice.
	offlineRaw, _ := json.Marshal(assistantwire.Clients{AppOnline: false})
	s.FleetEvent(context.Background(), assistantwire.EventClients, offlineRaw)
	s.FleetEvent(context.Background(), assistantwire.EventClients, onlineRaw)
	s.ownerAppeared(context.Background(), "user_spoke")
	time.Sleep(100 * time.Millisecond)

	if got := len(sentTextsTo(ft.f, "sup1")); got != 1 {
		t.Fatalf("supervisor nudged %d times for the same matter, want 1", got)
	}
}

// When a supervised task is waiting on an owner-only action (only_owner_knows or judge Waits)
// and the user speaks in any conversation, the hub sends one supervision digest entry to the
// task's supervisor and does not duplicate it on repeated presence signals.
func TestOwnerPresentWakesSupervisedTaskOnUserSpokeAndDedupes(t *testing.T) {
	f := newFakeHub()
	f.devices = []Device{{ID: "server", Name: "Server", Online: true}}
	f.setSessions("server",
		proto.Session{MachineID: "server", SessionID: "main1", Assistant: true, Status: proto.StatusIdle},
		proto.Session{MachineID: "server", SessionID: "lead1", Assistant: true, Status: proto.StatusIdle},
		proto.Session{MachineID: "server", SessionID: "other_chat", Assistant: true, Status: proto.StatusIdle},
	)
	s := newTestService(t, f)
	shortSupervise(t, s)
	ctx := context.Background()
	if err := s.setSettings(ctx, Settings{MachineID: "server", Main: "main1"}); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	todo, err := s.addTodo(ctx, Todo{Title: "Deploy with sudo password", Origin: "lead1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.updateSupervision(ctx, todo.ID, func(v *Supervision) bool {
		v.Supervisor = "lead1"
		v.SupervisorSource = "explicit"
		v.State = reviewNeedsUser
		v.Reviews = append(v.Reviews, TaskReview{
			At:       now,
			Verdict:  reviewNeedsUser,
			WhyOwner: whyOnlyOwnerKnow,
			Reason:   "Need owner sudo password to finish install",
		})
		return true
	}); err != nil {
		t.Fatal(err)
	}

	// User speaks in an unrelated conversation.
	s.UserSpoke(ctx, SessionRef{MachineID: "server", SessionID: "other_chat"})

	waitFor(t, "supervisor receives owner_present supervision message", func() bool {
		return len(sentTextsTo(f, "lead1")) == 1
	})
	msg := sentTextsTo(f, "lead1")[0]
	if !strings.Contains(msg, "signal=owner_present") || !strings.Contains(msg, todo.ID) {
		t.Fatalf("supervision message = %q, want owner_present for %s", msg, todo.ID)
	}

	// Speaking again or app coming online must not repeat the nudge for the same waiting round.
	s.UserSpoke(ctx, SessionRef{MachineID: "server", SessionID: "other_chat"})
	s.ownerAppeared(ctx, "app_online")
	time.Sleep(150 * time.Millisecond)

	if got := len(sentTextsTo(f, "lead1")); got != 1 {
		t.Fatalf("supervisor got %d messages for same task wait, want 1", got)
	}
}

// If a live owner-only card is already pending in "Needs you" for the task, user presence must not
// redundantly nudge the supervisor to issue another card.
func TestOwnerPresentSkipsWhenLiveOwnerCardAlreadyPending(t *testing.T) {
	ft := newFleetTestNoStore(t)
	s := ft.s
	shortSupervise(t, s)
	ctx := context.Background()
	ft.f.setSessions("mac", proto.Session{MachineID: "mac", SessionID: "lead1", Assistant: true, Status: proto.StatusIdle})
	if err := s.setSettings(ctx, Settings{MachineID: "mac", Main: "lead1"}); err != nil {
		t.Fatal(err)
	}
	todo, err := s.addTodo(ctx, Todo{Title: "Need sudo", Origin: "lead1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.updateSupervision(ctx, todo.ID, func(v *Supervision) bool {
		v.Supervisor = "lead1"
		v.SupervisorSource = "explicit"
		v.Waits = &OwnerWait{Work: time.Now().UTC(), At: time.Now().UTC(), Reason: "Waiting on password"}
		return true
	}); err != nil {
		t.Fatal(err)
	}

	// Put a live secret_run card on the supervisor conversation.
	cardCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		_, _ = s.waitInteraction(cardCtx, proto.Interaction{
			ID: "ix_live", MachineID: "mac", SessionID: "lead1",
			Kind: proto.IxPermission, ToolName: proto.SecretRunTool,
			Fields: []proto.Field{{Key: "password", Label: "Password", Secret: true}},
		})
	}()
	ft.waitPending(1)

	s.ownerAppeared(ctx, "app_online")
	time.Sleep(150 * time.Millisecond)
	if got := len(sentTextsTo(ft.f, "lead1")); got != 0 {
		t.Fatalf("expected 0 nudges while live owner card is pending, got %d", got)
	}
}
