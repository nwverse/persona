package assistant

import (
	"context"
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

var planIDRe = regexp.MustCompile(`plan_[0-9a-f]+`)

// leaderPlanArgs is a main-conversation task_plan for one writable task leader.
func leaderPlanArgs(role string) json.RawMessage {
	task := map[string]any{"title": "拆开做登录改造", "prompt": "把登录改造拆成几块交给 worker 做", "machine": "box", "cwd": "/code/app",
		"readonly": false, "permission": "bypassPermissions", "why": "这个仓库在 box 上", "done_when": []string{"所有 worker 的改动合进一个 PR"}}
	if role != "" {
		task["role"] = role
	}
	raw, _ := json.Marshal(map[string]any{"tasks": []any{task}})
	return raw
}

// workerArgs is a task leader's task_plan of one worker.
func workerArgs(extra map[string]any) json.RawMessage {
	task := map[string]any{"title": "改登录页", "prompt": "只改登录页的布局，补测试", "readonly": false, "permission": "bypassPermissions"}
	for k, v := range extra {
		task[k] = v
	}
	raw, _ := json.Marshal(map[string]any{"tasks": []any{task}})
	return raw
}

// startTaskLeader makes a task leader of the main conversation's task (its agent s1 on box, listed and idle) and waits for
// the link.
func startTaskLeader(t *testing.T, s *Service, f *fakeHub) Todo {
	t.Helper()
	ctx := context.Background()
	res, err := s.toolTaskPlan(ctx, "conv1", leaderPlanArgs("leader"))
	if err != nil {
		t.Fatal(err)
	}
	// A writable task waits for the user's Start on its card.
	if _, err := s.startPlan(ctx, planIDRe.FindString(res), nil, false); err != nil {
		t.Fatalf("%s: %v", res, err)
	}
	waitFor(t, "the leader's link", func() bool { return len(onlyTodo(s).Agents) == 1 })
	f.setSessions("box", proto.Session{SessionID: "s1", MachineID: "box", Cwd: "/code/app", Agent: "claude", Title: "leader",
		Status: proto.StatusIdle, UpdatedAt: time.Now()})
	return onlyTodo(s)
}

func todosOpenedBy(s *Service, origin string) []Todo {
	return s.todosOf(context.Background(), origin)
}

// planWorker has the leader (s1 on box) plan one worker, waits for its agent, and lists it as session sN on box.
func planWorker(t *testing.T, s *Service, f *fakeHub, extra map[string]any) Todo {
	t.Helper()
	before := len(todosOpenedBy(s, "s1"))
	res := s.mcpCall(context.Background(), "box", "s1", "task_plan", workerArgs(extra), false)
	if res.IsError {
		t.Fatalf("the leader's task_plan: %s", res.Content[0].Text)
	}
	waitFor(t, "the worker's link", func() bool {
		list := todosOpenedBy(s, "s1")
		return len(list) == before+1 && len(list[len(list)-1].Agents) == 1
	})
	list := todosOpenedBy(s, "s1")
	w := list[len(list)-1]
	sessions := []proto.Session{{SessionID: "s1", MachineID: "box", Cwd: "/code/app", Agent: "claude", Status: proto.StatusIdle, UpdatedAt: time.Now()}}
	for _, o := range list {
		for _, a := range o.Agents {
			sessions = append(sessions, proto.Session{SessionID: a.SessionID, MachineID: "box", Cwd: "/code/app", Agent: "claude",
				Status: proto.StatusWorking, UpdatedAt: time.Now()})
		}
	}
	f.setSessions("box", sessions...)
	return w
}

// sendsTo are the texts session.send put into machine's session.
func sendsTo(f *fakeHub, machine, session string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		if c.Device != machine || c.Method != DeviceSessionSend {
			continue
		}
		var p struct {
			SessionID string `json:"session_id"`
			Text      string `json:"text"`
		}
		raw, _ := json.Marshal(c.Params)
		if json.Unmarshal(raw, &p) == nil && p.SessionID == session {
			out = append(out, p.Text)
		}
	}
	return out
}

