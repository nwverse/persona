package extension

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// NodePackage writes the package at root as the tar.gz a computer on platform gets (docs/plans/plan-extensions.md "7"):
// everything but the other platforms' node programs and the hub's programs, in a fixed order with no times or owners,
// so the same package always makes the same bytes, and the node can tell by the digest that it has it already.
func NodePackage(m *Manifest, root, platform string, w io.Writer) error {
	if m.NodeProgram() == nil {
		return fmt.Errorf("plugin %s has no program for computers", m.ID)
	}
	mine, ok := m.Node.Command.For(platform)
	if !ok {
		return fmt.Errorf("plugin %s has no program for %s", m.ID, platform)
	}
	skip := map[string]bool{}
	for _, p := range m.Node.Command.ByPlatform {
		skip[p] = true
	}
	if m.Runtime != nil {
		if m.Runtime.Command.ByPlatform == nil {
			skip[m.Runtime.Command.Path] = true
		}
		for _, p := range m.Runtime.Command.ByPlatform {
			skip[p] = true
		}
	}
	delete(skip, mine)
	gz := gzip.NewWriter(w) // no name, no time: the same bytes every time
	tw := tar.NewWriter(gz)
	var size int64
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil || rel == "." {
			return err
		}
		rel = filepath.ToSlash(rel)
		if skip[rel] {
			return nil
		}
		fi, err := os.Lstat(p)
		if err != nil {
			return err
		}
		hd := &tar.Header{Name: rel, Format: tar.FormatPAX}
		switch {
		case fi.IsDir():
			hd.Typeflag, hd.Name, hd.Mode = tar.TypeDir, rel+"/", 0o755
		case fi.Mode()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			if !linkInside(rel, target) {
				return fmt.Errorf("%s 是指向包外的链接", rel)
			}
			hd.Typeflag, hd.Linkname, hd.Mode = tar.TypeSymlink, target, 0o777
		case fi.Mode().IsRegular():
			hd.Typeflag, hd.Size, hd.Mode = tar.TypeReg, fi.Size(), int64(mode(fi.Mode()))
			if size += fi.Size(); size > PackageMost {
				return fmt.Errorf("包超过 %d MB", PackageMost>>20)
			}
		default:
			return nil // sockets and the like are no part of a package
		}
		if err := tw.WriteHeader(hd); err != nil {
			return err
		}
		if hd.Typeflag != tar.TypeReg {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.CopyN(tw, f, fi.Size())
		return err
	})
	if err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

// NodePackageFile writes NodePackage to file and says its sha256 and size.
func NodePackageFile(m *Manifest, root, platform, file string) (digest string, size int64, err error) {
	f, err := os.Create(file)
	if err != nil {
		return "", 0, err
	}
	h := sha256.New()
	cw := &countWriter{w: io.MultiWriter(f, h)}
	err = NodePackage(m, root, platform, cw)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(file)
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), cw.n, nil
}

type countWriter struct {
	w io.Writer
	n int64
}

func (c *countWriter) Write(b []byte) (int, error) {
	n, err := c.w.Write(b)
	c.n += int64(n)
	return n, err
}

// UnpackNodePackage unpacks a package NodePackage made into to, with the checks installing one has (nothing outside
// it, no links out of it, within PackageMost).
func UnpackNodePackage(file, to string) error { return untar(file, to) }

// NodeRuns says whether m's program on computers can run on a machine of goos / goarch.
func (m *Manifest) NodeRuns(goos, goarch string) bool {
	if m.NodeProgram() == nil {
		return false
	}
	_, ok := m.Node.Command.For(strings.ToLower(goos) + "-" + strings.ToLower(goarch))
	return ok
}
