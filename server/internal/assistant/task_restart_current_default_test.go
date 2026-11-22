package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

// Another Agent for an existing task (task_plan's continues, task_retry) runs on what that call names, else on the
// task's own launch as its plan named it, else on the current default: a tier the task names resolves against the
// model profile in use now, and a task that names neither a launcher nor a tier gets Model configuration › Agents as
// it is now. What an earlier start resolved for the task is not a choice of this call (#12618). Launcher, model and
// tier names are placeholders.

// restartHistoryStore is profileTaskStore that lists every plan as the plans' history, as the database does, so a
// continuation's failed start is retried through the plan that binds its to-do now (currentPlanAssignment).
type restartHistoryStore struct{ *profileTaskStore }

func (st restartHistoryStore) TaskPlansMadeSince(context.Context, time.Time) ([]db.TaskPlanRow, error) {
	st.planMu.Lock()
	defer st.planMu.Unlock()
	out := make([]db.TaskPlanRow, 0, len(st.plans))
	for _, p := range st.plans {
		p.Tasks = slices.Clone(p.Tasks)
		out = append(out, p)
	}
	return out, nil
}

// restartHub: box has Claude Code and launchers of its own, first and second (Claude Code) and other (another agent);
// conv1 is the main conversation. Model configuration › Agents starts first with model-one at high effort.
func restartHub(t *testing.T) (*Service, *fakeHub) {
	t.Helper()
	f := newFakeHub().withMachine()
	f.sessions["box"] = nil
	f.devices = []Device{
		{ID: "server", Name: "server", Home: "/home/me", Online: true, Reachable: true},
		{ID: "box", Name: "box", Home: "/Users/me", Online: true, Reachable: true, ClaudeVersion: "2.1.0"},
	}
	f.zshrc = map[string][]proto.Launcher{"box": {
		{Name: "first", Source: "zshrc"}, {Name: "second", Source: "zshrc"}, {Name: "other", Source: "zshrc", Agent: "codex"},
	}}
	s := newServiceOn(t, f, restartHistoryStore{&profileTaskStore{memStore: &memStore{}, plans: map[string]db.TaskPlanRow{}}})
	setSettings(t, s, func(v *Settings) { v.MachineID, v.Main = "server", "conv1" })
	setModel(t, s, "agent.new", ModelBinding{Provider: "launcher.first", Model: "model-one", Effort: "high"})
	return s, f
}

// restartTask is a writable task on box with extra (its launcher, tier, model, effort, continues…) merged in.
func restartTask(extra map[string]any) json.RawMessage {
	task := map[string]any{"title": "Fix the build", "prompt": "Fix the build", "machine": "box", "cwd": "/Users/me/code/app",
		"why": "the repository is on box", "done_when": []string{"CI is green"}}
	for k, v := range extra {
		task[k] = v
	}
	raw, _ := json.Marshal(map[string]any{"tasks": []map[string]any{task}})
	return raw
}

// planRestartTask plans task in conv1 and answers its plan's id.
func planRestartTask(t *testing.T, s *Service, extra map[string]any) string {
	t.Helper()
	res, err := s.toolTaskPlan(context.Background(), "conv1", restartTask(extra))
	if err != nil {
		t.Fatal(err)
	}
	return planID(t, res)
}

// startRestartPlan starts plan id (the card's Start) with the node answering failure (nil: the agent starts), waits
// until its start is over, and answers the to-do and what its session.start carried.
func startRestartPlan(t *testing.T, s *Service, f *fakeHub, id string, failure error) (string, map[string]any) {
	t.Helper()
	f.mu.Lock()
	f.failStart = failure
	before := len(f.starts)
	f.mu.Unlock()
	if _, err := s.startPlan(context.Background(), id, nil, false); err != nil {
		t.Fatal(err)
	}
	todo := todoIDOfPlan(t, s, id)
	waitFor(t, "the start to be over", func() bool {
		cur := todoNow(s, todo)
		over := len(cur.Agents) == 1 || failure != nil && cur.StartError != ""
		return over && len(startsOf(f)) > before && s.taskStarting.Load() == 0
	})
	f.mu.Lock()
	f.failStart = nil
	f.mu.Unlock()
	return todo, startsOf(f)[before]
}

