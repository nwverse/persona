package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// release manifest no longer takes --min-node: it wrote a key nothing read, so a release could ask for a node order
// that never happened (docs/operations/hub-update.md, #3226).
func TestReleaseManifestHasNoMinNodeFlag(t *testing.T) {
	err := runRelease([]string{"manifest", "--min-node", "209901010000"})
	if err == nil || !strings.Contains(err.Error(), "min-node") {
		t.Fatalf("--min-node is still accepted: %v", err)
	}
}

// A served latest.json that is there but cannot be read fails check-next instead of counting as nothing served: a
// nil manifest passes the check whatever the build and commit, and publish.sh would publish an older build.
func TestReleaseCheckNextUnreadableServed(t *testing.T) {
	served := t.TempDir() // a directory: reading it fails
	err := runRelease([]string{"check-next", served, "--build", "209901010000", "--commit", "abc"})
	if err == nil || !strings.Contains(err.Error(), served) {
		t.Fatalf("want an error naming %s, got %v", served, err)
	}
	var ue usageError
	if errors.As(err, &ue) {
		t.Fatalf("a read failure reported as a usage mistake: %v", err)
	}
}

// Nothing served yet, a missing file or the empty one publish.sh writes when the fetch fails, still passes.
func TestReleaseCheckNextNothingServed(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "served.json")
	if err := os.WriteFile(empty, []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, served := range []string{filepath.Join(dir, "missing.json"), empty} {
		if err := runRelease([]string{"check-next", served, "--build", "209901010000", "--commit", "abc"}); err != nil {
			t.Fatalf("%s: %v", served, err)
		}
	}
}

// An argument left after the flags is a usage mistake: flag parsing stops there and would drop the flags after it.
func TestReleaseRefusesStrayArguments(t *testing.T) {
	served := filepath.Join(t.TempDir(), "missing.json")
	for _, args := range [][]string{
		{"check-next", served, "--build", "209901010000", "extra", "--commit", "abc"},
		{"manifest", "--dir", "d", "--out", "o", "--build", "209901010000", "extra"},
	} {
		var ue usageError
		if err := runRelease(args); !errors.As(err, &ue) || !strings.Contains(err.Error(), `"extra"`) {
			t.Fatalf("%v: want a usage error naming the argument, got %v", args, err)
		}
	}
}

// manifest without --dir or --out is a usage mistake naming the flag, before anything is read or written.
func TestReleaseManifestNeedsDirAndOut(t *testing.T) {
	for _, tc := range []struct {
		args []string
		flag string
	}{
		{[]string{"manifest", "--out", "o", "--build", "209901010000", "--commit", "abc"}, "--dir"},
		{[]string{"manifest", "--dir", "d", "--build", "209901010000", "--commit", "abc"}, "--out"},
	} {
		var ue usageError
		if err := runRelease(tc.args); !errors.As(err, &ue) || !strings.Contains(err.Error(), tc.flag) {
			t.Fatalf("%v: want a usage error naming %s, got %v", tc.args, tc.flag, err)
		}
	}
}
