package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// The old Agent is on another computer and owned by a runtime, whose close
// contract is session.close (session.stop only controls terminal agents).
func TestContinuationRuntimeCrossMachineHandoff(t *testing.T) {
	for _, status := range []string{proto.StatusIdle, proto.StatusWorking, proto.StatusInterrupted, proto.StatusOffline} {
		t.Run(status, func(t *testing.T) {
			e := newThreadEnv(t, nil)
			closeable := true
			e.sessions("old-mac", proto.Session{SessionID: "agent01", Status: status, SourceKind: proto.SourceRuntime,
				Capabilities: &proto.SessionCapabilities{Read: true, Send: true, Close: &closeable}})
			putTodo(e.s, Todo{ID: "todo_x", Title: "Synthetic handoff", Origin: "m1",
				Agents: []TodoAgent{{MachineID: "old-mac", SessionID: "agent01"}}})
			brief, err := e.s.ensureTaskBrief(context.Background(), todoNow(e.s, "todo_x"))
			if err != nil {
				t.Fatal(err)
			}
			var closes atomic.Int32
			var refuse atomic.Bool
			refuse.Store(true)
			e.f.answerNode(deviceSessionStop, func(string, json.RawMessage) (any, error) {
				return nil, errors.New("no_terminal: this session belongs to another tool")
			})
			e.f.answerNode("session.close", func(machine string, raw json.RawMessage) (any, error) {
				if machine != "old-mac" || !strings.Contains(string(raw), "agent01") {
					t.Errorf("wrong close: %s %s", machine, raw)
				}
				closes.Add(1)
				if refuse.Load() {
					return nil, errors.New("runtime refused close")
				}
				return map[string]any{"ok": true}, nil
			})
			{
				result, err := e.s.toolTaskPlan(context.Background(), "m1", continuesArgs("todo_x"))
				if err != nil || !strings.Contains(result, "runtime refused close") {
					t.Fatalf("close refusal: %s %v", result, err)
				}
				if len(e.f.started()) != 0 || !todoNow(e.s, "todo_x").Agents[0].active() {
					t.Fatal("unconfirmed close released or started work")
				}
			}
			refuse.Store(false)
			if _, err := e.s.toolTaskPlan(context.Background(), "m1", continuesArgs("todo_x")); err != nil {
				t.Fatal(err)
			}
			if old := newAgentOn(t, e, "todo_x"); old.ReleasedAt == nil {
				t.Fatal("old Agent not released")
			}
			if todoCount(e.s) != 1 || len(e.f.started()) != 1 {
				t.Fatal("handoff split or duplicated work")
			}
			if kept, ok := e.s.briefOfTodo("todo_x"); !ok || kept.ID != brief.ID {
				t.Fatal("brief changed")
			}
			want := int32(2)
			if closes.Load() != want {
				t.Fatalf("closes = %d, want %d", closes.Load(), want)
			}
			plans, err := e.s.conversationPlans(context.Background(), "m1")
			if err != nil {
				t.Fatal(err)
			}
			for _, plan := range plans {
				if _, err := e.s.startPlan(context.Background(), plan.ID, nil, false); err == nil {
					t.Fatal("repeated Start accepted an already dispatched plan")
				}
			}
			if len(e.f.started()) != 1 || closes.Load() != want {
				t.Fatal("repeated Start repeated the handoff")
			}
		})
	}
}

func TestContinuationCloseRefusalsPreserveOwnership(t *testing.T) {
	for _, name := range []string{"runtime_away", "runtime_terminal", "no_close", "legacy_row", "old_node", "unconfirmed", "unknown", "assistant", "missing"} {
		t.Run(name, func(t *testing.T) {
			e := newThreadEnv(t, nil)
			closeable := name != "no_close" && name != "runtime_away"
			row := proto.Session{SessionID: "agent01", SourceKind: proto.SourceRuntime, Status: proto.StatusWorking,
				Capabilities: &proto.SessionCapabilities{Read: true, Send: true, Close: &closeable}}
			if name == "runtime_away" {
				row.Status = proto.StatusOffline
			}
			if name == "assistant" {
				row.Assistant = true
			}
			if name == "legacy_row" {
				row.Capabilities = nil
			}
			if name == "runtime_terminal" {
				row.Capabilities.Terminal = true
			}
			e.sessions("box", row)
			if name == "missing" {
				e.sessions("box")
			}
			putTodo(e.s, Todo{ID: "todo_x", Title: "Synthetic handoff", Origin: "m1",
				Agents: []TodoAgent{{MachineID: "box", SessionID: "agent01"}}})
			e.f.answerNode(deviceSessionStop, func(string, json.RawMessage) (any, error) {
				return nil, errors.New("no_terminal")
			})
			var closes atomic.Int32
			e.f.answerNode("session.close", func(string, json.RawMessage) (any, error) {
				closes.Add(1)
				if name == "unconfirmed" {
					return map[string]any{}, nil
				}
				if name == "old_node" {
					return nil, &rpc.Error{Code: "method_not_found", Message: "unknown method session.close"}
				}
				if name == "unknown" {
					return nil, &rpc.Error{Code: "unknown", Message: "close outcome is unknown"}
				}
				return nil, errors.New(name)
			})
			result, err := e.s.toolTaskPlan(context.Background(), "m1", continuesArgs("todo_x"))
			if err != nil || !strings.Contains(result, "dispatch failed") {
				t.Fatalf("unconfirmed handoff: %s %v", result, err)
			}
			if len(e.f.started()) != 0 || !todoNow(e.s, "todo_x").Agents[0].active() {
				t.Fatal("unconfirmed handoff changed ownership")
			}
			if (name == "assistant" || name == "runtime_terminal" || !closeable || name == "legacy_row" || name == "missing") && closes.Load() != 0 {
				t.Fatal("closed a session without authority or support")
			}
			if name == "runtime_away" {
				if _, err := e.s.reusableTodo(context.Background(), "m1", "todo_x", false); err == nil {
					t.Fatal("away runtime reused from cached offline row")
				}
			}
		})
	}
}