func leaderToolNames(tools []proto.Tool) []string {
	var out []string
	for _, tl := range tools {
		out = append(out, tl.Name)
	}
	return out
}

// The main conversation's task_plan marks a task's agent as a task leader (role leader): the agent's todo_agents row
// says so, and only that agent's tool list has the restricted task_plan; any other agent, a worker included, has none.
func TestTaskLeaderGetsRestrictedTaskPlanAndWorkersDoNot(t *testing.T) {
	s, f := newQuietService(t)
	ctx := context.Background()
	todo := startTaskLeader(t, s, f)
	if a := todo.Agents[0]; a.Source != sourceLeader || a.SessionID != "s1" || a.MachineID != "box" {
		t.Fatalf("the leader's link: %+v", a)
	}
	if v := todo.Supervision; v == nil || v.Supervisor != "conv1" {
		t.Fatalf("the leader's own task is supervised by the conversation that handed it out: %+v", v)
	}
	if prompt, _ := f.starts[0]["prompt"].(string); !strings.Contains(prompt, "you are the leader of this task") {
		t.Fatalf("the leader's first message does not say it leads: %s", prompt)
	}

	var restricted []proto.Tool
	for _, tl := range s.sessionMCPTools(ctx, "box", "s1", false, false) {
		if tl.Name == "task_plan" {
			restricted = append(restricted, tl)
		}
	}
	if len(restricted) != 1 || !strings.Contains(restricted[0].Description, "Hand part of your own task to workers") ||
		strings.Contains(string(restricted[0].InputSchema), "todo_id") || strings.Contains(string(restricted[0].InputSchema), `"role"`) {
		t.Fatalf("the leader's tools: %+v", restricted)
	}
	if got := leaderToolNames(s.sessionMCPTools(ctx, "box", "someone-else", false, false)); slices.Contains(got, "task_plan") {
		t.Fatalf("an agent of no task got task_plan: %v", got)
	}

	w := planWorker(t, s, f, nil)
	if got := leaderToolNames(s.sessionMCPTools(ctx, "box", w.Agents[0].SessionID, false, false)); slices.Contains(got, "task_plan") {
		t.Fatalf("a worker got task_plan: %v", got)
	}
	if got := leaderToolNames(s.sessionMCPTools(ctx, "box", "s1", false, false)); !slices.Contains(got, "report_to_supervisor") {
		t.Fatalf("the leader lost the agents' own tools: %v", got)
	}
}

