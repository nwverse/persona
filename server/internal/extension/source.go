package extension

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Limits of a package.
const (
	PackageMost  = 200 << 20 // bytes, unpacked and downloaded
	PackageFiles = 20000
)

// Source is where a package comes from (docs/plans/plan-extensions.md "Key decisions").
type Source struct {
	Kind string // dir | file | https | remote | upload | market
	Path string // dir, file: a path on the hub's machine; https: the URL without the fragment; upload: the file's name
	// SHA256 is what an https download must hash to.
	SHA256 string
	// RPCSocket / HTTPSocket: a remote plugin's sockets.
	RPCSocket, HTTPSocket string
}

// String is the source as written, without secrets (there are none in a source).
func (s Source) String() string {
	switch s.Kind {
	case "https":
		return s.Path + "#sha256=" + s.SHA256
	case "remote":
		var parts []string
		if s.RPCSocket != "" {
			parts = append(parts, "rpc="+s.RPCSocket)
		}
		if s.HTTPSocket != "" {
			parts = append(parts, "http="+s.HTTPSocket)
		}
		return "remote:" + strings.Join(parts, ",")
	case "upload":
		return "upload:" + s.Path
	case "market":
		return "market:" + s.Path
	}
	return s.Kind + ":" + s.Path
}

// ParseSource reads dir:/p, file:/p.tar.gz, https://…#sha256=<hex>, remote:rpc=/p.sock,http=/q.sock, or a plain path
// (a directory or a package file, by what is there).
func ParseSource(s string) (Source, error) {
	s = strings.TrimSpace(s)
	switch {
	case strings.HasPrefix(s, "dir:"):
		return Source{Kind: "dir", Path: expandHome(strings.TrimPrefix(s, "dir:"))}, nil
	case strings.HasPrefix(s, "file:"):
		return Source{Kind: "file", Path: expandHome(strings.TrimPrefix(s, "file:"))}, nil
	case strings.HasPrefix(s, "https://"):
		u, err := url.Parse(s)
		if err != nil || u.Host == "" {
			return Source{}, errors.New("not a URL")
		}
		frag := u.Fragment
		u.Fragment = ""
		sum, ok := strings.CutPrefix(frag, "sha256=")
		if b, err := hex.DecodeString(sum); !ok || err != nil || len(b) != sha256.Size {
			return Source{}, errors.New("an https source needs #sha256=<64 hex digits>")
		}
		return Source{Kind: "https", Path: u.String(), SHA256: strings.ToLower(sum)}, nil
	case strings.HasPrefix(s, "http://"):
		return Source{}, errors.New("only https downloads (with #sha256=…)")
	case strings.HasPrefix(s, "remote:"):
		src := Source{Kind: "remote"}
		for _, part := range strings.Split(strings.TrimPrefix(s, "remote:"), ",") {
			k, v, _ := strings.Cut(part, "=")
			switch k {
			case "rpc":
				src.RPCSocket = expandHome(v)
			case "http":
				src.HTTPSocket = expandHome(v)
			default:
				return Source{}, fmt.Errorf("remote: %q is not rpc=<socket> or http=<socket>", part)
			}
		}
		if src.RPCSocket == "" && src.HTTPSocket == "" {
			return Source{}, errors.New("remote: rpc=<socket> and / or http=<socket>")
		}
		return src, nil
	case s == "":
		return Source{}, errors.New("no source")
	}
	p := expandHome(s)
	if fi, err := os.Stat(p); err == nil && fi.IsDir() {
		return Source{Kind: "dir", Path: p}, nil
	}
	return Source{Kind: "file", Path: p}, nil
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, p[2:])
	}
	return p
}

// Fetch downloads an https package; tests replace it.
var Fetch = func(ctx context.Context, rawURL string, w io.Writer) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", res.StatusCode)
	}
	n, err := io.Copy(w, io.LimitReader(res.Body, PackageMost+1))
	if err == nil && n > PackageMost {
		err = fmt.Errorf("more than %d bytes", PackageMost)
	}
	return err
}

// UploadName is a browser upload's file name as kept (its base name, without control characters, at most 128
// bytes), or an error when it is not a package (.tar.gz, .tgz, .zip).
func UploadName(name string) (string, error) {
	name = filepath.Base(strings.ReplaceAll(strings.TrimSpace(name), "\\", "/"))
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, name)
	if len(name) > 128 {
		name = name[len(name)-128:]
	}
	switch n := strings.ToLower(name); {
	case strings.HasSuffix(n, ".tar.gz"), strings.HasSuffix(n, ".tgz"), strings.HasSuffix(n, ".zip"):
		return name, nil
	}
	return "", errors.New("上传的包只能是 .tar.gz、.tgz 或 .zip")
}

// StageUpload writes an uploaded package (at most PackageMost bytes) into dir (made fresh) and unpacks it there,
// like Stage does a file on the hub's machine. It returns the package's root.
func StageUpload(body io.Reader, name, dir string) (string, error) {
	name, err := UploadName(name)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	file := filepath.Join(dir, "upload")
	if err := writeFile(file, io.LimitReader(body, PackageMost+1), 0o600, -1); err != nil {
		return "", err
	}
	if fi, err := os.Stat(file); err != nil || fi.Size() > PackageMost {
		return "", fmt.Errorf("包超过 %d MB", PackageMost>>20)
	}
	out := filepath.Join(dir, "pkg")
	if err := unpackAs(file, name, out); err != nil {
		return "", err
	}
	_ = os.Remove(file)
	return packageRoot(out)
}

