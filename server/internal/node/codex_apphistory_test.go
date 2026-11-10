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
// user's words and images, the answer, its reasoning, a command (without the login shell around it) with its output and
// exit code, a file change, an MCP call, a web search, an image looked at or made, a compaction. Kinds the chat does not
// show, and items still running, are left out.
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
	cmd := parse("commandExecution", `{"type":"commandExecution","id":"c1","command":"/bin/bash -lc 'go test ./...'","cwd":"/w","status":"failed","aggregatedOutput":"FAIL x","exitCode":1,"durationMs":1500}`)
	if len(cmd) != 2 || cmd[0].ToolName != "Bash" || !strings.Contains(string(cmd[0].ToolInput), `"go test ./..."`) ||
		strings.Contains(string(cmd[0].ToolInput), "bash") || !cmd[1].IsError || cmd[1].Outcome == nil || cmd[1].Outcome.DurationMS == nil || *cmd[1].Outcome.DurationMS != 1500 {
		t.Fatalf("command = %+v", cmd)
	}
	if running := parse("commandExecution", `{"type":"commandExecution","id":"c2","command":"sleep 9","status":"inProgress"}`); running != nil {
		t.Fatalf("a running command = %+v", running)
	}
	edit := parse("fileChange", `{"type":"fileChange","id":"f1","status":"completed","changes":[{"path":"/w/a.go","kind":{"type":"update","move_path":null},"diff":"@@ -1 +1 @@\n-a\n+b\n"}]}`)
	if len(edit) != 2 || edit[0].ToolName != "Edit" || !strings.Contains(string(edit[0].ToolInput), "/w/a.go") || edit[1].Text != "update /w/a.go" {
		t.Fatalf("file change = %+v", edit)
	}
	mcp := parse("mcpToolCall", `{"type":"mcpToolCall","id":"m1","server":"docs","tool":"search","status":"completed","arguments":{"q":"csv"},"result":{"content":[{"type":"text","text":"3 hits"}]},"error":null}`)
	if len(mcp) != 2 || mcp[0].ToolName != "mcp__docs__search" || mcp[1].Text != "3 hits" {
		t.Fatalf("mcp = %+v", mcp)
	}
	web := parse("webSearch", `{"type":"webSearch","id":"w1","query":"utf-8 bom csv","action":{"type":"search","url":""},"results":[{"title":"BOM","url":"https://example.com/bom","domain":"example.com","snippet":"…"}]}`)
	if len(web) != 2 || !strings.Contains(string(web[0].ToolInput), "utf-8 bom csv") || !strings.Contains(web[1].Text, "https://example.com/bom") {
		t.Fatalf("web search = %+v", web)
	}
	if img := parse("imageView", `{"type":"imageView","id":"i1","path":"/tmp/shot.png"}`); len(img) != 2 || img[0].ToolName != "Read" {
		t.Fatalf("image = %+v", img)
	}
	if made := parse("imageGeneration", `{"type":"imageGeneration","id":"g1","status":"completed","savedPath":"/tmp/made.png","revisedPrompt":"a cat"}`); len(made) != 2 || !strings.Contains(string(made[0].ToolInput), "/tmp/made.png") {
		t.Fatalf("image made = %+v", made)
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

type historyTurn struct {
	thread, id, status string
	ordinal            int
	errJSON            string
	items              [][2]string // kind, JSON
}

// writeCodexHistory writes the App's history database in home, inserting the items in reverse order: the history is
// read in the order of their ordinals, not of their rows.
func writeCodexHistory(t *testing.T, home string, turns []historyTurn) {
	t.Helper()
	sqlite := sqlite3WithJSON(t)
	q := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
	var b strings.Builder
	b.WriteString(`CREATE TABLE IF NOT EXISTS thread_turns (thread_id TEXT NOT NULL, turn_id TEXT NOT NULL, rollout_ordinal INTEGER NOT NULL,
		status TEXT NOT NULL, error_json TEXT, duration_ms INTEGER, PRIMARY KEY (thread_id, turn_id));
		CREATE TABLE IF NOT EXISTS thread_items (thread_id TEXT NOT NULL, turn_id TEXT NOT NULL, item_id TEXT NOT NULL,
		rollout_ordinal INTEGER NOT NULL, created_at_ms INTEGER NOT NULL, item_json TEXT NOT NULL, item_type TEXT NOT NULL DEFAULT '',
		PRIMARY KEY (thread_id, turn_id, item_id)); DELETE FROM thread_turns; DELETE FROM thread_items;`)
	n := 0
	var rows []string
	for _, turn := range turns {
		errJSON := "NULL"
		if turn.errJSON != "" {
			errJSON = q(turn.errJSON)
		}
		fmt.Fprintf(&b, "INSERT INTO thread_turns VALUES (%s, %s, %d, %s, %s, 1200);", q(turn.thread), q(turn.id), turn.ordinal, q(turn.status), errJSON)
		for _, it := range turn.items {
			// Item ids sort against their ordinals (z…a): neither the rows' order nor the key's is the history's.
			rows = append(rows, fmt.Sprintf("INSERT INTO thread_items VALUES (%s, %s, 'i%04d', %d, %d, %s, %s);", q(turn.thread), q(turn.id), 9999-n, n,
				time.Date(2026, 11, 1, 10, 0, n, 0, time.UTC).UnixMilli(), q(it[1]), q(it[0])))
			n++
		}
	}
	for i := len(rows) - 1; i >= 0; i-- {
		b.WriteString(rows[i])
	}
	if out, err := exec.Command(sqlite, filepath.Join(home, "thread_history_1.sqlite"), b.String()).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3: %v: %s", err, out)
	}
}

