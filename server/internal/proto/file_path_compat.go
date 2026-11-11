package proto

import (
	"encoding/base64"
	"encoding/json"
	"strings"
)

// NeedsExactFilePaths identifies file requests an older node would resolve after trimming whitespace.
// The manager and hub refuse these requests until the node advertises exact path identity.
func NeedsExactFilePaths(method string, params any, windows bool) bool {
	switch method {
	case "file.read", "file.chunk", "file.thumb", "file.convert", "fs.list", "fs.stat", "fs.find", "fs.watch", "file.rename", "file.move", "file.trash", "file.restore", "file.upload.begin", "workspace.resolve":
	default:
		return false
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return false
	}
	var p struct {
		Path     string   `json:"path"`
		Paths    []string `json:"paths"`
		Cwd      string   `json:"cwd"`
		Dir      string   `json:"dir"`
		Dest     string   `json:"dest"`
		Name     string   `json:"name"`
		TrashIDs []string `json:"trash_ids"`
		Dirs     []struct {
			Path string `json:"path"`
			Cwd  string `json:"cwd"`
		} `json:"dirs"`
	}
	if json.Unmarshal(raw, &p) != nil {
		return false
	}
	sensitive := func(path string) bool {
		withoutSlash := strings.TrimRight(path, "/")
		if path != strings.TrimSpace(path) || withoutSlash != strings.TrimSpace(withoutSlash) {
			return true
		}
		if windows {
			for _, part := range strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' }) {
				if strings.HasSuffix(part, " ") {
					return true
				}
			}
		}
		return false
	}
	switch method {
	case "file.read", "file.chunk", "file.thumb", "file.convert", "fs.list", "file.rename", "workspace.resolve":
		return sensitive(p.Path) || (windows && sensitive(p.Cwd))
	case "fs.find":
		return sensitive(p.Cwd)
	case "file.move":
		if sensitive(p.Dir) || (windows && sensitive(p.Cwd)) {
			return true
		}
		fallthrough
	case "fs.stat", "file.trash":
		if windows && sensitive(p.Cwd) {
			return true
		}
		for _, path := range p.Paths {
			if sensitive(path) {
				return true
			}
		}
	case "fs.watch":
		for _, dir := range p.Dirs {
			if sensitive(dir.Path) || (windows && sensitive(dir.Cwd)) {
				return true
			}
		}
	case "file.upload.begin":
		return (p.Dest == UploadDestDir || p.Dest == UploadDestInbox) && (sensitive(p.Dir) || sensitive(p.Name) || (windows && sensitive(p.Cwd)))
	case "file.restore":
		for _, id := range p.TrashIDs {
			b, err := base64.RawURLEncoding.DecodeString(id)
			if err != nil {
				continue
			}
			var entry struct {
				Name string `json:"n"`
				Real string `json:"r"`
			}
			if json.Unmarshal(b, &entry) == nil {
				if sensitive(entry.Name) || sensitive(entry.Real) {
					return true
				}
				if i := strings.LastIndexAny(entry.Real, `/\\`); i >= 0 && sensitive(entry.Real[:i]) {
					return true
				}
			}
		}
	}
	return false
}
