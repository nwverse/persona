package assistant

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/kb"
	"github.com/nwverse/persona/server/internal/proto"
)

// skip_project_memory (#13538) is a per-start opt-out of the automatic project and direction handoff. The
// assignment, its criteria and the framework rules are untouched; omitted and false keep today's first prompt.
// All names and notes here are placeholders.

// skipProjectMemoryMarkers are the distinct strings seeded across every automatic project/direction memory section
// (direction notes, project summary, recent outcomes, effective decisions, pitfalls, learned lessons, and extension
// project context) so tests verify each section is present by default and omitted under skip_project_memory=true.
var skipProjectMemoryMarkers = []string{
	"Direction rule: verify rollback steps.",
	"Bounded project summary text.",
	"Merged the weather widget fix",
	"Use beta first",
	"Catches regressions early",
	"Never skip the cold-start smoke test",
	"Run the focused unit test before the full suite.",
	"Extension note for weather project.",
}

// skipProjectMemoryEnv builds a service with a project (天气-app at box:/code/weather) carrying every project and
// direction memory section: direction notes, summary.md, recent outcomes, effective decisions, pitfalls, learned
// lessons (with task_lessons enabled), and extension project_context.
func skipProjectMemoryEnv(t *testing.T) (*Service, *fakeHub) {
	t.Helper()
	st := planUpdateStore{&planMemStore{memStore: &memStore{}, plans: map[string]db.TaskPlanRow{}}}
	s, f, _ := outcomeEnv(t, st, outcomeReply)
	ctx := context.Background()
	f.hello.Locale = "en"
	f.known["conv_lead"] = true
	f.setSessions("server",
		proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle},
		proto.Session{SessionID: "conv_lead", MachineID: "server", Assistant: true, Status: proto.StatusIdle},
	)
	e := kbEng(t, s)
	p, _ := e.Store.Project("天气-app")
	p.Goal = "Ship the weather app on iOS and Android"
	if err := e.Store.PutProject(p); err != nil {
		t.Fatal(err)
	}
	setSettings(t, s, func(v *Settings) {
		v.Main = "conv1"
		v.Leaders = []string{"conv_lead"}
		v.TaskLessons = new(true)
	})
	b := &s.directions
	b.mu.Lock()
	if !s.directionsLocked(ctx) {
		t.Fatal("directions unreadable")
	}
	b.m["conv_lead"] = &direction{MachineID: "server", Project: "天气-app", By: directionByUser, At: time.Now(), Name: "Weather"}
	b.mu.Unlock()
	if _, _, err := s.setDirectionNotes(ctx, "conv_lead", "Direction rule: verify rollback steps.", nil); err != nil {
		t.Fatal(err)
	}
	if err := e.Store.PutSummary(kb.KindProject, "天气-app", "Bounded project summary text."); err != nil {
		t.Fatal(err)
	}
	for _, rec := range []kb.ProjectMemoryRecord{
		{
			ID: "outcome-1", Type: kb.ProjectMemoryOutcome,
			Conclusion:   "Merged the weather widget fix",
			Verification: kb.ProjectMemoryAgentReported, RecordedAt: "2026-11-20T01:00:00Z",
		},
		{
			ID: "decision-1", Type: kb.ProjectMemoryDecision,
			Question: "Which release channel?", Decision: "Use beta first", Reason: "Catches regressions early",
			Verification: kb.ProjectMemoryAgentReported, RecordedAt: "2026-11-20T02:00:00Z",
		},
		{
			ID: "pitfall-1", Type: kb.ProjectMemoryPitfall,
			Conclusion:   "Never skip the cold-start smoke test",
			Verification: kb.ProjectMemoryAgentReported, RecordedAt: "2026-11-20T03:00:00Z",
		},
	} {
		if err := e.AppendProjectMemory("天气-app", rec); err != nil {
			t.Fatal(err)
		}
	}
	appendTestLesson(t, e, "天气-app", "lesson-1", kb.LessonImprovement, "Run the focused unit test before the full suite.", time.Now())
	f.answerApp(AppPlugins, PluginsProjectContext, func(params json.RawMessage) (any, error) {
		return assistantwire.ExtensionContextResult{Blocks: []assistantwire.ExtensionContextBlock{
			{Extension: "notes", Name: "Notes", Project: "天气-app", Text: "Extension note for weather project."},
		}}, nil
	})
	return s, f
}

