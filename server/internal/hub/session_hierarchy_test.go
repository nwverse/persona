package hub

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestCodexHierarchySurvivesSessionAPI(t *testing.T) {
	h := newTestHub(t)
	m := newMachine(h, MachineConfig{ID: "box", Name: "Box", Kind: "local"})
	h.machines, h.byID["box"] = append(h.machines, m), m
	child := proto.Session{SessionID: "child", Agent: "codex", SourceKind: "codex_app_server", Status: proto.StatusWorking,
		ParentThreadID: "raw-root", ParentSessionID: "root", RootSessionID: "root", IsSubagent: true,
		Cwd: "/code/project/.claude/worktrees/task", RepoRoot: "/code/project"}
	root := proto.Session{SessionID: "root", Agent: "codex", SourceKind: "codex_app_server", Status: proto.StatusWorking}
	m.sessions = []proto.Session{root, child}
	app := &client{out: make(chan []byte, 16), subs: map[[2]string]bool{}, role: roleFull, delta: true, have: map[string]map[string][]byte{}}
	addTestClient(h, app)
	check := func(got proto.Session) {
		t.Helper()
		if got.SessionID != child.SessionID || got.ParentThreadID != child.ParentThreadID || got.ParentSessionID != child.ParentSessionID || got.RootSessionID != child.RootSessionID || !got.IsSubagent || got.RepoRoot != child.RepoRoot {
			t.Fatalf("session lost hierarchy or repository identity: %+v", got)
		}
	}
	h.snapshotSessions(app, m)
	state := applyPushed(nil, drainPushed(app))
	check(state["child"])

	// A metadata-only change must reach clients through the delta path too.
	child.ParentSessionID = "intermediate"
	m.sessions = []proto.Session{root, child}
	h.broadcastSessions(m, h.decorated(m))
	state = applyPushed(state, drainPushed(app))
	check(state["child"])

	// History is fetched over HTTP, including children whose parent is missing.
	child.SourceKind, child.Status = "codex_history", proto.StatusOffline
	m.sessions = []proto.Session{child}
	w := httptest.NewRecorder()
	h.listSessions(w, httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("list status %d: %s", w.Code, w.Body.String())
	}
	var result struct {
		Sessions []proto.Session `json:"sessions"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Sessions) != 1 {
		t.Fatalf("history child was hidden: %+v", result.Sessions)
	}
	check(result.Sessions[0])
}
