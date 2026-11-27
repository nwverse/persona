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

func agyKeepAlwaysHello() map[string]any {
	return map[string]any{
		"runtime":  "antigravity",
		"name":     "Antigravity",
		"agent":    "antigravity",
		"launcher": "antigravity",
		"caps": map[string]any{
			"start":                 true,
			"send":                  true,
			"interrupt":             true,
			"assistant":             true,
			"assistant_environment": true,
			"keep_always":           true,
		},
	}
}

func startedAGYAssistant(t *testing.T, n *Node, host *fakeRuntime) string {
	t.Helper()
	const sid = "antigravity-a1"
	host.answer = func(method string, _ json.RawMessage) (any, error) {
		if method == "session.start" {
			return map[string]any{"session": map[string]any{
				"session_id": sid, "cwd": filepath.Join(n.home, ".persona", "assistant"), "status": "idle",
			}}, nil
		}
		return map[string]any{"ok": true}, nil
	}
	raw, err := nodeCall(t, n, "assistant.send", map[string]any{
		"launcher": "antigravity", "cwd": filepath.Join(n.home, ".persona", "assistant"), "text": "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		SessionID string `json:"session_id"`
	}
	_ = json.Unmarshal(raw, &res)
	if res.SessionID != sid {
		t.Fatalf("assistant.send %s", raw)
	}
	return res.SessionID
}

func TestRuntimeAssistantAlwaysKeysScoping(t *testing.T) {
	n, hub := runtimeNode(t)
	t.Cleanup(func() { n.gates().close() })
	assistantHub(hub)
	_ = prepareRuntimeAssistantEnvironment(t, n, true)
	host := dialRuntime(t, n, agyKeepAlwaysHello())
	sid := startedAGYAssistant(t, n, host)
	n.noteModes(sid, proto.ModeDefault, "")

	allow := true
	hub.mu.Lock()
	hub.resolve = func(proto.Interaction) proto.Resolution { return proto.Resolution{Allow: &allow} }
	hub.mu.Unlock()

	ask := func(ix map[string]any) proto.Interaction {
		t.Helper()
		hub.mu.Lock()
		before := len(hub.asked)
		hub.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var res proto.Resolution
		if err := host.peer.Call(ctx, "interaction.create", map[string]any{"interaction": ix}, &res); err != nil {
			t.Fatal(err)
		}
		hub.mu.Lock()
		defer hub.mu.Unlock()
		if len(hub.asked) != before+1 {
			t.Fatalf("expected 1 new card (%d -> %d), got %d", before, before+1, len(hub.asked))
		}
		return hub.asked[len(hub.asked)-1]
	}

	workFile := filepath.Join(t.TempDir(), "work", "report.md")
	workScope := "//" + strings.TrimPrefix(filepath.ToSlash(filepath.Dir(workFile)), "/") + "/**"
	homeDocsScope := "//" + strings.TrimPrefix(filepath.ToSlash(filepath.Join(n.home, "docs")), "/") + "/**"

	for _, tc := range []struct {
		name string
		ix   map[string]any
		want []string
	}{
		{
			name: "Read with absolute path gets directory Read scope",
			ix: map[string]any{
				"kind": "permission", "session_id": sid, "tool_name": "Read", "tool_use_id": "c-read",
				"tool_input": map[string]any{"file_path": workFile},
			},
			want: []string{"tool:Read(" + workScope + ")"},
		},
		{
			name: "Read with home-relative tilde path expands home",
			ix: map[string]any{
				"kind": "permission", "session_id": sid, "tool_name": "Read", "tool_use_id": "c-read-tilde",
				"tool_input": map[string]any{"file_path": "~/docs/a.md"},
			},
			want: []string{"tool:Read(" + homeDocsScope + ")"},
		},
		{
			name: "Write with absolute path gets directory Edit scope",
			ix: map[string]any{
				"kind": "permission", "session_id": sid, "tool_name": "Write", "tool_use_id": "c-write",
				"tool_input": map[string]any{"file_path": workFile, "content": "hi"},
			},
			want: []string{"tool:Edit(" + workScope + ")"},
		},
		{
			name: "Edit with absolute path gets directory Edit scope",
			ix: map[string]any{
				"kind": "permission", "session_id": sid, "tool_name": "Edit", "tool_use_id": "c-edit",
				"tool_input": map[string]any{"file_path": workFile, "old_string": "a", "new_string": "b"},
			},
			want: []string{"tool:Edit(" + workScope + ")"},
		},
		{
			name: "Write with relative path fails closed with no AlwaysKeys",
			ix: map[string]any{
				"kind": "permission", "session_id": sid, "tool_name": "Write", "tool_use_id": "c-write-rel",
				"tool_input": map[string]any{"file_path": "relative/notes.md"},
			},
			want: nil,
		},
		{
			name: "Read with empty path fails closed with no AlwaysKeys",
			ix: map[string]any{
				"kind": "permission", "session_id": sid, "tool_name": "Read", "tool_use_id": "c-read-empty",
				"tool_input": map[string]any{},
			},
			want: nil,
		},
		{
			name: "Write at filesystem root fails closed with no AlwaysKeys",
			ix: map[string]any{
				"kind": "permission", "session_id": sid, "tool_name": "Write", "tool_use_id": "c-write-root",
				"tool_input": map[string]any{"file_path": "/root-file.txt"},
			},
			want: nil,
		},
		{
			name: "Write with truncated path fails closed with no AlwaysKeys",
			ix: map[string]any{
				"kind": "permission", "session_id": sid, "tool_name": "Write", "tool_use_id": "c-write-trunc",
				"tool_input": map[string]any{"file_path": workFile + "\n…（已截断，共 30000 字节）"},
			},
			want: nil,
		},
		{
			name: "non-gated Bash in default mode gets tool:Bash",
			ix: map[string]any{
				"kind": "permission", "session_id": sid, "tool_name": "Bash", "tool_use_id": "c-bash",
				"tool_input": map[string]any{"command": "ls -la"},
			},
			want: []string{"tool:Bash"},
		},
		{
			name: "empty Bash fails closed with no AlwaysKeys",
			ix: map[string]any{
				"kind": "permission", "session_id": sid, "tool_name": "Bash", "tool_use_id": "c-bash-empty",
				"tool_input": map[string]any{"command": "   "},
			},
			want: nil,
		},
		{
			name: "truncated Bash fails closed with no AlwaysKeys",
			ix: map[string]any{
				"kind": "permission", "session_id": sid, "tool_name": "Bash", "tool_use_id": "c-bash-trunc",
				"tool_input": map[string]any{"command": "echo hi\n…（已截断，共 30000 字节）"},
			},
			want: nil,
		},
		{
			name: "WebSearch gets tool:WebSearch",
			ix: map[string]any{
				"kind": "permission", "session_id": sid, "tool_name": "WebSearch", "tool_use_id": "c-websearch",
				"tool_input": map[string]any{"query": "golang"},
			},
			want: []string{"tool:WebSearch"},
		},
		{
			name: "Persona MCP tool gets tool:mcp__persona__* and drops spoofed runtime keys/label",
			ix: map[string]any{
				"kind": "permission", "session_id": sid, "tool_name": "mcp__persona__todo_list", "tool_use_id": "c-mcp",
				"always_label": "Always allow everything",
				"always_keys":  []string{"tool:*"},
				"tool_input":   map[string]any{},
			},
			want: []string{"tool:mcp__persona__todo_list"},
		},
		{
			name: "card with secret fields gets no AlwaysKeys",
			ix: map[string]any{
				"kind": "permission", "session_id": sid, "tool_name": "WebFetch", "tool_use_id": "c-fields",
				"tool_input": map[string]any{"url": "https://example.com"},
				"fields":     []map[string]any{{"key": "pw", "label": "Password", "secret": true}},
			},
			want: nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ask(tc.ix)
			if !slices.Equal(got.AlwaysKeys, tc.want) {
				t.Fatalf("AlwaysKeys = %q, want %q", got.AlwaysKeys, tc.want)
			}
			if got.AlwaysLabel != "" {
				t.Fatalf("Assistant AlwaysLabel = %q, want empty", got.AlwaysLabel)
			}
		})
	}
}

