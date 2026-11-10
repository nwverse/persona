package node

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
// Its transcript is that history written back as rollout lines — session_meta, then per finished turn task_started, an
// item_completed per item, and task_complete (with its error when it failed) or turn_aborted — so the transcript reader,
// the clients and every check on a rollout read it as any other (#3305). The file is
// <state dir>/codex-app-history/<home hash>-<thread>.jsonl. It grows as a rollout does: the turns finished since it was
// last written are appended (a turn still running is left for later), so a reader goes on where it was. A <file>.mark
// next to it records the last turn written, the App's change it was written for and the file's size; a file that is not
// what the mark says is written again from the start. It is brought up to date when the transcript is read (file) and,
// while the chat is watched, about once a second (pumpWatch), never by the list. Without sqlite3 or the history database
// there is none, as before.

// codexAppHistoryWait bounds one read of the history database; a long chat's turns stream in well under it.
const codexAppHistoryWait = 30 * time.Second

// codexAppHistoryRetry: after a failed write, the same change of the chat is not tried again for this long.
const codexAppHistoryRetry = 5 * time.Minute

var codexAppHistoryLocks sync.Map // file path → *sync.Mutex: one writer per chat

var codexAppHistoryFailed = struct {
	sync.Mutex
	byPath map[string]codexHistoryFailure
}{byPath: map[string]codexHistoryFailure{}}

type codexHistoryFailure struct {
	updated int64
	at      time.Time
}

// codexHistoryMark is what <file>.mark says: the last turn written (its rollout_ordinal; -1 for none), the App's change
// (updated_at_ms) it is up to date with, and the size the file had then (-1: no file, the chat has no finished turn).
type codexHistoryMark struct {
	Turn    int64 `json:"turn"`
	Updated int64 `json:"updated"`
	Size    int64 `json:"size"`
}

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

func codexHomeTag(home string) string {
	h := sha256.Sum256([]byte(home))
	return hex.EncodeToString(h[:6])
}

// codexAppHistoryFile is where the transcript of an App chat with no rollout is written ("" without a state directory,
// or in a scoped node, which reads transcripts only from its own directories).
func (n *Node) codexAppHistoryFile(src codexSource) string {
	if n.dir == "" || n.sessionScope != nil || !validID(src.thread) {
		return ""
	}
	return filepath.Join(n.dir, "codex-app-history", codexHomeTag(src.home)+"-"+src.thread+".jsonl")
}

// withoutGoneRollout clears a source's rollout when the file is gone: the registry keeps a path once known (the App
// deletes a rollout after moving its history into the database), as listedCodexSource does for the list.
func withoutGoneRollout(src codexSource, pass *codexListPass) codexSource {
	if src.path != "" {
		if _, err := pass.stat(src.path); err != nil && errors.Is(err, os.ErrNotExist) {
			src.path = ""
		}
	}
	return src
}

// codexAppHistoryPath: the transcript written for src, an App chat with no rollout, while it exists; else "".
func (n *Node) codexAppHistoryPath(src codexSource, pass *codexListPass) string {
	if _, ok := codexAppThreadOf(withoutGoneRollout(src, pass)); !ok {
		return ""
	}
	p := n.codexAppHistoryFile(src)
	if p == "" {
		return ""
	}
	if _, err := pass.stat(p); err != nil {
		return ""
	}
	return p
}

// codexAppHistoryChat: session id is an App chat with no rollout, whose transcript comes from the App's database.
func (n *Node) codexAppHistoryChat(id string) (codexSource, codexAppThread, bool) {
	src, ok := n.codexSource(id)
	if !ok {
		return codexSource{}, codexAppThread{}, false
	}
	src = withoutGoneRollout(src, nil)
	if src.path != "" || rolloutPath(src.home, src.thread) != "" {
		return codexSource{}, codexAppThread{}, false // a rollout is there: it is the transcript
	}
	t, ok := codexAppThreadOf(src)
	return src, t, ok
}