// The tool list is only the listing: a call must pass the same check, for the leader's own task only.
func TestTaskLeaderTaskPlanCallEnforcement(t *testing.T) {
	s, f := newQuietService(t)
	ctx := context.Background()
	todo := startTaskLeader(t, s, f)
	other, err := s.createTaskTodo(ctx, "别的任务", "", "conv1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	call := func(session string, args json.RawMessage) proto.ToolResult {
		return s.mcpCall(ctx, "box", session, "task_plan", args, false)
	}
	refused := func(what string, res proto.ToolResult, want ...string) {
		t.Helper()
		if !res.IsError {
			t.Fatalf("%s was accepted: %+v", what, res)
		}
		for _, w := range want {
			if !strings.Contains(res.Content[0].Text, w) {
				t.Fatalf("%s: %q lacks %q", what, res.Content[0].Text, w)
			}
		}
	}

	refused("an agent of no task", call("someone-else", workerArgs(nil)), "unknown tool task_plan")
	refused("a call with no session", call("", workerArgs(nil)), "unknown tool task_plan")
	refused("a leader taking another todo", call("s1", workerArgs(map[string]any{"todo_id": other.ID})), "todo_id")
	refused("a leader making a leader", call("s1", workerArgs(map[string]any{"role": "leader"})), "role")
	refused("a leader attaching files", call("s1", workerArgs(map[string]any{"attachments": []string{"/tmp/a.png"}})), "attachments")
	refused("a writable worker with no permission", call("s1", workerArgs(map[string]any{"permission": ""})), "bypassPermissions")
	refused("a writable worker with another permission", call("s1", workerArgs(map[string]any{"permission": "acceptEdits"})), "bypassPermissions")
	if got := len(todosOpenedBy(s, "s1")); got != 0 {
		t.Fatalf("refused calls opened %d todos", got)
	}

	// Left-out machine and cwd are the leader's own place; the worker starts at once, with no plan card or push.
	feedBefore, pushesBefore := feedKinds(s, "conv1"), len(f.pushed())
	res := call("s1", workerArgs(nil))
	if res.IsError || !strings.Contains(res.Content[0].Text, "beneath your task "+todo.ID) {
		t.Fatalf("the leader's worker: %+v", res)
	}
	waitFor(t, "the worker's start", func() bool { return len(f.starts) == 2 })
	if cwd, _ := f.starts[1]["cwd"].(string); cwd != "/code/app" {
		t.Fatalf("the worker's directory: %v", f.starts[1])
	}
	if mode, _ := f.starts[1]["permission_mode"].(string); mode != "bypassPermissions" {
		t.Fatalf("the worker's permission: %v", f.starts[1])
	}
	if prompt, _ := f.starts[1]["prompt"].(string); !strings.Contains(prompt, "handed to you by the leader") {
		t.Fatalf("the worker is not told who handed it the task: %s", prompt)
	}
	waitFor(t, "the worker's link", func() bool {
		l := todosOpenedBy(s, "s1")
		return len(l) == 1 && len(l[0].Agents) == 1
	})
	w := todosOpenedBy(s, "s1")[0]
	a := w.Agents[0]
	if a.Source != sourceWorker || a.Via != todo.ID || w.Supervision != nil || w.Quiet {
		t.Fatalf("the worker's link: %+v, todo %+v", a, w)
	}
	// The worker's supervisor is its leader: the leader task's own agent, found by the link, not by a name in a prompt.
	if lead, linked, ok := s.workerLeader(w, "box", a.SessionID); !linked || !ok || lead.SessionID != "s1" || lead.MachineID != "box" {
		t.Fatalf("the worker's supervisor: %+v linked %v ok %v", lead, linked, ok)
	}
	if got := feedKinds(s, "conv1"); got != feedBefore {
		t.Fatalf("the worker's plan came to the main conversation's feed: %s -> %s", feedBefore, got)
	}
	if n := len(f.pushed()); n != pushesBefore {
		t.Fatalf("the user was pushed %d notifications for the leader's workers", n-pushesBefore)
	}

	// A worker has no dispatch right, and is told why; neither has anyone else's agent.
	f.setSessions("box", proto.Session{SessionID: "s1", MachineID: "box", Cwd: "/code/app", Agent: "claude", Status: proto.StatusIdle},
		proto.Session{SessionID: a.SessionID, MachineID: "box", Cwd: "/code/app", Agent: "claude", Status: proto.StatusWorking})
	refused("a worker's task_plan", call(a.SessionID, workerArgs(nil)), "does not hand out Persona tasks")
	refused("a worker's task_plan, with the leader's role", call(a.SessionID, leaderPlanArgs("leader")), "does not hand out Persona tasks")
	if got := len(todosOpenedBy(s, a.SessionID)); got != 0 {
		t.Fatalf("a worker opened %d todos", got)
	}

	// The leader's task closes: its tool goes, and it cannot plan more.
	if ok, err := s.autoDoneTodo(ctx, todo.ID, "done", time.Now().Add(-time.Minute)); err != nil || !ok {
		t.Fatalf("closing the leader's task: %v %v", ok, err)
	}
	refused("a closed task's leader", call("s1", workerArgs(nil)))
}

// The room is bounded: a leader cannot keep more than leaderWorkersMost workers open.
func TestTaskLeaderWorkersAreBounded(t *testing.T) {
	s, f := newQuietService(t)
	ctx := context.Background()
	startTaskLeader(t, s, f)
	for i := 0; i < leaderWorkersMost; i++ {
		if _, err := s.createTaskTodo(ctx, "worker", "", "s1", "", ""); err != nil {
			t.Fatal(err)
		}
	}
	res := s.mcpCall(ctx, "box", "s1", "task_plan", workerArgs(nil), false)
	if !res.IsError || !strings.Contains(res.Content[0].Text, "at most 8 workers") {
		t.Fatalf("a ninth open worker: %+v", res)
	}
}

