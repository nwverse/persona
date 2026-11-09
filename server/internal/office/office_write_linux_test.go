package office

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
)

// A write that fails part way (here at the file size limit, as on a full disk) leaves the earlier file as it was: the
// new one is written beside it and renamed over it only once it is complete. Linux only, for RLIMIT_FSIZE; Go ignores
// SIGXFSZ, so the write just fails. The limit is the whole process's, which is safe because no test here is parallel.
func TestMainFailedWriteKeepsTheEarlierFile(t *testing.T) {
	dir := t.TempDir()
	md := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(md, []byte("# Plan\n\nOne paragraph.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := Main([]string{"docx", md}, io.Discard, io.Discard); code != 0 {
		t.Fatalf("exit %d", code)
	}
	doc := filepath.Join(dir, "notes.docx")
	v1, err := os.ReadFile(doc)
	if err != nil {
		t.Fatal(err)
	}
	var long strings.Builder
	long.WriteString("# Plan\n\n")
	for i := range 400 {
		fmt.Fprintf(&long, "Paragraph %d of the second version, %x.\n\n", i, i*7919)
	}
	if err := os.WriteFile(md, []byte(long.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	limit := uint64(len(v1) + 100)
	if v2, err := Docx([]byte(long.String()), dir, "notes"); err != nil || uint64(len(v2)) <= limit {
		t.Fatalf("the second version is %d bytes, not over %d: %v", len(v2), limit, err)
	}

	var old syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_FSIZE, &old); err != nil {
		t.Fatal(err)
	}
	if old.Max < limit {
		t.Skipf("the file size limit is already %d bytes", old.Max)
	}
	restore := sync.OnceFunc(func() {
		if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &old); err != nil {
			t.Errorf("restoring the file size limit: %v", err)
		}
	})
	t.Cleanup(restore)
	if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &syscall.Rlimit{Cur: limit, Max: old.Max}); err != nil {
		t.Fatal(err)
	}
	var errOut bytes.Buffer
	code := Main([]string{"docx", md}, io.Discard, &errOut)
	restore()

	if code != 1 || !strings.Contains(errOut.String(), "file too large") {
		t.Errorf("exit %d: %s", code, errOut.String())
	}
	if after, err := os.ReadFile(doc); err != nil || !bytes.Equal(after, v1) {
		t.Fatalf("the earlier file changed (%d bytes, was %d): %v", len(after), len(v1), err)
	}
	if info, err := Check(doc); err != nil || fmt.Sprint(info["headings"]) != "[Plan]" {
		t.Errorf("check: %v %v", info, err)
	}
	if names := dirNames(t, dir); !slices.Equal(names, []string{"notes.docx", "notes.md"}) {
		t.Errorf("left behind: %q", names)
	}
}

// -o names a file of its kind only (docs/assistant/office.md, "No approval every time"), and a link by that name to
// some other file is replaced, not written through.
func TestMainReplacesALinkAtTheOutput(t *testing.T) {
	dir := t.TempDir()
	rc := filepath.Join(dir, ".zshrc")
	if err := os.WriteFile(rc, []byte("export EDITOR=vi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "notes.docx")
	if err := os.Symlink(rc, out); err != nil {
		t.Fatal(err)
	}
	md := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(md, []byte("# Plan\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var errOut bytes.Buffer
	if code := Main([]string{"docx", md, "-o", out}, io.Discard, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if b, err := os.ReadFile(rc); err != nil || string(b) != "export EDITOR=vi\n" {
		t.Errorf("wrote through the link: %s is %d bytes: %v", rc, len(b), err)
	}
	if st, err := os.Lstat(out); err != nil || !st.Mode().IsRegular() {
		t.Errorf("%s is not a file of its own: %v %v", out, st, err)
	}
}

// Replacing an earlier output keeps its permissions, as writing into it did.
func TestMainKeepsTheOutputsPermissions(t *testing.T) {
	dir := t.TempDir()
	md := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(md, []byte("# Plan\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "notes.docx")
	for _, want := range []os.FileMode{0o644, 0o600} {
		if code := Main([]string{"docx", md}, io.Discard, io.Discard); code != 0 {
			t.Fatalf("exit %d", code)
		}
		st, err := os.Stat(out)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != want {
			t.Errorf("mode %v, want %v", st.Mode().Perm(), want)
		}
		if err := os.Chmod(out, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}
