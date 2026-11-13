package transcript

import (
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/nwverse/persona/server/internal/proto"
)

// pictureExts are the files whose Read is the agent looking at a picture (what the phone's file thumbnails draw).
var pictureExts = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".heic": true,
	".heif": true, ".bmp": true, ".tif": true, ".tiff": true}

// ViewedImages are the images the tool calls of run looked at, in the order they came, each once
// (docs/protocol/protocol.md "Images viewed this turn"): the images their results carried, and the picture files Read opened whose
// results carried none (Codex's view_image is a Read too). uses holds the calls by id (the page's: an image's call
// may sit before the run). The apps apply the same rules to the work they have; a fold carries what it hides.
func ViewedImages(run []proto.Item, uses map[string]proto.Item) []proto.FoldImage {
	var out []proto.FoldImage
	carried, done := map[string]bool{}, map[string]bool{}
	for _, it := range run {
		if it.Kind == proto.KindImage && it.ToolUseID != "" && it.Image != nil {
			carried[it.ToolUseID] = true
		}
		if it.Kind == proto.KindToolResult && !it.IsError {
			done[it.ToolUseID] = true
		}
	}
	seen := map[string]bool{}
	add := func(im proto.FoldImage) {
		if k := viewedKey(im); !seen[k] {
			seen[k] = true
			out = append(out, im)
		}
	}
	for _, it := range run {
		switch {
		case it.Kind == proto.KindImage && it.ToolUseID != "" && it.Image != nil:
			im := viewedFrom(uses[it.ToolUseID])
			im.ItemID, im.ToolUseID = it.ID, it.ToolUseID
			ph := it.Image.Placeholder()
			im.Mime, im.Size, im.Width, im.Height = ph.Mime, ph.Size, ph.Width, ph.Height
			add(im)
		case it.Kind == proto.KindToolUse && !carried[it.ToolUseID] && done[it.ToolUseID]:
			if im := viewedFrom(it); im.Path != "" && pictureExts[strings.ToLower(filepath.Ext(im.Path))] {
				add(im)
			}
		}
	}
	return out
}

// viewedFrom is what a call says about the images it looked at: its tool, where they came from, and the file or the
// machine it named. Only Read names a file: it reads one picture.
func viewedFrom(use proto.Item) proto.FoldImage {
	im := proto.FoldImage{ToolUseID: use.ToolUseID, Tool: use.ToolName, Source: "tool"}
	var in struct {
		FilePath string `json:"file_path"`
		Machine  string `json:"machine"`
	}
	_ = json.Unmarshal(use.ToolInput, &in)
	base, mcp := use.ToolName, false
	if i := strings.LastIndex(base, "__"); strings.HasPrefix(base, "mcp__") && i > len("mcp_") {
		base, mcp = base[i+2:], true
	}
	switch {
	case base == "Read" && !mcp:
		im.Source, im.Path = "read", in.FilePath
	case mcp && strings.HasPrefix(base, "computer_"):
		im.Source, im.Machine = "screenshot", in.Machine
	case mcp && strings.HasPrefix(base, "browser_"):
		im.Source, im.Machine = "browser", in.Machine
	case mcp:
		im.Source = "mcp"
	}
	return im
}

// viewedKey tells images apart: one file once, else one image item; an image the same as one before (the same bytes'
// size and shape: an unchanged screen looked at again) once too.
func viewedKey(im proto.FoldImage) string {
	switch {
	case im.Path != "":
		return "p:" + im.Path
	case im.Size > 0 && im.Width > 0 && im.Height > 0:
		return "s:" + im.Mime + ":" + strconv.Itoa(im.Size) + ":" + strconv.Itoa(im.Width) + "x" + strconv.Itoa(im.Height)
	}
	return "i:" + im.ItemID
}
