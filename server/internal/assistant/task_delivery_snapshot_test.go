package assistant

import (
	"context"
	"testing"
	"time"
)

// The real snapshot projection, not a hand-built template value: delivered work and owner waits use the same words as
// the to-do pages, including a task with no attached Agent. Accepted work is completed, not awaiting review (#5732).
func TestSnapshotTasksUseDeliveryAndOwnerWaitStates(t *testing.T) {
	s, _ := newQuietService(t)
	ctx := context.Background()
	delivered, err := s.createTodo(ctx, "Snapshot delivery", "", "conv1", "")
	if err != nil {
		t.Fatal(err)
	}
	s.superviseTodo(ctx, delivered.ID, "conv1", []string{"result verified"}, 30)
	if _, _, err := s.updateSupervision(ctx, delivered.ID, func(v *Supervision) bool { v.State = reviewPending; return true }); err != nil {
		t.Fatal(err)
	}
	s.setTaskDelivery(ctx, delivered.ID, TodoDelivery{State: deliveryLive, At: time.Now()})
	live, err := s.createTodo(ctx, "Live without a ledger", "", "conv1", "")
	if err != nil {
		t.Fatal(err)
	}
	s.setTaskDelivery(ctx, live.ID, TodoDelivery{State: deliveryLive, At: time.Now()})
	s.superviseTodo(ctx, live.ID, "conv1", []string{"result verified"}, 30)

	byID := func() map[string]struct{ State, Health string } {
		out := map[string]struct{ State, Health string }{}
		for _, task := range s.snapshotTasks(ctx, "https://hub.example.com") {
			out[task.ID] = struct{ State, Health string }{task.State, task.Health}
		}
		return out
	}
	got := byID()
	if got[delivered.ID].State != "已交付·待看" || got[delivered.ID].Health != "已交付·待看" {
		t.Fatalf("pending review projection = %+v", got[delivered.ID])
	}
	if got[live.ID].State != "已交付·待看" || got[live.ID].Health != "已交付·待看" {
		t.Fatalf("unaccepted live projection = %+v", got[live.ID])
	}

	if _, _, err := s.updateSupervision(ctx, delivered.ID, func(v *Supervision) bool { v.State = reviewNeedsUser; return true }); err != nil {
		t.Fatal(err)
	}
	if got := byID()[delivered.ID]; got.State != "等你处理" || got.Health != "等你处理" {
		t.Fatalf("owner-wait projection = %+v", got)
	}
	if _, err := runJSON(t, s, "conv1", "task_review", map[string]any{"todo_id": delivered.ID, "verdict": "accepted", "note": "verified"}); err != nil {
		t.Fatal(err)
	}
	if got := byID()[delivered.ID]; got.State != "已完成" || got.Health != "已验收" {
		t.Fatalf("accepted projection = %+v", got)
	}
}
