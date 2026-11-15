package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// A task that runs on an existing to-do through task_plan's todo_id keeps that to-do's plan id, which a piece of a
// leader's direction (todo_create) does not have: only the plan task's todo_id binds it to the plan. When its agent does
// not start, task_retry, the HTTP retry and the start remedies find that plan as they find a continuation's (#8770); a
// to-do no started plan binds, or no conversation opened, is still not started again. Ids are placeholders.

// failedDirectionTodoStart is a piece of leader c1x1x1's direction that the leader's read-only plan ran an agent on
// through todo_id, and that plan, after the start failed with failure: a refusal that kept its tab leaves the start
// being remedied, any other fails the to-do.
func failedDirectionTodoStart(t *testing.T, e *threadEnv, failure error) (Todo, *TaskPlan) {
	t.Helper()
	ctx := context.Background()
	setSettings(t, e.s, func(v *Settings) { v.Leaders = []string{"c1x1x1"} })
	if _, err := runJSON(t, e.s, "c1x1x1", "todo_create", map[string]any{"title": "Tablet layout"}); err != nil {
		t.Fatal(err)
	}
	piece := mustTodo(t, e.s, "Tablet layout")
	if piece.PlanID != "" || piece.Origin != "c1x1x1" {
		t.Fatalf("not a piece of the leader's direction: %+v", piece)
	}
	e.f.mu.Lock()
	e.f.failStart = failure
	e.f.mu.Unlock()
	if _, err := e.s.toolTaskPlan(ctx, "c1x1x1", reuseArgs(true, piece.ID)); err != nil {
		t.Fatal(err)
	}
	plan := planOf(t, e.s, "c1x1x1")
	waitFor(t, "the failed start's bookkeeping", func() bool {
		p, err := e.s.loadPlan(ctx, plan.ID)
		return err == nil && p.Tasks[0].TodoID == piece.ID && p.Tasks[0].Error != "" && e.s.taskStarting.Load() == 0
	})
	e.f.mu.Lock()
	e.f.failStart = nil
	e.f.mu.Unlock()
	if cur := todoNow(e.s, piece.ID); cur.PlanID != "" {
		t.Fatalf("the fixture gave the to-do a plan id: %+v", cur)
	}
	return todoNow(e.s, piece.ID), plan
}

func TestDirectionTodoFailedStartRetriesCurrentAssignment(t *testing.T) {
	// The node never answers session.start: the start reconciles with the machine before it fails (#6540), in
	// milliseconds here.
	shortTaskStartReconcile(t)
	for _, backend := range []string{"memory", "database"} {
		for _, entry := range []string{"http", "mcp"} {
			t.Run(backend+"/"+entry, func(t *testing.T) {
				var store Store
				if backend == "database" {
					store = taskRecoveryDatabase(t)
				}
				e := newThreadEnv(t, store)
				ctx := context.Background()
				piece, plan := failedDirectionTodoStart(t, e, context.DeadlineExceeded)
				if piece.DoneAt == nil || piece.StartError == "" {
					t.Fatalf("the start did not fail: %+v", piece)
				}
				if _, p, i, err := e.s.remedyOf(ctx, piece.ID); err != nil || p.ID != plan.ID || i != 0 {
					t.Errorf("remedyOf: %+v, %d, %v; want task 0 of plan %s", p, i, err, plan.ID)
				}
				if entry == "http" {
					if w := e.do(http.MethodPost, "/api/v1/todos/"+piece.ID+"/retry", ""); w.Code != http.StatusOK {
						t.Fatalf("HTTP retry: %d %s", w.Code, w.Body.String())
					}
				} else {
					args, _ := json.Marshal(map[string]string{"todo_id": piece.ID, "reason": "The computer did not answer; start it again."})
					if _, err := e.s.runTool(ctx, "server", "m1", "task_retry", json.RawMessage(args)); err != nil {
						t.Fatalf("task_retry: %v", err)
					}
				}
				waitFor(t, "the retried agent on the same to-do", func() bool {
					cur := todoNow(e.s, piece.ID)
					return len(e.f.started()) == 2 && cur.DoneAt == nil && len(cur.Agents) == 1
				})
				starts := e.f.started()
				if starts[1]["cwd"] != starts[0]["cwd"] || starts[1]["permission_mode"] != starts[0]["permission_mode"] {
					t.Fatalf("the retry did not start the plan's task as it was: %+v", starts)
				}
				if cur := todoNow(e.s, piece.ID); cur.PlanID != "" || cur.ReopenedAt == nil || cur.StartError != "" || todoCount(e.s) != 1 {
					t.Fatalf("the to-do after the retry: %+v; %d to-dos", cur, todoCount(e.s))
				}
				if entry == "mcp" {
					waitFor(t, "the retry recorded on the plan's task", func() bool {
						p, err := e.s.loadPlan(ctx, plan.ID)
						return err == nil && p.Tasks[0].Start != nil && p.Tasks[0].Start.State == remedyStarted && p.Tasks[0].Start.Attempt == 1
					})
				}
			})
		}
	}
}