func TestRuntimeAssistantAlwaysKeysSecurityBoundaries(t *testing.T) {
	oldCtx := larkContextFromCLI
	larkContextFromCLI = func(context.Context, *Node, proto.AssistantLark, larkCall) larkCardContext {
		return larkCardContext{}
	}
	t.Cleanup(func() { larkContextFromCLI = oldCtx })

	t.Run("gated Lark write still runs larkCheck and never receives tool:Bash", func(t *testing.T) {
		api := newFakeAPI(t)
		n, hub := runtimeNode(t)
		t.Cleanup(func() { n.gates().close() })
		assistantHub(hub)
		_ = prepareRuntimeAssistantEnvironment(t, n, true)
		n.mu.Lock()
		n.larkCfg.Gates[0].Env["EXAMPLE_BASE"] = api.URL
		n.mu.Unlock()
		host := dialRuntime(t, n, agyKeepAlwaysHello())
		sid := startedAGYAssistant(t, n, host)
		n.noteModes(sid, proto.ModeDefault, "")

		allow := true
		hub.mu.Lock()
		hub.resolve = func(proto.Interaction) proto.Resolution { return proto.Resolution{Allow: &allow} }
		hub.mu.Unlock()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var res proto.Resolution
		if err := host.peer.Call(ctx, "interaction.create", map[string]any{"interaction": map[string]any{
			"kind": "permission", "session_id": sid, "tool_name": "Bash", "tool_use_id": "c-lark-write",
			"tool_input": map[string]any{"command": `ex POST /conversations/c1/messages '{"text":"hello"}'`},
		}}, &res); err != nil {
			t.Fatal(err)
		}
		hub.mu.Lock()
		card := hub.asked[len(hub.asked)-1]
		hub.mu.Unlock()
		if card.ToolName != "示例" || !card.OutwardWrite {
			t.Fatalf("expected Lark gate card, got %+v", card)
		}
		if !slices.Equal(card.AlwaysKeys, []string{"lark:POST /conversations/*/messages"}) {
			t.Fatalf("Lark gate AlwaysKeys = %q, want [lark:POST /conversations/*/messages]", card.AlwaysKeys)
		}
	})

	t.Run("space session withholds AlwaysKeys for file, Bash, and Lark calls", func(t *testing.T) {
		api := newFakeAPI(t)
		n, hub := runtimeNode(t)
		t.Cleanup(func() { n.gates().close() })
		assistantHub(hub)
		_ = prepareRuntimeAssistantEnvironment(t, n, true)
		n.mu.Lock()
		n.larkCfg.Gates[0].Env["EXAMPLE_BASE"] = api.URL
		n.mu.Unlock()
		healthDir := filepath.Join(n.home, ".persona", "health")
		if err := os.MkdirAll(healthDir, 0o755); err != nil {
			t.Fatal(err)
		}
		host := dialRuntime(t, n, agyKeepAlwaysHello())
		const spaceSid = "antigravity-health-1"
		n.state.put(spaceSid, sessionState{Launcher: "antigravity", Cwd: healthDir})
		if err := host.peer.Notify("runtime.sessions", map[string]any{"sessions": []proto.Session{
			{SessionID: spaceSid, Title: "Health", Cwd: healthDir, Space: "health", Status: proto.StatusIdle},
		}}); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "space session listed", func() bool { _, _, ok := n.runtimes.rowOf(spaceSid); return ok })

		allow := true
		hub.mu.Lock()
		hub.resolve = func(proto.Interaction) proto.Resolution { return proto.Resolution{Allow: &allow} }
		hub.mu.Unlock()

		for _, ix := range []map[string]any{
			{"kind": "permission", "session_id": spaceSid, "tool_name": "Read", "tool_use_id": "s-read",
				"tool_input": map[string]any{"file_path": filepath.Join(t.TempDir(), "a.md")}},
			{"kind": "permission", "session_id": spaceSid, "cwd": t.TempDir(), "tool_name": "Bash", "tool_use_id": "s-bash",
				"tool_input": map[string]any{"command": "ls -la"}},
			{"kind": "permission", "session_id": spaceSid, "cwd": t.TempDir(), "tool_name": "Bash", "tool_use_id": "s-lark",
				"tool_input": map[string]any{"command": `ex POST /conversations/c1/messages '{"text":"health"}'`}},
		} {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			var res proto.Resolution
			err := host.peer.Call(ctx, "interaction.create", map[string]any{"interaction": ix}, &res)
			cancel()
			if err != nil {
				t.Fatal(err)
			}
			hub.mu.Lock()
			card := hub.asked[len(hub.asked)-1]
			hub.mu.Unlock()
			if len(card.AlwaysKeys) != 0 {
				t.Fatalf("space card %s got AlwaysKeys=%q, want none", ix["tool_use_id"], card.AlwaysKeys)
			}
		}
	})

	t.Run("origin:inbox strips tool:Bash while keeping directory-scoped Edit and forwards origin hint", func(t *testing.T) {
		n, hub := runtimeNode(t)
		t.Cleanup(func() { n.gates().close() })
		assistantHub(hub)
		cwd := prepareRuntimeAssistantEnvironment(t, n, true)

		allow := true
		hub.mu.Lock()
		hub.resolve = func(proto.Interaction) proto.Resolution { return proto.Resolution{Allow: &allow} }
		hub.mu.Unlock()

		host := dialRuntime(t, n, agyKeepAlwaysHello())
		const inboxSid = "antigravity-inbox-1"
		n.state.put(inboxSid, sessionState{Launcher: "antigravity", Cwd: cwd, Origin: originInbox, OriginKnown: true})
		if err := host.peer.Notify("runtime.sessions", map[string]any{"sessions": []proto.Session{
			{SessionID: inboxSid, Title: "Inbox", Cwd: cwd, Status: proto.StatusIdle},
		}}); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "inbox session listed", func() bool { _, _, ok := n.runtimes.rowOf(inboxSid); return ok })

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var res proto.Resolution
		// 1. Direct Inbox Bash -> AlwaysKeys empty, origin=inbox.
		if err := host.peer.Call(ctx, "interaction.create", map[string]any{"interaction": map[string]any{
			"kind": "permission", "session_id": inboxSid, "tool_name": "Bash", "tool_use_id": "in-bash",
			"tool_input": map[string]any{"command": "ls -la"},
		}}, &res); err != nil {
			t.Fatal(err)
		}
		// 2. Child session of Inbox Bash -> AlwaysKeys empty, origin=inbox.
		if err := host.peer.Call(ctx, "interaction.create", map[string]any{"interaction": map[string]any{
			"kind": "permission", "session_id": inboxSid + "@child1", "tool_name": "Bash", "tool_use_id": "in-child-bash",
			"tool_input": map[string]any{"command": "ls -la"},
		}}, &res); err != nil {
			t.Fatal(err)
		}
		// 3. Inbox Edit with absolute file_path -> retains directory-scoped Edit key.
		targetFile := filepath.Join(t.TempDir(), "notes", "draft.md")
		wantEditScope := "//" + strings.TrimPrefix(filepath.ToSlash(filepath.Dir(targetFile)), "/") + "/**"
		if err := host.peer.Call(ctx, "interaction.create", map[string]any{"interaction": map[string]any{
			"kind": "permission", "session_id": inboxSid, "tool_name": "Edit", "tool_use_id": "in-edit",
			"tool_input": map[string]any{"file_path": targetFile},
		}}, &res); err != nil {
			t.Fatal(err)
		}

		hub.mu.Lock()
		cards := append([]proto.Interaction(nil), hub.asked...)
		origins := append([]string(nil), hub.askedOrigins...)
		hub.mu.Unlock()
		if len(cards) != 3 {
			t.Fatalf("cards = %d, want 3", len(cards))
		}
		if len(cards[0].AlwaysKeys) != 0 || origins[0] != originInbox {
			t.Fatalf("inbox Bash: keys=%q origin=%q, want none / inbox", cards[0].AlwaysKeys, origins[0])
		}
		if len(cards[1].AlwaysKeys) != 0 || origins[1] != originInbox {
			t.Fatalf("inbox child Bash: keys=%q origin=%q, want none / inbox", cards[1].AlwaysKeys, origins[1])
		}
		if !slices.Equal(cards[2].AlwaysKeys, []string{"tool:Edit(" + wantEditScope + ")"}) {
			t.Fatalf("inbox Edit: keys=%q, want directory Edit scope", cards[2].AlwaysKeys)
		}
	})
}

