package transcript

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/nwverse/persona/server/internal/proto"
)

// snippetLine is the first line of s, at most n runes.
func snippetLine(s string, n int) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	r := []rune(strings.TrimSpace(s))
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return string(r)
}

func clip(s string) string { return clipTo(s, maxTextLen) }

// clipTo cuts s to at most n bytes on a UTF-8 boundary and notes the original size.
func clipTo(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return s[:cut] + fmt.Sprintf("\n…（已截断，共 %d 字节）", len(s))
}

// light is the copy of an item that pages and pushes carry; cut says something was left out (fetch it with Full).
// Long text ends in a line that says it was cut, so apps that do not know FullSize still show it truncated, not whole.
// Only a tool's output has a "View full text" button in the app, so only its note says to tap for the whole.
func light(it proto.Item, keepImage bool) (_ proto.Item, cut bool) {
	limit, note := pageTextLen, "\n…（已截断，共 %d 字节）"
	if it.Kind == proto.KindToolResult {
		limit, note = pageOutputLen, "\n…（已截断，点开看全文，共 %d 字节）"
	}
	if len(it.Text) > limit && it.Role == "user" && it.Kind == proto.KindText {
		if t, ok := lightContexts(it.Text, limit); ok { // what the user wrote stays whole, the blocks' bodies are cut
			it.FullSize, it.Text = len(it.Text), t
			cut = true
		}
	}
	if len(it.Text) > limit {
		end := limit
		for end > 0 && (it.Text[end]&0xC0) == 0x80 {
			end--
		}
		if nl := strings.LastIndexByte(it.Text[:end], '\n'); nl > end-512 { // end on a whole line when one is near
			end = nl
		}
		it.FullSize = len(it.Text)
		it.Text = it.Text[:end] + fmt.Sprintf(note, len(it.Text))
		cut = true
	}
	if it.Image != nil && it.Image.Data != "" && !keepImage {
		it.Image = it.Image.Placeholder()
		cut = true
	}
	return it, cut
}

const (
	minInputShare = 256 // a long string in a tool input is never cut shorter than this
	clipNoteRoom  = 48  // what clipTo's note adds to a cut string, as JSON
)

// clipJSON shortens long strings anywhere in a tool input object: a Write's content, but also push_files'
// files[].content, MultiEdit's edits[].new_string or an MCP call's nested blocks, which went out whole in every page and
// push. The shape stays, so the apps' renderers still find their fields. A value that is not an object is left as it is:
// a Claude tool_use's input and an MCP call's arguments are always objects.
func clipJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) <= maxInputLen {
		return raw
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return raw
	}
	b, _ := json.Marshal(mapStrings(m, func(s string) string { return clipTo(s, maxInputLen/2) }))
	if len(b) <= maxInputLen {
		return b
	}
	// Still too long (many medium strings): the strings over minInputShare share evenly what the rest leaves. Shorter
	// strings, keys and other values are never cut, and a share is never under minInputShare, so an input made mostly of
	// those (thousands of short array items) or of a great many long strings still goes over maxInputLen.
	long, kept := 0, 0
	rest, _ := json.Marshal(mapStrings(m, func(s string) string {
		if len(s) <= minInputShare {
			return s
		}
		long, kept = long+1, kept+min(len(s), maxInputLen/2)
		return ""
	}))
	if long == 0 {
		return b // nothing left that may be cut
	}
	// JSON escapes make a kept byte cost more than one: scale the share by what the first pass's strings cost.
	share := (maxInputLen - len(rest)) * kept / (len(b) - len(rest)) / long
	share = min(max(share-clipNoteRoom, minInputShare), maxInputLen/2)
	b, _ = json.Marshal(mapStrings(m, func(s string) string {
		if len(s) <= share+clipNoteRoom {
			return s // within a note of the share: cut, it would come out about as long, or longer
		}
		return clipTo(s, share)
	}))
	return b
}

// mapStrings is a copy of v, a decoded JSON value, with f applied to every string in it (object keys are kept).
func mapStrings(v any, f func(string) string) any {
	switch v := v.(type) {
	case string:
		return f(v)
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, e := range v {
			out[k] = mapStrings(e, f)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, e := range v {
			out[i] = mapStrings(e, f)
		}
		return out
	}
	return v
}
