// Package runtimepkg is the code of the runtimes personad keeps up to date itself (docs/integrations/runtimes.md "Auto-update"): a
// runtime's plugin loaded into another program (DSH's), which that program would otherwise only reload by hand. The
// files are copied from integrations/ at build time (go generate) and embedded, so a node carries the plugin of its
// own build; a runtime that says it runs a package with another hash is offered this one (runtime.hello's update) and
// fetches it (runtime.package), and its loader switches to it.
package runtimepkg

//go:generate go run ./gen

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/nwverse/persona/server/internal/compat"
	"github.com/nwverse/persona/server/internal/identity"
	"github.com/nwverse/persona/server/internal/lazyre"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

//go:embed all:dist
var embedded embed.FS

// Packages are the packages and the files of integrations/ each is made of (path patterns, `*` only in the last
// segment). The generator copies exactly these; a runtime's loader hashes its own copy by the same list
// (integrations/runtime-sdk/lib/package.js PACKAGES). Keep the two in step: a file one side counts and the other does
// not makes every hello look like another version.
var Packages = map[string][]string{
	"persona-dsh-runtime": {"dsh/package.json", "dsh/lib/*.js", "runtime-sdk/package.json", "runtime-sdk/index.js", "runtime-sdk/lib/*.js"},
	"persona-pi-runtime":  {"pi/package.json", "pi/index.js", "pi/companion.mjs", "pi/lib/*.js", "runtime-sdk/package.json", "runtime-sdk/index.js", "runtime-sdk/lib/*.js"},
	// The Paseo bridge is not loaded by another program: the node writes it out and runs it (node/paseobridge.go).
	"persona-paseo-runtime": {"paseo/package.json", "paseo/bridge.mjs", "paseo/lib/*.js", "runtime-sdk/package.json", "runtime-sdk/index.js", "runtime-sdk/lib/*.js"},
	// The Agent SDK bridge, likewise run by the node (the assistant's agentsdk runtime): one bundled file, the SDK in it.
	"persona-agent-sdk-runtime": {"agent-sdk/package.json", "agent-sdk/dist/bridge.mjs"},
}

// OverrideEnv names a directory whose <package>/ trees replace the embedded ones: end-to-end runs offer a package
// that differs from the runtime's without building another personad.
const OverrideEnv = "PERSONA_RUNTIME_PACKAGES"

// NameRe: a package name.
var NameRe = lazyre.New(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// MaxSize bounds a package (all its files): far above what a runtime's JS is, far below a message the peers refuse.
const MaxSize = 8 << 20

// File is one file of a package, at a slash-separated path relative to the package root.
type File struct {
	Path string
	Data []byte
}

// Package is a package's files and their hash.
type Package struct {
	Name  string
	Hash  string
	Files []File
}

// Hash is a package's identity: sha256 over its files sorted by path, each as "<path>\x00<hex sha256 of the
// content>\n", in hex. The same function as the loader's (package.js packageHash).
func Hash(files []File) string {
	sorted := append([]File(nil), files...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })
	h := sha256.New()
	for _, f := range sorted {
		sum := sha256.Sum256(f.Data)
		fmt.Fprintf(h, "%s\x00%s\n", f.Path, hex.EncodeToString(sum[:]))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ValidPath: a package file's path is relative, slash-separated, without empty, "." or ".." segments.
func ValidPath(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, "\\") || len(p) > 256 {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

var (
	embeddedOnce sync.Once
	embeddedPkgs map[string]*Package
	embeddedErr  error
)

// Load is the package this personad offers under name: the override directory's when OverrideEnv is set, else the
// embedded one. nil without an error: this build carries no such package (built without go generate).
func Load(name string) (*Package, error) {
	requested := name
	name = compat.RuntimePackage(name)
	if !NameRe.MatchString(name) {
		return nil, fmt.Errorf("bad package name %q", name)
	}
	if dir := identity.Getenv(OverrideEnv); dir != "" {
		root := filepath.Join(dir, name)
		if _, err := os.Stat(root); errors.Is(err, fs.ErrNotExist) {
			root = filepath.Join(dir, compat.LegacyRuntimePackage(name))
			if _, err := os.Stat(root); errors.Is(err, fs.ErrNotExist) {
				return nil, nil
			}
		}
		return read(os.DirFS(root), requested)
	}
	embeddedOnce.Do(func() {
		embeddedPkgs = map[string]*Package{}
		entries, err := fs.ReadDir(embedded, "dist")
		if err != nil {
			embeddedErr = err
			return
		}
		for _, e := range entries {
			if !e.IsDir() || !NameRe.MatchString(e.Name()) {
				continue
			}
			sub, err := fs.Sub(embedded, path.Join("dist", e.Name()))
			if err != nil {
				embeddedErr = err
				return
			}
			p, err := read(sub, e.Name())
			if err != nil {
				embeddedErr = err
				return
			}
			embeddedPkgs[e.Name()] = p
		}
	})
	if embeddedErr != nil {
		return nil, embeddedErr
	}
	if pkg := embeddedPkgs[name]; pkg != nil {
		copy := *pkg
		copy.Name = requested
		return &copy, nil
	}
	return nil, nil
}

// read takes every regular file under fsys as the package's.
func read(fsys fs.FS, name string) (*Package, error) {
	p := &Package{Name: name}
	size := 0
	err := fs.WalkDir(fsys, ".", func(pth string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		if !ValidPath(pth) {
			return fmt.Errorf("package %s: bad path %q", name, pth)
		}
		data, err := fs.ReadFile(fsys, pth)
		if err != nil {
			return err
		}
		if size += len(data); size > MaxSize {
			return fmt.Errorf("package %s is over %d bytes", name, MaxSize)
		}
		p.Files = append(p.Files, File{Path: pth, Data: data})
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(p.Files) == 0 {
		return nil, nil
	}
	sort.Slice(p.Files, func(i, j int) bool { return p.Files[i].Path < p.Files[j].Path })
	p.Hash = Hash(p.Files)
	return p, nil
}

// Generate copies each package's files (Packages) from src (the integrations directory) into out/<name>/, replacing
// what was there, and returns them. A pattern that matches no file fails: a renamed file must not leave a package short.
func Generate(src, out string) (map[string][]File, error) {
	names := make([]string, 0, len(Packages))
	for name := range Packages {
		names = append(names, name)
	}
	sort.Strings(names)
	res := map[string][]File{}
	for _, name := range names {
		dst := filepath.Join(out, name)
		if err := os.RemoveAll(dst); err != nil {
			return nil, err
		}
		var files []File
		for _, pattern := range Packages[name] {
			matches, err := filepath.Glob(filepath.Join(src, filepath.FromSlash(pattern)))
			if err != nil {
				return nil, err
			}
			sort.Strings(matches)
			n := 0
			for _, m := range matches {
				st, err := os.Stat(m)
				if err != nil || !st.Mode().IsRegular() {
					continue
				}
				rel, err := filepath.Rel(src, m)
				if err != nil {
					return nil, err
				}
				rel = filepath.ToSlash(rel)
				data, err := os.ReadFile(m)
				if err != nil {
					return nil, err
				}
				target := filepath.Join(dst, filepath.FromSlash(rel))
				if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
					return nil, err
				}
				if err := os.WriteFile(target, data, 0o644); err != nil {
					return nil, err
				}
				files = append(files, File{Path: rel, Data: data})
				n++
			}
			if n == 0 {
				return nil, fmt.Errorf("package %s: %s matches no file under %s", name, pattern, src)
			}
		}
		res[name] = files
	}
	return res, nil
}
