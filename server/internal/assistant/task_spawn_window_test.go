package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// spawnWindowHub plays a node that runs the new session, and sends its first prompt, before session.start answers
// (#11999). While the start is in flight it reports the session list (with the child, and another Agent that just
// opened in the same workspace) through the manager's own sessions.changed handler, and, as the RPCs run in parallel,
// lets each of them list its tools and call task_spawn through the node's mcp.tools / mcp.call requests. Then the
// start answers, or fails with an unknown result.
type spawnWindowHub struct {
	Hub
	s             *Service
	f             *fakeHub
	armed         bool
	fail          error
	others        []proto.Session
	childLauncher string
	child         string
	// duringStart lets a regression change the caller after its start reached the node but before session.start answers.
	duringStart func(proto.Session)
	window      map[string]spawnWindowSeen
	devs        *fleetDevs
}

// spawnWindowSeen is what a session got from the manager: whether task_spawn was listed, and the call's result.
type spawnWindowSeen struct {
	listed bool
	failed bool
	text   string
	broken string // the request did not reach the tool: a fixture fault, never a refusal
}

func (h *spawnWindowHub) DeviceCall(ctx context.Context, p DeviceCall) (json.RawMessage, error) {
	raw, err := h.Hub.DeviceCall(ctx, p)
	if err != nil || p.Method != DeviceSessionStart || !h.armed {
		return raw, err
	}
	h.armed = false
	var res struct {
		Session proto.Session `json:"session"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	child := res.Session
	child.MachineID, child.SpaceName, child.Cwd = p.Device, "app", "/code/app"
	launcher := h.childLauncher
	if launcher == "" {
		launcher = "codex"
	}
	child.Launcher, child.Agent, child.PermissionMode, child.Status = launcher, launcher, "plan", proto.StatusWorking
	h.child = child.SessionID
	h.f.mu.Lock()
	h.f.sessions[p.Device] = append(h.f.sessions[p.Device], append([]proto.Session{child}, h.others...)...)
	list := slices.Clone(h.f.sessions[p.Device])
	h.f.mu.Unlock()
	if h.duringStart != nil {
		fn := h.duringStart
		h.duringStart = nil
		fn(child)
		if h.fail != nil {
			return nil, h.fail
		}
		return raw, nil
	}
	done := make(chan struct{})
	go func() { // another RPC, handled while this start has not answered
		defer close(done)
		h.syncDevs()
		h.s.SessionsChanged(context.Background(), DeviceSessions{MachineID: p.Device, Sessions: list})
		h.window = map[string]spawnWindowSeen{}
		for _, id := range append([]string{child.SessionID}, sessionIDs(h.others)...) {
			h.window[id] = spawnWindowNode(h, id, "Only inspect the second parser assertion; edit nothing and return its line.")
		}
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		return nil, fmt.Errorf("test fixture: the child's requests did not finish")
	}
	if h.fail != nil {
		return nil, h.fail
	}
	return raw, nil
}

func sessionIDs(list []proto.Session) []string {
	var ids []string
	for _, s := range list {
		ids = append(ids, s.SessionID)
	}
	return ids
}

// syncDevs mirrors the fake hub's rows into the devices, which the node requests' fleet gate reads. The devices are
// set once by the fixture and only their rows change here: background work started by sessions.changed reads them.
func (h *spawnWindowHub) syncDevs() {
	h.f.mu.Lock()
	list := slices.Clone(h.f.sessions["box"])
	h.f.mu.Unlock()
	h.devs.setSessions("box", list)
}

// spawnWindowNode asks as the node does for session: its tools (mcp.tools), then task_spawn (mcp.call).
func spawnWindowNode(h *spawnWindowHub, session, prompt string) spawnWindowSeen {
	ctx := context.Background()
	s := h.s
	h.syncDevs()
	var seen spawnWindowSeen
	req, _ := json.Marshal(map[string]any{"session_id": session})
	out, _, err := s.NodeRequest(ctx, "box", "mcp.tools", req)
	m, ok := out.(map[string]any)
	tools, listed := m["tools"].([]proto.Tool)
	if !ok || err != nil || !listed {
		seen.broken = fmt.Sprintf("mcp.tools: err=%v out=%v", err, out)
		return seen
	}
	seen.listed = toolListed(tools, "task_spawn")
	req, _ = json.Marshal(map[string]any{"session_id": session, "name": "task_spawn", "args": spawnPromptArgs(prompt)})
	out, handled, err := s.NodeRequest(ctx, "box", "mcp.call", req)
	r, ok := out.(proto.ToolResult)
	if err != nil || !handled || !ok || len(r.Content) == 0 {
		seen.broken = fmt.Sprintf("handled=%v err=%v out=%v", handled, err, out)
		return seen
	}
	seen.failed, seen.text = r.IsError, r.Content[0].Text
	return seen
}

// reached fails the test when a request never reached the tool, so a fixture fault cannot pass for a refusal.
func reached(t *testing.T, who string, seen spawnWindowSeen) spawnWindowSeen {
	t.Helper()
	if seen.broken != "" {
		t.Fatalf("%s: %s", who, seen.broken)
	}
	return seen
}

// spawnWindowWait makes a synchronization mistake a bounded test failure, never a hung package.
func spawnWindowWait[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(10 * time.Second):
		t.Fatalf("timeout waiting for %s", what)
		var zero T
		return zero
	}
}

// A newly opened workspace Agent beside the child: linked by the same workspace while the start is in flight.
var spawnWindowSibling = proto.Session{MachineID: "box", SessionID: "sibling", SpaceName: "app", Cwd: "/code/app",
	Launcher: "codex", Agent: "codex", PermissionMode: "plan", Status: proto.StatusWorking}

// spawnWindowQuiesce gives asynchronous starts, naming and todo pushes a happens-before boundary before a test
// replaces the service hub or store. Without it, the fixture itself can race production background reads.
func spawnWindowQuiesce(t *testing.T, s *Service) {
	t.Helper()
	waitFor(t, "hub, naming and todo pushes to settle", func() bool {
		s.tpush.mu.Lock()
		defer s.tpush.mu.Unlock()
		return s.tpush.sending == 0 && s.taskStarting.Load() == 0
	})
	s.agentNames.runs.Wait()
}

func spawnWindowFixture(t *testing.T, fail error) (*Service, *fakeHub, Todo, *spawnWindowHub) {
	t.Helper()
	return spawnWindowFixtureStore(t, fail, nil)
}

func spawnWindowFixtureStore(t *testing.T, fail error, store Store) (*Service, *fakeHub, Todo, *spawnWindowHub) {
	t.Helper()
	s, f, todo := spawnFixtureOn(t, "codex", "plan", store)
	if _, err := s.linkTodoSpace(t.Context(), todo.ID, "box", "app"); err != nil {
		t.Fatal(err)
	}
	h := &spawnWindowHub{Hub: s.hub, s: s, f: f, armed: true, fail: fail, others: []proto.Session{spawnWindowSibling},
		devs: newFleetDevs()}
	h.devs.add("box", "Box")
	h.syncDevs()
	spawnWindowQuiesce(t, s)
	s.hub, s.devs = h, h.devs
	return s, f, todo, h
}

func spawnWindowFixtureLauncher(t *testing.T, fail error, launcher string) (*Service, *fakeHub, Todo, *spawnWindowHub) {
	t.Helper()
	s, f, todo := spawnFixtureOn(t, launcher, "plan", nil)
	if _, err := s.linkTodoSpace(t.Context(), todo.ID, "box", "app"); err != nil {
		t.Fatal(err)
	}
	h := &spawnWindowHub{Hub: s.hub, s: s, f: f, armed: true, fail: fail, childLauncher: launcher, devs: newFleetDevs()}
	h.devs.add("box", "Box")
	h.syncDevs()
	spawnWindowQuiesce(t, s)
	s.hub, s.devs = h, h.devs
	return s, f, todo, h
}

// The child of a task_spawn runs before its start answers. Until it is linked as spawned it neither sees nor can call
// task_spawn, however its workspace linked it; afterwards it is refused as a spawned child. An Agent that merely
// opened in the same workspace meanwhile is held back only while the start is in flight, then lists and uses
// task_spawn as before.
func TestTaskSpawnChildCannotSpawnBeforeItsStartAnswers(t *testing.T) {
	s, f, _, h := spawnWindowFixture(t, nil)
	before := len(f.starts)
	outer := s.mcpCall(t.Context(), "box", "s1", "task_spawn", spawnPromptArgs("Only inspect the first parser assertion; edit nothing and return its line."), false)
	if outer.IsError || h.child == "" {
		t.Fatalf("first layer refused or never started: %+v", outer)
	}
	if len(h.window) != 2 {
		t.Fatalf("fixture: the in-flight requests did not run: %+v", h.window)
	}
	child, sibling := reached(t, "child", h.window[h.child]), reached(t, "sibling", h.window["sibling"])
	t.Logf("in flight: child=%s %+v sibling %+v", h.child, child, sibling)
	if child.listed || !child.failed || !strings.Contains(child.text, "one supervised handoff layer") || !strings.Contains(child.text, "do not retry") {
		t.Errorf("child could spawn before its start answered: %+v", child)
	}
	if sibling.listed || !sibling.failed {
		t.Errorf("a session linked while the start was in flight passed for a first-layer Agent: %+v", sibling)
	}
	now := onlyTodo(s)
	link, ok := activeTodoAgent(now, "box", h.child)
	if !ok || link.Source != sourceSpawned || link.Via != "box/s1" {
		t.Fatalf("child is not linked as spawned by s1: %+v", now.Agents)
	}
	if side, ok := activeTodoAgent(now, "box", "sibling"); !ok || side.Source != "space" {
		t.Fatalf("workspace sibling lost its own link: %+v", now.Agents)
	}
	if len(f.starts) != before+1 || len(now.Agents) != 3 {
		t.Fatalf("starts_delta=%d want 1, links=%d want 3 (s1, child, sibling): %+v", len(f.starts)-before, len(now.Agents), now.Agents)
	}
	// The workspace sees the child again in the next session list: it does not link it back as space.
	h.syncDevs()
	h.f.mu.Lock()
	list := slices.Clone(h.f.sessions["box"])
	h.f.mu.Unlock()
	s.SessionsChanged(t.Context(), DeviceSessions{MachineID: "box", Sessions: list})
	if link, ok := activeTodoAgent(onlyTodo(s), "box", h.child); !ok || link.Source != sourceSpawned || link.Via != "box/s1" {
		t.Fatalf("the next session list relinked the child: %+v", onlyTodo(s).Agents)
	}
	after := reached(t, "child after", spawnWindowNode(h, h.child, "Only inspect the third assertion; edit nothing and return its line."))
	if after.listed || !after.failed || !strings.Contains(after.text, "already a spawned child") {
		t.Fatalf("linked child was not refused as spawned: %+v", after)
	}
	// The sibling is an ordinary workspace Agent once the child is linked: it lists task_spawn and starts its own child.
	again := reached(t, "sibling after", spawnWindowNode(h, "sibling", "Only inspect the lexer assertion; edit nothing and return its line."))
	if !again.listed || again.failed || len(f.starts) != before+2 {
		t.Fatalf("workspace Agent kept no task_spawn after the start answered: %+v starts_delta=%d", again, len(f.starts)-before)
	}
	for _, a := range onlyTodo(s).Agents {
		if a.Via == "box/"+h.child {
			t.Fatalf("a grandchild was linked: %+v", a)
		}
	}
	if _, ok := activeTodoAgent(onlyTodo(s), "box", "s1"); !ok {
		t.Fatal("parent lost its link")
	}
}

// When the start's result is unknown, the session the node may have started cannot be told from a workspace Agent
// linked meanwhile: both keep no task_spawn. Agents linked before or after keep it.
func TestTaskSpawnUnknownStartKeepsWindowSessionsFromSpawning(t *testing.T) {
	s, f, _, h := spawnWindowFixture(t, context.DeadlineExceeded)
	before := len(f.starts)
	outer := s.mcpCall(t.Context(), "box", "s1", "task_spawn", spawnPromptArgs("Only inspect the first parser assertion; edit nothing and return its line."), false)
	if !outer.IsError || h.child == "" {
		t.Fatalf("an unknown start result was reported as started: %+v", outer)
	}
	if len(h.window) != 2 {
		t.Fatalf("fixture: the in-flight requests did not run: %+v", h.window)
	}
	for _, id := range []string{h.child, "sibling"} {
		if seen := reached(t, id, h.window[id]); seen.listed || !seen.failed {
			t.Fatalf("%s could spawn while the start was in flight: %+v", id, seen)
		}
		if _, ok := activeTodoAgent(onlyTodo(s), "box", id); !ok {
			t.Fatalf("fixture: %s was not linked by its workspace: %+v", id, onlyTodo(s).Agents)
		}
		later := reached(t, id+" later", spawnWindowNode(h, id, "Only inspect the third assertion; edit nothing and return its line."))
		if later.listed || !later.failed || !strings.Contains(later.text, "one supervised handoff layer") {
			t.Fatalf("%s spawned after a start with an unknown result: %+v", id, later)
		}
	}
	// A workspace Agent linked after the start ended is not suspect.
	late := proto.Session{MachineID: "box", SessionID: "late", SpaceName: "app", Cwd: "/code/app", Launcher: "codex",
		Agent: "codex", PermissionMode: "plan", Status: proto.StatusWorking}
	f.mu.Lock()
	f.sessions["box"] = append(f.sessions["box"], late)
	list := slices.Clone(f.sessions["box"])
	f.mu.Unlock()
	h.syncDevs()
	s.SessionsChanged(t.Context(), DeviceSessions{MachineID: "box", Sessions: list})
	if seen := reached(t, "late", spawnWindowNode(h, "late", "Only inspect the lexer assertion; edit nothing and return its line.")); !seen.listed || seen.failed {
		t.Fatalf("a workspace Agent linked after the start ended cannot spawn: %+v", seen)
	}
	if seen := reached(t, "s1", spawnWindowNode(h, "s1", "Only inspect the formatter assertion; edit nothing and return its line.")); !seen.listed || seen.failed {
		t.Fatalf("the original Agent cannot spawn after an unknown start: %+v", seen)
	}
	if len(f.starts) != before+3 {
		t.Fatalf("starts_delta=%d want 3 (the unknown start, late's child, s1's child)", len(f.starts)-before)
	}
}

// spawnWindowSetSessionForRace updates only the node/fleet rows, without auto-linking the session to a to-do.
func spawnWindowSetSessionForRace(h *spawnWindowHub, c proto.Session) {
	h.f.mu.Lock()
	found := false
	for i := range h.f.sessions["box"] {
		if h.f.sessions["box"][i].SessionID == c.SessionID {
			h.f.sessions["box"][i] = c
			found = true
			break
		}
	}
	if !found {
		h.f.sessions["box"] = append(h.f.sessions["box"], c)
	}
	h.f.mu.Unlock()
	h.syncDevs()
}

// spawnWindowLinkForRace adds c through the production workspace auto-link path and waits for its membership.
func spawnWindowLinkForRace(ctx context.Context, h *spawnWindowHub, c proto.Session) error {
	spawnWindowSetSessionForRace(h, c)
	h.f.mu.Lock()
	list := slices.Clone(h.f.sessions["box"])
	h.f.mu.Unlock()
	h.s.SessionsChanged(ctx, DeviceSessions{MachineID: "box", Sessions: list})
	if _, ok := activeTodoAgent(onlyTodo(h.s), "box", c.SessionID); !ok {
		return fmt.Errorf("workspace did not link %s: %+v", c.SessionID, onlyTodo(h.s).Agents)
	}
	return nil
}

// A successful parent links its child as spawned and closes the pending start. The caller invokes this while the
// authorization test hook is paused, exactly where the old code had already taken its space snapshot.
func spawnWindowCompleteParent(ctx context.Context, s *Service, p *spawnStart, todoID, child string) error {
	if err := s.linkTodoAgentWithSource(ctx, todoID, "box", child, "Race child", "/code/app", "codex", "", "spawned", "box/s1"); err != nil {
		return err
	}
	s.endSpawnStart(p, true)
	return nil
}

// A space snapshot and the pending set must be one decision. Otherwise a child can pass as space, let its parent
// finish, and then use the now-empty pending set to list task_spawn or start a grandchild (#11999 review P1-1).
func TestTaskSpawnSuccessfulEndCannotAuthorizeStaleSpaceSnapshot(t *testing.T) {
	for _, entry := range []string{"discovery", "direct"} {
		t.Run(entry, func(t *testing.T) {
			s, f, todo, h := spawnWindowFixture(t, nil)
			h.armed = false // this test drives the parent transition itself, not the node-start fixture
			p := s.beginSpawnStart("box")
			child := proto.Session{MachineID: "box", SessionID: "stale-space-child", SpaceName: "app", Cwd: "/code/app",
				Launcher: "codex", Agent: "codex", PermissionMode: "plan", Status: proto.StatusWorking}
			if err := spawnWindowLinkForRace(t.Context(), h, child); err != nil {
				t.Fatal(err)
			}

			reachedHook, release := make(chan struct{}), make(chan struct{})
			var enteredOnce, releaseOnce sync.Once
			releaseHook := func() { releaseOnce.Do(func() { close(release) }) }
			spawnAuthorizationTestHook = func() {
				enteredOnce.Do(func() { close(reachedHook) })
				<-release
			}
			t.Cleanup(func() { releaseHook(); spawnAuthorizationTestHook = nil })
			type outcome struct {
				tools []proto.Tool
				text  string
				err   error
			}
			done := make(chan outcome, 1)
			go func() {
				if entry == "discovery" {
					done <- outcome{tools: s.spawnTools(t.Context(), "box", child.SessionID)}
					return
				}
				text, err := s.taskSpawn(t.Context(), "box", child.SessionID, spawnPromptArgs("Only inspect one bounded assertion; edit nothing and return its result."))
				done <- outcome{text: text, err: err}
			}()
			spawnWindowWait(t, reachedHook, "authorization hook")
			parentDone := make(chan error, 1)
			go func() { parentDone <- spawnWindowCompleteParent(t.Context(), s, p, todo.ID, child.SessionID) }()
			if err := spawnWindowWait(t, parentDone, "parent link and successful end"); err != nil {
				t.Fatal(err)
			}
			startsAfterParent := len(f.starts)
			releaseHook()
			got := spawnWindowWait(t, done, "authorization result")
			spawnAuthorizationTestHook = nil

			if entry == "discovery" {
				if toolListed(got.tools, "task_spawn") {
					t.Fatalf("a closed start accepted the stale space snapshot and rediscovered task_spawn: %+v", got.tools)
				}
			} else {
				if got.err == nil || !strings.Contains(got.err.Error(), "already a spawned child") {
					t.Fatalf("a closed start accepted the stale space snapshot: text=%q err=%v", got.text, got.err)
				}
			}
			if len(f.starts) != startsAfterParent {
				t.Fatalf("the stale authorization started %d new sessions", len(f.starts)-startsAfterParent)
			}
		})
	}
}

// Failing a start must suspect every link from the whole end transition. In particular, an auto-link after an early
// snapshot but before pending is deleted is still inside the unknown-start window (#11999 review P1-2).
func TestTaskSpawnFailedEndSuspectsLinksAddedBeforePendingDelete(t *testing.T) {
	s, f, _, h := spawnWindowFixture(t, nil)
	h.armed = false
	p := s.beginSpawnStart("box")
	child := proto.Session{MachineID: "box", SessionID: "failure-window-child", SpaceName: "app", Cwd: "/code/app",
		Launcher: "codex", Agent: "codex", PermissionMode: "plan", Status: proto.StatusWorking}

	snapshot := make(chan map[string]bool, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseHook := func() { releaseOnce.Do(func() { close(release) }) }
	spawnEndTestHook = func() {
		snapshot <- s.linkedSessionsOn("box")
		<-release
	}
	t.Cleanup(func() { releaseHook(); spawnEndTestHook = nil })
	ended := make(chan struct{})
	go func() {
		s.endSpawnStart(p, false)
		close(ended)
	}()
	before := spawnWindowWait(t, snapshot, "failure-end snapshot")
	if before[child.SessionID] {
		t.Fatal("fixture: child was linked before the early snapshot")
	}
	// This is after the old code's snapshot and before it deletes pending. The fixed end transition has not taken its
	// authoritative snapshot yet, so this link must become suspect rather than escape the unknown-start window.
	linkDone := make(chan error, 1)
	go func() { linkDone <- spawnWindowLinkForRace(t.Context(), h, child) }()
	if err := spawnWindowWait(t, linkDone, "auto-link during failure end"); err != nil {
		t.Fatal(err)
	}
	releaseHook()
	spawnWindowWait(t, ended, "failure end")
	spawnEndTestHook = nil
	startsAfterEnd := len(f.starts)

	seen := reached(t, "failure-window child", spawnWindowNode(h, child.SessionID, "Only inspect one bounded assertion; edit nothing and return its result."))
	if seen.listed || !seen.failed || !strings.Contains(seen.text, "one supervised handoff layer") {
		t.Fatalf("a link inside the failure-end transition escaped suspect: %+v", seen)
	}
	if len(f.starts) != startsAfterEnd {
		t.Fatalf("the escaped failure-window link started %d new sessions", len(f.starts)-startsAfterEnd)
	}
}

// spawnWindowDisconnectForRace is the in-memory equivalent of the production agent unlink used by these mem-only
// fixtures. It does not erase the spawn window's history: that is the boundary under test.
func spawnWindowDisconnectForRace(s *Service, todoID, session string) {
	now := time.Now().UTC()
	s.todos.writing.Lock()
	s.todos.mu.Lock()
	if target := s.todos.todos[todoID]; target != nil {
		for i := range target.Agents {
			if target.Agents[i].MachineID == "box" && target.Agents[i].SessionID == session {
				target.Agents[i].ReleasedAt = &now
				target.Agents[i].DismissedAt = &now
			}
		}
	}
	if s.todos.activeBySession[[2]string{"box", session}] == todoID {
		delete(s.todos.activeBySession, [2]string{"box", session})
	}
	s.todos.mu.Unlock()
	s.todos.writing.Unlock()
}

// A final active-only snapshot misses a window member that disconnected before a failed end. The failed start must
// remember it when it is linked again, or it can return as an ordinary space Agent and start the next layer.
func TestTaskSpawnFailedEndRemembersDisconnectedWindowLinks(t *testing.T) {
	s, f, todo, h := spawnWindowFixture(t, nil)
	h.armed = false
	p := s.beginSpawnStart("box")
	child := proto.Session{MachineID: "box", SessionID: "disconnected-window-child", SpaceName: "app", Cwd: "/code/app",
		Launcher: "codex", Agent: "codex", PermissionMode: "plan", Status: proto.StatusWorking}
	if err := spawnWindowLinkForRace(t.Context(), h, child); err != nil {
		t.Fatal(err)
	}
	spawnWindowDisconnectForRace(s, todo.ID, child.SessionID)
	if _, active := s.taskOf(t.Context(), "box", child.SessionID); active {
		t.Fatal("fixture: child is still active after disconnect")
	}
	s.endSpawnStart(p, false)
	if err := s.linkTodoAgentWithSource(t.Context(), todo.ID, "box", child.SessionID, "Race child", "/code/app", "codex", "", "space", spaceKey("box", "app")); err != nil {
		t.Fatal(err)
	}
	startsAfterRelink := len(f.starts)
	seen := reached(t, "relinked failure-window child", spawnWindowNode(h, child.SessionID, "Only inspect one bounded assertion; edit nothing and return its result."))
	if seen.listed || !seen.failed || !strings.Contains(seen.text, "one supervised handoff layer") {
		t.Fatalf("a disconnected failure-window member escaped suspect after relink: %+v", seen)
	}
	if len(f.starts) != startsAfterRelink {
		t.Fatalf("the relinked failure-window member started %d new sessions", len(f.starts)-startsAfterRelink)
	}
}

// The assignment read is not the final authorization. A caller that becomes spawned or loses its membership before
// begin must be refused before its start reaches the node (#11999 design follow-up).
func TestTaskSpawnBeginRechecksAuthorizationBeforeNodeStart(t *testing.T) {
	for _, change := range []string{"source", "membership"} {
		t.Run(change, func(t *testing.T) {
			s, f, todo, h := spawnWindowFixture(t, nil)
			h.armed = false
			child := proto.Session{MachineID: "box", SessionID: "late-begin-child", SpaceName: "app", Cwd: "/code/app",
				Launcher: "codex", Agent: "codex", PermissionMode: "plan", Status: proto.StatusWorking}
			if err := spawnWindowLinkForRace(t.Context(), h, child); err != nil {
				t.Fatal(err)
			}
			reachedHook, release := make(chan struct{}), make(chan struct{})
			var enteredOnce, releaseOnce sync.Once
			releaseHook := func() { releaseOnce.Do(func() { close(release) }) }
			spawnBeginTestHook = func() {
				enteredOnce.Do(func() { close(reachedHook) })
				<-release
			}
			t.Cleanup(func() { releaseHook(); spawnBeginTestHook = nil })
			type outcome struct {
				text string
				err  error
			}
			done := make(chan outcome, 1)
			go func() {
				text, err := s.taskSpawn(t.Context(), "box", child.SessionID, spawnPromptArgs("Only inspect one bounded assertion; edit nothing and return its result."))
				done <- outcome{text: text, err: err}
			}()
			spawnWindowWait(t, reachedHook, "late-begin hook")
			if change == "source" {
				p := s.beginSpawnStart("box")
				parentDone := make(chan error, 1)
				go func() { parentDone <- spawnWindowCompleteParent(t.Context(), s, p, todo.ID, child.SessionID) }()
				if err := spawnWindowWait(t, parentDone, "parent transition before begin"); err != nil {
					t.Fatal(err)
				}
			} else {
				spawnWindowDisconnectForRace(s, todo.ID, child.SessionID)
			}
			startsAfterChange := len(f.starts)
			releaseHook()
			got := spawnWindowWait(t, done, "late-begin result")
			spawnBeginTestHook = nil
			if got.err == nil {
				t.Fatalf("late-begin %s change authorized a start: %s", change, got.text)
			}
			if change == "source" && !strings.Contains(got.err.Error(), "already a spawned child") {
				t.Fatalf("late-begin source change returned the wrong refusal: %v", got.err)
			}
			if len(f.starts) != startsAfterChange {
				t.Fatalf("late-begin %s change reached the node %d times", change, len(f.starts)-startsAfterChange)
			}
			for _, agent := range onlyTodo(s).Agents {
				if agent.Via == "box/"+child.SessionID {
					t.Fatalf("late-begin %s change linked a grandchild: %+v", change, agent)
				}
			}
		})
	}
}

// Starting the node is not permission to confirm the child. If the caller becomes spawned while session.start is in
// flight, the final authorization refuses the link and stops the unconfirmed child instead.
func TestTaskSpawnStartRechecksAuthorizationBeforeLinkingChild(t *testing.T) {
	s, f, todo, h := spawnWindowFixture(t, nil)
	child := proto.Session{MachineID: "box", SessionID: "post-start-caller", SpaceName: "app", Cwd: "/code/app",
		Launcher: "codex", Agent: "codex", PermissionMode: "plan", Status: proto.StatusWorking}
	if err := spawnWindowLinkForRace(t.Context(), h, child); err != nil {
		t.Fatal(err)
	}
	var linkErr error
	h.duringStart = func(_ proto.Session) {
		linkErr = s.linkTodoAgentWithSource(t.Context(), todo.ID, "box", child.SessionID, "Caller became spawned", "/code/app", "codex", "", "spawned", "box/s1")
	}
	text, err := s.taskSpawn(t.Context(), "box", child.SessionID, spawnPromptArgs("Only inspect one bounded assertion; edit nothing and return its result."))
	if linkErr != nil {
		t.Fatal(linkErr)
	}
	if err == nil || !strings.Contains(err.Error(), "already a spawned child") {
		t.Fatalf("post-start ancestry was not rechecked: text=%q err=%v", text, err)
	}
	for _, agent := range onlyTodo(s).Agents {
		if agent.Via == "box/"+child.SessionID {
			t.Fatalf("post-start ancestry produced a confirmed grandchild: %+v", agent)
		}
	}
	if h.child == "" || !slices.Contains(f.stopped, h.child) {
		t.Fatalf("unconfirmed child was not stopped: child=%q stopped=%v", h.child, f.stopped)
	}
}

// spawnReloadStore exposes only the inventory reads used by loadTodosLocked, while retaining the fixture's plan store.
// Embedding the two interfaces keeps optional stores absent rather than turning them into nil-method panics.
type spawnReloadStore struct {
	Store
	todoStore
	mu          sync.Mutex
	todos       []db.TodoRow
	spaces      []db.TodoSpaceRow
	agents      []db.TodoAgentRow
	supervision []db.TodoSupervisionRow
}

func (s *spawnReloadStore) ListTodos(context.Context) ([]db.TodoRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.todos), nil
}
func (s *spawnReloadStore) ListAllTodoSpaces(context.Context) ([]db.TodoSpaceRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.spaces), nil
}
func (s *spawnReloadStore) ListAllTodoAgents(context.Context) ([]db.TodoAgentRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.agents), nil
}
func (s *spawnReloadStore) ListTodoSupervision(context.Context) ([]db.TodoSupervisionRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.supervision), nil
}
func (*spawnReloadStore) PutTodoSupervision(context.Context, db.TodoSupervisionRow) error { return nil }
func (*spawnReloadStore) ReviewDoneTodo(context.Context, string, string, bool, time.Time) (*db.TodoRow, bool, error) {
	return &db.TodoRow{}, false, nil
}
func (*spawnReloadStore) LinkTodoReviewer(context.Context, string, string, string, string, string, string, string, time.Time) error {
	return nil
}
func (s *spawnReloadStore) LinkTodoAgentWithSource(_ context.Context, todoID, machineID, sessionID, title, cwd, agent, startError, source, via string, linkedAt time.Time) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.agents {
		if s.agents[i].MachineID == machineID && s.agents[i].SessionID == sessionID {
			s.agents[i].ReleasedAt = &linkedAt
		}
	}
	s.agents = slices.DeleteFunc(s.agents, func(a db.TodoAgentRow) bool {
		return a.TodoID == todoID && a.MachineID == machineID && a.SessionID == sessionID
	})
	s.agents = append(s.agents, db.TodoAgentRow{TodoID: todoID, MachineID: machineID, SessionID: sessionID, Title: title,
		Cwd: cwd, Agent: agent, StartError: startError, Source: source, Via: via, LinkedAt: linkedAt})
	return nil, nil
}
func (*spawnReloadStore) ListTodoDecisions(context.Context, string, bool) ([]db.TodoDecisionRow, error) {
	return nil, nil
}
func (*spawnReloadStore) ListTodoThreads(context.Context) (map[string]db.TodoThread, error) {
	return map[string]db.TodoThread{}, nil
}
func (*spawnReloadStore) ListTodoSources(context.Context) (map[string]db.TodoSource, error) {
	return map[string]db.TodoSource{}, nil
}
func (s *spawnReloadStore) TaskPlansMadeSince(ctx context.Context, since time.Time) ([]db.TaskPlanRow, error) {
	if history, ok := s.Store.(planExecutionHistoryStore); ok {
		return history.TaskPlansMadeSince(ctx, since)
	}
	return nil, nil
}

func newSpawnReloadStore(s *Service, todo Todo, child string) *spawnReloadStore {
	now := time.Now().UTC()
	return &spawnReloadStore{Store: s.store(),
		todos: []db.TodoRow{{ID: todo.ID, Title: todo.Title, Notes: todo.Notes, CreatedAt: todo.CreatedAt, UpdatedAt: todo.UpdatedAt,
			Origin: todo.Origin, PlanID: todo.PlanID}},
		spaces: []db.TodoSpaceRow{{TodoID: todo.ID, MachineID: "box", Space: "app", LinkedAt: now}},
		agents: []db.TodoAgentRow{
			{TodoID: todo.ID, MachineID: "box", SessionID: "s1", Cwd: "/code/app", Agent: "codex", Source: "manual", LinkedAt: now},
			{TodoID: todo.ID, MachineID: "box", SessionID: child, Title: "Reload child", Cwd: "/code/app", Agent: "codex", Source: "space", Via: spaceKey("box", "app"), LinkedAt: now},
		},
		supervision: []db.TodoSupervisionRow{todo.Supervision.row(todo.ID)},
	}
}

func spawnWindowReloadFixture(t *testing.T, child string) (*Service, *fakeHub, Todo, *spawnWindowHub) {
	t.Helper()
	plans := &profileTaskStore{memStore: &memStore{}, plans: map[string]db.TaskPlanRow{}}
	s, f, todo, h := spawnWindowFixtureStore(t, nil, plans)
	h.armed = false
	spawnWindowQuiesce(t, s)
	store := newSpawnReloadStore(s, todo, child)
	s.store = func() Store { return store }
	s.todos.mu.Lock()
	s.todos.loaded = false // an uncertain write invalidates the inventory; the next read reloads durable rows
	s.todos.mu.Unlock()
	return s, f, todo, h
}

func spawnWindowRequireReloadedCaller(t *testing.T, s *Service, child string) {
	t.Helper()
	current := onlyTodo(s)
	agent, ok := activeTodoAgent(current, "box", child)
	if !ok {
		t.Fatalf("fixture: reload did not activate %s: agents=%+v", child, current.Agents)
	}
	if _, ok := s.spawnTask(t.Context(), "box", child); !ok {
		t.Fatalf("fixture: reloaded caller is not on an open supervised task: agent=%+v supervision=%+v origin=%q done=%v", agent, current.Supervision, current.Origin, current.DoneAt)
	}
}

// A known child remains suspect when confirmation fails even if session.stop fails and the child is not in any
// membership snapshot. Otherwise a late workspace link would make it an ordinary first-layer Agent.
func TestTaskSpawnFailedStopKeepsLateChildSuspect(t *testing.T) {
	s, f, todo, h := spawnWindowFixture(t, nil)
	child := proto.Session{MachineID: "box", SessionID: "failed-stop-caller", SpaceName: "app", Cwd: "/code/app",
		Launcher: "codex", Agent: "codex", PermissionMode: "plan", Status: proto.StatusWorking}
	if err := spawnWindowLinkForRace(t.Context(), h, child); err != nil {
		t.Fatal(err)
	}
	f.retireErr = errors.New("session.stop failed")
	var linkErr error
	h.duringStart = func(_ proto.Session) {
		linkErr = s.linkTodoAgentWithSource(t.Context(), todo.ID, "box", child.SessionID, "Caller became spawned", "/code/app", "codex", "", "spawned", "box/s1")
	}
	text, err := s.taskSpawn(t.Context(), "box", child.SessionID, spawnPromptArgs("Only inspect one bounded assertion; edit nothing and return its result."))
	if linkErr != nil {
		t.Fatal(linkErr)
	}
	if err == nil {
		t.Fatalf("post-start refusal unexpectedly succeeded: %s", text)
	}
	if !strings.Contains(err.Error(), "stop result: session.stop failed") {
		t.Fatalf("failed-stop cleanup was not exercised: %v", err)
	}
	for _, agent := range onlyTodo(s).Agents {
		if agent.SessionID == h.child && agent.Source == sourceSpawned {
			t.Fatalf("unconfirmed child was already linked as spawned: %+v", agent)
		}
	}
	late := proto.Session{MachineID: "box", SessionID: h.child, SpaceName: "app", Cwd: "/code/app",
		Launcher: "codex", Agent: "codex", PermissionMode: "plan", Status: proto.StatusWorking}
	if err := spawnWindowLinkForRace(t.Context(), h, late); err != nil {
		t.Fatal(err)
	}
	if agent, ok := activeTodoAgent(onlyTodo(s), "box", h.child); !ok || agent.Source != "space" {
		t.Fatalf("late child did not arrive as an ordinary space membership: %+v", agent)
	}
	startsBeforeCall := len(f.starts)
	seen := reached(t, "late unconfirmed child", spawnWindowNode(h, h.child, "Only inspect one bounded assertion; edit nothing and return its result."))
	if seen.listed || !seen.failed || !strings.Contains(seen.text, "one supervised handoff layer") {
		t.Fatalf("failed-stop late child escaped suspect: %+v", seen)
	}
	if len(f.starts) != startsBeforeCall {
		t.Fatalf("failed-stop late child started %d new sessions", len(f.starts)-startsBeforeCall)
	}
}

// Inventory reload is also a membership writer: it must publish through the same window boundary and history as
// auto-link and hand links, both during the final failed-end transition and across disconnect/relink.
func TestTaskSpawnReloadPublishesMembershipInsideWindow(t *testing.T) {
	for _, mode := range []string{"failure-end", "disconnect-relink"} {
		t.Run(mode, func(t *testing.T) {
			const childID = "reload-window-child"
			s, f, todo, h := spawnWindowReloadFixture(t, childID)
			p := s.beginSpawnStart("box")
			if mode == "failure-end" {
				snapshot := make(chan map[string]bool, 1)
				release := make(chan struct{})
				var releaseOnce sync.Once
				releaseHook := func() { releaseOnce.Do(func() { close(release) }) }
				spawnEndTestHook = func() {
					snapshot <- s.linkedSessionsOn("box")
					<-release
				}
				t.Cleanup(func() { releaseHook(); spawnEndTestHook = nil })
				ended := make(chan struct{})
				go func() { s.endSpawnStart(p, false); close(ended) }()
				before := spawnWindowWait(t, snapshot, "reload failure-end snapshot")
				if before[childID] {
					t.Fatal("fixture: reload child was active before the snapshot")
				}
				loaded := make(chan error, 1)
				go func() { _, err := s.loadTodoInventory(t.Context()); loaded <- err }()
				if err := spawnWindowWait(t, loaded, "inventory reload during failure end"); err != nil {
					t.Fatal(err)
				}
				spawnWindowRequireReloadedCaller(t, s, childID)
				releaseHook()
				spawnWindowWait(t, ended, "reload failure end")
				spawnEndTestHook = nil
			} else {
				if _, err := s.loadTodoInventory(t.Context()); err != nil {
					t.Fatal(err)
				}
				if _, ok := activeTodoAgent(onlyTodo(s), "box", childID); !ok {
					t.Fatal("fixture: reload did not publish the child")
				}
				spawnWindowRequireReloadedCaller(t, s, childID)
				spawnWindowDisconnectForRace(s, todo.ID, childID)
				s.endSpawnStart(p, false)
				if err := s.linkTodoAgentWithSource(t.Context(), todo.ID, "box", childID, "Reload child", "/code/app", "codex", "", "space", spaceKey("box", "app")); err != nil {
					t.Fatal(err)
				}
			}
			spawnWindowSetSessionForRace(h, proto.Session{MachineID: "box", SessionID: childID, SpaceName: "app", Cwd: "/code/app",
				Launcher: "codex", Agent: "codex", PermissionMode: "plan", Status: proto.StatusWorking})
			startsBeforeCall := len(f.starts)
			seen := reached(t, "reload child", spawnWindowNode(h, childID, "Only inspect one bounded assertion; edit nothing and return its result."))
			if seen.listed || !seen.failed || !strings.Contains(seen.text, "one supervised handoff layer") {
				t.Fatalf("reload-published child escaped suspect in %s: %+v", mode, seen)
			}
			if len(f.starts) != startsBeforeCall {
				t.Fatalf("reload-published child started %d new sessions in %s", len(f.starts)-startsBeforeCall, mode)
			}
		})
	}
}

// The post-start authorization and the child link must be one write transaction. A caller that changes between the
// old recheck and link cannot turn the already-started child into a confirmed grandchild.
func TestTaskSpawnLinkAuthorizationIsAtomic(t *testing.T) {
	for _, change := range []string{"source", "membership"} {
		t.Run(change, func(t *testing.T) {
			s, f, todo, h := spawnWindowFixture(t, nil)
			child := proto.Session{MachineID: "box", SessionID: "atomic-link-caller", SpaceName: "app", Cwd: "/code/app",
				Launcher: "codex", Agent: "codex", PermissionMode: "plan", Status: proto.StatusWorking}
			if err := spawnWindowLinkForRace(t.Context(), h, child); err != nil {
				t.Fatal(err)
			}
			h.duringStart = func(_ proto.Session) {}
			reachedHook, release := make(chan struct{}), make(chan struct{})
			var enteredOnce, releaseOnce sync.Once
			releaseHook := func() { releaseOnce.Do(func() { close(release) }) }
			spawnLinkTestHook = func() {
				enteredOnce.Do(func() { close(reachedHook) })
				<-release
			}
			t.Cleanup(func() { releaseHook(); spawnLinkTestHook = nil })
			type outcome struct {
				text string
				err  error
			}
			done := make(chan outcome, 1)
			go func() {
				text, err := s.taskSpawn(t.Context(), "box", child.SessionID, spawnPromptArgs("Only inspect one bounded assertion; edit nothing and return its result."))
				done <- outcome{text: text, err: err}
			}()
			spawnWindowWait(t, reachedHook, "post-start link hook")
			if change == "source" {
				if err := s.linkTodoAgentWithSource(t.Context(), todo.ID, "box", child.SessionID, "Caller became spawned", "/code/app", "codex", "", "spawned", "box/s1"); err != nil {
					t.Fatal(err)
				}
			} else {
				spawnWindowDisconnectForRace(s, todo.ID, child.SessionID)
			}
			startsAfterChange := len(f.starts)
			releaseHook()
			got := spawnWindowWait(t, done, "atomic-link result")
			spawnLinkTestHook = nil
			if got.err == nil {
				t.Fatalf("post-start %s change was confirmed as a grandchild: %s", change, got.text)
			}
			for _, agent := range onlyTodo(s).Agents {
				if agent.Via == "box/"+child.SessionID {
					t.Fatalf("post-start %s change linked a grandchild: %+v", change, agent)
				}
			}
			if len(f.starts) != startsAfterChange {
				t.Fatalf("post-start %s change started another session", change)
			}
			if h.child == "" || !slices.Contains(f.stopped, h.child) {
				t.Fatalf("post-start %s change did not stop the unconfirmed child: child=%q stopped=%v", change, h.child, f.stopped)
			}
		})
	}
}

// An unanswered start can still produce its child after the failure window closes. Reconcile that exact late identity
// instead of treating it as an ordinary late Agent; an unrelated StartedByPersona=false late Agent keeps old behavior.
func TestTaskSpawnUnknownStartReconcilesLateChildIdentity(t *testing.T) {
	s, f, _, h := spawnWindowFixture(t, context.DeadlineExceeded)
	h.others = nil
	h.duringStart = func(child proto.Session) {
		// Adoption must not find the child before the failed end: it is still being built and its answer/id are lost.
		h.f.mu.Lock()
		h.f.sessions["box"] = slices.DeleteFunc(h.f.sessions["box"], func(row proto.Session) bool { return row.SessionID == child.SessionID })
		h.f.mu.Unlock()
	}
	text, err := s.taskSpawn(t.Context(), "box", "s1", spawnPromptArgs("Only inspect one bounded assertion; edit nothing and return its result."))
	if err == nil || h.child == "" {
		t.Fatalf("fixture: unknown start returned success or no child id: text=%q err=%v child=%q", text, err, h.child)
	}
	late := proto.Session{MachineID: "box", SessionID: h.child, SpaceName: "app", Cwd: "/code/app", Launcher: "codex",
		Agent: "codex", PermissionMode: "plan", Status: proto.StatusWorking, StartedByPersona: true}
	if err := spawnWindowLinkForRace(t.Context(), h, late); err != nil {
		t.Fatal(err)
	}
	if agent, ok := activeTodoAgent(onlyTodo(s), "box", h.child); !ok || agent.Source != "space" {
		t.Fatalf("late unknown child did not arrive as space: %+v", agent)
	}
	startsBeforeCall := len(f.starts)
	seen := reached(t, "late unknown child", spawnWindowNode(h, h.child, "Only inspect one bounded assertion; edit nothing and return its result."))
	if seen.listed || !seen.failed || !strings.Contains(seen.text, "one supervised handoff layer") {
		t.Fatalf("late child from an unknown start escaped ancestry tracking: %+v", seen)
	}
	if len(f.starts) != startsBeforeCall {
		t.Fatalf("late unknown child started %d new sessions", len(f.starts)-startsBeforeCall)
	}
}

// Heuristic adoption is not proof of the child. A same-shaped concurrent session must not be stopped or linked as
// spawned; the real child still has to reconcile through unresolved identity tracking.
func TestTaskSpawnDoesNotConfirmHeuristicAdoption(t *testing.T) {
	s, f, _, h := spawnWindowFixture(t, context.DeadlineExceeded)
	h.others = nil
	h.duringStart = func(child proto.Session) {
		h.f.mu.Lock()
		h.f.sessions["box"] = slices.DeleteFunc(h.f.sessions["box"], func(row proto.Session) bool { return row.SessionID == child.SessionID })
		h.f.sessions["box"] = append(h.f.sessions["box"], proto.Session{MachineID: "box", SessionID: "wrong-adoptee", SpaceName: "app", Cwd: "/code/app",
			Launcher: "codex", Agent: "codex", PermissionMode: "plan", Status: proto.StatusWorking, StartedByPersona: true, UpdatedAt: time.Now()})
		h.f.mu.Unlock()
		h.syncDevs()
	}
	disabled := false
	s.admission.mu.Lock()
	s.admission.cached = &AdmissionSettings{Enabled: &disabled}
	s.admission.at = time.Now()
	s.admission.mu.Unlock()
	text, err := s.taskSpawn(t.Context(), "box", "s1", spawnPromptArgs("Only inspect one bounded assertion; edit nothing and return its result."))
	if err == nil {
		t.Fatalf("heuristic adoption was confirmed as the child: %s", text)
	}
	s.spawns.mu.Lock()
	unresolved := len(s.spawns.unresolved)
	for identity := range s.spawns.unresolved {
		t.Logf("identity: expected=%q launcher=%q cwd=%q machine=%q until=%v known=%v", identity.expected, identity.launcher, identity.cwd, identity.machine, identity.until, identity.known)
	}
	s.spawns.mu.Unlock()
	if unresolved != 1 {
		t.Fatalf("heuristic adoption left %d unresolved identities, want 1", unresolved)
	}
	for _, agent := range onlyTodo(s).Agents {
		if agent.SessionID == "wrong-adoptee" && agent.Source == sourceSpawned {
			t.Fatalf("heuristic adoptee was linked as spawned: %+v", agent)
		}
	}
	if slices.Contains(f.stopped, "wrong-adoptee") {
		t.Fatal("heuristic adoptee was stopped without proof")
	}
	late := proto.Session{MachineID: "box", SessionID: h.child, SpaceName: "app", Cwd: "/code/app", Launcher: "codex",
		Agent: "codex", PermissionMode: "plan", Status: proto.StatusWorking, StartedByPersona: true}
	for identity := range s.spawns.unresolved {
		t.Logf("late identity match=%v session=%+v", identity.matches(late), late)
	}
	if err := spawnWindowLinkForRace(t.Context(), h, late); err != nil {
		t.Fatal(err)
	}
	t.Logf("suspect after late link: %+v", s.spawns.suspect)
	startsBeforeCall := len(f.starts)
	seen := reached(t, "real late child", spawnWindowNode(h, h.child, "Only inspect one bounded assertion; edit nothing and return its result."))
	if seen.listed || !seen.failed || !strings.Contains(seen.text, "one supervised handoff layer") {
		t.Fatalf("real child escaped after heuristic adoption: %+v", seen)
	}
	if len(f.starts) != startsBeforeCall {
		t.Fatalf("real child started %d new sessions", len(f.starts)-startsBeforeCall)
	}
}

// An exact-id start must not confirm a same-shaped heuristic adoptee when its expected session is absent. The wrong
// session stays running and unconfirmed; the real expected session remains inside task_spawn ancestry tracking.
func TestTaskSpawnExactIDAdoptionRejectsSameShapeSession(t *testing.T) {
	s, f, _, h := spawnWindowFixtureLauncher(t, context.DeadlineExceeded, "claude")
	expected := ""
	f.startReply = func(params map[string]any, sess *proto.Session) *proto.StartInstead {
		expected, _ = params["session_id"].(string)
		sess.SessionID = expected
		return nil
	}
	wrong := proto.Session{MachineID: "box", SessionID: "wrong-exact-adoptee", SpaceName: "app", Cwd: "/code/app",
		Launcher: "claude", Agent: "claude", PermissionMode: "plan", Status: proto.StatusWorking, StartedByPersona: true, UpdatedAt: time.Now()}
	h.duringStart = func(child proto.Session) {
		if expected == "" || child.SessionID != expected {
			t.Errorf("fixture: expected id was not used: expected=%q child=%q", expected, child.SessionID)
		}
		h.f.mu.Lock()
		h.f.sessions["box"] = slices.DeleteFunc(h.f.sessions["box"], func(row proto.Session) bool { return row.SessionID == child.SessionID })
		h.f.sessions["box"] = append(h.f.sessions["box"], wrong)
		h.f.mu.Unlock()
		h.syncDevs()
	}
	disabled := false
	s.admission.mu.Lock()
	s.admission.cached = &AdmissionSettings{Enabled: &disabled}
	s.admission.at = time.Now()
	s.admission.mu.Unlock()
	text, err := s.taskSpawn(t.Context(), "box", "s1", spawnPromptArgs("Only inspect one bounded assertion; edit nothing and return its result."))
	if err == nil {
		t.Fatalf("same-shaped heuristic adoptee was confirmed for exact id %q: %s", expected, text)
	}
	if expected == "" || h.child != expected {
		t.Fatalf("fixture: expected id mismatch: expected=%q child=%q", expected, h.child)
	}
	for _, agent := range onlyTodo(s).Agents {
		if agent.SessionID == wrong.SessionID && agent.Source == sourceSpawned {
			t.Fatalf("wrong session was linked as the spawned child: %+v", agent)
		}
	}
	if slices.Contains(f.stopped, wrong.SessionID) {
		t.Fatal("wrong heuristic adoptee was stopped as though it were the exact child")
	}
	s.spawns.mu.Lock()
	unresolved := len(s.spawns.unresolved)
	s.spawns.mu.Unlock()
	if unresolved != 1 {
		t.Fatalf("exact-id start left %d unresolved identities, want 1", unresolved)
	}
	startsAfterStart := len(f.starts)
	if err := spawnWindowLinkForRace(t.Context(), h, wrong); err != nil {
		t.Fatal(err)
	}
	if seen := reached(t, "wrong exact adoptee", spawnWindowNode(h, wrong.SessionID, "Only inspect one bounded assertion; edit nothing and return its result.")); seen.listed || !seen.failed {
		t.Fatalf("wrong exact adoptee escaped ancestry tracking: %+v", seen)
	}
	real := proto.Session{MachineID: "box", SessionID: h.child, SpaceName: "app", Cwd: "/code/app",
		Launcher: "claude", Agent: "claude", PermissionMode: "plan", Status: proto.StatusWorking, StartedByPersona: true, UpdatedAt: time.Now()}
	if err := spawnWindowLinkForRace(t.Context(), h, real); err != nil {
		t.Fatal(err)
	}
	if seen := reached(t, "real exact child", spawnWindowNode(h, real.SessionID, "Only inspect one bounded assertion; edit nothing and return its result.")); seen.listed || !seen.failed {
		t.Fatalf("real exact child became an ordinary first-layer Agent: %+v", seen)
	}
	if len(f.starts) != startsAfterStart {
		t.Fatalf("wrong or real late sessions started another layer: before=%d after=%d", startsAfterStart, len(f.starts))
	}
}

// The reconnect adoption pass has the same exact-id rule: B appears only after the first lookup has failed and the
// link is coming back, so this exercises adoptTaskStartSessionUntil rather than the first adoption branch.
func TestTaskSpawnExactIDReconnectAdoptionRejectsSameShapeSession(t *testing.T) {
	s, f, _, h := spawnWindowFixtureLauncher(t, rpc.ErrDropped, "claude")
	oldWait, oldPoll, oldRetry := taskStartAdoptWait, taskStartAdoptPoll, agentStartAdoptRetry
	taskStartAdoptWait, taskStartAdoptPoll, agentStartAdoptRetry = 20*time.Millisecond, 100*time.Millisecond, 100*time.Millisecond
	t.Cleanup(func() {
		taskStartAdoptWait, taskStartAdoptPoll, agentStartAdoptRetry = oldWait, oldPoll, oldRetry
	})
	expected := ""
	f.startReply = func(params map[string]any, sess *proto.Session) *proto.StartInstead {
		expected, _ = params["session_id"].(string)
		sess.SessionID = expected
		return nil
	}
	wrong := proto.Session{MachineID: "box", SessionID: "wrong-reconnect-adoptee", SpaceName: "app", Cwd: "/code/app",
		Launcher: "claude", Agent: "claude", PermissionMode: "plan", Status: proto.StatusWorking, StartedByPersona: true, UpdatedAt: time.Now()}
	h.duringStart = func(child proto.Session) {
		h.f.mu.Lock()
		h.f.sessions["box"] = slices.DeleteFunc(h.f.sessions["box"], func(row proto.Session) bool { return row.SessionID == child.SessionID })
		h.f.mu.Unlock()
		lists := 0
		h.f.onList = func(f *fakeHub, machine string) { // the second lookup is the reconnect adoption pass
			lists++
			if lists == 2 {
				f.sessions[machine] = append(f.sessions[machine], wrong)
			}
		}
		h.syncDevs()
	}
	disabled := false
	s.admission.mu.Lock()
	s.admission.cached = &AdmissionSettings{Enabled: &disabled}
	s.admission.at = time.Now()
	s.admission.mu.Unlock()
	s.devs = nil // force adoption through the fake Hub's sessions.list, including the staged second lookup
	text, err := s.taskSpawn(t.Context(), "box", "s1", spawnPromptArgs("Only inspect one bounded assertion; edit nothing and return its result."))
	if err == nil {
		t.Fatalf("reconnect heuristic adoptee was confirmed for exact id %q: %s", expected, text)
	}
	for _, agent := range onlyTodo(s).Agents {
		if agent.SessionID == wrong.SessionID && agent.Source == sourceSpawned {
			t.Fatalf("reconnect wrong session was linked as spawned: %+v", agent)
		}
	}
	if slices.Contains(f.stopped, wrong.SessionID) {
		t.Fatal("reconnect wrong heuristic adoptee was stopped")
	}
	s.spawns.mu.Lock()
	unresolved := len(s.spawns.unresolved)
	s.spawns.mu.Unlock()
	if unresolved != 1 {
		t.Fatalf("reconnect exact-id start left %d unresolved identities, want 1", unresolved)
	}
}

// A definite pre-launch refusal must not poison later ordinary Agents. Only genuinely unknown starts can leave an
// unresolved identity, and that entry must have a bounded lifecycle.
func TestTaskSpawnDefiniteRefusalDoesNotPoisonLaterAgents(t *testing.T) {
	s, f, _, h := spawnWindowFixture(t, nil)
	h.armed = false
	f.failStart = errors.New("definite start refusal")
	if _, err := s.taskSpawn(t.Context(), "box", "s1", spawnPromptArgs("Only inspect one bounded assertion; edit nothing and return its result.")); err == nil {
		t.Fatal("definite start refusal unexpectedly succeeded")
	}
	f.failStart = nil // the next ordinary start must not inherit the injected one-shot refusal
	ordinary := proto.Session{MachineID: "box", SessionID: "ordinary-after-refusal", SpaceName: "app", Cwd: "/code/app",
		Launcher: "codex", Agent: "codex", PermissionMode: "plan", Status: proto.StatusWorking, StartedByPersona: true}
	if err := spawnWindowLinkForRace(t.Context(), h, ordinary); err != nil {
		t.Fatal(err)
	}
	before := len(f.starts)
	seen := reached(t, "ordinary after refusal", spawnWindowNode(h, ordinary.SessionID, "Only inspect one bounded assertion; edit nothing and return its result."))
	if !seen.listed || seen.failed {
		t.Fatalf("definite refusal poisoned an ordinary Agent: %+v", seen)
	}
	if len(f.starts) != before+1 {
		t.Fatalf("ordinary Agent did not start exactly one child: before=%d after=%d", before, len(f.starts))
	}
}

// Unresolved identities are scoped to their starting machine, including exact-id collisions. Another machine's
// ordinary same-launcher/cwd Agent must not inherit a failed start's suspect marker.
func TestTaskSpawnUnresolvedIdentityStaysOnStartMachine(t *testing.T) {
	s, f, todo, h := spawnWindowFixture(t, context.DeadlineExceeded)
	h.others = nil
	h.duringStart = func(child proto.Session) {
		h.f.mu.Lock()
		h.f.sessions["box"] = slices.DeleteFunc(h.f.sessions["box"], func(row proto.Session) bool { return row.SessionID == child.SessionID })
		h.f.mu.Unlock()
	}
	if _, err := s.taskSpawn(t.Context(), "box", "s1", spawnPromptArgs("Only inspect one bounded assertion; edit nothing and return its result.")); err == nil {
		t.Fatal("unknown start unexpectedly succeeded")
	}
	other := proto.Session{MachineID: "other", SessionID: "other-ordinary", SpaceName: "app", Cwd: "/code/app",
		Launcher: "codex", Agent: "codex", PermissionMode: "plan", Status: proto.StatusWorking, StartedByPersona: true}
	h.devs.add("other", "Other")
	h.devs.setSessions("other", []proto.Session{other})
	if _, err := s.linkTodoSpace(t.Context(), todo.ID, "other", "app"); err != nil {
		t.Fatal(err)
	}
	s.autoLinkTodos("other", []proto.Session{other})
	f.setSessions("other", other)
	before := len(f.starts)
	_, err := s.taskSpawn(t.Context(), "other", other.SessionID, spawnPromptArgs("Only inspect one bounded assertion; edit nothing and return its result."))
	if err != nil {
		t.Fatalf("unrelated machine inherited suspect: %v", err)
	}
	if len(f.starts) != before+1 {
		t.Fatalf("unrelated machine did not start exactly one child: before=%d after=%d", before, len(f.starts))
	}
	late := proto.Session{MachineID: "box", SessionID: h.child, SpaceName: "app", Cwd: "/code/app", Launcher: "codex",
		Agent: "codex", PermissionMode: "plan", Status: proto.StatusWorking, StartedByPersona: true}
	if err := spawnWindowLinkForRace(t.Context(), h, late); err != nil {
		t.Fatal(err)
	}
	seen := reached(t, "originating-machine child", spawnWindowNode(h, h.child, "Only inspect one bounded assertion; edit nothing and return its result."))
	if seen.listed || !seen.failed {
		t.Fatalf("originating machine's late child was not refused: %+v", seen)
	}
}

// Suspect marking and unresolved retirement are different decisions. A heuristic B at batch N must not retire the
// start before the real A arrives at batch N+1. Exact identities retire only on A; heuristic identities stay bounded
// until expiry, while suspect entries keep both A and B inside the one-layer rule.
func TestTaskSpawnUnresolvedRetirementRequiresExactIdentity(t *testing.T) {
	for _, tc := range []struct {
		name     string
		launcher string
	}{{"exact", "claude"}, {"non-exact", "codex"}} {
		t.Run(tc.name, func(t *testing.T) {
			s, f, _, h := spawnWindowFixtureLauncher(t, context.DeadlineExceeded, tc.launcher)
			if tc.launcher == "claude" {
				f.startReply = func(params map[string]any, sess *proto.Session) *proto.StartInstead {
					sess.SessionID, _ = params["session_id"].(string)
					return nil
				}
			}
			h.duringStart = func(child proto.Session) {
				h.f.mu.Lock()
				h.f.sessions["box"] = slices.DeleteFunc(h.f.sessions["box"], func(row proto.Session) bool { return row.SessionID == child.SessionID })
				h.f.mu.Unlock()
				h.syncDevs()
			}
			disabled := false
			s.admission.mu.Lock()
			s.admission.cached = &AdmissionSettings{Enabled: &disabled}
			s.admission.at = time.Now()
			s.admission.mu.Unlock()
			if text, err := s.taskSpawn(t.Context(), "box", "s1", spawnPromptArgs("Only inspect one bounded assertion; edit nothing and return its result.")); err == nil {
				t.Fatalf("unknown start unexpectedly succeeded: %s", text)
			}
			wrong := proto.Session{MachineID: "box", SessionID: "wrong-" + tc.name, SpaceName: "app", Cwd: "/code/app",
				Launcher: tc.launcher, Agent: tc.launcher, PermissionMode: "plan", Status: proto.StatusWorking, StartedByPersona: true, UpdatedAt: time.Now()}
			if err := spawnWindowLinkForRace(t.Context(), h, wrong); err != nil { // batch N: B only
				t.Fatal(err)
			}
			startsAfterStart := len(f.starts)
			if seen := reached(t, "batch N wrong session", spawnWindowNode(h, wrong.SessionID, "Only inspect one bounded assertion; edit nothing and return its result.")); seen.listed || !seen.failed {
				t.Fatalf("batch N wrong session escaped suspect: %+v", seen)
			}
			real := proto.Session{MachineID: "box", SessionID: h.child, SpaceName: "app", Cwd: "/code/app",
				Launcher: tc.launcher, Agent: tc.launcher, PermissionMode: "plan", Status: proto.StatusWorking, StartedByPersona: true, UpdatedAt: time.Now()}
			if err := spawnWindowLinkForRace(t.Context(), h, real); err != nil { // batch N+1: the actual child
				t.Fatal(err)
			}
			if seen := reached(t, "batch N+1 real child", spawnWindowNode(h, real.SessionID, "Only inspect one bounded assertion; edit nothing and return its result.")); seen.listed || !seen.failed {
				t.Fatalf("real child escaped after heuristic retirement at batch N: %+v", seen)
			}
			if seen := reached(t, "batch N+1 wrong session", spawnWindowNode(h, wrong.SessionID, "Only inspect one bounded assertion; edit nothing and return its result.")); seen.listed || !seen.failed {
				t.Fatalf("wrong session lost its suspect marker at batch N+1: %+v", seen)
			}
			s.spawns.mu.Lock()
			unresolved := len(s.spawns.unresolved)
			s.spawns.mu.Unlock()
			want := 1
			if tc.launcher == "claude" {
				want = 0 // the exact child was seen; its suspect marker still remains for this process
			}
			if unresolved != want {
				t.Fatalf("unresolved retirement after exact/heuristic batches = %d, want %d", unresolved, want)
			}
			if len(f.starts) != startsAfterStart {
				t.Fatalf("late sessions started another layer: before=%d after=%d", startsAfterStart, len(f.starts))
			}
		})
	}
}

// Workspace fanout can publish a late child before its sessions.changed event. Discovery and direct calls must
// reconcile that caller's typed identity even when the workspace membership arrived first.
func TestTaskSpawnLateSpaceFanoutReconcilesUnknownStart(t *testing.T) {
	oldWait, oldPoll, oldRetry := taskStartAdoptWait, taskStartAdoptPoll, agentStartAdoptRetry
	taskStartAdoptWait, taskStartAdoptPoll, agentStartAdoptRetry = 20*time.Millisecond, 5*time.Millisecond, time.Millisecond
	t.Cleanup(func() { taskStartAdoptWait, taskStartAdoptPoll, agentStartAdoptRetry = oldWait, oldPoll, oldRetry })
	for _, tc := range []struct {
		name     string
		launcher string
	}{{"exact", "claude"}, {"non-exact", "codex"}} {
		for _, inventoryFirst := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/inventory-first-%v", tc.name, inventoryFirst), func(t *testing.T) {
				s, f, todo, h := spawnWindowFixtureLauncher(t, context.DeadlineExceeded, tc.launcher)
				if err := s.unlinkTodoSpace(t.Context(), todo.ID, "box", "app"); err != nil {
					t.Fatal(err)
				}
				if tc.launcher == "claude" {
					f.startReply = func(params map[string]any, sess *proto.Session) *proto.StartInstead {
						sess.SessionID, _ = params["session_id"].(string)
						return nil
					}
				}
				h.duringStart = func(child proto.Session) {
					h.f.mu.Lock()
					h.f.sessions["box"] = slices.DeleteFunc(h.f.sessions["box"], func(row proto.Session) bool { return row.SessionID == child.SessionID })
					h.f.mu.Unlock()
					h.syncDevs()
				}
				disabled := false
				s.admission.mu.Lock()
				s.admission.cached, s.admission.at = &AdmissionSettings{Enabled: &disabled}, time.Now()
				s.admission.mu.Unlock()
				if text, err := s.taskSpawn(t.Context(), "box", "s1", spawnPromptArgs("Only inspect one bounded assertion; edit nothing and return its result.")); err == nil {
					t.Fatalf("fixture: uncertain start unexpectedly succeeded: %s", text)
				}
				s.spawns.mu.Lock()
				unresolved := len(s.spawns.unresolved)
				s.spawns.mu.Unlock()
				if unresolved != 1 || h.child == "" {
					t.Fatalf("fixture: unresolved=%d child=%q", unresolved, h.child)
				}
				late := proto.Session{MachineID: "box", SessionID: h.child, SpaceName: "app", Cwd: "/code/app",
					Launcher: tc.launcher, Agent: tc.launcher, PermissionMode: "plan", Status: proto.StatusWorking,
					StartedByPersona: true, UpdatedAt: time.Now()}
				spawnWindowSetSessionForRace(h, late) // lookup sees A before any sessions.changed event
				if inventoryFirst {
					h.f.mu.Lock()
					list := slices.Clone(h.f.sessions["box"])
					h.f.mu.Unlock()
					s.SessionsChanged(t.Context(), DeviceSessions{MachineID: "box", Sessions: list})
				}
				if _, err := s.linkTodoSpace(t.Context(), todo.ID, "box", "app"); err != nil {
					t.Fatal(err)
				}
				if agent, ok := activeTodoAgent(onlyTodo(s), "box", h.child); !ok || agent.Source != "space" {
					t.Fatalf("fixture: fanout did not publish the child as space: %+v", agent)
				}
				before := len(f.starts)
				seen := reached(t, "late fanout child", spawnWindowNode(h, h.child, "Only inspect a second bounded assertion; edit nothing and return its result."))
				if seen.listed || !seen.failed || len(f.starts) != before {
					t.Fatalf("late workspace fanout escaped unresolved ancestry: launcher=%s inventoryFirst=%v listed=%v failed=%v starts_delta=%d text=%q", tc.launcher, inventoryFirst, seen.listed, seen.failed, len(f.starts)-before, seen.text)
				}
			})
		}
	}
}

// The unresolved registry is bounded: after the node's own start budget expires, an ordinary same-shaped late Agent
// is no longer held back by an old unknown start.
func TestTaskSpawnUnresolvedIdentityExpires(t *testing.T) {
	s, f, _, h := spawnWindowFixture(t, context.DeadlineExceeded)
	h.others = nil
	h.duringStart = func(child proto.Session) {
		h.f.mu.Lock()
		h.f.sessions["box"] = slices.DeleteFunc(h.f.sessions["box"], func(row proto.Session) bool { return row.SessionID == child.SessionID })
		h.f.mu.Unlock()
	}
	if _, err := s.taskSpawn(t.Context(), "box", "s1", spawnPromptArgs("Only inspect one bounded assertion; edit nothing and return its result.")); err == nil {
		t.Fatal("unknown start unexpectedly succeeded")
	}
	s.spawns.mu.Lock()
	for identity := range s.spawns.unresolved {
		identity.until = time.Now().Add(-time.Second)
	}
	s.spawns.mu.Unlock()
	ordinary := proto.Session{MachineID: "box", SessionID: "ordinary-after-expiry", SpaceName: "app", Cwd: "/code/app",
		Launcher: "codex", Agent: "codex", PermissionMode: "plan", Status: proto.StatusWorking, StartedByPersona: true}
	if err := spawnWindowLinkForRace(t.Context(), h, ordinary); err != nil {
		t.Fatal(err)
	}
	before := len(f.starts)
	seen := reached(t, "ordinary after expiry", spawnWindowNode(h, ordinary.SessionID, "Only inspect one bounded assertion; edit nothing and return its result."))
	if !seen.listed || seen.failed {
		t.Fatalf("expired unresolved identity still refused an ordinary Agent: %+v", seen)
	}
	if len(f.starts) != before+1 {
		t.Fatalf("ordinary Agent after expiry did not start exactly one child: before=%d after=%d", before, len(f.starts))
	}
}
