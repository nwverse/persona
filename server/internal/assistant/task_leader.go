package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// Task leaders (#3656, docs/assistant/unified-entry.md "Task leaders"). A task the main conversation or a leader
// conversation hands out with task_plan role "leader" has a coding agent that leads it: it gets a restricted task_plan
// of its own and hands workers to its task, and what the workers report goes to it. Depth stops there: dot (a leader
// conversation) → task leader → worker; a worker hands out nothing (workerRefusal).
//
// What is kept, with no new column or table: the leader's todo_agents row has source "leader" (set when its agent is
// linked from the plan task's Role, which the saved plan keeps), and each worker's row has source "worker" and via the
// leader's task id. A worker's todo has no supervision ledger on purpose: the ledger belongs to the conversations'
// supervision (task_review, the 督办 messages, the cards routed up in ask_route.go), none of which a task leader takes
// part in, and a ledger would send the worker's signals and cards to the main conversation. The worker's report goes to
// the leader's session as a message instead, so it reaches nobody else.

const (
	roleLeader = "leader"
	// sourceLeader and sourceWorker are todo_agents.source of a task leader's agent and of its workers' (via: the
	// leader's task id), as sourceReview marks a reviewer.
	sourceLeader = "leader"
	sourceWorker = "worker"
	// permBypass is the only permission a task that changes files may be planned with: it starts at once, with nobody
	// there to answer a permission prompt.
	permBypass = "bypassPermissions"
	// leaderWorkersMost bounds the workers of one leader that are open at a time.
	leaderWorkersMost = 8
)

// leaderListWait bounds how long a tool list asked for by an agent that is no task's yet waits for a task leader being
// started: its agent asks for its tools before the start returns and it is linked.
var leaderListWait = 8 * time.Second

func (a TodoAgent) active() bool { return a.ReleasedAt == nil && a.DismissedAt == nil }

// linkSource is the todo_agents source and via the agent of plan task t is linked with.
func (t PlanTask) linkSource() (source, via string) {
	switch {
	case t.Role == roleLeader:
		return sourceLeader, ""
	case t.Parent != "":
		return sourceWorker, t.Parent
	}
	return "manual", ""
}

// linkPlanAgent links the agent just started for plan task t to its todo.
func (s *Service) linkPlanAgent(ctx context.Context, t PlanTask, mid, sid string, allowDone bool) error {
	source, via := t.linkSource()
	return s.linkTodoAgentWithSourceAllowingDone(ctx, t.TodoID, mid, sid, t.Title, t.Cwd, "", "", source, via, allowDone)
}

// leaderTask is the open task agent machine/session leads, with the agent's own row; false for any other agent.
func (s *Service) leaderTask(ctx context.Context, machine, session string) (Todo, TodoAgent, bool) {
	if session == "" {
		return Todo{}, TodoAgent{}, false
	}
	machine = s.canonical(machine)
	t, ok := s.taskOf(ctx, machine, session)
	if !ok || t.Cancelled() {
		return Todo{}, TodoAgent{}, false
	}
	for _, a := range t.Agents {
		if a.MachineID == machine && a.SessionID == session && a.active() && a.Source == sourceLeader {
			return t, a, true
		}
	}
	return Todo{}, TodoAgent{}, false
}

// workerLeader is the leader agent of the worker machine/session of open task w. linked: the agent is a worker; ok:
// its leader's task is still open and has its agent.
func (s *Service) workerLeader(w Todo, machine, session string) (lead TodoAgent, linked, ok bool) {
	machine = s.canonical(machine)
	var via string
	for _, a := range w.Agents {
		if a.MachineID == machine && a.SessionID == session && a.active() && a.Source == sourceWorker {
			via = a.Via
		}
	}
	if via == "" {
		return TodoAgent{}, false, false
	}
	l, found := s.todoNow(via)
	if !found || l.DoneAt != nil || l.Cancelled() {
		return TodoAgent{}, true, false
	}
	for _, a := range l.Agents {
		if a.active() && a.Source == sourceLeader && (!ok || a.LinkedAt.After(lead.LinkedAt)) {
			lead, ok = a, true
		}
	}
	return lead, true, ok
}

