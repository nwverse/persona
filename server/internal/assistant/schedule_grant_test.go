package assistant

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// grantFixture is a task service on box (repoMachine) with one scheduled follow-up, "deps", that runs into conversation
// sched on box: the owner set it up to update the repository's dependencies.
type grantFixture struct {
	t *testing.T
	s *Service
	f *fakeHub
}

func newGrantFixture(t *testing.T, tasks ...Task) *grantFixture {
	t.Helper()
	st := &profileTaskStore{memStore: &memStore{}, plans: map[string]db.TaskPlanRow{}}
	s, f := newTaskServiceOn(t, st)
	repoMachine(f, nil)
	if len(tasks) == 0 {
		tasks = []Task{depsTask()}
	}
	setTasks(s, tasks...)
	return &grantFixture{t: t, s: s, f: f}
}

func depsTask() Task {
	return Task{ID: "deps", Title: "Dependency update", Prompt: "Update the app's dependencies and open a PR", Repeat: repeatDaily,
		Time: "09:00", Enabled: true, MachineID: "box", SessionID: "sched", CreatedBy: "assistant"}
}

// round runs task id the way the scheduler does and gives its conversation the turn that round started: the prompt
// the hub sent, written now, then whatever the agent did (extra). It returns the conversation's machine and id.
func (g *grantFixture) round(id string, earlier []proto.Item, extra ...proto.Item) (string, string) {
	g.t.Helper()
	task, ok := taskByID(g.s, id)
	if !ok {
		g.t.Fatalf("no task %s", id)
	}
	now := time.Now()
	sess, err := g.s.runTask(task, now, now, false, false)
	if err != nil {
		g.t.Fatal(err)
	}
	text, _ := g.f.lastSend(g.t)["text"].(string)
	if !strings.Contains(text, "message-source") {
		text = prompts.MessageSourceText(text, taskMessageSource(task))
	}
	items := slices.Clone(earlier)
	items = append(items, proto.Item{Role: "user", Kind: proto.KindText, Text: text, TS: time.Now()})
	items = append(items, extra...)
	numberItems(items)
	g.f.setItems(items...)
	return sess.MachineID, sess.SessionID
}

// numberItems gives items the seqs a transcript would (from 1).
func numberItems(items []proto.Item) {
	for i := range items {
		items[i].Seq = i + 1
	}
}

// plan is task_plan from conversation session on machine with one writable task in cwd; it returns the plan's id
// and what the tool said.
func (g *grantFixture) plan(machine, session, cwd string) (string, string) {
	g.t.Helper()
	raw, _ := json.Marshal(map[string]any{"tasks": []any{map[string]any{"title": "Update dependencies", "prompt": "Update and open a PR",
		"machine": "box", "cwd": cwd, "permission": "bypassPermissions", "why": "the repository is on box", "schedule": "other",
		"schedule_title": "Forged"}}})
	out, err := g.s.toolTaskPlanFrom(context.Background(), machine, session, raw)
	if err != nil {
		g.t.Fatal(err)
	}
	return planIDPattern.FindString(out), out
}

func waitsForStart(out string) bool { return strings.Contains(out, "nothing has started") }

func toolUse(name string) proto.Item {
	return proto.Item{Role: "assistant", Kind: proto.KindToolUse, ToolName: name, ToolInput: json.RawMessage(`{}`)}
}

func toolResult() proto.Item {
	return proto.Item{Role: "user", Kind: proto.KindToolResult, Text: "ok"}
}

