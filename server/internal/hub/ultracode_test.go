package hub

import (
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

func TestSessionModesSplitsUltracode(t *testing.T) {
	for _, tt := range []struct {
		name, body string
		methods    []string
		params     []map[string]any
	}{
		{"speed", `{"service_tier":"priority"}`, []string{"session.speed"}, []map[string]any{{"session_id": "s1", "service_tier": "priority"}}},
		{"on", `{"ultracode":true}`, []string{"session.ultracode"}, []map[string]any{{"session_id": "s1", "ultracode": true}}},
		{"off", `{"ultracode":false}`, []string{"session.ultracode"}, []map[string]any{{"session_id": "s1", "ultracode": false}}},
		{"after model and effort", `{"model":"opus","effort":"low","permission_mode":"auto","ultracode":true,"session_id":"wrong"}`,
			[]string{"session.modes", "session.ultracode"}, []map[string]any{
				{"session_id": "s1", "model": "opus", "effort": "low", "permission_mode": "auto"},
				{"session_id": "s1", "ultracode": true}}},
		{"legacy", `{"effort":"high"}`, []string{"session.modes"}, []map[string]any{{"session_id": "s1", "effort": "high"}}},
		{"dismiss", `{"ultracode_dismiss_lost":true}`, []string{"session.ultracode"}, []map[string]any{{"session_id": "s1", "dismiss_lost": true}}},
		{"reopen and dismiss", `{"ultracode":true,"ultracode_dismiss_lost":true}`, []string{"session.ultracode"}, []map[string]any{{"session_id": "s1", "ultracode": true, "dismiss_lost": true}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newTestHub(t)
			var methods []string
			var params []map[string]any
			modelsNode(t, h, MachineConfig{ID: "box"}, func(method string, p map[string]any) (any, error) {
				methods, params = append(methods, method), append(params, p)
				return map[string]any{"ok": true, "method": method}, nil
			})
			w := modelsRequest(h, "POST", "/api/v1/sessions/box/s1/modes", tt.body)
			if w.Code != http.StatusOK || !reflect.DeepEqual(methods, tt.methods) || !reflect.DeepEqual(params, tt.params) {
				t.Fatalf("%d %s; methods=%v params=%v", w.Code, w.Body, methods, params)
			}
			if !strings.Contains(w.Body.String(), tt.methods[len(tt.methods)-1]) {
				t.Fatalf("last response was dropped: %s", w.Body)
			}
		})
	}
}

func TestSessionModesUltracodeErrors(t *testing.T) {
	for _, tt := range []struct {
		name, body, failMethod, code string
		err                          error
		wantStatus, wantCalls        int
	}{
		{"old node", `{"ultracode":true}`, "session.ultracode", "node_outdated", errors.New("unknown method session.ultracode"), 409, 1},
		{"old node dismissal", `{"ultracode_dismiss_lost":true}`, "session.ultracode", "node_outdated", errors.New("unknown method session.ultracode"), 409, 1},
		{"refused", `{"ultracode":true}`, "session.ultracode", "ultracode_unsupported", &rpc.Error{Code: "ultracode_unsupported", Message: "模型不支持"}, 409, 1},
		{"effort failed", `{"effort":"high","ultracode":true}`, "session.modes", "input_draft", &rpc.Error{Code: "input_draft", Message: "输入框有草稿"}, 409, 1},
		{"string", `{"ultracode":"on"}`, "", "", nil, 400, 0},
		{"null", `{"ultracode":null}`, "", "", nil, 400, 0},
		{"invalid dismissal", `{"effort":"high","ultracode_dismiss_lost":1}`, "", "", nil, 400, 0},
		{"null body", `null`, "", "", nil, 400, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newTestHub(t)
			calls := 0
			modelsNode(t, h, MachineConfig{ID: "box"}, func(method string, _ map[string]any) (any, error) {
				calls++
				if method == tt.failMethod {
					return nil, tt.err
				}
				return map[string]any{"ok": true}, nil
			})
			w := modelsRequest(h, "POST", "/api/v1/sessions/box/s1/modes", tt.body)
			if w.Code != tt.wantStatus || calls != tt.wantCalls || (tt.code != "" && !strings.Contains(w.Body.String(), `"code":"`+tt.code+`"`)) {
				t.Fatalf("%d %s; %d calls", w.Code, w.Body, calls)
			}
		})
	}
}

func TestSessionStartForwardsUltracode(t *testing.T) {
	for _, value := range []string{"true", "false", ""} {
		t.Run("value="+value, func(t *testing.T) {
			h := newTestHub(t)
			var got map[string]any
			modelsNode(t, h, MachineConfig{ID: "box"}, func(method string, p map[string]any) (any, error) {
				if method == "session.start" {
					got = p
					return map[string]any{"session": proto.Session{SessionID: "s1", Starting: true}}, nil
				}
				return map[string]any{}, nil
			})
			connectAssistant(t, h)
			body := `{"machine_id":"box","cwd":"/code","launcher":"claude","tab_name":"work","async":true`
			if value != "" {
				body += `,"ultracode":` + value
			}
			w := modelsRequest(h, "POST", "/api/v1/sessions", body+`}`)
			if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"starting":true`) {
				t.Fatalf("%d %s", w.Code, w.Body)
			}
			v, exists := got["ultracode"]
			if (value != "") != exists || (exists && v != (value == "true")) {
				t.Fatalf("node got %v", got)
			}
		})
	}
}

func TestSessionSpeedValidation(t *testing.T) {
	for _, body := range []string{`{"service_tier":true}`, `{"service_tier":""}`, `{"service_tier":"priority","effort":"low"}`} {
		h := newTestHub(t)
		calls := 0
		modelsNode(t, h, MachineConfig{ID: "box"}, func(method string, p map[string]any) (any, error) { calls++; return nil, nil })
		w := modelsRequest(h, "POST", "/api/v1/sessions/box/s1/modes", body)
		if w.Code != http.StatusBadRequest || calls != 0 {
			t.Fatalf("%s: %d calls=%d", body, w.Code, calls)
		}
	}
	h := newTestHub(t)
	modelsNode(t, h, MachineConfig{ID: "box"}, func(method string, p map[string]any) (any, error) {
		return nil, errors.New("unknown method session.speed")
	})
	w := modelsRequest(h, "POST", "/api/v1/sessions/box/s1/modes", `{"service_tier":"priority"}`)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "node_outdated") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}
