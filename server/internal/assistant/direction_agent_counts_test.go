package assistant

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// Exercise the response all three sidebars read, including the omission on the secretary group.
func checkDirectionAgentCounts(t *testing.T, s *Service, wantA, wantB [2]int) {
	t.Helper()
	_, raw := directionFilingTreeGet(t, s)
	t.Logf("GET /api/v1/assistant/directions/tree %s", raw)
	rows := directionFilingTreeMaps(t, raw)
	if len(rows) != 3 {
		t.Fatalf("want two directions and the secretary: %s", raw)
	}
	for _, row := range rows {
		if string(row["secretary"]) == "true" {
			if _, ok := row["agent_counts"]; ok {
				t.Fatalf("secretary has agent_counts: %s", raw)
			}
			continue
		}
		var counts map[string]int
		if err := json.Unmarshal(row["agent_counts"], &counts); err != nil {
			t.Fatalf("%s has no agent_counts: %v; response %s", row["id"], err, raw)
		}
		want := wantA
		if string(row["id"]) == `"conv_lead_b"` {
			want = wantB
		}
		running, r := counts["running"]
		stopped, d := counts["stopped"]
		if !r || !d || len(counts) != 2 || [2]int{running, stopped} != want {
			t.Fatalf("%s: agent_counts=%v, want running=%d stopped=%d; response %s", row["id"], counts, want[0], want[1], raw)
		}
	}
}

func TestDirectionAgentCountsAcrossTasksWorkersAndFiledSessions(t *testing.T) {
	e := directionFilingEnv(t, nil)
	const a, b = "conv_lead_a", "conv_lead_b"
	checkDirectionAgentCounts(t, e.s, [2]int{0, 1}, [2]int{0, 1})
	directionFilingMustPut(t, e.s, "server", "chat-b", a) // quiet: absent from tree.sessions
	e.s.originSet(context.Background(), "server", "opened", OriginSpawned, a, "")
	directionFilingSetSessions(e, proto.Session{SessionID: "opened", Status: proto.StatusWorking})
	now := time.Now().UTC()
	task := func(title, sup string, ended bool, links ...TodoAgent) {
		e.task(title, func(todo *Todo) {
			todo.Supervision, todo.Agents = &Supervision{Supervisor: sup}, links
			if ended {
				todo.DoneAt = &now
			}
		})
	}
	lead := TodoAgent{MachineID: "mac", SessionID: "lead", Source: sourceLeader}
	task("lead task", a, false, lead, TodoAgent{MachineID: "server", SessionID: a})
	task("ended worker", leaderRef("mac", "lead"), true, TodoAgent{MachineID: "mac", SessionID: "worker", ReleasedAt: &now})
	task("same lead again", a, true, lead)
	task("same id on another computer", a, true, TodoAgent{MachineID: "other", SessionID: "worker"})
	task("other direction", b, true, TodoAgent{MachineID: "other", SessionID: "b"})
	e.f.setSessions("mac",
		proto.Session{MachineID: "mac", SessionID: "lead", Status: proto.StatusWorking, Subagents: []proto.Subagent{
			{ID: "one", SessionID: "child", Running: true}, {ID: "two", ParentID: "one", Running: false},
			{ID: "stale", SessionID: "listed-stopped", Running: true}}},
		proto.Session{MachineID: "mac", SessionID: "listed-stopped", Status: proto.StatusDone},
		proto.Session{MachineID: "mac", SessionID: "child", ParentSessionID: "lead", Status: proto.StatusWorking},
		proto.Session{MachineID: "mac", SessionID: "grandchild", ParentSessionID: "child", Status: proto.StatusIdle},
		proto.Session{MachineID: "mac", SessionID: "worker", Status: proto.StatusDone})
	e.f.setSessions("other", proto.Session{MachineID: "other", SessionID: "worker", Status: proto.StatusWorking})
	// A runs opened, lead, child, other/worker; A, chat-b, lead@two, grandchild, mac/worker and listed-stopped have ended.
	// B's own conversation and its missing historical Agent are both stopped.
	checkDirectionAgentCounts(t, e.s, [2]int{4, 6}, [2]int{0, 2})
}

