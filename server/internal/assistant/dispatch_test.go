package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/proto"
)

// newDispatchService is an assistant on two computers: an agent ("caller-0001") works on "mac", "box" has none. A
// start on box lists its new session as working, as a node does. The switch is on unless on is false.
func newDispatchService(t *testing.T, on bool) (*Service, *fakeHub, *memStore) {
	t.Helper()
	f := newFakeHub()
	f.sessions["mac"] = []proto.Session{{SessionID: "caller-0001", Title: "fix the build", Status: proto.StatusWorking, Cwd: "/code/app"}}
	f.sessions["box"] = nil
	f.settings = &Settings{Timezone: "UTC"}
	if on {
		f.hello.Agents.Dispatch.Enabled = &on
	}
	f.onStart = func(f *fakeHub, _ map[string]any) {
		f.sessions["box"] = append(f.sessions["box"], proto.Session{SessionID: fmt.Sprintf("s%d", f.agents+1), Status: proto.StatusWorking})
	}
	st := &memStore{}
	return newServiceOn(t, f, st), f, st
}

func dispatchCall(s *Service, machine, session, name, args string) proto.ToolResult {
	return s.mcpCall(context.Background(), machine, session, name, json.RawMessage(args), false)
}

func setStatus(f *fakeHub, machine, id, status string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.sessions[machine] {
		if f.sessions[machine][i].SessionID == id {
			f.sessions[machine][i].Status = status
		}
	}
}

func toolListed(tools []proto.Tool, name string) bool {
	for _, t := range tools {
		if t.Name == name {
			return true
		}
	}
	return false
}

// Off by default: an agent is not offered the tools, and a call of one starts nothing.
func TestDispatchIsOffByDefault(t *testing.T) {
	s, f, _ := newDispatchService(t, false)
	if tools := s.mcpTools(context.Background(), false); toolListed(tools, "dispatch_start") || toolListed(tools, "dispatch_read") {
		t.Fatal("the dispatch tools are listed with the switch off")
	}
	r := dispatchCall(s, "mac", "caller-0001", "dispatch_start", `{"machine":"box","cwd":"/code/app","prompt":"run the tests"}`)
	if !r.IsError || !strings.Contains(resultText(r), "agents.dispatch.enabled") || len(f.starts) != 0 {
		t.Fatalf("result %q, starts %v", resultText(r), f.starts)
	}
}

// On: agents get the tools; the assistant's conversations, which have agent_start, do not.
func TestDispatchToolsAreTheAgentsWhenOn(t *testing.T) {
	s, _, _ := newDispatchService(t, true)
	if tools := s.mcpTools(context.Background(), false); !toolListed(tools, "dispatch_start") || !toolListed(tools, "dispatch_read") {
		t.Fatal("the dispatch tools are not listed for an agent")
	}
	if toolListed(s.mcpTools(context.Background(), true), "dispatch_start") {
		t.Fatal("dispatch_start is listed for an assistant conversation")
	}
}

// The whole way: an agent on mac hands a subtask to box, the new agent is told who handed it the task, and the
// caller reads its state and, once it stopped, its final reply.
func TestDispatchStartsOnAnotherComputerAndReadsTheResult(t *testing.T) {
	s, f, _ := newDispatchService(t, true)
	r := dispatchCall(s, "mac", "caller-0001", "dispatch_start", `{"machine":"box","cwd":"/code/app","prompt":"run the Linux tests and report failures"}`)
	out := resultText(r)
	if r.IsError || !strings.Contains(out, "session s1") {
		t.Fatalf("start: %q", out)
	}
	if len(f.starts) != 1 || f.starts[0]["cwd"] != "/code/app" {
		t.Fatalf("starts %v", f.starts)
	}
	prompt, _ := f.starts[0]["prompt"].(string)
	if !strings.Contains(prompt, "handed to you by another coding agent") || !strings.Contains(prompt, "fix the build") ||
		!strings.HasSuffix(prompt, "run the Linux tests and report failures") {
		t.Fatalf("the new agent's first message:\n%s", prompt)
	}
	var started string
	for _, c := range f.calls {
		if c.Method == DeviceSessionStart {
			started = c.Device
		}
	}
	if started != "box" {
		t.Fatalf("the agent started on %q, want box", started)
	}

	// Still working: the read says so and does not pass its text off as the result.
	if out := resultText(dispatchCall(s, "mac", "caller-0001", "dispatch_read", `{"session_id":"s1"}`)); !strings.Contains(out, ": working.") || !strings.Contains(out, "not its result yet") {
		t.Fatalf("read while working: %q", out)
	}
	// It stops: its last reply comes back as the result.
	setStatus(f, "box", "s1", proto.StatusDone)
	f.setItems(proto.Item{ID: "i1", Role: "assistant", Kind: proto.KindText, Text: "2 tests fail on Linux: TestA, TestB"})
	out = resultText(dispatchCall(s, "mac", "caller-0001", "dispatch_read", `{"session_id":"s1"}`))
	if !strings.Contains(out, ": stopped.") || !strings.Contains(out, "2 tests fail on Linux: TestA, TestB") {
		t.Fatalf("read after it stopped: %q", out)
	}
	// With no arguments: the other computer (not the caller's own) and the task.
	out = resultText(dispatchCall(s, "mac", "caller-0001", "dispatch_read", `{}`))
	if !strings.Contains(out, "box (id box): online") || strings.Contains(out, "mac (id mac)") || !strings.Contains(out, "- s1 on box (/code/app): stopped") {
		t.Fatalf("overview: %q", out)
	}
}

