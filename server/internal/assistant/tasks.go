package assistant

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// Tasks dispatched from an assistant conversation (docs/assistant/unified-entry.md, #930). The assistant hands the user a plan
// (task_plan); read-only plans, tasks the user asked for in so many words (#2559) and kinds the user trusts start at
// once, the rest wait for the user's "Start" on the plan card. A task is a todo (todos.go) whose origin is the conversation; its agents are ordinary agents
// (the Agents page shows them as always). What the assistant adds to the conversation — the plan card, a started
// task, an answered question, a receipt, a message sent straight to a task — is the conversation's feed, kept apart
// from Claude's transcript.

// PlanTask is one task of a plan, as the assistant proposed it and the user may have edited it.
type PlanTask struct {
	Title      string `json:"title"`
	Prompt     string `json:"prompt"`
	Machine    string `json:"machine"`
	Cwd        string `json:"cwd"`
	Launcher   string `json:"launcher,omitempty"`
	Model      string `json:"model,omitempty"`
	Effort     string `json:"effort,omitempty"`
	Tier       string `json:"tier,omitempty"`
	Permission string `json:"permission,omitempty"`
	// Ultracode: the agent starts with Claude Code's ultracode on (#12). A pointer so that an edit from the card can
	// turn it off, and one from a client that does not know it keeps the assistant's choice.
	Ultracode *bool `json:"ultracode,omitempty"`
	Readonly  bool  `json:"readonly,omitempty"`
	// UserRequested: the user asked for this very task in the turn that planned it (#2559); with
	// assistant.start_requested_tasks on it starts without the card's "Start".
	UserRequested bool   `json:"user_requested,omitempty"`
	Why           string `json:"why,omitempty"`
	// DoneWhen: what done means, criteria the supervisor checks before it accepts the work; ExpectMinutes: how long it
	// should take (#3124, supervise.go). Both optional; the agent is told the criteria with its prompt.
	DoneWhen      []string `json:"done_when,omitempty"`
	ExpectMinutes int      `json:"expect_minutes,omitempty"`
	// ReuseTodo: the todo this task runs on instead of a new one (task_plan's todo_id, #5279): one this conversation
	// opened that has no agent yet, such as the todo a Lark topic opened (feishu_thread_open.go). Checked when the
	// plan is made and again when it starts; it is not what the apps send back with an edit.
	ReuseTodo string `json:"reuse_todo,omitempty"`
	// Set once started: the todo the task is, and why its agent did not start.
	TodoID string `json:"todo_id,omitempty"`
	Error  string `json:"error,omitempty"`
}

// TaskPlan is a plan the assistant handed the user.
type TaskPlan struct {
	ID           string     `json:"id"`
	Conversation string     `json:"conversation"`
	Tasks        []PlanTask `json:"tasks"`
	State        string     `json:"state"` // planOpen, planStarted, planDismissed
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

const (
	planOpen      = "open"
	planStarted   = "started"
	planDismissed = "dismissed"
)

// FeedItem is one item the hub inserted into an assistant conversation; clients merge them with the transcript by At.
type FeedItem struct {
	ID           string          `json:"id"`
	Conversation string          `json:"conversation"`
	At           time.Time       `json:"at"`
	Kind         string          `json:"kind"` // feedPlan, feedTask, feedAnswered, feedReceipt, feedRouted
	Ref          string          `json:"ref,omitempty"`
	Body         json.RawMessage `json:"body,omitempty"`
}

const (
	feedPlan     = "plan"     // ref: the plan id
	feedTask     = "task"     // ref: the todo id
	feedAnswered = "answered" // ref: the todo id; body: answeredBody (answered.go)
	feedReceipt  = "receipt"  // ref: the todo id; body: title, state, summary, pr, machine
	feedRouted   = "routed"   // ref: the todo id; body: text, interrupted (agent_interrupt stopped its turn first)
	feedReport   = "report"   // ref: the todo id; body: reportBody (a supervised task's agent reported, agent_control.go)
	feedMessage  = "message"  // ref: the inbox item id; body: MessageCard (a message about a task, with its draft reply)
)

const trustSetting = "tasks.trust"

// settleDelay is how long a task's agents stay still before it counts as done: as long as the hub waits before its
// own finished push.
func (s *Service) settleDelay(ctx context.Context) time.Duration {
	return time.Duration(s.hello(ctx).DoneDelayMS) * time.Millisecond
}

type tasksState struct {
	mu sync.Mutex
	// completionMu serializes a resumed turn with committing an earlier judgment.
	completionMu sync.Mutex
	// Without a store (unit tests) plans and the feed live here.
	plans map[string]*TaskPlan
	feed  map[string][]FeedItem
	// working: when one of a task's agents last started a turn; receipted: when its last receipt went out. A
	// receipt is due when the task settles after working since the last one.
	working   map[string]time.Time
	receipted map[string]time.Time
	// awaitingAnswer remembers a stopped status whose transcript still ends in a tool.
	awaitingAnswer map[SessionRef]bool
	// retrying: the tasks whose agent is starting again after a failed start (one start at a time, #1396).
	retrying map[string]bool
	// outcomes: the completions whose outcome was recorded (todo_outcome.go, outcomeKey).
	outcomes map[string]time.Time
	// reusing: the todos a started plan took over (PlanTask.ReuseTodo) whose agent is not linked yet, so that a second
	// plan cannot take the same todo in that gap (takeTodoForTask).
	reusing map[string]bool
	// outcomeRuns are the outcome recordings still going (goRecordTaskOutcome, waitTaskOutcomes).
	outcomeRuns sync.WaitGroup
}

func newTasksState() tasksState {
	return tasksState{plans: map[string]*TaskPlan{}, feed: map[string][]FeedItem{},
		working: map[string]time.Time{}, receipted: map[string]time.Time{}, retrying: map[string]bool{},
		outcomes: map[string]time.Time{}, reusing: map[string]bool{}}
}

// ---- plans and the feed: storage ----

func (s *Service) savePlan(ctx context.Context, p *TaskPlan) error {
	st := s.store()
	if st == nil {
		s.tasks.mu.Lock()
		cp := *p
		cp.Tasks = append([]PlanTask(nil), p.Tasks...)
		s.tasks.plans[p.ID] = &cp
		s.tasks.mu.Unlock()
		return nil
	}
	raw, _ := json.Marshal(p.Tasks)
	return st.CreateTaskPlan(ctx, db.TaskPlanRow{ID: p.ID, Conversation: p.Conversation, Tasks: raw, State: p.State,
		CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt})
}

var errPlanNotFound = errors.New("plan not found")

func (s *Service) loadPlan(ctx context.Context, id string) (*TaskPlan, error) {
	st := s.store()
	if st == nil {
		s.tasks.mu.Lock()
		defer s.tasks.mu.Unlock()
		p := s.tasks.plans[id]
		if p == nil {
			return nil, errPlanNotFound
		}
		cp := *p
		cp.Tasks = append([]PlanTask(nil), p.Tasks...)
		return &cp, nil
	}
	row, err := st.TaskPlan(ctx, id)
	if errors.Is(err, db.ErrTaskPlanNotFound) {
		return nil, errPlanNotFound
	}
	if err != nil {
		return nil, err
	}
	return planFromRow(*row), nil
}

func planFromRow(row db.TaskPlanRow) *TaskPlan {
	p := &TaskPlan{ID: row.ID, Conversation: row.Conversation, State: row.State, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
	_ = json.Unmarshal(row.Tasks, &p.Tasks)
	return p
}

// movePlan moves p from state from to its State with its Tasks; false when someone else moved it first.
func (s *Service) movePlan(ctx context.Context, p *TaskPlan, from string) (bool, error) {
	p.UpdatedAt = time.Now().UTC()
	st := s.store()
	if st == nil {
		s.tasks.mu.Lock()
		defer s.tasks.mu.Unlock()
		cur := s.tasks.plans[p.ID]
		if cur == nil || cur.State != from {
			return false, nil
		}
		cp := *p
		cp.Tasks = append([]PlanTask(nil), p.Tasks...)
		s.tasks.plans[p.ID] = &cp
		return true, nil
	}
	raw, _ := json.Marshal(p.Tasks)
	return st.UpdateTaskPlan(ctx, p.ID, from, p.State, raw, p.UpdatedAt)
}

// appendFeed adds an item to a conversation's feed and tells the clients.
func (s *Service) appendFeed(ctx context.Context, conversation, kind, ref string, body any) (FeedItem, error) {
	return s.appendFeedAt(ctx, conversation, kind, ref, body, time.Now())
}

// appendFeedAt is appendFeed for an item that belongs at another time (the "Today" card: the start of the day).
func (s *Service) appendFeedAt(ctx context.Context, conversation, kind, ref string, body any, at time.Time) (FeedItem, error) {
	it := FeedItem{ID: "feed_" + randomHex(8), Conversation: conversation, At: at.UTC(), Kind: kind, Ref: ref}
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return FeedItem{}, err
		}
		it.Body = raw
	}
	if st := s.store(); st == nil {
		s.tasks.mu.Lock()
		s.tasks.feed[conversation] = append(s.tasks.feed[conversation], it)
		s.tasks.mu.Unlock()
	} else if err := st.AppendFeed(ctx, db.FeedRow{ID: it.ID, Conversation: conversation, At: it.At, Kind: kind, Ref: ref, Body: it.Body}); err != nil {
		return FeedItem{}, err
	}
	s.event(ctx, map[string]any{"type": "assistant_feed", "conversation": conversation, "item": it})
	return it, nil
}

