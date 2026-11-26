package node

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestParseAgyQueuedLines(t *testing.T) {
	screen := strings.Join([]string{
		"  Running unit tests...",
		"I need (something to send immediately) in prose",
		"Also run the linter... (alt+m to send immediately)",
		"❯ And check git diff (Alt+M to send immediately)",
		"• Fix foo(bar) (opt+m to send immediately)",
		"────────────────────────────────────────",
	}, "\n")
	want := []string{
		"Also run the linter...",
		"And check git diff",
		"Fix foo(bar)",
	}
	if got := parseAgyQueuedLines(screen); !slices.Equal(got, want) {
		t.Fatalf("parseAgyQueuedLines() = %q, want %q", got, want)
	}
	if got := parseAgyQueuedLines(idleScreen); len(got) != 0 {
		t.Fatalf("idle screen parsed queued lines %q, want none", got)
	}
}

func TestAgyPreviewMatchesText(t *testing.T) {
	if !agyPreviewMatchesText("Also run the linter...", "Also run the linter and report back") {
		t.Fatal("expected ellipsis preview to match full prompt")
	}
	if !agyPreviewMatchesText("Check this screenshot…", "<attached_images>\n/tmp/a.png\n</attached_images>\nCheck this screenshot carefully") {
		t.Fatal("expected preview to match prompt with attached_images block")
	}
	if agyPreviewMatchesText("Unrelated follow-up...", "Also run the linter") {
		t.Fatal("unexpected match for unrelated text")
	}
}

func TestAgyBusyQueueLifecycleAndClear(t *testing.T) {
	defer func(d time.Duration) { agyQueuePollEvery = d }(agyQueuePollEvery)
	agyQueuePollEvery = time.Hour

	n, f, home := herdrNode(t)
	t.Setenv("ANTIGRAVITY_CLI_CONFIG_DIR", "")
	a := agyTestAgent(agyTestPane, home, agyTestID)
	a["agent_status"] = proto.StatusWorking
	seedAgents(t, n, f, a)
	file := agyTestFile(t, n.agyDataRoot(), agyTestID, "")

	const prompt = "Also run the linter at the end of this turn"
	var mu sync.Mutex
	screen := "  Running tests...\n"
	f.handle("pane.read", func(map[string]any) any {
		mu.Lock()
		defer mu.Unlock()
		return map[string]any{"read": map[string]any{"text": screen}}
	})
	f.handle("agent.prompt", func(map[string]any) any {
		mu.Lock()
		screen = "  Running tests...\nAlso run the linter... (alt+m to send immediately)\n"
		mu.Unlock()
		return map[string]any{}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := requestCtx(t, ctx, n, "session.send", map[string]any{"session_id": agyTestRow, "text": prompt}); err != nil {
		t.Fatalf("busy agy send failed: %v", err)
	}

	findRow := func() *proto.Session {
		for _, s := range listSessions(t, n) {
			if s.SessionID == agyTestRow {
				row := s
				return &row
			}
		}
		return nil
	}

	row := findRow()
	if row == nil {
		t.Fatal("missing agy row")
	}
	if row.Capabilities == nil || row.Capabilities.QueueSendNow == nil || !*row.Capabilities.QueueSendNow ||
		row.Capabilities.QueueEdit == nil || *row.Capabilities.QueueEdit {
		t.Fatalf("unexpected agy queue capabilities: %+v", row.Capabilities)
	}
	// Full prompt is surfaced and deduplicated against the truncated screen preview.
	if !slices.Equal(row.Queued, []string{prompt}) {
		t.Fatalf("Session.Queued = %q, want [%q]", row.Queued, prompt)
	}

	// Once agy consumes the message and writes USER_INPUT to transcript.jsonl, Session.Queued clears cleanly.
	line := fmt.Sprintf(`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":%q,"content":"<USER_REQUEST>\n%s\n</USER_REQUEST>"}`+"\n", time.Now().UTC().Format(time.RFC3339Nano), prompt)
	if err := os.WriteFile(file, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	screen = "  Running linter...\n"
	mu.Unlock()
	if row = findRow(); len(row.Queued) != 0 {
		t.Fatalf("Session.Queued after USER_INPUT consume = %q, want empty", row.Queued)
	}
}

func TestAgyQueueClearsOnIdleAndSendNow(t *testing.T) {
	defer func(d, w time.Duration) { agyQueuePollEvery, sendNowWait = d, w }(agyQueuePollEvery, sendNowWait)
	agyQueuePollEvery = 20 * time.Millisecond
	sendNowWait = 600 * time.Millisecond

	n, f, home := herdrNode(t)
	t.Setenv("ANTIGRAVITY_CLI_CONFIG_DIR", "")
	a := agyTestAgent(agyTestPane, home, agyTestID)
	a["agent_status"] = proto.StatusWorking
	seedAgents(t, n, f, a)

	var mu sync.Mutex
	screen := "  Working...\nTyped in terminal... (alt+m to send immediately)\n"
	f.handle("pane.read", func(map[string]any) any {
		mu.Lock()
		defer mu.Unlock()
		return map[string]any{"read": map[string]any{"text": screen}}
	})
	var keys []string
	f.handle("pane.send_keys", func(p map[string]any) any {
		mu.Lock()
		if ks, ok := p["keys"].([]any); ok {
			for _, k := range ks {
				if s, ok := k.(string); ok {
					keys = append(keys, s)
				}
			}
		}
		screen = "  Working...\n"
		mu.Unlock()
		return map[string]any{}
	})

	// Background screen poll picks up a queued message typed directly in the terminal.
	waitFor(t, "terminal-typed agy queued message", func() bool {
		for _, s := range listSessions(t, n) {
			if s.SessionID == agyTestRow && slices.Equal(s.Queued, []string{"Typed in terminal..."}) {
				return true
			}
		}
		return false
	})

	// session.queue.send_now dispatches alt+m and clears Session.Queued.
	if _, err := request(t, n, "session.queue.send_now", map[string]any{"session_id": agyTestRow}); err != nil {
		t.Fatalf("session.queue.send_now on agy: %v", err)
	}
	mu.Lock()
	gotKeys := slices.Clone(keys)
	mu.Unlock()
	if !slices.Equal(gotKeys, []string{"alt+m"}) {
		t.Fatalf("send_now keys = %q, want [alt+m]", gotKeys)
	}
	for _, s := range listSessions(t, n) {
		if s.SessionID == agyTestRow && len(s.Queued) != 0 {
			t.Fatalf("Session.Queued after send_now = %q, want empty", s.Queued)
		}
	}

	// Returning to idle also clears any tracked queued entries cleanly.
	n.recordAgyQueued(agyTestRow, agyTestPane, "Follow up while busy", nil, []string{"Follow up..."})
	a["agent_status"] = proto.StatusIdle
	seedAgents(t, n, f, a)
	for _, s := range listSessions(t, n) {
		if s.SessionID == agyTestRow && len(s.Queued) != 0 {
			t.Fatalf("Session.Queued while idle = %q, want empty", s.Queued)
		}
	}
}