func TestContinuationTerminalStopReceipts(t *testing.T) {
	for _, name := range []string{"stopped", "already_stopped", "unconfirmed"} {
		t.Run(name, func(t *testing.T) {
			e := newThreadEnv(t, nil)
			putTodo(e.s, Todo{ID: "todo_x", Title: "Synthetic handoff", Origin: "m1",
				Agents: []TodoAgent{{MachineID: "box", SessionID: "agent01"}}})
			closeable := true
			e.sessions("box", proto.Session{SessionID: "agent01", Status: proto.StatusWorking,
				Capabilities: &proto.SessionCapabilities{Terminal: true, Close: &closeable}})
			e.f.answerNode("session.close", func(string, json.RawMessage) (any, error) {
				t.Error("a task handoff must keep a terminal agent's shell")
				return nil, errors.New("terminal close was not authorized")
			})
			e.f.answerNode(deviceSessionStop, func(string, json.RawMessage) (any, error) {
				if name == "unconfirmed" {
					return map[string]any{}, nil
				}
				return map[string]any{"stopped": name == "stopped"}, nil
			})
			result, err := e.s.toolTaskPlan(context.Background(), "m1", continuesArgs("todo_x"))
			if err != nil {
				t.Fatal(err)
			}
			if name == "unconfirmed" {
				if !strings.Contains(result, "did not confirm") || len(e.f.started()) != 0 || !todoNow(e.s, "todo_x").Agents[0].active() {
					t.Fatalf("missing receipt accepted: %s", result)
				}
			} else if old := newAgentOn(t, e, "todo_x"); old.ReleasedAt == nil {
				t.Fatal("confirmed stop did not release the old Agent")
			}
		})
	}
}

func TestContinuationRechecksOwnershipAfterLookup(t *testing.T) {
	e := newThreadEnv(t, nil)
	putTodo(e.s, Todo{ID: "todo_old", Title: "Original task", Origin: "m1",
		Agents: []TodoAgent{{MachineID: "box", SessionID: "agent01"}}})
	putTodo(e.s, Todo{ID: "todo_other", Title: "Other task", Origin: "m1"})
	e.f.onDevicesList = func() {
		if err := e.s.linkTodoAgent(context.Background(), "todo_other", "box", "agent01", "", "", ""); err != nil {
			t.Error(err)
		}
	}
	_, err := e.s.stopContinuedTaskAgents(context.Background(), todoNow(e.s, "todo_old"))
	if err == nil || len(e.f.stopped) != 0 || !todoNow(e.s, "todo_other").Agents[0].active() {
		t.Fatalf("closed a reassigned Agent: %v", err)
	}
}

