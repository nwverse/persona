package node

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/nwverse/persona/server/internal/osproc"
)

// privateCacheDir is <user cache>/persona/<name>, for files of this user only (images sent to an agent, a server's
// log). It is made 0700 and refused when it is a symlink, not a directory, or (where the system has owners and modes)
// another user's or open to group or others. Without a user cache folder (no HOME) it is a folder of this process's
// own in the temp folder, made by os.MkdirTemp: never a fixed name there, which another local user could create first
// and so read or swap what the node writes.
func privateCacheDir(name string) (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return processTempDir(name)
	}
	dir := filepath.Join(base, "persona", name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, privateDir(dir)
}

// processTempDirs are the temp folders processTempDir made, by name. Nothing removes them when the process ends; the
// system's temp cleanup does.
var (
	processTempMu   sync.Mutex
	processTempDirs = map[string]string{}
)

// processTempDir is this process's own folder for name in the temp folder, made once (again if it went away).
func processTempDir(name string) (string, error) {
	processTempMu.Lock()
	defer processTempMu.Unlock()
	if dir, ok := processTempDirs[name]; ok && privateDir(dir) == nil {
		return dir, nil
	}
	dir, err := os.MkdirTemp("", "persona-"+name+"-")
	if err != nil {
		return "", err
	}
	processTempDirs[name] = dir
	return dir, nil
}

// privateDir refuses a folder others could reach into. Windows reports no owner and every folder as 0777; there the
// user's own cache folder (LocalAppData) keeps it private.
func privateDir(dir string) error {
	st, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	if runtime.GOOS != "windows" && (st.Mode().Perm()&0o077 != 0 || !osproc.OwnedByMe(st)) {
		return fmt.Errorf("%s is not private to this user (mode %v)", dir, st.Mode().Perm())
	}
	return nil
}
