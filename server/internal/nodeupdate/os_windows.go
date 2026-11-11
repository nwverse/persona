package nodeupdate

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"

	"github.com/nwverse/persona/server/internal/nodeproto"
	"github.com/nwverse/persona/server/internal/osproc"
)

// exeSuffix is what a file needs at the end of its name to run: CreateProcess (and exec.Command's lookup) will not
// start a file without it, so the staged build and the backup the watchdog runs from carry it too.
const exeSuffix = ".exe"

// retiredPrefix names a program file replaceFile moved aside: removeRetired deletes it once nothing runs from it.
const retiredPrefix = ".node-update-retired-"

// Restart asks the supervisor to start the installed program again: Windows has no exec, so this process exits with
// nodeproto.RestartExitCode, which persona-link's supervisor takes as "start it again now". Only a node run with
// --supervised gets here (New refuses updates otherwise). Like an exec it never returns: deferred cleanup is skipped.
func Restart(string) error {
	os.Exit(nodeproto.RestartExitCode)
	return nil
}

// replaceFile puts src at dst. Windows refuses to replace a file a process runs from, or one anybody holds open
// without FILE_SHARE_DELETE (os.Open never shares delete), but lets it be renamed: the file at dst moves aside to a
// retired name first, and back when src cannot take its place. MOVEFILE_WRITE_THROUGH returns only once the rename is
// on disk, which syncDir cannot do here.
func replaceFile(src, dst string) error {
	aside, err := retiredPath(filepath.Dir(dst))
	if err != nil {
		return err
	}
	if err := moveFile(dst, aside); err != nil {
		return err
	}
	if err := moveFile(src, dst); err != nil {
		_ = moveFile(aside, dst)
		return err
	}
	return nil
}

// retiredPath is a new retired name in dir.
func retiredPath(dir string) (string, error) {
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return filepath.Join(dir, retiredPrefix+hex.EncodeToString(random[:])), nil
}

// discard deletes path, or, while a process still runs from it (the watchdog runs from the backup it may restore),
// moves it to a retired name for removeRetired to delete once that process is gone.
func discard(path string) {
	if err := os.Remove(path); err == nil || os.IsNotExist(err) {
		return
	}
	if aside, err := retiredPath(filepath.Dir(path)); err == nil {
		_ = moveFile(path, aside)
	}
}

func moveFile(from, to string) error {
	f, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	t, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	if err := windows.MoveFileEx(f, t, windows.MOVEFILE_WRITE_THROUGH); err != nil {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: err}
	}
	return nil
}

// removeRetired deletes the files replaceFile moved aside in dir. One a process still runs from cannot be deleted
// yet and stays for a later call (the next confirmation, recovery or sweep).
func removeRetired(dir string) {
	paths, _ := filepath.Glob(filepath.Join(dir, retiredPrefix+"*"))
	for _, path := range paths {
		_ = os.Remove(path)
	}
}

// syncFile flushes the staged file to disk. FlushFileBuffers needs a handle that may write: one opened for reading
// only gets "access denied".
func syncFile(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

// syncDir does nothing: Windows cannot flush a directory handle opened for reading (FlushFileBuffers says access
// denied). The renames that matter are made durable by MOVEFILE_WRITE_THROUGH (replaceFile) instead.
func syncDir(string) error { return nil }

// processStart is pid's creation time through a process handle, exact to 100 ns (osproc.Identify): Windows has no ps.
func processStart(pid int) (string, error) {
	if id := osproc.Identify(pid); id.Start != "" {
		return id.Start, nil
	}
	return "", fmt.Errorf("no creation time for process %d", pid)
}
