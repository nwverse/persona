package runtimepkg

import (
	"os"
	"testing"
)

// TestDistBudget bounds what personad, persona-node and persona-manager embed of the runtime packages: every file
// Generate writes from integrations/. Measured 191,106 B on 2026-11-20 with per-assistant Antigravity ACP workers,
// runtime SDK PID reporting (#11676) and Antigravity's guard against pictures the model service cannot decode (#11789);
// the Antigravity user images, checkpoint notices, and prompt retry fixes (#11734) on main bring the combined budget
// to 192,088 B. Raise it only for code a runtime needs, never to embed the packages raw again.
func TestDistBudget(t *testing.T) {
	const budget = 192_088
	_, out := generateIntegrations(t)
	entries, err := os.ReadDir(out)
	if err != nil {
		t.Fatal(err)
	}
	size := int64(0)
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().IsRegular() {
			size += info.Size()
		}
	}
	if size > budget {
		t.Fatalf("generated runtime packages are %d bytes, over the budget of %d", size, budget)
	}
	t.Logf("generated runtime packages: %d bytes (budget %d)", size, budget)
}
