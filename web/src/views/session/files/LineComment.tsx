// Comments on lines of a file or a diff (#2437, #11585): the line number turns into a + when the pointer is over the
// row, a click (or a drag, or shift-click, over several) picks the lines, and the shared floating CommentBar opens
// anchored at the picked line (Enter or ✓ saves, Esc cancels).
import { useEffect, useRef, useState } from "react";
import { lineRange } from "../../../lib/comments";
import { Icon } from "../../../ui/icons";
import { CommentBar, type CommentBarAnchor } from "../../chat/CommentBar";
import { NumberDot } from "../../chat/CommentCard";
import { t } from "../../../i18n";

export interface LinePick {
  /** The lines picked, once the pointer let go. */
  range: { from: number; to: number } | null;
  /** Viewport rect of the picked line's gutter button, so the floating bar anchors right at the line. */
  anchor: CommentBarAnchor | null;
  /** The lines being swept right now (the bar waits for the pointer). */
  sweeping: { from: number; to: number } | null;
  down: (n: number, e: React.PointerEvent) => void;
  enter: (n: number, e?: React.PointerEvent) => void;
  clear: () => void;
}

function rectOf(el: EventTarget | null): CommentBarAnchor | null {
  if (!(el instanceof Element)) return null;
  const r = el.getBoundingClientRect();
  return { x: r.right + 8, top: r.top, bottom: r.bottom };
}

function visibleRectOf(el: Element | null): CommentBarAnchor | null {
  if (!el || !el.isConnected) return null;
  const r = el.getBoundingClientRect();
  if (r.width === 0 && r.height === 0) return null;
  const scroller = el.closest("[data-testid='diff'], [data-testid='file-code']") ?? el.parentElement?.closest("[class*='overflow-']");
  const bounds = scroller ? scroller.getBoundingClientRect() : new DOMRect(0, 0, window.innerWidth, window.innerHeight);
  if (r.bottom < bounds.top || r.top > bounds.bottom) return null;
  if (r.bottom < 0 || r.top > window.innerHeight) return null;
  return { x: r.right + 8, top: r.top, bottom: r.bottom };
}

function sameAnchor(a: CommentBarAnchor | null, b: CommentBarAnchor | null): boolean {
  if (a === b) return true;
  if (!a || !b) return false;
  return a.x === b.x && a.top === b.top && a.bottom === b.bottom;
}