// refreshCodexAppHistory brings the transcript of session id up to date when it is an App chat with no rollout and the
// App's database changed it since: the turns finished since are appended, or the file is written anew.
func (n *Node) refreshCodexAppHistory(id string) {
	src, t, ok := n.codexAppHistoryChat(id)
	if !ok {
		return
	}
	p := n.codexAppHistoryFile(src)
	if p == "" {
		return
	}
	mu, _ := codexAppHistoryLocks.LoadOrStore(p, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()
	mark, size := readCodexHistoryMark(p)
	if mark.Updated == t.UpdatedAtMs && mark.Size == size {
		return // up to date
	}
	codexAppHistoryFailed.Lock()
	f, failed := codexAppHistoryFailed.byPath[p]
	codexAppHistoryFailed.Unlock()
	if failed && f.updated == t.UpdatedAtMs && time.Since(f.at) < codexAppHistoryRetry {
		return
	}
	db := codexHistoryDBPath(src.home)
	if db == "" {
		return
	}
	from, grow := int64(-1), false
	if mark.Size >= 0 && mark.Size == size && mark.Updated != 0 {
		from, grow = mark.Turn, true // the file is as last written: add what came since
	}
	ctx, cancel := context.WithTimeout(context.Background(), codexAppHistoryWait)
	defer cancel()
	last, err := writeCodexAppHistory(ctx, db, t, p, from, grow)
	if err != nil {
		codexAppHistoryFailed.Lock()
		codexAppHistoryFailed.byPath[p] = codexHistoryFailure{t.UpdatedAtMs, time.Now()}
		codexAppHistoryFailed.Unlock()
		n.logf("codex app history %s: %v", src.thread, err)
		return
	}
	codexAppHistoryFailed.Lock()
	delete(codexAppHistoryFailed.byPath, p)
	codexAppHistoryFailed.Unlock()
	_, size = readCodexHistoryMark(p)
	writeCodexHistoryMark(p, codexHistoryMark{Turn: last, Updated: t.UpdatedAtMs, Size: size})
	pruneCodexAppHistory(filepath.Dir(p), codexHomeTag(src.home), codexAppThreads(src.home))
}

// readCodexHistoryMark is p's mark and p's size now (-1 when there is no file).
func readCodexHistoryMark(p string) (codexHistoryMark, int64) {
	size := int64(-1)
	if st, err := os.Stat(p); err == nil {
		size = st.Size()
	}
	m := codexHistoryMark{Turn: -1, Size: -2} // no mark: nothing matches it
	if b, err := os.ReadFile(p + ".mark"); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m, size
}

func writeCodexHistoryMark(p string, m codexHistoryMark) {
	b, _ := json.Marshal(m)
	_ = os.WriteFile(p+".mark", b, 0o600)
}

// pruneCodexAppHistory removes the transcripts (and marks) of home's chats the App no longer lists, and temporary files
// a stopped write left behind.
func pruneCodexAppHistory(dir, tag string, live map[string]codexAppThread) {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".write-") {
			if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > time.Hour {
				os.Remove(filepath.Join(dir, name))
			}
			continue
		}
		if !strings.HasPrefix(name, tag+"-") {
			continue
		}
		thread := strings.TrimSuffix(strings.TrimSuffix(strings.TrimPrefix(name, tag+"-"), ".mark"), ".jsonl")
		if _, ok := live[thread]; !ok && live != nil {
			os.Remove(filepath.Join(dir, name))
		}
	}
}

