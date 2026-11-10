package devices

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/nwverse/persona/server/internal/devicewire"
	"github.com/nwverse/persona/server/internal/nodeproto"
	"github.com/nwverse/persona/server/internal/rpc"
)

func TestConfigureNodeForwardsHubFeatures(t *testing.T) {
	_, _, m, _ := dirsHub(t)
	f := connectDirsNode(t, m)
	features := []string{nodeproto.PersonaContracts, "future_feature"}
	raw, _ := json.Marshal(features)
	for _, tt := range []struct {
		name  string
		reply *devicewire.ConnectedReply
		want  []string
	}{
		{"current hub", &devicewire.ConnectedReply{Configure: map[string]json.RawMessage{"hub_features": raw}}, features},
		{"old hub reconnect", &devicewire.ConnectedReply{}, []string{}},
		{"hub unavailable", nil, []string{}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m.configureNode(context.Background(), m.peer, tt.reply, true)
			f.mu.Lock()
			last := f.configure[len(f.configure)-1]
			f.mu.Unlock()
			var got []string
			if err := json.Unmarshal(last["hub_features"], &got); err != nil {
				t.Fatalf("hub_features missing or invalid: %s (%v)", last["hub_features"], err)
			}
			if got == nil || !slices.Equal(got, tt.want) {
				t.Fatalf("hub_features = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHelloClearsRetainedHubFeatures(t *testing.T) {
	d, _, m, _ := dirsHub(t)
	f := connectDirsNode(t, m)
	raw, _ := json.Marshal([]string{nodeproto.PersonaContracts})
	m.configureNode(context.Background(), m.peer, &devicewire.ConnectedReply{
		Configure: map[string]json.RawMessage{"hub_features": raw},
	}, true)
	f.mu.Lock()
	before := f.configure[len(f.configure)-1]
	f.mu.Unlock()
	d.Hello(json.RawMessage(`{}`))
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.configure) != 2 {
		t.Fatalf("hello did not wait for the capability reset: %v", f.configure)
	}
	reset := f.configure[1]
	if string(reset["hub_features"]) != "[]" {
		t.Fatalf("hello retained the previous hub capabilities: %s", reset["hub_features"])
	}
	if string(reset["claude_dirs"]) != string(before["claude_dirs"]) {
		t.Fatalf("capability reset changed an older node's directories: %s", reset["claude_dirs"])
	}
}

func TestHelloClosesNodeWhenCapabilityResetFails(t *testing.T) {
	d := newTestDevices(t)
	n := &testNode{}
	m := connectedMachine(t, d, "computer", n)
	m.mu.Lock()
	peer := m.peer
	m.mu.Unlock()
	n.mu.Lock()
	n.refuse = errors.New("reset refused")
	n.mu.Unlock()
	d.Hello(json.RawMessage(`{}`))
	select {
	case <-peer.Done():
	default:
		t.Fatal("failed reset left a node with stale capabilities available")
	}
}

func TestHelloKeepsNodesWithoutConfigure(t *testing.T) {
	for _, refusal := range []error{
		errors.New("unknown method node.configure"),
		&rpc.Error{Code: "unknown_method", Message: "node.configure"},
	} {
		t.Run(refusal.Error(), func(t *testing.T) {
			d := newTestDevices(t)
			n := &testNode{}
			m := connectedMachine(t, d, "computer", n)
			m.mu.Lock()
			peer := m.peer
			m.mu.Unlock()
			n.mu.Lock()
			n.refuse = refusal
			n.mu.Unlock()
			d.Hello(json.RawMessage(`{}`))
			select {
			case <-peer.Done():
				t.Fatal("capability reset disconnected an older node without node.configure")
			default:
			}
		})
	}
}