func TestRuntimeNonAssistantAGYAlwaysKeysParity(t *testing.T) {
	n, hub := runtimeNode(t)
	t.Cleanup(func() { n.gates().close() })
	host := dialRuntime(t, n, agyKeepAlwaysHello())
	regularDir := t.TempDir()
	const regSid = "antigravity-agent-1"
	if err := host.peer.Notify("runtime.sessions", map[string]any{"sessions": []proto.Session{
		{SessionID: regSid, Title: "Agent", Cwd: regularDir, Status: proto.StatusIdle},
	}}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "regular session listed", func() bool { _, _, ok := n.runtimes.rowOf(regSid); return ok })

	allow := true
	hub.mu.Lock()
	hub.resolve = func(proto.Interaction) proto.Resolution { return proto.Resolution{Allow: &allow, Always: true} }
	hub.mu.Unlock()

	ask := func(ix map[string]any) proto.Interaction {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var res proto.Resolution
		if err := host.peer.Call(ctx, "interaction.create", map[string]any{"interaction": ix}, &res); err != nil {
			t.Fatal(err)
		}
		hub.mu.Lock()
		defer hub.mu.Unlock()
		return hub.asked[len(hub.asked)-1]
	}

	// 1. Native ACP allow_always present (always_label set): keep native always_label and do NOT widen to hub AlwaysKeys.
	native := ask(map[string]any{
		"kind": "permission", "session_id": regSid, "tool_name": "Bash", "tool_use_id": "r-native",
		"always_label": "Always allow git status",
		"tool_input":   map[string]any{"command": "git status"},
	})
	if len(native.AlwaysKeys) != 0 {
		t.Fatalf("non-assistant native allow_always widened AlwaysKeys = %q, want none", native.AlwaysKeys)
	}
	if native.AlwaysLabel != "Always allow git status" {
		t.Fatalf("non-assistant native AlwaysLabel = %q, want preserved", native.AlwaysLabel)
	}

	// 2. Native ACP allow_always absent (always_label omitted): hub keeps scoped AlwaysKeys.
	target := filepath.Join(regularDir, "src", "main.go")
	scope := "//" + strings.TrimPrefix(filepath.ToSlash(filepath.Dir(target)), "/") + "/**"
	writeCard := ask(map[string]any{
		"kind": "permission", "session_id": regSid, "tool_name": "Write", "tool_use_id": "r-write",
		"tool_input": map[string]any{"file_path": target, "content": "package main"},
	})
	if !slices.Equal(writeCard.AlwaysKeys, []string{"tool:Edit(" + scope + ")"}) {
		t.Fatalf("non-assistant Write AlwaysKeys = %q, want [tool:Edit(%s)]", writeCard.AlwaysKeys, scope)
	}
	bashCard := ask(map[string]any{
		"kind": "permission", "session_id": regSid, "tool_name": "Bash", "tool_use_id": "r-bash",
		"tool_input": map[string]any{"command": "go test ./..."},
	})
	if !slices.Equal(bashCard.AlwaysKeys, []string{"tool:Bash"}) {
		t.Fatalf("non-assistant Bash AlwaysKeys = %q, want [tool:Bash]", bashCard.AlwaysKeys)
	}
	truncCard := ask(map[string]any{
		"kind": "permission", "session_id": regSid, "tool_name": "Bash", "tool_use_id": "r-bash-trunc",
		"tool_input": map[string]any{"command": "echo x\n…（已截断，共 30000 字节）"},
	})
	if len(truncCard.AlwaysKeys) != 0 {
		t.Fatalf("non-assistant truncated Bash AlwaysKeys = %q, want none", truncCard.AlwaysKeys)
	}
}
