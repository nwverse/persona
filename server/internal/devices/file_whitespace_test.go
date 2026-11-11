package devices

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/nodeproto"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

func TestWhitespacePathGateBeforeNodeRPC(t *testing.T) {
	d := newTestDevices(t)
	var received atomic.Int32
	m, _ := fakeMachine(t, d, "a", "Computer", func(_ context.Context, _ string, _ json.RawMessage) (any, error) {
		received.Add(1)
		return map[string]any{"ok": true}, nil
	})
	call := func(path string) error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return m.call(ctx, "file.rename", map[string]any{"path": path, "name": "new"}, nil)
	}
	var re *rpc.Error
	if err := call("report "); !errors.As(err, &re) || re.Code != proto.CodeNodeOutdated || received.Load() != 0 {
		t.Fatalf("old node: %v, calls %d", err, received.Load())
	}
	if err := call("report"); err != nil || received.Load() != 1 {
		t.Fatalf("ordinary path: %v, calls %d", err, received.Load())
	}
	m.mu.Lock()
	m.info.Capabilities = append(m.info.Capabilities, nodeproto.ExactFilePaths)
	m.mu.Unlock()
	if err := call("report "); err != nil || received.Load() != 2 {
		t.Fatalf("new node: %v, calls %d", err, received.Load())
	}
}
