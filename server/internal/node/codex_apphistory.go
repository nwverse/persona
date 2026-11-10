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

	"github.com/nwverse/persona/server/internal/link"
)

// A Codex App chat whose rollout is gone (codex_appdb.go) keeps its history in <CODEX_HOME>/thread_history_<n>.sqlite:
// thread_turns, and thread_items holding the app-server's ThreadItems (v2: userMessage, agentMessage, commandExecution…).
// Its transcript is that history written back as rollout lines — session_meta, then per finished turn task_started, an
// item_completed per item, and task_complete (with its error when it failed) or turn_aborted — so the transcript reader,
// the clients and every check on a rollout read it as any other (#3305). The file is
// <state dir>/codex-app-history/<home hash>-<thread>.jsonl. It grows as a rollout does: the turns finished since it was
// last written are appended (the last turn while it still runs is left for later; one that never finished but has turns
// after it is written as interrupted), so a reader goes on where it was. <file>.mark records what was written; a file
// unlike its mark, or whose last turn is no longer in the database as it was, is written again from the start. It is
// brought up to date when the transcript is read (file) and, while the chat is watched, about once a second (pumpWatch),
// never by the list; one writer at a time, across processes too (<file>.lock). The database is read while the App has it
// closed too (codexDBTarget). Without sqlite3 or the history database there is none, as before.

// codexAppHistoryWait bounds one read of the history database; a long chat's turns stream in well under it.
const codexAppHistoryWait = 30 * time.Second

// codexAppHistoryRetry: after a failed write, the same change of the chat is not tried again for this long.
const codexAppHistoryRetry = 5 * time.Minute

var codexAppHistoryFailed = struct {
	sync.Mutex
	byPath map[string]codexHistoryFailure
}{byPath: map[string]codexHistoryFailure{}}

type codexHistoryFailure struct {
	key string // the change it failed for: updated_at_ms and the history database's files
	at  time.Time
}

