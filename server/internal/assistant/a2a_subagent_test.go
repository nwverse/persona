package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

// memA2AStore embeds memStore and adds an in-memory a2aStore implementation so A2A
// subagent launch, visibility decoupling, and restart-persistence tests always execute
// under `go test -race` even when PERSONA_TEST_DATABASE_URL is not configured.
type memA2AStore struct {
	memStore
	mu   sync.Mutex
	rows map[string]db.A2ARow
}

func newMemA2AStore() *memA2AStore {
	return &memA2AStore{rows: map[string]db.A2ARow{}}
}

func (m *memA2AStore) A2ACreate(_ context.Context, r db.A2ARow) (*db.A2ARow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, old := range m.rows {
		if old.Owner == r.Owner && old.Request == r.Request {
			cp := old
			return &cp, nil
		}
	}
	if r.Created.IsZero() {
		r.Created = time.Now().UTC()
	}
	if r.State == a2aSubmitted {
		for _, old := range m.rows {
			if old.TargetMachine == r.TargetMachine && old.TargetSession == r.TargetSession && !a2aTerminal(old.State) {
				r.State = a2aQueued
				break
			}
		}
	}
	r.Revision = 1
	m.rows[r.ID] = r
	cp := r
	return &cp, nil
}

func (m *memA2AStore) A2ARequest(_ context.Context, owner, request string) (*db.A2ARow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.rows {
		if r.Owner == owner && r.Request == request {
			cp := r
			return &cp, nil
		}
	}
	return nil, nil
}

func (m *memA2AStore) A2AGet(_ context.Context, id string) (*db.A2ARow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rows[id]
	if !ok {
		return nil, nil
	}
	cp := r
	return &cp, nil
}

