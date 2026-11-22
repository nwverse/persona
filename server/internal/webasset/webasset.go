// Package webasset is what the hub and the build agree on about the web client's files: which responses gzip makes
// worth sending compressed (CompressibleType, MinSize), which of the built files are kept only gzipped in a
// published personad (Precompressible, Precompress; docs/web/web.md "Precompressed assets"), and the brotli copies
// the web build writes of the first screen's files (BrotliSuffix). The hub compresses other responses as it sends
// them (internal/hub/gzip.go); these it sends as they are stored.
package webasset

import (
	"bytes"
	"compress/gzip"
	"io/fs"
	"mime"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// MinSize is the smallest body worth compressing: below it gzip's header and trailer outweigh what it saves.
const MinSize = 1024

// Suffix is the name a precompressed file is kept under: its own name and this.
const Suffix = ".gz"

// BrotliSuffix is the name of the brotli copy the web build writes beside each file of MinSize bytes or more that a
// cold first screen loads (web/vite.config.ts, startup-preload): its own name and this. Go has no brotli encoder, so
// Precompress leaves these copies as they are and still gzips the file itself; the hub sends the copy to a client
// that takes br, and the gzip or plain file to any other.
const BrotliSuffix = ".br"

// Licenses is the web build's third-party notices beside index.html (THIRD_PARTY_NOTICES.md "Web client"): the one
// root file big enough to be worth keeping gzipped, and read rarely enough that decompressing it for a client
// without gzip costs nothing.
const Licenses = "third-party-licenses.txt"

// CompressibleType: a media type that gzip makes much smaller, and that is not an event stream.
func CompressibleType(contentType string) bool {
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	switch {
	case mt == "text/event-stream":
		return false
	case strings.HasPrefix(mt, "text/"), mt == "application/json", strings.HasSuffix(mt, "+json"),
		mt == "application/javascript", mt == "application/x-javascript", mt == "application/xml",
		strings.HasSuffix(mt, "+xml"), mt == "application/wasm":
		return true
	}
	return false
}

// Precompressible says whether the build keeps the file at name (a slash path below the web root) gzipped, when it
// has MinSize bytes or more: a file under assets/ (Vite names them by their content hash, so they never change
// under one name) whose extension is of a CompressibleType, and the root's Licenses. A brotli copy (BrotliSuffix) is
// compressed already and stays as it is; that is said by name, since mime.TypeByExtension also reads the system's
// mime.types, which may give .br any type. index.html, sw.js, the manifest, the admin page's files and the root's
// other files stay as they are.
func Precompressible(name string) bool {
	if name == Licenses {
		return true
	}
	base := path.Base(name)
	return strings.HasPrefix(name, "assets/") && !strings.HasPrefix(base, ".") && !strings.HasSuffix(base, BrotliSuffix) &&
		CompressibleType(mime.TypeByExtension(path.Ext(base)))
}

// Gzip is how a precompressed file is kept: gzip at its best compression, with no name or time in its header, so
// the same input always gives the same bytes.
func Gzip(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := zw.Write(data); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Stats counts what Precompress did: the files it replaced, their bytes before and after.
type Stats struct {
	Files         int
	Before, After int64
}

// Precompress replaces each file below root that is Precompressible and of MinSize bytes or more with its Gzip
// copy (name + Suffix). Run it on the web client's copy before go build embeds it (scripts/release/publish.sh,
// scripts/release/build_hub_release.py, the Dockerfile); running it again changes nothing.
func Precompress(root string) (Stats, error) {
	var st Stats
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil || !Precompressible(filepath.ToSlash(rel)) {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil || len(data) < MinSize {
			return err
		}
		gz, err := Gzip(data)
		if err != nil {
			return err
		}
		if err := os.WriteFile(p+Suffix, gz, 0o644); err != nil {
			return err
		}
		st.Files++
		st.Before += int64(len(data))
		st.After += int64(len(gz))
		return os.Remove(p)
	})
	return st, err
}
