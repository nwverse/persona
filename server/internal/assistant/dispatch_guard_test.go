package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

const (
	guardMachine = "server"
	guardSession = "session-guarded"
	otherSession = "session-other"
	guardSecret  = "AKIA-SECRET-VALUE token=do-not-print"
)

func newGuardFakeHub() *fakeHub {
	f := newFakeHub().withMachine()
	f.hello.Locale = string(i18n.En)
	f.known[guardSession] = true
	f.known[otherSession] = true
	return f
}

func guardPolicy(t *testing.T, deny ...string) DispatchGuard {
	t.Helper()
	return DispatchGuard{
		Enabled: true,
		Targets: []DispatchGuardTarget{{
			MachineID: guardMachine,
			SessionID: guardSession,
			Deny:      deny,
		}},
	}
}

func putGuard(t *testing.T, s *Service, g DispatchGuard) {
	t.Helper()
	if err := s.store().PutSetting(context.Background(), dispatchGuardSetting, g); err != nil {
		t.Fatal(err)
	}
}

func guardNamesOf(tools []proto.Tool) []string {
	out := make([]string, 0, len(tools))
	for _, tool := range tools {
		out = append(out, tool.Name)
	}
	return out
}

func TestDispatchGuardRefusalAndToolNarrowing(t *testing.T) {
	f := newGuardFakeHub()
	s := newTestService(t, f)
	all := guardNamesOf(s.Tools(context.Background()))
	for _, name := range dispatchGuardToolNames {
		if !slices.Contains(all, name) {
			t.Fatalf("global tool list is missing %q", name)
		}
	}
	putGuard(t, s, guardPolicy(t, dispatchGuardToolNames...))

	if err := s.guardRefusal(context.Background(), guardMachine, guardSession, "task_plan"); err == nil || err.Error() != dispatchGuardRefusalText {
		t.Fatalf("guarded task_plan: %v", err)
	}
	res := s.Tool(context.Background(), ToolCall{
		MachineID: guardMachine, SessionID: guardSession, Name: "agent_start",
		Arguments: []byte(`{"prompt":"` + guardSecret + `"}`),
	})
	if !res.IsError || !strings.Contains(res.Content[0].Text, dispatchGuardRefusalText) {
		t.Fatalf("guarded tool call: %+v", res)
	}
	text := res.Content[0].Text
	for _, hidden := range []string{guardSecret, guardMachine, guardSession, "agent_start", "task_plan"} {
		if strings.Contains(text, hidden) {
			t.Fatalf("refusal leaked %q: %q", hidden, text)
		}
	}

	visible := guardNamesOf(s.ToolsFor(context.Background(), guardMachine, guardSession))
	if len(visible) != len(all)-len(dispatchGuardToolNames) {
		t.Fatalf("narrowed tools %d, want %d", len(visible), len(all)-len(dispatchGuardToolNames))
	}
	for _, name := range dispatchGuardToolNames {
		if slices.Contains(visible, name) {
			t.Fatalf("target still lists guarded tool %q", name)
		}
	}
	if got := guardNamesOf(s.ToolsFor(context.Background(), guardMachine, otherSession)); !slices.Equal(got, all) {
		t.Fatalf("another session's tool list changed")
	}
	if got := guardNamesOf(s.Tools(context.Background())); !slices.Equal(got, all) {
		t.Fatalf("the global tool list changed")
	}
}

func TestDispatchGuardDefaultsAndFallback(t *testing.T) {
	f := newGuardFakeHub()
	s := newTestService(t, f)
	all := guardNamesOf(s.Tools(context.Background()))
	ctx := context.Background()

	checkOff := func(name string) {
		t.Helper()
		if err := s.guardRefusal(ctx, guardMachine, guardSession, "task_plan"); err != nil {
			t.Fatalf("%s: guard refused: %v", name, err)
		}
		if got := guardNamesOf(s.ToolsFor(ctx, guardMachine, guardSession)); !slices.Equal(got, all) {
			t.Fatalf("%s: tool list narrowed", name)
		}
	}
	checkOff("no policy")

	g := guardPolicy(t, "task_plan")
	g.Enabled = false
	putGuard(t, s, g)
	checkOff("explicitly disabled")

	past := time.Now().Add(-time.Minute).UTC()
	expired := guardPolicy(t, "task_plan")
	expired.ExpiresAt = &past
	putGuard(t, s, expired)
	checkOff("expired policy")

	enabled := guardPolicy(t, "task_plan")
	putGuard(t, s, enabled)
	if err := s.guardRefusal(ctx, guardMachine, otherSession, "task_plan"); err != nil {
		t.Fatalf("another target was guarded: %v", err)
	}
	if err := s.guardRefusal(ctx, guardMachine, guardSession, "notify_user"); err != nil {
		t.Fatalf("a non-dispatch tool was guarded: %v", err)
	}
	if err := s.guardRefusal(ctx, guardMachine, guardSession, "task_plan"); err == nil {
		t.Fatal("an enabled target was not refused")
	}
}

