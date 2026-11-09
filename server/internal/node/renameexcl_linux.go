package node

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// renameExcl renames from to to unless to exists (os.ErrExist then): the check and the rename are one call, so a
// file that appears meanwhile is never replaced.
func renameExcl(from, to string) error {
	err := unix.Renameat2(unix.AT_FDCWD, from, unix.AT_FDCWD, to, unix.RENAME_NOREPLACE)
	if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EINVAL) { // an old kernel or a file system without it
		return renameIfFree(from, to)
	}
	if err != nil {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: err}
	}
	return nil
}
