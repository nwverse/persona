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

// tagStart is the start of any tag: `<`, spaces, slashes or backslashes or none, spaces, and a letter (any case, any
// script). Every tag the apps, the hub, the node's transcript reader or the runtime read out of a message's text starts
// so: a context block's or a file's (either name), the photos and pasted text a message carries (the readers also take
// `<\pasted_content>` and `<\/pasted_content>`, as nested pastes write them), the notes Persona and the runtime add
// (view feedback, a system reminder, a local command's caveat), another session's or a teammate's message, a task's
// notification, a command's or a shell's input and output, and any added later. "2 < 3" starts none.
var tagStart = lazyre.New(`<(\s*[/\\]*\s*\pL)`)

// PlainWords is someone else's text that stands as a message's own words after Persona's framing block (a partner
// hub's message): the `<` of every tag in it is written as `&lt;`, so none opens, closes or forges a block, a file, a
// photo, a note or someone else's message, the apps show all of it as this message's words and the model reads it as
// the words it is. Templates call it as `plain`.
func PlainWords(s string) string {
	return tagStart.ReplaceAllString(s, "&lt;$1")
}