func (m *memA2AStore) A2AList(_ context.Context, owner, machine, session, contextID, state, _ string, participant bool, _ *time.Time, limit int) ([]db.A2ARow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []db.A2ARow
	for _, r := range m.rows {
		ownerMatch := r.Owner == owner && (participant || machine == "" || (r.TargetMachine == machine && r.TargetSession == session))
		targetMatch := participant && r.TargetMachine == machine && r.TargetSession == session
		if !ownerMatch && !targetMatch {
			continue
		}
		if contextID != "" && r.Context != contextID {
			continue
		}
		if state != "" && r.State != state && !(state == a2aSubmitted && r.State == a2aQueued) {
			continue
		}
		out = append(out, r)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (m *memA2AStore) A2ACount(ctx context.Context, owner, machine, session, contextID, state string, after *time.Time) (int, error) {
	rows, err := m.A2AList(ctx, owner, machine, session, contextID, state, "", false, after, 0)
	return len(rows), err
}

func (m *memA2AStore) A2AUpdate(_ context.Context, r db.A2ARow, _ time.Time, _ *time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	old, ok := m.rows[r.ID]
	if !ok || old.Revision != r.Revision {
		return false, nil
	}
	r.Revision++
	m.rows[r.ID] = r
	return true, nil
}

func (m *memA2AStore) A2AClaim(_ context.Context, limit int) ([]db.A2ARow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []db.A2ARow
	for _, r := range m.rows {
		if !a2aTerminal(r.State) && r.State != a2aQueued {
			out = append(out, r)
			if limit > 0 && len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (m *memA2AStore) A2AReady(_ context.Context, _, _ string) error { return nil }

func (m *memA2AStore) A2ADispatches(_ context.Context, owner, machine, session string, since time.Time) ([]db.A2ARow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []db.A2ARow
	for _, r := range m.rows {
		var body a2aBody
		if json.Unmarshal(r.Body, &body) != nil || body.Launch == nil {
			continue
		}
		isTarget := r.TargetMachine == machine && r.TargetSession == session
		if r.Owner != owner && !isTarget {
			continue
		}
		if isTarget || !r.Created.Before(since) || !a2aTerminal(r.State) {
			out = append(out, r)
		}
	}
	return out, nil
}

func (m *memA2AStore) A2ALaunches(_ context.Context, since time.Time) ([]db.A2ARow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []db.A2ARow
	for _, r := range m.rows {
		var body a2aBody
		if json.Unmarshal(r.Body, &body) != nil || body.Launch == nil || r.SourceSession == "" || r.TargetSession == "" {
			continue
		}
		if !r.Created.Before(since) || !a2aTerminal(r.State) {
			out = append(out, r)
		}
	}
	return out, nil
}

func memA2AFixture(t *testing.T) (*Service, *a2aTestHub, *memA2AStore, a2aCaller, a2aRef) {
	t.Helper()
	st := newMemA2AStore()
	f := newFakeHub()
	h := &a2aTestHub{fakeHub: f}
	f.hello.Agents.A2A = assistantwire.A2AConfig{} // default scope: Machines == nil, Launch == nil
	f.hello.Agents.Dispatch.Enabled = nil          // dispatch switch off by default
	from, peer := a2aRef{"mac", "caller-0001"}, a2aRef{"box", "runtime.peer-0001"}
	f.sessions[from.Machine] = []proto.Session{{SessionID: from.Session, Status: proto.StatusWorking, Agent: "claude", PermissionMode: "acceptEdits"}}
	f.sessions[peer.Machine] = []proto.Session{{SessionID: peer.Session, Status: proto.StatusIdle, Agent: "pi",
		SourceKind: "runtime", Capabilities: &proto.SessionCapabilities{Send: true, Read: true}}}
	s := New(Options{Hub: h, Store: func() Store { return st }, Logf: testLogf(t)})
	return s, h, st, a2aSessionCaller(from), peer
}

func processMemA2A(t *testing.T, s *Service, st *memA2AStore, cfg assistantwire.A2AConfig, id string) *a2aTask {
	t.Helper()
	ctx := t.Context()
	task, err := decodeA2A(st.A2AGet(ctx, id))
	if err != nil || task == nil {
		t.Fatalf("get task %s: %v %v", id, task, err)
	}
	if err := s.a2aProcess(ctx, st, cfg, task); err != nil {
		t.Fatal(err)
	}
	task, err = decodeA2A(st.A2AGet(ctx, id))
	if err != nil || task == nil {
		t.Fatalf("get processed task %s: %v %v", id, task, err)
	}
	return task
}

// Verifies default-enabled cross-machine & same-machine cross-harness A2A subagent launches,
// config opt-outs, permission_mode capping, durable subagent provenance across restarts,
// peer preservation, and a2aDiscoverable vs a2aTaskEndpointAllowed decoupling without Postgres.
func TestA2ADefaultSubagentFlowWithoutPostgres(t *testing.T) {
	s, h, st, parent, peer := memA2AFixture(t)
	ctx := t.Context()
	h.fakeModel(t)
	h.startReply = func(args map[string]any, row *proto.Session) *proto.StartInstead {
		row.SessionID = args["session_id"].(string)
		return nil
	}

	// 1. Default cross-machine launch works without agents.dispatch.enabled or explicit machines list.
	rawCross, _ := json.Marshal(map[string]any{
		"machine_id": "box", "cwd": "/code/app", "launcher": "codex",
		"message_id": "m-cross-1", "text": "Run Linux checks.",
	})
	resCross := s.mcpCall(ctx, parent.Ref.Machine, parent.Ref.Session, "a2a_send", rawCross, false)
	if resCross.IsError {
		t.Fatalf("default cross-machine launch failed: %s", resultText(resCross))
	}
	var crossView a2aTaskView
	if err := json.Unmarshal([]byte(resultText(resCross)), &crossView); err != nil {
		t.Fatal(err)
	}
	cfg := s.a2aConfig(ctx)
	processMemA2A(t, s, st, cfg, crossView.ID)
	var tk *a2aTask
	waitFor(t, "cross launch finished", func() bool {
		var err error
		tk, err = decodeA2A(st.A2AGet(ctx, crossView.ID))
		return err == nil && tk != nil && tk.Launch != nil && !tk.Launch.Pending
	})
	if !tk.Launch.Subagent || len(h.starts) != 1 || h.starts[0]["permission_mode"] != "plan" {
		t.Fatalf("cross launch = %+v, starts = %+v", tk.Launch, h.starts)
	}

	// 2. Default same-machine cross-harness launch (mac Claude -> mac Codex) with permission_mode capped at acceptEdits.
	rawSame, _ := json.Marshal(map[string]any{
		"machine_id": parent.Ref.Machine, "cwd": "/code/app", "launcher": "codex",
		"permission_mode": "acceptEdits",
		"message_id":      "m-same-1", "text": "Review patch with Codex.",
	})
	resSame := s.mcpCall(ctx, parent.Ref.Machine, parent.Ref.Session, "a2a_send", rawSame, false)
	if resSame.IsError {
		t.Fatalf("same-machine cross-harness launch failed: %s", resultText(resSame))
	}
	var sameView a2aTaskView
	if err := json.Unmarshal([]byte(resultText(resSame)), &sameView); err != nil {
		t.Fatal(err)
	}
	processMemA2A(t, s, st, cfg, sameView.ID)
	waitFor(t, "same-machine launch finished", func() bool {
		t2, err := decodeA2A(st.A2AGet(ctx, sameView.ID))
		return err == nil && t2 != nil && t2.Launch != nil && !t2.Launch.Pending
	})
	if len(h.starts) != 2 || h.starts[1]["permission_mode"] != "acceptEdits" {
		t.Fatalf("same-machine launch permission_mode not honored: %+v", h.starts)
	}

	// 3. Permission escalation past parent's acceptEdits is rejected.
	rawEsc, _ := json.Marshal(map[string]any{
		"machine_id": "box", "cwd": "/code/app", "permission_mode": "bypassPermissions",
		"message_id": "m-esc", "text": "Escalate.",
	})
	if r := s.mcpCall(ctx, parent.Ref.Machine, parent.Ref.Session, "a2a_send", rawEsc, false); !r.IsError {
		t.Fatalf("permission escalation was allowed: %s", resultText(r))
	}

	// 4. Ordinary A2A message to an existing independent peer NEVER marks it as a subagent.
	rawPeer, _ := json.Marshal(map[string]any{
		"machine_id": peer.Machine, "session_id": peer.Session,
		"message_id": "m-peer-1", "text": "Please review.",
	})
	if r := s.mcpCall(ctx, parent.Ref.Machine, parent.Ref.Session, "a2a_send", rawPeer, false); r.IsError {
		t.Fatalf("peer send failed: %s", resultText(r))
	}

	// 5. Simulated restart: fresh Service recovers subagent provenance from durable store.
	restarted := New(Options{Hub: h, Store: func() Store { return st }, Logf: testLogf(t)})
	subRef := tk.target()
	boxRows := []proto.Session{
		{MachineID: peer.Machine, SessionID: peer.Session, Status: proto.StatusIdle, Agent: "pi",
			SourceKind: "runtime", Capabilities: &proto.SessionCapabilities{Send: true, Read: true}},
		{MachineID: subRef.Machine, SessionID: subRef.Session, Status: proto.StatusIdle, Agent: "codex",
			Capabilities: &proto.SessionCapabilities{Send: true, Read: true}},
	}
	restarted.decorateSubagents(ctx, "box", boxRows)
	if boxRows[0].IsSubagent || boxRows[0].A2ATaskID() != "" || boxRows[0].ParentSessionID != "" {
		t.Fatalf("ordinary peer was marked as subagent: %+v %+v", boxRows[0], boxRows[0].Hierarchy)
	}
	if sub := boxRows[1]; !sub.IsSubagent || sub.ParentSessionID != parent.Ref.Session ||
		sub.ParentMachineID() != parent.Ref.Machine || sub.A2ATaskID() != tk.ID || sub.SubagentOrigin() != proto.SubagentOriginA2A {
		t.Fatalf("A2A subagent lost provenance after restart: %+v %+v", sub, sub.Hierarchy)
	}
	// Same-machine subagent omits parent_machine_id.
	sameRows := []proto.Session{{MachineID: parent.Ref.Machine, SessionID: sameView.Target.Session, Status: proto.StatusIdle, Agent: "codex"}}
	restarted.decorateSubagents(ctx, parent.Ref.Machine, sameRows)
	if !sameRows[0].IsSubagent || sameRows[0].ParentSessionID != parent.Ref.Session || sameRows[0].ParentMachineID() != "" ||
		sameRows[0].SubagentOrigin() != proto.SubagentOriginA2A {
		t.Fatalf("same-machine subagent provenance wrong: %+v %+v", sameRows[0], sameRows[0].Hierarchy)
	}

	// Put decorated rows back into fakeHub so a2aDiscover / a2aDeliver see IsSubagent=true.
	h.mu.Lock()
	h.sessions["box"] = boxRows
	h.mu.Unlock()

	// 6. Subagent excluded from peer discovery & unsolicited peer send, YET allowed on its own task!
	disc := restarted.mcpCall(ctx, parent.Ref.Machine, parent.Ref.Session, "a2a_sessions", json.RawMessage(`{}`), false)
	if disc.IsError || strings.Contains(resultText(disc), subRef.Session) || !strings.Contains(resultText(disc), peer.Session) {
		t.Fatalf("a2a_sessions leaked subagent or missed peer: %s", resultText(disc))
	}
	if _, err := restarted.a2aStart(ctx, st, cfg, parent, subRef, "m-unsolicited", "", "Direct ping.", false, nil); !errors.Is(err, errA2AUnavailable) {
		t.Fatalf("unsolicited peer send to subagent got %v, want errA2AUnavailable", err)
	}
	tools, err := restarted.sessionMCPToolsChecked(ctx, subRef.Machine, subRef.Session, false, false)
	if err != nil || !toolListed(tools, "a2a_report") || !toolListed(tools, "a2a_get") {
		t.Fatalf("A2A subagent missing A2A tools: err=%v", err)
	}
	repArgs, _ := json.Marshal(map[string]any{"task_id": tk.ID, "message_id": "r-ask", "state": a2aInput, "text": "Which target?"})
	if r := restarted.mcpCall(ctx, subRef.Machine, subRef.Session, "a2a_report", repArgs, false); r.IsError {
		t.Fatalf("A2A subagent a2a_report failed: %s", resultText(r))
	}
	ansArgs, _ := json.Marshal(map[string]any{"task_id": tk.ID, "message_id": "p-ans", "text": "Use integration."})
	if r := restarted.mcpCall(ctx, parent.Ref.Machine, parent.Ref.Session, "a2a_send", ansArgs, false); r.IsError {
		t.Fatalf("parent answer to subagent failed: %s", resultText(r))
	}
	tk = processMemA2A(t, restarted, st, cfg, tk.ID)
	if tk.State != a2aWorking || len(h.sentCalls()) == 0 || !strings.Contains(string(h.sentCalls()[len(h.sentCalls())-1].Params), "Use integration.") {
		t.Fatalf("follow-up was not delivered to IsSubagent=true target: state=%s sends=%+v", tk.State, h.sentCalls())
	}
	// Depth-1 guard still prevents subagent from launching a grandchild.
	gcArgs, _ := json.Marshal(map[string]any{"machine_id": "mac", "cwd": "/code/app", "message_id": "m-gc", "text": "Grandchild."})
	if r := restarted.mcpCall(ctx, subRef.Machine, subRef.Session, "a2a_send", gcArgs, false); !r.IsError || !strings.Contains(resultText(r), "does not dispatch") {
		t.Fatalf("subagent bypassed depth-1 guard: %s", resultText(r))
	}
	doneArgs, _ := json.Marshal(map[string]any{"task_id": tk.ID, "message_id": "r-done", "state": a2aComplete, "text": "All green."})
	if r := restarted.mcpCall(ctx, subRef.Machine, subRef.Session, "a2a_report", doneArgs, false); r.IsError {
		t.Fatalf("subagent completion report failed: %s", resultText(r))
	}

	// 7. Config opt-outs: Launch=false disables launches while preserving peer discovery; Machines=[] disables all.
	off := false
	h.hello.Agents.A2A = assistantwire.A2AConfig{Launch: &off}
	rawDisabled, _ := json.Marshal(map[string]any{"machine_id": "box", "cwd": "/code/app", "message_id": "m-off", "text": "No."})
	if r := restarted.mcpCall(ctx, parent.Ref.Machine, parent.Ref.Session, "a2a_send", rawDisabled, false); !r.IsError {
		t.Fatalf("Launch=false allowed launch: %s", resultText(r))
	}
	if r := restarted.mcpCall(ctx, parent.Ref.Machine, parent.Ref.Session, "a2a_sessions", json.RawMessage(`{}`), false); r.IsError {
		t.Fatalf("Launch=false broke discovery: %s", resultText(r))
	}
	h.hello.Agents.A2A = assistantwire.A2AConfig{Machines: []string{}}
	if r := restarted.mcpCall(ctx, parent.Ref.Machine, parent.Ref.Session, "a2a_send", rawDisabled, false); !r.IsError {
		t.Fatalf("Machines=[] allowed launch: %s", resultText(r))
	}
}
