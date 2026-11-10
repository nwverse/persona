package assistant

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
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
// leader's task id. A worker's todo is a supervised task like any other, with the leader's agent session as the
// ledger's supervisor (so the API shows it, and a restart keeps it) and the task's done_when. Where a supervisor is a
// conversation everything goes to that conversation; here the same signals, the worker's reports included, are one
// message into the leader agent's session instead (deliverToLeaderAgent, called from sendDigest), and the leader closes
// its workers with a task_review of its own that reaches its direct workers only (leaderTaskReview). None of it goes
// through digestTo, which would send a session that is no conversation to the main chat. What a leader needs the user
// for goes up to the conversation that handed it its task (sigDecide).

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

// isLeaderSession: sid is the session of a task leader's agent, whatever the state of its row.
func (s *Service) isLeaderSession(sid string) bool {
	if sid == "" {
		return false
	}
	s.todos.mu.RLock()
	defer s.todos.mu.RUnlock()
	for _, t := range s.todos.todos {
		for _, a := range t.Agents {
			if a.SessionID == sid && a.Source == sourceLeader {
				return true
			}
		}
	}
	return false
}

// agentSupervised: task t's supervisor is a task leader's agent, not a conversation.
func (s *Service) agentSupervised(t Todo) bool {
	return t.Supervision != nil && s.isLeaderSession(t.Supervision.Supervisor)
}

// leaderSupervisor is the leader agent that supervises task t now: the session its ledger names while that is the
// agent of an open leader task, else the current agent of the leader task its worker's row says it belongs to (a leader
// started again has a new session). supervised says t's supervisor is a leader agent at all; ok that it can be reached.
func (s *Service) leaderSupervisor(t Todo) (lead TodoAgent, supervised, ok bool) {
	if !s.agentSupervised(t) {
		return TodoAgent{}, false, false
	}
	s.todos.mu.RLock()
	defer s.todos.mu.RUnlock()
	open := func(l *Todo) bool { return l != nil && l.DoneAt == nil && !l.Cancelled() }
	pick := func(l *Todo, sid string) {
		for _, a := range l.Agents {
			if a.active() && a.Source == sourceLeader && (sid == "" || a.SessionID == sid) && (!ok || a.LinkedAt.After(lead.LinkedAt)) {
				lead, ok = a, true
			}
		}
	}
	for _, l := range s.todos.todos {
		if open(l) {
			pick(l, t.Supervision.Supervisor)
		}
	}
	if ok {
		return lead, true, true
	}
	for _, a := range t.Agents {
		if a.Source == sourceWorker && a.Via != "" && open(s.todos.todos[a.Via]) {
			pick(s.todos.todos[a.Via], "")
		}
	}
	return lead, true, ok
}

// leaderPlanTool is the restricted task_plan a task leader gets, under the name the model calls (prompts/mcp/leader).
func leaderPlanTool() proto.Tool {
	t := prompts.Tool("leader_task_plan")
	return proto.Tool{Name: "task_plan", Description: t.Description, InputSchema: t.InputSchema}
}

// leaderToolList are a task leader's own tools: the restricted task_plan and task_review, under the names the model calls.
func leaderToolList() []proto.Tool {
	r := prompts.Tool("leader_task_review")
	return []proto.Tool{leaderPlanTool(), {Name: "task_review", Description: r.Description, InputSchema: r.InputSchema}}
}