const feedPage = 500

// feed is a conversation's items after since, oldest first (with the message cards sent after since: the client
// has them, not that they went out), and the plans they name.
func (s *Service) feed(ctx context.Context, conversation string, since time.Time) ([]FeedItem, map[string]*TaskPlan, error) {
	var items []FeedItem
	if st := s.store(); st == nil {
		s.tasks.mu.Lock()
		for _, it := range s.tasks.feed[conversation] {
			if at := cardSentAt(it); it.At.After(since) || it.Kind == feedMessage && at != nil && at.After(since) {
				items = append(items, it)
			}
		}
		s.tasks.mu.Unlock()
	} else {
		rows, err := st.Feed(ctx, conversation, since, feedPage)
		if err != nil {
			return nil, nil, err
		}
		for _, r := range rows {
			items = append(items, FeedItem{ID: r.ID, Conversation: r.Conversation, At: r.At, Kind: r.Kind, Ref: r.Ref, Body: r.Body})
		}
	}
	s.settleCards(ctx, items)
	s.fileCards(ctx, items)
	plans := map[string]*TaskPlan{}
	for _, it := range items {
		if it.Kind == feedPlan && plans[it.Ref] == nil {
			if p, err := s.loadPlan(ctx, it.Ref); err == nil {
				plans[it.Ref] = p
			}
		}
	}
	return items, plans, nil
}

// ---- the assistant's task_plan tool ----

// planTaskIn is a task as task_plan's arguments have it. Its todo_id is the todo to run on (PlanTask.ReuseTodo); the
// plan's own todo_id, the todo a started task became, is the hub's to set, and the shallower field wins in JSON.
type planTaskIn struct {
	PlanTask
	TodoID string `json:"todo_id"`
}

