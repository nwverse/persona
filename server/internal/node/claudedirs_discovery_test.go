package node

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
)

func retainedClaudeDirs(t testing.TB) (*Node, []string) {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home := t.TempDir()
	dirs := []string{filepath.Join(home, ".claude"), filepath.Join(home, "profile1"), filepath.Join(home, "profile2")}
	for _, dir := range dirs {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	n := &Node{home: home, dir: filepath.Join(home, "node"), cdirs: newClaudeDirs(), state: &stateFile{Sessions: map[string]sessionState{}}}
	n.cdirs.scan = func() map[int]procEnv { return nil }
	n.setBlockedClaudeDirs([]string{"~/unused"})
	for i := range 1000 {
		n.state.Sessions[strconv.Itoa(i)] = sessionState{ConfigDir: dirs[i%len(dirs)]}
	}
	return n, dirs
}

func TestClaudeDiscoveryResolvesDistinctDirectoriesOnce(t *testing.T) {
	n, dirs := retainedClaudeDirs(t)
	for range 2 {
		calls := map[string]int{}
		got := n.discoverClaudeDirs(func(path string) (string, error) {
			calls[path]++
			return filepath.EvalSymlinks(path)
		})
		if len(got) != len(dirs) || got[0] != dirs[0] || len(calls) != len(dirs) {
			t.Fatalf("directories %v, calls %v", got, calls)
		}
		for _, dir := range dirs {
			if calls[dir] != 1 || !slices.Contains(got, dir) {
				t.Fatalf("directories %v, calls %v", got, calls)
			}
		}
	}
}

func BenchmarkClaudeDiscoveryRetainedSessions(b *testing.B) {
	n, _ := retainedClaudeDirs(b)
	b.ReportAllocs()
	for b.Loop() {
		n.claudeConfigDirs()
	}
}

func TestClaudeDiscoverySeesDirectoryChangesImmediately(t *testing.T) {
	n, dirs := retainedClaudeDirs(t)
	profile := filepath.Join(n.home, "new-profile")
	contains := func(path string, want bool) {
		t.Helper()
		if got := n.claudeConfigDirs(); slices.Contains(got, path) != want {
			t.Fatalf("contains %s: want %v, got %v", path, want, got)
		}
	}
	n.setKnownClaudeDirs([]string{profile})
	contains(profile, false)
	if err := os.Mkdir(profile, 0o700); err != nil {
		t.Fatal(err)
	}
	contains(profile, true)
	n.setKnownClaudeDirs(nil)
	contains(profile, false)
	n.claudeStartDir.Store(&profile)
	contains(profile, true)
	n.claudeStartDir.Store(nil)
	contains(profile, false)
	n.state.put("new", sessionState{ConfigDir: profile})
	contains(profile, true)
	n.setBlockedClaudeDirs([]string{profile})
	contains(profile, false)
	n.setBlockedClaudeDirs(nil)
	contains(profile, true)
	if err := os.Remove(profile); err != nil {
		t.Fatal(err)
	}
	contains(profile, false)
	contains(dirs[0], true)
}

func TestClaudeDiscoveryResolvesParentAfterSymlink(t *testing.T) {
	n, dirs := retainedClaudeDirs(t)
	subdir := filepath.Join(dirs[2], "subdir")
	if err := os.Mkdir(subdir, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(n.home, "linked-subdir")
	if err := os.Symlink(subdir, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	// Join would clean ".." and select home instead of the symlink target's parent.
	spelling := link + string(filepath.Separator) + ".."
	n.claudeStartDir.Store(&spelling)
	got := n.claudeConfigDirs()
	if !slices.Contains(got, spelling) || slices.Contains(got, dirs[2]) {
		t.Fatalf("expected %q to represent %q, got %v", spelling, dirs[2], got)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dirs[1], link); err != nil {
		t.Fatal(err)
	}
	got = n.claudeConfigDirs()
	if !slices.Contains(got, spelling) || !slices.Contains(got, dirs[2]) {
		t.Fatalf("retargeted link: %v", got)
	}
}