// leaderPlanTool is the restricted task_plan a task leader gets, under the name the model calls (prompts/mcp/leader).
func leaderPlanTool() proto.Tool {
	t := prompts.Tool("leader_task_plan")
	return proto.Tool{Name: "task_plan", Description: t.Description, InputSchema: t.InputSchema}
}

// leaderTools are the tools only a task leader's agent gets, on top of any agent's: none for any other agent.
func (s *Service) leaderTools(ctx context.Context, machine, session string) []proto.Tool {
	if session == "" {
		return nil
	}
	if _, _, ok := s.leaderTask(ctx, machine, session); ok {
		return []proto.Tool{leaderPlanTool()}
	}
	// An agent asks for its tools as it starts, before the start that made it returns and it is linked: with a leader
	// being started, an agent that is no task's yet waits for it (bounded), so a leader is not left without its tool
	// for the whole session. A worker, or an agent in any other task, is known and does not wait.
	if _, ok := s.taskOf(ctx, s.canonical(machine), session); ok {
		return nil
	}
	for deadline := time.Now().Add(leaderListWait); s.leaderStarts.Load() > 0 && time.Now().Before(deadline); {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(100 * time.Millisecond):
		}
		if _, _, ok := s.leaderTask(ctx, machine, session); ok {
			return []proto.Tool{leaderPlanTool()}
		}
	}
	return nil
}

// leaderTaskPlan is task_plan called by agent machine/session through its MCP server: it runs only for the leader of
// an open task, and only for workers beneath that task. Any other agent is refused as if the tool did not exist; a
// task's other agents are told why.
func (s *Service) leaderTaskPlan(ctx context.Context, machine, session string, args json.RawMessage) proto.ToolResult {
	lead, agent, ok := s.leaderTask(ctx, machine, session)
	if !ok {
		if err := s.workerRefusal(ctx, machine, session); err != nil {
			return proto.TextResult(err.Error(), true)
		}
		return proto.TextResult("unknown tool task_plan", true)
	}
	res, err := s.taskPlanFrom(ctx, agent.MachineID, session, args, &leaderScope{task: lead, agent: agent})
	if err != nil {
		return proto.TextResult(err.Error(), true)
	}
	return proto.TextResult(res, false)
}

// leaderScope is what a task leader's task_plan is bound to: its open task and its own agent (the default place).
type leaderScope struct {
	task  Todo
	agent TodoAgent
}

// leaderTaskIn is a worker task as a task leader may plan it: nothing else (no todo_id, role, attachments, project,
// user_requested…) is read, an unknown field is refused.
type leaderTaskIn struct {
	Title         string   `json:"title"`
	Prompt        string   `json:"prompt"`
	Machine       string   `json:"machine"`
	MachinePolicy string   `json:"machine_policy"`
	Cwd           string   `json:"cwd"`
	Launcher      string   `json:"launcher"`
	Model         string   `json:"model"`
	Tier          string   `json:"tier"`
	Effort        string   `json:"effort"`
	Permission    string   `json:"permission"`
	Readonly      bool     `json:"readonly"`
	DoneWhen      []string `json:"done_when"`
	Why           string   `json:"why"`
}

// workerTasks reads a leader's task_plan arguments as worker tasks beneath its task.
func (l *leaderScope) workerTasks(args json.RawMessage) ([]PlanTask, error) {
	var in struct {
		Tasks []leaderTaskIn `json:"tasks"`
	}
	dec := json.NewDecoder(bytes.NewReader(args))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return nil, fmt.Errorf("a task leader's task_plan takes only the fields its schema lists; todo_id, role, attachments and "+
			"any other are refused, and workers can only be new tasks beneath your own task (%v)", err)
	}
	out := make([]PlanTask, 0, len(in.Tasks))
	for _, t := range in.Tasks {
		pt := PlanTask{Title: t.Title, Prompt: t.Prompt, Machine: t.Machine, MachinePolicy: t.MachinePolicy, Cwd: t.Cwd,
			Launcher: t.Launcher, Model: t.Model, Tier: t.Tier, Effort: t.Effort, Permission: t.Permission, Readonly: t.Readonly,
			DoneWhen: t.DoneWhen, Why: t.Why, Parent: l.task.ID, Project: l.task.Project}
		if strings.TrimSpace(pt.Machine) == "" {
			pt.Machine = l.agent.MachineID
			if strings.TrimSpace(pt.Cwd) == "" {
				pt.Cwd = l.agent.Cwd
			}
		}
		out = append(out, pt)
	}
	return out, nil
}

