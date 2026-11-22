package node

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/proto"
)

// forkLines is a transcript of two turns: the first one has a tool call and its result after the reply's text.
const forkLines = `{"type":"user","uuid":"u1","sessionId":"S","message":{"role":"user","content":"remember APPLE"}}
{"type":"assistant","uuid":"a1","sessionId":"S","message":{"role":"assistant","content":[{"type":"text","text":"OK"}]}}
{"type":"assistant","uuid":"a2","sessionId":"S","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{}}]}}
{"type":"user","uuid":"r1","sessionId":"S","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"done"}]}}
{"type":"assistant","uuid":"a3","sessionId":"S","message":{"role":"assistant","content":[{"type":"text","text":"saved"}]}}
{"type":"user","uuid":"u2","sessionId":"S","message":{"role":"user","content":"remember BANANA"}}
{"type":"assistant","uuid":"a4","sessionId":"S","message":{"role":"assistant","content":[{"type":"text","text":"OK"}]}}
`

func forkFixture(t *testing.T) (*Node, string, string) {
	t.Helper()
	n, home := testNode(t)
	n.bgScans, n.metas = map[string]*bgScan{}, map[string]quickMeta{}
	dir := filepath.Join(home, ".claude", "projects", "-work")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	id := "11111111-2222-4333-8444-555555555555"
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(strings.ReplaceAll(forkLines, `"S"`, `"`+id+`"`)), 0o600); err != nil {
		t.Fatal(err)
	}
	return n, dir, id
}

func readUUIDs(t *testing.T, path, wantID string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var e struct{ UUID, SessionID, Type string }
		if err := json.Unmarshal([]byte(l), &e); err != nil {
			t.Fatalf("bad line %q: %v", l, err)
		}
		if e.SessionID != wantID {
			t.Fatalf("line %q keeps session id %q", l, e.SessionID)
		}
		if e.UUID == "" {
			out = append(out, e.Type)
		} else {
			out = append(out, e.UUID)
		}
	}
	return out
}

// Branching at a reply keeps its whole turn (the tool call after it and its result) and nothing of the next one.
func TestForkCopiesUpToTheReplysTurn(t *testing.T) {
	n, dir, id := forkFixture(t)
	newID, err := n.fork(context.Background(), id, "a1#0")
	if err != nil {
		t.Fatal(err)
	}
	if newID == id || !validID(newID) {
		t.Fatalf("new id %q", newID)
	}
	got := strings.Join(readUUIDs(t, filepath.Join(dir, newID+".jsonl"), newID), ",")
	if got != "u1,a1,a2,r1,a3,custom-title" { // the fork is named after the first prompt
		t.Fatalf("copied %s", got)
	}
	// the original is untouched
	if b, _ := os.ReadFile(filepath.Join(dir, id+".jsonl")); string(b) != strings.ReplaceAll(forkLines, `"S"`, `"`+id+`"`) {
		t.Fatal("the original transcript changed")
	}
}

// Branching at the last reply copies everything; no item copies everything too.
func TestForkAtTheEnd(t *testing.T) {
	n, dir, id := forkFixture(t)
	for _, at := range []string{"a4#0", ""} {
		newID, err := n.fork(context.Background(), id, at)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(readUUIDs(t, filepath.Join(dir, newID+".jsonl"), newID), ","); got != "u1,a1,a2,r1,a3,u2,a4,custom-title" {
			t.Fatalf("at %q copied %s", at, got)
		}
	}
}

func TestForkRefusals(t *testing.T) {
	n, dir, id := forkFixture(t)
	before, _ := os.ReadDir(dir)
	if _, err := n.fork(context.Background(), id, "nope#0"); err == nil || !strings.Contains(err.Error(), "找不到") {
		t.Fatalf("unknown item: %v", err)
	}
	if _, err := n.fork(context.Background(), "99999999-2222-4333-8444-555555555555", ""); err == nil {
		t.Fatal("a session without a transcript forked")
	}
	if _, err := n.fork(context.Background(), "../x", ""); err == nil {
		t.Fatal("a bad id forked")
	}
	n.agents[id] = host.Agent{Agent: "codex"}
	if _, err := n.fork(context.Background(), id, ""); err == nil {
		t.Fatal("a Codex session forked")
	}
	if after, _ := os.ReadDir(dir); len(after) != len(before) {
		t.Fatalf("a refused fork left a file: %d → %d", len(before), len(after))
	}
}

