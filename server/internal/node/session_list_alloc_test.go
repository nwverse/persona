package node

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/proto"
)

var updateGolden = flag.Bool("update", false, "rewrite the golden files in testdata")

// listT0 is when the fixtures' sessions were last used, give or take a few minutes: fixed, so their lists are too.
var listT0 = time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)

// listID is the fixtures' session id number i.
func listID(i int) string { return fmt.Sprintf("0e000000-0000-4000-8000-%012d", i) }

// listNode is a node with one connected runtime and nothing else to list: no engine, no running Claude, no Codex.
func listNode(tb testing.TB) (*Node, *runtimeConn) {
	tb.Helper()
	n, _ := testNode(tb)
	n.runtimes = newRuntimeRegistry()
	n.cdirs = newClaudeDirs()
	n.cdirs.scan = func() map[int]procEnv { return nil }
	n.metas, n.bgScans = map[string]quickMeta{}, map[string]*bgScan{}
	n.modes = map[string]sessionModes{}
	return n, n.runtimes.add(nil)
}

// runtimeListNode is a node whose list is rows sessions of one runtime, the fleet soak's machines (10 x 50 sessions,
// one item and a busy toggle per machine every 2 s): rows tie on UpdatedAt in sevens, and none is filtered out.
func runtimeListNode(tb testing.TB, rows int) *Node {
	tb.Helper()
	n, h := listNode(tb)
	list := make([]proto.Session, rows)
	for i := range list {
		list[i] = proto.Session{SessionID: listID(i), Agent: "pi", Status: proto.StatusIdle, Title: fmt.Sprintf("task %d", i),
			Cwd: fmt.Sprintf("/work/p%d", i%5), UpdatedAt: listT0.Add(time.Duration(i%7) * time.Minute), Rev: int64(1000 + i)}
	}
	n.runtimes.setSessions(h, list)
	return n
}

// transcriptListNode is a node whose list is rows Claude transcripts of the last few days, none of them running: the
// offline rows most of a workstation's list is.
func transcriptListNode(tb testing.TB, rows int) *Node {
	tb.Helper()
	n, _ := listNode(tb)
	dir := filepath.Join(n.home, ".claude", "projects", "-work-app")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		tb.Fatal(err)
	}
	prompt := strings.Repeat("Make the upload test pass on slow disks. ", 60) // over the 2 KB a listed transcript has
	for i := range rows {
		p := filepath.Join(dir, listID(i)+".jsonl")
		line := fmt.Sprintf(`{"type":"user","cwd":"/work/app","timestamp":"2026-11-01T08:00:00Z","message":{"role":"user","content":%q}}`+"\n"+
			`{"type":"assistant","timestamp":"2026-11-01T08:01:00Z","message":{"id":"m%d","role":"assistant","model":"claude-sonnet-4-5",`+
			`"content":[{"type":"text","text":"Done: the test waits for the write to land."}]}}`+"\n"+
			`{"type":"custom-title","customTitle":"task %d"}`+"\n", prompt, i, i)
		at := time.Now().Add(-time.Duration(i%40) * time.Hour)
		if err := os.WriteFile(p, []byte(line), 0o600); err != nil {
			tb.Fatal(err)
		}
		if err := os.Chtimes(p, at, at); err != nil {
			tb.Fatal(err)
		}
	}
	return n
}

// BenchmarkSessionList is one session list (sessionList, as each poll, notify and sessions.list builds it) of a node
// whose runtime lists 50 or 300 sessions, and of one with 80 recent transcripts, as many as are listed (their scan
// cached, as it is for 20 s).
func BenchmarkSessionList(b *testing.B) {
	for _, c := range []struct {
		name string
		rows int
		node func(testing.TB, int) *Node
	}{{"runtime/50", 50, runtimeListNode}, {"runtime/300", 300, runtimeListNode}, {"transcripts/80", 80, transcriptListNode}} {
		b.Run(c.name, func(b *testing.B) {
			n := c.node(b, c.rows)
			if got := len(n.sessionList(context.Background(), false)); got != c.rows {
				b.Fatalf("listed %d sessions, want %d", got, c.rows)
			}
			b.ReportAllocs()
			for b.Loop() {
				n.sessionList(context.Background(), false)
			}
		})
	}
}

// A list is built in one slice of its own size (#2826): 300 rows of a runtime cost 1.09 times the rows' bytes, the rest
// being the seen set and the sort's positions. Grown from nil, copied into the rows kept and with the runtime's rows in
// a slice of their own first, they cost 10.5 times. The bound is the cost measured then, plus 3%.
func TestSessionListAllocatesOneList(t *testing.T) {
	const rows = 300
	n := runtimeListNode(t, rows)
	per := bytesPerRun(20, func() { n.sessionList(context.Background(), false) })
	if most := 1.125 * rows * float64(unsafe.Sizeof(proto.Session{})); per > most {
		t.Errorf("a list of %d rows allocates %.0f bytes, want at most %.0f", rows, per, most)
	}
}

