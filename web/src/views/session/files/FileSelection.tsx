import { useCallback, useEffect, useRef, useState, type RefObject } from "react";
import { commentKey } from "../../../lib/comment-target";
import { rangeLabel } from "../../../lib/comments";
import { closePanel, type Panel } from "../../../lib/panel";
import { addQuote } from "../../../lib/drafts";
import { mediaMatches, NARROW } from "../../../lib/viewport";
import { CommentPill, isCommentShortcut } from "../../chat/CommentPill";

export type FileCommentTarget = Omit<Extract<Panel, { kind: "file" }>, "kind" | "line" | "to">;

function codeEdge(node: Node, offset: number) {
  const span = (node.nodeType === Node.ELEMENT_NODE ? node as Element : node.parentElement)?.closest("[data-file-code-text]");
  const line = Number(span?.closest("[data-line]")?.getAttribute("data-line"));
  if (!span || !line) return null;
  const prefix = span.ownerDocument.createRange();
  prefix.selectNodeContents(span);
  prefix.setEnd(node, offset);
  return { line, offset: prefix.toString().length };
}

/** The source rows are virtualized and contain a gutter. Rebuild the selection from its text endpoints and source lines. */
function selectedCode(range: Range, lines: readonly string[], maxWidth: number) {
  const start = codeEdge(range.startContainer, range.startOffset);
  const end = codeEdge(range.endContainer, range.endOffset);
  if (!start || !end) return null;
  const last = end.offset === 0 && end.line > start.line ? end.line - 1 : end.line;
  const text = lines.slice(start.line - 1, last).map((raw, index) => {
    const line = start.line + index;
    return raw.slice(line === start.line ? start.offset : 0, line === end.line ? end.offset : maxWidth);
  }).join("\n");
  return { text, line: start.line, to: last };
}

/** Both the main document and a static srcdoc use the browser's own selection. The button is always in Persona's page. */
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
    const el = view.current;
    if (!el) return;
    const frame = el instanceof HTMLIFrameElement ? el : null;
    let detach = () => {};
    const attach = () => {
      detach();
      hide();
      const doc = frame ? frame.contentDocument : el.ownerDocument;
      if (!doc || (frame && doc.URL !== "about:srcdoc")) return;
      const root = frame ? doc.body : el;
      if (!root) return;
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
        const bounds = el.getBoundingClientRect();
        // A child document's Range is relative to the frame viewport, including after a scroll inside the frame.
        const x = last.right + (frame ? bounds.left + frame.clientLeft : 0);
        const top = last.top + (frame ? bounds.top + frame.clientTop : 0);
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
      if (frame) document.addEventListener("pointerdown", down, true);
      window.addEventListener("resize", soon);
      detach = () => {
        cancelAnimationFrame(raf);
        doc.removeEventListener("selectionchange", soon);
        doc.removeEventListener("pointerdown", down, true);
        doc.removeEventListener("pointerup", up, true);
        doc.removeEventListener("pointercancel", up, true);
        doc.removeEventListener("keydown", press);
        doc.removeEventListener("scroll", soon, true);
        if (frame) document.removeEventListener("pointerdown", down, true);
        window.removeEventListener("resize", soon);
      };
    };
    frame?.addEventListener("load", attach);
    attach();
    return () => {
      frame?.removeEventListener("load", attach);
      detach();
    };
  }, [view, lines, source, maxWidth, mid, sid, add, hide]);

  return pill && <CommentPill at={pill} onComment={add} />;
}