func TestDirectionAgentCountsAttentionIsIndependentOfExecution(t *testing.T) {
	e := directionFilingEnv(t, nil)
	for _, tt := range []struct {
		name, status, state string
		running             bool
	}{
		{"host working", proto.StatusWorking, proto.StateWorking, true},
		{"question during a turn", proto.StatusWorking, proto.StateNeedsYou, true},
		{"host waiting for permission", proto.StatusBlocked, proto.StateNeedsYou, true},
		{"question on a stopped turn", proto.StatusIdle, proto.StateNeedsYou, false},
		{"unread while still working", proto.StatusWorking, proto.StateUnread, true},
		{"unread completed turn", proto.StatusDone, proto.StateUnread, false},
		{"idle", proto.StatusIdle, proto.StateIdle, false},
		{"explicitly stopped", proto.StatusOffline, proto.StateOffline, false},
		{"interrupted", proto.StatusInterrupted, proto.StateNeedsYou, false},
		{"offline computer still reporting last turn", proto.StatusWorking, proto.StateOffline, true},
		{"offline with no running evidence", proto.StatusUnknown, proto.StateOffline, false},
		{"old working row", "", proto.StateWorking, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e.f.setSessions("server", proto.Session{MachineID: "server", SessionID: "conv_lead_a", Status: tt.status, State: tt.state})
			want := [2]int{0, 1}
			if tt.running {
				want = [2]int{1, 0}
			}
			checkDirectionAgentCounts(t, e.s, want, [2]int{0, 1})
		})
	}
}

func TestDirectionAgentCountsOnlyCurrentContinuations(t *testing.T) {
	e := directionFilingEnv(t, nil)
	ctx := context.Background()
	const dir = "conv_lead_a"
	now := time.Now().UTC()
	old := TodoAgent{MachineID: "old-box", SessionID: "old", LinkedAt: now.Add(-time.Hour)}
	modern := e.task("same to-do, new run", func(task *Todo) {
		task.Supervision = &Supervision{Supervisor: dir}
		task.Agents = []TodoAgent{old}
	})
	e.f.setSessions("old-box", proto.Session{MachineID: "old-box", SessionID: "old", Status: proto.StatusOffline})
	checkDirectionAgentCounts(t, e.s, [2]int{0, 2}, [2]int{0, 1})
	// Use the real takeover release and link, which leave both Agents in the to-do's history.
	if err := e.s.releaseStoppedAgents(ctx, modern, []TodoAgent{old}); err != nil {
		t.Fatal(err)
	}
	e.s.originSet(ctx, "old-box", old.SessionID, OriginSpawned, dir, "")
	if err := e.s.writeTodoAgentLink(ctx, modern, "new-box", "new", "new", "/repo", "codex", "", sourceLeader, "", false); err != nil {
		t.Fatal(err)
	}
	e.f.setSessions("new-box", proto.Session{MachineID: "new-box", SessionID: "new", Status: proto.StatusWorking})
	checkDirectionAgentCounts(t, e.s, [2]int{1, 1}, [2]int{0, 1})
	if got := todoNow(e.s, modern); len(got.Agents) != 2 {
		t.Fatalf("lost takeover history: %+v", got.Agents)
	}

	legacy := handedOver(now)
	e.task("legacy unreleased takeover", func(task *Todo) {
		task.Supervision = &Supervision{Supervisor: dir}
		task.DoneAt = &now
		task.Agents = legacy.Agents
	})
	other := proto.Session{MachineID: "box", SessionID: "separate", Status: proto.StatusDone}
	current := proto.Session{MachineID: "box", SessionID: "new", Status: proto.StatusWorking}
	current.SetEngineHistory([]proto.EngineLeg{{SessionID: "engine-old", Agent: "claude", Until: now}})
	current.SetAliases([]string{"provider-alias"})
	e.f.setSessions("box", proto.Session{MachineID: "box", SessionID: "old", Status: proto.StatusOffline},
		proto.Session{MachineID: "box", SessionID: "engine-old", Status: proto.StatusDone}, current, other,
		proto.Session{MachineID: "box", SessionID: "distinct-child", ParentSessionID: "engine-old", Status: proto.StatusIdle})
	e.task("separate ended task and multiple paths to the current engine", func(task *Todo) {
		task.Supervision = &Supervision{Supervisor: dir}
		task.DoneAt = &now
		task.Agents = []TodoAgent{{MachineID: "box", SessionID: "provider-alias"}, {MachineID: "box", SessionID: "engine-old"},
			{MachineID: "box", SessionID: "separate", ReleasedAt: &now}}
	})
	// Same "new" ID on two computers counts twice, earlier task/engine IDs do not. The unrelated closed Agent
	// and distinct child under the old engine still count alongside the direction itself.
	checkDirectionAgentCounts(t, e.s, [2]int{2, 3}, [2]int{0, 1})
}