func userItem(id, text string) [2]string {
	return [2]string{"userMessage", fmt.Sprintf(`{"type":"userMessage","id":%q,"content":[{"type":"text","text":%q,"text_elements":[]}]}`, id, text)}
}

func answerItem(id, text string) [2]string {
	return [2]string{"agentMessage", fmt.Sprintf(`{"type":"agentMessage","id":%q,"text":%q,"phase":"final_answer"}`, id, text)}
}

func historyText(list []proto.Item) string {
	var parts []string
	for _, it := range list {
		parts = append(parts, it.Role+":"+string(it.Kind)+":"+it.Text)
	}
	return strings.Join(parts, " | ")
}

func statOf(t *testing.T, p string) os.FileInfo {
	t.Helper()
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// The transcript of an App chat whose rollout is gone is its history in the App's database, written as a rollout: only
// this chat's finished turns, in order, a failed one with its error, an interrupted one marked, one with no item kept; a
// turn still running is left for later. The list writes nothing. Read again with no change, the file stays as it is; once
// the App finished more turns they are appended, so a reader goes on where it was (#3305).
func TestCodexAppChatHistoryComesFromTheAppsDatabase(t *testing.T) {
	n := remoteTestNode(t)
	home := n.codexHome()
	project := t.TempDir()
	updated := time.Now().Add(-48 * time.Hour).Truncate(time.Millisecond)
	writeCodexApp(t, home, project, codexAppSchema+codexAppRow(appChat, "", project, "Alpha chat", updated, 0))
	running := historyTurn{thread: appChat, id: "t4", status: "inProgress", ordinal: 4, items: [][2]string{userItem("u4", "and the PDF?")}}
	turns := []historyTurn{
		{thread: appChat, id: "t0", status: "completed", ordinal: 0, items: [][2]string{userItem("u0", "fix the export"), answerItem("a0", "Fixed.")}},
		{thread: appChat, id: "t1", status: "failed", ordinal: 1, errJSON: `{"message":"Selected model is at capacity.","codexErrorInfo":"serverOverloaded"}`,
			items: [][2]string{userItem("u1", "try again")}},
		{thread: appChat, id: "t2", status: "interrupted", ordinal: 2, items: [][2]string{userItem("u2", "stop")}},
		{thread: appChat, id: "t3", status: "completed", ordinal: 3},
		running,
		{thread: appOld, id: "x0", status: "completed", ordinal: 0, items: [][2]string{userItem("x0", "another chat's words")}},
	}
	writeCodexHistory(t, home, turns)

	var id string
	for _, s := range n.codexSessions(map[string]bool{}) {
		if s.SourceThreadID == appChat {
			id = s.SessionID
		}
	}
	if id == "" {
		t.Fatal("the chat is not listed")
	}
	if entries, _ := os.ReadDir(filepath.Join(n.dir, "codex-app-history")); len(entries) != 0 {
		t.Fatalf("the list wrote %d files", len(entries))
	}
	f, err := n.file(id)
	if err != nil {
		t.Fatal(err)
	}
	got, err := f.Refresh()
	if err != nil {
		t.Fatal(err)
	}
	s := historyText(got)
	for _, want := range []string{"user:text:fix the export", "assistant:text:Fixed.", "user:text:try again", "user:text:stop"} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in %s", want, s)
		}
	}
	if strings.Contains(s, "another chat") || strings.Contains(s, "and the PDF?") {
		t.Fatalf("another chat's or a running turn's words: %s", s)
	}
	if strings.Index(s, "fix the export") > strings.Index(s, "Fixed.") || strings.Index(s, "Fixed.") > strings.Index(s, "try again") {
		t.Fatalf("out of order: %s", s)
	}
	if !strings.Contains(s, transcript.InterruptedNotice) {
		t.Fatalf("the interrupted turn is not marked: %s", s)
	}
	p := n.transcriptPath(id)
	if !strings.HasPrefix(p, n.dir) {
		t.Fatalf("transcript at %q, want under the state directory", p)
	}
	before := statOf(t, p)

	// Nothing changed: read again, the file is as it was, and the database is not even asked (made unreadable here: a
	// query would fail and be remembered).
	db := filepath.Join(home, "thread_history_1.sqlite")
	if err := os.Chmod(db, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := n.file(id); err != nil {
		t.Fatal(err)
	}
	codexAppHistoryFailed.Lock()
	asked := len(codexAppHistoryFailed.byPath)
	codexAppHistoryFailed.Unlock()
	if err := os.Chmod(db, 0o600); err != nil {
		t.Fatal(err)
	}
	if asked != 0 {
		t.Fatal("the database was asked again without a change")
	}
	if st := statOf(t, p); !os.SameFile(st, before) || !st.ModTime().Equal(before.ModTime()) || st.Size() != before.Size() {
		t.Fatal("written again without a change")
	}

	// The App finished the running turn and another: they are appended to the same file, and the reader gets only them.
	running.status = "completed"
	running.items = append(running.items, answerItem("a4", "PDF too."))
	turns[4] = running
	turns = append(turns, historyTurn{thread: appChat, id: "t5", status: "completed", ordinal: 5, items: [][2]string{userItem("u5", "thanks")}})
	writeCodexHistory(t, home, turns)
	writeCodexApp(t, home, project, "DELETE FROM threads;"+codexAppRow(appChat, "", project, "Alpha chat", updated.Add(time.Hour), 0))
	if _, err := n.file(id); err != nil {
		t.Fatal(err)
	}
	more, err := f.Refresh()
	if err != nil {
		t.Fatal(err)
	}
	if s := historyText(more); !strings.Contains(s, "user:text:and the PDF?") || !strings.Contains(s, "assistant:text:PDF too.") ||
		!strings.Contains(s, "user:text:thanks") || strings.Contains(s, "fix the export") {
		t.Fatalf("after the App's turns = %s", s)
	}
	if !os.SameFile(statOf(t, p), before) {
		t.Fatal("the file was replaced instead of grown")
	}

	// Archived in the App: no transcript of its history any more.
	writeCodexApp(t, home, project, "DELETE FROM threads;"+codexAppRow(appChat, "", project, "Alpha chat", updated.Add(2*time.Hour), 1))
	src, _ := n.codexSource(id)
	if got := n.codexAppHistoryPath(src, nil); got != "" {
		t.Fatalf("archived chat's history at %q", got)
	}
}