func TestDispatchGuardFailsClosedOnReadError(t *testing.T) {
	f := newGuardFakeHub()
	st := &memStore{}
	s := newServiceOn(t, f, st)
	st.failKey(dispatchGuardSetting, errors.New("settings store unavailable"))

	err := s.guardRefusal(context.Background(), guardMachine, guardSession, "conversation_send")
	if err == nil || err.Error() != dispatchGuardRefusalText {
		t.Fatalf("read error: %v", err)
	}
	visible := guardNamesOf(s.ToolsFor(context.Background(), guardMachine, guardSession))
	for _, name := range dispatchGuardToolNames {
		if slices.Contains(visible, name) {
			t.Fatalf("dispatch tool %q stayed visible while the guard could not be read", name)
		}
	}
}

func TestDispatchGuardInvalidEnabledPolicyFailsClosed(t *testing.T) {
	f := newGuardFakeHub()
	s := newTestService(t, f)
	putGuard(t, s, DispatchGuard{
		Enabled: true,
		Targets: []DispatchGuardTarget{{MachineID: guardMachine, SessionID: guardSession, Deny: []string{"not-a-tool"}}},
	})
	if err := s.guardRefusal(context.Background(), guardMachine, guardSession, "task_plan"); err == nil || err.Error() != dispatchGuardRefusalText {
		t.Fatalf("invalid enabled policy: %v", err)
	}
	visible := guardNamesOf(s.ToolsFor(context.Background(), guardMachine, guardSession))
	for _, name := range dispatchGuardToolNames {
		if slices.Contains(visible, name) {
			t.Fatalf("invalid enabled policy left %q visible", name)
		}
	}
}