func (s *Service) toolTaskPlan(ctx context.Context, sessionID string, args json.RawMessage) (string, error) {
	var in struct {
		Tasks []planTaskIn `json:"tasks"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", fmt.Errorf("tasks: %v", err)
	}
	var a struct{ Tasks []PlanTask }
	for _, t := range in.Tasks {
		t.PlanTask.ReuseTodo = strings.TrimSpace(t.TodoID)
		a.Tasks = append(a.Tasks, t.PlanTask)
	}
	if len(a.Tasks) == 0 {
		return "", fmt.Errorf("tasks is empty")
	}
	if len(a.Tasks) > 8 {
		return "", fmt.Errorf("at most 8 tasks in one plan")
	}
	reused := map[string]bool{}
	for i := range a.Tasks {
		t := &a.Tasks[i]
		t.TodoID, t.Error = "", ""
		t.Title, t.Prompt, t.Cwd = strings.TrimSpace(t.Title), strings.TrimSpace(t.Prompt), strings.TrimSpace(t.Cwd)
		t.DoneWhen, t.ExpectMinutes = cleanDoneWhen(t.DoneWhen), max(0, min(t.ExpectMinutes, 24*60))
		if t.Title == "" || t.Prompt == "" || t.Cwd == "" {
			return "", fmt.Errorf("task %d: title, prompt and cwd are required", i+1)
		}
		if !s.placeTask(ctx, t) {
			return "", fmt.Errorf("task %d: unknown machine %q (machines: %s)", i+1, t.Machine, s.machineIDs(ctx))
		}
		// Validate the tier now without pinning its model: it is resolved again at dispatch.
		preview := *t
		if err := s.applyTaskTier(ctx, &preview); err != nil {
			return "", fmt.Errorf("task %d: %w", i+1, err)
		}
		s.healthyPlace(ctx, &preview) // health follows the resolved launcher
		t.Machine, t.Cwd, t.Why = preview.Machine, preview.Cwd, preview.Why
	}
	if err := s.ensureTodosLoaded(ctx); err != nil {
		return "", fmt.Errorf("todos are unavailable: %v", err)
	}
	for i, t := range a.Tasks {
		if t.ReuseTodo == "" {
			continue
		}
		if reused[t.ReuseTodo] {
			return "", fmt.Errorf("task %d: todo_id %s is already the todo of another task in this plan", i+1, t.ReuseTodo)
		}
		reused[t.ReuseTodo] = true
		if err := s.reusableTodo(sessionID, t.ReuseTodo, false); err != nil {
			return "", fmt.Errorf("task %d: %v", i+1, err)
		}
	}
	dups := s.planDuplicates(ctx, a.Tasks) // before the plan's own todos exist
	now := time.Now().UTC()
	p := &TaskPlan{ID: "plan_" + randomHex(6), Conversation: sessionID, Tasks: a.Tasks, State: planOpen, CreatedAt: now, UpdatedAt: now}
	if err := s.savePlan(ctx, p); err != nil {
		return "", err
	}
	// The plan goes in the item's body too: a client that missed the task_plan event (or raced it) still draws the
	// card from the conversation itself.
	if _, err := s.appendFeed(ctx, sessionID, feedPlan, p.ID, map[string]any{"plan": p}); err != nil {
		s.logf("task plan %s: feed: %v", p.ID, err)
	}
	// A plan from the health space hands its prompts to agents outside it: it waits for the user's "Start" on its card,
	// whatever would start it at once, and its push names no task (health.go).
	health := s.inHealth(ctx, sessionID)
	if health || !s.planStartsAtOnce(ctx, p.Tasks) {
		s.event(ctx, map[string]any{"type": "task_plan", "plan": p})
		// A card below the answer is easy to miss: push it as well.
		titles := make([]string, 0, len(p.Tasks))
		for _, t := range p.Tasks {
			titles = append(titles, t.Title)
		}
		l := s.lang(ctx)
		title := i18n.T(l, "assistant-tasks.plan_waiting", i18n.Args{"n": len(p.Tasks)})
		if len(titles) == 1 {
			title = i18n.T(l, "assistant-tasks.plan_waiting_single")
		}
		here := s.settings(ctx).MachineID
		machineName := "Persona"
		if m, ok := s.device(ctx, here); ok {
			machineName = m.Name
		}
		body := strings.Join(titles, i18n.T(l, "assistant-tasks.list_sep"))
		if health {
			title, body = healthNotice(l)
		}
		s.push(ctx, Notification{Subtitle: machineName, DeviceID: here, SessionID: sessionID, Title: title, Body: body})
		return fmt.Sprintf("Plan %s is on the user's screen as a card and pushed to their phone; nothing has started. "+
			"Don't wait for it: carry on. When they start it, the tasks show up in the context snapshot.", p.ID) + dups, nil
	}
	started, err := s.startPlan(ctx, p.ID, nil, false)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Started %d task(s) without asking (read-only, asked for by the user, or a kind the user trusts):\n", len(started.Tasks))
	for _, t := range started.Tasks {
		fmt.Fprintf(&b, "- %s → todo %s on %s\n", t.Title, t.TodoID, t.Machine)
	}
	b.WriteString("Their progress shows in the context snapshot; the user sees them as cards.")
	if s.isMain(ctx, sessionID) {
		b.WriteString(" You supervise them: a 督办 message comes when one needs you (review it with task_review).")
	}
	b.WriteString(dups)
	return b.String(), nil
}

// placeTask names t's machine by its id and makes ~ in its directory that machine's home; false: no such machine.
func (s *Service) placeTask(ctx context.Context, t *PlanTask) bool {
	m, ok := s.machineByName(ctx, t.Machine)
	if !ok {
		return false
	}
	t.Machine = m.ID
	t.Cwd = proto.ExpandHome(t.Cwd, m.Home)
	return true
}

// planStartsAtOnce: every task only reads, was asked for by the user in this turn (while start_requested_tasks is on),
// or is a kind the user said to start without asking.
func (s *Service) planStartsAtOnce(ctx context.Context, tasks []PlanTask) bool {
	trusted := s.trustedKinds(ctx)
	requested := s.startRequestedTasks(ctx)
	for _, t := range tasks {
		if !t.Readonly && !(requested && t.UserRequested) && !trusted[trustKey(t)] {
			return false
		}
	}
	return true
}

// trustKey names a kind of task the user can let start without asking: a machine's directory, reading or editing.
func trustKey(t PlanTask) string {
	kind := "edit"
	if t.Readonly {
		kind = "read"
	}
	return t.Machine + "|" + t.Cwd + "|" + kind
}

func (s *Service) trustedKinds(ctx context.Context) map[string]bool {
	out := map[string]bool{}
	st := s.store()
	if st == nil {
		return out
	}
	var keys []string
	if ok, err := st.Setting(ctx, trustSetting, &keys); err == nil && ok {
		for _, k := range keys {
			out[k] = true
		}
	}
	return out
}

func (s *Service) trustKinds(ctx context.Context, tasks []PlanTask) {
	st := s.store()
	if st == nil {
		return
	}
	have := s.trustedKinds(ctx)
	for _, t := range tasks {
		have[trustKey(t)] = true
	}
	keys := make([]string, 0, len(have))
	for k := range have {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if err := st.PutSetting(ctx, trustSetting, keys); err != nil {
		s.logf("tasks: trust: %v", err)
	}
}

// ---- starting and dismissing a plan ----

var errPlanNotOpen = errors.New("this plan was already started or dismissed")

// startPlan opens a todo for each task of the open plan id (edits, when given, replace the tasks' fields the user can
// change) and starts their agents in the background; remember lets this kind start without asking next time.
func (s *Service) startPlan(ctx context.Context, id string, edits []PlanTask, remember bool) (*TaskPlan, error) {
	if err := s.ensureTodosLoaded(ctx); err != nil {
		return nil, err
	}
	p, err := s.loadPlan(ctx, id)
	if err != nil {
		return nil, err
	}
	if p.State != planOpen {
		return nil, errPlanNotOpen
	}
	for i := range p.Tasks {
		if i < len(edits) {
			applyPlanEdit(&p.Tasks[i], edits[i])
		}
		if !s.placeTask(ctx, &p.Tasks[i]) {
			return nil, i18n.Err("assistant-tasks.task_no_machine", i18n.Args{"task": p.Tasks[i].Title, "machine": strconv.Quote(p.Tasks[i].Machine)})
		}
	}
	p.State = planStarted
	ok, err := s.movePlan(ctx, p, planOpen)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errPlanNotOpen
	}
	for i := range p.Tasks {
		t := &p.Tasks[i]
		var todo Todo
		if t.ReuseTodo != "" {
			// The todo is already there with its own title, notes and task card in the conversation's feed (a Lark topic
			// opened it): the task runs on it, and nothing is made or shown twice.
			todo, err = s.takeTodoForTask(p.Conversation, t.ReuseTodo)
			if err != nil {
				t.Error = err.Error()
				continue
			}
		} else {
			title, retitle := s.taskTitle(ctx, t.Title, t.Prompt) // the assistant's own title stays when it is a real one
			todo, err = s.createTodo(ctx, title, t.Prompt, p.Conversation, p.ID)
			if err != nil {
				t.Error = err.Error()
				continue
			}
			if retitle {
				s.nameTodoLater(ctx, todo.ID, title, t.Prompt)
			}
		}
		t.TodoID = todo.ID
		if s.isMain(ctx, p.Conversation) { // the main conversation follows up what it hands out (supervise.go)
			s.superviseTodo(ctx, todo.ID, p.Conversation, t.DoneWhen, t.ExpectMinutes)
		}
		if t.ReuseTodo != "" {
			continue
		}
		if _, err := s.appendFeed(ctx, p.Conversation, feedTask, todo.ID, nil); err != nil {
			s.logf("task %s: feed: %v", todo.ID, err)
		}
	}
	if _, err := s.movePlan(ctx, p, planStarted); err != nil { // keep the todo ids
		s.logf("plan %s: %v", p.ID, err)
	}
	if remember {
		s.trustKinds(ctx, p.Tasks)
	}
	s.event(ctx, map[string]any{"type": "task_plan", "plan": p})
	for i := range p.Tasks {
		if task := p.Tasks[i]; task.TodoID != "" {
			go func() {
				defer s.releaseTakenTodo(task.ReuseTodo) // linked or failed: the todo is no longer in the gap
				s.startPlanTask(p.ID, p.Conversation, i, task, false)
			}()
		}
	}
	return p, nil
}

// notReusable is the refusal of a todo_id: it says why, so the assistant can plan again without it.
func notReusable(id, why string) error {
	return fmt.Errorf("todo_id %s cannot be used: %s (leave todo_id out and the task gets a todo of its own)", id, why)
}

// reusableTodo says whether a task of a plan from conversation may run on todo id (task_plan's todo_id): the todo is
// one that conversation opened, is not done, has no agent on it (one linked and not let go or dismissed) and is not
// taken by a plan that started and has not linked its agent yet. taking: that last one is what the caller is about
// to claim itself, so it is not looked at (takeTodoForTask holds it).
func (s *Service) reusableTodo(conversation, id string, taking bool) error {
	t, ok := s.todoNow(id)
	switch {
	case !ok:
		return notReusable(id, "there is no such todo")
	case conversation == "" || t.Origin != conversation:
		return notReusable(id, "it was not opened from this conversation")
	case t.DoneAt != nil:
		return notReusable(id, "it is already done")
	}
	for _, a := range t.Agents {
		if a.ReleasedAt == nil && a.DismissedAt == nil {
			return notReusable(id, "an agent is already working on it")
		}
	}
	if !taking {
		s.tasks.mu.Lock()
		busy := s.tasks.reusing[id]
		s.tasks.mu.Unlock()
		if busy {
			return notReusable(id, "another plan is starting an agent on it")
		}
	}
	return nil
}

// takeTodoForTask is the todo a started plan's task runs on (PlanTask.ReuseTodo), checked again now (it may have been
// closed or taken since the plan was made) and held until the task's agent is linked (releaseTakenTodo).
func (s *Service) takeTodoForTask(conversation, id string) (Todo, error) {
	s.tasks.mu.Lock()
	if s.tasks.reusing[id] {
		s.tasks.mu.Unlock()
		return Todo{}, notReusable(id, "another plan is starting an agent on it")
	}
	s.tasks.reusing[id] = true
	s.tasks.mu.Unlock()
	if err := s.reusableTodo(conversation, id, true); err != nil {
		s.releaseTakenTodo(id)
		return Todo{}, err
	}
	t, _ := s.todoNow(id)
	return formatTodo(t), nil
}

// releaseTakenTodo lets go of a todo takeTodoForTask held; id "" is nothing.
func (s *Service) releaseTakenTodo(id string) {
	if id == "" {
		return
	}
	s.tasks.mu.Lock()
	delete(s.tasks.reusing, id)
	s.tasks.mu.Unlock()
}

// applyPlanEdit takes what the user changed on the card; an empty field keeps the assistant's.
func applyPlanEdit(t *PlanTask, e PlanTask) {
	if s := strings.TrimSpace(e.Title); s != "" {
		t.Title = s
	}
	if s := strings.TrimSpace(e.Prompt); s != "" {
		t.Prompt = s
	}
	if s := strings.TrimSpace(e.Machine); s != "" {
		t.Machine = s
	}
	if s := strings.TrimSpace(e.Cwd); s != "" {
		t.Cwd = s
	}
	if s := strings.TrimSpace(e.Launcher); s != "" {
		t.Launcher = s
	}
	if s := strings.TrimSpace(e.Model); s != "" {
		t.Model = s
	}
	if s := strings.TrimSpace(e.Tier); s != "" {
		t.Tier = s
	}
	if s := strings.TrimSpace(e.Effort); s != "" {
		t.Effort = s
	}
	if s := strings.TrimSpace(e.Permission); s != "" {
		t.Permission = s
	}
	if e.Ultracode != nil {
		t.Ultracode = e.Ultracode
	}
}

// taskStartTimeout bounds one start of a task's agent: a busy machine's shell can take minutes to run claude
// (node's claudeStartWait, #1396).
var taskStartTimeout = 5 * time.Minute

// taskFailTimeout bounds recording a failed start (the todo, the plan, the receipt), after the start's own time.
const taskFailTimeout = 30 * time.Second

// startPlanTask starts one task's agent and links it to the task's todo. A failed start ends the task: the todo is
// done with why (so nothing shows it as going on, #1077), the plan keeps the error, and the conversation gets a
// receipt — one per task: a retry that fails again (retry) updates the error the receipt shows (clients read it off
// the todo). A task the user finished while its agent was still starting (#2954) is not a failed start: the agent
// that did start stays in the task and the close loop retires it (tasks_close.go).
func (s *Service) startPlanTask(planID, conversation string, i int, t PlanTask, retry bool) {
	ctx, cancel := context.WithTimeout(context.Background(), taskStartTimeout)
	defer cancel()
	placed := t.Machine
	mid, sid, err := s.startTaskAgent(ctx, &t)
	if err != nil && ctx.Err() == nil {
		// The machine's link dropped (a node restarting, a tunnel blinking): try once more after a moment. Any other
		// failure now counts against the launcher there, so the retry goes to a machine where it works, if one has it.
		retry := transientStartErr(err)
		if !retry {
			_, _, retry = s.healthDetour(ctx, Device{ID: t.Machine}, s.startLauncher(ctx, t.Launcher))
		}
		if retry {
			s.logf("task %s: start: %v; retrying", t.TodoID, err)
			select {
			case <-time.After(s.taskStartRetry()):
				mid, sid, err = s.startTaskAgent(ctx, &t)
			case <-ctx.Done():
			}
		}
	}
	if t.Machine != placed { // the card shows where it runs
		s.updatePlanTask(ctx, planID, i, t)
	}
	if err == nil {
		err = s.linkTodoAgent(ctx, t.TodoID, mid, sid, t.Title, t.Cwd, "")
		if errors.Is(err, db.ErrTodoCompleted) {
			// The user tapped "Done" while the agent was starting (a busy machine takes minutes, #1396). The task stays
			// done and its agent stays in it: nothing reads failed, and the close loop still finds the agent to retire.
			err = s.linkTodoAgentOfDoneTask(ctx, t.TodoID, mid, sid, t.Title, t.Cwd, "")
			if err != nil {
				s.logf("task %s: link to the finished task: %v", t.TodoID, err)
			}
		}
	}
	if err == nil {
		return
	}
	s.logf("task %s: start: %v", t.TodoID, err)
	// The start may have used up ctx (a busy machine's shell taking all of taskStartTimeout): recording the failure
	// on it failed too, and the task stayed "starting" with no agent for ever (#4510). The bookkeeping gets its own time.
	ctx, cancel = context.WithTimeout(context.WithoutCancel(ctx), taskFailTimeout)
	defer cancel()
	reason := clip(oneLine(err.Error()), 200)
	if ferr := s.failTodo(ctx, t.TodoID, reason); ferr != nil {
		s.logf("task %s: fail: %v", t.TodoID, ferr)
	}
	if p, lerr := s.loadPlan(ctx, planID); lerr == nil && i < len(p.Tasks) {
		p.Tasks[i].Error = err.Error()
		_, _ = s.movePlan(ctx, p, planStarted)
		s.event(ctx, map[string]any{"type": "task_plan", "plan": p})
	}
	s.tasks.mu.Lock()
	s.tasks.receipted[t.TodoID] = time.Now()
	s.tasks.mu.Unlock()
	if retry {
		return
	}
	_, _ = s.appendFeed(ctx, conversation, feedReceipt, t.TodoID, receiptBody{Title: t.Title, State: "failed",
		Summary: i18n.T(s.lang(ctx), "assistant-tasks.failed_start", i18n.Args{"reason": reason})})
	if todo, ok, err := s.todo(ctx, t.TodoID); err == nil && ok {
		s.taskNotice(ctx, todo, t.Machine, "failed", "", reason)
		s.taskReport(todo, reportFailed, t.Machine, [2]string{}, reason, nil)
	}
}

var (
	errTaskNotRetryable = i18n.Err("assistant-tasks.not_retryable")
	errTaskNotInPlan    = i18n.Err("assistant-tasks.not_in_plan")
	errMachineOffline   = errors.New("machine offline") // matched with errors.Is; the text is machineOffline's
)

// machineOffline is errMachineOffline for machine name, said in the request's language.
func machineOffline(name string) error {
	return offlineError{i18n.Err("assistant-tasks.machine_offline", i18n.Args{"name": name})}
}

type offlineError struct{ msg error }

func (e offlineError) Error() string        { return e.msg.Error() }
func (e offlineError) Unwrap() error        { return e.msg }
func (e offlineError) Is(target error) bool { return target == errMachineOffline }

// retryTask starts again the agent of a task that did not start (#1396): the same task as its plan has it (machine,
// directory, prompt, launcher, model, permission, title), in the same todo — reopened, its error and the plan's
// cleared — so the task shows as starting where it was, not as a second one. A task already
// starting again, or started since, is answered as it is: a double tap starts one agent. A machine that is offline is
// refused before anything changes. A task that is open is not started again: one whose agent the model service cut
// off is told to go on (continueTask).
// The todo it answers is as the apps get it.
func (s *Service) retryTask(ctx context.Context, todoID string) (json.RawMessage, error) {
	if err := s.ensureTodosLoaded(ctx); err != nil {
		return nil, err
	}
	cur, ok, err := s.todo(ctx, todoID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, db.ErrTodoNotFound
	}
	if cur.PlanID == "" || cur.Origin == "" {
		return nil, errTaskNotRetryable
	}
	if cur.DoneAt == nil {
		return s.continueTask(ctx, cur)
	}
	s.tasks.mu.Lock()
	busy := s.tasks.retrying[todoID]
	if !busy && cur.DoneAt != nil && cur.StartError != "" {
		s.tasks.retrying[todoID] = true
	}
	s.tasks.mu.Unlock()
	if busy || cur.DoneAt == nil || cur.StartError == "" {
		if !busy && cur.DoneAt != nil {
			return nil, errTaskNotRetryable // done by hand or by the hub, not a failed start
		}
		return s.todoView(ctx, todoID)
	}
	started := false
	defer func() {
		if !started {
			s.tasks.mu.Lock()
			delete(s.tasks.retrying, todoID)
			s.tasks.mu.Unlock()
		}
	}()
	p, err := s.loadPlan(ctx, cur.PlanID)
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(p.Tasks, func(t PlanTask) bool { return t.TodoID == todoID })
	if i < 0 {
		return nil, errTaskNotInPlan
	}
	t := p.Tasks[i]
	m, ok := s.machineByName(ctx, t.Machine)
	if !ok {
		return nil, i18n.Err("assistant-tasks.no_machine", i18n.Args{"machine": strconv.Quote(t.Machine)})
	}
	if !m.Reachable {
		return nil, machineOffline(m.Name)
	}
	reopened, ok, err := s.reopenFailedTodo(ctx, todoID)
	if err != nil {
		return nil, err
	}
	view, err := json.Marshal(reopened)
	if err != nil {
		return nil, err
	}
	if !ok { // another assistant sharing the store reopened it first
		return view, nil
	}
	p.Tasks[i].Error = ""
	if _, err := s.movePlan(ctx, p, planStarted); err != nil {
		s.logf("plan %s: %v", p.ID, err)
	}
	s.event(ctx, map[string]any{"type": "task_plan", "plan": p})
	s.logf("task %s: retrying its start on %s", todoID, m.ID)
	started = true
	go func() {
		defer func() {
			s.tasks.mu.Lock()
			delete(s.tasks.retrying, todoID)
			s.tasks.mu.Unlock()
		}()
		s.startPlanTask(p.ID, p.Conversation, i, t, true)
	}()
	return view, nil
}

// continueText is what goes on a cut-off turn: what the chat's "Retry" on an API error card sends.
const continueText = "继续"

// continueHold bounds how long a sent "Continue" keeps further taps from sending another while the agent takes it up.
var continueHold = 30 * time.Second

// continueTask tells the agent of a task whose latest turn the model service cut off (#1516) to go on, as the chat's
// "Retry" on the error card does; the task shows as in progress again once the agent works. One "Continue" per cut: taps while
// one is on its way, or before the agent took it up, answer the todo as it is, as does a task with no agent cut off.
// The agent's machine offline is refused.
func (s *Service) continueTask(ctx context.Context, cur Todo) (json.RawMessage, error) {
	var cut proto.Session
	for _, c := range s.taskAgents(ctx, cur) {
		if c.TurnError != "" && (cut.SessionID == "" || c.UpdatedAt.After(cut.UpdatedAt)) {
			cut = c
		}
	}
	if cut.SessionID == "" {
		return s.todoView(ctx, cur.ID)
	}
	m, ok := s.device(ctx, cut.MachineID)
	if !ok || !m.Reachable {
		name := cut.MachineID
		if ok {
			name = m.Name
		}
		return nil, machineOffline(name)
	}
	s.tasks.mu.Lock()
	busy := s.tasks.retrying[cur.ID]
	s.tasks.retrying[cur.ID] = true
	s.tasks.mu.Unlock()
	if busy {
		return s.todoView(ctx, cur.ID)
	}
	release := func() {
		s.tasks.mu.Lock()
		delete(s.tasks.retrying, cur.ID)
		s.tasks.mu.Unlock()
	}
	if err := s.call(ctx, cut.MachineID, DeviceSessionSend, map[string]any{"session_id": cut.SessionID, "text": continueText}, nil); err != nil {
		release()
		return nil, err
	}
	s.logf("task %s: told %s/%s to go on after: %s", cur.ID, cut.MachineID, cut.SessionID, clip(oneLine(cut.TurnError), 120))
	go func() {
		defer release()
		ctx := context.WithoutCancel(ctx)
		for end := time.Now().Add(continueHold); time.Now().Before(end); time.Sleep(200 * time.Millisecond) {
			// Still cut off by the same error: not taken up yet. The session is as the node lists it: Claude Code's error
			// is its last words, a Codex turn's the node's own TurnError (no words of its answer, #2185).
			c, ok := s.session(ctx, cut.MachineID, cut.SessionID)
			if !ok || c.Status == proto.StatusWorking || (strings.TrimSpace(c.LastText) != cut.TurnError && c.TurnError != cut.TurnError) {
				return // it took it up (or is gone): a later cut may be continued again
			}
		}
	}()
	return s.todoView(ctx, cur.ID)
}

// taskStartRetryDelay is how long a task waits before its one retry after a dropped link (Service.taskRetry shortens
// it in tests).
const taskStartRetryDelay = 3 * time.Second

// taskStartRetry is the wait before a failed task start's one retry: taskStartRetryDelay unless tests set taskRetry.
func (s *Service) taskStartRetry() time.Duration {
	if s.taskRetry > 0 {
		return s.taskRetry
	}
	return taskStartRetryDelay
}

// Bounds of the bounded retry before a needs recheck treats a node as unreachable: agentReachBackoff before the
// second try and doubled before the third; agentReachMax bounds one whole recheck, machines included (tests shorten).
var (
	agentReachAttempts = 3
	agentReachBackoff  = 300 * time.Millisecond
	agentReachMax      = 2 * time.Second
)

// transientStartErr: the agent did not start because a link on the way was down or dropped (this hub's to the
// machine, or one further on that answered with the same words: a member hub, a connector), not because the node
// refused; worth one retry.
func transientStartErr(err error) bool {
	var re *rpc.Error
	return errors.Is(err, rpc.ErrClosed) || errors.Is(err, apierr.ErrOffline) ||
		errors.As(err, &re) && re.Message == rpc.ErrClosed.Error()
}

// healthyPlace moves t off a machine where its launcher failed its health check, to one where it works
// (launcher_health.go); its why says so. False: it stays.
func (s *Service) healthyPlace(ctx context.Context, t *PlanTask) bool {
	m, ok := s.machineByName(ctx, t.Machine)
	if !ok {
		return false
	}
	alt, note, moved := s.healthDetour(ctx, m, s.startLauncher(ctx, t.Launcher))
	if note != "" && !strings.Contains(t.Why, note) {
		t.Why = strings.TrimSpace(t.Why + "（" + note + "）")
	}
	if moved {
		t.Machine, t.Cwd = alt.ID, rehome(t.Cwd, m, alt)
	}
	return moved
}

// startTaskAgent starts the agent for t on its machine (another when its launcher does not work there: t says which)
// and answers the machine's id and the session's: the machine it reached is the one the agent is linked to (looking it
// up again after the start could find another, or none). A failed start counts against the launcher there.
func (s *Service) startTaskAgent(ctx context.Context, t *PlanTask) (mid, sid string, err error) {
	params := map[string]any{"launcher": t.Launcher, "model": t.Model, "effort": t.Effort, "tier": t.Tier}
	if err := s.applyStartTier(ctx, params); err != nil {
		return "", "", err
	}
	if err := s.applyModel(ctx, "agent.new", params); err != nil {
		return "", "", err
	}
	t.Launcher, _ = params["launcher"].(string)
	t.Model, _ = params["model"].(string)
	t.Effort, _ = params["effort"].(string)
	s.healthyPlace(ctx, t)
	m, ok := s.machineByName(ctx, t.Machine)
	if !ok {
		return "", "", fmt.Errorf("unknown machine %q", t.Machine)
	}
	prompt := t.Prompt
	if len(t.DoneWhen) > 0 { // what the supervisor will check (supervise.go)
		prompt += "\n\n" + prompts.RenderTaskDoneWhen(t.DoneWhen)
	}
	if !t.Readonly && s.doneNeedsDelivery(ctx) { // what done means: live and checked (delivery.go)
		prompt += "\n\n" + prompts.RenderTaskDelivery()
	}
	// The project background, and the todo's decisions (a task started again may have some), go before the prompt
	// (#2820, #1618); the tab is named from the title.
	prompt = s.withProjectContext(ctx, m.ID, t.Cwd, s.withTodoDecisions(ctx, t.TodoID, prompt))
	params["cwd"], params["prompt"], params["tab_name"] = t.Cwd, prompt, s.tabName(ctx, t.Title)
	if t.Model != "" {
		params["model"] = t.Model
	}
	if t.Permission != "" {
		params["permission_mode"] = t.Permission
	}
	if t.Ultracode != nil && *t.Ultracode {
		params["ultracode"] = true
	}
	var res struct {
		Session proto.Session `json:"session"`
	}
	if err := s.call(ctx, m.ID, DeviceSessionStart, params, &res); err != nil {
		picked, _ := params["launcher"].(string)
		s.recordStartFailure(ctx, m.ID, s.startLauncher(ctx, picked), err)
		return "", "", err
	}
	return m.ID, res.Session.SessionID, nil
}

// updatePlanTask keeps the machine, directory and why task i of started plan planID runs with now.
func (s *Service) updatePlanTask(ctx context.Context, planID string, i int, t PlanTask) {
	p, err := s.loadPlan(ctx, planID)
	if err != nil || i >= len(p.Tasks) {
		return
	}
	p.Tasks[i].Machine, p.Tasks[i].Cwd, p.Tasks[i].Why = t.Machine, t.Cwd, t.Why
	p.Tasks[i].Launcher, p.Tasks[i].Model, p.Tasks[i].Effort = t.Launcher, t.Model, t.Effort
	if _, err := s.movePlan(ctx, p, planStarted); err != nil {
		s.logf("plan %s: %v", planID, err)
		return
	}
	s.event(ctx, map[string]any{"type": "task_plan", "plan": p})
}

func (s *Service) dismissPlan(ctx context.Context, id string) (*TaskPlan, error) {
	p, err := s.loadPlan(ctx, id)
	if err != nil {
		return nil, err
	}
	if p.State != planOpen {
		return nil, errPlanNotOpen
	}
	p.State = planDismissed
	ok, err := s.movePlan(ctx, p, planOpen)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errPlanNotOpen
	}
	s.event(ctx, map[string]any{"type": "task_plan", "plan": p})
	return p, nil
}

// ---- following a task's agents ----

// taskAgents are the sessions of t's active agents, as the clients see them (with state).
func (s *Service) taskAgents(ctx context.Context, t Todo) []proto.Session {
	var out []proto.Session
	for _, a := range t.Agents {
		if a.ReleasedAt != nil || a.DismissedAt != nil {
			continue
		}
		list, err := s.sessions(ctx, a.MachineID)
		if err != nil {
			continue
		}
		for _, c := range list {
			if c.SessionID == a.SessionID {
				out = append(out, c)
				break
			}
		}
	}
	return out
}

// sessionsReached lists machine's sessions, retrying a dropped or offline link a bounded number of times within
// ctx. reached is false when the node could not be asked after the retries: the caller must not read that as
// "the machine has no sessions". ErrNoDevice is deliberately different: the hub itself answers that it has no
// such machine (it was removed from the hub's configuration), a definite answer rather than a flaky node — an
// agent registered on a machine the hub no longer knows cannot still be waiting, so it answers with an empty
// list and reached true. Retrying it would spend the budget and wrongly keep a needs notice for a machine gone
// for good.
func (s *Service) sessionsReached(ctx context.Context, machine string) ([]proto.Session, bool) {
	backoff := agentReachBackoff
	for attempt := 0; ; attempt++ {
		list, err := s.sessions(ctx, machine)
		if err == nil {
			return list, true
		}
		if errors.Is(err, ErrNoDevice) {
			return nil, true // definite: the hub no longer knows this machine
		}
		if attempt+1 >= agentReachAttempts || ctx.Err() != nil {
			return nil, false
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, false
		case <-timer.C:
		}
		backoff *= 2
	}
}

// taskAgentsReached is taskAgents plus reachability. Each distinct machine of t's active agents is asked once
// within one agentReachMax deadline so retries across machines cannot stack; reached is false when one stayed
// unreachable, so callers keep "still waits" rather than read an unreachable node as an answer (ErrNoDevice: none).
func (s *Service) taskAgentsReached(ctx context.Context, t Todo) (agents []proto.Session, reached bool) {
	ctx, cancel := context.WithTimeout(ctx, agentReachMax)
	defer cancel()
	reached = true
	lists, asked := map[string][]proto.Session{}, map[string]bool{}
	for _, a := range t.Agents {
		if a.ReleasedAt != nil || a.DismissedAt != nil || asked[a.MachineID] {
			continue
		}
		asked[a.MachineID] = true
		list, ok := s.sessionsReached(ctx, a.MachineID)
		if !ok {
			reached = false
			continue
		}
		lists[a.MachineID] = list
	}
	for _, a := range t.Agents {
		if a.ReleasedAt != nil || a.DismissedAt != nil {
			continue
		}
		for _, c := range lists[a.MachineID] {
			if c.SessionID == a.SessionID {
				agents = append(agents, c)
				break
			}
		}
	}
	return agents, reached
}

// taskNeedsRecheck freshly asks t's agents' machines whether one still waits for the user. known is false when an
// agent machine stayed unreachable after the bounded retries, so a caller can keep the notice rather than take
// an unreachable node for an answer.
func (s *Service) taskNeedsRecheck(ctx context.Context, t Todo) (waits, known bool) {
	agents, reached := s.taskAgentsReached(ctx, t)
	if !reached {
		return false, false
	}
	for _, c := range agents {
		if c.State == proto.StateNeedsYou || c.PendingInteractions > 0 {
			return true, true
		}
	}
	return false, true
}

// taskWaits: one of t's agents still waits for the user. A machine the recheck cannot reach is not an answer: it
// is taken as still waiting, so a needs notice is never dropped on a transient RPC failure.
func (s *Service) taskWaits(ctx context.Context, t Todo) bool {
	waits, known := s.taskNeedsRecheck(ctx, t)
	return waits || !known
}

// taskWorking: one of the task's agents started a turn. The agent of a task the hub closed meanwhile (judged done,
// reviewed) works on it again — sent more by agent_send, which resumes a closed one, or by the user: the task is open
// again first, as a change asked for on its "Please verify" card reopens it (#3457), so this turn is followed
// (interrupted or done receipt, judgment) and the task shows in progress, not done (#5892).
func (s *Service) taskWorking(ctx context.Context, mid, sid string) {
	s.tasks.completionMu.Lock()
	defer s.tasks.completionMu.Unlock()
	t, ok := s.taskOf(ctx, mid, sid)
	if !ok {
		closed, found := s.closedTaskOf(ctx, mid, sid)
		if !found {
			return
		}
		if err := s.reopenTodo(ctx, closed.ID); err != nil {
			s.logf("task %s: reopen: %v", closed.ID, err)
			return
		}
		if t, ok = s.taskOf(ctx, mid, sid); !ok {
			return
		}
		s.logf("task %s: reopened: its agent works again", t.ID)
	}
	s.tasks.mu.Lock()
	s.tasks.working[t.ID] = time.Now()
	s.tasks.mu.Unlock()
	s.setTaskStage(ctx, t.ID, "develop", "")
	if t.Supervision != nil {
		s.supervisedWorking(ctx, t)
	}
}

type receiptBody struct {
	Title   string         `json:"title"`
	State   string         `json:"state"` // done, failed, interrupted
	Summary string         `json:"summary,omitempty"`
	PR      string         `json:"pr,omitempty"`
	Machine string         `json:"machine,omitempty"`
	Review  *receiptReview `json:"review,omitempty"`
	// Error: what the model service answered when the turn was cut off (interrupted), for the card's detail.
	Error string `json:"error,omitempty"`
}

type receiptReview struct {
	State    string `json:"state"` // awaiting
	Criteria int    `json:"criteria"`
}

// interruptedSummary is an interrupted task's receipt: the error itself is no conclusion (#1516).
const interruptedSummary = assistantwire.InterruptedSummary

// silentTurnError is the interrupted receipt's detail for a turn with no output at all (silentTurn, #2349), in the
// default language (silentTurnErrorIn: the user's).
var silentTurnError = silentTurnErrorIn(i18n.Default)

func silentTurnErrorIn(l i18n.Lang) string { return i18n.T(l, "assistant-tasks.silent_turn") }

var prLink = regexp.MustCompile(`https://github\.com/[\w.-]+/[\w.-]+/pull/\d+`)

// taskTurnEnded: one of the task's agents (sid on machine) ended a turn. Once none of them works or waits for the
// user any more, the conversation gets a receipt (once per stretch of work), and the todo is closed if the judge
// finds the work finished.
func (s *Service) taskTurnEnded(ctx context.Context, machine, sid string) {
	time.Sleep(s.settleDelay(ctx)) // a turn that ends to ask, or goes on at once, is not the end
	t, ok := s.taskOf(ctx, machine, sid)
	if !ok {
		return
	}
	agents, reached := s.taskAgentsReached(ctx, t)
	if !reached || !taskAgentsSettled(t, agents) {
		return
	}
	var last, cut proto.Session
	for _, c := range agents {
		if c.State == proto.StateWorking || c.State == proto.StateNeedsYou || c.PendingInteractions > 0 {
			return
		}
		if w := c.Work; w != nil && w.Phase == proto.WorkBlocked && w.On == proto.WaitTransient && w.WakeAt != nil {
			return // "Continue" goes by itself (agents_work.go): the receipt waits for how that ends
		}
		if c.UpdatedAt.After(last.UpdatedAt) {
			last = c
		}
		// The node's word, whatever the state now; or, once it stopped, Claude Code's last words when they are the
		// error ("API Error: …", "Not logged in · …"): the node's raw list has no turn error for those (fleet.go
		// turnError reads them for the apps).
		text, kind := strings.TrimSpace(c.TurnError), c.TurnErrorKind
		if text == "" {
			text, kind = turnError(c)
		}
		if text != "" && (cut.SessionID == "" || c.UpdatedAt.After(cut.UpdatedAt)) {
			cut = c
			cut.TurnError, cut.TurnErrorKind = text, kind
		}
	}
	s.tasks.mu.Lock()
	since, worked := s.tasks.working[t.ID]
	if !worked {
		since = t.CreatedAt // a hub restarted since it began: count from the start
	}
	if r, ok := s.tasks.receipted[t.ID]; ok && !r.Before(since) {
		s.tasks.mu.Unlock()
		return
	}
	s.tasks.receipted[t.ID] = time.Now()
	s.tasks.mu.Unlock()
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	name := machine
	if m, ok := s.device(cctx, machine); ok {
		name = m.Name
	}
	if !s.taskWorkCurrent(t.ID, since) || !s.taskRuntimeSettled(cctx, t) {
		return
	}
	if cut.SessionID != "" {
		// A turn cut off by the model service is no end of the work: an interrupted receipt (with "Continue" on the card),
		// and nothing to judge or close (#1516).
		s.logf("task %s: interrupted: %s", t.ID, clip(oneLine(cut.TurnError), 200))
		where := [2]string{cut.MachineID, cut.SessionID}
		body := receiptBody{Title: t.Title, State: "interrupted", Summary: i18n.T(s.lang(cctx), "assistant-tasks.interrupted"), Machine: name,
			Error: clip(strings.TrimSpace(cut.TurnError), 300)}
		if _, err := s.appendFeed(cctx, t.Origin, feedReceipt, t.ID, body); err != nil {
			s.logf("task %s: receipt: %v", t.ID, err)
		}
		s.taskNotice(cctx, t, name, "interrupted", "", body.Error)
		s.taskReport(t, reportInterrupted, name, where, body.Error, nil)
		return
	}
	items, read := s.lastTurnItems(cctx, last.MachineID, last.SessionID)
	if !s.taskWorkCurrent(t.ID, since) || !s.taskRuntimeSettled(cctx, t) {
		return
	}
	if read && taskTurnAwaitingAnswer(items) && !silentTurn(items) {
		// Do not consume this stretch's receipt while the runtime is between tools and its answer.
		s.tasks.mu.Lock()
		if !s.tasks.working[t.ID].After(since) {
			delete(s.tasks.receipted, t.ID)
			if s.tasks.awaitingAnswer == nil {
				s.tasks.awaitingAnswer = make(map[SessionRef]bool)
			}
			s.tasks.awaitingAnswer[SessionRef{MachineID: machine, SessionID: sid}] = true
		}
		s.tasks.mu.Unlock()
		// The answer's update may have arrived during the transcript read, before
		// the retry was registered. Reconcile that update instead of waiting for another tick.
		if latest, ok := s.session(cctx, last.MachineID, last.SessionID); ok &&
			(latest.Status != last.Status || latest.Rev != last.Rev || latest.LastText != last.LastText || !latest.UpdatedAt.Equal(last.UpdatedAt)) &&
			s.takeTaskAnswerRetry(machine, sid) {
			go s.taskTurnEnded(context.WithoutCancel(ctx), machine, sid)
		}
		return
	}
	if read && silentTurn(items) {
		// No reply and no tool call, yet no error either (the model service answered 200 with nothing in it): no end
		// of the work. Interrupted, as a cut-off turn is, with nothing to judge or close (#2349).
		s.logf("task %s: interrupted: the turn had no output", t.ID)
		where := [2]string{last.MachineID, last.SessionID}
		l := s.lang(cctx)
		body := receiptBody{Title: t.Title, State: "interrupted", Summary: i18n.T(l, "assistant-tasks.interrupted"), Machine: name,
			Error: silentTurnErrorIn(l)}
		if _, err := s.appendFeed(cctx, t.Origin, feedReceipt, t.ID, body); err != nil {
			s.logf("task %s: receipt: %v", t.ID, err)
		}
		s.taskNotice(cctx, t, name, "interrupted", "", body.Error)
		s.taskReport(t, reportInterrupted, name, where, body.Error, nil)
		return
	}
	// The whole last reply, as Markdown: the session list's last_text is one line cut at 200 characters.
	text := strings.TrimSpace(last.LastText)
	if _, answer := turnExchange(items); answer != "" {
		text = answer
	}
	body := receiptBody{Title: t.Title, State: "done", Summary: clipMarkdown(text, 600), Machine: name}
	// Persona follows up on a supervised stop through sigReview or sigIdle after judgment; the receipt need not wait.
	// needs_user waits on the user instead. Read the latest todo in case it changed while reading the reply.
	if current, ok := s.todoNow(t.ID); ok && current.DoneAt == nil {
		if v := current.Supervision; v != nil && v.state(current) != reviewNeedsUser {
			body.Review = &receiptReview{State: "awaiting", Criteria: len(v.DoneWhen)}
		}
	}
	if body.Summary == "" {
		body.Summary = i18n.T(s.lang(cctx), "assistant-tasks.done")
	}
	if pr := prLink.FindAllString(text, -1); len(pr) > 0 {
		body.PR = pr[len(pr)-1]
	}
	if _, err := s.appendFeed(cctx, t.Origin, feedReceipt, t.ID, body); err != nil {
		s.logf("task %s: receipt: %v", t.ID, err)
	}
	s.taskNotice(cctx, t, name, "done", "", clipMarkdown(text, 1500))
	stage, pr := stageOf(text)
	s.setTaskStage(cctx, t.ID, stage, pr)
	if stageIndex(stage) >= stageIndex("deploy") {
		go s.askAcceptance(t, last.MachineID, last.SessionID, text) // the user tries it (acceptance.go)
	}
	s.taskReport(t, reportDone, name, [2]string{last.MachineID, last.SessionID}, text, nil)
	s.autoFinishTask(t, since, worked) // the work may be finished: then the todo is done (done.go)
}

// routeToTask sends text straight to the agent of task todoID that most needs it, and keeps it in the conversation.
func (s *Service) routeToTask(ctx context.Context, conversation, todoID, text string) (FeedItem, error) {
	if err := s.ensureTodosLoaded(ctx); err != nil {
		return FeedItem{}, err
	}
	t, ok, err := s.todo(ctx, todoID)
	if err != nil {
		return FeedItem{}, err
	}
	if !ok || t.Origin != conversation {
		return FeedItem{}, i18n.Err("assistant-tasks.route_no_task")
	}
	agents := s.taskAgents(ctx, t)
	if len(agents) == 0 {
		return FeedItem{}, i18n.Err("assistant-tasks.route_no_agent")
	}
	c := pickTaskAgent(agents)
	if _, ok := s.device(ctx, c.MachineID); !ok {
		return FeedItem{}, i18n.Err("assistant-tasks.route_offline")
	}
	// An agent closed after its task was done (tasks_close.go) is resumed first: that can take minutes, and goes
	// on when the phone stops waiting, as a send from the chat does.
	wait := 90 * time.Second
	if c.Status == proto.StatusOffline {
		wait = 5 * time.Minute
	}
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), wait)
	defer cancel()
	if err := s.call(sctx, c.MachineID, DeviceSessionSend, map[string]any{"session_id": c.SessionID, "text": text}, nil); err != nil {
		return FeedItem{}, err
	}
	return s.appendFeed(sctx, conversation, feedRouted, todoID, map[string]string{"text": text})
}