// wait_seconds holds the read until the task's turn is over.
func TestDispatchReadWaitsForTheTaskToStop(t *testing.T) {
	old := dispatchPoll
	dispatchPoll = 5 * time.Millisecond
	t.Cleanup(func() { dispatchPoll = old })
	s, f, _ := newDispatchService(t, true)
	if r := dispatchCall(s, "mac", "caller-0001", "dispatch_start", `{"machine":"box","cwd":"/code/app","prompt":"build it"}`); r.IsError {
		t.Fatal(resultText(r))
	}
	f.setItems(proto.Item{ID: "i1", Role: "assistant", Kind: proto.KindText, Text: "built"})
	go func() {
		time.Sleep(40 * time.Millisecond)
		setStatus(f, "box", "s1", proto.StatusDone)
	}()
	out := resultText(dispatchCall(s, "mac", "caller-0001", "dispatch_read", `{"session_id":"s1","wait_seconds":5}`))
	if !strings.Contains(out, ": stopped.") || !strings.Contains(out, "built") {
		t.Fatalf("read: %q", out)
	}
}

// A task is its caller's only: another agent cannot read it, by any id.
func TestDispatchReadIsOnlyForTheAgentThatDispatched(t *testing.T) {
	s, f, _ := newDispatchService(t, true)
	f.sessions["mac"] = append(f.sessions["mac"], proto.Session{SessionID: "other-0002", Status: proto.StatusWorking})
	if r := dispatchCall(s, "mac", "caller-0001", "dispatch_start", `{"machine":"box","cwd":"/code/app","prompt":"build it"}`); r.IsError {
		t.Fatal(resultText(r))
	}
	r := dispatchCall(s, "mac", "other-0002", "dispatch_read", `{"session_id":"s1"}`)
	if !r.IsError || !strings.Contains(resultText(r), "no task s1 among those you dispatched") {
		t.Fatalf("another agent read it: %q", resultText(r))
	}
	if out := resultText(dispatchCall(s, "mac", "other-0002", "dispatch_read", `{}`)); strings.Contains(out, "s1") {
		t.Fatalf("another agent's overview lists it: %q", out)
	}
}

// One level deep: the dispatched agent cannot dispatch, back to the caller's computer or anywhere else.
func TestDispatchedTaskDoesNotDispatch(t *testing.T) {
	s, f, _ := newDispatchService(t, true)
	if r := dispatchCall(s, "mac", "caller-0001", "dispatch_start", `{"machine":"box","cwd":"/code/app","prompt":"build it"}`); r.IsError {
		t.Fatal(resultText(r))
	}
	r := dispatchCall(s, "box", "s1", "dispatch_start", `{"machine":"mac","cwd":"/code/app","prompt":"and back"}`)
	if !r.IsError || !strings.Contains(resultText(r), "a dispatched task does not dispatch") || len(f.starts) != 1 {
		t.Fatalf("result %q, starts %d", resultText(r), len(f.starts))
	}
}

// At most max_active dispatched tasks of one agent work at a time; one stopping makes room.
func TestDispatchLimitsWorkingTasks(t *testing.T) {
	s, f, _ := newDispatchService(t, true)
	f.hello.Agents.Dispatch.MaxActive = 2
	start := func() proto.ToolResult {
		return dispatchCall(s, "mac", "caller-0001", "dispatch_start", `{"machine":"box","cwd":"/code/app","prompt":"build it"}`)
	}
	for range 2 {
		if r := start(); r.IsError {
			t.Fatal(resultText(r))
		}
	}
	if r := start(); !r.IsError || !strings.Contains(resultText(r), "the limit is 2") || len(f.starts) != 2 {
		t.Fatalf("third start: %q, starts %d", resultText(r), len(f.starts))
	}
	setStatus(f, "box", "s1", proto.StatusDone)
	if r := start(); r.IsError {
		t.Fatalf("a start after one stopped: %q", resultText(r))
	}
}

