package assistant

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/nwverse/persona/server/internal/db"
)

// relayedPlan is task_plan's arguments for one task on box the model marks user_requested: writable unless over says
// otherwise.
func relayedPlan(over map[string]any) string {
	task := map[string]any{"title": "Fix the export", "prompt": "The export drops the old format: fix it and add a test",
		"machine": "box", "cwd": "/code/app", "readonly": false, "permission": "bypassPermissions", "user_requested": true,
		"why": "the repository is on box", "done_when": []string{"the export keeps the old format"}, "expect_minutes": 30}
	for k, v := range over {
		task[k] = v
	}
	raw, _ := json.Marshal(map[string]any{"tasks": []any{task}})
	return string(raw)
}

// newStartRuleHarness is the dispatch harness with a database that keeps and starts task plans, and the computer box
// for tasks.
func newStartRuleHarness(t *testing.T) *dispatchPolicyHarness {
	t.Helper()
	d := newDispatchPolicyHarnessOn(t, planUpdateStore{&planMemStore{memStore: &memStore{}, plans: map[string]db.TaskPlanRow{}}})
	d.f.setSessions("box")
	return d
}

// startsSoFar counts the agents started so far.
func (d *dispatchPolicyHarness) startsSoFar() int {
	d.f.mu.Lock()
	defer d.f.mu.Unlock()
	return len(d.f.starts)
}

// A turn another conversation's message started (#11699): a writable task the model marks user_requested waits for the
// owner's Start when that message carried no verified quote, and in a direction's lead also when it did. With a
// verified quote in a conversation that is no lead, and in the owner's own turn, it starts as before. Read-only tasks
// and kinds the owner trusts start either way, and a plan whose every task continues another reads the turn itself.
func TestRelayedTurnStartsWritableWorkOnlyOnTheOwnersVerifiedWords(t *testing.T) {
	for _, tc := range []struct {
		name   string
		lead   bool   // the planning conversation is the lead (else an ordinary one)
		turn   string // what started its turn: the owner, another conversation's message, or one with the owner's words
		over   map[string]any
		trust  bool
		starts bool
	}{
		{"no quote", false, "relay", nil, false, false},
		{"no quote, in the lead", true, "relay", nil, false, false},
		{"the owner's words, in the lead", true, "quoted", nil, false, false},
		{"the owner's words, elsewhere", false, "quoted", nil, false, true},
		{"the owner's own turn, in the lead", true, "owner", nil, false, true},
		{"read-only, no quote", false, "relay", map[string]any{"readonly": true, "permission": "plan", "goal": "find why the export drops it"}, false, true},
		{"a trusted kind, the owner's words, in the lead", true, "quoted", nil, true, true},
		{"a continuation, no quote", false, "relay", map[string]any{"continues": "", "continues_why": "the first agent stopped"}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := newStartRuleHarness(t)
			ctx := context.Background()
			planner := d.sideID
			if tc.lead {
				planner = d.leaderID
			}
			if _, ok := tc.over["continues"]; ok { // a task handed out earlier, on the owner's word, for this one to continue
				d.f.setItems(ownerItem("m-first", "look into the export"))
				first := relayedPlan(map[string]any{"title": "Look into the export", "readonly": true, "permission": "plan",
					"goal": "find why the export drops the old format"})
				if res := d.tool(t, planner, "task_plan", first); res.IsError || waitsForStart(res.Content[0].Text) {
					t.Fatalf("the first task: %s", res.Content[0].Text)
				}
				waitFor(t, "the first task's agent", func() bool { return d.startsSoFar() == 1 })
				todo, ok := todoByTitle(d.s, "Look into the export")
				if !ok {
					t.Fatal("no first task")
				}
				tc.over["continues"] = todo.ID
			}
			if tc.trust {
				if err := d.s.store().PutSetting(ctx, trustSetting, []string{trustKey(PlanTask{Machine: "box", Cwd: "/code/app"})}); err != nil {
					t.Fatal(err)
				}
			}
			switch tc.turn {
			case "owner":
				d.f.setItems(ownerItem("m-owner", "fix the export now"))
			case "relay":
				d.send(t, d.mainID, planner, `"text":"please fix the export"`)
				d.f.setItems(received(t, d.f, d.mainID))
			case "quoted":
				d.f.setItems(ownerItem("m-owner", quotedWords))
				d.send(t, d.mainID, planner, `"text":"please fix the export","quote":"latest"`)
				d.f.setItems(received(t, d.f, d.mainID))
			}
			before := d.startsSoFar()
			res := d.tool(t, planner, "task_plan", relayedPlan(tc.over))
			out := res.Content[0].Text
			if res.IsError {
				t.Fatalf("task_plan: %s", out)
			}
			if waits := waitsForStart(out); waits == tc.starts {
				t.Fatalf("started %v, want %v: %s", !waits, tc.starts, out)
			}
			if tc.starts {
				waitFor(t, "the agent to start", func() bool { return d.startsSoFar() == before+1 })
				return
			}
			waitFor(t, "the start reservation to settle", func() bool { return d.s.taskStarting.Load() == 0 })
			if n := d.startsSoFar() - before; n != 0 {
				t.Fatalf("%d agents started without the owner's Start", n)
			}
		})
	}
}

// The plan that waits still shows the owner's words its message carried: the card's brief takes them, so the owner
// sees what they asked for before Start.
func TestARelayedPlanThatWaitsShowsTheOwnersWords(t *testing.T) {
	d := newStartRuleHarness(t)
	d.f.setItems(ownerItem("m-owner", quotedWords))
	d.send(t, d.mainID, d.leaderID, `"text":"please fix the export","quote":"latest"`)
	d.f.setItems(received(t, d.f, d.mainID))
	res := d.tool(t, d.leaderID, "task_plan", relayedPlan(nil))
	if res.IsError || !waitsForStart(res.Content[0].Text) {
		t.Fatalf("task_plan: %s", res.Content[0].Text)
	}
	p, err := d.s.loadPlan(context.Background(), planIDPattern.FindString(res.Content[0].Text))
	if err != nil {
		t.Fatal(err)
	}
	if b := p.Tasks[0].Brief; b == nil || b.Request == nil || b.Request.Text != quotedWords || b.Trigger != "conversation" || p.Tasks[0].UserRequested {
		t.Fatalf("the waiting plan's task: %+v", p.Tasks[0])
	}
}
