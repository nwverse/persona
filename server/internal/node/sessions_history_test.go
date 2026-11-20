package node

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
	"github.com/nwverse/persona/server/internal/sessionhistory"
)

// historyRuntime answers sessions.history from rows the way the SDK's rt.history does (window by active_at, else
// updated_at; newest first, ties by id), with an offset cursor of its own.
func historyRuntime(rows []map[string]any) func(method string, params json.RawMessage) (any, error) {
	at := func(r map[string]any) time.Time {
		for _, k := range []string{"active_at", "updated_at"} {
			if s, ok := r[k].(string); ok {
				t, _ := time.Parse(time.RFC3339Nano, s)
				return t
			}
		}
		return time.Time{}
	}
	sorted := append([]map[string]any(nil), rows...)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := at(sorted[i]).UnixMilli(), at(sorted[j]).UnixMilli()
		if a != b {
			return a > b
		}
		return sorted[i]["session_id"].(string) < sorted[j]["session_id"].(string)
	})
	return func(method string, params json.RawMessage) (any, error) {
		if method != "sessions.history" {
			return map[string]any{"ok": true, "items": []any{}}, nil
		}
		var p struct {
			Since, Until, Cursor string
			Limit                int
		}
		_ = json.Unmarshal(params, &p)
		since, _ := time.Parse(time.RFC3339Nano, p.Since)
		until, _ := time.Parse(time.RFC3339Nano, p.Until)
		var in []map[string]any
		for _, r := range sorted {
			if t := at(r).UnixMilli(); t >= since.UnixMilli() && t < until.UnixMilli() {
				in = append(in, r)
			}
		}
		from, _ := strconv.Atoi(p.Cursor)
		to := min(len(in), from+p.Limit)
		next := ""
		if to < len(in) {
			next = strconv.Itoa(to)
		}
		return map[string]any{"rows": in[from:to], "next_cursor": next, "complete": true}, nil
	}
}

func historyCall(t *testing.T, n *Node, params map[string]any) sessionhistory.Answer {
	t.Helper()
	raw, err := nodeCall(t, n, "sessions.history", params)
	if err != nil {
		t.Fatal(err)
	}
	var ans sessionhistory.Answer
	if err := json.Unmarshal(raw, &ans); err != nil {
		t.Fatal(err)
	}
	return ans
}