func assertHasAllProjectMemory(t *testing.T, label, prompt, assignment string) {
	t.Helper()
	if !strings.Contains(prompt, assignment) {
		t.Fatalf("%s: prompt lost assignment %q:\n%s", label, assignment, prompt)
	}
	for _, marker := range skipProjectMemoryMarkers {
		if !strings.Contains(prompt, marker) {
			t.Errorf("%s: prompt missing memory marker %q:\n%s", label, marker, prompt)
		}
	}
}

func assertHasNoProjectMemory(t *testing.T, label, prompt, assignment string) {
	t.Helper()
	if !strings.Contains(prompt, assignment) {
		t.Fatalf("%s: prompt lost assignment %q:\n%s", label, assignment, prompt)
	}
	for _, marker := range skipProjectMemoryMarkers {
		if strings.Contains(prompt, marker) {
			t.Errorf("%s: prompt still contains memory marker %q:\n%s", label, marker, prompt)
		}
	}
	for _, tag := range []string{`title="Direction ·`, `title="Project ·`, `title="方向 ·`, `title="项目 ·`} {
		if strings.Contains(prompt, tag) {
			t.Errorf("%s: prompt still contains project/direction context block %q:\n%s", label, tag, prompt)
		}
	}
}

func assertProjectMemoryUntouched(t *testing.T, s *Service) {
	t.Helper()
	ctx := context.Background()
	e := kbEng(t, s)
	if got := e.Store.ProjectSummary("天气-app", 200); got != "Bounded project summary text." {
		t.Fatalf("project summary mutated: %q", got)
	}
	mem, ok := e.ProjectMemoryForDispatch("天气-app", 5, 5, 3)
	if !ok || len(mem.Outcomes) != 1 || len(mem.Decisions) != 1 || len(mem.Pitfalls) != 1 {
		t.Fatalf("project memory mutated: ok=%t %+v", ok, mem)
	}
	if lessons := e.ProjectLessons("天气-app").Live; len(lessons) != 1 || lessons[0].Conclusion != "Run the focused unit test before the full suite." {
		t.Fatalf("project lessons mutated: %+v", lessons)
	}
	if d := s.directionOf("conv_lead"); d == nil || d.Notes != "Direction rule: verify rollback steps." {
		t.Fatalf("direction notes mutated: %+v", d)
	}
	if !s.taskLessons(ctx) {
		t.Fatal("assistant.task_lessons global setting was turned off")
	}
}

