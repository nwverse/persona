package node

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/transcript"
)

// Each kind of the app-server's ThreadItems the chat shows becomes the rollout item the transcript reader knows: the
// user's words and images, the answer, its reasoning, a command with its output and exit code, a file change, an MCP
// call, a web search, an image looked at, a compaction. Kinds the chat does not show are left out.
func TestCodexRolloutItemsReadAsARollout(t *testing.T) {
	parse := func(kind, raw string) []proto.Item {
		t.Helper()
		item := codexRolloutItem(kind, []byte(raw))
		if item == nil {
			return nil
		}
		line, _ := json.Marshal(map[string]any{"timestamp": "2026-11-01T00:00:00Z", "type": "event_msg",
			"payload": map[string]any{"type": "item_completed", "item": item}})
		var m transcript.Meta
		return transcript.ParseLine(0, line, &m)
	}
	user := parse("userMessage", `{"type":"userMessage","id":"u1","content":[{"type":"text","text":"fix the export","text_elements":[]},{"type":"localImage","path":"/tmp/shot.png"}]}`)
	if len(user) != 1 || user[0].Role != "user" || !strings.Contains(user[0].Text, "fix the export") || !strings.Contains(user[0].Text, "/tmp/shot.png") {
		t.Fatalf("user = %+v", user)
	}
	answer := parse("agentMessage", `{"type":"agentMessage","id":"a1","text":"Done: the CSV is UTF-8 now.","phase":"final_answer"}`)
	if len(answer) != 1 || answer[0].Role != "assistant" || answer[0].Kind != proto.KindText || answer[0].Text != "Done: the CSV is UTF-8 now." {
		t.Fatalf("answer = %+v", answer)
	}
	if note := parse("agentMessage", `{"type":"agentMessage","id":"a2","text":"Looking at the writer.","phase":"commentary"}`); len(note) != 1 || note[0].Kind != proto.KindThinking {
		t.Fatalf("commentary = %+v", note)
	}
	if r := parse("reasoning", `{"type":"reasoning","id":"r1","summary":["Check the encoder"],"content":[]}`); len(r) != 1 || r[0].Kind != proto.KindThinking {
		t.Fatalf("reasoning = %+v", r)
	}
	cmd := parse("commandExecution", `{"type":"commandExecution","id":"c1","command":"go test ./...","cwd":"/w","status":"failed","aggregatedOutput":"FAIL x","exitCode":1,"durationMs":1500}`)
	if len(cmd) != 2 || cmd[0].ToolName != "Bash" || !strings.Contains(string(cmd[0].ToolInput), "go test ./...") || !cmd[1].IsError ||
		cmd[1].Outcome == nil || cmd[1].Outcome.DurationMS == nil || *cmd[1].Outcome.DurationMS != 1500 {
		t.Fatalf("command = %+v", cmd)
	}
	edit := parse("fileChange", `{"type":"fileChange","id":"f1","status":"completed","changes":[{"path":"/w/a.go","kind":{"type":"update","move_path":null},"diff":"@@ -1 +1 @@\n-a\n+b\n"}]}`)
	if len(edit) != 2 || edit[0].ToolName != "Edit" || !strings.Contains(string(edit[0].ToolInput), "/w/a.go") || edit[1].Text != "update /w/a.go" {
		t.Fatalf("file change = %+v", edit)
	}
	mcp := parse("mcpToolCall", `{"type":"mcpToolCall","id":"m1","server":"docs","tool":"search","status":"completed","arguments":{"q":"csv"},"result":{"content":[{"type":"text","text":"3 hits"}]},"error":null}`)
	if len(mcp) != 2 || mcp[0].ToolName != "mcp__docs__search" || mcp[1].Text != "3 hits" {
		t.Fatalf("mcp = %+v", mcp)
	}
	if web := parse("webSearch", `{"type":"webSearch","id":"w1","query":"utf-8 bom csv","action":{"type":"search","url":""},"results":[{"title":"BOM","url":"https://example.com/bom","domain":"example.com","snippet":"…"}]}`); len(web) == 0 {
		t.Fatal("a web search shows nothing")
	}
	if img := parse("imageView", `{"type":"imageView","id":"i1","path":"/tmp/shot.png"}`); len(img) != 2 || img[0].ToolName != "Read" {
		t.Fatalf("image = %+v", img)
	}
	if c := parse("contextCompaction", `{"type":"contextCompaction","id":"k1"}`); len(c) != 1 || c[0].Kind != proto.KindNotice {
		t.Fatalf("compaction = %+v", c)
	}
	for _, kind := range []string{"sleep", "subAgentActivity", "somethingNew"} {
		if item := codexRolloutItem(kind, []byte(`{"type":"`+kind+`","id":"x"}`)); item != nil {
			t.Errorf("%s = %v, want left out", kind, item)
		}
	}
}