func TestDispatchGuardRouteValidationAndFallback(t *testing.T) {
	f := newGuardFakeHub()
	s := newTestService(t, f)
	expiry := time.Now().Add(30 * time.Minute).UTC().Format(time.RFC3339Nano)
	body := `{"dispatch_guard":{"enabled":true,"expires_at":"` + expiry + `","targets":[{"machine_id":"server","session_id":"session-1","deny":["task_plan"]}]}}`

	_, listChanged := s.mcpChanges.snapshot()
	if w := serveAs(s, roleExtension, http.MethodPut, "/api/v1/assistant/dispatch-guard", body); w.Code != http.StatusOK {
		t.Fatalf("extension write: %d %s", w.Code, w.Body.String())
	}
	select {
	case <-listChanged:
	default:
		t.Fatal("the policy write did not invalidate existing MCP tool lists")
	}
	if s.guardRefusal(context.Background(), "server", "session-1", "task_plan") == nil {
		t.Fatal("the route did not activate the guard")
	}

	noExpiry := `{"dispatch_guard":{"enabled":true,"targets":[{"machine_id":"server","session_id":"session-1","deny":["task_plan"]}]}}`
	if w := serveAs(s, roleExtension, http.MethodPut, "/api/v1/assistant/dispatch-guard", noExpiry); w.Code != http.StatusBadRequest {
		t.Fatalf("extension policy without expiry: %d %s", w.Code, w.Body.String())
	}
	far := time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339Nano)
	overHour := `{"dispatch_guard":{"enabled":true,"expires_at":"` + far + `","targets":[{"machine_id":"server","session_id":"session-1","deny":["task_plan"]}]}}`
	if w := serveAs(s, roleExtension, http.MethodPut, "/api/v1/assistant/dispatch-guard", overHour); w.Code != http.StatusBadRequest {
		t.Fatalf("extension expiry over the hour: %d %s", w.Code, w.Body.String())
	}
	if w := serveAs(s, "answer", http.MethodPut, "/api/v1/assistant/dispatch-guard", body); w.Code != http.StatusForbidden {
		t.Fatalf("answer role: %d %s", w.Code, w.Body.String())
	}
	if w := serveAs(s, roleFull, http.MethodPut, "/api/v1/assistant/dispatch-guard", noExpiry); w.Code != http.StatusOK {
		t.Fatalf("full credential without expiry: %d %s", w.Code, w.Body.String())
	}
	bad := `{"dispatch_guard":{"enabled":true,"targets":[{"machine_id":"server","session_id":"session-1","deny":["unknown_tool"]}]}}`
	if w := serveAs(s, roleFull, http.MethodPut, "/api/v1/assistant/dispatch-guard", bad); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown denied tool: %d %s", w.Code, w.Body.String())
	}
	clear := `{"dispatch_guard":{"enabled":false,"targets":[]}}`
	if w := serveAs(s, roleExtension, http.MethodPut, "/api/v1/assistant/dispatch-guard", clear); w.Code != http.StatusOK {
		t.Fatalf("extension clear: %d %s", w.Code, w.Body.String())
	}
	if w := serveAs(s, roleFull, http.MethodGet, "/api/v1/assistant/dispatch-guard", ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"targets":[]`) {
		t.Fatalf("GET after clear: %d %s", w.Code, w.Body.String())
	}
}

func TestDispatchGuardNarrowsSessionMCPListOnlyForTarget(t *testing.T) {
	f := newGuardFakeHub()
	s := newTestService(t, f)
	putGuard(t, s, guardPolicy(t, dispatchGuardToolNames...))
	ctx := context.Background()

	visible := guardNamesOf(s.sessionMCPTools(ctx, guardMachine, guardSession, true, false))
	for _, name := range dispatchGuardToolNames {
		if slices.Contains(visible, name) {
			t.Fatalf("actual MCP list still includes guarded tool %q", name)
		}
	}
	other := guardNamesOf(s.sessionMCPTools(ctx, guardMachine, otherSession, true, false))
	global := guardNamesOf(s.mcpTools(ctx, true))
	for _, name := range dispatchGuardToolNames {
		if !slices.Contains(other, name) || !slices.Contains(global, name) {
			t.Fatalf("untargeted MCP list lost %q", name)
		}
	}
	res := s.mcpCall(ctx, guardMachine, guardSession, "task_plan", json.RawMessage(`{}`), true)
	if !res.IsError || !strings.Contains(res.Content[0].Text, dispatchGuardRefusalText) {
		t.Fatalf("hidden tool was callable through mcpCall: %+v", res)
	}
}

func TestDispatchGuardUnauthorizedWriteLeavesStartUnchanged(t *testing.T) {
	f := newGuardFakeHub()
	s := newTestService(t, f)
	start := func() string {
		t.Helper()
		if _, err := s.Start(context.Background(), StartParams{Text: "hello"}); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(f.lastSend(t))
		return string(raw)
	}
	before := start()
	body := `{"dispatch_guard":{"enabled":true,"targets":[{"machine_id":"server","session_id":"guarded","deny":["task_plan"]}]}}`
	if w := serveAs(s, "answer", http.MethodPut, "/api/v1/assistant/dispatch-guard", body); w.Code != http.StatusForbidden {
		t.Fatalf("unauthorized write: %d %s", w.Code, w.Body.String())
	}
	after := start()
	if before != after {
		t.Fatalf("start params changed:\nbefore %s\nafter  %s", before, after)
	}
}

func TestDispatchGuardWriteRequiresACompleteKnownShape(t *testing.T) {
	store := &memStore{}
	s := newServiceOn(t, newGuardFakeHub(), store)
	original := guardPolicy(t, "task_plan")
	original.Enabled = false // a retained configuration should survive a rejected edit verbatim
	putGuard(t, s, original)
	initialWrites := store.puts
	cursor, _ := s.mcpChanges.snapshot()
	for name, body := range map[string]string{
		"null envelope":     `null`,
		"mistyped case":     `{"dispatch_guard":{"Enabled":false,"targets":[]}}`,
		"repeated property": `{"dispatch_guard":{"enabled":false,"enabled":false,"targets":[]}}`,
		"empty envelope":    `{}`,
		"missing policy":    `{"example":{}}`,
		"null policy":       `{"dispatch_guard":null}`,
		"missing enabled":   `{"dispatch_guard":{"targets":[]}}`,
		"null enabled":      `{"dispatch_guard":{"enabled":null,"targets":[]}}`,
		"missing targets":   `{"dispatch_guard":{"enabled":true}}`,
		"null targets":      `{"dispatch_guard":{"enabled":false,"targets":null}}`,
		"unknown envelope":  `{"dispatch_guard":{"enabled":false},"extra":true}`,
		"unknown policy":    `{"dispatch_guard":{"enabled":false,"extra":true}}`,
		"unknown target":    `{"dispatch_guard":{"enabled":false,"targets":[{"machine_id":"desk","session_id":"notes","deny":["task_plan"],"extra":true}]}}`,
		"trailing document": `{"dispatch_guard":{"enabled":false,"targets":[]}} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			if res := serveAs(s, roleFull, http.MethodPut, dispatchGuardRoute, body); res.Code != http.StatusBadRequest {
				t.Fatalf("expected a validation error, got %d", res.Code)
			}
			if stored, set, err := s.loadDispatchGuard(t.Context()); !set || err != nil || !reflect.DeepEqual(stored, original) {
				t.Fatalf("rejected input changed the stored configuration: set=%t, err=%v", set, err)
			}
			if store.puts != initialWrites {
				t.Fatal("rejected input wrote to the settings store")
			}
			if next, _ := s.mcpChanges.snapshot(); next != cursor {
				t.Fatal("rejected input notified watchers")
			}
		})
	}
}

