package node

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/proto"
)

// A work agent (pi, the Cursor CLI, the Antigravity CLI) starts in a herdr pane with the model the start names, and
// herdr is told which agent it is. The first message is not an argument of the command.
func TestWorkAgentStartTypesTheModel(t *testing.T) {
	for _, tc := range []struct{ kind, program, model, want string }{
		{piAgent, "pi", "ag-chat/cursor/grok-4.7", "HERDR_AGENT=pi pi --model ag-chat/cursor/grok-4.7"},
		{cursorAgent, "cursor-agent", "cursor/grok-4.7", "HERDR_AGENT=cursor cursor-agent --model cursor/grok-4.7"},
		{agyAgent, "agy", "", "HERDR_AGENT=agy agy"},
		{piAgent, "pi", "", "HERDR_AGENT=pi pi"},
	} {
		t.Run(tc.kind+" "+tc.model, func(t *testing.T) {
			n, f, home := herdrNode(t)
			f.reply("session.snapshot", snapshot(nil, nil, nil, nil))
			f.reply("tab.create", map[string]any{"tab": map[string]any{"tab_id": "w1:t2"}, "root_pane": map[string]any{"pane_id": "w1:p2"}})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f.after(func(c herdrCall) {
				// newTab marks the pane with the persona token first; stop on the command that runs the agent.
				if c.Method == "pane.send_input" && strings.Contains(c.String(), "HERDR_AGENT=") {
					cancel()
				}
			})
			_, _ = n.startWorkAgent(ctx, n.hosts.Get(host.Herdr).(*herdrHost), home, tc.kind, tc.kind, tc.model, "do the thing", "w1", "")
			want := call("pane.send_input", map[string]any{"pane_id": "w1:p2", "keys": []string{"Enter"}, "text": inAgentEnv(n.dir, tc.want, shellQuote)})
			var got []string
			for _, c := range f.trace("pane.send_input") {
				if strings.Contains(c, "HERDR_AGENT=") {
					got = append(got, c)
				}
			}
			if len(got) != 1 || got[0] != want {
				t.Errorf("typed %v, want %s", got, want)
			}
		})
	}
}

// Abandoning after the start's context was cancelled still quits the work agent under its pane-derived session id,
// without closing the terminal it leaves for the user.
func TestAbandonStartAfterCancelQuitsAgentAndKeepsTheTab(t *testing.T) {
	for _, kind := range []string{piAgent, cursorAgent, agyAgent} {
		t.Run(kind, func(t *testing.T) {
			n, f, home := herdrNode(t)
			f.reply("session.snapshot", snapshot(nil, nil, nil, nil))
			f.reply("tab.create", map[string]any{"tab": map[string]any{"tab_id": "w1:t2"}, "root_pane": map[string]any{"pane_id": "w1:p2"}})
			created, err := n.newTab(context.Background(), n.hosts.Get(host.Herdr).(*herdrHost), home, "w1", "", "")
			if err != nil {
				t.Fatal(err)
			}
			id := otherSession(host.Agent{Agent: kind, PaneID: "w1:p2"})
			running := agentOf(kind, "", proto.StatusIdle, "w1:p2", "w1:t2", "w1", home, kind)
			f.reply("session.snapshot", snapshot(nil, nil, []any{running}, nil))
			quitsOn(f, "w1:p2", id, nil, shellPane("w1:p2", "w1:t2", "w1", home, id))
			withStop(t, 1, time.Millisecond)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if err := n.abandonStart(ctx, created, kind, ctx.Err()); err != context.Canceled {
				t.Fatalf("abandonStart: %v", err)
			}
			assertTrace(t, f.trace("pane.send_keys"), []string{
				sendKeys("w1:p2", "ctrl+c"), sendKeys("w1:p2", "ctrl+c"),
			})
			closesNothing(t, f)
		})
	}
}

