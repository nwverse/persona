import { canonicalAppURL } from "./compatibility";
import type { ContextRef } from "./links";
import type { Context } from "./turns";

export type InlineDocument = readonly (string | ContextRef)[];
export type InlineSelection = [start: number, end: number];

/** Offsets are UTF-16 code units, with one unit per reference, regardless of its label's length. */
export const INLINE_TOKEN = "\uFFFC";

export function editorText(doc: InlineDocument): string {
  return doc.map((part) => typeof part === "string" ? part : INLINE_TOKEN).join("");
}

export function joinInline(...parts: InlineDocument[]): InlineDocument {
  const result: (string | ContextRef)[] = [];
  for (const part of parts.flat()) {
    if (!part) continue;
    const last = result.at(-1);
    if (typeof part === "string" && typeof last === "string") result[result.length - 1] = last + part;
    else result.push(part);
  }
  return result;
}

export function sliceInline(doc: InlineDocument, start: number, end = Infinity): InlineDocument {
  let offset = 0;
  return doc.flatMap<string | ContextRef>((part) => {
    const from = offset;
    offset += typeof part === "string" ? part.length : 1;
    if (offset <= start || end <= from) return [];
    return typeof part === "string" ? part.slice(Math.max(0, start - from), end - from) : [part];
  });
}

export function replaceInlineRange(doc: InlineDocument, start: number, end: number, insert: InlineDocument): InlineDocument {
  return joinInline(sliceInline(doc, 0, start), insert, sliceInline(doc, end));
}

export function mentionLabel(ref: ContextRef): string {
  return (ref.title || ref.attrs?.path || ref.url).replace(/\s*\r?\n\s*/g, " ").trim();
}

function mentionedText(ref: ContextRef): string {
  return ref.kind === "file" && ref.attrs?.path ? `@${ref.attrs.path}` : mentionLabel(ref);
}

export function visibleInlineText(doc: InlineDocument): string {
  return doc.map((part) => typeof part === "string" ? part : mentionedText(part)).join("");
}

const DRAFT = "\0persona-inline-v1\n";

export function writeInlineDraft(doc: InlineDocument): string {
  return doc.some((part) => typeof part !== "string") ? DRAFT + JSON.stringify(doc) : doc.join("");
}

export function readInlineDraft(raw: string): InlineDocument {
  if (raw.startsWith(DRAFT)) {
    try {
      const doc = JSON.parse(raw.slice(DRAFT.length));
      if (Array.isArray(doc) && doc.every((p) => typeof p === "string" || (p && typeof p.id === "string"
        && typeof p.kind === "string" && typeof p.title === "string" && typeof p.url === "string" && typeof p.text === "string"
        && (p.attrs === undefined || (p.attrs && Object.values(p.attrs).every((v) => typeof v === "string")))))) return doc;
    } catch { /* A stored plain draft may happen to start with the prefix. */ }
  }
  return raw ? [raw] : [];
}

export function draftText(raw: string): string {
  const start = raw.indexOf(DRAFT);
  return start < 0 ? raw : visibleInlineText(readInlineDraft(raw.slice(start)));
}

/** Use wire identity, which survives draft IDs, reordered blocks and hub enrichment of context descriptions. */
function referenceKey(ref: Context): string | undefined {
  const a = ref.attrs;
  const id = ref.kind === "skill" ? a?.ref
    : ref.kind === "file" && a?.path ? [a.path, a.machine, a.cwd, a.launcher, a.runtime]
    : ref.kind === "workspace" && a?.cwd ? [a.machine, a.cwd]
    : ref.kind === "repository" ? a?.repo_id
    : ref.kind === "context" && ref.url ? canonicalAppURL(ref.url).match(/^persona:\/\/session\/[^?]+$/)?.[0] : undefined;
  return id === undefined ? undefined : JSON.stringify([ref.kind, id]);
}

export function inlineReferences(doc: InlineDocument): ContextRef[] {
  const refs = new Map<string | undefined, ContextRef>();
  for (const part of doc) if (typeof part !== "string") refs.set(referenceKey(part) ?? part.id, part);
  return [...refs.values()];
}

type Anchor = [fromEnd: number, length: number, key: string];
const TRAILER = /\n\n<!-- persona-inline-v1 (\[[^\r\n]*\]) -->$/;

export function splitInlineTrailer(body: string): { text: string; inlineTrailer?: string } {
  const match = TRAILER.exec(body);
  return match ? { text: body.slice(0, match.index), inlineTrailer: match[0] } : { text: body };
}

/** The body stays ordinary text, including native @paths. The trailer points back from the end of the prose so quotes
 * and other prefixes do not move anchors. Actual selection metadata still travels in the existing context tags. */
export function inlineBody(doc: InlineDocument, trim = true): string {
  const anchors: Anchor[] = [];
  let text = "";
  for (const part of doc) {
    if (typeof part === "string") {
      text += part;
    } else {
      const label = mentionedText(part);
      const key = referenceKey(part);
      if (key && label) anchors.push([text.length, label.length, key]);
      text += label;
    }
  }
  if (!anchors.length) return text;
  if (trim) {
    const [start, length] = anchors[anchors.length - 1];
    text = text.slice(0, Math.max(start + length, text.trimEnd().length));
  }
  for (const anchor of anchors) anchor[0] = text.length - anchor[0];
  const encoded = JSON.stringify(anchors).replaceAll("-", "\\u002d").replaceAll("<", "\\u003c");
  return `${trim ? text.trimStart() : text}\n\n<!-- persona-inline-v1 ${encoded} -->`;
}

export function readInlineBody<T extends Context>(body: string, refs: readonly T[]) {
  const used = new Set<T>();
  const trailer = TRAILER.exec(body);
  if (!trailer) return { text: body, document: body ? [body] : [], used };
  const text = body.slice(0, trailer.index);
  const document: (string | ContextRef)[] = [];
  const byKey = new Map(refs.map((ref) => [referenceKey(ref), ref]));
  let cursor = 0;
  try {
    const anchors: unknown = JSON.parse(trailer[1]);
    if (Array.isArray(anchors)) for (const anchor of anchors) {
      if (!Array.isArray(anchor)) continue;
      const [fromEnd, length, key] = anchor;
      if (!Number.isSafeInteger(fromEnd) || !Number.isSafeInteger(length) || length < 1 || typeof key !== "string") continue;
      const start = text.length - fromEnd;
      const ref = byKey.get(key);
      if (!ref || start < cursor || start + length > text.length) continue;
      const picked = { ...ref, id: "id" in ref ? String(ref.id) : key, kind: ref.kind as ContextRef["kind"], title: ref.title ?? "", url: ref.url ?? "" };
      if (text.slice(start, start + length) !== mentionedText(picked)) continue;
      document.push(text.slice(cursor, start), picked);
      used.add(ref);
      cursor = start + length;
    }
  } catch { return { text: body, document: [body], used }; }
  document.push(text.slice(cursor));
  return { text, document: joinInline(document), used };
}