func TestExplicitEmptyDispatchGuardTargetsClearBothRoles(t *testing.T) {
	for _, role := range []string{roleFull, roleExtension} {
		t.Run(role, func(t *testing.T) {
			s := newTestService(t, newGuardFakeHub())
			putGuard(t, s, guardPolicy(t, "task_plan"))
			_, change := s.mcpChanges.snapshot()
			w := serveAs(s, role, http.MethodPut, dispatchGuardRoute, `{"dispatch_guard":{"enabled":true,"targets":[]}}`)
			if w.Code != http.StatusOK {
				t.Fatalf("explicit clear: %d %s", w.Code, w.Body.String())
			}
			var receipt struct {
				Guard DispatchGuard `json:"dispatch_guard"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &receipt); err != nil || receipt.Guard.Enabled || receipt.Guard.Targets == nil || len(receipt.Guard.Targets) != 0 {
				t.Fatalf("clear receipt: %s", w.Body.String())
			}
			stored, ok, err := s.loadDispatchGuard(t.Context())
			if err != nil || !ok || stored.Enabled || len(stored.Targets) != 0 || stored.ExpiresAt != nil {
				t.Fatal("clear did not persist the disabled policy")
			}
			select {
			case <-change:
			default:
				t.Fatal("clear did not wake live tool lists")
			}
		})
	}
}

func TestDispatchGuardWatchRefreshesWithoutAPolicyWrite(t *testing.T) {
	for _, transition := range []string{"expiry", "read recovery"} {
		for _, when := range []string{"admission", "heartbeat"} {
			t.Run(transition+"/"+when, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					f := newGuardFakeHub()
					f.ownModels = true
					st := &memStore{}
					s := newServiceOn(t, f, st)
					beforeExpiry := 5 * time.Second
					if transition == "expiry" {
						g := guardPolicy(t, "task_plan")
						end := time.Now().Add(beforeExpiry)
						g.ExpiresAt = &end
						putGuard(t, s, g)
					} else {
						st.failKey(dispatchGuardSetting, errors.New("settings store unavailable"))
					}
					watch := func(since string) map[string]any {
						return s.sessionMCPToolsSince(t.Context(), guardMachine, guardSession, true, false, since)
					}
					first := watch("")
					tools, ok := first["tools"].([]proto.Tool)
					if !ok || slices.Contains(guardNamesOf(tools), "task_plan") {
						t.Fatal("the initial discovery list did not reflect its active constraint")
					}
					cursor := first["revision"].(string)
					recover := func() {
						if transition == "expiry" {
							<-time.After(beforeExpiry) // advance the virtual clock to natural expiry
						} else {
							st.failKey(dispatchGuardSetting, nil)
						}
					}
					if when == "admission" {
						recover()
					}
					next := make(chan map[string]any, 1)
					go func() { next <- watch(cursor) }()
					synctest.Wait()
					if when == "heartbeat" {
						select {
						case <-next:
							t.Fatal("the watch returned before the effective tool set changed")
						default:
						}
						recover()
					}
					restored := <-next
					tools, ok = restored["tools"].([]proto.Tool)
					if !ok || restored["revision"] == cursor || !slices.Contains(guardNamesOf(tools), "task_plan") {
						t.Fatalf("the watch missed the restored tool list, revision=%v unchanged=%v", restored["revision"], restored["unchanged"])
					}
					stable := watch(restored["revision"].(string))
					if stable["revision"] != restored["revision"] || stable["unchanged"] != true || stable["tools"] != nil {
						t.Fatal("a stable list was sent again")
					}
				})
			})
		}
	}
}
