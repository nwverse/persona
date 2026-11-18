package assistant

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

// The To-dos view stays fresh (todo_view_push.go, #10226 D8): the apps are told to read the list again
// ({"type":"todos"}) when what a row is filed under changes with no write to its to-do, because one of its runs
// changed or time alone moved it; never for a working run's every step.

// todoViewClock puts the views' clock, the pushes' settle and the direction tree on one fake clock, now.
func todoViewClock(e *todoEnv) *fakeClock {
	c := &fakeClock{now: time.Now()}
	e.s.tview.now, e.s.tview.after = c.Now, c.After
	e.s.tpush.after, e.s.dtree.after = c.After, c.After
	return c
}

// todosSent is how many {"type":"todos"} the apps were sent so far.
func todosSent(e *todoEnv) int {
	n := 0
	for _, ev := range strings.Fields(e.f.eventTypes()) {
		if ev == "todos" {
			n++
		}
	}
	return n
}

// todoViewsNow reads GET /api/v1/todos (which an app does after every push) and answers each row's view by id.
func todoViewsNow(e *todoEnv) map[string]assistantwire.TodoSummary {
	e.t.Helper()
	var got struct{ Todos []listedTodo }
	if err := json.Unmarshal(e.must("GET", "/api/v1/todos", "").Body.Bytes(), &got); err != nil {
		e.t.Fatal(err)
	}
	out := map[string]assistantwire.TodoSummary{}
	for _, r := range got.Todos {
		if r.View == nil {
			e.t.Fatalf("%s has no view", r.ID)
		}
		out[r.ID] = *r.View
	}
	return out
}

// asking is run c waiting on the owner since at, as the hub lists it.
func asking(c proto.Session, at time.Time) proto.Session {
	c.Status, c.State, c.PendingInteractions = proto.StatusBlocked, proto.StateNeedsYou, 1
	c.StateSince, c.UpdatedAt, c.Asks = at, at, "Trim the top too?"
	return c
}

// A linked run that comes to ask files its to-do under Needs you: the apps are told once. Its latest line alone, or
// a run no to-do links, tells them nothing; the same lists again neither.
func TestTodoViewPushFollowsLinkedRuns(t *testing.T) {
	todoStores(t, func(t *testing.T, e *todoEnv, _ *db.DB) {
		clock := todoViewClock(e)
		began := time.Now().UTC().Add(-time.Minute)
		run := proto.Session{SessionID: "s1", Title: "Hedge trimming", Status: proto.StatusWorking, State: proto.StateWorking,
			StateSince: began, UpdatedAt: began, Activity: "measuring the hedge"}
		other := proto.Session{SessionID: "s2", Title: "Unrelated", Status: proto.StatusIdle, State: proto.StateIdle,
			StateSince: began, UpdatedAt: began}
		e.sessions("server", run, other)
		id := e.create("Prune the hedge").ID
		e.must("PUT", "/api/v1/todos/"+id+"/agents/server/s1", "")
		clock.advance(time.Minute) // the writes' own pushes go out
		if v := todoViewsNow(e)[id]; v.Section != assistantwire.TodoListSectionWorking {
			t.Fatalf("a working run's to-do: %+v", v)
		}
		settled := func() int {
			clock.advance(todoViewSettle + todosSettle)
			return todosSent(e)
		}
		sent := todosSent(e)

		// The run's latest line and activity move: still Working, nothing is sent.
		step := run
		step.Activity, step.UpdatedAt = "trimming the top", began.Add(30*time.Second)
		e.sessions("server", step, other)
		if n := settled(); n != sent {
			t.Fatalf("a working run's next step sent %d todos events, want none", n-sent)
		}
		if v := todoViewsNow(e)[id]; v.Line == nil || v.Line.Args["text"] != "trimming the top" {
			t.Fatalf("the list does not follow the run's line: %+v", v)
		}

		// A run no to-do links comes to ask: nothing is sent.
		e.sessions("server", step, asking(other, began.Add(35*time.Second)))
		if n := settled(); n != sent {
			t.Fatalf("a run no to-do links sent %d todos events, want none", n-sent)
		}

		// The linked run asks: one push, and the list files it under Needs you.
		asks := asking(step, began.Add(40*time.Second))
		e.sessions("server", asks, asking(other, began.Add(35*time.Second)))
		if n := settled(); n != sent+1 {
			t.Fatalf("the linked run asking sent %d todos events, want 1", n-sent)
		}
		if v := todoViewsNow(e)[id]; v.Section != assistantwire.TodoListSectionNeedsYou || len(v.Asks) != 1 {
			t.Fatalf("after the run asks: %+v", v)
		}

		// The same lists again, with or without a list read in between: nothing more.
		e.sessions("server", asks, asking(other, began.Add(35*time.Second)))
		if n := settled(); n != sent+1 {
			t.Fatalf("the same lists again sent %d more todos events", n-sent-1)
		}
		todoViewsNow(e)
		e.sessions("server", asks, asking(other, began.Add(35*time.Second)))
		if n := settled(); n != sent+1 {
			t.Fatalf("the same lists after a read sent %d more todos events", n-sent-1)
		}
	})
}

