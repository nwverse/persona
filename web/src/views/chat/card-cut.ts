// A waiting card taller than the room left for it in the box it scrolls in (the dock's cards over the composer, a tray's
// list) keeps its answer row in view at that box's bottom edge (Card's sticky footer, #3112), while what the row answers
// (the title, the command) is out of view above it. Then the row says what it answers, and the stack says how many cards
// it holds (docs/design/DESIGN_SYSTEM.md 4.8): never an Allow next to nothing.
import { useEffect, useState, type RefObject } from "react";

/** The nearest ancestor that scrolls vertically: where a card's sticky answer row sticks. */
export function scrollBox(el: Element | null): HTMLElement | null {
  for (let p = el?.parentElement; p; p = p.parentElement) {
    const y = getComputedStyle(p).overflowY;
    if (y === "auto" || y === "scroll") return p;
  }
  return null;
}

/** Whether the end of a card's body (`end`, a zero-height mark under it) is out of view below the box it scrolls in:
 *  its answer row then stands at the box's bottom edge, away from what it answers. Above the box is not "below". */
export function cutBelow(visible: boolean, end: { top: number }, boxBottom: number): boolean {
  return !visible && end.top >= boxBottom - 1;
}

/** cutBelow for the mark `end`, kept current as the box scrolls and its cards come and go (one IntersectionObserver,
 *  no scroll listener); false where there is none (tests, older browsers). */
export function useCutBelow(end: RefObject<HTMLElement | null>): boolean {
  const [cut, setCut] = useState(false);
  useEffect(() => {
    const el = end.current;
    if (!el || typeof IntersectionObserver === "undefined") return;
    const io = new IntersectionObserver(([e]) => setCut(cutBelow(e.isIntersecting, e.boundingClientRect, e.rootBounds?.bottom ?? innerHeight)), { root: scrollBox(el) });
    io.observe(el);
    return () => io.disconnect();
  }, [end]);
  return cut;
}