// The first message of a cursor or agy start goes through herdr's agent.prompt once herdr reports the agent, and the
// session is bound to the pane with the launcher and model it was started with.
func TestWorkAgentStartPromptsThroughHerdr(t *testing.T) {
	n, f, home := herdrNode(t)
	f.reply("session.snapshot", snapshot(nil, nil, nil, nil))
	f.reply("tab.create", map[string]any{"tab": map[string]any{"tab_id": "w1:t2"}, "root_pane": map[string]any{"pane_id": "w1:p2"}})
	f.reply("session.snapshot", snapshot(nil, nil, []any{agentOf("cursor", "", proto.StatusIdle, "w1:p2", "w1:t2", "w1", home, "cursor")}, nil))
	f.reply("agent.prompt", map[string]any{})
	s, err := n.startWorkAgent(context.Background(), n.hosts.Get(host.Herdr).(*herdrHost), home, "cursor", cursorAgent,
		"cursor/grok-4.7", "do the thing", "w1", "")
	if err != nil {
		t.Fatal(err)
	}
	if s.Agent != cursorAgent || s.PaneID != "w1:p2" || s.Launcher != "cursor" || !s.StartedByPersona {
		t.Errorf("session agent %q pane %q launcher %q started %v", s.Agent, s.PaneID, s.Launcher, s.StartedByPersona)
	}
	got := f.trace("agent.prompt")
	if len(got) != 1 || !strings.Contains(got[0], "do the thing") {
		t.Errorf("prompted %v", got)
	}
	for _, c := range f.trace("pane.send_input") {
		if strings.Contains(c, "do the thing") {
			t.Errorf("the first message was typed: %s", c)
		}
	}
}

// A pi whose extension never lists the session is quit and keeps its tab; the first message is not typed into the pane.
func TestPiStartWithoutExtensionQuitsAgentAndKeepsTheTab(t *testing.T) {
	n, f, home := herdrNode(t)
	old := piResumeWait
	piResumeWait = 300 * time.Millisecond
	t.Cleanup(func() { piResumeWait = old })
	withStop(t, 1, time.Millisecond)
	f.reply("session.snapshot", snapshot(nil, nil, nil, nil))
	f.reply("tab.create", map[string]any{"tab": map[string]any{"tab_id": "w1:t2"}, "root_pane": map[string]any{"pane_id": "w1:p2"}})
	f.reply("pane.read", map[string]any{"text": ""})
	const id = "pi-session"
	running := agentOf(piAgent, id, proto.StatusIdle, "w1:p2", "w1:t2", "w1", home, piAgent)
	quitsOn(f, "w1:p2", id, nil, shellPane("w1:p2", "w1:t2", "w1", home, id))
	f.after(func(c herdrCall) {
		if c.Method == "pane.send_input" && strings.Contains(c.String(), "HERDR_AGENT=pi") {
			f.reply("session.snapshot", snapshot(nil, nil, []any{running}, nil))
		}
	})
	_, err := n.startWorkAgent(context.Background(), n.hosts.Get(host.Herdr).(*herdrHost), home, piAgent, piAgent, "", "hi", "w1", "")
	if err == nil || !strings.Contains(err.Error(), "pi_no_extension") && !strings.Contains(err.Error(), "没有 Persona 扩展") {
		t.Fatalf("start: %v", err)
	}
	assertTrace(t, f.trace("pane.send_keys"), []string{
		sendKeys("w1:p2", "ctrl+c"), sendKeys("w1:p2", "ctrl+c"),
	})
	closesNothing(t, f)
	for _, c := range f.trace("pane.send_input", "agent.prompt") {
		if strings.Contains(c, "hi") {
			t.Errorf("the message went in: %s", c)
		}
	}
}

// session.start with launcher pi and a model reaches the work-agent start, not the Claude one.
func TestSessionStartPiUsesTheWorkAgent(t *testing.T) {
	n, f, home := herdrNode(t)
	f.reply("session.snapshot", snapshot(nil, nil, nil, nil))
	f.reply("tab.create", map[string]any{"tab": map[string]any{"tab_id": "w1:t2"}, "root_pane": map[string]any{"pane_id": "w1:p2"}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.after(func(c herdrCall) {
		if c.Method == "pane.send_input" && strings.Contains(c.String(), "HERDR_AGENT=") {
			cancel()
		}
	})
	_, err := n.start(ctx, home, piAgent, "fix it", "", "w1", "", "", "", "", "", modelExecution{Model: "ag-chat/cursor/grok-4.7"})
	// The hook cancels mid-call. The in-flight herdr read then ends either as "context canceled" or, when the
	// cancellation wins the race and expires the connection's deadline first, as "i/o timeout". Both are this test
	// stopping the start on purpose; anything else is a real failure. The trace below checks the command landed.
	if err != nil && !strings.Contains(err.Error(), "context canceled") && !strings.Contains(err.Error(), "i/o timeout") {
		t.Fatal(err)
	}
	typed := f.trace("pane.send_input")
	if len(typed) != 1 || !strings.Contains(typed[0], "HERDR_AGENT=pi pi --model ag-chat/cursor/grok-4.7") || strings.Contains(typed[0], "--session-id") {
		t.Errorf("typed %v", typed)
	}
}