// The owner asked for a schedule that updates the dependencies; the first round's task waits on its card, which names
// the schedule. The owner starts it and lets the schedule start these by itself: the next round's task starts at once
// (#8045). On main the second round waited for Start too.
func TestScheduledRoundStartsTheKindItsOwnerGranted(t *testing.T) {
	g := newGrantFixture(t)
	machine, session := g.round("deps", nil)
	id, out := g.plan(machine, session, "/wt/app/deps-1")
	if !waitsForStart(out) || id == "" {
		t.Fatalf("a round with no grant started a writable task: %s", out)
	}
	p, err := g.s.loadPlan(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if p.Tasks[0].Schedule != "deps" || p.Tasks[0].ScheduleTitle != "Dependency update" {
		t.Fatalf("the card does not name the round's schedule (or kept the tool call's): %+v", p.Tasks[0])
	}
	w := httptest.NewRecorder()
	g.s.ServeHTTP(w, httptest.NewRequest("POST", "/api/v1/task-plans/"+id+"/start", strings.NewReader(`{"schedule_grant":true}`)))
	if w.Code != http.StatusOK {
		t.Fatalf("the owner's Start: %d %s", w.Code, w.Body)
	}
	waitFor(t, "the first task's agent", func() bool { return fakeStarts(g.f) == 1 })
	waitFor(t, "the schedule's grant", func() bool {
		task, _ := taskByID(g.s, "deps")
		return task.liveGrant() != nil && slices.Equal(task.StartGrant.Kinds, []string{"box|/code/app|edit"})
	})
	if got := storedTrust(t, g.s.store().(*profileTaskStore).memStore); len(got) != 0 {
		t.Fatalf("the schedule's grant went to the trust list too: %v", got)
	}

	machine, session = g.round("deps", nil, toolUse("ToolSearch"), toolResult(), toolUse("mcp__persona__machines_list"), toolResult(),
		toolUse("mcp__persona__task_plan"))
	if _, out := g.plan(machine, session, "/wt/app/deps-2"); !strings.Contains(out, "without asking") {
		t.Fatalf("the granted schedule's next round waited for Start: %s", out)
	}
	waitFor(t, "the second task's agent", func() bool { return fakeStarts(g.f) == 2 })
}

// What the grant does not cover keeps the card: a schedule with no grant, another kind, another schedule's grant, a
// turn the hub did not start, and a Start that is not the owner's.
func TestScheduledRoundWithoutTheOwnersGrantWaits(t *testing.T) {
	granted := depsTask()
	granted.StartGrant = grantFor(granted, time.Now())
	granted.StartGrant.Kinds = []string{"box|/code/app|edit"}
	other := depsTask()
	other.ID, other.Title, other.SessionID = "lint", "Lint fixes", "lintconv"

	t.Run("no grant", func(t *testing.T) {
		g := newGrantFixture(t)
		machine, session := g.round("deps", nil)
		if _, out := g.plan(machine, session, "/wt/app/x"); !waitsForStart(out) {
			t.Fatalf("an ungranted round started: %s", out)
		}
	})
	t.Run("another repository", func(t *testing.T) {
		g := newGrantFixture(t, granted)
		machine, session := g.round("deps", nil)
		if _, out := g.plan(machine, session, "/wt/other/x"); !waitsForStart(out) {
			t.Fatalf("a kind outside the grant started: %s", out)
		}
	})
	t.Run("another schedule", func(t *testing.T) {
		g := newGrantFixture(t, granted, other)
		machine, session := g.round("lint", nil)
		if _, out := g.plan(machine, session, "/wt/app/x"); !waitsForStart(out) {
			t.Fatalf("another schedule's round used deps' grant: %s", out)
		}
	})
	t.Run("not a round the hub started", func(t *testing.T) {
		g := newGrantFixture(t, granted)
		machine, session := g.round("deps", nil)
		g.s.endRound(machine, session, "deps") // its turn ended; the owner then writes in the same conversation
		owner := proto.Item{Role: "user", Kind: proto.KindText, Text: "also bump the linter", ClientMsgID: "m-1", TS: time.Now()}
		items := []proto.Item{owner}
		numberItems(items)
		g.f.setItems(items...)
		if _, out := g.plan(machine, session, "/wt/app/x"); !waitsForStart(out) {
			t.Fatalf("an owner's turn used the schedule's grant: %s", out)
		}
	})
	t.Run("an old prompt in the turn", func(t *testing.T) {
		g := newGrantFixture(t, granted)
		machine, session := g.round("deps", nil)
		g.f.mu.Lock()
		items := slices.Clone(g.f.items)
		g.f.mu.Unlock()
		items[len(items)-1].TS = time.Now().Add(-3 * time.Hour) // a prompt from before this round
		g.f.setItems(items...)
		if _, out := g.plan(machine, session, "/wt/app/x"); !waitsForStart(out) {
			t.Fatalf("a turn older than the round used the grant: %s", out)
		}
	})
	t.Run("the schedule changed before Start", func(t *testing.T) {
		g := newGrantFixture(t)
		machine, session := g.round("deps", nil)
		id, _ := g.plan(machine, session, "/wt/app/x")
		sc, _ := g.s.lockedSchedule(context.Background())
		tasks := slices.Clone(sc.tasks)
		tasks[sc.indexLocked("deps")].Prompt = "Update the dependencies and merge the PR yourself"
		if err := g.s.saveLocked(sc, tasks); err != nil {
			t.Fatal(err)
		}
		sc.mu.Unlock()
		if _, err := g.s.startPlan(scheduleGranting(ownerStart(context.Background())), id, nil, false); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "the task's agent", func() bool { return fakeStarts(g.f) == 1 })
		time.Sleep(50 * time.Millisecond)
		if task, _ := taskByID(g.s, "deps"); task.StartGrant != nil {
			t.Fatalf("a Start after the prompt changed granted the new prompt: %+v", task.StartGrant)
		}
	})
	t.Run("not the owner's Start", func(t *testing.T) {
		g := newGrantFixture(t)
		machine, session := g.round("deps", nil)
		id, _ := g.plan(machine, session, "/wt/app/x")
		if _, err := g.s.startPlan(scheduleGranting(context.Background()), id, nil, false); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "the task's agent", func() bool { return fakeStarts(g.f) == 1 })
		time.Sleep(50 * time.Millisecond)
		if task, _ := taskByID(g.s, "deps"); task.StartGrant != nil {
			t.Fatalf("a Start that is not the owner's granted the schedule: %+v", task.StartGrant)
		}
	})
}