// ---- messages about a task ----

// messageCard puts the card into the conversation that opened the task (none when the task is gone or done).
func (s *Service) messageCard(ctx context.Context, itemID string, body MessageCard) {
	t, ok, err := s.todo(ctx, body.TodoID)
	if err != nil || !ok || t.DoneAt != nil || t.Origin == "" {
		return
	}
	if _, err := s.appendFeed(ctx, t.Origin, feedMessage, itemID, body); err != nil {
		s.logf("task %s: message card: %v", body.TodoID, err)
	}
}

// markCardsSent records on the cards about an inbox item put in by at that its reply went out (text: what it said;
// empty: not known) and tells the clients (tell), which draw them sent; the cards it changed. The inbox says when a
// reply went out (replyInboxAs).
func (s *Service) markCardsSent(ctx context.Context, itemID string, at time.Time, text string, tell bool) []FeedItem {
	at = at.UTC()
	patch, _ := json.Marshal(struct {
		SentAt time.Time `json:"sent_at"`
		Sent   string    `json:"sent,omitempty"`
	}{at, text})
	var out []FeedItem
	if st := s.store(); st == nil {
		s.tasks.mu.Lock()
		for c, list := range s.tasks.feed {
			for i, it := range list {
				if it.Kind != feedMessage || it.Ref != itemID || it.At.After(at) || cardSentAt(it) != nil {
					continue
				}
				var body map[string]json.RawMessage
				_ = json.Unmarshal(it.Body, &body)
				if body == nil {
					body = map[string]json.RawMessage{}
				}
				var p map[string]json.RawMessage
				_ = json.Unmarshal(patch, &p)
				for k, v := range p {
					body[k] = v
				}
				it.Body, _ = json.Marshal(body)
				s.tasks.feed[c][i] = it
				out = append(out, it)
			}
		}
		s.tasks.mu.Unlock()
	} else {
		rows, err := st.MarkFeedSent(ctx, feedMessage, itemID, at, patch)
		if err != nil {
			s.logf("inbox %s: mark the cards sent: %v", itemID, err)
		}
		for _, r := range rows {
			out = append(out, FeedItem{ID: r.ID, Conversation: r.Conversation, At: r.At, Kind: r.Kind, Ref: r.Ref, Body: r.Body})
		}
	}
	if tell {
		for _, it := range out {
			s.event(ctx, map[string]any{"type": "assistant_feed", "conversation": it.Conversation, "item": it})
		}
	}
	return out
}