// The writable task's permission is explicit, from any caller: refused before anything is planned, readably; read-only
// tasks are not affected.
func TestWritableTaskPlanNeedsExplicitBypassPermissions(t *testing.T) {
	s, f := newQuietService(t)
	f.known["lead1"] = true
	f.setSessions("server", proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle},
		proto.Session{SessionID: "lead1", MachineID: "server", Assistant: true, Status: proto.StatusIdle})
	setSettings(t, s, func(v *Settings) { v.Leaders = []string{"lead1"} })
	ctx := context.Background()
	plan := func(self string, readonly bool, permission string) (string, error) {
		task := map[string]any{"title": "修登录崩溃", "prompt": "修好并补测试", "machine": "box", "cwd": "/code/app", "readonly": readonly,
			"why": "这个仓库在 box 上"}
		if permission != "" {
			task["permission"] = permission
		}
		raw, _ := json.Marshal(map[string]any{"tasks": []any{task}})
		return s.toolTaskPlan(ctx, self, raw)
	}
	for _, self := range []string{"conv1", "lead1"} {
		for _, permission := range []string{"", "default", "acceptEdits", "auto", "plan"} {
			_, err := plan(self, false, permission)
			if err == nil || !strings.Contains(err.Error(), `permission must be "bypassPermissions"`) ||
				!strings.Contains(err.Error(), "Nothing was planned") {
				t.Fatalf("%s, permission %q: %v", self, permission, err)
			}
		}
		if _, err := plan(self, false, "bypassPermissions"); err != nil {
			t.Fatalf("%s, bypassPermissions: %v", self, err)
		}
		if _, err := plan(self, true, ""); err != nil {
			t.Fatalf("%s, read-only: %v", self, err)
		}
	}
	// Only the accepted plans were saved: a refusal leaves no plan, card or todo.
	if got := len(s.tasks.plans); got != 4 {
		t.Fatalf("plans saved: %d", got)
	}
}

// role leader is the supervising conversations' to give; a side conversation or a bad role is refused with the rest.
func TestTaskPlanRoleIsForSupervisors(t *testing.T) {
	s, f := newQuietService(t)
	f.setSessions("server", proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle},
		proto.Session{SessionID: "conv2", MachineID: "server", Assistant: true, Status: proto.StatusIdle})
	ctx := context.Background()
	if _, err := s.toolTaskPlan(ctx, "conv2", leaderPlanArgs("leader")); err == nil || !strings.Contains(err.Error(), "main conversation or a leader conversation") {
		t.Fatalf("a side conversation made a leader: %v", err)
	}
	if _, err := s.toolTaskPlan(ctx, "conv1", leaderPlanArgs("boss")); err == nil || !strings.Contains(err.Error(), `"boss" is not one`) {
		t.Fatalf("a made-up role: %v", err)
	}
	if _, err := s.toolTaskPlan(ctx, "conv1", leaderPlanArgs("leader")); err != nil {
		t.Fatalf("the main conversation's leader: %v", err)
	}
	// A client's edit on the card can change what the user may, never the role.
	p := &TaskPlan{}
	for _, pl := range s.tasks.plans {
		p = pl
	}
	task := p.Tasks[0]
	applyPlanEdit(&task, PlanTask{Role: "", Parent: "todo_x", Machine: "box"})
	if task.Role != roleLeader || task.Parent != "" {
		t.Fatalf("an edit changed the role or the parent: %+v", task)
	}
}