// codexHistoryMark is what <file>.mark says: the last turn written (its rollout_ordinal and turn_id; -1 and "" for none),
// the App's change (updated_at_ms) it is up to date with, the size the file had then (-1: no file, the chat has no
// finished turn), and, when the last turn still ran, the history database's files then (Pending, History): the App may
// finish the turn without changing updated_at_ms again.
type codexHistoryMark struct {
	Turn    int64  `json:"turn"`
	TurnID  string `json:"turn_id"`
	Updated int64  `json:"updated"`
	Size    int64  `json:"size"`
	Pending bool   `json:"pending,omitempty"`
	History string `json:"history,omitempty"`
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

// codexAppHistoryDir is where the transcripts of App chats with no rollout are written ("" without a state directory, or
// in a scoped node, which reads transcripts only from its own directories).
func (n *Node) codexAppHistoryDir() string {
	if n.dir == "" || n.sessionScope != nil {
		return ""
	}
	return filepath.Join(n.dir, "codex-app-history")
}

// codexAppHistoryFile is where the transcript of src, an App chat with no rollout, is written ("" when none can be).
func (n *Node) codexAppHistoryFile(src codexSource) string {
	dir := n.codexAppHistoryDir()
	if dir == "" || !validID(src.thread) {
		return ""
	}
	return filepath.Join(dir, codexHomeTag(src.home)+"-"+src.thread+".jsonl")
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
	p := n.codexAppHistoryFile(src)
	if p == "" {
		return ""
	}
	if _, ok := codexAppThreadOf(withoutGoneRollout(src, pass)); !ok {
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
// App's databases changed it since: the turns finished since are appended, or the file is written anew. One writer at a
// time, across processes too (two nodes on one computer share the state directory); another's write is not waited for.
func (n *Node) refreshCodexAppHistory(id string) {
	src, t, ok := n.codexAppHistoryChat(id)
	if !ok {
		return
	}
	p := n.codexAppHistoryFile(src)
	if p == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return
	}
	release, err := link.LockFile(p+".lock", false)
	if err != nil {
		return // another process writes it now
	}
	defer release()
	mark, size := readCodexHistoryMark(p)
	db := codexHistoryDBPath(src.home)
	history := ""
	if db != "" {
		history = fileStamp(db) + fileStamp(db+"-wal")
	}
	if mark.Updated == t.UpdatedAtMs && mark.Size == size && (!mark.Pending || mark.History == history) {
		return // up to date
	}
	if db == "" {
		return
	}
	key := fmt.Sprintf("%d %s", t.UpdatedAtMs, history)
	codexAppHistoryFailed.Lock()
	f, failed := codexAppHistoryFailed.byPath[p]
	codexAppHistoryFailed.Unlock()
	if failed && f.key == key && time.Since(f.at) < codexAppHistoryRetry {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), codexAppHistoryWait)
	defer cancel()
	grow := mark.Size >= 0 && mark.Size == size && mark.TurnID != ""
	res, err := writeCodexAppHistory(ctx, db, t, p, mark, grow)
	if errors.Is(err, errCodexHistoryChanged) { // a written turn is not what it was (the App rolled back): anew
		res, err = writeCodexAppHistory(ctx, db, t, p, codexHistoryMark{Turn: -1}, false)
	}
	if err == nil {
		_, size = readCodexHistoryMark(p)
		m := codexHistoryMark{Turn: res.turn, TurnID: res.turnID, Updated: t.UpdatedAtMs, Size: size, Pending: res.pending}
		if res.pending {
			m.History = history
		}
		err = writeCodexHistoryMark(p, m)
	}
	codexAppHistoryFailed.Lock()
	if err != nil {
		codexAppHistoryFailed.byPath[p] = codexHistoryFailure{key, time.Now()}
	} else {
		delete(codexAppHistoryFailed.byPath, p)
	}
	codexAppHistoryFailed.Unlock()
	if err != nil {
		n.logf("codex app history %s: %v", src.thread, err)
		return
	}
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

func writeCodexHistoryMark(p string, m codexHistoryMark) error {
	b, _ := json.Marshal(m)
	return os.WriteFile(p+".mark", b, 0o600)
}

// pruneCodexAppHistory removes the files of home's chats the App no longer lists, and temporary files a stopped write left
// behind.
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
		if live == nil || !strings.HasPrefix(name, tag+"-") {
			continue
		}
		thread := strings.TrimPrefix(name, tag+"-")
		for _, ext := range []string{".lock", ".mark", ".jsonl"} {
			thread = strings.TrimSuffix(thread, ext)
		}
		if _, ok := live[thread]; !ok {
			os.Remove(filepath.Join(dir, name))
		}
	}
}

// errCodexHistoryChanged: a turn the file has is not in the database as it was (the App rolled the chat back).
var errCodexHistoryChanged = errors.New("a written turn changed")

type codexHistoryWrite struct {
	turn    int64  // the last turn written (rollout_ordinal); the mark's when none was
	turnID  string // its turn_id
	pending bool   // the last turn still runs: not written
}

