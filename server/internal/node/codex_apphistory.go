package node

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// A Codex App chat whose rollout is gone (codex_appdb.go) keeps its history in <CODEX_HOME>/thread_history_<n>.sqlite:
// thread_turns, and thread_items holding the app-server's ThreadItems (v2: userMessage, agentMessage, commandExecution…).
// Its transcript is that history written back as rollout lines — session_meta, then per turn task_started, an
// item_completed per item, and task_complete or turn_aborted once the turn ended — so the transcript reader, the clients
// and every check on a rollout read it as any other (#3305). The file is <state dir>/codex-app-history/<home>-<thread>.jsonl,
// written again (to a new file renamed over it, so readers start over, transcript.File) when the App's state database
// says the thread changed; its mtime is that change. It is written only when the transcript is read (file), never by
// the list. Without sqlite3 or the history database there is none, as before.

// codexAppHistoryWait bounds one read of the history database; a long chat's items stream in well under it.
const codexAppHistoryWait = 30 * time.Second

var codexAppHistoryWriting sync.Map // file path → *sync.Mutex: one writer per chat

// codexHistoryDBPath is the newest history database in home (thread_history_1.sqlite in 2026-11).
func codexHistoryDBPath(home string) string {
	paths, _ := filepath.Glob(filepath.Join(home, "thread_history_*.sqlite"))
	best, bestN := "", -1
	for _, p := range paths {
		n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(filepath.Base(p), "thread_history_"), ".sqlite"))
		if err == nil && n > bestN {
			best, bestN = p, n
		}
	}
	return best
}

// codexAppHistoryFile is where the transcript of an App chat with no rollout is written ("" without a state directory).
func (n *Node) codexAppHistoryFile(src codexSource) string {
	if n.dir == "" || !validID(src.thread) {
		return ""
	}
	h := sha256.Sum256([]byte(src.home))
	return filepath.Join(n.dir, "codex-app-history", hex.EncodeToString(h[:6])+"-"+src.thread+".jsonl")
}

