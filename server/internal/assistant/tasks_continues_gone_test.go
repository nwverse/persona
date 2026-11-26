package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// Regression for the orphaned Antigravity worker stuck continues: after its companion restarts, an
// agy- row vanishes from the machine's session list while the to-do still links it. A continues
// takeover called session.stop, the node answered "invalid session id", and that hard error kept
// the stale link forever, so every retry failed the same way.

// goneAgySessionID mirrors the real incident id (agy-64c84f4d): a short pane/worker-derived row id,
// not the companion's agy-<conversation-uuid> form.
const goneAgySessionID = "agy-64c84f4d"

func armStopAnswer(e *todoEnv, err error) {
	e.t.Helper()
	e.f.answerNode(deviceSessionStop, func(string, json.RawMessage) (any, error) {
		return map[string]any{"stopped": false}, err
	})
}

// stopCallIDs reads every session.stop target from the fake's recorded calls (custom node answers do
// not populate f.stopped).
func stopCallIDs(e *todoEnv) []string {
	e.t.Helper()
	e.f.mu.Lock()
	defer e.f.mu.Unlock()
	var ids []string
	for _, c := range e.f.calls {
		if c.Method != deviceSessionStop {
			continue
		}
		var p struct {
			SessionID string `json:"session_id"`
		}
		_ = json.Unmarshal(c.Params, &p)
		ids = append(ids, p.SessionID)
	}
	return ids
}

func assertOneStopFor(t *testing.T, e *todoEnv, wantID string) {
	t.Helper()
	ids := stopCallIDs(e)
	if len(ids) != 1 || ids[0] != wantID {
		t.Fatalf("session.stop targets = %v, want exactly [%s]", ids, wantID)
	}
}

// A session the computer authoritatively does not have any more is let go idempotently, both for a
// current typed not_found answer and the legacy "invalid session id" text older nodes send.
func TestStopContinuedTaskAgentsReleasesAGoneSession(t *testing.T) {
	for _, tc := range []struct {
		name    string
		stopErr error
	}{
		{"legacy invalid session id text", &rpc.Error{Message: fmt.Sprintf("invalid session id %q", goneAgySessionID)}},
		{"typed not_found code", &rpc.Error{Code: codeSessionNotFound, Message: "session is not on this computer"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTodoEnv(t, nil) // its only machine "server" lists no sessions
			armStopAnswer(e, tc.stopErr)
			todo := Todo{ID: "todo_gone", Agents: []TodoAgent{{MachineID: "server", SessionID: goneAgySessionID}}}

			stopped, err := e.s.stopContinuedTaskAgents(context.Background(), todo)
			if err != nil {
				t.Fatalf("an authoritative gone answer must be tolerated as idempotent stop, got %v", err)
			}
			if len(stopped) != 1 {
				t.Fatalf("stopped agents = %d, want the one stale link", len(stopped))
			}
			assertOneStopFor(t, e, goneAgySessionID)
		})
	}
}

// Uncertain or unrelated stop answers stay hard errors: a computer that cannot be reached is not an
// answer, and another session's invalid-id text says nothing about this link.
func TestStopContinuedTaskAgentsKeepsUncertainStopsHard(t *testing.T) {
	for _, tc := range []struct {
		name    string
		stopErr error
		want    string
	}{
		{"plain node unreachable", errors.New("server: node_unreachable: dial tcp: i/o timeout"), "cannot stop agent"},
		{"context deadline", context.DeadlineExceeded, "cannot stop agent"},
		{"another rpc refusal code", &rpc.Error{Code: "not_retirable", Message: "still starting"}, "cannot stop agent"},
		{"invalid id of another session", &rpc.Error{Message: `invalid session id "agy-some-other-row"`}, "cannot stop agent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTodoEnv(t, nil)
			armStopAnswer(e, tc.stopErr)
			todo := Todo{ID: "todo_uncertain", Agents: []TodoAgent{{MachineID: "server", SessionID: goneAgySessionID}}}

			stopped, err := e.s.stopContinuedTaskAgents(context.Background(), todo)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one containing %q", err, tc.want)
			}
			if len(stopped) != 0 {
				t.Fatalf("stopped agents = %d, want none released on an uncertain stop", len(stopped))
			}
			assertOneStopFor(t, e, goneAgySessionID)
		})
	}
}

// A session the fresh list still shows as live must never be released on a contradictory stop
// answer: fail closed, and a retry reconciles once the session really is gone.
func TestStopContinuedTaskAgentsDoesNotForgiveAnErrorWhileListedLive(t *testing.T) {
	const liveID = "agy-live-row-id"
	e := newTodoEnv(t, nil)
	e.sessions("server", proto.Session{SessionID: liveID, Status: proto.StatusWorking})
	armStopAnswer(e, &rpc.Error{Message: fmt.Sprintf("invalid session id %q", liveID)})
	todo := Todo{ID: "todo_listed", Agents: []TodoAgent{{MachineID: "server", SessionID: liveID}}}

	stopped, err := e.s.stopContinuedTaskAgents(context.Background(), todo)
	if err == nil {
		t.Fatal("a stop error for a session the list shows live must stay a hard error")
	}
	if len(stopped) != 0 {
		t.Fatalf("stopped agents = %d, want none", len(stopped))
	}
}

