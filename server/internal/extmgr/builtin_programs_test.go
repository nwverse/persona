package extmgr

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// fakeNodesBuiltin: the fake Macs of the cua and ego tests (computerMac, egoMac) are nodes that have the built-in
// programs (node.info's builtin_programs), and the hub must ask them over plugin.node.call. Off, they are nodes from
// before, asked by the old names. Only TestBuiltinProgramsSameFlows turns it on; the tests are not parallel. A fake
// node reads it once, when it is made: its goroutines can outlive the test that set it.
var fakeNodesBuiltin bool

func fakeBuiltinPrograms(builtin bool) []string {
	if builtin {
		return []string{"cua", "ego", "imessage"}
	}
	return nil
}

// fakeBuiltinRoute is the fake node's side of callBuiltin: a plugin.node.call to cua or ego back to the old method
// name and its params, for the fake to answer as before; a call the wrong way for this node (builtin: whether it has
// the built-in programs) fails the test.
func fakeBuiltinRoute(t *testing.T, builtin bool, method string, params json.RawMessage) (string, json.RawMessage) {
	if method == "plugin.node.call" {
		var c proto.PluginNodeCall
		_ = json.Unmarshal(params, &c)
		if !builtin {
			t.Errorf("plugin.node.call %s.%s to a node without built-in programs", c.ID, c.Method)
		}
		if c.ID == imessageExtensionID { // its methods keep their names (connector.*)
			return c.Method, c.Params
		}
		for prefix, id := range builtinPrefixes {
			if id == c.ID {
				return prefix + c.Method, c.Params
			}
		}
		t.Errorf("plugin.node.call to %q", c.ID)
		return method, params
	}
	for _, prefix := range []string{"computer.", "browser.", "connector."} {
		if strings.HasPrefix(method, prefix) && builtin {
			t.Errorf("%s by its old name to a node with built-in programs", method)
		}
	}
	return method, params
}

// The cua and ego tests again, with nodes that have the built-in programs: the same flows, answers and checks,
// every call over plugin.node.call.
func TestBuiltinProgramsSameFlows(t *testing.T) {
	fakeNodesBuiltin = true
	t.Cleanup(func() { fakeNodesBuiltin = false })
	for name, test := range map[string]func(*testing.T){
		"ComputerExtensionIsOffUntilTurnedOnForAMachine": TestComputerExtensionIsOffUntilTurnedOnForAMachine,
		"ComputerToolsCallTheNode":                       TestComputerToolsCallTheNode,
		"ComputerJobs":                                   TestComputerJobs,
		"ComputerClipboard":                              TestComputerClipboard,
		"TheCursorAndTheClickSound":                      TestTheCursorAndTheClickSound,
		"NoAutoLock":                                     TestNoAutoLock,
		"ComputerTellsReachTheMacInOrder":                TestComputerTellsReachTheMacInOrder,
		"CallApps":                                       TestCallApps,
		"ComputerAppsAreAskedOnce":                       TestComputerAppsAreAskedOnce,
		"EgoExtension":                                   TestEgoExtension,
		"IPhoneExtension":                                TestIPhoneExtension,
		"IPhoneApprovals":                                TestIPhoneApprovals,
		"IPhoneCallFailures":                             TestIPhoneCallFailures,
		"IPhoneJobs":                                     TestIPhoneJobs,
		"BrowserCookies":                                 TestBrowserCookies,
		"IMessageThroughTheHub":                          TestIMessageThroughTheHub,
		"IMessageOldOrOfflineMac":                        TestIMessageOldOrOfflineMac,
		"IMessageAISwitch":                               TestIMessageAISwitch,
		"IMessageArchive":                                TestIMessageArchive,
		"IMessageExtensionPicksTheMac":                   TestIMessageExtensionPicksTheMac,
	} {
		t.Run(name, test)
	}
}

