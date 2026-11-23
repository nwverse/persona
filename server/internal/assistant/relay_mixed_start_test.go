package assistant

import (
	"context"
	"encoding/json"
	"testing"
)

// A plan can combine new work on the current hand-over with a continuation whose brief still names an older message.
// Both start only when the current hand-over was verifiably delivered to this lead; neither an inherited brief nor a
// started plan in another folder may substitute for that request.
func TestAMixedPlanStartsOnlyWithTheCurrentVerifiedHandover(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*handover)
		starts bool
	}{
		{name: "delivered to this lead", starts: true},
		{name: "a different lead", change: func(e *handover) { e.To = "another-lead" }},
		{name: "not delivered", change: func(e *handover) { e.State = handoverQueued }},
		{name: "a different message", change: func(e *handover) { e.MessageID = "m-other#1" }},
		{name: "a different source conversation", change: func(e *handover) { e.Conversation = "another-conversation" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := newStartRuleHarness(t, nil)
			stopHolds(t, d.s)
			d.f.setItems(ownerItem("m-earlier", "Fix the earlier export"))
			if !d.planIn(t, d.leaderID, map[string]any{"cwd": "/code/earlier"}) {
				t.Fatal("the original plan on the owner's direct words waits for Start")
			}
			todo := onlyTodo(d.s)
			d.handOver(t)
			if tc.change != nil {
				h := &d.s.handovers
				h.mu.Lock()
				for id, e := range h.m {
					tc.change(&e)
					h.m[id] = e
				}
				h.mu.Unlock()
			}

			var input struct {
				Tasks []map[string]any `json:"tasks"`
			}
			if err := json.Unmarshal([]byte(relayedPlan(map[string]any{"cwd": "/code/app"})), &input); err != nil {
				t.Fatal(err)
			}
			continuation := map[string]any{}
			for k, v := range input.Tasks[0] {
				continuation[k] = v
			}
			continuation["title"] = "Continue the export repair"
			continuation["cwd"] = "/code/continued"
			continuation["continues"] = todo.ID
			continuation["continues_why"] = "Replace the earlier Agent"
			input.Tasks = append(input.Tasks, continuation)
			raw, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}

			before := d.startsSoFar()
			res := d.tool(t, d.leaderID, "task_plan", string(raw))
			out := res.Content[0].Text
			if res.IsError {
				t.Fatalf("task_plan: %s", out)
			}
			started := !waitsForStart(out)
			if started != tc.starts {
				t.Fatalf("started %v, want %v: %s", started, tc.starts, out)
			}
			if tc.starts {
				waitFor(t, "both agents to start", func() bool { return d.startsSoFar() == before+2 })
			} else {
				waitFor(t, "the start reservation to settle", func() bool { return d.s.taskStarting.Load() == 0 })
				if n := d.startsSoFar() - before; n != 0 {
					t.Fatalf("%d agents started on a plan that waits", n)
				}
			}

			plans, err := d.s.conversationPlans(context.Background(), d.leaderID)
			if err != nil {
				t.Fatal(err)
			}
			var mixed *TaskPlan
			for _, p := range plans {
				if len(p.Tasks) == 2 && p.Tasks[1].Continues == todo.ID {
					mixed = p
				}
			}
			if mixed == nil {
				t.Fatal("the mixed plan was not saved")
			}
			if tc.starts {
				if mixed.State != planStarted || !mixed.Tasks[0].UserRequested || !mixed.Tasks[1].UserRequested {
					t.Fatalf("the mixed plan did not keep both verified requests: %+v", mixed)
				}
			} else if mixed.State == planStarted {
				t.Fatalf("the unverified mixed plan started: %+v", mixed)
			}
		})
	}
}