// keepFixture is a node whose list has every row the keep pass and the sort treat apart: rows tied on UpdatedAt from
// live panes, a runtime and a transcript; a blocked directory's rows, one of them kept and one the assistant's; a Codex
// parent dropped there whose subagents still take its project; one row outside the directory the scoped list allows; a
// session listed by two runtimes. scoped: the node lists only /work.
func keepFixture(t *testing.T, scoped bool) *Node {
	t.Helper()
	n, h := listNode(t)
	n.cdirs.blocked = []string{"/work/blocked"}
	if scoped {
		n.sessionScope = &nodeSessionScope{dir: "/work", claude: filepath.Join(n.home, ".claude"), codex: filepath.Join(n.home, ".codex")}
	}
	at := func(m int) time.Time { return listT0.Add(time.Duration(m) * time.Minute) }
	for i := 3; i >= 1; i-- { // live panes with no transcript yet: tied at zero
		n.agents[listID(i)] = host.Agent{Agent: "claude", AgentStatus: proto.StatusWorking, Cwd: "/work/repo", PaneID: fmt.Sprintf("p%d", i)}
	}
	n.runtimes.setSessions(h, []proto.Session{
		{SessionID: listID(10), Agent: "pi", Status: proto.StatusIdle, Title: "tied a", Cwd: "/work/app", UpdatedAt: at(5), Rev: 10},
		{SessionID: listID(11), Agent: "pi", Status: proto.StatusWorking, Title: "tied b", Cwd: "/work/app", UpdatedAt: at(5), Rev: 11},
		{SessionID: listID(12), Agent: "pi", Status: proto.StatusIdle, Title: "blocked", Cwd: "/work/blocked/x", UpdatedAt: at(9)},
		{SessionID: listID(13), Agent: "pi", Status: proto.StatusIdle, Title: "kept", Cwd: "/work/blocked/kept", UpdatedAt: at(1)},
		{SessionID: listID(14), Agent: "codex", SourceKind: "codex", Status: proto.StatusDone, Title: "parent", Cwd: "/work/blocked/parent",
			RepoRoot: "/work/repo", SpaceID: "codex-project:/work/repo", SpaceKind: "project", SpaceName: "repo", UpdatedAt: at(2)},
		{SessionID: listID(15), Agent: "codex", SourceKind: "codex", IsSubagent: true, ParentSessionID: listID(14), Status: proto.StatusDone,
			Title: "child", Cwd: "/work/app", UpdatedAt: at(3)},
		{SessionID: listID(16), Agent: "codex", SourceKind: "codex", IsSubagent: true, ParentSessionID: listID(15), Status: proto.StatusDone,
			Title: "grandchild", Cwd: "/work/app", UpdatedAt: at(3)},
		{SessionID: listID(17), Agent: "pi", Status: proto.StatusIdle, Title: "elsewhere", Cwd: "/elsewhere/y", UpdatedAt: at(7)},
		{SessionID: listID(18), Agent: "pi", Status: proto.StatusIdle, Title: "never used", Cwd: "/work/app"},
		{SessionID: listID(19), Agent: "pi", Status: proto.StatusOffline, Title: "listed twice, offline here", Cwd: "/work/app", UpdatedAt: at(4)},
	})
	other := n.runtimes.add(nil) // the one running 19 answers for it
	n.runtimes.setSessions(other, []proto.Session{
		{SessionID: listID(19), Agent: "pi", Status: proto.StatusWorking, Title: "listed twice, running here", Cwd: "/work/app", UpdatedAt: at(6)},
	})
	n.procs[listID(20)] = &assistantProc{id: listID(20), cwd: "/work/blocked/assistant", lastActive: at(8)}
	writeTranscript(t, n.home, "/work/app", listID(30), at(5), `{"type":"custom-title","customTitle":"transcript, tied"}`)
	n.setKept([]string{listID(13), listID(30)})
	return n
}

// The keep pass (scope, blocked directories and their exceptions, the Codex hierarchy resolved over the whole list
// first) and the stable sort give the very list, row for row and field for field, that main's sessionList gave
// (testdata/session_list_keep.json, written before the list was built in one slice).
func TestSessionListKeepPassGolden(t *testing.T) {
	var got bytes.Buffer
	for _, scoped := range []bool{false, true} {
		n := keepFixture(t, scoped)
		fmt.Fprintf(&got, "scoped=%v\n", scoped)
		for _, s := range n.sessionList(context.Background(), false) {
			b, err := json.Marshal(s)
			if err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(&got, "%s\n", strings.ReplaceAll(string(b), n.home, "<home>"))
		}
	}
	path := filepath.Join("testdata", "session_list_keep.json")
	if *updateGolden {
		if err := os.WriteFile(path, got.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Bytes(), want) {
		t.Errorf("the list differs from %s (go test -run %s -update rewrites it; read the diff first):\n%s", path, t.Name(), got.String())
	}
}