// writeCodexHistory writes the App's history database in home: turns (id, status) and items (turn, kind, JSON).
func writeCodexHistory(t *testing.T, home, thread string, turns [][2]string, items [][3]string) {
	t.Helper()
	sqlite := sqlite3WithJSON(t)
	q := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
	var b strings.Builder
	b.WriteString(`CREATE TABLE IF NOT EXISTS thread_turns (thread_id TEXT NOT NULL, turn_id TEXT NOT NULL, rollout_ordinal INTEGER NOT NULL,
		status TEXT NOT NULL, duration_ms INTEGER, PRIMARY KEY (thread_id, turn_id));
		CREATE TABLE IF NOT EXISTS thread_items (thread_id TEXT NOT NULL, turn_id TEXT NOT NULL, item_id TEXT NOT NULL,
		rollout_ordinal INTEGER NOT NULL, created_at_ms INTEGER NOT NULL, item_json TEXT NOT NULL, item_type TEXT NOT NULL DEFAULT '',
		PRIMARY KEY (thread_id, turn_id, item_id)); DELETE FROM thread_turns; DELETE FROM thread_items;`)
	for i, turn := range turns {
		fmt.Fprintf(&b, "INSERT INTO thread_turns VALUES (%s, %s, %d, %s, 1200);", q(thread), q(turn[0]), i, q(turn[1]))
	}
	for i, it := range items {
		fmt.Fprintf(&b, "INSERT INTO thread_items VALUES (%s, %s, 'i%d', %d, %d, %s, %s);", q(thread), q(it[0]), i, i,
			time.Date(2026, 11, 1, 10, 0, i, 0, time.UTC).UnixMilli(), q(it[2]), q(it[1]))
	}
	if out, err := exec.Command(sqlite, filepath.Join(home, "thread_history_1.sqlite"), b.String()).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3: %v: %s", err, out)
	}
}

// The transcript of an App chat whose rollout is gone is its history in the App's database, written as a rollout when
// read: the turns and their items, read like any Codex transcript; written again once the App changed the chat (#3305).
func TestCodexAppChatHistoryComesFromTheAppsDatabase(t *testing.T) {
	n := remoteTestNode(t)
	home := n.codexHome()
	project := t.TempDir()
	updated := time.Now().Add(-48 * time.Hour).Truncate(time.Millisecond)
	writeCodexApp(t, home, project, codexAppSchema+codexAppRow(appChat, "", project, "Alpha chat", updated, 0))
	turns := [][2]string{{"t1", "completed"}}
	items := [][3]string{
		{"t1", "userMessage", `{"type":"userMessage","id":"u1","content":[{"type":"text","text":"fix the export","text_elements":[]}]}`},
		{"t1", "commandExecution", `{"type":"commandExecution","id":"c1","command":"go test ./...","status":"completed","aggregatedOutput":"ok","exitCode":0,"durationMs":900}`},
		{"t1", "sleep", `{"type":"sleep","id":"s1","durationMs":1000}`},
		{"t1", "agentMessage", `{"type":"agentMessage","id":"a1","text":"Fixed.","phase":"final_answer"}`},
	}
	writeCodexHistory(t, home, appChat, turns, items)

	var id string
	for _, s := range n.codexSessions(map[string]bool{}) {
		if s.SourceThreadID == appChat {
			id = s.SessionID
		}
	}
	if id == "" {
		t.Fatal("the chat is not listed")
	}
	read := func() []proto.Item {
		t.Helper()
		f, err := n.file(id)
		if err != nil {
			t.Fatal(err)
		}
		got, err := f.Refresh()
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	texts := func(list []proto.Item) string {
		var parts []string
		for _, it := range list {
			parts = append(parts, it.Role+":"+string(it.Kind)+":"+it.Text)
		}
		return strings.Join(parts, " | ")
	}
	got := read()
	s := texts(got)
	if !strings.Contains(s, "user:text:fix the export") || !strings.Contains(s, "assistant:text:Fixed.") || !strings.Contains(s, "tool_use") ||
		!strings.Contains(s, "turn_duration") {
		t.Fatalf("history = %s", s)
	}
	if p := n.transcriptPath(id); !strings.HasPrefix(p, n.dir) {
		t.Fatalf("transcript at %q, want under the state directory", p)
	}

	// The App answered again: the database says the chat changed, and the next read has the new turn.
	turns = append(turns, [2]string{"t2", "completed"})
	items = append(items, [3]string{"t2", "userMessage", `{"type":"userMessage","id":"u2","content":[{"type":"text","text":"and the PDF?","text_elements":[]}]}`},
		[3]string{"t2", "agentMessage", `{"type":"agentMessage","id":"a2","text":"PDF too.","phase":"final_answer"}`})
	writeCodexHistory(t, home, appChat, turns, items)
	writeCodexApp(t, home, project, "DELETE FROM threads;"+codexAppRow(appChat, "", project, "Alpha chat", updated.Add(time.Hour), 0))
	if s := texts(read()); !strings.Contains(s, "user:text:and the PDF?") || !strings.Contains(s, "assistant:text:PDF too.") {
		t.Fatalf("after the App's answer = %s", s)
	}
}

// Without the App's history database nothing is written, and the chat has no transcript, as before.
func TestCodexAppChatWithoutHistoryDatabaseHasNoTranscript(t *testing.T) {
	n := remoteTestNode(t)
	home := n.codexHome()
	project := t.TempDir()
	writeCodexApp(t, home, project, codexAppSchema+codexAppRow(appChat, "", project, "Alpha chat", time.Now(), 0))
	var id string
	for _, s := range n.codexSessions(map[string]bool{}) {
		if s.SourceThreadID == appChat {
			id = s.SessionID
		}
	}
	if _, err := n.file(id); err == nil {
		t.Fatal("a transcript without the history database")
	}
	if entries, _ := os.ReadDir(filepath.Join(n.dir, "codex-app-history")); len(entries) != 0 {
		t.Fatalf("written: %v", entries)
	}
}
