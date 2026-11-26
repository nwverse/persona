package assistant

import (
	"context"
	"testing"
	"time"

	approvalops "github.com/nwverse/persona/server/internal/approval"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/proto"
)

func setPendingReminderDelay(t *testing.T, d time.Duration) {
	t.Helper()
	prev := pendingReminderDelay
	pendingReminderDelay = d
	t.Cleanup(func() { pendingReminderDelay = prev })
}

func TestPendingApprovalReminderPush(t *testing.T) {
	t.Run("unanswered permission card reminds once", func(t *testing.T) {
		setPendingReminderDelay(t, 40*time.Millisecond)
		ft := newFleetTestNoStore(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		done := make(chan proto.Resolution, 1)
		go func() {
			r, _ := ft.s.waitInteraction(ctx, proto.Interaction{
				ID:        "ix_remind_once",
				MachineID: "mac",
				SessionID: "s1",
				Kind:      proto.IxPermission,
				ToolName:  "Bash",
				ExpiresAt: time.Now().Add(time.Hour).UTC(),
			})
			done <- r
		}()

		got := ft.pushesOf(assistantwire.NotifyInput, 2, 2*time.Second)
		if len(got) != 2 {
			t.Fatalf("pushes = %d, want 2 (initial + 1 reminder): %+v", len(got), got)
		}
		// Wait past another delay window to verify at most one reminder fires.
		time.Sleep(100 * time.Millisecond)
		if again := ft.pushesOf(assistantwire.NotifyInput, 3, 0); len(again) != 2 {
			t.Fatalf("pushes after extra wait = %d, want 2", len(again))
		}
		cancel()
		<-done
	})

	t.Run("resolved card before delay sends no reminder", func(t *testing.T) {
		setPendingReminderDelay(t, 120*time.Millisecond)
		ft := newFleetTestNoStore(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		done := make(chan proto.Resolution, 1)
		go func() {
			r, _ := ft.s.waitInteraction(ctx, proto.Interaction{
				ID:        "ix_remind_resolved",
				MachineID: "mac",
				SessionID: "s1",
				Kind:      proto.IxPermission,
				ToolName:  "Bash",
				ExpiresAt: time.Now().Add(time.Hour).UTC(),
			})
			done <- r
		}()

		if got := ft.pushesOf(assistantwire.NotifyInput, 1, 2*time.Second); len(got) != 1 {
			t.Fatalf("initial pushes = %d, want 1", len(got))
		}
		allow := true
		if err := ft.s.resolve("ix_remind_resolved", proto.Resolution{Allow: &allow}); err != nil {
			t.Fatalf("resolve: %v", err)
		}
		<-done
		time.Sleep(200 * time.Millisecond)
		if got := ft.pushesOf(assistantwire.NotifyInput, 2, 0); len(got) != 1 {
			t.Fatalf("pushes after resolve = %d, want 1", len(got))
		}
	})

	t.Run("short-lived card expiring before delay does not remind", func(t *testing.T) {
		setPendingReminderDelay(t, 200*time.Millisecond)
		ft := newFleetTestNoStore(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		done := make(chan proto.Resolution, 1)
		go func() {
			r, _ := ft.s.waitInteraction(ctx, proto.Interaction{
				ID:        "ix_remind_short",
				MachineID: "mac",
				SessionID: "s1",
				Kind:      proto.IxPermission,
				ToolName:  "Bash",
				ExpiresAt: time.Now().Add(80 * time.Millisecond).UTC(),
			})
			done <- r
		}()

		if got := ft.pushesOf(assistantwire.NotifyInput, 1, 2*time.Second); len(got) != 1 {
			t.Fatalf("initial pushes = %d, want 1", len(got))
		}
		<-done
		time.Sleep(250 * time.Millisecond)
		if got := ft.pushesOf(assistantwire.NotifyInput, 2, 0); len(got) != 1 {
			t.Fatalf("pushes on expired card = %d, want 1", len(got))
		}
	})

	t.Run("muted session suppresses reminder", func(t *testing.T) {
		setPendingReminderDelay(t, 80*time.Millisecond)
		ft := newFleetTestNoStore(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		done := make(chan proto.Resolution, 1)
		go func() {
			r, _ := ft.s.waitInteraction(ctx, proto.Interaction{
				ID:        "ix_remind_muted",
				MachineID: "mac",
				SessionID: "s1",
				Kind:      proto.IxQuestion,
				ToolName:  "ask_user",
				ExpiresAt: time.Now().Add(time.Hour).UTC(),
			})
			done <- r
		}()

		if got := ft.pushesOf(assistantwire.NotifyInput, 1, 2*time.Second); len(got) != 1 {
			t.Fatalf("initial pushes = %d, want 1", len(got))
		}
		ft.s.setMutes(assistantwire.Mutes{Sessions: []string{"mac/s1"}})

		time.Sleep(160 * time.Millisecond)
		if got := ft.pushesOf(assistantwire.NotifyInput, 2, 0); len(got) != 1 {
			t.Fatalf("pushes after mute = %d, want 1", len(got))
		}
		cancel()
		<-done
	})

	t.Run("prepared card entering keepWaiting arms reminder on extended deadline", func(t *testing.T) {
		setPendingReminderDelay(t, 40*time.Millisecond)
		prevInit, prevWait := approvalInitialWait, approvalWaitingWindow
		approvalInitialWait = 20 * time.Millisecond
		approvalWaitingWindow = time.Hour
		t.Cleanup(func() {
			approvalInitialWait = prevInit
			approvalWaitingWindow = prevWait
		})

		ft := newFleetTest(t)
		ft.d.setSessions("mac", []proto.Session{{SessionID: "conv-1", Assistant: true, Status: proto.StatusWorking}})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		op := approvalops.Operation{
			Apply: func(context.Context, proto.Resolution) (string, error) { return "applied", nil },
		}
		// Initial expiry is shorter than pendingReminderDelay, so only keepWaiting's extended deadline arms the reminder.
		out, err := ft.s.waitPreparedCard(ctx, proto.Interaction{
			ID:        "ix_remind_waiting",
			MachineID: "mac",
			SessionID: "conv-1",
			Kind:      proto.IxPermission,
			ToolName:  "Bash",
			ExpiresAt: time.Now().Add(30 * time.Millisecond).UTC(),
		}, op)
		if err != nil || !out.Clarifying {
			t.Fatalf("waitPreparedCard = (%+v, %v), want clarifying waiting result", out, err)
		}
		got := ft.pushesOf(assistantwire.NotifyInput, 2, 2*time.Second)
		if len(got) != 2 {
			t.Fatalf("pushes after keepWaiting = %d, want 2 (initial + 1 reminder): %+v", len(got), got)
		}
		allow := false
		_ = ft.s.resolve("ix_remind_waiting", proto.Resolution{Allow: &allow})
	})
}
