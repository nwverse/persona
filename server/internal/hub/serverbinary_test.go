package hub

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The server's binary is the one running, unless that is persona-hub (the hub alone): then the personad beside it,
// resolved through symlinks, and an error when there is none (never persona-hub itself, which runs no built-in
// manager and is no update target).
func TestServerBinary(t *testing.T) {
	dir := realDir(t)
	touch := func(name string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	personad, hub := touch("personad"), touch("persona-hub")
	for _, c := range []struct{ running, want string }{
		{personad, personad},
		{hub, personad},
		{touch("personad-linux-amd64"), filepath.Join(dir, "personad-linux-amd64")}, // the Docker image's
		{touch("hub.test"), filepath.Join(dir, "hub.test")},
	} {
		if got, err := serverBinaryOf(c.running); err != nil || got != c.want {
			t.Errorf("%s: %q %v, want %q", filepath.Base(c.running), got, err, c.want)
		}
	}
	if got, err := serverBinaryOf(touch("persona-hub.exe")); err == nil {
		t.Errorf("persona-hub.exe without personad.exe beside it: %q", got)
	}
	touch("personad.exe")
	if got, err := serverBinaryOf(filepath.Join(dir, "persona-hub.exe")); err != nil || got != filepath.Join(dir, "personad.exe") {
		t.Errorf("persona-hub.exe: %q %v", got, err)
	}
	alone := filepath.Join(t.TempDir(), "persona-hub")
	if err := os.WriteFile(alone, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := serverBinaryOf(alone); err == nil {
		t.Errorf("persona-hub without personad beside it: %q", got)
	}
	// An install under the former name: personad is a link to the real file, which the update replaces.
	legacy := realDir(t)
	real := filepath.Join(legacy, "legacyd")
	if err := os.WriteFile(real, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("legacyd", filepath.Join(legacy, "personad")); err != nil {
		t.Fatal(err)
	}
	if got, err := serverBinaryOf(filepath.Join(legacy, "persona-hub")); err != nil || got != real {
		t.Errorf("personad as a link: %q %v, want %q", got, err, real)
	}
}

// The built-in manager the hub starts: personad's own binary when the hub runs as personad (an install keeps it until
// its owner switches); for persona-hub, the persona-manager beside it when it is the same build, else the personad
// beside it, saying why when a persona-manager was there.
func TestManagerBinary(t *testing.T) {
	versions := map[string]string{} // file name -> what `version -v` says ("" fails)
	version := func(_ context.Context, path string) (string, error) {
		if v := versions[filepath.Base(path)]; v != "" {
			return v, nil
		}
		return "", errors.New("exec format error")
	}
	setup := func(t *testing.T, files ...string) string {
		dir := realDir(t)
		for _, f := range files {
			if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}
	ctx := context.Background()
	versions["persona-hub"], versions["persona-hub.exe"], versions["personad"] = "v2\nchannel release, build 2", "v2\nchannel release, build 2", "v2\nchannel release, build 2"

	dir := setup(t, "personad", "persona-hub", "persona-manager")
	if got, note, err := managerBinary(ctx, filepath.Join(dir, "personad"), version); err != nil || got != filepath.Join(dir, "personad") || note != "" {
		t.Errorf("run as personad: %q %q %v", got, note, err)
	}
	versions["persona-manager"] = "v2\nchannel release, build 2"
	if got, note, err := managerBinary(ctx, filepath.Join(dir, "persona-hub"), version); err != nil || got != filepath.Join(dir, "persona-manager") || note != "" {
		t.Errorf("same build: %q %q %v", got, note, err)
	}
	versions["persona-manager"] = "v1\nchannel release, build 1"
	if got, note, err := managerBinary(ctx, filepath.Join(dir, "persona-hub"), version); err != nil || got != filepath.Join(dir, "personad") ||
		!strings.Contains(note, "another build") {
		t.Errorf("another build: %q %q %v", got, note, err)
	}
	versions["persona-manager"] = ""
	if got, note, err := managerBinary(ctx, filepath.Join(dir, "persona-hub"), version); err != nil || got != filepath.Join(dir, "personad") ||
		!strings.Contains(note, "does not run") {
		t.Errorf("one that does not run: %q %q %v", got, note, err)
	}
	dir = setup(t, "personad", "persona-hub")
	if got, note, err := managerBinary(ctx, filepath.Join(dir, "persona-hub"), version); err != nil || got != filepath.Join(dir, "personad") || note != "" {
		t.Errorf("no persona-manager: %q %q %v", got, note, err)
	}
	// The Docker image: persona-manager beside persona-hub, no plain personad.
	versions["persona-manager"] = "v2\nchannel release, build 2"
	dir = setup(t, "persona-hub", "persona-manager", "personad-linux-amd64")
	if got, _, err := managerBinary(ctx, filepath.Join(dir, "persona-hub"), version); err != nil || got != filepath.Join(dir, "persona-manager") {
		t.Errorf("the image's layout: %q %v", got, err)
	}
	dir = setup(t, "persona-hub")
	if got, _, err := managerBinary(ctx, filepath.Join(dir, "persona-hub"), version); err == nil {
		t.Errorf("neither persona-manager nor personad: %q", got)
	}
	versions["persona-manager.exe"] = "v2\nchannel release, build 2"
	dir = setup(t, "persona-hub.exe", "persona-manager.exe")
	if got, _, err := managerBinary(ctx, filepath.Join(dir, "persona-hub.exe"), version); err != nil || got != filepath.Join(dir, "persona-manager.exe") {
		t.Errorf("Windows names: %q %v", got, err)
	}
}

// realDir is a temporary directory by its path with symlinks resolved (macOS's /var is one), as the running binary's.
func realDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}