// A task can keep an old pane-derived agy id that the node later lists only as an alias of the live
// row (the engine gave the conversation another pane). For this short id form the legacy invalid-id
// text is otherwise trusted, so alias matching is the one thing that keeps a live conversation fail
// closed here. (The companion agy-<uuid> form is additionally guarded by
// stopErrSaysSessionGone's form check.)
func TestStopContinuedTaskAgentsDoesNotForgiveALiveAliasRow(t *testing.T) {
	const oldPaneID = "agy-oldpane01"
	const newPaneID = "agy-newpane02"
	e := newTodoEnv(t, nil)
	live := proto.Session{SessionID: newPaneID, Status: proto.StatusWorking}
	live.SetAliases([]string{oldPaneID})
	e.sessions("server", live)
	armStopAnswer(e, &rpc.Error{Message: fmt.Sprintf("invalid session id %q", oldPaneID)})
	todo := Todo{ID: "todo_alias_live", Agents: []TodoAgent{{MachineID: "server", SessionID: oldPaneID}}}

	stopped, err := e.s.stopContinuedTaskAgents(context.Background(), todo)
	if err == nil {
		t.Fatal("a live row aliasing the linked id must keep the takeover fail-closed")
	}
	if len(stopped) != 0 {
		t.Fatalf("stopped agents = %d, want none while an alias row is live", len(stopped))
	}
	assertOneStopFor(t, e, oldPaneID)
}

// Two panes reporting one Antigravity conversation make the node leave the companion id out of both
// rows' aliases, so the list cannot connect the live pane rows to the stored agy-<conversation-uuid>.
// The node's plain invalid-id answer for that companion id does not certify the conversation is gone;
// the hub stays fail-closed on the legacy text (a typed not_found remains authoritative).
func TestStopContinuedTaskAgentsDoesNotForgiveCompanionUUIDLegacyText(t *testing.T) {
	const companionID = "agy-33333333-4444-5555-6666-777777777777"
	t.Run("no row at all", func(t *testing.T) {
		e := newTodoEnv(t, nil)
		armStopAnswer(e, &rpc.Error{Message: fmt.Sprintf("invalid session id %q", companionID)})
		todo := Todo{ID: "todo_companion_uuid", Agents: []TodoAgent{{MachineID: "server", SessionID: companionID}}}

		stopped, err := e.s.stopContinuedTaskAgents(context.Background(), todo)
		if err == nil {
			t.Fatal("legacy invalid-id text must not release a companion-uuid link: its live pane can be alias-ambiguous")
		}
		if len(stopped) != 0 {
			t.Fatalf("stopped agents = %d, want none", len(stopped))
		}
	})
	t.Run("two live panes without the alias", func(t *testing.T) {
		e := newTodoEnv(t, nil)
		e.sessions("server",
			proto.Session{SessionID: "agy-w1-p1", Status: proto.StatusWorking},
			proto.Session{SessionID: "agy-w2-p2", Status: proto.StatusWorking},
		)
		armStopAnswer(e, &rpc.Error{Message: fmt.Sprintf("invalid session id %q", companionID)})
		todo := Todo{ID: "todo_double_pane", Agents: []TodoAgent{{MachineID: "server", SessionID: companionID}}}

		stopped, err := e.s.stopContinuedTaskAgents(context.Background(), todo)
		if err == nil {
			t.Fatal("two live unaliased panes plus legacy invalid-id must stay fail-closed")
		}
		if len(stopped) != 0 {
			t.Fatalf("stopped agents = %d, want none", len(stopped))
		}
	})
	t.Run("typed not_found is still accepted", func(t *testing.T) {
		e := newTodoEnv(t, nil)
		armStopAnswer(e, &rpc.Error{Code: codeSessionNotFound, Message: "no such session"})
		todo := Todo{ID: "todo_companion_typed", Agents: []TodoAgent{{MachineID: "server", SessionID: companionID}}}

		stopped, err := e.s.stopContinuedTaskAgents(context.Background(), todo)
		if err != nil || len(stopped) != 1 {
			t.Fatalf("typed not_found should release the companion link: stopped=%d err=%v", len(stopped), err)
		}
	})
}

// When a stopped alias row sorts before the live exact row for one id, presence must still report the
// agent as not stopped: any live matching row wins.
func TestTodoAgentStopPresenceLiveRowWinsOverStoppedAlias(t *testing.T) {
	const id = "agy-deadbeef"
	e := newTodoEnv(t, nil)
	stoppedAlias := proto.Session{SessionID: "agy-offline-fork", Status: proto.StatusOffline}
	stoppedAlias.SetAliases([]string{id})
	liveExact := proto.Session{SessionID: id, Status: proto.StatusWorking}
	e.sessions("server", stoppedAlias, liveExact)
	a := TodoAgent{MachineID: "server", SessionID: id}

	stopped, listed := e.s.todoAgentStopPresence(context.Background(), a)
	if !listed {
		t.Fatal("listed = false, want true")
	}
	if stopped {
		t.Fatal("stopped = true with a live exact row, want false (any live match wins)")
	}
}

