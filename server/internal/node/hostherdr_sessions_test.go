package node

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/herdr"
	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/nodeclient"
	"github.com/nwverse/persona/server/internal/proto"
)

// Named herdr sessions (#2196): the herdr host drives every running named session next to its own server, with the
// named sessions' ids qualified ("@<session>:<id>"), and sends each call to the session its id names.

// namedFakes are the fake servers of namedHerdr: the own one, the two running named sessions it drives, and the
// sessions it must leave alone (stopped, another node's data directory's, a name that cannot stand in an id), on
// fakes that must never be called.
type namedFakes struct {
	own, build, review *fakeHerdr
	untouched          []*fakeHerdr
}

// namedHerdr is a herdr host on own (a new fake when nil) whose account also runs the named sessions of namedFakes.
func namedHerdr(t *testing.T, own *fakeHerdr) (*herdrHost, namedFakes) {
	t.Helper()
	if own == nil {
		own = newFakeHerdr(t)
	}
	fs := namedFakes{own: own, build: newFakeHerdr(t), review: newFakeHerdr(t)}
	stopped, other, bad := newFakeHerdr(t), newFakeHerdr(t), newFakeHerdr(t)
	fs.untouched = []*fakeHerdr{stopped, other, bad}
	infos := []herdr.SessionInfo{
		{Name: "default", Default: true, Running: true, Socket: own.sock},
		{Name: "build", Running: true, Socket: fs.build.sock},
		{Name: "review", Running: true, Socket: fs.review.sock},
		{Name: "old", Socket: stopped.sock},
		{Name: "persona-1a2b3c4d", Running: true, Socket: other.sock},
		{Name: "a b", Running: true, Socket: bad.sock},
	}
	return namedHost(own.sock, infos), fs
}

// namedHost is a herdr host on the server at sock whose `herdr session list` says infos.
func namedHost(sock string, infos []herdr.SessionInfo) *herdrHost {
	c := &herdr.Client{Socket: sock}
	return &herdrHost{c: c, named: &herdrSessions{own: c, list: func(context.Context) ([]herdr.SessionInfo, error) {
		return infos, nil
	}}}
}

func (fs namedFakes) assertUntouched(t *testing.T) {
	t.Helper()
	for _, f := range fs.untouched {
		if calls := f.calls(); len(calls) != 0 {
			t.Errorf("a session that must be left alone was called: %v", calls)
		}
	}
}

// sessionSnapshot is a snapshot of one server with workspace w4 ("app"), its tab w4:t1 and a claude in w4:p1 — the
// same ids in every session — whose agent session is id.
func sessionSnapshot(id string) map[string]any {
	snap := snapshot(
		[]any{map[string]any{"workspace_id": "w4", "label": "app", "number": 1}},
		[]any{map[string]any{"tab_id": "w4:t1", "workspace_id": "w4", "label": "main", "number": 1}},
		[]any{claudeAgent(id, proto.StatusIdle, "w4:p1", "w4:t1", "w4", "/tmp/app", id)},
		nil)
	rect := map[string]any{"x": 0, "y": 0, "width": 80, "height": 24}
	snap["snapshot"].(map[string]any)["layouts"] = []any{map[string]any{"tab_id": "w4:t1", "workspace_id": "w4",
		"focused_pane_id": "w4:p1", "splits": []any{}, "panes": []any{map[string]any{"pane_id": "w4:p1", "rect": rect}}}}
	return snap
}

