package devices

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The hub's computer's node runs as persona-node beside the manager's program when it is the same build; another
// build, or PERSONA_SPLIT_PROGRAMS=0, leaves it on that program, as before (said once until it changes).
func TestLocalNodeProgram(t *testing.T) {
	dir := t.TempDir()
	self, node := filepath.Join(dir, "personad"), filepath.Join(dir, "persona-node")
	for _, p := range []string{self, node} {
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	var logged []string
	d := New(Options{Hub: &fakeHost{}, Store: newMemStore(), Logf: func(f string, a ...any) { logged = append(logged, fmt.Sprintf(f, a...)) }})
	m := &Machine{d: d, cfg: MachineConfig{ID: "here", Kind: "local"}}
	build := "persona 1.0 abc"
	version := func(_ context.Context, p string) (string, error) {
		if p == node {
			return build, nil
		}
		return "persona 1.0 abc", nil
	}
	if got := m.localNode(context.Background(), self, version); got != node || len(logged) != 0 {
		t.Fatalf("same build: %q %v", got, logged)
	}
	build = "persona 0.9 old"
	for range 2 {
		if got := m.localNode(context.Background(), self, version); got != self {
			t.Fatalf("another build: %q", got)
		}
	}
	if len(logged) != 1 || !strings.Contains(logged[0], "another build") || !strings.Contains(logged[0], "the node runs as personad") {
		t.Fatalf("logged %q", logged)
	}
	build = "persona 1.0 abc"
	t.Setenv("PERSONA_SPLIT_PROGRAMS", "0")
	if got := m.localNode(context.Background(), self, version); got != self {
		t.Fatalf("opted out: %q", got)
	}
	if err := os.Remove(node); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PERSONA_SPLIT_PROGRAMS", "")
	if got := m.localNode(context.Background(), self, version); got != self {
		t.Fatalf("none beside: %q", got)
	}
}
