package node

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

// callInbox sends data to the receive folder dir ("": the default) the way the hub does.
func callInbox(t *testing.T, n *Node, dir, name string, data []byte) (proto.UploadedFile, map[string]any, error) {
	t.Helper()
	call := func(method string, p map[string]any) (any, error) {
		raw, _ := json.Marshal(p)
		return n.uploadCall(method, raw)
	}
	res, err := call("file.upload.begin", map[string]any{"name": name, "size": len(data), "dest": proto.UploadDestInbox, "dir": dir})
	if err != nil {
		return proto.UploadedFile{}, nil, err
	}
	begin := res.(map[string]any)
	if _, err := call("file.upload.chunk", map[string]any{"upload_id": begin["upload_id"], "offset": 0, "data": data}); err != nil {
		return proto.UploadedFile{}, begin, err
	}
	out, err := call("file.upload.end", map[string]any{"upload_id": begin["upload_id"]})
	if err != nil {
		return proto.UploadedFile{}, begin, err
	}
	return out.(proto.UploadedFile), begin, nil
}

// A file sent to keep lands in ~/Downloads/Persona (made when missing) under its own name; the same name again is
// kept beside it as "name 2.ext", never over it; the hub is told the destination was understood.
func TestInboxUploadKeepsItsNameInTheDefaultFolder(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	n, home := writeNode(t)
	out, begin, err := callInbox(t, n, "", "Quarterly report.pdf", []byte("one"))
	if err != nil {
		t.Fatal(err)
	}
	if begin["dest"] != proto.UploadDestInbox {
		t.Fatalf("begin did not echo dest: %v", begin)
	}
	want := filepath.Join(home, "Downloads", "Persona", "Quarterly report.pdf")
	if out.Path != want || out.Name != "Quarterly report.pdf" || out.Size != 3 {
		t.Fatalf("got %+v, want %s", out, want)
	}
	again, _, err := callInbox(t, n, "", "Quarterly report.pdf", []byte("two"))
	if err != nil {
		t.Fatal(err)
	}
	if again.Path != filepath.Join(home, "Downloads", "Persona", "Quarterly report 2.pdf") {
		t.Fatalf("second: %s", again.Path)
	}
	if b, _ := os.ReadFile(want); !bytes.Equal(b, []byte("one")) {
		t.Fatalf("first file was changed: %q", b)
	}
	if st, _ := os.Stat(want); st.Mode().Perm() != 0o644 {
		t.Fatalf("mode %v", st.Mode())
	}
	ents, _ := os.ReadDir(filepath.Dir(want))
	if len(ents) != 2 { // no part file left beside them
		t.Fatalf("folder has %v", ents)
	}
}

// The receive folder the config names is used, "~/" in the home or absolute; a name that tries to leave it is
// flattened into it.
func TestInboxUploadConfiguredFolder(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	n, home := writeNode(t)
	out, _, err := callInbox(t, n, "~/Inbox/from phone", "../../escape.txt", []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "Inbox", "from phone", ".._.._escape.txt"); out.Path != want {
		t.Fatalf("path %s, want %s", out.Path, want)
	}
	abs := filepath.Join(home, "proj", "in")
	if out, _, err = callInbox(t, n, abs, "a.txt", []byte("x")); err != nil || out.Path != filepath.Join(abs, "a.txt") {
		t.Fatalf("absolute: %+v %v", out, err)
	}
}

// A receive folder outside what the phone may write to is refused before anything is made there: outside the home,
// ../ above it, Persona's own folder, a secret one, a link out of the home, a relative path.
func TestInboxUploadFolderCannotEscape(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	n, home := writeNode(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(home, "out")); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{
		filepath.Join(outside, "in"),
		"~/../elsewhere",
		"~/.persona/inbox",
		"~/.ssh/in",
		"~/out/in",
		"Downloads",
	} {
		if _, _, err := callInbox(t, n, dir, "a.txt", []byte("x")); err == nil {
			t.Errorf("%q: accepted", dir)
		}
	}
	if ents, _ := os.ReadDir(outside); len(ents) != 0 {
		t.Fatalf("made %v outside the home", ents)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(home), "elsewhere")); err == nil {
		t.Fatal("made a folder beside the home")
	}
	if _, err := os.Stat(filepath.Join(home, ".persona", "inbox")); err == nil {
		t.Fatal("made a folder in Persona's folder")
	}
}

// The inbox takes files over the chat's 200 MB, up to its own limit.
func TestInboxUploadSizeLimit(t *testing.T) {
	n, _ := writeNode(t)
	raw, _ := json.Marshal(map[string]any{"name": "big.iso", "size": proto.MaxInboxUploadBytes + 1, "dest": proto.UploadDestInbox})
	if _, err := n.uploadCall("file.upload.begin", raw); err == nil {
		t.Fatal("over the limit accepted")
	}
	raw, _ = json.Marshal(map[string]any{"name": "big.iso", "size": proto.MaxUploadBytes + 1, "dest": proto.UploadDestInbox})
	res, err := n.uploadCall("file.upload.begin", raw)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(map[string]any{"upload_id": res.(map[string]any)["upload_id"]})
	_, _ = n.uploadCall("file.upload.abort", raw)
}