// Stage puts the package src points at into dir (made fresh): a copy, never a link, so changing the original later
// changes nothing installed. It returns the package's root (where persona-plugin.json is).
func Stage(ctx context.Context, src Source, dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	out := filepath.Join(dir, "pkg")
	if src.Kind != "https" {
		reportState(ctx, StepUnpack, StepRunning)
	}
	switch src.Kind {
	case "dir":
		if err := copyTree(src.Path, out); err != nil {
			return "", err
		}
	case "file":
		if err := unpack(src.Path, out); err != nil {
			return "", err
		}
	case "https":
		file := filepath.Join(dir, "download")
		f, err := os.OpenFile(file, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return "", err
		}
		h := sha256.New()
		err = Fetch(ctx, src.Path, io.MultiWriter(f, h))
		f.Close()
		if err != nil {
			return "", fmt.Errorf("下载 %s：%w", src.Path, err)
		}
		reportState(ctx, StepDownload, StepDone)
		if got := hex.EncodeToString(h.Sum(nil)); got != src.SHA256 {
			return "", errOfKind(ErrChecksum, "下载的包 sha256 是 %s，不是 %s", got, src.SHA256)
		}
		reportState(ctx, StepVerify, StepDone)
		reportState(ctx, StepUnpack, StepRunning)
		name := src.Path
		if u, err := url.Parse(src.Path); err == nil {
			name = u.Path
		}
		if err := unpackAs(file, name, out); err != nil {
			return "", err
		}
		_ = os.Remove(file)
	default:
		return "", fmt.Errorf("a %s source has no package", src.Kind)
	}
	return packageRoot(out)
}

// packageRoot is dir, or its only top-level directory, whichever has the manifest.
func packageRoot(dir string) (string, error) {
	if hasManifest(dir) {
		return dir, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	if len(entries) == 1 && entries[0].IsDir() {
		sub := filepath.Join(dir, entries[0].Name())
		if hasManifest(sub) {
			return sub, nil
		}
	}
	return "", fmt.Errorf("包里没有 %s（或老名字 %s；根目录或唯一的顶层目录里）", ManifestFile, LegacyManifestFile)
}

// macMetadata says whether rel is what macOS puts into a package made on a Mac (Finder's zip, tar without
// COPYFILE_DISABLE): __MACOSX/, AppleDouble ._ files, .DS_Store. Nothing of the plugin; left out when unpacking, so
// the package's root is still its only top-level directory.
func macMetadata(rel string) bool {
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if part == "__MACOSX" || part == ".DS_Store" || strings.HasPrefix(part, "._") {
			return true
		}
	}
	return false
}

// budget keeps a package within PackageMost bytes and PackageFiles files (or its own limits, for a tool).
type budget struct{ bytes, files, maxBytes, maxFiles int64 }

func (b *budget) take(n int64) error {
	most, files := int64(PackageMost), int64(PackageFiles)
	if b.maxBytes > 0 {
		most, files = b.maxBytes, b.maxFiles
	}
	b.bytes += n
	b.files++
	if b.bytes > most {
		return fmt.Errorf("包超过 %d MB", most>>20)
	}
	if b.files > files {
		return fmt.Errorf("包里超过 %d 个文件", files)
	}
	return nil
}

// mode keeps only the permission bits that matter: executable or not.
func mode(m fs.FileMode) fs.FileMode {
	if m&0o111 != 0 {
		return 0o755
	}
	return 0o644
}

// inside says whether rel stays inside its root.
func inside(rel string) bool {
	rel = filepath.ToSlash(rel)
	return rel != "" && !strings.HasPrefix(rel, "/") && rel != ".." && !strings.HasPrefix(rel, "../") && filepath.Clean(rel) == filepath.FromSlash(rel)
}

// linkInside says whether a symbolic link at rel pointing at target resolves inside the package.
func linkInside(rel, target string) bool {
	if filepath.IsAbs(target) {
		return false
	}
	return inside(filepath.ToSlash(filepath.Join(filepath.Dir(rel), target)))
}

func copyTree(from, to string) error {
	fi, err := os.Stat(from)
	if err != nil {
		return fmt.Errorf("读不到 %s：%w", from, err)
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s 不是目录", from)
	}
	var b budget
	return filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, p)
		dst := filepath.Join(to, rel)
		if rel == "." {
			return os.MkdirAll(dst, 0o755)
		}
		if d.Name() == ".git" && d.IsDir() {
			return filepath.SkipDir
		}
		if macMetadata(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(dst, 0o755)
		case info.Mode()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			if !linkInside(rel, target) {
				return fmt.Errorf("%s 是指向包外的链接", rel)
			}
			return os.Symlink(target, dst)
		case !info.Mode().IsRegular():
			return fmt.Errorf("%s 不是普通文件", rel)
		}
		if err := b.take(info.Size()); err != nil {
			return err
		}
		return copyFile(p, dst, mode(info.Mode()))
	})
}

