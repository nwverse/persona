package node

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// An assistant conversation's permission card names what "Always allow in this session" grants, for the hub to keep past the process
// (#2202); an Agents session's card names nothing (its rule stays Claude's own, as before).
func TestPermissionHookAlwaysKeys(t *testing.T) {
	n := codexTestNode(t)
	fh := withFakeHub(t, n)
	fh.mu.Lock()
	fh.answer = func(proto.Interaction) proto.Resolution {
		yes := true
		return proto.Resolution{Allow: &yes, Always: true}
	}
	fh.mu.Unlock()
	const assistant, agent = "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"
	n.mu.Lock()
	n.procs[assistant] = &assistantProc{id: assistant, agent: "claude", exited: make(chan struct{})}
	n.mu.Unlock()
	hook := func(session string) any {
		t.Helper()
		raw, _ := json.Marshal(hookInput{HookEventName: "PermissionRequest", SessionID: session, ToolName: "mcp__computer__click",
			ToolUseID: "toolu_" + session[:4], ToolInput: json.RawMessage(`{"x":1}`)})
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		out, err := n.handleHook(ctx, sockRequest{Input: raw, Attempt: "a1"})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	for _, c := range []struct {
		session string
		keys    []string
	}{{assistant, []string{"tool:mcp__computer__click"}}, {agent, nil}} {
		out := hook(c.session)
		ix := fh.interactions()
		if got := ix[len(ix)-1]; got.SessionID != c.session || !slices.Equal(got.AlwaysKeys, c.keys) {
			t.Fatalf("%s: always keys %q, want %q", c.session[:4], got.AlwaysKeys, c.keys)
		}
		b, _ := json.Marshal(out)
		hasRule := strings.Contains(string(b), `"type":"addRules"`)
		if hasRule != (c.session == agent) {
			t.Fatalf("%s: session rule present = %v: %s", c.session[:4], hasRule, b)
		}
	}
}

// A gated write's card names its routes, ids blanked: the hub keeps them when this node's own memory of them is gone.
func TestLarkGateAlwaysKeys(t *testing.T) {
	n := codexTestNode(t)
	fh := withFakeHub(t, n)
	n.mu.Lock()
	n.larkCfg, n.larkCfgAt = olCfg(), time.Now().Add(time.Hour)
	n.mu.Unlock()
	fh.mu.Lock()
	fh.answer = func(proto.Interaction) proto.Resolution {
		yes := true
		return proto.Resolution{Allow: &yes, Always: true}
	}
	fh.mu.Unlock()
	cwd := t.TempDir()
	cmd := `ex POST /conversations/7123/messages '{"text":"hi"}' && ex POST /conversations/8456/messages '{"text":"yo"}'`
	if d, _, err := n.larkCheck(context.Background(), "s1", cwd, cmd, proto.Interaction{SessionID: "s1", ExpiresAt: time.Now().Add(time.Minute)}); err != nil || d != "allow" {
		t.Fatalf("larkCheck: %q %v", d, err)
	}
	ix := fh.interactions()
	if len(ix) != 1 || !slices.Equal(ix[0].AlwaysKeys, []string{"lark:POST /conversations/*/messages"}) {
		t.Fatalf("cards %d, always keys %q", len(ix), ix[len(ix)-1].AlwaysKeys)
	}
	// This node remembers too: the same route is not asked about again while it runs.
	if d, _, _ := n.larkCheck(context.Background(), "s1", cwd, `ex POST /conversations/9/messages '{"text":"x"}'`, proto.Interaction{SessionID: "s1", ExpiresAt: time.Now().Add(time.Minute)}); d != "allow" || len(fh.interactions()) != 1 {
		t.Fatalf("remembered route asked again: %q, %d cards", d, len(fh.interactions()))
	}
}

// A health conversation's Feishu writes are confirmed each time (docs/assistant/health.md "Outbound"): a rule that allows the
// route still asks, the card offers no "Always allow in this session", and a yes with always is not remembered. Reads still go.
func TestLarkGateHealthAsksEachTime(t *testing.T) {
	n := codexTestNode(t)
	fh := withFakeHub(t, n)
	n.mu.Lock()
	n.larkCfg, n.larkCfgAt = olCfg(proto.LarkRule{Method: "POST", Path: "/conversations/*/messages", Action: "allow"}), time.Now().Add(time.Hour)
	n.mu.Unlock()
	fh.mu.Lock()
	fh.answer = func(proto.Interaction) proto.Resolution {
		yes := true
		return proto.Resolution{Allow: &yes, Always: true}
	}
	fh.mu.Unlock()
	health := filepath.Join(t.TempDir(), ".persona", "health")
	if err := os.MkdirAll(health, 0o755); err != nil {
		t.Fatal(err)
	}
	write := `ex POST /conversations/7123/messages '{"text":"这周的体检结果"}'`
	ix := proto.Interaction{SessionID: "h1", ExpiresAt: time.Now().Add(time.Minute)}
	for i := 1; i <= 2; i++ {
		if d, _, err := n.larkCheck(context.Background(), "h1", health, write, ix); err != nil || d != "allow" {
			t.Fatalf("write %d: %q %v", i, d, err)
		}
		cards := fh.interactions()
		if len(cards) != i || len(cards[i-1].AlwaysKeys) != 0 {
			t.Fatalf("write %d: %d cards, always keys %q", i, len(cards), cards[len(cards)-1].AlwaysKeys)
		}
	}
	if d, _, _ := n.larkCheck(context.Background(), "h1", health, `ex GET /conversations/7123/messages`, ix); d != "allow" || len(fh.interactions()) != 2 {
		t.Fatalf("a read was asked about: %q, %d cards", d, len(fh.interactions()))
	}
	// Elsewhere the allow rule still lets the same write through unasked.
	if d, _, _ := n.larkCheck(context.Background(), "s1", t.TempDir(), write, proto.Interaction{SessionID: "s1", ExpiresAt: time.Now().Add(time.Minute)}); d != "allow" || len(fh.interactions()) != 2 {
		t.Fatalf("the assistant's own allowed write: %q, %d cards", d, len(fh.interactions()))
	}
}

// File grants retain their directory keys, but no process rule bypasses the hub after revocation.
// Terminal Agents still keep their own whole-tool session rules.
func TestPermissionHookAlwaysFileScope(t *testing.T) {
	n := codexTestNode(t)
	fh := withFakeHub(t, n)
	fh.mu.Lock()
	fh.answer = func(proto.Interaction) proto.Resolution {
		yes := true
		return proto.Resolution{Allow: &yes, Always: true}
	}
	fh.mu.Unlock()
	const assistant, agent = "33333333-3333-4333-8333-333333333333", "44444444-4444-4444-8444-444444444444"
	n.mu.Lock()
	n.procs[assistant] = &assistantProc{id: assistant, agent: "claude", exited: make(chan struct{})}
	n.mu.Unlock()
	call := func(session, tool string) string {
		t.Helper()
		raw, _ := json.Marshal(hookInput{HookEventName: "PermissionRequest", SessionID: session, ToolName: tool,
			ToolUseID: "toolu_" + session[:4] + tool, ToolInput: json.RawMessage(`{"file_path":"/home/me/work/report.md"}`)})
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		out, err := n.handleHook(ctx, sockRequest{Input: raw, Attempt: "a1"})
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(out)
		return string(b)
	}
	read := func(session string) string { return call(session, "Read") }
	out := read(assistant)
	ix := fh.interactions()
	if got := ix[len(ix)-1].AlwaysKeys; !slices.Equal(got, []string{"tool:Read(//home/me/work/**)"}) {
		t.Fatalf("assistant's always keys %q", got)
	}
	if strings.Contains(out, "updatedPermissions") {
		t.Fatalf("assistant received a cached session rule: %s", out)
	}
	if out := read(agent); strings.Contains(out, "ruleContent") || !strings.Contains(out, `"toolName":"Read"`) {
		t.Fatalf("an Agents session's rule changed: %s", out)
	}
	// A write's rule is an Edit rule: Claude Code never matches Write(path).
	out = call(assistant, "Write")
	ix = fh.interactions()
	if got := ix[len(ix)-1].AlwaysKeys; !slices.Equal(got, []string{"tool:Edit(//home/me/work/**)"}) {
		t.Fatalf("a write's always keys %q", got)
	}
	if strings.Contains(out, "updatedPermissions") {
		t.Fatalf("a write received a cached session rule: %s", out)
	}
}
