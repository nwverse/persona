//go:build !windows

package node

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// A symlink at the log's name is not followed: the file it points to stays as it was.
func TestHerdrLogDoesNotFollowASymlink(t *testing.T) {
	dir := t.TempDir()
	sentinel := filepath.Join(dir, "sentinel")
	if err := os.WriteFile(sentinel, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(dir, "persona-herdr-server.log")
	if err := os.Symlink(sentinel, log); err != nil {
		t.Fatal(err)
	}
	if f, err := openHerdrLog(log); err == nil {
		f.Close()
		t.Fatal("opened the log through a symlink")
	}
	if b, _ := os.ReadFile(sentinel); string(b) != "keep me" {
		t.Fatalf("the symlink's target now holds %q", b)
	}
}

// A FIFO at the log's name fails the open instead of hanging it.
func TestHerdrLogRefusesAFIFO(t *testing.T) {
	log := filepath.Join(t.TempDir(), "persona-herdr-server.log")
	if err := syscall.Mkfifo(log, 0o600); err != nil {
		t.Skip("mkfifo:", err)
	}
	if f, err := openHerdrLog(log); err == nil {
		f.Close()
		t.Fatal("opened a FIFO as the log")
	}
}

// The log of the last start is emptied for the next one.
func TestHerdrLogEmptiesTheLastOne(t *testing.T) {
	log := filepath.Join(t.TempDir(), "persona-herdr-server.log")
	if err := os.WriteFile(log, []byte("the last start"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := openHerdrLog(log)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("new"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if b, _ := os.ReadFile(log); string(b) != "new" {
		t.Fatalf("log holds %q, want %q", b, "new")
	}
}
