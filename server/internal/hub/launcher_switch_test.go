package hub

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/nodeproto"
	"github.com/nwverse/persona/server/internal/proto"
)

// POST …/modes with a launcher (#5035) goes to the node's session.modes as it came, launcher included; a launcher that
// is not a string is refused before the node is asked, and so is any launcher to a node that has not announced
// launcher_switch (it would drop the launcher and switch the model where the Agent runs).
func TestSessionModesPassesTheLauncherSwitch(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		info       *proto.NodeInfo
		status     int
		code       string
		want       map[string]any // what session.modes got; nil: not called
	}{
		{"switch", `{"launcher":"work","model":"opus","effort":"high"}`, nil, http.StatusOK, "",
			map[string]any{"session_id": "s1", "launcher": "work", "model": "opus", "effort": "high"}},
		{"cancel", `{"launcher":"claude"}`, nil, http.StatusOK, "", map[string]any{"session_id": "s1", "launcher": "claude"}},
		{"announced", `{"launcher":"work"}`, &proto.NodeInfo{Version: "test", Protocol: 1,
			Capabilities: []string{nodeproto.Sessions, nodeproto.LauncherSwitch}}, http.StatusOK, "",
			map[string]any{"session_id": "s1", "launcher": "work"}},
		{"not a string", `{"launcher":7}`, nil, http.StatusBadRequest, "", nil},
		{"older node", `{"launcher":"work","model":"opus"}`, &proto.NodeInfo{Version: "old", Protocol: 1,
			Capabilities: []string{nodeproto.Sessions}}, http.StatusConflict, proto.CodeNodeOutdated, nil},
		{"older node, no launcher", `{"model":"opus"}`, &proto.NodeInfo{Version: "old", Protocol: 1,
			Capabilities: []string{nodeproto.Sessions}}, http.StatusOK, "", map[string]any{"session_id": "s1", "model": "opus"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestHub(t)
			var got map[string]any
			modelsNode(t, h, MachineConfig{ID: "box"}, func(method string, p map[string]any) (any, error) {
				if method == "session.modes" {
					got = p
				}
				return map[string]any{}, nil
			})
			if tc.info != nil {
				m := h.machineByID("box")
				m.mu.Lock()
				m.info = *tc.info
				m.mu.Unlock()
			}
			w := modelsRequest(h, "POST", "/api/v1/sessions/box/s1/modes", tc.body)
			if w.Code != tc.status || (tc.code != "" && !strings.Contains(w.Body.String(), `"code":"`+tc.code+`"`)) {
				t.Fatalf("%d %s", w.Code, w.Body)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("session.modes got %v, want %v", got, tc.want)
			}
		})
	}
}

// A model picked with a launcher is remembered as used under that launcher, not the one the session runs on now.
func TestModesLauncher(t *testing.T) {
	s := proto.Session{Launcher: "mine"}
	for _, tc := range []struct {
		p    map[string]any
		s    proto.Session
		want string
	}{
		{map[string]any{"model": "opus", "launcher": "work"}, s, "work"},
		{map[string]any{"model": "opus", "launcher": " "}, s, "mine"},
		{map[string]any{"model": "opus"}, s, "mine"},
		{map[string]any{"model": "opus"}, proto.Session{}, "claude"},
	} {
		if got := modesLauncher(tc.p, tc.s); got != tc.want {
			t.Errorf("modesLauncher(%v, %q) = %q, want %q", tc.p, tc.s.Launcher, got, tc.want)
		}
	}
}