// What is refused starts nothing: the caller's own computer, an unknown one, a work computer, one that is offline,
// a caller the node did not name, and a work computer's agent.
func TestDispatchRefusals(t *testing.T) {
	cases := []struct {
		name, machine, session, args, want string
		setup                              func(f *fakeHub)
	}{
		{name: "own computer", machine: "mac", session: "caller-0001", args: `{"machine":"mac","cwd":"/c","prompt":"p"}`, want: "the computer you run on"},
		{name: "unknown computer", machine: "mac", session: "caller-0001", args: `{"machine":"nas","cwd":"/c","prompt":"p"}`, want: `unknown computer "nas"`},
		{name: "missing prompt", machine: "mac", session: "caller-0001", args: `{"machine":"box","cwd":"/c"}`, want: "are required"},
		{name: "no caller", machine: "mac", session: "", args: `{"machine":"box","cwd":"/c","prompt":"p"}`, want: "cannot tell which agent is calling"},
		{name: "work computer as the target", machine: "mac", session: "caller-0001", args: `{"machine":"box","cwd":"/c","prompt":"p"}`, want: "work computer",
			setup: func(f *fakeHub) { f.hello.Assistant.WorkMachines = []string{"box"} }},
		{name: "work computer's agent", machine: "mac", session: "caller-0001", args: `{"machine":"box","cwd":"/c","prompt":"p"}`, want: "",
			setup: func(f *fakeHub) { f.hello.Assistant.WorkMachines = []string{"mac"} }},
		{name: "offline", machine: "mac", session: "caller-0001", args: `{"machine":"box","cwd":"/c","prompt":"p"}`, want: "box is offline",
			setup: func(f *fakeHub) { f.offline = map[string]bool{"box": true} }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, f, _ := newDispatchService(t, true)
			if c.setup != nil {
				c.setup(f)
			}
			r := dispatchCall(s, c.machine, c.session, "dispatch_start", c.args)
			if !r.IsError || !strings.Contains(resultText(r), c.want) {
				t.Fatalf("result (error=%v) %q, want an error with %q", r.IsError, resultText(r), c.want)
			}
			if len(f.agentSent) != 0 || f.agents != 0 {
				t.Fatalf("an agent was started: %v", f.starts)
			}
		})
	}
}

// A start the computer never answers is not reported as failed (the agent may be running) nor recorded.
func TestDispatchStartWithoutAnAnswerIsNotRetried(t *testing.T) {
	s, f, _ := newDispatchService(t, true)
	f.failStart = fmt.Errorf("box: %w", context.DeadlineExceeded)
	r := dispatchCall(s, "mac", "caller-0001", "dispatch_start", `{"machine":"box","cwd":"/code/app","prompt":"build it"}`)
	if !r.IsError || !strings.Contains(resultText(r), "may or may not have started") {
		t.Fatalf("result %q", resultText(r))
	}
	f.failStart = fmt.Errorf("box: %w", apierr.ErrOffline)
	if r := dispatchCall(s, "mac", "caller-0001", "dispatch_start", `{"machine":"box","cwd":"/code/app","prompt":"build it"}`); !strings.Contains(resultText(r), "box is offline") {
		t.Fatalf("result %q", resultText(r))
	}
}

// With worktree the target's engine makes the checkout first and the agent starts in it.
func TestDispatchStartInANewWorktree(t *testing.T) {
	s, f, _ := newDispatchService(t, true)
	r := dispatchCall(s, "mac", "caller-0001", "dispatch_start", `{"machine":"box","cwd":"/code/app","prompt":"build it","worktree":{"branch":"fix/linux"}}`)
	if r.IsError || !strings.Contains(resultText(r), "new worktree on branch fix/linux") {
		t.Fatalf("result %q", resultText(r))
	}
	if len(f.starts) != 1 || f.starts[0]["cwd"] != "/wt/app/fix-linux" || f.starts[0]["workspace_id"] != "ws-new" {
		t.Fatalf("starts %v", f.starts)
	}
}

// The tasks are saved: after a manager restart the caller still reads its task, and the task still cannot dispatch.
func TestDispatchedTasksSurviveARestart(t *testing.T) {
	s, f, st := newDispatchService(t, true)
	if r := dispatchCall(s, "mac", "caller-0001", "dispatch_start", `{"machine":"box","cwd":"/code/app","prompt":"build it"}`); r.IsError {
		t.Fatal(resultText(r))
	}
	setStatus(f, "box", "s1", proto.StatusDone)
	f.setItems(proto.Item{ID: "i1", Role: "assistant", Kind: proto.KindText, Text: "built"})
	again := newServiceOn(t, f, st)
	if out := resultText(dispatchCall(again, "mac", "caller-0001", "dispatch_read", `{"session_id":"s1"}`)); !strings.Contains(out, "built") {
		t.Fatalf("read after a restart: %q", out)
	}
	if r := dispatchCall(again, "box", "s1", "dispatch_start", `{"machine":"mac","cwd":"/c","prompt":"p"}`); !strings.Contains(resultText(r), "does not dispatch") {
		t.Fatalf("the task dispatched after a restart: %q", resultText(r))
	}
}
