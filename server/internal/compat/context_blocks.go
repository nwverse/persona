package compat

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// ContextBlock describes one top-level <persona-context> or legacy context block in text.
// Attrs is the raw slice between the tag name and '>' (empty or whitespace-only for a bare snapshot tag).
type ContextBlock struct {
	Start   int
	OpenEnd int
	CloseAt int
	End     int
	Attrs   string
}

type contextTagToken struct {
	start   int
	end     int
	isClose bool
	attrs   string
}

var contextTokenRe = lazy(`(?s)<` + ContextPattern() + `(?:\s+[^<>]*)?>|</` + ContextPattern() + `\s*>`)
var legacyOpenTagRe = lazy(`^<persona-context([[:space:]>])`)
var legacyCloseTagRe = lazy(`^</persona-context([[:space:]>])`)
var legacyFileTagRe = lazy(`(</?)persona-(file)([[:space:]/>])`)

// HasContextTag reports whether s could contain a context block tag.
func HasContextTag(s string) bool {
	return strings.Contains(s, "-context")
}

// FindContextBlocks returns all top-level context blocks in s in linear O(len(s)) time.
//
// Matching is depth-aware so a block whose body contains unescaped nested <persona-context>...</persona-context>
// pairs stays whole instead of truncating at the inner closing tag. When a historical/unescaped block contains an
// unclosed literal opening tag (2 opens, 1 close), depth never returns to 0; the matcher falls back to the leftmost
// closing tag achieving the minimum positive depth so the outer block closes cleanly without swallowing subsequent
// valid blocks or trailing user text.
func FindContextBlocks(s string) []ContextBlock {
	if !HasContextTag(s) {
		return nil
	}
	raw := contextTokenRe().FindAllStringIndex(s, -1)
	if len(raw) < 2 {
		return nil
	}
	tokens := make([]contextTagToken, 0, len(raw))
	hasClose := false
	for _, loc := range raw {
		tok := s[loc[0]:loc[1]]
		if strings.HasPrefix(tok, "</") {
			tokens = append(tokens, contextTagToken{start: loc[0], end: loc[1], isClose: true})
			hasClose = true
			continue
		}
		if strings.HasSuffix(tok, "/>") {
			continue
		}
		nameEnd := strings.IndexAny(tok[1:], " \t\r\n>") + 1
		attrs := tok[nameEnd : len(tok)-1]
		tokens = append(tokens, contextTagToken{start: loc[0], end: loc[1], attrs: attrs})
	}
	if !hasClose || len(tokens) < 2 {
		return nil
	}
	m := len(tokens)
	bal := make([]int, m+1)
	for i, tok := range tokens {
		if tok.isClose {
			bal[i+1] = bal[i] - 1
		} else {
			bal[i+1] = bal[i] + 1
		}
	}
	match := make([]int, m)
	nextAtBal := make(map[int]int, m)
	minVal := m + 1
	minIdx := -1
	for i := m - 1; i >= 0; i-- {
		if tokens[i].isClose {
			v := bal[i+1]
			nextAtBal[v] = i
			if v <= minVal {
				minVal = v
				minIdx = i
			}
			match[i] = -1
			continue
		}
		if j, ok := nextAtBal[bal[i]]; ok {
			match[i] = j
		} else {
			match[i] = minIdx
		}
	}
	var blocks []ContextBlock
	for i := 0; i < m; {
		if tokens[i].isClose || match[i] < 0 {
			i++
			continue
		}
		j := match[i]
		blocks = append(blocks, ContextBlock{
			Start:   tokens[i].start,
			OpenEnd: tokens[i].end,
			CloseAt: tokens[j].start,
			End:     tokens[j].end,
			Attrs:   tokens[i].attrs,
		})
		i = j + 1
	}
	return blocks
}

// FindLeadingContextBlocks returns the contiguous context blocks at the start of s (separated only by whitespace)
// and the byte offset immediately after the last leading block.
func FindLeadingContextBlocks(s string) ([]ContextBlock, int) {
	blocks := FindContextBlocks(s)
	if len(blocks) == 0 {
		return nil, 0
	}
	pos := 0
	count := 0
	for _, b := range blocks {
		if strings.TrimSpace(s[pos:b.Start]) != "" {
			break
		}
		count++
		pos = b.End
	}
	return blocks[:count], pos
}

// StripContextBlocks replaces every top-level context block in s with replacement.
// When trimTrailingSpace is true, ASCII whitespace immediately following each removed block is also consumed.
func StripContextBlocks(s, replacement string, trimTrailingSpace bool) string {
	blocks := FindContextBlocks(s)
	if len(blocks) == 0 {
		return s
	}
	var out strings.Builder
	out.Grow(len(s))
	prev := 0
	for _, b := range blocks {
		if b.Start < prev {
			continue
		}
		out.WriteString(s[prev:b.Start])
		out.WriteString(replacement)
		end := b.End
		if trimTrailingSpace {
			for end < len(s) && isASCIISpace(s[end]) {
				end++
			}
		}
		prev = end
	}
	out.WriteString(s[prev:])
	return out.String()
}

// StripSnapshotBlocks removes only top-level attribute-free (bare) context snapshot blocks and their trailing
// whitespace. Attributed context blocks (quote, forward, context, message-source, ...) are skipped whole so literal
// bare tags inside an attributed block can never steal the outer closing tag.
func StripSnapshotBlocks(s string) string {
	return stripSnapshotBlocks(s, false)
}

// StripSnapshotBlocksUnicode is StripSnapshotBlocks consuming Unicode/JS whitespace after each removed bare block.
func StripSnapshotBlocksUnicode(s string) string {
	return stripSnapshotBlocks(s, true)
}

func contextTagName(tag string) string {
	rest := strings.TrimPrefix(strings.TrimPrefix(tag, "</"), "<")
	if end := strings.IndexAny(rest, " \t\r\n>"); end >= 0 {
		return rest[:end]
	}
	return rest
}

func stripSnapshotBlocks(s string, unicodeSpace bool) string {
	blocks := FindContextBlocks(s)
	if len(blocks) == 0 {
		return s
	}
	var out strings.Builder
	prev := 0
	changed := false
	for _, b := range blocks {
		if b.Start < prev || strings.TrimSpace(b.Attrs) != "" {
			continue
		}
		if contextTagName(s[b.Start:b.OpenEnd]) != contextTagName(s[b.CloseAt:b.End]) {
			continue
		}
		if !changed {
			out.Grow(len(s))
			changed = true
		}
		out.WriteString(s[prev:b.Start])
		end := b.End
		if unicodeSpace {
			for end < len(s) {
				r, size := utf8.DecodeRuneInString(s[end:])
				if !isJSSpace(r) {
					break
				}
				end += size
			}
		} else {
			for end < len(s) && isASCIISpace(s[end]) {
				end++
			}
		}
		prev = end
	}
	if !changed {
		return s
	}
	out.WriteString(s[prev:])
	return out.String()
}

func isASCIISpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\f' || b == '\v'
}

func isJSSpace(r rune) bool {
	return unicode.IsSpace(r) || r == 0xFEFF
}
