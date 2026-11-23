package hub

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// Codex and compatible variants return their session only after startup even when a client asks for async: the node gets the agent
// type the launcher resolves to, and the start's budget (startBudget) follows it. What the start runs on is the
// manager's (agents.start_params), whose calls carry no deadline over the bus: the budget is checked on the decision.
func TestSessionStartBudgetMatchesNodeStartup(t *testing.T) {
	for _, tc := range []struct {
		name       string
		agentType  string
		configured string
		async      bool
		short      bool
	}{
		{name: "default Claude async", async: true, short: true},
		{name: "explicit Claude async", agentType: "claude", async: true, short: true},
		{name: "explicit Codex async", agentType: "codex", async: true},
		{name: "explicit TRAE CLI async", agentType: "traex", async: true},
		{name: "explicit AGY async", agentType: "agy", async: true},
		{name: "configured Codex async", configured: "codex", async: true},
		{name: "configured TRAE CLI async", configured: "traex", async: true},
		{name: "Claude sync"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestHub(t)
			connectAssistant(t, h) // it says what a start runs on; without it nothing starts
			startReceiptNode(t, h, func(_ context.Context, params json.RawMessage) (any, error) {
				var in struct {
					AgentType string `json:"agent_type"`
				}
				if err := json.Unmarshal(params, &in); err != nil {
					return nil, err
				}
				want := tc.agentType
				if want == "" {
					want = tc.configured
				}
				if in.AgentType != want {
					t.Errorf("node agent_type = %q, want %q", in.AgentType, want)
				}
				return map[string]any{"session": proto.Session{SessionID: "created"}}, nil
			})
			h.byID["machine"].cfg.Launchers = []assistantwire.ConfiguredLauncher{{Launcher: proto.Launcher{Name: "wrapper"}, AgentType: tc.configured}}
			body, err := json.Marshal(map[string]any{
				"machine_id": "machine", "cwd": "/code", "launcher": "wrapper", "prompt": "hello", "tab_name": "test",
				"agent_type": tc.agentType, "async": tc.async,
			})
			if err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest("POST", "/api/v1/sessions", strings.NewReader(string(body)))
			w := httptest.NewRecorder()
			h.startSession(w, r)
			if w.Code != http.StatusOK {
				t.Fatalf("start = %d %s", w.Code, w.Body)
			}
			want := tc.agentType
			if want == "" {
				want = tc.configured
			}
			budget := startBudget(startsAsync(tc.async, "", "wrapper", want), 0)
			min, max := 60*time.Second, 101*time.Second
			if tc.short {
				min, max = 10*time.Second, 16*time.Second
			}
			if budget < min || budget > max {
				t.Fatalf("operation budget = %s, want between %s and %s", budget, min, max)
			}
		})
	}
}

