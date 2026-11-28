package assistant

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
)

// An open plan's Start presentation is computed where it is read (#17853): current tasks.trust coverage and the real
// wait decide whether Start These Directly is useful. Nothing is stored on the plan, so a read after persistence agrees.
func TestPlanStartPresentation(t *testing.T) {
	ctx := context.Background()
	trusted := PlanTask{Machine: "box", Cwd: "/home/me/src/tool"}
	uncovered := PlanTask{Machine: "box", Cwd: "/home/me/src/other"}
	readonly := PlanTask{Machine: "box", Cwd: "/home/me/src/readonly", Readonly: true}

	t.Run("mixed plan offers only the uncovered task after persistence", func(t *testing.T) {
		s, _ := trustFixture(t)
		s.trustKinds(ctx, []PlanTask{trusted})
		p := &TaskPlan{ID: "plan_mixed", Conversation: "side", State: planOpen, Tasks: []PlanTask{trusted, uncovered, readonly}}
		raw, err := json.Marshal(p.Tasks)
		if err != nil {
			t.Fatal(err)
		}
		read := planFromRow(db.TaskPlanRow{ID: p.ID, Conversation: p.Conversation, State: p.State, Tasks: raw})
		got := s.planWithSpace(ctx, read)
		if got.Start == nil {
			t.Fatal("current hub omitted the open plan's start presentation")
		}
		if got.Start.Reason != assistantwire.PlanStartReasonStartAuthorization || !reflect.DeepEqual(got.Start.Remember, []int{1}) {
			t.Fatalf("start = %+v, want start_authorization with only task 1", got.Start)
		}
		if p.Start != nil {
			t.Fatal("presentation mutated or was stored on the source plan")
		}
	})

	t.Run("covered and read-only tasks offer nothing", func(t *testing.T) {
		s, _ := trustFixture(t)
		s.trustKinds(ctx, []PlanTask{trusted})
		got := s.planWithSpace(ctx, &TaskPlan{ID: "plan_covered", Conversation: "side", State: planOpen, Tasks: []PlanTask{trusted, readonly}})
		if got.Start == nil || got.Start.Reason != assistantwire.PlanStartReasonStartAuthorization || len(got.Start.Remember) != 0 {
			t.Fatalf("covered plan start = %+v, want start_authorization with no remember indexes", got.Start)
		}
	})

	for _, tc := range []struct {
		name   string
		task   PlanTask
		reason assistantwire.PlanStartReason
	}{
		{name: "outbound confirmation", task: PlanTask{Machine: "box", Cwd: "/home/me/src/other", MemoryTopic: []string{"health"}}, reason: assistantwire.PlanStartReasonOutboundConfirmation},
		{name: "owner step", task: PlanTask{Machine: "box", Cwd: "/home/me/src/other", OwnerStep: ownerStepMoney}, reason: assistantwire.PlanStartReasonOwnerStep},
		{name: "request gate", task: PlanTask{Machine: "box", Cwd: "/home/me/src/other", RequestGate: "todo_request"}, reason: assistantwire.PlanStartReasonRequestGate},
		{name: "ordinary conversation continuation", task: PlanTask{Machine: "box", Cwd: "/home/me/src/other", Continues: "todo_previous", HandoffOperationID: "op_previous"}, reason: assistantwire.PlanStartReasonContinuation},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := trustFixture(t)
			got := s.planWithSpace(ctx, &TaskPlan{ID: "plan_gate", Conversation: "side", State: planOpen, Tasks: []PlanTask{tc.task}})
			if got.Start == nil || got.Start.Reason != tc.reason || len(got.Start.Remember) != 0 {
				t.Fatalf("start = %+v, want %q and no trust offer", got.Start, tc.reason)
			}
		})
	}

	t.Run("authorized direction continuation keeps the ordinary trust offer", func(t *testing.T) {
		s, _ := trustFixture(t)
		setSettings(t, s, func(v *Settings) { v.Leaders = []string{"lead"} })
		task := uncovered
		task.Continues, task.HandoffOperationID = "todo_previous", "op_previous"
		got := s.planWithSpace(ctx, &TaskPlan{ID: "plan_lead", Conversation: "lead", State: planOpen, Tasks: []PlanTask{task}})
		if got.Start == nil || got.Start.Reason != assistantwire.PlanStartReasonStartAuthorization || !reflect.DeepEqual(got.Start.Remember, []int{0}) {
			t.Fatalf("authorized continuation start = %+v", got.Start)
		}
	})

	t.Run("a settled plan has no start presentation", func(t *testing.T) {
		s, _ := trustFixture(t)
		got := s.planWithSpace(ctx, &TaskPlan{ID: "plan_started", Conversation: "side", State: planStarted, Tasks: []PlanTask{uncovered}})
		if got.Start != nil {
			t.Fatalf("settled plan start = %+v", got.Start)
		}
	})
}