// writeCodexAppHistory writes thread t's turns to path: when grow, those after the mark's last turn, appended (that turn
// must still be the first the database gives, else errCodexHistoryChanged); else all of them, as a new file (through a
// temporary one renamed over it) beginning with session_meta, none written when no turn finished. A finished turn is
// written as it streams in; the last turn while it still runs is left for later (pending); one that never finished but
// has turns after it is written as interrupted.
func writeCodexAppHistory(ctx context.Context, db string, t codexAppThread, path string, mark codexHistoryMark, grow bool) (codexHistoryWrite, error) {
	res := codexHistoryWrite{turn: -1}
	if grow {
		res = codexHistoryWrite{turn: mark.Turn, turnID: mark.TurnID}
	}
	sqlite, err := exec.LookPath("sqlite3")
	if err != nil {
		return res, err
	}
	if !validID(t.ID) {
		return res, fmt.Errorf("bad thread id")
	}
	// The output first: a process once started is always waited for.
	var file *os.File
	if grow {
		file, err = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	} else {
		file, err = os.CreateTemp(filepath.Dir(path), ".write-*")
	}
	if err != nil {
		return res, err
	}
	defer file.Close()
	if !grow {
		defer os.Remove(file.Name()) // renamed away once complete
	}
	w := bufio.NewWriterSize(file, 256<<10)
	line := func(at time.Time, typ string, payload any) {
		b, _ := json.Marshal(map[string]any{"timestamp": at.UTC().Format(time.RFC3339Nano), "type": typ, "payload": payload})
		w.Write(b)
		w.WriteByte('\n')
	}

	from := int64(-1)
	if grow {
		from = mark.Turn // read again: it must still be the turn written
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// One query, one snapshot: each turn (in order) with its items (in order). Every value is one line: JSON escapes
	// control characters, so neither the separator nor a newline appears in one.
	q := fmt.Sprintf(`SELECT t.rollout_ordinal, t.turn_id, t.status, COALESCE(t.duration_ms, -1), COALESCE(t.error_json, ''), `+
		`COALESCE(i.created_at_ms, 0), COALESCE(i.item_type, ''), COALESCE(i.item_json, '') FROM thread_turns t `+
		`LEFT JOIN thread_items i ON i.thread_id = t.thread_id AND i.turn_id = t.turn_id `+
		`WHERE t.thread_id = '%s' AND t.rollout_ordinal >= %d ORDER BY t.rollout_ordinal, i.rollout_ordinal`, t.ID, from)
	cmd := exec.CommandContext(ctx, sqlite, "-readonly", "-bail", "-noheader", "-list", "-separator", "\x1f", "-cmd", ".timeout 2000", codexDBTarget(db), q)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return res, err
	}
	if err := cmd.Start(); err != nil {
		return res, err
	}

	type turn struct {
		ordinal     int64
		id, status  string
		durationMs  int64
		errJSON     string
		last        time.Time
		final, done bool     // done: written before (the mark's turn)
		held        [][2]any // a turn not finished: its items, written only once a later turn shows it ended
		heldStart   time.Time
	}
	metaWritten := grow
	startTurn := func(c *turn, at time.Time) {
		if !metaWritten {
			line(at, "session_meta", map[string]any{"id": t.ID, "cwd": t.Cwd, "originator": "Codex Desktop", "source": "vscode"})
			metaWritten = true
		}
		line(at, "event_msg", map[string]any{"type": "task_started", "turn_id": c.id})
	}
	closeTurn := func(c *turn, aborted bool) {
		closing := map[string]any{"type": "task_complete", "turn_id": c.id}
		switch {
		case aborted || c.status == "interrupted":
			closing = map[string]any{"type": "turn_aborted", "turn_id": c.id}
		case c.status == "failed":
			if json.Valid([]byte(c.errJSON)) {
				closing["error"] = json.RawMessage(c.errJSON)
			} else {
				closing["error"] = map[string]string{"message": "The turn failed."}
			}
		}
		if c.durationMs >= 0 && closing["type"] == "task_complete" {
			closing["duration_ms"] = c.durationMs
		}
		line(c.last, "event_msg", closing)
		res.turn, res.turnID = c.ordinal, c.id
	}
	// ended: c is followed by a later turn, so it is over.
	ended := func(c *turn) {
		switch {
		case c == nil || c.done:
		case c.final:
			closeTurn(c, false)
		default:
			startTurn(c, c.heldStart)
			for _, it := range c.held {
				line(it[0].(time.Time), "event_msg", map[string]any{"type": "item_completed", "item": it[1]})
			}
			closeTurn(c, true)
		}
	}
	at := time.UnixMilli(t.UpdatedAtMs)
	var cur *turn
	changed := false
	r := bufio.NewReaderSize(stdout, 1<<20)
	var readErr error
	for !changed {
		row, err := r.ReadBytes('\n')
		if parts := strings.SplitN(strings.TrimSuffix(string(row), "\n"), "\x1f", 8); len(parts) == 8 {
			ordinal, _ := strconv.ParseInt(parts[0], 10, 64)
			if ms, _ := strconv.ParseInt(parts[5], 10, 64); ms > 0 {
				at = time.UnixMilli(ms)
			}
			if cur == nil || cur.ordinal != ordinal {
				if grow && cur == nil && (ordinal != mark.Turn || parts[1] != mark.TurnID) {
					changed = true
					continue
				}
				ended(cur)
				dur, _ := strconv.ParseInt(parts[3], 10, 64)
				status := parts[2]
				cur = &turn{ordinal: ordinal, id: parts[1], status: status, durationMs: dur, errJSON: parts[4], last: at, heldStart: at,
					final: status == "completed" || status == "failed" || status == "interrupted", done: grow && cur == nil}
				if cur.final && !cur.done {
					startTurn(cur, at)
				}
			}
			cur.last = at
			if item := codexRolloutItem(parts[6], []byte(parts[7])); item != nil && !cur.done {
				if cur.final {
					line(at, "event_msg", map[string]any{"type": "item_completed", "item": item})
				} else {
					cur.held = append(cur.held, [2]any{at, item})
				}
			}
		}
		if err != nil {
			if err != io.EOF {
				readErr = err
			}
			break
		}
	}
	if changed {
		cancel() // the rest of the query is not needed
	}
	waitErr := cmd.Wait()
	switch {
	case changed:
		return res, errCodexHistoryChanged
	case readErr != nil:
		return res, readErr
	case waitErr != nil:
		return res, fmt.Errorf("%v: %s", waitErr, strings.TrimSpace(stderr.String()))
	case grow && cur == nil:
		return res, errCodexHistoryChanged // the mark's turn is gone
	}
	if cur != nil && !cur.done {
		if cur.final {
			closeTurn(cur, false)
		} else {
			res.pending = true // the last turn still runs
		}
	}
	if err := w.Flush(); err != nil {
		return res, err
	}
	if err := file.Close(); err != nil {
		return res, err
	}
	if grow || res.turn < 0 { // appended; or no finished turn: no transcript, as before
		return res, nil
	}
	return res, os.Rename(file.Name(), path)
}