// A runtime that declares caps.history is listed in full over a 90-day window, page by page, far past what it pushes
// (rt.recent: 7 days, 80); archived sessions are placed by when they were last active, deleted ones never come; a
// runtime without the capability is not asked and is marked honored: false.
func TestSessionsHistoryPagesARuntimeInFullAndMarksOneWithoutIt(t *testing.T) {
	n, _ := runtimeNode(t)
	now := time.Now().UTC()
	var rows []map[string]any
	for i := 0; i < 450; i++ { // one every 4 hours: 75 days
		rows = append(rows, map[string]any{"session_id": fmt.Sprintf("s%03d", i), "status": "offline",
			"updated_at": now.Add(-time.Duration(i) * 4 * time.Hour).Format(time.RFC3339Nano)})
	}
	// Archived just now, last active 80 days ago: in the window by active_at, and last of all.
	rows = append(rows, map[string]any{"session_id": "archived-old", "status": "done", "archived": true,
		"updated_at": now.Add(-time.Minute).Format(time.RFC3339Nano), "active_at": now.Add(-80 * 24 * time.Hour).Format(time.RFC3339Nano),
		"origin": "automation", "user_messages": 0})
	// Archived just now, last active 100 days ago: out of the window though its updated_at is in it.
	rows = append(rows, map[string]any{"session_id": "archived-older", "archived": true,
		"updated_at": now.Add(-time.Minute).Format(time.RFC3339Nano), "active_at": now.Add(-100 * 24 * time.Hour).Format(time.RFC3339Nano)})
	rows = append(rows, map[string]any{"session_id": "gone", "status": "deleted", "updated_at": now.Add(-time.Hour).Format(time.RFC3339Nano)})
	hello := dshHello()
	hello["caps"].(map[string]any)["history"] = true
	withHistory := dialRuntime(t, n, hello)
	withHistory.mu.Lock()
	withHistory.answer = historyRuntime(rows)
	withHistory.mu.Unlock()
	without := dialRuntime(t, n, map[string]any{"runtime": "toy", "name": "Toy", "caps": map[string]any{}})
	waitFor(t, "both ready", func() bool { return len(n.runtimes.ready()) == 2 })

	since := now.Add(-90 * 24 * time.Hour).Format(time.RFC3339Nano)
	var got []sessionhistory.Row
	cursor, pages := "", 0
	for {
		ans := historyCall(t, n, map[string]any{"since": since, "source": "runtime", "limit": 200, "cursor": cursor})
		pages++
		got = append(got, ans.Rows...)
		if pages == 1 {
			states := map[string]sessionhistory.SourceState{}
			for _, s := range ans.Sources {
				states[s.Runtime] = s
			}
			if s := states["dsh"]; !s.Honored || !s.Complete {
				t.Errorf("the runtime with listHistory is honored and complete: %+v", s)
			}
			if s := states["toy"]; s.Honored || s.Reason != sessionhistory.ReasonUnsupported {
				t.Errorf("the runtime without it: honored false, unsupported: %+v", s)
			}
			if ans.Complete {
				t.Error("with one source unsupported the answer is not complete")
			}
		}
		if ans.NextCursor == "" {
			break
		}
		cursor = ans.NextCursor
		if pages > 10 {
			t.Fatal("no last page")
		}
	}
	if pages != 3 || len(got) != 451 {
		t.Fatalf("%d pages, %d rows; want 3 pages of the 450 and the archived one in the window", pages, len(got))
	}
	seen := map[string]bool{}
	for i, r := range got {
		if seen[r.Session.SessionID] {
			t.Fatalf("%s twice", r.Session.SessionID)
		}
		seen[r.Session.SessionID] = true
		if r.Source != sessionhistory.SourceRuntime || r.Runtime != "dsh" || r.Session.Agent != "dsh" {
			t.Fatalf("row %d: source %q runtime %q agent %q", i, r.Source, r.Runtime, r.Session.Agent)
		}
		if i < 450 && r.Session.SessionID != fmt.Sprintf("s%03d", i) {
			t.Fatalf("row %d is %s: newest first", i, r.Session.SessionID)
		}
	}
	last := got[450]
	if last.Session.SessionID != "archived-old" || !last.Session.Archived || last.Origin != sessionhistory.OriginAutomation ||
		last.UserMessages == nil || *last.UserMessages != 0 || last.ActiveAt.IsZero() {
		t.Errorf("the archived one, ordered by active_at with its fields kept: %+v", last)
	}
	if seen["archived-older"] || seen["gone"] {
		t.Error("a session last active before the window and a deleted one are not listed")
	}
	if len(without.got("sessions.history")) != 0 {
		t.Error("a runtime without caps.history is not asked")
	}
	for _, s := range listedSessions(t, n) {
		if s.SessionID == "s100" {
			t.Error("history rows never enter the live list")
		}
	}
}

func listedSessions(t *testing.T, n *Node) []proto.Session {
	t.Helper()
	raw, err := nodeCall(t, n, "sessions.list", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Sessions []proto.Session `json:"sessions"`
	}
	_ = json.Unmarshal(raw, &out)
	return out.Sessions
}

