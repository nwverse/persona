package assistant

import (
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

const similarityReply = `{"candidate":1,"proposed_evidence":"landscape login crash","existing_evidence":"landscape crash on login"}`

func TestDispatchReuseHighOverlapRequiresAChoice(t *testing.T) {
	for _, entry := range []string{"task_plan", "agent_start", "Start"} {
		t.Run(entry, func(t *testing.T) {
			s, f, _ := briefService(t, "Can you reproduce it with a rotated phone?")
			intent := "Reproduce the landscape login crash on a rotated phone"
			planArgs := briefTask(map[string]any{"title": intent, "prompt": intent, "owner_step": "outward"})
			var planID string
			if entry == "Start" {
				got, refused := tool(t, s, "task_plan", planArgs)
				planID = planIDRe.FindString(got)
				if refused || planID == "" {
					t.Fatalf("waiting plan: %v %s", refused, got)
				}
			}
			todo, err := s.createTodo(t.Context(), "Fix the landscape crash on login", "", "conv1", "")
			if err != nil {
				t.Fatal(err)
			}
			if err := s.linkTodoAgent(t.Context(), todo.ID, "box", "previous", "login", "/code/other-worktree", "claude"); err != nil {
				t.Fatal(err)
			}
			f.mu.Lock()
			f.dispatchMatch = similarityReply
			f.mu.Unlock()
			var got string
			var refused bool
			switch entry {
			case "task_plan":
				got, refused = tool(t, s, entry, planArgs)
			case "agent_start":
				got, refused = tool(t, s, entry, map[string]any{"machine": "box", "cwd": "/code/app", "prompt": intent,
					"worktree": map[string]string{"branch": "new-work"}})
			case "Start":
				_, err := s.startPlan(t.Context(), planID, nil, false)
				if err != nil {
					got, refused = err.Error(), true
				}
				p, err := s.loadPlan(t.Context(), planID)
				if err != nil || p.State != planOpen {
					t.Fatalf("plan did not stay open: %+v %v", p, err)
				}
			}
			if !refused || !strings.Contains(got, "high overlap") || !strings.Contains(got, todo.ID) ||
				!strings.Contains(got, "agent_send") || !strings.Contains(got, "continues") || !strings.Contains(got, "distinct_from") {
				t.Fatalf("expected all three choices for %s: %v %s", todo.ID, refused, got)
			}
			f.mu.Lock()
			worktrees := len(f.worktrees)
			f.mu.Unlock()
			if todoCount(s) != 1 || len(f.started()) != 0 || worktrees != 0 {
				t.Fatalf("refusal mutated dispatch state: todos=%d starts=%d worktrees=%d", todoCount(s), len(f.started()), worktrees)
			}
		})
	}
}

func TestDispatchReuseOverlapDistinctReasonSurvivesStart(t *testing.T) {
	s, f, _ := briefService(t, "Independently verify on a clean checkout")
	todo, err := s.createTodo(t.Context(), "Fix the landscape crash on login", "", "conv1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.linkTodoAgent(t.Context(), todo.ID, "box", "previous", "login", "/code/app", "claude"); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.dispatchMatch = similarityReply
	f.mu.Unlock()
	reason := "Independent validation on a clean checkout"
	intent := "Reproduce the landscape login crash on another device"
	got, refused := tool(t, s, "task_plan", briefTask(map[string]any{"title": intent, "prompt": intent, "owner_step": "outward",
		"distinct_from": []TaskDistinctFrom{{TodoID: todo.ID, Reason: reason}}}))
	id := planIDRe.FindString(got)
	if refused || id == "" {
		t.Fatalf("separate work did not get a plan: %v %s", refused, got)
	}
	p, err := s.startPlan(t.Context(), id, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	b, ok := s.briefOfTodo(p.Tasks[0].TodoID)
	if !ok || len(b.DistinctFrom) != 1 || b.DistinctFrom[0].Reason != reason {
		t.Fatalf("separation reason missing from new brief: %+v", b.DistinctFrom)
	}
}

func TestDispatchSimilarityRequiresGroundedEvidence(t *testing.T) {
	candidates := []dispatchSimilarityCandidate{{Todo: Todo{ID: "first"}, Text: "Fix the landscape crash on login"}}
	for _, tt := range []struct {
		name, answer string
		matched      bool
	}{
		{"grounded", similarityReply, true},
		{"weak", `{"candidate":0}`, false},
		{"out of range", `{"candidate":2,"proposed_evidence":"landscape login crash","existing_evidence":"landscape crash on login"}`, false},
		{"invented", `{"candidate":1,"proposed_evidence":"unrelated work","existing_evidence":"landscape crash on login"}`, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			match, found, err := judgeDispatchSimilarity(t.Context(), &cannedJSON{answer: tt.answer},
				"Reproduce the landscape login crash", candidates)
			if err != nil || found != tt.matched || found && match.ID != "first" {
				t.Fatalf("got %q, found=%t, err=%v", match.ID, found, err)
			}
		})
	}
}

func TestDispatchReuseChecksOverlapWhenRequestNamesMultipleTodos(t *testing.T) {
	s, f, _ := briefService(t, "")
	old, err := s.createTodo(t.Context(), "Fix the landscape crash on login", "", "conv1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.linkTodoAgent(t.Context(), old.ID, "box", "previous", "login", "/code/app", "claude"); err != nil {
		t.Fatal(err)
	}
	other, err := s.createTodo(t.Context(), "Update the billing receipts", "", "conv1", "")
	if err != nil {
		t.Fatal(err)
	}
	f.setItems(proto.Item{ID: "request", Role: "user", Kind: proto.KindText,
		Text: "Compare " + other.ID + " with " + old.ID + ", then reproduce the landscape login crash."})
	f.mu.Lock()
	f.dispatchMatch = similarityReply
	f.mu.Unlock()

	intent := "Reproduce the landscape login crash on a rotated phone"
	got, refused := tool(t, s, "task_plan", briefTask(map[string]any{"title": intent, "prompt": intent, "owner_step": "outward"}))
	if !refused || !strings.Contains(got, old.ID) || !strings.Contains(got, "high overlap") {
		t.Fatalf("multiple references bypassed the reuse choice for %s: %v %s", old.ID, refused, got)
	}
	if todoCount(s) != 2 || len(f.started()) != 0 {
		t.Fatal("refused planning started new work")
	}
}
