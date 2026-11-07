package node

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// fakeSoffice puts a LibreOffice first on PATH that writes "<outdir>/<input name>.pdf" and counts its runs.
func fakeSoffice(t *testing.T, dir string) string {
	t.Helper()
	bin := filepath.Join(dir, "bin")
	os.Mkdir(bin, 0o700)
	calls := filepath.Join(dir, "calls")
	script := `#!/bin/sh
echo "$@" >> ` + calls + `
out=""; last=""
while [ $# -gt 0 ]; do
  if [ "$1" = "--outdir" ]; then out="$2"; shift; fi
  last="$1"; shift
done
name=$(basename "$last"); name="${name%.*}"
printf '%%PDF-1.4 converted\n' > "$out/$name.pdf"
`
	os.WriteFile(filepath.Join(bin, "soffice"), []byte(script), 0o700)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return calls
}

func TestConvertWritesAPDFOnceAndReusesIt(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, "cache"))
	t.Setenv("HOME", dir) // os.UserCacheDir on a Mac
	n := &Node{home: dir}
	calls := fakeSoffice(t, dir)
	os.WriteFile(filepath.Join(dir, "旧 文档 (1).doc"), []byte("\xd0\xcf\x11\xe0 old word"), 0o600)
	got, err := n.convertFile(context.Background(), proto.FileConvertRequest{Path: "~/旧 文档 (1).doc", To: "pdf"})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(got.Path)
	if err != nil || !strings.HasPrefix(string(data), "%PDF") || got.Mime != "application/pdf" || got.Size != int64(len(data)) {
		t.Fatalf("%+v %q %v", got, data, err)
	}
	again, err := n.convertFile(context.Background(), proto.FileConvertRequest{Path: "旧 文档 (1).doc", Cwd: dir})
	if err != nil || again.Path != got.Path {
		t.Fatalf("second = %+v, %v", again, err)
	}
	if b, _ := os.ReadFile(calls); strings.Count(string(b), "\n") != 1 || !strings.Contains(string(b), "--headless") {
		t.Fatalf("soffice ran %q", b)
	}
	if _, err := n.convertFile(context.Background(), proto.FileConvertRequest{Path: "~/旧 文档 (1).doc", To: "docx"}); err == nil {
		t.Fatal("converted to something other than pdf")
	}
}

func TestConvertWithoutLibreOffice(t *testing.T) {
	if runtime.GOOS == "darwin" && sofficePath() != "" {
		t.Skip("this Mac has LibreOffice installed")
	}
	dir := t.TempDir()
	t.Setenv("PATH", filepath.Join(dir, "nothing"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, "cache"))
	t.Setenv("HOME", dir) // os.UserCacheDir on a Mac
	n := &Node{home: dir}
	os.WriteFile(filepath.Join(dir, "a.ppt"), []byte("x"), 0o600)
	_, err := n.convertFile(context.Background(), proto.FileConvertRequest{Path: "~/a.ppt"})
	var re *rpc.Error
	if !errors.As(err, &re) || re.Code != proto.CodeNoConverter {
		t.Fatal(err)
	}
}