// task_plan honors skip_project_memory: omitted and false stay byte-identical and carry every project/direction memory
// section; true omits all automatic project/direction memory for that start only while keeping the assignment intact
// and leaving stored memory, settings and later Agents untouched (#13538).
func TestTaskPlanSkipProjectMemory(t *testing.T) {
	s, f := skipProjectMemoryEnv(t)
	ctx := context.Background()
	const assignment = "Audit the parser edge cases independently."

	runPlan := func(opt *bool) string {
		t.Helper()
		f.mu.Lock()
		before := len(f.starts)
		f.mu.Unlock()
		task := map[string]any{
			"title":          "Independent check",
			"prompt":         assignment,
			"role":           "worker",
			"machine":        "box",
			"cwd":            "/code/weather",
			"readonly":       true,
			"user_requested": true,
			"goal":           "Verify parser edge cases",
			"why":            "repo is on box",
			"project":        "天气-app",
		}
		if opt != nil {
			task["skip_project_memory"] = *opt
		}
		raw, err := json.Marshal(map[string]any{"tasks": []map[string]any{task}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.toolTaskPlanFrom(ctx, "server", "conv_lead", raw); err != nil {
			t.Fatalf("task_plan (opt=%v): %v", opt, err)
		}
		waitFor(t, "task_plan start", func() bool {
			f.mu.Lock()
			defer f.mu.Unlock()
			return len(f.starts) == before+1
		})
		f.mu.Lock()
		defer f.mu.Unlock()
		prompt, _ := f.starts[before]["prompt"].(string)
		return prompt
	}

	promptOmitted := runPlan(nil)
	promptFalse := runPlan(new(false))
	promptTrue := runPlan(new(true))
	promptAfter := runPlan(nil)

	if promptOmitted != promptFalse {
		t.Fatalf("omitted and false task_plan prompts differ:\n--- omitted ---\n%s\n--- false ---\n%s", promptOmitted, promptFalse)
	}
	assertHasAllProjectMemory(t, "task_plan omitted", promptOmitted, assignment)
	assertHasNoProjectMemory(t, "task_plan true", promptTrue, assignment)
	if !strings.HasPrefix(promptTrue, assignment) {
		t.Fatalf("task_plan true did not preserve assignment at start of prompt:\n%s", promptTrue)
	}
	if promptAfter != promptOmitted {
		t.Fatalf("subsequent task_plan changed after skip_project_memory=true:\n--- before ---\n%s\n--- after ---\n%s", promptOmitted, promptAfter)
	}
	assertProjectMemoryUntouched(t, s)
}

// agent_start honors skip_project_memory: omitted and false stay byte-identical and carry every project/direction
// memory section; true omits all automatic project/direction memory for that start only while keeping the caller's
// prompt unchanged and leaving stored memory, settings and later Agents untouched (#13538).
func TestAgentStartSkipProjectMemory(t *testing.T) {
	s, f := skipProjectMemoryEnv(t)
	ctx := context.Background()
	const assignment = "Check the weather cache invalidation path independently."

	runStart := func(opt *bool) string {
		t.Helper()
		f.mu.Lock()
		before := len(f.starts)
		f.mu.Unlock()
		args := map[string]any{
			"machine": "box",
			"cwd":     "/code/weather",
			"prompt":  assignment,
		}
		if opt != nil {
			args["skip_project_memory"] = *opt
		}
		raw, err := json.Marshal(args)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.runTool(ctx, "server", "conv_lead", "agent_start", raw); err != nil {
			t.Fatalf("agent_start (opt=%v): %v", opt, err)
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		if len(f.starts) != before+1 {
			t.Fatalf("starts = %d, want %d", len(f.starts), before+1)
		}
		prompt, _ := f.starts[before]["prompt"].(string)
		return prompt
	}

	promptOmitted := runStart(nil)
	promptFalse := runStart(new(false))
	promptTrue := runStart(new(true))
	promptAfter := runStart(nil)

	if promptOmitted != promptFalse {
		t.Fatalf("omitted and false agent_start prompts differ:\n--- omitted ---\n%s\n--- false ---\n%s", promptOmitted, promptFalse)
	}
	assertHasAllProjectMemory(t, "agent_start omitted", promptOmitted, assignment)
	assertHasNoProjectMemory(t, "agent_start true", promptTrue, assignment)
	if !strings.HasPrefix(promptTrue, assignment) {
		t.Fatalf("agent_start true did not start directly with assignment:\n%s", promptTrue)
	}
	if promptAfter != promptOmitted {
		t.Fatalf("subsequent agent_start changed after skip_project_memory=true:\n--- before ---\n%s\n--- after ---\n%s", promptOmitted, promptAfter)
	}
	assertProjectMemoryUntouched(t, s)
}

// A task leader's restricted task_plan also honors skip_project_memory so a leader can request a clean worker check
// without injecting its project or direction memory (#13538).
func TestLeaderTaskPlanSkipProjectMemory(t *testing.T) {
	s, f := skipProjectMemoryEnv(t)
	ctx := context.Background()
	leadTodo, err := s.createTodo(ctx, "Lead weather audit", "Coordinate audit", "conv_lead", "")
	if err != nil {
		t.Fatal(err)
	}
	s.todos.mu.Lock()
	s.todos.todos[leadTodo.ID].Project = "天气-app"
	s.todos.mu.Unlock()
	s.superviseTodo(ctx, leadTodo.ID, "conv_lead", []string{"Audit complete"}, 0)
	if err := s.linkTodoAgentWithSource(ctx, leadTodo.ID, "box", "lead-sess", "Leader", "/code/weather", "claude", "", sourceLeader, ""); err != nil {
		t.Fatal(err)
	}
	const assignment = "Inspect retry backoff without prior context."

	runWorker := func(opt *bool) string {
		t.Helper()
		f.mu.Lock()
		before := len(f.starts)
		f.mu.Unlock()
		task := map[string]any{
			"title":    "Worker check",
			"prompt":   assignment,
			"role":     "worker",
			"readonly": true,
			"goal":     "Verify retry backoff",
		}
		if opt != nil {
			task["skip_project_memory"] = *opt
		}
		raw, _ := json.Marshal(map[string]any{"tasks": []map[string]any{task}})
		res := s.leaderTaskPlan(ctx, "box", "lead-sess", raw)
		if res.IsError {
			t.Fatalf("leaderTaskPlan (opt=%v) error: %+v", opt, res.Content)
		}
		waitFor(t, "leader worker start", func() bool {
			f.mu.Lock()
			defer f.mu.Unlock()
			return len(f.starts) == before+1
		})
		f.mu.Lock()
		defer f.mu.Unlock()
		prompt, _ := f.starts[before]["prompt"].(string)
		return prompt
	}

	promptOmitted := runWorker(nil)
	promptFalse := runWorker(new(false))
	promptTrue := runWorker(new(true))

	if promptOmitted != promptFalse {
		t.Fatalf("leader task_plan omitted and false differ:\n--- omitted ---\n%s\n--- false ---\n%s", promptOmitted, promptFalse)
	}
	assertHasAllProjectMemory(t, "leader task_plan omitted", promptOmitted, assignment)
	assertHasNoProjectMemory(t, "leader task_plan true", promptTrue, assignment)
	assertProjectMemoryUntouched(t, s)
}

// skip_project_memory survives plan persistence and card approval (with edits that do not name it), and also omits
// legacy project background (OneLine + log.md outcomes) when a project has no memory.jsonl (#13538).
func TestSkipProjectMemoryCardApprovalAndLegacyProject(t *testing.T) {
	st := planUpdateStore{&planMemStore{memStore: &memStore{}, plans: map[string]db.TaskPlanRow{}}}
	s, f, todo := outcomeEnv(t, st, outcomeReply)
	ctx := context.Background()
	if err := kbEng(t, s).Store.AppendLog("天气-app", time.Now(), kb.TodoLogSource+kb.TodoLogTitle(todo.Title), "结论：旧日志结论"); err != nil {
		t.Fatal(err)
	}
	const assignment = "Implement the clean-slate parser fix."

	// Writable task with user_requested=false stays open on a plan card until approved via startPlan.
	raw, _ := json.Marshal(map[string]any{"tasks": []map[string]any{{
		"title":               "Clean fix",
		"prompt":              assignment,
		"role":                "worker",
		"machine":             "box",
		"cwd":                 "/code/weather",
		"readonly":            false,
		"user_requested":      false,
		"skip_project_memory": true,
		"done_when":           []string{"Tests pass"},
		"why":                 "repo is on box",
	}}})
	if _, err := s.toolTaskPlanFrom(ctx, "server", "conv1", raw); err != nil {
		t.Fatalf("task_plan: %v", err)
	}
	plans, err := s.conversationPlans(ctx, "conv1")
	if err != nil || len(plans) != 1 {
		t.Fatalf("conversationPlans: err=%v plans=%d", err, len(plans))
	}
	planID := plans[0].ID
	// Approve with a card edit that omits SkipProjectMemory; the task's opt-out must stay intact.
	if _, err := s.startPlan(ctx, planID, []PlanTask{{Title: "Clean fix edited"}}, false); err != nil {
		t.Fatalf("startPlan: %v", err)
	}
	waitFor(t, "approved card start", func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return len(f.starts) == 1
	})
	f.mu.Lock()
	planPrompt, _ := f.starts[0]["prompt"].(string)
	f.mu.Unlock()
	if !strings.Contains(planPrompt, assignment) || strings.Contains(planPrompt, "天气 App 的 iOS 和安卓版") || strings.Contains(planPrompt, "旧日志结论") {
		t.Fatalf("approved task_plan with skip_project_memory=true leaked legacy project background:\n%s", planPrompt)
	}

	// Legacy project on agent_start: omitted includes OneLine + log outcome; true omits both.
	startArgs, _ := json.Marshal(map[string]any{"machine": "box", "cwd": "/code/weather", "prompt": assignment})
	if _, err := s.runTool(ctx, "server", "conv1", "agent_start", startArgs); err != nil {
		t.Fatal(err)
	}
	skipArgs, _ := json.Marshal(map[string]any{"machine": "box", "cwd": "/code/weather", "prompt": assignment, "skip_project_memory": true})
	if _, err := s.runTool(ctx, "server", "conv1", "agent_start", skipArgs); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defaultStartPrompt, _ := f.starts[1]["prompt"].(string)
	skippedStartPrompt, _ := f.starts[2]["prompt"].(string)
	f.mu.Unlock()
	if !strings.Contains(defaultStartPrompt, "天气 App 的 iOS 和安卓版") || !strings.Contains(defaultStartPrompt, "旧日志结论") {
		t.Fatalf("legacy agent_start default missing legacy project context:\n%s", defaultStartPrompt)
	}
	if !strings.HasPrefix(skippedStartPrompt, assignment) || strings.Contains(skippedStartPrompt, "天气 App 的 iOS 和安卓版") || strings.Contains(skippedStartPrompt, "旧日志结论") {
		t.Fatalf("legacy agent_start skip_project_memory=true leaked legacy project context:\n%s", skippedStartPrompt)
	}
}

// The saved plan's JSON round trip retains the opt-out, including what queued starts, retries and reloads read.
// The ordinary assignment, its criteria and read-only constraint still reach the Agent after that round trip.
func TestSkipProjectMemoryPlanRoundTrip(t *testing.T) {
	for _, mode := range []string{"omitted", "false", "true"} {
		t.Run(mode, func(t *testing.T) {
			s, f := skipProjectMemoryEnv(t)
			task := map[string]any{
				"title": "Inspect the layout", "prompt": "Read the current implementation only.", "machine": "box",
				"cwd": "/code/weather", "readonly": true, "why": "the repository is on box", "supervisor": "conv_lead",
				"done_when": []string{"Report the layout findings"}, "expect_minutes": 30,
			}
			if mode != "omitted" {
				task["skip_project_memory"] = mode == "true"
			}
			raw, _ := json.Marshal(task)
			var proposed PlanTask
			if err := json.Unmarshal(raw, &proposed); err != nil {
				t.Fatal(err)
			}
			saved, err := json.Marshal(proposed)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Contains(string(saved), `"skip_project_memory":true`); got != (mode == "true") {
				t.Errorf("saved plan skip_project_memory=true present = %v, want %v: %s", got, mode == "true", saved)
			}
			var reloaded PlanTask
			if err := json.Unmarshal(saved, &reloaded); err != nil {
				t.Fatal(err)
			}
			if _, _, err := s.startTaskAgent(context.Background(), &reloaded, false); err != nil {
				t.Fatal(err)
			}
			f.mu.Lock()
			if len(f.starts) != 1 {
				f.mu.Unlock()
				t.Fatalf("starts = %+v", f.starts)
			}
			prompt, _ := f.starts[0]["prompt"].(string)
			f.mu.Unlock()
			for _, marker := range skipProjectMemoryMarkers {
				if got := strings.Contains(prompt, marker); got != (mode != "true") {
					t.Errorf("memory marker %q present = %v, want %v:\n%s", marker, got, mode != "true", prompt)
				}
			}
			if !strings.HasPrefix(prompt, "Read the current implementation only.") ||
				!strings.Contains(prompt, "Report the layout findings") {
				t.Errorf("the assignment or its criteria changed:\n%s", prompt)
			}
		})
	}
}