func TestForkTitle(t *testing.T) {
	for _, c := range []struct{ title, first, want string }{
		{"", "", ""},
		{"Claude Code", "", ""},
		{"修 bug", "", "分支 · 修 bug"},
		{"分支 · 修 bug", "", "分支 · 修 bug"},
		{"", "remember   APPLE", "分支 · remember APPLE"},
		{"Claude Code", strings.Repeat("长", 50), "分支 · " + strings.Repeat("长", 40) + "…"},
	} {
		if got := forkTitle(c.title, c.first); got != c.want {
			t.Errorf("forkTitle(%q, %q) = %q, want %q", c.title, c.first, got, c.want)
		}
	}
}

// Lines other than the session id stay byte for byte (escapes, key order).
func TestForkKeepsLinesAsTheyAre(t *testing.T) {
	n, dir, id := forkFixture(t)
	src := filepath.Join(dir, id+".jsonl")
	raw := `{"type":"user","uuid":"u1","sessionId":"` + id + `","message":{"role":"user","content":"a <b> & c"},"z":1,"a":2}` + "\n"
	if err := os.WriteFile(src, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	newID, err := n.fork(context.Background(), id, "")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, newID+".jsonl"))
	first, _, _ := strings.Cut(string(b), "\n")
	if want := strings.Replace(strings.TrimSuffix(raw, "\n"), id, newID, 1); first != want {
		t.Fatalf("copied\n%s\nwant\n%s", first, want)
	}
}

// A short fork (one prompt and a short answer: under the 2 KB the offline scan skips) is still listed.
func TestForkShortIsListed(t *testing.T) {
	n, dir, id := forkFixture(t)
	newID, err := n.fork(context.Background(), id, "a1#0")
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(filepath.Join(dir, newID+".jsonl")); st.Size() >= 2048 {
		t.Fatalf("the fixture's fork is %d bytes: not short", st.Size())
	}
	for _, s := range n.appendOffline(nil, map[string]bool{}) {
		if s.SessionID == newID {
			if s.Title != "分支 · remember APPLE" {
				t.Errorf("title %q", s.Title)
			}
			return
		}
	}
	t.Fatal("the fork is not listed")
}

// Forking at the end of a turn still being written is refused: the copy could end on a tool call without a result.
func TestForkRefusesARunningTurn(t *testing.T) {
	n, dir, id := forkFixture(t)
	n.procs[id] = &assistantProc{id: id, agent: "claude", busy: true, exited: make(chan struct{})}
	before, _ := os.ReadDir(dir)
	if _, err := n.fork(context.Background(), id, "a4#0"); err == nil || !strings.Contains(err.Error(), "还没做完") {
		t.Fatalf("a running turn forked: %v", err)
	}
	if after, _ := os.ReadDir(dir); len(after) != len(before) {
		t.Fatal("the refused copy was left behind")
	}
	if _, err := n.fork(context.Background(), id, "a1#0"); err != nil { // an earlier turn is done: fine
		t.Fatal(err)
	}
}

func TestForkable(t *testing.T) {
	n, _, id := forkFixture(t)
	if n.forkable(proto.Session{SessionID: id, Agent: "claude"}) {
		t.Fatal("forkable before the transcript was found: the list must not look for it")
	}
	n.transcriptPath(id) // the list's rows have been looked up
	if !n.forkable(proto.Session{SessionID: id, Agent: "claude"}) {
		t.Fatal("a Claude session with a transcript is not forkable")
	}
	for _, s := range []proto.Session{
		{SessionID: id, Agent: "codex"},
		{SessionID: id, SourceKind: proto.SourceRuntime},
		{SessionID: id, Starting: true},
		{SessionID: "99999999-2222-4333-8444-555555555555"},
	} {
		if n.forkable(s) {
			t.Errorf("forkable(%+v)", s)
		}
	}
}
