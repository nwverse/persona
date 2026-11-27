import { CONTEXT_TAG_RE, ESCAPED_CONTEXT_TAG_RE } from "./compatibility";

/** A text to go inside a `<persona-context>` block, with every literal opening or closing context tag's `<`
 *  escaped as `&lt;` so nothing inside the body can break block boundaries, trigger snapshot stripping, or be
 *  rewritten by compatibility adapters, while readers can restore the original text losslessly. One linear pass. */
export function contextBody(text: string): string {
  return text.includes("<") ? text.replace(CONTEXT_TAG_RE, "&lt;$1") : text;
}

/** Restores literal context tags escaped by `contextBody`. */
export function unescapeContextBody(text: string): string {
  return text.includes("&") ? text.replace(ESCAPED_CONTEXT_TAG_RE, "<$1") : text;
}
