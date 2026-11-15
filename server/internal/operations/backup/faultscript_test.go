package backup

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

// scriptedBackend fails the Exists, Rename and Drop calls it receives
// according to a script: 0 works, 1 fails without effect, 2 takes effect and
// then fails (Exists has no effect, so 2 is a plain failure). Past the end of
// the script every call works.
type scriptedBackend struct {
	*fakeBackend
	script []int
	next   int
}

func (s *scriptedBackend) outcome() int {
	if s.next < len(s.script) {
		s.next++
		return s.script[s.next-1]
	}
	return 0
}

func (s *scriptedBackend) Exists(ctx context.Context, target Target, name string) (bool, error) {
	if s.outcome() != 0 {
		return false, errors.New("scripted: cannot check")
	}
	return s.fakeBackend.Exists(ctx, target, name)
}

func (s *scriptedBackend) Rename(ctx context.Context, target Target, from, to string) error {
	switch s.outcome() {
	case 1:
		return errors.New("scripted: rename failed")
	case 2:
		if err := s.fakeBackend.Rename(ctx, target, from, to); err != nil {
			return err
		}
		return errors.New("scripted: rename committed, then failed")
	}
	return s.fakeBackend.Rename(ctx, target, from, to)
}

func (s *scriptedBackend) Drop(ctx context.Context, target Target, name string) error {
	switch s.outcome() {
	case 1:
		return errors.New("scripted: drop failed")
	case 2:
		_ = s.fakeBackend.Drop(ctx, target, name)
		return errors.New("scripted: drop committed, then failed")
	}
	return s.fakeBackend.Drop(ctx, target, name)
}

type counterReader struct{ n byte }

func (c *counterReader) Read(p []byte) (int, error) {
	for i := range p {
		c.n++
		p[i] = c.n
	}
	return len(p), nil
}

// runFaultScript is the database half of Restore (swap, then the discard of the
// stages Restore defers) against one script, then what Restore decides about
// the files, and the invariants that must hold whatever the script did.
func runFaultScript(t *testing.T, script []int) (kept bool, failed bool) {
	t.Helper()
	fake := newFakeBackend()
	backend := &scriptedBackend{fakeBackend: fake, script: script}
	hub := Target{Part: "hub", URL: "postgres://u@db.example/persona", Database: "persona"}
	manager := Target{Part: "manager", URL: "postgres://u@db.example/persona_manager", Database: "persona_manager"}
	fake.add(hub, hub.Database, "orig-hub")
	fake.add(manager, manager.Database, "orig-manager")
	fake.add(hub, "persona_staged_h_0", "bundle-hub")
	fake.add(manager, "persona_staged_m_0", "bundle-manager")
	hubStage := &stagedDatabase{name: "persona_staged_h_0", active: true}
	managerStage := &stagedDatabase{name: "persona_staged_m_0", active: true}
	operator := &Operator{backend: backend, random: &counterReader{}}
	label := fmt.Sprint(script)

	previous, err := operator.swapDatabases(context.Background(), hub, manager, hubStage, managerStage)
	operator.discardStage(hub, hubStage)
	operator.discardStage(manager, managerStage)
	server := fake.server(hub)
	var undoFailed *undoFailedError
	kept = errors.As(err, &undoFailed) && undoFailed.bundleMayBeLive
	failed = err != nil

	if err == nil {
		for _, part := range []struct{ name, bundle, role string }{{"persona", "bundle-hub", "h"}, {"persona_manager", "bundle-manager", "m"}} {
			if server[part.name] == nil || server[part.name].dump != part.bundle {
				t.Fatalf("script %s: success, but %s is %+v", label, part.name, server[part.name])
			}
		}
		if previous[0] == "" || previous[1] == "" || server[previous[0]] == nil || server[previous[1]] == nil {
			t.Fatalf("script %s: success without the previous databases %v", label, previous)
		}
		return false, false
	}
	message := err.Error()
	// Never: a bundle database is live and the files are rolled back.
	if !kept {
		for _, name := range []string{"persona", "persona_manager"} {
			if database := server[name]; database != nil && strings.HasPrefix(database.dump, "bundle-") {
				t.Fatalf("script %s: %s is the bundle's and the files would be rolled back (err %v)", label, name, err)
			}
		}
	}
	// Never: an original database is lost, and every parked one is named.
	for _, part := range []struct{ target, dump, role string }{{"persona", "orig-hub", "h"}, {"persona_manager", "orig-manager", "m"}} {
		var homes []string
		for name, database := range server {
			if database.dump == part.dump {
				homes = append(homes, name)
			}
		}
		sort.Strings(homes)
		if len(homes) == 0 {
			t.Fatalf("script %s: the original %s is lost (err %v)", label, part.dump, err)
		}
		for _, name := range homes {
			if name != part.target && !strings.Contains(message, name) {
				t.Fatalf("script %s: the original %s is parked as %s, which the error does not name: %v", label, part.dump, name, err)
			}
		}
	}
	// Re-running converges: with the server healthy, staging again and
	// swapping puts the bundle's databases in place and keeps the originals.
	backend.script, backend.next = nil, 0
	fake.add(hub, "persona_staged_h_1", "bundle-hub")
	fake.add(manager, "persona_staged_m_1", "bundle-manager")
	if _, rerunErr := operator.swapDatabases(context.Background(), hub, manager,
		&stagedDatabase{name: "persona_staged_h_1", active: true}, &stagedDatabase{name: "persona_staged_m_1", active: true}); rerunErr != nil {
		t.Fatalf("script %s: re-running does not converge: %v", label, rerunErr)
	}
	if server["persona"].dump != "bundle-hub" || server["persona_manager"].dump != "bundle-manager" {
		t.Fatalf("script %s: after re-running the live databases are %+v and %+v", label, server["persona"], server["persona_manager"])
	}
	for _, dump := range []string{"orig-hub", "orig-manager"} {
		found := false
		for _, database := range server {
			found = found || database.dump == dump
		}
		if !found {
			t.Fatalf("script %s: re-running lost the original %s", label, dump)
		}
	}
	return kept, true
}

// Every combination of the first eight calls working, failing without effect or
// failing after taking effect, and a fixed random set of longer scripts: the
// swap and its rollback never leave a bundle database live under rolled-back
// files, never lose an original database, always name the parked ones, and a
// re-run converges.
func TestSwapSurvivesEveryFaultScript(t *testing.T) {
	const exhaustive = 8
	runs, failures, kept := 0, 0, 0
	for code := 0; code < pow3(exhaustive); code++ {
		script := make([]int, exhaustive)
		for i, rest := 0, code; i < exhaustive; i, rest = i+1, rest/3 {
			script[i] = rest % 3
		}
		k, failed := runFaultScript(t, script)
		runs++
		if failed {
			failures++
		}
		if k {
			kept++
		}
	}
	random := rand.New(rand.NewSource(20261115))
	for run := 0; run < 4000; run++ {
		script := make([]int, 18)
		for i := range script {
			if random.Intn(3) == 0 {
				script[i] = 1 + random.Intn(2)
			}
		}
		k, failed := runFaultScript(t, script)
		runs++
		if failed {
			failures++
		}
		if k {
			kept++
		}
	}
	// The scripts must really exercise the failure and the keep-the-files paths.
	if failures < 1000 || kept < 100 {
		t.Fatalf("scripts too tame: %d runs, %d failed, %d kept the files", runs, failures, kept)
	}
	t.Logf("%d scripts, %d failed swaps, %d kept the files", runs, failures, kept)
}

func pow3(n int) int {
	result := 1
	for ; n > 0; n-- {
		result *= 3
	}
	return result
}
