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
)

func continuationAppSession() proto.Session {
	return proto.Session{SessionID: "agent01", Title: "Original work", Cwd: "/code/app", Agent: "codex",
		SourceKind: "codex_app_server", SourceInstanceID: "app-instance", Status: proto.StatusDone,
		Capabilities: &proto.SessionCapabilities{Read: true, Send: true}}
}

func failedAppContinuation(t *testing.T, store Store) (*threadEnv, *TaskPlan, string) {
	t.Helper()
	e := newThreadEnv(t, store)
	e.sessions("box", continuationAppSession())
	e.f.retireErr = errors.New("this session belongs to another tool")
	todo, err := e.s.createTaskTodo(t.Context(), "Original work", "Original scope", "m1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	e.must("PUT", "/api/v1/todos/"+todo.ID+"/agents/box/agent01", "")
	args, _ := json.Marshal(map[string]any{"tasks": []map[string]any{{"title": "Continue original work", "prompt": "Check the remaining examples",
		"machine": "box", "cwd": "/code/app", "launcher": "codex", "workspace": "none", "role": "worker",
		"continues": todo.ID, "continues_why": "Use the original context"}}})
	if _, err := e.s.toolTaskPlan(t.Context(), "m1", args); err != nil {
		t.Fatal(err)
	}
	p := planOf(t, e.s, "m1")
	if p.State == planOpen {
		p, err = e.s.startPlan(t.Context(), p.ID, nil, false)
		if err != nil {
			t.Fatal(err)
		}
	}
	pt := p.Tasks[0]
	if !strings.Contains(pt.Error, "this session belongs to another tool") || pt.TodoID != "" || pt.ContinuationRecovery == nil {
		t.Fatalf("missing failed-attempt evidence and recovery action: %+v", pt)
	}
	if todoCount(e.s) != 1 || len(e.f.starts) != 0 || todoNow(e.s, todo.ID).Agents[0].ReleasedAt != nil {
		t.Fatal("failed attempt created a replacement or released the original Agent")
	}
	return e, p, todo.ID
}

func continuationSendArgs(p *TaskPlan) map[string]any {
	return map[string]any{"machine": "box", "session_id": "agent01", "text": "Check the remaining examples",
		"resume": true, "resume_why": "Use its original context", "continuation": planContinuationRequest{PlanID: p.ID, TaskIndex: 0}}
}

func TestPlanContinuationRecoveryDelivery(t *testing.T) {
	for _, backend := range []string{"memory", "database"} {
		for _, queued := range []bool{false, true} {
			t.Run(backend+map[bool]string{false: "/sent", true: "/queued"}[queued], func(t *testing.T) {
				var store Store
				if backend == "database" {
					store = taskRecoveryDatabase(t)
				}
				e, p, todoID := failedAppContinuation(t, store)
				action := continuationAction(p.ID, 0, p.Tasks[0])
				if !strings.Contains(action, p.ID) || !strings.Contains(action, `"task_index":0`) || !strings.Contains(action, `"agent_send"`) {
					t.Fatalf("no precise supported action: %s", action)
				}
				var input string
				e.f.answerNode(DeviceSessionSend, func(_ string, params json.RawMessage) (any, error) {
					var a map[string]any
					_ = json.Unmarshal(params, &a)
					input, _ = a["client_msg_id"].(string)
					return agentSendReply{OK: true, Queued: queued}, nil
				})
				if _, err := runJSON(t, e.s, "m1", "agent_send", continuationSendArgs(p)); err != nil {
					t.Fatal(err)
				}
				got, err := e.s.loadPlan(t.Context(), p.ID)
				if err != nil {
					t.Fatal(err)
				}
				r := got.Tasks[0].ContinuationRecovery
				want := map[bool]string{false: "sent", true: "queued"}[queued]
				if r.State != want || input == "" || r.InputID != input || r.SentAt.IsZero() || r.TodoID != todoID ||
					got.Tasks[0].Error != p.Tasks[0].Error || got.Tasks[0].TodoID != "" {
					t.Fatalf("delivery overwrote failed execution or lost its evidence: %+v", got.Tasks[0])
				}
				// The live session may now work; that is separate from delivery and creates no replacement.
				live := continuationAppSession()
				live.Status, live.UpdatedAt = proto.StatusWorking, time.Now()
				e.sessions("box", live)
				if current, ok := e.s.openTodoOfAgent("box", "agent01"); !ok || current.ID != todoID {
					t.Fatal("working session lost the original task")
				}
				// An uncorrelated later send/scope does not grant a second send for this attempt.
				if _, err := runJSON(t, e.s, "m1", "agent_send", continuationSendArgs(p)); err == nil || !strings.Contains(err.Error(), "already sent") {
					t.Fatalf("duplicate action was replayed: %v", err)
				}
				if sessionSends(e.f) != 1 || len(e.f.starts) != 0 || todoCount(e.s) != 1 {
					t.Fatalf("sends=%d starts=%d todos=%d", sessionSends(e.f), len(e.f.starts), todoCount(e.s))
				}
				if store != nil {
					reloaded := newThreadEnv(t, store)
					persisted, err := reloaded.s.loadPlan(t.Context(), p.ID)
					if err != nil || persisted.Tasks[0].ContinuationRecovery.InputID != input || persisted.Tasks[0].Error == "" {
						t.Fatalf("recovery/history did not survive reload: %v %+v", err, persisted)
					}
				}
			})
		}
	}
}

func TestPlanContinuationRecoveryFollowsSupervisorTransfer(t *testing.T) {
	e, p, todoID := failedAppContinuation(t, nil)
	setSettings(t, e.s, func(v *Settings) { v.Leaders = []string{"lead0001", "lead0002"} })
	e.f.mu.Lock()
	e.f.known["lead0001"], e.f.known["lead0002"] = true, true
	e.f.mu.Unlock()
	e.sessions("server", proto.Session{SessionID: "m1", Assistant: true},
		proto.Session{SessionID: "lead0001", Assistant: true}, proto.Session{SessionID: "lead0002", Assistant: true})
	if _, err := e.s.toolAgentTransfer(t.Context(), "m1", transferArgs("box", "agent01", "lead0001")); err != nil {
		t.Fatal(err)
	}
	if r := e.s.continuationOffer(t.Context(), "lead0001", p.Tasks[0]); r == nil {
		t.Fatal("current supervisor could not obtain the original recovery action")
	}
	if _, err := e.s.toolAgentTransfer(t.Context(), "lead0001", transferArgs("box", "agent01", "lead0002")); err != nil {
		t.Fatal(err)
	}
	args := continuationSendArgs(p)
	if _, err := runJSON(t, e.s, "lead0001", "agent_send", args); err == nil {
		t.Fatal("former non-origin supervisor used a stale action after transfer")
	}
	if sessionSends(e.f) != 0 || len(e.f.starts) != 0 {
		t.Fatal("refused former supervisor dispatched work")
	}
	e.f.answerNode(DeviceSessionSend, func(_ string, _ json.RawMessage) (any, error) { return agentSendReply{OK: true}, nil })
	if _, err := runJSON(t, e.s, "lead0002", "agent_send", args); err != nil {
		t.Fatal(err)
	}
	got, err := e.s.loadPlan(t.Context(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Tasks[0].ContinuationRecovery.State != "sent" || got.Tasks[0].Error != p.Tasks[0].Error ||
		todoNow(e.s, todoID).Supervision.Supervisor != "lead0002" || sessionSends(e.f) != 1 || len(e.f.starts) != 0 || todoCount(e.s) != 1 {
		t.Fatal("recovery changed supervision, lost failure history or duplicated work")
	}
}

func TestPlanContinuationRecoveryRefusals(t *testing.T) {
	for _, kind := range []string{"other conversation", "other task", "other session", "scope changed", "user stopped", "unknown stopped", "exit stopped", "user cause on stale live row", "future stop cause", "unavailable", "instance changed", "permission changed", "new task", "exit message"} {
		t.Run(kind, func(t *testing.T) {
			e, p, todoID := failedAppContinuation(t, nil)
			args, self := continuationSendArgs(p), "m1"
			c := continuationAppSession()
			switch kind {
			case "other conversation":
				self = "c1x1x1"
			case "other task":
				putTodo(e.s, Todo{ID: "other", Title: "Other work", Origin: "m1", Agents: []TodoAgent{{MachineID: "box", SessionID: "agent01"}}})
			case "other session":
				c.SessionID, args["session_id"] = "agent02", "agent02"
			case "scope changed":
				e.s.tbriefs.mu.Lock()
				for _, b := range e.s.tbriefs.briefs {
					b.rec.CreatedAt = b.rec.CreatedAt.Add(time.Hour)
				}
				e.s.tbriefs.mu.Unlock()
			case "user stopped", "unknown stopped", "exit stopped":
				c.Status = proto.StatusOffline
				c.Capabilities.Send = false
				c.Capabilities.StopCause = map[string]proto.StopCause{"user stopped": proto.StopUser, "exit stopped": proto.StopExit}[kind]
			case "unavailable":
				c.Capabilities.Send = false
			case "user cause on stale live row":
				c.Capabilities.StopCause = proto.StopUser
			case "future stop cause":
				c.Capabilities.StopCause = "future-stop"
			case "instance changed":
				c.SourceInstanceID = "another-instance"
			case "permission changed":
				_ = e.s.patchPlanTask(t.Context(), p.ID, 0, func(t *PlanTask) { t.Permission = "bypassPermissions" })
			case "new task":
				args["task"] = map[string]any{"title": "Another task"}
			case "exit message":
				args["text"] = "/exit"
			}
			e.sessions("box", c)
			if _, err := runJSON(t, e.s, self, "agent_send", args); err == nil {
				t.Fatal("unsafe or unrelated continuation was accepted")
			}
			got, _ := e.s.loadPlan(t.Context(), p.ID)
			if sessionSends(e.f) != 0 || len(e.f.starts) != 0 || got.Tasks[0].ContinuationRecovery.State != "available" || got.Tasks[0].Error == "" {
				t.Fatalf("refusal sent work or marked %s recovered: %+v", todoID, got.Tasks[0])
			}
		})
	}
}

// The observation is tied to the exact input this recovery sent, not the original thread's pre-existing work.
func TestPlanContinuationRecoveryObservesItsOwnReply(t *testing.T) {
	e, p, todoID := failedAppContinuation(t, nil)
	var input string
	at := time.Now().UTC()
	e.f.answerNode(DeviceSessionSend, func(_ string, params json.RawMessage) (any, error) {
		var a map[string]any
		_ = json.Unmarshal(params, &a)
		input, _ = a["client_msg_id"].(string)
		c := continuationAppSession()
		c.Status, c.UpdatedAt = proto.StatusDone, at.Add(2*time.Second)
		e.f.sessions["box"] = []proto.Session{c} // answerNode holds the fake lock
		return agentSendReply{OK: true}, nil
	})
	e.f.answerNode(DeviceSessionItems, func(_ string, _ json.RawMessage) (any, error) {
		return map[string]any{"items": []proto.Item{
			{ID: "old-reply", Role: "assistant", Kind: proto.KindText, Text: "Previous result", TS: at.Add(-time.Hour)},
			{ID: "input", Role: "user", Kind: proto.KindText, Text: "Check the remaining examples", ClientMsgID: input, TS: at},
			{ID: "new-reply", Role: "assistant", Kind: proto.KindText, Text: "Remaining examples checked", TS: at.Add(time.Second)},
		}}, nil
	})
	args := continuationSendArgs(p)
	args["wait_seconds"] = 1
	res, err := runJSON(t, e.s, "m1", "agent_send", args)
	if err != nil || !strings.Contains(res, `"state":"completed"`) || !strings.Contains(res, `"reply":"Remaining examples checked"`) || strings.Contains(res, "Previous result") {
		t.Fatalf("new execution was not attributed to the recovery input: %q %v", res, err)
	}
	if input == "" || sessionSends(e.f) != 1 || len(e.f.starts) != 0 || e.todoOf("box", "agent01") != todoID {
		t.Fatal("observation changed execution or lost the original task")
	}
}

func TestPlanContinuationRecoveryDoesNotInferFromUnrelatedSend(t *testing.T) {
	e, p, _ := failedAppContinuation(t, nil)
	e.f.answerNode(DeviceSessionSend, func(_ string, _ json.RawMessage) (any, error) { return agentSendReply{OK: true}, nil })
	args := continuationSendArgs(p)
	delete(args, "continuation")
	if _, err := runJSON(t, e.s, "m1", "agent_send", args); err != nil {
		t.Fatal(err)
	}
	got, _ := e.s.loadPlan(t.Context(), p.ID)
	if got.Tasks[0].ContinuationRecovery.State != "available" || got.Tasks[0].Error == "" {
		t.Fatal("an uncorrelated message rewrote historical failure")
	}
}

func TestPlanContinuationRecoveryAckSurvivesImmediateSessionStateChange(t *testing.T) {
	for _, status := range []string{proto.StatusBlocked, proto.StatusInterrupted, proto.StatusOffline} {
		t.Run(status, func(t *testing.T) {
			e, p, _ := failedAppContinuation(t, nil)
			e.f.answerNode(DeviceSessionSend, func(_ string, _ json.RawMessage) (any, error) {
				c := continuationAppSession()
				c.Status = status
				if status == proto.StatusOffline {
					c.Capabilities.Send, c.Capabilities.StopCause = false, proto.StopUser
				}
				e.f.sessions["box"] = []proto.Session{c} // answerNode holds the fake lock
				return agentSendReply{OK: true}, nil
			})
			if _, err := runJSON(t, e.s, "m1", "agent_send", continuationSendArgs(p)); err != nil {
				t.Fatalf("a later session state erased its confirmed receipt: %v", err)
			}
			got, _ := e.s.loadPlan(t.Context(), p.ID)
			if got.Tasks[0].ContinuationRecovery.State != "sent" || got.Tasks[0].Error == "" || len(e.f.starts) != 0 {
				t.Fatalf("lost receipt/history or resumed a stopped session: %+v", got.Tasks[0])
			}
		})
	}
}

func TestPlanContinuationRecoveryUnknownAndFailed(t *testing.T) {
	for _, kind := range []string{"refused", "timeout", "missing ack", "scope changed during send"} {
		t.Run(kind, func(t *testing.T) {
			e, p, _ := failedAppContinuation(t, nil)
			e.f.answerNode(DeviceSessionSend, func(_ string, _ json.RawMessage) (any, error) {
				switch kind {
				case "refused":
					return nil, nodeRefused
				case "timeout":
					return nil, context.DeadlineExceeded
				case "scope changed during send":
					e.s.tbriefs.mu.Lock()
					for _, b := range e.s.tbriefs.briefs {
						b.rec.CreatedAt = b.rec.CreatedAt.Add(time.Hour)
					}
					e.s.tbriefs.mu.Unlock()
					return agentSendReply{OK: true}, nil
				}
				return agentSendReply{}, nil
			})
			if _, err := runJSON(t, e.s, "m1", "agent_send", continuationSendArgs(p)); err == nil {
				t.Fatal("unconfirmed recovery reported success")
			}
			got, _ := e.s.loadPlan(t.Context(), p.ID)
			want := "unknown"
			if kind == "refused" {
				want = "available"
			}
			if got.Tasks[0].ContinuationRecovery.State != want || got.Tasks[0].Error != p.Tasks[0].Error {
				t.Fatalf("lost true outcome/history: %+v", got.Tasks[0])
			}
			if kind != "refused" {
				_, _ = runJSON(t, e.s, "m1", "agent_send", continuationSendArgs(p))
				if sessionSends(e.f) != 1 {
					t.Fatal("uncertain send replayed")
				}
			}
		})
	}
}

func TestPlanContinuationRecoveryConcurrentSendAndReplacement(t *testing.T) {
	e, p, todoID := failedAppContinuation(t, nil)
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	e.f.answerNode(DeviceSessionSend, func(_ string, _ json.RawMessage) (any, error) {
		calls.Add(1)
		return agentSendReply{OK: true}, nil
	})
	e.f.mu.Lock()
	e.f.beforeSend = func() { close(entered); <-release }
	e.f.mu.Unlock()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	done := make(chan error, 1)
	go func() {
		_, err := runJSON(t, e.s, "m1", "agent_send", continuationSendArgs(p))
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("send did not reach node")
	}
	if _, err := runJSON(t, e.s, "m1", "agent_send", continuationSendArgs(p)); err == nil {
		t.Error("concurrent action was not refused")
	}
	if _, err := e.s.takeTodoForTask(t.Context(), "other-plan", "m1", p.Tasks[0]); err == nil {
		t.Error("replacement was admitted during continuation")
	}
	// Membership changes are guarded while the input is still in flight.
	e.s.todos.writing.Lock()
	joinErr := e.s.writeTodoAgentLink(t.Context(), todoID, "box", "agent02", "Another", "/code/app", "codex", "", "manual", "", false)
	moveErr := e.s.writeTodoAgentLink(t.Context(), "other", "box", "agent01", "Original", "/code/app", "codex", "", "manual", "", false)
	e.s.todos.writing.Unlock()
	if joinErr == nil || moveErr == nil {
		t.Error("in-flight delivery allowed a second writer or task reassignment")
	}
	child := proto.Session{SessionID: "child01", Agent: "codex", Status: proto.StatusWorking, SpawnFrom: todoID + "@box/agent01"}
	e.s.autoLinkTodos("box", []proto.Session{child})
	if _, linked := e.s.openTodoOfAgent("box", "child01"); linked {
		t.Error("in-flight delivery admitted a spawned writer")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || len(e.f.starts) != 0 || e.s.taskStartOnWay(todoID) {
		t.Fatal("duplicate send/start or leaked start claim")
	}
	e.s.autoLinkTodos("box", []proto.Session{child})
	if current, linked := e.s.openTodoOfAgent("box", "child01"); !linked || current.ID != todoID {
		t.Fatal("settled delivery kept blocking legitimate membership")
	}
}

func TestPlanContinuationRecoveryAckSurvivesTaskCompletion(t *testing.T) {
	e, p, todoID := failedAppContinuation(t, nil)
	e.f.mu.Lock()
	e.f.beforeSend = func() { e.must("PATCH", "/api/v1/todos/"+todoID, `{"done":true}`) }
	e.f.mu.Unlock()
	e.f.answerNode(DeviceSessionSend, func(_ string, _ json.RawMessage) (any, error) {
		return agentSendReply{OK: true}, nil
	})
	if _, err := runJSON(t, e.s, "m1", "agent_send", continuationSendArgs(p)); err != nil {
		t.Fatalf("completion erased confirmed delivery: %v", err)
	}
	got, _ := e.s.loadPlan(t.Context(), p.ID)
	if got.Tasks[0].ContinuationRecovery.State != "sent" || todoNow(e.s, todoID).DoneAt == nil {
		t.Fatal("receipt or owner completion was lost")
	}
}

func TestPlanContinuationRecoveryPersistedClaimCannotReplay(t *testing.T) {
	store := taskRecoveryDatabase(t)
	e, p, _ := failedAppContinuation(t, store)
	if err := e.s.patchPlanTask(t.Context(), p.ID, 0, func(task *PlanTask) {
		task.ContinuationRecovery.State = "sending"
		task.ContinuationRecovery.InputID = "already-in-flight"
	}); err != nil {
		t.Fatal(err)
	}
	reloaded := newThreadEnv(t, store)
	reloaded.sessions("box", continuationAppSession())
	if _, err := runJSON(t, reloaded.s, "m1", "agent_send", continuationSendArgs(p)); err == nil {
		t.Fatal("restart replayed an unconfirmed send")
	}
	if sessionSends(reloaded.f) != 0 || len(reloaded.f.starts) != 0 {
		t.Fatal("restart dispatched work")
	}
}

func TestPlanContinuationRecoveryRejectsSecondWriter(t *testing.T) {
	e, p, todoID := failedAppContinuation(t, nil)
	second := continuationAppSession()
	second.SessionID = "agent02"
	e.sessions("box", continuationAppSession(), second)
	e.must("PUT", "/api/v1/todos/"+todoID+"/agents/box/agent02", "")
	if _, err := runJSON(t, e.s, "m1", "agent_send", continuationSendArgs(p)); err == nil || !strings.Contains(err.Error(), "another Agent") {
		t.Fatalf("second writer was not rejected: %v", err)
	}
	if sessionSends(e.f) != 0 || len(e.f.starts) != 0 {
		t.Fatal("second writer refusal dispatched work")
	}
}
