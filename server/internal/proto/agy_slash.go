package proto

import "strings"

// agySlashCommands is the allowlist of Antigravity (agy) native reasoning,
// orchestration, and workflow slash commands that start a turn with a prompt
// body and require byte 0 to stay "/<cmd>" for native command recognition.
// Single-line local control commands such as /compact and /status stay on the
// runtimeCommandOnly path.
var agySlashCommands = map[string]bool{
	"/boost":    true,
	"/teamwork": true,
	"/swarm":    true,
	"/goal":     true,
	"/plan":     true,
	"/grill-me": true,
	"/learn":    true,
	"/schedule": true,
	"/browser":  true,
}

// isASCIIWhitespace matches the exact 6 ASCII whitespace bytes ([ \t\n\r\f\v])
// shared with integrations/antigravity/lib/runtime.js so Go and JS never drift
// on non-ASCII whitespace such as U+0085, U+00A0, or U+FEFF.
func isASCIIWhitespace(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\r', '\f', '\v':
		return true
	default:
		return false
	}
}

// asciiLower lowercases only ASCII A-Z so Unicode case-folding quirks such as
// U+212A (Kelvin sign) never fold into an allowlisted command name.
func asciiLower(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			if b.Len() == 0 {
				b.Grow(len(s))
				b.WriteString(s[:i])
			}
			b.WriteByte(c + ('a' - 'A'))
		} else if b.Len() > 0 {
			b.WriteByte(c)
		}
	}
	if b.Len() == 0 {
		return s
	}
	return b.String()
}

// AgySlashCommand returns the lower-cased allowlisted Antigravity slash command
// at the start of text (for example "/boost"), or "" when text does not begin
// with one. Leading ASCII whitespace is ignored and the command must be followed
// by end-of-string or ASCII whitespace, so filesystem paths such as
// "/plan/spec.md" or "/Users/foo/bar" return "".
func AgySlashCommand(text string) string {
	trimmed := strings.TrimLeftFunc(text, isASCIIWhitespace)
	if len(trimmed) < 2 || trimmed[0] != '/' {
		return ""
	}
	word := trimmed
	if i := strings.IndexFunc(trimmed, isASCIIWhitespace); i >= 0 {
		word = trimmed[:i]
	}
	lower := asciiLower(word)
	if agySlashCommands[lower] {
		return lower
	}
	return ""
}

// HasAgySlashCommand reports whether text begins with an allowlisted
// Antigravity native slash command.
func HasAgySlashCommand(text string) bool {
	return AgySlashCommand(text) != ""
}

// PrependPreservingAgySlashCommand combines preamble and prompt. For ordinary
// prompts it returns preamble + "\n\n" + prompt (matching Persona's existing
// prepend order). When prompt begins with an allowlisted Antigravity slash
// command, it keeps "/<cmd>" strictly at byte 0 and moves preamble after the
// prompt body, preserving one or more trailing line-bounded <attached_images>
// blocks at the very end without splitting inline prose references.
func PrependPreservingAgySlashCommand(preamble, prompt string) string {
	preamble = strings.TrimSpace(preamble)
	if !HasAgySlashCommand(prompt) {
		if preamble == "" {
			return prompt
		}
		return preamble + "\n\n" + prompt
	}
	cleanPrompt := strings.TrimLeftFunc(prompt, isASCIIWhitespace)
	if preamble == "" {
		return cleanPrompt
	}
	const openTag = "<attached_images>"
	const closeTag = "</attached_images>"
	head := strings.TrimRightFunc(cleanPrompt, isASCIIWhitespace)
	splitAt := len(head)
	for {
		cur := strings.TrimRightFunc(head[:splitAt], isASCIIWhitespace)
		if !strings.HasSuffix(cur, closeTag) {
			break
		}
		closeIdx := len(cur) - len(closeTag)
		openIdx := strings.LastIndex(cur[:closeIdx], openTag)
		if openIdx <= 0 || strings.Contains(cur[openIdx:closeIdx], closeTag) {
			break
		}
		if cur[openIdx-1] != '\n' {
			break
		}
		before := strings.TrimRightFunc(cur[:openIdx], isASCIIWhitespace)
		if before == "" {
			break
		}
		splitAt = len(before)
	}
	if splitAt < len(head) {
		images := strings.TrimLeftFunc(head[splitAt:], isASCIIWhitespace)
		return head[:splitAt] + "\n\n" + preamble + "\n\n" + images
	}
	return head + "\n\n" + preamble
}
