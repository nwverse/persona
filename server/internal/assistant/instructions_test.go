package assistant

import (
	"context"
	"testing"
)

// The CLAUDE.md of the assistant and of the health space are written on the assistant's machine right after the hub
// starts, again only when what they say changes, and not while the machine is offline (refreshInstructions).
func TestRefreshInstructions(t *testing.T) {
	ctx := context.Background()
	f := healthHub()
	f.devices[0].Online = true
	s := newTestService(t, f)
	setSettings(t, s, func(v *Settings) { v.MachineID = "server" })

	s.refreshInstructions(ctx)
	if len(f.prepared) != 2 || f.prepared[0]["dir"] != s.assistantRel() || f.prepared[1]["dir"] != s.healthRel() {
		t.Fatalf("after start: %v", f.prepared)
	}
	s.refreshInstructions(ctx)
	if len(f.prepared) != 2 {
		t.Fatalf("unchanged, written again: %d", len(f.prepared))
	}
	// What the machine has is not what this hub renders (an older hub's prompts): only that one is written.
	s.preparedMu.Lock()
	s.preparedMD["server\x00"+s.healthRel()] = "old prompt"
	s.preparedMu.Unlock()
	s.refreshInstructions(ctx)
	if len(f.prepared) != 3 || f.prepared[2]["dir"] != s.healthRel() {
		t.Fatalf("changed: %v", f.prepared[2:])
	}
	// A conversation started in between writes it too and counts as written.
	if _, err := s.prepare(ctx, "server"); err != nil {
		t.Fatal(err)
	}
	s.refreshInstructions(ctx)
	if len(f.prepared) != 4 {
		t.Fatalf("after prepare: %d", len(f.prepared))
	}

	off := healthHub() // Online false
	s = newTestService(t, off)
	setSettings(t, s, func(v *Settings) { v.MachineID = "server" })
	s.refreshInstructions(ctx)
	if len(off.prepared) != 0 {
		t.Fatalf("offline machine: %v", off.prepared)
	}
}
