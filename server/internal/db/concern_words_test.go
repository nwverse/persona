package db

import (
	"context"
	"errors"
	"testing"
	"time"
)

// The matter tables (migrations 20261124120000/1): a message is filed under at most one concern, a move rewrites its
// todo_id, state upserts and the delete of a concern cascades to both tables.
func TestConcernWordsAndState(t *testing.T) {
	ctx := context.Background()
	d, err := Open(ctx, freshDatabase(t), ManagerSet)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	mkConcern := func(id string) {
		t.Helper()
		if err := d.CreateTodo(ctx, TodoRow{ID: id, Title: "matter " + id, Kind: KindConcern,
			Origin: "conv1", CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	mkConcern("todo_c1")
	mkConcern("todo_c2")

	// File two messages under c1, oldest said first when listed.
	if err := d.PutConcernWord(ctx, ConcernWordRow{Conversation: "conv1", MessageID: "m2", TodoID: "todo_c1",
		Text: "a follow-up", SaidAt: now.Add(time.Minute), FiledAt: now.Add(2 * time.Minute), FiledBy: FiledByAssistant}); err != nil {
		t.Fatal(err)
	}
	if err := d.PutConcernWord(ctx, ConcernWordRow{Conversation: "conv1", MessageID: "m1", TodoID: "todo_c1",
		Text: "the first message", SaidAt: now, FiledAt: now, FiledBy: FiledByAssistant}); err != nil {
		t.Fatal(err)
	}
	words, err := d.ListConcernWords(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(words) != 2 || words[0].MessageID != "m1" || words[1].MessageID != "m2" {
		t.Fatalf("words oldest first: %+v", words)
	}

	// Filing m1 again moves it to c2; one message stays under exactly one concern.
	if err := d.PutConcernWord(ctx, ConcernWordRow{Conversation: "conv1", MessageID: "m1", TodoID: "todo_c2",
		Text: "the first message", SaidAt: now, FiledAt: now.Add(3 * time.Minute), FiledBy: FiledByOwner}); err != nil {
		t.Fatal(err)
	}
	words, _ = d.ListConcernWords(ctx)
	byMessage := map[string]ConcernWordRow{}
	for _, w := range words {
		byMessage[w.MessageID] = w
	}
	if len(words) != 2 || byMessage["m1"].TodoID != "todo_c2" || byMessage["m1"].FiledBy != FiledByOwner ||
		byMessage["m2"].TodoID != "todo_c1" {
		t.Fatalf("after the move: %+v", words)
	}

	// State upserts, including an options replacement and an empty-options write; an omitted field keeps its value.
	if err := d.PutConcernState(ctx, ConcernStateRow{TodoID: "todo_c1", About: "what it is", Now: "in progress",
		Ask: "which way?", Options: []string{"a", "b"}, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := d.PutConcernState(ctx, ConcernStateRow{TodoID: "todo_c1", About: "what it is", Ask: "which way?",
		Now: "done soon", Options: []string{}, UpdatedAt: now.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	states, err := d.ListConcernStates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]ConcernStateRow{}
	for _, st := range states {
		got[st.TodoID] = st
	}
	st := got["todo_c1"]
	if st.About != "what it is" || st.Now != "done soon" || st.Ask != "which way?" || len(st.Options) != 0 {
		t.Fatalf("state upsert: %+v", st)
	}

	// A write under a missing concern is ErrTodoNotFound (the foreign key), and deleting the concern cascades.
	if err := d.PutConcernWord(ctx, ConcernWordRow{Conversation: "conv1", MessageID: "m9", TodoID: "todo_gone",
		Text: "x", SaidAt: now, FiledAt: now, FiledBy: FiledByOwner}); !errors.Is(err, ErrTodoNotFound) {
		t.Fatalf("a word under a missing concern: %v", err)
	}
	if err := d.DeleteTodo(ctx, "todo_c1"); err != nil {
		t.Fatal(err)
	}
	if states, err = d.ListConcernStates(ctx); err != nil {
		t.Fatal(err)
	}
	for _, st := range states {
		if st.TodoID == "todo_c1" {
			t.Fatal("the concern's state survived its delete")
		}
	}
	if words, err = d.ListConcernWords(ctx); err != nil {
		t.Fatal(err)
	}
	for _, w := range words {
		if w.TodoID == "todo_c1" {
			t.Fatal("the concern's words survived its delete")
		}
	}
}

// ConfirmTodo writes confirmed_at without touching updated_at, and a nil time clears it.
func TestConfirmTodo(t *testing.T) {
	ctx := context.Background()
	d, err := Open(ctx, freshDatabase(t), ManagerSet)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	if err := d.CreateTodo(ctx, TodoRow{ID: "todo_t1", Title: "a task", Origin: "conv1",
		CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	at := now.Add(time.Hour)
	row, err := d.ConfirmTodo(ctx, "todo_t1", &at)
	if err != nil {
		t.Fatal(err)
	}
	if row.ConfirmedAt == nil || !row.ConfirmedAt.Equal(at) || !row.UpdatedAt.Equal(now) {
		t.Fatalf("confirm: %+v", row)
	}
	row, err = d.ConfirmTodo(ctx, "todo_t1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if row.ConfirmedAt != nil || !row.UpdatedAt.Equal(now) {
		t.Fatalf("undo: %+v", row)
	}
	if _, err := d.ConfirmTodo(ctx, "todo_gone", &at); !errors.Is(err, ErrTodoNotFound) {
		t.Fatalf("an unknown to-do: %v", err)
	}
}