// A worker's report goes into its leader's session on the leader's machine, as a message from Persona. The leader's own
// report goes to the conversation that handed it its task. Nothing of the worker's reaches the main conversation.
func TestWorkerReportGoesToLeaderOnly(t *testing.T) {
	s, f := newQuietService(t)
	ctx := context.Background()
	todo := startTaskLeader(t, s, f)
	w := planWorker(t, s, f, nil)
	worker := w.Agents[0].SessionID

	res := s.agentTool(ctx, "box", worker, "report_to_supervisor", json.RawMessage(`{"kind":"done","text":"登录页改好了","evidence":["go test ./login 通过"]}`))
	if res.IsError || !strings.Contains(res.Content[0].Text, "Reported to your task leader") {
		t.Fatalf("the worker's report: %+v", res)
	}
	got := sendsTo(f, "box", "s1")
	if len(got) != 1 {
		t.Fatalf("messages to the leader: %q", got)
	}
	for _, want := range []string{"Persona · worker report", "Task: 改登录页", "Kind: done", "登录页改好了", "go test ./login 通过"} {
		if !strings.Contains(got[0], want) {
			t.Errorf("the leader's message lacks %q:\n%s", want, got[0])
		}
	}
	if n := sendsTo(f, "box", worker); len(n) != 0 {
		t.Fatalf("the worker was sent %q", n)
	}

	// A question that says it can wait is no briefing item either: a worker's report always goes to its leader, at once.
	res = s.agentTool(ctx, "box", worker, "report_to_supervisor", json.RawMessage(`{"kind":"question","text":"用哪个分支？","urgent":false}`))
	if res.IsError || !strings.Contains(res.Content[0].Text, "Reported to your task leader") {
		t.Fatalf("the worker's urgent:false question: %+v", res)
	}
	if got := sendsTo(f, "box", "s1"); len(got) != 2 || !strings.Contains(got[1], "Kind: question") || !strings.Contains(got[1], "用哪个分支？") {
		t.Fatalf("messages to the leader: %q", got)
	}

	// The leader's own report is the conversation's, and carries nothing of the worker's.
	res = s.agentTool(ctx, "box", "s1", "report_to_supervisor", json.RawMessage(`{"kind":"blocked","text":"leader 自己卡在评审"}`))
	if res.IsError {
		t.Fatalf("the leader's report: %+v", res)
	}
	waitFor(t, "the main conversation's message", func() bool { return len(f.notices()) == 1 })
	n := f.notices()[0]
	if !strings.Contains(n, "leader 自己卡在评审") || strings.Contains(n, "登录页改好了") || strings.Contains(n, "改登录页") {
		t.Fatalf("the main conversation's message:\n%s", n)
	}
	if after := sendsTo(f, "box", "s1"); len(after) != 2 {
		t.Fatalf("the leader's own report went to the leader: %q", after)
	}
	items, _, _ := s.feed(ctx, "conv1", time.Time{})
	for _, it := range items {
		if it.Kind == feedReport && it.Ref != todo.ID {
			t.Fatalf("a worker's report was put in the main conversation's feed: %+v", it)
		}
	}
	if v := todoNow(s, w.ID).Supervision; v != nil {
		t.Fatalf("a worker has a supervision ledger: %+v", v)
	}
}

// A worker whose leader's task is closed, or whose leader is gone, reaches nobody and is told so; its report does not
// fall to the main conversation.
func TestWorkerReportWithoutALeader(t *testing.T) {
	s, f := newQuietService(t)
	ctx := context.Background()
	todo := startTaskLeader(t, s, f)
	w := planWorker(t, s, f, nil)
	worker := w.Agents[0].SessionID

	f.mu.Lock()
	f.failSend = context.DeadlineExceeded
	f.mu.Unlock()
	res := s.agentTool(ctx, "box", worker, "report_to_supervisor", json.RawMessage(`{"kind":"progress","text":"x"}`))
	if !res.IsError || !strings.Contains(res.Content[0].Text, "leader could not be reached") {
		t.Fatalf("a leader that cannot be reached: %+v", res)
	}
	f.mu.Lock()
	f.failSend = nil
	f.mu.Unlock()

	if ok, err := s.autoDoneTodo(ctx, todo.ID, "done", time.Now().Add(-time.Minute)); err != nil || !ok {
		t.Fatalf("closing the leader's task: %v %v", ok, err)
	}
	res = s.agentTool(ctx, "box", worker, "report_to_supervisor", json.RawMessage(`{"kind":"progress","text":"x"}`))
	if res.IsError || !strings.Contains(res.Content[0].Text, "Nobody receives this") {
		t.Fatalf("a worker of a closed task: %+v", res)
	}
	if got := f.notices(); len(got) != 0 {
		t.Fatalf("the main conversation was told: %q", got)
	}
}