// writeCodexAppHistory writes thread t's finished turns after turn ordinal from to path: appended when grow, else as a
// new file (through a temporary one) beginning with session_meta; none written when the thread has no finished turn and
// there is no file. It stops at the first turn still running. It returns the last turn written (from when none).
func writeCodexAppHistory(ctx context.Context, db string, t codexAppThread, path string, from int64, grow bool) (int64, error) {
	sqlite, err := exec.LookPath("sqlite3")
	if err != nil {
		return from, err
	}
	if !validID(t.ID) {
		return from, fmt.Errorf("bad thread id")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// One query, one snapshot: each turn (in order) with its items (in order). Every value is one line: JSON escapes
	// control characters, so the separator and a newline appear in none.
	q := fmt.Sprintf(`SELECT t.rollout_ordinal, t.turn_id, t.status, COALESCE(t.duration_ms, -1), COALESCE(t.error_json, ''), `+
		`COALESCE(i.created_at_ms, 0), COALESCE(i.item_type, ''), COALESCE(i.item_json, '') FROM thread_turns t `+
		`LEFT JOIN thread_items i ON i.thread_id = t.thread_id AND i.turn_id = t.turn_id `+
		`WHERE t.thread_id = '%s' AND t.rollout_ordinal > %d ORDER BY t.rollout_ordinal, i.rollout_ordinal`, t.ID, from)
	cmd := exec.CommandContext(ctx, sqlite, "-readonly", "-bail", "-noheader", "-list", "-separator", "\x1f", "-cmd", ".timeout 2000", db, q)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return from, err
	}
	if err := cmd.Start(); err != nil {
		return from, err
	}

	// Where the lines go, a finished turn at a time: appended to the file, or a new one (renamed over it at the end). A
	// write that stops halfway leaves the file unlike its mark: the next one writes it anew.
	var file *os.File
	if grow {
		file, err = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	} else if err = os.MkdirAll(filepath.Dir(path), 0o700); err == nil {
		file, err = os.CreateTemp(filepath.Dir(path), ".write-*")
	}
	if err != nil {
		return from, err
	}
	defer file.Close()
	if !grow {
		defer os.Remove(file.Name()) // renamed away when it is complete
	}
	w := bufio.NewWriterSize(file, 256<<10)
	line := func(at time.Time, typ string, payload any) {
		b, _ := json.Marshal(map[string]any{"timestamp": at.UTC().Format(time.RFC3339Nano), "type": typ, "payload": payload})
		w.Write(b)
		w.WriteByte('\n')
	}
	if !grow {
		line(time.UnixMilli(t.UpdatedAtMs), "session_meta", map[string]any{"id": t.ID, "cwd": t.Cwd, "originator": "Codex Desktop", "source": "vscode"})
	}
	type turn struct {
		ordinal       int64
		id, status    string
		durationMs    int64
		errJSON       string
		started, last time.Time
		items         [][2]any // time, item
	}
	last := from
	var cur *turn
	stopped := false
	finish := func() bool { // writes cur when it ended; false when it still runs (nothing after it is written)
		if cur == nil {
			return true
		}
		closing := map[string]any{"type": "task_complete", "turn_id": cur.id}
		switch cur.status {
		case "completed":
		case "failed":
			if json.Valid([]byte(cur.errJSON)) {
				closing["error"] = json.RawMessage(cur.errJSON)
			}
		case "interrupted":
			closing = map[string]any{"type": "turn_aborted", "turn_id": cur.id}
		default:
			return false // still running (inProgress), or a state not known yet
		}
		if cur.durationMs >= 0 && closing["type"] == "task_complete" {
			closing["duration_ms"] = cur.durationMs
		}
		line(cur.started, "event_msg", map[string]any{"type": "task_started", "turn_id": cur.id})
		for _, it := range cur.items {
			line(it[0].(time.Time), "event_msg", map[string]any{"type": "item_completed", "item": it[1]})
		}
		line(cur.last, "event_msg", closing)
		last = cur.ordinal
		return true
	}
	at := time.UnixMilli(t.UpdatedAtMs)
	r := bufio.NewReaderSize(stdout, 1<<20)
	var scanErr error
	for {
		row, err := r.ReadBytes('\n')
		if len(row) > 0 {
			parts := strings.SplitN(strings.TrimSuffix(string(row), "\n"), "\x1f", 8)
			if len(parts) == 8 {
				ordinal, _ := strconv.ParseInt(parts[0], 10, 64)
				if cur == nil || cur.ordinal != ordinal {
					if !finish() {
						stopped = true
						break
					}
					dur, _ := strconv.ParseInt(parts[3], 10, 64)
					cur = &turn{ordinal: ordinal, id: parts[1], status: parts[2], durationMs: dur, errJSON: parts[4], started: at, last: at}
				}
				if ms, _ := strconv.ParseInt(parts[5], 10, 64); ms > 0 {
					at = time.UnixMilli(ms)
					if len(cur.items) == 0 {
						cur.started = at
					}
					cur.last = at
				}
				if item := codexRolloutItem(parts[6], []byte(parts[7])); item != nil {
					cur.items = append(cur.items, [2]any{at, item})
				}
			}
		}
		if err != nil {
			if err != io.EOF {
				scanErr = err
			}
			break
		}
	}
	if !stopped {
		stopped = !finish()
	}
	if stopped {
		cancel() // stopped at a running turn: the rest of the query is not waited for
	}
	waitErr := cmd.Wait()
	if scanErr != nil {
		return from, scanErr
	}
	if waitErr != nil && !stopped {
		return from, fmt.Errorf("%v: %s", waitErr, strings.TrimSpace(stderr.String()))
	}
	if err := w.Flush(); err != nil {
		return from, err
	}
	if err := file.Close(); err != nil {
		return from, err
	}
	if grow {
		return last, nil
	}
	if last == from { // no finished turn: no transcript, as before
		return from, nil
	}
	return last, os.Rename(file.Name(), path)
}

// codexShellArgv splits the App's command line ("/bin/bash -lc 'git status'") into the argv a rollout carries, so the
// transcript reader drops the login shell around it as it does for a rollout's (transcript.shellCommand).
func codexShellArgv(command string) []string {
	fields := strings.SplitN(command, " ", 3)
	if len(fields) != 3 || (fields[1] != "-lc" && fields[1] != "-c") || !strings.HasPrefix(fields[0], "/") && !strings.HasSuffix(fields[0], "sh") {
		return []string{command}
	}
	inner := fields[2]
	switch {
	case len(inner) >= 2 && inner[0] == '\'' && inner[len(inner)-1] == '\'':
		inner = strings.ReplaceAll(inner[1:len(inner)-1], `'\''`, `'`)
	case len(inner) >= 2 && inner[0] == '"' && inner[len(inner)-1] == '"':
		if s, err := strconv.Unquote(inner); err == nil {
			inner = s
		}
	}
	return []string{fields[0], fields[1], inner}
}

// codexRolloutItem turns one of the app-server's ThreadItems (v2, camelCase) into the item a rollout's item_completed
// carries (transcript/codex.go codexItem); nil for a kind the chat does not show (a sleep, a subagent's progress) and for
// an item still running.
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
		Error     json.RawMessage `json:"error"`
		Path      string          `json:"path"`
		SavedPath string          `json:"savedPath"`
		Changes   []struct {
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
	if json.Unmarshal(raw, &v) != nil || v.Status == "inProgress" {
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
		item["type"], item["command"], item["status"], item["aggregated_output"] = "CommandExecution", codexShellArgv(v.Command), v.Status, v.AggregatedOutput
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
	case "imageGeneration": // the picture it made, shown as a rollout shows an image it looked at
		if v.SavedPath == "" {
			return nil
		}
		item["type"], item["path"] = "ImageView", v.SavedPath
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