// retryRestart is task_retry from the main conversation with extra (launcher, model), waiting for its start.
func retryRestart(t *testing.T, s *Service, f *fakeHub, todo string, extra map[string]any) map[string]any {
	t.Helper()
	before := len(startsOf(f))
	a := map[string]any{"todo_id": todo, "reason": "the start failed once"}
	for k, v := range extra {
		a[k] = v
	}
	args, _ := json.Marshal(a)
	if _, err := s.runTool(context.Background(), "server", "conv1", "task_retry", args); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the retry's start", func() bool { return len(startsOf(f)) > before && s.taskStarting.Load() == 0 })
	return startsOf(f)[before]
}

// launchOf is what a session.start carried: launcher, model, effort ("" for one it did not carry).
func launchOf(st map[string]any) [3]string {
	l, _ := st["launcher"].(string)
	m, _ := st["model"].(string)
	e, _ := st["effort"].(string)
	return [3]string{l, m, e}
}

// failedOnceOnFirst plans a task with extra (nothing of its launch when nil) while Model configuration › Agents runs
// the other agent, then starts it after the default moved to first (model-one, high): Claude Code runs with Allow all,
// so the start settles the task's permission again and writes the task back to its plan (as a health or admission
// detour, a launcher swap or a policy's note would). The start fails for good.
func failedOnceOnFirst(t *testing.T, s *Service, f *fakeHub, extra map[string]any) string {
	t.Helper()
	setModel(t, s, "agent.new", ModelBinding{Provider: "launcher.other", Model: "model-zero"})
	id := planRestartTask(t, s, extra)
	setModel(t, s, "agent.new", ModelBinding{Provider: "launcher.first", Model: "model-one", Effort: "high"})
	todo, first := startRestartPlan(t, s, f, id, errors.New("the example start failed"))
	if got := launchOf(first); got != [3]string{"first", "model-one", "high"} {
		t.Fatalf("first start %v", got)
	}
	p, err := s.loadPlan(context.Background(), id)
	if err != nil || p.Tasks[0].Permission != "bypassPermissions" {
		t.Fatalf("the start did not write the task back: %+v %v", p, err)
	}
	return todo
}

// task_retry with no launcher, after a start that wrote the task back: the retry runs on the current Agents default,
// not on what the failed start resolved, which stays only as the failed start's record (its launcher).
func TestTaskRetryFollowsTheCurrentDefault(t *testing.T) {
	s, f := restartHub(t)
	todo := failedOnceOnFirst(t, s, f, nil)
	_, p, i, err := s.remedyOf(context.Background(), todo)
	if err != nil {
		t.Fatal(err)
	}
	if pt := p.Tasks[i]; pt.Launcher != "" || pt.Model != "" || pt.Effort != "" || pt.Tier != "" || pt.ErrorArgs["launcher"] != "first" {
		t.Fatalf("the plan keeps %q/%q/%q/%q (failed on %q), want the task's own launch and the failed start's launcher",
			pt.Launcher, pt.Model, pt.Effort, pt.Tier, pt.ErrorArgs["launcher"])
	}
	setModel(t, s, "agent.new", ModelBinding{Provider: "launcher.second", Model: "model-two", Effort: "low"})
	if got := launchOf(retryRestart(t, s, f, todo, nil)); got != [3]string{"second", "model-two", "low"} {
		t.Fatalf("task_retry started %v, want the current default [second model-two low]", got)
	}
}

// The same for the owner's Retry on the card (POST /api/v1/todos/{id}/retry), which shares the retry's start.
func TestOwnerRetryFollowsTheCurrentDefault(t *testing.T) {
	s, f := restartHub(t)
	todo := failedOnceOnFirst(t, s, f, nil)
	setModel(t, s, "agent.new", ModelBinding{Provider: "launcher.second", Model: "model-two", Effort: "low"})
	before := len(startsOf(f))
	if _, err := s.retryTask(context.Background(), todo); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the retry's start", func() bool { return len(startsOf(f)) > before && s.taskStarting.Load() == 0 })
	if got := launchOf(startsOf(f)[before]); got != [3]string{"second", "model-two", "low"} {
		t.Fatalf("Retry started %v, want the current default [second model-two low]", got)
	}
}

