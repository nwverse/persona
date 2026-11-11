package devices

import (
	"context"
	"errors"
	"testing"

	"github.com/nwverse/persona/server/internal/nodeproto"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// A hand-off goes only to a node that announces secret_env: an older one would answer secret.stage "unknown method"
// and, worse, start an Agent without the values its ticket stands for.
func TestSecretHandOffNeedsANodeWithSecretEnv(t *testing.T) {
	_, _, m, _ := engineHub(t)
	f, _ := connectEngineNode(t, m)
	outdated := func(what string, err error) {
		t.Helper()
		var rerr *rpc.Error
		if !errors.As(err, &rerr) || rerr.Code != proto.CodeNodeOutdated || rerr.Message == "" {
			t.Fatalf("%s: %v", what, err)
		}
	}
	m.mu.Lock()
	m.info.Version, m.info.Protocol, m.info.Capabilities = "old", 1, []string{nodeproto.Sessions}
	m.mu.Unlock()
	outdated("secret.stage", m.call(context.Background(), proto.MethodSecretStage, proto.SecretStageParams{Check: true}, nil))
	outdated("a start with a ticket", m.call(context.Background(), "session.start", map[string]any{"cwd": "/tmp", "secret_ticket": "t"}, nil))
	if calls := f.seen(); len(calls) != 0 {
		t.Fatalf("sent to the old node: %v", calls)
	}
	if err := m.call(context.Background(), "session.start", map[string]any{"cwd": "/tmp"}, nil); err != nil {
		t.Fatalf("a start without one: %v", err)
	}

	m.mu.Lock()
	m.info.Capabilities = append(m.info.Capabilities, nodeproto.SecretEnv)
	m.mu.Unlock()
	if err := m.call(context.Background(), "session.start", map[string]any{"cwd": "/tmp", "secret_ticket": "t"}, nil); err != nil {
		t.Fatalf("a node with secret_env: %v", err)
	}
}
