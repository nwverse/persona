//go:build !linux && !darwin

package node

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/nwverse/persona/server/internal/osproc"
)

func (*Node) stopCodexWriter(context.Context, codexWriter) error {
	return takeoverRefusal("unavailable")
}

func (*Node) prepareCodexWriterQuit(context.Context, codexWriter) (func() error, error) {
	return nil, takeoverRefusal("unavailable")
}

func codexWriterLockHeldOS(src codexSource) bool {
	if src.home == "" || src.thread == "" {
		return false
	}
	// Check owner metadata non-destructively on platforms without Unix flock.
	ownerPath := filepath.Join(src.home, "thread-writer-locks", src.thread+".owner.json")
	b, err := os.ReadFile(ownerPath)
	if err != nil {
		return false
	}
	var o struct {
		PID int `json:"pid"`
	}
	return json.Unmarshal(b, &o) == nil && o.PID > 0 && osproc.AliveMine(o.PID)
}
