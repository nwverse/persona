package backup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateOutputDirectoryCreatesPrivateDirectory(t *testing.T) {
	home := t.TempDir()
	want := RecommendedDevelopmentBackupDir(home)
	got, err := ValidateOutputDirectory(home, want)
	if err != nil {
		fatal(t, err)
	}
	resolvedHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		fatal(t, err)
	}
	if got != filepath.Join(resolvedHome, "update", "development-backups") {
		fatalf(t, "resolved directory = %q", got)
	}
	info, err := os.Stat(got)
	if err != nil {
		fatal(t, err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		fatalf(t, "directory mode %04o is not private", info.Mode().Perm())
	}
}

func TestValidateOutputDirectoryExpandsHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	got, err := ValidateOutputDirectory(home, "~/snapshots")
	if err != nil {
		fatal(t, err)
	}
	resolvedHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		fatal(t, err)
	}
	if got != filepath.Join(resolvedHome, "snapshots") {
		fatalf(t, "resolved directory = %q", got)
	}
}

func TestValidateOutputDirectoryRefusals(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "data"), 0o700); err != nil {
		fatal(t, err)
	}
	symlink := filepath.Join(home, "linked")
	if err := os.Symlink(filepath.Join(home, "elsewhere"), symlink); err != nil {
		fatal(t, err)
	}
	public := filepath.Join(home, "public")
	if err := os.Mkdir(public, 0o700); err != nil {
		fatal(t, err)
	}
	if err := os.Chmod(public, 0o755); err != nil {
		fatal(t, err)
	}
	file := filepath.Join(home, "a-file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		fatal(t, err)
	}
	for name, c := range map[string]struct {
		dir  string
		want string
	}{
		"relative":         {"relative/dir", "absolute"},
		"root":             {"/", "filesystem root"},
		"knowledge base":   {filepath.Join(home, "data", "snapshots"), "which the backup collects"},
		"manager files":    {filepath.Join(home, "managers", "assistant", "snapshots"), "which the backup collects"},
		"symlink":          {symlink, "symbolic link"},
		"public directory": {public, "readable by other users"},
		"not a directory":  {file, "not a directory"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ValidateOutputDirectory(home, c.dir); err == nil || !strings.Contains(err.Error(), c.want) {
				fatalf(t, "ValidateOutputDirectory(%q) = %v, want %q", c.dir, err, c.want)
			}
		})
	}
	if _, err := ValidateOutputDirectory(home, ""); err == nil {
		fatal(t, "accepted an empty directory")
	}
}

// A symlinked parent is resolved, and a path that reaches a collected tree
// through one is still refused.
func TestValidateOutputDirectoryFollowsParentsButRefusesCollectedTargets(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "data"), 0o700); err != nil {
		fatal(t, err)
	}
	elsewhere := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.Mkdir(elsewhere, 0o700); err != nil {
		fatal(t, err)
	}
	link := filepath.Join(home, "link")
	if err := os.Symlink(elsewhere, link); err != nil {
		fatal(t, err)
	}
	got, err := ValidateOutputDirectory(home, filepath.Join(link, "snapshots"))
	if err != nil {
		fatal(t, err)
	}
	resolvedElsewhere, err := filepath.EvalSymlinks(elsewhere)
	if err != nil {
		fatal(t, err)
	}
	if got != filepath.Join(resolvedElsewhere, "snapshots") {
		fatalf(t, "resolved directory = %q", got)
	}

	intoKB := filepath.Join(home, "intolink")
	if err := os.Symlink(filepath.Join(home, "data"), intoKB); err != nil {
		fatal(t, err)
	}
	if _, err := ValidateOutputDirectory(home, intoKB); err == nil || !strings.Contains(err.Error(), "which the backup collects") {
		fatalf(t, "accepted a symlink into the knowledge base: %v", err)
	}
	if _, err := ValidateOutputDirectory(home, filepath.Join(intoKB, "snapshots")); err == nil {
		fatal(t, "accepted a directory reached through a link into the knowledge base")
	}
}
