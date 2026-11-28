package assistant

import (
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestDispatchReuseStopsPlanAndDirectStart(t *testing.T) {
	calls := []struct {
		name string
		args any
	}{
		{"task_plan", briefTask(nil)},
		{"agent_start", map[string]any{"machine": "box", "cwd": "/code/app", "prompt": "Investigate coverage",
			"worktree": map[string]string{"branch": "new-work"}}},
	}
	for _, call := range calls {
		t.Run(call.name, func(t *testing.T) {
			s, f, _ := briefService(t, "")
			todo, err := s.createTodo(t.Context(), "Investigate coverage", "", "conv1", "")
			if err != nil {
				t.Fatal(err)
			}
			session := "01a20000-0000-7000-8000-000000000001"
			if err := s.linkTodoAgent(t.Context(), todo.ID, "box", session, "coverage", "/code/app", "claude"); err != nil {
				t.Fatal(err)
			}
			for _, reference := range []string{todo.ID, session, session[:8]} {
				f.setItems(proto.Item{ID: "request", Role: "user", Kind: proto.KindText, Text: "Continue work on " + reference})
				got, refused := tool(t, s, call.name, call.args)
				if !refused || !strings.Contains(got, todo.ID) || !strings.Contains(got, "distinct_from") {
					t.Fatalf("expected reuse choice for %s: %v %s", reference, refused, got)
				}
				if n := todoCount(s); n != 1 || len(f.started()) != 0 {
					t.Fatalf("refusal changed state: todos=%d starts=%d", n, len(f.started()))
				}
			}
		})
	}
}

func TestDispatchReuseRechecksSavedPlanAtStart(t *testing.T) {
	s, f, _ := briefService(t, "")
	todo, err := s.createTodo(t.Context(), "Existing investigation", "", "conv1", "")
	if err != nil {
		t.Fatal(err)
	}
	orig := BriefRequest{Text: "Continue related work", Conversation: "conv1", MessageID: "owner-turn"}
	f.setItems(proto.Item{ID: orig.MessageID, Role: "user", Kind: proto.KindText, Text: orig.Text})
	got, refused := tool(t, s, "task_plan", briefTask(map[string]any{"owner_step": "outward"}))
	if refused {
		t.Fatalf("plan failed before the binding: %s", got)
	}
	fields := strings.Fields(got)
	if len(fields) < 2 || !strings.HasPrefix(fields[1], "plan_") {
		t.Fatalf("no waiting plan: %s", got)
	}
	id := fields[1]
	s.briefFor(t.Context(), briefRecord{Request: &orig, StartedBy: briefByOwner}, todo, nil, 0)
	if _, err := s.startPlan(t.Context(), id, nil, false); err == nil || !strings.Contains(err.Error(), todo.ID) {
		t.Fatalf("Start accepted a new binding: %v", err)
	}
	if n := todoCount(s); n != 1 || len(f.started()) != 0 {
		t.Fatalf("Start changed state: todos=%d starts=%d", n, len(f.started()))
	}
	p, err := s.loadPlan(t.Context(), id)
	if err != nil || p.State != planOpen {
		t.Fatalf("plan did not stay open: %+v %v", p, err)
	}
}

func TestDispatchReuseAllowsSeparateWorkAndKeepsReason(t *testing.T) {
	s, f, _ := briefService(t, "")
	todo, err := s.createTodo(t.Context(), "Existing investigation", "", "conv1", "")
	if err != nil {
		t.Fatal(err)
	}
	f.setItems(proto.Item{ID: "separate", TS: time.Now(), Role: "user", Kind: proto.KindText, Text: "Independently verify " + todo.ID})
	reason := "Independent validation in a separate checkout"
	got, refused := tool(t, s, "agent_start", map[string]any{"machine": "box", "cwd": "/code/app", "prompt": "Verify the integration",
		"distinct_from": []TaskDistinctFrom{{TodoID: todo.ID, Reason: reason}}})
	if refused {
		t.Fatalf("separate work was refused: %s", got)
	}
	for _, candidate := range s.todos.todos {
		if candidate.ID != todo.ID {
			b, _ := s.briefOfTodo(candidate.ID)
			if len(b.DistinctFrom) == 1 && b.DistinctFrom[0].Reason == reason {
				return
			}
		}
	}
	t.Fatalf("separate reason was not retained: %s", got)
}

func TestDispatchReuseLeavesWeakMatchesAdvisory(t *testing.T) {
	s, f, _ := briefService(t, "Check the same project for a different regression")
	old, err := s.createTodo(t.Context(), "Fix the login crash", "", "conv1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.linkTodoAgent(t.Context(), old.ID, "box", "01a20000-0000-7000-8000-000000000009", "login", "/code/app", "claude"); err != nil {
		t.Fatal(err)
	}
	got, refused := tool(t, s, "task_plan", briefTask(map[string]any{"title": "Check a separate payment regression"}))
	if refused {
		t.Fatalf("same directory blocked separate work: %s", got)
	}
	if len(f.started()) == 0 && !strings.Contains(got, "Queued") && !strings.Contains(got, "Start") {
		t.Fatalf("separate work did not get a plan: %s", got)
	}
}