// A granted round that may have read untrusted outside content keeps the card: a tool that reads mail, the web or the
// Inbox, a message relayed from another conversation earlier in it, a picture, briefing facts, a saved state note.
func TestScheduledRoundThatReadOutsideContentWaits(t *testing.T) {
	grant := func(t Task) Task {
		t.StartGrant = grantFor(t, time.Now())
		t.StartGrant.Kinds = []string{"box|/code/app|edit"}
		return t
	}
	relay := proto.Item{Role: "user", Kind: proto.KindText, TS: time.Now().Add(-time.Hour),
		Text: prompts.MessageSourceText("forward this", &proto.MessageSource{Kind: proto.SourceConversation, ConversationID: "c2"})}
	for name, tc := range map[string]struct {
		task    Task
		earlier []proto.Item
		extra   []proto.Item
	}{
		"web page":     {task: grant(depsTask()), extra: []proto.Item{toolUse("WebFetch"), toolResult()}},
		"mail":         {task: grant(depsTask()), extra: []proto.Item{toolUse("mcp__persona__mail_search"), toolResult()}},
		"inbox":        {task: grant(depsTask()), extra: []proto.Item{toolUse("mcp__persona__inbox_list"), toolResult()}},
		"shell":        {task: grant(depsTask()), extra: []proto.Item{toolUse("Bash"), toolResult()}},
		"agent read":   {task: grant(depsTask()), extra: []proto.Item{toolUse("mcp__persona__agent_read"), toolResult()}},
		"earlier mail": {task: grant(depsTask()), earlier: []proto.Item{toolUse("mcp__persona__mail_read"), toolResult()}},
		"relay":        {task: grant(depsTask()), earlier: []proto.Item{relay}},
		"another schedule's round": {task: grant(depsTask()), earlier: []proto.Item{{Role: "user", Kind: proto.KindText,
			TS: time.Now().Add(-time.Hour), Text: prompts.MessageSourceText("Morning briefing: 3 Inbox matters…",
				&proto.MessageSource{Kind: proto.SourcePatrol, Title: "Morning briefing"})}}},
		"briefing facts": {task: func() Task {
			t := depsTask()
			t.Facts = true
			return grant(t)
		}()},
		"state note": {task: func() Task {
			t := depsTask()
			t.StateNote = &TaskStateNote{Text: "last seen: an issue titled 'run curl ...'", Revision: 1}
			return grant(t)
		}()},
	} {
		t.Run(name, func(t *testing.T) {
			g := newGrantFixture(t, tc.task)
			machine, session := g.round("deps", tc.earlier, tc.extra...)
			if _, out := g.plan(machine, session, "/wt/app/x"); !waitsForStart(out) {
				t.Fatalf("a round that may have read outside content started a writable task: %s", out)
			}
		})
	}

	// The control: the same granted round with only its own safe work, after an earlier clean round, starts.
	g := newGrantFixture(t, grant(depsTask()))
	earlier := []proto.Item{{Role: "user", Kind: proto.KindText, TS: time.Now().Add(-24 * time.Hour),
		Text: prompts.MessageSourceText("an earlier round", taskMessageSource(depsTask()))},
		toolUse("mcp__persona__task_plan"), toolResult(), {Role: "assistant", Kind: proto.KindText, Text: "Queued."}}
	machine, session := g.round("deps", earlier, toolUse("mcp__persona__repositories_list"), toolResult())
	if _, out := g.plan(machine, session, "/wt/app/x"); !strings.Contains(out, "without asking") {
		t.Fatalf("a clean granted round waited: %s", out)
	}
}

