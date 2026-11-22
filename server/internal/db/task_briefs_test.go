package db

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// A brief is written once and never rewritten; only its state changes. To-dos link to it once, and the links go with
// their to-dos.
func TestTaskBriefs(t *testing.T) {
	d, ctx := open(t), context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	for _, id := range []string{"tb1", "tb2"} {
		if err := d.CreateTodo(ctx, TodoRow{ID: id, Title: "fix the login crash", CreatedAt: now, UpdatedAt: now, Origin: "main"}); err != nil {
			t.Fatal(err)
		}
	}
	made, err := d.CreateTaskBrief(ctx, TaskBriefRow{ID: "brief_t1", Brief: json.RawMessage(`{"goal":"fix the login crash"}`), CreatedAt: now, UpdatedAt: now})
	if err != nil || !made {
		t.Fatalf("create = %v, %v", made, err)
	}
	made, err = d.CreateTaskBrief(ctx, TaskBriefRow{ID: "brief_t1", Brief: json.RawMessage(`{"goal":"something else"}`), CreatedAt: now, UpdatedAt: now})
	if err != nil || made {
		t.Fatalf("a second create must leave the brief as it is: %v, %v", made, err)
	}
	if ok, err := d.PutTaskBriefState(ctx, "brief_t1", json.RawMessage(`{"log":[{"kind":"scope"}]}`), now.Add(time.Minute)); err != nil || !ok {
		t.Fatalf("state = %v, %v", ok, err)
	}
	if ok, err := d.PutTaskBriefState(ctx, "brief_none", json.RawMessage(`{}`), now); err != nil || ok {
		t.Fatalf("state of a missing brief = %v, %v", ok, err)
	}
	if ok, err := d.LinkTaskBriefTodo(ctx, TaskBriefTodoRow{TodoID: "tb1", BriefID: "brief_t1", LinkedAt: now}); err != nil || !ok {
		t.Fatalf("link = %v, %v", ok, err)
	}
	if ok, err := d.LinkTaskBriefTodo(ctx, TaskBriefTodoRow{TodoID: "tb2", BriefID: "brief_t1", Continues: "tb1", Reason: "the first agent's computer went offline", LinkedAt: now.Add(time.Second)}); err != nil || !ok {
		t.Fatalf("link of the take-over = %v, %v", ok, err)
	}
	if ok, err := d.LinkTaskBriefTodo(ctx, TaskBriefTodoRow{TodoID: "tb2", BriefID: "brief_t1", LinkedAt: now}); err != nil || ok {
		t.Fatalf("a to-do links once: %v, %v", ok, err)
	}
	if _, err := d.LinkTaskBriefTodo(ctx, TaskBriefTodoRow{TodoID: "tb_gone", BriefID: "brief_t1", LinkedAt: now}); !errors.Is(err, ErrTodoNotFound) {
		t.Fatalf("missing to-do: %v", err)
	}
	briefs, links, err := d.ListTaskBriefs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var got *TaskBriefRow
	for i := range briefs {
		if briefs[i].ID == "brief_t1" {
			got = &briefs[i]
		}
	}
	var brief struct{ Goal string }
	if got == nil || json.Unmarshal(got.Brief, &brief) != nil || brief.Goal != "fix the login crash" || !got.CreatedAt.Equal(now) ||
		!got.UpdatedAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("brief = %+v", got)
	}
	var state struct{ Log []struct{ Kind string } }
	if json.Unmarshal(got.State, &state) != nil || len(state.Log) != 1 || state.Log[0].Kind != "scope" {
		t.Fatalf("state = %s", got.State)
	}
	var mine []TaskBriefTodoRow
	for _, l := range links {
		if l.BriefID == "brief_t1" {
			mine = append(mine, l)
		}
	}
	if len(mine) != 2 || mine[0].TodoID != "tb1" || mine[1].TodoID != "tb2" || mine[1].Continues != "tb1" || mine[1].Reason == "" {
		t.Fatalf("links = %+v", mine)
	}
	if err := d.DeleteTodo(ctx, "tb2"); err != nil {
		t.Fatal(err)
	}
	_, links, _ = d.ListTaskBriefs(ctx)
	for _, l := range links {
		if l.TodoID == "tb2" {
			t.Fatalf("the link stayed after its to-do went: %+v", l)
		}
	}
}