// Every old method, both ways: a node with the built-in programs gets plugin.node.call {id, method, params} with the
// params byte for byte; one without gets the old name and the same params; the answer reads the same.
func TestCallBuiltinRoutes(t *testing.T) {
	methods := []struct {
		old, id, short string
		params         any
	}{
		{"computer.status", "cua", "status", map[string]any{}},
		{"computer.call", "cua", "call", map[string]any{"session": "s", "tool": "click", "args": map[string]any{"x": 1}, "keep_awake": true, "click_sound": "none"}},
		{"computer.install", "cua", "install", map[string]any{}},
		{"computer.grant", "cua", "grant", map[string]any{}},
		{"computer.cancel", "cua", "cancel", map[string]any{}},
		{"computer.lock", "cua", "lock", map[string]any{}},
		{"computer.clipboard", "cua", "clipboard", map[string]any{"set": "你好"}},
		{"computer.cursor", "cua", "cursor", map[string]any{"on": true}},
		{"computer.nolock", "cua", "nolock", map[string]any{"on": false}},
		{"browser.status", "ego", "status", map[string]any{}},
		{"browser.install", "ego", "install", map[string]any{}},
		{"browser.open", "ego", "open", map[string]any{}},
		{"browser.cancel", "ego", "cancel", map[string]any{}},
		{"browser.call", "ego", "call", map[string]any{"session": "s", "script": "document.title", "look": false}},
		{"browser.cookies.list", "ego", "cookies.list", map[string]any{}},
		{"browser.cookies.import", "ego", "cookies.import", map[string]any{"cookies": []map[string]any{{"name": "a", "value": "b", "domain": "example.com"}}}},
		{"browser.cookies.clear", "ego", "cookies.clear", map[string]any{"sites": []string{"example.com"}, "all": false}},
	}
	for _, builtin := range []bool{true, false} {
		h := newTestManager(t)
		ctx, cancel := context.WithCancel(context.Background())
		h2nR, h2nW := io.Pipe()
		n2hR, n2hW := io.Pipe()
		hubSide, nodeSide := rpc.New("h", h2nW), rpc.New("n", n2hW)
		var gotMethod string
		var gotParams json.RawMessage
		nodeSide.OnRequest = func(_ context.Context, method string, params json.RawMessage) (any, error) {
			gotMethod, gotParams = method, params
			return map[string]any{"answered": method}, nil
		}
		go func() { _ = hubSide.Serve(ctx, n2hR) }()
		go func() { _ = nodeSide.Serve(ctx, h2nR) }()
		m := newMachine(h, MachineConfig{ID: "mac", Name: "mac"})
		m.peer, m.online, m.info = hubSide, true, proto.NodeInfo{OS: "darwin"}
		if builtin {
			m.info.BuiltinPrograms = []string{"cua", "ego"}
		}
		for _, c := range methods {
			var out map[string]any
			if err := m.callBuiltin(ctx, c.old, c.params, &out); err != nil {
				t.Fatalf("%s (builtin %v): %v", c.old, builtin, err)
			}
			want, _ := json.Marshal(c.params)
			if builtin {
				var call proto.PluginNodeCall
				_ = json.Unmarshal(gotParams, &call)
				if gotMethod != "plugin.node.call" || call.ID != c.id || call.Method != c.short || string(call.Params) != string(want) {
					t.Errorf("%s to a node with built-in programs: %s %s", c.old, gotMethod, gotParams)
				}
				if out["answered"] != "plugin.node.call" {
					t.Errorf("%s: answer %v", c.old, out)
				}
			} else if gotMethod != c.old || string(gotParams) != string(want) {
				t.Errorf("%s to an older node: %s %s", c.old, gotMethod, gotParams)
			}
		}
		// Not a built-in program's method: as it was, whatever the node.
		if err := m.callBuiltin(ctx, "session.list", map[string]any{}, nil); err != nil || gotMethod != "session.list" {
			t.Errorf("session.list: %s, %v", gotMethod, err)
		}
		cancel()
		_ = h2nW.Close()
		_ = n2hW.Close()
	}
}