func copyFile(from, to string, m fs.FileMode) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	return writeFile(to, in, m, -1)
}

// writeFile writes r to path; at most limit bytes when limit >= 0.
func writeFile(path string, r io.Reader, m fs.FileMode, limit int64) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, m)
	if err != nil {
		return err
	}
	return copyOut(out, r, limit)
}

// writeRootFile is writeFile for name inside root: a path that leads out of it through links is refused.
func writeRootFile(root *os.Root, name string, r io.Reader, m fs.FileMode, limit int64) error {
	if err := root.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return err
	}
	out, err := root.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_EXCL, m)
	if err != nil {
		return err
	}
	return copyOut(out, r, limit)
}

// copyOut copies r into out (at most limit bytes when limit >= 0) and closes it.
func copyOut(out *os.File, r io.Reader, limit int64) error {
	if limit >= 0 {
		r = io.LimitReader(r, limit+1)
	}
	n, err := io.Copy(out, r)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil && limit >= 0 && n > limit {
		err = errors.New("文件比它说的长")
	}
	return err
}

func unpack(file, to string) error { return unpackAs(file, file, to) }

// unpackAs unpacks file by the name's extension (.tar.gz, .tgz, .zip).
func unpackAs(file, name, to string) error {
	switch n := strings.ToLower(name); {
	case strings.HasSuffix(n, ".tar.gz") || strings.HasSuffix(n, ".tgz"):
		return untar(file, to)
	case strings.HasSuffix(n, ".zip"):
		return unzip(file, to)
	}
	return fmt.Errorf("%s：包只能是目录、.tar.gz 或 .zip", filepath.Base(name))
}

func untar(file, to string) error { return untarIn(file, to, &budget{}) }

func untarIn(file, to string, b *budget) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("不是 gzip：%w", err)
	}
	tr := tar.NewReader(gz)
	if err := os.MkdirAll(to, 0o755); err != nil {
		return err
	}
	// Everything is made through root: each link is checked alone (linkInside), but links made earlier can lead a
	// later path out of the package together ("a" -> ".", then "a/b" -> ".."), and root refuses that.
	root, err := os.OpenRoot(to)
	if err != nil {
		return err
	}
	defer root.Close()
	var links []string
	for {
		hd, err := tr.Next()
		if err == io.EOF {
			// A link that only points out, through another one ("d" -> "a/.."), is refused as well. One pointing at
			// nothing inside the package stays as it was allowed before.
			for _, l := range links {
				if _, err := root.Stat(l); err != nil && !errors.Is(err, fs.ErrNotExist) {
					return fmt.Errorf("%s 是指向包外的链接", filepath.ToSlash(l))
				}
			}
			return nil
		}
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(hd.Name, "./")
		if rel == "" || rel == "." {
			continue
		}
		rel = strings.TrimSuffix(rel, "/")
		if !inside(rel) {
			return fmt.Errorf("%s 跳出了包", hd.Name)
		}
		if macMetadata(rel) {
			continue
		}
		dst := filepath.FromSlash(rel)
		switch hd.Typeflag {
		case tar.TypeDir:
			if err := root.MkdirAll(dst, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := b.take(hd.Size); err != nil {
				return err
			}
			if err := writeRootFile(root, dst, tr, mode(fs.FileMode(hd.Mode)), hd.Size); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if !linkInside(rel, hd.Linkname) {
				return fmt.Errorf("%s 是指向包外的链接", rel)
			}
			if err := root.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			if err := root.Symlink(hd.Linkname, dst); err != nil {
				return err
			}
			links = append(links, dst)
		case tar.TypeXGlobalHeader, tar.TypeXHeader:
		default:
			return fmt.Errorf("%s 不是普通文件", rel)
		}
	}
}

func unzip(file, to string) error { return unzipIn(file, to, &budget{}) }

func unzipIn(file, to string, b *budget) error {
	zr, err := zip.OpenReader(file)
	if err != nil {
		return fmt.Errorf("不是 zip：%w", err)
	}
	defer zr.Close()
	for _, zf := range zr.File {
		rel := strings.TrimSuffix(zf.Name, "/")
		if rel == "" {
			continue
		}
		if !inside(rel) {
			return fmt.Errorf("%s 跳出了包", zf.Name)
		}
		if macMetadata(rel) {
			continue
		}
		dst := filepath.Join(to, filepath.FromSlash(rel))
		m := zf.Mode()
		switch {
		case m.IsDir():
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return err
			}
			continue
		case m&fs.ModeSymlink != 0:
			return fmt.Errorf("%s：zip 包里不能有链接", rel)
		case !m.IsRegular():
			return fmt.Errorf("%s 不是普通文件", rel)
		}
		if err := b.take(int64(zf.UncompressedSize64)); err != nil {
			return err
		}
		rc, err := zf.Open()
		if err != nil {
			return err
		}
		err = writeFile(dst, rc, mode(m), int64(zf.UncompressedSize64))
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