// cardSentAt is when a message card's reply went out; nil while it has not.
func cardSentAt(it FeedItem) *time.Time {
	var b struct {
		SentAt *time.Time `json:"sent_at"`
	}
	_ = json.Unmarshal(it.Body, &b)
	return b.SentAt
}

// settleCards fills in the cards among items whose item was replied to after they were put in but which do not say
// so: cards from before it was recorded on them (the reply went out, the card still offered "Send"). When the items
// were replied to is the inbox's (inboxRepliedAt).
func (s *Service) settleCards(ctx context.Context, items []FeedItem) {
	var ids []string
	for _, it := range items {
		if it.Kind == feedMessage && cardSentAt(it) == nil && !slices.Contains(ids, it.Ref) {
			ids = append(ids, it.Ref)
		}
	}
	if len(ids) == 0 {
		return
	}
	replied := s.inboxRepliedAt(ctx, ids)
	for _, it := range items {
		if it.Kind != feedMessage || cardSentAt(it) != nil {
			continue
		}
		at, ok := replied[it.Ref]
		if !ok || at.Before(it.At) {
			continue
		}
		for _, m := range s.markCardsSent(ctx, it.Ref, at, "", false) {
			for j := range items {
				if items[j].ID == m.ID {
					items[j] = m
				}
			}
		}
	}
}

