package assistant

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestTaskStageMergeRequestLinks(t *testing.T) {
	for _, url := range []string{
		"https://gitlab.example.com/group/repo/merge_requests/42",
		"https://example.com/group/subgroup/repo/-/merge_requests/17",
		"https://example.com:8443/group/repo/-/merge_requests/8",
	} {
		for _, tc := range []struct{ reply, stage string }{
			{"Opened [MR](" + url + ").", "review"},
			{"Merged " + url, "merge"},
			{url + " is not merged yet", "review"},
			{"Not merged: " + url, "review"},
		} {
			stage, pr := stageOf(tc.reply)
			if stage != tc.stage || pr != url {
				t.Errorf("stageOf(%q) = %s %s; want %s %s", tc.reply, stage, pr, tc.stage, url)
			}
		}
	}
	for _, reply := range []string{"MR !42 merged", "MR !42 awaiting review", "https://example.com/group/repo/-/merge_requests/not-a-number"} {
		stage, pr := stageOf(reply)
		if stage != "develop" || pr != "" {
			t.Errorf("unlinked MR %q = %s %s", reply, stage, pr)
		}
	}
}

func TestTaskStageKind(t *testing.T) {
	for _, tc := range []struct {
		name       string
		readonly   bool
		permission string
		criteria   []string
		pr         string
		want       string
	}{
		{"unknown is code", false, "", nil, "", "code"},
		{"read-only", true, "", nil, "", "general"},
		{"plan permission", false, "plan", nil, "", "general"},
		{"research", false, "", []string{"Publish the research report"}, "", "general"},
		{"MR delivery", false, "", []string{"MR merged and deployed"}, "", "code"},
		{"release", false, "", []string{"Release shipped"}, "", "code"},
		{"Chinese delivery", false, "", []string{"合并、部署、上线"}, "", "code"},
		{"MR evidence overrides read-only", true, "plan", []string{"Research report ready"}, "https://example.com/g/r/-/merge_requests/9", "code"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := taskStageKind(tc.readonly, tc.permission, tc.criteria, tc.pr); got != tc.want {
				t.Fatalf("kind = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestTaskStageMidTaskReply(t *testing.T) {
	s, f := newTaskService(t)
	todo := startedTask(t, s, f)
	ctx := context.Background()
	if got := todoNow(s, todo.ID); got.StageKind != "general" {
		t.Fatalf("read-only task kind = %s", got.StageKind)
	}
	agentTurn(s, f, proto.StatusWorking, "", 0)
	url := "https://gitlab.example.com/group/repo/-/merge_requests/42"
	// No TurnEnded: a progress reply from the list must update a running task.
	s.SessionsChanged(ctx, DeviceSessions{MachineID: "box", Sessions: []proto.Session{{SessionID: "s1", Status: proto.StatusWorking, LastText: "Opened " + url}}})
	if got := todoNow(s, todo.ID); got.Stage != "review" || got.PRURL != url || got.StageKind != "code" || got.DoneAt != nil {
		t.Fatalf("mid-task review = %+v", got)
	}
	s.SessionItems(ctx, SessionItems{MachineID: "box", SessionID: "s1", Items: []proto.Item{{Role: "assistant", Kind: proto.KindText, Text: "MR merged"}}})
	if got := todoNow(s, todo.ID); got.Stage != "merge" || got.PRURL != url {
		t.Fatalf("mid-task merge = %+v", got)
	}
	s.SessionItems(ctx, SessionItems{MachineID: "box", SessionID: "s1", Items: []proto.Item{
		{Role: "user", Kind: proto.KindText, Text: "Already deployed and verified"},
		{Role: "assistant", Kind: proto.KindToolResult, Text: "Already deployed and verified"},
		{Role: "assistant", Kind: proto.KindText, Text: "Still developing"},
	}})
	if got := todoNow(s, todo.ID); got.Stage != "merge" || got.DoneAt != nil {
		t.Fatalf("non-answer or earlier stage changed progress: %+v", got)
	}
}

func TestTaskStageClippedProgressReply(t *testing.T) {
	s, f := newTaskService(t)
	todo := startedTask(t, s, f)
	ctx := context.Background()
	url := "https://example.com/group/repo/-/merge_requests/73"
	f.setItems(proto.Item{Role: "assistant", Kind: proto.KindText, Text: strings.Repeat("Checked the change. ", 20) + "Merged " + url})
	d := DeviceSessions{MachineID: "box", Sessions: []proto.Session{{SessionID: "s1", Status: proto.StatusWorking, LastText: "Checked the change. …"}}}
	s.taskStageSessions(ctx, d)
	if got := todoNow(s, todo.ID); got.Stage != "merge" || got.PRURL != url {
		t.Fatalf("clipped reply missed its MR: %+v", got)
	}
	// An unchanged row does not fetch again or accept a different transcript as that reply.
	f.setItems(proto.Item{Role: "assistant", Kind: proto.KindText, Text: "Deployed and verified"})
	s.taskStageSessions(ctx, d)
	if todoNow(s, todo.ID).Stage != "merge" {
		t.Fatal("unchanged list row was reprocessed")
	}
}

func TestTaskStageDeliveryPriority(t *testing.T) {
	url := "https://example.com/g/r/merge_requests/3"
	for _, tc := range []struct{ mark, stage string }{
		{"停在 PR", "review"}, {"已合未上线", "merge"}, {"默认关待启用", "deploy"}, {"已上线并实测", "deploy"}, {"不涉及上线", "develop"},
	} {
		reply := "Merged " + url + ", deployed and verified\n交付：" + tc.mark
		if got, pr := stageOf(reply); got != tc.stage || pr != url {
			t.Errorf("mark %s = %s %s, want %s", tc.mark, got, pr, tc.stage)
		}
	}
	s, f := newTaskService(t)
	todo := startedTask(t, s, f)
	ctx := context.Background()
	// A later sentence can omit the known link without reducing a deployed result to merge.
	s.setTaskStage(ctx, todo.ID, "merge", url)
	s.advanceTaskReply(ctx, todoNow(s, todo.ID), "box", "s1", "Merged and deployed")
	if got := todoNow(s, todo.ID); got.Stage != "deploy" {
		t.Fatalf("link-free deployed reply = %s", got.Stage)
	}
	s.setTaskStage(ctx, todo.ID, "verify", url) // an earlier keyword guess
	s.setTaskDelivery(ctx, todo.ID, TodoDelivery{State: deliveryMerged})
	s.advanceTaskReply(ctx, todoNow(s, todo.ID), "box", "s1", "Merged "+url+", deployed and verified")
	if got := todoNow(s, todo.ID); got.Stage != "merge" {
		t.Fatalf("delivery lost to keyword guess: %s", got.Stage)
	}
	// Explicit acceptance is stronger than the held delivery and remains so after later delivery writes.
	s.setTaskStageKind(ctx, todo.ID, "verify", "", "", true)
	s.setTaskDelivery(ctx, todo.ID, TodoDelivery{State: deliveryPR})
	if got := todoNow(s, todo.ID); got.Stage != "verify" {
		t.Fatalf("explicit acceptance lost: %s", got.Stage)
	}
}

func TestTaskStagePendingPlan(t *testing.T) {
	s, f := newTaskService(t)
	todo := startedTask(t, s, f)
	ctx := context.Background()
	agentTurn(s, f, proto.StatusWorking, "", 0)
	// A permission or question is not evidence of a plan waiting for approval.
	ix := proto.Interaction{ID: "plan_stage", MachineID: "box", SessionID: "s1", Kind: proto.IxPermission}
	s.fleet.mu.Lock()
	s.fleet.interactions = map[string]*pending{ix.ID: {ix: ix}}
	s.fleet.mu.Unlock()
	s.taskPlanApproval("box", "s1")
	if todoNow(s, todo.ID).Stage != "develop" {
		t.Fatal("permission guessed as plan")
	}
	ix.Kind = proto.IxPlan
	s.fleet.mu.Lock()
	s.fleet.interactions[ix.ID].ix = ix
	s.fleet.mu.Unlock()
	s.InteractionRaised(ctx, InteractionRaised{Interaction: ix})
	if got := todoNow(s, todo.ID); got.Stage != "plan" {
		t.Fatalf("pending plan stage = %s", got.Stage)
	}
	if stored := s.todoStages(ctx)[todo.ID].Stage; stored != "develop" {
		t.Fatalf("pending plan rolled back the stored stage: %s", stored)
	}
	// Starting another turn is not proof that the plan was approved.
	s.taskWorking(ctx, "box", "s1")
	if todoNow(s, todo.ID).Stage != "plan" {
		t.Fatal("working status cleared pending plan")
	}
	s.finishInteraction(ix.ID)
	if todoNow(s, todo.ID).Stage != "develop" {
		t.Fatal("resolved plan still current")
	}
}

func TestTaskStageReviewVerdict(t *testing.T) {
	for _, tc := range []struct {
		name      string
		userCheck bool
		outcome   string
		want      string
	}{
		{"accepted", false, "done", "verify"},
		{"owner-check", true, "done", "merge"},
		{"cannot", false, "cannot", "merge"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, f := newTaskService(t)
			todo := startedTask(t, s, f)
			ctx := context.Background()
			s.setTaskDelivery(ctx, todo.ID, TodoDelivery{State: deliveryMerged})
			_, _, err := s.updateSupervision(ctx, todo.ID, func(v *Supervision) bool {
				v.Supervisor = "conv1"
				v.State = reviewPending
				return true
			})
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(map[string]any{"todo_id": todo.ID, "verdict": "accepted", "note": "Review complete", "user_check": tc.userCheck, "outcome": tc.outcome})
			if _, err := s.toolTaskReview(ctx, "conv1", raw); err != nil {
				t.Fatal(err)
			}
			if got := todoNow(s, todo.ID).Stage; got != tc.want {
				t.Fatalf("stage = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestTaskStageMetadataRoundTrip(t *testing.T) {
	ctx := context.Background()
	st := &memStore{}
	s, _ := newTaskServiceOn(t, st)
	todo, err := s.createTodo(ctx, "Research report", "Inspect the results", "conv1", "")
	if err != nil {
		t.Fatal(err)
	}
	s.setTaskStageKind(ctx, todo.ID, "develop", "", "general", false)
	var stored map[string]todoStage
	if _, err := st.Setting(ctx, reportStagesKey, &stored); err != nil {
		t.Fatal(err)
	}
	if stored[todo.ID].Kind != "general" {
		t.Fatalf("stored kind = %+v", stored)
	}
	wire, err := json.Marshal(formatTodo(todoNow(s, todo.ID)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(wire), `"stage_kind":"general"`) {
		t.Fatalf("wire = %s", wire)
	}
	// Loading by a new service preserves the setting without a schema migration.
	s2, _ := newTaskServiceOn(t, st)
	if got := s2.todoStages(ctx)[todo.ID].Kind; got != "general" {
		t.Fatalf("reloaded kind = %s", got)
	}
	b := (&briefFacts{Progress: []briefProgress{{ID: todo.ID, Stage: "develop", StageKind: "general"}}}).body(s, "progress")
	if b.Progress[0].StageKind != "general" {
		t.Fatal("briefing omitted kind")
	}
}