// leaderTools are the tools only a task leader's agent gets, on top of any agent's: none for any other agent.
func (s *Service) leaderTools(ctx context.Context, machine, session string) []proto.Tool {
	if session == "" {
		return nil
	}
	if _, _, ok := s.leaderTask(ctx, machine, session); ok {
		return leaderToolList()
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
			return leaderToolList()
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
	ExpectMinutes int      `json:"expect_minutes"`
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
			DoneWhen: t.DoneWhen, ExpectMinutes: t.ExpectMinutes, Why: t.Why, Parent: l.task.ID, Project: l.task.Project}
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

// ---- supervision messages ----

// leaderRoute says where signal g of a task goes when its supervisor is a leader agent: handled false for any other
// task (digestTo decides), else the leader agent it goes to, reachable false when there is none now (the signal is
// dropped: it never falls to the main conversation). A signal that names its conversation itself (g.To: a card's rung,
// a decision sent up) is not one of these.
func (s *Service) leaderRoute(g superviseSignal) (lead TodoAgent, handled, reachable bool) {
	if g.To != "" {
		return TodoAgent{}, false, false
	}
	t, ok := s.todoNow(g.Todo)
	if !ok {
		return TodoAgent{}, false, false
	}
	lead, handled, reachable = s.leaderSupervisor(t)
	return lead, handled, reachable
}

// deliverToLeaderAgent puts the signals list of its workers into leader agent lead's session as one supervision message
// (agents/worker_supervise), deduplicated as a conversation's are; now is when sendDigest marked it as the last message
// and prev the last before. True when it went in. session.send queues behind a running turn and resumes a closed agent,
// so nothing is queued here.
func (s *Service) deliverToLeaderAgent(ctx context.Context, lead TodoAgent, list []superviseSignal, web string, now, prev time.Time) bool {
	var entries []prompts.SuperviseEntry
	var kept []superviseSignal
	for _, g := range list {
		if e, ok := s.digestEntry(ctx, g, web); ok {
			entries, kept = append(entries, e), append(kept, g)
		}
	}
	if len(entries) == 0 {
		return false
	}
	entries, kept, rollback, err := s.reserveSupervision(ctx, entries, kept, now)
	if err != nil {
		s.logf("supervise: deduplication unavailable; retrying: %v", err)
		s.restoreDigest(kept, now, prev)
		return false
	}
	if len(entries) == 0 {
		return false
	}
	if err := s.sendToLeader(ctx, lead, prompts.RenderWorkerSupervise(sortEntries(entries))); err != nil {
		s.logf("supervise: %d signal(s) for task leader %s: %v; trying again", len(entries), short(lead.SessionID), err)
		rollback()
		s.restoreDigest(kept, now, prev)
		return false
	}
	s.logf("supervise: %d signal(s) to task leader %s", len(entries), short(lead.SessionID))
	return true
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
				s.logf("task leader %s: supervision message: %v", short(lead.SessionID), err)
			}
		}()
		return nil
	}
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 90*time.Second)
	defer cancel()
	return s.call(sctx, lead.MachineID, DeviceSessionSend, sendParams(lead.SessionID, text, false), nil)
}

// ---- the leader's task_review ----

// leaderTaskReview is task_review called by agent machine/session through its MCP server: only for a task leader.
func (s *Service) leaderTaskReview(ctx context.Context, machine, session string, args json.RawMessage) proto.ToolResult {
	_, agent, ok := s.leaderTask(ctx, machine, session)
	if !ok {
		return proto.TextResult("unknown tool task_review", true)
	}
	res, err := s.leaderReview(ctx, agent, args)
	if err != nil {
		return proto.TextResult(err.Error(), true)
	}
	return proto.TextResult(res, false)
}