// fileCards adds filed_at to the cards among items that do not say sent_at and whose item is filed (Done, Dismiss, read
// elsewhere, archived: not open), as of when it was filed: Send on them is refused (inbox_item_done), so the apps
// hide it. It is worked out on each read and not stored: moved back to the inbox, the item is open and the card
// offers Send again.
func (s *Service) fileCards(ctx context.Context, items []FeedItem) {
	var ids []string
	for _, it := range items {
		if it.Kind == feedMessage && cardSentAt(it) == nil && !slices.Contains(ids, it.Ref) {
			ids = append(ids, it.Ref)
		}
	}
	if len(ids) == 0 {
		return
	}
	filed := s.inboxFiledAt(ctx, ids)
	for i, it := range items {
		at, ok := filed[it.Ref]
		if it.Kind != feedMessage || !ok || cardSentAt(it) != nil {
			continue
		}
		var body map[string]json.RawMessage
		_ = json.Unmarshal(it.Body, &body)
		if body == nil {
			body = map[string]json.RawMessage{}
		}
		body["filed_at"], _ = json.Marshal(at.UTC())
		items[i].Body, _ = json.Marshal(body)
	}
}

// cardSent is whether card cardID, about inbox item itemID, says its reply went out ("Send" on the card sends once per
// card: sendFromCard).
func (s *Service) cardSent(ctx context.Context, cardID, itemID string) (bool, error) {
	card, err := s.feedItem(ctx, cardID)
	if err != nil {
		return false, err
	}
	return card != nil && card.Kind == feedMessage && card.Ref == itemID && cardSentAt(*card) != nil, nil
}

