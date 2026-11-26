package assistant

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/kb"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// decisionMemStore wraps an in-memory Store so unit tests can exercise todo
// decision handoff without requiring an external PostgreSQL test database.
type decisionMemStore struct {
	Store
	todoStore
	decisions map[string][]db.TodoDecisionRow
}

func (d *decisionMemStore) ListTodoDecisions(_ context.Context, todoID string, _ bool) ([]db.TodoDecisionRow, error) {
	return d.decisions[todoID], nil
}

func (d *decisionMemStore) LinkTodoAgentWithSource(_ context.Context, _, _, _, _, _, _, _, _, _ string, _ time.Time) ([]string, error) {
	return nil, nil
}

func (d *decisionMemStore) LinkTodoAgentOfDoneTask(_ context.Context, _, _, _, _, _, _, _, _, _ string, _ time.Time) ([]string, error) {
	return nil, nil
}

func installDecisionStore(s *Service, decisions map[string][]db.TodoDecisionRow) {
	wrapped := Store(&decisionMemStore{Store: s.store(), decisions: decisions})
	fn := func() Store { return wrapped }
	s.storeFn.Store(&fn)
}

func multiEngineLaunchers() []proto.Launcher {
	return append(companionLaunchers(), proto.Launcher{Name: "codex", Agent: "codex"})
}

