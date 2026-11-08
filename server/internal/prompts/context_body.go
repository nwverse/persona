package prompts

import "github.com/nwverse/persona/server/internal/lazyre"

// closingTag is anything a reader of a message could take for the end of a <persona-context>
// block: either name (#3182), any case, spaces around the slash and before `>` (the apps match it as loosely, web
// lib/context-body.ts).
var closingTag = lazyre.New(`(?i)<\s*/\s*persona-context\s*>`)

// ContextBody is a text to go inside a context block with nothing left in it that closes the block, so what follows
// cannot sit outside it as the user's own words (a quoted or forwarded message, a mail handed to the AI twin, comes
// from someone else; #3754). Closing tags are removed, and one the removal itself puts together
// ("</persona-</persona-context>context>") has its `<` written as `&lt;`, which joins nothing: two passes, linear in
// the text. Templates call it as `ctx`.
func ContextBody(s string) string {
	s = closingTag.ReplaceAllString(s, "")
	return closingTag.ReplaceAllStringFunc(s, func(tag string) string { return "&lt;" + tag[1:] })
}