// End to end through takeTodoForTask with continues: the orphaned link is released, the takeover
// succeeds, and a following todo_id reuse is admitted instead of being stuck forever.
func TestContinuesTakeClearsTheOrphanedAgyLink(t *testing.T) {
	e := newTodoEnv(t, nil)
	armStopAnswer(e, &rpc.Error{Message: fmt.Sprintf("invalid session id %q", goneAgySessionID)})
	todo := Todo{
		ID:     "todo_orphan_take",
		Title:  "rework",
		Origin: "m1",
		Agents: []TodoAgent{{MachineID: "server", SessionID: goneAgySessionID}},
	}
	putTodo(e.s, todo)

	if _, err := e.s.takeTodoForTask(context.Background(), "plan_next", "m1",
		PlanTask{ReuseTodo: todo.ID, Continues: todo.ID}); err != nil {
		t.Fatalf("continues takeover must clear the gone link and proceed, got: %v", err)
	}
	var released bool
	for _, a := range todoNow(e.s, todo.ID).Agents {
		if a.SessionID == goneAgySessionID {
			released = a.ReleasedAt != nil
		}
	}
	if !released {
		t.Fatal("the orphaned agent link was not released")
	}
	assertOneStopFor(t, e, goneAgySessionID)
	e.s.releaseTakenTodo(todo.ID)

	stopped, err := e.s.reusableTodo(context.Background(), "m1", todo.ID, false)
	if err != nil {
		t.Fatalf("the to-do should be reusable after the stale link is released, got %v", err)
	}
	if len(stopped) != 0 {
		t.Fatalf("reusableTodo reports %d stopped agents, want 0: %+v", len(stopped), stopped)
	}
}

// A genuinely live listed agent is still stopped exactly once and then released: the fix only
// forgives authoritative gone answers, it does not skip stopping.
func TestContinuesTakeStopsALiveAgent(t *testing.T) {
	const liveID = "agent-live-hex-id"
	e := newTodoEnv(t, nil)
	e.sessions("server", proto.Session{SessionID: liveID, Status: proto.StatusWorking})
	todo := Todo{
		ID:     "todo_live_take",
		Title:  "rework",
		Origin: "m1",
		Agents: []TodoAgent{{MachineID: "server", SessionID: liveID}},
	}
	putTodo(e.s, todo)

	if _, err := e.s.takeTodoForTask(context.Background(), "plan_next", "m1",
		PlanTask{ReuseTodo: todo.ID, Continues: todo.ID}); err != nil {
		t.Fatalf("a live agent's continues takeover should stop it and proceed, got: %v", err)
	}
	assertOneStopFor(t, e, liveID)
	for _, a := range todoNow(e.s, todo.ID).Agents {
		if a.SessionID == liveID && a.ReleasedAt == nil {
			t.Fatal("the stopped live agent link was not released")
		}
	}
	e.s.releaseTakenTodo(todo.ID)
}

// The real task_plan(continues=...) entry: an orphaned agy link whose stop answers the legacy
// invalid-session-id error is released and the replacement Agent starts on the same to-do, instead
// of the plan failing forever.
func TestTaskPlanContinuesRestartsAfterOrphanedAgySession(t *testing.T) {
	e := newThreadEnv(t, nil) // box lists agent01, not the orphaned agy id
	armStopAnswer(e.todoEnv, &rpc.Error{Message: fmt.Sprintf("invalid session id %q", goneAgySessionID)})
	todo := Todo{
		ID:     "todo_orphan_plan",
		Title:  "rework",
		Origin: "m1",
		Agents: []TodoAgent{{MachineID: "box", SessionID: goneAgySessionID}},
	}
	putTodo(e.s, todo)

	if _, err := e.s.toolTaskPlan(context.Background(), "m1", continuesArgs(todo.ID)); err != nil {
		t.Fatalf("task_plan continues must not fail on the orphaned session, got: %v", err)
	}
	waitFor(t, "the replacement agent to link", func() bool {
		for _, a := range todoNow(e.s, todo.ID).Agents {
			if a.SessionID != goneAgySessionID && a.active() {
				return true
			}
		}
		return false
	})
	var orphanReleased bool
	for _, a := range todoNow(e.s, todo.ID).Agents {
		if a.SessionID == goneAgySessionID {
			orphanReleased = a.ReleasedAt != nil
		}
	}
	if !orphanReleased {
		t.Fatal("the orphaned agy link was not released through task_plan continues")
	}
	var sawStop bool
	for _, id := range stopCallIDs(e.todoEnv) {
		if id == goneAgySessionID {
			sawStop = true
		}
	}
	if !sawStop {
		t.Fatalf("session.stop was not attempted for %s; targets %v", goneAgySessionID, stopCallIDs(e.todoEnv))
	}
	if todoCount(e.s) != 1 {
		t.Fatalf("continuation made %d todos, want the same one", todoCount(e.s))
	}
}
