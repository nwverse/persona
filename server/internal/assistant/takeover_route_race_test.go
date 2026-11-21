package assistant

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// The owner's message during a continues takeover starts no second agent (SV-CE1). A plan continuing a to-do stops its
// old agent and then lets go of it (releaseStoppedAgents). In between, the old agent is stopped but still linked, so
// routeToTask finds every agent stopped and would start a successor. The takeover holds the to-do's one start
// (claimTaskStart, takeTodoForTask), so the message waits for that start and goes to the agent it brings up. Before
// both paths shared that claim, the takeover held only tasks.reusing and the message started an agent of its own: two
// active agents on one to-do.
func TestOwnerMessageDuringContinuesTakeoverStartsNoSecondAgent(t *testing.T) {
	e := newThreadEnv(t, nil) // agent01 is listed as running
	putTodo(e.s, Todo{ID: "todo_x", Title: "发版", Origin: "m1", Supervision: &Supervision{Supervisor: "m1"},
		Agents: []TodoAgent{{MachineID: "box", SessionID: "agent01", Cwd: "/code/app", Agent: "claude"}}})
	// Hold the to-do store's writer: the takeover stops agent01 and then waits to let go of it (releaseStoppedAgents).
	e.s.todos.writing.Lock()
	held := true
	unlock := func() {
		if held {
			held = false
			e.s.todos.writing.Unlock()
		}
	}
	defer unlock()
	planned := make(chan error, 1)
	go func() {
		_, err := e.s.toolTaskPlan(context.Background(), "m1", continuesArgs("todo_x"))
		planned <- err
	}()
	waitFor(t, "the takeover stopping agent01", func() bool {
		e.f.mu.Lock()
		defer e.f.mu.Unlock()
		return len(e.f.stopped) == 1
	})
	// The node lists it stopped now (its list only: the sessions.changed handler waits for the same writer); it is
	// still linked to todo_x.
	old := proto.Session{MachineID: "box", SessionID: "agent01", Title: "修发版", Cwd: "/code/app", Agent: "claude", Status: proto.StatusOffline}
	e.f.setSessions("box", old)
	e.f.mu.Lock()
	e.f.onStart = func(f *fakeHub, _ map[string]any) { // the node lists each agent it starts, working
		f.sessions["box"] = append(f.sessions["box"], proto.Session{MachineID: "box", SessionID: fmt.Sprintf("s%d", f.agents+1),
			Cwd: "/code/app", Agent: "claude", Status: proto.StatusWorking, State: proto.StateWorking})
	}
	e.f.mu.Unlock()
	routed := make(chan error, 1)
	go func() {
		_, err := e.s.routeToTask(context.Background(), "m1", "todo_x", "顺便把 changelog 也更新一下")
		routed <- err
	}()
	// Give the message time to reach the agent start it would make on its own.
	time.Sleep(300 * time.Millisecond)
	e.f.mu.Lock()
	early := len(e.f.starts)
	e.f.mu.Unlock()
	unlock()
	if err := <-planned; err != nil {
		t.Fatalf("continues: %v", err)
	}
	newAgentOn(t, e, "todo_x")
	select {
	case err := <-routed:
		if err != nil {
			t.Fatalf("the owner's message: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the owner's message never ended")
	}
	e.f.mu.Lock()
	starts := len(e.f.starts)
	e.f.mu.Unlock()
	var active []string
	for _, a := range todoNow(e.s, "todo_x").Agents {
		if a.active() {
			active = append(active, a.SessionID)
		}
	}
	if early != 0 || starts != 1 || len(active) != 1 {
		t.Fatalf("one agent should take todo_x over: %d start(s) while the takeover held it, %d in all; active agents %v", early, starts, active)
	}
	if sent := sentToAgent(e.f); len(sent) != 1 || sent[0] != "顺便把 changelog 也更新一下" {
		t.Fatalf("the owner's message should reach the agent the takeover started: %q", sent)
	}
}