// A session found through history that its runtime never listed (archived there) is read the usual way: the node asks
// the runtimes who has it and sends session.items to the one that answers.
func TestHistoryRowItemsGoToTheRuntime(t *testing.T) {
	n, _ := runtimeNode(t)
	const archived = "aaaaaaaa-2222-4333-8444-555566667777"
	hello := dshHello()
	hello["caps"].(map[string]any)["history"] = true
	host := dialRuntime(t, n, hello)
	host.mu.Lock()
	list := historyRuntime([]map[string]any{{"session_id": archived, "archived": true, "updated_at": time.Now().UTC().Format(time.RFC3339Nano)}})
	host.answer = func(method string, params json.RawMessage) (any, error) {
		var p struct {
			SessionID string `json:"session_id"`
		}
		_ = json.Unmarshal(params, &p)
		switch {
		case method == "sessions.history":
			return list(method, params)
		case method == "session.items" && p.SessionID == archived:
			return map[string]any{"items": []any{map[string]any{"id": "1#0", "seq": 1, "kind": "text", "role": "user", "text": "hello"}}, "to_seq": 1}, nil
		case method == "session.items":
			return nil, &rpc.Error{Message: "no session " + p.SessionID, Code: "not_found"}
		}
		return map[string]any{"ok": true}, nil
	}
	host.mu.Unlock()
	_ = host.peer.Notify("runtime.sessions", map[string]any{"sessions": []map[string]any{}})
	waitFor(t, "ready", func() bool { return len(n.runtimes.ready()) == 1 })

	ans := historyCall(t, n, map[string]any{"since": time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339Nano), "source": "runtime"})
	if len(ans.Rows) != 1 || ans.Rows[0].Session.SessionID != archived || !ans.Rows[0].Session.Archived {
		t.Fatalf("history lists the archived session: %+v", ans.Rows)
	}
	raw, err := nodeCall(t, n, "session.items", map[string]any{"session_id": archived, "turns": 2})
	if err != nil {
		t.Fatalf("items of a history row: %v", err)
	}
	if !strings.Contains(string(raw), "hello") {
		t.Fatalf("the runtime's items come back: %s", raw)
	}
	var asked []string
	for _, p := range host.got("session.items") {
		var v struct {
			SessionID string `json:"session_id"`
		}
		_ = json.Unmarshal(p, &v)
		asked = append(asked, v.SessionID)
	}
	if len(asked) == 0 || asked[len(asked)-1] != archived {
		t.Fatalf("session.items went to the runtime for it: %v", asked)
	}
}

// writeHistoryTranscript writes a Claude transcript last active at `at` (its lines' times and its file time).
func writeHistoryTranscript(t *testing.T, dir, id, prompt string, at time.Time) {
	t.Helper()
	ts := at.UTC().Format(time.RFC3339Nano)
	body := fmt.Sprintf(`{"type":"user","uuid":"u1","timestamp":%q,"cwd":"/src/app","message":{"role":"user","content":[{"type":"text","text":%q}]}}`+"\n"+
		`{"type":"assistant","uuid":"a1","timestamp":%q,"message":{"role":"assistant","content":[{"type":"text","text":"done"}]}}`+"\n", ts, prompt, ts)
	p := filepath.Join(dir, id+".jsonl")
	if err := os.WriteFile(p, []byte(body+strings.Repeat(" ", 4096)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, at, at); err != nil {
		t.Fatal(err)
	}
}

// The node's own Claude Code transcripts are listed by the window, whatever their age: the live list stops at 7 days,
// history does not. Each row's active_at is its last record's time.
func TestSessionsHistoryListsClaudeTranscriptsOlderThanAWeek(t *testing.T) {
	n, _ := claudeHome(t)
	n.metas, n.bgScans = map[string]quickMeta{}, map[string]*bgScan{}
	dir := filepath.Join(n.home, ".claude", "projects", "-src-app")
	_ = os.MkdirAll(dir, 0o700)
	now := time.Now()
	ids := map[int]string{3: "11111111-0000-4000-8000-000000000003", 20: "11111111-0000-4000-8000-000000000020",
		60: "11111111-0000-4000-8000-000000000060", 120: "11111111-0000-4000-8000-000000000120"}
	for days, id := range ids {
		writeHistoryTranscript(t, dir, id, fmt.Sprintf("work from %d days ago", days), now.Add(-time.Duration(days)*24*time.Hour))
	}
	live := map[string]bool{}
	for _, s := range n.scanOffline() {
		live[s.SessionID] = true
	}
	if !live[ids[3]] || live[ids[20]] || live[ids[60]] {
		t.Fatalf("the live list keeps only the last week: %v", live)
	}
	ans, err := n.sessionsHistory(t.Context(), mustJSON(t, map[string]any{"since": now.Add(-90 * 24 * time.Hour).UTC().Format(time.RFC3339Nano)}))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range ans.Rows {
		got = append(got, r.Session.SessionID)
		if r.Source != sessionhistory.SourceClaude || r.ActiveAt.IsZero() {
			t.Errorf("%s: source %q, active_at %v", r.Session.SessionID, r.Source, r.ActiveAt)
		}
	}
	if want := []string{ids[3], ids[20], ids[60]}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("history over 90 days: %v, want %v (newest first; 120 days is out)", got, want)
	}
	if !ans.Complete {
		t.Errorf("the node's own sources are complete: %+v", ans.Sources)
	}
	// A window ending before the newest one.
	ans, _ = n.sessionsHistory(t.Context(), mustJSON(t, map[string]any{"since": now.Add(-90 * 24 * time.Hour).UTC().Format(time.RFC3339Nano),
		"until": now.Add(-10 * 24 * time.Hour).UTC().Format(time.RFC3339Nano), "source": "claude", "limit": 1}))
	if len(ans.Rows) != 1 || ans.Rows[0].Session.SessionID != ids[20] || ans.NextCursor == "" {
		t.Fatalf("first page of [90 days, 10 days): %+v", ans)
	}
	ans, _ = n.sessionsHistory(t.Context(), mustJSON(t, map[string]any{"since": now.Add(-90 * 24 * time.Hour).UTC().Format(time.RFC3339Nano),
		"until": now.Add(-10 * 24 * time.Hour).UTC().Format(time.RFC3339Nano), "source": "claude", "limit": 1, "cursor": ans.NextCursor}))
	if len(ans.Rows) != 1 || ans.Rows[0].Session.SessionID != ids[60] || ans.NextCursor != "" {
		t.Fatalf("second and last page: %+v", ans)
	}
}