// feedItem is one feed item by its id; nil when there is none.
func (s *Service) feedItem(ctx context.Context, id string) (*FeedItem, error) {
	st := s.store()
	if st == nil {
		s.tasks.mu.Lock()
		defer s.tasks.mu.Unlock()
		for _, list := range s.tasks.feed {
			for _, it := range list {
				if it.ID == id {
					return &it, nil
				}
			}
		}
		return nil, nil
	}
	r, err := st.FeedItem(ctx, id)
	if err != nil || r == nil {
		return nil, err
	}
	return &FeedItem{ID: r.ID, Conversation: r.Conversation, At: r.At, Kind: r.Kind, Ref: r.Ref, Body: r.Body}, nil
}

// activeTasks are the tasks going on, as the inbox's judge and AI twin see them.
func (s *Service) activeTasks(ctx context.Context) []prompts.InboxTask {
	var out []prompts.InboxTask
	for _, t := range s.snapshotTasks(ctx, "") {
		if t.State == taskStateLabel("done") || t.State == taskStateLabel("auto_done") || t.State == taskStateLabel("failed") {
			continue
		}
		out = append(out, prompts.InboxTask{ID: t.ID, Title: t.Title, State: t.State, Last: t.Last})
	}
	return out
}

// ---- what the assistant sees ----

// snapshotTasks are the tasks opened from assistant conversations that are going on or ended in the last day.
func (s *Service) snapshotTasks(ctx context.Context, web string) []prompts.SnapshotTask {
	var list []Todo
	for _, t := range s.todosOf(ctx, "*") {
		if t.Origin != "" && (t.DoneAt == nil || time.Since(*t.DoneAt) < 24*time.Hour) && time.Since(t.UpdatedAt) < 7*24*time.Hour {
			list = append(list, t)
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].CreatedAt.After(list[j].CreatedAt) })
	type ranked struct {
		task prompts.SnapshotTask
		rank int
	}
	var all []ranked
	now := time.Now()
	for _, t := range list {
		agents := s.taskAgents(ctx, t)
		st := taskState(t, agents)
		if st == "followup" && time.Since(t.UpdatedAt) > 24*time.Hour {
			continue
		}
		if st == "done" && t.DoneBy == db.DoneByAuto {
			st = "auto_done"
		}
		task := snapshotTask(t, st, agents, now)
		if st == "failed" && task.Last == "" {
			task.Last = clip(oneLine(t.StartError), 80) // why it did not start, for the morning brief (#4124)
		}
		task.Link = todoLink(web, t.ID)
		task.Delivery = deliverySummary(t)
		var rank int
		task.Health, rank = taskHealth(t, agents, now) // supervised tasks: what needs the assistant first (supervise.go)
		if t.Supervision != nil {
			task.DoneWhen = len(t.Supervision.DoneWhen)
			if p := t.Supervision.Progress; p != nil {
				reported(&task, p, taskLead(st, agents), now)
			}
		}
		all = append(all, ranked{task, rank})
		if len(all) == 30 {
			break
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].rank < all[j].rank })
	var out []prompts.SnapshotTask
	for _, r := range all {
		out = append(out, r.task)
		if len(out) == 12 {
			break
		}
	}
	return out
}

