package node

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

const (
	convertTimeout = 90 * time.Second
	convertKeep    = 7 * 24 * time.Hour // converted copies older than this are removed on the next conversion
	convertMax     = 200 << 20          // bigger documents are not handed to LibreOffice
)

// converting lets one LibreOffice run at a time: it is heavy, and two runs on one profile refuse each other.
var converting sync.Mutex

// sofficePath finds LibreOffice: on PATH (soffice, libreoffice), else the app bundle on a Mac.
func sofficePath() string {
	for _, name := range []string{"soffice", "libreoffice"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	if runtime.GOOS == "darwin" {
		for _, p := range []string{"/Applications/LibreOffice.app/Contents/MacOS/soffice", filepath.Join(os.Getenv("HOME"), "Applications/LibreOffice.app/Contents/MacOS/soffice")} {
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				return p
			}
		}
	}
	return ""
}

// convertDir keeps converted copies, named by the file and its version, so opening it again is instant.
func convertDir() string {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	return filepath.Join(base, "persona", "convert")
}

// convertFile turns a document a browser cannot show (doc, ppt, rtf, odt, pages, key, or a docx / pptx the web's
// renderer could not draw) into a PDF with LibreOffice, when the machine has it (docs/protocol/protocol.md "File preview"). The
// PDF lands in the node's cache and is read back like any file (file/download).
func (n *Node) convertFile(ctx context.Context, p proto.FileConvertRequest) (*proto.FileConverted, error) {
	if p.To != "" && p.To != "pdf" {
		return nil, fmt.Errorf("can only convert to pdf")
	}
	path, f, st, err := n.openRegular(p.Path, p.Cwd)
	if err != nil {
		return nil, err
	}
	f.Close()
	if st.Size() > convertMax {
		return nil, fmt.Errorf("file is too big to convert (%d bytes)", st.Size())
	}
	sum := sha256.Sum256([]byte(path + "\x00" + fileRevision(st)))
	dir := convertDir()
	out := filepath.Join(dir, hex.EncodeToString(sum[:12])+".pdf")
	if st, err := os.Stat(out); err == nil && st.Size() > 0 {
		return &proto.FileConverted{Path: out, Size: st.Size(), Mime: "application/pdf"}, nil
	}
	soffice := sofficePath()
	if soffice == "" {
		return nil, &rpc.Error{Code: proto.CodeNoConverter, Message: "LibreOffice is not installed on this machine"}
	}
	converting.Lock()
	defer converting.Unlock()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	pruneConverted(dir)
	work, err := os.MkdirTemp("", "persona-convert-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(work)
	// LibreOffice names its output after the input: copy the input under a plain name, so an odd file name (or a
	// running LibreOffice of the user's own, through its own profile) never gets in the way.
	ext := strings.ToLower(filepath.Ext(path))
	in := filepath.Join(work, "document"+ext)
	if err := copyFile(path, in); err != nil {
		return nil, explainDenied(path, n.home, err)
	}
	ctx, cancel := context.WithTimeout(ctx, convertTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, soffice, "-env:UserInstallation=file://"+filepath.ToSlash(filepath.Join(work, "profile")),
		"--headless", "--norestore", "--nologo", "--convert-to", "pdf", "--outdir", work, in)
	cmd.WaitDelay = 2 * time.Second
	if msg, err := cmd.CombinedOutput(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("LibreOffice took longer than %s", convertTimeout)
		}
		return nil, fmt.Errorf("LibreOffice could not convert it: %v %s", err, strings.TrimSpace(string(msg)))
	}
	pdf := filepath.Join(work, "document.pdf")
	got, err := os.Stat(pdf)
	if err != nil || got.Size() == 0 {
		return nil, fmt.Errorf("LibreOffice did not write a PDF")
	}
	if err := os.Rename(pdf, out); err != nil {
		if err := copyFile(pdf, out); err != nil {
			return nil, err
		}
	}
	return &proto.FileConverted{Path: out, Size: got.Size(), Mime: "application/pdf"}, nil
}

func copyFile(from, to string) error {
	src, err := os.Open(from)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		return err
	}
	return dst.Close()
}

// pruneConverted removes copies older than convertKeep.
func pruneConverted(dir string) { pruneOlder(dir, convertKeep) }

// pruneOlder removes the files in dir last written more than keep ago.
func pruneOlder(dir string, keep time.Duration) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range ents {
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > keep {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}