func TestAgySlashPromptPreservedInTaskPlan(t *testing.T) {
	ctx := context.Background()
	s, f, todo := outcomeEnv(t, nil, outcomeReply)
	f.mu.Lock()
	f.launcherList = multiEngineLaunchers()
	f.mu.Unlock()
	f.setSessions("box")
	closeoutOn(t, s)

	installDecisionStore(s, map[string][]db.TodoDecisionRow{
		todo.ID: {{
			ID: "dec_slash_1", TodoID: todo.ID, Question: "Transport?", Choice: "ACP",
			Reason: "Native protocol", Source: db.DecisionManual,
			CreatedAt: time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC),
		}},
	})
	grant := dispatchAuthorization()
	grant.Directory = "/code/weather"
	if err := s.store().PutSetting(ctx, standingAuthorizationsKey, StandingAuthorizations{
		Revision: 1, Authorizations: []StandingAuthorization{grant},
	}); err != nil {
		t.Fatalf("save standing authorizations: %v", err)
	}
	store := kbEng(t, s).Store
	if err := store.AppendLog("天气-app", time.Date(2026, time.November, 1, 12, 0, 0, 0, store.Zone()),
		kb.TodoLogSource+kb.TodoLogTitle(todo.Title), "结论：已完成并发锁改造"); err != nil {
		t.Fatal(err)
	}
	const handoffSummary = "Previous attempt identified a race in lock acquisition."
	if _, _, err := s.persistTodoContextHandoff(ctx, TodoContextHandoff{
		ID: "hoff_slash_1", OperationID: "op_slash_1", TodoID: todo.ID,
		Strategy: "summary", Generator: "test", Summary: handoffSummary,
		DeliveryStatus: "pending", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("persist handoff: %v", err)
	}

	readonlyRule := prompts.RenderReadonlyTaskConstraint(answerLanguage(f.hello.Locale))

	for _, tc := range []struct {
		name       string
		prompt     string
		wantPrefix string
	}{
		{"boost", "/boost inspect the lock ordering\nadd a regression test", "/boost inspect the lock ordering\nadd a regression test"},
		{"swarm", "/swarm coordinate analysis workers\nsynthesize findings", "/swarm coordinate analysis workers\nsynthesize findings"},
		{"teamwork", "/teamwork split backend and tests\nverify race detector", "/teamwork split backend and tests\nverify race detector"},
		{"goal with leading space", "  /goal finish migration\nkeep checks green", "/goal finish migration\nkeep checks green"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.mu.Lock()
			f.starts = nil
			f.mu.Unlock()
			task := PlanTask{
				Title:     "Inspect " + tc.name,
				Prompt:    tc.prompt,
				Machine:   "box",
				Cwd:       "/code/weather",
				Launcher:  "agy",
				TodoID:    todo.ID,
				HandoffID: "hoff_slash_1",
				Context:   "Focus on concurrent state transitions.",
				Readonly:  true,
				DoneWhen:  []string{"root cause documented"},
			}
			if _, _, err := s.startTaskAgent(ctx, &task, false); err != nil {
				t.Fatalf("startTaskAgent: %v", err)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.starts) != 1 {
				t.Fatalf("starts = %d, want 1", len(f.starts))
			}
			got, _ := f.starts[0]["prompt"].(string)
			if !strings.HasPrefix(got, tc.wantPrefix) {
				t.Fatalf("prompt did not preserve %q at byte 0:\n%s", tc.wantPrefix, got)
			}
			for _, mustContain := range []string{
				handoffSummary,
				task.Context,
				readonlyRule,
				"root cause documented",
				"Transport?",
				"ACP",
				"已完成并发锁改造",
				`<closeout status="done|not_done|blocked"`,
			} {
				if !strings.Contains(got, mustContain) {
					t.Errorf("prompt dropped required constraint/context %q:\n%s", mustContain, got)
				}
			}
			assertDispatchAuthorization(t, got, grant, true)
		})
	}

	t.Run("non-allowlisted multiline path keeps normal prepend order", func(t *testing.T) {
		f.mu.Lock()
		f.starts = nil
		f.mu.Unlock()
		pathPrompt := "/Users/example/repo/main.go\ncheck line 42"
		task := PlanTask{
			Title:     "Inspect path",
			Prompt:    pathPrompt,
			Machine:   "box",
			Cwd:       "/code/weather",
			Launcher:  "agy",
			TodoID:    todo.ID,
			HandoffID: "hoff_slash_1",
			Readonly:  true,
		}
		if _, _, err := s.startTaskAgent(ctx, &task, false); err != nil {
			t.Fatalf("startTaskAgent: %v", err)
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		got, _ := f.starts[0]["prompt"].(string)
		if !strings.HasPrefix(got, handoffSummary+"\n\n"+pathPrompt) {
			t.Fatalf("non-allowlisted path changed prepend order:\n%s", got)
		}
	})

	// Non-AGY engines (Claude Code, Codex) must keep original prepend order and leading whitespace untouched.
	for _, launcher := range []string{"claude", "codex"} {
		t.Run("non-agy launcher "+launcher+" keeps original prepend order", func(t *testing.T) {
			f.mu.Lock()
			f.starts = nil
			f.mu.Unlock()
			const slashPrompt = "  /plan review the design\nkeep rollback notes"
			task := PlanTask{
				Title:     "Non-AGY " + launcher,
				Prompt:    slashPrompt,
				Machine:   "box",
				Cwd:       "/code/weather",
				Launcher:  launcher,
				TodoID:    todo.ID,
				HandoffID: "hoff_slash_1",
				Readonly:  true,
			}
			if _, _, err := s.startTaskAgent(ctx, &task, false); err != nil {
				t.Fatalf("startTaskAgent(%s): %v", launcher, err)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			got, _ := f.starts[0]["prompt"].(string)
			if !strings.HasPrefix(got, handoffSummary+"\n\n"+slashPrompt) {
				t.Fatalf("non-AGY launcher %s unexpectedly reordered or trimmed prompt:\n%s", launcher, got)
			}
		})
	}
}

func TestAgySlashPromptPreservedInAgentStart(t *testing.T) {
	ctx := context.Background()
	s, f, todo := outcomeEnv(t, nil, outcomeReply)
	f.mu.Lock()
	f.launcherList = multiEngineLaunchers()
	f.mu.Unlock()
	closeoutOn(t, s)

	store := kbEng(t, s).Store
	if err := store.AppendLog("天气-app", time.Date(2026, time.November, 1, 12, 0, 0, 0, store.Zone()),
		kb.TodoLogSource+kb.TodoLogTitle(todo.Title), "结论：已完成缓存改造"); err != nil {
		t.Fatal(err)
	}

	t.Run("allowlisted slash command stays at byte 0 with project context and closeout intact", func(t *testing.T) {
		f.mu.Lock()
		f.starts = nil
		f.mu.Unlock()
		args, err := json.Marshal(map[string]any{
			"machine":  "box",
			"cwd":      "/code/weather",
			"launcher": "agy",
			"prompt":   "  /boost diagnose cache invalidation\nkeep existing API",
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.runTool(ctx, "server", "conv9", "agent_start", args); err != nil {
			t.Fatalf("agent_start: %v", err)
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		if len(f.starts) != 1 {
			t.Fatalf("starts = %d, want 1", len(f.starts))
		}
		got, _ := f.starts[0]["prompt"].(string)
		const wantHead = "/boost diagnose cache invalidation\nkeep existing API"
		if !strings.HasPrefix(got, wantHead) {
			t.Fatalf("agent_start prompt lost byte-0 slash command:\n%s", got)
		}
		for _, mustContain := range []string{"已完成缓存改造", `<closeout status="done|not_done|blocked"`} {
			if !strings.Contains(got, mustContain) {
				t.Errorf("agent_start prompt dropped %q:\n%s", mustContain, got)
			}
		}
	})

	t.Run("non-allowlisted slash path keeps project context prepended", func(t *testing.T) {
		f.mu.Lock()
		f.starts = nil
		f.mu.Unlock()
		const pathPrompt = "/Users/example/weather/cache.go\ninspect eviction"
		args, err := json.Marshal(map[string]any{
			"machine":  "box",
			"cwd":      "/code/weather",
			"launcher": "agy",
			"prompt":   pathPrompt,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.runTool(ctx, "server", "conv9", "agent_start", args); err != nil {
			t.Fatalf("agent_start: %v", err)
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		got, _ := f.starts[0]["prompt"].(string)
		if strings.HasPrefix(got, pathPrompt) || !strings.Contains(got, "已完成缓存改造") || !strings.Contains(got, "\n\n"+pathPrompt) {
			t.Fatalf("non-allowlisted path did not keep project context prepended:\n%s", got)
		}
	})

	for _, launcher := range []string{"claude", "codex"} {
		t.Run("non-agy launcher "+launcher+" keeps project context prepended", func(t *testing.T) {
			f.mu.Lock()
			f.starts = nil
			f.mu.Unlock()
			const slashPrompt = "  /plan inspect cache invalidation\nkeep existing API"
			args, err := json.Marshal(map[string]any{
				"machine":  "box",
				"cwd":      "/code/weather",
				"launcher": launcher,
				"prompt":   slashPrompt,
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.runTool(ctx, "server", "conv9", "agent_start", args); err != nil {
				t.Fatalf("agent_start(%s): %v", launcher, err)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			got, _ := f.starts[0]["prompt"].(string)
			if strings.HasPrefix(got, strings.TrimSpace(slashPrompt)) || !strings.Contains(got, "已完成缓存改造") || !strings.Contains(got, "\n\n"+slashPrompt) {
				t.Fatalf("non-AGY launcher %s unexpectedly reordered or trimmed agent_start prompt:\n%s", launcher, got)
			}
		})
	}
}

func TestAgySlashPromptPreservedInTodoHandoffAndSuccessor(t *testing.T) {
	ctx := context.Background()
	s, f, todo := outcomeEnv(t, nil, outcomeReply)
	f.setSessions("box")
	store := kbEng(t, s).Store
	if err := store.AppendLog("天气-app", time.Date(2026, time.November, 1, 12, 0, 0, 0, store.Zone()),
		kb.TodoLogSource+kb.TodoLogTitle(todo.Title), "结论：先统一接口字段"); err != nil {
		t.Fatal(err)
	}
	installDecisionStore(s, map[string][]db.TodoDecisionRow{
		todo.ID: {{
			ID: "dec_handoff_1", TodoID: todo.ID, Question: "Release track?", Choice: "Canary first",
			Reason: "Reduce blast radius", Source: db.DecisionManual,
			CreatedAt: time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC),
		}},
	})

	handoffPrompt := "/boost continue rollout\nverify telemetry"
	res, err := s.TodoHandoff(ctx, TodoHandoff{TodoID: todo.ID, MachineID: "box", Cwd: "/code/weather", Text: handoffPrompt, New: true, Agent: "agy"})
	if err != nil {
		t.Fatalf("TodoHandoff(agy): %v", err)
	}
	if !strings.HasPrefix(res.Text, handoffPrompt) {
		t.Fatalf("TodoHandoff(agy) lost byte-0 slash command:\n%s", res.Text)
	}
	for _, mustContain := range []string{"Canary first", "先统一接口字段"} {
		if !strings.Contains(res.Text, mustContain) {
			t.Errorf("TodoHandoff(agy) dropped %q:\n%s", mustContain, res.Text)
		}
	}

	// Non-AGY and unspecified agents keep original prepend order and leading whitespace.
	for _, agent := range []string{"claude", "codex", ""} {
		const nonAgyPrompt = "  /plan continue rollout\nverify telemetry"
		got, err := s.TodoHandoff(ctx, TodoHandoff{TodoID: todo.ID, MachineID: "box", Cwd: "/code/weather", Text: nonAgyPrompt, New: true, Agent: agent})
		if err != nil {
			t.Fatalf("TodoHandoff(%q): %v", agent, err)
		}
		if !strings.HasSuffix(got.Text, "\n\n"+nonAgyPrompt) || !strings.Contains(got.Text, "Canary first") || !strings.Contains(got.Text, "先统一接口字段") {
			t.Fatalf("TodoHandoff(%q) unexpectedly reordered non-AGY prompt:\n%s", agent, got.Text)
		}
	}

	f.mu.Lock()
	f.starts = nil
	f.mu.Unlock()
	successorPrompt := "/teamwork finish remaining checks\nreport coverage"
	if _, err := s.startTaskSuccessor(ctx, todoNow(s, todo.ID), proto.Session{
		MachineID: "box", SessionID: "old-agy", Cwd: "/code/weather",
		Launcher: "agy", Agent: "agy", LastText: "Completed phase 1 migration",
	}, successorPrompt); err != nil {
		t.Fatalf("startTaskSuccessor(agy): %v", err)
	}
	f.mu.Lock()
	if len(f.starts) != 1 {
		f.mu.Unlock()
		t.Fatalf("starts = %d, want 1", len(f.starts))
	}
	got, _ := f.starts[0]["prompt"].(string)
	f.mu.Unlock()
	if !strings.HasPrefix(got, successorPrompt) {
		t.Fatalf("startTaskSuccessor(agy) lost byte-0 slash command:\n%s", got)
	}
	for _, mustContain := range []string{"Completed phase 1 migration", "Canary first", "file:"} {
		if !strings.Contains(got, mustContain) {
			t.Errorf("startTaskSuccessor(agy) dropped %q:\n%s", mustContain, got)
		}
	}

	// Non-AGY successors (Claude/Codex) must keep original RenderTaskSuccessor + decisions prepend order.
	for _, engine := range []string{"claude", "codex"} {
		f.mu.Lock()
		f.starts = nil
		f.mu.Unlock()
		const nonAgySuccessor = "  /plan finish remaining checks\nreport coverage"
		if _, err := s.startTaskSuccessor(ctx, todoNow(s, todo.ID), proto.Session{
			MachineID: "box", SessionID: "old-" + engine, Cwd: "/code/weather",
			Launcher: engine, Agent: engine, LastText: "Completed phase 1 migration",
		}, nonAgySuccessor); err != nil {
			t.Fatalf("startTaskSuccessor(%s): %v", engine, err)
		}
		f.mu.Lock()
		gotNonAgy, _ := f.starts[0]["prompt"].(string)
		f.mu.Unlock()
		if !strings.HasSuffix(gotNonAgy, nonAgySuccessor) || strings.HasPrefix(gotNonAgy, strings.TrimSpace(nonAgySuccessor)) {
			t.Fatalf("startTaskSuccessor(%s) unexpectedly reordered non-AGY prompt:\n%s", engine, gotNonAgy)
		}
	}
}

func TestAgySlashPromptPreservedInMainHandoff(t *testing.T) {
	s, _ := newTaskService(t)
	ctx := context.Background()
	handoff := mainHandoff{
		From:    "conv-old-1",
		Until:   time.Date(2026, 11, 20, 12, 30, 0, 0, time.UTC),
		Summary: "Previous main chat tracked release checklist.",
		Recent: []struct {
			Role string `json:"role"`
			Text string `json:"text"`
		}{{Role: "user", Text: "Keep rollback plan updated"}},
	}

	slashMsg := "/boost review release checklist\nflag high-risk items"
	got := s.renderHandoff(ctx, handoff, slashMsg, true)
	if !strings.HasPrefix(got, slashMsg) {
		t.Fatalf("renderHandoff(agy) lost byte-0 slash command:\n%s", got)
	}
	for _, mustContain := range []string{`<persona-context kind="context" title="接上旧主对话"`, handoff.Summary, "Keep rollback plan updated"} {
		if !strings.Contains(got, mustContain) {
			t.Errorf("renderHandoff(agy) dropped %q:\n%s", mustContain, got)
		}
	}

	// Non-AGY main chats must NOT reorder slash commands.
	nonAgy := s.renderHandoff(ctx, handoff, "  /plan review release checklist\nflag high-risk items", false)
	if !strings.HasPrefix(nonAgy, `<persona-context kind="context" title="接上旧主对话"`) || !strings.HasSuffix(nonAgy, "  /plan review release checklist\nflag high-risk items") {
		t.Fatalf("non-AGY main handoff unexpectedly reordered slash command:\n%s", nonAgy)
	}

	plain := s.renderHandoff(ctx, handoff, "ordinary message", true)
	if !strings.HasPrefix(plain, `<persona-context kind="context" title="接上旧主对话"`) || !strings.HasSuffix(plain, "ordinary message") {
		t.Fatalf("ordinary main handoff order changed:\n%s", plain)
	}
}