// A worker that does not start is told to its leader, not to a conversation.
func TestFailedWorkerStartIsTheLeadersToHear(t *testing.T) {
	s, f := newQuietService(t)
	startTaskLeader(t, s, f)
	f.mu.Lock()
	f.failStart = context.DeadlineExceeded
	f.mu.Unlock()
	res := s.mcpCall(context.Background(), "box", "s1", "task_plan", workerArgs(nil), false)
	if res.IsError {
		t.Fatalf("%+v", res)
	}
	waitFor(t, "the leader's message", func() bool { return len(sendsTo(f, "box", "s1")) == 1 })
	if msg := sendsTo(f, "box", "s1")[0]; !strings.Contains(msg, "Kind: failed") || !strings.Contains(msg, "It did not start") {
		t.Fatalf("the leader's message:\n%s", msg)
	}
	if got := f.notices(); len(got) != 0 {
		t.Fatalf("the main conversation was told: %q", got)
	}
}

// The leader, its workers and where a report goes are kept in the database's rows: a restarted manager reads them back.
func TestTaskLeaderSurvivesARestart(t *testing.T) {
	ctx := context.Background()
	database := taskRecoveryDatabase(t)
	s, f := newQuietServiceOn(t, database)
	todo := startTaskLeader(t, s, f)
	w := planWorker(t, s, f, nil)
	worker := w.Agents[0].SessionID

	restarted, hub := newQuietServiceOn(t, database)
	hub.setSessions("box", proto.Session{SessionID: "s1", MachineID: "box", Cwd: "/code/app", Agent: "claude", Status: proto.StatusIdle},
		proto.Session{SessionID: worker, MachineID: "box", Cwd: "/code/app", Agent: "claude", Status: proto.StatusWorking})
	lead, agent, ok := restarted.leaderTask(ctx, "box", "s1")
	if !ok || lead.ID != todo.ID || agent.Source != sourceLeader {
		t.Fatalf("the leader after a restart: %+v %+v %v", lead, agent, ok)
	}
	if got := leaderToolNames(restarted.sessionMCPTools(ctx, "box", "s1", false, false)); !slices.Contains(got, "task_plan") {
		t.Fatalf("the leader's tools after a restart: %v", got)
	}
	if got := leaderToolNames(restarted.sessionMCPTools(ctx, "box", worker, false, false)); slices.Contains(got, "task_plan") {
		t.Fatalf("a worker's tools after a restart: %v", got)
	}
	if _, linked, ok := restarted.workerLeader(todoNow(restarted, w.ID), "box", worker); !linked || !ok {
		t.Fatalf("the worker's leader after a restart: linked %v ok %v", linked, ok)
	}
	res := restarted.agentTool(ctx, "box", worker, "report_to_supervisor", json.RawMessage(`{"kind":"progress","text":"重启后还在跑"}`))
	if res.IsError {
		t.Fatalf("the report after a restart: %+v", res)
	}
	if got := sendsTo(hub, "box", "s1"); len(got) != 1 || !strings.Contains(got[0], "重启后还在跑") {
		t.Fatalf("the leader's message after a restart: %q", got)
	}
	if got := hub.notices(); len(got) != 0 {
		t.Fatalf("the main conversation was told: %q", got)
	}
	// The saved plans keep the role and the parent, so starting a task again (a retry) links the same way.
	if p, err := restarted.loadPlan(ctx, todo.PlanID); err != nil || p.Tasks[0].Role != roleLeader || p.Tasks[0].Parent != "" {
		t.Fatalf("the leader's plan after a restart: %+v %v", p, err)
	}
	if p, err := restarted.loadPlan(ctx, w.PlanID); err != nil || p.Conversation != "s1" || p.Tasks[0].Role != "" || p.Tasks[0].Parent != todo.ID {
		t.Fatalf("the worker's plan after a restart: %+v %v", p, err)
	}
	// And in the rows the link is read from:
	rows, err := database.ListAllTodoAgents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for _, r := range rows {
		kinds[r.SessionID] = r.Source + "/" + r.Via
	}
	if kinds["s1"] != "leader/" || kinds[worker] != "worker/"+todo.ID {
		t.Fatalf("todo_agents: %v", kinds)
	}
}

