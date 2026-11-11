package node

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// These tests use a synthetic temporary home and the actual in-memory RPC transport.
func TestRound3WhitespacePathIdentity(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows pathname rules do not admit these names")
	}

	for _, suffix := range []string{" ", "\u00a0"} {
		t.Run("read and rename distinct names ending in "+fmtWhitespace(suffix), func(t *testing.T) {
			f := isolatedWhitespaceFixture(t)
			dir := filepath.Join(f.n.home, "proj")
			plain := filepath.Join(dir, "note.txt")
			spaced := filepath.Join(dir, "note.txt"+suffix)
			makeFile(t, plain, "plain synthetic")
			makeFile(t, spaced, "spaced synthetic")
			a, ae := os.Stat(plain)
			b, be := os.Stat(spaced)
			if ae != nil || be != nil || os.SameFile(a, b) {
				t.Skip("temporary filesystem cannot distinguish the two names")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var listing struct {
				Entries []struct {
					Name string `json:"name"`
				} `json:"entries"`
			}
			if err := f.peer.Call(ctx, "fs.list", map[string]any{"path": "~/proj"}, &listing); err != nil {
				t.Fatal(err)
			}
			foundPlain, foundSpaced := false, false
			for _, row := range listing.Entries {
				foundPlain = foundPlain || row.Name == "note.txt"
				foundSpaced = foundSpaced || row.Name == "note.txt"+suffix
			}
			if !foundPlain || !foundSpaced {
				t.Fatalf("listing did not expose both exact names: %+v", listing.Entries)
			}
			var read struct {
				Text string `json:"text"`
			}
			if err := f.peer.Call(ctx, "file.read", map[string]any{"path": "~/proj/note.txt" + suffix}, &read); err != nil {
				t.Fatal(err)
			}
			if read.Text != "spaced synthetic" {
				t.Errorf("selected spaced filename read different file: %q", read.Text)
			}
			var renamed map[string]any
			if err := f.peer.Call(ctx, "file.rename", map[string]any{"path": "~/proj/note.txt" + suffix, "name": "renamed.txt"}, &renamed); err != nil {
				t.Fatal(err)
			}
			if got, err := os.ReadFile(filepath.Join(dir, "renamed.txt")); err != nil || string(got) != "spaced synthetic" {
				t.Errorf("renamed wrong content: %q %v", got, err)
			}
			if got, err := os.ReadFile(plain); err != nil || string(got) != "plain synthetic" {
				t.Errorf("unselected file changed: %q %v", got, err)
			}
			if _, err := os.Lstat(spaced); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("selected old name remains: %v", err)
			}
		})
	}

	t.Run("move respects a destination ending in space", func(t *testing.T) {
		f := isolatedWhitespaceFixture(t)
		dir := filepath.Join(f.n.home, "proj")
		makeFile(t, filepath.Join(dir, "source.txt"), "synthetic")
		for _, name := range []string{"dest", "dest "} {
			if err := os.MkdirAll(filepath.Join(dir, name), 0700); err != nil {
				t.Fatal(err)
			}
		}
		a, ae := os.Stat(filepath.Join(dir, "dest"))
		b, be := os.Stat(filepath.Join(dir, "dest "))
		if ae != nil || be != nil || os.SameFile(a, b) {
			t.Skip("temporary filesystem cannot distinguish destination names")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var out map[string]any
		if err := f.peer.Call(ctx, "file.move", map[string]any{"paths": []string{"~/proj/source.txt"}, "dir": "~/proj/dest "}, &out); err != nil {
			t.Fatal(err)
		}
		if got, err := os.ReadFile(filepath.Join(dir, "dest ", "source.txt")); err != nil || string(got) != "synthetic" {
			t.Errorf("selected destination missing content: %q %v", got, err)
		}
		if _, err := os.Lstat(filepath.Join(dir, "dest", "source.txt")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("unselected destination was modified: %v", err)
		}
	})

	t.Run("trash keeps the unselected plain name", func(t *testing.T) {
		f := isolatedWhitespaceFixture(t)
		dir := filepath.Join(f.n.home, "proj")
		t.Setenv("XDG_DATA_HOME", filepath.Join(f.n.home, "xdg"))
		plain, spaced := filepath.Join(dir, "note.txt"), filepath.Join(dir, "note.txt ")
		makeFile(t, plain, "plain synthetic")
		makeFile(t, spaced, "spaced synthetic")
		a, ae := os.Stat(plain)
		b, be := os.Stat(spaced)
		if ae != nil || be != nil || os.SameFile(a, b) {
			t.Skip("temporary filesystem cannot distinguish names")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var out map[string]any
		if err := f.peer.Call(ctx, "file.trash", map[string]any{"paths": []string{"~/proj/note.txt "}}, &out); err != nil {
			t.Fatal(err)
		}
		if got, err := os.ReadFile(plain); err != nil || string(got) != "plain synthetic" {
			t.Errorf("unselected name was trashed: %q %v", got, err)
		}
		if _, err := os.Lstat(spaced); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("selected spaced name remains: %v", err)
		}
	})

	t.Run("ordinary name control", func(t *testing.T) {
		f := isolatedWhitespaceFixture(t)
		dir := filepath.Join(f.n.home, "proj")
		makeFile(t, filepath.Join(dir, "normal.txt"), "normal")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var out map[string]any
		if err := f.peer.Call(ctx, "file.rename", map[string]any{"path": "~/proj/normal.txt", "name": "renamed.txt"}, &out); err != nil {
			t.Fatal(err)
		}
		if got, err := os.ReadFile(filepath.Join(dir, "renamed.txt")); err != nil || string(got) != "normal" {
			t.Fatalf("ordinary rename: %q %v", got, err)
		}
	})
}

func fmtWhitespace(s string) string {
	if s == " " {
		return "ASCII-space"
	}
	return "NBSP"
}