// snapshotTask is one task in the snapshot, in state st: its lead agent's (taskLead) last words, the step it is on
// while it works (or has said nothing yet: an agent that only runs tools, #1583) and its running subagents, as the
// task cards show them (docs/assistant/unified-entry.md "Progress on task cards").
func snapshotTask(t Todo, st string, agents []proto.Session, now time.Time) prompts.SnapshotTask {
	task := prompts.SnapshotTask{ID: t.ID, Title: t.Title, State: taskStateLabel(st)}
	last := taskLead(st, agents)
	if last.SessionID == "" {
		return task
	}
	task.Machine, task.Session = last.MachineID, short(last.SessionID)
	task.Last = clip(oneLine(last.LastText), 80)
	working := last.State == proto.StateWorking
	if doing := strings.TrimSpace(last.Activity); doing != "" && (working || task.Last == "") {
		task.Doing = doing
		if working && last.ActivityAt != nil && now.Sub(*last.ActivityAt) >= time.Minute {
			task.DoingFor = humanDuration(now.Sub(*last.ActivityAt))
		}
	}
	for _, sa := range last.Subagents {
		if sa.Running && len(task.Subagents) < 3 {
			task.Subagents = append(task.Subagents, clip(oneLine(cmp.Or(sa.Description, sa.Type, "子任务")), 40))
		}
	}
	task.Workflows = runningWorkflows(last, now)
	task.Plan = snapshotPlan(last)
	return task
}

// reported puts the agent's latest report p on task. A report newer than lead's plan says more than the plan's count:
// it stands alone.
func reported(task *prompts.SnapshotTask, p *TaskProgress, lead proto.Session, now time.Time) {
	task.Reported = &prompts.SnapshotReport{Kind: p.Kind, Step: p.Step, Steps: p.Steps, Text: p.Text,
		Minutes: max(0, int(now.Sub(p.At).Minutes()))}
	if lead.Plan != nil && p.At.After(lead.Plan.At) {
		task.Plan = nil
	}
}

// snapshotPlan is c's own task list as the snapshot tells it, nil when it keeps none (docs/integrations/agent-goal.md "Plan").
func snapshotPlan(c proto.Session) *prompts.SnapshotAgentPlan {
	p := c.Plan
	if p == nil || p.Total == 0 {
		return nil
	}
	return &prompts.SnapshotAgentPlan{Done: p.Done, Total: p.Total, Current: p.Current}
}

// taskLead is the agent a task in state st is shown with: the latest of those that put it in that state (the one
// waiting for the user, cut off, finished unseen, working), so its session id and words are the ones the state is
// about (#4192); the latest of all when none does (a task that stopped, or is done).
func taskLead(st string, agents []proto.Session) proto.Session {
	var lead, latest proto.Session
	for _, c := range agents {
		if latest.SessionID == "" || c.UpdatedAt.After(latest.UpdatedAt) {
			latest = c
		}
		var makes bool
		switch st {
		case "needs_you":
			makes = c.State == proto.StateNeedsYou || c.PendingInteractions > 0
		case "interrupted":
			makes = c.TurnError != ""
		case "unread":
			makes = c.State == proto.StateUnread
		case "working":
			makes = c.State == proto.StateWorking
		}
		if makes && (lead.SessionID == "" || c.UpdatedAt.After(lead.UpdatedAt)) {
			lead = c
		}
	}
	if lead.SessionID != "" {
		return lead
	}
	return latest
}

// taskState follows docs/assistant/todos.md "How the state is computed"(clients compute the same).
func taskState(t Todo, agents []proto.Session) string {
	if t.DoneAt != nil && t.StartError != "" {
		return "failed"
	}
	if t.DoneAt != nil {
		return "done"
	}
	has := map[string]bool{}
	interrupted := false
	for _, c := range agents {
		has[c.State] = true
		interrupted = interrupted || c.TurnError != ""
	}
	switch {
	case has[proto.StateNeedsYou]:
		return "needs_you"
	case interrupted: // stopped on a model API error: not done, waiting for "Continue" (#1516)
		return "interrupted"
	case has[proto.StateUnread]:
		return "unread"
	case has[proto.StateWorking]:
		return "working"
	case len(agents) > 0:
		return "followup"
	}
	return "not_started"
}

func taskStateLabel(st string) string {
	return map[string]string{"done": "已完成", "auto_done": "已完成（hub 判断做完了，自动关的）", "failed": "没开起来", "needs_you": "等用户",
		"interrupted": "模型服务出错，中断了（没做完，等用户让它继续）", "unread": "做完了（用户还没看）", "working": "在做",
		"followup": "停着", "not_started": "还没开始"}[st]
}

// ---- HTTP ----

func (s *Service) taskRoutes() {
	s.doneCheckRoute()
	s.handle("GET /api/v1/task-plans/{id}", func(w http.ResponseWriter, r *http.Request) {
		p, err := s.loadPlan(r.Context(), r.PathValue("id"))
		if err != nil {
			planErr(w, err)
			return
		}
		apierr.WriteJSON(w, map[string]any{"plan": p})
	})
	s.handle("POST /api/v1/task-plans/{id}/start", func(w http.ResponseWriter, r *http.Request) {
		if !fullRole(r) {
			apierr.Write(w, http.StatusForbidden, errors.New("read-only role cannot start tasks"))
			return
		}
		var in struct {
			Tasks    []PlanTask `json:"tasks"`
			Remember bool       `json:"remember"`
		}
		if r.ContentLength != 0 {
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
				apierr.Write(w, http.StatusBadRequest, err)
				return
			}
		}
		p, err := s.startPlan(r.Context(), r.PathValue("id"), in.Tasks, in.Remember)
		if err != nil {
			planErr(w, err)
			return
		}
		apierr.WriteJSON(w, map[string]any{"plan": p})
	})
	s.handle("POST /api/v1/task-plans/{id}/dismiss", func(w http.ResponseWriter, r *http.Request) {
		if !fullRole(r) {
			apierr.Write(w, http.StatusForbidden, errors.New("read-only role cannot change tasks"))
			return
		}
		p, err := s.dismissPlan(r.Context(), r.PathValue("id"))
		if err != nil {
			planErr(w, err)
			return
		}
		apierr.WriteJSON(w, map[string]any{"plan": p})
	})
	s.handle("POST /api/v1/todos/{id}/retry", func(w http.ResponseWriter, r *http.Request) {
		if !fullRole(r) {
			apierr.Write(w, http.StatusForbidden, errors.New("read-only role cannot start tasks"))
			return
		}
		t, err := s.retryTask(r.Context(), r.PathValue("id"))
		switch {
		case err == nil:
			apierr.WriteJSON(w, map[string]any{"todo": t})
		case errors.Is(err, db.ErrTodoNotFound):
			apierr.Write(w, http.StatusNotFound, err)
		case errors.Is(err, errTaskNotRetryable), errors.Is(err, errTaskNotInPlan), errors.Is(err, errMachineOffline):
			apierr.Write(w, http.StatusConflict, err)
		default:
			planErr(w, err)
		}
	})
	s.handle("GET /api/v1/assistant/conversations/{id}/feed", func(w http.ResponseWriter, r *http.Request) {
		var since time.Time
		if s := r.URL.Query().Get("since"); s != "" {
			t, err := time.Parse(time.RFC3339Nano, s)
			if err != nil {
				apierr.Write(w, http.StatusBadRequest, fmt.Errorf("since: %v", err))
				return
			}
			since = t
		}
		items, plans, err := s.feed(r.Context(), r.PathValue("id"), since)
		if err != nil {
			apierr.Write(w, http.StatusServiceUnavailable, err)
			return
		}
		if items == nil {
			items = []FeedItem{}
		}
		views, err := s.conversationViews(r.Context(), r.PathValue("id"))
		if err != nil {
			apierr.Write(w, http.StatusServiceUnavailable, err)
			return
		}
		actions, err := s.conversationViewActions(r.Context(), r.PathValue("id"))
		if err != nil {
			apierr.Write(w, http.StatusServiceUnavailable, err)
			return
		}
		blocks, err := s.conversationViewBlockKeys(r.Context(), r.PathValue("id"))
		if err != nil {
			apierr.Write(w, http.StatusServiceUnavailable, err)
			return
		}
		apierr.WriteJSON(w, map[string]any{"items": items, "plans": plans, "views": views, "view_actions": actions, "view_blocks": blocks})
	})
	s.handle("POST /api/v1/assistant/conversations/{id}/route", func(w http.ResponseWriter, r *http.Request) {
		if !fullRole(r) {
			apierr.Write(w, http.StatusForbidden, errors.New("read-only role cannot send"))
			return
		}
		var in struct {
			TodoID string `json:"todo_id"`
			Text   string `json:"text"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
			apierr.Write(w, http.StatusBadRequest, err)
			return
		}
		if strings.TrimSpace(in.Text) == "" || in.TodoID == "" {
			apierr.Write(w, http.StatusBadRequest, errors.New("todo_id and text are required"))
			return
		}
		it, err := s.routeToTask(r.Context(), r.PathValue("id"), in.TodoID, in.Text)
		if err != nil {
			apierr.Write(w, http.StatusConflict, err)
			return
		}
		apierr.WriteJSON(w, map[string]any{"item": it})
	})
}

func planErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errPlanNotFound):
		apierr.Write(w, http.StatusNotFound, err)
	case errors.Is(err, errPlanNotOpen):
		apierr.Write(w, http.StatusConflict, err)
	default:
		apierr.Write(w, http.StatusServiceUnavailable, err)
	}
}
