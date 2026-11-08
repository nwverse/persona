// An agent's live text (its `delta`) against what its transcript already holds (docs/integrations/claude-integration.md
// "Live replies (MessageDisplay)"). The live text is never the source: once the transcript has a reply, the reply's
// item replaces it, matched by its start. The node already sends only what its transcript lacks, again right after
// each push of items; this is the page's side of it, for a delta that reaches the page after items that hold it (a page
// whose own read of the transcript got ahead of the node's last push). Only the words of the agent's latest API
// message can be the live message's: a message lands whole, with its tool call's result, so words followed by anything
// not the agent's (a tool's result, a message typed meanwhile) are an earlier message's, which may begin alike.
import type { Item } from "./types";

/** An agent's words as the transcript has them; `size`: a cut item's whole length in bytes (the hub cuts long text for
 *  pages and pushes, `text` holding only its start), 0 for a whole one. */
export interface Landed {
  text: string;
  size: number;
}

/** The words of the agent's latest message: its text items at the end of the transcript so far, oldest first; none
 *  when the transcript ends in anything else (a tool's result, a message of the user's, a notice). */
export function landedWords(items: readonly Item[]): Landed[] {
  const out: Landed[] = [];
  for (let i = items.length - 1; i >= 0 && items[i].role === "assistant"; i--) {
    const it = items[i];
    if (it.kind === "text" && it.text?.trim()) out.push(landedItem(it));
  }
  return out.reverse();
}

function landedItem(it: Item): Landed {
  const text = it.text ?? "";
  if (!it.full_size) return { text, size: 0 };
  const note = text.lastIndexOf("\n…"); // the line a cut text ends in
  return { text: note >= 0 ? text.slice(0, note) : text, size: it.full_size };
}

const LEADING = /^[ \t\r\n]+/;
const TRAILING = /[ \t\r\n]+$/;
const SPACE = /\s/u;
const PUNCT = /\p{P}/u;

/** `t`, a start of `body`, ends where a word does: body goes on with a space or nothing, or t ends in punctuation
 *  ("OK" is not the start of "OK, the tests pass"; the node's `wordEnd`). */
function wordEnd(body: string, t: string): boolean {
  if (body.length === t.length) return true;
  const next = String.fromCodePoint(body.codePointAt(t.length) ?? 0);
  const last = Array.from(t).at(-1) ?? "";
  return SPACE.test(next) || PUNCT.test(last);
}

const utf8 = new TextEncoder();
const fromUTF8 = new TextDecoder();

/** What of a live message the transcript does not have yet, "" for nothing (the node's `unlanded`, livereply.go): the
 *  latest message's words that begin like it are taken off its start, block by block (a cut item holds its whole block,
 *  `size` bytes); live text an item begins with is all there. What is left is as it came. */
export function unlandedText(text: string, words: readonly Landed[]): string {
  let s = text;
  let trimmed = false;
  for (const w of words) {
    const t = w.text.trim();
    if (!t) continue;
    const body = s.replace(LEADING, "");
    if (!body.trim()) break;
    if (t.startsWith(body.replace(TRAILING, ""))) return "";
    if (!body.startsWith(t)) break;
    let rest: string;
    if (w.size > 0) {
      const bytes = utf8.encode(body);
      let n = w.size - utf8.encode(w.text.slice(0, w.text.length - w.text.replace(LEADING, "").length)).length;
      while (n < bytes.length && (bytes[n] & 0xc0) === 0x80) n++; // on a character's first byte
      if (n >= bytes.length) return "";
      rest = fromUTF8.decode(bytes.slice(n));
    } else {
      if (!wordEnd(body, t)) break;
      rest = body.slice(t.length);
    }
    s = rest.replace(LEADING, "");
    trimmed = true;
  }
  if (!s.trim()) return "";
  return trimmed ? s : text;
}

/** What of a live message the page's newest items do not hold: unlandedText against the latest message's words. A live
 *  text that is all of the message before them (its words, then a tool's result) is that message's copy come late: a
 *  message's final batch comes before its tool call, so a copy left from before its items landed is whole. */
export function unlandedLive(text: string, items: readonly Item[]): string {
  const rest = unlandedText(text, landedWords(items));
  if (!rest) return "";
  const whole = text.trim();
  let i = items.length - 1;
  while (i >= 0 && items[i].role === "assistant") i--; // the latest message
  for (; i >= 0 && items[i].role !== "assistant"; i--) if (opens(items[i])) return rest; // what came between
  for (; i >= 0 && items[i].role === "assistant"; i--) if (items[i].kind === "text" && !items[i].full_size && items[i].text?.trim() === whole) return "";
  return rest;
}

/** The message that opens an exchange: a prompt of the user's, or a background task waking the agent. */
function opens(it: Item): boolean {
  return Boolean(it.wake) || (it.role === "user" && it.kind === "text" && !it.queued);
}
