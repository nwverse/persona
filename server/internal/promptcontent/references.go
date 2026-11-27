// Package promptcontent reads the shared leading context envelope used by composers and headless turns.
package promptcontent

import (
	"encoding/xml"
	"errors"
	"strings"

	"github.com/nwverse/persona/server/internal/compat"
)

// Split returns the scheduled picker envelope. A blank line ends the selections and begins literal prose.
func Split(text string) (prefix, body string) {
	blocks, _ := compat.FindLeadingContextBlocks(text)
	pos := 0
	for _, blk := range blocks {
		if pos > 0 && strings.Count(text[pos:blk.Start], "\n") >= 2 {
			break
		}
		pos = blk.End
	}
	return text[:pos], text[pos:]
}

type File struct{ Path, Machine, Cwd, Launcher, Runtime string }

var ErrFile = errors.New("selected file is unavailable or its execution environment changed; remove it and select it again")

// Files recognizes explicit file selections only. Display text never supplies executable paths.
func Files(text string) ([]File, error) {
	var out []File
	blocks, _ := compat.FindLeadingContextBlocks(text)
	for _, blk := range blocks {
		tag := blk.Attrs
		token, err := xml.NewDecoder(strings.NewReader("<context" + tag + "/>")).Token()
		if err != nil {
			if strings.Contains(tag, `"file"`) {
				return nil, ErrFile
			}
			continue
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		file := false
		for _, a := range start.Attr {
			file = file || a.Name.Local == "kind" && a.Value == "file"
		}
		if !file {
			continue
		}
		attrs := map[string]string{}
		for _, a := range start.Attr {
			if _, dup := attrs[a.Name.Local]; dup || a.Name.Space != "" {
				return nil, ErrFile
			}
			switch a.Name.Local {
			case "kind", "title", "url", "path", "machine", "cwd", "launcher", "runtime":
			default:
				return nil, ErrFile
			}
			if strings.ContainsAny(a.Value, "\x00\r\n") || len(a.Value) > 8192 {
				return nil, ErrFile
			}
			attrs[a.Name.Local] = a.Value
		}
		if attrs["path"] == "" || attrs["machine"] == "" || attrs["cwd"] == "" || attrs["launcher"] == "" || len(out) >= 16 {
			return nil, ErrFile
		}
		out = append(out, File{attrs["path"], attrs["machine"], attrs["cwd"], attrs["launcher"], attrs["runtime"]})
	}
	return out, nil
}