// An agent being started as a leader asks for its tools before it is linked: its list waits for the link, a list of any
// other unlinked agent waits no longer than the bound, and neither is given the tool by the wait alone.
func TestTaskLeaderListWaitsForItsLink(t *testing.T) {
	s, _ := newQuietService(t)
	old := leaderListWait
	leaderListWait = 400 * time.Millisecond
	t.Cleanup(func() { leaderListWait = old })
	ctx := context.Background()

	if got := s.leaderTools(ctx, "box", "sx"); got != nil {
		t.Fatalf("no leader is starting: %v", got)
	}
	s.leaderStarts.Add(1)
	start := time.Now()
	if got := s.leaderTools(ctx, "box", "sx"); got != nil || time.Since(start) < 300*time.Millisecond {
		t.Fatalf("an unlinked agent while a leader starts: %v after %s", got, time.Since(start))
	}
	todo, err := s.createTaskTodo(ctx, "leader", "", "conv1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	time.AfterFunc(100*time.Millisecond, func() {
		_ = s.linkTodoAgentWithSource(ctx, todo.ID, "box", "sx", "leader", "/code/app", "", "", sourceLeader, "")
		s.leaderStarts.Add(-1)
	})
	if got := s.leaderTools(ctx, "box", "sx"); len(got) != 1 || got[0].Name != "task_plan" {
		t.Fatalf("the leader's list after its link: %v", got)
	}
}

// The messages the leader and its workers are given.
func TestTaskLeaderMessages(t *testing.T) {
	for _, want := range []string{"you are the leader of this task", "task_plan", "bypassPermissions", "Persona · worker report", "stop and ask the user"} {
		if !strings.Contains(prompts.RenderTaskLeader(), want) {
			t.Errorf("the leader's brief lacks %q", want)
		}
	}
	for _, want := range []string{"handed to you by the leader", "report_to_supervisor", "cannot hand it on", "stop and ask the user"} {
		if !strings.Contains(prompts.RenderTaskWorker(), want) {
			t.Errorf("the worker's brief lacks %q", want)
		}
	}
	got := prompts.RenderWorkerReport(prompts.WorkerReport{Worker: "登录页", Title: "改登录页", Kind: "question", Text: "用哪个分支？", Step: 2, Steps: 5})
	want := "Persona · worker report. This message is not from the user: a worker you handed out with task_plan sent it through " +
		"report_to_supervisor (登录页).\n\nTask: 改登录页\nKind: question · step 2 of 5\n\n用哪个分支？\n\nIt waits for a decision. If the " +
		"decision is yours, hand the work on to a new worker with the answer in its prompt; if it is the user's, ask the user.\n"
	if got != want && got+"\n" != want {
		t.Errorf("the worker report:\n%q\nwant\n%q", got, want)
	}
}

