package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// Forked background supervisor round sessions (tracked via deliverRelay's returned session_id or via node
// ParentSessionID/RootSessionID linkage after a restart) inherit full assistant authorization and supervision tools
// (task_review, agent_send, supervision_finish, notify_user), even when their listed cwd drifted or when the
// background supervisor runs on BackgroundSupervisorMachineID. Host-pane sessions and work machines remain denied.
func TestBackgroundForkedRoundInheritsAuthAndSupervisionTools(t *testing.T) {
	s, d, f, _ := privacyService(t)
	ctx := context.Background()
	at := time.Now().Add(-time.Hour).UTC()
	setSettings(t, s, func(v *Settings) {
		v.MachineID = "server"
		v.Main = "conv1"
		v.BackgroundSupervisor = "bg1"
		v.BackgroundSupervisorMachineID = "twin"
		v.BackgroundSupervisorAt = &at
	})
	// Seed a supervised task owned by conv1 so task_review can be called by the background supervisor's forked round.
	todo := Todo{
		ID:     "todo-fork-1",
		Title:  "Investigate flaky check",
		Origin: "conv1",
		Agents: []TodoAgent{{MachineID: "other", SessionID: "agent1"}},
		Supervision: &Supervision{
			Supervisor: "conv1",
			State:      "running",
			DoneWhen:   []string{"Root cause documented"},
		},
	}
	if err := s.ensureTodosLoaded(ctx); err != nil {
		t.Fatal(err)
	}
	s.todos.mu.Lock()
	s.todos.todos[todo.ID] = &todo
	s.todos.mu.Unlock()
	s.supervise.mu.Lock()
	s.supervise.init()
	s.supervise.hold["bg1"] = time.Now()
	s.supervise.mu.Unlock()

	// Case 1: forked round id returned by the node on deliverRelay (claude --fork-session / round branch).
	const forkFromRelay = "bg1-round-fork-1"
	f.setSessions("twin")
	f.mu.Lock()
	f.known["bg1"] = true
	f.assistantAnswer = func(a map[string]any) map[string]any {
		return map[string]any{"session_id": forkFromRelay, "context": "fresh"}
	}
	f.mu.Unlock()
	if err := s.deliverRelay(ctx, "twin", "bg1", relayMsg{
		From: superviseTool,
		Tool: superviseTool,
		Text: prompts.SuperviseRoundMarker + "\n📋 督办：检查任务",
		At:   time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	// Case 2: forked round discovered only from node session ParentSessionID (e.g. after manager restart) with drifted cwd.
	const forkFromParent = "bg1-round-fork-2"
	const paneSession = "bg1-pane-spoof"
	d.setSessions("twin", []proto.Session{
		{SessionID: "bg1", MachineID: "twin", Cwd: "/home/u/.persona/assistant", Status: proto.StatusIdle},
		{SessionID: forkFromRelay, MachineID: "twin", Cwd: "/code/example", Status: proto.StatusWorking},
		{SessionID: forkFromParent, MachineID: "twin", ParentSessionID: "bg1", Cwd: "/code/example", Status: proto.StatusWorking},
		{SessionID: paneSession, MachineID: "twin", ParentSessionID: "bg1", Cwd: "/home/u/.persona/assistant", PaneID: "pane-1", Status: proto.StatusWorking},
	})
	d.setSessions("work", []proto.Session{
		{SessionID: forkFromRelay, MachineID: "work", ParentSessionID: "bg1", Cwd: "/code/example", Status: proto.StatusWorking},
	})
	s.setSessions("twin", d.machines["twin"].sessions)

	for _, sid := range []string{forkFromRelay, forkFromParent} {
		t.Run("authorized/"+sid, func(t *testing.T) {
			if !s.assistantCaller(ctx, "twin", sid) {
				t.Fatalf("assistantCaller(twin, %s) = false, want true", sid)
			}
			if !s.assistantDiscovery(ctx, "twin", sid) {
				t.Fatalf("assistantDiscovery(twin, %s) = false, want true", sid)
			}
			out := privacyRequest(t, s, "twin", "mcp.tools", map[string]any{
				"session_id": sid,
				"assistant":  true,
				"tool_set":   toolSetSupervise,
			})
			var names []string
			for _, tool := range out.(map[string]any)["tools"].([]proto.Tool) {
				names = append(names, tool.Name)
			}
			for _, want := range []string{"task_review", "agent_send", "supervision_finish", "notify_user", "todo_get"} {
				if !slices.Contains(names, want) {
					t.Fatalf("supervise tool list for %s missing %q: %v", sid, want, names)
				}
			}
			for _, unwanted := range []string{"report_to_supervisor", "todo_decisions", "todo_decision_add", "todo_bind_thread"} {
				if slices.Contains(names, unwanted) {
					t.Fatalf("supervise tool list for %s downgraded to ordinary agent tool %q: %v", sid, unwanted, names)
				}
			}
		})
	}

	// Host-pane sessions and work machines remain strictly denied even if they claim ParentSessionID == bg1.
	if s.assistantCaller(ctx, "twin", paneSession) {
		t.Fatal("host-pane session was authorized as background supervisor")
	}
	if s.assistantCaller(ctx, "work", forkFromRelay) {
		t.Fatal("work machine session was authorized as background supervisor")
	}

	// Calling supervision_finish (silent before promotion) and task_review through mcp.call from forked rounds succeeds
	// (no assistant_denied) and resolves the base supervisor's supervision hold.
	resFinish := privacyRequest(t, s, "twin", "mcp.call", map[string]any{
		"session_id": forkFromParent,
		"assistant":  true,
		"name":       "supervision_finish",
		"args":       map[string]any{"visibility": "silent", "summary": "Round finished"},
	}).(proto.ToolResult)
	if resFinish.IsError {
		t.Fatalf("supervision_finish from forked round failed: %+v", resFinish)
	}
	if len(resFinish.Content) == 0 || !strings.Contains(resFinish.Content[0].Text, `"level":"triage"`) {
		t.Fatalf("supervision_finish unexpected result: %+v", resFinish)
	}

	resReview := privacyRequest(t, s, "twin", "mcp.call", map[string]any{
		"session_id": forkFromRelay,
		"assistant":  true,
		"name":       "task_review",
		"args": map[string]any{
			"todo_id": todo.ID,
			"verdict": "accepted",
			"note":    "Verified",
			"checks":  completeReviewChecks(todoNow(s, todo.ID)),
		},
	}).(proto.ToolResult)
	if resReview.IsError {
		t.Fatalf("task_review from forked round failed: %+v", resReview)
	}
	if done := todoNow(s, todo.ID); done.DoneAt == nil {
		t.Fatalf("task_review from forked round did not accept todo: %+v", done)
	}
}

// Forked background supervisor round branches do not leak into the published session list (visible or archived) or the
// agents_list tool output, while ordinary agents and the base background supervisor remain intact.
func TestBackgroundForkedRoundsExcludedFromAgentsList(t *testing.T) {
	s, d, f, _ := privacyService(t)
	ctx := context.Background()
	at := time.Now().Add(-time.Hour).UTC()
	setSettings(t, s, func(v *Settings) {
		v.MachineID = "server"
		v.Main = "conv1"
		v.BackgroundSupervisor = "bg1"
		v.BackgroundSupervisorMachineID = "server"
		v.BackgroundSupervisorAt = &at
	})
	s.registerBackgroundRoundFork(ctx, "server", "bg1-fork-a", "bg1")
	now := time.Now()
	list := []proto.Session{
		{SessionID: "conv1", MachineID: "server", Cwd: "/home/u/.persona/assistant", Status: proto.StatusIdle, Assistant: true, UpdatedAt: now},
		{SessionID: "bg1", MachineID: "server", Cwd: "/home/u/.persona/assistant", Status: proto.StatusIdle, Assistant: true, UpdatedAt: now},
		{SessionID: "bg1-fork-a", MachineID: "server", Cwd: "/code/example", Status: proto.StatusWorking, State: proto.StateWorking, UpdatedAt: now},
		{SessionID: "bg1-fork-b", MachineID: "server", ParentSessionID: "bg1", Cwd: "/code/example", Status: proto.StatusWorking, State: proto.StateWorking, UpdatedAt: now},
		{SessionID: "agent-real", MachineID: "server", Cwd: "/code/app", Status: proto.StatusWorking, State: proto.StateWorking, Title: "Real Agent", UpdatedAt: now},
	}
	d.setSessions("server", list)
	f.setSessions("server", list...)
	s.setSessions("server", list)

	published := s.publishList("server")
	var ids []string
	for _, row := range published {
		ids = append(ids, row.SessionID)
	}
	for _, hidden := range []string{"bg1-fork-a", "bg1-fork-b"} {
		if slices.Contains(ids, hidden) {
			t.Fatalf("publishList leaked forked round %q: %v", hidden, ids)
		}
	}
	for _, kept := range []string{"conv1", "bg1", "agent-real"} {
		if !slices.Contains(ids, kept) {
			t.Fatalf("publishList dropped %q: %v", kept, ids)
		}
	}

	out, err := runJSON(t, s, "conv1", "agents_list", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	for _, hidden := range []string{"bg1-fork-a", "bg1-fork-b"} {
		if strings.Contains(out, hidden) {
			t.Fatalf("agents_list leaked forked round %q:\n%s", hidden, out)
		}
	}
	if !strings.Contains(out, "agent-real") {
		t.Fatalf("agents_list missing real agent:\n%s", out)
	}
}

// setupAllStoppedBackground creates a background-supervisor test service with a task whose agent has been stopped for
// superviseAllStoppedAfter so sendDigest emits an all_stopped signal. Calling nextSignal shifts the agent's stop
// timestamp so allStoppedSignal produces a fresh valid key that passes checkedAllStopped.
func setupAllStoppedBackground(t *testing.T) (*Service, *fakeHub, Todo, string, func(step int) superviseSignal) {
	t.Helper()
	s, f, todo, _ := newRoundService(t)
	presenceTimes(t, 0, 0)
	ownerTurnTimes(t, 0, 0)
	bg := withBackgroundBG1(t, s, f)
	tNow := todoNow(s, todo.ID)
	tNow.Supervision.ExpectMinutes = 1440
	s.todos.mu.Lock()
	s.todos.todos[todo.ID] = &tNow
	s.todos.mu.Unlock()
	baseStop := time.Now().Add(-superviseAllStoppedAfter - 10*time.Minute)
	nextSignal := func(step int) superviseSignal {
		t.Helper()
		stoppedAt := baseStop.Add(time.Duration(step) * time.Minute)
		agent := proto.Session{
			MachineID:  "box",
			SessionID:  "s1",
			Status:     proto.StatusDone,
			State:      proto.StateUnread,
			StateSince: stoppedAt,
			UpdatedAt:  stoppedAt,
			LastText:   "Stopped waiting for review",
		}
		f.setSessions("box", agent)
		g, ok := allStoppedSignal(todoNow(s, todo.ID), nil, []proto.Session{agent}, time.Now())
		if !ok {
			t.Fatalf("step %d: fixture did not produce all_stopped signal", step)
		}
		return g
	}
	return s, f, tNow, bg, nextSignal
}

// If a background supervisor round hits assistant_denied, the circuit breaker trips immediately: the next all_stopped
// wake falls back once to the main conversation with a clear note instead of waking the background supervisor, and
// subsequent wakes within the backoff window are suppressed.
func TestBackgroundBreakerTripsOnAssistantDeniedAndFallsBackOnce(t *testing.T) {
	s, f, _, bg, nextSignal := setupAllStoppedBackground(t)
	ctx := context.Background()

	// Round 1 wakes the background supervisor on all_stopped.
	wakeRound(s, nextSignal(1))
	if got := len(assistantSendsTo(f, bg)); got != 1 {
		t.Fatalf("round 1 sends to bg = %d, want 1", got)
	}

	// Simulate an assistant_denied tool failure from the background supervisor machine during this active round.
	res := s.mcpCall(ctx, "server", "unrecognized-fork-session", "task_review", json.RawMessage(`{}`), true)
	if !res.IsError || !strings.Contains(res.Content[0].Text, "assistant") {
		// In fakeHub (no fleetDevs), test direct noteAssistantDenied as well if assistantCaller allowed unknown session.
		s.noteAssistantDenied(ctx, "server", "unrecognized-fork-session", "task_review")
	}

	// Round 2: background supervisor is tripped -> falls back ONCE to main conversation conv1 with breaker note.
	wakeRound(s, nextSignal(2))
	if got := len(assistantSendsTo(f, bg)); got != 1 {
		t.Fatalf("bg was woken again after assistant_denied: %d sends", got)
	}
	mainSends := assistantSendsTo(f, "conv1")
	if len(mainSends) != 1 {
		t.Fatalf("expected 1 fallback send to main conv1, got %d", len(mainSends))
	}
	if text, _ := mainSends[0]["text"].(string); !strings.Contains(text, "assistant_denied") || !strings.Contains(text, "task_review") {
		t.Fatalf("fallback message to main lacks assistant_denied explanation:\n%s", text)
	}

	// Round 3 within the backoff window: suppressed completely (neither bg nor conv1 is spammed).
	wakeRound(s, nextSignal(3))
	if got := len(assistantSendsTo(f, bg)); got != 1 {
		t.Fatalf("bg woken during backoff: %d sends", got)
	}
	if got := len(assistantSendsTo(f, "conv1")); got != 1 {
		t.Fatalf("main spammed during backoff: %d sends", got)
	}
}

// Two consecutive no-op background supervisor rounds on the same task's all_stopped signal trip the circuit breaker,
// falling back once to the main conversation and suppressing subsequent wakes during backoff. Real supervision
// progress (task_review / supervision_finish / supervisedWorking) resets the breaker.
func TestBackgroundBreakerTripsOnRepeatedNoOpAllStoppedAndResetsOnProgress(t *testing.T) {
	s, f, todo, bg, nextSignal := setupAllStoppedBackground(t)
	ctx := context.Background()

	// Round 1: wakes bg, finishes with zero progress tools called -> noOps=1 (not tripped yet).
	wakeRound(s, nextSignal(1))
	if got := len(assistantSendsTo(f, bg)); got != 1 {
		t.Fatalf("round 1 sends to bg = %d, want 1", got)
	}
	s.backgroundRoundTurnEnded(ctx, bg, time.Now())

	// Round 2: wakes bg again, finishes with zero progress -> noOps=2 -> trips breaker.
	wakeRound(s, nextSignal(2))
	if got := len(assistantSendsTo(f, bg)); got != 2 {
		t.Fatalf("round 2 sends to bg = %d, want 2", got)
	}
	s.backgroundRoundTurnEnded(ctx, bg, time.Now())

	// Round 3: breaker is open -> falls back ONCE to main conversation with a clear explanation; bg is NOT woken.
	wakeRound(s, nextSignal(3))
	if got := len(assistantSendsTo(f, bg)); got != 2 {
		t.Fatalf("bg woken after repeated no-op breaker tripped: %d sends", got)
	}
	mainSends := assistantSendsTo(f, "conv1")
	if len(mainSends) != 1 {
		t.Fatalf("expected 1 fallback send to main conv1, got %d", len(mainSends))
	}
	if text, _ := mainSends[0]["text"].(string); !strings.Contains(text, "repeated_noop") || !strings.Contains(text, "连续 2 轮无进展") {
		t.Fatalf("fallback message to main lacks repeated_noop explanation:\n%s", text)
	}

	// Round 4: still within backoff -> suppressed for both bg and conv1.
	wakeRound(s, nextSignal(4))
	if got := len(assistantSendsTo(f, bg)); got != 2 || len(assistantSendsTo(f, "conv1")) != 1 {
		t.Fatalf("woke during backoff: bg=%d conv1=%d", len(assistantSendsTo(f, bg)), len(assistantSendsTo(f, "conv1")))
	}

	// Progress resets the breaker: once the agent resumes work (supervisedWorking) or task_review succeeds,
	// a new all_stopped signal can wake bg again.
	s.resetBackgroundBreakerForTodo(todo.ID)
	wakeRound(s, nextSignal(5))
	if got := len(assistantSendsTo(f, bg)); got != 3 {
		t.Fatalf("after breaker reset, bg sends = %d, want 3", got)
	}

	// A round that calls a progress tool (supervision_finish) does NOT count as a no-op.
	s.noteBackgroundToolProgress(ctx, bg, "supervision_finish", json.RawMessage(`{}`))
	s.backgroundRoundTurnEnded(ctx, bg, time.Now())
	wakeRound(s, nextSignal(6))
	if got := len(assistantSendsTo(f, bg)); got != 4 {
		t.Fatalf("round after progress tool did not wake bg: %d", got)
	}
}

// If mcp.tools itself suffers an assistant-discovery denial during an active background round (downgrading tools to the
// 4 ordinary agent tools), the breaker trips even if the model never invokes an MCP tool and falls back to Bash. An
// unrelated terminal pane denied on the same machine does not trip the background supervisor's breaker.
func TestBackgroundBreakerTripsOnMCPToolsDiscoveryDowngrade(t *testing.T) {
	s, d, _, _ := privacyService(t)
	ctx := context.Background()
	at := time.Now().Add(-time.Hour).UTC()
	setSettings(t, s, func(v *Settings) {
		v.MachineID = "server"
		v.Main = "conv1"
		v.BackgroundSupervisor = "bg1"
		v.BackgroundSupervisorMachineID = "server"
		v.BackgroundSupervisorAt = &at
	})
	d.setSessions("server", []proto.Session{
		{SessionID: "bg1", MachineID: "server", Cwd: "/home/u/.persona/assistant", Status: proto.StatusWorking},
		{SessionID: "pane-agent", MachineID: "server", Cwd: "/home/u/.persona/assistant", PaneID: "pane-1", Status: proto.StatusWorking},
		{SessionID: "orphan-branch", MachineID: "server", Cwd: "/code/example", Status: proto.StatusWorking},
	})
	s.backgroundRoundDelivered(ctx, "bg1", relayMsg{Tool: superviseTool}, time.Now())

	// An unrelated host pane hitting mcp.tools denial must NOT trip the background breaker.
	_ = privacyTools(t, s, "server", "pane-agent", true)
	r := &s.background.rounds
	r.mu.Lock()
	trippedAfterPane := !r.breaker.authUntil.IsZero()
	r.mu.Unlock()
	if trippedAfterPane {
		t.Fatal("unrelated host pane tripped background supervisor breaker")
	}

	// A headless (non-pane) session on the background machine denied during an active background round trips the breaker.
	downgraded := privacyTools(t, s, "server", "orphan-branch", true)
	if !slices.Contains(downgraded, "report_to_supervisor") {
		t.Fatalf("expected denied caller to receive agent tools, got %v", downgraded)
	}
	r.mu.Lock()
	tripped := !r.breaker.authUntil.IsZero() && r.breaker.lastDeniedTool == "mcp.tools"
	r.mu.Unlock()
	if !tripped {
		t.Fatal("mcp.tools discovery downgrade during active background round did not trip breaker")
	}
}

// Replacing an outgrown background supervisor clears any fork and circuit-breaker state that belonged to the previous
// supervisor, so the replacement starts with a clean slate; if starting the replacement fails, the old supervisor's
// state stays intact.
func TestReplaceBackgroundClearsForkAndBreakerState(t *testing.T) {
	s, f, _, bg, nextSignal := setupAllStoppedBackground(t)
	ctx := context.Background()

	// Seed a forked round session and trip both the repeated no-op all_stopped breaker and the auth-denied breaker.
	const staleFork = "bg1-fork-stale"
	s.registerBackgroundRoundFork(ctx, "server", staleFork, bg)
	if !s.isBackgroundSession(ctx, staleFork) {
		t.Fatal("registered fork should be recognized before replacement")
	}

	wakeRound(s, nextSignal(1))
	s.backgroundRoundTurnEnded(ctx, bg, time.Now())
	wakeRound(s, nextSignal(2))
	s.backgroundRoundTurnEnded(ctx, bg, time.Now())
	s.backgroundRoundDelivered(ctx, bg, relayMsg{Tool: superviseTool}, time.Now())
	s.noteAssistantDenied(ctx, "server", staleFork, "task_review")

	// Mark bg outgrown so ensureBackground attempts replaceBackground.
	s.superviseRoundAnswered(bg, superviseRoundReply{Context: "kept", Why: "no_anchor"})

	// If starting the replacement fails, old supervisor continues and its fork/breaker state must NOT be wiped.
	f.mu.Lock()
	f.failAssistant = errors.New("start failed")
	f.mu.Unlock()
	if id, gone := s.ensureBackground(ctx); id != bg || gone != "" {
		t.Fatalf("failed replaceBackground returned (%q, %q), want (%q, \"\")", id, gone, bg)
	}
	r := &s.background.rounds
	r.mu.Lock()
	keptForks := len(r.forks)
	keptByTodo := len(r.breaker.byTodo)
	keptAuthTrips := r.breaker.authTrips
	r.replacedAt = time.Time{} // allow immediate retry once start succeeds
	r.mu.Unlock()
	if keptForks != 1 || keptByTodo == 0 || keptAuthTrips == 0 {
		t.Fatalf("failed replaceBackground wiped state: forks=%d byTodo=%d authTrips=%d", keptForks, keptByTodo, keptAuthTrips)
	}

	// Once start succeeds, replaceBackground installs a new supervisor and clears stale fork and breaker state.
	f.mu.Lock()
	f.failAssistant = nil
	f.mu.Unlock()
	newID, gone := s.ensureBackground(ctx)
	if newID == "" || newID == bg || gone != bg {
		t.Fatalf("replaceBackground returned (%q, %q), want new id replacing %q", newID, gone, bg)
	}
	r.mu.Lock()
	gotForks := len(r.forks)
	gotActive := r.breaker.active
	gotByTodo := len(r.breaker.byTodo)
	gotAuthTrips := r.breaker.authTrips
	gotAuthUntil := r.breaker.authUntil
	gotAuthAlerted := r.breaker.authAlerted
	gotLastDenied := r.breaker.lastDeniedTool
	r.mu.Unlock()
	if gotForks != 0 || gotActive != nil || gotByTodo != 0 || gotAuthTrips != 0 || !gotAuthUntil.IsZero() || gotAuthAlerted || gotLastDenied != "" {
		t.Fatalf("replaceBackground left stale state: forks=%d active=%v byTodo=%d authTrips=%d authUntil=%v authAlerted=%v lastDenied=%q",
			gotForks, gotActive, gotByTodo, gotAuthTrips, gotAuthUntil, gotAuthAlerted, gotLastDenied)
	}
	if s.isBackgroundSession(ctx, staleFork) {
		t.Fatal("stale fork from replaced supervisor is still authorized")
	}

	// The new supervisor receives the next all_stopped wake cleanly (not suppressed or redirected to main).
	f.setSessions("server",
		proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle},
		proto.Session{SessionID: newID, MachineID: "server", Assistant: true, Status: proto.StatusIdle},
	)
	wakeRound(s, nextSignal(3))
	if got := len(assistantSendsTo(f, newID)); got != 1 {
		t.Fatalf("new supervisor sends after replacement = %d, want 1 (main sends = %d)", got, len(assistantSendsTo(f, "conv1")))
	}
	if got := len(assistantSendsTo(f, "conv1")); got != 0 {
		t.Fatalf("main conversation should not receive fallback after clean replacement: %d sends", got)
	}
}