// leaderReview is the leader agent's task_review on one of its direct workers: accepted closes it, cancelled closes and
// stops it, rework sends what is missing back to the worker (two rounds, then it goes up), needs_user sends the
// question to whoever handed the leader its task. No conclusion card, "Please verify" or other card goes to the user,
// and no other task is reachable: it is the leader's direct worker (its ledger names this leader) or nothing.
func (s *Service) leaderReview(ctx context.Context, lead TodoAgent, args json.RawMessage) (string, error) {
	var a struct {
		TodoID    string        `json:"todo_id"`
		Verdict   string        `json:"verdict"`
		Checks    []ReviewCheck `json:"checks"`
		Note      string        `json:"note"`
		Reason    string        `json:"reason"`
		Recommend string        `json:"recommend"`
	}
	dec := json.NewDecoder(bytes.NewReader(args))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&a); err != nil {
		return "", fmt.Errorf("a task leader's task_review takes only the fields its schema lists (%v)", err)
	}
	a.Note = strings.TrimSpace(a.Note)
	switch a.Verdict {
	case reviewAccepted, reviewRework, reviewNeedsUser, reviewCancelled:
	default:
		return "", errors.New("verdict must be accepted, rework, needs_user or cancelled")
	}
	if a.Verdict == reviewRework && a.Note == "" && !slices.ContainsFunc(a.Checks, func(c ReviewCheck) bool { return !c.Met }) {
		return "", errors.New("rework: say what is missing (a check that is not met, or note)")
	}
	t, ok := s.todoNow(strings.TrimSpace(a.TodoID))
	if !ok {
		return "", fmt.Errorf("no task %q", a.TodoID)
	}
	if cur, supervised, reachable := s.leaderSupervisor(t); !supervised || !reachable || cur.MachineID != lead.MachineID || cur.SessionID != lead.SessionID {
		return "", fmt.Errorf("task %s is not one of your workers: you review the workers you handed out with task_plan, nothing else", t.ID)
	}
	if t.DoneAt != nil && (a.Verdict == reviewRework || a.Verdict == reviewNeedsUser) {
		return "", fmt.Errorf("task %s is done already (%s): hand out a new worker for what is left", t.ID, cmp.Or(t.DoneReason, "closed"))
	}
	var up string // the conversation a needs_user goes to: whoever handed the leader its task
	if lt, ok := s.leaderTaskOf(lead); ok && lt.Supervision != nil {
		up = lt.Supervision.Supervisor
	}
	if a.Verdict == reviewNeedsUser && up == "" {
		return "", errors.New("your own task has nobody to hand a decision to: decide it yourself, or review the worker with the other verdicts")
	}
	checks := make([]ReviewCheck, 0, len(a.Checks))
	for _, c := range a.Checks {
		c.Criterion, c.Evidence = clip(oneLine(c.Criterion), 200), clip(strings.TrimSpace(c.Evidence), 600)
		if c.Criterion != "" {
			checks = append(checks, c)
		}
	}
	verdict, note, over := a.Verdict, clip(a.Note, 2000), false
	v, _, err := s.updateSupervision(ctx, t.ID, func(v *Supervision) bool {
		if verdict == reviewRework && v.Round >= maxReworkRounds {
			verdict, over = reviewNeedsUser, true
		}
		r := TaskReview{At: time.Now().UTC(), Verdict: verdict, Checks: checks, Note: note}
		if verdict == reviewRework {
			v.Round++
			r.Round = v.Round
		}
		v.State = verdict
		v.Reviews = append(v.Reviews, r)
		if len(v.Reviews) > 20 {
			v.Reviews = v.Reviews[len(v.Reviews)-20:]
		}
		return true
	})
	if err != nil {
		return "", err
	}
	if verdict == reviewNeedsUser && up == "" {
		up = s.settings(ctx).Main // reworked too often with nobody above the leader: the main conversation
	}
	if verdict == reviewRework || verdict == reviewNeedsUser {
		s.setTaskStageKind(ctx, t.ID, "unverified", "", "", false)
	}
	b := &s.supervise
	b.mu.Lock()
	b.init()
	for _, k := range []string{sigReview, sigIdle, sigUnjudged, sigNeedsYou, sigStalled, sigOrphaned} {
		delete(b.pending, t.ID+"|"+k) // reviewed: what waited is answered
	}
	b.mu.Unlock()
	switch verdict {
	case reviewAccepted, reviewCancelled:
		reason := cmp.Or(clip(oneLine(note), 80), "验收通过") // i18n-ignore: done_reason, as a conversation's review writes it
		if verdict == reviewCancelled {
			reason = "取消：" + cmp.Or(clip(oneLine(note), 80), "不做了") // i18n-ignore: done_reason, as a conversation's review writes it
		}
		if t.DoneAt == nil {
			if _, err := s.reviewDoneTodo(ctx, t.ID, reason, verdict == reviewAccepted); err != nil {
				return "", err
			}
		}
		if verdict == reviewCancelled {
			s.stopTaskAgents(ctx, t)
			return fmt.Sprintf("Worker task %s is cancelled and closed; its agent is being closed and archived.", t.ID), nil
		}
		return fmt.Sprintf("Worker task %s is accepted and closed; its agent is closed once it stays still.", t.ID), nil
	case reviewNeedsUser:
		why := cmp.Or(clip(oneLine(a.Reason), 200), clip(oneLine(note), 200))
		if over && a.Reason == "" {
			why = fmt.Sprintf("sent back %d times already, still short: %s", maxReworkRounds, clip(oneLine(note), 160))
		}
		s.signal(superviseSignal{Todo: t.ID, Kind: sigDecide, To: up, Key: fmt.Sprintf("decide@%d@%s", v.Round, time.Now().UTC().Format(time.RFC3339Nano)),
			Detail: why, Ask: clip(oneLine(a.Recommend), 200)})
		if over {
			return fmt.Sprintf("Worker task %s was sent back %d times already: it now waits for a decision, which went up to whoever handed "+
				"you your task. Review it again once the answer reaches you.", t.ID, maxReworkRounds), nil
		}
		return fmt.Sprintf("Worker task %s waits for a decision, which went up to whoever handed you your task. You are told what "+
			"they decide; no card went to the user.", t.ID), nil
	}
	text := prompts.RenderSuperviseRework(prompts.SuperviseRework{Round: v.Round, Max: maxReworkRounds, Checks: reworkChecks(checks), Note: note})
	sent, err := s.sendToTask(ctx, t, text)
	if err != nil {
		return "", fmt.Errorf("recorded rework round %d, but sending it to the worker failed: %v", v.Round, err)
	}
	return fmt.Sprintf("Rework round %d of %d: %s. Its next stop comes back to you as a supervision message.", v.Round, maxReworkRounds, sent), nil
}

// agentSupervisedSession: the open task agent machine/session works for is supervised by a task leader's agent.
func (s *Service) agentSupervisedSession(machine, session string) bool {
	t, ok := s.taskOf(context.Background(), s.canonical(machine), session)
	return ok && s.agentSupervised(t)
}

// leaderTaskOf is the open task leader agent lead leads.
func (s *Service) leaderTaskOf(lead TodoAgent) (Todo, bool) {
	t, ok := s.taskOf(context.Background(), lead.MachineID, lead.SessionID)
	return t, ok
}
