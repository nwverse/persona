package db

import (
	"context"
	"testing"
	"time"
)

// A todo's due date, reminder and priority (#1683): stored and read back; a due day and a due moment replace each
// other; a reminder is claimed once, and only while it is the one claimed for, not done; a new reminder time re-arms
// it; the store refuses both due fields at once and an unknown priority.
func TestTodoPlan(t *testing.T) {
	d := open(t)
	ctx := context.Background()
	base := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	day := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	remind := base.Add(time.Hour)
	if err := d.CreateTodo(ctx, TodoRow{ID: "todo_p", Title: "Renew the domain", CreatedAt: base, UpdatedAt: base, DueDate: &day,
		RemindAt: &remind, Priority: PriorityHigh}); err != nil {
		t.Fatal(err)
	}
	got, err := d.GetTodo(ctx, "todo_p")
	if err != nil || got.DueDate == nil || !got.DueDate.Equal(day) || got.DueAt != nil || got.RemindAt == nil ||
		!got.RemindAt.Equal(remind) || got.RemindedAt != nil || got.Priority != PriorityHigh {
		t.Fatalf("stored: %+v %v", got, err)
	}

	if ok, err := d.ClaimTodoReminder(ctx, "todo_p", remind.Add(time.Minute), base); err != nil || ok {
		t.Fatalf("claimed a reminder for another time: %v %v", ok, err)
	}
	if ok, err := d.ClaimTodoReminder(ctx, "todo_p", remind, remind); err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	if ok, err := d.ClaimTodoReminder(ctx, "todo_p", remind, remind); err != nil || ok {
		t.Fatalf("claimed twice: %v %v", ok, err)
	}

	// The same reminder time keeps it pushed; another one re-arms it. A due moment replaces the day.
	at := base.Add(48 * time.Hour)
	row, err := d.EditTodo(ctx, "todo_p", TodoEdit{SetDue: true, DueAt: &at, SetRemind: true, RemindAt: &remind}, base.Add(time.Minute))
	if err != nil || row.DueAt == nil || !row.DueAt.Equal(at) || row.DueDate != nil || row.RemindedAt == nil {
		t.Fatalf("edit, same reminder: %+v %v", row, err)
	}
	later := remind.Add(24 * time.Hour)
	low := PriorityLow
	row, err = d.EditTodo(ctx, "todo_p", TodoEdit{SetRemind: true, RemindAt: &later, Priority: &low}, base.Add(2*time.Minute))
	if err != nil || row.RemindedAt != nil || !row.RemindAt.Equal(later) || row.Priority != PriorityLow || !row.DueAt.Equal(at) {
		t.Fatalf("edit, new reminder: %+v %v", row, err)
	}

	// Done: not claimed.
	done := true
	if _, err := d.EditTodo(ctx, "todo_p", TodoEdit{Done: &done}, base.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if ok, err := d.ClaimTodoReminder(ctx, "todo_p", later, later); err != nil || ok {
		t.Fatalf("claimed a done todo's reminder: %v %v", ok, err)
	}
	// Cleared.
	row, err = d.EditTodo(ctx, "todo_p", TodoEdit{SetDue: true, SetRemind: true}, base.Add(4*time.Minute))
	if err != nil || row.DueAt != nil || row.DueDate != nil || row.RemindAt != nil {
		t.Fatalf("cleared: %+v %v", row, err)
	}

	both := TodoRow{ID: "todo_both", Title: "x", CreatedAt: base, UpdatedAt: base, DueAt: &at, DueDate: &day}
	if err := d.CreateTodo(ctx, both); err == nil {
		t.Error("a todo with a due day and a due moment was stored")
	}
	odd := TodoRow{ID: "todo_odd", Title: "x", CreatedAt: base, UpdatedAt: base, Priority: "urgent"}
	if err := d.CreateTodo(ctx, odd); err == nil {
		t.Error("a todo with an unknown priority was stored")
	}
}
