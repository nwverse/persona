package backup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// RecommendedDevelopmentBackupDir is where a Web development update keeps its
// snapshots unless the operator chose another directory: under the runtime's
// update/ directory, which a snapshot never collects, so a snapshot cannot
// contain an earlier one.
func RecommendedDevelopmentBackupDir(home string) string {
	return filepath.Join(home, "update", "development-backups")
}

// ValidateOutputDirectory checks and prepares a snapshot directory chosen in
// the Web UI and returns the canonical absolute path. It applies the same rules
// as Create's --output: the directory may not be inside a tree a snapshot
// collects (the knowledge base, managers/assistant, extensions, skills or
// hub.machines_kept). Snapshots hold database dumps, configuration and
// encryption keys, so the directory is created with 0700 permissions and must
// already be private when it exists; it may not be a symbolic link.
func ValidateOutputDirectory(home, dir string) (string, error) {
	name, err := expandHome(dir)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(name) {
		return "", errors.New("choose an absolute path, or one starting with ~")
	}
	clean := filepath.Clean(name)
	if clean == string(filepath.Separator) {
		return "", errors.New("the filesystem root is not a backup directory")
	}
	// Validate before creating anything: a refused path leaves no directory
	// behind inside the data a snapshot collects.
	if err := refuseOutputInsideCollectedTree(home, filepath.Join(clean, ".persona-snapshot")); err != nil {
		return "", err
	}
	info, err := os.Lstat(clean)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(clean, 0o700); err != nil {
			return "", err
		}
		info, err = os.Lstat(clean)
	}
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("the backup directory is a symbolic link; choose a real directory")
	}
	if !info.IsDir() {
		return "", errors.New("the backup path is not a directory")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("the backup directory is readable by other users (%04o); run chmod 700 on it", info.Mode().Perm())
	}
	// Resolve symlinked parents once, so the snapshot store is a real path
	// (and a path that only reaches a collected tree through a link is caught).
	resolved, err := filepath.EvalSymlinks(clean)
	if err != nil {
		return "", err
	}
	if err := refuseOutputInsideCollectedTree(home, filepath.Join(resolved, ".persona-snapshot")); err != nil {
		return "", err
	}
	if info, err := os.Stat(resolved); err != nil {
		return "", err
	} else if info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("the backup directory is readable by other users (%04o); run chmod 700 on it", info.Mode().Perm())
	}
	return resolved, nil
}

// expandHome expands a leading ~ or ~/ to the user's home directory. Other
// forms (~user) are left alone, so they fail the absolute-path check.
func expandHome(name string) (string, error) {
	if name == "" {
		return "", errors.New("choose a backup directory")
	}
	if name != "~" && !strings.HasPrefix(name, "~/") {
		return name, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, strings.TrimPrefix(name, "~")), nil
}