func TestContinuationReservesMembershipDuringClose(t *testing.T) {
	for _, backend := range []string{"memory", "database"} {
		t.Run(backend, func(t *testing.T) {
			var store Store
			if backend == "database" {
				store = taskRecoveryDatabase(t)
			}
			e := newThreadEnv(t, store)
			ctx := context.Background()
			old, err := e.s.createTaskTodo(ctx, "Original task", "Synthetic", "m1", "", "")
			if err != nil {
				t.Fatal(err)
			}
			other, err := e.s.createTaskTodo(ctx, "Other task", "Synthetic", "m1", "", "")
			if err != nil {
				t.Fatal(err)
			}
			if err := e.s.linkTodoAgent(ctx, old.ID, "box", "agent01", "", "", ""); err != nil {
				t.Fatal(err)
			}
			closeable := true
			e.sessions("box", proto.Session{SessionID: "agent01", SourceKind: proto.SourceRuntime, Status: proto.StatusWorking,
				Capabilities: &proto.SessionCapabilities{Close: &closeable}})
			e.f.answerNode("session.close", func(string, json.RawMessage) (any, error) {
				e.s.todos.writing.Lock()
				err := e.s.writeTodoAgentLink(ctx, other.ID, "box", "agent01", "", "", "", "", "manual", "", false)
				e.s.todos.writing.Unlock()
				if err == nil {
					t.Error("reassigned an Agent while its close was in flight")
				}
				return map[string]any{"ok": true}, nil
			})
			if _, err := e.s.toolTaskPlan(ctx, "m1", continuesArgs(old.ID)); err != nil {
				t.Fatal(err)
			}
			if a := newAgentOn(t, e, old.ID); a.ReleasedAt == nil {
				t.Fatal("handoff did not finish")
			}
			if len(todoNow(e.s, other.ID).Agents) != 0 {
				t.Fatal("other task gained a closing Agent")
			}
			if err := e.s.linkTodoAgent(ctx, other.ID, "box", "agent01", "", "", ""); err != nil {
				t.Fatalf("handoff reservation leaked: %v", err)
			}
		})
	}
}

func TestContinuationReservesClosedTaskAgentAgainstAutoLink(t *testing.T) {
	e := newThreadEnv(t, nil)
	done := time.Now()
	putTodo(e.s, Todo{ID: "todo_old", Title: "Original task", Origin: "m1", DoneAt: &done,
		Agents: []TodoAgent{{MachineID: "box", SessionID: "agent01"}}})
	putTodo(e.s, Todo{ID: "todo_other", Title: "Other task", Origin: "m1"})
	closeable := true
	row := proto.Session{SessionID: "agent01", SourceKind: proto.SourceRuntime, Status: proto.StatusWorking,
		Capabilities: &proto.SessionCapabilities{Close: &closeable}}
	e.sessions("box", row)
	var closes atomic.Int32
	e.f.answerNode("session.close", func(string, json.RawMessage) (any, error) {
		closes.Add(1)
		spawned := row
		spawned.SpawnFrom = "todo_other@box/parent"
		if e.s.autoLinkTodos("box", []proto.Session{spawned}) {
			t.Error("auto-linked an Agent during its handoff close")
		}
		return map[string]any{"ok": true}, nil
	})
	if _, err := e.s.toolTaskPlan(context.Background(), "m1", continuesArgs("todo_old")); err != nil {
		t.Fatal(err)
	}
	if a := newAgentOn(t, e, "todo_old"); a.ReleasedAt == nil {
		t.Fatal("old Agent not released")
	}
	if closes.Load() != 1 || len(todoNow(e.s, "todo_other").Agents) != 0 {
		t.Fatal("auto-link stole a closing Agent")
	}
}

func TestContinuationUnreachableComputerKeepsOfflineLink(t *testing.T) {
	e := newThreadEnv(t, nil)
	e.stoppedAgent01()
	e.f.down = map[string]bool{"box": true}
	putTodo(e.s, Todo{ID: "todo_x", Title: "Synthetic handoff", Origin: "m1",
		Agents: []TodoAgent{{MachineID: "box", SessionID: "agent01"}}})
	_, err := e.s.stopContinuedTaskAgents(context.Background(), todoNow(e.s, "todo_x"))
	if err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("unreachable machine accepted as stopped: %v", err)
	}
	if !todoNow(e.s, "todo_x").Agents[0].active() || len(e.f.started()) != 0 {
		t.Fatal("unreachable handoff changed task ownership")
	}
	if _, err := e.s.reusableTodo(context.Background(), "m1", "todo_x", false); err == nil {
		t.Fatal("unreachable cached row permitted todo_id reuse")
	}
}

func TestReusableTodoDoesNotReleaseLiveHiddenOrInterruptedRuntime(t *testing.T) {
	for _, archived := range []bool{false, true} {
		e := newThreadEnv(t, nil)
		e.sessions("box", proto.Session{SessionID: "agent01", Status: proto.StatusInterrupted,
			SourceKind: proto.SourceRuntime, Archived: archived, Capabilities: &proto.SessionCapabilities{Send: true, Read: true}})
		putTodo(e.s, Todo{ID: "todo_x", Title: "Synthetic handoff", Origin: "m1",
			Agents: []TodoAgent{{MachineID: "box", SessionID: "agent01"}}})
		if _, err := e.s.reusableTodo(context.Background(), "m1", "todo_x", false); err == nil {
			t.Fatalf("live runtime accepted without a handoff, archived=%v", archived)
		}
	}
}