// A tier the task named is resolved again against the model profile in use when the retry starts.
func TestTaskRetryResolvesTheTaskTierNow(t *testing.T) {
	s, f := restartHub(t)
	saveTier(t, s, "deep", ModelTier{Launcher: "other", Model: "model-zero"})
	id := planRestartTask(t, s, map[string]any{"tier": "deep"})
	saveTier(t, s, "deep", ModelTier{Launcher: "first", Model: "model-one", Effort: "high"})
	todo, first := startRestartPlan(t, s, f, id, errors.New("the example start failed"))
	if got := launchOf(first); got != [3]string{"first", "model-one", "high"} {
		t.Fatalf("first start %v", got)
	}
	saveTier(t, s, "deep", ModelTier{Launcher: "second", Model: "model-two", Effort: "low"})
	if got := launchOf(retryRestart(t, s, f, todo, nil)); got != [3]string{"second", "model-two", "low"} {
		t.Fatalf("task_retry started %v, want tier deep as it is now [second model-two low]", got)
	}
}

// task_retry's own launcher, and its launcher with a model, win for their fields; the rest is not what the failed
// start resolved: the launcher's own default model, no effort of the earlier default.
func TestTaskRetryCallerLauncherAndModelWin(t *testing.T) {
	for _, c := range []struct {
		name  string
		extra map[string]any
		want  [3]string
	}{
		{"launcher", map[string]any{"launcher": "second"}, [3]string{"second", "", ""}},
		{"launcher and model", map[string]any{"launcher": "second", "model": "model-three"}, [3]string{"second", "model-three", ""}},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, f := restartHub(t)
			todo := failedOnceOnFirst(t, s, f, nil)
			if got := launchOf(retryRestart(t, s, f, todo, c.extra)); got != c.want {
				t.Fatalf("task_retry %v started %v, want %v", c.extra, got, c.want)
			}
		})
	}
}

// What the task's own plan named stays the task's: a retry with no launcher keeps it, whatever the default is now.
func TestTaskRetryKeepsWhatThePlanNamed(t *testing.T) {
	s, f := restartHub(t)
	id := planRestartTask(t, s, map[string]any{"launcher": "second", "model": "model-three", "effort": "medium"})
	todo, _ := startRestartPlan(t, s, f, id, errors.New("the example start failed"))
	setModel(t, s, "agent.new", ModelBinding{Provider: "launcher.first", Model: "model-two", Effort: "low"})
	if got := launchOf(retryRestart(t, s, f, todo, nil)); got != [3]string{"second", "model-three", "medium"} {
		t.Fatalf("task_retry started %v, want the plan's own [second model-three medium]", got)
	}
}

// continuedOnFirst plans and starts a task with original as its own launch; its agent runs. The default then moves to
// second (model-two, low).
func continuedOnFirst(t *testing.T, s *Service, f *fakeHub, original map[string]any) string {
	t.Helper()
	todo, _ := startRestartPlan(t, s, f, planRestartTask(t, s, original), nil)
	setModel(t, s, "agent.new", ModelBinding{Provider: "launcher.second", Model: "model-two", Effort: "low"})
	return todo
}

