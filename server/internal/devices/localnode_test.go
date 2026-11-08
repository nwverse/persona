package devices

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/compat"
)

// The hub's computer's node runs as the persona-node beside the manager's program when it is the same build, else as
// the personad beside it when that is, else as the program itself, so it never runs as persona-manager while a
// personad of its build is there. PERSONA_SPLIT_PROGRAMS=0 keeps the program. What was passed over is said once until
// it changes, and a connection given up while asking says nothing.
func TestLocalNodeProgram(t *testing.T) {
	dir := t.TempDir()
	manager, personad, node := filepath.Join(dir, "persona-manager"), filepath.Join(dir, "personad"), filepath.Join(dir, "persona-node")
	write := func(p string) {
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{manager, personad, node} {
		write(p)
	}
	const build = "persona 1.0 abc"
	builds := map[string]string{manager: build, personad: build, node: build}
	calls := 0
	version := func(ctx context.Context, p string) (string, error) {
		calls++
		if err := ctx.Err(); err != nil {
			return "", err
		}
		return builds[p], nil
	}
	var logged []string
	d := New(Options{Hub: &fakeHost{}, Store: newMemStore(), Logf: func(f string, a ...any) { logged = append(logged, fmt.Sprintf(f, a...)) }})
	m := &Machine{d: d, cfg: MachineConfig{ID: "here", Kind: "local"}}
	run := func(self string) string { return m.localNode(context.Background(), self, version) }
	gone, cancel := context.WithCancel(context.Background())
	cancel()
	said := func(what string, want ...string) {
		t.Helper()
		if len(logged) != 1 {
			t.Fatalf("%s: logged %q, want one line", what, logged)
		}
		for _, w := range want {
			if !strings.Contains(logged[0], w) {
				t.Fatalf("%s: logged %q, want %q", what, logged[0], w)
			}
		}
		logged = nil
	}

	if got := run(manager); got != node || len(logged) != 0 {
		t.Fatalf("persona-node of the build: %q %q", got, logged)
	}

	builds[node] = "persona 0.9 old"
	for range 2 {
		if got := run(manager); got != personad {
			t.Fatalf("persona-node of another build: %q", got)
		}
	}
	said("persona-node of another build", node+" is another build", "the node runs as personad")

	if err := os.Remove(node); err != nil {
		t.Fatal(err)
	}
	if got := run(manager); got != personad || len(logged) != 0 {
		t.Fatalf("personad of the build: %q %q", got, logged)
	}

	builds[personad] = "persona 0.9 old"
	for range 2 {
		if got := run(manager); got != manager {
			t.Fatalf("personad of another build: %q", got)
		}
	}
	said("personad of another build", personad+" is another build", "the node runs as persona-manager")
	m.localNode(gone, manager, version)
	if got := run(manager); got != manager || len(logged) != 0 {
		t.Fatalf("said again after a connection given up: %q %q", got, logged)
	}

	builds[personad] = build
	if got := m.localNode(gone, manager, version); len(logged) != 0 {
		t.Fatalf("connection given up while asking: %q %q", got, logged)
	}
	if got := run(manager); got != personad || len(logged) != 0 {
		t.Fatalf("after a connection given up: %q %q", got, logged)
	}
	for _, self := range []string{personad, filepath.Join(dir, compat.DaemonBinary)} {
		calls = 0
		if got := run(self); got != self || calls != 0 {
			t.Fatalf("self %s: %q, %d version -v", self, got, calls)
		}
	}

	write(node)
	builds[node] = build
	t.Setenv("PERSONA_SPLIT_PROGRAMS", "0")
	if got := run(manager); got != manager || len(logged) != 0 {
		t.Fatalf("opted out: %q %q", got, logged)
	}
}
