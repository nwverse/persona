import { useCallback, useEffect, useRef, useState, type RefObject } from "react";
import { commentKey } from "../../../lib/comment-target";
import { rangeLabel } from "../../../lib/comments";
import { closePanel, type Panel } from "../../../lib/panel";
import { addQuote } from "../../../lib/drafts";
import { mediaMatches, NARROW } from "../../../lib/viewport";
import { CommentPill, isCommentShortcut } from "../../chat/CommentPill";

export type FileCommentTarget = Omit<Extract<Panel, { kind: "file" }>, "kind" | "line" | "to">;

function codeEdge(node: Node, offset: number) {
  const element = node.nodeType === Node.ELEMENT_NODE ? node as Element : node.parentElement;
  const row = element?.closest("[data-line]");
  const span = row?.querySelector("[data-file-code-text]");
  const line = Number(row?.getAttribute("data-line"));
  if (!span || !line || (node !== row && !span.contains(node))) return null;
  const prefix = span.ownerDocument.createRange();
  prefix.selectNodeContents(span);
  const marker = span.querySelector("[data-file-truncation-marker]");
  if (marker) prefix.setEndBefore(marker);
  // Endpoints in or beyond the marker stop at the last visible source character.
  if (prefix.comparePoint(node, offset) <= 0) prefix.setEnd(node, offset);
  return { line, offset: prefix.toString().length };
}

/** The source rows are virtualized and contain a gutter. Rebuild the selection from its text endpoints and source lines. */
function selectedCode(range: Range, lines: readonly string[], maxWidth: number) {
  const start = codeEdge(range.startContainer, range.startOffset);
  const end = codeEdge(range.endContainer, range.endOffset);
  // Controls and virtual spacers have no code edge. Both edges must resolve to loaded source rows.
  if (!start || !end || start.line > end.line || end.line > lines.length) return null;
  const startLength = Math.min(lines[start.line - 1].length, maxWidth);
  const first = start.offset > 0 && start.offset >= startLength && start.line < end.line ? start.line + 1 : start.line;
  const last = end.offset === 0 && end.line > start.line ? end.line - 1 : end.line;
  const text = lines.slice(first - 1, last).map((raw, index) => {
    const line = first + index;
    const visibleLength = Math.min(raw.length, maxWidth);
    const from = line === start.line ? Math.min(start.offset, visibleLength) : 0;
    const to = line === end.line ? Math.min(end.offset, visibleLength) : visibleLength;
    return raw.slice(from, to);
  }).join("\n");
  return { text, line: first, to: last };
}

/** Offer a comment for text selected in the file viewer. */
export function FileSelection({ view, file, lines, source, maxWidth = Infinity }: {
  view: RefObject<HTMLElement | null>;
  file: FileCommentTarget;
  lines?: readonly string[];
  source?: boolean;
  maxWidth?: number;
}) {
  const { mid, sid, cwd, path } = file;
  const [pill, setPill] = useState<{ x: number; y: number } | null>(null);
  const picked = useRef<{ key: string; doc: Document; text: string; line?: number; to?: number } | null>(null);
  const hide = useCallback(() => {
    picked.current = null;
    setPill(null);
  }, []);
  const add = useCallback(() => {
    const selection = picked.current;
    hide();
    if (!selection || commentKey(mid, sid) !== selection.key) return;
    const { key, doc, text, line, to } = selection;
    const label = path.replaceAll("\r", "\\r").replaceAll("\n", "\\n");
    const location = line === undefined ? label : `${label}:${rangeLabel(line, to ?? line)}`;
    const panel = { kind: "file" as const, path, line, to, mid, sid, cwd };
    doc.getSelection()?.removeAllRanges();
    addQuote(key, { text, comment: { body: "", file: { location, panel } } }, true);
    if (mediaMatches(NARROW)) closePanel();
  }, [mid, sid, cwd, path, hide]);

  useEffect(() => {
    const root = view.current;
    if (!root) return;
    hide();
    const doc = root.ownerDocument;
    let raf = 0;
    let dragging = false;
    const read = () => {
      const key = commentKey(mid, sid);
      const sel = doc.getSelection();
      if (!key || dragging || !sel || sel.isCollapsed || !sel.rangeCount) return hide();
      const range = sel.getRangeAt(0);
      if (!root.contains(range.commonAncestorContainer)) return hide();
      const code = lines && selectedCode(range, lines, maxWidth);
      const text = lines ? code?.text : sel.toString().trim();
      if (!text?.trim()) return hide();
      const rects = range.getClientRects();
      const last = rects[rects.length - 1];
      if (!last) return hide();
      const bounds = root.getBoundingClientRect();
      const x = last.right;
      const top = last.top;
      const bottom = top + last.height;
      if (bottom <= bounds.top || bottom > bounds.bottom || x < bounds.left || x > bounds.right) return hide();
      picked.current = { key, doc, text, line: source ? code?.line : undefined, to: source ? code?.to : undefined };
      setPill({ x: Math.min(Math.max(x - 36, 8), window.innerWidth - 112), y: bottom + 38 > window.innerHeight ? Math.max(0, top - 38) : bottom + 6 });
    };
    const soon = () => {
      cancelAnimationFrame(raf);
      raf = requestAnimationFrame(read);
    };
    const down = (e: PointerEvent) => {
      const target = e.target as Element | null;
      if (target?.closest?.("[data-comment-pill]")) return;
      dragging = Boolean(target && root.contains(target));
      hide();
    };
    const up = () => {
      if (!dragging) return;
      dragging = false;
      soon();
    };
    const press = (e: KeyboardEvent) => {
      if (!picked.current || !isCommentShortcut(e)) return;
      e.preventDefault();
      add();
    };
    doc.addEventListener("selectionchange", soon);
    doc.addEventListener("pointerdown", down, true);
    doc.addEventListener("pointerup", up, true);
    doc.addEventListener("pointercancel", up, true);
    doc.addEventListener("keydown", press);
    doc.addEventListener("scroll", soon, { capture: true, passive: true });
    window.addEventListener("resize", soon);
    return () => {
      cancelAnimationFrame(raf);
      doc.removeEventListener("selectionchange", soon);
      doc.removeEventListener("pointerdown", down, true);
      doc.removeEventListener("pointerup", up, true);
      doc.removeEventListener("pointercancel", up, true);
      doc.removeEventListener("keydown", press);
      doc.removeEventListener("scroll", soon, true);
      window.removeEventListener("resize", soon);
    };
  }, [view, lines, source, maxWidth, mid, sid, add, hide]);

  return pill && <CommentPill at={pill} onComment={add} />;
}