// A chat registered with its rollout, which the App then deleted: its history comes from the database (the registry
// keeps the path it knew).
func TestCodexAppChatHistoryAfterItsRolloutWasDeleted(t *testing.T) {
	n := remoteTestNode(t)
	home := n.codexHome()
	project := t.TempDir()
	rollout := writeAppRollout(t, home, appChat, project, time.Now().Add(-time.Hour))
	writeCodexApp(t, home, project, codexAppSchema+codexAppRow(appChat, rollout, project, "Alpha chat", time.Now(), 0))
	writeCodexHistory(t, home, []historyTurn{{thread: appChat, id: "t0", status: "completed", ordinal: 0,
		items: [][2]string{userItem("u0", "from the database")}}})
	var id string
	for _, s := range n.codexSessions(map[string]bool{}) {
		if s.SourceThreadID == appChat {
			id = s.SessionID
		}
	}
	if src, _ := n.codexSource(id); src.path != rollout {
		t.Fatalf("registered with %q, want its rollout", src.path)
	}
	if err := os.Remove(rollout); err != nil {
		t.Fatal(err)
	}
	expireRollouts(home, true)
	f, err := n.file(id)
	if err != nil {
		t.Fatal(err)
	}
	got, err := f.Refresh()
	if err != nil {
		t.Fatal(err)
	}
	if s := historyText(got); !strings.Contains(s, "user:text:from the database") {
		t.Fatalf("history = %s", s)
	}
}