// The node's own Codex rollouts are listed by the window, whatever their age, archived ones as archived; a subagent's
// thread is not a conversation of its own.
func TestSessionsHistoryListsCodexRolloutsOlderThanTwoWeeks(t *testing.T) {
	n := remoteTestNode(t)
	home := t.TempDir()
	n.configureCodex(proto.CodexConfig{Homes: []string{home}})
	now := time.Now()
	write := func(dir, id, meta string, at time.Time) {
		t.Helper()
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		ts := at.UTC().Format(time.RFC3339Nano)
		body := fmt.Sprintf(`{"timestamp":%q,"type":"session_meta","payload":%s}`+"\n", ts, meta) +
			fmt.Sprintf(`{"timestamp":%q,"type":"event_msg","payload":{"type":"user_message","message":"fix the build"}}`+"\n", ts)
		p := filepath.Join(dir, "rollout-"+at.Format("2006-01-02T15-04-05")+"-"+id+".jsonl")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		_ = os.Chtimes(p, at, at)
	}
	day := func(d int) (string, time.Time) {
		at := now.Add(-time.Duration(d) * 24 * time.Hour)
		return filepath.Join(home, "sessions", at.Format("2006"), at.Format("01"), at.Format("02")), at
	}
	const old, archived, sub, tooOld = "0f000000-0000-4000-8000-0000000000c1", "0f000000-0000-4000-8000-0000000000c2",
		"0f000000-0000-4000-8000-0000000000c3", "0f000000-0000-4000-8000-0000000000c4"
	dir, at := day(30)
	write(dir, old, `{"id":"`+old+`","cwd":"/work/app","source":"cli"}`, at)
	write(dir, sub, `{"id":"`+sub+`","cwd":"/work/app","source":{"subagent":{"thread_spawn":{"parent_thread_id":"`+old+`"}}}}`, at)
	_, at = day(45)
	write(filepath.Join(home, "archived_sessions"), archived, `{"id":"`+archived+`","cwd":"/work/app","source":"cli"}`, at)
	dir, at = day(200)
	write(dir, tooOld, `{"id":"`+tooOld+`","cwd":"/work/app","source":"cli"}`, at)

	ans, err := n.sessionsHistory(t.Context(), mustJSON(t, map[string]any{"since": now.Add(-90 * 24 * time.Hour).UTC().Format(time.RFC3339Nano), "source": "codex"}))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range ans.Rows {
		got = append(got, r.Session.SourceThreadID)
		if r.Source != sessionhistory.SourceCodex || r.Session.Archived != (r.Session.SourceThreadID == archived) {
			t.Errorf("%s: source %q archived %v", r.Session.SourceThreadID, r.Source, r.Session.Archived)
		}
	}
	if strings.Join(got, ",") != old+","+archived {
		t.Fatalf("history over 90 days: %v, want the 30-day one and the archived 45-day one", got)
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
