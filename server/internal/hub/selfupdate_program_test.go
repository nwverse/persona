package hub

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/nwverse/persona/server/internal/update"
)

// splitRelease is a release whose manifest carries persona-hub and persona-manager besides personad (as publish.sh
// makes it), each a script answering `version -v` as the release's build. Its code contains gitA's.
func splitRelease(t *testing.T, programs ...string) *fakePublisher {
	t.Helper()
	plat := runtime.GOOS + "-" + runtime.GOARCH
	prog := []byte("#!/bin/sh\necho v2-202611021700\necho 'channel release, build 202611021700'\n")
	pgz := gz(prog)
	outer, inner := sha256.Sum256(pgz), sha256.Sum256(prog)
	extra := map[string][]byte{}
	p := newPublisherWith(t, "v2-202611021700", 202611021700, func(m *update.Manifest) {
		m.Commit = gitB
		m.Ancestors, _ = update.EncodeAncestors([]string{gitB, gitA})
		m.Programs = map[string]map[string]update.File{}
		for _, name := range programs {
			f := update.File{Name: name + "-" + plat + ".gz", Size: int64(len(pgz)), SHA256: hex.EncodeToString(outer[:]),
				BinarySHA256: hex.EncodeToString(inner[:])}
			m.Programs[name] = map[string]update.File{plat: f}
			extra["/dl/"+f.Name] = pgz
		}
	})
	p.extra = extra
	return p
}

// runningAs makes the updating hub run as the program name beside its target (the old build's), and returns its path.
func runningAs(t *testing.T, h *Hub, target, name string) string {
	t.Helper()
	self := filepath.Join(filepath.Dir(target), name)
	if err := os.WriteFile(self, []byte("#!/bin/sh\necho old "+name+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	h.upd.self = func() (string, error) { return self, nil }
	return self
}

// A hub run as persona-hub goes on as the persona-hub the update swapped (not as personad, the target), and going
// back runs the old persona-hub again; the pending marker keeps personad as its Target for older builds.
func TestUpdateGoesOnAsPersonaHub(t *testing.T) {
	p := splitRelease(t, "persona-hub", "persona-manager")
	h, _, target, execs := updatingHub(t, p, "release")
	self := runningAs(t, h, target, "persona-hub")
	ctx := context.Background()
	if _, err := h.upd.check(ctx); err != nil {
		t.Fatal(err)
	}
	if err := h.upd.begin(ctx, true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the restart", func() bool { return len(execs()) == 1 })
	if execs()[0] != self {
		t.Fatalf("restarted into %q, not the hub's own program %q", execs()[0], self)
	}
	var pend update.Pending
	if ok, _ := update.Load(update.PendingPath(h.upd.dir), &pend); !ok || pend.Target != target || pend.Run != self {
		t.Fatalf("pending %+v", pend)
	}
	if b, _ := os.ReadFile(self); string(b) == "#!/bin/sh\necho old persona-hub\n" {
		t.Fatal("persona-hub was not swapped")
	}
	h.upd.rollback(pend, "test")
	if got := execs(); len(got) != 2 || got[1] != self {
		t.Fatalf("going back ran %v", got)
	}
	if b, _ := os.ReadFile(self); string(b) != "#!/bin/sh\necho old persona-hub\n" {
		t.Fatal("going back did not restore persona-hub")
	}
	if b, _ := os.ReadFile(target); string(b) != "#!/bin/sh\necho old\n" {
		t.Fatal("going back did not restore personad")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(target), "persona-manager")); !os.IsNotExist(err) {
		t.Fatal("going back left persona-manager, which was not there before")
	}
}

// Behind the edge the update hands over to the hub's own program too.
func TestUpdateHandsOverToPersonaHub(t *testing.T) {
	p := splitRelease(t, "persona-hub", "persona-manager")
	h, _, target, execs := updatingHub(t, p, "release")
	self := runningAs(t, h, target, "persona-hub")
	var mu sync.Mutex
	var handed []string
	h.upd.handover = func(ctx context.Context, bin string, build int64) error {
		mu.Lock()
		handed = append(handed, bin)
		mu.Unlock()
		return nil
	}
	ctx := context.Background()
	if _, err := h.upd.check(ctx); err != nil {
		t.Fatal(err)
	}
	if err := h.upd.begin(ctx, true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the handover", func() bool { mu.Lock(); defer mu.Unlock(); return len(handed) == 1 })
	if handed[0] != self || len(execs()) != 0 {
		t.Fatalf("handed over to %v (execs %v), not %q", handed, execs(), self)
	}
}

// A release without persona-hub leaves a hub run as persona-hub by hand (no service runs it: unreplaced) on personad,
// which the update did replace, rather than on the persona-hub it did not; a hub run as personad goes on as personad
// whatever the release carries.
func TestUpdateStaysOnPersonadWithoutAPersonaHub(t *testing.T) {
	for _, c := range []struct {
		name, runs string
		programs   []string
	}{
		{"persona-hub, release without it", "persona-hub", []string{"persona-manager"}},
		{"personad, release with both", "personad", []string{"persona-hub", "persona-manager"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := splitRelease(t, c.programs...)
			h, _, target, execs := updatingHub(t, p, "release")
			if c.runs == "personad" {
				h.upd.self = func() (string, error) { return target, nil }
			} else {
				runningAs(t, h, target, c.runs)
			}
			ctx := context.Background()
			if _, err := h.upd.check(ctx); err != nil {
				t.Fatal(err)
			}
			if err := h.upd.begin(ctx, true); err != nil {
				t.Fatal(err)
			}
			waitFor(t, "the restart", func() bool { return len(execs()) == 1 })
			var pend update.Pending
			if ok, _ := update.Load(update.PendingPath(h.upd.dir), &pend); !ok || pend.Run != "" || execs()[0] != target {
				t.Fatalf("restarted into %v, pending %+v; want %q", execs(), pend, target)
			}
		})
	}
}
