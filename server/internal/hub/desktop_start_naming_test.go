package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/proto"
)

// A start in Claude's desktop app (#13728): the hub waits on no tab name for it (the session has no terminal tab), and
// once the node has answered, session.start_ended carries the new session, its placeholder title and what it is named
// from (the start of the first message), for the manager to name it off the start's path.
func TestADesktopStartReportsItsSessionForNaming(t *testing.T) {
	const first = "用中文工作，最后用中文汇报。帮我修复 PDF 导出时丢失图片的问题"
	long := first + strings.Repeat("。补充说明", assistantwire.StartedNameFromRunes)
	for _, tc := range []struct {
		name, body, nameFrom string
		// asked: the first message the manager gets with the start's params. A start that picks the desktop app sends
		// none; one on the default launcher sends it, since only the manager knows the default, and the manager leaves
		// a start it resolves to the desktop app without a tab name (assistant agents_start.go, inDesktopApp).
		asked string
	}{
		{"desktop launcher picked", `{"machine_id":"machine","cwd":"/code/report","launcher":"claude-desktop","prompt":` + quote(first) + `}`, first, ""},
		// The app sends async with no launcher; the manager resolves Models › Agents' default, the desktop app's.
		{"desktop launcher by default", `{"machine_id":"machine","cwd":"/code/report","async":true,"prompt":` + quote(first) + `}`, first, first},
		// A long first message is named from its start: no more of it goes over the bus again.
		{"long first message", `{"machine_id":"machine","cwd":"/code/report","launcher":"claude-desktop","prompt":` + quote(long) + `}`,
			string([]rune(long)[:assistantwire.StartedNameFromRunes]), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestHub(t)
			h.bus.idents = &memIdents{}
			mux := http.NewServeMux()
			h.busRoutes(mux)
			srv := httptest.NewServer(mux)
			t.Cleanup(srv.Close)
			startReceiptNode(t, h, func(context.Context, json.RawMessage) (any, error) {
				return map[string]any{"session": proto.Session{SessionID: "created", Title: "report", Cwd: "/code/report",
					SourceKind: proto.SourceClaudeDesktop}}, nil
			})
			m := h.byID["machine"]
			m.owner = "m-desktop"
			h.cfg.Machines = []MachineConfig{m.cfg}
			h.devices.assign = map[string]string{m.cfg.ID: m.owner}
			h.devices.assignRead = true
			tabNames := make(chan string, 4)
			nameFrom := make(chan string, 1)
			manager := connectGuardManager(t, h, srv.URL, m.owner, func(method string, raw json.RawMessage) (any, error) {
				switch method {
				case assistantwire.MethodAgentStartParams:
					var in assistantwire.AgentStartParams
					if err := json.Unmarshal(raw, &in); err != nil {
						return nil, err
					}
					nameFrom <- in.NameFrom
					in.Params["launcher"] = "claude-desktop"
					return map[string]any{"params": in.Params}, nil
				case assistantwire.MethodAgentTabName:
					tabNames <- string(raw)
					return map[string]any{"name": "fix-pdf"}, nil
				}
				return map[string]any{}, nil
			})
			ended := make(chan json.RawMessage, 1)
			manager.mu.Lock()
			manager.onEvent = func(method string, params json.RawMessage) {
				if method == assistantwire.EventStartEnded {
					ended <- params
				}
			}
			manager.mu.Unlock()
			w := httptest.NewRecorder()
			h.startSession(w, httptest.NewRequest("POST", "/api/v1/sessions", strings.NewReader(tc.body)))
			if w.Code != http.StatusOK {
				t.Fatalf("start = %d %s", w.Code, w.Body)
			}
			select {
			case raw := <-tabNames:
				t.Fatalf("the desktop start waited for a tab name: %s", raw)
			default:
			}
			if got := <-nameFrom; got != tc.asked {
				t.Fatalf("the manager got %q to name a tab from, want %q", got, tc.asked)
			}
			var event struct {
				OK      bool `json:"ok"`
				Started *struct {
					SessionID string `json:"session_id"`
					Title     string `json:"title"`
					NameFrom  string `json:"name_from"`
				} `json:"started"`
			}
			select {
			case raw := <-ended:
				if err := json.Unmarshal(raw, &event); err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("no session.start_ended")
			}
			if !event.OK || event.Started == nil || event.Started.SessionID != "created" || event.Started.Title != "report" || event.Started.NameFrom != tc.nameFrom {
				t.Fatalf("session.start_ended: %+v (started %+v)", event, event.Started)
			}
		})
	}
}

// A start in a terminal is named by its tab as before, and its session.start_ended carries no session to name.
func TestATerminalStartReportsNoSessionForNaming(t *testing.T) {
	h := newTestHub(t)
	h.bus.idents = &memIdents{}
	mux := http.NewServeMux()
	h.busRoutes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	startReceiptNode(t, h, func(context.Context, json.RawMessage) (any, error) {
		return map[string]any{"session": proto.Session{SessionID: "created", Title: "hello"}}, nil
	})
	m := h.byID["machine"]
	m.owner = "m-terminal"
	h.cfg.Machines = []MachineConfig{m.cfg}
	h.devices.assign = map[string]string{m.cfg.ID: m.owner}
	h.devices.assignRead = true
	manager := connectGuardManager(t, h, srv.URL, m.owner, func(method string, raw json.RawMessage) (any, error) {
		if method == assistantwire.MethodAgentStartParams {
			var in assistantwire.AgentStartParams
			if err := json.Unmarshal(raw, &in); err != nil {
				return nil, err
			}
			return map[string]any{"params": in.Params}, nil
		}
		return map[string]any{}, nil
	})
	ended := make(chan json.RawMessage, 1)
	manager.mu.Lock()
	manager.onEvent = func(method string, params json.RawMessage) {
		if method == assistantwire.EventStartEnded {
			ended <- params
		}
	}
	manager.mu.Unlock()
	w := httptest.NewRecorder()
	h.startSession(w, httptest.NewRequest("POST", "/api/v1/sessions", strings.NewReader(`{"machine_id":"machine","cwd":"/code","launcher":"claude","prompt":"hello"}`)))
	if w.Code != http.StatusOK {
		t.Fatalf("start = %d %s", w.Code, w.Body)
	}
	select {
	case raw := <-ended:
		if strings.Contains(string(raw), `"started"`) || strings.Contains(string(raw), "hello") {
			t.Fatalf("a terminal start was reported for naming: %s", raw)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no session.start_ended")
	}
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
