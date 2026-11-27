package prompts

import (
	"strings"

	"github.com/nwverse/persona/server/internal/compat"
	"github.com/nwverse/persona/server/internal/lazyre"
)

// closingTag is anything a reader of a message could take for the end of a context block: either generation,
// any case, spaces around the slash and before '>'.
var closingTag = lazyre.New(`(?i)<\s*/\s*` + compat.ContextPattern() + `\s*>`)

// contextTag matches any literal opening or closing context tag prefix (either generation, any case, optional
// spaces around the slash), even without a closing '>'.
var contextTag = lazyre.New(`(?i)<(\s*/?\s*` + compat.ContextPattern() + `\b)`)

// escapedContextTag matches the entity-escaped form written by ContextBody.
var escapedContextTag = lazyre.New(`(?i)&lt;(\s*/?\s*` + compat.ContextPattern() + `\b)`)

// ContextBody makes text safe to go inside a context block: every literal opening or closing context tag has its
// '<' written as '&lt;' so nothing inside the body can close the block, trigger snapshot stripping, or be rewritten
// by compatibility adapters, while readers can restore the original text losslessly. Replacing '<' with '&lt;'
// never joins surrounding fragments into a new tag, so one linear pass suffices. Templates call it as 'ctx'.
func ContextBody(s string) string {
	if !strings.ContainsRune(s, '<') {
		return s
	}
	return contextTag.ReplaceAllString(s, "&lt;$1")
}

// UnescapeContextBody restores literal context tags escaped by ContextBody.
func UnescapeContextBody(s string) string {
	if !strings.ContainsRune(s, '&') {
		return s
	}
	return escapedContextTag.ReplaceAllString(s, "<$1")
}