// Many runs on two computers come to ask at once: the apps are told once, after the settle.
func TestTodoViewPushOnceForManyRuns(t *testing.T) {
	todoStores(t, func(t *testing.T, e *todoEnv, _ *db.DB) {
		clock := todoViewClock(e)
		began := time.Now().UTC().Add(-time.Minute)
		runs := map[string][]proto.Session{}
		for _, machine := range []string{"server", "laptop"} {
			for i := range 3 {
				runs[machine] = append(runs[machine], proto.Session{SessionID: fmt.Sprintf("%s-%d", machine, i),
					Title: fmt.Sprintf("Run %d", i), Status: proto.StatusWorking, State: proto.StateWorking,
					StateSince: began, UpdatedAt: began})
			}
			e.sessions(machine, runs[machine]...)
		}
		ids := map[string]bool{}
		for machine, list := range runs {
			for _, c := range list {
				id := e.create("Chore for " + c.SessionID).ID
				ids[id] = true
				e.must("PUT", "/api/v1/todos/"+id+"/agents/"+machine+"/"+c.SessionID, "")
			}
		}
		clock.advance(time.Minute)
		todoViewsNow(e)
		sent := todosSent(e)

		for round := range 3 { // each computer's list comes again within the settle
			for machine, list := range runs {
				changed := make([]proto.Session, len(list))
				for i, c := range list {
					changed[i] = asking(c, began.Add(time.Duration(10+round)*time.Second))
				}
				e.sessions(machine, changed...)
			}
		}
		clock.advance(todoViewSettle + todosSettle)
		if n := todosSent(e); n != sent+1 {
			t.Fatalf("six runs asking at once sent %d todos events, want 1", n-sent)
		}
		for id, v := range todoViewsNow(e) {
			if ids[id] && v.Section != assistantwire.TodoListSectionNeedsYou {
				t.Errorf("%s: %+v", id, v)
			}
		}
	})
}

// A closed to-do nobody has seen leaves Just finished 24 hours after it closed: at that moment the clock tells the
// apps, once, though nothing was written.
func TestTodoViewPushWhenJustFinishedEnds(t *testing.T) {
	todoStores(t, func(t *testing.T, e *todoEnv, _ *db.DB) {
		clock := todoViewClock(e)
		id := e.create("Order compost").ID
		e.must("PATCH", "/api/v1/todos/"+id, `{"done":true}`)
		clock.advance(time.Minute)
		v := todoViewsNow(e)[id]
		if v.Section != assistantwire.TodoListSectionJustFinished || v.NextChangeAt.IsZero() {
			t.Fatalf("a closed to-do nobody saw: %+v", v)
		}
		sent := todosSent(e)

		clock.advance(v.NextChangeAt.Sub(clock.Now()) - time.Second)
		if n := todosSent(e); n != sent {
			t.Fatalf("%d todos events before its 24 hours ended, want none", n-sent)
		}
		clock.advance(time.Second + todosSettle)
		if n := todosSent(e); n != sent+1 {
			t.Fatalf("its 24 hours ending sent %d todos events, want 1", n-sent)
		}
		if v := todoViewsNow(e)[id]; v.Section != assistantwire.TodoListSectionDone || !v.NextChangeAt.IsZero() {
			t.Fatalf("after its 24 hours: %+v", v)
		}
		clock.advance(72 * time.Hour)
		if n := todosSent(e); n != sent+1 {
			t.Fatalf("%d more todos events with nothing left to change", n-sent-1)
		}
	})
}