func TestSessionStartUsesResolvedLauncherBudget(t *testing.T) {
	for _, tc := range []struct {
		name, requested, resolved, agent, resume string
		codexWait, replyAt, timeout              time.Duration
		replyError                               *rpc.Error
	}{
		{name: "named AGY launcher", requested: "agy", resolved: "agy", replyAt: 20 * time.Second},
		{name: "manager resolved AGY", resolved: "agy-wrapper", agent: "agy", replyAt: 99 * time.Second},
		{name: "AGY retains its own budget", resolved: "agy-wrapper", agent: "agy", codexWait: 3 * time.Minute,
			replyAt: 101 * time.Second, timeout: 100 * time.Second},
		{name: "named Codex launcher", requested: "codex", resolved: "codex", codexWait: 3 * time.Minute, replyAt: 150 * time.Second},
		{name: "manager resolved Codex", resolved: "codex-wrapper", agent: "codex", codexWait: 3 * time.Minute, replyAt: 150 * time.Second},
		{name: "old Codex node", resolved: "codex-wrapper", agent: "codex", replyAt: 101 * time.Second, timeout: 100 * time.Second},
		{name: "Codex resume retains its own budget", requested: "codex", resolved: "codex", agent: "codex", resume: "existing",
			codexWait: 3 * time.Minute, replyAt: 101 * time.Second, timeout: 100 * time.Second},
		{name: "Codex sends its own configured timeout", resolved: "codex-wrapper", agent: "codex", codexWait: 4 * time.Minute,
			replyAt: 4 * time.Minute, replyError: &rpc.Error{Code: "codex_no_box", Message: "no composer within 240 seconds", Data: json.RawMessage(`{"seconds":240}`)}},
		{name: "Codex budget includes a startup allowance", resolved: "codex-wrapper", agent: "codex", codexWait: 4 * time.Minute,
			replyAt: 281 * time.Second, timeout: 280 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h := newTestHub(t)
				startReceiptNode(t, h, func(ctx context.Context, raw json.RawMessage) (any, error) {
					var params struct {
						Launcher string
						Async    bool
					}
					if err := json.Unmarshal(raw, &params); err != nil {
						return nil, err
					}
					if params.Launcher != tc.resolved {
						t.Errorf("node launcher = %q, want %q", params.Launcher, tc.resolved)
					}
					if tc.resume == "" && (tc.agent == "codex" || tc.resolved == "codex") && params.Async {
						t.Error("fresh Codex start requested an async reply from the node")
					}
					select {
					case <-ctx.Done():
						return nil, ctx.Err()
					case <-time.After(tc.replyAt):
						if tc.replyError != nil {
							return nil, tc.replyError
						}
						return map[string]any{"session": proto.Session{SessionID: "created", Agent: tc.agent}}, nil
					}
				})
				h.byID["machine"].info.CodexStartWait = tc.codexWait
				hubIn, managerOut := io.Pipe()
				managerIn, hubOut := io.Pipe()
				hubPeer, managerPeer := rpc.New("h", hubOut), rpc.New("m", managerOut)
				managerPeer.OnRequest = func(_ context.Context, method string, raw json.RawMessage) (any, error) {
					if method != assistantwire.MethodAgentStartParams {
						return nil, nil
					}
					var p assistantwire.AgentStartParams
					if err := json.Unmarshal(raw, &p); err != nil {
						return nil, err
					}
					p.Params["launcher"] = tc.resolved
					if tc.agent != "" {
						p.Params["agent_type"] = tc.agent
					}
					return map[string]any{"params": p.Params}, nil
				}
				t.Cleanup(func() {
					hubPeer.Close(io.EOF)
					managerPeer.Close(io.EOF)
					_ = hubIn.Close()
					_ = managerIn.Close()
				})
				go hubPeer.Serve(t.Context(), hubIn)
				go managerPeer.Serve(t.Context(), managerIn)
				h.bus.mu.Lock()
				h.bus.current = map[string]*busConn{assistantwire.AssistantID: {peer: hubPeer}}
				h.bus.mu.Unlock()

				body, err := json.Marshal(map[string]any{
					"machine_id": "machine", "cwd": "/code", "launcher": tc.requested, "tab_name": "test", "async": true,
					"resume_session_id": tc.resume,
				})
				if err != nil {
					t.Fatal(err)
				}
				r := httptest.NewRequest("POST", "/api/v1/sessions", strings.NewReader(string(body)))
				w := httptest.NewRecorder()
				startedAt := time.Now()
				h.startSession(w, r)
				elapsed := time.Since(startedAt)
				switch {
				case tc.timeout > 0:
					if w.Code == http.StatusOK || elapsed != tc.timeout || !strings.Contains(w.Body.String(), `"code":"timeout"`) {
						t.Fatalf("start after %s = %d %s, want timeout after %s", elapsed, w.Code, w.Body, tc.timeout)
					}
				case tc.replyError != nil:
					if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), `"code":"codex_no_box"`) ||
						!strings.Contains(w.Body.String(), `"seconds":240`) {
						t.Fatalf("start after %s = %d %s, want the node's timeout", elapsed, w.Code, w.Body)
					}
				default:
					if w.Code != http.StatusOK {
						t.Fatalf("start after %s = %d %s, want success", elapsed, w.Code, w.Body)
					}
				}
			})
		})
	}
}

func TestMachineSnapshotReportsCodexStartBudget(t *testing.T) {
	h := newTestHub(t)
	m := newMachine(h, MachineConfig{ID: "machine"})
	if got := m.snapshot().CodexStartBudgetMS; got != 0 {
		t.Fatalf("old node budget = %d ms, want no advertised budget", got)
	}
	m.info.CodexStartWait = 3 * time.Minute
	if got := m.snapshot().CodexStartBudgetMS; got != 220_000 {
		t.Fatalf("advertised budget = %d ms, want 220000", got)
	}
}

// A start's goal (POST /api/v1/sessions) reaches the node's session.start, where the node sets it after the first
// message: the route does not drop the documented field.
func TestSessionStartForwardsTheGoal(t *testing.T) {
	h := newTestHub(t)
	connectAssistant(t, h)
	var gotGoal string
	startReceiptNode(t, h, func(_ context.Context, params json.RawMessage) (any, error) {
		var in struct {
			Goal string `json:"goal"`
		}
		if err := json.Unmarshal(params, &in); err != nil {
			return nil, err
		}
		gotGoal = in.Goal
		return map[string]any{"session": proto.Session{SessionID: "created"}}, nil
	})
	body, err := json.Marshal(map[string]any{
		"machine_id": "machine", "cwd": "/code", "launcher": "claude", "prompt": "fix the build", "tab_name": "t",
		"goal": "CI is green",
	})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/api/v1/sessions", strings.NewReader(string(body)))
	w := httptest.NewRecorder()
	h.startSession(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("start = %d %s", w.Code, w.Body)
	}
	if gotGoal != "CI is green" {
		t.Fatalf("the node got goal %q", gotGoal)
	}
}