func TestHerdrHostListsEveryRunningSession(t *testing.T) {
	h, fs := namedHerdr(t, nil)
	fs.own.reply("session.snapshot", sessionSnapshot(sidA))
	fs.build.reply("session.snapshot", sessionSnapshot(sidB))
	fs.review.reply("session.snapshot", sessionSnapshot(sidC))
	l, err := h.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var agents []string
	for _, a := range l.Agents {
		agents = append(agents, a.SessionID()+" "+a.PaneID+" "+a.TabID+" "+a.WorkspaceID)
	}
	want := []string{
		sidA + " w4:p1 w4:t1 w4",
		sidB + " @build:w4:p1 @build:w4:t1 @build:w4",
		sidC + " @review:w4:p1 @review:w4:t1 @review:w4",
	}
	if !slices.Equal(agents, want) {
		t.Errorf("agents\n%s\nwant\n%s", strings.Join(agents, "\n"), strings.Join(want, "\n"))
	}
	var spaces, slots []string
	for _, s := range l.Spaces {
		spaces = append(spaces, s.ID+"="+s.Label)
	}
	for _, s := range l.Slots {
		slots = append(slots, s.ID+" in "+s.SpaceID)
	}
	if want := []string{"w4=app", "@build:w4=build · app", "@review:w4=review · app"}; !slices.Equal(spaces, want) {
		t.Errorf("spaces %v, want %v", spaces, want)
	}
	if want := []string{"w4:t1 in w4", "@build:w4:t1 in @build:w4", "@review:w4:t1 in @review:w4"}; !slices.Equal(slots, want) {
		t.Errorf("slots %v, want %v", slots, want)
	}
	for _, pane := range []string{"w4:p1", "@build:w4:p1", "@review:w4:p1"} {
		if i, ok := l.PaneIndex[pane]; !ok || i != 0 {
			t.Errorf("pane index of %s: %d %v", pane, i, ok)
		}
	}
	fs.assertUntouched(t)

	// The lighter listings cover the same sessions.
	for _, f := range []*fakeHerdr{fs.own, fs.build, fs.review} {
		f.reply("agent.list", map[string]any{"agents": []any{claudeAgent("", proto.StatusIdle, "w4:p1", "w4:t1", "w4", "/tmp/app", "")}})
		f.reply("workspace.list", map[string]any{"workspaces": []any{map[string]any{"workspace_id": "w4", "label": "app", "number": 1}}})
	}
	as, err := h.Agents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var panes []string
	for _, a := range as {
		panes = append(panes, a.PaneID)
	}
	if want := []string{"w4:p1", "@build:w4:p1", "@review:w4:p1"}; !slices.Equal(panes, want) {
		t.Errorf("agents %v, want %v", panes, want)
	}
	ss, err := h.Spaces(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	spaces = nil
	for _, s := range ss {
		spaces = append(spaces, s.ID+"="+s.Label)
	}
	if want := []string{"w4=app", "@build:w4=build · app", "@review:w4=review · app"}; !slices.Equal(spaces, want) {
		t.Errorf("spaces %v, want %v", spaces, want)
	}
	fs.assertUntouched(t)
}

// A host that drives only its own server (a node on a session of its own, the tests' plain hosts) lists and routes
// as it always did: no session is asked about, ids stay herdr's.
func TestHerdrHostWithoutNamedSessionsIsUnchanged(t *testing.T) {
	f := newFakeHerdr(t)
	h := &herdrHost{c: &herdr.Client{Socket: f.sock}}
	f.reply("session.snapshot", sessionSnapshot(sidA))
	l, err := h.Snapshot(context.Background())
	if err != nil || len(l.Agents) != 1 || l.Agents[0].PaneID != "w4:p1" || l.Spaces[0].Label != "app" {
		t.Fatalf("snapshot %+v, %v", l, err)
	}
	if _, err := h.Read(context.Background(), host.PaneRef{Kind: host.Herdr, ID: "@build:w4:p1"}, host.ReadOpts{Source: "visible"}); !errors.Is(err, errNoSuchSession) {
		t.Errorf("a pane of a session this host does not drive: %v", err)
	}
	if tr := f.trace("pane.read"); len(tr) != 0 {
		t.Errorf("read on the own server: %v", tr)
	}
}

// Every call on a pane, tab or workspace goes to the session its id names, with herdr's own id there; ids that come
// back are qualified again. The own server's ids go to the own server, as before.
func TestHerdrHostRoutesEachCallToItsSession(t *testing.T) {
	ctx := context.Background()
	h, fs := namedHerdr(t, nil)
	for _, f := range []*fakeHerdr{fs.own, fs.build, fs.review} {
		f.reply("agent.get", map[string]any{"agent": claudeAgent(sidA, proto.StatusIdle, "w4:p1", "w4:t1", "w4", "/tmp/app", "")})
		f.reply("pane.split", map[string]any{"pane": map[string]any{"pane_id": "w4:p2"}})
		f.reply("tab.create", map[string]any{"tab": map[string]any{"tab_id": "w4:t2"}, "root_pane": map[string]any{"pane_id": "w4:p3"}})
		f.reply("pane.list", map[string]any{"panes": []any{map[string]any{"pane_id": "w4:p1", "tab_id": "w4:t1"}}})
		f.setScreen("w4:p1", "screen of "+f.sock)
	}
	drive := func(session string) {
		t.Helper()
		q := func(id string) string { return herdr.QualifyID(session, id) }
		pane := host.PaneRef{Kind: host.Herdr, ID: q("w4:p1")}
		if text, err := h.Read(ctx, pane, host.ReadOpts{Source: "visible", Lines: 10}); err != nil || text == "" {
			t.Errorf("read %q: %q %v", pane.ID, text, err)
		}
		must := func(what string, err error) {
			t.Helper()
			if err != nil {
				t.Errorf("%s on %s: %v", what, pane.ID, err)
			}
		}
		must("send text", h.SendText(ctx, pane, "hi"))
		must("send keys", h.SendKeys(ctx, pane, "esc"))
		must("prompt", h.Prompt(ctx, pane, "hello"))
		a, err := h.Agent(ctx, pane)
		if err != nil || a.PaneID != q("w4:p1") || a.TabID != q("w4:t1") || a.WorkspaceID != q("w4") {
			t.Errorf("agent of %s: %+v %v", pane.ID, a, err)
		}
		_, err = h.PaneProcesses(ctx, pane)
		must("processes", err)
		must("name", h.NameAgent(ctx, pane, "n"))
		must("run", h.Run(ctx, pane, "ls"))
		split, err := h.Split(ctx, pane, "right", "")
		if err != nil || split.ID != q("w4:p2") {
			t.Errorf("split of %s: %+v %v", pane.ID, split, err)
		}
		created, err := h.CreateSlot(ctx, q("w4"), "/tmp/app", "new")
		if err != nil || created.SlotID != q("w4:t2") || created.Pane.ID != q("w4:p3") {
			t.Errorf("new tab in %s: %+v %v", q("w4"), created, err)
		}
		must("rename tab", h.RenameSlot(ctx, q("w4:t1"), "tab"))
		must("rename space", h.RenameSpace(ctx, q("w4"), sessionLabel(session, "renamed")))
		must("close", h.Close(ctx, pane, q("w4"), q("w4:t1")))
		must("close tab", h.CloseSlot(ctx, q("w4:t1")))
		must("resume", h.PaneReportResume(ctx, pane, resumeSource, "claude", sidA, []string{"claude", "--resume", sidA}))
		must("focus", h.FocusPane(ctx, pane))
		must("metadata", h.ReportMetadata(ctx, pane, "persona", map[string]*string{}))
	}
	// Each call's ids as herdr got them: never qualified, and the session's own.
	idsOf := func(f *fakeHerdr) []string {
		var out []string
		for _, c := range f.calls() {
			for _, k := range []string{"pane_id", "target", "target_pane_id", "tab_id", "workspace_id", "label"} {
				if v, ok := c.Params[k].(string); ok {
					out = append(out, c.Method+" "+k+"="+v)
				}
			}
		}
		return out
	}
	wantIDs := []string{
		"pane.read pane_id=w4:p1", "pane.send_text pane_id=w4:p1", "pane.send_keys pane_id=w4:p1", "agent.prompt target=w4:p1",
		"agent.get target=w4:p1", "pane.process_info pane_id=w4:p1", "agent.rename target=w4:p1", "pane.send_input pane_id=w4:p1",
		"pane.split target_pane_id=w4:p1", "tab.create workspace_id=w4", "tab.create label=new", "tab.rename tab_id=w4:t1",
		"tab.rename label=tab", "workspace.rename workspace_id=w4", "workspace.rename label=renamed", "pane.list workspace_id=w4",
		"tab.close tab_id=w4:t1", "tab.close tab_id=w4:t1", "pane.report_agent pane_id=w4:p1",
		"pane.report_agent_session pane_id=w4:p1", "pane.focus pane_id=w4:p1", "pane.report_metadata pane_id=w4:p1",
	}

	drive("build")
	if got := idsOf(fs.build); !slices.Equal(got, wantIDs) {
		t.Errorf("build got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(wantIDs, "\n"))
	}
	for _, f := range []*fakeHerdr{fs.own, fs.review} {
		if calls := f.calls(); len(calls) != 0 {
			t.Errorf("a call for build went to another server: %v", calls)
		}
	}
	fs.build.reset()

	drive("")
	if got := idsOf(fs.own); !slices.Equal(got, wantIDs) {
		t.Errorf("own server got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(wantIDs, "\n"))
	}
	for _, f := range []*fakeHerdr{fs.build, fs.review} {
		if calls := f.calls(); len(calls) != 0 {
			t.Errorf("a call for the own server went to a named session: %v", calls)
		}
	}
	fs.own.reset()

	// A pane and a tab of different sessions, or a session that is not running, reach no server at all.
	if err := h.Close(ctx, host.PaneRef{Kind: host.Herdr, ID: "@build:w4:p1"}, "w4", "w4:t1"); err == nil {
		t.Error("closing a build pane with the own server's tab: want an error")
	}
	for _, id := range []string{"@old:w4:p1", "@persona-1a2b3c4d:w4:p1", "@nope:w4:p1"} {
		if err := h.SendKeys(ctx, host.PaneRef{Kind: host.Herdr, ID: id}, "esc"); !errors.Is(err, errNoSuchSession) {
			t.Errorf("keys to %s: %v, want errNoSuchSession", id, err)
		}
	}
	for _, f := range []*fakeHerdr{fs.own, fs.build, fs.review} {
		if calls := f.calls(); len(calls) != 0 {
			t.Errorf("refused calls reached a server: %v", calls)
		}
	}
	fs.assertUntouched(t)
}

// A named session that does not answer does not hold up the own server's list: its agents come from its last answer
// meanwhile, and it is not asked again while a call to it is out.
func TestNamedSessionThatHangsHoldsNothingUp(t *testing.T) {
	old := namedWait
	namedWait = 200 * time.Millisecond
	t.Cleanup(func() { namedWait = old })
	h, fs := namedHerdr(t, nil)
	fs.own.reply("session.snapshot", sessionSnapshot(sidA))
	fs.review.reply("session.snapshot", sessionSnapshot(sidC))
	fs.build.reply("session.snapshot", sessionSnapshot(sidB))
	panes := func(l *host.Layout) []string {
		var out []string
		for _, a := range l.Agents {
			out = append(out, a.PaneID)
		}
		return out
	}
	l, err := h.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := panes(l); !slices.Equal(got, []string{"w4:p1", "@build:w4:p1", "@review:w4:p1"}) {
		t.Fatalf("panes %v", got)
	}
	var calls sync.WaitGroup
	calls.Add(1)
	fs.build.handle("session.snapshot", func(map[string]any) any {
		defer calls.Done()
		time.Sleep(time.Second) // a hung server, as far as the listing is concerned
		return sessionSnapshot(sidB)
	})
	for i := range 3 { // the second and third find the first call still out and do not ask again
		start := time.Now()
		l, err := h.Snapshot(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if took := time.Since(start); took > 700*time.Millisecond {
			t.Errorf("listing %d took %s: the hung session held it up", i, took)
		}
		if got := panes(l); !slices.Equal(got, []string{"w4:p1", "@build:w4:p1", "@review:w4:p1"}) {
			t.Errorf("listing %d: panes %v, want build's from its last answer", i, got)
		}
	}
	calls.Wait()
	if n := len(fs.build.trace("session.snapshot")); n != 2 {
		t.Errorf("build asked %d times, want 2 (once before it hung, once while it hung)", n)
	}
}

// Watch opens one stream per server, each subscribing to its own panes by herdr's ids; an event of a named session
// counts as one of the host's.
func TestHerdrHostWatchesEverySession(t *testing.T) {
	own, bld := newEventsFake(t), newEventsFake(t)
	h := namedHost(own.sock, []herdr.SessionInfo{
		{Name: "default", Default: true, Running: true, Socket: own.sock},
		{Name: "build", Running: true, Socket: bld.sock},
	})
	ctx, cancel := context.WithCancel(context.Background())
	urgent := make(chan bool, 16)
	done := make(chan error, 1)
	go func() {
		done <- h.Watch(ctx, []host.PaneRef{{Kind: host.Herdr, ID: "w4:p1"}, {Kind: host.Herdr, ID: "@build:w4:p1"},
			{Kind: host.Herdr, ID: "@build:w5:p1"}}, func(u bool) { urgent <- u })
	}()
	statusPanes := func(subs []any) []string {
		var out []string
		for _, s := range subs {
			m, _ := s.(map[string]any)
			if m["type"] == "pane.agent_status_changed" {
				out = append(out, str(m["pane_id"]))
			}
		}
		return out
	}
	for _, c := range []struct {
		f    *eventsFake
		want []string
	}{{own, []string{"w4:p1"}}, {bld, []string{"w4:p1", "w5:p1"}}} {
		select {
		case subs := <-c.f.subs:
			if got := statusPanes(subs); !slices.Equal(got, c.want) {
				t.Errorf("subscribed to %v, want %v", got, c.want)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("no subscription")
		}
	}
	drain := func() {
		for {
			select {
			case <-urgent:
			case <-time.After(200 * time.Millisecond):
				return
			}
		}
	}
	drain() // the streams starting
	bld.events <- `{"event":"pane.agent_status_changed","data":{"pane_id":"w4:p1","agent_status":"blocked"}}`
	select {
	case u := <-urgent:
		if !u {
			t.Error("a named session's agent starting to wait is urgent")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a named session's event did not come through")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Watch did not end with its context")
	}
}

// eventsFake is a herdr socket that takes events.subscribe and keeps the stream open, pushing what events gets.
type eventsFake struct {
	sock   string
	subs   chan []any
	events chan string
}

func newEventsFake(t *testing.T) *eventsFake {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "hev")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	f := &eventsFake{sock: filepath.Join(dir, "h.sock"), subs: make(chan []any, 8), events: make(chan string, 8)}
	ln, err := herdr.Listen(f.sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				line, err := bufio.NewReader(c).ReadBytes('\n')
				if err != nil {
					return
				}
				var req struct {
					ID     string `json:"id"`
					Params struct {
						Subscriptions []any `json:"subscriptions"`
					} `json:"params"`
				}
				_ = json.Unmarshal(line, &req)
				f.subs <- req.Params.Subscriptions
				b, _ := json.Marshal(map[string]any{"id": req.ID, "result": map[string]any{}})
				if _, err := c.Write(append(b, '\n')); err != nil {
					return
				}
				closed := make(chan struct{})
				go func() { _, _ = c.Read(make([]byte, 1)); close(closed) }()
				for {
					select {
					case e := <-f.events:
						if _, err := c.Write([]byte(e + "\n")); err != nil {
							return
						}
					case <-closed:
						return
					}
				}
			}()
		}
	}()
	return f
}

// The node lists a named session's agents as live herdr sessions — not as transcripts outside any pane — apart from
// the own server's with the same pane id, and reads and presses keys in the right one.
func TestNodeListsAndDrivesNamedSessions(t *testing.T) {
	n, f, _ := herdrNode(t)
	h, fs := namedHerdr(t, f)
	n.hosts = host.NewRegistry(h)
	f.reply("session.snapshot", sessionSnapshot(sidA))
	fs.build.reply("session.snapshot", sessionSnapshot(sidB))
	fs.review.reply("session.snapshot", snapshot(nil, nil, nil, nil))
	m := byID(listSessions(t, n))
	a, b := m[sidA], m[sidB]
	if a.PaneID != "w4:p1" || a.SpaceID != "w4" || a.SlotID != "w4:t1" || a.SpaceName != "app" {
		t.Errorf("own server's session %+v", a)
	}
	if b.PaneID != "@build:w4:p1" || b.SpaceID != "@build:w4" || b.SlotID != "@build:w4:t1" ||
		b.SpaceName != "build · app" || b.SpaceKind != host.Herdr || b.Host != host.Herdr {
		t.Errorf("named session's session %+v", b)
	}
	if b.Status != proto.StatusIdle {
		t.Errorf("named session's status %q, want herdr's", b.Status)
	}
	f.setScreen("w4:p1", "own screen")
	fs.build.setScreen("w4:p1", "build screen")
	f.reset()
	fs.build.reset()
	res, err := request(t, n, "session.screen", map[string]any{"session_id": sidB})
	if err != nil || !strings.Contains(str(res["text"]), "build screen") {
		t.Errorf("screen of the named session's agent: %v %v", res, err)
	}
	if _, err := request(t, n, "session.keys", map[string]any{"session_id": sidB, "keys": []string{"esc"}}); err != nil {
		t.Errorf("keys: %v", err)
	}
	if tr := fs.build.trace("pane.send_keys"); len(tr) != 1 || !strings.Contains(tr[0], `"pane_id":"w4:p1"`) {
		t.Errorf("build keys %v", tr)
	}
	if tr := f.trace("pane.read", "pane.send_keys"); len(tr) != 0 {
		t.Errorf("the own server's w4:p1 was driven for the named session's agent: %v", tr)
	}
	res, err = request(t, n, "session.screen", map[string]any{"session_id": sidA})
	if err != nil || !strings.Contains(str(res["text"]), "own screen") {
		t.Errorf("screen of the own server's agent: %v %v", res, err)
	}
	fs.assertUntouched(t)
}

// A process's pane from its environment: a named session's qualified, the own server's as it is — so a claude in
// build's w4:p1 is never taken for the one in the own server's w4:p1.
func TestEnginePaneRefsTellSessionsApart(t *testing.T) {
	n, _, _ := herdrNode(t)
	env := func(kv ...string) map[string]string {
		m := map[string]string{}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return m
	}
	for _, c := range []struct {
		env  map[string]string
		want string
	}{
		{env(herdrPaneEnv, "w4:p1"), "w4:p1"},
		{env(herdrPaneEnv, "w4:p1", herdrSessionEnv, "default"), "w4:p1"},
		{env(herdrPaneEnv, "w4:p1", herdrSessionEnv, "build"), "@build:w4:p1"},
		{env(herdrPaneEnv, "w4:p1", herdrSocketEnv, "/h/.config/herdr/sessions/review/herdr.sock"), "@review:w4:p1"},
	} {
		if got := n.enginePaneRefs(c.env); !slices.Equal(got, []string{c.want}) {
			t.Errorf("%v: %v, want %s", c.env, got, c.want)
		}
	}
	// A node on a session of its own writes its own panes bare, and the default server's qualified.
	n.hosts = herdrHosts(&herdr.Client{Socket: "/tmp/x/h.sock", Session: "work"})
	if got := n.enginePaneRefs(env(herdrPaneEnv, "w1:p1", herdrSessionEnv, "work")); !slices.Equal(got, []string{"w1:p1"}) {
		t.Errorf("own session's pane %v", got)
	}
	if got := n.enginePaneRefs(env(herdrPaneEnv, "w1:p1")); !slices.Equal(got, []string{"@default:w1:p1"}) {
		t.Errorf("default server's pane on a node of another session %v", got)
	}
}

// A hook in a named session names its session (nodeclient.HookPane); the node takes its own server's session off
// again, so a node on a session it was told to use binds its own panes as before.
func TestHookPanesOfNamedSessions(t *testing.T) {
	t.Setenv("PERSONA_HOME", "")
	env := func(kv ...string) func(string) string {
		return func(k string) string {
			for i := 0; i+1 < len(kv); i += 2 {
				if kv[i] == k {
					return kv[i+1]
				}
			}
			return ""
		}
	}
	for _, c := range []struct {
		env  func(string) string
		want string
	}{
		{env(herdrPaneEnv, "w4:p1"), "w4:p1"},
		{env(herdrPaneEnv, "w4:p1", herdrSessionEnv, "default"), "w4:p1"},
		{env(herdrPaneEnv, "w4:p1", herdrSessionEnv, "build", herdrSocketEnv, "/h/.config/herdr/sessions/build/herdr.sock"), "@build:w4:p1"},
		{env(herdrPaneEnv, "w4:p1", herdrSocketEnv, "/h/.config/herdr/sessions/build/herdr.sock"), "@build:w4:p1"},
	} {
		if got := nodeclient.HookPane(c.env); got != c.want {
			t.Errorf("HookPane = %q, want %q", got, c.want)
		}
	}
	// The session of this data directory's own node (a custom PERSONA_HOME) is that node's own server: bare.
	t.Setenv("PERSONA_HOME", filepath.Join(t.TempDir(), "test-node"))
	own := herdr.OwnSession()
	if own == "" {
		t.Skip("no data-directory session here")
	}
	if got := nodeclient.HookPane(env(herdrPaneEnv, "w4:p1", herdrSessionEnv, own)); got != "w4:p1" {
		t.Errorf("own data directory's session: %q", got)
	}

	n, _, _ := herdrNode(t)
	n.hosts = herdrHosts(&herdr.Client{Socket: "/tmp/x/h.sock", Session: "work"})
	for in, want := range map[string]string{"@work:w1:p1": "w1:p1", "@build:w1:p1": "@build:w1:p1", "w1:p1": "w1:p1", "": ""} {
		if got := n.ownPane(in); got != want {
			t.Errorf("ownPane(%q) = %q, want %q", in, got, want)
		}
	}
}

// A compatible CLI (or any process) found by its pid: the pane of a named session comes qualified.
func TestPaneOfPIDTellsSessionsApart(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("reads /proc")
	}
	n, _, _ := herdrNode(t)
	cmd := exec.Command("sleep", "30")
	cmd.Env = append(os.Environ(), herdrPaneEnv+"=w4:p1", herdrSessionEnv+"=build")
	if err := cmd.Start(); err != nil {
		t.Skip(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	for deadline := time.Now().Add(5 * time.Second); ; { // its environment is sleep's once it has exec'd
		if b, _ := os.ReadFile(filepath.Join("/proc", strconv.Itoa(cmd.Process.Pid), "cmdline")); strings.HasPrefix(string(b), "sleep") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("sleep did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := n.paneOfPID(cmd.Process.Pid); got != "@build:w4:p1" {
		t.Errorf("pane %q, want @build:w4:p1", got)
	}
}

// A Codex process's pane carries its herdr session too, for the registry to write it as the host does.
func TestCodexProcessKnowsItsHerdrSession(t *testing.T) {
	p, ok := codexProcessArgs(1, []string{"codex"}, "HERDR_PANE_ID=w4:p1\x00HERDR_SESSION=build\x00CODEX_HOME=/c\x00")
	if !ok || p.pane != "w4:p1" || p.herdrSession != "build" {
		t.Errorf("process %+v", p)
	}
	p, ok = codexProcessArgs(1, []string{"codex"}, "codex HERDR_PANE_ID=w4:p1 HERDR_SOCKET_PATH=/h/.config/herdr/sessions/review/herdr.sock")
	if !ok || p.pane != "w4:p1" || p.herdrSession != "review" {
		t.Errorf("ps process %+v", p)
	}
	p, ok = codexProcessArgs(1, []string{"codex"}, "HERDR_PANE_ID=w4:p1\x00HERDR_SOCKET_PATH=/h/.config/herdr/herdr.sock\x00")
	if !ok || p.pane != "w4:p1" || p.herdrSession != "" {
		t.Errorf("default server's process %+v", p)
	}
}

// The named sessions are asked about only from the default server at its usual socket.
func TestNamedSessionsOnlyBesideTheDefaultServer(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no named sessions on Windows")
	}
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	def := filepath.Join(cfg, "herdr", "herdr.sock")
	if newHerdrSessions(&herdr.Client{Bin: "herdr", Socket: def}, nil) == nil {
		t.Error("the default server drives the named sessions")
	}
	for _, c := range []*herdr.Client{
		{Bin: "herdr", Socket: filepath.Join(cfg, "herdr", "sessions", "persona-1a2b3c4d", "herdr.sock"), Session: "persona-1a2b3c4d"},
		{Bin: "herdr", Socket: "/tmp/elsewhere.sock"},
	} {
		if newHerdrSessions(c, nil) != nil {
			t.Errorf("%+v drives named sessions", c)
		}
	}
}

// A session that starts after the list was taken is found when one of its ids comes in; a stopped one is dropped.
func TestNamedSessionsFollowHerdr(t *testing.T) {
	own, bld := newFakeHerdr(t), newFakeHerdr(t)
	var mu sync.Mutex
	infos := []herdr.SessionInfo{{Name: "default", Default: true, Running: true, Socket: own.sock}}
	c := &herdr.Client{Socket: own.sock}
	h := &herdrHost{c: c, named: &herdrSessions{own: c, list: func(context.Context) ([]herdr.SessionInfo, error) {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(infos), nil
	}}}
	if got := h.named.running(context.Background()); len(got) != 0 {
		t.Fatalf("running %v", got)
	}
	mu.Lock()
	infos = append(infos, herdr.SessionInfo{Name: "build", Running: true, Socket: bld.sock})
	mu.Unlock()
	h.named.mu.Lock()
	h.named.at = time.Now().Add(-2 * time.Second) // the last look is over a second old
	h.named.mu.Unlock()
	if err := h.SendKeys(context.Background(), host.PaneRef{Kind: host.Herdr, ID: "@build:w1:p1"}, "esc"); err != nil {
		t.Fatalf("keys to a session started since: %v", err)
	}
	if tr := bld.trace("pane.send_keys"); len(tr) != 1 {
		t.Errorf("build keys %v", tr)
	}
	mu.Lock()
	infos[1].Running = false
	mu.Unlock()
	h.named.mu.Lock()
	h.named.sessions["build"].c.Socket = bld.sock
	h.named.mu.Unlock()
	h.named.refresh(context.Background())
	if got := h.named.running(context.Background()); len(got) != 0 {
		t.Errorf("a stopped session is still driven: %v", got)
	}
}
