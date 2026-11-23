package runtimepkg

import (
	"os"
	"testing"
)

// TestDistBudget bounds what personad, persona-node and persona-manager embed of the runtime packages: every file
// Generate writes from integrations/. The complete Antigravity package, including the image guard (#11790),
// measured 192,060 B; preserving delivered replies through a background wake (#11756) added 1,822 B. Claude Code
// plugin 0.2.0 session settings overrides brought the total to 196,211 B; busy owner input queueing + turn interrupts
// add 2,129 B on their branch. Main measured 196,937 B on 2026-11-21, and keeping an Antigravity send from rewriting
// the conversation's history (#12014) adds 738 B (199,188 B ceiling). Embedding persona-opencode-runtime
// (opencode/package.json, index.js, companion.mjs, lib/*.js including #12504/#12545; shared runtime-sdk blob reused)
// measured 248,704 B on 2026-11-21. Session-bound A2A tool bridges bring the generated total to about 251.3 kB.
// Minifying the remaining non-compat Antigravity lib sources (acp.js, file-links.js, models.js; #12781, #13123)
// brings the generated total from 254,085 B back to 251,311 B under the unchanged 252,000 B ceiling.
// Antigravity's empty-response retry and interrupted-turn notices (#13957) use the remaining room and add 850 B.
// Raise the budget only for code a runtime needs, never to embed the packages raw again.
func TestDistBudget(t *testing.T) {
	const budget = 252_850
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