func TestDirectionTodoWithoutAssignmentStaysNotRetryable(t *testing.T) {
	shortTaskStartReconcile(t)
	for _, backend := range []string{"memory", "database"} {
		t.Run(backend, func(t *testing.T) {
			var store Store
			if backend == "database" {
				store = taskRecoveryDatabase(t)
			}
			e := newThreadEnv(t, store)
			ctx := context.Background()
			retryTool := func(id string) error {
				args, _ := json.Marshal(map[string]string{"todo_id": id, "reason": "Start it again."})
				_, err := e.s.runTool(ctx, "server", "m1", "task_retry", json.RawMessage(args))
				return err
			}
			piece, plan := failedDirectionTodoStart(t, e, context.DeadlineExceeded)
			// No started plan binds it now (a later plan retires an earlier one's binding so): the plan still names it
			// as the to-do it was made for, which says nothing of what runs it now.
			if err := e.s.patchPlanTask(ctx, plan.ID, 0, func(task *PlanTask) { task.TodoID = "" }); err != nil {
				t.Fatal(err)
			}
			if err := retryTool(piece.ID); !errors.Is(err, errTaskNotInPlan) {
				t.Fatalf("task_retry with no binding: %v", err)
			}
			if w := e.do(http.MethodPost, "/api/v1/todos/"+piece.ID+"/retry", ""); w.Code != http.StatusConflict {
				t.Fatalf("HTTP retry with no binding: %d %s", w.Code, w.Body.String())
			}
			if cur := todoNow(e.s, piece.ID); cur.DoneAt == nil || cur.StartError == "" {
				t.Fatalf("a refused retry reopened the to-do: %+v", cur)
			}

			// A to-do no conversation opened (the user's own) is nobody's task to start again, even with a binding.
			if _, err := runJSON(t, e.s, "m1", "todo_create", map[string]any{"title": "Buy milk"}); err != nil {
				t.Fatal(err)
			}
			own := mustTodo(t, e.s, "Buy milk")
			bound := &TaskPlan{ID: "plan_bound", Conversation: "m1", State: planStarted, CreatedAt: time.Now(), UpdatedAt: time.Now(),
				Tasks: []PlanTask{{Title: own.Title, Prompt: own.Title, Machine: "box", Cwd: "/code/app", TodoID: own.ID}}}
			if err := e.s.savePlan(ctx, bound); err != nil {
				t.Fatal(err)
			}
			if err := e.s.failTodo(ctx, own.ID, "the computer did not answer"); err != nil {
				t.Fatal(err)
			}
			if err := retryTool(own.ID); !errors.Is(err, errTaskNotRetryable) {
				t.Fatalf("task_retry of a to-do nobody opened: %v", err)
			}
			if w := e.do(http.MethodPost, "/api/v1/todos/"+own.ID+"/retry", ""); w.Code != http.StatusConflict {
				t.Fatalf("HTTP retry of a to-do nobody opened: %d %s", w.Code, w.Body.String())
			}
			if n := len(e.f.started()); n != 1 {
				t.Fatalf("a refused retry reached the node: %d starts", n)
			}
		})
	}
}

// A piece of a direction whose agent's new tab stopped on a question is remedied, and the supervisor's sweep gives
// the start up once the computer no longer keeps the tab, as it does any task's.
func TestDirectionTodoRemedyExpiresWithItsTab(t *testing.T) {
	for _, backend := range []string{"memory", "database"} {
		t.Run(backend, func(t *testing.T) {
			var store Store
			if backend == "database" {
				store = taskRecoveryDatabase(t)
			}
			e := newThreadEnv(t, store)
			ctx := context.Background()
			piece, plan := failedDirectionTodoStart(t, e, keptRefusal(proto.CodeShellWaiting))
			if piece.DoneAt != nil {
				t.Fatalf("failed at once, not remedied: %+v", piece)
			}
			if _, p, i, err := e.s.remedyOf(ctx, piece.ID); err != nil || p.ID != plan.ID || p.Tasks[i].Start == nil || p.Tasks[i].Start.State != remedying {
				t.Errorf("remedyOf: %+v, %d, %v; want the remedy on plan %s", p, i, err, plan.ID)
			}
			if err := e.s.patchPlanTask(ctx, plan.ID, 0, func(task *PlanTask) { task.Start.Until = time.Now().Add(-time.Second) }); err != nil {
				t.Fatal(err)
			}
			e.s.remedyWatch(ctx, todoNow(e.s, piece.ID), time.Now())
			if cur := todoNow(e.s, piece.ID); cur.DoneAt == nil || cur.StartError == "" {
				t.Fatalf("the remedy outlived its tab: %+v", cur)
			}
			if p, err := e.s.loadPlan(ctx, plan.ID); err != nil || p.Tasks[0].Start == nil || p.Tasks[0].Start.State != remedyFailed || p.Tasks[0].Start.By != "hub" {
				t.Fatalf("the plan's task after its tab: %+v, %v", p, err)
			}
		})
	}
}