func TestDirectionAgentCountsSeparateEndedTasksOnSameBrief(t *testing.T) {
	e := directionFilingEnv(t, nil)
	ctx := context.Background()
	const dir = "conv_lead_a"
	now := time.Now().UTC()
	add := func(sid string) string {
		return e.task(sid, func(todo *Todo) {
			todo.Supervision = &Supervision{Supervisor: dir}
			todo.Agents = []TodoAgent{{MachineID: "box", SessionID: sid}}
			todo.DoneAt = &now
		})
	}
	old, next, independent := add("old-brief-run"), add("new-brief-run"), add("other-participant")
	// Some older handovers created distinct to-dos. Only the recorded continues edge identifies the predecessor.
	brief, err := e.s.ensureTaskBrief(ctx, todoNow(e.s, old))
	if err != nil {
		t.Fatal(err)
	}
	for _, link := range [][2]string{{next, old}, {independent, ""}} {
		if err := e.s.linkTaskBrief(ctx, brief.ID, link[0], link[1], ""); err != nil {
			t.Fatal(err)
		}
	}
	e.f.setSessions("box", proto.Session{MachineID: "box", SessionID: "new-brief-run", Status: proto.StatusWorking})
	before := e.f.sessionsListCalls("box")
	checkDirectionAgentCounts(t, e.s, [2]int{1, 2}, [2]int{0, 1})
	if n := e.f.sessionsListCalls("box") - before; n != 1 {
		t.Errorf("historical machines listed %d times, want once", n)
	}
}

func TestDirectionAgentCountsRepublishForQuietAndHistoricalMachines(t *testing.T) {
	e := directionFilingEnv(t, nil)
	now := time.Now().UTC()
	e.task("historical only", func(todo *Todo) {
		todo.Supervision = &Supervision{Supervisor: "conv_lead_a"}
		todo.DoneAt = &now
		todo.Agents = []TodoAgent{{MachineID: "archive", SessionID: "past", ReleasedAt: &now}}
	})
	ctx := context.Background()
	e.clock.advance(time.Minute)
	e.s.directionTreeSync(ctx)
	for _, step := range []struct {
		machine, sid string
		want         [2]int
	}{
		{"server", "conv_lead_a", [2]int{1, 1}},
		{"archive", "past", [2]int{2, 0}},
	} {
		prev := e.directionsEvents()
		row := proto.Session{MachineID: step.machine, SessionID: step.sid, Status: proto.StatusWorking}
		e.f.setSessions(step.machine, row)
		e.s.directionTreeSessions(DeviceSessions{MachineID: step.machine, Sessions: []proto.Session{row}})
		if !directionTreeArmed(e.s) {
			t.Fatalf("no subscription for %s", step.machine)
		}
		e.clock.advance(time.Minute)
		if got := e.directionsEvents(); got != prev+1 {
			t.Fatalf("changed %s: %d directions events after %d", step.machine, got, prev)
		}
		checkDirectionAgentCounts(t, e.s, step.want, [2]int{0, 1})
	}
}

func TestDirectionAgentCountsCanonicalMachinesAndRecordedLeader(t *testing.T) {
	ft := directionFilingFleetEnv(t, newFleetMem())
	ctx := context.Background()
	if err := ft.s.ensureTodosLoaded(ctx); err != nil {
		t.Fatal(err)
	}
	row := func(sid, status string) proto.Session {
		return proto.Session{SessionID: sid, Cwd: "/home/u/.persona/assistant", Status: status}
	}
	ft.sessions("mac", row("conv_lead_a", proto.StatusIdle), row("conv_lead_b", proto.StatusIdle), row("same", proto.StatusWorking))
	directionFleetComputer(t, ft, "other", row("conv_lead_b", proto.StatusWorking), row("same", proto.StatusIdle))
	ft.s.directions.mu.Lock()
	ft.s.directions.m["conv_lead_b"].MachineID = "other"
	ft.s.directions.mu.Unlock()
	task, err := ft.s.createTodo(ctx, "Synthetic route bindings", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	ft.s.todos.mu.Lock()
	v := ft.s.todos.todos[task.ID]
	v.Supervision = &Supervision{Supervisor: "conv_lead_a"}
	v.Agents = []TodoAgent{{MachineID: "old-mac", SessionID: "same", Source: sourceLeader}, {MachineID: "mac", SessionID: "same"}, {MachineID: "other", SessionID: "same"}}
	ft.s.todos.mu.Unlock()
	worker, err := ft.s.createTodo(ctx, "Worker of the recorded computer alias", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	ft.s.todos.mu.Lock()
	w := ft.s.todos.todos[worker.ID]
	w.Supervision, w.DoneAt = &Supervision{Supervisor: leaderRef("mac", "same")}, &now
	w.Agents = []TodoAgent{{MachineID: "other", SessionID: "separate-ended-worker"}}
	ft.s.todos.mu.Unlock()
	checkDirectionAgentCounts(t, ft.s, [2]int{1, 3}, [2]int{1, 0})
}
