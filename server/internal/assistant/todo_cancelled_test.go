package assistant

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/db"
)

// Cancellation is its own outcome in the API even though older rows store it as done_by=cancelled and newer clients
// still need done_by=review for compatibility (#5732).
func TestCancelledTodoCarriesItsTimeAndReason(t *testing.T) {
	s, _, todo := newSupervisedService(t)
	raw, _ := json.Marshal(map[string]any{"todo_id": todo.ID, "verdict": "cancelled", "note": "用户改期，不再本轮做"})
	if _, err := s.runTool(context.Background(), "server", "conv1", "task_review", raw); err != nil {
		t.Fatal(err)
	}
	stored := todoNow(s, todo.ID)
	if stored.DoneBy != db.DoneByCancelled || stored.DoneAt == nil {
		t.Fatalf("stored cancellation = %+v", stored)
	}
	got := listedTodos(t, s)[todo.ID]
	if got["done_by"] != db.DoneByReview || got["cancelled_reason"] != "用户改期，不再本轮做" {
		t.Fatalf("listed cancellation = %+v", got)
	}
	if got["cancelled_at"] == nil || got["cancelled_at"] != got["done_at"] {
		t.Fatalf("cancelled_at %v, done_at %v", got["cancelled_at"], got["done_at"])
	}
	for _, task := range s.snapshotTasks(context.Background(), "https://hub.example.com") {
		if task.ID == todo.ID {
			if task.State != "已取消" || task.CancelledReason != "用户改期，不再本轮做" {
				t.Fatalf("snapshot cancellation = %+v", task)
			}
			return
		}
	}
	t.Fatal("cancelled task is missing from the snapshot")
}

// Legacy rows predate the stored completion marker: their supervision verdict still has to project the additive
// cancellation fields at the API boundary, including the existing review-note fallback (#5732).
func TestLegacyCancelledTodoCarriesItsTimeAndReason(t *testing.T) {
	s, _, _ := newSupervisedService(t)
	done := time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)
	putTodo(s, Todo{ID: "legacy_cancelled", Title: "旧方案停止", Origin: "conv1", DoneAt: &done, DoneBy: db.DoneByReview,
		Supervision: &Supervision{Supervisor: "conv1", State: reviewCancelled,
			Reviews: []TaskReview{{Verdict: reviewCancelled, Note: "由新方案接手"}}}})
	got := listedTodos(t, s)["legacy_cancelled"]
	if got["done_by"] != db.DoneByReview || got["cancelled_reason"] != "由新方案接手" {
		t.Fatalf("legacy listed cancellation = %+v", got)
	}
	if got["cancelled_at"] == nil || got["cancelled_at"] != got["done_at"] {
		t.Fatalf("legacy cancelled_at %v, done_at %v", got["cancelled_at"], got["done_at"])
	}
}
