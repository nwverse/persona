package node

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// cacheEnv points the user cache folder (or, with cache false, none at all) and the temp folder at fresh folders,
// and forgets the per-process temp folders made so far.
func cacheEnv(t *testing.T, cache bool) (cacheDir, tmp string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the user cache folder is LocalAppData on Windows")
	}
	tmp = t.TempDir()
	t.Setenv("TMPDIR", tmp)
	if cache {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
		var err error
		if cacheDir, err = os.UserCacheDir(); err != nil {
			t.Fatal(err)
		}
	} else {
		t.Setenv("HOME", "")
		t.Setenv("XDG_CACHE_HOME", "")
		if _, err := os.UserCacheDir(); err == nil {
			t.Skip("this system has a user cache folder without HOME")
		}
	}
	reset := func() {
		processTempMu.Lock()
		clear(processTempDirs)
		processTempMu.Unlock()
	}
	reset()
	t.Cleanup(reset)
	return cacheDir, tmp
}

func TestPrivateCacheDirIsTheUsersOwn(t *testing.T) {
	cache, _ := cacheEnv(t, true)
	dir, err := privateCacheDir("things")
	if err != nil {
		t.Fatal(err)
	}
	if dir != filepath.Join(cache, "persona", "things") {
		t.Fatalf("dir %s, want under %s", dir, cache)
	}
	if st, err := os.Stat(dir); err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode %v (%v), want 0700", st.Mode(), err)
	}
}

// A folder others can write to, or a symlink to one, is refused rather than used.
func TestPrivateCacheDirRefusesAnOpenFolder(t *testing.T) {
	cache, _ := cacheEnv(t, true)
	open := filepath.Join(cache, "persona", "things")
	if err := os.MkdirAll(open, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(open, 0o777); err != nil {
		t.Fatal(err)
	}
	if dir, err := privateCacheDir("things"); err == nil {
		t.Fatalf("used %s, which others can write to", dir)
	}
	elsewhere := t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(cache, "persona", "linked")); err != nil {
		t.Fatal(err)
	}
	if dir, err := privateCacheDir("linked"); err == nil {
		t.Fatalf("used %s through a symlink", dir)
	}
}

// Without a user cache folder the files go to a folder of this process's own in the temp folder, not to a fixed name
// another user could have made first.
func TestPrivateCacheDirFallbackIsThisProcesss(t *testing.T) {
	_, tmp := cacheEnv(t, false)
	planted := filepath.Join(tmp, "persona-things")
	if err := os.Mkdir(planted, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(planted, 0o777); err != nil {
		t.Fatal(err)
	}
	dir, err := privateCacheDir("things")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(dir) != tmp || dir == planted || !strings.HasPrefix(filepath.Base(dir), "persona-things-") {
		t.Fatalf("fallback dir %s, want a fresh one in %s", dir, tmp)
	}
	if st, err := os.Stat(dir); err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode %v (%v), want 0700", st.Mode(), err)
	}
	if again, err := privateCacheDir("things"); err != nil || again != dir {
		t.Fatalf("second call %s (%v), want the same %s", again, err, dir)
	}
}