// The hub's own signals about a worker (its turn ending, it stopping, a question waiting on the user) have no supervision
// ledger to ride on: none reaches the main conversation, and the worker's to-do is closed by the done judge as an
// unsupervised task's is. What a worker says to its leader is only what it reports.
func TestWorkerSignalsDoNotReachTheMainConversation(t *testing.T) {
	s, f := newQuietService(t)
	ctx := context.Background()
	f.judge = `{"verdict":"done","reason":"PR 已合并"}`
	startTaskLeader(t, s, f)
	w := planWorker(t, s, f, nil)
	worker := w.Agents[0].SessionID
	turn := func(status, text string, pending int) {
		state := proto.StateUnread
		if pending > 0 {
			state = proto.StateNeedsYou
		}
		if status == proto.StatusWorking {
			state = proto.StateWorking
		}
		f.setSessions("box", proto.Session{SessionID: "s1", MachineID: "box", Cwd: "/code/app", Agent: "claude", Status: proto.StatusIdle},
			proto.Session{SessionID: worker, MachineID: "box", Cwd: "/code/app", Agent: "claude", Status: status, State: state, LastText: text,
				PendingInteractions: pending, UpdatedAt: time.Now()})
		if status == proto.StatusWorking {
			s.taskWorking(ctx, "box", worker)
		} else {
			s.taskTurnEnded(ctx, "box", worker)
		}
	}
	turn(proto.StatusWorking, "", 0)
	turn(proto.StatusBlocked, "要不要先停写？", 1)
	turn(proto.StatusWorking, "", 0)
	turn(proto.StatusDone, "做完了，PR https://github.com/o/r/pull/7 已合并", 0)
	waitFor(t, "the judge to close the worker's to-do", func() bool { return todoNow(s, w.ID).DoneAt != nil })
	s.sendDigest(ctx)
	if got := f.notices(); len(got) != 0 {
		t.Fatalf("the main conversation was told about a worker: %q", got)
	}
	b := &s.supervise
	b.mu.Lock()
	pending := len(b.pending)
	b.mu.Unlock()
	if pending != 0 {
		t.Fatalf("%d supervision signals wait for a worker", pending)
	}
	if v := todoNow(s, w.ID).Supervision; v != nil {
		t.Fatalf("a worker has a supervision ledger: %+v", v)
	}
	items, _, _ := s.feed(ctx, "conv1", time.Time{})
	for _, it := range items {
		if it.Ref == w.ID {
			t.Fatalf("the worker's to-do is in the main conversation's feed: %+v", it)
		}
	}
}

// dot (the main conversation) hands a task leader to a leader conversation with task_plan's supervisor: the leader's own
// task is that leader conversation's to supervise, its report goes there, and its workers still report to the task
// leader's agent: main → leader conversation → task leader → worker, and no deeper.
func TestTaskLeaderUnderALeaderConversation(t *testing.T) {
	s, f := newQuietService(t)
	ctx := context.Background()
	f.known["lead1"] = true
	f.setSessions("server", proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle},
		proto.Session{SessionID: "lead1", MachineID: "server", Assistant: true, Status: proto.StatusIdle})
	setSettings(t, s, func(v *Settings) { v.Leaders = []string{"lead1"} })
	var in map[string]any
	_ = json.Unmarshal(leaderPlanArgs("leader"), &in)
	in["supervisor"] = "lead1"
	raw, _ := json.Marshal(in)
	res, err := s.toolTaskPlan(ctx, "conv1", raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.startPlan(ctx, planIDRe.FindString(res), nil, false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the leader's link", func() bool { return len(onlyTodo(s).Agents) == 1 })
	todo := onlyTodo(s)
	if v := todo.Supervision; v == nil || v.Supervisor != "lead1" || todo.Agents[0].Source != sourceLeader {
		t.Fatalf("the task leader's task: %+v %+v", todo.Supervision, todo.Agents)
	}
	f.setSessions("box", proto.Session{SessionID: "s1", MachineID: "box", Cwd: "/code/app", Agent: "claude", Status: proto.StatusIdle})
	w := planWorker(t, s, f, nil)

	if res := s.agentTool(ctx, "box", w.Agents[0].SessionID, "report_to_supervisor", json.RawMessage(`{"kind":"progress","text":"worker 的进度"}`)); res.IsError {
		t.Fatalf("%+v", res)
	}
	if res := s.agentTool(ctx, "box", "s1", "report_to_supervisor", json.RawMessage(`{"kind":"blocked","text":"leader 的阻塞"}`)); res.IsError {
		t.Fatalf("%+v", res)
	}
	waitFor(t, "the leader conversation's message", func() bool { return len(f.noticesTo("lead1")) == 1 })
	if n := f.noticesTo("lead1")[0]; !strings.Contains(n, "leader 的阻塞") || strings.Contains(n, "worker 的进度") {
		t.Fatalf("the leader conversation's message:\n%s", n)
	}
	if got := f.notices(); len(got) != 0 {
		t.Fatalf("the main conversation was told: %q", got)
	}
	if got := sendsTo(f, "box", "s1"); len(got) != 1 || !strings.Contains(got[0], "worker 的进度") {
		t.Fatalf("the task leader's messages: %q", got)
	}
}