// codexAppHistoryPath: the transcript written for src, an App chat with no rollout, while it exists; else "".
func (n *Node) codexAppHistoryPath(src codexSource) string {
	if _, ok := codexAppThreadOf(src); !ok {
		return ""
	}
	p := n.codexAppHistoryFile(src)
	if p == "" {
		return ""
	}
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

// refreshCodexAppHistory writes the transcript of session id when it is an App chat with no rollout and the App's
// database changed it since it was last written (or it never was). Called before the transcript is read.
func (n *Node) refreshCodexAppHistory(id string) {
	src, ok := n.codexSource(id)
	if !ok {
		return
	}
	t, ok := codexAppThreadOf(src)
	if !ok {
		return
	}
	p := n.codexAppHistoryFile(src)
	db := codexHistoryDBPath(src.home)
	if p == "" || db == "" {
		return
	}
	mu, _ := codexAppHistoryWriting.LoadOrStore(p, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()
	if st, err := os.Stat(p); err == nil && st.ModTime().UnixMilli() == t.UpdatedAtMs {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), codexAppHistoryWait)
	defer cancel()
	if err := writeCodexAppHistory(ctx, db, t, p); err != nil {
		n.logf("codex app history %s: %v", src.thread, err)
	}
}

type codexHistoryTurn struct {
	ID         string `json:"turnId"`
	Status     string `json:"status"`
	DurationMs int64  `json:"durationMs"`
}

// writeCodexAppHistory writes thread t's history from db as rollout lines to path, through a temporary file.
func writeCodexAppHistory(ctx context.Context, db string, t codexAppThread, path string) error {
	sqlite, err := exec.LookPath("sqlite3")
	if err != nil {
		return err
	}
	if !validID(t.ID) {
		return fmt.Errorf("bad thread id")
	}
	out, err := exec.CommandContext(ctx, sqlite, "-readonly", "-bail", "-json", "-cmd", ".timeout 2000", db,
		`SELECT turn_id AS turnId, status, COALESCE(duration_ms, -1) AS durationMs FROM thread_turns WHERE thread_id = '`+t.ID+`'`).Output()
	if err != nil {
		return err
	}
	turns := map[string]codexHistoryTurn{}
	if len(bytes.TrimSpace(out)) > 0 {
		var rows []codexHistoryTurn
		if err := json.Unmarshal(out, &rows); err != nil {
			return err
		}
		for _, r := range rows {
			turns[r.ID] = r
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".write-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	w := bufio.NewWriterSize(tmp, 256<<10)
	start := time.UnixMilli(t.UpdatedAtMs)
	line := func(at time.Time, typ string, payload any) {
		b, _ := json.Marshal(map[string]any{"timestamp": at.UTC().Format(time.RFC3339Nano), "type": typ, "payload": payload})
		w.Write(b)
		w.WriteByte('\n')
	}
	// The items, oldest first, one per line: turn, time, type, then the item's JSON (escaped: one line, no separator in it).
	cmd := exec.CommandContext(ctx, sqlite, "-readonly", "-bail", "-noheader", "-list", "-separator", "\x1f", "-cmd", ".timeout 2000", db,
		`SELECT turn_id, created_at_ms, item_type, item_json FROM thread_items WHERE thread_id = '`+t.ID+`' ORDER BY rollout_ordinal`)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	turn, last := "", start
	closeTurn := func() {
		if turn == "" {
			return
		}
		switch r := turns[turn]; r.Status {
		case "completed", "failed":
			p := map[string]any{"type": "task_complete", "turn_id": turn}
			if r.DurationMs >= 0 {
				p["duration_ms"] = r.DurationMs
			}
			line(last, "event_msg", p)
		case "interrupted":
			line(last, "event_msg", map[string]any{"type": "turn_aborted", "turn_id": turn})
		}
	}
	first := true
	for sc.Scan() {
		parts := strings.SplitN(sc.Text(), "\x1f", 4)
		if len(parts) != 4 {
			continue
		}
		if ms, err := strconv.ParseInt(parts[1], 10, 64); err == nil && ms > 0 {
			last = time.UnixMilli(ms)
		}
		if first {
			line(last, "session_meta", map[string]any{"id": t.ID, "cwd": t.Cwd, "originator": "Codex Desktop", "source": "vscode"})
			first = false
		}
		if parts[0] != turn {
			closeTurn()
			turn = parts[0]
			line(last, "event_msg", map[string]any{"type": "task_started", "turn_id": turn})
		}
		if item := codexRolloutItem(parts[2], []byte(parts[3])); item != nil {
			line(last, "event_msg", map[string]any{"type": "item_completed", "item": item})
		}
	}
	scanErr := sc.Err()
	if err := cmd.Wait(); err != nil {
		return err
	}
	if scanErr != nil {
		return scanErr
	}
	if first {
		line(start, "session_meta", map[string]any{"id": t.ID, "cwd": t.Cwd, "originator": "Codex Desktop", "source": "vscode"})
	}
	closeTurn()
	if err := w.Flush(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chtimes(tmp.Name(), time.Now(), start); err != nil { // its mtime: the change it was written for
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// codexRolloutItem turns one of the app-server's ThreadItems (v2, camelCase) into the item a rollout's item_completed
// carries (transcript/codex.go codexItem); nil for a kind the chat does not show (a sleep, a subagent's progress).
func codexRolloutItem(kind string, raw []byte) map[string]any {
	var v struct {
		ID      string `json:"id"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
			Path string `json:"path"`
		} `json:"content"`
		Text             string          `json:"text"`
		Phase            string          `json:"phase"`
		Summary          []string        `json:"summary"`
		Command          string          `json:"command"`
		Status           string          `json:"status"`
		AggregatedOutput string          `json:"aggregatedOutput"`
		ExitCode         *int            `json:"exitCode"`
		DurationMs       *int64          `json:"durationMs"`
		Server           string          `json:"server"`
		Tool             string          `json:"tool"`
		Arguments        json.RawMessage `json:"arguments"`
		Result           *struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
		Error   json.RawMessage `json:"error"`
		Path    string          `json:"path"`
		Changes []struct {
			Path string `json:"path"`
			Kind struct {
				Type string `json:"type"`
			} `json:"kind"`
			Diff string `json:"diff"`
		} `json:"changes"`
		Query  string `json:"query"`
		Action *struct {
			Type    string   `json:"type"`
			URL     string   `json:"url"`
			Queries []string `json:"queries"`
		} `json:"action"`
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Domain  string `json:"domain"`
			Snippet string `json:"snippet"`
		} `json:"results"`
	}
	if json.Unmarshal(raw, &v) != nil {
		return nil
	}
	item := map[string]any{"id": v.ID}
	switch kind {
	case "userMessage":
		var content []map[string]string
		for _, c := range v.Content {
			switch c.Type {
			case "text":
				content = append(content, map[string]string{"type": "text", "text": c.Text})
			case "localImage":
				content = append(content, map[string]string{"type": "local_image", "path": c.Path})
			}
		}
		item["type"], item["content"] = "UserMessage", content
	case "agentMessage":
		item["type"], item["content"], item["phase"] = "AgentMessage", []map[string]string{{"type": "text", "text": v.Text}}, v.Phase
	case "reasoning":
		item["type"], item["summary_text"] = "Reasoning", v.Summary
	case "commandExecution":
		item["type"], item["command"], item["status"], item["aggregated_output"] = "CommandExecution", []string{v.Command}, v.Status, v.AggregatedOutput
		if v.ExitCode != nil {
			item["exit_code"] = *v.ExitCode
		}
		if v.DurationMs != nil && *v.DurationMs >= 0 {
			item["duration"] = map[string]int64{"secs": *v.DurationMs / 1000, "nanos": *v.DurationMs % 1000 * 1_000_000}
		}
	case "fileChange":
		changes := map[string]map[string]string{}
		for _, c := range v.Changes {
			changes[c.Path] = map[string]string{"type": c.Kind.Type, "unified_diff": c.Diff}
		}
		item["type"], item["changes"], item["status"] = "FileChange", changes, v.Status
	case "mcpToolCall":
		item["type"], item["server"], item["tool"], item["arguments"], item["status"] = "McpToolCall", v.Server, v.Tool, v.Arguments, v.Status
		if v.Result != nil {
			item["result"] = v.Result
		}
		if len(v.Error) > 0 {
			item["error"] = v.Error
		}
	case "imageView":
		item["type"], item["path"] = "ImageView", v.Path
	case "webSearch":
		item["type"], item["kind"], item["query"], item["results"] = "Extension", "web.search", v.Query, v.Results
		if v.Action != nil {
			item["action"] = v.Action
		}
	case "contextCompaction":
		item["type"] = "ContextCompaction"
	default:
		return nil
	}
	return item
}