// The clock checks the views at the earliest next_change_at, never sooner than a minute after its last check and
// never sleeping longer than an hour.
func TestTodoViewWakeBounds(t *testing.T) {
	f := newFakeHub().withMachine()
	e := &todoEnv{t: t, s: newStorelessService(t, f), f: f}
	clock := todoViewClock(e)
	b := &e.s.tview
	now := clock.Now()
	arm := func(next map[string]time.Time, woke time.Time) time.Time {
		b.mu.Lock()
		defer b.mu.Unlock()
		b.stopWakeLocked()
		b.next, b.woke = next, woke
		e.s.todoViewArmLocked()
		return b.wakeAt
	}
	soon := now.Add(10 * time.Minute)
	if at := arm(map[string]time.Time{"a": now.Add(3 * time.Hour), "b": soon}, time.Time{}); !at.Equal(soon) {
		t.Errorf("the earliest of two: %v, want %v", at, soon)
	}
	if at := arm(map[string]time.Time{"a": now.Add(24 * time.Hour)}, time.Time{}); !at.Equal(now.Add(todoViewWakeCap)) {
		t.Errorf("a far one: %v, want the cap %v", at, now.Add(todoViewWakeCap))
	}
	if at := arm(map[string]time.Time{"a": now}, now.Add(-10*time.Second)); !at.Equal(now.Add(50 * time.Second)) {
		t.Errorf("one due now, checked 10 s ago: %v, want a minute after that check", at)
	}
	if at := arm(nil, time.Time{}); !at.IsZero() {
		t.Errorf("nothing to change: a check at %v", at)
	}
}

// What a row is filed under follows its section, reason, asks, marks and steps, never its line, since,
// next_change_at or rank.
func TestTodoClassLeavesOutTheLine(t *testing.T) {
	at := time.Date(2026, 4, 1, 9, 0, 0, 0, time.UTC)
	working := func(text string) *assistantwire.TodoLine {
		return &assistantwire.TodoLine{Key: "working", Args: map[string]string{"text": text}}
	}
	base := assistantwire.TodoSummary{Phase: assistantwire.TodoPhaseWorking, Section: assistantwire.TodoListSectionWorking,
		Reason: assistantwire.TodoReasonAgentRunning, Line: working("a"), Since: at, NextChangeAt: at.Add(48 * time.Hour),
		Rank: 2}
	same := base
	same.Line, same.Since, same.NextChangeAt, same.Rank = working("b"), at.Add(time.Minute), at.Add(49*time.Hour), 0
	if todoClassOf(base) != todoClassOf(same) {
		t.Error("the line, since, next_change_at or rank changed what the row is filed under")
	}
	for name, change := range map[string]func(*assistantwire.TodoSummary){
		"section":  func(v *assistantwire.TodoSummary) { v.Section = assistantwire.TodoListSectionWaiting },
		"reason":   func(v *assistantwire.TodoSummary) { v.Reason = assistantwire.TodoReasonBackground },
		"asks":     func(v *assistantwire.TodoSummary) { v.Asks = []assistantwire.OwnerAsk{{ID: "run:server/s1@1"}} },
		"inactive": func(v *assistantwire.TodoSummary) { v.Inactive = at },
		"can_wait": func(v *assistantwire.TodoSummary) { v.CanWait = true },
		"seen":     func(v *assistantwire.TodoSummary) { v.Seen = true },
		"steps":    func(v *assistantwire.TodoSummary) { v.Steps = &assistantwire.TodoStepCount{Done: 1, Total: 2} },
	} {
		v := base
		change(&v)
		if todoClassOf(v) == todoClassOf(base) {
			t.Errorf("%s: the row is filed alike", name)
		}
	}
}