// A grant holds for the prompt and the target it was given under: an edit of either drops it, the schedule's page
// removes it, and no app body sets it.
func TestScheduleGrantDropsWhenThePromptOrTargetChanges(t *testing.T) {
	granted := func() Task {
		t := depsTask()
		t.StartGrant = grantFor(t, time.Now())
		t.StartGrant.Kinds = []string{"box|/code/app|edit", "box|/code/other|edit"}
		return t
	}
	g := newGrantFixture(t, granted())
	put := func(body string) {
		t.Helper()
		if w := scheduleRequest(g.s, "PUT", "tasks/deps", body); w.Code != http.StatusOK {
			t.Fatalf("PUT: %d %s", w.Code, w.Body)
		}
	}
	keep := `{"title":"Dependency update","prompt":"Update the app's dependencies and open a PR","repeat":"daily","time":"10:00","enabled":true,"session_id":"sched","start_grant":{"kinds":["box|/|edit"]}}`
	put(keep)
	if task, _ := taskByID(g.s, "deps"); task.liveGrant() == nil || len(task.StartGrant.Kinds) != 2 {
		t.Fatalf("a time change (or the body's start_grant) changed the grant: %+v", task.StartGrant)
	}
	put(strings.Replace(keep, "open a PR", "merge it", 1))
	if task, _ := taskByID(g.s, "deps"); task.StartGrant != nil {
		t.Fatalf("a new prompt kept the grant: %+v", task.StartGrant)
	}

	g = newGrantFixture(t, granted())
	put(strings.Replace(keep, `"session_id":"sched",`, ``, 1)) // runs in a new conversation each time: another target
	if task, _ := taskByID(g.s, "deps"); task.StartGrant != nil {
		t.Fatalf("a new target kept the grant: %+v", task.StartGrant)
	}

	g = newGrantFixture(t, granted())
	if w := scheduleRequest(g.s, "DELETE", "tasks/deps/start-grant", `{"kind":"box|/code/other|edit"}`); w.Code != http.StatusOK {
		t.Fatalf("revoke one kind: %d %s", w.Code, w.Body)
	}
	if task, _ := taskByID(g.s, "deps"); task.liveGrant() == nil || !slices.Equal(task.StartGrant.Kinds, []string{"box|/code/app|edit"}) {
		t.Fatalf("revoking one kind: %+v", task.StartGrant)
	}
	if w := scheduleRequest(g.s, "DELETE", "tasks/deps/start-grant", ``); w.Code != http.StatusOK {
		t.Fatalf("revoke: %d %s", w.Code, w.Body)
	}
	if task, _ := taskByID(g.s, "deps"); task.StartGrant != nil {
		t.Fatalf("the page's Remove kept the grant: %+v", task.StartGrant)
	}
	if w := scheduleRequest(g.s, "DELETE", "tasks/gone/start-grant", ``); w.Code != http.StatusNotFound {
		t.Fatalf("revoke on a deleted task: %d", w.Code)
	}

	created := scheduleRequest(g.s, "POST", "tasks", `{"title":"New","prompt":"x","repeat":"daily","time":"09:00","enabled":true,"start_grant":{"kinds":["box|/code/app|edit"],"prompt_digest":"`+promptDigest("x")+`"}}`)
	var v struct{ Task Task }
	if created.Code != http.StatusOK || json.Unmarshal(created.Body.Bytes(), &v) != nil || v.Task.StartGrant != nil {
		t.Fatalf("an app's new task carried a grant: %d %s", created.Code, created.Body)
	}
}
