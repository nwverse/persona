package extension

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"
)

// WebShellItem is a purely declarative contribution to web_shells (docs/extensions/extensions.md "Web shells", #15396):
// a static Web/Desktop frontend bundle inside the extension package that a user may opt into on the Hub origin,
// with a guaranteed server-side fallback to the built-in Web UI (`?shell=builtin`).
type WebShellItem struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Dir         string `json:"dir"`
}

// Bounds for a web shell bundle inside a package: keeps memory/transfer predictable across local and remote managers
// (matching LookMaxTotal for static declarative bundles).
const (
	WebShellMaxTotal = 16 << 20 // 16 MiB total per web shell
	WebShellMaxFile  = 8 << 20  // 8 MiB per single file
	WebShellMaxFiles = 512      // max file count per web shell
)

// BuiltinWebShellIDs are reserved shell identifiers that always refer to the built-in Web UI.
var BuiltinWebShellIDs = []string{"builtin", "default"}

func init() {
	Points["web_shells"] = Point{Name: "web_shells", Namespace: "web_shells", Version: 1, Item: func(raw json.RawMessage) (string, error) {
		var ws WebShellItem
		if err := strict(raw, &ws); err != nil {
			return "", err
		}
		return ws.ID, ws.check()
	}}
}

func (ws WebShellItem) check() error {
	if !idPattern.MatchString(ws.ID) {
		return fmt.Errorf("web_shell id %q: a-z0-9_-", ws.ID)
	}
	if slices.Contains(BuiltinWebShellIDs, ws.ID) {
		return fmt.Errorf("web_shell %s: reserved id", ws.ID)
	}
	if n := utf8.RuneCountInString(strings.TrimSpace(ws.Name)); n == 0 || n > 40 {
		return fmt.Errorf("web_shell %s: name: 1 to 40 characters", ws.ID)
	}
	if utf8.RuneCountInString(ws.Description) > 200 {
		return fmt.Errorf("web_shell %s: description: at most 200 characters", ws.ID)
	}
	if err := checkPackagePath(ws.Dir); err != nil {
		return fmt.Errorf("web_shell %s: dir: %v", ws.ID, err)
	}
	if strings.HasPrefix(ws.Dir, ".") || strings.Contains(ws.Dir, "/.") {
		return fmt.Errorf("web_shell %s: dir %q: hidden", ws.ID, ws.Dir)
	}
	return nil
}

// WebShells are the manifest's contributions to web_shells.
func (m *Manifest) WebShells() []WebShellItem {
	if m == nil {
		return nil
	}
	var out []WebShellItem
	_ = json.Unmarshal(m.Contributes["web_shells"], &out)
	return out
}

// WebShellKey formats the canonical selector "<extension>/<shell-id>" used in cookies and query switches.
func WebShellKey(extensionID, shellID string) string {
	return extensionID + "/" + shellID
}

// ParseWebShellKey splits a "<extension>/<shell-id>" selector and validates both segments.
func ParseWebShellKey(raw string) (extensionID, shellID string, ok bool) {
	ext, id, found := strings.Cut(strings.TrimSpace(raw), "/")
	if !found || strings.Contains(id, "/") {
		return "", "", false
	}
	if !idPattern.MatchString(ext) || !idPattern.MatchString(id) || slices.Contains(BuiltinWebShellIDs, id) {
		return "", "", false
	}
	return ext, id, true
}

// CheckWebShellFiles validates a web shell's static bundle directory inside package root:
// the directory and every file inside it must stay within root (no symlink escape), must contain index.html,
// must contain no hidden files, and must stay within file count and byte budgets.
func CheckWebShellFiles(ws WebShellItem, root string) error {
	_, err := walkWebShellFiles(ws, root, false)
	return err
}

// ReadWebShellFiles validates and loads every static file of ws under root, keyed by clean slash-separated paths
// relative to ws.Dir (for example "index.html", "assets/app.js").
func ReadWebShellFiles(ws WebShellItem, root string) (map[string][]byte, error) {
	return walkWebShellFiles(ws, root, true)
}

// WebShellDigest returns a deterministic SHA-256 hex digest of a web shell's static files.
func WebShellDigest(files map[string][]byte) string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	h := sha256.New()
	for _, name := range names {
		data := files[name]
		fmt.Fprintf(h, "%s\x00%d\x00", name, len(data))
		_, _ = h.Write(data)
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func walkWebShellFiles(ws WebShellItem, root string, load bool) (map[string][]byte, error) {
	if err := ws.check(); err != nil {
		return nil, err
	}
	rootResolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("web_shell %s: package root: %w", ws.ID, err)
	}
	dirPath := filepath.Join(root, filepath.FromSlash(ws.Dir))
	dirResolved, err := filepath.EvalSymlinks(dirPath)
	if err != nil {
		return nil, fmt.Errorf("web_shell %s: dir %s is missing from the package", ws.ID, ws.Dir)
	}
	relDir, err := filepath.Rel(rootResolved, dirResolved)
	if err != nil || !inside(filepath.ToSlash(relDir)) {
		return nil, fmt.Errorf("web_shell %s: dir %s escapes the package", ws.ID, ws.Dir)
	}
	fi, err := os.Stat(dirResolved)
	if err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("web_shell %s: dir %s is not a directory", ws.ID, ws.Dir)
	}

	var files map[string][]byte
	if load {
		files = map[string][]byte{}
	}
	var total int64
	var readTotal int64
	count := 0
	hasIndex := false

	err = filepath.WalkDir(dirResolved, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if p == dirResolved {
			return nil
		}
		rel, err := filepath.Rel(dirResolved, p)
		if err != nil {
			return err
		}
		slash := filepath.ToSlash(rel)
		for _, seg := range strings.Split(slash, "/") {
			if strings.HasPrefix(seg, ".") {
				return fmt.Errorf("web_shell %s: %s: hidden paths are not allowed", ws.ID, slash)
			}
		}
		// Reject symlinks anywhere in the bundle to keep bundles hermetic and escape-proof.
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("web_shell %s: %s: symlinks are not allowed", ws.ID, slash)
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("web_shell %s: %s: non-regular file", ws.ID, slash)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		size := info.Size()
		if size > WebShellMaxFile {
			return fmt.Errorf("web_shell %s: %s exceeds %d bytes", ws.ID, slash, WebShellMaxFile)
		}
		count++
		if count > WebShellMaxFiles {
			return fmt.Errorf("web_shell %s: exceeds %d files", ws.ID, WebShellMaxFiles)
		}
		total += size
		if total > WebShellMaxTotal {
			return fmt.Errorf("web_shell %s: exceeds %d bytes total", ws.ID, WebShellMaxTotal)
		}
		clean := path.Clean(slash)
		if clean == "index.html" {
			hasIndex = true
		}
		if load {
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			if int64(len(b)) > WebShellMaxFile {
				return fmt.Errorf("web_shell %s: %s exceeds %d bytes", ws.ID, slash, WebShellMaxFile)
			}
			readTotal += int64(len(b))
			if readTotal > WebShellMaxTotal {
				return fmt.Errorf("web_shell %s: exceeds %d bytes total", ws.ID, WebShellMaxTotal)
			}
			files[clean] = b
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !hasIndex {
		return nil, fmt.Errorf("web_shell %s: %s/index.html is missing", ws.ID, ws.Dir)
	}
	return files, nil
}
