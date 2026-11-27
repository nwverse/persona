package persona.core.contracts

/**
 * What a reader may take as the end of a `<persona-context>` block: any case, spaces
 * around the slash and before `>`. The same pattern as the hub's `prompts.ContextBody` and the web's `contextBody`.
 */
val contextClosingTag = ProtocolCompatibility.contextClosingTag

/**
 * [text] made safe to go inside a context block: every literal opening or closing context tag has its `<` written as
 * `&lt;` so nothing inside the body can close the block, trigger snapshot stripping, or be rewritten by compatibility
 * adapters, while readers can restore the original text losslessly. One linear pass; matches the hub, web, and Apple.
 */
fun contextBody(text: String): String =
    if ('<' !in text) text else text.replace(ProtocolCompatibility.contextTagPrefix) { "&lt;" + it.groupValues[1] }

/** Restores literal context tags escaped by [contextBody]. */
fun unescapeContextBody(text: String): String =
    if ('&' !in text) text else text.replace(ProtocolCompatibility.escapedContextTagPrefix) { "<" + it.groupValues[1] }