/** Picking lines by their numbers: pointer down starts (shift extends), moving over other rows while it is down sweeps. */
export function useLinePick(enabled: boolean): LinePick {
  const [range, setRange] = useState<{ from: number; to: number } | null>(null);
  const [anchor, setAnchor] = useState<CommentBarAnchor | null>(null);
  const [sweeping, setSweeping] = useState<{ from: number; to: number } | null>(null);
  const startLine = useRef<number | null>(null);
  const anchorRef = useRef<CommentBarAnchor | null>(null);
  const anchorElRef = useRef<Element | null>(null);
  const scopeRef = useRef<Element | null>(null);
  const drag = useRef(false);
  useEffect(() => {
    const up = () => {
      if (!drag.current) return;
      drag.current = false;
      setSweeping((s) => {
        if (s) {
          setRange(s);
          setAnchor(anchorRef.current);
        }
        return null;
      });
    };
    window.addEventListener("pointerup", up);
    window.addEventListener("pointercancel", up);
    return () => {
      window.removeEventListener("pointerup", up);
      window.removeEventListener("pointercancel", up);
    };
  }, []);
  useEffect(() => {
    if (!range) return;
    let frame = 0;
    const measure = () => {
      let el = anchorElRef.current;
      // Only re-query within our own connected scroll container so another mounted file/diff gutter is never matched.
      if ((!el || !el.isConnected) && startLine.current !== null && scopeRef.current?.isConnected) {
        el = scopeRef.current.querySelector(`[data-gutter-at="${startLine.current}"]`);
        if (el) anchorElRef.current = el;
      }
      const next = visibleRectOf(el);
      anchorRef.current = next;
      setAnchor((prev) => (sameAnchor(prev, next) ? prev : next));
    };
    const soon = () => {
      cancelAnimationFrame(frame);
      frame = requestAnimationFrame(measure);
    };
    window.addEventListener("scroll", soon, true);
    window.addEventListener("resize", soon);
    return () => {
      cancelAnimationFrame(frame);
      window.removeEventListener("scroll", soon, true);
      window.removeEventListener("resize", soon);
    };
  }, [range]);
  return {
    range,
    anchor,
    sweeping,
    down: (n, e) => {
      if (!enabled || e.button !== 0) return;
      e.preventDefault(); // no text selection while sweeping the numbers
      if (e.shiftKey && startLine.current !== null) {
        if (!anchorRef.current) {
          anchorElRef.current = e.currentTarget;
          scopeRef.current = e.currentTarget.closest("[data-testid='diff'], [data-testid='file-code']") ?? null;
          anchorRef.current = rectOf(e.currentTarget);
        }
        setSweeping(lineRange(startLine.current, n));
      } else {
        startLine.current = n;
        anchorElRef.current = e.currentTarget;
        scopeRef.current = e.currentTarget.closest("[data-testid='diff'], [data-testid='file-code']") ?? null;
        anchorRef.current = rectOf(e.currentTarget);
        setSweeping({ from: n, to: n });
      }
      setRange(null);
      setAnchor(null);
      drag.current = true;
    },
    enter: (n, e) => {
      if (drag.current && startLine.current !== null) {
        // Keep the anchor at the start line where the pick began; only populate if missing.
        if (!anchorRef.current && e) {
          anchorElRef.current = e.currentTarget;
          anchorRef.current = rectOf(e.currentTarget);
        }
        setSweeping(lineRange(startLine.current, n));
      }
    },
    clear: () => {
      startLine.current = null;
      anchorRef.current = null;
      anchorElRef.current = null;
      scopeRef.current = null;
      setRange(null);
      setAnchor(null);
      setSweeping(null);
    },
  };
}

/** A row's number: the digits, a + under the pointer, the comment's number where one waits. */
export function GutterNumber({ n, at = n, pick, marked, enabled, className, style }: { n: number | undefined; /** What the pick counts by when it is not the number itself (a diff counts rows). */ at?: number; pick: LinePick; marked?: number; enabled: boolean; className?: string; style?: React.CSSProperties }) {
  if (n === undefined || at === undefined || !enabled) return <span className={className} style={style}>{n ?? ""}</span>;
  return (
    <button
      type="button"
      tabIndex={-1}
      aria-label={marked ? t("chat-comments.gutter_marked", { n: marked }) : t("chat-comments.gutter_add", { line: n })}
      title={t("chat-comments.gutter_add", { line: n })}
      onPointerDown={(e) => pick.down(at, e)}
      onPointerEnter={(e) => pick.enter(at, e)}
      className={`${className ?? ""} relative flex cursor-pointer items-center justify-end`}
      style={style}
      data-gutter={n}
      data-gutter-at={at}
    >
      <span className="[@media(hover:hover)]:group-hover/row:invisible">{marked ? <NumberDot n={marked} /> : n}</span>
      <span aria-hidden className="absolute inset-y-0 right-1.5 hidden items-center text-accent-ink [@media(hover:hover)]:group-hover/row:flex">
        <Icon name="plus" size={13} stroke={2.4} />
      </span>
    </button>
  );
}

/** Floating one-line comment bar anchored at the picked code line (or docked on narrow screens / preview panel). */
const isLineGutter = (el: Element) => Boolean(el.closest("[data-gutter]"));

export function LineBox({ where, anchor, onAdd, onCancel }: { where: string; anchor?: CommentBarAnchor | null; onAdd: (body: string) => void; onCancel: () => void }) {
  return (
    <CommentBar
      anchor={anchor ?? null}
      context={where}
      showContextOnDesktop
      autoFocus
      captureEscape
      ignoreOutside={isLineGutter}
      onSave={onAdd}
      onCancel={onCancel}
      ariaLabel={t("chat-comments.box_label")}
    />
  );
}
