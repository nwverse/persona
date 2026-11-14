// A long group of the complete Agents list, a part at a time (WindowedRows draws a short one itself). The first rows
// (`windowHead`, about a screen of them) are always drawn, so the first screen and a group opening never wait; the
// rest come in parts of `windowPart` rows, each drawn while it is within a screen of the list's view (an
// IntersectionObserver decides, so nothing is measured on the main thread for it) or while it holds the open row (or
// held it last) or the keyboard's focus or is next to one that does, so the highlight, the scroll to the open row, the
// next / previous row shortcut and Tab find their rows. A part out of reach is an empty box of the height it had when
// last drawn (a ResizeObserver's report) or, never drawn, of the first rows' average, so the scroll bar and what is
// below keep their place. The rows stay in the list's flow, not placed by offsets: sticky group headings, the folds
// above and their animation work as they did.
import { useEffect, useRef, useState } from "react";
import { SESSION_LIST_METRICS } from "../ui/list-metrics";
import type { WindowedProps } from "./WindowedRows";

const HEAD = SESSION_LIST_METRICS.windowHead;
const PART = SESSION_LIST_METRICS.windowPart;
/** The list's scroller (the sidebar's rows, the Agents page): the observer's root, its view and a screen above and
 *  below it being near. Without one, the window's. */
const ROW_SCROLLER = "[data-row-scroller]";

/** How many parts a list of `n` rows has: the head, then parts of `windowPart`. */
export const partCount = (n: number) => (n <= HEAD ? 1 : 1 + Math.ceil((n - HEAD) / PART));
/** The part holding row `index`. */
export const partOf = (index: number) => (index < HEAD ? 0 : 1 + Math.floor((index - HEAD) / PART));
/** Where part `i` of a list of `n` rows starts and ends. */
export const partRows = (i: number, n: number): [number, number] => (i === 0 ? [0, Math.min(HEAD, n)] : [HEAD + (i - 1) * PART, Math.min(n, HEAD + i * PART)]);

export function WindowedParts<T>({ items, render, holds, className = "flex flex-col gap-px" }: WindowedProps<T>) {
  const [near, setNear] = useState<ReadonlySet<number>>(() => new Set());
  // the part holding the keyboard's focus, drawn with the two beside it: Tab from a part's last row finds the next
  // part's first, and a focused row scrolled away is not taken out from under the focus
  const [focused, setFocused] = useState(-1);
  // each part's height when last drawn, with how many rows it had then
  const heights = useRef(new Map<number, { height: number; rows: number }>());
  const [rowHeight, setRowHeight] = useState<number>(SESSION_LIST_METRICS.estimate);
  const observers = useRef<{ near: IntersectionObserver; sizes: ResizeObserver } | null>(null);
  const boxes = useRef(new Map<number, HTMLElement>());
  // one ref per part for the list's life, so a render does not unobserve and observe them all again
  const refs = useRef(new Map<number, (el: HTMLDivElement | null) => void>());
  const refFor = (i: number) => {
    let ref = refs.current.get(i);
    if (!ref) {
      ref = (el) => {
        const was = boxes.current.get(i);
        if (was && was !== el) {
          observers.current?.near.unobserve(was);
          observers.current?.sizes.unobserve(was);
          boxes.current.delete(i);
        }
        if (el && el !== was) {
          boxes.current.set(i, el);
          observers.current?.near.observe(el);
          observers.current?.sizes.observe(el);
        }
      };
      refs.current.set(i, ref);
    }
    return ref;
  };
  useEffect(() => {
    const root = boxes.current.get(0)?.closest<HTMLElement>(ROW_SCROLLER) ?? null;
    const shown = new Set<number>();
    const nearBy = new IntersectionObserver((entries) => {
      let changed = false;
      for (const e of entries) {
        const i = Number((e.target as HTMLElement).dataset.rowsPart);
        if (e.isIntersecting === shown.has(i)) continue;
        changed = true;
        if (e.isIntersecting) shown.add(i);
        else shown.delete(i);
      }
      if (changed) setNear(new Set(shown));
    }, { root, rootMargin: "100% 0px" });
    const sizes = new ResizeObserver((entries) => {
      for (const e of entries) {
        const el = e.target as HTMLElement;
        const rows = Number(el.dataset.rowsDrawn ?? 0);
        if (!rows) continue; // an empty box: its height is the one it was given
        const height = e.borderBoxSize?.[0]?.blockSize ?? el.getBoundingClientRect().height;
        heights.current.set(Number(el.dataset.rowsPart), { height, rows });
        if (el.dataset.rowsPart === "0" && height > 0) setRowHeight((h) => (Math.abs(h - height / rows) < 0.5 ? h : height / rows));
      }
    });
    observers.current = { near: nearBy, sizes };
    for (const el of boxes.current.values()) {
      nearBy.observe(el);
      sizes.observe(el);
    }
    return () => {
      nearBy.disconnect();
      sizes.disconnect();
      observers.current = null;
    };
  }, []);
  const n = items.length;
  const open = holds ? items.findIndex(holds) : -1;
  const openPart = open < 0 ? -1 : partOf(open);
  // the part that held the open row last stays drawn after it does not (a herdr Agent's page moves on to its tab's
  // address at once, which no row here holds): the list's second try to scroll to it, once a group that opened for it
  // has grown, still finds the row
  const [held, setHeld] = useState(openPart);
  if (openPart >= 0 && openPart !== held) setHeld(openPart);
  const beside = (i: number, at: number) => at >= 0 && Math.abs(i - at) <= 1;
  return (
    <>
      {Array.from({ length: partCount(n) }, (_, i) => {
        const [from, to] = partRows(i, n);
        if (i === 0 || near.has(i) || beside(i, openPart >= 0 ? openPart : held) || beside(i, focused)) {
          return <div key={i} ref={refFor(i)} data-rows-part={i} data-rows-drawn={to - from} onFocus={() => setFocused(i)} className={className}>{items.slice(from, to).map((item) => render(item))}</div>;
        }
        const kept = heights.current.get(i);
        return <div key={i} ref={refFor(i)} data-rows-part={i} aria-hidden style={{ height: kept && kept.rows === to - from ? kept.height : (to - from) * rowHeight }} />;
      })}
    </>
  );
}
