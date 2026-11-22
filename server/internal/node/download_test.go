package node

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

func TestDownloadFileChunks(t *testing.T) {
	dir := t.TempDir()
	n := &Node{home: dir}
	data := bytes.Repeat([]byte{0, 255, 9, 1}, proto.FileDownloadChunkBytes+17)
	path := filepath.Join(dir, "preview.mp4")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	info, err := n.readFileChunk(proto.FileChunkRequest{Path: "~/preview.mp4"})
	if err != nil {
		t.Fatal(err)
	}
	if info.Size != int64(len(data)) || len(info.Data) != 0 || info.Revision == "" || info.Mime != "video/mp4" {
		t.Fatalf("%+v", info)
	}
	var got []byte
	for off := int64(0); off < info.Size; {
		chunk, err := n.readFileChunk(proto.FileChunkRequest{Path: "preview.mp4", Cwd: dir, Offset: off, Length: proto.FileDownloadChunkBytes, Revision: info.Revision})
		if err != nil {
			t.Fatal(err)
		}
		if chunk.Offset != off || len(chunk.Data) > proto.FileDownloadChunkBytes {
			t.Fatal("invalid chunk")
		}
		got = append(got, chunk.Data...)
		off += int64(len(chunk.Data))
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("downloaded %d of %d bytes", len(got), len(data))
	}
	legacy, err := n.readFile(path, "", 0, false)
	if err != nil || !legacy.Truncated {
		t.Fatal("legacy binary preview should still be capped")
	}
	if err := os.Chtimes(path, time.Now(), info.Modified.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := n.readFileChunk(proto.FileChunkRequest{Path: path, Length: 10, Revision: info.Revision}); err == nil {
		t.Fatal("changed file was accepted")
	}
}

func TestDownloadFileRefusesInvalidInput(t *testing.T) {
	dir := t.TempDir()
	n := &Node{home: dir}
	path := filepath.Join(dir, "preview.mp4")
	if err := os.WriteFile(path, []byte{0, 1, 2, 3}, 0600); err != nil {
		t.Fatal(err)
	}
	for _, p := range []proto.FileChunkRequest{
		{Path: path, Offset: -1}, {Path: path, Offset: 5}, {Path: path, Length: -1},
		{Path: path, Length: proto.FileDownloadChunkBytes + 1}, {Path: dir}, {Path: "preview.mp4"},
		{Path: "~/Library/Mobile Documents/blocked.mp4"}, {Path: ""},
	} {
		if _, err := n.readFileChunk(p); err == nil {
			t.Errorf("accepted %+v", p)
		}
	}
	end, err := n.readFileChunk(proto.FileChunkRequest{Path: path, Offset: 4, Length: 10})
	if err != nil || len(end.Data) != 0 {
		t.Fatal("EOF is not empty", err)
	}
}

// A chunk asked at a revision the file no longer has says so with a code, which a transfer reports as such.
func TestDownloadChunkOfAChangedFileHasACode(t *testing.T) {
	dir := t.TempDir()
	n := &Node{home: dir}
	path := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(path, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := n.readFileChunk(proto.FileChunkRequest{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("second, longer"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = n.readFileChunk(proto.FileChunkRequest{Path: path, Length: 4, Revision: info.Revision})
	var re *rpc.Error
	if !errors.As(err, &re) || re.Code != proto.CodeFileChanged {
		t.Fatalf("err %v", err)
	}
}