// codexShellArgv splits the App's command line ("/bin/bash -lc 'git status'") into the argv a rollout carries, so the
// transcript reader drops the login shell around it as it does for a rollout's (transcript.shellCommand). The command is
// one shell word, unquoted as a POSIX shell does; one that is not (several words, an open quote) is kept whole.
func codexShellArgv(command string) []string {
	fields := strings.SplitN(command, " ", 3)
	if len(fields) != 3 || (fields[1] != "-lc" && fields[1] != "-c") || !strings.HasPrefix(fields[0], "/") && !strings.HasSuffix(fields[0], "sh") {
		return []string{command}
	}
	if inner, ok := shellWord(fields[2]); ok {
		return []string{fields[0], fields[1], inner}
	}
	return []string{command}
}

// shellWord unquotes s as one POSIX shell word: '…' as it is, "…" with \\ \" \$ \` and a backslashed newline, a
// backslash outside quotes escaping the next character. false when s is not one whole word.
func shellWord(s string) (string, bool) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '\'':
			j := strings.IndexByte(s[i+1:], '\'')
			if j < 0 {
				return "", false
			}
			b.WriteString(s[i+1 : i+1+j])
			i += j + 1
		case '"':
			for i++; ; i++ {
				if i >= len(s) {
					return "", false
				}
				if s[i] == '"' {
					break
				}
				if s[i] == '\\' && i+1 < len(s) && strings.IndexByte("\\\"$`\n", s[i+1]) >= 0 {
					i++
					if s[i] == '\n' {
						continue
					}
				}
				b.WriteByte(s[i])
			}
		case '\\':
			if i+1 >= len(s) {
				return "", false
			}
			i++
			b.WriteByte(s[i])
		case ' ', '\t', '\n':
			return "", false // a second word
		default:
			b.WriteByte(c)
		}
	}
	return b.String(), true
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