// Without the App's history database, or with one this code cannot read, nothing is written and no temporary file is
// left: the chat has no transcript, as before.
func TestCodexAppChatWithoutReadableHistoryHasNoTranscript(t *testing.T) {
	for name, sql := range map[string]string{
		"no database":  "",
		"other schema": "CREATE TABLE thread_turns (thread_id TEXT, turn_id TEXT);",
	} {
		t.Run(name, func(t *testing.T) {
			n := remoteTestNode(t)
			home := n.codexHome()
			project := t.TempDir()
			writeCodexApp(t, home, project, codexAppSchema+codexAppRow(appChat, "", project, "Alpha chat", time.Now(), 0))
			if sql != "" {
				if out, err := exec.Command(sqlite3WithJSON(t), filepath.Join(home, "thread_history_1.sqlite"), sql).CombinedOutput(); err != nil {
					t.Fatalf("sqlite3: %v: %s", err, out)
				}
			}
			var id string
			for _, s := range n.codexSessions(map[string]bool{}) {
				if s.SourceThreadID == appChat {
					id = s.SessionID
				}
			}
			if _, err := n.file(id); err == nil {
				t.Fatal("a transcript without a readable history")
			}
			entries, _ := os.ReadDir(filepath.Join(n.dir, "codex-app-history"))
			for _, e := range entries {
				if strings.HasSuffix(e.Name(), ".jsonl") || strings.HasPrefix(e.Name(), ".write-") {
					t.Fatalf("left %s", e.Name())
				}
			}
		})
	}
}

func TestCodexHistoryDBPathTakesTheNewest(t *testing.T) {
	home := t.TempDir()
	for _, name := range []string{"thread_history_1.sqlite", "thread_history_3.sqlite", "thread_history_x.sqlite", "state_5.sqlite"} {
		os.WriteFile(filepath.Join(home, name), nil, 0o600)
	}
	if got := codexHistoryDBPath(home); !strings.HasSuffix(got, "thread_history_3.sqlite") {
		t.Fatalf("db = %s", got)
	}
}

// A turn that failed keeps the App's error, as a rollout's task_complete does; a chat with only a running turn has no
// transcript yet.
func TestCodexAppChatFailedTurnAndRunningOnly(t *testing.T) {
	n := remoteTestNode(t)
	home := n.codexHome()
	project := t.TempDir()
	writeCodexApp(t, home, project, codexAppSchema+codexAppRow(appChat, "", project, "Alpha chat", time.Now(), 0)+
		codexAppRow(appOld, "", project, "Old chat", time.Now(), 0))
	writeCodexHistory(t, home, []historyTurn{
		{thread: appChat, id: "t0", status: "completed", ordinal: 0, items: [][2]string{userItem("u0", "hello")}},
		{thread: appChat, id: "t1", status: "failed", ordinal: 1, errJSON: `{"message":"Selected model is at capacity.","codexErrorInfo":"serverOverloaded"}`,
			items: [][2]string{userItem("u1", "again")}},
		{thread: appOld, id: "r0", status: "inProgress", ordinal: 0, items: [][2]string{userItem("r0", "working on it")}},
	})
	ids := map[string]string{}
	for _, s := range n.codexSessions(map[string]bool{}) {
		ids[s.SourceThreadID] = s.SessionID
	}
	f, err := n.file(ids[appChat])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Refresh(); err != nil {
		t.Fatal(err)
	}
	if m := f.Meta(); m.TurnError == "" {
		t.Fatalf("the failed turn's error is lost: %+v", m)
	}
	if _, err := n.file(ids[appOld]); err == nil {
		t.Fatal("a transcript for a chat whose only turn still runs")
	}
}