// checkLeaderRoom refuses a plan that would leave the leader with more than leaderWorkersMost open workers.
func (s *Service) checkLeaderRoom(ctx context.Context, session string, adding int) error {
	open := 0
	for _, t := range s.todosOf(ctx, session) { // a worker's todo is opened from its leader's session
		if t.DoneAt == nil {
			open++
		}
	}
	if open+adding > leaderWorkersMost {
		return fmt.Errorf("at most %d workers of your task are open at a time (%d now): wait for some to finish; nothing was planned",
			leaderWorkersMost, open)
	}
	return nil
}

// ---- reports ----

// reportToLeader delivers worker agent machine/session's report of task w to its task leader's session, as a message
// from Persona: no supervision signal, feed item or card is made, so it reaches no conversation.
func (s *Service) reportToLeader(ctx context.Context, w Todo, lead TodoAgent, machine, session, kind, text string, evidence []string, step, steps int) (string, error) {
	r := prompts.WorkerReport{Title: w.Title, Kind: kind, Text: text, Evidence: evidence, Step: step, Steps: steps}
	if c, ok := s.session(ctx, s.canonical(machine), session); ok {
		r.Worker = agentLabel(c)
	}
	if err := s.sendToLeader(ctx, lead, prompts.RenderWorkerReport(r)); err != nil {
		return "", fmt.Errorf("your leader could not be reached (%v): go on with your work and put what you could not report into your final reply", err)
	}
	s.logf("task %s: %s reports %s to its leader %s", w.ID, short(session), kind, short(lead.SessionID))
	return "Reported to your task leader: it reads this in its own session; nobody else is told. Go on with your work.", nil
}

// tellLeaderOfFailedStart tells the leader of failed worker task t that its agent did not start.
func (s *Service) tellLeaderOfFailedStart(ctx context.Context, t PlanTask, reason string) {
	l, ok := s.todoNow(t.Parent)
	if !ok || l.DoneAt != nil {
		return
	}
	var lead TodoAgent
	found := false
	for _, a := range l.Agents {
		if a.active() && a.Source == sourceLeader && (!found || a.LinkedAt.After(lead.LinkedAt)) {
			lead, found = a, true
		}
	}
	if !found {
		return
	}
	text := prompts.RenderWorkerReport(prompts.WorkerReport{Title: t.Title, Kind: "failed", Text: reason})
	if err := s.sendToLeader(ctx, lead, text); err != nil {
		s.logf("task %s: its leader was not told of the failed start: %v", t.TodoID, err)
	}
}

// sendToLeader sends text into the leader agent's session; a closed agent is resumed first, which can take minutes, so
// that send goes on in the background.
func (s *Service) sendToLeader(ctx context.Context, lead TodoAgent, text string) error {
	c, ok := s.session(ctx, lead.MachineID, lead.SessionID)
	if !ok {
		return errors.New("its session is not listed")
	}
	if c.Status == proto.StatusOffline {
		go func() {
			sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
			defer cancel()
			if err := s.call(sctx, lead.MachineID, DeviceSessionSend, sendParams(lead.SessionID, text, false), nil); err != nil {
				s.logf("task leader %s: report: %v", short(lead.SessionID), err)
			}
		}()
		return nil
	}
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 90*time.Second)
	defer cancel()
	return s.call(sctx, lead.MachineID, DeviceSessionSend, sendParams(lead.SessionID, text, false), nil)
}