// task_plan with continues: the replacement runs on what this call names, field by field, else on the current
// default; whatever the task it continues named (a launcher, model and effort, or a tier) is not this call's.
func TestTaskPlanContinuesFollowsTheCurrentDefault(t *testing.T) {
	saveTiers := func(t *testing.T, s *Service) {
		t.Helper()
		ps := modelProfiles{Revision: 1, Profiles: []ModelProfile{{ID: "p", Name: "P", Tiers: map[string]ModelTier{
			"deep":  {Launcher: "first", Model: "model-one", Effort: "high"},
			"quick": {Launcher: "second", Model: "model-four", Effort: "medium"},
		}}}}
		if err := s.store().PutSetting(context.Background(), modelProfilesKey, ps); err != nil {
			t.Fatal(err)
		}
		if err := s.store().PutSetting(context.Background(), modelProfileStateKey, modelProfileState{Active: "p"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, original := range []struct {
		name string
		pick map[string]any
	}{
		{"original named launcher, model and effort", map[string]any{"launcher": "first", "model": "model-one", "effort": "high"}},
		{"original named a tier", map[string]any{"tier": "deep"}},
		{"original named nothing", nil},
	} {
		for _, c := range []struct {
			name string
			pick map[string]any
			want [3]string
		}{
			{"none named", nil, [3]string{"second", "model-two", "low"}},
			{"launcher", map[string]any{"launcher": "first"}, [3]string{"first", "", ""}},
			{"tier", map[string]any{"tier": "quick"}, [3]string{"second", "model-four", "medium"}},
			{"model", map[string]any{"model": "model-three"}, [3]string{"second", "model-three", "low"}},
			{"effort", map[string]any{"effort": "medium"}, [3]string{"second", "model-two", "medium"}},
		} {
			t.Run(original.name+"/"+c.name, func(t *testing.T) {
				s, f := restartHub(t)
				saveTiers(t, s)
				todo := continuedOnFirst(t, s, f, original.pick)
				extra := map[string]any{"continues": todo, "continues_why": "the first agent stopped"}
				for k, v := range c.pick {
					extra[k] = v
				}
				before := len(startsOf(f))
				id := planRestartTask(t, s, extra)
				if _, err := s.startPlan(context.Background(), id, nil, false); err != nil {
					t.Fatal(err)
				}
				waitFor(t, "the continuation's start", func() bool { return len(startsOf(f)) > before && s.taskStarting.Load() == 0 })
				if got := launchOf(startsOf(f)[before]); got != c.want {
					t.Fatalf("continuation %v started %v, want %v", c.pick, got, c.want)
				}
			})
		}
	}
}

// A continuation whose own start failed after writing itself back is retried on the current default too.
func TestContinuationRetryFollowsTheCurrentDefault(t *testing.T) {
	s, f := restartHub(t)
	original, _ := startRestartPlan(t, s, f, planRestartTask(t, s, nil), nil)
	todo := failedOnceOnFirst(t, s, f, map[string]any{"continues": original, "continues_why": "the first agent stopped"})
	if todo != original {
		t.Fatalf("the continuation runs on %s, not on the to-do it continues (%s)", todo, original)
	}
	setModel(t, s, "agent.new", ModelBinding{Provider: "launcher.second", Model: "model-two", Effort: "low"})
	if got := launchOf(retryRestart(t, s, f, todo, nil)); got != [3]string{"second", "model-two", "low"} {
		t.Fatalf("task_retry started %v, want the current default [second model-two low]", got)
	}
}

// What one try put in place of the task's launch is that try's own: a flexible task moved to another computer between
// tries, whose later try runs the default there (its launch went back to the task's own after a policy's effort), keeps
// the task's own launch in the plan, not that default.
func TestAnEarlierTrysSwapIsNotALaterTrysDefault(t *testing.T) {
	s, f := defaultsHub(t) // Model configuration › Agents runs gateway (gw-model, high), which box has and l4 lacks
	stubStartDefaults(s, func(StartFacts) (StartDefaults, bool) { return StartDefaults{Effort: "max"}, true })
	ctx := context.Background()
	task := defaultsTask()
	task.Machine, task.Cwd = "l4", "/home/me/code/app"
	if _, _, err := s.startTaskAgent(ctx, &task, false); err != nil {
		t.Fatal(err)
	}
	if st := startsOf(f)[0]; st["launcher"] != "claude" {
		t.Fatalf("l4 started %v", st)
	}
	task.Machine, task.Cwd = "box", "/Users/me/code/app"
	if _, _, err := s.startTaskAgent(ctx, &task, true); err != nil {
		t.Fatal(err)
	}
	if st := startsOf(f)[1]; st["launcher"] != "gateway" || st["model"] != "gw-model" {
		t.Fatalf("box started %v", st)
	}
	if kept := task.storedLaunch(); kept.launcher != "" || kept.model != "" || kept.effort != "" || kept.tier != "" {
		t.Fatalf("the plan keeps %+v, want the task's own launch (none)", kept)
	}
}

// What an engine ran in place of what the task asked for (#8140) is what the task names from then on: its launcher
// (without the tier) and that launcher's own default model, as the card shows it.
func TestEngineInsteadIsWhatThePlanKeeps(t *testing.T) {
	s, f := paseoFitHub(t)
	ctx := context.Background()
	f.startReply = func(params map[string]any, sess *proto.Session) *proto.StartInstead {
		sess.Launcher = "krcc"
		return &proto.StartInstead{Launcher: "claude-sub", Model: "model-three"}
	}
	res, err := s.toolTaskPlan(ctx, "conv9", json.RawMessage(`{"tasks":[{"title":"Ping","permission":"bypassPermissions",
		"prompt":"Reply with pong","machine":"pbox","cwd":"~/code/app","launcher":"claude-sub","model":"model-three","why":"A quick check"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	id := planID(t, res)
	if _, err := s.startPlan(ctx, id, nil, false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the start's bookkeeping", func() bool { return len(startsOf(f)) == 1 && s.taskStarting.Load() == 0 })
	p, err := s.loadPlan(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if pt := p.Tasks[0]; pt.Launcher != "krcc" || pt.Model != "" || pt.Tier != "" || !containsAll(pt.Why, "krcc", "claude-sub") {
		t.Fatalf("the plan keeps %q/%q/%q (%q), want what the engine ran", pt.Launcher, pt.Model, pt.Tier, pt.Why)
	}
}

// A launcher the engine stands in at dispatch for the one the task named (the card could not tell when it was made)
// keeps the task's own model when it offers it: the plan names the stand-in and still the task's model.
func TestDispatchStandInKeepsTheTasksModel(t *testing.T) {
	s, f := paseoFitHub(t)
	ctx := context.Background()
	f.agentModels = map[string][]proto.AgentModel{"codex-cpa": {{ID: "model-three"}}}
	f.mu.Lock()
	host := f.hostLaunchers["pbox"]
	delete(f.hostLaunchers, "pbox") // no engine list while the plan is made
	f.mu.Unlock()
	res, err := s.toolTaskPlan(ctx, "conv9", json.RawMessage(`{"tasks":[{"title":"Ping","permission":"bypassPermissions",
		"prompt":"Reply with pong","machine":"pbox","cwd":"~/code/app","launcher":"codex","model":"model-three","why":"A quick check"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	id := planID(t, res)
	f.mu.Lock()
	f.hostLaunchers["pbox"] = host
	f.mu.Unlock()
	if _, err := s.startPlan(ctx, id, nil, false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the start's bookkeeping", func() bool { return len(startsOf(f)) == 1 && s.taskStarting.Load() == 0 })
	if st := startsOf(f)[0]; st["launcher"] != "codex-cpa" || st["model"] != "model-three" {
		t.Fatalf("started %v", st)
	}
	p, err := s.loadPlan(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if pt := p.Tasks[0]; pt.Launcher != "codex-cpa" || pt.Model != "model-three" || !containsAll(pt.Why, "codex-cpa") {
		t.Fatalf("the plan keeps %q/%q (%q), want the stand-in with the task's model", pt.Launcher, pt.Model, pt.Why)
	}
}

// A launcher the computer has, swapped in at dispatch for a default it lacks (#2661), runs its own default model, not
// the model the task named for the default: the plan names the swapped launcher with no model, as the card shows it, so
// a later retry does not hand that model to the swapped launcher.
func TestDispatchSwapDropsTheTasksModel(t *testing.T) {
	s, f := launcherFitHub(t)
	ctx := context.Background()
	l4 := f.zshrc["l4"]
	f.mu.Lock()
	delete(f.zshrc, "l4") // no list from l4 while the plan is made
	f.mu.Unlock()
	res, err := s.toolTaskPlan(ctx, "conv9", json.RawMessage(`{"tasks":[{"title":"Ping","permission":"bypassPermissions",
		"prompt":"Reply with pong","machine":"l4","cwd":"~/code/app","model":"model-three"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	id := planID(t, res)
	f.mu.Lock()
	f.zshrc["l4"] = l4
	f.mu.Unlock()
	if _, err := s.startPlan(ctx, id, nil, false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the start's bookkeeping", func() bool { return len(startsOf(f)) == 1 && s.taskStarting.Load() == 0 })
	if st := startsOf(f)[0]; st["launcher"] != "claude" || !unset(st["model"]) {
		t.Fatalf("started %v", st)
	}
	p, err := s.loadPlan(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if pt := p.Tasks[0]; pt.Launcher != "claude" || pt.Model != "" || !strings.Contains(pt.Why, "gateway") {
		t.Fatalf("the plan keeps %q/%q (%q), want the swapped launcher with its own default model", pt.Launcher, pt.Model, pt.Why)
	}
}
